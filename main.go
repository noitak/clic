package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultConfig = `# CLIC config.
# Edit this file directly.
# Other model candidates: gemma4:e2b, gemma3:1b.

ollama:
  host: http://localhost:11434
  model: qwen2.5-coder:3b

ai:
  timeout_add: 10s
  timeout_ask: 3s
`

type Config struct {
	OllamaHost string
	Model      string
	TimeoutAdd time.Duration
	TimeoutAsk time.Duration
}

type Param struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type Entry struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	OriginalCommand string   `json:"original_command"`
	Template        string   `json:"template"`
	Tags            []string `json:"tags"`
	Params          []Param  `json:"params"`
	Timestamp       string   `json:"timestamp"`
	Description     string   `json:"description"`
	Notes           string
	Path            string
	Body            string
}

type addJSON struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Template    string   `json:"template"`
	Params      []Param  `json:"params"`
	Tags        []string `json:"tags"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	input := bufio.NewReader(stdin)
	if len(args) == 0 {
		return cmdList(stdout)
	}
	switch args[0] {
	case "--help", "help":
		printHelp(stdout)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "add":
		return cmdAdd(args[1:], input, stdout, stderr)
	case "ask":
		return cmdAsk(args[1:], input, stdout, stderr)
	case "list":
		return cmdList(stdout)
	case "show":
		return cmdShow(args[1:], stdout)
	case "path":
		return cmdPath(args[1:], stdout)
	default:
		return cmdAsk(args, input, stdout, stderr)
	}
	return nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "clic")
}

func commandsDir() string { return filepath.Join(homeDir(), "commands") }
func configPath() string  { return filepath.Join(homeDir(), "config.yaml") }

func ensureDirs() error {
	if err := os.MkdirAll(commandsDir(), 0755); err != nil {
		return err
	}
	return nil
}

func cmdInit(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 || args[0] != "zsh" {
		return errors.New("usage: clic init zsh")
	}
	if err := os.MkdirAll(commandsDir(), 0755); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Ready: %s\n", commandsDir())
	if _, err := os.Stat(configPath()); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(configPath(), []byte(defaultConfig), 0644); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "Created: %s\n", configPath())
	} else if err != nil {
		return err
	} else {
		fmt.Fprintf(stderr, "Reused: %s\n", configPath())
	}
	fmt.Fprint(stdout, `clic() {
  if [[ "$1" == "add" && "$2" == <-> ]]; then
    local cmd
    cmd="$(fc -ln "$2" "$2")" || {
      print -u2 "Error: zsh history entry $2 was not found."
      return 1
    }
    command clic add --stdin --history-id "$2" "${@:3}" <<< "$cmd"
  else
    command clic "$@"
  fi
}
`)
	return nil
}

func cmdAdd(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if r, ok := stdin.(*bufio.Reader); ok {
		stdin = r
	} else {
		stdin = bufio.NewReader(stdin)
	}
	var note, historyID string
	var useStdin bool
	var parts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--note":
			i++
			if i >= len(args) {
				return errors.New("--note requires a value")
			}
			note = args[i]
		case "--stdin":
			useStdin = true
		case "--history-id":
			i++
			if i >= len(args) {
				return errors.New("--history-id requires a value")
			}
			historyID = args[i]
		default:
			parts = append(parts, args[i])
		}
	}
	if err := ensureDirs(); err != nil {
		return err
	}
	var command string
	if useStdin {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		command = trimHistoryCommand(string(b))
		if historyID == "" {
			return errors.New("--stdin requires --history-id")
		}
	} else {
		if len(parts) == 0 {
			return errors.New("usage: clic add <history-number> [--note ...] or clic add <command...> [--note ...]")
		}
		if isDigits(parts[0]) {
			return errors.New("numeric history entries require zsh integration: eval \"$(clic init zsh)\"")
		}
		command = strings.Join(parts, " ")
	}
	command = normalizeOriginal(command)
	if strings.TrimSpace(command) == "" {
		return errors.New("empty command")
	}
	if isClicCommand(command) {
		return errors.New("refusing to save clic commands")
	}
	if dup, ok, err := findDuplicate(command); err != nil {
		return err
	} else if ok {
		fmt.Fprintln(stdout, "A command with the same original_command already exists:")
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, dup.ID)
		fmt.Fprintln(stdout, dup.Title)
		fmt.Fprintln(stdout, "Path:", dup.Path)
		fmt.Fprintln(stdout)
		ans, err := prompt(stdout, stdin, "Save another entry? [y/N] ")
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			return nil
		}
	}
	cfg := loadConfig()
	gen, err := generateAddMetadata(cfg, command, note)
	if err != nil {
		return err
	}
	entry := Entry{
		ID:              gen.ID,
		Title:           gen.Title,
		OriginalCommand: command,
		Template:        gen.Template,
		Tags:            gen.Tags,
		Params:          gen.Params,
		Timestamp:       time.Now().Format(time.RFC3339),
		Description:     gen.Description,
		Notes:           note,
	}
	if err := validateEntry(entry); err != nil {
		return err
	}
	entry.ID = uniqueID(entry.ID)
	entry.Path = filepath.Join(commandsDir(), entry.ID+".md")
	for {
		printEntrySummary(stdout, entry)
		ans, err := prompt(stdout, stdin, "Save? [Y/e/n] ")
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "", "y":
			return os.WriteFile(entry.Path, []byte(renderMarkdown(entry)), 0644)
		case "n":
			return nil
		case "e":
			tmp, err := writeTempEntry(entry)
			if err != nil {
				return err
			}
			if err := editFile(tmp); err != nil {
				return err
			}
			edited, err := readEntry(tmp)
			if err != nil {
				return err
			}
			edited.Path = entry.Path
			if err := validateEntry(edited); err != nil {
				return err
			}
			entry = edited
			if filepath.Base(entry.Path) != entry.ID+".md" {
				entry.ID = uniqueID(entry.ID)
				entry.Path = filepath.Join(commandsDir(), entry.ID+".md")
			}
		default:
			fmt.Fprintln(stdout, "Please answer y, e, or n.")
		}
	}
}

func cmdAsk(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if r, ok := stdin.(*bufio.Reader); ok {
		stdin = r
	} else {
		stdin = bufio.NewReader(stdin)
	}
	query := strings.Join(args, " ")
	if strings.TrimSpace(query) == "" {
		return cmdList(stdout)
	}
	matches, exact, err := searchEntries(query)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		fmt.Fprintf(stdout, "No saved command matched: %q\n\n", query)
		fmt.Fprintln(stdout, "Save a command first:")
		fmt.Fprintf(stdout, "  clic add <history-number> --note %q\n", query)
		return nil
	}
	selected := matches[0]
	if !exact && len(matches) > 1 {
		fmt.Fprintln(stdout, "Matches:")
		fmt.Fprintln(stdout)
		for i, e := range matches {
			if i >= 10 {
				break
			}
			fmt.Fprintf(stdout, "%d. %-24s %s\n", i+1, e.ID, e.Title)
		}
		fmt.Fprintln(stdout)
		ans, err := prompt(stdout, stdin, fmt.Sprintf("Select [1-%d], q to quit: ", min(len(matches), 10)))
		if err != nil {
			return err
		}
		ans = strings.TrimSpace(ans)
		if strings.ToLower(ans) == "q" {
			return nil
		}
		n, err := strconv.Atoi(ans)
		if err != nil || n < 1 || n > min(len(matches), 10) {
			return errors.New("invalid selection")
		}
		selected = matches[n-1]
	}
	inferred := map[string]string{}
	if !exact {
		var err error
		inferred, err = inferArgs(loadConfig(), query, selected)
		if err != nil {
			fmt.Fprintln(stderr, "Warning: Ollama argument inference failed. Continuing with saved defaults.")
		}
	}
	values := map[string]string{}
	for _, p := range selected.Params {
		def := p.Default
		if v, ok := inferred[p.Name]; ok && v != "" {
			def = v
		}
		ans, err := prompt(stdout, stdin, fmt.Sprintf("%s [%s]: ", p.Name, def))
		if err != nil {
			return err
		}
		if strings.TrimRight(ans, "\r\n") == "" {
			values[p.Name] = def
		} else {
			values[p.Name] = ans
		}
	}
	rendered := renderTemplate(selected.Template, values)
	return confirmAndCopy(rendered, stdin, stdout)
}

func cmdList(stdout io.Writer) error {
	entries, err := loadEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No commands saved yet.")
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Add one:")
		fmt.Fprintln(stdout, `  clic add <history-number> --note "..."`)
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, e := range entries {
		fmt.Fprintf(stdout, "%-24s %s\n", e.ID, e.Title)
	}
	return nil
}

func cmdShow(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: clic show <id>")
	}
	e, err := getEntry(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, e.ID)
	fmt.Fprintln(stdout, e.Title)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Original:")
	fmt.Fprintln(stdout, e.OriginalCommand)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Template:")
	fmt.Fprintln(stdout, e.Template)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Params:")
	for _, p := range e.Params {
		fmt.Fprintf(stdout, "  %-8s default=%-12s %s\n", p.Name, p.Default, p.Description)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Tags:")
	fmt.Fprintln(stdout, strings.Join(e.Tags, ", "))
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Path:")
	fmt.Fprintln(stdout, e.Path)
	return nil
}

func cmdPath(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: clic path <id|--home|--commands|--config>")
	}
	switch args[0] {
	case "--home":
		fmt.Fprintln(stdout, homeDir())
	case "--commands":
		fmt.Fprintln(stdout, commandsDir())
	case "--config":
		fmt.Fprintln(stdout, configPath())
	default:
		e, err := getEntry(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, e.Path)
	}
	return nil
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `clic - personal command knowledge-base

