package glassinteract

import (
	"fmt"
	"sort"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Canvas dimensions for known devices. The host scene pipeline clamps boxes,
// so these are hints for prefab layout, not hard limits.
const (
	CanvasG2Width  = 576
	CanvasG2Height = 288
)

// Two-column layout constants.
const (
	// Left column holds HUD + auxiliary context. Narrow enough to maximize
	// the main content area, wide enough for short Chinese labels.
	colLeftX = 8
	colLeftW = 104

	// Main content column occupies the rest of the canvas.
	colRightX = 128
	colRightW = 440
)

// --- Primitive constructors (game-engine nodes) ---

func TextEl(id string, x, y, w, h int, text string) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:   id,
		Type: "text",
		Box:  gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Text: text,
	}
}

func BorderedTextEl(id string, x, y, w, h, border, radius int, text string) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:     id,
		Type:   "text",
		Box:    gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Text:   text,
		Border: int32(border),
		Radius: int32(radius),
	}
}

func ImageEl(id string, x, y, w, h int, data string) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:        id,
		Type:      "image",
		Box:       gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		ImageData: data,
	}
}

func RectEl(id string, x, y, w, h, border, radius int) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:     id,
		Type:   "rect",
		Box:    gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Border: int32(border),
		Radius: int32(radius),
	}
}

func ListEl(id string, x, y, w, h int, options []gen.GlassSceneOption, selectedIdx int) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:       id,
		Type:     "list",
		Box:      gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Options:  options,
		Role:     "selectable",
		Selected: selectedIdx == 0,
	}
}

func ButtonEl(id string, x, y, w, h int, label string) gen.GlassSceneElement {
	return gen.GlassSceneElement{
		ID:     id,
		Type:   "button",
		Box:    gen.GlassSceneBox{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)},
		Label:  label,
		Role:   "action",
		Border: 1,
	}
}

// --- Column layout helpers ---

func LeftColText(id, content string) gen.GlassSceneElement {
	return TextEl(id, colLeftX, 0, colLeftW, CanvasG2Height, content)
}

func RightColText(id, content string) gen.GlassSceneElement {
	return TextEl(id, colRightX, 0, colRightW, CanvasG2Height, content)
}

// --- Composite prefabs ---

// IdleFrame is the resting UI driven by real workspace state. Layout is
// intentionally minimal to fit glasses with small line budgets:
//   - top:    time (the device HUD bar has no clock, so time stays in-frame;
//     battery/connection are HUD-bar-only and deliberately not duplicated)
//   - middle: active agents (Title preferred, prefixed with ·)
//   - bottom: coordinator status
// The right column only appears when there is recent history; no title, no
// empty-state placeholder.
func IdleFrame(now time.Time, mon *agentMonitor) gen.GlassRenderFrame {
	if mon == nil {
		return FrameFromElements(0, []gen.GlassSceneElement{
			LeftColText("idle-left", now.Format("15:04")+"\n等待数据"),
		})
	}

	// Top: time only (battery/conn live in the device HUD bar).
	left := now.Format("15:04")

	// Middle: active agents on the second row / onward.
	active := mon.active()
	shown := active
	if len(shown) > maxActiveDisplay {
		shown = shown[:maxActiveDisplay]
	}
	for _, ag := range shown {
		name := agentName(ag.Title, ag.DisplayName, ag.ActorID)
		task := ""
		if ag.Runtime != nil && ag.Runtime.CurrentTaskSummary != "" {
			task = ": " + ag.Runtime.CurrentTaskSummary
		}
		left += "\n\u00b7 " + name + task
	}

	// Bottom: coordinator status.
	coordName := mon.coordinatorDisplayName()
	if mon.coordinatorIdle() {
		left += "\n" + coordName + " 空闲"
	} else {
		left += "\n" + coordName + " 工作中"
	}

	// Right column: history entries only, no title, omitted entirely when empty.
	history := mon.recentHistory()
	var elements []gen.GlassSceneElement
	elements = append(elements, LeftColText("idle-left", left))
	if len(history) > 0 {
		right := ""
		if len(history) > maxHistoryDisplay {
			history = history[:maxHistoryDisplay]
		}
		for _, h := range history {
			name := agentName(h.Title, h.DisplayName, h.ActorID)
			label := "完成"
			if h.Outcome == "failed" {
				label = "失败"
			} else if h.Outcome == "cancelled" {
				label = "取消"
			}
			if right != "" {
				right += "\n"
			}
			right += fmt.Sprintf("[%s] %s %s", label, name, formatTimeAgo(now, h.FinishedAt))
		}
		elements = append(elements, RightColText("idle-right", right))
	}

	return FrameFromElements(0, elements)
}

