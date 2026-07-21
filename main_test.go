package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