Usage:
  clic add <history-number> [--note "..."]
  clic add <command...> [--note "..."]
  clic ask <query...>
  clic <query...>
  clic list
  clic show <id>
  clic path <id|--home|--commands|--config>
  clic init zsh
  clic help

Paths:`)
	fmt.Fprintf(w, "  home:     %s\n", homeDir())
	fmt.Fprintf(w, "  commands: %s\n", commandsDir())
	fmt.Fprintf(w, "  config:   %s\n", configPath())
}

func loadConfig() Config {
	cfg := Config{OllamaHost: "http://localhost:11434", Model: "qwen2.5-coder:3b", TimeoutAdd: 10 * time.Second, TimeoutAsk: 3 * time.Second}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "host:") {
			cfg.OllamaHost = strings.Trim(strings.TrimSpace(strings.TrimPrefix(s, "host:")), `"`)
		}
		if strings.HasPrefix(s, "model:") {
			cfg.Model = strings.Trim(strings.TrimSpace(strings.TrimPrefix(s, "model:")), `"`)
		}
		if strings.HasPrefix(s, "timeout_add:") {
			if d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(s, "timeout_add:"))); err == nil {
				cfg.TimeoutAdd = d
			}
		}
		if strings.HasPrefix(s, "timeout_ask:") {
			if d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(s, "timeout_ask:"))); err == nil {
				cfg.TimeoutAsk = d
			}
		}
	}
	return cfg
}

