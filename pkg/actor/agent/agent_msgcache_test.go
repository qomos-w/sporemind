package agent

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// fullRebuildStepMessages is the pre-msgCache reference implementation: every
// step converted with stepToChatMessage and assigned its flat Idx, exactly
// what takeSnapshot did before the cache. Equivalence tests compare the
// cached/lazy path against this on every publish.
func fullRebuildStepMessages(steps []domain.Step) []domain.ChatMessage {
	msgs := make([]domain.ChatMessage, 0, len(steps))
	for _, step := range steps {
		msg := stepToChatMessage(step)
		msg.Idx = int32(len(msgs))
		msgs = append(msgs, msg)
	}
	return msgs
}

func assertMsgCacheInvariant(t *testing.T, a *Actor) {
	t.Helper()
	if len(a.msgCache) != len(a.steps) {
		t.Fatalf("msgCache/steps length invariant broken: len(msgCache)=%d len(steps)=%d", len(a.msgCache), len(a.steps))
	}
	if len(a.msgDirty) != len(a.steps) {
		t.Fatalf("msgDirty/steps length invariant broken: len(msgDirty)=%d len(steps)=%d", len(a.msgDirty), len(a.steps))
	}
}

func assertSnapshotMatchesFullRebuild(t *testing.T, a *Actor) {
	t.Helper()
	snap := a.snapshot.Load()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	want := fullRebuildStepMessages(a.steps)
	if len(snap.messages) != len(want) {
		t.Fatalf("snapshot.messages length = %d, want %d", len(snap.messages), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(snap.messages[i], want[i]) {
			t.Fatalf("snapshot.messages[%d] mismatch\n got: %+v\nwant: %+v", i, snap.messages[i], want[i])
		}
	}
}

// TestMsgCache_DeterministicEventSequence walks every applyStepEvent branch
// and the direct write helpers, checking the length invariant after each
// mutation and full-rebuild equivalence after each publish.
func TestMsgCache_DeterministicEventSequence(t *testing.T) {
	a := &Actor{}
	assertMsgCacheInvariant(t, a)

	// Direct appends (user step / system step paths use appendStep).
	a.appendStep(domain.Step{
		ID: "u1", Role: "user", Type: "text", Closed: true, Seq: 1,
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}},
	})
	a.appendStep(domain.Step{ID: "sys1", Role: "system", Type: "text", Closed: true, Seq: 2})
	assertMsgCacheInvariant(t, a)

	// Assistant text step via events: open with block, delta, progress, close with usage.
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s1", TurnID: "t1", StepType: "text", Role: "assistant", Block: &domain.ContentBlock{Type: domain.ContentBlockText, Text: ""}})
	a.applyStepEvent(domain.StepEvent{Kind: "block.delta", StepID: "s1", BlockIndex: 0, Delta: "world"})
	a.applyStepEvent(domain.StepEvent{Kind: "step.execution_progress", StepID: "s1", Progress: `{"phase":"working"}`})
	a.applyStepEvent(domain.StepEvent{Kind: "step.closed", StepID: "s1", Usage: &domain.UsageData{InputTokens: 10, OutputTokens: 5}})
	assertMsgCacheInvariant(t, a)

	// Duplicate step.opened must dedup (no length change).
	before := len(a.steps)
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s1", TurnID: "t1", StepType: "text", Role: "assistant"})
	if len(a.steps) != before {
		t.Fatalf("duplicate step.opened appended: len %d -> %d", before, len(a.steps))
	}
	assertMsgCacheInvariant(t, a)

	// tool_use + tool_result, then a second tool_result with the same ToolUseID
	// (fork_child placeholder replacement path).
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s2", TurnID: "t1", StepType: "tool_call", Role: "assistant", Block: &domain.ContentBlock{Type: domain.ContentBlockToolUse, ToolUseID: "tu1", ToolName: "fork_child"}})
	a.applyStepEvent(domain.StepEvent{Kind: "block.appended", StepID: "s2", Block: &domain.ContentBlock{Type: domain.ContentBlockToolResult, ToolUseID: "tu1", Text: "placeholder"}})
	a.applyStepEvent(domain.StepEvent{Kind: "block.appended", StepID: "s2", Block: &domain.ContentBlock{Type: domain.ContentBlockToolResult, ToolUseID: "tu1", Text: "actual result"}})
	if got := a.steps[3].Content; len(got) != 2 || got[1].Text != "actual result" {
		t.Fatalf("fork tool_result replacement wrong: %+v", got)
	}
	assertMsgCacheInvariant(t, a)

	// Reasoning step: delta accumulates into ReasoningContent.
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s3", TurnID: "t1", StepType: "reasoning", Role: "assistant"})
	a.applyStepEvent(domain.StepEvent{Kind: "block.delta", StepID: "s3", Delta: "thinking"})
	a.applyStepEvent(domain.StepEvent{Kind: "step.closed", StepID: "s3"})
	assertMsgCacheInvariant(t, a)

	// step.reset clears text.
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s4", TurnID: "t1", StepType: "text", Role: "assistant", Block: &domain.ContentBlock{Type: domain.ContentBlockText, Text: "draft"}})
	a.applyStepEvent(domain.StepEvent{Kind: "step.reset", StepID: "s4"})
	if got := a.steps[5].Content[0].Text; got != "" {
		t.Fatalf("step.reset left text %q", got)
	}
	assertMsgCacheInvariant(t, a)

	// step.error.
	a.applyStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s5", TurnID: "t1", StepType: "text", Role: "assistant"})
	a.applyStepEvent(domain.StepEvent{Kind: "step.error", StepID: "s5", Error: "boom"})
	assertMsgCacheInvariant(t, a)

	// Interaction requested (with task content) then resolved (appends task block).
	a.applyStepEvent(domain.StepEvent{Kind: "step.interaction_requested", StepID: "s6", TurnID: "t1", InteractionType: "ask_user", RequestID: "r1", Task: map[string]any{"q": "ok?"}})
	a.applyStepEvent(domain.StepEvent{Kind: "step.interaction_resolved", StepID: "s6", Task: map[string]any{"a": "yes"}})
	assertMsgCacheInvariant(t, a)

	// Publish #1: everything so far must match a full rebuild.
	a.takeSnapshot()
	assertSnapshotMatchesFullRebuild(t, a)

	// Second publish with no mutations in between: cache must be clean and
	// still equivalent.
	a.takeSnapshot()
	assertSnapshotMatchesFullRebuild(t, a)

	// In-place mutations with touchStep (the agent_chat Meta paths and the
	// startTurnWithName TurnID re-anchor follow this pattern).
	a.steps[0].Meta = "goal_submit"
	a.touchStep(0)
	for i := range a.steps {
		if a.steps[i].Role == "system" {
			a.steps[i].TurnID = "t1"
			a.touchStep(i)
		}
	}
	assertMsgCacheInvariant(t, a)
	a.takeSnapshot()
	assertSnapshotMatchesFullRebuild(t, a)

	// clearSession path: steps nil + wholesale cache rebuild.
	a.steps = nil
	a.rebuildMsgCache()
	assertMsgCacheInvariant(t, a)
	a.takeSnapshot()
	assertSnapshotMatchesFullRebuild(t, a)
	if got := a.snapshot.Load().messages; len(got) != 0 {
		t.Fatalf("messages after clear = %d, want 0", len(got))
	}
}

