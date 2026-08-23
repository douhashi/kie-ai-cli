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
- **One kind of operation.** Market models and the older per-service APIs
  (Suno, Veo3, 4o Image, Flux Kontext, Runway, File Upload) are exposed the same
  way. An operation is one create/query pair, addressed by an operation ID.
- **A catalog generated from the docs.** Operations, their endpoints and their
  input schemas are derived from the OpenAPI specifications embedded in the
  kie.ai documentation, so new models can be picked up without hand-written
  definitions.
- **A single binary.** Written in Go, with the catalog baked in. Updating the
  catalog is an explicit command; nothing is fetched behind your back.

## State on disk

Everything lives under `$XDG_DATA_HOME/kie-ai-cli`: the SQLite ledger, the
downloaded catalog, and the configuration file. The API key is read from the
environment first and from the configuration file otherwise.

## License

MIT. See [LICENSE](LICENSE).

This project is not affiliated with kie.ai.
