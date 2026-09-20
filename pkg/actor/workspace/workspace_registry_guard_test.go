package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// TestSaveAgentRegistry_RefusesWhenCardNotLoaded pins the fail-stop guard
// against the restart-wipes-registry failure: Load() runs first and flips
// registryCardLoaded only after the .ragents card was read (or is
// authoritatively absent). A partial/failed load must leave every registry
// write refused, so an empty derived cache can never overwrite the card.
func TestSaveAgentRegistry_RefusesWhenCardNotLoaded(t *testing.T) {
	// Actor constructed without OnInit/Load: gate stays closed.
	ps := persist.NewFSPersist(t.TempDir())
	a := &Actor{store: ps, actorID: "ws-test"}
	a.Agents = []domain.AgentRef{{ID: "W1", ActorID: genID()}}
	if err := a.saveAgentRegistry(); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected refusal before Load, got %v", err)
	}

	// Real corruption shape: the card exists but cannot decode. Load()
	// returns the error (OnInit logs and continues), and every save stays
	// refused — the card content survives untouched.
	if err := ps.Save(a.agentRegistryName(), "this is not a []AgentRef"); err != nil {
		t.Fatal(err)
	}
	a2 := &Actor{store: ps, actorID: a.actorID}
	if err := a2.Load(); err == nil {
		t.Fatal("expected Load error on corrupt registry card")
	}
	if a2.registryCardLoaded.Load() {
		t.Fatal("registryCardLoaded must stay false after a failed card load")
	}
	a2.Agents = nil
	if err := a2.saveAgentRegistry(); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected refusal after failed load, got %v", err)
	}

	// A successful load opens the gate: fresh store → card absent →
	// ErrNotExist is authoritative empty, saves proceed.
	a3, _ := freshActor(t)
	a3.Agents = []domain.AgentRef{{ID: "W2", ActorID: genID()}}
	if err := a3.saveAgentRegistry(); err != nil {
		t.Fatalf("expected save after successful load, got %v", err)
	}
	if !a3.registryCardLoaded.Load() {
		t.Fatal("registryCardLoaded must be true after freshActor's Load")
	}
}
