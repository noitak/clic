# CLIC Specification

## Concept

`clic` is a personal command knowledge-base CLI for macOS and zsh.
The name stands for **Command Library in Context**.

It saves useful shell commands as small Markdown runbook entries, then lets the user search those entries with natural language, fill template parameters, and copy the resulting command to the clipboard.

`clic` never executes generated commands.
It prepares commands for the user to paste into their own shell.

The core idea is:

```text
clic = search + select + fill params + copy command
```

## Design Goals

- Keep the daily interface as small as possible for personal use.
- Use plain text files that are easy to inspect, edit, sync, and version.
- Avoid making the user write metadata by hand.
- Use fast local LLM assistance through Ollama.
- Preserve the original command separately from the reusable template.
- Never generate commands from scratch when there is no saved entry.
- Never execute commands from `clic`.

## Non-Goals

- Do not build a general shell history replacement.
- Do not support bash or fish in the MVP.
- Do not require a cloud LLM.
- Do not maintain execution logs.
- Do not provide edit/delete/config subcommands when normal file operations are enough.
- Do not add search-only or run-only subcommands.

## Target Environment

MVP target:

```text
OS: macOS
Shell: zsh
Clipboard: pbcopy
LLM: Ollama
```

Portability is handled by the plain text storage format, not by broad runtime support in the MVP.

## Recommended Implementation

Use Go rather than shell script for the main implementation.

Reasons:

- Single-binary distribution.
- Easier Markdown/YAML parsing and validation.
- Easier HTTP integration with Ollama.
- Better timeout and JSON handling.
- Safer command rendering and danger detection.
- Easier interactive prompts.

Shell script is acceptable only for an early prototype.

## Main Commands

### Add

Save a command.

Public forms:

```sh
clic add <history-number> [--note "..."]
clic add <command...> [--note "..."]
```

Examples:

```sh
clic add 12345 --note "請求書PDFを画像化する"
clic add ffmpeg -i input.mov -vf scale=-2:720 output.mp4 --note "動画を720pに変換する"
clic add --note "resize video" ffmpeg -i input.mov output.mp4
```

`clic add <history-number>` requires zsh integration from `clic init zsh`.
The wrapper resolves the history number through zsh and passes the command to the binary.

Internal form, documented for implementers but hidden from `--help`:

```sh
command clic add --stdin --history-id 12345
```

Rules:

- A numeric first argument means zsh history number.
- There is no `--last`, `--history`, `-H`, `--literal`, `--url`, or `--no-ai`.
- Non-flag arguments are joined with spaces to form the command string.
- Complex commands should be saved from zsh history so quoting and multiple lines are preserved.
- Empty `clic add` is a usage error.
- Commands whose first command is `clic` or `command clic` are rejected.
- Multi-line commands are allowed.
- `--note` is optional and may appear before or after command arguments.

Default behavior:

1. Resolve the command from zsh history or direct arguments.
2. Trim history-output padding while preserving command content and multi-line indentation.
3. Reject `clic` commands.
4. Check for duplicate `original_command` using light normalization.
5. Generate metadata with Ollama.
6. Validate the generated entry.
7. Resolve ID/file-name collisions by appending `-2`, `-3`, and so on.
8. Show a compact confirmation.
9. Save on `Y`, edit on `e`, discard on `n`.

Duplicate detection:

```text
normalize:
  - trim leading/trailing whitespace
  - remove at most one trailing final newline
  - preserve internal whitespace and newlines
```

If an existing entry has the same normalized `original_command`, show it and ask:

```text
A command with the same original_command already exists:

pdf-to-png
PDFをPNG画像に変換する
Path: /Users/taku/.config/clic/commands/pdf-to-png.md

Save another entry? [y/N]
```

Saving another entry is allowed only when the user answers `y`.

Example generated confirmation:

```text
ID:
pdf-to-png

Title:
PDFをPNG画像に変換する

Template:
pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"

Params:
  dpi=300        出力画像の解像度
  input=invoice.pdf  入力PDFファイル
  prefix=invoice     出力ファイル名のプレフィックス

Tags:
pdf,image,poppler

Save? [Y/e/n]
```

`e` opens the generated Markdown entry in `$EDITOR`, or `vi` when `$EDITOR` is unset.
After editing, `clic` validates the file again, then shows the confirmation summary again.

If Ollama is unavailable, times out, returns invalid JSON, or returns an invalid entry, `clic add` exits with an error and does not save anything.

### Ask

`ask` is the only query command.
It replaces both search and run.

```sh
clic ask <query...>
```

