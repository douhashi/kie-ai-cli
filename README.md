# kie-ai-cli

An unofficial command-line interface for [kie.ai](https://kie.ai).

> **Status:** the commands below work, and binaries are published on the
> [releases page](https://github.com/douhashi/kie-ai-cli/releases). See
> `docs/business/overview.md` for the concept and `docs/development/roadmap.md`
> for the plan.

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
  resolves with no network at all. The catalog records the day it was generated,
  and one older than 90 days says so. Updating the catalog is an explicit
  command; nothing is fetched behind your back.

## Install

Every release publishes one binary per platform and a `checksums.txt` covering
all of them. Take the one for your machine, verify it, and put it on your
`PATH` under the name `kie` — the name every example below types.

```sh
base=https://github.com/douhashi/kie-ai-cli/releases/latest/download
curl -fLO "$base/kie-ai-cli_linux_amd64"
curl -fLO "$base/checksums.txt"
sha256sum --ignore-missing -c checksums.txt
mkdir -p ~/.local/bin
install -m 755 kie-ai-cli_linux_amd64 ~/.local/bin/kie
```

The other names are `kie-ai-cli_linux_arm64`, `kie-ai-cli_darwin_amd64`,
`kie-ai-cli_darwin_arm64` and `kie-ai-cli_windows_amd64.exe`. macOS has no
`sha256sum`; verify there with
`grep kie-ai-cli_darwin_arm64 checksums.txt | shasum -a 256 -c`.

With a Go toolchain at hand, `go install github.com/douhashi/kie-ai-cli@latest`
builds the same program from source. It lands as `kie-ai-cli`, so rename or
link it to `kie` to type the shorter name.

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

kie completion show <bash|zsh>
kie completion list <model|category|vendor>
```

`model list` prints one line per model — ID, category, vendor and name — and
`model show` adds the documentation link and every input field with its type,
whether it is required, its default and the values it accepts.

`catalog update` downloads the published catalog into the state directory, and
every command reads that copy from then on. `catalog show` reports which
catalog is in effect — `built-in` or `downloaded` — the day it was generated,
how many models it holds, and, for a downloaded one, the directory it sits in.
There is no command to go back: delete that directory and the binary returns to
the catalog it was built with. A downloaded catalog this binary cannot read is
reported rather than skipped over, so the origin `catalog show` names is always
the one actually in use.

`task run` takes the model ID as the first argument after the verb and its
inputs as flags named after the model's own input fields. `--input` accepts the
same inputs as a JSON document, read from a file or from standard input; flags
override what the JSON sets. An array field is given one element per
occurrence, and a boolean is written `--field` or `--field=false`.

A field the catalog does not list is refused before anything is sent, because a
submission cannot be cancelled and a mistyped field name would be paid for. If
the model really does take it, `model show <model-id>` says what this catalog
knows and `catalog update` fetches a newer one. A handful of fields are
declared with a name no shell can pass — a trailing space — and are reachable
through `--input` alone.

Nothing is submitted until the whole input has been checked and the ledger has
been opened, so a required field that is missing, or a ledger that cannot be
written to, costs nothing. Once a task exists its ID is printed even if
recording it afterwards fails: it is the only handle on what was bought.

Nothing waits for a result. `task refresh` asks kie.ai about every task that
has not finished yet and writes down what it says; `task list` only reads the
ledger, and reports at the end how many of its rows may already be out of date.
A task is `submitted`, `running`, `succeeded` or `failed`: kie.ai describes each
family of models in a vocabulary of its own, and those four are what they are
normalised to.

Three query endpoints are understood, which covers 145 of the 161 models in the
catalog. `task refresh` names the rest by task ID and endpoint and leaves their
rows exactly as they were, rather than guessing: the same field means "failed"
on one endpoint and "still generating" on another, so a reading taken from the
documentation alone would be written into the ledger as fact. Submitting to them
is not restricted and their task IDs are kept, so they can be collected once
their answers have been read against the live API.

`task download` writes what a task produced into a directory — the current one
unless `--dir` names another, which is made if it is not there. Each result is
saved as `<task-id>-<n>`, with the extension the result URL carries or, failing
that, the one the host declared for it. A task is saved whole or not at all: the
paths reach the ledger only once every file is on disk, so a download
interrupted half way is still a task with something left to save. What is
already saved is not fetched again — a result was paid for and may have been
edited since, and the ledger says where each one went.

`--unsaved`, on `task download` and on `task list` alike, is every task that has
produced something no path has been recorded for. A success with nothing behind
it is not one of them: the lyrics endpoints answer with the words themselves, so
such a task has no file to save and would otherwise sit in the listing for ever.

Results are fetched with a plain unauthenticated GET, which is all the hosts
serving them ask for; the API key is never sent to them. kie.ai will re-issue a
link for a result, expiring twenty minutes later, and that is used only when the
recorded URL is refused — once, because a result that has expired is refused
however many links are issued for it.

`file upload` takes a path on this machine or an http/https address, and prints
the URL kie.ai stored the file under and nothing else, so that
`kie task run <model> --image "$(kie file upload photo.png)"` is all it takes to
use one. Every upload goes into a directory of its own: kie.ai addresses a
stored file by its path and name together and has no endpoint that deletes one,
so a second `photo.png` would otherwise take the place of the first. Uploads
expire on their own after a few days.

`completion show` prints the completion script for a shell, which completes
the commands, their flags, and the model IDs, categories and vendors of the
catalog in effect. A model ID averages 25 characters of `vendor/model` and has
neither an abbreviation nor an alias, so completion is how one is typed.

```sh
source <(kie completion show bash)   # in ~/.bashrc
source <(kie completion show zsh)    # in ~/.zshrc, after compinit
```

The script holds the commands and their flags, and asks the binary for the
values that come from the catalog, so `catalog update` changes what is
completed without the script being written again. `completion list` is what it
asks with; it prints one value per line and reads a small index beside the
catalog rather than the catalog itself, so a keystroke does not wait for
700KB of schemas to be decoded. The flags `task run` takes for the input
fields of a model are not completed: they are the one part of the command line
that only the catalog knows.

`--json` changes the output of a successful command, and the two `completion`
commands do not take it: neither a shell script nor a list of words for one is
a document. Errors are always plain text on stderr, distinguished by the exit
code.

## Not covered

The chat models on kie.ai (Gemini, Claude, GPT, Grok, Codex) speak the
OpenAI-compatible `/v1/chat/completions` endpoint. They are synchronous, return
no `taskId`, and never reach the ledger, so they are left out of the catalog —
use any OpenAI-compatible client for those.

## State on disk

Everything lives under `$XDG_DATA_HOME/kie-ai-cli`, or under
`~/.local/share/kie-ai-cli` when that variable is unset or not an absolute
path: the SQLite ledger in `ledger.db`, the downloaded catalog in `catalog/`,
and the configuration in `config/config.json`. A downloaded catalog is three
files: the two that are published, and `index.tsv`, which is derived from them
when they are downloaded and is what completion reads.

What tasks produce does not live there. A generated image or video is the
user's own work rather than this tool's state, so `task download` writes it into
the current directory, or into the one `--dir` names, and never under the state
directory. The ledger records the absolute path of every file it saved, which is
how `--unsaved` knows what is left to collect — and it records the path it
wrote, so moving the file afterwards does not make the task look uncollected.

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
arm64, except Windows) and writes `dist/checksums.txt` over the result. Every
binary is built with `CGO_ENABLED=0`: no C compiler is involved and the result
has no runtime dependencies. This is the same task a release runs, with
`KIE_AI_VERSION` set to the tag so that `--version` reports it.

The development setup is described in `docs/development/setup.md`, in Japanese.

## License

MIT. See [LICENSE](LICENSE).

This project is not affiliated with kie.ai.
