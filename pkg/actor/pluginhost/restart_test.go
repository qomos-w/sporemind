package pluginhost

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestArtifactReloadPersistenceUsesCommittedCandidate(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	candidate := gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "native.test", Runtime: "native", Version: "2.0.0"}, ArtifactPath: "candidate.dll", ArtifactHash: "candidate", Abi: gen.PluginAbi{Name: "spore-plugin", Version: 1}}
	a := &Actor{actorID: "pluginhost-reload", store: store, ArtifactLoads: map[string]gen.PluginArtifactLoadReq{candidate.Manifest.ID: candidate}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	restored := &Actor{actorID: "pluginhost-reload", store: store}
	if err := restored.Load(); err != nil {
		t.Fatal(err)
	}
	got := restored.ArtifactLoads[candidate.Manifest.ID]
	if got.Manifest.Version != "2.0.0" || got.ArtifactPath != "candidate.dll" || got.ArtifactHash != "candidate" {
		t.Fatalf("restored candidate = %+v", got)
	}
}

func TestArtifactLoadStatePersists(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	req := gen.PluginArtifactLoadReq{
		Manifest:     gen.AppManifest{ID: "native.test", Runtime: "native"},
		ArtifactPath: "artifact.dll",
		ArtifactHash: "hash",
		Abi:          gen.PluginAbi{Name: "spore-plugin", Version: 1},
	}
	a := &Actor{actorID: "pluginhost-test", store: store, ArtifactLoads: map[string]gen.PluginArtifactLoadReq{req.Manifest.ID: req}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	restored := &Actor{actorID: "pluginhost-test", store: store}
	if err := restored.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := restored.ArtifactLoads[req.Manifest.ID]
	if !ok || got.ArtifactPath != req.ArtifactPath || got.ArtifactHash != req.ArtifactHash || got.Abi.Name != req.Abi.Name {
		t.Fatalf("restored artifact load = %+v", got)
	}
}
