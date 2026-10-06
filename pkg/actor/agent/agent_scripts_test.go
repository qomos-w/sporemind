package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newScriptsTestActor(t *testing.T) *Actor {
	t.Helper()
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	a := &Actor{actorID: testutil.GenActorID().String()}
	if err := a.loadSavedScripts(); err != nil {
		t.Fatalf("loadSavedScripts: %v", err)
	}
	return a
}

func TestScriptSaveUpsertAndPersist(t *testing.T) {
	a := newScriptsTestActor(t)

	resp, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:        "sum-range",
		Description: "sum integers 1..n",
		Script:      "fun run(n: int): int { return n * (n + 1) / 2 }",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if resp.Name != "sum-range" || resp.Updated {
		t.Fatalf("first save resp = %+v, want {sum-range Updated=false}", resp)
	}

	// Upsert by the same Name reports Updated and replaces the body.
	resp, err = a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:   "sum-range",
		Script: "fun run(n: int): int { var s: int = 0\nreturn s }",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !resp.Updated {
		t.Fatal("upsert Updated = false, want true")
	}
	if got := a.savedScripts["sum-range"].Script; !strings.Contains(got, "var s: int = 0") {
		t.Fatalf("upsert did not replace body: %q", got)
	}

	// A fresh actor over the same data dir restores the persisted doc.
	// Upsert is a full-record replace: the second save carried no
	// Description, so the reloaded record keeps the new body and drops the
	// old description.
	b := &Actor{actorID: a.actorID}
	if err := b.loadSavedScripts(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.savedScripts) != 1 {
		t.Fatalf("persisted round-trip = %+v", b.savedScripts)
	}
	got := b.savedScripts["sum-range"]
	if got.Description != "" || !strings.Contains(got.Script, "var s: int = 0") {
		t.Fatalf("reloaded record = %+v, want replaced body with dropped description", got)
	}
}

func TestScriptSaveValidation(t *testing.T) {
	a := newScriptsTestActor(t)

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Script: "fun run() {}"}); err == nil {
		t.Fatal("empty Name must error")
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "x"}); err == nil {
		t.Fatal("empty Script must error")
	}
	// Name must be trimmed: " x " upserts "x".
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: " x ", Script: "fun run() {}"}); err != nil {
		t.Fatalf("trimmed name save: %v", err)
	}
	if _, ok := a.savedScripts["x"]; !ok {
		t.Fatal("name was not trimmed to x")
	}
}

func TestScriptDelete(t *testing.T) {
	a := newScriptsTestActor(t)
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "gone", Script: "fun run() {}"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	resp, err := a.handleScriptDelete(nil, domain.AgentScriptDeleteReq{Name: "gone"})
	if err != nil || !resp.Deleted {
		t.Fatalf("delete = %+v, %v; want Deleted=true", resp, err)
	}
	if _, ok := a.savedScripts["gone"]; ok {
		t.Fatal("script still present after delete")
	}

	// The deletion is durable: a fresh actor sees nothing.
	b := &Actor{actorID: a.actorID}
	if err := b.loadSavedScripts(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.savedScripts) != 0 {
		t.Fatalf("deleted script survived reload: %+v", b.savedScripts)
	}

	// Unknown name is an explicit error.
	if _, err := a.handleScriptDelete(nil, domain.AgentScriptDeleteReq{Name: "nope"}); err == nil {
		t.Fatal("deleting unknown Name must error")
	}
}

func TestBuildSavedScriptsBlock(t *testing.T) {
	a := newScriptsTestActor(t)
	if a.buildSavedScriptsBlock() != nil {
		t.Fatal("empty set must yield nil block")
	}

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "b-second", Description: "second", Script: "fun runB() {}"}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "a-first", Description: "first", Script: "fun runA() {}"}); err != nil {
		t.Fatalf("save a: %v", err)
	}

	block := a.buildSavedScriptsBlock()
	if block == nil {
		t.Fatal("nil block with saved scripts")
	}
	if !strings.Contains(block.Text, "## Saved Scripts") {
		t.Errorf("block missing header: %q", block.Text)
	}
	// Sorted by name; each entry carries description + fenced body.
	aIdx := strings.Index(block.Text, "### a-first")
	bIdx := strings.Index(block.Text, "### b-second")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Errorf("entries not sorted by name (a@%d b@%d)", aIdx, bIdx)
	}
	if !strings.Contains(block.Text, "```spore\nfun runA() {}\n```") {
		t.Errorf("fenced body missing: %q", block.Text)
	}
}
