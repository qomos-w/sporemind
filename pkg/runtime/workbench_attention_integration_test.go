package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/workbench"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// TestWorkbenchAttentionProjection boots a minimal app containing only the
// workbench actor and exercises the full frontend-facing surface: the attention
// snapshot, the layout projection (the gospore.projection.get path
// getProjection/watchSnapshot ride), user promotion, and the lane-routed event
// ingress (workbench.ingest_step → terminal summon).
func TestWorkbenchAttentionProjection(t *testing.T) {
	// Isolate the data dir: the workbench actor persists card scores, and the
	// default dir lives next to the test binary — shared by every execution
	// of this binary in-process. Without isolation a rerun (-count=2) loads
	// the previous run's promoted scores and the promote assertion fails.
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		Children: []runtime.ChildSpec{
			{Name: "workbench", Factory: workbench.NewActor(), RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()
	select {
	case <-time.After(500 * time.Millisecond):
	}

	app := h.App()
	ref, ok := app.LookupService("workbench")
	if !ok {
		t.Fatal("workbench service not found")
	}

	// 1. Initial snapshot lists the retired terminal card.
	initial := decodeSnapshot(t, invoke(t, ref, "workbench.snapshot", gen.WorkbenchSnapshotReq{IncludeHidden: true}))
	if cardByID(initial, "terminal") == nil {
		t.Fatalf("expected the terminal card in the initial snapshot: %+v", initial)
	}
	if c := cardByID(initial, "terminal"); !c.Hidden || c.Slot != "hidden" {
		t.Fatalf("terminal should start hidden, got %+v", c)
	}

	// 2. Declaring a card surfaces it in the main slot.
	invoke(t, ref, "workbench.upsert_card", gen.WorkbenchUpsertCardReq{ID: "app:x", Kind: "app", Title: "X"})
	afterUpsert := decodeSnapshot(t, invoke(t, ref, "workbench.snapshot", gen.WorkbenchSnapshotReq{}))
	card := cardByID(afterUpsert, "app:x")
	if card == nil {
		t.Fatalf("app:x not projected: %+v", afterUpsert)
	}
	if card.Slot != "main" {
		t.Fatalf("app:x slot = %q, want main", card.Slot)
	}

	// 3. User promotion raises the card above the max + hysteresis.
	before := cardByID(decodeSnapshot(t, invoke(t, ref, "workbench.snapshot", gen.WorkbenchSnapshotReq{})), "app:x").Score
	promoted := decodeSnapshot(t, invoke(t, ref, "workbench.promote", gen.WorkbenchCardRefReq{ID: "app:x"}))
	if got := cardByID(promoted, "app:x").Score; got < before+workbench.Hysteresis+1 {
		t.Fatalf("promoted score = %v, want >= %v", got, before+workbench.Hysteresis+1)
	}

	// 4. Step ingress (lane-routed) creates the agent card and summons the
	//    terminal card on the command keyword. The ingress callable returns no
	//    value, so it is fired fire-and-forget; the lane applies it asynchronously.
	voidInvoke(t, ref, "workbench.ingest_step", gen.WorkbenchStepIngestReq{
		EmitterID: "agent-1",
		Step:      gen.StepEvent{Block: &gen.ContentBlock{Type: "tool_use", ToolName: "Bash", Input: "npm run build"}},
	})
	afterStep := waitForCard(t, ref, "agent:agent-1")
	if c := cardByID(afterStep, "terminal"); c == nil || c.Hidden {
		t.Fatalf("terminal should be summoned by the step keyword, got %+v", c)
	}

	// 5. The projection component path the frontend getProjection/
	//    watchProjection helpers use resolves and returns the snapshot.
	raw := invoke(t, app.Self(), "gospore.projection.get", map[string]any{
		"actorPath": "workbench",
		"component": "Snapshot",
		"schemaId":  gen.WorkbenchSnapshotSchemaID,
	})
	var proj gen.WorkbenchSnapshot
	if err := json.Unmarshal(raw, &proj); err != nil {
		t.Fatalf("decode projection: %v (raw=%s)", err, raw)
	}
	if len(proj.Cards) == 0 || proj.Generation == 0 {
		t.Fatalf("projection not published: %+v", proj)
	}
	// The projection wire keys are the declared public json/schema field names
	// (`Cards`, `Id` — projection wireName); the frontend tolerates both shapes
	// (wireShape.ts pascalize). Pin the casing so a silent flip (e.g. a
	// lowerFirst(GoName) regression emitting `iD`) cannot desync the board.
	if !strings.Contains(string(raw), `"Cards"`) || !strings.Contains(string(raw), `"Id"`) {
		t.Fatalf("projection wire keys are not the declared public names: %s", raw)
	}
}

func invoke(t *testing.T, target ref.Ref, callID string, payload any) []byte {
	t.Helper()
	call := target.Invoke(context.Background(), callID, payload)
	if call == nil {
		t.Fatalf("%s: invoke returned nil", callID)
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		t.Fatalf("%s: %v", callID, err)
	}
	return raw
}

// voidInvoke fires a no-return callable and does not wait for a response.
func voidInvoke(t *testing.T, target ref.Ref, callID string, payload any) {
	t.Helper()
	call := target.Invoke(context.Background(), callID, payload)
	if call == nil {
		t.Fatalf("%s: invoke returned nil", callID)
	}
	_ = call.Close()
}

// waitForCard polls the snapshot until the given card is projected (the
// lane-routed ingress applies asynchronously).
func waitForCard(t *testing.T, target ref.Ref, id string) gen.WorkbenchSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap := decodeSnapshot(t, invoke(t, target, "workbench.snapshot", gen.WorkbenchSnapshotReq{IncludeHidden: true}))
		if cardByID(snap, id) != nil {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("card %q not projected within deadline: %+v", id, snap)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func decodeSnapshot(t *testing.T, raw []byte) gen.WorkbenchSnapshot {
	t.Helper()
	var snap gen.WorkbenchSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot: %v (raw=%s)", err, raw)
	}
	return snap
}

func cardByID(snap gen.WorkbenchSnapshot, id string) *gen.WorkbenchCardState {
	for i := range snap.Cards {
		if snap.Cards[i].ID == id {
			return &snap.Cards[i]
		}
	}
	return nil
}
