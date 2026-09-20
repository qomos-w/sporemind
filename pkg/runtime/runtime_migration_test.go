package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// writeLegacyStateDir seeds the pre-doc-path persist layout: one directory
// per name directly holding a state.json blob (<name>/state.json).
func writeLegacyStateDir(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedLegacyDataDir writes a pre-upgrade DataDir: the legacy <name>/state.json
// layout, workspace flat suffix cards ("<actorID>.rmounts" etc.), and a
// registry pinning the old workspace actor ID. It returns that ID.
func seedLegacyDataDir(t *testing.T, dir string) (wsID string) {
	t.Helper()
	// The ID a pre-upgrade install recorded in registry.json. Startup must
	// restore exactly this ID — a drifted ID would orphan the persisted cards.
	cid, err := identity.NewCanonicalID(1700000000000, 1, 0, 4242)
	if err != nil {
		t.Fatal(err)
	}
	wsID = cid.String()

	wsRoot := filepath.Join(dir, ".actors", "workspace")

	// Legacy layout: main combined record plus flat suffix cards.
	writeLegacyStateDir(t, wsRoot, wsID, `{"Version":3}`)
	writeLegacyStateDir(t, wsRoot, wsID+".rmounts", `[{"Name":"legacy-project","Path":"/legacy/path","Root":true}]`)
	writeLegacyStateDir(t, wsRoot, wsID+".ragents", `[{"Id":"agent-legacy","ActorId":"`+wsID+`","DisplayName":"Legacy Agent","AgentKind":"worker"}]`)

	reg, err := json.Marshal(map[string]any{
		"nodeId":   "1",
		"services": map[string]string{"workspace": wsID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), reg, 0o644); err != nil {
		t.Fatal(err)
	}
	return wsID
}

// bootRuntime cold-starts the full runtime against the current test DataDir
// and returns the started app, leaving shutdown to the caller's cleanup.
//
// Startup sequencing mirrors TestNew_FullAppStartup: WaitForAllCellsStart
// alone races — it returns nil while a.cells is still empty, before Run has
// registered the root cell — so we first block on the root WatchStarted
// event, which by definition means the root cell exists and its OnInit (the
// persist layout migration consumer) has run.
func bootRuntime(t *testing.T) app.App {
	t.Helper()
	a, err := runtime.New(runtime.Config{NoGateway: true, Children: defaultChildren()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("app.Run did not return after cancel")
		}
	})

	sub, err := a.Events().Subscribe(a.Self().ID(), 0, []actor.WatchKind{actor.WatchStarted})
	if err != nil {
		t.Fatalf("Events.Subscribe: %v", err)
	}
	defer sub.Close()
	if _, err := sub.Recv(); err != nil {
		t.Fatalf("waiting for root start: %v", err)
	}
	if err := a.WaitForAllCellsStart(10 * time.Second); err != nil {
		t.Fatalf("WaitForAllCellsStart: %v (services=%v)", err, a.Service().Names())
	}
	return a
}

// TestColdStart_LegacyPersistLayoutMigration is the end-to-end migration
// rehearsal required by the persist-layer generalization workflow (T7): a
// DataDir built by a pre-upgrade binary (<name>/state.json directories plus
// the workspace's legacy flat suffix cards "<actorID>.rmounts" etc.) must
// come up on the new document layout with actor IDs and state intact and no
// orphaned legacy artifacts.
//
// Chain under test, all through the real cold-start path (runtime.New →
// MigrateFSLegacyLayout → registry WithID restore → workspace.OnInit Load):
//   - <actorID>/state.json           → <actorID>.json         (main record)
//   - <actorID>.rmounts/state.json   → <actorID>.rmounts.json (layout pass)
//     → <actorID>/rmounts.json       (workspace T4 card migration on Load)
func TestColdStart_LegacyPersistLayoutMigration(t *testing.T) {
	dir := t.TempDir()
	wsID := seedLegacyDataDir(t, dir)
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	// Cold start on the old DataDir.
	a := bootRuntime(t)

	wsRoot := filepath.Join(dir, ".actors", "workspace")

	// 1. Actor ID restored from the registry — no ID drift.
	wsRef, ok := a.LookupService("workspace")
	if !ok {
		t.Fatalf("workspace service not found after startup (services=%v)", a.Service().Names())
	}
	if got := wsRef.ID().String(); got != wsID {
		t.Errorf("workspace actor ID drifted: got %q, want %q", got, wsID)
	}

	// 2. Actor state restored: the migrated mounts card is visible through
	// the public callable.
	call := wsRef.Invoke(context.Background(), "workspace.list_project", nil)
	if call == nil {
		t.Fatal("workspace.list_project invoke returned nil")
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		t.Fatalf("workspace.list_project: %v", err)
	}
	var list domain.ProjectRefListResp
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list_project response %q: %v", raw, err)
	}
	found := false
	for _, p := range list.Items {
		if p.Name == "legacy-project" && p.Path == "/legacy/path" {
			found = true
		}
	}
	if !found {
		t.Errorf("seeded legacy mount not restored; got %+v", list.Items)
	}

	// 3. Layout migration completed with no orphaned legacy artifacts.
	assertGone := func(path string) {
		t.Helper()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("orphaned legacy artifact %s still exists (stat err %v)", path, err)
		}
	}
	assertGone(filepath.Join(wsRoot, wsID, "state.json"))    // legacy main blob
	assertGone(filepath.Join(wsRoot, wsID+".rmounts"))       // legacy suffix dir
	assertGone(filepath.Join(wsRoot, wsID+".rmounts.json"))  // intermediate flat doc
	assertGone(filepath.Join(wsRoot, wsID+".ragents"))       // legacy suffix dir
	assertGone(filepath.Join(wsRoot, wsID+".ragents.json"))  // intermediate flat doc

	// Migrated cards now live at their sub-path names under <actorID>/.
	for _, doc := range []string{"rmounts", "ragents"} {
		if _, err := os.Stat(filepath.Join(wsRoot, wsID, doc+".json")); err != nil {
			t.Errorf("migrated card %s/%s.json missing: %v", wsID, doc, err)
		}
	}
	// The main record migrated to the document layout too; it is reclaimed
	// together with the subtree by Delete(actorID).
	if _, err := os.Stat(filepath.Join(wsRoot, wsID+".json")); err != nil {
		t.Errorf("migrated main record %s.json missing: %v", wsID, err)
	}

	// No state.json blob or atomic-write temp file may survive anywhere
	// under the actor data root.
	actorsRoot := filepath.Join(dir, ".actors")
	walkErr := filepath.WalkDir(actorsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "state.json" || filepath.Ext(d.Name()) == ".tmp" {
			t.Errorf("orphan file left after migration: %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", actorsRoot, walkErr)
	}

	// 4. Registry still pins the same ID after a full start cycle.
	regAfter, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("read registry after startup: %v", err)
	}
	var regDecoded struct {
		Services map[string]string `json:"services"`
	}
	if err := json.Unmarshal(regAfter, &regDecoded); err != nil {
		t.Fatalf("decode registry after startup: %v", err)
	}
	if got := regDecoded.Services["workspace"]; got != wsID {
		t.Errorf("registry workspace ID after startup: got %q, want %q", got, wsID)
	}
}
