---
id: builtin:bundle:web-search
type: bundle
title: Web Search
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: search
  visual:
    icon: search
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - websearch.search
    - websearch.fetch
    - websearch.download
---

## Web Search

Search the web via the configured provider. Returns structured results (title, URL, snippet, source, publish date) suitable for LLM consumption.

### Callable

- **websearch.search** — Execute a web search. Parameters: `Query`, optional `MaxResults` (1-50, default 10), `TimeRange` (`oneDay`/`oneWeek`/`oneMonth`/`oneYear`/`noLimit`), `Engine` (provider-specific override). Returns `Results[]` with `Title`, `Url`, `Snippet`, `Source`, `Icon`, `PublishedDate`.
- **websearch.fetch** — Fetch a URL and extract visible text and metadata via headless HTTP GET. Parameters: `Url` (required), optional `MaxChars` (default 10000). Returns `Url`, `Title`, `Text`, `Meta` (with `Description`, `SiteName`, `Lang`), and `Truncated` (true when the response exceeded MaxChars or the raw body exceeded 2 MB).
- **websearch.download** — Download a file from a URL via plain HTTP GET and save it to disk. Parameters: `Url` (required), `SavePath` (required), optional `MaxBytes` (default 500MB). Returns `SavedPath`, `BytesDownloaded`, `ContentType`, and `Truncated` (true when the download was cut off at MaxBytes).

### Providers

| Provider | Key | Notes |
|----------|-----|-------|
| zhipu | Yes (reuses GLM API key) | 智谱 Web Search API. Query limited to 70 characters. Auto-resolves token from aimanager when a bigmodel.cn provider is configured. |
| deepseek | Yes (reuses DeepSeek API key) | DeepSeek web search via Anthropic Messages API + `web_search_20250305` server-side tool. Auto-resolves token from aimanager when an api.deepseek.com provider is configured. |

### Notes

- Provider configuration (API keys, active account) is managed in Settings → Web Search, not via tool callables.
- Results are returned as structured JSON, not raw HTML.
- `websearch.fetch` performs a plain HTTP GET (no browser, no JavaScript rendering). Pages that require client-side rendering to produce content will return empty or incomplete text.
- `websearch.download` is a browser-less HTTP file download (no browser, no JavaScript rendering); it saves the response body directly to disk.
