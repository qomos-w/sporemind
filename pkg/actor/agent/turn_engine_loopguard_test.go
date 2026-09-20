package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestLoopGuardNormalize(t *testing.T) {
	if got := loopGuardNormalize("  Hello   WORLD \n\t Foo  "); got != "hello world foo" {
		t.Fatalf("loopGuardNormalize = %q", got)
	}
	// CJK preserved, full-width space collapsed.
	if got := loopGuardNormalize("检查　文件\n内容"); got != "检查 文件 内容" {
		t.Fatalf("loopGuardNormalize(CJK) = %q", got)
	}
}

func TestSketchSimilarity(t *testing.T) {
	a := sketchLoopText(loopGuardNormalize(strings.Repeat("alpha beta gamma delta epsilon zeta eta theta", 8)))
	b := sketchLoopText(loopGuardNormalize(strings.Repeat("alpha beta gamma delta epsilon zeta eta theta", 8)))
	c := sketchLoopText(loopGuardNormalize(strings.Repeat("iota kappa lambda mu nu xi omicron pi rho sigma tau", 8)))
	if s := sketchSimilarity(a, b); s < 0.95 {
		t.Fatalf("identical texts similarity = %.2f, want >= 0.95", s)
	}
	if s := sketchSimilarity(a, c); s > 0.3 {
		t.Fatalf("disjoint texts similarity = %.2f, want <= 0.3", s)
	}
}

func TestFlateJointRatio(t *testing.T) {
	base := strings.Repeat("the quick brown fox jumps over the lazy dog while analyzing repository state. ", 20)
	if r := flateJointRatio(base, base); r > 0.6 {
		t.Fatalf("flateJointRatio(identical) = %.2f, want <= 0.6", r)
	}
	// Disjoint varied content must sit near 1.0 — the joint compression gains
	// nothing from cross-text redundancy.
	var a, b strings.Builder
	for i := 0; i < 60; i++ {
		a.WriteString(fmt.Sprintf("entry %d: module-%d reports status %s with latency %dms\n", i, i*7, "ready", 40+i))
		b.WriteString(fmt.Sprintf("record %d: service-%d consumed quota %d units across region %d\n", i, i*13, 900-i, i%5))
	}
	if r := flateJointRatio(a.String(), b.String()); r < 0.9 {
		t.Fatalf("flateJointRatio(disjoint varied) = %.2f, want >= 0.9", r)
	}
}

func TestDetectTextLoopVerbatim(t *testing.T) {
	text := loopGuardNormalize(strings.Repeat("读取配置文件失败，尝试重新解析并重试一次。", 12))
	ring := appendLoopGuardEntry(nil, text)
	matched, sim := detectTextLoop(text, ring)
	if !matched || sim < 0.95 {
		t.Fatalf("verbatim repeat: matched=%v sim=%.2f, want matched sim>=0.95", matched, sim)
	}

	// Case/whitespace reformatting of Latin text normalizes to the same
	// shingles and still counts as a repeat.
	latin := strings.Repeat("Analyze the failing test, read the assertion diff, and adjust the fixture. ", 8)
	ring2 := appendLoopGuardEntry(nil, loopGuardNormalize(latin))
	variant := loopGuardNormalize("  ANALYZE THE FAILING TEST,\nread the assertion diff,  AND ADJUST THE FIXTURE. " + latin)
	if matched, _ := detectTextLoop(variant, ring2); !matched {
		t.Fatal("case/whitespace variant should match")
	}
}

func TestDetectTextLoopMostlyIdentical(t *testing.T) {
	base := "We need to inspect the authentication module and verify the token refresh path handles concurrent requests. "
	for i := 0; i < 40; i++ {
		base += "The refresh routine acquires a lock, reads the cached expiry, and decides whether to rotate the key. "
	}
	head := strings.Repeat("Detail block one with unique content about logging and metrics. ", 10)
	tail := strings.Repeat("Detail block two with different content about caching and retries. ", 10)
	a := loopGuardNormalize(base + head)
	b := loopGuardNormalize(base + tail)
	ring := appendLoopGuardEntry(nil, a)
	matched, sim := detectTextLoop(b, ring)
	if !matched {
		t.Fatalf("mostly-identical texts should be detected (sim=%.2f)", sim)
	}
}

func TestDetectTextLoopTemplateOnlyNotDetected(t *testing.T) {
	// Two texts sharing a boilerplate header but with materially different
	// bodies must NOT trip the guard — this is the false-positive the flate
	// confirmation (and shingle threshold) exists to prevent.
	tmpl := "I'll analyze the repository structure, locate the relevant files, and review the current implementation before making changes. "
	a := loopGuardNormalize(tmpl + strings.Repeat("The parser module handles tokenization errors by retrying with a reduced context window. ", 12))
	b := loopGuardNormalize(tmpl + strings.Repeat("Deployment configuration lives in the manifest and is validated during the release pipeline. ", 12))
	ring := appendLoopGuardEntry(nil, a)
	matched, _ := detectTextLoop(b, ring)
	if matched {
		t.Fatal("template-sharing but distinct texts must not be detected")
	}
}

func TestLoopCorrectionText(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range loopCorrectionPool {
		if v == "" || seen[v] {
			t.Fatalf("empty or duplicate correction variant: %q", v)
		}
		seen[v] = true
	}
	if len(loopCorrectionPool) < 4 {
		t.Fatalf("correction pool too small: %d", len(loopCorrectionPool))
	}
	if loopCorrectionText(len(loopCorrectionPool)) != loopCorrectionPool[0] {
		t.Fatal("loopCorrectionText should wrap idx")
	}
}