func generateAddMetadata(cfg Config, command, note string) (addJSON, error) {
	prompt := fmt.Sprintf(`You summarize a shell command for reuse.
Return only JSON.
Generate id as a lowercase English slug.
Allowed id characters: a-z, 0-9, hyphen.
Do not invent new flags.
Do not change the command structure or pipeline structure.
Convert only obvious file paths, numbers, names, dates, URLs, and user-specific literals into {{params}}.
Keep fixed flags unchanged.
Use quotes around file/path parameters when appropriate for zsh.

Environment:
OS: macOS
Shell: zsh

Command:
%s

User note:
%s`, command, note)
	var out addJSON
	if err := ollamaJSON(cfg, cfg.TimeoutAdd, prompt, 600, &out); err != nil {
		return out, err
	}
	return out, nil
}

func inferArgs(cfg Config, query string, e Entry) (map[string]string, error) {
	payload := map[string]any{
		"environment": map[string]string{"os": "macOS", "shell": "zsh"},
		"query":       query,
		"entry": map[string]any{
			"id":       e.ID,
			"template": e.Template,
			"params":   e.Params,
		},
	}
	b, _ := json.MarshalIndent(payload, "", "  ")
	prompt := "Infer arguments for the selected saved shell command. Return only JSON in the shape {\"arguments\":{\"name\":\"value\"}}. Use empty strings when the query does not specify a value.\n\n" + string(b)
	var out struct {
		Arguments map[string]string `json:"arguments"`
	}
	if err := ollamaJSON(cfg, cfg.TimeoutAsk, prompt, 300, &out); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, p := range e.Params {
		allowed[p.Name] = true
	}
	result := map[string]string{}
	for k, v := range out.Arguments {
		if allowed[k] {
			result[k] = v
		}
	}
	return result, nil
}

