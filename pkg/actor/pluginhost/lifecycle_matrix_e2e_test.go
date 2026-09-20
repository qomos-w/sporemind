package pluginhost_test

// Matrix-level e2e for the plugin lifecycle (workflow S3):
//
//  1. TestMatrixSubprocessAllOpsNoRestart — every appmanager operation
//     (register → reload → unload → load → unregister) over the subprocess
//     transport is immediate, over the REAL wire between the appmanager and
//     pluginhost actors in one live runtime. No operation ever produces
//     restart_pending (subprocess semantics unchanged: hot swap, kill+respawn).
//
//  2. TestMatrixInProcessStagingThenRestartActivation — the three in-process
//     staging paths (re-register a different artifact / reload / load while
//     unload_pending) each end in restart_pending with the new artifact
//     persisted (success, not error, old mapping kept alive). A simulated
//     host restart (fresh runtime on the same data dir, pluginhost started
//     before appmanager so the spawn-loop cross-validation is reachable)
//     activates all three to running; a fourth app whose artifact file was
//     deleted is honestly marked failed with no false running (B1).
//
//  3. TestMatrixInProcessRestartHonestyProductionOrder — the default
//     production start order (pluginhost after appmanager) is pinned: the
//     attestation protects the pluginhost's own restore from a 误杀 (the
//     spawn loop must keep persisted state when the pluginhost is not yet
//     reachable), and an explicit plugin_load still activates afterwards.
//
// The pluginhost actor boots inside the real runtime with a stub opener
// (SetOpenerOverrideForTest) standing in for real shared libraries/child
// processes, so the loader's real blocker logic (unload-pending, different
// artifact, idempotent same-artifact branch) and the real actor messaging
// round-trip are exercised end to end.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/appmanager"
	phactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// matrix state strings (mirror of the appmanager state contract; plain
// AppStatus.State strings, no schema involvement).
const mRestartPending = "restart_pending"

// matrixStubOpener is a fake ArtifactOpener standing in for real native
// artifacts. It returns a trivial invoke/closer so the loader's real
// in-process blocker logic (unload-pending / different-artifact) and the
// idempotent same-artifact branch run against genuine loader state.
type matrixStubOpener struct {
	opens int
}

func (o *matrixStubOpener) Open(_ gen.PluginAbi, _ string, _ string, _ string, _ map[string]struct{}, _ []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), pluginhost.InvokeStreamFunc, func() error, string, error) {
	o.opens++
	return func(context.Context, string, []byte) ([]byte, error) { return []byte(`{"pong":"ok"}`), nil }, nil, func() error { return nil }, "", nil
}

// matrixHost is one live runtime with the real appmanager + pluginhost
// actors wired over real actor messaging.
type matrixHost struct {
	t       *testing.T
	cancel  context.CancelFunc
	handle  *runtime.Handle
	am      ref.Ref
	ph      ref.Ref
	phActor *phactor.Actor
	stopped bool
}

func bootMatrixHost(t *testing.T, dataDir string, pluginhostFirst bool, opener pluginhost.ArtifactOpener) *matrixHost {
	t.Helper()
	phActor := &phactor.Actor{}
	phactor.SetOpenerOverrideForTest(phActor, opener)

	appSpec := runtime.ChildSpec{Name: "appmanager", Factory: func() actor.Actor { return appmanager.NewActor() }, RequirePersistent: true, Role: "system", Planner: true}
	phSpec := runtime.ChildSpec{Name: "pluginhost", Factory: func() actor.Actor { return phActor }, RequirePersistent: true, Role: "system", Planner: true}
	if pluginhostFirst {
		// pluginhost fully starts (ArtifactLoads restored) before the
		// appmanager's OnStart spawn loop, so restart activation is
		// cross-validated over the real wire.
		appSpec.DependsOn = []string{"pluginhost"}
	} else {
		// Production order: pluginhost starts after the appmanager; the
		// spawn loop cannot reach it at OnStart and must defer (keep the
		// persisted state) instead of failing records.
		phSpec.DependsOn = []string{"appmanager"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	h, err := runtime.Bootstrap(ctx, runtime.Config{NoGateway: true, Children: []runtime.ChildSpec{phSpec, appSpec}})
	if err != nil {
		cancel()
		t.Fatalf("Bootstrap: %v", err)
	}
	// gospore App exposes no ready signal; give OnStart time to register
	// handlers and expose the services.
	time.Sleep(600 * time.Millisecond)

	app := h.App()
	am, ok := app.LookupService("appmanager")
	if !ok {
		cancel()
		_ = h.Wait()
		t.Fatal("appmanager service not registered")
	}
	ph, ok := app.LookupService("pluginhost")
	if !ok {
		cancel()
		_ = h.Wait()
		t.Fatal("pluginhost service not registered")
	}
	m := &matrixHost{t: t, cancel: cancel, handle: h, am: am, ph: ph, phActor: phActor}
	t.Cleanup(m.stop)
	return m
}

// stop shuts the host down exactly once (idempotent for t.Cleanup safety).
func (m *matrixHost) stop() {
	if m.stopped {
		return
	}
	m.stopped = true
	m.cancel()
	if err := m.handle.Wait(); err != nil {
		m.t.Errorf("host stop: %v", err)
	}
}

// callMatrix invokes a callable over the real in-process wire and decodes
// the reply via a JSON round-trip (same pattern as appmanager system e2e).
// Error-only callables (no response struct) end their stream with io.EOF —
// that is the successful void reply, not a failure.
func callMatrix(t *testing.T, svc ref.Ref, callID string, payload any, out any) {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		call := svc.Invoke(context.Background(), callID, payload)
		if call == nil {
			t.Fatalf("%s: invoke returned nil", callID)
		}
		v, err := call.Recv()
		_ = call.Close()
		if err != nil {
			if errors.Is(err, io.EOF) && out == nil {
				return // void callable completed
			}
			if strings.Contains(err.Error(), "not registered") && attempt < 49 {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			t.Fatalf("%s: %v", callID, err)
		}
		raw, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			t.Fatalf("%s: marshal reply: %v", callID, marshalErr)
		}
		if out != nil {
			if unmarshalErr := json.Unmarshal(raw, out); unmarshalErr != nil {
				t.Fatalf("%s: decode reply %s: %v", callID, raw, unmarshalErr)
			}
		}
		return
	}
}

// invokeMatrix is a non-fatal callMatrix (returns the error instead of
// failing the test) for assertions that must inspect error paths. A void
// callable completion (io.EOF with no output target) is success.
func invokeMatrix(m *matrixHost, svc ref.Ref, callID string, payload any, out any) error {
	call := svc.Invoke(context.Background(), callID, payload)
	if call == nil {
		return fmt.Errorf("%s: invoke returned nil", callID)
	}
	v, err := call.Recv()
	_ = call.Close()
	if err != nil {
		if errors.Is(err, io.EOF) && out == nil {
			return nil
		}
		return fmt.Errorf("%s: %w", callID, err)
	}
	raw, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		return fmt.Errorf("%s: marshal reply: %w", callID, marshalErr)
	}
	if out != nil {
		if unmarshalErr := json.Unmarshal(raw, out); unmarshalErr != nil {
			return fmt.Errorf("%s: decode reply %s: %w", callID, raw, unmarshalErr)
		}
	}
	return nil
}

