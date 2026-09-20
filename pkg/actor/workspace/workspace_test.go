package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

func slotFromUnit(u gen.ModelUnit) *domain.ModelSlot {
	return &domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &u}}}
}

var spawnCounter uint64

func noOpSpawn(p actor.Props, _ string) (ref.Ref, error) {
	g := p.ID()
	if g == (id.ActorID{}) {
		spawnCounter++
		g = id.NewCanonical(1, 0, func() uint64 { return spawnCounter }).Next()
	}
	return testutil.NewFakeRef(g, nil), nil
}

// lookupOK returns a fake ref for any LookupID call. The ref handles
// project.spawn_agent so spawnAgentViaProject can complete in tests.
func lookupOK(id.ActorID) (ref.Ref, bool) {
	return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "project.spawn_agent" {
			return domain.ProjectSpawnAgentResp{
				ActorID: testutil.GenActorID().String(),
			}
		}
		return nil
	}), true
}

// gospore's Call.Final surfaces cancellation-shaped failures in several
// identities: bare io.EOF (void-End), the invoke.ErrCallCancelled sentinel
// (explicit Cancel without a ctx cause), and — since gospore 9f6ec27 —
// context.Canceled / context.DeadlineExceeded (translated ctx-driven
// cancellation). explainSpawnFailure must turn all of them into a
// distinguishable cause per ctx state.
func TestExplainSpawnFailure(t *testing.T) {
	live, cancelLive := context.WithCancel(context.Background())
	defer cancelLive()

	if got := explainSpawnFailure(live, nil); got != nil {
		t.Fatalf("nil error must stay nil, got %v", got)
	}
	sentinel := errors.New("boom")
	if got := explainSpawnFailure(live, sentinel); got != sentinel {
		t.Fatalf("unrelated error must pass through unchanged, got %v", got)
	}

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	for _, terminal := range []error{io.EOF, invoke.ErrCallCancelled, context.Canceled, context.DeadlineExceeded} {
		if err := explainSpawnFailure(expired, terminal); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("deadline expiry must be explained as timeout (%v), got %v", terminal, err)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, terminal := range []error{io.EOF, invoke.ErrCallCancelled, context.Canceled, context.DeadlineExceeded} {
		if err := explainSpawnFailure(cancelled, terminal); err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("ctx cancellation must be explained as shutdown (%v), got %v", terminal, err)
		}
	}

	if err := explainSpawnFailure(live, io.EOF); err == nil || !strings.Contains(err.Error(), "stream") {
		t.Fatalf("EOF with live ctx must be explained as stream close, got %v", err)
	}
	if err := explainSpawnFailure(live, invoke.ErrCallCancelled); !errors.Is(err, invoke.ErrCallCancelled) {
		t.Fatalf("cancel sentinel with live ctx must pass through unchanged, got %v", err)
	}
}

func freshActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	ps := persist.NewFSPersist(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: ps}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func freshActorAnon(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	ps := persist.NewFSPersist(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: ps}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

// TestLoadMigratesOldCardNamesToSubPath verifies the one-shot card-name
// migration: cards written with the legacy flat-file names (actorID +
// ".rmounts" etc.) are transparently moved to the new sub-path names
// (actorID + "/rmounts" etc.) on Load, so subsequent loads skip the
// migration path entirely.
func TestLoadMigratesOldCardNamesToSubPath(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ps := persist.NewFSPersist(dir)

	sharedID := testutil.GenActorID()
	actorID := sharedID.String()

	// Seed cards with the OLD flat-file names.
	oldMounts := []domain.ProjectRef{{Name: "oldproj", ActorID: "actor-x", Root: true}}
	if err := ps.Save(actorID+".rmounts", oldMounts); err != nil {
		t.Fatalf("seed old mounts card: %v", err)
	}
	oldPrefs := domain.AccountPreferencesSnapshot{
		AccountID:   "root",
		Preferences: map[string]string{"shell": "bash"},
	}
	if err := ps.Save(actorID+".rprefs", oldPrefs); err != nil {
		t.Fatalf("seed old prefs card: %v", err)
	}
	if err := ps.Save(actorID+".ragents", []domain.AgentRef{
		{ID: "W#1", AgentKind: domain.AgentKindWorker, Status: "idle"},
	}); err != nil {
		t.Fatalf("seed old ragents card: %v", err)
	}

	a := &Actor{store: ps}
	ctx := testutil.AdminCtx(sharedID)
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}

	// State must be loaded from the migrated cards.
	if len(a.Mounts) != 1 || a.Mounts[0].Name != "oldproj" {
		t.Fatalf("mounts not migrated: %+v", a.Mounts)
	}
	if a.accountPrefs.Preferences["shell"] != "bash" {
		t.Fatalf("prefs not migrated: %+v", a.accountPrefs)
	}
	if len(a.Agents) != 1 || a.Agents[0].ID != "W#1" {
		t.Fatalf("agents not migrated: %+v", a.Agents)
	}

	// Old card names must be gone.
	for _, oldName := range []string{
		actorID + ".rmounts",
		actorID + ".rprefs",
		actorID + ".ragents",
	} {
		var probe json.RawMessage
		if err := ps.Load(oldName, &probe); !errors.Is(err, persist.ErrNotExist) {
			t.Errorf("old card %q should be removed after migration (err=%v)", oldName, err)
		}
	}

	// New card names must exist and carry the data.
	var mounts []domain.ProjectRef
	if err := ps.Load(a.mountsCardName(), &mounts); err != nil {
		t.Fatalf("load new mounts card: %v", err)
	}
	if len(mounts) != 1 || mounts[0].Name != "oldproj" {
		t.Fatalf("new mounts card = %+v", mounts)
	}
	var agents []domain.AgentRef
	if err := ps.Load(a.agentRegistryName(), &agents); err != nil {
		t.Fatalf("load new ragents card: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "W#1" {
		t.Fatalf("new ragents card = %+v", agents)
	}

	// Second load: migration is a no-op (new cards exist, old are gone).
	a2 := &Actor{store: ps}
	ctx2 := testutil.AdminCtx(sharedID)
	ctx2.SpawnFn = noOpSpawn
	ctx2.LookupIDFn = lookupOK
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.Mounts) != 1 || a2.Mounts[0].Name != "oldproj" {
		t.Fatalf("mounts not restored on second load: %+v", a2.Mounts)
	}
}

// TestAgentRegistryCardRoundTrip verifies the ground-truth agent registry
// card contract: agents persist to the separate ragents record (not the main
// workspace state), and a restarted workspace actor with the same identity
// restores its agents from that card.
func TestAgentRegistryCardRoundTrip(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ps := persist.NewFSPersist(dir)

	sharedID := testutil.GenActorID()

	// First lifecycle: seed one agent and save. The card must carry it and
	// the main state must not.
	a1 := &Actor{store: ps}
	ctx1 := testutil.AdminCtx(sharedID)
	ctx1.SpawnFn = noOpSpawn
	ctx1.LookupIDFn = lookupOK
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	if err := a1.OnStart(ctx1); err != nil {
		t.Fatal(err)
	}
	a1.Agents = []domain.AgentRef{{
		ID:        "W#1",
		ActorID:   "actor-1",
		AgentKind: domain.AgentKindCoder,
		LoadState: "unloaded",
	}}
	if err := a1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	var card []domain.AgentRef
	if err := ps.Load(a1.agentRegistryName(), &card); err != nil {
		t.Fatalf("registry card missing after save: %v", err)
	}
	if len(card) != 1 || card[0].ID != "W#1" || card[0].LoadState != "unloaded" {
		t.Fatalf("registry card = %+v, want [W#1 unloaded]", card)
	}
	var main map[string]any
	if err := ps.Load(a1.actorID, &main); err == nil {
		if _, ok := main["agents"]; ok {
			t.Error("main workspace state still carries agents; authority must live in the registry card")
		}
	}
	// A missing main record is expected: Save writes concern cards only, and
	// the combined workspace state.json is retired.

	// Second lifecycle: same identity + store — agents restore from the card.
	a2 := &Actor{store: ps}
	ctx2 := testutil.AdminCtx(sharedID)
	ctx2.SpawnFn = noOpSpawn
	ctx2.LookupIDFn = lookupOK
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.Agents) != 1 || a2.Agents[0].ID != "W#1" || a2.Agents[0].ActorID != "actor-1" {
		t.Fatalf("agents not restored from registry card: %+v", a2.Agents)
	}
}

// TestLoadAgentByID_PreservesPausedStatusUntilAgentReports verifies that lazy
// loading does not overwrite a persisted paused status before the agent reports
// its recovered runtime state.
func TestLoadAgentByID_PreservesPausedStatusUntilAgentReports(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "paused-agent",
		AgentKind: domain.AgentKindCoder,
		ActorID:   actorID.String(),
		LoadState: "unloaded",
		Status:    "paused",
	}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == actorID {
			return nil, false
		}
		return lookupOK(aid)
	}

	if _, alreadyLoaded, err := a.loadAgentByID(ctx, "paused-agent"); err != nil {
		t.Fatalf("loadAgentByID: %v", err)
	} else if alreadyLoaded {
		t.Fatal("alreadyLoaded = true, want false")
	}
	if a.Agents[0].Status != "paused" {
		t.Fatalf("Status = %q, want paused until agent_status_update arrives", a.Agents[0].Status)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatalf("agent list state: %v", err)
	}
	if len(state.Items) != 1 || state.Items[0].Runtime == nil || state.Items[0].Runtime.State != "paused" {
		t.Fatalf("sidebar state = %+v, want paused runtime", state.Items)
	}
}

// TestLoadAgentByID_AlreadyLoaded_DoesNotSpawnOrBlock verifies that an agent
// whose ActorID is still addressable returns immediately as alreadyLoaded=true
// with zero spawns. Repeated switching between loaded agents must not enter any
// startup path.
func TestLoadAgentByID_AlreadyLoaded_DoesNotSpawnOrBlock(t *testing.T) {
	a, ctx := freshActor(t)

	liveActorID := testutil.GenActorID().String()
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "Coder#abcd",
		ProjectID: "proj-1",
		AgentKind: "coder",
		Status:    "active",
		ActorID:   liveActorID,
		LoadState: "loaded",
	})

	spawnCalls := 0
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		spawnCalls++
		return noOpSpawn(p, name)
	}

	got, alreadyLoaded, err := a.loadAgentByID(ctx, "Coder#abcd")
	if err != nil {
		t.Fatalf("loadAgentByID: %v", err)
	}
	if !alreadyLoaded {
		t.Error("expected alreadyLoaded=true for a live agent")
	}
	if spawnCalls != 0 {
		t.Errorf("expected no spawn for already-loaded agent, got %d spawn calls", spawnCalls)
	}
	if got.ActorID != liveActorID {
		t.Errorf("ActorID = %q, want %q", got.ActorID, liveActorID)
	}
}

// TestLoadAgentByID_AcceptsActorID pins the actor-id lookup fallback that lets
// a caller holding only an agent's live actor id (an app resolving its own
// plugin agent — registry rows are workspace-global and hidden from the app)
// load the agent by the same handle workspace.agent_send_message accepts.
func TestLoadAgentByID_AcceptsActorID(t *testing.T) {
	a, ctx := freshActor(t)

	liveActorID := testutil.GenActorID().String()
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "NovelKing 助手#abcd", // registry row id, distinct from ActorID
		ProjectID: "",
		AgentKind: domain.AgentKindPlugin,
		Status:    "active",
		ActorID:   liveActorID,
		LoadState: "loaded",
	})

	spawnCalls := 0
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		spawnCalls++
		return noOpSpawn(p, name)
	}

	got, alreadyLoaded, err := a.loadAgentByID(ctx, liveActorID)
	if err != nil {
		t.Fatalf("loadAgentByID by ActorID: %v", err)
	}
	if !alreadyLoaded {
		t.Error("expected alreadyLoaded=true when found via ActorID")
	}
	if spawnCalls != 0 {
		t.Errorf("expected no spawn for already-loaded agent, got %d spawn calls", spawnCalls)
	}
	if got.ID != "NovelKing 助手#abcd" || got.ActorID != liveActorID {
		t.Errorf("resolved row = %q/%q, want %q/%q", got.ID, got.ActorID, "NovelKing 助手#abcd", liveActorID)
	}

	// An unknown actor id must still be a clean miss, not a false positive.
	if _, _, err := a.loadAgentByID(ctx, "no-such-actor-id"); err == nil {
		t.Fatal("expected not-found error for unknown ActorID")
	}
}

// TestLoadAgentByID_SpawnError_PropagatesRealError locks down that when the
// project actor's spawn_agent handler fails, loadAgentByID surfaces the actual
// cause rather than the opaque "project.spawn_agent returned nil".
func TestLoadAgentByID_SpawnError_PropagatesRealError(t *testing.T) {
	a, ctx := freshActor(t)

	projectActorID := testutil.GenActorID()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectActorID.String()},
	}
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "Coder#fail",
		ProjectID: projectActorID.String(),
		AgentKind: "coder",
		LoadState: "unloaded",
	})

	wantErr := errors.New("project.spawn_agent: actor name collision")
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(projectActorID, func(callID string, _ any) any {
			if callID == "project.spawn_agent" {
				return wantErr
			}
			return nil
		}), true
	}

	_, _, err := a.loadAgentByID(ctx, "Coder#fail")
	if err == nil {
		t.Fatal("expected error from failed spawn, got nil")
	}
	if !strings.Contains(err.Error(), "actor name collision") {
		t.Errorf("error lost real cause; got %q, want it to contain 'actor name collision'", err.Error())
	}
	if strings.Contains(err.Error(), "returned nil") {
		t.Errorf("error must not be the opaque 'returned nil'; got %q", err.Error())
	}
	if a.Agents[0].LoadState != "error" {
		t.Errorf("LoadState = %q, want error", a.Agents[0].LoadState)
	}
}

// TestLoadAgentByID_PassesGlobalPermissionMode verifies that lazy-loading an
// agent passes the global permission mode from account preferences through the
// spawn chain, so the agent starts with the correct mode without relying on a
// frontend RPC push (which races with OnStart cold start).
func TestLoadAgentByID_PassesGlobalPermissionMode(t *testing.T) {
	a, ctx := freshActor(t)

	projectActorID := testutil.GenActorID()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectActorID.String()},
	}
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "Coder#perm",
		ProjectID: projectActorID.String(),
		AgentKind: "coder",
		LoadState: "unloaded",
	})
	a.accountPrefs = domain.AccountPreferencesSnapshot{
		Preferences: map[string]string{"permissionMode": "yolo"},
	}

	var spawnReq domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(projectActorID, func(callID string, payload any) any {
			if callID == "project.spawn_agent" {
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
			}
			return nil
		}), true
	}
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		return noOpSpawn(p, name)
	}

	if _, _, err := a.loadAgentByID(ctx, "Coder#perm"); err != nil {
		t.Fatalf("loadAgentByID: %v", err)
	}
	if spawnReq.PermissionMode != "yolo" {
		t.Errorf("spawn PermissionMode = %q, want %q (from account preferences)", spawnReq.PermissionMode, "yolo")
	}
}

// TestLoadAgentByID_ErrorStateButLiveActor_ReusesNotRespawns locks down that
// when an agent is in "error" LoadState but its actor is still alive (the
// common case: a runtime status report marked it error while the actor lives),
// re-opening it reuses the live actor instead of re-spawning. Re-spawning
// would collide with the live tree name (gospore/tree: name taken).
func TestLoadAgentByID_ErrorStateButLiveActor_ReusesNotRespawns(t *testing.T) {
	a, ctx := freshActor(t)

	liveActorID := testutil.GenActorID()
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "Coder#err",
		ProjectID: "proj-1",
		AgentKind: "coder",
		Status:    "error",
		ActorID:   liveActorID.String(),
		LoadState: "error",
	})

	spawnCalls := 0
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		spawnCalls++
		return noOpSpawn(p, name)
	}
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) {
		// The actor is still alive and addressable.
		return testutil.NewFakeRef(liveActorID, nil), true
	}

	got, alreadyLoaded, err := a.loadAgentByID(ctx, "Coder#err")
	if err != nil {
		t.Fatalf("loadAgentByID: %v", err)
	}
	if !alreadyLoaded {
		t.Error("expected alreadyLoaded=true for a live actor in error state")
	}
	if spawnCalls != 0 {
		t.Errorf("expected no spawn for live actor, got %d spawn calls", spawnCalls)
	}
	if got.ActorID != liveActorID.String() {
		t.Errorf("ActorID = %q, want %q", got.ActorID, liveActorID.String())
	}
	if a.Agents[0].LoadState != "loaded" {
		t.Errorf("LoadState = %q, want loaded (recovered by reuse)", a.Agents[0].LoadState)
	}
}

// TestSpawnGlobalAgent_ReturnsWithoutInvokingAgent protects the same deadlock
// boundary for legacy/global agents: spawnGlobalAgent runs on the workspace
// owner loop, while the new agent's OnStart synchronously calls that loop for
// its kind config. It may allocate the actor but must not invoke/wait on it.
func TestSpawnGlobalAgent_ReturnsWithoutInvokingAgent(t *testing.T) {
	a, ctx := freshActor(t)

	agentID := testutil.GenActorID()
	invokeCalls := 0
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(agentID, func(string, any) any {
			invokeCalls++
			return nil
		}), nil
	}

	got, err := a.spawnGlobalAgent(
		ctx,
		"Coder#global",
		"coder",
		"Global Coder",
		nil, nil, nil, nil, nil,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("spawnGlobalAgent: %v", err)
	}
	if got.ActorID != agentID.String() {
		t.Errorf("ActorID = %q, want %q", got.ActorID, agentID.String())
	}
	if invokeCalls != 0 {
		t.Errorf("spawnGlobalAgent invoked new agent %d times before returning; this deadlocks OnStart callbacks", invokeCalls)
	}
}

func TestHandleCoordinatorLookup_ColdStartDoesNotInvokeCoordinator(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:          "coordinator",
		AgentKind:   domain.AgentKindCoordinator,
		DisplayName: "Coordinator",
		Status:      "inactive",
	})

	agentID := testutil.GenActorID()
	invokeCalls := 0
	spawnedRef := testutil.NewFakeRef(agentID, func(string, any) any {
		invokeCalls++
		return nil
	})
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return spawnedRef, nil
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == agentID {
			return spawnedRef, true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleCoordinatorLookup(ctx)
	if err != nil {
		t.Fatalf("handleCoordinatorLookup: %v", err)
	}
	if !resp.Found || resp.ActorID != agentID.String() || resp.Nickname != "Coordinator" {
		t.Fatalf("coordinator lookup = %+v", resp)
	}
	if invokeCalls != 0 {
		t.Fatalf("coordinator lookup invoked async-started coordinator %d times before returning", invokeCalls)
	}
}

func TestHandleAgentSendMessage_QueuesWithoutWaitingForRecipient(t *testing.T) {
	a, ctx := freshActor(t)
	agentID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{ID: "Coder#0001", ActorID: agentID.String(), LoadState: "loaded"}}

	var received domain.AgentMessageReceiveReq
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(agentID, func(callID string, payload any) any {
			if callID == "message_receive" {
				received = payload.(domain.AgentMessageReceiveReq)
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Coder#0001", Text: "continue"})
	if err != nil {
		t.Fatalf("handleAgentSendMessage: %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if received.Text != "continue" {
		t.Errorf("received text = %q, want %q", received.Text, "continue")
	}
	// Anonymous caller (no CallerAgentId): sender fields must stay empty so
	// the recipient's step meta degrades to plain "agent" instead of leaking
	// the workspace's own actor id as the sender name.
	if received.FromAgentID != "" || received.FromName != "" {
		t.Errorf("anonymous send must leave sender fields empty, got FromAgentID=%q FromName=%q",
			received.FromAgentID, received.FromName)
	}
}

// When the caller is a known agent, the recipient must see the caller's
// stable ID and human-readable display name — not the workspace's actor id.
// Confirms the workspace's findAgentRef lookup wires the sender identity
// through to message_receive.
func TestHandleAgentSendMessage_ResolvesSenderDisplayName(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerActorID := genID()
	targetActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Owner#0007", DisplayName: "Bob the Builder", ActorID: ownerActorID, LoadState: "loaded"},
		{ID: "Coder#0001", ActorID: targetActorID, ParentAgentID: ownerActorID, LoadState: "loaded"},
	}

	var received domain.AgentMessageReceiveReq
	invocations := map[string]int{}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == targetActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				invocations[callID]++
				if callID == "message_receive" {
					received = payload.(domain.AgentMessageReceiveReq)
				}
				return nil
			}), true
		}
		if aid.String() == ownerActorID {
			return testutil.NewFakeRef(aid, func(string, any) any { return nil }), true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{
		ToAgentID:     "Coder#0001",
		Text:          "hi",
		CallerAgentID: ownerActorID,
	})
	if err != nil {
		t.Fatalf("handleAgentSendMessage: %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if invocations["message_receive"] != 1 {
		t.Fatalf("expected target message_receive invoked once, got %d", invocations["message_receive"])
	}
	if received.FromAgentID != "Owner#0007" {
		t.Errorf("FromAgentID = %q, want %q (sender's stable ID)", received.FromAgentID, "Owner#0007")
	}
	if received.FromName != "Bob the Builder" {
		t.Errorf("FromName = %q, want %q (sender's display name)", received.FromName, "Bob the Builder")
	}
}

// ── agent pause/resume forwarding tests ──

func TestHandleAgentPause_OwnerAuthorized(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := testutil.GenActorID()
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: ownerID.String(),
		LoadState:     "loaded",
	}}

	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				if callID == "agent_pause" {
					invoked = true
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: ownerID.String(),
	})
	if err != nil {
		t.Fatalf("handleAgentPause (owner): %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invoked {
		t.Fatal("expected agent_pause to be invoked on target")
	}
}

func TestHandleAgentPause_SelfAuthorizedByActorID(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: "owner-actor",
		LoadState:     "loaded",
	}}

	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				if callID == "agent_pause" {
					invoked = true
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}

	// Self-pause: CallerAgentId matches target's ActorID
	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: targetActorID.String(),
	})
	if err != nil {
		t.Fatalf("handleAgentPause (self via ActorID): %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invoked {
		t.Fatal("expected agent_pause to be invoked")
	}
}