func ollamaJSON(cfg Config, timeout time.Duration, prompt string, numPredict int, target any) error {
	body, _ := json.Marshal(map[string]any{
		"model":  cfg.Model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
		"options": map[string]any{
			"temperature": 0.1,
			"num_predict": numPredict,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.OllamaHost, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("ollama returned %s", res.Status)
	}
	var raw struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return err
	}
	if raw.Response == "" {
		return errors.New("ollama returned empty response")
	}
	if err := json.Unmarshal([]byte(raw.Response), target); err != nil {
		return err
	}
	return nil
}

func validateEntry(e Entry) error {
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(e.ID) {
		return fmt.Errorf("invalid id: %s", e.ID)
	}
	if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.OriginalCommand) == "" || strings.TrimSpace(e.Template) == "" || strings.TrimSpace(e.Description) == "" {
		return errors.New("id, title, original_command, template, and description are required")
	}
	if firstToken(e.OriginalCommand) != firstToken(e.Template) {
		return errors.New("first command token of original_command and template must match")
	}
	vars := templateVars(e.Template)
	seen := map[string]bool{}
	for _, p := range e.Params {
		if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(p.Name) {
			return fmt.Errorf("invalid param name: %s", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate param: %s", p.Name)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.Description) == "" {
			return fmt.Errorf("param %s description is required", p.Name)
		}
	}
	for v := range vars {
		if !seen[v] {
			return fmt.Errorf("template variable %s is missing from params", v)
		}
	}
	for p := range seen {
		if !vars[p] {
			return fmt.Errorf("param %s is not referenced by template", p)
		}
	}
	ordered := templateVarsOrdered(e.Template)
	if len(ordered) != len(e.Params) {
		return errors.New("params must match template variables")
	}
	for i, name := range ordered {
		if e.Params[i].Name != name {
			return errors.New("params must be ordered by first occurrence in template")
		}
	}
	return nil
}

func renderMarkdown(e Entry) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "id", e.ID)
	writeYAMLScalar(&b, "title", e.Title)
	writeYAMLScalar(&b, "original_command", e.OriginalCommand)
	writeYAMLScalar(&b, "template", e.Template)
	b.WriteString("tags: [")
	for i, t := range e.Tags {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(yamlQuote(t))
	}
	b.WriteString("]\n")
	if len(e.Params) == 0 {
		b.WriteString("params: []\n")
	} else {
		b.WriteString("params:\n")
		for _, p := range e.Params {
			b.WriteString("  - name: " + yamlQuote(p.Name) + "\n")
			b.WriteString("    default: " + yamlQuote(p.Default) + "\n")
			b.WriteString("    description: " + yamlQuote(p.Description) + "\n")
		}
	}
	writeYAMLScalar(&b, "timestamp", e.Timestamp)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(e.Description))
	b.WriteString("\n")
	if strings.TrimSpace(e.Notes) != "" {
		b.WriteString("\n## Notes\n\n")
		b.WriteString(strings.TrimSpace(e.Notes))
		b.WriteString("\n")
	}
	return b.String()
}

func writeYAMLScalar(b *strings.Builder, key, value string) {
	if strings.Contains(value, "\n") {
		b.WriteString(key + ": |\n")
		for _, line := range strings.Split(value, "\n") {
			b.WriteString("  " + line + "\n")
		}
		return
	}
	b.WriteString(key + ": " + yamlQuote(value) + "\n")
}

func yamlQuote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

func readEntry(path string) (Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	text := string(b)
	if !strings.HasPrefix(text, "---\n") {
		return Entry{}, fmt.Errorf("%s: missing front matter", path)
	}
	rest := text[4:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Entry{}, fmt.Errorf("%s: missing front matter end", path)
	}
	fm, body := rest[:idx], strings.TrimPrefix(rest[idx+4:], "\n")
	e, err := parseFrontMatter(fm)
	if err != nil {
		return Entry{}, fmt.Errorf("%s: %w", path, err)
	}
	e.Path = path
	e.Body = body
	e.Description = strings.TrimSpace(body)
	if noteIdx := strings.Index(body, "\n## Notes\n"); noteIdx >= 0 {
		e.Description = strings.TrimSpace(body[:noteIdx])
		e.Notes = strings.TrimSpace(body[noteIdx+len("\n## Notes\n"):])
	}
	return e, nil
}