// --- fixtures ---

func matrixManifest(id, version string) gen.AppManifest {
	return gen.AppManifest{
		ID: id, Name: "Matrix-" + id, Version: version, Runtime: "native",
		Namespace: "sporeapp." + id, ProtocolVersion: 2,
		Permissions: []string{},
		Callables: []gen.AppCallableDescriptor{
			{ID: "ping", RequestSchema: "Empty", ResponseSchema: "Empty"},
		},
	}
}

func inprocMatrixAbi() *gen.PluginAbi {
	return &gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: pluginhost.IsolationInProcess, TrustClass: pluginhost.TrustFirstParty, Signer: "sporemind.first-party",
	}
}

func subprocMatrixAbi() *gen.PluginAbi {
	return &gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: pluginhost.IsolationSubprocess, TrustClass: pluginhost.TrustFirstParty, Signer: "sporemind.first-party",
	}
}

func matrixArtifact(t *testing.T, dir, name, content string) (path, hash string) {
	t.Helper()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, hashOf(content)
}

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// matrixInstallZip builds an appmanager.install_local package: manifest +
// abi.json + a compiled artifact + the required entry module.
func matrixInstallZip(t *testing.T, manifest gen.AppManifest, abi *gen.PluginAbi, artifactName string, artifact []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	mj, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	add("app.manifest.json", mj)
	aj, err := json.Marshal(abi)
	if err != nil {
		t.Fatal(err)
	}
	add("abi.json", aj)
	add(artifactName, artifact)
	add("main.gen.go", []byte("package main\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// --- wire helpers over the matrix host ---

func (m *matrixHost) install(id, version string, abi *gen.PluginAbi, artifactContent string) (gen.AppStatus, error) {
	m.t.Helper()
	name := "plugin.so"
	if abi.Isolation == pluginhost.IsolationSubprocess {
		name = "plugin.exe"
	}
	zipBytes := matrixInstallZip(m.t, matrixManifest(id, version), abi, name, []byte(artifactContent))
	var resp gen.AppManagerInstallLocalResp
	if err := invokeMatrix(m, m.am, "appmanager.install_local", gen.AppManagerInstallLocalReq{PackageData: zipBytes}, &resp); err != nil {
		return gen.AppStatus{}, err
	}
	return resp.Status, nil
}

// registerArtifact registers a plugin whose artifact was pre-loaded into
// the pluginhost by the caller (mirror of the project/install flows' inner
// contract: appmanager.register itself does not load the artifact). The
// caller controls the artifact path so the record's ArtifactPath is known.
func (m *matrixHost) registerArtifact(id string, manifest gen.AppManifest, abi *gen.PluginAbi, artifactPath, artifactHash string) error {
	m.t.Helper()
	var loadResp gen.PluginArtifactLoadResp
	if err := invokeMatrix(m, m.ph, "pluginhost.artifact_load", gen.PluginArtifactLoadReq{
		Manifest: manifest, Abi: *abi, ArtifactPath: artifactPath, ArtifactHash: artifactHash,
	}, &loadResp); err != nil {
		return fmt.Errorf("pre-load artifact: %w", err)
	}
	var status gen.AppStatus
	if err := invokeMatrix(m, m.am, "appmanager.register", gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: artifactPath, ArtifactHash: loadResp.ArtifactHash, Abi: abi,
	}, &status); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	return nil
}

func (m *matrixHost) get(id string) (gen.AppManagerGetResp, error) {
	m.t.Helper()
	var resp gen.AppManagerGetResp
	err := invokeMatrix(m, m.am, "appmanager.get", gen.AppManagerGetReq{ID: id}, &resp)
	return resp, err
}

func (m *matrixHost) unload(id string) (gen.AppManagerPluginUnloadResp, error) {
	m.t.Helper()
	var resp gen.AppManagerPluginUnloadResp
	err := invokeMatrix(m, m.am, "appmanager.plugin_unload", gen.AppManagerPluginUnloadReq{ID: id}, &resp)
	return resp, err
}

func (m *matrixHost) load(id string) (gen.AppManagerPluginLoadResp, error) {
	m.t.Helper()
	var resp gen.AppManagerPluginLoadResp
	err := invokeMatrix(m, m.am, "appmanager.plugin_load", gen.AppManagerPluginLoadReq{ID: id}, &resp)
	return resp, err
}

func (m *matrixHost) reload(id string, candidateManifest gen.AppManifest, candidateAbi *gen.PluginAbi, candidatePath, candidateHash string) (gen.AppManagerReloadResp, error) {
	m.t.Helper()
	var resp gen.AppManagerReloadResp
	err := invokeMatrix(m, m.am, "appmanager.reload", gen.AppManagerReloadReq{
		ID: id, CandidateManifest: &candidateManifest, CandidateAbi: candidateAbi,
		CandidateArtifactPath: candidatePath, CandidateArtifactHash: candidateHash,
	}, &resp)
	return resp, err
}

func (m *matrixHost) unregister(id string) error {
	m.t.Helper()
	return invokeMatrix(m, m.am, "appmanager.unregister", gen.AppManagerUnregisterReq{ID: id}, nil)
}

// invokePlugin exercises the real native routing: the loader installed the
// handler map and the pluginhost dispatches the namespaced callable.
func (m *matrixHost) invokePlugin(id, callable string) ([]byte, error) {
	m.t.Helper()
	var resp gen.PluginInvokeResp
	if err := invokeMatrix(m, m.ph, "pluginhost.invoke", gen.PluginInvokeReq{ID: id, Callable: callable}, &resp); err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

// stateSet writes app-private state for id through the pluginhost wire.
func (m *matrixHost) stateSet(id, key string, value []byte) error {
	m.t.Helper()
	return invokeMatrix(m, m.ph, "pluginhost.state_set", gen.PluginStateSetReq{Plugin: id, Key: key, Value: value}, nil)
}

// stateGet reads app-private state for id through the pluginhost wire.
func (m *matrixHost) stateGet(id, key string) (gen.PluginStateGetResp, error) {
	m.t.Helper()
	var resp gen.PluginStateGetResp
	err := invokeMatrix(m, m.ph, "pluginhost.state_get", gen.PluginStateGetReq{Plugin: id, Key: key}, &resp)
	return resp, err
}

// plugins returns the pluginhost's physical plugin view (ID → status /
// artifact hash). The reply crosses the wire as a plain-struct schema whose
// keys may be cased by the codec, so fields are matched case-insensitively.
func (m *matrixHost) plugins() map[string]map[string]string {
	m.t.Helper()
	var raw any
	callMatrix(m.t, m.ph, "pluginhost.list_plugins", nil, &raw)
	out := map[string]map[string]string{}
	root, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for _, v := range root {
		items, ok := v.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			pm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, rec := "", map[string]string{}
			for k, v := range pm {
				s, isStr := v.(string)
				if !isStr {
					continue
				}
				switch strings.ToLower(k) {
				case "id":
					id = s
				case "status":
					rec["status"] = s
				case "artifacthash", "hash":
					rec["artifactHash"] = s
				case "isolation":
					rec["isolation"] = s
				case "version":
					rec["version"] = s
				}
			}
			if id != "" {
				out[id] = rec
			}
		}
	}
	return out
}

// appmanagerRecordDump reads the appmanager actor's persisted record for a
// debug dump of fields the wire status doesn't expose (ActorID, ArtifactPath).
func appmanagerRecordDump(t *testing.T, dataDir, appID string) string {
	t.Helper()
	var reg struct {
		Services map[string]string `json:"services"`
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "registry.json"))
	if err != nil {
		return fmt.Sprintf("registry read: %v", err)
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return fmt.Sprintf("registry decode: %v", err)
	}
	actorID, ok := reg.Services["appmanager"]
	if !ok || actorID == "" {
		return fmt.Sprintf("no appmanager actor id: %+v", reg.Services)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, ".actors", "appmanager", actorID+".json"))
	if err != nil {
		return fmt.Sprintf("state read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Sprintf("state decode: %v", err)
	}
	records, _ := root["records"].(map[string]any)
	rec, ok := records[appID].(map[string]any)
	if !ok {
		return "record missing"
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			for rk, rv := range rec {
				if strings.EqualFold(rk, k) {
					if s, isStr := rv.(string); isStr {
						return s
					}
				}
			}
		}
		return ""
	}
	return fmt.Sprintf("state=%s actorId=%s artifactPath=%s artifactHash=%s error=%s",
		get("state"), get("actorId"), get("artifactPath"), get("artifactHash"), get("error"))
}