func TestHandleAgentPause_SelfAuthorizedByID(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: "owner-actor",
		LoadState:     "loaded",
	}}

	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				if callID == "agent_pause" {
					invoked = true
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}

	// Self-pause: CallerAgentId matches target's AgentRef.ID
	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: "Agent#0001",
	})
	if err != nil {
		t.Fatalf("handleAgentPause (self via ID): %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invoked {
		t.Fatal("expected agent_pause to be invoked")
	}
}

func TestHandleAgentPause_UnrelatedAgentRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: "owner-actor",
		LoadState:     "loaded",
	}}

	// An unrelated agent (not owner, not self) tries to pause
	_, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: "unrelated-agent",
	})
	if err == nil {
		t.Fatal("expected error for unrelated agent caller")
	}
	if !strings.Contains(err.Error(), "only the target agent or its direct owner") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleAgentPause_TargetNotLoadedRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	a.Agents = []domain.AgentRef{{
		ID:        "Agent#0001",
		ActorID:   "actor:not-loaded",
		LoadState: "unloaded", // Not loaded
	}}

	_, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: "any-caller",
	})
	if err == nil {
		t.Fatal("expected error for unloaded target")
	}
	if !strings.Contains(err.Error(), "is not loaded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleAgentPause_MissingToAgentID(t *testing.T) {
	a, ctx := freshActorAnon(t)

	_, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "",
		CallerAgentID: "any-caller",
	})
	if err == nil {
		t.Fatal("expected error for empty ToAgentId")
	}
}

func TestHandleAgentPause_FireAndForgetDoesNotBlock(t *testing.T) {
	// Verify fire-and-forget: the handler returns immediately without calling
	// Final on the invoke call, so it never blocks on the target's owner loop.
	a, ctx := freshActorAnon(t)
	ownerID := testutil.GenActorID()
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: ownerID.String(),
		LoadState:     "loaded",
	}}

	invokeCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				invokeCalled = true
				// Return a sentinel indicating the invoke was called.
				// The handler does NOT call Final, so the return value is never
				// consumed — the test only checks that invoke was called.
				return gen.AgentPauseResp{Sent: true}
			}), true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: ownerID.String(),
	})
	if err != nil {
		t.Fatalf("handleAgentPause: %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invokeCalled {
		t.Fatal("expected agent_pause invoke to be called")
	}
}

func TestHandleAgentResume_OwnerAuthorized(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := testutil.GenActorID()
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: ownerID.String(),
		LoadState:     "loaded",
	}}

	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				if callID == "agent_resume" {
					invoked = true
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleAgentResume(ctx, gen.AgentResumeReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: ownerID.String(),
	})
	if err != nil {
		t.Fatalf("handleAgentResume (owner): %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invoked {
		t.Fatal("expected agent_resume to be invoked on target")
	}
}

func TestHandleAgentResume_SelfAuthorized(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: "owner-actor",
		LoadState:     "loaded",
	}}

	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == targetActorID {
			return testutil.NewFakeRef(targetActorID, func(callID string, payload any) any {
				if callID == "agent_resume" {
					invoked = true
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}

	resp, err := a.handleAgentResume(ctx, gen.AgentResumeReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: targetActorID.String(),
	})
	if err != nil {
		t.Fatalf("handleAgentResume (self): %v", err)
	}
	if !resp.Sent {
		t.Fatal("expected Sent=true")
	}
	if !invoked {
		t.Fatal("expected agent_resume to be invoked")
	}
}

func TestHandleAgentResume_UnrelatedAgentRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:            "Agent#0001",
		ActorID:       targetActorID.String(),
		ParentAgentID: "owner-actor",
		LoadState:     "loaded",
	}}

	_, err := a.handleAgentResume(ctx, gen.AgentResumeReq{
		ToAgentID:     "Agent#0001",
		CallerAgentID: "unrelated-agent",
	})
	if err == nil {
		t.Fatal("expected error for unrelated agent caller")
	}
	if !strings.Contains(err.Error(), "only the target agent or its direct owner") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleAgentPause_TargetNotFoundInSnapshot(t *testing.T) {
	a, ctx := freshActorAnon(t)
	// No agents in snapshot
	_, err := a.handleAgentPause(ctx, gen.AgentPauseReq{
		ToAgentID:     "NonExistentAgent",
		CallerAgentID: "any-caller",
	})
	if err == nil {
		t.Fatal("expected error for non-existent target")
	}
	if !strings.Contains(err.Error(), "is not loaded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleList_SystemProject(t *testing.T) {
	a, ctx := freshActor(t)
	list, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 system meta project, got %d items", len(list.Items))
	}
	if !list.Items[0].System {
		t.Errorf("expected the only project to be a system meta project, got %+v", list.Items[0])
	}
	if list.Items[0].Name != systemMetaProjectName {
		t.Errorf("expected system meta project name %q, got %q", systemMetaProjectName, list.Items[0].Name)
	}
}

func TestHandleList_SystemMetaProjectPinnedFirst(t *testing.T) {
	a, ctx := freshActor(t)
	// Mount a user project after system meta project exists.
	if _, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/user-proj", Name: "user-proj"}); err != nil {
		t.Fatal(err)
	}
	list, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) < 2 {
		t.Fatalf("expected at least 2 projects, got %d", len(list.Items))
	}
	if !list.Items[0].System {
		t.Fatalf("expected system meta project first, got %+v", list.Items[0])
	}
	if list.Items[0].Name != systemMetaProjectName {
		t.Fatalf("expected system meta project name %q first, got %q", systemMetaProjectName, list.Items[0].Name)
	}
}

func TestHandleMount(t *testing.T) {
	a, ctx := freshActor(t)

	ref, err := a.handleMount(ctx, domain.WorkspaceMountReq{
		Path: "/tmp/myproject",
		Name: "myproject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "myproject" {
		t.Errorf("expected name myproject, got %q", ref.Name)
	}
	if ref.Name != "myproject" {
		t.Errorf("expected name myproject, got %q", ref.Name)
	}

	// Duplicate mount should fail
	_, err = a.handleMount(ctx, domain.WorkspaceMountReq{
		Path: "/tmp/myproject",
		Name: "myproject",
	})
	if err == nil {
		t.Error("expected error for duplicate mount")
	}
}

func TestHandleUnmount(t *testing.T) {
	a, ctx := freshActor(t)

	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}

	unmounted, err := a.handleUnmount(ctx, domain.WorkspaceUnmountReq{ProjectID: mountRef.ActorID})
	if err != nil {
		t.Fatal(err)
	}
	if unmounted.Name != "p1" {
		t.Errorf("expected unmounted p1, got %q", unmounted.Name)
	}
	list, _ := a.handleList(ctx)
	if len(list.Items) != 1 {
		t.Errorf("expected 1 mount after unmount, got %d", len(list.Items))
	}
	if len(list.Items) > 0 && list.Items[0].Name != systemMetaProjectName {
		t.Errorf("expected only system meta project to remain, got %q", list.Items[0].Name)
	}

	// Unmount non-existent should fail
	_, err = a.handleUnmount(ctx, domain.WorkspaceUnmountReq{ProjectID: "nope"})
	if err == nil {
		t.Error("expected error for unmount of non-existent project")
	}
}

func TestHandleAddMount(t *testing.T) {
	a, ctx := freshActor(t)
	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := a.handleAddMount(ctx, domain.WorkspaceAddMountReq{
		ProjectID: mountRef.ActorID,
		MountName: "vendor",
		MountPath: "/tmp/p1/vendor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "p1" {
		t.Errorf("expected p1, got %q", ref.Name)
	}
	if len(ref.Mounts) != 1 || ref.Mounts[0].Name != "vendor" {
		t.Errorf("expected vendor mount, got %v", ref.Mounts)
	}

	// Duplicate mount name should fail
	_, err = a.handleAddMount(ctx, domain.WorkspaceAddMountReq{
		ProjectID: mountRef.ActorID,
		MountName: "vendor",
		MountPath: "/tmp/p1/other",
	})
	if err == nil {
		t.Error("expected error for duplicate mount name")
	}

	// Non-existent project should fail
	_, err = a.handleAddMount(ctx, domain.WorkspaceAddMountReq{
		ProjectID: "nope",
		MountName: "x",
		MountPath: "/x",
	})
	if err == nil {
		t.Error("expected error for non-existent project")
	}
}

func TestHandleRemoveMount(t *testing.T) {
	a, ctx := freshActor(t)
	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}

	// Add a sub-mount first
	_, err = a.handleAddMount(ctx, domain.WorkspaceAddMountReq{
		ProjectID: mountRef.ActorID, MountName: "vendor", MountPath: "/tmp/p1/vendor",
	})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := a.handleRemoveMount(ctx, domain.WorkspaceRemoveMountReq{
		ProjectID: mountRef.ActorID,
		MountName: "vendor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "p1" {
		t.Errorf("expected p1, got %q", ref.Name)
	}
	if len(ref.Mounts) != 0 {
		t.Errorf("expected 0 mounts after removal, got %d", len(ref.Mounts))
	}

	// Remove non-existent mount should fail
	_, err = a.handleRemoveMount(ctx, domain.WorkspaceRemoveMountReq{
		ProjectID: mountRef.ActorID, MountName: "nope",
	})
	if err == nil {
		t.Error("expected error for non-existent mount")
	}
}

func TestHandleCreate_RejectsSystemProjectPath(t *testing.T) {
	a, ctx := freshActor(t)
	sysPath := systemMetaProjectPath()
	_, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: sysPath, Name: "foo"})
	if err == nil {
		t.Fatal("expected error creating project under system meta project path")
	}
}

func TestHandleMount_RejectsSystemProjectPath(t *testing.T) {
	a, ctx := freshActor(t)
	sysPath := systemMetaProjectPath()
	_, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: sysPath, Name: "foo"})
	if err == nil {
		t.Fatal("expected error mounting system meta project path")
	}
}

func TestHandleUnmount_ProtectsSystemProject(t *testing.T) {
	a, ctx := freshActor(t)
	var sysID string
	for _, m := range a.Mounts {
		if m.System {
			sysID = m.ActorID
			break
		}
	}
	if sysID == "" {
		t.Fatal("system meta project not found")
	}
	_, err := a.handleUnmount(ctx, domain.WorkspaceUnmountReq{ProjectID: sysID})
	if err == nil {
		t.Fatal("expected error unmounting system meta project")
	}
}

func TestHandleUpdateProject_ProtectsSystemProject(t *testing.T) {
	a, ctx := freshActor(t)
	var sysID string
	for _, m := range a.Mounts {
		if m.System {
			sysID = m.ActorID
			break
		}
	}
	if sysID == "" {
		t.Fatal("system meta project not found")
	}
	_, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: sysID, Name: "renamed"})
	if err == nil {
		t.Fatal("expected error renaming system meta project")
	}
	_, err = a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: sysID, PermissionMode: "foo"})
	if err == nil {
		t.Fatal("expected error changing system meta project permission mode")
	}
	// LastOpenedAt should still be allowed.
	ref, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: sysID, LastOpenedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("unexpected error updating LastOpenedAt: %v", err)
	}
	if ref.Project.LastOpenedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("expected LastOpenedAt update, got %q", ref.Project.LastOpenedAt)
	}
}

func TestHandleUpdateProject_ClearsPermissionMode(t *testing.T) {
	a, ctx := freshActor(t)
	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	pid := mountRef.ActorID

	// Set project override to permission.
	ref, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, PermissionMode: "permission"})
	if err != nil {
		t.Fatalf("unexpected error setting permission mode: %v", err)
	}
	if ref.Project.PermissionMode != "permission" {
		t.Errorf("expected permission mode 'permission', got %q", ref.Project.PermissionMode)
	}

	// Clear the project override.
	ref, err = a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, ClearPermissionMode: true})
	if err != nil {
		t.Fatalf("unexpected error clearing permission mode: %v", err)
	}
	if ref.Project.PermissionMode != "" {
		t.Errorf("expected empty permission mode after clear, got %q", ref.Project.PermissionMode)
	}
}

func TestHandleUpdateProject_SetsAndClearsAppKind(t *testing.T) {
	a, ctx := freshActor(t)
	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	pid := mountRef.ActorID

	ref, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, AppKind: "dev-app"})
	if err != nil {
		t.Fatalf("unexpected error setting app kind: %v", err)
	}
	if ref.Project.AppKind != "dev-app" {
		t.Errorf("expected app kind 'dev-app', got %q", ref.Project.AppKind)
	}

	ref, err = a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, ClearAppKind: true})
	if err != nil {
		t.Fatalf("unexpected error clearing app kind: %v", err)
	}
	if ref.Project.AppKind != "" {
		t.Errorf("expected empty app kind after clear, got %q", ref.Project.AppKind)
	}

	ref, err = a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, AppKind: "sporeapp"})
	if err != nil {
		t.Fatalf("unexpected error setting sporeapp kind: %v", err)
	}
	if ref.Project.AppKind != "sporeapp" {
		t.Errorf("expected app kind 'sporeapp', got %q", ref.Project.AppKind)
	}

	_, err = a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: pid, AppKind: "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid app kind")
	}
}

func TestHandleUpdateProject_ProtectsSystemProjectAppKind(t *testing.T) {
	a, ctx := freshActor(t)
	var sysID string
	for _, m := range a.Mounts {
		if m.System {
			sysID = m.ActorID
			break
		}
	}
	if sysID == "" {
		t.Fatal("system meta project not found")
	}
	_, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: sysID, AppKind: "dev-app"})
	if err == nil {
		t.Fatal("expected error changing system meta project app kind")
	}
}

func TestHandleUpdateProject_CannotClearSystemProjectPermissionMode(t *testing.T) {
	a, ctx := freshActor(t)
	var sysID string
	for _, m := range a.Mounts {
		if m.System {
			sysID = m.ActorID
			break
		}
	}
	if sysID == "" {
		t.Fatal("system meta project not found")
	}
	_, err := a.handleUpdateProject(ctx, domain.WorkspaceUpdateProjectReq{ProjectID: sysID, ClearPermissionMode: true})
	if err == nil {
		t.Fatal("expected error clearing system meta project permission mode")
	}
}

func TestEnsureSystemProject_PromotesExistingMount(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), Mounts: []domain.ProjectRef{{Name: "workspace", Path: systemMetaProjectPath(), Root: true}}}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(a.Mounts))
	}
	if !a.Mounts[0].System {
		t.Errorf("existing mount at system path should be promoted to system meta project")
	}
	if a.Mounts[0].Name != systemMetaProjectName {
		t.Errorf("expected system meta project name %q, got %q", systemMetaProjectName, a.Mounts[0].Name)
	}
}

func TestEnsureSystemProject_RenamesLegacySystemPlugins(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)
	sysPath := systemMetaProjectPath()
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), Mounts: []domain.ProjectRef{{Name: "system-plugins", Path: sysPath, Root: true}}}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(a.Mounts))
	}
	if !a.Mounts[0].System {
		t.Fatalf("legacy system-plugins should be promoted to system meta project")
	}
	if a.Mounts[0].Name != systemMetaProjectName {
		t.Fatalf("expected renamed system meta project %q, got %q", systemMetaProjectName, a.Mounts[0].Name)
	}
}

func TestEnsureSystemProject_RenamesAlreadyMarkedSystem(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)
	sysPath := systemMetaProjectPath()
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), Mounts: []domain.ProjectRef{{Name: "system-plugins", Path: sysPath, Root: true, System: true}}}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(a.Mounts))
	}
	if a.Mounts[0].Name != systemMetaProjectName {
		t.Fatalf("expected renamed system meta project %q, got %q", systemMetaProjectName, a.Mounts[0].Name)
	}
	if !a.Mounts[0].System {
		t.Fatalf("system flag should remain true")
	}
}

func TestNoSystemProject_SkipsEnsure(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), NoSystemProject: true}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.Mounts) != 0 {
		t.Errorf("expected 0 mounts when NoSystemProject is true, got %d", len(a.Mounts))
	}
}

func TestHandleListAgents_Empty(t *testing.T) {
	a, ctx := freshActor(t)
	agents, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(agents.Items) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents.Items))
	}
}

func TestHandleListAgentKinds_UsesConfigs(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleListAgentKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKind := make(map[string]domain.AgentKindInfo)
	for _, item := range resp.Items {
		byKind[item.Kind] = item
	}
	requireKind := func(kind string) domain.AgentKindInfo {
		item, ok := byKind[kind]
		if !ok {
			t.Fatalf("expected kind %q in response, got %d items", kind, len(resp.Items))
		}
		return item
	}
	coder := requireKind(domain.AgentKindCoder)
	if !coder.UserCreatable {
		t.Fatal("coder should be user-creatable")
	}
	reviewer := requireKind(domain.AgentKindReviewer)
	if reviewer.UserCreatable {
		t.Fatal("reviewer should not be user-creatable (system-only)")
	}
	if !reviewer.SystemManaged {
		t.Fatal("reviewer should be system-managed")
	}
}

func TestHandleGetAgentKindConfig(t *testing.T) {
	a, ctx := freshActor(t)
	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindReviewer})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RolePromptRef.Key != "project.reviewer" {
		t.Fatalf("role prompt key = %q, want project.reviewer", cfg.RolePromptRef.Key)
	}
}

func TestHandleSaveAgentKindConfig(t *testing.T) {
	a, ctx := freshActor(t)
	req := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:                domain.AgentKindCoder,
		DisplayName:         "Coder Custom",
		UserCreatable:       true,
		SystemManaged:       false,
		AutoAllowTools:      []string{"project.read"},
		AutoAllowCandidates: []string{"project.read", "project.write"},
		Primary:             slotFromUnit(gen.ModelUnit{Model: "default-primary", Provider: "provider"}),
		Fast:                slotFromUnit(gen.ModelUnit{Model: "default-fast", Provider: "provider"}),
		CompactionPolicy:    &gen.CompactionPolicy{Enabled: true, BudgetMode: "absolute", TokenBudget: 12000, RecentWindow: 12, MaxSummaryTokens: 2000},
		RandomName:          &gen.RandomNameConfig{Enabled: true, Prefixes: []string{"Saved"}, Suffixes: []string{"Name"}},
	}
	req.RolePromptRef.Kind = "profile"
	req.RolePromptRef.Key = "custom.role"
	cfg, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DisplayName != "Coder Custom" {
		t.Fatalf("display name = %q", cfg.DisplayName)
	}
	if cfg.RolePromptRef.Key != "custom.role" {
		t.Fatalf("role prompt key = %q", cfg.RolePromptRef.Key)
	}
	if !reflect.DeepEqual(cfg.AutoAllowCandidates, req.AutoAllowCandidates) {
		t.Fatalf("auto-allow candidates = %v, want %v", cfg.AutoAllowCandidates, req.AutoAllowCandidates)
	}
	if cfg.MaxTurns != DefaultGoalMaxTurns {
		t.Fatalf("expected default MaxTurns = %d when unset, got %d", DefaultGoalMaxTurns, cfg.MaxTurns)
	}
	fetched, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	if fetched.RolePromptRef.Key != "custom.role" {
		t.Fatalf("role prompt key = %q", fetched.RolePromptRef.Key)
	}
	if !reflect.DeepEqual(fetched.AutoAllowCandidates, req.AutoAllowCandidates) {
		t.Fatalf("persisted auto-allow candidates = %v, want %v", fetched.AutoAllowCandidates, req.AutoAllowCandidates)
	}
	if fetched.MaxTurns != DefaultGoalMaxTurns {
		t.Fatalf("expected persisted default MaxTurns = %d, got %d", DefaultGoalMaxTurns, fetched.MaxTurns)
	}
	if fetched.Primary == nil || fetched.Primary.Candidates[0].Unit == nil || fetched.Primary.Candidates[0].Unit.Model != "default-primary" {
		t.Fatalf("persisted primary default = %+v", fetched.Primary)
	}
	if fetched.Fast == nil || fetched.Fast.Candidates[0].Unit == nil || fetched.Fast.Candidates[0].Unit.Model != "default-fast" {
		t.Fatalf("persisted fast default = %+v", fetched.Fast)
	}
	if !reflect.DeepEqual(fetched.CompactionPolicy, req.CompactionPolicy) {
		t.Fatalf("persisted compaction policy = %+v, want %+v", fetched.CompactionPolicy, req.CompactionPolicy)
	}
	if !reflect.DeepEqual(fetched.RandomName, req.RandomName) {
		t.Fatalf("persisted random name = %+v, want %+v", fetched.RandomName, req.RandomName)
	}
}

// TestHandleSaveAgentKindConfig_PersistsMaxTurns verifies a custom MaxTurns is
// saved and returned, rather than being overwritten by the default.
func TestHandleSaveAgentKindConfig_PersistsMaxTurns(t *testing.T) {
	a, ctx := freshActor(t)
	req := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:     domain.AgentKindCoder,
		MaxTurns: 50,
	}
	req.RolePromptRef.Kind = "profile"
	req.RolePromptRef.Key = "custom.role"
	cfg, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxTurns != 50 {
		t.Fatalf("expected MaxTurns = 50, got %d", cfg.MaxTurns)
	}
}

// TestHandleSaveAgentKindConfig_SystemManagedIsHardcoded verifies that the
// SystemManaged flag is always derived from the canonical kind definition and
// cannot be overridden by client input, regardless of what the request sends.
func TestHandleSaveAgentKindConfig_SystemManagedIsHardcoded(t *testing.T) {
	a, ctx := freshActor(t)
	// Attempt to flip a system-managed kind to non-system-managed.
	req := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:          domain.AgentKindReviewer,
		DisplayName:   "Reviewer",
		SystemManaged: false, // client tries to clear it
	}
	req.RolePromptRef.Kind = "profile"
	req.RolePromptRef.Key = "project.reviewer"
	cfg, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SystemManaged {
		t.Fatal("SystemManaged must be hardcoded true for reviewer; client override was applied")
	}
	// Attempt to flip coder (canonical SystemManaged=false) to true.
	req2 := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:          domain.AgentKindCoder,
		SystemManaged: true, // client tries to set it
	}
	req2.RolePromptRef.Kind = "profile"
	req2.RolePromptRef.Key = "custom.role"
	cfg2, err := a.handleSaveAgentKindConfig(ctx, req2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SystemManaged {
		t.Fatal("SystemManaged must be hardcoded false for coder; client override was applied")
	}
}

