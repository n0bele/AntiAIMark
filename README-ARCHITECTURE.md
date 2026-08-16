# Architecture & extension guide

(Distribution layout note: this standalone Go project was extracted from the
antiaimark monorepo; references to `service/scripts/` describe the
optional Python ML-harness adapters, which are env-gated and absent here by
default.)

The Go tree is organized as a **core library plus thin facades**. If you are
writing a plugin for an AI IDE (Claude Code, Cursor, Windsurf, Cline,
Continue, Zed, VS Code, JetBrains, …), build it on one of the facades below —
never reimplement the pipeline.

```
                 ┌────────────────────────────────────────────┐
                 │              facades (thin)                │
   AI IDEs ──────▶  MCP server   HTTP API   CLIs   web UI     │
   web apps  ─────▶  internal/mcp internal/httpapi  cmd/*     │
   scripts   ──────▶            cmd/antiaimark-mcp            │
                 └───────────────┬────────────────────────────┘
                                 │  pure library calls
                 ┌───────────────▼────────────────────────────┐
                 │                core library                │
                 │  internal/cleaning   (text/image/container │
                 │                      pipelines, audits)    │
                 │  internal/i18n      (en/zh/es/fr/ru)       │
                 │  internal/cliutil   (flag & exit plumbing) │
                 └────────────────────────────────────────────┘
```

## Layers

| Package | Role | Depends on |
| --- | --- | --- |
| `internal/cleaning` | The whole pipeline: Layer A Unicode, image metadata (PNG/JPEG/WebP), containers (PDF/DOCX/ODT/SVG/HTML/MD + best-effort video), audits, safe writes, subprocess adapters for the optional ML harnesses. All results are plain `map[string]interface{}` in the Python-compatible JSON shape. | `i18n` (English fallback text only) |
| `internal/i18n` | Message catalog for every human-facing string (CLI output, HTTP errors, web UI, MCP tool descriptions). English is the reference and byte-identical to the Python originals. | — |
| `internal/cliutil` | Shared CLI plumbing: argparse-style interspersed flag parsing, `--lang`, Python-parity fatal-error exits. | `cleaning`, `i18n` |
| `internal/httpapi` | Embeddable HTTP service: the Python-compatible JSON API, the multipart upload/download web extension, the embedded web UI and `/api/i18n`. Errors localize per `Accept-Language`. | `cleaning`, `i18n` |
| `internal/mcp` | MCP server exposing `capabilities`, `inspect_file`, `clean_file`, `inspect_text`, `clean_text` over both transports: JSON-RPC 2.0 over stdio (`antiaimark-mcp`) and Streamable HTTP (`/mcp` on the HTTP service, 2025-03-26 revision, with per-session state). Tool descriptions localize to the client's `initialize` locale. | `cleaning`, `i18n` |
| `internal/janitor` | Background auto-clean: scheduled eviction of expired downloads; when free disk space falls below a threshold (default 11%) it removes this service's stale `wm-*` temp dirs oldest-first, purging pending downloads as a last resort. Injectable free-space probe and hooks. | `i18n` |
| `cmd/*` | Thin binaries: the 9 CLIs, `antiaimark-server` (flags + `httpapi`), `antiaimark-mcp` (flags + stdio loop). | everything above |

## Which facade should my plugin use?

* **IDE / agent plugin (any MCP client)** → run `antiaimark-mcp` as a stdio
  server. Registration for Claude Code:

  ```bash
  claude mcp add antiaimark -- /abs/path/to/antiaimark-mcp
  ```

  Cursor / Windsurf / Cline `mcp.json` equivalent:

  ```json
  { "mcpServers": { "antiaimark": { "command": "/abs/path/to/antiaimark-mcp" } } }
  ```

  …or, with the HTTP service running, register it as a remote server (no
  binary path, and it works from any host that can reach the service):

  ```json
  { "mcpServers": { "antiaimark": { "type": "http", "url": "http://127.0.0.1:8765/mcp" } } }
  ```

  Both transports expose the identical tool set and localize descriptions
  from the client's `initialize` locale.

* **Web / desktop app** → embed the HTTP facade directly:

  ```go
  api := httpapi.New(httpapi.Options{Version: "1.0", APIKey: ""})
  mux := http.NewServeMux()
  mux.Handle("/", api.Handler())          // or mount per-route
  go http.ListenAndServe("127.0.0.1:8765", mux)
  ```

  …or shell out to the `antiaimark-server` binary. The web UI at `/` already
  does image/video drag-and-drop with upload + download in 5 languages.

* **Scripts / build tooling** → the CLIs (stable Python-compatible flags,
  JSON output, exit codes: 0 clean, 1 findings/residual, 2 bad input).

## Adding a language

1. Add a `messages_<tag>.go` file in `internal/i18n` with the full key set
   (copy `messages_en.go` and translate).
2. Register the tag in `Tags`/`Names` and the `Normalize` switch in `i18n.go`.
3. `TestCatalogCompleteness` and `TestPlaceholdersMatch` enforce that the new
   catalog has every key with matching fmt verbs — the language then works
   everywhere at once (CLIs, HTTP errors, web UI, MCP descriptions).

Language selection: `--lang` flag > `ANTIAIMARK_LANG` env > system locale
(`LANG`/`LC_ALL`, POSIX and Windows forms) > English. HTTP errors negotiate
`Accept-Language`; the web UI persists its own choice in `localStorage`;
MCP descriptions follow the client's `initialize` locale.

Machine-readable JSON reports stay English by design (findings must remain
greppable and cross-identical with the Python pipeline); only human-facing
wrapping text is localized.