func TestRunLoopGuardEscalation(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	para := strings.Repeat("the model keeps reading the same configuration file and proposing the identical fix each iteration. ", 3)

	// First sighting: recorded, no correction.
	if e.runLoopGuard(ctx, "turn-lg", para, "") {
		t.Fatal("first iteration must not fail the turn")
	}
	if n := len(e.pendingMessages); n != 0 {
		t.Fatalf("first iteration queued %d messages, want 0", n)
	}

	// Second sighting: soft correction queued, turn continues.
	if e.runLoopGuard(ctx, "turn-lg", para, "") {
		t.Fatal("second iteration must not fail the turn")
	}
	if n := len(e.pendingMessages); n != 1 {
		t.Fatalf("second iteration queued %d messages, want 1", n)
	}
	if e.loopState == LoopFailed {
		t.Fatal("turn failed too early")
	}

	// Third sighting: another soft correction.
	if e.runLoopGuard(ctx, "turn-lg", "", para) {
		t.Fatal("third iteration must not fail the turn yet")
	}
	if n := len(e.pendingMessages); n != 2 {
		t.Fatalf("third iteration queued %d messages, want 2", n)
	}

	// Fourth sighting: hard break.
	if !e.runLoopGuard(ctx, "turn-lg", para, para) {
		t.Fatal("fourth iteration (strike 3) must fail the turn")
	}
	if e.loopState != LoopFailed {
		t.Fatalf("loopState = %v, want LoopFailed", e.loopState)
	}
	if !strings.Contains(e.turnError, "Repetition loop guard") {
		t.Fatalf("turnError = %q, want repetition loop guard message", e.turnError)
	}
}

func TestRunLoopGuardStrikesReset(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	loop := strings.Repeat("the agent retries the same build command and observes the same compiler diagnostics. ", 3)
	fresh := strings.Repeat("compilation now succeeds, so the next step is running the integration suite for the parser package. ", 3)

	if e.runLoopGuard(ctx, "turn-lg", loop, "") {
		t.Fatal("first sighting must not fail")
	}
	if e.runLoopGuard(ctx, "turn-lg", loop, "") {
		t.Fatal("second sighting must not fail")
	}
	if e.loopGuardStrikes != 1 {
		t.Fatalf("strikes = %d, want 1", e.loopGuardStrikes)
	}
	// A materially different iteration resets the streak.
	if e.runLoopGuard(ctx, "turn-lg", fresh, "") {
		t.Fatal("fresh iteration must not fail")
	}
	if e.loopGuardStrikes != 0 {
		t.Fatalf("strikes after fresh iteration = %d, want 0", e.loopGuardStrikes)
	}
	// Falling back into the loop starts over at strike 1 (soft), not hard fail.
	if e.runLoopGuard(ctx, "turn-lg", loop, "") {
		t.Fatal("relapse after reset must be soft, not hard fail")
	}
	if e.loopState == LoopFailed {
		t.Fatal("turn must not fail after streak reset")
	}
}

func TestRunLoopGuardShortOutputSkipped(t *testing.T) {
	e, ctx := newLifecycleEngine(t)
	short := "ok, retrying."
	if e.runLoopGuard(ctx, "turn-lg", short, short) {
		t.Fatal("short output must never fail the turn")
	}
	if len(e.loopGuardRing) != 0 {
		t.Fatalf("short output recorded %d entries, want 0", len(e.loopGuardRing))
	}
	if e.runLoopGuard(ctx, "turn-lg", short, short) {
		t.Fatal("short output must never fail the turn (2nd)")
	}
	if e.loopState == LoopFailed || len(e.pendingMessages) != 0 {
		t.Fatal("short outputs must not trigger any guard action")
	}
}

func TestRunLoopGuardRingWindow(t *testing.T) {
	ring := appendLoopGuardEntry(nil, strings.Repeat("a sample iteration body long enough to sketch reliably. ", 4))
	for i := 0; i < loopGuardWindow+3; i++ {
		ring = appendLoopGuardEntry(ring, strings.Repeat("another distinct iteration body with different words entirely. ", 4))
	}
	if len(ring) != loopGuardWindow {
		t.Fatalf("ring len = %d, want capped at %d", len(ring), loopGuardWindow)
	}
}

// TestRunLoopGuardCorrectionVariants ensures the queued corrections actually
// vary: over many soft-strike pairs, more than one distinct correction text
// appears (guards against a degenerate constant correction prompt).
func TestRunLoopGuardCorrectionVariants(t *testing.T) {
	distinct := map[string]bool{}
	for range 40 {
		e, ctx := newLifecycleEngine(t)
		para := strings.Repeat("identical reasoning text repeated across iterations of this turn. ", 4)
		_ = e.runLoopGuard(ctx, "turn-lg", para, "")
		_ = e.runLoopGuard(ctx, "turn-lg", para, "")
		for _, m := range e.pendingMessages {
			if len(m.Content) == 1 && m.Content[0].Type == domain.ContentBlockText {
				distinct[m.Content[0].Text] = true
			}
		}
	}
	if len(distinct) < 2 {
		t.Fatalf("correction texts over 40 draws: %d distinct, want >= 2", len(distinct))
	}
}