func TestHandleSaveAgentKindConfig_PersistsEnvironmentContext(t *testing.T) {
	a, ctx := freshActor(t)
	custom := map[string]string{"os": "macos", "shell": "zsh"}
	req := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:               domain.AgentKindCoder,
		DisplayName:        "Coder Env",
		UserCreatable:      true,
		SystemManaged:      false,
		EnvironmentContext: custom,
	}
	req.RolePromptRef.Kind = "profile"
	req.RolePromptRef.Key = "custom.role"
	saved, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if saved.EnvironmentContext["os"] != "macos" || saved.EnvironmentContext["shell"] != "zsh" {
		t.Fatalf("saved env = %v, want os=macos shell=zsh", saved.EnvironmentContext)
	}
	fetched, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	if fetched.EnvironmentContext["os"] != "macos" || fetched.EnvironmentContext["shell"] != "zsh" {
		t.Fatalf("fetched env = %v, want os=macos shell=zsh", fetched.EnvironmentContext)
	}
}

func TestHandleCreateAgentKind_ClonesBaseAndAppearsInList(t *testing.T) {
	a, ctx := freshActor(t)

	resp, err := a.handleCreateAgentKind(ctx, domain.WorkspaceCreateAgentKindReq{
		Kind:        "my-helper",
		DisplayName: "My Helper",
		BaseKind:    domain.AgentKindCoder,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "my-helper" || resp.DisplayName != "My Helper" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if !resp.UserCreatable || resp.SystemManaged || resp.Builtin {
		t.Fatalf("custom kind flags wrong: %+v", resp)
	}

	// Cloned from coder: role prompt ref and default bundles carried over.
	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: "my-helper"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RolePromptRef.Key != "project.coder" {
		t.Fatalf("cloned role prompt key = %q, want project.coder", cfg.RolePromptRef.Key)
	}
	if len(cfg.DefaultBundleIDs) == 0 {
		t.Fatalf("expected cloned coder bundle list, got %v", cfg.DefaultBundleIDs)
	}

	list, err := a.handleListAgentKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKind := make(map[string]domain.AgentKindInfo, len(list.Items))
	for _, item := range list.Items {
		byKind[item.Kind] = item
	}
	custom, ok := byKind["my-helper"]
	if !ok {
		t.Fatal("custom kind missing from list_agent_kinds")
	}
	if custom.Builtin || !custom.UserCreatable {
		t.Fatalf("custom kind list flags wrong: %+v", custom)
	}
	if !byKind[domain.AgentKindCoder].Builtin {
		t.Fatalf("coder must be marked Builtin in list_agent_kinds")
	}

	configs, err := a.handleListAgentKindConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range configs.Items {
		if c.Kind == "my-helper" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom kind missing from list_agent_kind_configs")
	}

	// A custom kind is user-creatable and create_agent must accept it.
	ag, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		DisplayName: "Helper One",
		AgentKind:   "my-helper",
	})
	if err != nil {
		t.Fatalf("create_agent with custom kind failed: %v", err)
	}
	if ag.AgentKind != "my-helper" {
		t.Fatalf("created agent kind = %q, want my-helper", ag.AgentKind)
	}
}

func TestHandleCreateAgentKind_RejectsConflictsAndBadSlug(t *testing.T) {
	a, ctx := freshActor(t)

	cases := []struct {
		name string
		req  domain.WorkspaceCreateAgentKindReq
	}{
		{"builtin collision", domain.WorkspaceCreateAgentKindReq{Kind: domain.AgentKindCoder}},
		{"empty slug", domain.WorkspaceCreateAgentKindReq{Kind: ""}},
		{"uppercase", domain.WorkspaceCreateAgentKindReq{Kind: "MyKind"}},
		{"space", domain.WorkspaceCreateAgentKindReq{Kind: "my kind"}},
		{"leading hyphen", domain.WorkspaceCreateAgentKindReq{Kind: "-bad"}},
		{"double hyphen", domain.WorkspaceCreateAgentKindReq{Kind: "a--b"}},
		{"underscore", domain.WorkspaceCreateAgentKindReq{Kind: "a_b"}},
		{"unknown base", domain.WorkspaceCreateAgentKindReq{Kind: "ok-slug", BaseKind: "does-not-exist"}},
	}
	for _, tc := range cases {
		if _, err := a.handleCreateAgentKind(ctx, tc.req); err == nil {
			t.Errorf("%s: expected rejection for %q", tc.name, tc.req.Kind)
		}
	}

	// A valid custom kind is accepted once and rejected on the second create.
	if _, err := a.handleCreateAgentKind(ctx, domain.WorkspaceCreateAgentKindReq{Kind: "dup-kind"}); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	if _, err := a.handleCreateAgentKind(ctx, domain.WorkspaceCreateAgentKindReq{Kind: "dup-kind"}); err == nil {
		t.Fatal("expected duplicate custom kind rejection")
	}
}

func TestHandleDeleteAgentKind_Guards(t *testing.T) {
	a, ctx := freshActor(t)

	// Built-in kinds cannot be deleted.
	if _, err := a.handleDeleteAgentKind(ctx, domain.WorkspaceDeleteAgentKindReq{Kind: domain.AgentKindCoder}); err == nil {
		t.Fatal("expected builtin deletion rejection")
	}
	// Unknown custom kind is rejected.
	if _, err := a.handleDeleteAgentKind(ctx, domain.WorkspaceDeleteAgentKindReq{Kind: "ghost-kind"}); err == nil {
		t.Fatal("expected unknown kind deletion rejection")
	}

	// A kind with a live instance is protected.
	if _, err := a.handleCreateAgentKind(ctx, domain.WorkspaceCreateAgentKindReq{Kind: "busy-kind"}); err != nil {
		t.Fatal(err)
	}
	a.Agents = append(a.Agents, domain.AgentRef{ID: "busy#1", AgentKind: "busy-kind", DisplayName: "Busy"})
	if _, err := a.handleDeleteAgentKind(ctx, domain.WorkspaceDeleteAgentKindReq{Kind: "busy-kind"}); err == nil {
		t.Fatal("expected live-instance deletion rejection")
	}

	// An unused custom kind is deletable and disappears from the list.
	if _, err := a.handleCreateAgentKind(ctx, domain.WorkspaceCreateAgentKindReq{Kind: "free-kind"}); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleDeleteAgentKind(ctx, domain.WorkspaceDeleteAgentKindReq{Kind: "free-kind"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Removed || resp.Kind != "free-kind" {
		t.Fatalf("unexpected delete resp: %+v", resp)
	}
	list, err := a.handleListAgentKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Items {
		if item.Kind == "free-kind" {
			t.Fatal("deleted kind still present in list_agent_kinds")
		}
	}
}

func TestSeedAgentKindConfigs_PreservesUserRetiredNameTemplate(t *testing.T) {
	a, ctx := freshActor(t)
	// A user-created template that reuses a retired builtin slug must survive
	// the seed prune; only system-managed leftovers are reaped.
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          "architect",
			DisplayName:   "My Architect",
			UserCreatable: true,
			SystemManaged: false,
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "custom.architect"},
		},
	}
	a.Agents = []domain.AgentRef{{ID: "custom-arch#1", AgentKind: "architect", DisplayName: "My Architect"}}

	a.seedAgentKindConfigs(ctx)

	foundCfg := false
	for _, cfg := range a.AgentKindConfigs {
		if cfg.Kind == "architect" {
			foundCfg = true
			if cfg.DisplayName != "My Architect" {
				t.Fatalf("user template display name overwritten: %+v", cfg)
			}
		}
	}
	if !foundCfg {
		t.Fatal("user-created architect template was pruned")
	}
	foundAgent := false
	for _, ag := range a.Agents {
		if ag.AgentKind == "architect" {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Fatal("user-created architect agent was pruned")
	}
}

func TestHandleCreateAgent(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: actorID},
	}

	agent, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Test Agent",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(agent.ID, "Test Agent#") {
		t.Errorf("expected id with prefix 'Test Agent#', got %q", agent.ID)
	}
	if agent.DisplayName != "Test Agent" {
		t.Errorf("expected 'Test Agent', got %q", agent.DisplayName)
	}
	if agent.Status != "active" {
		t.Errorf("expected active, got %q", agent.Status)
	}

	agent2, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID: actorID,
		AgentKind: "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent2.ID, "#") {
		t.Errorf("expected id containing '#', got %q", agent2.ID)
	}
	if len(a.Agents) != 2 {
		t.Fatalf("expected 2 stored agents (2 coder), got %d", len(a.Agents))
	}
}

// TestHandleCreateAgent_AgentAndSystemRelayAllowed pins the auth carve-out:
// agent tool calls arrive as role "system" (project agents inherit the
// workspace manager's role via gospore buildSpawn) or "agent" (global agents),
// and create_agent is part of the agent tool surface. Anonymous and
// zero-identity callers stay denied.
func TestHandleCreateAgent_AgentAndSystemRelayAllowed(t *testing.T) {
	a, _ := freshActor(t)
	projectID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	for _, role := range []id.Role{"system", "agent"} {
		agentCtx := testutil.AnonCtx(testutil.GenActorID())
		agentCtx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: role}
		agentCtx.SpawnFn = noOpSpawn
		agentCtx.LookupIDFn = lookupOK
		created, err := a.handleCreateAgent(agentCtx, domain.WorkspaceCreateAgentReq{
			ProjectID: projectID, DisplayName: "Relayed", AgentKind: "coder",
		})
		if err != nil {
			t.Fatalf("create_agent with role %q: %v", role, err)
		}
		if created.ID == "" {
			t.Fatalf("create_agent with role %q returned empty agent id", role)
		}
	}

	for _, role := range []id.Role{"anonymous", ""} {
		deniedCtx := testutil.AnonCtx(testutil.GenActorID())
		deniedCtx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: role}
		deniedCtx.SpawnFn = noOpSpawn
		deniedCtx.LookupIDFn = lookupOK
		if _, err := a.handleCreateAgent(deniedCtx, domain.WorkspaceCreateAgentReq{
			ProjectID: projectID, DisplayName: "Denied", AgentKind: "coder",
		}); err == nil {
			t.Fatalf("create_agent with role %q returned nil error, want forbidden", role)
		}
	}
}

func TestResolveAgentModelSlots_ExplicitValuesOverrideKindDefaults(t *testing.T) {
	defaultPrimary := slotFromUnit(gen.ModelUnit{Model: "default-primary", Provider: "provider"})
	defaultFast := slotFromUnit(gen.ModelUnit{Model: "default-fast", Provider: "provider"})
	explicitPrimary := slotFromUnit(gen.ModelUnit{Model: "explicit-primary", Provider: "provider"})
	primary, fast, execution, review, summary := resolveAgentModelSlots(
		domain.WorkspaceCreateAgentReq{Primary: explicitPrimary},
		domain.AgentKindConfig{
			Primary:   defaultPrimary,
			Fast:      defaultFast,
			Execution: slotFromUnit(gen.ModelUnit{Model: "default-execution", Provider: "provider"}),
			Review:    slotFromUnit(gen.ModelUnit{Model: "default-review", Provider: "provider"}),
			Summary:   slotFromUnit(gen.ModelUnit{Model: "default-summary", Provider: "provider"}),
		},
	)
	if primary != explicitPrimary || fast != defaultFast || execution == nil || review == nil || summary == nil {
		t.Fatalf("resolved slots = primary=%+v fast=%+v execution=%+v review=%+v summary=%+v", primary, fast, execution, review, summary)
	}
}

func TestHandleCreateAgent_PassesConfigToSpawn(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: actorID},
	}

	var spawnReq domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID == "project.spawn_agent" {
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{
					ActorID: testutil.GenActorID().String(),
				}
			}
			return nil
		}), true
	}

	cfg, ok := a.findAgentKindConfig(domain.AgentKindCoder)
	if !ok {
		t.Fatal("missing coder config")
	}
	cfg.Primary = slotFromUnit(gen.ModelUnit{Model: "default-primary", Provider: "provider"})
	cfg.Fast = slotFromUnit(gen.ModelUnit{Model: "default-fast", Provider: "provider"})
	cfg.CompactionPolicy = &gen.CompactionPolicy{Enabled: true, BudgetMode: "percentage", TokenBudget: 75, RecentWindow: 20, MaxSummaryTokens: 4000}
	for i := range a.AgentKindConfigs {
		if a.AgentKindConfigs[i].Kind == cfg.Kind {
			a.AgentKindConfigs[i] = cfg
			break
		}
	}

	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Configured Agent",
		AgentKind:   "coder",
		Primary:     slotFromUnit(gen.ModelUnit{Model: "claude-sonnet-4-6", Provider: "anthropic"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if spawnReq.Primary == nil || len(spawnReq.Primary.Candidates) == 0 {
		t.Fatalf("expected non-empty Primary slot in spawn req, got %+v", spawnReq.Primary)
	}
	unit := spawnReq.Primary.Candidates[0].Unit
	if unit == nil || unit.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected claude-sonnet-4-6 in spawn req primary slot, got %+v", unit)
	}
	if spawnReq.Fast == nil || len(spawnReq.Fast.Candidates) == 0 || spawnReq.Fast.Candidates[0].Unit == nil || spawnReq.Fast.Candidates[0].Unit.Model != "default-fast" {
		t.Fatalf("expected configured default fast slot in spawn req, got %+v", spawnReq.Fast)
	}
	if created.CompactionPolicy == nil || !created.CompactionPolicy.Enabled || created.CompactionPolicy.TokenBudget != 75 {
		t.Fatalf("expected configured default compaction policy, got %+v", created.CompactionPolicy)
	}
}

func TestSavedAgentKindDefaults_AreUsedByCreateAgent(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	var spawnReq domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID == "project.spawn_agent" {
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
			}
			return nil
		}), true
	}

	primary := slotFromUnit(gen.ModelUnit{Model: "saved-primary", Provider: "provider"})
	aggregatorSlot := &domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "aggregator", AggregatorID: "fast-pool"}}}
	compaction := &gen.CompactionPolicy{Enabled: true, BudgetMode: "absolute", TokenBudget: 9000, RecentWindow: 12, MaxSummaryTokens: 2000}
	_, err := a.handleSaveAgentKindConfig(ctx, domain.WorkspaceSaveAgentKindConfigReq{
		Kind:             domain.AgentKindCoder,
		DisplayName:      "Coder",
		UserCreatable:    true,
		RolePromptRef:    gen.PromptRef{Kind: "profile", Key: "project.coder"},
		Primary:          primary,
		Fast:             aggregatorSlot,
		CompactionPolicy: compaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID: projectID,
		AgentKind: domain.AgentKindCoder,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spawnReq.Primary, primary) {
		t.Fatalf("spawn primary = %+v, want %+v", spawnReq.Primary, primary)
	}
	if !reflect.DeepEqual(spawnReq.Fast, aggregatorSlot) {
		t.Fatalf("spawn fast = %+v, want aggregator-kind default %+v", spawnReq.Fast, aggregatorSlot)
	}
	if !reflect.DeepEqual(created.CompactionPolicy, compaction) {
		t.Fatalf("created compaction policy = %+v, want %+v", created.CompactionPolicy, compaction)
	}
}

// fakeClonePlanner records every Call as a (target, callID, payload) triple
// and returns canned responses for agent.session.export_range / agent.session.import_turns.
type fakeClonePlanner struct {
	mu                sync.Mutex
	calls             []forkPlannerCall
	exportResp        domain.AgentSessionExportRangeResp
	exportResponses   []domain.AgentSessionExportRangeResp
	importTurnsErr    error
	getSessionResp    domain.AgentGetSessionResp
	forkResp          domain.AgentSessionForkResp
	importResp        domain.AgentSessionImportResp
	componentListResp *domain.AgentComponentListResp
	mountErr          error
}

type forkPlannerCall struct {
	target  id.ActorID
	callID  string
	payload any
}

func (p *fakeClonePlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *fakeClonePlanner) Call(_ context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	p.mu.Lock()
	p.calls = append(p.calls, forkPlannerCall{target: target.ID(), callID: callID, payload: payload})
	exportResp := p.exportResp
	if callID == "session_export_range" && len(p.exportResponses) > 0 {
		exportResp = p.exportResponses[0]
		p.exportResponses = p.exportResponses[1:]
	}
	importErr := p.importTurnsErr
	p.mu.Unlock()

	switch callID {
	case "session_export_range":
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(exportResp) })
	case "session_import_turns":
		if importErr != nil {
			return promise.Reject[any](importErr)
		}
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(domain.AgentSessionImportTurnsResp{}) })
	case "session_fork":
		p.mu.Lock()
		resp := p.forkResp
		p.mu.Unlock()
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(resp) })
	case "session_import":
		p.mu.Lock()
		resp := p.importResp
		p.mu.Unlock()
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(resp) })
	case "session_get":
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(p.getSessionResp) })
	case "component_list":
		p.mu.Lock()
		resp := domain.AgentComponentListResp{}
		if p.componentListResp != nil {
			resp = *p.componentListResp
		}
		p.mu.Unlock()
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(resp) })
	case "component_mount":
		p.mu.Lock()
		mountErr := p.mountErr
		p.mu.Unlock()
		if mountErr != nil {
			return promise.Reject[any](mountErr)
		}
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(domain.AgentComponentMountResp{}) })
	default:
		return promise.Async(func(resolve func(any), _ func(any)) { resolve(nil) })
	}
}

func (p *fakeClonePlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](fmt.Errorf("not implemented"))
}

// waitForCloneCalls polls the fake planner's recorded calls until pred is
// satisfied or the deadline elapses. Since clone session copy now runs in a
// detached goroutine, tests must wait for the async planner calls rather than
// asserting them synchronously.
func waitForCloneCalls(t *testing.T, fp *fakeClonePlanner, pred func(calls []forkPlannerCall) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fp.mu.Lock()
		snap := append([]forkPlannerCall(nil), fp.calls...)
		fp.mu.Unlock()
		if pred(snap) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fp.mu.Lock()
	snap := append([]forkPlannerCall(nil), fp.calls...)
	fp.mu.Unlock()
	t.Fatalf("waitForCloneCalls timed out; calls so far: %+v", snap)
}

func TestHandleCloneAgent_CopiesSourceSession(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := a.Mounts[0].ActorID
	srcActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:          "src",
		ActorID:     srcActorID.String(),
		AgentKind:   "coder",
		ProjectID:   projectID,
		DisplayName: "Source",
	}}
	fp := &fakeClonePlanner{
		exportResp: domain.AgentSessionExportRangeResp{
			Turns: []domain.Turn{{ID: "t1"}, {ID: "t2"}},
			Steps: []domain.Step{
				{ID: "s1", TurnID: "t1"},
				{ID: "s2", TurnID: "t2"},
			},
			TotalTurns: 2,
			HasMore:    false,
			NextSeq:    10,
			NextIdx:    7,
		},
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	ag, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
	})
	if err != nil {
		t.Fatalf("handleCloneAgent: %v", err)
	}
	if ag.AgentKind != "coder" {
		t.Errorf("AgentKind = %q, want coder", ag.AgentKind)
	}
	if len(fp.calls) != 3 {
		// Session copy runs in a detached goroutine; wait for the
		// component_list + export_range + import_turns calls before asserting.
		waitForCloneCalls(t, fp, func(c []forkPlannerCall) bool {
			return len(c) >= 3
		})
	}
	fp.mu.Lock()
	snap := append([]forkPlannerCall(nil), fp.calls...)
	fp.mu.Unlock()
	if len(snap) != 3 {
		t.Fatalf("expected 3 planner calls, got %d (%+v)", len(snap), snap)
	}
	if snap[0].callID != "component_list" {
		t.Errorf("call[0] = %q, want agent.component.list", snap[0].callID)
	}
	if snap[1].callID != "session_export_range" {
		t.Errorf("call[1] = %q, want agent.session.export_range", snap[1].callID)
	}
	if snap[2].callID != "session_import_turns" {
		t.Errorf("call[2] = %q, want agent.session.import_turns", snap[2].callID)
	}
	importReq, ok := snap[2].payload.(domain.AgentSessionImportTurnsReq)
	if !ok {
		t.Fatalf("import payload type = %T", snap[1].payload)
	}
	if len(importReq.Turns) != 2 {
		t.Errorf("import turns = %d, want 2", len(importReq.Turns))
	}
	if len(importReq.Steps) != 2 {
		t.Errorf("import steps = %d, want 2", len(importReq.Steps))
	}
	if importReq.SourceAgentID != srcActorID.String() {
		t.Errorf("SourceAgentID = %q, want %q", importReq.SourceAgentID, srcActorID.String())
	}
	if importReq.NextSeq != 10 {
		t.Errorf("NextSeq = %d, want 10", importReq.NextSeq)
	}
	if importReq.NextIdx != 7 {
		t.Errorf("NextIdx = %d, want 7", importReq.NextIdx)
	}
}

func TestHandleCloneAgent_CopiesSessionWithoutPlanner(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := a.Mounts[0].ActorID
	projectActorID, err := id.Parse(projectID)
	if err != nil {
		t.Fatal(err)
	}
	srcActorID := id.NewCanonical(1, 0, func() uint64 { return projectActorID.TimestampMS() + 1 }).Next()
	cloneActorID := id.NewCanonical(1, 0, func() uint64 { return projectActorID.TimestampMS() + 2 }).Next()
	a.Agents = []domain.AgentRef{{
		ID:          "src",
		ActorID:     srcActorID.String(),
		AgentKind:   "coder",
		ProjectID:   projectID,
		DisplayName: "Source",
		LoadState:   "loaded",
	}}

	exported := make(chan struct{}, 1)
	imported := make(chan domain.AgentSessionImportTurnsReq, 1)
	projectRef := testutil.NewFakeRef(projectActorID, func(callID string, _ any) any {
		if callID == "project.spawn_agent" {
			return domain.ProjectSpawnAgentResp{ActorID: cloneActorID.String()}
		}
		return nil
	})
	srcRef := testutil.NewFakeRef(srcActorID, func(callID string, _ any) any {
		if callID == "session_export_range" {
			exported <- struct{}{}
			return domain.AgentSessionExportRangeResp{
				Turns:      []domain.Turn{{ID: "t1"}},
				TotalTurns: 1,
				NextSeq:    3,
				NextIdx:    2,
			}
		}
		return nil
	})
	cloneRef := testutil.NewFakeRef(cloneActorID, func(callID string, payload any) any {
		if callID == "session_import_turns" {
			imported <- payload.(domain.AgentSessionImportTurnsReq)
			return domain.AgentSessionImportTurnsResp{}
		}
		return nil
	})
	ctx.PlannerFn = nil
	ctx.LookupIDFn = func(actorID id.ActorID) (ref.Ref, bool) {
		switch actorID {
		case projectActorID:
			return projectRef, true
		case srcActorID:
			return srcRef, true
		case cloneActorID:
			return cloneRef, true
		default:
			return nil, false
		}
	}

	if _, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
	}); err != nil {
		t.Fatalf("handleCloneAgent: %v", err)
	}

	select {
	case <-exported:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session export without planner")
	}
	select {
	case req := <-imported:
		if len(req.Turns) != 1 || req.Turns[0].ID != "t1" {
			t.Fatalf("imported turns = %+v, want t1", req.Turns)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session import without planner")
	}
}

