package appmanager

import (
	"slices"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func newStatusTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		Apps:              map[string]gen.AppManifest{},
		Records:           map[string]appRecord{},
		children:          map[string]string{},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		store:             persist.NewFSPersist(t.TempDir()),
	}
}

func TestStatusFromRecordMirrorsDeclaredPermissions(t *testing.T) {
	a := newStatusTestActor(t)
	manifest := gen.AppManifest{
		ID:          "app.status",
		Runtime:     "spore",
		Version:     "1.0.0",
		Namespace:   "status.test",
		Permissions: []string{appbinding.CapFSRead, appbinding.CapFSWrite, appbinding.CapLLMInvoke},
	}
	record := appRecord{Manifest: manifest, State: "running"}

	status := a.statusFromRecord(manifest, record)

	if !slices.Equal(status.Permissions, manifest.Permissions) {
		t.Fatalf("Permissions = %v, want %v", status.Permissions, manifest.Permissions)
	}
	wantGranted := []string{appbinding.CapFSRead, appbinding.CapFSWrite, appbinding.CapLLMInvoke}
	if !slices.Equal(status.GrantedCapabilities, wantGranted) {
		t.Fatalf("GrantedCapabilities = %v, want %v", status.GrantedCapabilities, wantGranted)
	}
}

func TestStatusFromRecordSortsGrantedCapabilities(t *testing.T) {
	a := newStatusTestActor(t)
	manifest := gen.AppManifest{
		ID:          "app.sorted",
		Runtime:     "spore",
		Version:     "1.0.0",
		Namespace:   "sorted.test",
		Permissions: []string{appbinding.CapShellExec, appbinding.CapLLMInvoke, appbinding.CapFSRead},
	}
	status := a.statusFromRecord(manifest, appRecord{Manifest: manifest, State: "running"})

	want := []string{appbinding.CapFSRead, appbinding.CapLLMInvoke, appbinding.CapShellExec}
	if !slices.Equal(status.GrantedCapabilities, want) {
		t.Fatalf("GrantedCapabilities = %v, want sorted %v", status.GrantedCapabilities, want)
	}
}

func TestStatusFromRecordCarriesGeneration(t *testing.T) {
	a := &Actor{
		Apps:              map[string]gen.AppManifest{},
		Records:           map[string]appRecord{},
		children:          map[string]string{},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		store:             persist.NewFSPersist(t.TempDir()),
	}

	manifest := gen.AppManifest{ID: "app.gen", Runtime: "native", Version: "1.0.0", Namespace: "gen.test"}
	record := appRecord{Manifest: manifest, State: "running", Generation: 7}

	status := a.statusFromRecord(manifest, record)
	if status.Generation != 7 {
		t.Fatalf("Generation = %d, want 7", status.Generation)
	}
}
