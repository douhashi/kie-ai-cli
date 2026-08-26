---
name: {{.Name}}
description: Generate images, video and music through kie.ai with the {{.Program}} command-line tool - find a model, submit a task, collect the result. Use when the user asks to generate media through kie.ai, or to check on or collect tasks that were already submitted.
---

{{.Marker}}

# kie.ai from the command line

`{{.Program}}` submits generation tasks to kie.ai and collects the results afterwards.
The API is asynchronous: submitting prints a task ID and returns, and the result
is fetched later by a separate command. The tool keeps a local ledger of every
task it sent, because kie.ai has no endpoint that lists them.

## Before running anything

- **A task costs the user money and cannot be cancelled once submitted.** Submit
  only what the user asked for, and never resubmit to see whether it works this
  time. Always report the task ID that comes back: it is the only handle on what
  was bought.
- The binary is installed as `{{.Program}}` or as `{{.Full}}`. If neither is on the PATH,
  say so rather than installing anything.
- `{{.Program}} config show` reports whether an API key is in effect and where it came
  from; `{{.Program}} credits show` reports the balance.
- Add `--json` to any command whose output you are going to read; the plain
  output is laid out for a person. Errors are plain text on stderr whatever the
  format, and the exit code tells the kinds apart: 2 is a mistake in how the
  command was called, 1 is a command that ran and failed.

## 1. Find the model

Model IDs and their input fields come from a catalog inside the binary. **Do not
guess either one.** A model ID has no abbreviation and no alias, and an input
field the catalog does not list is refused before anything is sent.

```sh
{{.Program}} model list --category image        # image, video, music
{{.Program}} model list --vendor seedream
{{.Program}} model show bytedance/seedream-v4-text-to-image
```

`model show` prints the documentation link and every input field with its type,
whether it is required, its default and the values it accepts. Read it before
submitting: the catalog is the only description of the model this tool has.

If the model the user names is not listed, `{{.Program}} catalog update` fetches a newer
catalog, and `{{.Program}} catalog show` says how old the one in effect is.

## 2. Submit

```sh
{{.Program}} task run <model-id> --prompt "a cat, oil on canvas"
{{.Program}} task run <model-id> --input request.json    # - reads stdin; flags win over the file
```

The command prints the task ID and exits. Nothing waits for the result. An array
field takes one occurrence per element (`--image "$one" --image "$two"`), and a
boolean is written `--field` or `--field=false`.

kie.ai takes images and other media as URLs, not as paths on this machine.
Upload first:

```sh
{{.Program}} task run <model-id> --image "$({{.Program}} file upload photo.png)"
```

## 3. Follow it

```sh
{{.Program}} task refresh          # ask kie.ai about every unfinished task
{{.Program}} task list --json      # read the ledger; this one talks to nobody
```

A task is `submitted`, `running`, `succeeded` or `failed`. `task list` reports how
many of its rows may be out of date, and `task refresh` is what brings them up to
date.

**Do not sit in a polling loop.** Generation takes minutes, and sleeping in a
shell spends the user's time and your context on nothing. Refresh once, tell the
user the task ID and what it says, and stop there. Refresh again when they ask.

## 4. Collect

```sh
{{.Program}} task download --unsaved --dir ./out    # everything not saved yet
{{.Program}} task download <task-id>
```

Results are written to the current directory unless `--dir` names another, as
`<task-id>-<n>` with the extension the result carries. What is already saved is
not fetched again, and `{{.Program}} task list --unsaved` is what is left to collect.
Results expire — a download link in twenty minutes, some results in days — so
collect a finished task rather than leaving it there.

## Not covered

The chat models on kie.ai (Gemini, Claude, GPT, Grok, Codex) are synchronous
OpenAI-compatible endpoints. They return no task ID, are not in the catalog, and
are not reachable through this tool.

## Every command

{{range .Commands}}- `{{$.Program}} {{.Name}}` — {{.Summary}}
{{end}}
Everything the tool persists — the ledger, a downloaded catalog and the
configuration — lives under `$XDG_DATA_HOME/{{.Full}}`, or under
`~/.local/share/{{.Full}}`. What tasks produce does not: that is written where
`task download` was told to write it.