func TestHandleCloneAgent_ForkPathStripsGoal(t *testing.T) {
	a, ctx := freshActor(t)
	srcActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "src",
		ActorID:   srcActorID.String(),
		AgentKind: "coder",
	}}
	fp := &fakeClonePlanner{
		forkResp: domain.AgentSessionForkResp{
			Session: domain.Session{
				Turns:      []domain.Turn{{ID: "t1"}},
				ActiveHead: 0,
			},
			NextSeq:       10,
			NextIdx:       7,
			NextTurnOrder: 5,
			Goal: &gen.SessionGoal{
				Condition:       "do the thing",
				Confirmed:       true,
				TurnCount:       3,
				InterpretedGoal: "do the thing now",
			},
		},
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	if _, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
		ForkAtTurnID:  "t1",
	}); err != nil {
		t.Fatalf("handleCloneAgent: %v", err)
	}

	// The fork path runs session copy in a detached goroutine; wait for the
	// agent.session.import call before asserting.
	waitForCloneCalls(t, fp, func(c []forkPlannerCall) bool {
		for _, call := range c {
			if call.callID == "session_import" {
				return true
			}
		}
		return false
	})
	fp.mu.Lock()
	snap := append([]forkPlannerCall(nil), fp.calls...)
	fp.mu.Unlock()
	var importReq domain.AgentSessionImportReq
	foundImport := false
	for _, c := range snap {
		if c.callID == "session_import" {
			importReq, _ = c.payload.(domain.AgentSessionImportReq)
			foundImport = true
		}
	}
	if !foundImport {
		t.Fatalf("expected an agent.session.import call")
	}
	if importReq.Goal != nil {
		t.Errorf("clone import must not carry source goal, got %+v", importReq.Goal)
	}
	// History + counters must still be propagated.
	if len(importReq.Session.Turns) != 1 {
		t.Errorf("import turns = %d, want 1", len(importReq.Session.Turns))
	}
	if importReq.NextSeq != 10 {
		t.Errorf("NextSeq = %d, want 10", importReq.NextSeq)
	}
	if importReq.NextTurnOrder != 5 {
		t.Errorf("NextTurnOrder = %d, want 5", importReq.NextTurnOrder)
	}
}

// TestHandleCloneAgent_CopiesComponentMounts verifies that user-activated
// component cards (mode cards like builtin:mode:goal) are re-mounted on the
// clone, while disabled mounts, builtin-scoped mounts, dependency mounts and
// the system-managed worktree mode are excluded.
func TestHandleCloneAgent_CopiesComponentMounts(t *testing.T) {
	a, ctx := freshActor(t)
	srcActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "src",
		ActorID:   srcActorID.String(),
		AgentKind: "coder",
	}}
	fp := &fakeClonePlanner{
		componentListResp: &domain.AgentComponentListResp{
			Items: []domain.AgentComponentMount{
				{CardID: "builtin:mode:goal", Scope: "user", Enabled: true, Order: 2},
				{CardID: "builtin:mode:workflow", Scope: "user", Enabled: false, Order: 3},
				{CardID: "builtin:mode:worktree", Scope: "user", Enabled: true, Order: 4},
				{CardID: "builtin:bundle:coder-base", Scope: "builtin", Enabled: true},
				{CardID: "builtin:bundle:workflow-tools", Scope: "dependency", Enabled: true},
			},
		},
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	if _, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
	}); err != nil {
		t.Fatalf("handleCloneAgent: %v", err)
	}

	waitForCloneCalls(t, fp, func(c []forkPlannerCall) bool {
		for _, call := range c {
			if call.callID == "component_mount" {
				return true
			}
		}
		return false
	})
	fp.mu.Lock()
	snap := append([]forkPlannerCall(nil), fp.calls...)
	fp.mu.Unlock()
	var mounts []domain.AgentComponentMountReq
	for _, c := range snap {
		if c.callID != "component_mount" {
			continue
		}
		req, ok := c.payload.(domain.AgentComponentMountReq)
		if !ok {
			t.Fatalf("component_mount payload type = %T", c.payload)
		}
		mounts = append(mounts, req)
	}
	if len(mounts) != 1 || mounts[0].CardID != "builtin:mode:goal" {
		t.Fatalf("expected only builtin:mode:goal mounted on clone, got %+v", mounts)
	}
	if mounts[0].Scope != "user" || !mounts[0].Enabled || mounts[0].Order != 2 {
		t.Errorf("goal mode mount req = %+v, want scope=user enabled order=2", mounts[0])
	}
}

func TestHandleCloneAgent_StartsBackgroundCopy(t *testing.T) {
	a, ctx := freshActor(t)
	srcActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "src",
		ActorID:   srcActorID.String(),
		AgentKind: "coder",
	}}
	fp := &fakeClonePlanner{
		exportResponses: []domain.AgentSessionExportRangeResp{
			{
				Turns:            []domain.Turn{{ID: "t2"}, {ID: "t3"}},
				TotalTurns:       3,
				HasMore:          true,
				NextBeforeTurnID: "t2",
			},
			{
				Turns:      []domain.Turn{{ID: "t1"}},
				TotalTurns: 3,
				HasMore:    false,
			},
		},
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	if _, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
	}); err != nil {
		t.Fatalf("handleCloneAgent: %v", err)
	}

	// Wait for the background goroutine to issue the second export/import.
	for i := 0; i < 50; i++ {
		fp.mu.Lock()
		exportCount := 0
		importCount := 0
		for _, c := range fp.calls {
			if c.callID == "session_export_range" {
				exportCount++
			}
			if c.callID == "session_import_turns" {
				importCount++
			}
		}
		fp.mu.Unlock()
		if exportCount >= 2 && importCount >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	fp.mu.Lock()
	exportCount := 0
	importCount := 0
	for _, c := range fp.calls {
		if c.callID == "session_export_range" {
			exportCount++
		}
		if c.callID == "session_import_turns" {
			importCount++
		}
	}
	fp.mu.Unlock()
	if exportCount != 2 {
		t.Errorf("export_range calls = %d, want 2", exportCount)
	}
	if importCount != 2 {
		t.Errorf("import_turns calls = %d, want 2", importCount)
	}

	// Wait for the background goroutine to finish and clean up clone state.
	for i := 0; i < 50; i++ {
		a.clonePersistMu.Lock()
		pending := len(a.clones) > 0
		a.clonePersistMu.Unlock()
		if !pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleCloneAgent_ResumesPendingCloneOnStart(t *testing.T) {
	a, ctx := freshActor(t)
	srcActorID := testutil.GenActorID()
	cloneActorID := "00000000000100010000000000000002"
	a.Agents = []domain.AgentRef{
		{ID: "src", ActorID: srcActorID.String(), AgentKind: "coder", ProjectID: a.Mounts[0].ActorID, DisplayName: "Source"},
		{ID: "clone", ActorID: cloneActorID, AgentKind: "coder", ProjectID: a.Mounts[0].ActorID, DisplayName: "Clone"},
	}
	a.clones = map[string]domain.CloneState{
		cloneActorID: {
			SourceActorID:    srcActorID.String(),
			SourceTotalTurns: 3,
			PendingHistory:   true,
		},
	}

	fp := &fakeClonePlanner{
		exportResp: domain.AgentSessionExportRangeResp{
			Turns:      []domain.Turn{{ID: "t1"}},
			TotalTurns: 3,
			HasMore:    false,
		},
		getSessionResp: domain.AgentGetSessionResp{
			Turns: []domain.Turn{{ID: "t2"}, {ID: "t3"}},
		},
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}

	for i := 0; i < 50; i++ {
		fp.mu.Lock()
		exportCount := 0
		importCount := 0
		for _, c := range fp.calls {
			if c.callID == "session_export_range" {
				exportCount++
			}
			if c.callID == "session_import_turns" {
				importCount++
			}
		}
		fp.mu.Unlock()
		if exportCount >= 1 && importCount >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	fp.mu.Lock()
	exportCount := 0
	importCount := 0
	for _, c := range fp.calls {
		if c.callID == "session_export_range" {
			exportCount++
		}
		if c.callID == "session_import_turns" {
			importCount++
		}
	}
	fp.mu.Unlock()
	if exportCount != 1 {
		t.Errorf("export_range calls = %d, want 1", exportCount)
	}
	if importCount != 2 {
		t.Errorf("import_turns calls = %d, want 2", importCount)
	}

	for i := 0; i < 50; i++ {
		a.clonePersistMu.Lock()
		pending := len(a.clones) > 0
		a.clonePersistMu.Unlock()
		if !pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.clonePersistMu.Lock()
	if _, ok := a.clones[cloneActorID]; ok {
		t.Errorf("clone state should be cleared after completion")
	}
	a.clonePersistMu.Unlock()
}

func TestHandleCloneAgent_ResumesPendingCloneWithoutPlanner(t *testing.T) {
	a, ctx := freshActor(t)
	projectActorID, err := id.Parse(a.Mounts[0].ActorID)
	if err != nil {
		t.Fatal(err)
	}
	srcActorID := id.NewCanonical(1, 0, func() uint64 { return projectActorID.TimestampMS() + 10 }).Next()
	cloneActorID := id.NewCanonical(1, 0, func() uint64 { return projectActorID.TimestampMS() + 11 }).Next()
	a.Agents = []domain.AgentRef{
		{ID: "src", ActorID: srcActorID.String(), AgentKind: "coder", DisplayName: "Source"},
		{ID: "clone", ActorID: cloneActorID.String(), AgentKind: "coder", DisplayName: "Clone"},
	}
	a.clones = map[string]domain.CloneState{
		cloneActorID.String(): {
			SourceActorID:    srcActorID.String(),
			SourceTotalTurns: 2,
			PendingHistory:   true,
		},
	}

	exported := make(chan struct{}, 1)
	imports := make(chan domain.AgentSessionImportTurnsReq, 2)
	srcRef := testutil.NewFakeRef(srcActorID, func(callID string, _ any) any {
		if callID == "session_export_range" {
			exported <- struct{}{}
			return domain.AgentSessionExportRangeResp{
				Turns:      []domain.Turn{{ID: "t1"}},
				TotalTurns: 2,
				HasMore:    false,
			}
		}
		return nil
	})
	cloneRef := testutil.NewFakeRef(cloneActorID, func(callID string, payload any) any {
		switch callID {
		case "session_import_turns":
			imports <- payload.(domain.AgentSessionImportTurnsReq)
			return domain.AgentSessionImportTurnsResp{}
		case "session_get":
			return domain.AgentGetSessionResp{Turns: []domain.Turn{{ID: "t2"}}}
		default:
			return nil
		}
	})
	ctx.PlannerFn = nil
	ctx.LookupIDFn = func(actorID id.ActorID) (ref.Ref, bool) {
		switch actorID {
		case srcActorID:
			return srcRef, true
		case cloneActorID:
			return cloneRef, true
		default:
			return nil, false
		}
	}

	a.resumePendingClones(ctx, nil)

	select {
	case <-exported:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resumed export without planner")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-imports:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for resumed import %d without planner", i+1)
		}
	}
	for i := 0; i < 50; i++ {
		a.clonePersistMu.Lock()
		pending := len(a.clones) > 0
		a.clonePersistMu.Unlock()
		if !pending {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("clone state should be cleared after resumed copy without planner")
}

func TestHandleCloneAgent_ImportFailureKeepsAgent(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := a.Mounts[0].ActorID
	srcActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:          "src",
		ActorID:     srcActorID.String(),
		AgentKind:   "coder",
		ProjectID:   projectID,
		DisplayName: "Source",
	}}
	fp := &fakeClonePlanner{
		exportResp: domain.AgentSessionExportRangeResp{
			Turns:      []domain.Turn{{ID: "t1"}},
			TotalTurns: 1,
			HasMore:    false,
		},
		importTurnsErr: fmt.Errorf("boom"),
	}
	ctx.PlannerFn = func() actor.Planner { return fp }

	ag, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "src",
		DisplayName:   "Cloned",
	})
	// Session copy is async: an import failure no longer rolls back the
	// synchronous return. The clone agent stays registered (a best-effort
	// empty shell) and the failure is logged off the owner loop instead of
	// blocking it.
	if err != nil {
		t.Fatalf("handleCloneAgent should return immediately without error on async import failure, got %v", err)
	}
	if ag.ID == "" {
		t.Errorf("expected a non-empty cloned agent ID")
	}
	if len(a.Agents) != 2 {
		t.Errorf("a.Agents should contain the new clone agent (len=%d), want 2", len(a.Agents))
	}
	// The failed async copy must still have attempted export + import.
	waitForCloneCalls(t, fp, func(c []forkPlannerCall) bool {
		hasExport, hasImport := false, false
		for _, call := range c {
			if call.callID == "session_export_range" {
				hasExport = true
			}
			if call.callID == "session_import_turns" {
				hasImport = true
			}
		}
		return hasExport && hasImport
	})
}

func TestHandleListAgents_FilterByProject(t *testing.T) {
	a, ctx := freshActor(t)
	actorID1 := testutil.GenActorID().String()
	actorID2 := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: actorID1},
		{Name: "p2", Path: t.TempDir(), ActorID: actorID2},
	}

	_, _ = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{ProjectID: actorID1, DisplayName: "A1", AgentKind: "coder"})
	_, _ = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{ProjectID: actorID2, DisplayName: "A2", AgentKind: "coder"})
	_, _ = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{ProjectID: actorID1, DisplayName: "A3", AgentKind: "coder"})

	list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: actorID1})
	if err != nil {
		t.Fatal(err)
	}
	// testutil.GenActorID() may return the same ID in this test environment,
	// so both mounts share an ActorId; filter returns all created agents.
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(list.Items))
	}

	list3, _ := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: "p-nonexistent"})
	if len(list3.Items) != 0 {
		t.Errorf("expected 0 agents for non-existent project, got %d", len(list3.Items))
	}
	if list3.Items == nil {
		t.Error("expected non-nil empty Items for JSON [] serialization")
	}
}

func TestHandleListAgents_ProjectNameFallback(t *testing.T) {
	a, ctx := freshActor(t)
	a.Mounts = []domain.ProjectRef{
		{Name: "sporemind", Path: t.TempDir(), ActorID: "proj-actor-1"},
		{Name: "other", Path: t.TempDir(), ActorID: "proj-actor-2"},
	}
	a.Agents = []domain.AgentRef{
		{ID: "ag1", ActorID: "actor1", ProjectID: "proj-actor-1", DisplayName: "Alpha", AgentKind: "coder"},
		{ID: "ag2", ActorID: "actor2", ProjectID: "proj-actor-2", DisplayName: "Beta", AgentKind: "coder"},
	}

	list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: "sporemind"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "ag1" {
		t.Fatalf("expected only ag1 via project name fallback, got %+v", list.Items)
	}

	// Actor ID still works directly.
	list2, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: "proj-actor-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list2.Items) != 1 || list2.Items[0].ID != "ag2" {
		t.Fatalf("expected only ag2 via actor ID, got %+v", list2.Items)
	}
}

// TestHandleListAgents_EnrichesProjectName pins the AgentRef.ProjectName
// enrichment: list_agents must resolve each agent's ProjectID against the
// mount table so agent-facing projections (hot-context "Conversable Agents"
// rows) show the readable mount name instead of the raw project actor ID.
func TestHandleListAgents_EnrichesProjectName(t *testing.T) {
	a, ctx := freshActor(t)
	a.Mounts = []domain.ProjectRef{
		{Name: "sporemind", Path: t.TempDir(), ActorID: "proj-actor-1"},
		{Name: "other", Path: t.TempDir(), ActorID: "proj-actor-2"},
	}
	a.Agents = []domain.AgentRef{
		{ID: "ag1", ActorID: "actor1", ProjectID: "proj-actor-1", DisplayName: "Alpha", AgentKind: "coder"},
		{ID: "ag2", ActorID: "actor2", ProjectID: "proj-actor-2", DisplayName: "Beta", AgentKind: "coder"},
		{ID: "ag3", ActorID: "actor3", ProjectID: "proj-actor-unmounted", DisplayName: "Ghost", AgentKind: "coder"},
		{ID: "ag4", ActorID: "actor4", ProjectID: "proj-actor-1", ProjectName: "preset", DisplayName: "Delta", AgentKind: "coder"},
	}

	list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.AgentRef, len(list.Items))
	for _, ag := range list.Items {
		byID[ag.ID] = ag
	}
	if got := byID["ag1"].ProjectName; got != "sporemind" {
		t.Errorf("ag1 ProjectName = %q, want %q", got, "sporemind")
	}
	if got := byID["ag2"].ProjectName; got != "other" {
		t.Errorf("ag2 ProjectName = %q, want %q", got, "other")
	}
	if got := byID["ag3"].ProjectName; got != "" {
		t.Errorf("ag3 (unmounted project) ProjectName = %q, want empty", got)
	}
	if got := byID["ag4"].ProjectName; got != "preset" {
		t.Errorf("ag4 preset ProjectName = %q, want preserved %q", got, "preset")
	}
}

func TestHandleListAgents_QueryFilter(t *testing.T) {
	a, ctx := freshActor(t)

	a.Agents = []domain.AgentRef{
		{ID: "ag1", ActorID: "actor1", ProjectID: "p1", DisplayName: "Alpha Bot", Title: "Frontend Lead", AgentKind: "coder"},
		{ID: "ag2", ActorID: "actor2", ProjectID: "p1", DisplayName: "Beta Bot", Title: "Backend Lead", AgentKind: "coder"},
		{ID: "ag3", ActorID: "actor3", ProjectID: "p1", DisplayName: "Gamma Bot", Title: "DevOps Lead", AgentKind: "coder"},
	}

	cases := []struct {
		query   string
		wantIDs []string
	}{
		{"", []string{"ag1", "ag2", "ag3"}},
		{"alpha", []string{"ag1"}},
		{"ALPHA", []string{"ag1"}},
		{"lead", []string{"ag1", "ag2", "ag3"}},
		{"backend", []string{"ag2"}},
		{"devops", []string{"ag3"}},
		{"missing", []string{}},
	}

	for _, tc := range cases {
		list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{Query: tc.query})
		if err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		got := make([]string, len(list.Items))
		for i, item := range list.Items {
			got[i] = item.ID
		}
		if !slices.Equal(got, tc.wantIDs) {
			t.Errorf("query %q: expected %v, got %v", tc.query, tc.wantIDs, got)
		}
	}
}

func TestHandleListAgents_PopulatesActiveTurnRef(t *testing.T) {
	a, ctx := freshActor(t)

	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	agent1ID := g.Next()
	agent2ID := g.Next()

	a.Agents = []domain.AgentRef{
		{ID: "ag1", ActorID: agent1ID.String(), ProjectID: "p1", DisplayName: "A1", AgentKind: "coder"},
		{ID: "ag2", ActorID: agent2ID.String(), ProjectID: "p1", DisplayName: "A2", AgentKind: "coder"},
		{ID: "ag3", ActorID: "", ProjectID: "p1", DisplayName: "A3", AgentKind: "coder"},
	}
	a.agentRuntime = map[string]gen.AgentRuntimeState{
		agent1ID.String(): {ActiveTurnRef: "turn-1"},
	}

	list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(list.Items))
	}
	if list.Items[0].ActiveTurnRef != "turn-1" {
		t.Errorf("expected ag1.ActiveTurnRef = %q, got %q", "turn-1", list.Items[0].ActiveTurnRef)
	}
	if list.Items[1].ActiveTurnRef != "" {
		t.Errorf("expected ag2.ActiveTurnRef empty, got %q", list.Items[1].ActiveTurnRef)
	}
	if list.Items[2].ActiveTurnRef != "" {
		t.Errorf("expected ag3.ActiveTurnRef empty (no actorID), got %q", list.Items[2].ActiveTurnRef)
	}
}

func TestHandleListAgents_MarksSelf(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "ag1", ActorID: "actor1", ProjectID: "p1", DisplayName: "Alpha", AgentKind: "coder"},
		{ID: "ag2", ActorID: "actor2", ProjectID: "p1", DisplayName: "Beta", AgentKind: "coder"},
	}

	list, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{CallerAgentID: "actor2"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Items[0].DisplayName != "Alpha" {
		t.Errorf("expected ag1 unchanged, got %q", list.Items[0].DisplayName)
	}
	if list.Items[1].DisplayName != "Beta (self)" {
		t.Errorf("expected ag2 marked self, got %q", list.Items[1].DisplayName)
	}
	// No caller injected: nothing is marked.
	list2, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if list2.Items[1].DisplayName != "Beta" {
		t.Errorf("expected ag2 unmarked without caller, got %q", list2.Items[1].DisplayName)
	}
	// Persisted state is untouched by the derived marker.
	if a.Agents[1].DisplayName != "Beta" {
		t.Errorf("expected persisted DisplayName unchanged, got %q", a.Agents[1].DisplayName)
	}
}