func randText(rng *rand.Rand, n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz \n"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(letters[rng.Intn(len(letters))])
	}
	return sb.String()
}

// TestMsgCache_EquivalenceFuzz drives pseudo-random event sequences through
// the production mutation paths and, on every publish, requires
// snapshot.messages to equal the full stepToChatMessage rebuild field-by-field.
// It also verifies that previously published snapshots are unaffected by
// later mutations (published messages must be immutable).
func TestMsgCache_EquivalenceFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20260907))

	for trial := 0; trial < 20; trial++ {
		a := &Actor{}
		var openIDs []string
		type published struct {
			snap *sessionSnapshotData
			want []domain.ChatMessage
		}
		var pubs []published

		publish := func() {
			t.Helper()
			a.takeSnapshot()
			want := fullRebuildStepMessages(a.steps)
			snap := a.snapshot.Load()
			if !reflect.DeepEqual(snap.messages, want) {
				for i := range want {
					if i >= len(snap.messages) || !reflect.DeepEqual(snap.messages[i], want[i]) {
						t.Fatalf("trial %d: snapshot.messages[%d] mismatch\n got: %+v\nwant: %+v", trial, i, snap.messages[i], want[i])
					}
				}
				t.Fatalf("trial %d: snapshot.messages length %d, want %d", trial, len(snap.messages), len(want))
			}
			pubs = append(pubs, published{snap, want})
			if len(pubs) > 3 {
				pubs = pubs[1:]
			}
		}

		ops := 120 + rng.Intn(120)
		for op := 0; op < ops; op++ {
			switch rng.Intn(13) {
			case 0, 1:
				id := fmt.Sprintf("t%d-s%d", trial, op)
				typ := []string{"text", "reasoning", "tool_call"}[rng.Intn(3)]
				ev := domain.StepEvent{Kind: "step.opened", StepID: id, TurnID: fmt.Sprintf("t%d", trial), StepType: typ, Role: "assistant"}
				if typ != "reasoning" && rng.Intn(2) == 0 {
					ev.Block = &domain.ContentBlock{Type: domain.ContentBlockText, Text: randText(rng, 16)}
				}
				a.applyStepEvent(ev)
				openIDs = append(openIDs, id)
			case 2:
				if len(openIDs) == 0 {
					continue
				}
				id := openIDs[rng.Intn(len(openIDs))]
				var blk domain.ContentBlock
				if rng.Intn(3) == 0 {
					// Small ToolUseID pool so the fork_child replacement path fires.
					blk = domain.ContentBlock{Type: domain.ContentBlockToolResult, ToolUseID: fmt.Sprintf("tu%d", rng.Intn(4)), Text: randText(rng, 24), IsError: rng.Intn(2) == 0}
				} else {
					blk = domain.ContentBlock{Type: domain.ContentBlockText, Text: randText(rng, 24)}
				}
				a.applyStepEvent(domain.StepEvent{Kind: "block.appended", StepID: id, Block: &blk})
			case 3:
				if len(openIDs) == 0 {
					continue
				}
				a.applyStepEvent(domain.StepEvent{Kind: "block.delta", StepID: openIDs[rng.Intn(len(openIDs))], BlockIndex: int32(rng.Intn(3)), Delta: randText(rng, 12)})
			case 4:
				if len(openIDs) == 0 {
					continue
				}
				a.applyStepEvent(domain.StepEvent{Kind: "step.reset", StepID: openIDs[rng.Intn(len(openIDs))]})
			case 5:
				if len(openIDs) == 0 {
					continue
				}
				a.applyStepEvent(domain.StepEvent{Kind: "step.execution_progress", StepID: openIDs[rng.Intn(len(openIDs))], Progress: fmt.Sprintf(`{"phase":"%d"}`, rng.Intn(9))})
			case 6:
				if len(openIDs) == 0 {
					continue
				}
				a.applyStepEvent(domain.StepEvent{Kind: "step.closed", StepID: openIDs[rng.Intn(len(openIDs))], Usage: &domain.UsageData{InputTokens: int64(rng.Intn(1000)), OutputTokens: int64(rng.Intn(500))}})
			case 7:
				if len(openIDs) == 0 {
					continue
				}
				a.applyStepEvent(domain.StepEvent{Kind: "step.error", StepID: openIDs[rng.Intn(len(openIDs))], Error: randText(rng, 8)})
			case 8:
				id := fmt.Sprintf("t%d-i%d", trial, op)
				a.applyStepEvent(domain.StepEvent{Kind: "step.interaction_requested", StepID: id, TurnID: fmt.Sprintf("t%d", trial), InteractionType: "ask_user", RequestID: fmt.Sprintf("r%d", op), Task: map[string]any{"q": randText(rng, 6)}})
				openIDs = append(openIDs, id)
				if rng.Intn(2) == 0 {
					a.applyStepEvent(domain.StepEvent{Kind: "step.interaction_resolved", StepID: id, Task: map[string]any{"a": randText(rng, 6)}})
				}
			case 9:
				// In-place mutation + touchStep, mirroring the agent_chat Meta
				// tagging and turn re-anchor paths.
				if len(a.steps) == 0 {
					continue
				}
				i := rng.Intn(len(a.steps))
				a.steps[i].Meta = "fuzz"
				a.touchStep(i)
			case 10:
				// Direct append (system/goal/cancel-marker paths).
				a.appendStep(domain.Step{
					ID: fmt.Sprintf("t%d-m%d", trial, op), Role: "system", Type: "text", Closed: true, Seq: int64(op),
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: randText(rng, 20)}},
				})
			case 11, 12:
				publish()
			}
			assertMsgCacheInvariant(t, a)
		}
		publish()

		// Immutability: snapshots published earlier in this trial must still
		// match their captured expectation even after later mutations.
		for i, p := range pubs {
			if !reflect.DeepEqual(p.snap.messages, p.want) {
				t.Fatalf("trial %d: published snapshot %d changed after later mutations", trial, i)
			}
		}
	}
}

