package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Param struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type Entry struct {
	ID       string  `json:"id"`
	Template string  `json:"template"`
	Params   []Param `json:"params"`
}

type AddCase struct {
	ID             string   `json:"id"`
	Command        string   `json:"command"`
	Note           string   `json:"note"`
	ExpectedParams []string `json:"expected_params"`
	FixedFragments []string `json:"fixed_fragments"`
}

type AskCase struct {
	ID                string            `json:"id"`
	Query             string            `json:"query"`
	Entry             Entry             `json:"entry"`
	ExpectedArguments map[string]string `json:"expected_arguments"`
}

type Cases struct {
	AddCases []AddCase `json:"add_cases"`
	AskCases []AskCase `json:"ask_cases"`
}

type addJSON struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Template    string   `json:"template"`
	Params      []Param  `json:"params"`
	Tags        []string `json:"tags"`
}

type record struct {
	StartedAt           string         `json:"started_at"`
	Model               string         `json:"model"`
	TaskType            string         `json:"task_type"`
	CaseID              string         `json:"case_id"`
	Run                 int            `json:"run"`
	LatencyMS           int64          `json:"latency_ms"`
	RepairAttempted     bool           `json:"repair_attempted,omitempty"`
	ValidJSON           bool           `json:"valid_json"`
	ValidationPassed    bool           `json:"validation_passed"`
	MatchedExpectations bool           `json:"matched_expectations"`
	Error               string         `json:"error,omitempty"`
	RawResponse         string         `json:"raw_response,omitempty"`
	Response            any            `json:"response,omitempty"`
	Expected            map[string]any `json:"expected,omitempty"`
}

func main() {
	var host, models, casesPath, outPath, task string
	var runs int
	var timeoutAdd, timeoutAsk time.Duration
	flag.StringVar(&host, "host", "http://localhost:11434", "Ollama host")
	flag.StringVar(&models, "models", "qwen2.5-coder:3b,gemma4:e2b-mlx", "comma-separated model names")
	flag.StringVar(&casesPath, "cases", "eval/cases.json", "evaluation cases JSON")
	flag.StringVar(&outPath, "out", "", "output JSONL path")
	flag.StringVar(&task, "task", "all", "task to run: all, add, or ask")
	flag.IntVar(&runs, "runs", 3, "runs per model and case")
	flag.DurationVar(&timeoutAdd, "timeout-add", 10*time.Second, "timeout for add generation")
	flag.DurationVar(&timeoutAsk, "timeout-ask", 3*time.Second, "timeout for ask inference")
	flag.Parse()

	cases, err := loadCases(casesPath)
	if err != nil {
		fatal(err)
	}
	if task != "all" && task != "add" && task != "ask" {
		fatal(errors.New("-task must be all, add, or ask"))
	}
	if outPath == "" {
		if err := os.MkdirAll("eval/results", 0755); err != nil {
			fatal(err)
		}
		outPath = filepath.Join("eval/results", "model_eval_"+time.Now().Format("20060102_150405")+".jsonl")
	}
	out, err := os.Create(outPath)
	if err != nil {
		fatal(err)
	}
	defer out.Close()

	var records []record
	for _, model := range splitCSV(models) {
		for run := 1; run <= runs; run++ {
			if task == "all" || task == "add" {
				for _, c := range cases.AddCases {
					rec := evalAdd(host, model, timeoutAdd, c, run)
					records = append(records, rec)
					writeRecord(out, rec)
				}
			}
			if task == "all" || task == "ask" {
				for _, c := range cases.AskCases {
					rec := evalAsk(host, model, timeoutAsk, c, run)
					records = append(records, rec)
					writeRecord(out, rec)
				}
			}
		}
	}

	printSummary(records, outPath)
}