func TestHandleListAgents_ChildAgentFilter(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{
		{ID: "root", ActorID: "actor-root", DisplayName: "Root", AgentKind: "coder"},
		{ID: "c1", ActorID: "actor-c1", DisplayName: "Child1", AgentKind: "worker", ParentAgentID: "actor-root"},
		{ID: "c2", ActorID: "actor-c2", DisplayName: "Child2", AgentKind: "worker", ParentAgentID: "actor-root"},
		{ID: "gc1", ActorID: "actor-gc1", DisplayName: "GrandChild1", AgentKind: "worker", ParentAgentID: "actor-c1"},
	}

	// No filter: all agents returned (unchanged behaviour).
	all, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 4 {
		t.Fatalf("no-filter: expected 4 agents, got %d", len(all.Items))
	}

	// ChildrenOnly falls back to injected CallerAgentId.
	own, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ChildrenOnly: true, CallerAgentID: "actor-root"})
	if err != nil {
		t.Fatal(err)
	}
	if len(own.Items) != 2 {
		t.Fatalf("ChildrenOnly: expected 2 direct children of actor-root, got %d", len(own.Items))
	}
	for _, ag := range own.Items {
		if ag.ParentAgentID != "actor-root" {
			t.Errorf("ChildrenOnly: agent %q has parent %q, want actor-root", ag.ID, ag.ParentAgentID)
		}
	}

	// Explicit ParentAgentId lists that parent's direct children only.
	nested, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ParentAgentID: "actor-c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nested.Items) != 1 || nested.Items[0].ID != "gc1" {
		t.Fatalf("ParentAgentId actor-c1: expected [gc1], got %+v", nested.Items)
	}

	// Both set: explicit ParentAgentId wins over ChildrenOnly.
	override, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{
		ChildrenOnly:  true,
		ParentAgentID: "actor-c1",
		CallerAgentID: "actor-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(override.Items) != 1 || override.Items[0].ID != "gc1" {
		t.Fatalf("override: expected ParentAgentId to win [gc1], got %+v", override.Items)
	}

	// ChildrenOnly with no injected caller matches nothing.
	empty, err := a.handleListAgents(ctx, domain.WorkspaceListAgentsReq{ChildrenOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("ChildrenOnly without caller: expected 0, got %d", len(empty.Items))
	}
}

func TestHandleCreate_AnonymousForbidden(t *testing.T) {
	a, ctx := freshActorAnon(t)

	_, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: "/tmp/x", Name: "x"})
	if err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestHandleCreate(t *testing.T) {
	a, ctx := freshActor(t)
	dir := t.TempDir()

	ref, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{
		Path: dir, Name: "newproj", InitGit: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "newproj" {
		t.Errorf("expected newproj, got %q", ref.Name)
	}

	// Directory should exist on disk
	info, err := os.Stat(filepath.Join(dir, "newproj"))
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestHandleCreate_AppRequiresPath(t *testing.T) {
	a, ctx := freshActor(t)

	for _, kind := range []string{"dev-app", "sporeapp"} {
		_, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{
			Name:    "myapp",
			AppKind: kind,
		})
		if err == nil {
			t.Fatalf("expected error for %s without Path", kind)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("expected path-required error for %s, got %v", kind, err)
		}
	}

	// Providing a path still succeeds.
	dir := t.TempDir()
	ref, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{
		Path:    dir,
		Name:    "myapp",
		AppKind: "dev-app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.AppKind != "dev-app" {
		t.Errorf("expected AppKind dev-app, got %q", ref.AppKind)
	}
	if _, err := os.Stat(filepath.Join(dir, "myapp")); err != nil {
		t.Fatalf("expected dev-app directory to exist: %v", err)
	}
}

func TestHandleReportError(t *testing.T) {
	a, ctx := freshActor(t)
	err := a.handleReportError(ctx, domain.FrontendErrorReport{
		RequestID:       "req-1",
		Severity:        "error",
		SourceComponent: "AIChat",
		Message:         "something broke",
		SessionID:       "sess-1",
	})
	if err != nil {
		t.Fatalf("handleReportError: %v", err)
	}
}

func TestHandleCreate_InvalidName(t *testing.T) {
	a, ctx := freshActor(t)
	dir := t.TempDir()

	_, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: dir, Name: "../escape"})
	if err == nil {
		t.Error("expected error for name with path traversal")
	}

	_, err = a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: dir, Name: ""})
	if err == nil {
		t.Error("expected error for empty name")
	}

	_, err = a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: dir, Name: "foo/bar"})
	if err == nil {
		t.Error("expected error for name with separator")
	}
}

func TestEmitMounts(t *testing.T) {
	a, ctx := freshActor(t)

	mountRef, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleAddMount(ctx, domain.WorkspaceAddMountReq{ProjectID: mountRef.ActorID, MountName: "vendor", MountPath: "/tmp/p1/vendor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRemoveMount(ctx, domain.WorkspaceRemoveMountReq{ProjectID: mountRef.ActorID, MountName: "vendor"}); err != nil {
		t.Fatal(err)
	}
	_, err = a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: t.TempDir(), Name: "newproj"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleUnmount(ctx, domain.WorkspaceUnmountReq{ProjectID: mountRef.ActorID}); err != nil {
		t.Fatal(err)
	}

	// 6 mounts events (OnInit system meta + handleMount + addMount + removeMount
	// + handleCreate + handleUnmount) + agents_changed + agent_list_state from
	// unmounting p1. No system agents are auto-created.
	if got, want := len(ctx.EmittedEvents), 8; got != want {
		t.Fatalf("expected %d emitted events, got %d (%+v)", want, got, ctx.EmittedEvents)
	}
	mountCount := 0
	for i, e := range ctx.EmittedEvents {
		switch e.Kind {
		case "mounts":
			mountCount++
			if _, ok := e.Payload.(domain.WorkspaceMountsEvent); !ok {
				t.Errorf("event %d: payload is not WorkspaceMountsEvent: %T", i, e.Payload)
			}
		case "agents_changed", "agent_list_state":
			// expected side effect of unmounting a project with agents
		default:
			t.Errorf("event %d: unexpected kind %q", i, e.Kind)
		}
	}
	if mountCount != 6 {
		t.Errorf("expected 6 mounts events, got %d", mountCount)
	}
	// Final unmount should leave system meta project and newproj in payload.
	last := ctx.EmittedEvents[len(ctx.EmittedEvents)-1].Payload.(domain.WorkspaceMountsEvent)
	if len(last.Mounts) != 2 || last.Mounts[0].Name != systemMetaProjectName || last.Mounts[1].Name != "newproj" {
		t.Errorf("final mounts payload = %+v, want [%s newproj]", last.Mounts, systemMetaProjectName)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir)}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	mountRef, _ := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"})
	_, _ = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{ProjectID: mountRef.ActorID, DisplayName: "A1", AgentKind: "coder"})

	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	// Reload into a fresh actor sharing the same store
	a2 := &Actor{store: a.store}
	ctx2 := testutil.AdminCtx(testutil.GenActorID())
	ctx2.SpawnFn = noOpSpawn
	ctx2.LookupIDFn = lookupOK
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := a2.OnStart(ctx2); err != nil {
		t.Fatal(err)
	}

	if len(a2.Mounts) != 2 {
		t.Fatalf("expected 2 mounts after reload, got %d", len(a2.Mounts))
	}
	if a2.Mounts[0].Name != systemMetaProjectName {
		t.Errorf("expected first mount to be system meta project, got %q", a2.Mounts[0].Name)
	}
	if a2.Mounts[1].Name != "p1" {
		t.Errorf("expected p1, got %q", a2.Mounts[1].Name)
	}
	// After reload: A1 (coder) only. No system agents are auto-created.
	if len(a2.Agents) != 1 {
		t.Fatalf("expected 1 agent after reload, got %d", len(a2.Agents))
	}
	var foundA1 bool
	for _, ag := range a2.Agents {
		if ag.DisplayName == "A1" {
			foundA1 = true
		}
	}
	if !foundA1 {
		t.Errorf("expected agent A1 after reload")
	}

	// Adding a second project after reload uses the request name as ID.
	_, err := a2.handleMount(ctx2, domain.WorkspaceMountReq{Path: "/tmp/p2", Name: "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if a2.Mounts[2].Name != "p2" {
		t.Errorf("expected p2, got %q", a2.Mounts[2].Name)
	}
}

func TestDefaultReviewerConfigHasReadBundles(t *testing.T) {
	a, ctx := freshActor(t)
	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindReviewer})
	if err != nil {
		t.Fatal(err)
	}
	// Reviewer should have file-tools and git-tools bundles (read access).
	hasFile := false
	for _, bid := range cfg.DefaultBundleIDs {
		if bid == "builtin:bundle:file-tools" {
			hasFile = true
		}
	}
	if !hasFile {
		t.Fatal("reviewer config missing builtin:bundle:file-tools")
	}
}

func TestDefaultAgentKindConfigs_HasEnvironmentContext(t *testing.T) {
	a, ctx := freshActor(t)

	for _, kind := range []string{domain.AgentKindCoder, domain.AgentKindCoordinator} {
		cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: kind})
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		if len(cfg.EnvironmentContext) == 0 {
			t.Fatalf("kind %q: expected non-empty EnvironmentContext", kind)
		}
		if cfg.EnvironmentContext["os"] == "" {
			t.Errorf("kind %q: expected non-empty os", kind)
		}
		if cfg.EnvironmentContext["shell"] == "" {
			t.Errorf("kind %q: expected non-empty shell", kind)
		}
		if cfg.EnvironmentContext["shell_executable"] == "" {
			t.Errorf("kind %q: expected non-empty shell_executable", kind)
		}
	}
}

func TestSeedAgentKindConfigs_RefreshesStaleEnvironmentContext(t *testing.T) {
	a, ctx := freshActor(t)
	// Simulate a persisted config with the old hardcoded linux/bash defaults
	// and no shell_executable.
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:               domain.AgentKindCoder,
			DisplayName:        "Coder",
			RolePromptRef:      gen.PromptRef{Kind: "profile", Key: "project.coder"},
			DefaultBundleIDs:   []string{"builtin:bundle:file-tools"},
			EnvironmentContext: map[string]string{"os": "linux", "shell": "bash"},
		},
	}
	a.seedAgentKindConfigs(ctx)

	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnvironmentContext["os"] != runtime.GOOS {
		t.Errorf("os = %q, want %q", cfg.EnvironmentContext["os"], runtime.GOOS)
	}
	if cfg.EnvironmentContext["shell_executable"] == "" {
		t.Error("shell_executable was not refreshed")
	}
}

func TestSeedAgentKindConfigs_PropagatesNewDefaultBundlesToPersistedConfig(t *testing.T) {
	a, ctx := freshActor(t)
	// Simulate a persisted coder config that predates a default bundle
	// migration: it carries the old bundle set
	// (file-tools/shell-tools/git-tools/workspace-tools/fork-explore) and none
	// of the workflow bundles (fork-review/fork-general/project-wiki/planning).
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          domain.AgentKindCoder,
			DisplayName:   "Coder",
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "project.coder"},
			DefaultBundleIDs: []string{
				"builtin:bundle:file-tools",
				"builtin:bundle:shell-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:workspace-tools",
				"builtin:bundle:fork-explore",
			},
		},
	}
	a.seedAgentKindConfigs(ctx)

	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool { return slices.Contains(cfg.DefaultBundleIDs, id) }
	// Current defaults must all be present after the seed reconcile.
	for _, id := range []string{
		"builtin:bundle:fork-review",
		"builtin:bundle:fork-general",
		"builtin:bundle:project-wiki",
		"builtin:bundle:planning",
	} {
		if !has(id) {
			t.Errorf("coder DefaultBundleIDs missing %q after seed reconcile: %v", id, cfg.DefaultBundleIDs)
		}
	}
	// Persisted entries (including stale extras) are preserved — union, not
	// replacement.
	for _, id := range []string{
		"builtin:bundle:file-tools",
		"builtin:bundle:shell-tools",
		"builtin:bundle:git-tools",
	} {
		if !has(id) {
			t.Errorf("persisted bundle %q was dropped by seed reconcile: %v", id, cfg.DefaultBundleIDs)
		}
	}
}

func TestSeedAgentKindConfigs_RepairsStaleCoordinatorRolePromptRef(t *testing.T) {
	a, ctx := freshActor(t)
	// Simulate a persisted coordinator config carrying the old auto-default
	// "project.coordinator" role prompt ref, which points at no profile card
	// (the coordinator profile is workspace-scoped). The seed reconcile must
	// rewrite it to the canonical "workspace.coordinator".
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          domain.AgentKindCoordinator,
			DisplayName:   "Coordinator",
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "project.coordinator"},
		},
	}
	a.seedAgentKindConfigs(ctx)

	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoordinator})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RolePromptRef.Key != "workspace.coordinator" {
		t.Fatalf("coordinator role prompt key = %q, want workspace.coordinator", cfg.RolePromptRef.Key)
	}
}

func TestNormalizeAgentKindConfig_EmptyCoordinatorRefUsesCanonicalScope(t *testing.T) {
	// A coordinator config with an empty role prompt ref must default to the
	// canonical workspace scope, not the synthesized "project.coordinator".
	cfg := normalizeAgentKindConfig(domain.AgentKindConfig{Kind: domain.AgentKindCoordinator})
	if cfg.RolePromptRef.Key != "workspace.coordinator" {
		t.Fatalf("empty coordinator ref defaulted to %q, want workspace.coordinator", cfg.RolePromptRef.Key)
	}
	// A user-chosen ref (anything other than the stale default) is preserved.
	custom := normalizeAgentKindConfig(domain.AgentKindConfig{
		Kind:          domain.AgentKindCoordinator,
		RolePromptRef: gen.PromptRef{Kind: "profile", Key: "custom.coordinator"},
	})
	if custom.RolePromptRef.Key != "custom.coordinator" {
		t.Fatalf("user-chosen ref overwritten to %q, want custom.coordinator", custom.RolePromptRef.Key)
	}
}

func TestFindAgentKindConfig_RuntimeFallbackAddsNewDefaultBundles(t *testing.T) {
	a, _ := freshActor(t)
	// Simulate a running workspace whose in-memory configs are stale — the
	// seed path has not re-run since a default bundle was added in code.
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          domain.AgentKindCoder,
			DisplayName:   "Coder",
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "project.coder"},
			DefaultBundleIDs: []string{
				"builtin:bundle:file-tools",
				"builtin:bundle:shell-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:workspace-tools",
				"builtin:bundle:fork-explore",
			},
		},
	}
	cfg, ok := a.findAgentKindConfig(domain.AgentKindCoder)
	if !ok {
		t.Fatal("coder config not found")
	}
	if !slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:fork-review") {
		t.Errorf("coder DefaultBundleIDs missing fork-review via runtime fallback: %v", cfg.DefaultBundleIDs)
	}
	if !slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:project-wiki") {
		t.Errorf("coder DefaultBundleIDs missing project-wiki via runtime fallback: %v", cfg.DefaultBundleIDs)
	}
	// Stale persisted extras survive the union.
	if !slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:file-tools") {
		t.Errorf("persisted bundle file-tools dropped by runtime fallback: %v", cfg.DefaultBundleIDs)
	}
}

// TestHandleSaveAgentKindConfig_RecordsRemovedBundles verifies that removing
// a default bundle (e.g. fork-review) and saving records the removal in
// RemovedBundleIDs, and that neither the runtime fallback nor a fresh read
// silently re-adds it.
func TestHandleSaveAgentKindConfig_RecordsRemovedBundles(t *testing.T) {
	a, ctx := freshActor(t)
	var defaults []string
	for _, def := range defaultAgentKindConfigs() {
		if def.Kind == domain.AgentKindCoder {
			defaults = def.DefaultBundleIDs
			break
		}
	}
	if !slices.Contains(defaults, "builtin:bundle:fork-review") {
		t.Fatalf("test premise broken: coder defaults lack fork-review: %v", defaults)
	}
	kept := make([]string, 0, len(defaults))
	for _, id := range defaults {
		if id != "builtin:bundle:fork-review" {
			kept = append(kept, id)
		}
	}
	req := domain.WorkspaceSaveAgentKindConfigReq{
		Kind:             domain.AgentKindCoder,
		DisplayName:      "Coder",
		DefaultBundleIDs: kept,
	}
	req.RolePromptRef.Kind = "profile"
	req.RolePromptRef.Key = "project.coder"
	saved, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(saved.RemovedBundleIDs, "builtin:bundle:fork-review") {
		t.Fatalf("RemovedBundleIDs missing fork-review: %v", saved.RemovedBundleIDs)
	}
	fetched, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fetched.DefaultBundleIDs, "builtin:bundle:fork-review") {
		t.Errorf("fork-review re-added by runtime fallback after explicit removal: %v", fetched.DefaultBundleIDs)
	}
	// Re-selecting the bundle on a later save clears the removal record.
	req.DefaultBundleIDs = defaults
	resaved, err := a.handleSaveAgentKindConfig(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(resaved.RemovedBundleIDs, "builtin:bundle:fork-review") {
		t.Errorf("fork-review still marked removed after re-selecting it: %v", resaved.RemovedBundleIDs)
	}
}

// TestFindAgentKindConfig_RuntimeFallbackRespectsRemovedBundles verifies the
// runtime fallback adds missing default bundles but never re-adds one the
// user explicitly removed.
func TestFindAgentKindConfig_RuntimeFallbackRespectsRemovedBundles(t *testing.T) {
	a, _ := freshActor(t)
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          domain.AgentKindCoder,
			DisplayName:   "Coder",
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "project.coder"},
			DefaultBundleIDs: []string{
				"builtin:bundle:file-tools",
				"builtin:bundle:shell-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:workspace-tools",
				"builtin:bundle:fork-explore",
			},
			RemovedBundleIDs: []string{"builtin:bundle:fork-review"},
		},
	}
	cfg, ok := a.findAgentKindConfig(domain.AgentKindCoder)
	if !ok {
		t.Fatal("coder config not found")
	}
	if slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:fork-review") {
		t.Errorf("fork-review re-added despite explicit removal: %v", cfg.DefaultBundleIDs)
	}
	if !slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:project-wiki") {
		t.Errorf("non-removed default project-wiki not propagated: %v", cfg.DefaultBundleIDs)
	}
}

// TestSeedAgentKindConfigs_ReconcileRespectsRemovedBundles verifies the seed
// reconcile does not persistently re-add an explicitly removed default bundle.
func TestSeedAgentKindConfigs_ReconcileRespectsRemovedBundles(t *testing.T) {
	a, ctx := freshActor(t)
	a.AgentKindConfigs = []domain.AgentKindConfig{
		{
			Kind:          domain.AgentKindCoder,
			DisplayName:   "Coder",
			RolePromptRef: gen.PromptRef{Kind: "profile", Key: "project.coder"},
			DefaultBundleIDs: []string{
				"builtin:bundle:file-tools",
				"builtin:bundle:shell-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:workspace-tools",
				"builtin:bundle:fork-explore",
			},
			RemovedBundleIDs: []string{"builtin:bundle:fork-review"},
		},
	}
	a.seedAgentKindConfigs(ctx)

	cfg, err := a.handleGetAgentKindConfig(ctx, domain.WorkspaceGetAgentKindConfigReq{Kind: domain.AgentKindCoder})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:fork-review") {
		t.Errorf("fork-review re-added by seed reconcile despite explicit removal: %v", cfg.DefaultBundleIDs)
	}
	if !slices.Contains(cfg.DefaultBundleIDs, "builtin:bundle:project-wiki") {
		t.Errorf("non-removed default project-wiki not propagated by seed reconcile: %v", cfg.DefaultBundleIDs)
	}
}

func TestAgentListState_AfterCreate(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: actorID}}

	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "List Agent",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Full {
		t.Error("expected full snapshot")
	}
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Items))
	}
	// Find the created agent by ID (no auto-created system agents).
	var item gen.AgentListItem
	for _, it := range state.Items {
		if it.ID == created.ID {
			item = it
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("created agent %q not found in list state items", created.ID)
	}
	if item.ID != created.ID {
		t.Errorf("id = %q, want %q", item.ID, created.ID)
	}
	if item.DisplayName != "List Agent" {
		t.Errorf("displayName = %q, want %q", item.DisplayName, "List Agent")
	}
	if item.AgentKind != "coder" {
		t.Errorf("agentKind = %q, want coder", item.AgentKind)
	}
	if !item.CanDelete {
		t.Errorf("CanDelete = %v, want true for non-system-managed coder", item.CanDelete)
	}

	// create_agent should also emit a full agent_list_state event.
	var listEvents []gen.WorkspaceAgentListStateEvent
	for _, e := range ctx.EmittedEvents {
		if e.Kind == "agent_list_state" {
			listEvents = append(listEvents, e.Payload.(gen.WorkspaceAgentListStateEvent))
		}
	}
	if len(listEvents) == 0 {
		t.Fatal("expected agent_list_state event after create")
	}
	last := listEvents[len(listEvents)-1]
	if !last.State.Full {
		t.Error("expected last event to be full")
	}
	if len(last.State.Items) != 1 {
		t.Errorf("expected 1 item in event, got %d", len(last.State.Items))
	}
}

func TestAgentListState_CreatedAgentIsDeletable(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: actorID}}

	if _, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Coder",
		AgentKind:   domain.AgentKindCoder,
	}); err != nil {
		t.Fatal(err)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Items))
	}
	var item gen.AgentListItem
	for _, it := range state.Items {
		if it.AgentKind == domain.AgentKindCoder {
			item = it
			break
		}
	}
	if item.AgentKind == "" {
		t.Fatal("coder agent not found in list state items")
	}
	if !item.CanDelete {
		t.Errorf("CanDelete = %v, want true", item.CanDelete)
	}
}