func parseFrontMatter(fm string) (Entry, error) {
	var e Entry
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "|" {
			var block []string
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
				i++
				block = append(block, strings.TrimPrefix(lines[i], "  "))
			}
			val = strings.Join(block, "\n")
		} else {
			val = unquoteYAML(val)
		}
		switch key {
		case "id":
			e.ID = val
		case "title":
			e.Title = val
		case "original_command":
			e.OriginalCommand = val
		case "template":
			e.Template = val
		case "timestamp":
			e.Timestamp = val
		case "tags":
			e.Tags = parseInlineList(strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(val, "]"), "[")))
		case "params":
			if val == "[]" {
				e.Params = []Param{}
			} else {
				params, ni := parseParams(lines, i+1)
				e.Params = params
				i = ni - 1
			}
		}
	}
	return e, nil
}

func parseParams(lines []string, start int) ([]Param, int) {
	var params []Param
	i := start
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "  - ") {
			break
		}
		p := Param{}
		for i < len(lines) {
			line := lines[i]
			if strings.HasPrefix(line, "  - ") {
				k, v, _ := strings.Cut(strings.TrimPrefix(line, "  - "), ":")
				if strings.TrimSpace(k) == "name" {
					p.Name = unquoteYAML(strings.TrimSpace(v))
				}
				i++
				continue
			}
			if !strings.HasPrefix(line, "    ") {
				break
			}
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok {
				switch strings.TrimSpace(k) {
				case "default":
					p.Default = unquoteYAML(strings.TrimSpace(v))
				case "description":
					p.Description = unquoteYAML(strings.TrimSpace(v))
				}
			}
			i++
		}
		params = append(params, p)
	}
	return params, i
}

func unquoteYAML(s string) string {
	if len(s) >= 2 && s[0] == '"' {
		var out string
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return strings.Trim(s, `"`)
}

func parseInlineList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, unquoteYAML(strings.TrimSpace(p)))
	}
	return out
}