// appmanagerRecordPaths decodes the persisted appmanager record for an app
// into its inventory pointers (packagePath/artifactPath/artifactHash) plus
// current state. The matrix tests need these exact on-disk paths to simulate
// an external wipe of the artifacts store while the packages store survives.
func appmanagerRecordPaths(t *testing.T, dataDir, appID string) (packagePath, artifactPath, artifactHash, state string) {
	t.Helper()
	var reg struct {
		Services map[string]string `json:"services"`
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "registry.json"))
	if err != nil {
		t.Fatalf("registry read: %v", err)
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("registry decode: %v", err)
	}
	actorID, ok := reg.Services["appmanager"]
	if !ok || actorID == "" {
		t.Fatalf("no appmanager actor id: %+v", reg.Services)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, ".actors", "appmanager", actorID+".json"))
	if err != nil {
		t.Fatalf("state read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("state decode: %v", err)
	}
	records, _ := root["records"].(map[string]any)
	rec, ok := records[appID].(map[string]any)
	if !ok {
		t.Fatalf("app %q record missing from appmanager state", appID)
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			for rk, rv := range rec {
				if strings.EqualFold(rk, k) {
					if s, isStr := rv.(string); isStr {
						return s
					}
				}
			}
		}
		return ""
	}
	return get("packagePath"), get("artifactPath"), get("artifactHash"), get("state")
}

