# kie-ai-cli

An unofficial command-line interface for [kie.ai](https://kie.ai).

> **Status:** design stage. Nothing is released yet. See `docs/business/overview.md`
> for the concept and `docs/development/roadmap.md` for the plan.

## Why

The kie.ai API is task-based and asynchronous: you create a task, poll it by
`taskId`, and receive a result URL. Waiting for each request in turn makes it
hard to run several generations at once — and kie.ai has no endpoint that lists
your tasks, so remembering what you submitted is the client's job.

`kie-ai-cli` submits without waiting and lets you collect the results later. It
keeps a local ledger of everything it sent.

## Concept

- **Submit and walk away.** Creating a task records it in a local ledger and
  exits immediately. Status checks and downloads are separate commands.
- **One kind of model.** Market models and the older per-service APIs
  (Suno, Veo3, 4o Image, Flux Kontext, Runway) are exposed the same way. A model
  is one create/query pair, addressed by a model ID.
- **A catalog generated from the docs.** Models, their endpoints and their input
  schemas are derived from the OpenAPI specifications embedded in the kie.ai
  documentation, so new models can be picked up without hand-written definitions.
- **A single binary.** Written in Go, with the catalog baked in, so every model
  resolves with no network at all. The baked-in catalog records the day it was
  generated, and a binary carrying one older than 90 days says so. Updating the
  catalog is an explicit command; nothing is fetched behind your back.

## Commands

Every command is a noun followed by a verb.

```sh
kie model list [--category image] [--vendor seedream]
kie model show <model-id>

kie task run <model-id> [--<field> <value>...] [--input <file|->]
kie task list [--status <status>] [--unsaved]
kie task refresh
kie task download <task-id> | --unsaved

kie catalog update
kie catalog show
kie config set <key> <value>
kie config show
kie file upload <path|url>
kie credits show
```

`task run` takes the model ID as a positional argument and its inputs as flags
derived from the catalog. `--input` accepts the same inputs as a JSON document,
read from a file or from standard input; flags override what the JSON sets.

Nothing waits for a result. `task refresh` polls the tasks that are still
running and updates the ledger; `task list` only reads the ledger.

`--json` changes the output of a successful command. Errors are always plain
text on stderr, distinguished by the exit code.

## Not covered

The chat models on kie.ai (Gemini, Claude, GPT, Grok, Codex) speak the
OpenAI-compatible `/v1/chat/completions` endpoint. They are synchronous, return
no `taskId`, and never reach the ledger, so they are left out of the catalog —
use any OpenAI-compatible client for those.

## State on disk

Everything lives under `$XDG_DATA_HOME/kie-ai-cli`, or under
`~/.local/share/kie-ai-cli` when that variable is unset or not an absolute
path: the SQLite ledger in `ledger.db`, the downloaded catalog in `catalog/`,
and the configuration in `config/config.json`.

The API key is read from the `KIE_AI_API_KEY` environment variable first and
from the configuration file otherwise, so a key can be given for a single
invocation without being stored. `kie config set api_key <value>` writes the
file, which is created with mode `0600` and never left more permissive than
that. `kie config show` reports which key is in effect — masked to its last four
characters — where it came from, and where each of those files lives.

## Build

[mise](https://mise.jdx.dev/) pins the Go toolchain, so no separate Go
installation is needed.

```sh
mise install     # install the pinned toolchain
mise run build   # build dist/kie-ai-cli for this machine
```

`mise run build-all` cross-compiles for Linux, macOS and Windows (amd64 and
arm64, except Windows). Every binary is built with `CGO_ENABLED=0`: no C
compiler is involved and the result has no runtime dependencies.

The development setup is described in `docs/development/setup.md`, in Japanese.

## License

MIT. See [LICENSE](LICENSE).

This project is not affiliated with kie.ai.