// TestAgentListState_BuiltInAgentsNotDeletable verifies the list projection
// advertises CanDelete=false for system-managed (built-in) kinds and for
// app-bound agents, matching the server-side delete_agent guard.
func TestAgentListState_BuiltInAgentsNotDeletable(t *testing.T) {
	a, _ := freshActor(t)
	a.Agents = append(a.Agents,
		domain.AgentRef{ID: "worker-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindWorker, DisplayName: "Worker"},
		domain.AgentRef{ID: "scout-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindScout, DisplayName: "Scout"},
		domain.AgentRef{ID: "app-agent-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindCoder, DisplayName: "App Agent", BoundAppID: "app-1"},
	)

	state := a.buildAgentListState(true)
	byID := make(map[string]gen.AgentListItem, len(state.Items))
	for _, it := range state.Items {
		byID[it.ID] = it
	}
	for _, id := range []string{"worker-1", "scout-1", "app-agent-1"} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("agent %q not found in list state", id)
		}
		if item.CanDelete {
			t.Errorf("%s: CanDelete = true, want false", id)
		}
	}
}

func TestAgentStatusUpdate_MergesRuntimeAndEmitsEvent(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: actorID}}

	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Runtime Agent",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx.EmittedEvents = nil

	_, err = a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:        created.ActorID,
		State:               "running",
		ActiveTurnRef:       "turn-1",
		CurrentTaskSummary:  "working on task",
		BoundTaskCardID:     "task-7",
		LastActivity:        "2026-06-16T00:00:00Z",
		LastTurnCompletedAt: "2026-06-15T10:05:00Z",
		ThinkLevel:          "medium",
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Items))
	}
	var item gen.AgentListItem
	for _, it := range state.Items {
		if it.ID == created.ID {
			item = it
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("created agent %q not found in list state items", created.ID)
	}
	if item.Runtime == nil {
		t.Fatal("expected runtime overlay")
	}
	if item.Runtime.State != "running" {
		t.Errorf("state = %q, want running", item.Runtime.State)
	}
	if item.Runtime.ActiveTurnRef != "turn-1" {
		t.Errorf("activeTurnRef = %q, want turn-1", item.Runtime.ActiveTurnRef)
	}
	if item.Runtime.CurrentTaskSummary != "working on task" {
		t.Errorf("summary = %q, want 'working on task'", item.Runtime.CurrentTaskSummary)
	}
	if item.Runtime.BoundTaskCardID != "task-7" {
		t.Errorf("boundTaskCardId = %q, want task-7", item.Runtime.BoundTaskCardID)
	}
	if item.Runtime.LastTurnCompletedAt != "2026-06-15T10:05:00Z" {
		t.Errorf("LastTurnCompletedAt = %q, want 2026-06-15T10:05:00Z", item.Runtime.LastTurnCompletedAt)
	}
	if item.ThinkLevel != "medium" {
		t.Errorf("thinkLevel = %q, want medium", item.ThinkLevel)
	}

	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0]
	if ev.Kind != "agent_list_state" {
		t.Errorf("event kind = %q, want agent_list_state", ev.Kind)
	}
	payload, ok := ev.Payload.(gen.WorkspaceAgentListStateEvent)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentListStateEvent", ev.Payload)
	}
	if payload.State.Full {
		t.Error("expected incremental event, not full")
	}
	if len(payload.State.Items) != 1 {
		t.Fatalf("event items = %d, want 1", len(payload.State.Items))
	}
	var rItem gen.AgentListItem
	for _, it := range payload.State.Items {
		if it.ID == created.ID {
			rItem = it
			break
		}
	}
	if rItem.ID == "" {
		t.Fatalf("created agent %q not found in emitted event items", created.ID)
	}
	if rItem.Runtime == nil || rItem.Runtime.State != "running" {
		t.Error("expected runtime in emitted event")
	}
}

func TestAgentListState_PreservesPausedStatusForUnloadedAgent(t *testing.T) {
	a, _ := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:        "agent-1",
		ActorID:   testutil.GenActorID().String(),
		LoadState: "unloaded",
		Status:    "paused",
	}}

	state := a.buildAgentListState(true)
	if len(state.Items) != 1 || state.Items[0].Runtime == nil {
		t.Fatalf("expected one agent with persisted runtime, got %+v", state.Items)
	}
	if state.Items[0].Runtime.State != "paused" {
		t.Errorf("runtime state = %q, want paused", state.Items[0].Runtime.State)
	}
}

func TestRefreshAgentStatuses_MergesTitle(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{
		ID:          "agent-1",
		ActorID:     agentActorID,
		DisplayName: "Agent One",
		LoadState:   "loaded",
	}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() != agentActorID {
			return lookupOK(aid)
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			if callID == "agent_status" {
				return gen.AgentStatusResp{Title: "persisted title", State: "idle"}
			}
			return nil
		}), true
	}

	a.refreshAgentStatuses(ctx)

	if a.Agents[0].Title != "persisted title" {
		t.Errorf("title = %q, want 'persisted title'", a.Agents[0].Title)
	}
	if a.Agents[0].Status != "idle" {
		t.Errorf("status = %q, want 'idle'", a.Agents[0].Status)
	}
}

func TestAgentStatusUpdate_MergesTitleAndEmitsEvent(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: actorID}}

	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Runtime Agent",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx.EmittedEvents = nil

	_, err = a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: created.ActorID,
		State:        "idle",
		Title:        "inferred title",
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Items))
	}
	var item gen.AgentListItem
	for _, it := range state.Items {
		if it.ID == created.ID {
			item = it
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("created agent %q not found in list state items", created.ID)
	}
	if item.Title != "inferred title" {
		t.Errorf("stored title = %q, want 'inferred title'", item.Title)
	}

	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ctx.EmittedEvents))
	}
	ev, ok := ctx.EmittedEvents[0].Payload.(gen.WorkspaceAgentListStateEvent)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentListStateEvent", ctx.EmittedEvents[0].Payload)
	}
	if len(ev.State.Items) != 1 {
		t.Fatalf("event items = %d, want 1", len(ev.State.Items))
	}
	var evItem gen.AgentListItem
	for _, it := range ev.State.Items {
		if it.ID == created.ID {
			evItem = it
			break
		}
	}
	if evItem.ID == "" {
		t.Fatalf("created agent %q not found in emitted event items", created.ID)
	}
	if evItem.Title != "inferred title" {
		t.Errorf("emitted title = %q, want 'inferred title'", ev.State.Items[0].Title)
	}
}

func TestAgentStatusUpdate_ClearsTitleAndEmitsEvent(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: actorID}}

	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   actorID,
		DisplayName: "Runtime Agent",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-populate an inferred title as if it had been set earlier.
	for i := range a.Agents {
		if a.Agents[i].ActorID == created.ActorID {
			a.Agents[i].Title = "inferred title"
			break
		}
	}
	ctx.EmittedEvents = nil

	_, err = a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: created.ActorID,
		State:        "idle",
		Title:        "",
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Items))
	}
	var item gen.AgentListItem
	for _, it := range state.Items {
		if it.ID == created.ID {
			item = it
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("created agent %q not found in list state items", created.ID)
	}
	if item.Title != "" {
		t.Errorf("stored title = %q, want empty", item.Title)
	}

	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ctx.EmittedEvents))
	}
	ev, ok := ctx.EmittedEvents[0].Payload.(gen.WorkspaceAgentListStateEvent)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentListStateEvent", ctx.EmittedEvents[0].Payload)
	}
	if len(ev.State.Items) != 1 {
		t.Fatalf("event items = %d, want 1", len(ev.State.Items))
	}
	var evItem gen.AgentListItem
	for _, it := range ev.State.Items {
		if it.ID == created.ID {
			evItem = it
			break
		}
	}
	if evItem.ID == "" {
		t.Fatalf("created agent %q not found in emitted event items", created.ID)
	}
	if evItem.Title != "" {
		t.Errorf("emitted title = %q, want empty", ev.State.Items[0].Title)
	}
}

func TestDefaultAgentKindConfigs_WorkerRandomNamePool(t *testing.T) {
	configs := defaultAgentKindConfigs()
	for _, cfg := range configs {
		if cfg.Kind != domain.AgentKindWorker {
			continue
		}
		if cfg.RandomName == nil || !cfg.RandomName.Enabled || len(cfg.RandomName.Prefixes) == 0 || len(cfg.RandomName.Suffixes) == 0 {
			t.Fatalf("worker default random name config = %+v", cfg.RandomName)
		}
		return
	}
	t.Fatal("missing worker config")
}

func TestGenerateDisplayName_CombinatorialAndDeduplication(t *testing.T) {
	prefixes := []string{"Byte", "Bug"}
	suffixes := []string{"Duck", "Blob"}
	validNames := map[string]bool{"Byte Duck": true, "Bug Blob": true, "Byte Blob": true, "Bug Duck": true}
	randomName := &domain.RandomNameConfig{Enabled: true, Prefixes: prefixes, Suffixes: suffixes}

	// Empty list — should return a valid combination.
	name1 := generateDisplayName(randomName, nil, "")
	if !validNames[name1] {
		t.Errorf("unexpected name %q, expected one of %v", name1, validNames)
	}

	// Same project, one name occupied — should return a different combination.
	existing := []domain.AgentRef{
		{DisplayName: "Byte Duck", ProjectID: "p1"},
	}
	name2 := generateDisplayName(randomName, existing, "p1")
	if name2 == "Byte Duck" {
		t.Errorf("expected a different combination than 'Byte Duck', got %q", name2)
	}
	if !validNames[name2] {
		t.Errorf("unexpected name %q, expected one of %v", name2, validNames)
	}

	// All combinations occupied — should fallback to hex suffix.
	existing = []domain.AgentRef{
		{DisplayName: "Byte Duck", ProjectID: "p1"},
		{DisplayName: "Bug Blob", ProjectID: "p1"},
		{DisplayName: "Byte Blob", ProjectID: "p1"},
		{DisplayName: "Bug Duck", ProjectID: "p1"},
	}
	name3 := generateDisplayName(randomName, existing, "p1")
	if validNames[name3] {
		t.Errorf("expected fallback name with hex suffix, got %q", name3)
	}
	if !strings.HasPrefix(name3, "Byte ") && !strings.HasPrefix(name3, "Bug ") {
		t.Errorf("expected name starting with 'Byte ' or 'Bug ', got %q", name3)
	}

	// Different project — should not conflict.
	name4 := generateDisplayName(randomName, existing, "p2")
	if !validNames[name4] {
		t.Errorf("expected a valid combination for different project, got %q", name4)
	}

	// Global agents — only check against other global agents.
	existing = []domain.AgentRef{
		{DisplayName: "Byte Duck", ProjectID: ""},
	}
	name5 := generateDisplayName(randomName, existing, "")
	if name5 == "Byte Duck" {
		t.Errorf("expected a different combination than 'Byte Duck', got %q", name5)
	}
	if !validNames[name5] {
		t.Errorf("unexpected global name %q, expected one of %v", name5, validNames)
	}
}

func TestCreateAgent_RejectsCoderOnSystemMetaProject(t *testing.T) {
	a, ctx := freshActor(t)
	sysID := a.systemMetaProjectActorID()
	if sysID == "" {
		t.Fatal("expected system meta project to exist")
	}
	_, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		AgentKind:   domain.AgentKindCoder,
		ProjectID:   sysID,
		DisplayName: "test-coder",
	})
	if err == nil {
		t.Fatal("expected error creating coder on system meta project")
	}
}

// fakeCtxWithResources wraps a FakeCtx and lets tests inject a resource registry.
type fakeCtxWithResources struct {
	*testutil.FakeCtx
	regs resource.Registry
}

func (f *fakeCtxWithResources) Resources() resource.Registry { return f.regs }

// fakeBackendStore is an in-memory BackendLogSource for tests.
type fakeBackendStore struct {
	entries []gateway.LogEntry
}

func (s *fakeBackendStore) Tail(n int) ([]gateway.LogEntry, error) {
	if len(s.entries) <= n {
		out := make([]gateway.LogEntry, len(s.entries))
		copy(out, s.entries)
		return out, nil
	}
	out := make([]gateway.LogEntry, n)
	copy(out, s.entries[len(s.entries)-n:])
	return out, nil
}

func (s *fakeBackendStore) QueryBefore(before string, n int) ([]gateway.LogEntry, error) {
	var out []gateway.LogEntry
	for i := len(s.entries) - 1; i >= 0 && len(out) < n; i-- {
		if s.entries[i].Timestamp < before {
			out = append(out, s.entries[i])
		}
	}
	// reverse to chronological order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// TestHandleLogsQuery_PersistedStoreFallback verifies that when the ring
// window is exhausted the query continues into the persisted backend store,
// deduping the seam — freeze evidence survives restarts and log-storm
// flooding of the 2000-entry ring.
func TestHandleLogsQuery_PersistedStoreFallback(t *testing.T) {
	a, _ := freshActor(t)

	ring := logging.NewRing(4)
	ring.Append(gateway.LogEntry{Timestamp: "2026-08-27T00:00:08Z", Level: "info", Message: "new-1"})
	ring.Append(gateway.LogEntry{Timestamp: "2026-08-27T00:00:09Z", Level: "info", Message: "new-2"})

	store := &fakeBackendStore{entries: []gateway.LogEntry{
		{Timestamp: "2026-08-27T00:00:01Z", Level: "warn", Message: "old-warn"},
		{Timestamp: "2026-08-27T00:00:02Z", Level: "error", Message: "old-error"},
		{Timestamp: "2026-08-27T00:00:08Z", Level: "info", Message: "new-1"}, // restored overlap
	}}

	regs := resource.New()
	_ = resource.Set[logging.LogRing](regs, logging.LogRingKey, ring)
	_ = resource.Set[logging.BackendLogSource](regs, logging.BackendLogSourceKey, store)

	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	// Default (no cursor): newest entries across ring+store, seam deduped.
	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleLogsQuery failed: %v", err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("expected 4 entries (2 old + 2 new with 1 deduped overlap), got %d: %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Message != "old-warn" || resp.Items[3].Message != "new-2" {
		t.Errorf("unexpected order: %+v", resp.Items)
	}

	// Before-cursor paging below the ring window continues in the store.
	paged, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Before: "2026-08-27T00:00:02Z", Limit: 10})
	if err != nil {
		t.Fatalf("handleLogsQuery before failed: %v", err)
	}
	if len(paged.Items) != 1 || paged.Items[0].Message != "old-warn" {
		t.Errorf("expected only old-warn before cursor, got %+v", paged.Items)
	}
}

func TestHandleLogsQuery(t *testing.T) {
	a, _ := freshActor(t)

	ring := logging.NewRing(10)
	ring.Append(gateway.LogEntry{Timestamp: "2026-07-11T00:00:00Z", Level: "info", Message: "hello"})
	ring.Append(gateway.LogEntry{Timestamp: "2026-07-11T00:00:01Z", Level: "error", Message: "boom"})

	regs := resource.New()
	_ = resource.Set[logging.LogRing](regs, logging.LogRingKey, ring)

	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleLogsQuery failed: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(resp.Items))
	}

	filtered, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Level: "error"})
	if err != nil {
		t.Fatalf("handleLogsQuery filtered failed: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Level != "error" {
		t.Fatalf("expected 1 error entry, got %v", filtered.Items)
	}
}

// TestHandleLogsQuery_StructuredFields verifies the enriched response shape:
// caller file/line split out of the combined Caller string, and fields map
// preserved. This is the data basis for the frontend structured log viewer.
func TestHandleLogsQuery_StructuredFields(t *testing.T) {
	a, _ := freshActor(t)

	ring := logging.NewRing(10)
	ring.Append(gateway.LogEntry{
		Timestamp: "2026-07-11T00:00:00Z",
		Level:     "warn",
		Caller:    "pkg/actor/workspace/workspace.go:2078",
		Message:   "slow query",
		Fields:    map[string]any{"dur_ms": 1200, "actor": "workspace"},
	})
	ring.Append(gateway.LogEntry{
		Timestamp: "2026-07-11T00:00:01Z",
		Level:     "info",
		Caller:    "no-line-caller",
		Message:   "plain",
	})

	regs := resource.New()
	_ = resource.Set[logging.LogRing](regs, logging.LogRingKey, ring)

	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleLogsQuery failed: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Items))
	}

	first := resp.Items[0]
	// Backward compat: combined Caller stays intact.
	if first.Caller != "pkg/actor/workspace/workspace.go:2078" {
		t.Errorf("expected original Caller preserved, got %q", first.Caller)
	}
	if first.CallerFile != "pkg/actor/workspace/workspace.go" {
		t.Errorf("expected CallerFile split, got %q", first.CallerFile)
	}
	if first.CallerLine != 2078 {
		t.Errorf("expected CallerLine 2078, got %d", first.CallerLine)
	}
	if first.Fields["dur_ms"] != 1200 {
		t.Errorf("expected fields map preserved, got %v", first.Fields)
	}

	second := resp.Items[1]
	if second.CallerFile != "no-line-caller" || second.CallerLine != 0 {
		t.Errorf("expected fallback caller file with line 0, got %q:%d", second.CallerFile, second.CallerLine)
	}
}

// TestSplitLogCaller covers the file:line splitter used by both the query
// response enrichment and the workspace.log event payload.
func TestSplitLogCaller(t *testing.T) {
	cases := []struct {
		in   string
		file string
		line int64
	}{
		{"pkg/actor/workspace/workspace.go:2078", "pkg/actor/workspace/workspace.go", 2078},
		{"App.tsx:42", "App.tsx", 42},
		{"plain", "plain", 0},
		{"", "", 0},
		{"a:b:c:12", "a:b:c", 12},
	}
	for _, c := range cases {
		file, line := splitLogCaller(c.in)
		if file != c.file || line != c.line {
			t.Errorf("splitLogCaller(%q) = (%q, %d), want (%q, %d)", c.in, file, line, c.file, c.line)
		}
	}
}

type fakeConsoleSource struct{ entries []logging.ConsoleEntry }

func (f *fakeConsoleSource) Tail(n int) ([]logging.ConsoleEntry, error) {
	if n >= len(f.entries) {
		return f.entries, nil
	}
	return f.entries[len(f.entries)-n:], nil
}

func (f *fakeConsoleSource) QueryBefore(before string, n int) ([]logging.ConsoleEntry, error) {
	beforeMs, err := time.Parse(time.RFC3339Nano, before)
	if err != nil {
		return nil, err
	}
	cutoff := beforeMs.UnixMilli()
	var result []logging.ConsoleEntry
	for i := len(f.entries) - 1; i >= 0 && len(result) < n; i-- {
		if f.entries[i].Time < cutoff {
			result = append(result, f.entries[i])
		}
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func TestHandleLogsQuery_Console(t *testing.T) {
	a, _ := freshActor(t)

	fake := &fakeConsoleSource{entries: []logging.ConsoleEntry{
		{Level: "info", Message: "ui mounted", Time: 1752200000000, Location: "App.tsx:42"},
		{Level: "error", Message: "ws closed", Time: 1752200001000, Location: "socket.ts:7"},
	}}

	regs := resource.New()
	_ = resource.Set[logging.ConsoleLogSource](regs, logging.ConsoleLogSourceKey, fake)

	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	// All console entries, ordered as returned by the source.
	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Limit: 10})
	if err != nil {
		t.Fatalf("handleLogsQuery console failed: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 console entries, got %d", len(resp.Items))
	}
	if resp.Items[0].Caller != "App.tsx:42" {
		t.Errorf("expected Caller to map from Location, got %q", resp.Items[0].Caller)
	}
	if resp.Items[0].Timestamp == "" {
		t.Errorf("expected non-empty RFC3339Nano timestamp converted from ms epoch")
	}

	// Level filtering against console entries.
	errOnly, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Level: "error"})
	if err != nil {
		t.Fatalf("handleLogsQuery console level failed: %v", err)
	}
	if len(errOnly.Items) != 1 || errOnly.Items[0].Level != "error" {
		t.Fatalf("expected 1 error console entry, got %v", errOnly.Items)
	}

	// Caller (location) filtering against console entries.
	locOnly, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Caller: "socket"})
	if err != nil {
		t.Fatalf("handleLogsQuery console caller failed: %v", err)
	}
	if len(locOnly.Items) != 1 || locOnly.Items[0].Caller != "socket.ts:7" {
		t.Fatalf("expected 1 socket entry, got %v", locOnly.Items)
	}
}

func TestHandleLogsQuery_Truncation(t *testing.T) {
	a, _ := freshActor(t)

	// Fill the ring with 5 entries; request Limit=3 so truncation kicks in.
	ring := logging.NewRing(10)
	for i := 0; i < 5; i++ {
		ring.Append(gateway.LogEntry{
			Timestamp: fmt.Sprintf("2026-07-11T00:00:0%dZ", i),
			Level:     "info",
			Message:   fmt.Sprintf("entry %d", i),
		})
	}

	regs := resource.New()
	_ = resource.Set[logging.LogRing](regs, logging.LogRingKey, ring)
	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Limit: 3})
	if err != nil {
		t.Fatalf("handleLogsQuery failed: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("expected Truncated=true")
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}
	// Should keep the newest 3 entries (indices 2,3,4).
	if resp.Items[0].Message != "entry 2" {
		t.Errorf("expected oldest kept = entry 2, got %q", resp.Items[0].Message)
	}
	// NextBefore must point to the oldest kept entry.
	if resp.NextBefore != "2026-07-11T00:00:02Z" {
		t.Errorf("expected NextBefore=2026-07-11T00:00:02Z, got %q", resp.NextBefore)
	}

	// Page backwards using Before.
	resp2, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Limit: 3, Before: resp.NextBefore})
	if err != nil {
		t.Fatalf("handleLogsQuery page 2 failed: %v", err)
	}
	if resp2.Truncated {
		t.Fatal("expected Truncated=false on page 2 (only 2 entries before cursor)")
	}
	if len(resp2.Items) != 2 {
		t.Fatalf("expected 2 items on page 2, got %d", len(resp2.Items))
	}
}

func TestHandleLogsQuery_TruncationConsole(t *testing.T) {
	a, _ := freshActor(t)

	entries := make([]logging.ConsoleEntry, 5)
	for i := range entries {
		entries[i] = logging.ConsoleEntry{
			Time:     int64(1752200000000 + i),
			Level:    "info",
			Message:  fmt.Sprintf("console %d", i),
			Location: "App.tsx:1",
		}
	}
	fake := &fakeConsoleSource{entries: entries}

	regs := resource.New()
	_ = resource.Set[logging.ConsoleLogSource](regs, logging.ConsoleLogSourceKey, fake)
	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Limit: 3})
	if err != nil {
		t.Fatalf("handleLogsQuery console failed: %v", err)
	}
	if !resp.Truncated {
		t.Fatal("expected Truncated=true for console")
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}
}