// seedLargeActor builds an actor with n closed steps of ~contentBytes text
// each, mimicking a large session history.
func seedLargeActor(n, contentBytes int) *Actor {
	a := &Actor{}
	body := strings.Repeat("x", contentBytes)
	for i := 0; i < n; i++ {
		a.appendStep(domain.Step{
			ID: fmt.Sprintf("s%d", i), Role: "assistant", Type: "text", Closed: true, Seq: int64(i + 1),
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: body}},
		})
	}
	return a
}

// BenchmarkTakeSnapshot_MsgCache_500x8KB measures the P1 path: one dirty step
// among 500 steps of 8KB content, as in a streaming flush.
func BenchmarkTakeSnapshot_MsgCache_500x8KB(b *testing.B) {
	a := seedLargeActor(500, 8*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.touchStep(len(a.steps) - 1)
		a.takeSnapshot()
	}
}

// BenchmarkTakeSnapshot_FullRebuildBaseline_500x8KB measures the pre-P1
// messages build (full stepToChatMessage walk) in the same binary for
// comparison. The remaining takeSnapshot work (steps/tasks copies etc.) is
// identical in both paths and excluded here.
func BenchmarkTakeSnapshot_FullRebuildBaseline_500x8KB(b *testing.B) {
	a := seedLargeActor(500, 8*1024)
	steps := a.steps
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		messages := make([]domain.ChatMessage, 0, len(steps))
		for _, step := range steps {
			msg := stepToChatMessage(step)
			msg.Idx = int32(len(messages))
			messages = append(messages, msg)
		}
		_ = messages
	}
}