func evalAdd(host, model string, timeout time.Duration, c AddCase, run int) record {
	rec := newRecord(model, "add", c.ID, run)
	prompt := addMetadataPrompt(c.Command, c.Note)
	var out addJSON
	start := time.Now()
	raw, err := ollamaJSON(host, model, timeout, prompt, 600, addMetadataSchema(), &out)
	rec.LatencyMS = time.Since(start).Milliseconds()
	rec.RawResponse = raw
	rec.Response = out
	rec.Expected = map[string]any{
		"expected_params": c.ExpectedParams,
		"fixed_fragments": c.FixedFragments,
	}
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	rec.ValidJSON = true
	if err := validateAdd(out); err != nil {
		rec.RepairAttempted = true
		var repaired addJSON
		repairPrompt := repairAddMetadataPrompt(c.Command, c.Note, out, err)
		repairStart := time.Now()
		repairRaw, repairErr := ollamaJSON(host, model, timeout, repairPrompt, 700, addMetadataSchema(), &repaired)
		rec.LatencyMS += time.Since(repairStart).Milliseconds()
		rec.RawResponse = repairRaw
		if repairErr != nil {
			rec.Error = err.Error() + "; repair failed: " + repairErr.Error()
			return rec
		}
		out = repaired
		rec.Response = out
		if err := validateAdd(out); err != nil {
			rec.Error = err.Error()
			return rec
		}
	}
	rec.ValidationPassed = true
	if err := matchAddExpectations(out, c); err != nil {
		rec.Error = err.Error()
		return rec
	}
	rec.MatchedExpectations = true
	return rec
}

func evalAsk(host, model string, timeout time.Duration, c AskCase, run int) record {
	rec := newRecord(model, "ask", c.ID, run)
	prompt := inferArgsPrompt(c.Query, c.Entry)
	var out struct {
		Arguments map[string]string `json:"arguments"`
	}
	start := time.Now()
	raw, err := ollamaJSON(host, model, timeout, prompt, 300, inferArgsSchema(), &out)
	rec.LatencyMS = time.Since(start).Milliseconds()
	rec.RawResponse = raw
	rec.Response = out
	rec.Expected = map[string]any{"arguments": c.ExpectedArguments}
	if err != nil {
		rec.Error = err.Error()
		return rec
	}
	rec.ValidJSON = true
	if out.Arguments == nil {
		rec.Error = "missing arguments object"
		return rec
	}
	rec.ValidationPassed = true
	if err := matchAskExpectations(out.Arguments, c.ExpectedArguments); err != nil {
		rec.Error = err.Error()
		return rec
	}
	rec.MatchedExpectations = true
	return rec
}

func newRecord(model, taskType, caseID string, run int) record {
	return record{
		StartedAt: time.Now().Format(time.RFC3339),
		Model:     model,
		TaskType:  taskType,
		CaseID:    caseID,
		Run:       run,
	}
}

func loadCases(path string) (Cases, error) {
	var cases Cases
	b, err := os.ReadFile(path)
	if err != nil {
		return cases, err
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		return cases, err
	}
	if len(cases.AddCases) == 0 && len(cases.AskCases) == 0 {
		return cases, errors.New("no evaluation cases found")
	}
	return cases, nil
}

func ollamaJSON(host, model string, timeout time.Duration, prompt string, numPredict int, format any, target any) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
		"format": format,
		"options": map[string]any{
			"temperature": 0.1,
			"num_predict": numPredict,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(host, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("ollama returned %s", res.Status)
	}
	var raw struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.Response) == "" {
		return raw.Response, errors.New("ollama returned empty response")
	}
	if err := json.Unmarshal([]byte(raw.Response), target); err != nil {
		return raw.Response, fmt.Errorf("invalid json response: %w", err)
	}
	return raw.Response, nil
}

func addMetadataSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id":          map[string]any{"type": "string"},
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"template":    map[string]any{"type": "string"},
			"params": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"default":     map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
					},
					"required": []string{"name", "default", "description"},
				},
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"id", "title", "description", "template", "params", "tags"},
	}
}

func inferArgsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"arguments": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"arguments"},
	}
}