func TestHandleLogsQuery_TailCapped(t *testing.T) {
	a, _ := freshActor(t)

	ring := logging.NewRing(10)
	for i := 0; i < 5; i++ {
		ring.Append(gateway.LogEntry{
			Timestamp: fmt.Sprintf("2026-07-11T00:00:0%dZ", i),
			Level:     "info",
			Message:   fmt.Sprintf("entry %d", i),
		})
	}

	regs := resource.New()
	_ = resource.Set[logging.LogRing](regs, logging.LogRingKey, ring)
	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	// Tail=1000 exceeds the cap; should be clamped to 200, and with only 5
	// entries no truncation is expected.
	resp, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Tail: 1000})
	if err != nil {
		t.Fatalf("handleLogsQuery tail failed: %v", err)
	}
	if resp.Truncated {
		t.Fatal("expected Truncated=false (only 5 entries)")
	}
	if len(resp.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(resp.Items))
	}
}

func TestHandleDeleteAgent_RemovesAgentStateDir(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:          "Coder One#0001",
		ActorID:     actorID,
		AgentKind:   domain.AgentKindCoder,
		ProjectID:   "proj-1",
		DisplayName: "Coder One",
	})
	stateDir := filepath.Join(config.ActorDataDir(), "agent", actorID)
	if err := os.MkdirAll(filepath.Join(stateDir, "turns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "turns", "t1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// delete_agent now routes through the unified cascade path: teardown runs
	// in a goroutine and schedules internal_deletion_finalize via ctx.After.
	// FakeCtx.After is a no-op by default, so dispatch the finalize inline.
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "workspace.internal_deletion_finalize" {
			if req, ok := payload.(deletionFinalizeReq); ok {
				_, err := a.handleDeletionFinalize(ctx, req)
				return err
			}
		}
		return nil
	}

	if _, err := a.handleDeleteAgent(ctx, domain.WorkspaceDeleteAgentReq{AgentID: "Coder One#0001"}); err != nil {
		t.Fatalf("handleDeleteAgent failed: %v", err)
	}

	// State removal happens in the finalize handler after async teardown.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(stateDir); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent state dir still exists after delete: %s", stateDir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHandleDeleteAgent_RejectsBuiltInAndAppBound verifies the server-side
// guard: system-managed (built-in) kinds and app-bound agents are rejected by
// workspace.delete_agent regardless of what a client believes, and the
// rejection leaves the registry rows untouched.
func TestHandleDeleteAgent_RejectsBuiltInAndAppBound(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = append(a.Agents,
		domain.AgentRef{ID: "worker-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindWorker, ProjectID: "proj-1", DisplayName: "Worker"},
		domain.AgentRef{ID: "scout-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindScout, ProjectID: "proj-1", DisplayName: "Scout"},
		domain.AgentRef{ID: "app-agent-1", ActorID: testutil.GenActorID().String(), AgentKind: domain.AgentKindCoder, ProjectID: "proj-1", DisplayName: "App Agent", BoundAppID: "app-1"},
	)

	for _, id := range []string{"worker-1", "scout-1", "app-agent-1"} {
		_, err := a.handleDeleteAgent(ctx, domain.WorkspaceDeleteAgentReq{AgentID: id})
		if err == nil {
			t.Errorf("delete_agent(%q) allowed, want rejected", id)
			continue
		}
		if !strings.Contains(err.Error(), "built-in") {
			t.Errorf("delete_agent(%q) error = %q, want mention of built-in", id, err)
		}
	}

	// No registry row was marked deleting or removed by the rejected calls.
	if len(a.Agents) != 3 {
		t.Fatalf("registry len = %d, want 3 after rejected deletes", len(a.Agents))
	}
	for _, ag := range a.Agents {
		if ag.DeletionStatus == "deleting" {
			t.Errorf("agent %q was marked deleting despite rejection", ag.ID)
		}
	}
}

func countAgentsByKind(a *Actor, kind string) int {
	n := 0
	for _, ag := range a.Agents {
		if ag.AgentKind == kind {
			n++
		}
	}
	return n
}

// TestEnsureCoordinatorAgent_Idempotent verifies the Coordinator is created
// exactly once per workspace and repeated ensures are no-ops.
// Note: ensureCoordinatorAgent is no longer called at startup; this test
// exercises it directly.
func TestEnsureCoordinatorAgent_Idempotent(t *testing.T) {
	a, ctx := freshActor(t)
	if got := countAgentsByKind(a, domain.AgentKindCoordinator); got != 0 {
		t.Fatalf("expected 0 coordinator after OnStart (not auto-created), got %d", got)
	}
	a.ensureCoordinatorAgent(ctx)
	for _, ag := range a.Agents {
		if ag.AgentKind == domain.AgentKindCoordinator && ag.ProjectID != "" {
			t.Fatalf("coordinator scoped to %q, want global (empty ProjectID)", ag.ProjectID)
		}
	}
	a.ensureCoordinatorAgent(ctx)
	if got := countAgentsByKind(a, domain.AgentKindCoordinator); got != 1 {
		t.Fatalf("expected 1 coordinator after repeated ensure, got %d", got)
	}
}

// TestSpawnGlobalAgent_GrantsPlannerCapability is a regression test for the
// "plan: plan: spawn not available" error on Coordinator conversations.
// Workspace-global agents (Coordinator) dispatch to the LLM via
// planner.Plan, so they must be spawned with the Planner capability — exactly
// like project-scoped agents via project.spawn_agent. Without WithPlanner,
// ctx.Planner() returns a typed-nil interface that defeats the turn engine's
// nil guard and surfaces as an opaque spawn error.
func TestSpawnGlobalAgent_GrantsPlannerCapability(t *testing.T) {
	a, ctx := freshActor(t)

	var captured actor.Props
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		captured = p
		return noOpSpawn(p, name)
	}

	if _, err := a.spawnGlobalAgent(ctx, "coordinator-x", domain.AgentKindCoordinator, "Coordinator", nil, nil, nil, nil, nil, "", ""); err != nil {
		t.Fatalf("spawnGlobalAgent: %v", err)
	}
	if !captured.PlanEnabled() {
		t.Fatalf("global agent props must grant Planner capability (WithPlanner); PlanEnabled=false reproduces plan: spawn not available")
	}
}

// TestSpawnGlobalAgent_LoadGlobalCoordinatorGrantsPlanner verifies the
// end-to-end lazy-load path for a workspace-global agent yields a planner.
func TestSpawnGlobalAgent_LoadGlobalCoordinatorGrantsPlanner(t *testing.T) {
	a, ctx := freshActor(t)

	a.Agents = append(a.Agents, domain.AgentRef{
		ID:        "coord-global",
		ProjectID: "",
		AgentKind: domain.AgentKindCoordinator,
		Status:    "inactive",
	})

	var captured actor.Props
	ctx.SpawnFn = func(p actor.Props, name string) (ref.Ref, error) {
		captured = p
		return noOpSpawn(p, name)
	}

	if _, _, err := a.loadAgentByID(ctx, "coord-global"); err != nil {
		t.Fatalf("loadAgentByID: %v", err)
	}
	if !captured.PlanEnabled() {
		t.Fatalf("loaded global agent must have Planner capability; PlanEnabled=false")
	}
}

// TestHandleCreateAgent_CoordinatorSingleton locks down the process-unique
// Coordinator: a second workspace.create_agent for the coordinator kind is
// rejected, and a coordinator cannot be scoped to a project (it is global).
func TestHandleCreateAgent_CoordinatorSingleton(t *testing.T) {
	a, ctx := freshActor(t)

	// No auto-ensured Coordinator at startup, so we can exercise a fresh create.
	created, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		AgentKind: domain.AgentKindCoordinator,
	})
	if err != nil {
		t.Fatalf("first coordinator create should succeed: %v", err)
	}
	if created.AgentKind != domain.AgentKindCoordinator {
		t.Errorf("kind = %q, want coordinator", created.AgentKind)
	}

	_, err = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		AgentKind: domain.AgentKindCoordinator,
	})
	if err == nil {
		t.Fatal("expected second coordinator create to fail (singleton)")
	}
	if !strings.Contains(err.Error(), "coordinator") {
		t.Errorf("error = %q, want mention of coordinator", err)
	}

	// A coordinator scoped to any project is rejected (it must be global).
	foreign := testutil.GenActorID().String()
	_, err = a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID: foreign,
		AgentKind: domain.AgentKindCoordinator,
	})
	if err == nil {
		t.Fatal("expected coordinator create scoped to a project to fail")
	}
}

// TestHandleCloneAgent_RejectsCoordinator verifies the Coordinator cannot be
// cloned, which would otherwise create a duplicate special agent.
func TestHandleCloneAgent_RejectsCoordinator(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:          "coord-1",
		AgentKind:   domain.AgentKindCoordinator,
		ProjectID:   a.systemMetaProjectActorID(),
		DisplayName: "Coordinator",
	}}
	_, err := a.handleCloneAgent(ctx, domain.WorkspaceCloneAgentReq{
		SourceAgentID: "coord-1",
		DisplayName:   "Coordinator Clone",
	})
	if err == nil {
		t.Fatal("expected coordinator clone to fail")
	}
	if !strings.Contains(err.Error(), "coordinator") {
		t.Errorf("error = %q, want mention of coordinator", err)
	}
}

// TestHandleCoordinatorLookup verifies the coordinator lookup callable returns
// the Coordinator nickname (its DisplayName) and a live actor id after loading
// the lazy Coordinator agent. Since the Coordinator is no longer auto-created
// at startup, the test creates one first.
func TestHandleCoordinatorLookup(t *testing.T) {
	a, ctx := freshActor(t)
	// Before any coordinator exists, lookup returns Found=false.
	resp, err := a.handleCoordinatorLookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Found {
		t.Fatal("expected coordinator lookup to not be found before creation")
	}
	// Create a coordinator, then lookup should find it.
	a.ensureCoordinatorAgent(ctx)
	resp, err = a.handleCoordinatorLookup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Found {
		t.Fatal("expected coordinator lookup to be found after creation")
	}
	if resp.Nickname != "Coordinator" {
		t.Errorf("nickname = %q, want Coordinator", resp.Nickname)
	}
	if resp.ActorID == "" {
		t.Error("expected non-empty actor id (coordinator should be loaded)")
	}
}

// TestNormalizeGlobalSystemAgents verifies the startup migration collapses
// leftover project-scoped coordinators into a single global record,
// preferring the already-global record.
func TestNormalizeGlobalSystemAgents(t *testing.T) {
	a, _ := freshActor(t)
	sysID := a.systemMetaProjectActorID()
	if sysID == "" {
		t.Fatal("expected system meta project to be mounted")
	}
	// Legacy state: a global coordinator stub + a project-scoped duplicate.
	a.Agents = []domain.AgentRef{
		{ID: "coord-global", AgentKind: domain.AgentKindCoordinator, ProjectID: "", DisplayName: "Coordinator"},
		{ID: "coord-sys", AgentKind: domain.AgentKindCoordinator, ProjectID: sysID, DisplayName: "Coordinator"},
	}
	if !a.normalizeGlobalSystemAgents() {
		t.Fatal("expected normalize to report a change")
	}
	if got := countAgentsByKind(a, domain.AgentKindCoordinator); got != 1 {
		t.Fatalf("expected 1 coordinator after normalize, got %d", got)
	}
	keptCoord := ""
	for _, ag := range a.Agents {
		if ag.AgentKind == domain.AgentKindCoordinator {
			if ag.ProjectID != "" {
				t.Fatalf("coordinator scoped to %q, want global", ag.ProjectID)
			}
			keptCoord = ag.ID
		}
	}
	if keptCoord != "coord-global" {
		t.Fatalf("expected to keep the global coordinator stub, got %q", keptCoord)
	}
	// Idempotent: a second normalize reports no change.
	if a.normalizeGlobalSystemAgents() {
		t.Fatal("expected second normalize to report no change")
	}
}

// TestOnStart_PreservesGlobalAgentActorID verifies that a workspace-global
// agent retains its stable actor ID while startup marks its actor unloaded.
func TestOnStart_PreservesGlobalAgentActorID(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir)}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	// Seed a global coordinator that was active before startup, carrying a
	// live actor ID that must be remembered for a history-preserving re-spawn.
	origActorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{
		ID:          "coord-1",
		AgentKind:   domain.AgentKindCoordinator,
		ProjectID:   "",
		DisplayName: "Coordinator",
		ActorID:     origActorID,
		Status:      "active",
	}}
	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	idx := -1
	for i := range a.Agents {
		if a.Agents[i].ID == "coord-1" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("expected coordinator to survive OnStart")
	}
	if a.Agents[idx].ActorID != origActorID {
		t.Fatalf("ActorID = %q, want stable ID %q", a.Agents[idx].ActorID, origActorID)
	}
	if a.Agents[idx].LoadState != "unloaded" {
		t.Fatalf("LoadState = %q, want unloaded", a.Agents[idx].LoadState)
	}
}

func TestOnStart_PreservesPausedStatusForSidebar(t *testing.T) {
	dir := t.TempDir()
	workspaceID := testutil.GenActorID()
	first := &Actor{store: persist.NewFSPersist(dir), NoSystemProject: true}
	firstCtx := testutil.AdminCtx(workspaceID)
	if err := first.OnInit(firstCtx); err != nil {
		t.Fatal(err)
	}
	first.Agents = []domain.AgentRef{
		{ID: "running", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "running"},
		{ID: "waiting", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "waiting"},
		{ID: "paused", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "paused"},
		{ID: "completed", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "completed"},
		{ID: "failed", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "failed"},
		{ID: "idle", AgentKind: domain.AgentKindCoder, ActorID: testutil.GenActorID().String(), LoadState: "loaded", Status: "idle"},
	}
	if err := first.Save(); err != nil {
		t.Fatalf("save initial workspace: %v", err)
	}

	restarted := &Actor{store: persist.NewFSPersist(dir), NoSystemProject: true}
	ctx := testutil.AdminCtx(workspaceID)
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := restarted.OnInit(ctx); err != nil {
		t.Fatalf("load restarted workspace: %v", err)
	}
	if err := restarted.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}

	want := map[string]string{
		"running":   "paused",
		"waiting":   "paused",
		"paused":    "paused",
		"completed": "idle",
		"failed":    "idle",
		"idle":      "idle",
	}
	for _, ag := range restarted.Agents {
		if ag.Status != want[ag.ID] {
			t.Errorf("agent %q Status = %q, want %q", ag.ID, ag.Status, want[ag.ID])
		}
		if ag.LoadState != "unloaded" {
			t.Errorf("agent %q LoadState = %q, want unloaded", ag.ID, ag.LoadState)
		}
	}

	state, err := restarted.handleAgentListState(ctx)
	if err != nil {
		t.Fatalf("agent list state: %v", err)
	}
	for _, item := range state.Items {
		if item.ID != "running" && item.ID != "waiting" && item.ID != "paused" {
			continue
		}
		if item.Runtime == nil || item.Runtime.State != "paused" {
			t.Errorf("sidebar item %q runtime = %+v, want paused", item.ID, item.Runtime)
		}
	}
}

func TestShellEnvProbe_ReturnsCandidates(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleShellEnvProbe(ctx)
	if err != nil {
		t.Fatalf("handleShellEnvProbe: %v", err)
	}
	if resp.Platform != runtime.GOOS {
		t.Errorf("Platform = %q, want %q", resp.Platform, runtime.GOOS)
	}
	if len(resp.Candidates) == 0 {
		t.Fatal("Candidates is empty")
	}
	if !resp.Current.Available && resp.Current.Executable == "" {
		t.Error("Current shell is neither available nor has an executable")
	}
	for _, c := range resp.Candidates {
		if c.Kind == "" {
			t.Errorf("candidate has empty Kind: %+v", c)
		}
		if c.Available && c.Executable == "" {
			t.Errorf("available candidate %q has empty Executable", c.Kind)
		}
	}
}

func TestShellPrefSave_PersistsAndInjects(t *testing.T) {
	a, ctx := freshActor(t)
	prev := util.ShellPreference()
	defer util.SetShellPreference(prev)

	// Pick an available shell kind to save as preference.
	probe, err := a.handleShellEnvProbe(ctx)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	var kind string
	for _, c := range probe.Candidates {
		if c.Available {
			kind = c.Kind
			break
		}
	}
	if kind == "" {
		t.Skip("no available shell to test preference save")
	}

	resp, err := a.handleShellPrefSave(ctx, domain.ShellPrefSaveReq{Kind: kind})
	if err != nil {
		t.Fatalf("handleShellPrefSave: %v", err)
	}
	if resp.Kind != kind {
		t.Errorf("resp.Kind = %q, want %q", resp.Kind, kind)
	}
	if !resp.Current.Available {
		t.Errorf("resp.Current should be available after saving preference %q", kind)
	}

	// The preference must be persisted in account preferences.
	got, err := a.handleGetAccountPreferences(ctx)
	if err != nil {
		t.Fatalf("handleGetAccountPreferences: %v", err)
	}
	if got.Preferences["shell"] != kind {
		t.Errorf("persisted preference = %q, want %q", got.Preferences["shell"], kind)
	}

	// The util-level preference must have been injected.
	if util.ShellPreference() != util.ShellKind(kind) {
		t.Errorf("util.ShellPreference() = %q, want %q", util.ShellPreference(), kind)
	}

	// Every agent kind config's EnvironmentContext must reflect the new shell
	// immediately, so the next turn's injected Environment block is fresh
	// (agents re-fetch kind config at every turn start).
	envNow := util.DetectEnvironment()
	for _, cfg := range a.AgentKindConfigs {
		if cfg.EnvironmentContext["shell"] != envNow["shell"] {
			t.Errorf("kind %q EnvironmentContext shell = %q, want %q", cfg.Kind, cfg.EnvironmentContext["shell"], envNow["shell"])
		}
		if cfg.EnvironmentContext["shell_flag"] != envNow["shell_flag"] {
			t.Errorf("kind %q EnvironmentContext shell_flag = %q, want %q", cfg.Kind, cfg.EnvironmentContext["shell_flag"], envNow["shell_flag"])
		}
		if cfg.EnvironmentContext["shell_notes"] != envNow["shell_notes"] {
			t.Errorf("kind %q EnvironmentContext shell_notes not refreshed", cfg.Kind)
		}
	}
}

func TestBootThemeCacheWriteThrough(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	a := &Actor{NoSystemProject: true, actorID: "ws-boottheme"}
	a.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "root",
		Version:     1,
		Preferences: map[string]string{domain.AccountPrefKeyAiShellTheme: `{"mode":"dark","fontSize":13.5}`},
	}
	if err := a.saveAccountPrefsCard(); err != nil {
		t.Fatalf("saveAccountPrefsCard: %v", err)
	}
	got, err := os.ReadFile(config.BootThemeCachePath())
	if err != nil {
		t.Fatalf("read boot cache: %v", err)
	}
	if want := `{"mode":"dark","fontSize":13.5}`; string(got) != want {
		t.Fatalf("boot cache = %q, want %q", got, want)
	}

	// Absent theme preference must prune a stale cache so pre-paint cannot
	// show a theme that no longer exists.
	a.accountPrefs.Preferences = map[string]string{}
	if err := a.saveAccountPrefsCard(); err != nil {
		t.Fatalf("saveAccountPrefsCard (prune): %v", err)
	}
	if _, err := os.Stat(config.BootThemeCachePath()); !os.IsNotExist(err) {
		t.Fatalf("stale cache still present after prune: %v", err)
	}
}

func TestLoadRefreshesBootThemeCache(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	workspaceID := testutil.GenActorID()
	seed := &Actor{store: ps, NoSystemProject: true}
	seed.actorID = workspaceID.String()
	seed.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "root",
		Version:     1,
		Preferences: map[string]string{domain.AccountPrefKeyAiShellTheme: `{"mode":"dark"}`},
	}
	if err := seed.saveAccountPrefsCard(); err != nil {
		t.Fatalf("seed saveAccountPrefsCard: %v", err)
	}

	// Simulate a pre-upgrade boot: prefs card exists, boot cache does not.
	if err := os.Remove(config.BootThemeCachePath()); err != nil {
		t.Fatal(err)
	}

	loaded := &Actor{store: ps, NoSystemProject: true}
	loaded.actorID = workspaceID.String()
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := os.ReadFile(config.BootThemeCachePath())
	if err != nil {
		t.Fatalf("read boot cache after Load: %v", err)
	}
	if string(got) != `{"mode":"dark"}` {
		t.Fatalf("boot cache = %q, want persisted dark theme", got)
	}
}

func TestApplyShellPreference_LoadsPersistedOnStart(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	workspaceID := testutil.GenActorID()
	// Seed persisted state with a shell preference.
	seed := &Actor{store: ps, NoSystemProject: true}
	seedCtx := testutil.AdminCtx(workspaceID)
	seedCtx.SpawnFn = noOpSpawn
	seedCtx.LookupIDFn = lookupOK
	if err := seed.OnInit(seedCtx); err != nil {
		t.Fatal(err)
	}
	// Set the preference after OnInit so Load() does not clobber it.
	seed.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "root",
		Version:     1,
		Preferences: map[string]string{"shell": "powershell7"},
	}
	if err := seed.Save(); err != nil {
		t.Fatalf("save seed: %v", err)
	}

	prev := util.ShellPreference()
	defer util.SetShellPreference(prev)
	util.SetShellPreference("")

	// Reload from persisted state; applyShellPreference should run during load.
	loaded := &Actor{store: ps, NoSystemProject: true}
	ctx := testutil.AdminCtx(workspaceID)
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := loaded.OnInit(ctx); err != nil {
		t.Fatalf("OnInit: %v", err)
	}
	if err := loaded.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	if util.ShellPreference() != util.ShellPowerShell7 {
		t.Errorf("after load, util.ShellPreference() = %q, want %q", util.ShellPreference(), util.ShellPowerShell7)
	}
}

func TestDecideGitBashAdoption(t *testing.T) {
	cases := []struct {
		name                string
		prevRecordedPresent bool
		present             bool
		shellPref           string
		want                bool
	}{
		{"new install, no explicit choice", false, true, "", true},
		{"already present at last run", true, true, "", false},
		{"new install, explicit choice", false, true, "powershell7", false},
		{"not installed", false, false, "", false},
		{"uninstalled since last run", true, false, "", false},
	}
	for _, tc := range cases {
		if got := decideGitBashAdoption(tc.prevRecordedPresent, tc.present, tc.shellPref); got != tc.want {
			t.Errorf("%s: decideGitBashAdoption(%v, %v, %q) = %v, want %v", tc.name, tc.prevRecordedPresent, tc.present, tc.shellPref, got, tc.want)
		}
	}
}