Examples:

```sh
clic ask ffmpeg
clic ask pdf png 300dpi
clic ask "請求書PDFを300dpiのPNGにしたい"
```

Default behavior:

1. Join query arguments with spaces.
2. Search saved Markdown command files.
3. Select automatically for exact ID/file-name match or a single search result.
   For a single non-exact search result, show its `id` and title before continuing.
4. Show a selection UI when multiple entries match.
5. Infer parameter values with Ollama when the query was not an exact ID/file-name match.
6. Prompt for parameters.
7. Render the final command.
8. Run danger detection.
9. Copy to clipboard through `pbcopy` after confirmation.

`clic ask` never executes the command.

No search-only option is provided.
There is no `--list`, `--run`, `--ai`, `--no-ai`, or `--fast`.

Empty query behaves like `clic list`:

```sh
clic
clic ask
```

Both show the saved command list.

No-match behavior:

```text
No saved command matched: "動画をAVIFにする"

Save a command first:
  clic add <history-number> --note "動画をAVIFにする"
```

`clic ask` does not ask Ollama to create a brand-new command when no saved entry matches.

Multiple matches:

```text
Matches:

1. pdf-to-png              PDFをPNG画像に変換する
2. pdf-compress            PDFを圧縮する

Select [1-2], q to quit:
```

Rules:

- Show at most 10 matches.
- Show ID and title only.
- `q` exits successfully with status `0`.
- No refine prompt is provided.

Parameter prompting:

```text
dpi [150]:
input [invoice.pdf]:
prefix [invoice]:
```

Default value priority:

```text
1. Ollama-inferred value from the ask query
2. saved params[].default
3. empty string
```

Exact ID or exact file-name stem match does not call ask-time Ollama.
Non-exact single result and user-selected matches call Ollama for argument inference.
If ask-time Ollama fails, show a warning and continue with saved defaults.

```text
Warning: Ollama argument inference failed. Continuing with saved defaults.
```

Final copy flow:

```text
Command:
pdftoppm -png -r 150 "invoice.pdf" "invoice"

Copy to clipboard? [Y/e/n]
```

For dangerous-looking commands:

```text
Warning: destructive-looking command.

Command:
rm -rf ./build

Copy to clipboard? [y/N/e]
```

`n` exits successfully with status `0`.
`e` opens the rendered command in `$EDITOR`, or `vi` when `$EDITOR` is unset.
After editing, `clic` reloads the command, checks for emptiness, reruns danger detection, and asks for copy confirmation again.

If the rendered command is empty, copying is refused:

```text
Error: rendered command is empty.
```

### Default Query Shortcut

Known subcommands:

```text
add
ask
list
show
path
init
help
--help
```

Anything else is treated as a query and is equivalent to `clic ask <query...>`.

Examples:

```sh
clic pdf-to-png
clic pdf png 300dpi
clic "請求書PDFを画像にしたい"
```

Important distinction:

```text
clic add 12345  # save zsh history entry 12345
clic 12345      # query for saved command "12345"
```

### List

Show saved commands as ID and title.

```sh
clic list
```

Output:

```text
ffmpeg-resize-height    動画を指定高さにリサイズする
pdf-to-png              PDFをPNG画像に変換する
```

Rules:

- Sort by `id` ascending.
- Show only ID and title.
- If there are no saved commands, show a short guide:

```text
No commands saved yet.

Add one:
  clic add <history-number> --note "..."
```

### Show

Show a saved command entry in a human-readable format.

```sh
clic show <id>
```

`show` requires an exact ID match.
It does not perform fuzzy search.

Example:

```text
pdf-to-png
PDFをPNG画像に変換する

Original:
pdftoppm -png -r 300 invoice.pdf invoice

Template:
pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"

Params:
  dpi      default=300          出力画像の解像度
  input    default=invoice.pdf  入力PDFファイル
  prefix   default=invoice      出力ファイル名のプレフィックス

Tags:
pdf, image, poppler

Path:
/Users/taku/.config/clic/commands/pdf-to-png.md
```

To see the raw Markdown file:

```sh
cat "$(clic path pdf-to-png)"
```

### Path

Print important paths.

```sh
clic path <id>
clic path --home
clic path --commands
clic path --config
```

Examples:

```text
$ clic path pdf-to-png
/Users/taku/.config/clic/commands/pdf-to-png.md

$ clic path --config
/Users/taku/.config/clic/config.yaml

$ clic path --commands
/Users/taku/.config/clic/commands

$ clic path --home
/Users/taku/.config/clic
```