// persistedArtifactLoad reads the pluginhost actor's persisted ArtifactLoads
// — the exact data its OnStart restore consumes — as the physical ground
// truth of which artifact will be activated on the next host restart.
// The pluginhost state lives at <dataDir>/.actors/pluginhost/<actorID>.json
// with the actorID stable across restarts via registry.json.
func persistedArtifactLoad(t *testing.T, dataDir, pluginID string) (path, hash string, ok bool) {
	t.Helper()
	var reg struct {
		Services map[string]string `json:"services"`
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "registry.json"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	actorID, ok := reg.Services["pluginhost"]
	if !ok || actorID == "" {
		t.Fatalf("registry has no pluginhost actor id: %+v", reg.Services)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, ".actors", "pluginhost", actorID+".json"))
	if err != nil {
		t.Fatalf("read pluginhost state: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode pluginhost state: %v", err)
	}
	loads, ok := root["artifactLoads"].(map[string]any)
	if !ok {
		return "", "", false
	}
	entry, ok := loads[pluginID].(map[string]any)
	if !ok {
		return "", "", false
	}
	get := func(key string) string {
		for k, v := range entry {
			if strings.EqualFold(k, key) {
				if s, isStr := v.(string); isStr {
					return s
				}
			}
		}
		return ""
	}
	return get("ArtifactPath"), get("ArtifactHash"), true
}

// --- 1. subprocess: every operation is immediate, no restart ---

// TestMatrixSubprocessAllOpsNoRestart drives the full operation chain over
// the subprocess transport through the real appmanager↔pluginhost wire and
// asserts no operation ever requires a restart (never restart_pending):
// register → unload (real stop) → load (immediate reload) → reload
// (prepare+commit hot swap) → unregister (clean removal).
func TestMatrixSubprocessAllOpsNoRestart(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	m := bootMatrixHost(t, config.DataDir(), false, &matrixStubOpener{})

	const id = "subproc.demo"
	abi := subprocMatrixAbi()
	v2Path, v2Hash := matrixArtifact(t, t.TempDir(), "v2.exe", "fake-subprocess-v2")

	// 1. register → running immediately.
	status, err := m.install(id, "1.0.0", abi, "fake-subprocess-v1")
	if err != nil {
		t.Fatalf("subprocess register: %v", err)
	}
	if status.State != "running" {
		t.Fatalf("register state = %q, want running (subprocess immediate)", status.State)
	}
	if got, err := m.invokePlugin(id, "ping"); err != nil || string(got) != `{"pong":"ok"}` {
		t.Fatalf("invoke after register: %q, %v", got, err)
	}

	// 2. unload → stopped immediately (process killed for real).
	unloadResp, err := m.unload(id)
	if err != nil {
		t.Fatalf("subprocess unload: %v", err)
	}
	if unloadResp.Status.State != "stopped" {
		t.Fatalf("unload state = %q, want stopped", unloadResp.Status.State)
	}

	// 3. load → running immediately (respawn).
	loadResp, err := m.load(id)
	if err != nil {
		t.Fatalf("subprocess load: %v", err)
	}
	if loadResp.Status.State != "running" {
		t.Fatalf("load state = %q, want running", loadResp.Status.State)
	}

	// 4. reload → prepare+commit hot swap; never restart_pending.
	repoResp, err := m.reload(id, matrixManifest(id, "2.0.0"), abi, v2Path, v2Hash)
	if err != nil {
		t.Fatalf("subprocess reload: %v", err)
	}
	if repoResp.Status.State == mRestartPending {
		t.Fatal("subprocess reload must never stage restart_pending (hot swap)")
	}
	// The swap is real on the pluginhost: the persisted ArtifactLoads (the
	// data restore consumes) now points at the committed candidate hash.
	if _, persistedHash, ok := persistedArtifactLoad(t, config.DataDir(), id); !ok || persistedHash != v2Hash {
		t.Fatalf("pluginhost committed artifact hash = %q (ok=%v), want %q", persistedHash, ok, v2Hash)
	}

	// 5. unregister → full removal.
	if err := m.unregister(id); err != nil {
		t.Fatalf("subprocess unregister: %v", err)
	}
	if _, err := m.get(id); err == nil {
		t.Fatal("app still present after unregister")
	}
	if len(m.plugins()) != 0 {
		t.Fatalf("pluginhost still holds plugins after unregister: %+v", m.plugins())
	}
}

