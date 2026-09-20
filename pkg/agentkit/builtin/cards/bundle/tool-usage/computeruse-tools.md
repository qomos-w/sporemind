---
id: builtin:bundle:computeruse-tools
type: bundle
title: Computer Use Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: monitor
  visual:
    icon: monitor
    accent: cyan
    color: "#0891b2"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - computeruse.screenshot
    - computeruse.capabilities
    - computeruse.list_windows
    - computeruse.list_displays
    - computeruse.list_processes
    - computeruse.get_window_info
    - computeruse.cursor_position
    - computeruse.list_elements
    - computeruse.ocr
    - computeruse.setup_ocr
    - computeruse.interact
    - computeruse.stream
    - computeruse.clipboard_get
    - computeruse.clipboard_set
    - computeruse.clipboard_get_rich
    - computeruse.clipboard_set_rich
---

## Computer Use Tools

Control the host desktop through screenshots, accessibility trees, OCR, and mouse/keyboard automation. Use these tools when the task involves native GUI applications, desktop browsers, canvas elements, captchas, or anything that cannot be reached through structured DOM/browser APIs.

> **Boundary with browser use**: prefer `browsermanager.use` / `browser_*` for pure web pages with accessible DOM. Fallback to `computeruse` when the target is a plugin, a canvas/WebGL surface, a captcha, or when structured DOM operations fail.

### Observational tools (read-only)

- **capabilities** — Report the platform capability matrix: which `feature.*`, `action.*`, and `ocr.*` entries are supported on this host, plus the policy `Scope` of every gated callable. **Call this first** on a new host so you know which actions will fail before you try them (e.g. `action.middle_click` on macOS, `feature.element_tree` off Windows, `feature.wayland` on Linux).
- **screenshot** — Capture the screen, a region, a window, or a window-region. Returns compact metadata (format, dimensions, cursor, display name); the image itself arrives in the immediately following message as an attached image you can see (vision models) or as a one-shot textual recognition (text-only models). Use `annotate=true` to overlay a red grid so you can refer to regions by labels like `A1` or `B3` instead of raw pixels.
- **list_windows / get_window_info** — Enumerate top-level windows and query their bounds/title/process.
- **list_displays** — Enumerate monitors with bounds and scale factors.
- **list_processes** — Enumerate running processes.
- **cursor_position** — Get the current mouse cursor coordinates.
- **list_elements** — Return the UIA/accessibility tree for a window (Windows only; macOS/Linux return a limited/cursor-based list). Use element refs with `interact` `element_*` actions.
- **ocr** — Run OCR on a screen region and return text plus per-line bounding boxes.
- **setup_ocr** — Download or verify PaddleOCR model files and the ONNX Runtime library. Automatically fetches from the go-ocr Hugging Face repo on first use. Optional: pass `Dir` to choose the install location and `Force=true` to re-download.

### Interaction tools (physical control, requires user confirmation)

- **interact** — Execute one or more mouse/keyboard/window/element actions. This is the main way to drive the desktop.
- **stream** — Start a low-bandwidth tile-diff screen stream; useful when you need continuous visual feedback. Read-only.
- **clipboard_get / clipboard_set / clipboard_get_rich / clipboard_set_rich** — Read or write the system clipboard, including files/images/HTML on Windows.

### Coordinate system

All coordinates are in **physical screen pixels** relative to the top-left of the primary virtual desktop `(0,0)`. When multiple displays exist, `list_displays` gives each monitor's bounds; add those offsets to target the correct screen.

### How to use `computeruse.screenshot`

Prefer the smallest region that contains the information you need to save tokens and avoid leaking unrelated screen content.

Typical workflow:

1. Call `list_windows` to find the target window ID.
2. Call `screenshot` with `mode=window` and the `window_id`.
3. If you need to click something, either:
   - call `screenshot` again with `annotate=true` and refer to the cell label (`grid_coord`), or
   - call `list_elements` to get a stable element reference and use `interact` `element_click`.

Useful fields:

- `mode`: `full`, `region`, `window`, `window_region`, `delta`
- `region`: `{x, y, width, height}` for `region` / `window_region`
- `window_id`: target a specific top-level window
- `annotate`: overlay the 100px red grid
- `format`: `jpeg` (default, smaller) or `png`
- `quality`: 0-100 for jpeg
- `scale`: 0.1-1.0 to downsample
- `grayscale`: true to remove color
- `include_cursor`: true to include the mouse cursor in the shot

`delta` mode compares against the previous capture and returns `unchanged=true` when pixels match, saving tokens during waits.

### How to use `computeruse.interact`

`interact` takes a list of `actions`. Each action has an `action` string and a `target`. Target resolution priority is:

1. `grid_coord` (e.g. `"B3"`) — center of the annotated cell.
2. `element` (element reference from `list_elements`) — exact accessibility element.
3. `window_id` — center of the window.
4. `x`/`y` — explicit pixel coordinates.

Actions:

- `click`, `double_click`, `right_click`, `middle_click`
- `move` — move cursor without clicking
- `scroll` — positive `amount` scrolls down/right; negative up/left
- `type` — type literal text
- `key`, `key_down`, `key_up` — press a named key (`enter`, `tab`, `escape`, `ctrl`, `alt`, etc.); use `key_down`/`key_up` for combos
- `copy`, `paste` — system clipboard shortcuts
- `drag` — drag from current cursor to target
- `wait` — pause up to 30000ms, can be cancelled by context
- `focus_window`, `window_state`, `window_move`, `window_close`
- `element_click`, `element_focus`, `element_set_value` — accessibility-element actions (best on Windows)

For text input, prefer `type` with a literal string. For keyboard shortcuts, use `key_down`/`key_up` combos, e.g.:

```json
[
  {"action": "key_down", "target": {"key": "ctrl"}},
  {"action": "key_down", "target": {"key": "c"}},
  {"action": "key_up", "target": {"key": "c"}},
  {"action": "key_up", "target": {"key": "ctrl"}}
]
```

### Platform capabilities

The static summary below is a rough guide; **the authoritative source is `computeruse.capabilities`**, which reports the live matrix (features, per-action support, OCR engines) for the host you are running on.

- **Windows**: full feature set — GDI screenshots, `SendInput`, UIA element tree, rich clipboard, element refs.
- **macOS**: screenshots via `screencapture`, input via `cliclick/osascript`; no native UIA, so `element_*` actions may fall back to coordinate clicking. No middle-click or right-drag.
- **Linux (X11)**: screenshots via ImageMagick `import`, input via `xdotool`; no UIA, so `element_*` actions fall back. Wayland is not supported.

On macOS/Linux, rely more on `annotate=true` + `grid_coord`, OCR, and explicit `(x,y)` targets.

### Safety rules

1. **Prefer observation first** — take a screenshot or list elements before interacting, unless you are continuing from a known state.
2. **Minimize scope** — target a window or region, not the full screen, to avoid accidental clicks.
3. **Avoid sensitive data** — screenshots and clipboard may contain credentials, tokens, or personal messages. Do not include them in responses to the user unless necessary, and never log base64 images to public channels.
4. **Confirm irreversible actions** — `interact` controls the real mouse/keyboard; destructive actions (closing windows, typing into production forms, pressing enter on submits) should be taken only when the user has explicitly enabled this bundle.
5. **Do not loop forever** — if a target is not found after one retry + screenshot, stop and ask for direction.
