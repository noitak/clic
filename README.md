# clic

`clic` は **Command Library in Context** の略です。

`clic` は、よく使うシェルコマンドを Markdown の小さな runbook として保存し、自然言語で探して、パラメータを埋めて、クリップボードへコピーするための個人用 CLI です。

`clic` は生成したコマンドを実行しません。
ユーザーが自分のシェルへ貼り付けて実行するためのコマンドを準備します。

```text
clic = search + select + fill params + copy command
```

## 対象環境

MVP は macOS と zsh を対象にしています。

- OS：macOS
- Shell：zsh
- Clipboard：`pbcopy`
- LLM：Ollama

## インストール

このリポジトリを取得して、Go でビルドします。

```sh
go build -o clic .
```

動作確認だけなら、生成された `./clic` をそのまま使えます。
普段使いする場合は、PATH の通った場所へ配置してください。

```sh
install ./clic /usr/local/bin/clic
```

## 初期化

zsh 連携を初期化します。

```sh
eval "$(clic init zsh)"
```

永続化する場合は、出力を `.zshrc` に追記します。

```sh
clic init zsh >> ~/.zshrc
```

`init zsh` は次のファイルとディレクトリを作成します。

```text
~/.config/clic/
  config.yaml
  commands/
```

既存の設定やコマンドファイルは上書きしません。

## Ollama

`clic add` は Ollama を使って、保存するコマンドのメタデータとテンプレートを作ります。

```sh
ollama serve
ollama pull qwen2.5-coder:3b
```

初期設定は `~/.config/clic/config.yaml` に作られます。

```yaml
ollama:
  host: http://localhost:11434
  model: qwen2.5-coder:3b

ai:
  timeout_add: 10s
  timeout_ask: 3s
```

## コマンドを保存する

履歴番号から保存する場合は、zsh 連携を読み込んだ状態で実行します。

```sh
clic add 12345 --note "請求書PDFを画像化する"
```

コマンドを直接渡して保存することもできます。

```sh
clic add ffmpeg -i input.mov -vf scale=-2:720 output.mp4 --note "動画を720pに変換する"
```

保存時には、Ollama が `id`、タイトル、説明、テンプレート、パラメータ、タグを生成します。
生成結果は確認画面で表示され、`Y` で保存、`e` で編集、`n` で破棄できます。

```text
Save? [Y/e/n]
```

同じ `original_command` を持つエントリがすでにある場合は、重複を警告してから保存するか確認します。

## コマンドを探してコピーする

保存したコマンドは `ask` で検索します。

```sh
clic ask pdf png 300dpi
clic ask "請求書PDFを300dpiのPNGにしたい"
```

既知のサブコマンド以外は、`ask` と同じ検索として扱われます。

```sh
clic pdf-to-png
clic pdf png 300dpi
```

検索結果が一つなら、`id` とタイトルを表示してから自動選択されます。
複数ある場合は、候補から番号で選びます。

```text
Match:

pdf-to-png              PDFをPNG画像に変換する
```

```text
Matches:

1. pdf-to-png              PDFをPNG画像に変換する
2. pdf-compress            PDFを圧縮する

Select [1-2], q to quit:
```

選択後、テンプレートのパラメータを入力します。
入力を空にすると、Ollama が推定した値、保存済みの default、空文字列の順で既定値が使われます。

```text
dpi [150]:
input [invoice.pdf]:
prefix [invoice]:
```

最後に描画されたコマンドを確認し、`Y` でクリップボードへコピーします。

```text
Command:
pdftoppm -png -r 150 "invoice.pdf" "invoice"

Copy to clipboard? [Y/e/n]
```

危険そうなコマンドでは、コピー確認の既定値が `N` になります。

```text
Warning: destructive-looking command.

Copy to clipboard? [y/N/e]
```

## 一覧と表示

保存済みコマンドの一覧を表示します。

```sh
clic list
```

引数なしの `clic` と空の `clic ask` も一覧を表示します。

```sh
clic
clic ask
```

保存済みエントリの詳細を表示します。

```sh
clic show pdf-to-png
```

## パス

設定や保存先のパスを表示できます。

```sh
clic path --home
clic path --commands
clic path --config
clic path pdf-to-png
```

編集や削除は通常のファイル操作で行います。

```sh
$EDITOR "$(clic path pdf-to-png)"
rm "$(clic path pdf-to-png)"
```

## 保存形式

各コマンドは `~/.config/clic/commands/<id>.md` に保存されます。

```md
---
id: pdf-to-png
title: PDFをPNG画像に変換する
original_command: pdftoppm -png -r 300 invoice.pdf invoice
template: pdftoppm -png -r {{dpi}} "{{input}}" "{{prefix}}"
tags: ["pdf", "image", "poppler"]
params:
  - name: "dpi"
    default: "300"
    description: "出力画像の解像度。"
  - name: "input"
    default: "invoice.pdf"
    description: "入力PDFファイル。"
  - name: "prefix"
    default: "invoice"
    description: "出力ファイル名のプレフィックス。"
timestamp: "2026-07-21T12:00:00+09:00"
---

pdftoppmを使ってPDFを指定DPIのPNG画像に変換する。

## Notes

請求書PDFを画像化するために使った。
```

Markdown ファイルが正なので、Git、dotfiles、Dropbox、iCloud などで同期できます。

## 開発

テストを実行します。

```sh
env GOCACHE=/private/tmp/clic-go-build-cache go test ./...
```

ビルドします。

```sh
env GOCACHE=/private/tmp/clic-go-build-cache go build -o /private/tmp/clic-test .
```

Go の標準ビルドキャッシュがサンドボックス外にある環境では、`GOCACHE` を `/private/tmp` などの書き込み可能な場所へ向けてください。

## 設計上の制約

`clic` はコマンドを実行しません。
保存済みエントリが見つからない場合も、Ollama に新しいコマンドを作らせません。

`run`、`search`、`edit`、`delete`、`config` サブコマンドは MVP にはありません。
編集や削除は保存された Markdown ファイルを直接操作します。
