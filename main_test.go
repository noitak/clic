package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderReadAndShowEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		ID:              "pdf-to-png",
		Title:           "PDFをPNG画像に変換する",
		OriginalCommand: "pdftoppm -png -r 300 invoice.pdf invoice",
		Template:        `pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"`,
		Tags:            []string{"pdf", "image", "poppler"},
		Params: []Param{
			{Name: "dpi", Default: "300", Description: "出力画像の解像度。"},
			{Name: "input", Default: "invoice.pdf", Description: "入力PDFファイル。"},
			{Name: "prefix", Default: "invoice", Description: "出力ファイル名のプレフィックス。"},
		},
		Timestamp:   "2026-07-21T12:00:00+09:00",
		Description: "pdftoppmを使ってPDFを指定DPIのPNG画像に変換する。",
		Notes:       "請求書PDFを画像化するために使った。",
	}
	path := filepath.Join(commandsDir(), entry.ID+".md")
	if err := os.WriteFile(path, []byte(renderMarkdown(entry)), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readEntry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != entry.ID || got.Template != entry.Template || got.Notes != entry.Notes {
		t.Fatalf("entry round trip mismatch: %#v", got)
	}
	var out bytes.Buffer
	if err := cmdShow([]string{entry.ID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Path:\n"+path) {
		t.Fatalf("show output missing path:\n%s", out.String())
	}
}

func TestAskExactMatchSkipsOllamaAndCanDeclineCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		ID:              "echo-name",
		Title:           "名前を表示する",
		OriginalCommand: "echo taku",
		Template:        "echo {{name}}",
		Tags:            []string{"echo"},
		Params:          []Param{{Name: "name", Default: "taku", Description: "表示する名前。"}},
		Timestamp:       "2026-07-21T12:00:00+09:00",
		Description:     "echoで名前を表示する。",
	}
	if err := os.WriteFile(filepath.Join(commandsDir(), entry.ID+".md"), []byte(renderMarkdown(entry)), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := cmdAsk([]string{"echo-name"}, strings.NewReader("\nn\n"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "Ollama") {
		t.Fatalf("exact match should not call ask-time Ollama: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "Command:\necho taku") {
		t.Fatalf("ask output missing rendered command:\n%s", out.String())
	}
}

func TestAskSingleSearchResultShowsIDAndTitle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		ID:              "resize-video",
		Title:           "動画をリサイズする",
		OriginalCommand: "ffmpeg -i input.mov output.mp4",
		Template:        `ffmpeg -i "{{input}}" "{{output}}"`,
		Tags:            []string{"ffmpeg"},
		Params: []Param{
			{Name: "input", Default: "input.mov", Description: "入力動画。"},
			{Name: "output", Default: "output.mp4", Description: "出力動画。"},
		},
		Timestamp:   "2026-07-21T12:00:00+09:00",
		Description: "請求書ではなく動画変換のためのコマンド。",
	}
	if err := os.WriteFile(filepath.Join(commandsDir(), entry.ID+".md"), []byte(renderMarkdown(entry)), 0644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := cmdAsk([]string{"動画変換"}, strings.NewReader("\n\nn\n"), &out, &errOut); err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "Match:\n\nresize-video") {
		t.Fatalf("ask output missing single match id:\n%s", got)
	}
	if !strings.Contains(got, "動画をリサイズする") {
		t.Fatalf("ask output missing single match title:\n%s", got)
	}
}

func TestAskPromptsParamsFromUnindentedYAMLList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	entry := `---
id: "convert-video-resolution"
title: "動画の解像度変更"
original_command: "ffmpeg -i input.mov -vf scale=-2:720 output.mp4"
template: "ffmpeg -i {{input_file}} -vf scale=-2:720 {{output_file}}"
tags: ["video", "resolution"]
params:
- name: "input_file"
  default: "input.mov"
  description: "入力ファイルのパス"
- name: "output_file"
  default: "output.mp4"
  description: "出力ファイルのパス"
timestamp: "2026-07-22T09:06:28+09:00"
---

指定したファイルの解像度を変更します。
`
	if err := os.WriteFile(filepath.Join(commandsDir(), "convert-video-resolution.md"), []byte(entry), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := cmdAsk([]string{"convert-video-resolution"}, strings.NewReader("\n\nn\n"), &out, &errOut); err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "input_file [input.mov]: ") {
		t.Fatalf("ask output missing input_file prompt:\n%s", got)
	}
	if !strings.Contains(got, "output_file [output.mp4]: ") {
		t.Fatalf("ask output missing output_file prompt:\n%s", got)
	}
	if !strings.Contains(got, "ffmpeg -i input.mov -vf scale=-2:720 output.mp4") {
		t.Fatalf("ask output missing rendered command:\n%s", got)
	}
}

func TestSearchFindsJapaneseBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		ID:              "resize-video",
		Title:           "動画をリサイズする",
		OriginalCommand: "ffmpeg -i input.mov output.mp4",
		Template:        `ffmpeg -i "{{input}}" "{{output}}"`,
		Tags:            []string{"ffmpeg"},
		Params: []Param{
			{Name: "input", Default: "input.mov", Description: "入力動画。"},
			{Name: "output", Default: "output.mp4", Description: "出力動画。"},
		},
		Timestamp:   "2026-07-21T12:00:00+09:00",
		Description: "請求書ではなく動画変換のためのコマンド。",
	}
	if err := os.WriteFile(filepath.Join(commandsDir(), entry.ID+".md"), []byte(renderMarkdown(entry)), 0644); err != nil {
		t.Fatal(err)
	}
	matches, exact, err := searchEntries("動画変換")
	if err != nil {
		t.Fatal(err)
	}
	if exact || len(matches) != 1 || matches[0].ID != entry.ID {
		t.Fatalf("unexpected search result exact=%v matches=%#v", exact, matches)
	}
}

func TestGenerateAddMetadataRetriesInvalidParams(t *testing.T) {
	old := ollamaJSONCall
	defer func() { ollamaJSONCall = old }()

	calls := 0
	ollamaJSONCall = func(cfg Config, timeout time.Duration, prompt string, numPredict int, target any) error {
		calls++
		out := target.(*addJSON)
		*out = addJSON{
			ID:          "resize-video",
			Title:       "動画をリサイズする",
			Description: "ffmpegで動画を指定した高さにリサイズする。",
			Template:    `ffmpeg -i "{{input_file}}" -vf scale=-2:{{height}} "{{output_file}}"`,
			Params: []Param{
				{Name: "input_file", Default: "input.mov", Description: "入力動画ファイル。"},
				{Name: "height", Default: "720", Description: "出力動画の高さ。"},
			},
			Tags: []string{"ffmpeg", "video"},
		}
		if calls == 2 {
			out.Params = append(out.Params, Param{Name: "output_file", Default: "output.mp4", Description: "出力動画ファイル。"})
		}
		return nil
	}

	gen, err := generateAddMetadata(Config{}, "ffmpeg -i input.mov -vf scale=-2:720 output.mp4", "動画を720pに変換する")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected one repair call, got %d", calls)
	}
	if len(gen.Params) != 3 || gen.Params[2].Name != "output_file" {
		t.Fatalf("missing repaired output_file param: %#v", gen.Params)
	}
}
