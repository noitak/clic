# Model Evaluation

This directory contains a small repeatable evaluation harness for comparing
Ollama models used by `clic`.

The current comparison target is:

- `qwen2.5-coder:3b`
- `gemma4:e2b-mlx`

`clic` uses the model in two different places, so the evaluation is split into
two task types:

- `add`: generate metadata for a saved shell command.
- `ask`: infer parameter values for a selected saved command.

## Run

Make sure Ollama is running and both models are available:

```sh
ollama list
ollama pull qwen2.5-coder:3b
ollama pull gemma4:e2b-mlx
```

Then run:

```sh
go run ./eval/model_compare \
  -models qwen2.5-coder:3b,gemma4:e2b-mlx \
  -runs 3
```

The harness writes JSONL records to `eval/results/`.
It uses Ollama structured outputs by passing a JSON schema in the `format`
field, matching the main application.

## Metrics

Each record includes:

- `latency_ms`: end-to-end Ollama request latency.
- `valid_json`: whether the model response decoded into the expected shape.
- `validation_passed`: whether structural project rules passed.
- `matched_expectations`: whether case-specific expectations passed.
- `error`: request, JSON, or validation error text.

For `add`, automated checks focus on structural correctness:

- slug-like `id`
- required Japanese-facing fields are present
- template placeholders and `params` match exactly
- expected parameters appear
- fixed command fragments are preserved

For `ask`, automated checks compare inferred argument values against expected
values.

After running, inspect failing JSONL records manually. The automatic checks are
intentionally conservative; final model choice should also consider whether the
Japanese title and descriptions are useful in daily use.