// --- 2. in-process: three staging paths → restart activation ---

// TestMatrixInProcessStagingThenRestartActivation covers the three
// in-process staging paths over the real wire — re-register a different
// artifact (A1), reload (A3), load while unload_pending (A2) — each ending
// in a successful restart_pending with the new artifact persisted and the
// old mapping kept alive. A simulated host restart (fresh runtime on the
// same data dir, pluginhost started first so the spawn-loop verification is
// reachable) activates all three to running; an app whose artifact file was
// deleted is honestly marked failed instead of falsely running (B1).
func TestMatrixInProcessStagingThenRestartActivation(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	const (
		reregID   = "inproc.rereg"
		reloadID  = "inproc.reloadapp"
		reload2ID = "inproc.reloadapp2"
		ghostID   = "inproc.ghost"
	)
	abi := inprocMatrixAbi()

	// Host #1: production order, all operations staged.
	m1 := bootMatrixHost(t, dataDir, false, &matrixStubOpener{})

	// A1: fresh register runs immediately, then re-registering a different
	// artifact defers (+ success) with the old mapping kept alive.
	regStatus, err := m1.install(reregID, "1.0.0", abi, "rereg-v1")
	if err != nil || regStatus.State != "running" {
		t.Fatalf("rereg fresh register: state=%q err=%v", regStatus.State, err)
	}
	reStatus, err := m1.install(reregID, "2.0.0", abi, "rereg-v2")
	if err != nil {
		t.Fatalf("rereg different-artifact register must succeed (deferred): %v", err)
	}
	if reStatus.State != mRestartPending {
		t.Fatalf("rereg second register state = %q, want restart_pending", reStatus.State)
	}
	// Old mapping still alive and routeable.
	if got, err := m1.invokePlugin(reregID, "ping"); err != nil || string(got) != `{"pong":"ok"}` {
		t.Fatalf("rereg old mapping should keep serving: %q, %v", got, err)
	}
	if p, ok := m1.plugins()[reregID]; !ok || p["status"] != "active" {
		t.Fatalf("rereg pluginhost mapping must stay active pre-restart: %+v", m1.plugins()[reregID])
	}

	// A3: reload on an in-process app defers to restart_pending (no commit).
	rlPath, rlHash := matrixArtifact(t, dataDir, "reloadapp-candidate.so", "reloadapp-v2")
	if st, err := m1.install(reloadID, "1.0.0", abi, "reloadapp-v1"); err != nil || st.State != "running" {
		t.Fatalf("reloadapp register: state=%q err=%v", st.State, err)
	}
	rlResp, err := m1.reload(reloadID, matrixManifest(reloadID, "2.0.0"), abi, rlPath, rlHash)
	if err != nil {
		t.Fatalf("reloadapp reload must succeed (deferred): %v", err)
	}
	if rlResp.Status.State != mRestartPending {
		t.Fatalf("reloadapp reload state = %q, want restart_pending", rlResp.Status.State)
	}
	// Old mapping still active (no swap happened on the loader).
	if p, ok := m1.plugins()[reloadID]; !ok || p["status"] != "active" {
		t.Fatalf("reloadapp pluginhost mapping must stay active pre-restart: %+v", m1.plugins()[reloadID])
	}

	// A2: unload → unload_pending (in-process), then load → restart_pending
	// (honest, no DLL revival).
	if st, err := m1.install(reload2ID, "1.0.0", abi, "reloadapp2-v1"); err != nil || st.State != "running" {
		t.Fatalf("reloadapp2 register: state=%q err=%v", st.State, err)
	}
	unResp, err := m1.unload(reload2ID)
	if err != nil {
		t.Fatalf("reloadapp2 unload: %v", err)
	}
	if unResp.Status.State != "unload_pending" {
		t.Fatalf("reloadapp2 unload state = %q, want unload_pending (in-process)", unResp.Status.State)
	}
	loResp, err := m1.load(reload2ID)
	if err != nil {
		t.Fatalf("reloadapp2 load must succeed (staged): %v", err)
	}
	if loResp.Status.State != mRestartPending {
		t.Fatalf("reloadapp2 load state = %q, want restart_pending (no revival)", loResp.Status.State)
	}

	// All three staged apps report restart_pending with the new artifact.
	for _, id := range []string{reregID, reloadID, reload2ID} {
		rec, err := m1.get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if rec.Status.State != mRestartPending {
			t.Fatalf("%s state = %q, want restart_pending", id, rec.Status.State)
		}
	}

	// Every staging path persisted the NEW artifact on the pluginhost (the
	// exact ArtifactLoads the restart restore consumes), and the unloaded
	// app's entry was dropped together with the unload.
	if _, h, ok := persistedArtifactLoad(t, dataDir, reregID); !ok || h != hashOf("rereg-v2") {
		t.Fatalf("rereg persisted ArtifactLoads = %q (ok=%v), want v2 hash %s", h, ok, hashOf("rereg-v2"))
	}
	if _, h, ok := persistedArtifactLoad(t, dataDir, reloadID); !ok || h != rlHash {
		t.Fatalf("reloadapp persisted ArtifactLoads = %q (ok=%v), want candidate hash %s", h, ok, rlHash)
	}
	if _, _, ok := persistedArtifactLoad(t, dataDir, reload2ID); ok {
		t.Fatal("reloadapp2 must have NO persisted ArtifactLoads after unload (unload_pending staging is appmanager-local)")
	}

	// Ghost app (B1 no-虚标): register with an artifact path we control (via
	// the register surface's inner contract: pre-load + appmanager.register),
	// then delete the artifact before the restart so the pluginhost restore
	// and the spawn-loop verification both fail.
	ghostPath, ghostHash := matrixArtifact(t, dataDir, "ghost.so", "ghost-v1")
	if err := m1.registerArtifact(ghostID, matrixManifest(ghostID, "1.0.0"), abi, ghostPath, ghostHash); err != nil {
		t.Fatalf("ghost register: %v", err)
	}
	if rec, err := m1.get(ghostID); err != nil || rec.Status.State != "running" {
		t.Fatalf("ghost state = %q err=%v, want running", rec.Status.State, err)
	}

	// Host restart: stop host 1 and delete the ghost artifact file.
	m1.stop()
	if err := os.Remove(ghostPath); err != nil {
		t.Fatalf("delete ghost artifact: %v", err)
	}

	// Host #2: pluginhost first so the appmanager spawn loop can
	// cross-validate the restored artifacts over the wire.
	m2 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})

	// After restart the three staged apps must be genuinely running (the
	// pluginhost restored their persisted artifacts and the spawn-loop
	// idempotent artifact_load confirmed them — activation path).
	for _, id := range []string{reregID, reloadID, reload2ID} {
		rec, err := m2.get(id)
		if err != nil {
			t.Fatalf("get %s after restart: %v", id, err)
		}
		if rec.Status.State != "running" {
			t.Fatalf("%s after restart = %q (version=%s error=%q), want running (restart activation); pluginhost view=%+v persisted=%s",
				id, rec.Status.State, rec.Status.Version, rec.Status.Error, m2.plugins()[id], appmanagerRecordDump(t, dataDir, id))
		}
		if got, err := m2.invokePlugin(id, "ping"); err != nil || string(got) != `{"pong":"ok"}` {
			t.Fatalf("invoke %s after restart: %q, %v", id, got, err)
		}
	}
	// The pluginhost physically holds the restored artifacts (active).
	plugins := m2.plugins()
	for _, id := range []string{reregID, reloadID} {
		if p, ok := plugins[id]; !ok || p["status"] != "active" {
			t.Fatalf("pluginhost %s = %+v, want active after restart", id, plugins[id])
		}
	}

	// B1: the ghost app is NOT running — the artifact is gone, so the
	// spawn-loop verification failed and the record is honestly failed.
	ghostRec, err := m2.get(ghostID)
	if err != nil {
		t.Fatalf("get %s after restart: %v", ghostID, err)
	}
	if ghostRec.Status.State != "failed" {
		t.Fatalf("ghost after restart = %q, want failed (no false running)", ghostRec.Status.State)
	}
}