func addMetadataPrompt(command, note string) string {
	return fmt.Sprintf(`You summarize a shell command for reuse.
Return only JSON.
Return exactly one JSON object with these keys:
{
  "id": "lowercase-english-slug",
  "title": "short Japanese title",
  "description": "one Japanese description paragraph",
  "template": "shell command template using {{param}} placeholders",
  "params": [
    {"name": "param_name", "default": "default value", "description": "Japanese parameter description"}
  ],
  "tags": ["tag"]
}
All keys are required. Use [] for params or tags when empty.
Keep title, description, and parameter descriptions in Japanese.
For each parameter default, use the literal value from the original command when available.
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
}

func repairAddMetadataPrompt(command, note string, previous addJSON, validationErr error) string {
	prev, _ := json.MarshalIndent(previous, "", "  ")
	return fmt.Sprintf(`Fix this JSON for a saved shell command entry.
Return only corrected JSON with the exact same required keys.
Do not change the command structure.
Every {{param}} in template must have exactly one params entry.
Every params entry must be referenced in template.
Params must be ordered by first occurrence in template.
Keep title, description, and parameter descriptions in Japanese.
For each parameter default, use the literal value from the original command when available.

Validation error:
%s

Original command:
%s

User note:
%s

Previous JSON:
%s`, validationErr.Error(), command, note, string(prev))
}

func inferArgsPrompt(query string, e Entry) string {
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
	return "Infer arguments for the selected saved shell command. Return only JSON in the shape {\"arguments\":{\"name\":\"value\"}}. Use empty strings when the query does not specify a value.\n\n" + string(b)
}

func validateAdd(out addJSON) error {
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(out.ID) {
		return fmt.Errorf("invalid id: %s", out.ID)
	}
	if strings.TrimSpace(out.Title) == "" {
		return errors.New("missing title")
	}
	if strings.TrimSpace(out.Description) == "" {
		return errors.New("missing description")
	}
	if strings.TrimSpace(out.Template) == "" {
		return errors.New("missing template")
	}
	if out.Tags == nil {
		return errors.New("missing tags")
	}
	placeholders := placeholderNames(out.Template)
	paramNames := make([]string, 0, len(out.Params))
	seen := map[string]bool{}
	for _, p := range out.Params {
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("empty param name")
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate param: %s", p.Name)
		}
		seen[p.Name] = true
		paramNames = append(paramNames, p.Name)
	}
	if strings.Join(placeholders, ",") != strings.Join(paramNames, ",") {
		return fmt.Errorf("template params %v do not match params %v", placeholders, paramNames)
	}
	return nil
}

func matchAddExpectations(out addJSON, c AddCase) error {
	names := map[string]bool{}
	for _, p := range out.Params {
		names[p.Name] = true
	}
	for _, expected := range c.ExpectedParams {
		if !names[expected] {
			return fmt.Errorf("missing expected param: %s", expected)
		}
	}
	for _, fragment := range c.FixedFragments {
		if !strings.Contains(out.Template, fragment) {
			return fmt.Errorf("template missing fixed fragment %q", fragment)
		}
	}
	return nil
}

func matchAskExpectations(got, expected map[string]string) error {
	for name, want := range expected {
		if got[name] != want {
			return fmt.Errorf("argument %s: got %q, want %q", name, got[name], want)
		}
	}
	return nil
}

func placeholderNames(template string) []string {
	re := regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)
	matches := re.FindAllStringSubmatch(template, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

func writeRecord(out *os.File, rec record) {
	b, err := json.Marshal(rec)
	if err != nil {
		fatal(err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		fatal(err)
	}
}

func printSummary(records []record, outPath string) {
	type stat struct {
		total       int
		validJSON   int
		validated   int
		matched     int
		latencySum  int64
		latencySeen int
	}
	stats := map[string]*stat{}
	for _, rec := range records {
		key := rec.Model + " " + rec.TaskType
		if stats[key] == nil {
			stats[key] = &stat{}
		}
		s := stats[key]
		s.total++
		if rec.ValidJSON {
			s.validJSON++
		}
		if rec.ValidationPassed {
			s.validated++
		}
		if rec.MatchedExpectations {
			s.matched++
		}
		if rec.LatencyMS > 0 {
			s.latencySum += rec.LatencyMS
			s.latencySeen++
		}
	}

	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fmt.Println("Results:", outPath)
	fmt.Println()
	fmt.Printf("%-28s %-6s %-10s %-10s %-10s %-14s\n", "model/task", "total", "json", "validate", "match", "avg_latency")
	for _, key := range keys {
		s := stats[key]
		avg := int64(0)
		if s.latencySeen > 0 {
			avg = s.latencySum / int64(s.latencySeen)
		}
		fmt.Printf("%-28s %-6d %-10d %-10d %-10d %-14d\n", key, s.total, s.validJSON, s.validated, s.matched, avg)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}