func loadEntries() ([]Entry, error) {
	files, err := filepath.Glob(filepath.Join(commandsDir(), "*.md"))
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, f := range files {
		e, err := readEntry(f)
		if err == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func getEntry(id string) (Entry, error) {
	path := filepath.Join(commandsDir(), id+".md")
	e, err := readEntry(path)
	if err != nil {
		return Entry{}, fmt.Errorf("command not found: %s", id)
	}
	if e.ID != id {
		return Entry{}, fmt.Errorf("command not found: %s", id)
	}
	return e, nil
}

func searchEntries(query string) ([]Entry, bool, error) {
	entries, err := loadEntries()
	if err != nil {
		return nil, false, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, e := range entries {
		stem := strings.TrimSuffix(filepath.Base(e.Path), ".md")
		if strings.ToLower(e.ID) == q || strings.ToLower(stem) == q {
			return []Entry{e}, true, nil
		}
	}
	type scored struct {
		e     Entry
		score int
	}
	var scoreds []scored
	tokens := strings.Fields(q)
	for _, e := range entries {
		score := 0
		high := strings.ToLower(e.ID + " " + strings.TrimSuffix(filepath.Base(e.Path), ".md") + " " + e.Title + " " + strings.Join(e.Tags, " "))
		medium := strings.ToLower(e.Template + " " + e.OriginalCommand)
		low := strings.ToLower(e.Body)
		for _, t := range tokens {
			if strings.Contains(high, t) {
				score += 10
			}
			if strings.Contains(medium, t) {
				score += 4
			}
			if strings.Contains(low, t) {
				score++
			}
		}
		if score > 0 {
			scoreds = append(scoreds, scored{e, score})
		}
	}
	sort.Slice(scoreds, func(i, j int) bool {
		if scoreds[i].score == scoreds[j].score {
			return scoreds[i].e.ID < scoreds[j].e.ID
		}
		return scoreds[i].score > scoreds[j].score
	})
	out := make([]Entry, 0, min(len(scoreds), 10))
	for i, s := range scoreds {
		if i >= 10 {
			break
		}
		out = append(out, s.e)
	}
	return out, false, nil
}

func findDuplicate(command string) (Entry, bool, error) {
	entries, err := loadEntries()
	if err != nil {
		return Entry{}, false, err
	}
	needle := normalizeOriginal(command)
	for _, e := range entries {
		if normalizeOriginal(e.OriginalCommand) == needle {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

func trimHistoryCommand(s string) string {
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 {
		lines[0] = strings.TrimLeft(lines[0], " \t")
	}
	return strings.Join(lines, "\n")
}

func normalizeOriginal(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimSuffix(s, "\n")
}

func isClicCommand(command string) bool {
	toks := strings.Fields(command)
	if len(toks) == 0 {
		return false
	}
	return toks[0] == "clic" || (len(toks) > 1 && toks[0] == "command" && toks[1] == "clic")
}

func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], `"'`)
}

func templateVars(t string) map[string]bool {
	re := regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(t, -1) {
		out[m[1]] = true
	}
	return out
}

func templateVarsOrdered(t string) []string {
	re := regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(t, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func renderTemplate(t string, values map[string]string) string {
	for k, v := range values {
		t = strings.ReplaceAll(t, "{{"+k+"}}", v)
	}
	return t
}

func uniqueID(id string) string {
	candidate := id
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(commandsDir(), candidate+".md")); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", id, i)
	}
}

func printEntrySummary(w io.Writer, e Entry) {
	fmt.Fprintln(w, "ID:")
	fmt.Fprintln(w, e.ID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Title:")
	fmt.Fprintln(w, e.Title)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Template:")
	fmt.Fprintln(w, e.Template)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Params:")
	for _, p := range e.Params {
		fmt.Fprintf(w, "  %s=%s  %s\n", p.Name, p.Default, p.Description)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tags:")
	fmt.Fprintln(w, strings.Join(e.Tags, ","))
	fmt.Fprintln(w)
}

func confirmAndCopy(command string, stdin io.Reader, stdout io.Writer) error {
	for {
		if strings.TrimSpace(command) == "" {
			return errors.New("rendered command is empty")
		}
		danger := isDangerous(command)
		if danger {
			fmt.Fprintln(stdout, "Warning: destructive-looking command.")
			fmt.Fprintln(stdout)
		}
		fmt.Fprintln(stdout, "Command:")
		fmt.Fprintln(stdout, command)
		fmt.Fprintln(stdout)
		label := "Copy to clipboard? [Y/e/n] "
		if danger {
			label = "Copy to clipboard? [y/N/e] "
		}
		ans, err := prompt(stdout, stdin, label)
		if err != nil {
			return err
		}
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "e" {
			tmp, err := os.CreateTemp("", "clic-command-*.sh")
			if err != nil {
				return err
			}
			name := tmp.Name()
			if _, err := tmp.WriteString(command + "\n"); err != nil {
				tmp.Close()
				return err
			}
			tmp.Close()
			if err := editFile(name); err != nil {
				return err
			}
			b, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			command = strings.TrimSuffix(string(b), "\n")
			continue
		}
		if danger {
			if ans != "y" {
				return nil
			}
		} else if ans == "n" {
			return nil
		} else if ans != "" && ans != "y" {
			fmt.Fprintln(stdout, "Please answer y, e, or n.")
			continue
		}
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(command)
		return cmd.Run()
	}
}

func isDangerous(command string) bool {
	l := strings.ToLower(command)
	patterns := []string{"rm ", "rm\t", "sudo ", "chmod -r", "chown -r", "curl | sh", "wget | sh", "dd ", "mkfs", "git reset --hard", "> /dev/", ">/dev/"}
	for _, p := range patterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	if regexp.MustCompile(`>\s*/`).MatchString(l) || regexp.MustCompile(`\d?>\s*&`).MatchString(l) {
		return true
	}
	return false
}

func prompt(stdout io.Writer, stdin io.Reader, label string) (string, error) {
	fmt.Fprint(stdout, label)
	r, ok := stdin.(*bufio.Reader)
	if !ok {
		r = bufio.NewReader(stdin)
	}
	s, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"), nil
}

func editFile(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeTempEntry(e Entry) (string, error) {
	f, err := os.CreateTemp("", "clic-entry-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(renderMarkdown(e)); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
