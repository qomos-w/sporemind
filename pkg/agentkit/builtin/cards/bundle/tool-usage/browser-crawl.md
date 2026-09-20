---
id: builtin:bundle:browser-crawl
type: bundle
title: Browser Crawl
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: spider
  visual:
    icon: spider
    accent: teal
    color: "#0d9488"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - crawl.start
    - crawl.status
    - crawl.results
    - crawl.cancel
    - crawl.handoff
---

## Browser Crawl

Run a resumable, rate-limited crawl from a `type: crawl` definition card. The crawl actor owns the task queue, frontier, and persistence — you only start the task, poll status, and pull results.

### Workflow: define card → start → poll → results

1. **Start** with `crawl.start` — two equivalent entry points:
   - **Card path** (workflow/persistent): `{Card: "<raw markdown>"}` — raw frontmatter+body of a `type: crawl` definition card.
   - **Inline path** (quick toolcall): `{Config: {Seeds: [...], MaxPages: 50, ...}}` — no card needed.
   - Both accept optional `Overrides` (highest-priority map merge) and can be combined: `{Card: <raw>, Config: {MaxPages: 42}}`.
   - Priority: defaults < Card < Config non-zero fields < Overrides.
   - Returns `TaskId`.
2. **Poll** `crawl.status` (`{"TaskId": "..."}`) → `running | awaiting_login | done | failed | cancelled` plus progress counters.
3. **Pull** `crawl.results` (`{"TaskId": "...", "Cursor": 0}`) — cursor-paginated `BrowserCrawlPageResult` items (`URL`, `Title`, `Links`, `Text`).
4. **Stop** with `crawl.cancel` (`{"TaskId": "..."}`) — cancels the engine; a task suspended on a login wall also releases its hidden window.

### Config fields (inline path)

| Field | Type | Notes |
|---|---|---|
| Seeds | `[]string` | Required (at least one URL) |
| MaxDepth | `int` | Default 0 (seed pages only) |
| MaxPages | `int` | Default 100 |
| SameDomain | `bool` | Restrict to seed domain |
| RateLimit | `string` | Duration string, e.g. `"500ms"` |
| Mode | `string` | `hidden_window` (default) \| `performance` |
| Profile | `string` | Browser profile directory |
| ExtractSchema | `string` | v1: `BrowserCrawlPageResult` only |
| ExtractSelectors | `map` | CSS selectors per field |

Note: `SameDomain: false` cannot override a card's `true` via Config (omitempty zero-value ambiguity); use `Overrides: {same_domain: false}` for that.

### Login walls

When the engine hits a login wall the task suspends as `awaiting_login` and releases the hidden window while keeping the profile. Call `crawl.handoff` to open a visible window on the same profile so the user can log in; the task resumes crawling once login completes. Do not work around this by hand-driving `browsermanager.use` against the crawl instance.

### Notes

- Tasks survive process restarts (persisted frontier + crawled set); a restarted task resumes without re-crawling visited URLs.
- v1 narrowing: `extract.schema` only accepts `BrowserCrawlPageResult`.
- `mode: hidden_window` requires the desktop runtime (WebView2 operator); headless returns `operator_not_bound`.
