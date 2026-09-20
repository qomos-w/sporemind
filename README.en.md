<div align="center">

**English** | [简体中文](README.md)

<img src="assets/icon.svg" width="110" alt="sporemind" />

# SporeMind

**An AI-native agent development workbench.**

Every function-providing extension is a component; SporeMind orchestrates them with scripts and workflows — multi-agent teamwork, local-first, fully open source. [www.sporemind.ai](https://www.sporemind.ai)

[![Release](https://img.shields.io/badge/release-v0.5.0-7c5cff?logo=github)](https://github.com/qomos-w/sporemind/releases)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go)](https://go.dev)
[![Plugin SDK](https://img.shields.io/badge/plugin%20sdk-MIT-green)](sporemind-plugin-sdk/LICENSE)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux%20%7C%20Android-lightgrey)](#build)

**[⬇ Download the desktop app](https://www.sporemind.ai)** · Windows v0.3 available now, macOS / Android in development

</div>

---

## Component ecosystem

**More than plugins: anything that provides functions is a component.** Three tracks, one model — components register functions with the runtime; scripts and workflows wire them into pipelines.

| Track | Description |
|---|---|
| 🧬 **Native Plugin** | Deep Go-native integrations — in-process or over a subprocess frame protocol |
| 🔌 **MCP Server** | The standard MCP ecosystem plugs straight in; external tools join out of the box |
| 📦 **Bundle** | Built-in capability packs: debugging, tutoring, workflow tools — install and go |

## Orchestration

Components provide the functions; **Spore Script and visual workflows** decide when and in what order they run.

```spore
// fetch-and-translate.spore — components provide functions, scripts orchestrate
fn run(task) {
  page    = http.fetch(task.url)          // component fn
  summary = ai.summarize(page.text)
  out     = ai.translate(summary, task.lang)
  kb.store("articles", out)               // store in KB
  notify.send("done", out.title)
}
```

- **Scripts call functions** — strongly typed Spore Script calls any component function, schema-first and type-safe
- **Workflow as program** — describe a goal with `/workflow` and get a task-card topology, nodes wired by dependency with context flowing between them
- **Multi-agent teamwork** — ten built-in roles to mix and match, sub-agents in parallel, interruptible and replayable throughout

## Principles

| | |
|---|---|
| 🏠 **Local-first** | Data never leaves the machine, works offline; cloud is an optional boost |
| 🔑 **BYOK** | OpenAI, Anthropic, Gemini, Ollama and more — you own the keys, never locked in |
| 🔍 **Structured audit** | Turn / step / request ledgers make every decision traceable and replayable |
| 🌍 **Fully open source** | Runtime and component ecosystem, all public — read, audit, modify |

## Foundation

Built on [gospore](https://github.com/qomos-w/gospore) (actor orchestration engine) and [spore](https://github.com/qomos-w/spore) (schema, codec, identity & script layer): agents, tools, panels and the LLM loop live in one supervised actor tree with typed wire contracts end to end, hot-reloadable behavior, and a signed plugin workshop (Ed25519 + AES-GCM).

## Build

> A raw `go build ./...` needs generated assets first (web frontend `dist/`, `sporemind.apk`, tiktoken data) — the Makefile targets handle these. Go 1.27+ · Node.js / bun · Wails CLI v3.

```bash
make dev-desktop      # hot-reload development
make build-desktop    # desktop build
make release-desktop  # production release
make build-apk        # android apk → ./build/sporemind-v{X.Y.Z}.apk
go test ./...         # tests
```

## Repository layout

| Path | Contents |
|---|---|
| `pkg/actor/...` | actors: agent, appmanager, browsermanager, computeruse, aiaggregator, lspserver, storeclient, ... |
| `pkg/desktop/` | Wails desktop host |
| `web/` | frontend (Vite + React) |
| `schemas/` | spore schemas — single source of truth for cross-language contracts |
| `cmd/` | server / codegen entry points |
| `sporemind-plugin-sdk/` | plugin SDK (MIT, vendored module via `go.mod` replace) |
| `CLAUDE.md` | engineering constraints & conventions for contributors |

## License

[AGPL-3.0](LICENSE) · the plugin SDK is [MIT](sporemind-plugin-sdk/LICENSE) so plugins can be commercial without AGPL obligations.

<div align="center">

**[www.sporemind.ai](https://www.sporemind.ai)**

</div>