// --- 3. production start order: defer, never 误杀 ---

// TestMatrixInProcessRestartHonestyProductionOrder pins the default
// production start order on restart: the pluginhost starts AFTER the
// appmanager, so at appmanager OnStart the spawn loop cannot reach it. The
// loop must defer (keep the persisted state) rather than fail the record —
// no 误杀 — and an explicit plugin_load still activates the staged artifact
// once the pluginhost is up.
func TestMatrixInProcessRestartHonestyProductionOrder(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	const id = "inproc.honest"
	abi := inprocMatrixAbi()

	m1 := bootMatrixHost(t, dataDir, false, &matrixStubOpener{})
	st, err := m1.install(id, "1.0.0", abi, "honest-v1")
	if err != nil || st.State != "running" {
		t.Fatalf("register: state=%q err=%v", st.State, err)
	}
	// Stage a reload so the record is restart_pending before the restart.
	rlPath, rlHash := matrixArtifact(t, dataDir, "honest-candidate.so", "honest-v2")
	rlResp, err := m1.reload(id, matrixManifest(id, "2.0.0"), abi, rlPath, rlHash)
	if err != nil || rlResp.Status.State != mRestartPending {
		t.Fatalf("reload: state=%q err=%v", rlResp.Status.State, err)
	}
	m1.stop()

	// Production order restart: pluginhost after appmanager.
	m2 := bootMatrixHost(t, dataDir, false, &matrixStubOpener{})
	rec, err := m2.get(id)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	// The record must NOT be failed (no 误杀): the spawn loop either
	// verified the artifact (pluginhost reachable) or deferred keeping the
	// persisted state.
	if rec.Status.State == "failed" {
		t.Fatalf("record failed after production-order restart; spawn loop must defer, not 误杀: %+v", rec.Status)
	}
	// Once the pluginhost is up, an explicit plugin_load activates the
	// staged artifact (idempotent same-artifact branch → running).
	loResp, err := m2.load(id)
	if err != nil {
		t.Fatalf("activation load after restart: %v", err)
	}
	if loResp.Status.State != "running" {
		t.Fatalf("activation load state = %q, want running", loResp.Status.State)
	}
	if got, err := m2.invokePlugin(id, "ping"); err != nil || string(got) != `{"pong":"ok"}` {
		t.Fatalf("invoke after activation: %q, %v", got, err)
	}
}