// agentName prefers Title, then DisplayName, then a short actor id.
func agentName(title, displayName, actorID string) string {
	if title != "" {
		return title
	}
	if displayName != "" {
		return displayName
	}
	return shortActorID(actorID)
}

func shortActorID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatTimeAgo(now, t time.Time) string {
	d := now.Sub(t)
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// NotificationFrame renders a passive information card. The left column shows
// the source and relative time; the right column shows the body text. No title
// banner, no borders — the column split provides enough visual structure.
// (HUD time/battery is device-drawn from glass.hud.update; the frame no
// longer embeds it.)
func NotificationFrame(body, source, timeAgo string) gen.GlassRenderFrame {
	left := ""
	if source != "" || timeAgo != "" {
		left = source
		if timeAgo != "" {
			left += "\n" + timeAgo
		}
	}
	return FrameFromElements(0, []gen.GlassSceneElement{
		LeftColText("notify-left", left),
		RightColText("notify-right", body),
	})
}

// AskUserFrame renders an interactive choice prompt. The left column carries
// a compact context label (HUD is device-drawn); the right column shows the
// question followed by selectable options. The selected option is marked with
// a leading
// \u25b6 (▶) in the rendered list; the device side may further highlight it.
func AskUserFrame(context, question string, options []string, selectedIdx int) gen.GlassRenderFrame {
	left := context
	opts := make([]gen.GlassSceneOption, len(options))
	for i, o := range options {
		opts[i] = gen.GlassSceneOption{ID: fmt.Sprintf("opt-%d", i), Text: o}
	}
	rightText := question
	if len(options) > 0 {
		rightText += "\n\n"
		for i, o := range options {
			if i == selectedIdx {
				rightText += "\u25b6 " + o + "\n"
			} else {
				rightText += "  " + o + "\n"
			}
		}
		rightText = strings.TrimSuffix(rightText, "\n")
	}
	return FrameFromElements(0, []gen.GlassSceneElement{
		LeftColText("ask-left", left),
		RightColText("ask-right", rightText),
	})
}

// TranscriptFrame renders live STT transcription in progress. The left column
// shows a listening indicator; the right column shows the partial text.
func TranscriptFrame(text string, remainingSeconds int) gen.GlassRenderFrame {
	left := fmt.Sprintf("聆听中\n%ds", remainingSeconds)
	return FrameFromElements(0, []gen.GlassSceneElement{
		LeftColText("transcript-left", left),
		RightColText("transcript-right", text),
	})
}

// HudBar renders the top status bar with a label and value. Kept for backward
// compatibility with callers that expect a classic single-bar HUD.
func HudBar(label, value string) []gen.GlassSceneElement {
	return []gen.GlassSceneElement{
		WithZ(RectEl("hud-bg", 0, 0, CanvasG2Width, 28, 1, 0), -1),
		WithZ(TextEl("hud-label", 10, 2, 200, 24, label), 0),
		WithZ(TextEl("hud-value", 220, 2, CanvasG2Width-230, 24, value), 0),
	}
}

// LogPanel renders a full-screen multi-line log. Useful for diagnostics.
func LogPanel(lines []string) []gen.GlassSceneElement {
	body := strings.Join(lines, "\n")
	return []gen.GlassSceneElement{
		TextEl("log-panel", 8, 40, CanvasG2Width-16, CanvasG2Height-48, body),
	}
}

// SplitView renders a 50/50 two-pane view. Kept for backward compatibility.
func SplitView(leftTitle, leftBody, rightTitle, rightBody string) []gen.GlassSceneElement {
	const colW = CanvasG2Width/2 - 4
	return []gen.GlassSceneElement{
		RectEl("sv-divider", CanvasG2Width/2-1, 8, 2, CanvasG2Height-16, 1, 0),
		TextEl("sv-left-title", 8, 10, colW-8, 28, leftTitle),
		TextEl("sv-left-body", 8, 42, colW-8, CanvasG2Height-50, leftBody),
		TextEl("sv-right-title", CanvasG2Width/2+4, 10, colW-8, 28, rightTitle),
		TextEl("sv-right-body", CanvasG2Width/2+4, 42, colW-8, CanvasG2Height-50, rightBody),
	}
}

// NotificationCard renders a bordered card with a title and body. Kept for
// callers that prefer the older card style.
func NotificationCard(title, body string) []gen.GlassSceneElement {
	const (
		cardX = 12
		cardY = 8
		cardW = 552
	)
	return []gen.GlassSceneElement{
		WithZ(RectEl("nc-border", cardX, cardY, cardW, 130, 1, 4), -1),
		WithZ(TextEl("nc-title", cardX+12, cardY+4, cardW-24, 32, title), 0),
		WithZ(TextEl("nc-body", cardX+12, cardY+40, cardW-24, 80, body), 0),
	}
}

// AskUser composes an interactive prompt with a scrollable option list.
// Deprecated: use AskUserFrame for the two-column layout.
func AskUser(prompt string, options []string) []gen.GlassSceneElement {
	const (
		cardX = 8
		cardY = 8
		cardW = CanvasG2Width - 16
	)
	sceneOpts := make([]gen.GlassSceneOption, len(options))
	for i, o := range options {
		sceneOpts[i] = gen.GlassSceneOption{ID: fmt.Sprintf("opt-%d", i), Text: o}
	}
	return []gen.GlassSceneElement{
		WithZ(RectEl("ask-border", cardX, cardY, cardW, CanvasG2Height-16, 1, 4), -2),
		WithZ(BorderedTextEl("ask-prompt", cardX+8, cardY+4, cardW-16, 36, 0, 0, prompt), -1),
		WithZ(ListEl("ask-list", cardX+8, cardY+44, cardW-16, CanvasG2Height-cardY-52, sceneOpts, 0), 0),
	}
}

// --- Scene assembly ---

// WithZ sets the z-order on an element. Lower = further back (drawn first),
// higher = in front (drawn last, overwrites). Default is 0.
func WithZ(el gen.GlassSceneElement, z int) gen.GlassSceneElement {
	el.Z = int32(z)
	return el
}

// SortedByZ returns elements sorted ascending by Z (stable), so background
// elements render before foreground. Equal-Z elements keep their original
// relative order.
func SortedByZ(elements []gen.GlassSceneElement) []gen.GlassSceneElement {
	sorted := make([]gen.GlassSceneElement, len(elements))
	copy(sorted, elements)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Z < sorted[j].Z
	})
	return sorted
}

func NewScene(tick int64, elements []gen.GlassSceneElement) *gen.GlassScene {
	for i := range elements {
		elements[i].Visible = true
	}
	return &gen.GlassScene{Tick: tick, Elements: SortedByZ(elements)}
}

func NewSceneWithFocus(tick int64, elements []gen.GlassSceneElement, focusId string) *gen.GlassScene {
	for i := range elements {
		elements[i].Visible = true
	}
	return &gen.GlassScene{Tick: tick, Elements: elements, FocusID: focusId}
}

func FrameFromScene(scene *gen.GlassScene) gen.GlassRenderFrame {
	return gen.GlassRenderFrame{Scene: scene}
}

func FrameFromElements(tick int64, elements []gen.GlassSceneElement) gen.GlassRenderFrame {
	return gen.GlassRenderFrame{Scene: NewScene(tick, elements)}
}

func FrameFromText(text string) gen.GlassRenderFrame {
	return gen.GlassRenderFrame{Text: text}
}