`path <id>` requires an exact ID match.
It does not perform fuzzy search.

Editing and deletion use normal file operations:

```sh
$EDITOR "$(clic path pdf-to-png)"
rm "$(clic path pdf-to-png)"
```

### Init

Initialize local files and print zsh integration.

```sh
eval "$(clic init zsh)"
```

Persistent setup:

```sh
clic init zsh >> ~/.zshrc
```

Behavior:

- Create `~/.config/clic`.
- Create `~/.config/clic/commands`.
- Create `~/.config/clic/config.yaml` if it does not exist.
- Never overwrite existing config or command files.
- Do not check whether Ollama is running or whether the configured model is installed.
- Print the zsh wrapper function to stdout.
- Print creation/reuse messages to stderr.

Stdout must contain only valid zsh code so `eval "$(clic init zsh)"` and `.zshrc` append workflows are safe.

Wrapper behavior:

```zsh
clic() {
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
```

Implementation should remove history-output padding from the first line while preserving command content:

```text
- first line: trim history-output leading padding
- subsequent lines: preserve as-is
- trailing newline: remove at most one final newline
```

If history entry lookup fails, exit with an error.
Do not show nearby history entries.

### Help

`clic --help` and `clic help` show only public commands.
Internal flags such as `--stdin` and `--history-id` are hidden.

Help should also show the main paths:

```text
Paths:
  home:     /Users/taku/.config/clic
  commands: /Users/taku/.config/clic/commands
  config:   /Users/taku/.config/clic/config.yaml
```

## Storage Model

Use plain text files under XDG config.
No environment variable override is provided in the MVP.

Default layout:

```text
~/.config/clic/
  commands/
    pdf-to-png.md
    ffmpeg-resize-height.md
  config.yaml
```

Each saved command is one Markdown file with YAML front matter.
The file name is `<id>.md`.

Example:

```md
---
id: pdf-to-png
title: PDFをPNG画像に変換する
original_command: pdftoppm -png -r 300 invoice.pdf invoice
template: pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"
tags: [pdf, image, poppler]
params:
  - name: dpi
    default: "300"
    description: 出力画像の解像度。
  - name: input
    default: invoice.pdf
    description: 入力PDFファイル。
  - name: prefix
    default: invoice
    description: 出力ファイル名のプレフィックス。
timestamp: "2026-07-21T12:00:00+09:00"
---

pdftoppmを使ってPDFを指定DPIのPNG画像に変換する。

## Notes

請求書PDFを画像化するために使った。
```

Notes:

- `tags: []` is kept even when empty.
- `params: []` is kept even when empty.
- `## Notes` is omitted when `--note` is empty.
- `timestamp` is local time in RFC3339 format.
- `timestamp` is a single timestamp, not split into created/updated fields.
- There is no `history.jsonl`.
- Execution history is not stored.

Rationale:

- Easy to inspect and edit by hand.
- Easy to sync through Git, dotfiles, Dropbox, or iCloud.
- Easy to back up and recover.
- No schema migration for personal use.
- Works well with `rg`.
- LLMs can consume individual Markdown entries naturally.
- The expected personal dataset is small enough for full-directory scans.

### Schema

Required front matter fields:

```yaml
id: pdf-to-png
title: PDFをPNG画像に変換する
original_command: pdftoppm -png -r 300 invoice.pdf invoice
template: pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"
tags: [pdf, image, poppler]
params:
  - name: dpi
    default: "300"
    description: 出力画像の解像度。
timestamp: "2026-07-21T12:00:00+09:00"
```

Required body:

```text
description paragraph
```

Optional body section:

```md
## Notes

user note
```

Removed fields:

- `type`
- `required`
- `generated_by`
- `confidence`
- `warnings`
- `created_at`
- `updated_at`

### IDs

`id` rules:

- Lowercase English slug.
- Allowed characters: `a-z`, `0-9`, `-`.
- No Japanese.
- No spaces.
- Used as file name.

Ollama generates an `id` candidate.
If the file already exists, `clic` appends `-2`, `-3`, and so on automatically.
The confirmation screen shows the final ID.

Fallback ID generation is not needed because `clic add` requires successful Ollama metadata generation.

### Params

`params` is required but may be empty.

Each param has:

```yaml
- name: input
  default: invoice.pdf
  description: 入力ファイル。
```

Rules:

- `default` is required but may be an empty string.
- `description` is required.
- Params are ordered by first occurrence of `{{param}}` in `template`.
- Params not referenced by `template` are invalid during `add`.
- Template variables not present in `params` are invalid during `add`.
- Param names must be unique.
- Param names may contain `a-z`, `A-Z`, `0-9`, and `_`.