// TestArtifactLossRestartReregisterPreservesAppState is the BP13 regression:
// deleting the build artifact and restarting the host must not wipe the
// pluginhost appstate for that app; re-registering a rebuilt artifact must
// keep the previously stored value.
func TestArtifactLossRestartReregisterPreservesAppState(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	const appID = "inproc.stateghost"
	abi := inprocMatrixAbi()

	m1 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})

	ghostPath, ghostHash := matrixArtifact(t, dataDir, "stateghost.so", "state-v1")
	if err := m1.registerArtifact(appID, matrixManifest(appID, "1.0.0"), abi, ghostPath, ghostHash); err != nil {
		t.Fatalf("register artifact: %v", err)
	}
	if err := m1.stateSet(appID, "counter", []byte("7")); err != nil {
		t.Fatalf("state.set: %v", err)
	}
	gotBefore, err := m1.stateGet(appID, "counter")
	if err != nil || !gotBefore.Found || string(gotBefore.Value) != "7" {
		t.Fatalf("state.get before restart = found=%v value=%q err=%v", gotBefore.Found, gotBefore.Value, err)
	}

	// Simulate "delete .sporecode/build": remove the artifact file and restart.
	m1.stop()
	if err := os.Remove(ghostPath); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	// Restart with pluginhost first so the spawn loop cross-validates the
	// missing artifact and honestly marks the record failed.
	m2 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})

	rec, err := m2.get(appID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if rec.Status.State != "failed" {
		t.Fatalf("record after restart = %q, want failed", rec.Status.State)
	}
	// BP13: the appstate must survive the artifact loss and the failed record.
	during, err := m2.stateGet(appID, "counter")
	if err != nil || !during.Found || string(during.Value) != "7" {
		t.Fatalf("state.get after restart (before re-register) = found=%v value=%q err=%v", during.Found, during.Value, err)
	}

	// Re-register with a rebuilt artifact at a new path (same app ID).
	newPath, newHash := matrixArtifact(t, dataDir, "stateghost-v2.so", "state-v2")
	if err := m2.registerArtifact(appID, matrixManifest(appID, "1.1.0"), abi, newPath, newHash); err != nil {
		t.Fatalf("re-register artifact: %v", err)
	}
	rec, err = m2.get(appID)
	if err != nil {
		t.Fatalf("get after re-register: %v", err)
	}
	if rec.Status.State != "running" {
		t.Fatalf("record after re-register = %q, want running", rec.Status.State)
	}

	// BP13: the stored counter must still be present after re-register.
	after, err := m2.stateGet(appID, "counter")
	if err != nil || !after.Found || string(after.Value) != "7" {
		t.Fatalf("state.get after re-register = found=%v value=%q err=%v", after.Found, after.Value, err)
	}
}

// TestMatrixRestartHealsWipedArtifactFromPackageZip is the restart self-heal
// e2e (重启自愈): an install_local native app whose artifact FILE is deleted
// out-of-band while its package zip stays in the inventory must come back
// running on a fresh runtime, with the artifact re-extracted from the zip.
// Both host start orders converge on running:
//
//   - Production order (appmanager starts before pluginhost): the appmanager
//     OnStart heal rewrites the file before pluginhost's own OnStart restore
//     runs, so the pluginhost restore itself succeeds.
//   - pluginhost-first order (e2e cross-validation): pluginhost's OnStart
//     restore fails first (error descriptor, record kept — 10-T6), then the
//     appmanager heal rewrites the file and the spawn-loop's idempotent
//     artifact_load reconciles the record back to running.
func TestMatrixRestartHealsWipedArtifactFromPackageZip(t *testing.T) {
	for _, pluginhostFirst := range []bool{false, true} {
		name := "appmanager-first"
		if pluginhostFirst {
			name = "pluginhost-first"
		}
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			config.SetDataDirForTest(dataDir)
			t.Cleanup(config.ResetForTest)

			const id = "inproc.heal"
			abi := inprocMatrixAbi()

			m1 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})
			st, err := m1.install(id, "1.0.0", abi, "heal-v1")
			if err != nil || st.State != "running" {
				t.Fatalf("install: state=%q err=%v", st.State, err)
			}
			pkgPath, artPath, artHash, _ := appmanagerRecordPaths(t, dataDir, id)
			if pkgPath == "" || artPath == "" || artHash == "" {
				t.Fatalf("record inventory pointers missing: pkg=%q art=%q hash=%q", pkgPath, artPath, artHash)
			}
			if _, err := os.Stat(pkgPath); err != nil {
				t.Fatalf("package zip %q missing after install: %v", pkgPath, err)
			}
			m1.stop()

			// External wipe: only the artifacts store disappears; the packages
			// zip must survive (that is the self-heal precondition).
			if err := os.Remove(artPath); err != nil {
				t.Fatalf("delete artifact %q: %v", artPath, err)
			}
			if _, err := os.Stat(pkgPath); err != nil {
				t.Fatalf("package zip %q vanished unexpectedly: %v", pkgPath, err)
			}

			// Fresh runtime (host restart) in the order under test.
			m2 := bootMatrixHost(t, dataDir, pluginhostFirst, &matrixStubOpener{})
			rec, err := m2.get(id)
			if err != nil {
				t.Fatalf("get after restart: %v", err)
			}
			if rec.Status.State != "running" {
				t.Fatalf("app after restart = %q (error=%q), want running (self-healed from package zip); record=%s",
					rec.Status.State, rec.Status.Error, appmanagerRecordDump(t, dataDir, id))
			}
			// The artifact file was re-extracted from the package zip at the
			// recorded path, with the recorded content.
			data, err := os.ReadFile(artPath)
			if err != nil {
				t.Fatalf("healed artifact %q unreadable: %v", artPath, err)
			}
			if string(data) != "heal-v1" {
				t.Fatalf("healed artifact content = %q, want %q", data, "heal-v1")
			}
			if got := hashOf(string(data)); !strings.EqualFold(got, artHash) {
				t.Fatalf("healed artifact hash = %s, want recorded %s", got, artHash)
			}
			// The pluginhost physically holds the restored artifact and routes
			// calls.
			if p, ok := m2.plugins()[id]; !ok || p["status"] != "active" {
				t.Fatalf("pluginhost %s = %+v, want active after self-heal", id, m2.plugins()[id])
			}
			if got, err := m2.invokePlugin(id, "ping"); err != nil || string(got) != `{"pong":"ok"}` {
				t.Fatalf("invoke after self-heal: %q, %v", got, err)
			}
		})
	}
}