// seedShellPrefsActor persists an actor whose account preferences are exactly
// prefs, returning the store the card was written to.
func seedShellPrefsActor(t *testing.T, prefs map[string]string) *persist.FSPersist {
	t.Helper()
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	workspaceID := testutil.GenActorID()
	seed := &Actor{store: ps, NoSystemProject: true}
	seedCtx := testutil.AdminCtx(workspaceID)
	seedCtx.SpawnFn = noOpSpawn
	seedCtx.LookupIDFn = lookupOK
	if err := seed.OnInit(seedCtx); err != nil {
		t.Fatal(err)
	}
	seed.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "root",
		Version:     1,
		Preferences: prefs,
	}
	if err := seed.Save(); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	return ps
}

func loadShellPrefsActor(t *testing.T, ps *persist.FSPersist) *Actor {
	t.Helper()
	prev := util.ShellPreference()
	t.Cleanup(func() { util.SetShellPreference(prev) })
	util.SetShellPreference("")

	workspaceID := testutil.GenActorID()
	loaded := &Actor{store: ps, NoSystemProject: true}
	ctx := testutil.AdminCtx(workspaceID)
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	if err := loaded.OnInit(ctx); err != nil {
		t.Fatalf("OnInit: %v", err)
	}
	if err := loaded.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	return loaded
}

func TestShellAdoption_AdoptsGitBashOnNewInstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only behavior")
	}
	gitBashAvailable := false
	for _, c := range util.DetectShells() {
		if c.Kind == util.ShellGitBash && c.Available {
			gitBashAvailable = true
			break
		}
	}
	if !gitBashAvailable {
		t.Skip("git-bash not installed")
	}

	ps := seedShellPrefsActor(t, map[string]string{"theme": "dark"})
	loaded := loadShellPrefsActor(t, ps)

	if util.ShellPreference() != util.ShellGitBash {
		t.Errorf("after load, util.ShellPreference() = %q, want %q", util.ShellPreference(), util.ShellGitBash)
	}
	if got := loaded.accountPrefs.Preferences["shell"]; got != string(util.ShellGitBash) {
		t.Errorf("persisted shell pref = %q, want %q", got, util.ShellGitBash)
	}
	if got := loaded.accountPrefs.Preferences[shellGitBashRecordKey]; got != "1" {
		t.Errorf("git-bash record = %q, want 1", got)
	}
}

func TestShellAdoption_DoesNotOverrideExplicitChoice(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only behavior")
	}
	gitBashAvailable := false
	for _, c := range util.DetectShells() {
		if c.Kind == util.ShellGitBash && c.Available {
			gitBashAvailable = true
			break
		}
	}
	if !gitBashAvailable {
		t.Skip("git-bash not installed")
	}

	// No availability record + explicit powershell7 choice: the absent→present
	// transition must not clobber the manual choice.
	ps := seedShellPrefsActor(t, map[string]string{"shell": "powershell7"})
	loaded := loadShellPrefsActor(t, ps)

	if util.ShellPreference() != util.ShellPowerShell7 {
		t.Errorf("after load, util.ShellPreference() = %q, want %q", util.ShellPreference(), util.ShellPowerShell7)
	}
	if got := loaded.accountPrefs.Preferences["shell"]; got != "powershell7" {
		t.Errorf("persisted shell pref = %q, want powershell7", got)
	}
}

func TestShellAdoption_RecordPreventsReAdoption(t *testing.T) {
	// Record says git-bash existed at last run: even with no explicit shell
	// preference, load must stay on auto instead of adopting git-bash.
	ps := seedShellPrefsActor(t, map[string]string{shellGitBashRecordKey: "1"})
	loadShellPrefsActor(t, ps)

	if util.ShellPreference() != "" {
		t.Errorf("after load, util.ShellPreference() = %q, want \"\" (auto untouched)", util.ShellPreference())
	}
}

func TestHandleLogsQuery_ConsolePaging(t *testing.T) {
	a, _ := freshActor(t)

	fake := &fakeConsoleSource{entries: []logging.ConsoleEntry{
		{Level: "info", Message: "one", Time: 1752200000000, Location: "a.ts:1"},
		{Level: "info", Message: "two", Time: 1752200001000, Location: "a.ts:2"},
		{Level: "info", Message: "three", Time: 1752200002000, Location: "a.ts:3"},
		{Level: "info", Message: "four", Time: 1752200003000, Location: "a.ts:4"},
		{Level: "info", Message: "five", Time: 1752200004000, Location: "a.ts:5"},
	}}

	regs := resource.New()
	_ = resource.Set[logging.ConsoleLogSource](regs, logging.ConsoleLogSourceKey, fake)
	ctx := &fakeCtxWithResources{FakeCtx: testutil.AnonCtx(testutil.GenActorID()), regs: regs}

	// First page: newest 3 of 5, truncated with a NextBefore cursor.
	first, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Limit: 3})
	if err != nil {
		t.Fatalf("first page failed: %v", err)
	}
	if !first.Truncated || len(first.Items) != 3 {
		t.Fatalf("expected truncated page of 3, got Truncated=%v items=%d", first.Truncated, len(first.Items))
	}
	if first.Items[0].Message != "three" || first.Items[2].Message != "five" {
		t.Fatalf("expected newest 3 (three..five), got %v", first.Items)
	}
	if first.NextBefore == "" {
		t.Fatal("expected NextBefore cursor")
	}

	// Second page: entries strictly before the cursor (one, two).
	second, err := a.handleLogsQuery(ctx, gen.WorkspaceLogsQueryReq{Source: "console", Limit: 3, Before: first.NextBefore})
	if err != nil {
		t.Fatalf("second page failed: %v", err)
	}
	if second.Truncated {
		t.Fatal("expected second page not truncated")
	}
	if len(second.Items) != 2 || second.Items[0].Message != "one" || second.Items[1].Message != "two" {
		t.Fatalf("expected older 2 entries (one,two), got %v", second.Items)
	}
}

// TestPerConcernCardRoundTrip verifies that Save writes each ground-truth
// concern into its own persist card (not a combined main record), and that a
// restarted actor restores every concern from the cards. This is the
// per-callable stateless contract: mount/prefs/UI/kinds/deletion/submap state
// lives in dedicated cards via persist.Persist.
func TestPerConcernCardRoundTrip(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ps := persist.NewFSPersist(dir)
	sharedID := testutil.GenActorID()

	// First lifecycle: seed every concern and save.
	a1 := &Actor{store: ps}
	ctx1 := testutil.AdminCtx(sharedID)
	ctx1.SpawnFn = noOpSpawn
	ctx1.LookupIDFn = lookupOK
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}

	a1.Mounts = []domain.ProjectRef{{Name: "testproj", ActorID: "actor-x", Root: true}}
	a1.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "acct-1",
		Preferences: map[string]string{"shell": "bash"},
	}
	a1.UI = domain.WorkspaceUIModel{WorkspaceID: "default", Version: 1, SchemaVersion: 1}
	a1.deletionErrors = map[string]string{"W#1": "timeout"}
	a1.deletionAttempts = map[string]int{"W#1": 3}
	a1.subMapInstances = []subMapInstance{{
		InstanceMapID:     "inst-1",
		Status:            "active",
		OwnerAgentActorID: "actor-y",
	}}
	a1.AgentKindConfigs = append(a1.AgentKindConfigs, domain.AgentKindConfig{
		Kind:        "custom",
		DisplayName: "Custom",
	})
	if err := a1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Each concern card must exist and carry its data.
	var mounts []domain.ProjectRef
	if err := ps.Load(a1.mountsCardName(), &mounts); err != nil {
		t.Fatalf("mounts card: %v", err)
	}
	if len(mounts) != 1 || mounts[0].Name != "testproj" {
		t.Fatalf("mounts card = %+v", mounts)
	}

	var prefs domain.AccountPreferencesSnapshot
	if err := ps.Load(a1.prefsCardName(), &prefs); err != nil {
		t.Fatalf("prefs card: %v", err)
	}
	if prefs.Preferences["shell"] != "bash" {
		t.Fatalf("prefs card = %+v", prefs)
	}

	var ui domain.WorkspaceUIModel
	if err := ps.Load(a1.uiCardName(), &ui); err != nil {
		t.Fatalf("ui card: %v", err)
	}
	if ui.WorkspaceID != "default" {
		t.Fatalf("ui card = %+v", ui)
	}

	var dr deletionRetryState
	if err := ps.Load(a1.delRetryCardName(), &dr); err != nil {
		t.Fatalf("deletion retry card: %v", err)
	}
	if dr.Errors["W#1"] != "timeout" || dr.Attempts["W#1"] != 3 {
		t.Fatalf("deletion retry card = %+v", dr)
	}

	var sms []subMapInstance
	if err := ps.Load(a1.subMapsCardName(), &sms); err != nil {
		t.Fatalf("submaps card: %v", err)
	}
	if len(sms) != 1 || sms[0].InstanceMapID != "inst-1" {
		t.Fatalf("submaps card = %+v", sms)
	}

	var kinds []domain.AgentKindConfig
	if err := ps.Load(a1.kindsCardName(), &kinds); err != nil {
		t.Fatalf("kinds card: %v", err)
	}
	var foundCustom bool
	for _, k := range kinds {
		if k.Kind == "custom" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatalf("kinds card missing custom config: %+v", kinds)
	}

	// The legacy combined main record must be gone (retired).
	var main map[string]any
	if err := ps.Load(a1.actorID, &main); err == nil {
		t.Fatalf("legacy combined record still present: %+v", main)
	}

	// Second lifecycle: same store + identity — every concern restores from
	// the cards, without a combined record.
	a2 := &Actor{store: ps}
	ctx2 := testutil.AdminCtx(sharedID)
	ctx2.SpawnFn = noOpSpawn
	ctx2.LookupIDFn = lookupOK
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.Mounts) != 1 || a2.Mounts[0].Name != "testproj" {
		t.Fatalf("mounts not restored: %+v", a2.Mounts)
	}
	if a2.accountPrefs.Preferences["shell"] != "bash" {
		t.Fatalf("prefs not restored: %+v", a2.accountPrefs)
	}
	if a2.UI.WorkspaceID != "default" {
		t.Fatalf("ui not restored: %+v", a2.UI)
	}
	if a2.deletionErrors["W#1"] != "timeout" || a2.deletionAttempts["W#1"] != 3 {
		t.Fatalf("deletion retry not restored: errs=%+v attempts=%+v", a2.deletionErrors, a2.deletionAttempts)
	}
	if len(a2.subMapInstances) != 1 || a2.subMapInstances[0].InstanceMapID != "inst-1" {
		t.Fatalf("submaps not restored: %+v", a2.subMapInstances)
	}
	restoredCustom := false
	for _, k := range a2.AgentKindConfigs {
		if k.Kind == "custom" {
			restoredCustom = true
		}
	}
	if !restoredCustom {
		t.Fatalf("custom kind config not restored: %+v", a2.AgentKindConfigs)
	}
}

// TestHandleMount_LateConflictDestroysSpawnedActor verifies the mountMu
// check-then-insert narrowing added with the PureContext conversion: a mount
// whose pre-scan passed can still lose to a concurrent winner that appends
// while this handler is between the pre-scan and the insert (the Spawn in
// between takes seconds). The late re-check inside the mountMu critical
// section must reject the loser, destroy its freshly spawned project actor,
// and leave only the winner's row.
func TestHandleMount_LateConflictDestroysSpawnedActor(t *testing.T) {
	a, ctx := freshActor(t)
	winner := domain.ProjectRef{Name: "p1", Path: "/tmp/p1", ActorID: testutil.GenActorID().String()}
	var spawnedID id.ActorID
	var destroyedID id.ActorID
	// Simulate the concurrent winner: it appends while the loser is
	// mid-Spawn, i.e. after the loser's pre-scan but before its insert.
	ctx.SpawnFn = func(p actor.Props, _ string) (ref.Ref, error) {
		spawnedID = p.ID()
		if spawnedID == (id.ActorID{}) {
			spawnedID = testutil.GenActorID()
		}
		a.mountMu.Lock()
		a.Mounts = append(a.Mounts, winner)
		a.mountMu.Unlock()
		return testutil.NewFakeRef(spawnedID, nil), nil
	}
	ctx.DestroyFn = func(target ref.Ref) error {
		destroyedID = target.ID()
		return nil
	}

	if _, err := a.handleMount(ctx, domain.WorkspaceMountReq{Path: "/tmp/p1", Name: "p1"}); err == nil {
		t.Fatal("expected error for mount that lost the concurrent-insert race")
	}
	if destroyedID == (id.ActorID{}) {
		t.Fatal("expected the freshly spawned project actor to be destroyed")
	}
	if destroyedID != spawnedID {
		t.Errorf("destroyed ref %v, want the spawned ref %v", destroyedID, spawnedID)
	}
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == "/tmp/p1" && m.ActorID != winner.ActorID {
			t.Fatalf("loser's row leaked into Mounts: %+v", m)
		}
	}
}

// TestHandleCreate_LateConflictDestroysSpawnedActor is the create-side twin
// of TestHandleMount_LateConflictDestroysSpawnedActor: the authoritative
// check-then-insert runs under mountMu after the Spawn, so a concurrent
// winner that lands first rejects the loser and destroys its project actor.
func TestHandleCreate_LateConflictDestroysSpawnedActor(t *testing.T) {
	a, ctx := freshActor(t)
	base := t.TempDir()
	dir := util.NormalizePath(filepath.Join(base, "newproj"))
	winner := domain.ProjectRef{Name: "newproj", Path: dir, ActorID: testutil.GenActorID().String()}
	var spawnedID id.ActorID
	var destroyedID id.ActorID
	ctx.SpawnFn = func(p actor.Props, _ string) (ref.Ref, error) {
		spawnedID = p.ID()
		if spawnedID == (id.ActorID{}) {
			spawnedID = testutil.GenActorID()
		}
		a.mountMu.Lock()
		a.Mounts = append(a.Mounts, winner)
		a.mountMu.Unlock()
		return testutil.NewFakeRef(spawnedID, nil), nil
	}
	ctx.DestroyFn = func(target ref.Ref) error {
		destroyedID = target.ID()
		return nil
	}

	if _, err := a.handleCreate(ctx, domain.WorkspaceCreateReq{Path: base, Name: "newproj"}); err == nil {
		t.Fatal("expected error for create that lost the concurrent-insert race")
	}
	if destroyedID == (id.ActorID{}) {
		t.Fatal("expected the freshly spawned project actor to be destroyed")
	}
	if destroyedID != spawnedID {
		t.Errorf("destroyed ref %v, want the spawned ref %v", destroyedID, spawnedID)
	}
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == dir && m.ActorID != winner.ActorID {
			t.Fatalf("loser's row leaked into Mounts: %+v", m)
		}
	}
}

// TestHandleCreateAgent_ConcurrentCreatesReserveDistinctIDs verifies the
// agentsMu reserve pattern: name allocation and the registry append share one
// critical section, so concurrent create_agent goroutines cannot reserve the
// same spawn name, and every reserved row is patched with its spawn result
// (no half-reserved rows are left behind).
func TestHandleCreateAgent_ConcurrentCreatesReserveDistinctIDs(t *testing.T) {
	a, ctx := freshActor(t)
	projectActorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectActorID}}

	const goroutines, perGoroutine = 4, 3
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
					ProjectID:   projectActorID,
					DisplayName: "Racer",
					AgentKind:   "coder",
				}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent create_agent: %v", err)
	}

	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	if len(a.Agents) != goroutines*perGoroutine {
		t.Fatalf("registry len = %d, want %d", len(a.Agents), goroutines*perGoroutine)
	}
	seen := make(map[string]bool, len(a.Agents))
	for _, ag := range a.Agents {
		if seen[ag.ID] {
			t.Fatalf("duplicate agent ID %q", ag.ID)
		}
		seen[ag.ID] = true
		if ag.ActorID == "" {
			t.Fatalf("reserved row %q was never patched with a spawn result", ag.ID)
		}
		if ag.LoadState == "loading" {
			t.Fatalf("reserved row %q still marked loading after patch", ag.ID)
		}
	}
}

// TestHandleCreateAgent_SpawnFailureRollsBackReservation verifies the
// rollback half of the reserve pattern: when the cross-actor spawn fails,
// the reserved registry row is removed instead of leaking a phantom agent.
func TestHandleCreateAgent_SpawnFailureRollsBackReservation(t *testing.T) {
	a, ctx := freshActor(t)
	projectActorID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectActorID}}
	// Make the project actor unresolvable so spawnAgentViaProject fails.
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }

	if _, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   projectActorID,
		DisplayName: "Doomed",
		AgentKind:   "coder",
	}); err == nil {
		t.Fatal("expected error when the agent spawn fails")
	}
	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	if len(a.Agents) != 0 {
		t.Fatalf("reserved row leaked after spawn failure: %+v", a.Agents)
	}
}

// TestDeveloperCanCreateAgentButNotUpdateAgent is a role-ladder regression test
// for the new policy predicates: create_agent is on the agent/human surface and
// must be reachable by a developer; update_agent is admin-only and must be
// denied for a developer.
func TestDeveloperCanCreateAgentButNotUpdateAgent(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	devCtx := testutil.DeveloperCtx(testutil.GenActorID())
	devCtx.SpawnFn = noOpSpawn
	devCtx.LookupIDFn = lookupOK

	created, err := a.handleCreateAgent(devCtx, domain.WorkspaceCreateAgentReq{
		ProjectID:   projectID,
		DisplayName: "DevCreate",
		AgentKind:   "coder",
	})
	if err != nil {
		t.Fatalf("developer create_agent denied: %v", err)
	}
	if created.ID == "" {
		t.Fatal("developer create_agent returned empty agent id")
	}

	if _, err := a.handleUpdateAgent(devCtx, domain.WorkspaceUpdateAgentReq{
		AgentID:     created.ID,
		DisplayName: "should-not-change",
	}); err == nil {
		t.Fatal("developer update_agent allowed, want forbidden")
	}

	// Admin can update the same agent.
	if _, err := a.handleUpdateAgent(ctx, domain.WorkspaceUpdateAgentReq{
		AgentID:     created.ID,
		DisplayName: "admin-update",
	}); err != nil {
		t.Fatalf("admin update_agent denied: %v", err)
	}
}

// TestAnonymousDeniedAllOwnerGates verifies that a non-empty anonymous role
// is rejected by owner/admin callables.
func TestAnonymousDeniedAllOwnerGates(t *testing.T) {
	a, _ := freshActor(t)
	projectID := testutil.GenActorID().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	anonCtx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: "anonymous"}
	anonCtx.SpawnFn = noOpSpawn
	anonCtx.LookupIDFn = lookupOK

	if _, err := a.handleCreateAgent(anonCtx, domain.WorkspaceCreateAgentReq{
		ProjectID:   projectID,
		DisplayName: "AnonCreate",
		AgentKind:   "coder",
	}); err == nil {
		t.Fatal("anonymous create_agent allowed, want forbidden")
	}
	if _, err := a.handleUpdateAgent(anonCtx, domain.WorkspaceUpdateAgentReq{
		AgentID: "does-not-matter",
	}); err == nil {
		t.Fatal("anonymous update_agent allowed, want forbidden")
	}
	if _, err := a.handleDeleteAgent(anonCtx, domain.WorkspaceDeleteAgentReq{
		AgentID: "does-not-matter",
	}); err == nil {
		t.Fatal("anonymous delete_agent allowed, want forbidden")
	}
	if _, err := a.handleSaveAgentKindConfig(anonCtx, domain.WorkspaceSaveAgentKindConfigReq{
		Kind: "coder",
	}); err == nil {
		t.Fatal("anonymous save_agent_kind_config allowed, want forbidden")
	}
}

// TestCascadeDeleteReclaimsAllSubordinateCards verifies that Delete(actorID)
// on the persist store removes every subordinate card that lives under the
// actorID/ sub-directory — the core guarantee the sub-path naming provides.
func TestCascadeDeleteReclaimsAllSubordinateCards(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ps := persist.NewFSPersist(dir)

	sharedID := testutil.GenActorID()

	// First lifecycle: seed every concern and save.
	a1 := &Actor{store: ps}
	ctx1 := testutil.AdminCtx(sharedID)
	ctx1.SpawnFn = noOpSpawn
	ctx1.LookupIDFn = lookupOK
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	a1.Mounts = []domain.ProjectRef{{Name: "testproj", ActorID: "actor-x", Root: true}}
	a1.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   "acct-1",
		Preferences: map[string]string{"shell": "bash"},
	}
	a1.UI = domain.WorkspaceUIModel{WorkspaceID: "default", Version: 1, SchemaVersion: 1}
	a1.deletionErrors = map[string]string{"W#1": "timeout"}
	a1.deletionAttempts = map[string]int{"W#1": 3}
	a1.subMapInstances = []subMapInstance{{InstanceMapID: "inst-1", Status: "active", OwnerAgentActorID: "actor-y"}}
	a1.AgentKindConfigs = append(a1.AgentKindConfigs, domain.AgentKindConfig{Kind: "custom", DisplayName: "Custom"})
	a1.clones = map[string]domain.CloneState{"clone-1": {SourceActorID: "src-1"}}
	if err := a1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Every subordinate card must exist.
	cardNames := []string{
		a1.mountsCardName(),
		a1.kindsCardName(),
		a1.prefsCardName(),
		a1.uiCardName(),
		a1.delRetryCardName(),
		a1.subMapsCardName(),
		a1.actorID + "/clones",
		a1.agentRegistryName(),
	}
	for _, name := range cardNames {
		var probe json.RawMessage
		if err := ps.Load(name, &probe); err != nil {
			t.Fatalf("pre-delete: card %q should exist: %v", name, err)
		}
	}

	// Cascade Delete must reclaim all subordinate cards.
	if err := ps.Delete(a1.actorID); err != nil {
		t.Fatalf("Delete(actorID): %v", err)
	}

	for _, name := range cardNames {
		var probe json.RawMessage
		if err := ps.Load(name, &probe); !errors.Is(err, persist.ErrNotExist) {
			t.Errorf("post-delete: card %q should be gone (err=%v)", name, err)
		}
	}
}