### Multi-Line Commands

Multi-line `original_command` and `template` values are allowed.
Use YAML block scalars.

Example:

```md
---
id: convert-pdfs-to-png
title: PDFをまとめてPNG画像に変換する
original_command: |
  for f in *.pdf; do
    pdftoppm -png "$f" "${f%.pdf}"
  done
template: |
  for f in {{input_glob}}; do
    pdftoppm -png "$f" "${f%.pdf}"
  done
tags: [pdf, image]
params:
  - name: input_glob
    default: "*.pdf"
    description: 変換対象PDFのglob。
timestamp: "2026-07-21T12:00:00+09:00"
---

カレントディレクトリ内のPDFをまとめてPNG画像に変換する。
```

## Search

MVP search loads all `~/.config/clic/commands/*.md` files and scores them locally.

Search is case-insensitive.
No Japanese morphological analysis is used.

Tokenization:

```text
- split query on whitespace
- each token is matched as a substring
- Japanese text is matched as-is by substring
```

Searchable fields:

- file name
- `id`
- `title`
- `tags`
- `template`
- `original_command`
- Markdown body and notes

Scoring priority:

```text
high:
  id
  filename stem
  title
  tags

medium:
  template
  original_command

low:
  Markdown body and notes
```

Selection rules:

```text
- exact id match -> selected
- exact filename stem match -> selected
- otherwise, 1 match -> selected
- otherwise, 2+ matches -> selection UI
```

Do not auto-select based on score difference.

If the library becomes large later, an optional generated index can be added.
Do not introduce an index in the MVP unless full-directory scans become slow.

Optional future index:

```text
~/.config/clic/index.json
```

The index is derived cache data.
The Markdown command files remain the source of truth.

## Command Template Format

Use Mustache-like placeholders:

```sh
pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"
```

Rendering rules:

- Replace placeholders with user-provided strings exactly.
- Do not shell-quote parameter values automatically.
- Templates must include quotes where needed.
- If appropriate, Ollama may add quotes around parameter placeholders when creating a template.
- The rendered command must not be empty.

Template generation should be conservative.

Parameterize:

- file paths
- directory paths
- URLs
- dates
- obvious numeric literals
- user-specific names
- values explicitly mentioned in `--note`

Do not parameterize:

- command names
- option names
- shell operators
- command structure
- pipelines as abstract steps
- whole option expressions unless the value boundary is obvious

Example:

```sh
ffmpeg -i input.mov -vf scale=-2:720 output.mp4
```

Good:

```sh
ffmpeg -i "{{input}}" -vf scale=-2:{{height}} "{{output}}"
```

Too broad:

```sh
{{command}} -i {{input}} -vf {{filter}} {{output}}
```

Validation:

- Every `{{param}}` in `template` must exist in `params`.
- Every `params[].name` must appear in `template`.
- The first command token of `original_command` and `template` must match.
- For pipelines and multi-line commands, MVP validation checks only the first command token.

## Ollama Integration

Ollama is required for `clic add`.
Ollama is optional for `clic ask` argument inference.

Default config file:

```yaml
# CLIC config.
# Edit this file directly.
# Other model candidates: gemma4:e2b, gemma3:1b.

ollama:
  host: http://localhost:11434
  model: qwen2.5-coder:3b

ai:
  timeout_add: 10s
  timeout_ask: 3s
```

The config file is created by `clic init zsh` if missing.
There is no `config` command.
Edit the file directly:

```sh
$EDITOR "$(clic path --config)"
```

### Model Candidates

Default:

```text
qwen2.5-coder:3b
```

Other candidates:

```text
gemma4:e2b
gemma3:1b
```

The model is configurable in `config.yaml`.

### Add-Time LLM Task

When saving, Ollama generates JSON:

- `id`
- `title`
- `description`
- `template`
- `params`
- `tags`

Ollama must not return Markdown.
Go validates the JSON and writes the Markdown file.

Environment context sent to Ollama:

```text
OS: macOS
Shell: zsh
```

Do not send:

- cwd
- username
- hostname
- environment variables

Use `/api/generate` for MVP:

```http
POST http://localhost:11434/api/generate
```

Request shape:

```json
{
  "model": "qwen2.5-coder:3b",
  "prompt": "...",
  "stream": false,
  "format": "json",
  "options": {
    "temperature": 0.1,
    "num_predict": 600
  }
}
```

Prompt requirements:

```text
You summarize a shell command for reuse.
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
pdftoppm -png -r 300 invoice.pdf invoice

User note:
請求書PDFを300dpiのPNGにしたい
```

Expected JSON:

```json
{
  "id": "pdf-to-png",
  "title": "PDFをPNG画像に変換する",
  "description": "pdftoppmを使ってPDFを指定DPIのPNG画像に変換する。",
  "template": "pdftoppm -png -r {{dpi}} \"{{input}}\" \"{{prefix}}\"",
  "params": [
    {
      "name": "dpi",
      "default": "300",
      "description": "出力画像の解像度。"
    },
    {
      "name": "input",
      "default": "invoice.pdf",
      "description": "入力PDFファイル。"
    },
    {
      "name": "prefix",
      "default": "invoice",
      "description": "出力ファイル名のプレフィックス。"
    }
  ],
  "tags": ["pdf", "image", "poppler"]
}
```

Invalid add-time LLM output is an error.
Do not save partial or basic entries.

### Ask-Time LLM Task

Ask-time Ollama only infers arguments for an already selected entry.
It does not select entries and does not create new commands.

Use ask-time Ollama for:

- non-exact search result with 1 match
- user-selected match from multiple candidates

Skip ask-time Ollama for:

- exact ID match
- exact file-name stem match

Input to Ollama:

```json
{
  "environment": {
    "os": "macOS",
    "shell": "zsh"
  },
  "query": "請求書PDFを150dpiで画像化",
  "entry": {
    "id": "pdf-to-png",
    "template": "pdftoppm -png -r {{dpi}} \"{{input}}\" \"{{prefix}}\"",
    "params": [
      {
        "name": "dpi",
        "default": "300",
        "description": "出力画像の解像度。"
      },
      {
        "name": "input",
        "default": "invoice.pdf",
        "description": "入力PDFファイル。"
      },
      {
        "name": "prefix",
        "default": "invoice",
        "description": "出力ファイル名のプレフィックス。"
      }
    ]
  }
}
```

Output from Ollama:

```json
{
  "arguments": {
    "dpi": "150",
    "input": "",
    "prefix": ""
  }
}
```

Rules:

- Extra arguments not present in `params` are ignored.
- Missing arguments fall back to saved defaults or empty strings.
- Inferred values are never written back to the saved Markdown file.
- If ask-time Ollama fails, continue with saved defaults.

## Safety

`clic` never executes rendered commands.

It only copies commands to the clipboard with `pbcopy`.
Failure to run `pbcopy` is an error.

Danger detection still runs before copying because the user may paste and execute the command manually.

Dangerous-looking commands include:

- `rm`
- `rm -rf`
- `sudo`
- `chmod -R`
- `chown -R`
- `curl | sh`
- `wget | sh`
- `dd`
- `mkfs`
- `git reset --hard`
- commands containing destructive redirects

Danger detection only changes the copy confirmation default:

```text
normal:    Copy to clipboard? [Y/e/n]
dangerous: Copy to clipboard? [y/N/e]
```

There is no `--force`.

The original command is preserved separately from the generated template.
LLM output is an assistive suggestion, not trusted source of truth.

## MVP Scope

Build this first:

```sh
clic add <history-number> [--note "..."]
clic add <command...> [--note "..."]
clic ask <query...>
clic <query...>
clic list
clic show <id>
clic path <id|--home|--commands|--config>
clic init zsh
clic --help
```

MVP features:

- macOS + zsh target.
- Plain-text Markdown storage under `~/.config/clic`.
- `clic init zsh` wrapper for numeric history saving.
- Ollama metadata generation on `add`.
- Local full-directory search over command files.
- Optional Ollama argument inference on `ask`.
- Interactive parameter filling.
- Clipboard copy through `pbcopy`.
- Danger warning before copying.
- `$EDITOR` / `vi` editing for generated entries and rendered commands.
- Public help that includes major paths.

Explicitly excluded from MVP:

- `run`
- `search`
- `edit`
- `delete`
- `enrich`
- `config`
- `--list`
- `--run`
- `--ai`
- `--no-ai`
- `--fast`
- `--last`
- `--history`
- `-H`
- `--literal`
- `--url`
- `history.jsonl`
- execution history
- bash/fish integration

## UX Principle

The normal save path should be one confirmation, not an editor session.

The normal ask path should end at the clipboard, not at execution.

Editor-based editing is available as an escape hatch:

```text
Save? [Y/e/n]
Copy to clipboard? [Y/e/n]
```

This keeps the tool small enough for personal daily use while still letting the command library grow into a useful runbook over time.
