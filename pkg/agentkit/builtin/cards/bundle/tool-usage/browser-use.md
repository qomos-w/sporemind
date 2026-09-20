---
id: builtin:bundle:browser-use
type: bundle
title: Browser Use
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: mouse-pointer-click
  visual:
    icon: mouse-pointer-click
    accent: indigo
    color: "#4f46e5"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - browsermanager.use
    - open_global_browser
---

## Browser Use

Drive a web page through its structured DOM with `browsermanager.use`. This is the preferred tool for automating web pages that expose accessible elements: read the page, then click, type, scroll, and wait using stable element refs — no pixel coordinates needed.

> **Boundary with Computer Use**: prefer `browsermanager.use` for web pages with an accessible DOM tree. Fall back to `computeruse` only for canvas/WebGL surfaces, plugins, captchas, or when structured DOM operations fail. Use `open_global_browser` (also in this bundle) to open or navigate to a URL in the shared global browser tab before automating it.

### Workflow: observe → act → re-observe

Every `browsermanager.use` call targets a browser instance and carries a single `Action`. The closed loop is:

1. **Observe** (`Action: "observe"`) — extract the page state and an annotated element list. Each interactive element gets a stable `data-sporemind-ref` id (the `Id` field of a `BrowserElement`). Observation itself is read-only.
2. **Act** (`click` / `type` / `scroll` / `wait` / `navigate` / `press` / `select`) — reference the target by its element `Id` from the last observation. A successful action automatically re-observes so the response's `Observation` reflects the new page state.
3. Repeat until the task is done.

### Actions

- **observe** — Read-only. Returns a `BrowserPageObservation` with the URL, title, viewport, scroll position, and an `Elements` list (each with `Id`, `TagName`, `Selector`, `Text`, `Role`, `AriaLabel`, `Rect`, `IsVisible`, `IsEnabled`, `ActionHint`, etc.). Use the element `Id` to target subsequent actions.
- **click** — Click an element. Pass `ElementId` (preferred, from observation) or `ElementSelector`. Alternatively pass `ClickX`/`ClickY` to click at a viewport coordinate.
- **type** — Enter text into an input/textarea/contenteditable element. Pass `ElementId`, `Text`, and optionally `Append` (keep existing text) and `Submit` (submit the enclosing form or press Enter).
- **scroll** — Scroll by a delta. Pass `ScrollX`/`ScrollY` (pixels), or scope it to a scrollable element with `ElementId`/`ScrollTarget`.
- **wait** — Pause for `WaitMs` milliseconds (async on the page, returns when the wait completes). Useful for animations, lazy-loaded content, or navigation transitions.
- **navigate** — Navigate to a URL or move in history. Pass `Url` (http/https/file only — the Go side rejects other schemes) and optionally `NavigateMode` (`"navigate"` default, `"back"`, `"forward"`, `"reload"`). Navigation changes the page; the auto re-observe captures the new state.
- **press** — Dispatch a key press (keydown/keypress/keyup) on the target element (by `ElementId`) or `document.activeElement`. Pass `Key` (e.g. `"Enter"`, `"Tab"`, `"Escape"`).
- **select** — Choose an option in a `<select>` element. Pass `ElementId` (must target a `<select>`) and `Text` (the option value to select). Dispatches `input` and `change` events.

### How to use `browsermanager.use`

First observe to get element refs:

```json
{"Action": "observe", "InstanceId": "global"}
```

Then act on an element by its `Id` (e.g. `e-3`):

```json
{"Action": "click", "InstanceId": "global", "ElementId": "e-3"}
```

Type into a field and submit:

```json
{"Action": "type", "InstanceId": "global", "ElementId": "e-7", "Text": "sporemind", "Submit": true}
```

### Instance routing

- `InstanceId` selects the target browser instance. Omit it to default to `"global"` (the shared instance).
- An instance ID or its human-friendly `Name` are both accepted.

### Reading the element list

`observe` returns interactive elements only — links, buttons, inputs, selects, textareas, and contenteditable regions. Each element's `ActionHint` suggests the natural action (`navigate`, `click`, `type`). Prefer elements with high `ActionConfidence` and `IsVisible: true`.

### Effect classification

`observe` is read-only, but `browsermanager.use` as a whole is classified as an **irreversible** effect because `click`/`type`/`scroll`/`navigate`/`press`/`select` mutate page state. Approval is decided per call by the turn-engine permission mode and applies to every action, including `observe`.

### Safety rules

1. **Observe before acting** — always obtain a fresh element list before clicking or typing, unless continuing from a known state.
2. **Target by element ref, not guesswork** — use the `ElementId` from the last observation. Avoid blind coordinate clicks.
3. **Verify after acting** — the response includes a re-observed `Observation`; check it to confirm the action had the intended effect before proceeding.
4. **Do not loop forever** — if an element is not found or an action fails, re-observe once; if it still fails, stop and ask for direction.