// TestMatrixRestartBothArtifactAndZipWipedKeepsRecordFailed is the negative
// case of the restart self-heal: when the package zip is gone together with
// the artifact (whole inventory wiped), the heal is impossible and the record
// must keep the existing failure semantics — preserved record, honest failed
// state, no panic. The appmanager heal pass logs the zip-missing condition
// explicitly instead of pretending the app can come back.
func TestMatrixRestartBothArtifactAndZipWipedKeepsRecordFailed(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	const id = "inproc.heal.wipeall"
	abi := inprocMatrixAbi()

	m1 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})
	st, err := m1.install(id, "1.0.0", abi, "wipeall-v1")
	if err != nil || st.State != "running" {
		t.Fatalf("install: state=%q err=%v", st.State, err)
	}
	pkgPath, artPath, _, _ := appmanagerRecordPaths(t, dataDir, id)
	if pkgPath == "" || artPath == "" {
		t.Fatalf("record inventory pointers missing: pkg=%q art=%q", pkgPath, artPath)
	}
	m1.stop()

	// Wipe the whole inventory: artifact file AND the package zip.
	if err := os.Remove(artPath); err != nil {
		t.Fatalf("delete artifact %q: %v", artPath, err)
	}
	if err := os.Remove(pkgPath); err != nil {
		t.Fatalf("delete package zip %q: %v", pkgPath, err)
	}

	m2 := bootMatrixHost(t, dataDir, true, &matrixStubOpener{})
	rec, err := m2.get(id)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if rec.Status.State != "failed" {
		t.Fatalf("app after restart = %q, want failed (zip also gone → no self-heal); record=%s",
			rec.Status.State, appmanagerRecordDump(t, dataDir, id))
	}
	// The record survives the failed restore (no deletion, no panic).
	if _, _, _, state := appmanagerRecordPaths(t, dataDir, id); state != "failed" {
		t.Fatalf("persisted record state = %q, want failed (record kept)", state)
	}
	// And the pluginhost has no live mapping for it (honest: nothing loaded).
	if p, ok := m2.plugins()[id]; ok && p["status"] == "active" {
		t.Fatalf("pluginhost %s unexpectedly active after total wipe: %+v", id, p)
	}
}

// --- T2: process-state upload path (pluginhost → appmanager) ---

// TestMatrixCrashReportUploadsToAppManager is the T2 e2e: a subprocess
// plugin's abnormal exit, reported through the pluginhost's explicit
// process-state seam, crosses the real wire into the appmanager record as
// 'crashed' — visible via get/list — including the crash cause. The oracle
// actor is absent from this runtime, which also pins the crash-problem
// tolerance: the missing lookup drops the diagnostic, never the state write.
func TestMatrixCrashReportUploadsToAppManager(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	m := bootMatrixHost(t, config.DataDir(), false, &matrixStubOpener{})

	const id = "subproc.crashdemo"
	status, err := m.install(id, "1.0.0", subprocMatrixAbi(), "fake-subprocess-v1")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if status.State != "running" {
		t.Fatalf("register state = %q, want running", status.State)
	}

	// Simulate the transport's abnormal-exit transition (session EOF with a
	// cause), exactly what processOpener.recordExit reports on a real crash.
	m.phActor.ReportProcessState(id, "crashed", "session EOF: stdout closed", "", 0)

	// The report is a fire-and-forget Tell; poll the record until it lands.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := m.get(id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if resp.Status.State == "crashed" {
			if !strings.Contains(resp.Status.Error, "session EOF") {
				t.Fatalf("crashed record error = %q, want the crash cause", resp.Status.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record state = %q after crash report, want crashed", resp.Status.State)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// list projection carries the same crashed verdict.
	var listResp gen.AppManagerListResp
	callMatrix(t, m.am, "appmanager.list", gen.AppManagerListReq{}, &listResp)
	found := false
	for _, item := range listResp.Items {
		if item.ID == id {
			found = true
			if item.State != "crashed" {
				t.Fatalf("list state = %q, want crashed", item.State)
			}
		}
	}
	if !found {
		t.Fatalf("app %q missing from list", id)
	}

	// An explicit plugin_load revives the crashed record back to running
	// through the normal load path.
	if _, err := m.load(id); err != nil {
		t.Fatalf("load after crash: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		resp, err := m.get(id)
		if err != nil {
			t.Fatalf("get after load: %v", err)
		}
		if resp.Status.State == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record state = %q after load, want running", resp.Status.State)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
