package appmanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/builtin/sporecall"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestEnsureBuiltinSporeAppSteadyStateSkip: with a live record carrying the
// current builtin package hash, the OnStart ensure step must be a no-op — no
// unregister, no respawn, the record is left exactly as restored.
func TestEnsureBuiltinSporeAppSteadyStateSkip(t *testing.T) {
	req := sporecall.RegisterReq()
	wantHash, err := canonicalPackageHash(req.Manifest, req.EntryModule, req.Modules, req.Assets, req.SchemaDescriptors, req.Abi, req.ArtifactHash)
	if err != nil {
		t.Fatalf("canonicalPackageHash: %v", err)
	}
	a := &Actor{
		Apps:    map[string]gen.AppManifest{req.Manifest.ID: req.Manifest},
		Records: map[string]appRecord{
			req.Manifest.ID: {Manifest: req.Manifest, State: stateRunning, PackageHash: wantHash, Origin: "builtin", ActorID: "01a0"},
		},
		children: map[string]string{req.Manifest.ID: "01a0"},
	}
	a.ensureBuiltinSporeApp(nil, req)
	record := a.Records[req.Manifest.ID]
	if record.State != stateRunning || record.ActorID != "01a0" || a.children[req.Manifest.ID] != "01a0" {
		t.Fatalf("steady-state ensure mutated the record: %+v children=%v", record, a.children)
	}
}

// TestBuiltinSporeRegisterReqsValid: every builtin req must satisfy
// doRegister's package preconditions (entry module present, manifest fields,
// paired callable schemas) so boot registration cannot fail structurally.
func TestBuiltinSporeRegisterReqsValid(t *testing.T) {
	for _, req := range builtinSporeRegisterReqs() {
		if req.EntryModule == "" || len(req.Modules) == 0 {
			t.Fatalf("builtin %s: entry module and modules required", req.Manifest.ID)
		}
		if _, ok := req.Modules[req.EntryModule]; !ok {
			t.Fatalf("builtin %s: entry module %q missing", req.Manifest.ID, req.EntryModule)
		}
		m := req.Manifest
		if m.ID == "" || m.Namespace == "" || m.Name == "" || m.Version == "" {
			t.Fatalf("builtin manifest incomplete: %+v", m)
		}
		if m.Runtime != "spore" || m.ProtocolVersion <= 0 || len(m.Callables) == 0 {
			t.Fatalf("builtin %s: invalid spore manifest: %+v", m.ID, m)
		}
	}
}
