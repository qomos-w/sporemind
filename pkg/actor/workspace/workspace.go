package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/spore/identity"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/codegen"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
	appruntime "github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/util"
	"io"
	"io/fs"
	mrand "math/rand"
	"os"
	"runtime"

	"path/filepath"
	"reflect"

	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
)

// derefSlotWS dereferences a ModelSlot pointer for the value-taking agent
// constructor; a nil slot resolves to an empty ModelSlot (= [auto]).
func derefSlotWS(s *domain.ModelSlot) domain.ModelSlot {
	if s == nil {
		return domain.ModelSlot{}
	}
	return *s
}

// modelSlotsEqual reports whether two ModelSlot pointers hold equal content.
// Both-nil is equal; exactly one nil is not. Compares the dereferenced values
// deeply so identical route chains do not trigger a spurious state flush.
func modelSlotsEqual(a, b *gen.ModelSlot) bool {
	if a == nil || b == nil {
		return a == b
	}
	return modelSlotValuesEqual(*a, *b)
}

// modelSlotValuesEqual deeply compares two ModelSlot values, following the Unit
// pointer inside each candidate. Used to guard route-chain persistence against
// unchanged-slot writes.
func modelSlotValuesEqual(a, b gen.ModelSlot) bool {
	if len(a.Candidates) != len(b.Candidates) {
		return false
	}
	for i := range a.Candidates {
		ca, cb := a.Candidates[i], b.Candidates[i]
		if ca.Kind != cb.Kind || ca.AggregatorID != cb.AggregatorID {
			return false
		}
		if (ca.Unit == nil) != (cb.Unit == nil) {
			return false
		}
		if ca.Unit != nil && *ca.Unit != *cb.Unit {
			return false
		}
	}
	return true
}

// cloneModelSlotPtr deep-copies a ModelSlot pointer so the workspace's
// persisted AgentRef never shares mutable slice/pointer storage with a
// transient request. Returns nil for nil.
func cloneModelSlotPtr(s *gen.ModelSlot) *gen.ModelSlot {
	if s == nil {
		return nil
	}
	out := &gen.ModelSlot{Candidates: make([]gen.ModelRef, len(s.Candidates))}
	for i, c := range s.Candidates {
		out.Candidates[i] = c
		if c.Unit != nil {
			u := *c.Unit
			out.Candidates[i].Unit = &u
		}
	}
	return out
}

// pinnedUnitSlotFromUnitString parses a "Provider|Model" unit string into a
// hard-pinned ModelSlot ([unit] only, no aggregator/auto fallback). The string
// is the wire format declared by the plugin_agent model binding in the appdef.
// Empty or malformed input returns nil so callers fall through to the host
// default ([auto]). Provider/model existence is not validated here — the model
// resolver surfaces a dispatch error if the unit is unknown at request time.
func pinnedUnitSlotFromUnitString(s string) *gen.ModelSlot {
	provider, model, ok := strings.Cut(s, "|")
	if !ok || provider == "" || model == "" || strings.Contains(model, "|") {
		return nil
	}
	unit := gen.ModelUnit{Provider: provider, Model: model}
	return &gen.ModelSlot{Candidates: []gen.ModelRef{
		{Kind: "unit", Unit: &unit},
	}}
}

// returns a 4-char hex string used as a uniqueness suffix
// for agent path segments (e.g. "Coder#a1b2").
func randomHexSuffix() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Should never happen on supported platforms.
		return "0000"
	}
	return hex.EncodeToString(b[:])
}

// System meta project constants for plugin development.
const systemMetaProjectName = "workspace"
const systemMetaProjectDirName = "data"

// App directory names under the system meta project. The app/ directory holds
// compiled/builtin apps. dev-app/ and sporeapp/ are still valid AppKind tiers
// for mounted projects, but they are no longer auto-created at startup.
const (
	appDirName      = "app"
	devAppDirName   = "dev-app"
	sporeAppDirName = "sporeapp"
)

// appDirs returns the App-tier subdirectory under the system meta project that
// is auto-created at startup. Only app/ (installed/builtin plugins) is created
// automatically; dev-app/ and sporeapp/ are created lazily when an app is
// actually installed or scaffolded there.
func systemMetaProjectPath() string {
	return filepath.Join(config.DataDir(), systemMetaProjectDirName)
}

func appDirs() []string {
	sys := systemMetaProjectPath()
	return []string{
		filepath.Join(sys, appDirName),
	}
}

const nativeScaffoldPrefix = "scaffoldtemplates/native"

// scaffoldVars holds the template variables substituted into native scaffold
// files (.tmpl).
type scaffoldVars struct {
	Name  string
	AppID string
}

func scaffoldApp(dir, name, appKind string) ([]string, error) {
	if appKind != devAppDirName && appKind != sporeAppDirName {
		return nil, fmt.Errorf("unsupported app kind %q", appKind)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// handleCreate runs git init before scaffolding; a directory holding only
	// .git is still scaffoldable.
	for _, e := range entries {
		if e.Name() != ".git" {
			return nil, fmt.Errorf("app directory %q is not empty", dir)
		}
	}
	if appKind == devAppDirName {
		return scaffoldPlugin(dir, name)
	}
	return scaffoldSporeApp(dir, name)
}

// scaffoldPlugin writes the embedded native scaffold templates into dir,
// substituting {{.Name}} and {{.AppID}}, then runs codegen.Generate to produce
// all generated artifacts (main.gen.go, handlers.go, schemas_gen.go,
// app.manifest.json, client.gen.ts) from the scaffolded app.appdef.
//
// Files ending in .tmpl are rendered via text/template and written without
// the suffix. The vendor-sdk/ subtree is intentionally absent from the
// embedded scaffold: the real sporemind-plugin-sdk is vendored into the app
// by codegen.Generate (dev checkout or embedded release zip — the same
// single source of truth dev_generate uses), so scaffolded apps never drift
// from the SDK the host actually speaks (permissions, streaming API,
// state client).
func scaffoldPlugin(dir, name string) ([]string, error) {
	vars := scaffoldVars{Name: name, AppID: "app." + name}
	created := make([]string, 0, 10)
	err := fs.WalkDir(nativeScaffoldFS, nativeScaffoldPrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relPath := strings.TrimPrefix(path, nativeScaffoldPrefix+"/")
		if strings.HasPrefix(relPath, "vendor-sdk/") {
			// Superseded by the real-SDK vendoring below.
			return nil
		}
		data, readErr := nativeScaffoldFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var content []byte
		outRel := relPath
		if strings.HasSuffix(relPath, ".tmpl") {
			tmpl, parseErr := template.New(relPath).Parse(string(data))
			if parseErr != nil {
				return fmt.Errorf("scaffold: parse template %s: %w", relPath, parseErr)
			}
			var buf bytes.Buffer
			if execErr := tmpl.Execute(&buf, vars); execErr != nil {
				return fmt.Errorf("scaffold: execute template %s: %w", relPath, execErr)
			}
			content = buf.Bytes()
			outRel = strings.TrimSuffix(relPath, ".tmpl")
		} else {
			content = data
		}
		outPath := filepath.Join(dir, outRel)
		if mkErr := os.MkdirAll(filepath.Dir(outPath), 0o755); mkErr != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", filepath.Dir(outRel), mkErr)
		}
		if writeErr := os.WriteFile(outPath, content, 0o644); writeErr != nil {
			return fmt.Errorf("scaffold: write %s: %w", outRel, writeErr)
		}
		created = append(created, outRel)
		return nil
	})
	if err != nil {
		for _, f := range created {
			_ = os.Remove(filepath.Join(dir, f))
		}
		return nil, err
	}

	// Run codegen.Generate to produce main.gen.go, handlers.go,
	// schemas_gen.go, app.manifest.json, client.gen.ts from app.appdef.
	// Resolve the real SDK (dev checkout walk / embedded release zip) and
	// let Generate vendor it into ./vendor-sdk — the scaffold go.mod already
	// carries the `replace => ./vendor-sdk` directive, which ensureGoMod
	// preserves.
	sdkPath, err := codegen.EnsureDevSDKWorkspace(dir)
	if err != nil {
		return nil, fmt.Errorf("scaffold: resolve plugin SDK: %w", err)
	}
	genResult, err := codegen.Generate(dir, codegen.Options{SDKPath: sdkPath})
	if err != nil {
		for _, f := range created {
			_ = os.Remove(filepath.Join(dir, f))
		}
		return nil, fmt.Errorf("scaffold: codegen generate: %w", err)
	}
	created = append(created, genResult.Files...)

	return created, nil
}

// scaffoldSporeApp writes the spore app scaffold (main.spore + manifest).
func scaffoldSporeApp(dir, name string) ([]string, error) {
	manifest := gen.AppManifest{
		ID: "app." + name, Name: name, Version: "0.1.0", Runtime: "spore",
		ProtocolVersion: 1, Namespace: "app." + name,
		Permissions: []string{}, Schemas: []gen.AppSchemaRef{},
		Callables: []gen.AppCallableDescriptor{}, Events: []gen.AppEventDescriptor{},
		Projections: []gen.AppProjectionDescriptor{}, Entrypoints: []gen.AppEntrypoint{},
		Dependencies: []gen.AppDependency{},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files := []struct {
		name string
		data []byte
	}{
		{"app.manifest.json", append(manifestBytes, '\n')},
		{"main.spore", []byte("// App entry module\nexport fun ping(): string = \"pong\"\n")},
		{"README.md", []byte("# " + name + "\n\nAppKind: sporeapp\n")},
	}
	created := make([]string, 0, len(files))
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.data, 0o644); err != nil {
			for _, f := range created {
				_ = os.Remove(filepath.Join(dir, f))
			}
			return nil, fmt.Errorf("write %s: %w", file.name, err)
		}
		created = append(created, file.name)
	}
	return created, nil
}

// ensureAppDirs creates the App-tier directory (app/) under the system meta
// project if it does not already exist. Called after ensureSystemMetaProject.
// dev-app/ and sporeapp/ are intentionally NOT auto-created; they are created
// lazily when an app is actually installed or scaffolded there.
func ensureAppDirs(ctx actor.Context) {
	for _, dir := range appDirs() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			ctx.Logger().Error("workspace: create app dir failed",
				"path", dir, "error", err)
		}
	}
}

// isAppTierSubPath reports whether the given path is a subdirectory under
// one of the three app-tier directories (app/, dev-app/, sporeapp/) within
// the system meta project. This is used by handleMount to allow mounting app
// dev directories while blocking other system-level paths.
func isAppTierSubPath(normPath, sysPath string) bool {
	for _, dirName := range []string{appDirName, devAppDirName, sporeAppDirName} {
		tierPath := util.NormalizePath(filepath.Join(systemMetaProjectPath(), dirName))
		if util.IsSubPath(normPath, tierPath) {
			return true
		}
	}
	return false
}

// inferAppKind determines the AppKind from the mount path. If the path is
// under dev-app/ → "dev-app", under sporeapp/ → "sporeapp",
// under app/ → "app". Otherwise "" (regular project).
func inferAppKind(normPath, sysPath string) string {
	for _, dirName := range []string{appDirName, devAppDirName, sporeAppDirName} {
		tierPath := util.NormalizePath(filepath.Join(systemMetaProjectPath(), dirName))
		if util.IsSubPath(normPath, tierPath) {
			return dirName
		}
	}
	return ""
}

// extraBundlesForAppKind appends the plugin-dev bundle to the spawn's
// ExtraBundleIDs when the target project is a native dev-app project, so
// agents created there (and their sub-agents) automatically mount the
// appmanager dev callables instead of requiring a manual component.mount.
// Idempotent: an already-present entry is not duplicated.
func extraBundlesForAppKind(appKind string, extra []string) []string {
	if appKind != devAppDirName {
		return extra
	}
	for _, id := range extra {
		if id == agentkit.PluginDevBundleID {
			return extra
		}
	}
	out := make([]string, 0, len(extra)+1)
	out = append(out, extra...)
	return append(out, agentkit.PluginDevBundleID)
}

// ownerInheritedPluginDevBundle returns [agentkit.PluginDevBundleID]
// when the owner agent's mounted components include an enabled plugin-dev
// bundle mount, and nil otherwise. spawn_assign workers inherit the bundle
// only when the owner actually mounted it — the persisted mount state is the
// source of truth, not the project path. A disabled mount, a different
// bundle, or an empty mount list all yield nil so plain-project owners never
// leak the dev bundle into their workers. The caller merges the result
// through extraBundlesForAppKind, so an already-present entry is never
// duplicated.
func ownerInheritedPluginDevBundle(mounts []domain.AgentComponentMount) []string {
	for _, m := range mounts {
		if m.Enabled && m.Kind == "bundle" && m.CardID == agentkit.PluginDevBundleID {
			return []string{agentkit.PluginDevBundleID}
		}
	}
	return nil
}

// systemMetaProjectActorID returns the ActorID of the system meta project, or "".
func (a *Actor) systemMetaProjectActorID() string {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if m.System {
			return m.ActorID
		}
	}
	return ""
}

// agentSnapshot returns a shallow copy of a.Agents under agentsMu.RLock.
// Lifecycle-loop handlers use this for all read access so they never hold
// the lock during cross-actor invokes or long iterations.
func (a *Actor) agentSnapshot() []domain.AgentRef {
	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	out := make([]domain.AgentRef, len(a.Agents))
	copy(out, a.Agents)
	return out
}

// agentAt returns a copy of the agent at idx under agentsMu.RLock.
func (a *Actor) agentAt(idx int) domain.AgentRef {
	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	if idx >= 0 && idx < len(a.Agents) {
		return a.Agents[idx]
	}
	return domain.AgentRef{}
}

// findAgentRef resolves an agent by ActorID or stable ID. It consults the
// in-memory a.Agents cache first; on miss it falls back to the authoritative
// ragents registry card and heals the cache so subsequent reads and lifecycle
// mutations act on the authoritative row.
//
// The card is the authoritative source (agentRegistryName/saveAgentRegistry);
// a.Agents is a derived in-memory cache that can desync from the card when a
// persist write fails or a process restart reloads it mid-flight. Lifecycle
// callables (review/terminate) must not dead-end on a stale cache miss for an
// agent that is authoritatively present — that dead-end orphaned workers and
// left their worktrees un-releasable.
func (a *Actor) findAgentRef(actorOrID string) (domain.AgentRef, bool) {
	for _, ag := range a.agentSnapshot() {
		if ag.ActorID == actorOrID || ag.ID == actorOrID {
			return ag, true
		}
	}
	if a.store == nil {
		a.store = persist.MustNew(config.PersistConfig("workspace"))
	}
	var cardAgents []domain.AgentRef
	if err := a.store.Load(a.agentRegistryName(), &cardAgents); err != nil {
		return domain.AgentRef{}, false
	}
	for _, ag := range cardAgents {
		if ag.ActorID == actorOrID || ag.ID == actorOrID {
			a.healAgentRef(ag)
			return ag, true
		}
	}
	return domain.AgentRef{}, false
}

// healAgentRef re-appends an authoritatively-present agent to the in-memory
// cache when a cache miss revealed it was dropped (desync), then persists so
// the card and cache stay consistent. Idempotent: if the agent is already
// present (a concurrent heal won the race), it is a no-op.
func (a *Actor) healAgentRef(ref domain.AgentRef) {
	a.agentsMu.Lock()
	for i := range a.Agents {
		if a.Agents[i].ID == ref.ID || (ref.ActorID != "" && a.Agents[i].ActorID == ref.ActorID) {
			a.agentsMu.Unlock()
			return
		}
	}
	a.Agents = append(a.Agents, ref)
	a.agentsMu.Unlock()
	_ = a.saveAgentRegistry()
}

// mutateAgentAt applies fn to the agent at idx under agentsMu.Lock.
// Returns the updated copy. If idx is out of bounds, fn is not called and
// the zero AgentRef is returned.
func (a *Actor) mutateAgentAt(idx int, fn func(*domain.AgentRef)) domain.AgentRef {
	a.agentsMu.Lock()
	defer a.agentsMu.Unlock()
	if idx >= 0 && idx < len(a.Agents) {
		fn(&a.Agents[idx])
		return a.Agents[idx]
	}
	return domain.AgentRef{}
}

// agentKindConfigsSnapshot returns a shallow copy of AgentKindConfigs under
// kindConfigsMu.RLock. PureContext handlers use this for read access so they
// never race owner-loop writes (seed, save_agent_kind_config, shell-preference
// refresh). The copy is value-only at the top level; readers must not mutate
// the shared inner slices/maps.
func (a *Actor) agentKindConfigsSnapshot() []domain.AgentKindConfig {
	a.kindConfigsMu.RLock()
	defer a.kindConfigsMu.RUnlock()
	out := make([]domain.AgentKindConfig, len(a.AgentKindConfigs))
	copy(out, a.AgentKindConfigs)
	return out
}

// validateNameSegment rejects strings that cannot be used as a tree-path
// segment: empty, containing "/" or "#".
func validateNameSegment(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name is empty", kind)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%s name %q contains invalid character", kind, name)
	}
	if strings.Contains(name, "#") {
		return fmt.Errorf("%s name %q contains reserved '#' character", kind, name)
	}
	return nil
}

// Actor is the workspace container: manages mounted projects and agents.
type Actor struct {
	actor.Host
	store  persist.Persist
	Mounts []domain.ProjectRef `gospore:"component,public"`
	// Agents is the in-memory cache of the ground-truth agent registry card
	// (ragents persist record). Every mutation writes the card through
	// saveAgentRegistry (via Save); the card is the authoritative source.
	// The field stays a public component so the projection store mirrors it.
	Agents           []domain.AgentRef        `gospore:"component,public"`
	AgentKindConfigs []domain.AgentKindConfig `gospore:"component,public"`
	UI               domain.WorkspaceUIModel  `gospore:"component,public"`
	actorID          string                   // set in OnStart from Self().ID()

	// aistatsSnap caches the resolved global aistats system actor (ref +
	// canonical id string) for discovery by agents. atomic.Value because the
	// pure workspace.aistats_actor_id handler reads it off the owner loop.
	aistatsSnap atomic.Value // aistatsState

	// NoSystemProject disables automatic mounting of the system plugin
	// development project. Used by the manifest generator to avoid
	// duplicate projections in the exported manifest.
	NoSystemProject bool

	// clones tracks asynchronous agent clone progress. Keyed by cloned
	// agent actor ID. Persisted manually by Save/Load.
	clones map[string]domain.CloneState

	// inactiveActorIDs is retained only while reading legacy snapshots. Stable
	// identity now lives exclusively in AgentRef.ActorID.
	inactiveActorIDs map[string]string

	// In-memory state (not exposed as component)
	accountPrefs   domain.AccountPreferencesSnapshot
	mu             sync.RWMutex
	clonePersistMu sync.Mutex
	// kindConfigsMu protects AgentKindConfigs. Owner-loop writers (seed in
	// OnStart, save_agent_kind_config, shell preference refresh) take the
	// write lock; PureContext readers (list/get_agent_kind_configs,
	// list_agent_kinds) take the read lock. Never held across cross-actor
	// invokes or persistence writes that take other locks.
	kindConfigsMu sync.RWMutex
	// mountMu protects Mounts. Mutations (mount/unmount/create/update_project/
	// add_mount/remove_mount and the OnStart system-project ensure path) hold
	// the write lock; pre-scan and pure readers hold the read lock. Never held
	// during cross-actor invokes — snapshot first (mountsSnapshot or an inline
	// copy) and invoke on the copy.
	mountMu sync.RWMutex
	// agentsMu protects a.Agents and a.agentRuntime across all handler
	// goroutines (ownerLoop bootstrap plus the stateless PureContext
	// handlers). Lock for mutations, RLock for reads. NEVER hold
	// this lock during cross-actor invokes — only during in-memory field access.
	agentsMu sync.RWMutex
	// agentListVersion is incremented on the ownerLoop only. agentListSnapshot
	// holds the latest built projection for lock-free reads by PureContext
	// handlers (handleAgentListState runs on a concurrent goroutine). It is a
	// non-authoritative lazy cache: rebuilt from the registry card's cache
	// (a.Agents) + agentRuntime on every mutation, never persisted.
	agentListVersion  atomic.Int32
	agentListSnapshot atomic.Value // gen.WorkspaceAgentListState
	// systemTreeCache caches the last system tree response for the PureContext
	// handleSystemTree (atomic.Value; last store wins).
	systemTreeCache atomic.Value // systemTreeCacheEntry
	// agentRuntime is ephemeral runtime state pushed by live agent actors via
	// workspace.agent_status_update. Never persisted: the list projection
	// prefers it for loaded agents and falls back to the registry card's
	// last-known Status for terminated/unloaded agents.
	agentRuntime map[string]gen.AgentRuntimeState
	// stateFlushPending coalesces high-frequency status-update persists into
	// one delayed flush (workspace.internal_flush_state) instead of a full
	// Save per push. OwnerLoop-only; atomic so a PureContext state_flush
	// conversion stays race-free.
	stateFlushPending atomic.Bool
	// registryCardLoaded gates saveAgentRegistry. It is set only after Load()
	// successfully read the ragents card (an absent card is authoritative
	// empty too). A card that exists but failed to decode must never be
	// overwritten by the derived in-memory cache — on a load failure the
	// cache starts empty, and the first registry write would silently wipe
	// every registered agent (the restart-wipes-registry failure).
	registryCardLoaded atomic.Bool

	// deletionErrors and deletionAttempts track per-agent retry state for
	// cascade deletion nodes whose teardown failed. Persisted in Save/Load so
	// the error is observable across restarts.
	deletionErrors   map[string]string
	deletionAttempts map[string]int
	// All deletion* in-memory maps above are protected by deletionMu
	// (deletion finalize stays on the dedicated "deletion" loop; retry and
	// sweep are stateless PureContext handlers) and can interleave with
	// owner-loop Save/persist reads and lifecycle
	// teardown claims). Never hold deletionMu across cross-actor invokes or
	// persist I/O - snapshot under the lock, then act on the copy.
	deletionMu sync.RWMutex

	// subMapInstances tracks active sub_map executor activations. Protected by
	// subMapMu. Persisted in Save/Load so sub-map completion propagation survives
	// workspace restarts.
	subMapInstances []subMapInstance
	subMapMu        sync.RWMutex

	// eventWaits tracks active event_wait executor activations, keyed by
	// projectID + "/" + taskCardID. Protected by eventWaitMu. Persisted in
	// Save/Load (only active waits) and re-armed in OnStart so a workspace
	// restart does not strand a doing card; pointer identity of the map value
	// doubles as the generation guard against duplicate activations.
	eventWaits  map[string]*eventWaitState
	eventWaitMu sync.RWMutex
	// eventWaitSubscribeFn overrides the production event subscription
	// (streaming gospore.events.subscribe_service invoke on the App root)
	// with a fake event source in tests. nil = production path. The seam
	// deliberately takes no context so no per-invocation state is captured
	// by the wait goroutine.
	eventWaitSubscribeFn func(service, kind string) eventWaitStream

	// scatterFanouts tracks active scatter executor fan-outs (join state
	// for <scatter-card-id>-item-<idx> children). Protected by scatterMu.
	// Persisted in Save/Load so the join sweep survives workspace
	// restarts; the cards remain the ground truth — the sweep reconciles
	// this projection onto them.
	scatterFanouts []scatterFanout
	scatterMu      sync.RWMutex
	// scatterSweepScheduled prevents duplicate arming of the self-
	// rescheduling scatter join sweep. Guarded by scatterMu.
	scatterSweepScheduled bool
	// deletionInFlight tracks agent IDs whose async teardown goroutine is
	// currently running. Prevents the sweep, retry, or a second cascade from
	// starting a concurrent teardown for the same agent. In-memory only;
	// cleared on restart (no goroutines survive restart).
	deletionInFlight map[string]bool
	// deletionInFlightSince records when the in-flight flag was set, for
	// staleness detection in the sweep. In-memory only.
	deletionInFlightSince map[string]time.Time
	// deletionGeneration is a per-agent monotonically increasing counter
	// incremented each time a new teardown starts. The finalize handler
	// checks the generation to reject stale results from old teardown
	// attempts. In-memory only.
	deletionGeneration map[string]uint64
	// deletionSweepScheduled prevents the self-rescheduling deletion sweep
	// from being started more than once. In-memory only.
	deletionSweepScheduled bool

	// gitStatusCache memoises workspace.git_status responses per project.
	// worktree.Status() walks the entire repo via merkle trie (~20MB
	// transient allocation per call on moderate repos); without a cache,
	// every UI refresh re-walks the tree and accumulates GC pressure that
	// surfaces as visible memory growth. TTL is short so freshly-changed
	// files still appear quickly.
	gitStatusCache   map[string]gitStatusCacheEntry
	gitStatusCacheMu sync.Mutex
	// childNameSeq generates unique child actor names for fork children spawned
	// via agent_spawn_by_type. Concurrent fork calls use atomic Add.
	childNameSeq atomic.Uint64

	// execRegistry maps data.exec.kind → Executor. Lazily initialized on
	// first dispatch; populated with built-in executors (worker_task) in
	// ensureExecRegistry. The orchestrator (handleAgentSpawnAssign) reads
	// the bound task card's data.exec.kind, looks up the executor here,
	// and delegates — no switch-on-card-type in the orchestrator. New
	// execKinds (crawl, sub_map) register here the same way; nothing
	// else in the workspace needs to change.
	execRegistry *Registry

	// topo is the runtime topology provider injected from gospore
	// resources (same mechanism as the agent). The toolcall executor
	// resolves CallableInterface metadata — ServiceName routing, EffectKind
	// gating, and the request schema for argument validation — from its
	// snapshot instead of trusting card data, so cards only carry the
	// callable ID and cannot drift from the live protocol.
	topo appruntime.TopologyProvider
}

type gitStatusCacheEntry struct {
	at   time.Time
	resp domain.WorkspaceGitStatusResp
}

// subMapInstance tracks a sub_map executor activation: one parent task card
// maps to one independent sub-map instance and its owner agent. It is persisted
// in workspace state so sub-map completion can be propagated across restarts.
type subMapInstance struct {
	ParentTaskCardID  string         `json:"parentTaskCardId"`
	ParentAgentID     string         `json:"parentAgentId"`
	InstanceMapID     string         `json:"instanceMapId"`
	OwnerAgentActorID string         `json:"ownerAgentActorId"`
	ProjectID         string         `json:"projectId"`
	TemplateMapID     string         `json:"templateMapId"`
	Status            string         `json:"status"`
	Outputs           map[string]any `json:"outputs,omitempty"`
	CreatedAt         string         `json:"createdAt"`
}

// gitStatusCacheTTL bounds how long a cached git_status response is
// served before a fresh worktree.Status() walk is required. Short enough
// that UI-shown file changes feel live, long enough to coalesce rapid
// repeated refreshes (turn-complete bursts, multi-call orchestrations).
const gitStatusCacheTTL = 5 * time.Second

var _ persist.Persistent = (*Actor)(nil)

// OnInit loads persisted state.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("workspace"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if err := a.Load(); err != nil {
		ctx.Logger().Error("workspace: load state failed, starting with empty state (decode failures leave a state.json.corrupt-* backup for manual recovery)", "error", err)
	}
	a.seedAgentKindConfigs(ctx)
	return nil
}

func defaultRandomNameConfig(kind string) *domain.RandomNameConfig {
	pool := defaultNamePool(kind)
	prefixes := make([]string, 0, len(pool))
	suffixes := make([]string, 0, len(pool))
	for _, name := range pool {
		parts := strings.SplitN(name, " ", 2)
		if len(parts) == 2 {
			prefixes = append(prefixes, parts[0])
			suffixes = append(suffixes, parts[1])
		}
	}
	return &domain.RandomNameConfig{
		Enabled:  true,
		Prefixes: prefixes,
		Suffixes: suffixes,
	}
}

func defaultAgentKindConfigs() []domain.AgentKindConfig {
	configs := agentkit.BaseKindConfigs()
	envCtx := util.DetectEnvironment()
	for i := range configs {
		configs[i].EnvironmentContext = envCtx
		configs[i].RandomName = defaultRandomNameConfig(configs[i].Kind)
	}
	return configs
}

func defaultNamePool(kind string) []string {
	switch kind {
	case domain.AgentKindWorker:
		return []string{
			"Swift Fox", "Busy Bee", "Quick Ant", "Task Finch", "Patch Raven",
			"Lint Otter", "Build Badger", "Probe Heron", "Ship Sparrow", "Fix Ferret",
		}
	case domain.AgentKindCoder:
		return []string{
			"Byte Duck", "Bug Blob", "Loop Cat", "Git Octopus", "Compile Owl",
			"Debug Turtle", "Merge Snail", "Branch Ghost", "Commit Axolotl", "Lint Robot",
			"Cache Rabbit", "Regex Dragon", "JSON Chonk", "Query Duck", "Build Blob",
			"Test Cat", "Deploy Owl", "Shell Octopus", "Async Snail", "Type Axolotl",
			"Code Weaver", "Syntax Sprite", "Patch Penguin", "Stack Capybara", "Token Turtle",
			"Heap Chonk", "Render Mushroom", "Var Ghost", "Func Duck", "Chaos Cat",
		}
	case domain.AgentKindScout:
		return []string{
			"Path Hawk", "Trail Owl", "Map Mole", "Scent Hound", "Radar Bat",
			"Scout Finch", "Signal Fox", "Pulse Heron", "Trace Lynx", "Beacon Moth",
		}
	}
	return nil
}

func generateDisplayName(randomName *domain.RandomNameConfig, existingAgents []domain.AgentRef, projectID string) string {
	if randomName == nil || !randomName.Enabled {
		return ""
	}

	prefixes := randomName.Prefixes
	suffixes := randomName.Suffixes
	if len(prefixes) == 0 || len(suffixes) == 0 {
		return ""
	}

	used := make(map[string]bool)
	for _, ag := range existingAgents {
		if projectID == "" {
			if ag.ProjectID == "" {
				used[ag.DisplayName] = true
			}
		} else {
			if ag.ProjectID == projectID {
				used[ag.DisplayName] = true
			}
		}
	}

	maxAttempts := len(prefixes) * len(suffixes) * 2
	if maxAttempts < 100 {
		maxAttempts = 100
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		prefix := prefixes[mrand.Intn(len(prefixes))]
		suffix := suffixes[mrand.Intn(len(suffixes))]
		candidate := prefix + " " + suffix
		if !used[candidate] {
			return candidate
		}
	}

	return prefixes[mrand.Intn(len(prefixes))] + " " + suffixes[mrand.Intn(len(suffixes))] + " " + randomHexSuffix()
}

func normalizeAgentKindConfig(cfg domain.AgentKindConfig) domain.AgentKindConfig {
	if cfg.DisplayName == "" {
		switch cfg.Kind {
		case domain.AgentKindCoder:
			cfg.DisplayName = "Coder"
		}
	}
	if cfg.RolePromptRef.Kind == "" {
		cfg.RolePromptRef.Kind = "profile"
	}
	if cfg.RolePromptRef.Key == "" {
		// Prefer the built-in canonical ref for the kind (e.g. coordinator uses
		// a workspace scope) over blindly synthesizing "project.<kind>", which
		// would point at a non-existent profile card.
		if key := canonicalRolePromptRefKey(cfg.Kind); key != "" {
			cfg.RolePromptRef.Key = key
		} else {
			cfg.RolePromptRef.Key = "project." + cfg.Kind
		}
	}
	if cfg.RandomName == nil || !cfg.RandomName.Enabled {
		if cfg.RandomName == nil {
			cfg.RandomName = &domain.RandomNameConfig{}
		}
		cfg.RandomName.Enabled = true
		if len(cfg.RandomName.Prefixes) == 0 || len(cfg.RandomName.Suffixes) == 0 {
			pool := defaultNamePool(cfg.Kind)
			prefixes := []string{}
			suffixes := []string{}
			for _, name := range pool {
				parts := strings.SplitN(name, " ", 2)
				if len(parts) == 2 {
					prefixes = append(prefixes, parts[0])
					suffixes = append(suffixes, parts[1])
				}
			}
			cfg.RandomName.Prefixes = prefixes
			cfg.RandomName.Suffixes = suffixes
		}
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = DefaultGoalMaxTurns
	}
	return cfg
}

// canonicalRolePromptRefKey returns the built-in role prompt ref key for an
// agent kind, or "" when the kind has no built-in default. It lets
// normalizeAgentKindConfig default to the correct (possibly non-project) scope
// instead of blindly synthesizing "project.<kind>".
func canonicalRolePromptRefKey(kind string) string {
	for _, c := range agentkit.BaseKindConfigs() {
		if c.Kind == kind && c.RolePromptRef.Key != "" {
			return c.RolePromptRef.Key
		}
	}
	return ""
}

// DefaultGoalMaxTurns is the default autonomous turn budget applied to goals
// when an agent kind config does not specify MaxTurns. Configured per-kind via
// AgentKindConfig.MaxTurns; this is the fallback.
const DefaultGoalMaxTurns int32 = 200

func (a *Actor) seedAgentKindConfigs(ctx actor.Context) {
	changed := false
	// All AgentKindConfigs reconciliation runs under the write lock so
	// concurrent PureContext readers (list/get_agent_kind_configs) never see a
	// half-reconciled slice. The persist write happens after unlock so the
	// save path can take the read lock.
	a.kindConfigsMu.Lock()
	// Prune configs and agent references for retired builtin kinds. Only
	// system-managed leftovers are pruned: a user-created template that happens
	// to reuse a retired builtin slug (e.g. a custom "architect") is
	// SystemManaged=false and must survive. Agents are pruned only for kinds
	// whose system-managed config was actually removed, so a custom template's
	// live agents are never reaped.
	if len(retiredBuiltinKinds) > 0 {
		prunedKinds := make(map[string]bool)
		keptConfigs := a.AgentKindConfigs[:0:0]
		for _, cfg := range a.AgentKindConfigs {
			if retiredBuiltinKinds[cfg.Kind] && cfg.SystemManaged {
				changed = true
				prunedKinds[cfg.Kind] = true
				continue
			}
			keptConfigs = append(keptConfigs, cfg)
		}
		a.AgentKindConfigs = keptConfigs
		if len(prunedKinds) > 0 {
			keptAgents := a.Agents[:0:0]
			for _, ag := range a.Agents {
				if prunedKinds[ag.AgentKind] {
					changed = true
					continue
				}
				keptAgents = append(keptAgents, ag)
			}
			a.Agents = keptAgents
		}
	}
	for _, want := range defaultAgentKindConfigs() {
		want = normalizeAgentKindConfig(want)
		found := false
		for i, existing := range a.AgentKindConfigs {
			if existing.Kind != want.Kind {
				continue
			}
			a.AgentKindConfigs[i] = normalizeAgentKindConfig(existing)
			if a.AgentKindConfigs[i].DisplayName == "" {
				a.AgentKindConfigs[i].DisplayName = want.DisplayName
				changed = true
			}
			// SystemManaged is hardcoded per kind; reconcile any stale persisted value.
			if a.AgentKindConfigs[i].SystemManaged != want.SystemManaged {
				a.AgentKindConfigs[i].SystemManaged = want.SystemManaged
				changed = true
			}
			// Reconcile the role prompt ref against the canonical default. An
			// older build persisted the auto-default "project.<kind>" even for
			// workspace-scoped kinds (coordinator → "project.coordinator"),
			// which points at no profile card; rewrite a stale default that
			// still matches it. A genuinely user-chosen ref is preserved.
			cur := a.AgentKindConfigs[i].RolePromptRef
			if cur.Key == "" || cur.Kind == "" || (cur.Key == "project."+want.Kind && cur.Key != want.RolePromptRef.Key) {
				a.AgentKindConfigs[i].RolePromptRef = want.RolePromptRef
				changed = true
			}
			if a.AgentKindConfigs[i].DefaultBundleIDs == nil {
				a.AgentKindConfigs[i].DefaultBundleIDs = append([]string(nil), want.DefaultBundleIDs...)
				changed = true
			} else if len(want.DefaultBundleIDs) > 0 {
				// Reconcile persisted bundle lists with the current builtin
				// defaults: a default bundle added in code after the config was
				// persisted (e.g. builtin:bundle:workflow-tools) must propagate
				// to the stored config, or agents of that kind never mount it
				// and none of its callables reach the runtime tool set. Union —
				// persisted entries (including user customizations) are
				// preserved; only missing default bundles are added. Bundles
				// the user explicitly removed (RemovedBundleIDs) are never
				// re-added.
				existing := make(map[string]struct{}, len(a.AgentKindConfigs[i].DefaultBundleIDs))
				for _, id := range a.AgentKindConfigs[i].DefaultBundleIDs {
					existing[id] = struct{}{}
				}
				removed := make(map[string]struct{}, len(a.AgentKindConfigs[i].RemovedBundleIDs))
				for _, id := range a.AgentKindConfigs[i].RemovedBundleIDs {
					removed[id] = struct{}{}
				}
				for _, id := range want.DefaultBundleIDs {
					if _, ok := existing[id]; ok {
						continue
					}
					if _, ok := removed[id]; ok {
						continue
					}
					a.AgentKindConfigs[i].DefaultBundleIDs = append(a.AgentKindConfigs[i].DefaultBundleIDs, id)
					existing[id] = struct{}{}
					changed = true
				}
			}
			// Refresh stale auto-detected environment context. The hardcoded
			// linux/bash defaults were replaced with runtime detection, and we
			// now advertise the resolved shell executable. Preserve any extra
			// user-defined keys while updating the canonical ones.
			merged := make(map[string]string, len(want.EnvironmentContext)+len(a.AgentKindConfigs[i].EnvironmentContext))
			for k, v := range a.AgentKindConfigs[i].EnvironmentContext {
				merged[k] = v
			}
			if merged["shell_executable"] != want.EnvironmentContext["shell_executable"] || len(a.AgentKindConfigs[i].EnvironmentContext) == 0 {
				for k, v := range want.EnvironmentContext {
					merged[k] = v
				}
				changed = true
			}
			a.AgentKindConfigs[i].EnvironmentContext = merged
			if a.AgentKindConfigs[i].AutoAllowTools == nil {
				a.AgentKindConfigs[i].AutoAllowTools = want.AutoAllowTools
				changed = true
			}
			if len(a.AgentKindConfigs[i].SkillIDs) == 0 && len(want.SkillIDs) > 0 {
				a.AgentKindConfigs[i].SkillIDs = append([]string(nil), want.SkillIDs...)
				changed = true
			}
			if a.AgentKindConfigs[i].SystemFragmentRefs == nil {
				a.AgentKindConfigs[i].SystemFragmentRefs = want.SystemFragmentRefs
				changed = true
			}
			found = true
			break
		}
		if found {
			continue
		}
		want.NamePool = defaultNamePool(want.Kind)
		a.AgentKindConfigs = append(a.AgentKindConfigs, want)
		changed = true
	}
	// Ensure every valid kind has at least a minimal config with defaults.
	for _, info := range domain.ValidAgentKinds() {
		found := false
		for _, cfg := range a.AgentKindConfigs {
			if cfg.Kind == info.Kind {
				found = true
				break
			}
		}
		if found {
			continue
		}
		a.AgentKindConfigs = append(a.AgentKindConfigs, normalizeAgentKindConfig(domain.AgentKindConfig{
			Kind:          info.Kind,
			DisplayName:   info.DisplayName,
			UserCreatable: info.UserCreatable,
			SystemManaged: info.SystemManaged,
			NamePool:      defaultNamePool(info.Kind),
		}))
		changed = true
	}
	a.kindConfigsMu.Unlock()
	if changed {
		a.saveOrLog(ctx)
	}
}

// cloneAgentKindConfig returns a deep copy of a kind config for use as an
// independent user-defined template. The value slices/maps and the RandomName
// pointer are copied so later seed reconciliation of the base kind cannot
// mutate the clone (and vice versa) through shared backing arrays. Model-slot
// and policy pointers are treated as immutable projections and shared.
func cloneAgentKindConfig(cfg domain.AgentKindConfig) domain.AgentKindConfig {
	out := cfg
	out.SystemFragmentRefs = append([]gen.PromptRef(nil), cfg.SystemFragmentRefs...)
	out.DefaultBundleIDs = append([]string(nil), cfg.DefaultBundleIDs...)
	out.RemovedBundleIDs = append([]string(nil), cfg.RemovedBundleIDs...)
	out.AutoAllowTools = append([]string(nil), cfg.AutoAllowTools...)
	out.AutoAllowCandidates = append([]string(nil), cfg.AutoAllowCandidates...)
	out.NamePool = append([]string(nil), cfg.NamePool...)
	out.SkillIDs = append([]string(nil), cfg.SkillIDs...)
	out.DefaultCardRefs = append([]gen.CardRef(nil), cfg.DefaultCardRefs...)
	if cfg.EnvironmentContext != nil {
		m := make(map[string]string, len(cfg.EnvironmentContext))
		for k, v := range cfg.EnvironmentContext {
			m[k] = v
		}
		out.EnvironmentContext = m
	}
	if cfg.RandomName != nil {
		rn := *cfg.RandomName
		rn.Prefixes = append([]string(nil), cfg.RandomName.Prefixes...)
		rn.Suffixes = append([]string(nil), cfg.RandomName.Suffixes...)
		out.RandomName = &rn
	}
	return out
}

func (a *Actor) findAgentKindConfig(kind string) (domain.AgentKindConfig, bool) {
	a.kindConfigsMu.RLock()
	defer a.kindConfigsMu.RUnlock()
	for _, cfg := range a.AgentKindConfigs {
		if cfg.Kind != kind {
			continue
		}
		cfg = normalizeAgentKindConfig(cfg)
		// Runtime fallback: if the persisted config predates SkillIDs, merge
		// them from the built-in defaults so agents see skills immediately
		// without requiring a workspace restart.
		if len(cfg.SkillIDs) == 0 {
			for _, def := range defaultAgentKindConfigs() {
				if def.Kind == kind && len(def.SkillIDs) > 0 {
					cfg.SkillIDs = append([]string(nil), def.SkillIDs...)
					break
				}
			}
		}
		// Runtime fallback: propagate default bundles added in code after the
		// config was persisted (e.g. builtin:bundle:workflow-tools). Without
		// this, a stale stored bundle list silently strips every callable the
		// new bundle declares from the agent's runtime tool set. Union only —
		// persisted entries are preserved and bundles the user explicitly
		// removed (RemovedBundleIDs) are never re-added.
		for _, def := range defaultAgentKindConfigs() {
			if def.Kind != kind {
				continue
			}
			if len(def.DefaultBundleIDs) == 0 {
				break
			}
			existing := make(map[string]struct{}, len(cfg.DefaultBundleIDs))
			for _, id := range cfg.DefaultBundleIDs {
				existing[id] = struct{}{}
			}
			removed := make(map[string]struct{}, len(cfg.RemovedBundleIDs))
			for _, id := range cfg.RemovedBundleIDs {
				removed[id] = struct{}{}
			}
			for _, id := range def.DefaultBundleIDs {
				if _, ok := existing[id]; ok {
					continue
				}
				if _, ok := removed[id]; ok {
					continue
				}
				cfg.DefaultBundleIDs = append(cfg.DefaultBundleIDs, id)
				existing[id] = struct{}{}
			}
			break
		}
		return cfg, true
	}
	// Fallback: if the kind is valid but not yet seeded, return a minimal
	// normalized config so that creation can proceed without requiring a
	// workspace actor restart.
	for _, info := range domain.ValidAgentKinds() {
		if info.Kind == kind {
			return normalizeAgentKindConfig(domain.AgentKindConfig{
				Kind:          info.Kind,
				DisplayName:   info.DisplayName,
				UserCreatable: info.UserCreatable,
				SystemManaged: info.SystemManaged,
				NamePool:      defaultNamePool(info.Kind),
			}), true
		}
	}
	return domain.AgentKindConfig{}, false
}

func (a *Actor) Type() string { return "workspace" }

// OnStart spawns project actors and registers callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("workspace: starting", "id", a.actorID)
	_ = ctx.RegisterEventKind("mounts", domain.WorkspaceMountsEvent{}, actor.Public())
	_ = ctx.RegisterEventKind("agents_changed", domain.WorkspaceAgentsChangedEvent{}, actor.Public())
	_ = ctx.RegisterEventKind("agent_list_state", gen.WorkspaceAgentListStateEvent{}, actor.Public())
	_ = ctx.RegisterEventKind("workspace.log", gen.WorkspaceLogStreamEvent{}, actor.Public())

	// Resolve the topology provider before executor dispatch: the toolcall
	// executor reads CallableInterface metadata (ServiceName, EffectKind,
	// request schema) from its snapshot at preflight time.
	if prov, ok := resource.Get[appruntime.TopologyProvider](ctx.Resources(), appruntime.TopologyKey); ok {
		a.topo = prov
	}

	// Initialize the execKind → executor registry before any orchestrator
	// dispatch (handleAgentSpawnAssign). worker_task is the only built-in
	// kind today; future kinds (crawl, sub_map) register here the same
	// way. The dispatcher never branches on card type — it only reads
	// data.exec.kind and looks up the executor.
	a.ensureExecRegistry()

	// Resolve the global aistats system actor so per-workspace discovery still
	// works via workspace.aistats_actor_id. Telemetry records are written to
	// this single global actor and scoped by WorkspaceID at query time.
	a.resolveAistats(ctx)

	// Wire the live log streamer to the workspace.log event kind. The
	// streamer is owned by pkg/logging and shared by the backend ring and
	// the console store; this actor only subscribes to its batched flushes.
	if streamer, ok := resource.Get[*logging.LogStreamer](ctx.Resources(), logging.LogStreamerKey); ok && streamer != nil {
		streamer.SetHandler(func(batch []logging.StreamEntry) {
			if !ctx.HasEventSubscribers("workspace.log") {
				return
			}
			entries := make([]gen.WorkspaceLogStreamEntry, len(batch))
			for i, e := range batch {
				file, line := splitLogCaller(e.LogEntry.Caller)
				entries[i] = gen.WorkspaceLogStreamEntry{
					Timestamp:  e.LogEntry.Timestamp,
					Level:      e.LogEntry.Level,
					CallerFile: file,
					CallerLine: line,
					Message:    e.LogEntry.Message,
					Fields:     e.LogEntry.Fields,
					Source:     e.Source,
					Seq:        e.Seq,
				}
			}
			_ = ctx.EmitEvent("workspace.log", gen.WorkspaceLogStreamEvent{Entries: entries})
		})
	}

	// The former "lifecycle" and "deletion" stateful loop lanes are gone:
	// after the Wave2 stateless conversions every handler that used to pin
	// them (spawn/delete/clone/review/terminate, deletion finalize) is a
	// PureContext handler running on forked goroutines, serialized by
	// agentsMu/deletionMu instead of a mailbox.

	if err := ctx.Register("workspace.agent_list_state", a.handleAgentListState, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register agent_list_state: %w", err)
	}
	if err := ctx.Register("workspace.aistats_actor_id", a.handleAistatsActorID, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register aistats_actor_id: %w", err)
	}
	if err := ctx.Register("workspace.slash_commands_list", a.handleListSlashCommands, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register slash_commands.list: %w", err)
	}
	if err := ctx.Register("workspace.debug_commands_list", a.handleListDebugCommands, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register debug_commands.list: %w", err)
	}
	if err := ctx.Register("workspace.debug_command_exec", a.handleExecDebugCommand, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register debug_command.exec: %w", err)
	}
	if err := ctx.Register("workspace.builtin_modes_list", a.handleListBuiltinModes, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register builtin.modes.list: %w", err)
	}
	if err := ctx.Register("workspace.agent_status_update", a.handleAgentStatusUpdate, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register agent_status_update: %w", err)
	}
	if err := ctx.Register("workspace.agent_access", a.handleAgentAccess, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register agent_access: %w", err)
	}
	if err := ctx.Register("workspace.agent_loaded", a.handleAgentLoaded, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register agent_loaded: %w", err)
	}
	if err := ctx.Register("workspace.agent_unload", a.handleAgentUnload, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible))); err != nil {
		return fmt.Errorf("workspace: register agent_unload: %w", err)
	}
	if err := ctx.Register("workspace.agent_spawn_scheduler", a.handleAgentSpawnScheduler, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible))); err != nil {
		return fmt.Errorf("workspace: register agent_spawn_scheduler: %w", err)
	}
	if err := ctx.Register("workspace.internal_deletion_finalize", a.handleDeletionFinalize, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register internal_deletion_finalize: %w", err)
	}
	if err := ctx.Register("workspace.internal_deletion_retry", a.handleDeletionRetry, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register internal_deletion_retry: %w", err)
	}
	if err := ctx.Register("workspace.internal_deletion_sweep", a.handleDeletionSweep, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register internal_deletion_sweep: %w", err)
	}
	if err := ctx.Register("workspace.internal_flush_state", a.handleStateFlush, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register internal_flush_state: %w", err)
	}
	if err := ctx.Register("workspace.internal_scatter_sweep", a.handleScatterSweep, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register internal_scatter_sweep: %w", err)
	}
	// Re-spawn project actors for restored mounts so the project tree
	// survives process restart. Project state is keyed on the actorID
	// (project.Save uses a.actorID), so a drifted ID orphans Roots. On
	// recovery we MUST reuse the persisted ID; a malformed persisted ID
	// surfaces as a spawn skip (not auto-gen) so the corruption is visible.
	for i := range a.Mounts {
		m := &a.Mounts[i]
		props := actor.PropsFromFunc(project.NewActor(m.Path, m.System)).WithPlanner()
		if m.ActorID != "" {
			cid, err := identity.ParseCanonicalID(m.ActorID)
			if err != nil {
				ctx.Logger().Error("workspace: re-spawn project failed: persisted ActorID is malformed; skipping (state preserved, will retry next restart)",
					"name", m.Name, "actorID", m.ActorID, "error", err)
				continue
			}
			props = props.WithID(id.From(cid))
		}
		spawned, err := ctx.Spawn(props, m.Name)
		if err != nil {
			ctx.Logger().Error("workspace: re-spawn project failed", "name", m.Name, "path", m.Path, "error", err)
			continue
		}
		if spawned == nil {
			continue
		}
		newID := spawned.ID().String()
		if m.ActorID != "" && newID != m.ActorID {
			ctx.Logger().Error("workspace: re-spawn project returned a different ActorID than requested; persistence may drift",
				"name", m.Name, "expected", m.ActorID, "got", newID)
		}
		m.ActorID = newID
		ctx.Logger().Info("workspace: re-spawned project", "name", m.Name, "actorId", m.ActorID)
	}

	// Ensure the system plugin-development project is always mounted.
	if !a.NoSystemProject {
		a.ensureSystemMetaProject(ctx)
		ensureAppDirs(ctx)
	}

	var agentsChanged bool

	// Normalize the process-unique global system agent (Coordinator): it is
	// workspace-global (ProjectID == "") and must exist at most once. Older
	// builds scoped it to the system meta project, and a weak dedup guard
	// could even leave duplicate coordinators; collapse them.
	if a.normalizeGlobalSystemAgents() {
		agentsChanged = true
	}

	// Re-spawn agent actors via their project actors so agents appear
	// as children of their project in the actor tree.
	//
	// All agents are lazy-loaded: their actor process is not spawned here;
	// they are only started when the user explicitly calls workspace.load_agent.
	// ActorID is kept stable across restarts so timeline history and context
	// are preserved without needing a separate inactiveActorIDs lookup.
	//
	// Agents are lazy-loaded after a process restart. ActorID remains their
	// durable identity; LoadState alone records whether an actor is live.
	actorIDToAgentID := make(map[string]string)
	for i := range a.Agents {
		ag := &a.Agents[i]
		if ag.ActorID != "" {
			actorIDToAgentID[ag.ActorID] = ag.ID
		}
		if ag.LoadState != "unloaded" {
			ag.LoadState = "unloaded"
			ag.Degraded = false
			ag.DegradedReason = ""
			ag.ActiveTurnRef = ""
			agentsChanged = true
		}
		// A paused turn remains resumable after restart even though its actor is
		// lazy-unloaded. Keep that durable recovery state in the sidebar; LoadState
		// still tells the frontend that loading is required before interaction.
		// A running actor no longer exists after restart, so expose it as paused
		// until the agent validates and reports its recovered turn state.
		switch ag.Status {
		case "running", "waiting":
			// A running actor no longer exists after restart; a workflow owner
			// parked in waiting can no longer be woken by its (dead) updater.
			// Expose both as paused; recoverTurnStatus canonicalizes the turn
			// to paused/recovery once the agent is loaded.
			ag.Status = "paused"
			agentsChanged = true
		case "", "idle", "paused":
		default:
			ag.Status = "idle"
			agentsChanged = true
		}
	}
	if agentsChanged {
		a.saveOrLog(ctx)
	}

	// Refresh runtime state (including inferred title) from each re-spawned
	// agent so the initial agent_list_state snapshot is authoritative.
	a.refreshAgentStatuses(ctx)

	// The Coordinator is NOT auto-created at startup. It is created by the user
	// through the onboarding flow (workspace.create_agent with
	// AgentKind=coordinator). It is a workspace-global singleton once created.

	// Build the authoritative agent list snapshot from restored state.
	initialList := a.buildAgentListState(true)
	// Let connected frontends know the initial list is ready. Skip the event
	// when there are no agents to avoid extra noise and existing tests that
	// count startup events.
	if len(initialList.Items) > 0 {
		a.emitAgentListState(ctx, true)
	}

	// Resume any interrupted cascade deletions: agents still marked "deleting"
	// from a prior process lifetime get their async teardown re-kicked here.
	a.resumeDeletionIntents(ctx)

	// Start the periodic deletion sweep as a safety net for ctx.After
	// scheduling failures. The sweep self-reschedules.
	a.startDeletionSweep(ctx)

	// Re-arm event_wait activations restored by Load: each persisted wait
	// gets a fresh subscription goroutine; deadlines keep their original
	// clock, and an already-expired deadline completes as timed out.
	a.rearmEventWaits(ctx)

	// Start the periodic scatter join sweep (self-reschedules; no-op when
	// no fan-outs are tracked).
	a.startScatterSweep(ctx)

	// Push initial mounts to filesystem for sandbox initialization
	a.pushRootsToFilesystem(ctx)

	// Resume any agent clones that were in-flight when the process was shut down.
	a.resumePendingClones(ctx, actorIDToAgentID)

	if err := ctx.Register("workspace.mount", a.handleMount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register mount: %w", err)
	}
	if err := ctx.Register("workspace.unmount", a.handleUnmount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register unmount: %w", err)
	}
	if err := ctx.Register("workspace.list_project", a.handleList, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List all mounted projects in the workspace with their refs, mounts, and permission modes. Use it to discover project IDs before calling project-scoped callables."),
	); err != nil {
		return fmt.Errorf("workspace: register list: %w", err)
	}
	if err := ctx.Register("workspace.report_error", a.handleReportError, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register report_error: %w", err)
	}
	if err := ctx.Register("workspace.create", a.handleCreate, actor.AdminOnly(),
		actor.WithDescription("Create a new project: scaffold the directory and mount it as a workspace project (AppKind dev-app/sporeapp requires Path). Developer role only; rarely needed from agent flows."),
	); err != nil {
		return fmt.Errorf("workspace: register create: %w", err)
	}
	if err := ctx.Register("workspace.add_mount", a.handleAddMount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register add_mount: %w", err)
	}
	if err := ctx.Register("workspace.remove_mount", a.handleRemoveMount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register remove_mount: %w", err)
	}
	if err := ctx.Register("workspace.list_agents", a.handleListAgents, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List agents in the workspace, enriched with live status (loading/active/paused/unloaded). Use it to find agent actor IDs, check which workers are still running, and confirm an agent was torn down after review. Optional filters: Query (substring), ProjectId, ParentAgentId (only direct children of that agent), ChildrenOnly (true = shorthand for your own children)."),
	); err != nil {
		return fmt.Errorf("workspace: register list_agents: %w", err)
	}
	if err := ctx.Register("workspace.agents", a.handleAgents, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register agents: %w", err)
	}
	if err := ctx.Register("workspace.create_agent", a.handleCreateAgent, actor.AdminOnly(),
		actor.WithDescription("Create a persistent agent from an agent kind config (AgentKind required; model slots inherit the kind defaults). For workflow workers use workspace.agent_spawn_assign; for fork children use the fork tools."),
	); err != nil {
		return fmt.Errorf("workspace: register create_agent: %w", err)
	}
	if err := ctx.Register("workspace.update_agent", a.handleUpdateAgent, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register update_agent: %w", err)
	}
	if err := ctx.Register("workspace.delete_agent", a.handleDeleteAgent, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register delete_agent: %w", err)
	}
	if err := ctx.Register("workspace.create_app_agent", a.handleCreateAppAgent, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register create_app_agent: %w", err)
	}
	if err := ctx.Register("workspace.remove_app_agent", a.handleRemoveAppAgent, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register remove_app_agent: %w", err)
	}
	if err := ctx.Register("workspace.project_wiki_forward", a.handleProjectWikiForward, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register project_wiki_forward: %w", err)
	}
	if err := ctx.Register("workspace.load_agent", a.handleLoadAgent, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register load_agent: %w", err)
	}
	if err := ctx.Register("workspace.coordinator_lookup", a.handleCoordinatorLookup, actor.Internal()); err != nil {
		return fmt.Errorf("workspace: register coordinator.lookup: %w", err)
	}
	if err := ctx.Register("workspace.coordinator_peek", a.handleCoordinatorPeek, actor.Internal(),
		actor.WithDescription("Read-only Coordinator presence/nickname query answered from the agent-list snapshot; never loads (spawns) the Coordinator. Use coordinator_lookup when a live actor is required."),
	); err != nil {
		return fmt.Errorf("workspace: register coordinator.peek: %w", err)
	}
	if err := ctx.Register("workspace.clone_agent", a.handleCloneAgent, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register clone_agent: %w", err)
	}
	if err := ctx.Register("workspace.list_agent_kinds", a.handleListAgentKinds, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register list_agent_kinds: %w", err)
	}
	if err := ctx.Register("workspace.list_agent_kind_configs", a.handleListAgentKindConfigs, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register list_agent_kind_configs: %w", err)
	}
	if err := ctx.Register("workspace.get_agent_kind_config", a.handleGetAgentKindConfig, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register get_agent_kind_config: %w", err)
	}
	if err := ctx.Register("workspace.save_agent_kind_config", a.handleSaveAgentKindConfig, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register save_agent_kind_config: %w", err)
	}
	if err := ctx.Register("workspace.create_agent_kind", a.handleCreateAgentKind, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register create_agent_kind: %w", err)
	}
	if err := ctx.Register("workspace.delete_agent_kind", a.handleDeleteAgentKind, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register delete_agent_kind: %w", err)
	}
	if err := ctx.Register("workspace.update_project", a.handleUpdateProject, actor.AdminOnly()); err != nil {
		return fmt.Errorf("workspace: register update_project: %w", err)
	}
	if err := ctx.Register("workspace.account", a.handleCurrentAccount, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return the caller's identity snapshot: account id, display name, roles, and default actor context. Use it to check who the caller is acting as."),
	); err != nil {
		return fmt.Errorf("workspace: register account: %w", err)
	}
	if err := ctx.Register("workspace.session", a.handleCurrentSession, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register session: %w", err)
	}
	if err := ctx.Register("workspace.preferences_get", a.handleGetAccountPreferences, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register preferences.get: %w", err)
	}
	if err := ctx.Register("workspace.preferences_save", a.handleSaveAccountPreferences, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register preferences.save: %w", err)
	}
	if err := ctx.Register("workspace.shell_env_probe", a.handleShellEnvProbe, actor.Public(),
		actor.WithEffect(string(domain.EffectNone))); err != nil {
		return fmt.Errorf("workspace: register shell_env_probe: %w", err)
	}
	if err := ctx.Register("workspace.shell_pref_save", a.handleShellPrefSave, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible))); err != nil {
		return fmt.Errorf("workspace: register shell_pref_save: %w", err)
	}
	if err := ctx.Register("workspace.ui_get", a.handleGetWorkspaceUI, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.get: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_layout", a.handleSaveWorkspaceLayout, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_layout: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_panels", a.handleSaveWorkspacePanels, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_panels: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_dock", a.handleSaveWorkspaceDock, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_dock: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_ai_shell", a.handleSaveWorkspaceAIShell, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_ai_shell: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_project_card_browser", a.handleSaveWorkspaceProjectCardBrowser, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_project_card_browser: %w", err)
	}
	if err := ctx.Register("workspace.ui_save_explorer", a.handleSaveWorkspaceExplorer, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register workspace.ui.save_explorer: %w", err)
	}
	if err := ctx.Register("workspace.git_status", a.handleGitStatus, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register git_status: %w", err)
	}
	if err := ctx.Register("workspace.git_log", a.handleGitLog, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register git_log: %w", err)
	}
	if err := ctx.Register("workspace.git_diff", a.handleGitDiff, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register git_diff: %w", err)
	}
	if err := ctx.Register("workspace.git_add", a.handleGitAdd, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_add: %w", err)
	}
	if err := ctx.Register("workspace.git_commit", a.handleGitCommit, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_commit: %w", err)
	}
	if err := ctx.Register("workspace.git_push", a.handleGitPush, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_push: %w", err)
	}
	if err := ctx.Register("workspace.git_pull", a.handleGitPull, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_pull: %w", err)
	}
	if err := ctx.Register("workspace.git_branch", a.handleGitBranch, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_branch: %w", err)
	}
	if err := ctx.Register("workspace.git_checkout", a.handleGitCheckout, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_checkout: %w", err)
	}
	if err := ctx.Register("workspace.git_reset", a.handleGitReset, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_reset: %w", err)
	}
	if err := ctx.Register("workspace.git_stash_save", a.handleGitStashSave, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_stash_save: %w", err)
	}
	if err := ctx.Register("workspace.git_stash_pop", a.handleGitStashPop, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_stash_pop: %w", err)
	}
	if err := ctx.Register("workspace.git_stash_list", a.handleGitStashList, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_stash_list: %w", err)
	}
	if err := ctx.Register("workspace.git_stash_drop", a.handleGitStashDrop, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_stash_drop: %w", err)
	}
	if err := ctx.Register("workspace.git_remote_list", a.handleGitRemoteList, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_remote_list: %w", err)
	}
	if err := ctx.Register("workspace.git_remote_add", a.handleGitRemoteAdd, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_remote_add: %w", err)
	}
	if err := ctx.Register("workspace.git_remote_remove", a.handleGitRemoteRemove, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_remote_remove: %w", err)
	}
	if err := ctx.Register("workspace.git_blame", a.handleGitBlame, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_blame: %w", err)
	}
	if err := ctx.Register("workspace.git_config_get", a.handleGitConfigGet, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_config_get: %w", err)
	}
	if err := ctx.Register("workspace.git_config_set", a.handleGitConfigSet, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register git_config_set: %w", err)
	}
	if err := ctx.Register("workspace.git_show", a.handleGitShow, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register git_show: %w", err)
	}
	if err := ctx.Register("workspace.git_fetch", a.handleGitFetch, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_fetch: %w", err)
	}
	if err := ctx.Register("workspace.git_discard", a.handleGitDiscard, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_discard: %w", err)
	}
	if err := ctx.Register("workspace.git_amend", a.handleGitAmend, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_amend: %w", err)
	}
	if err := ctx.Register("workspace.git_tag_list", a.handleGitTagList, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("workspace: register git_tag_list: %w", err)
	}
	if err := ctx.Register("workspace.git_tag_create", a.handleGitTagCreate, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_tag_create: %w", err)
	}
	if err := ctx.Register("workspace.git_tag_delete", a.handleGitTagDelete, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_tag_delete: %w", err)
	}
	if err := ctx.Register("workspace.git_merge", a.handleGitMerge, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("workspace: register git_merge: %w", err)
	}
	if err := ctx.Register("workspace.agent_send_message", a.handleAgentSendMessage, actor.Public(),
		actor.WithDescription("Fire-and-forget delivery of text to another agent's inbox (returns immediately; the recipient processes it asynchronously). The message always reaches the recipient's LLM context: an idle agent wakes and starts a new turn for it; an agent mid-turn absorbs it as a pending submit at the next step boundary. Use it for notifications and for waking or instructing an existing agent. Required: ToAgentId, Text."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.send_message: %w", err)
	}
	if err := ctx.Register("workspace.agent_read_message", a.handleAgentReadMessage, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Read a target agent's conversation text, newest entry first. Covers all closed steps, including the active turn's already-closed AI steps; tool-call steps show the sent parameters, not results. Optional Limit (last N, default 20, max 200) and BeforeSeq (continue reading strictly older than a seq for paging). Required: ToAgentId."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.read_message: %w", err)
	}
	if err := ctx.Register("workspace.agent_pause", a.handleAgentPause, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Pause a target agent at its next safe point (or cascade-pause its workflow children when it is a waiting owner). Only the target's direct owner, the target itself, or a human/admin may call this. Required: ToAgentId. Optional: Reason. Reversible via workspace.agent_resume."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.pause: %w", err)
	}
	if err := ctx.Register("workspace.agent_resume", a.handleAgentResume, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Resume a paused target agent (wakes a user-paused engine or restores a paused-from-waiting owner). Same authorization as agent_pause. Required: ToAgentId. Optional: Reason."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.resume: %w", err)
	}
	if err := ctx.Register("workspace.agent_spawn_assign", a.handleAgentSpawnAssign, actor.Public(),
		actor.WithDescription("Atomically spawn a temporary agent, bind it to a task card, use that card's body as the task context, and start its first turn. Required: To (display name), AgentKind (e.g. worker), BoundTaskCardId. Optional: InterpretedGoal, MaxTurns, ProjectId, WorktreeBranch (git-safe ASCII slug for the child worktree branch; use it when task card titles contain CJK or special characters — when omitted a branch name is auto-generated). Returns AgentActorId, DisplayName, Goal."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.spawn_assign: %w", err)
	}
	if err := ctx.Register("workspace.agent_spawn_by_type", a.handleAgentSpawnByType, actor.Public(),
		actor.WithDescription("Spawn a short-lived fork child agent of an explicit type under the calling parent agent. Restricted: requires CallerAgentId injected by the turn engine and verified via requireActiveWorkflow."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.spawn_by_type: %w", err)
	}
	if err := ctx.Register("workspace.agent_assign", a.handleAgentAssign, actor.Public(),
		actor.WithDescription("Assign a claimed task card to an existing agent: CAS backlog/todo -> doing, then hand the goal to the agent."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.assign: %w", err)
	}
	if err := ctx.Register("workspace.workflow_start", a.handleWorkflowStart, actor.Public(),
		actor.WithDescription("Start a workflow map on an existing agent: mount the agent when unloaded, activate the workflow (claims map ownership), then kick off the first orchestration turn."),
	); err != nil {
		return fmt.Errorf("workspace: register workflow_start: %w", err)
	}
	if err := ctx.Register("workspace.gate_approve", a.handleWorkspaceGateApprove, actor.Public(),
		actor.WithDescription("Approve a workflow gate card (data.exec.kind: gate). CAS-flips the bound task card from doing to done, writes the approval record (approver, approved_at, optional form_values normalized via the request layout, optional callable_id and note) to task_outputs, and dismisses the companion-window gate toast. Authorization mirrors workspace.agent_review: trusted developer/admin identity bypasses; non-developer callers must supply a non-empty CallerAgentId that resolves to a live agent with an active workflow."),
	); err != nil {
		return fmt.Errorf("workspace: register gate_approve: %w", err)
	}
	if err := ctx.Register("workspace.gate_reject", a.handleWorkspaceGateReject, actor.Public(),
		actor.WithDescription("Reject a workflow gate card (data.exec.kind: gate). CAS-flips the bound task card from doing to failed and writes the rejection record (approver, rejected_at, optional reason, optional callable_id) to task_outputs, then dismisses the companion-window gate toast. Same authorization rules as workspace.gate_approve."),
	); err != nil {
		return fmt.Errorf("workspace: register gate_reject: %w", err)
	}
	// Security gate v2: stamp mutating toolcall effects onto a workflow
	// map card after the agent confirms workflow start. The agent
	// invokes this callable from activateWorkflow via workspace service
	// lookup (avoids agent→workspace import cycle). The handler delegates
	// to StampWorkflowMutatingEffects, which scans the workflow's task
	// cards and writes the data.stamped_effects list that the toolcall
	// executor's Preflight double-checks before invoking mutating
	// callables. Internal-only — the user never invokes this directly.
	if err := ctx.Register("workspace.stamp_workflow_mutating_effects", a.handleStampWorkflowMutatingEffects, actor.Internal(),
		actor.WithDescription("Stamp mutating toolcall effects onto a workflow map card (data.stamped_effects) so the toolcall executor can verify plan-level approval before invoking mutating callables. Invoked by the agent's activateWorkflow after the user approves the workflow plan."),
	); err != nil {
		return fmt.Errorf("workspace: register stamp_workflow_mutating_effects: %w", err)
	}
	if err := ctx.Register("workspace.agent_review", a.handleAgentReview, actor.Public(),
		actor.WithDescription("Review an agent's completed work. AgentActorId plus Decision (approve | reject). For agent callers: approve verifies the worker's branch is already merged into your worktree (git merge <branch> first), cleans up the child worktree, sets the bound task card to done, and tears the agent down; reject sets the task card back to doing, rebases the child worktree onto your branch HEAD, and resumes the agent with Feedback text. For human/admin UI callers: approve auto-merges the worker's worktree into yours. Optional TaskCardId overrides which card the status change applies to."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.review: %w", err)
	}
	if err := ctx.Register("workspace.agent_terminate", a.handleAgentTerminate, actor.Public(),
		actor.WithDescription("Terminate a child agent (workflow worker, fork child, or other spawned subagent). Used by the parent agent or the UI when a child fails or is unresponsive."),
	); err != nil {
		return fmt.Errorf("workspace: register agent.terminate: %w", err)
	}
	if err := ctx.Register("workspace.system_tree", a.handleSystemTree, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register system_tree: %w", err)
	}
	if err := ctx.Register("workspace.logs_query", a.handleLogsQuery, actor.Public()); err != nil {
		return fmt.Errorf("workspace: register logs.query: %w", err)
	}
	if err := ctx.Register("workspace.wiki_list_cards", a.handleSystemWikiListCards, actor.Public(),
		actor.WithDescription("List mono cards in the system meta project. Tree mode (default) renders the hierarchy under RootId as ASCII Tree + structured Nodes; Flat mode (Flat=true) returns a flat array with title Query and metadata filters, sorted by OrderBy (default -modified), paginated by Limit, with Total when Total=true."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_list_cards: %w", err)
	}
	if err := ctx.Register("workspace.wiki_search_card_content", a.handleSystemWikiSearchCardContent, actor.Public(),
		actor.WithDescription("Search mono cards in the system meta project by body content (substring or /regexp/), with optional tags/status/type/source filters. Returns matching cards with a snippet and line number of the first match, and an untruncated Total."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_search_card_content: %w", err)
	}
	if err := ctx.Register("workspace.wiki_list_starred", a.handleWikiListStarred, actor.Public(),
		actor.WithDescription("List every knowledge-base starred card across all mounted projects in one round trip. Each item carries the owning ProjectID/ProjectName plus the CardID, projects in mount order and cards most-recently-starred first. Unreachable projects are skipped."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_list_starred: %w", err)
	}
	if err := ctx.Register("workspace.wiki_get_card", a.handleSystemWikiGetCard, actor.Public(),
		actor.WithDescription("Get a mono card from the system meta project."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_get_card: %w", err)
	}
	if err := ctx.Register("workspace.wiki_create_card", a.handleSystemWikiCreateCard, actor.Public(),
		actor.WithDescription("Create a mono card in the system meta project."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_create_card: %w", err)
	}
	if err := ctx.Register("workspace.wiki_edit_card", a.handleSystemWikiEditCard, actor.Public(),
		actor.WithDescription("Edit a mono card in the system meta project."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_edit_card: %w", err)
	}
	if err := ctx.Register("workspace.wiki_delete_card", a.handleSystemWikiDeleteCard, actor.Public(),
		actor.WithDescription("Delete a mono card from the system meta project."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.wiki_delete_card: %w", err)
	}
	if err := ctx.Register("workspace.component_get", a.handleSystemComponentGet, actor.Public(),
		actor.WithDescription("Get a component descriptor from the system meta project (resolves builtin cards)."),
	); err != nil {
		return fmt.Errorf("workspace: register workspace.component.get: %w", err)
	}
	if err := ctx.RegisterDomain("workspace").Expose(); err != nil {
		return fmt.Errorf("workspace: expose service: %w", err)
	}
	return nil
}

// resolveAistats looks up the single global aistats system actor and caches its
// id for discovery via workspace.aistats_actor_id. The global actor aggregates
// telemetry from all workspaces; per-workspace views are filtered by
// WorkspaceID at query time.
func (a *Actor) resolveAistats(ctx actor.Context) {
	if r, ok := ctx.LookupService("aistats"); ok {
		a.aistatsSnap.Store(aistatsState{ref: r, id: r.ID().String()})
		ctx.Logger().Info("workspace: resolved global aistats", "actorID", r.ID().String())
	} else {
		ctx.Logger().Warn("workspace: global aistats actor not found")
	}
}

// legacySystemMetaProjectNames are historical names of the system meta project that
// predate the rename to "workspace".
func isLegacySystemMetaProjectName(name string) bool {
	switch name {
	case "system-plugins", "systemplugins":
		return true
	default:
		return false
	}
}

// ensureSystemMetaProject creates the system meta project if it is
// not already mounted. It is always located under config.DataDir()/data.
// Existing mounts at the system path or with a legacy system meta project name are
// promoted and renamed to the canonical "workspace" name.
func (a *Actor) ensureSystemMetaProject(ctx actor.Context) {
	sysPath := systemMetaProjectPath()
	normalizedSysPath := util.NormalizePath(sysPath)

	// 1) Already marked system: normalize name/path and stop.
	for i := range a.Mounts {
		if !a.Mounts[i].System {
			continue
		}
		changed := false
		if a.Mounts[i].Name != systemMetaProjectName {
			if conflict := a.findMountByName(systemMetaProjectName, a.Mounts[i].ActorID); conflict {
				ctx.Logger().Error("workspace: cannot rename system meta project; name already used",
					"from", a.Mounts[i].Name, "to", systemMetaProjectName)
			} else {
				from := a.Mounts[i].Name
				a.mountMu.Lock()
				a.Mounts[i].Name = systemMetaProjectName
				a.mountMu.Unlock()
				ctx.Logger().Info("workspace: renaming system meta project",
					"from", from, "to", systemMetaProjectName)
				changed = true
			}
		}
		if util.NormalizePath(a.Mounts[i].Path) != normalizedSysPath {
			ctx.Logger().Warn("workspace: system meta project path mismatch",
				"expected", normalizedSysPath, "got", a.Mounts[i].Path)
		}
		if changed {
			a.saveOrLog(ctx)
			a.emitMounts(ctx)
		}
		return
	}

	// 2) Promote existing mount at the system path or with a legacy name.
	for i := range a.Mounts {
		pathMatch := util.NormalizePath(a.Mounts[i].Path) == normalizedSysPath
		legacyName := isLegacySystemMetaProjectName(a.Mounts[i].Name)
		if !pathMatch && !legacyName {
			continue
		}
		oldName := a.Mounts[i].Name
		nameConflict := false
		// Resolve the name conflict before taking the write lock:
		// findMountByName acquires mountMu.RLock itself and must never run
		// under mountMu.Lock.
		if a.Mounts[i].Name != systemMetaProjectName {
			nameConflict = a.findMountByName(systemMetaProjectName, a.Mounts[i].ActorID)
		}
		a.mountMu.Lock()
		a.Mounts[i].System = true
		if a.Mounts[i].Name != systemMetaProjectName && !nameConflict {
			a.Mounts[i].Name = systemMetaProjectName
		}
		finalName := a.Mounts[i].Name
		finalPath := a.Mounts[i].Path
		a.mountMu.Unlock()
		if nameConflict {
			ctx.Logger().Error("workspace: cannot rename promoted system meta project; name already used",
				"from", oldName, "to", systemMetaProjectName)
		}
		ctx.Logger().Info("workspace: promoted existing mount to system meta project",
			"from", oldName, "name", finalName, "path", finalPath)
		a.saveOrLog(ctx)
		a.emitMounts(ctx)
		return
	}

	// 3) Create a fresh system meta project.
	if err := os.MkdirAll(sysPath, 0o755); err != nil {
		ctx.Logger().Error("workspace: create system meta project dir failed",
			"path", sysPath, "error", err)
		return
	}

	if a.findMountByName(systemMetaProjectName, "") {
		ctx.Logger().Error("workspace: system meta project name already used by a user project",
			"name", systemMetaProjectName)
		return
	}

	ref := domain.ProjectRef{
		Name:   systemMetaProjectName,
		Path:   normalizedSysPath,
		Root:   true,
		System: true,
	}
	props := actor.PropsFromFunc(project.NewActor(sysPath, true)).WithPlanner()
	spawned, err := ctx.Spawn(props, ref.Name)
	if err != nil {
		ctx.Logger().Error("workspace: spawn system meta project failed",
			"path", sysPath, "error", err)
		return
	}
	if spawned != nil {
		ref.ActorID = spawned.ID().String()
	}

	a.mountMu.Lock()
	a.Mounts = append(a.Mounts, ref)
	a.mountMu.Unlock()
	a.saveOrLog(ctx)
	a.emitMounts(ctx)
	ctx.Logger().Info("workspace: ensured system meta project",
		"name", ref.Name, "path", sysPath, "actorId", ref.ActorID)
}

// findMountByName reports whether any mount (other than excludeActorID) uses name.
// Callers must not hold mountMu (takes the read lock itself).
func (a *Actor) findMountByName(name, excludeActorID string) bool {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if m.Name == name && (excludeActorID == "" || m.ActorID != excludeActorID) {
			return true
		}
	}
	return false
}

// findMountByActorID returns the mount with the given actor ID.
// Callers must not hold mountMu (takes the read lock itself).
func (a *Actor) findMountByActorID(actorID string) (domain.ProjectRef, bool) {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if m.ActorID == actorID {
			return m, true
		}
	}
	return domain.ProjectRef{}, false
}

func (a *Actor) OnStop(ctx actor.Context) error {
	return a.Save()
}

// agentRegistryName returns the persist record name for the ground-truth agent
// registry card. Agents are stored separately from the main workspace state so
// agent-lifecycle callables can read/write the card independently. The card is
// the authoritative source; a.Agents is a derived in-memory cache.
func (a *Actor) agentRegistryName() string {
	return a.actorID + "/ragents"
}

// saveAgentRegistry persists the agent registry card (a.Agents) to the
// separate ragents persist record. Callers must NOT hold agentsMu during
// the call (persist I/O is serialized internally).
func (a *Actor) saveAgentRegistry() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("workspace"))
		if err != nil {
			return err
		}
	}
	if !a.registryCardLoaded.Load() {
		return fmt.Errorf("workspace: refusing to overwrite agent registry card: it could not be loaded at startup (corrupt or unreadable — a corrupt-* backup was kept); recover it manually before mutating agents")
	}
	a.agentsMu.RLock()
	agentsCopy := make([]domain.AgentRef, len(a.Agents))
	copy(agentsCopy, a.Agents)
	a.agentsMu.RUnlock()
	return a.store.Save(a.agentRegistryName(), agentsCopy)
}

// Save persists every ground-truth concern into its own card as ONE batch via
// persist.SaveAll: on backends with multi-document transactions (SQL) the
// fan-out is atomic, so a crash mid-flush cannot tear the cards (the pre-B5
// seven-sequential-Save shape could); backends without transactions degrade to
// sequential writes. Each callable still write-throughs its own concern's card
// via saveXCard; Save is the OnStop / coalesced-flush safety net.
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("workspace"))
		if err != nil {
			return err
		}
	}
	if !a.registryCardLoaded.Load() {
		return fmt.Errorf("workspace: refusing to overwrite agent registry card: it could not be loaded at startup (corrupt or unreadable — a corrupt-* backup was kept); recover it manually before mutating agents")
	}
	docs := []persist.Doc{
		{Name: a.agentRegistryName(), Value: a.agentRegistrySnapshot()},
		{Name: a.mountsCardName(), Value: a.mountsSnapshot()},
		{Name: a.kindsCardName(), Value: a.agentKindConfigsSnapshot()},
		{Name: a.prefsCardName(), Value: a.accountPrefs},
		{Name: a.uiCardName(), Value: a.UI},
		{Name: a.actorID + "/clones", Value: a.clonesSnapshot()},
		{Name: a.delRetryCardName(), Value: a.deletionRetrySnapshot()},
		{Name: a.subMapsCardName(), Value: a.subMapInstancesSnapshot()},
		{Name: a.eventWaitsCardName(), Value: a.eventWaitsSnapshot()},
		{Name: a.scatterCardName(), Value: a.scatterFanoutsSnapshot()},
	}
	if err := persist.SaveAll(a.store, docs); err != nil {
		return err
	}
	// Write-through pre-paint cache (derived data; see writeBootThemeCache).
	_ = a.writeBootThemeCache()
	return nil
}

// agentRegistrySnapshot copies the agent registry under the read lock so the
// batch SaveAll marshals a stable value without holding agentsMu across
// persist I/O.
func (a *Actor) agentRegistrySnapshot() []domain.AgentRef {
	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	out := make([]domain.AgentRef, len(a.Agents))
	copy(out, a.Agents)
	return out
}

// clonesSnapshot copies the clone states under clonePersistMu so the batch
// SaveAll marshals a stable value without holding the lock across persist I/O.
func (a *Actor) clonesSnapshot() map[string]domain.CloneState {
	a.clonePersistMu.Lock()
	defer a.clonePersistMu.Unlock()
	out := make(map[string]domain.CloneState, len(a.clones))
	for actorID, state := range a.clones {
		out[actorID] = state
	}
	return out
}

func (a *Actor) saveCloneState() error {
	a.clonePersistMu.Lock()
	defer a.clonePersistMu.Unlock()
	clones := make(map[string]domain.CloneState, len(a.clones))
	for actorID, state := range a.clones {
		clones[actorID] = state
	}
	return a.store.Save(a.actorID+"/clones", clones)
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("workspace"))
		if err != nil {
			return err
		}
	}
	// Gate every registry write until the card is read (or authoritatively
	// absent). Any early return below leaves it false, so a partial/failed
	// load cannot be followed by an empty-cache overwrite of the card.
	a.registryCardLoaded.Store(false)

	// ONE-TIME MIGRATION — remove after verification.
	// Migrate legacy flat-file card names (actorID + ".rmounts" etc.) to
	// sub-path names (actorID + "/rmounts" etc.) so cascade Delete reclaims
	// all subordinate documents.
	migrateCardIfNeeded(a.store, a.actorID+".rmounts", a.mountsCardName())
	migrateCardIfNeeded(a.store, a.actorID+".rkinds", a.kindsCardName())
	migrateCardIfNeeded(a.store, a.actorID+".rprefs", a.prefsCardName())
	migrateCardIfNeeded(a.store, a.actorID+".rui", a.uiCardName())
	migrateCardIfNeeded(a.store, a.actorID+".rdel", a.delRetryCardName())
	migrateCardIfNeeded(a.store, a.actorID+".rsubmaps", a.subMapsCardName())
	migrateCardIfNeeded(a.store, a.actorID+".reventwaits", a.eventWaitsCardName())
	migrateCardIfNeeded(a.store, a.actorID+".clones", a.actorID+"/clones")
	migrateCardIfNeeded(a.store, a.actorID+".ragents", a.agentRegistryName())

	// Mounts card
	{
		var mounts []domain.ProjectRef
		a.mountMu.Lock()
		if err := a.store.Load(a.mountsCardName(), &mounts); err == nil {
			a.Mounts = mounts
		} else if !errors.Is(err, persist.ErrNotExist) {
			a.mountMu.Unlock()
			return err
		}
		a.mountMu.Unlock()
	}

	// Agent kind configs card
	{
		var kinds []domain.AgentKindConfig
		a.kindConfigsMu.Lock()
		if err := a.store.Load(a.kindsCardName(), &kinds); err == nil {
			a.AgentKindConfigs = kinds
		} else if !errors.Is(err, persist.ErrNotExist) {
			a.kindConfigsMu.Unlock()
			return err
		}
		a.kindConfigsMu.Unlock()
	}

	// Account preferences card
	{
		var prefs domain.AccountPreferencesSnapshot
		if err := a.store.Load(a.prefsCardName(), &prefs); err == nil {
			a.accountPrefs = prefs
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}
	// One-shot git-bash adoption: fires only when the previous run recorded
	// git-bash as absent, it is present now, and no explicit shell preference
	// exists. Refresh the advertised agent environment when it fires.
	if a.reconcileShellAdoption() {
		a.mu.Lock()
		a.refreshShellEnvironmentLocked()
		a.mu.Unlock()
	}
	a.applyShellPreference()

	// UI card
	{
		var ui domain.WorkspaceUIModel
		if err := a.store.Load(a.uiCardName(), &ui); err == nil {
			a.UI = ui
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}
	if a.UI.WorkspaceID == "" {
		a.UI = domain.WorkspaceUIModel{
			WorkspaceID:   "default",
			Version:       1,
			SchemaVersion: 1,
		}
	}

	// Deletion retry card
	{
		var state deletionRetryState
		if err := a.store.Load(a.delRetryCardName(), &state); err == nil {
			a.deletionMu.Lock()
			a.deletionErrors = state.Errors
			a.deletionAttempts = state.Attempts
			a.deletionMu.Unlock()
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}

	// Sub-map instances card
	{
		var instances []subMapInstance
		if err := a.store.Load(a.subMapsCardName(), &instances); err == nil {
			a.subMapInstances = instances
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}

	// Event-wait activations card (only active waits are persisted; they are
	// re-armed in OnStart — see rearmEventWaits)
	{
		var records []eventWaitRecord
		if err := a.store.Load(a.eventWaitsCardName(), &records); err == nil {
			a.loadEventWaitRecords(records)
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}

	// Scatter fan-outs card
	{
		var fanouts []scatterFanout
		if err := a.store.Load(a.scatterCardName(), &fanouts); err == nil {
			a.scatterFanouts = fanouts
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}

	// Clones card
	{
		var clones map[string]domain.CloneState
		if err := a.store.Load(a.actorID+"/clones", &clones); err == nil {
			a.clones = clones
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		}
	}

	// Agents registry card
	{
		var cardAgents []domain.AgentRef
		if err := a.store.Load(a.agentRegistryName(), &cardAgents); err == nil {
			a.Agents = cardAgents
			a.inactiveActorIDs = nil
			a.registryCardLoaded.Store(true)
		} else if !errors.Is(err, persist.ErrNotExist) {
			return err
		} else {
			a.registryCardLoaded.Store(true)
		}
	}

	// Refresh the pre-paint boot cache from the loaded preferences so the
	// next window creation needs no actor-tree wait.
	_ = a.writeBootThemeCache()
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstPositive(values ...int32) int32 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func (a *Actor) saveOrLog(ctx actor.PureContext) {
	if err := a.Save(); err != nil {
		ctx.Logger().Error("workspace: save state failed", "error", err)
	}
}

// stateFlushDelay bounds how long high-frequency status-update mutations
// (LastActivity, Status, Mode, Title) sit in memory before a coalesced
// persist write. On graceful shutdown OnStop→Save flushes the in-memory
// state, so the debounce only matters for a crash. A persisted paused status
// remains visible while its agent is lazy-unloaded and is validated once the
// agent reloads.
const stateFlushDelay = 2 * time.Second

// stateFlushReq/stateFlushResp are the named empty types for the
// workspace.internal_flush_state callable (the framework rejects anonymous
// struct parameters).
type stateFlushReq struct{}
type stateFlushResp struct{}

// scheduleStateFlush coalesces a burst of status-update mutations into one
// delayed Save instead of a full persist write per push. OwnerLoop-only
// caller today, but PureContext-compatible (After is thread-safe).
func (a *Actor) scheduleStateFlush(ctx actor.PureContext) {
	if !a.stateFlushPending.CompareAndSwap(false, true) {
		return
	}
	if err := ctx.After(stateFlushDelay, "workspace.internal_flush_state", stateFlushReq{}); err != nil {
		a.stateFlushPending.Store(false)
		a.saveOrLog(ctx)
	}
}

// handleStateFlush is a stateless handler: the pending flag is atomic and
// Save copies each card's state under its own lock, so a concurrent flush
// never races owner-loop mutations.
func (a *Actor) handleStateFlush(ctx actor.PureContext, _ stateFlushReq) (stateFlushResp, error) {
	a.stateFlushPending.Store(false)
	a.saveOrLog(ctx)
	return stateFlushResp{}, nil
}

// mountsSnapshot returns a copy of Mounts with system meta projects first so
// list/event consumers always see the workspace project pinned to the top.
func (a *Actor) mountsSnapshot() []domain.ProjectRef {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	snap := make([]domain.ProjectRef, 0, len(a.Mounts))
	for _, m := range a.Mounts {
		if m.System {
			snap = append(snap, m)
		}
	}
	for _, m := range a.Mounts {
		if !m.System {
			snap = append(snap, m)
		}
	}
	return snap
}

// emitMounts pushes the current mounts snapshot to subscribers so the AI
// shell sidebar, explorer dropdown, and other panels stay in sync without
// re-polling workspace.list_project. PureContext: EmitEvent is thread-safe.
func (a *Actor) emitMounts(ctx actor.PureContext) {
	if err := ctx.EmitEvent("mounts", domain.WorkspaceMountsEvent{Mounts: a.mountsSnapshot()}); err != nil {
		ctx.Logger().Error("workspace: emit mounts failed", "error", err)
	}
}

func (a *Actor) emitAgentsChanged(ctx actor.PureContext) {
	if err := ctx.EmitEvent("agents_changed", domain.WorkspaceAgentsChangedEvent{}); err != nil {
		ctx.Logger().Error("workspace: emit agents_changed failed", "error", err)
	}
	a.emitAgentListState(ctx, true)
}

// pushRootsToFilesystem syncs the current mount paths to the filesystem actor
// so it can enforce its path sandbox, and to each project actor so it can
// resolve absolute paths against all configured roots. PureContext: only
// lock-guarded snapshots and internally-synchronized invokes are used.
func (a *Actor) pushRootsToFilesystem(ctx actor.PureContext) {
	// Snapshot Mounts under the read lock: this function performs cross-actor
	// invokes below, and mountMu must never be held across them.
	a.mountMu.RLock()
	mounts := make([]domain.ProjectRef, len(a.Mounts))
	copy(mounts, a.Mounts)
	a.mountMu.RUnlock()
	var rootPaths []string
	var rootEntries []domain.ProjectInfoRoot
	for _, m := range mounts {
		if m.Path != "" {
			rootPaths = append(rootPaths, filepath.Clean(m.Path))
			rootEntries = append(rootEntries, domain.ProjectInfoRoot{Name: m.Name, Path: filepath.Clean(m.Path)})
		}
		for _, sub := range m.Mounts {
			if sub.Path != "" {
				rootPaths = append(rootPaths, filepath.Clean(sub.Path))
				rootEntries = append(rootEntries, domain.ProjectInfoRoot{Name: sub.Name, Path: filepath.Clean(sub.Path)})
			}
		}
	}
	if fsRef, ok := ctx.LookupService("filesystem"); ok {
		call := fsRef.Invoke(ctx.Lifecycle(), "filesystem.sync_roots", domain.FileSystemSyncRootsReq{Roots: rootPaths})
		_ = call.Close()
	}
	for _, m := range mounts {
		if m.ActorID == "" {
			continue
		}
		cid, err := identity.ParseCanonicalID(m.ActorID)
		if err != nil {
			continue
		}
		projRef, ok := ctx.LookupID(id.From(cid))
		if !ok {
			continue
		}
		var projRoots []domain.ProjectInfoRoot
		if m.Path != "" {
			projRoots = append(projRoots, domain.ProjectInfoRoot{Name: m.Name, Path: filepath.Clean(m.Path)})
		}
		for _, sub := range m.Mounts {
			if sub.Path != "" {
				projRoots = append(projRoots, domain.ProjectInfoRoot{Name: sub.Name, Path: filepath.Clean(sub.Path)})
			}
		}
		call := projRef.Invoke(ctx.Lifecycle(), "project.sync_roots", domain.ProjectSyncRootsReq{Roots: projRoots})
		_ = call.Close()
	}
}

// handleMount is a stateless handler. The duplicate pre-scan (mountMu.RLock)
// fails fast before the expensive project-actor Spawn; the authoritative
// check-then-insert then runs as a single mountMu critical section so two
// concurrent mounts of the same path/name cannot both append. The Spawn runs
// outside the lock (mountMu must never be held across cross-actor calls); a
// late conflict destroys the freshly spawned actor before returning.
func (a *Actor) handleMount(ctx actor.PureContext, req domain.WorkspaceMountReq) (domain.ProjectRef, error) {
	ctx.Logger().Info("workspace.mount: start", "path", req.Path, "name", req.Name)
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		ctx.Logger().Warn("workspace.mount: policy denied", "role", ctx.Identity().Role, "error", err)
		return domain.ProjectRef{}, err
	}
	normPath := util.NormalizePath(req.Path)
	sysPath := util.NormalizePath(systemMetaProjectPath())
	if normPath == sysPath {
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: path %q is reserved for the system meta project", normPath)
	}
	// Allow mounting subdirectories under the system meta project's app-tier
	// directories (app/, dev-app/, sporeapp/), but block mounting the
	// system meta project root itself or other system-level subdirectories.
	if util.IsSubPath(normPath, sysPath) {
		if !isAppTierSubPath(normPath, sysPath) {
			return domain.ProjectRef{}, fmt.Errorf("workspace.mount: path %q is under the system meta project but not an app-tier directory", normPath)
		}
	}
	// Auto-infer AppKind from path if not explicitly set.
	appKind := req.AppKind
	if appKind == "" {
		appKind = inferAppKind(normPath, sysPath)
	}
	if normPath == "" {
		ctx.Logger().Warn("workspace.mount: empty path")
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: path is empty")
	}
	if err := validateNameSegment("workspace.mount", req.Name); err != nil {
		ctx.Logger().Warn("workspace.mount: invalid name", "error", err)
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: %w", err)
	}
	// Pre-scan for duplicates under mountMu.RLock; the append below takes the
	// write lock. First conflicting mount wins, mirroring the original
	// check order (path before name per mount).
	conflict := ""
	a.mountMu.RLock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == normPath {
			conflict = "path"
			break
		}
		if m.Name == req.Name {
			conflict = "name"
			break
		}
	}
	a.mountMu.RUnlock()
	switch conflict {
	case "path":
		ctx.Logger().Warn("workspace.mount: already mounted", "path", normPath)
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: already mounted at %q", normPath)
	case "name":
		ctx.Logger().Warn("workspace.mount: name already used", "name", req.Name)
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: project name %q already exists", req.Name)
	}

	ref := domain.ProjectRef{
		Name:    req.Name,
		Path:    normPath,
		Root:    true,
		AppKind: appKind,
	}

	ctx.Logger().Info("workspace.mount: spawning project actor", "name", ref.Name, "path", normPath)
	props := actor.PropsFromFunc(project.NewActor(normPath, false)).WithPlanner()
	spawned, err := ctx.Spawn(props, ref.Name)
	if err != nil {
		ctx.Logger().Error("workspace.mount: spawn failed", "error", err)
		return domain.ProjectRef{}, fmt.Errorf("workspace.mount: spawn project: %w", err)
	}
	if spawned != nil {
		ref.ActorID = spawned.ID().String()
	}
	ctx.Logger().Info("workspace.mount: spawn ok", "name", ref.Name, "actorId", ref.ActorID)

	// Authoritative check-then-insert: the pre-scan above can race a
	// concurrent mount of the same path/name (the Spawn in between takes
	// seconds), so re-check inside the same mountMu critical section that
	// appends. The loser destroys its freshly spawned project actor.
	a.mountMu.Lock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == normPath {
			a.mountMu.Unlock()
			if spawned != nil {
				if derr := ctx.Destroy(spawned); derr != nil {
					ctx.Logger().Warn("workspace.mount: cleanup of lost race failed", "name", ref.Name, "error", derr)
				}
			}
			ctx.Logger().Warn("workspace.mount: already mounted (lost race)", "path", normPath)
			return domain.ProjectRef{}, fmt.Errorf("workspace.mount: already mounted at %q", normPath)
		}
		if m.Name == req.Name {
			a.mountMu.Unlock()
			if spawned != nil {
				if derr := ctx.Destroy(spawned); derr != nil {
					ctx.Logger().Warn("workspace.mount: cleanup of lost race failed", "name", ref.Name, "error", derr)
				}
			}
			ctx.Logger().Warn("workspace.mount: name already used (lost race)", "name", req.Name)
			return domain.ProjectRef{}, fmt.Errorf("workspace.mount: project name %q already exists", req.Name)
		}
	}
	a.Mounts = append(a.Mounts, ref)
	a.mountMu.Unlock()
	a.saveMountsOrLog(ctx)
	a.emitMounts(ctx)
	a.pushRootsToFilesystem(ctx)

	ctx.Logger().Info("workspace.mount: done", "name", ref.Name, "mounts", len(a.Mounts))
	return ref, nil
}

// handleUnmount is a stateless handler: the find-and-remove runs as a single
// mountMu critical section (same shape as add_mount/remove_mount), and every
// follow-up (stop agents, persist, emit, project Destroy) happens outside the
// lock using thread-safe PureContext operations.
func (a *Actor) handleUnmount(ctx actor.PureContext, req domain.WorkspaceUnmountReq) (domain.ProjectRef, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.ProjectRef{}, err
	}
	a.mountMu.Lock()
	idx := -1
	for i, m := range a.Mounts {
		if m.ActorID == req.ProjectID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mountMu.Unlock()
		return domain.ProjectRef{}, fmt.Errorf("workspace.unmount: project %q not found", req.ProjectID)
	}
	m := a.Mounts[idx]
	if m.System {
		a.mountMu.Unlock()
		return domain.ProjectRef{}, fmt.Errorf("workspace.unmount: cannot unmount system meta project %q", m.Name)
	}
	a.Mounts = append(a.Mounts[:idx], a.Mounts[idx+1:]...)
	a.mountMu.Unlock()
	a.stopAgentsForProject(ctx, m.ActorID)
	a.emitAgentsChanged(ctx)
	a.saveMountsOrLog(ctx)
	a.emitMounts(ctx)
	a.pushRootsToFilesystem(ctx)
	// Stop the spawned project actor
	if m.ActorID != "" {
		if cid, cerr := identity.ParseCanonicalID(m.ActorID); cerr == nil {
			if ref, ok := ctx.LookupID(id.From(cid)); ok {
				_ = ctx.Destroy(ref)
			}
		}
	}
	return m, nil
}

func (a *Actor) handleList(_ actor.PureContext) (domain.ProjectRefListResp, error) {
	return domain.ProjectRefListResp{Items: a.mountsSnapshot()}, nil
}

// SystemTreeNode is a virtual child of the system workspace project. It
// represents installed plugins, dev-plugin repos, or individual dev plugins.
type SystemTreeNode struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Kind     string            `json:"Kind"` // "app-dir" | "dev-app-dir" | "sporeapp-dir" | "dev-app" | "sporeapp" | "installed-plugin"
	Path     string            `json:"Path,omitempty"`
	Status   string            `json:"Status,omitempty"`
	Children []SystemTreeNode  `json:"Children,omitempty"`
	Extra    map[string]string `json:"Extra,omitempty"`
}

// SystemTreeResp wraps the system tree nodes for the callable return type.
type SystemTreeResp struct {
	Nodes []SystemTreeNode `json:"Nodes"`
}

// handleSystemTree returns the virtual child tree for the system workspace
// project, organized by App distribution tier:
//
//	app/      — compiled/builtin apps (immutable)
//	dev-app/  — code + compiled output (editable, recompilable)
//	sporeapp/ — pure Spore scripts (editable)
//
// The legacy "plugins" / "dev-plugins" nodes are folded into this structure:
// installed native plugins appear under app/, dev-plugin repos under dev-app/.
//
// PureContext: the handler only does a pluginhost RPC and reads the mount
// snapshot (under a read-lock); it never mutates workspace state, so it must
// not occupy the ownerLoop. Responses are cached briefly to absorb UI polling.
func (a *Actor) handleSystemTree(ctx actor.PureContext) (SystemTreeResp, error) {
	if v := a.systemTreeCache.Load(); v != nil {
		if e := v.(systemTreeCacheEntry); time.Since(e.at) < systemTreeCacheTTL {
			return e.resp, nil
		}
	}
	resp := a.buildSystemTree(ctx)
	// Concurrent pure handlers may both rebuild and store; last store wins,
	// which is fine for a read-mostly projection.
	a.systemTreeCache.Store(systemTreeCacheEntry{at: time.Now(), resp: resp})
	return resp, nil
}

// systemTreeCacheTTL bounds how stale a cached system tree may be. App
// installs/scans change on human timescales, so a few seconds is invisible.
const systemTreeCacheTTL = 5 * time.Second

type systemTreeCacheEntry struct {
	at   time.Time
	resp SystemTreeResp
}

func (a *Actor) buildSystemTree(ctx actor.PureContext) SystemTreeResp {
	var nodes []SystemTreeNode

	// app/: installed plugins (from pluginhost).
	appChildren := a.queryInstalledPlugins(ctx)
	if len(appChildren) > 0 {
		nodes = append(nodes, SystemTreeNode{
			ID:       appDirName,
			Name:     appDirName,
			Kind:     "app-dir",
			Children: appChildren,
		})
	}

	// dev-app/: code + compiled output repos.
	mounts := a.mountsSnapshot()
	if devApps := scanDevApps(mounts); len(devApps) > 0 {
		nodes = append(nodes, SystemTreeNode{
			ID:       devAppDirName,
			Name:     devAppDirName,
			Kind:     "dev-app-dir",
			Children: devApps,
		})
	}

	// sporeapp/: pure Spore script apps.
	if sporeApps := scanSporeApps(mounts); len(sporeApps) > 0 {
		nodes = append(nodes, SystemTreeNode{
			ID:       sporeAppDirName,
			Name:     sporeAppDirName,
			Kind:     "sporeapp-dir",
			Children: sporeApps,
		})
	}

	return SystemTreeResp{Nodes: nodes}
}

// maxLogsQueryLimit caps the number of entries returned in a single
// logs_query response so one call cannot blow up the LLM tool-result
// token budget. When more entries exist the response carries Truncated
// and NextBefore so the caller can page backwards in time.
const maxLogsQueryLimit = 200

// handleLogsQuery returns recent system logs. By default it reads the backend
// Go log ring (gospore/stdlib logs). When Source=="console" it instead reads
// persisted frontend console logs. It supports level/caller filtering and
// message substring search. When Tail is set, it reads from the tail of the
// persisted log store; otherwise it returns the latest matching entries.
//
// The response is capped at maxLogsQueryLimit entries. When truncation
// occurs, Truncated is set and NextBefore holds the timestamp of the oldest
// returned entry — pass it as Before in a follow-up call to fetch the next
// older segment.
func (a *Actor) handleLogsQuery(ctx actor.PureContext, req gen.WorkspaceLogsQueryReq) (gen.WorkspaceLogsQueryResp, error) {
	limit := effectiveLogsLimit(req)

	// Fetch one extra entry so we can detect whether more entries exist
	// after filtering.
	fetchN := limit + 1

	var entries []gateway.LogEntry
	if strings.EqualFold(req.Source, "console") {
		src, ok := resource.Get[logging.ConsoleLogSource](ctx.Resources(), logging.ConsoleLogSourceKey)
		if !ok || src == nil {
			return gen.WorkspaceLogsQueryResp{}, fmt.Errorf("workspace.logs_query: console log source not available")
		}
		var consoleEntries []logging.ConsoleEntry
		var err error
		if req.Before != "" {
			consoleEntries, err = src.QueryBefore(req.Before, fetchN)
		} else {
			consoleEntries, err = src.Tail(fetchN)
		}
		if err != nil {
			return gen.WorkspaceLogsQueryResp{}, fmt.Errorf("workspace.logs_query: console query: %w", err)
		}
		entries = make([]gateway.LogEntry, 0, len(consoleEntries))
		for _, ce := range consoleEntries {
			entries = append(entries, gateway.LogEntry{
				Timestamp: consoleTimestamp(ce.Time),
				Level:     ce.Level,
				Caller:    ce.Location,
				Message:   ce.Message,
			})
		}
	} else {
		ring, ok := resource.Get[logging.LogRing](ctx.Resources(), logging.LogRingKey)
		if !ok || ring == nil {
			return gen.WorkspaceLogsQueryResp{}, fmt.Errorf("workspace.logs_query: log ring not available")
		}
		if req.Tail > 0 {
			var err error
			entries, err = ring.Tail(fetchN)
			if err != nil {
				return gen.WorkspaceLogsQueryResp{}, fmt.Errorf("workspace.logs_query: tail: %w", err)
			}
			entries = mergePersistedBackendLogs(ctx, entries, "", fetchN)
		} else if req.Before != "" {
			var err error
			entries, err = ring.QueryBefore(req.Before, fetchN)
			if err != nil {
				return gen.WorkspaceLogsQueryResp{}, fmt.Errorf("workspace.logs_query: query before: %w", err)
			}
			if len(entries) < fetchN {
				// Ring window exhausted: continue paging in the persisted
				// store so older entries (e.g. pre-restart freeze evidence)
				// remain reachable.
				entries = mergePersistedBackendLogs(ctx, entries, req.Before, fetchN)
			}
		} else {
			entries = ring.QueryLogs(gateway.LogQuery{
				Level:  req.Level,
				Caller: req.Caller,
				Limit:  fetchN,
			})
			if len(entries) < fetchN {
				entries = mergePersistedBackendLogs(ctx, entries, "", fetchN)
			}
		}
	}

	items := filterLogEntries(entries, req)

	resp := gen.WorkspaceLogsQueryResp{Items: items}
	if len(items) > limit {
		// Keep the newest `limit` items; set cursor to the oldest kept entry.
		resp.Items = items[len(items)-limit:]
		resp.Truncated = true
		resp.NextBefore = resp.Items[0].Timestamp
	}
	return resp, nil
}

// mergePersistedBackendLogs augments in-memory ring results with entries
// from the persisted backend log store (daily JSONL files) so queries can
// page beyond the ring window — restart-safe freeze forensics. `before` is
// the caller's Before cursor ("" for tail-style reads). Results are deduped
// on (timestamp, message) because the ring is restored from the store's tail
// at startup, and the newest `want` entries are kept in chronological order.
func mergePersistedBackendLogs(ctx actor.PureContext, ringEntries []gateway.LogEntry, before string, want int) []gateway.LogEntry {
	store, ok := resource.Get[logging.BackendLogSource](ctx.Resources(), logging.BackendLogSourceKey)
	if !ok || store == nil || want <= 0 {
		return ringEntries
	}
	var fromStore []gateway.LogEntry
	var err error
	if before != "" {
		fromStore, err = store.QueryBefore(before, want)
	} else {
		fromStore, err = store.Tail(want)
	}
	if err != nil || len(fromStore) == 0 {
		return ringEntries
	}
	combined := make([]gateway.LogEntry, 0, len(fromStore)+len(ringEntries))
	seen := make(map[string]struct{}, len(fromStore)+len(ringEntries))
	for _, e := range append(fromStore, ringEntries...) {
		key := e.Timestamp + "|" + e.Message
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		combined = append(combined, e)
	}
	sort.SliceStable(combined, func(i, j int) bool {
		return combined[i].Timestamp < combined[j].Timestamp
	})
	if len(combined) > want {
		combined = combined[len(combined)-want:]
	}
	return combined
}

// effectiveLogsLimit resolves the entry cap for a logs_query request. Tail
// takes precedence over Limit. The result is clamped to (0, maxLogsQueryLimit].
func effectiveLogsLimit(req gen.WorkspaceLogsQueryReq) int {
	limit := int(req.Limit)
	if req.Tail > 0 {
		limit = int(req.Tail)
	}
	if limit <= 0 || limit > maxLogsQueryLimit {
		limit = maxLogsQueryLimit
	}
	return limit
}

// filterLogEntries applies Level/Caller/Message/Before filtering in memory.
// For the backend ring path the level/caller filters are redundant (the ring
// already applied them) but harmless; for the console path they are essential.
func filterLogEntries(entries []gateway.LogEntry, req gen.WorkspaceLogsQueryReq) []gen.WorkspaceLogEntry {
	message := strings.ToLower(req.Message)
	caller := strings.ToLower(req.Caller)
	level := strings.ToLower(req.Level)
	items := make([]gen.WorkspaceLogEntry, 0, len(entries))
	for _, e := range entries {
		if req.Before != "" && e.Timestamp >= req.Before {
			continue
		}
		if level != "" && !strings.EqualFold(e.Level, req.Level) {
			continue
		}
		if caller != "" && !strings.Contains(strings.ToLower(e.Caller), caller) {
			continue
		}
		if message != "" && !strings.Contains(strings.ToLower(e.Message), message) {
			continue
		}
		callerFile, callerLine := "", int64(0)
		if e.Caller != "" {
			callerFile, callerLine = splitLogCaller(e.Caller)
		}
		items = append(items, gen.WorkspaceLogEntry{
			Timestamp:  e.Timestamp,
			Level:      e.Level,
			Caller:     e.Caller,
			Message:    e.Message,
			Fields:     e.Fields,
			CallerFile: callerFile,
			CallerLine: callerLine,
		})
	}
	return items
}

// splitLogCaller splits a combined "file:line" string into its components.
// When the caller has no colon (or the line portion is not a number), the
// whole string is returned as file with line=0.
func splitLogCaller(caller string) (file string, line int64) {
	if caller == "" {
		return "", 0
	}
	if i := strings.LastIndexByte(caller, ':'); i >= 0 {
		file = caller[:i]
		if n, err := strconv.ParseInt(caller[i+1:], 10, 64); err == nil {
			line = n
		}
	} else {
		file = caller
	}
	return
}

// consoleTimestamp converts a frontend console entry millisecond epoch to an
// RFC3339Nano string so console entries share the same Timestamp shape as
// backend log entries.
func consoleTimestamp(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

func (a *Actor) queryInstalledPlugins(ctx actor.PureContext) []SystemTreeNode {
	sysPath := systemMetaProjectPath()
	pluginRef, ok := ctx.LookupService("pluginhost")
	if !ok || pluginRef == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := pluginRef.Invoke(callCtx, "pluginhost.list_plugins", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, _ := call.Final(callCtx)
	if v == nil {
		return nil
	}
	var descs []pluginhost.PluginDescriptor
	switch d := v.(type) {
	case []pluginhost.PluginDescriptor:
		descs = d
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &descs)
	}
	nodes := make([]SystemTreeNode, 0, len(descs))
	for _, d := range descs {
		nodes = append(nodes, SystemTreeNode{
			ID:     "plugin:" + d.ID,
			Name:   d.Name,
			Kind:   "installed-plugin",
			Status: d.Status,
			Extra: map[string]string{
				"version": d.Version,
				"path":    util.NormalizePath(sysPath),
			},
		})
	}
	return nodes
}

// scanDevApps returns one SystemTreeNode per mount whose AppKind is "dev-app".
// This replaces the fixed-directory scan: apps created at any path now appear
// in the system tree because they are listed in the workspace's Mounts.
func scanDevApps(mounts []domain.ProjectRef) []SystemTreeNode {
	var nodes []SystemTreeNode
	for _, m := range mounts {
		if m.AppKind != devAppDirName {
			continue
		}
		nodes = append(nodes, SystemTreeNode{
			ID:   "dev-app:" + m.Name,
			Name: m.Name,
			Kind: "dev-app",
			Path: m.Path,
		})
	}
	return nodes
}

// scanSporeApps returns one SystemTreeNode per mount whose AppKind is
// "sporeapp". This replaces the fixed-directory scan: Spore script apps
// created at any path now appear in the system tree because they are listed
// in the workspace's Mounts.
func scanSporeApps(mounts []domain.ProjectRef) []SystemTreeNode {
	var nodes []SystemTreeNode
	for _, m := range mounts {
		if m.AppKind != sporeAppDirName {
			continue
		}
		nodes = append(nodes, SystemTreeNode{
			ID:   "sporeapp:" + m.Name,
			Name: m.Name,
			Kind: "sporeapp",
			Path: m.Path,
		})
	}
	return nodes
}

// handleUpdateProject is a stateless handler. Find, validate, and mutate run
// in one mountMu critical section (no cross-actor invokes inside); persist,
// event emission, and root sync happen after the lock is released.
func (a *Actor) handleUpdateProject(ctx actor.PureContext, req domain.WorkspaceUpdateProjectReq) (domain.WorkspaceUpdateProjectResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.WorkspaceUpdateProjectResp{}, err
	}
	a.mountMu.Lock()
	idx := -1
	for i := range a.Mounts {
		if a.Mounts[i].ActorID == req.ProjectID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mountMu.Unlock()
		return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: project %q not found", req.ProjectID)
	}
	m := a.Mounts[idx]
	if m.System {
		if req.Name != "" && req.Name != m.Name {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: cannot rename system meta project %q", m.Name)
		}
		if req.PermissionMode != "" {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: cannot change permission mode of system meta project %q", m.Name)
		}
		if req.ClearPermissionMode {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: cannot clear permission mode of system meta project %q", m.Name)
		}
		if req.AppKind != "" && req.AppKind != m.AppKind {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: cannot change app kind of system meta project %q", m.Name)
		}
		if req.ClearAppKind && m.AppKind != "" {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: cannot clear app kind of system meta project %q", m.Name)
		}
	}
	if req.Name != "" && req.Name != m.Name {
		if err := validateNameSegment("workspace.update_project", req.Name); err != nil {
			a.mountMu.Unlock()
			return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: %w", err)
		}
		for _, other := range a.Mounts {
			if other.ActorID != req.ProjectID && other.Name == req.Name {
				a.mountMu.Unlock()
				return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: project name %q already exists", req.Name)
			}
		}
	}
	if req.AppKind != "" && req.AppKind != devAppDirName && req.AppKind != sporeAppDirName {
		a.mountMu.Unlock()
		return domain.WorkspaceUpdateProjectResp{}, fmt.Errorf("workspace.update_project: invalid app kind %q (want %q or %q)", req.AppKind, devAppDirName, sporeAppDirName)
	}
	if req.Name != "" && req.Name != m.Name {
		a.Mounts[idx].Name = req.Name
	}
	if req.LastOpenedAt != "" {
		a.Mounts[idx].LastOpenedAt = req.LastOpenedAt
	}
	if req.PermissionMode != "" {
		a.Mounts[idx].PermissionMode = req.PermissionMode
	}
	if req.ClearPermissionMode {
		a.Mounts[idx].PermissionMode = ""
	}
	if req.AppKind != "" {
		a.Mounts[idx].AppKind = req.AppKind
	}
	if req.ClearAppKind {
		a.Mounts[idx].AppKind = ""
	}
	updated := a.Mounts[idx]
	a.mountMu.Unlock()
	a.saveMountsOrLog(ctx)
	a.emitMounts(ctx)
	a.pushRootsToFilesystem(ctx)
	return domain.WorkspaceUpdateProjectResp{Project: updated}, nil
}

func (a *Actor) handleReportError(ctx actor.PureContext, req domain.FrontendErrorReport) error {
	ctx.Logger().Info("frontend error report",
		"severity", req.Severity,
		"component", req.SourceComponent,
		"message", req.Message,
		"session", req.SessionID,
	)
	return nil
}

// handleCreate is a stateless handler, same shape as handleMount: a
// mountMu.RLock pre-scan fails fast, the directory scaffolding and project
// Spawn run outside the lock, and the authoritative check-then-insert runs as
// a single mountMu critical section. The loser of a concurrent create race
// destroys its freshly spawned project actor.
func (a *Actor) handleCreate(ctx actor.PureContext, req domain.WorkspaceCreateReq) (domain.ProjectRef, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return domain.ProjectRef{}, err
	}
	if err := validateNameSegment("workspace.create", req.Name); err != nil {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: %w", err)
	}
	if req.Name == "." || req.Name == ".." || strings.Contains(req.Name, "..") {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: invalid name %q", req.Name)
	}

	basePath := req.Path
	if basePath == "" && (req.AppKind == "dev-app" || req.AppKind == "sporeapp") {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: path is required for app projects (AppKind %q)", req.AppKind)
	}
	dir := util.NormalizePath(filepath.Join(basePath, req.Name))
	sysPath := util.NormalizePath(systemMetaProjectPath())
	if dir == sysPath {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: path %q is reserved for the system meta project", dir)
	}
	if util.IsSubPath(dir, sysPath) && !isAppTierSubPath(dir, sysPath) {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: path %q is under the system meta project but not an app-tier directory", dir)
	}
	// Pre-scan under mountMu.RLock; the append below takes the write lock.
	conflict := ""
	a.mountMu.RLock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == dir {
			conflict = "path"
			break
		}
		if m.Name == req.Name {
			conflict = "name"
			break
		}
	}
	a.mountMu.RUnlock()
	switch conflict {
	case "path":
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: already mounted at %q", dir)
	case "name":
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: project name %q already exists", req.Name)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: mkdir %q: %w", dir, err)
	}

	if req.InitGit {
		if _, err := git.PlainInit(dir, false); err != nil {
			ctx.Logger().Warn("workspace.create: git init failed", "path", dir, "error", err)
		}
	}

	// Determine AppKind: explicit > path inference.
	appKind := req.AppKind
	if appKind == "" {
		appKind = inferAppKind(dir, sysPath)
	}

	// App projects receive a minimal editable scaffold before their project
	// actor is spawned. Ordinary projects remain untouched.
	if appKind == devAppDirName || appKind == sporeAppDirName {
		if _, err := scaffoldApp(dir, req.Name, appKind); err != nil {
			return domain.ProjectRef{}, fmt.Errorf("workspace.create: scaffold app: %w", err)
		}
	}

	ref := domain.ProjectRef{
		Name:    req.Name,
		Path:    dir,
		Root:    true,
		AppKind: appKind,
	}

	props := actor.PropsFromFunc(project.NewActor(dir, false)).WithPlanner()
	spawned, err := ctx.Spawn(props, ref.Name)
	if err != nil {
		return domain.ProjectRef{}, fmt.Errorf("workspace.create: spawn project: %w", err)
	}
	if spawned != nil {
		ref.ActorID = spawned.ID().String()
	}

	// Authoritative check-then-insert: re-check under the same mountMu
	// critical section that appends (the pre-scan above can race a
	// concurrent create/mount). The loser destroys its spawned project
	// actor; the scaffolded directory is left in place (it is inert).
	a.mountMu.Lock()
	for _, m := range a.Mounts {
		if util.NormalizePath(m.Path) == dir {
			a.mountMu.Unlock()
			if spawned != nil {
				if derr := ctx.Destroy(spawned); derr != nil {
					ctx.Logger().Warn("workspace.create: cleanup of lost race failed", "name", ref.Name, "error", derr)
				}
			}
			return domain.ProjectRef{}, fmt.Errorf("workspace.create: already mounted at %q", dir)
		}
		if m.Name == req.Name {
			a.mountMu.Unlock()
			if spawned != nil {
				if derr := ctx.Destroy(spawned); derr != nil {
					ctx.Logger().Warn("workspace.create: cleanup of lost race failed", "name", ref.Name, "error", derr)
				}
			}
			return domain.ProjectRef{}, fmt.Errorf("workspace.create: project name %q already exists", req.Name)
		}
	}
	a.Mounts = append(a.Mounts, ref)
	a.mountMu.Unlock()
	a.saveMountsOrLog(ctx)
	a.emitMounts(ctx)
	a.pushRootsToFilesystem(ctx)

	return ref, nil
}

// handleAddMount is a stateless handler. Find, duplicate check, and append
// run in one mountMu critical section so concurrent add_mount calls cannot
// both pass the check; persist/emit/sync run after the lock is released.
func (a *Actor) handleAddMount(ctx actor.PureContext, req domain.WorkspaceAddMountReq) (domain.ProjectRef, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.ProjectRef{}, err
	}
	a.mountMu.Lock()
	for i := range a.Mounts {
		if a.Mounts[i].ActorID != req.ProjectID {
			continue
		}
		for _, existing := range a.Mounts[i].Mounts {
			if existing.Name == req.MountName {
				a.mountMu.Unlock()
				return domain.ProjectRef{}, fmt.Errorf("workspace.add_mount: mount %q already exists", req.MountName)
			}
		}
		a.Mounts[i].Mounts = append(a.Mounts[i].Mounts, domain.ProjectMount{
			Name: req.MountName,
			Path: req.MountPath,
		})
		updated := a.Mounts[i]
		a.mountMu.Unlock()
		a.saveMountsOrLog(ctx)
		a.emitMounts(ctx)
		a.pushRootsToFilesystem(ctx)
		ctx.Logger().Info("workspace.add_mount", "project", req.ProjectID, "mount", req.MountName, "path", req.MountPath)
		return updated, nil
	}
	a.mountMu.Unlock()
	return domain.ProjectRef{}, fmt.Errorf("workspace.add_mount: project %q not found", req.ProjectID)
}

// handleRemoveMount is a stateless handler, with the same single-critical-
// section shape as handleAddMount.
func (a *Actor) handleRemoveMount(ctx actor.PureContext, req domain.WorkspaceRemoveMountReq) (domain.ProjectRef, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.ProjectRef{}, err
	}
	a.mountMu.Lock()
	for i := range a.Mounts {
		if a.Mounts[i].ActorID != req.ProjectID {
			continue
		}
		found := false
		for j, existing := range a.Mounts[i].Mounts {
			if existing.Name == req.MountName {
				a.Mounts[i].Mounts = append(a.Mounts[i].Mounts[:j], a.Mounts[i].Mounts[j+1:]...)
				found = true
				break
			}
		}
		if !found {
			a.mountMu.Unlock()
			return domain.ProjectRef{}, fmt.Errorf("workspace.remove_mount: mount %q not found", req.MountName)
		}
		updated := a.Mounts[i]
		a.mountMu.Unlock()
		a.saveMountsOrLog(ctx)
		a.emitMounts(ctx)
		a.pushRootsToFilesystem(ctx)
		ctx.Logger().Info("workspace.remove_mount", "project", req.ProjectID, "mount", req.MountName)
		return updated, nil
	}
	a.mountMu.Unlock()
	return domain.ProjectRef{}, fmt.Errorf("workspace.remove_mount: project %q not found", req.ProjectID)
}

// refreshAgentStatuses calls agent.status for every known agent and merges
// runtime fields (state, active turn, inferred title) back into a.Agents.
// Used during workspace startup so the initial agent_list_state snapshot is
// complete even if agents finish their own OnInit slightly later.
func (a *Actor) refreshAgentStatuses(ctx actor.Context) {
	agents := a.agentSnapshot()
	for i := range agents {
		ag := &agents[i]
		// Only query agents that are loaded (ActorID set and actor live in system).
		if ag.ActorID == "" || ag.LoadState != "loaded" {
			continue
		}
		cid, err := identity.ParseCanonicalID(ag.ActorID)
		if err != nil {
			continue
		}
		agentRef, ok := ctx.LookupID(id.From(cid))
		if !ok || agentRef == nil {
			continue
		}
		// Use a short timeout per agent so one blocked agent (e.g. its
		// default loop is busy with a slow turn start) does not stall the
		// workspace default loop for 15s × N agents. 2s is enough for
		// agent_status which is a fast in-memory snapshot.
		statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 2*time.Second)
		updates := domain.AgentRef{}
		hasUpdate := false
		var lastTurnCompletedAt string
		var activeWorkflowMapCardID string
		var activeWorkflowWorktreeID string
		if call := agentRef.Invoke(statusCtx, "agent_status", nil); call != nil {
			v, _ := call.Final(statusCtx)
			call.Close()
			if v != nil {
				var status gen.AgentStatusResp
				if s, ok := v.(gen.AgentStatusResp); ok {
					status = s
				} else {
					body, _ := json.Marshal(v)
					_ = json.Unmarshal(body, &status)
				}
				lastTurnCompletedAt = status.LastTurnCompletedAt
				activeWorkflowMapCardID = status.ActiveWorkflowMapCardID
				activeWorkflowWorktreeID = status.ActiveWorkflowWorktreeID
				if status.ActiveTurnRef != "" {
					updates.ActiveTurnRef = status.ActiveTurnRef
					hasUpdate = true
				}
				if status.State != "" {
					updates.Status = status.State
					hasUpdate = true
				}
				if status.Title != "" {
					updates.Title = status.Title
					hasUpdate = true
				}
				if status.LastActivity != "" {
					updates.LastActivity = status.LastActivity
					hasUpdate = true
				}
				// Merge model slots so child agents (whose slots are resolved
				// from the parent at spawn time) show the correct provider in
				// the composer instead of "Auto".
				if status.Primary != nil {
					updates.Primary = status.Primary
					hasUpdate = true
				}
				if status.Fast != nil {
					updates.Fast = status.Fast
					hasUpdate = true
				}
				if status.Execution != nil {
					updates.Execution = status.Execution
					hasUpdate = true
				}
				if status.Review != nil {
					updates.Review = status.Review
					hasUpdate = true
				}
				if status.Summary != nil {
					updates.Summary = status.Summary
					hasUpdate = true
				}
			}
		}
		cancel()
		if hasUpdate {
			a.agentsMu.Lock()
			if i < len(a.Agents) {
				if updates.ActiveTurnRef != "" {
					a.Agents[i].ActiveTurnRef = updates.ActiveTurnRef
				}
				if updates.Status != "" {
					a.Agents[i].Status = updates.Status
				}
				if updates.Title != "" {
					a.Agents[i].Title = updates.Title
				}
				if updates.LastActivity != "" {
					a.Agents[i].LastActivity = updates.LastActivity
				}
				if updates.Primary != nil {
					a.Agents[i].Primary = updates.Primary
				}
				if updates.Fast != nil {
					a.Agents[i].Fast = updates.Fast
				}
				if updates.Execution != nil {
					a.Agents[i].Execution = updates.Execution
				}
				if updates.Review != nil {
					a.Agents[i].Review = updates.Review
				}
				if updates.Summary != nil {
					a.Agents[i].Summary = updates.Summary
				}
				if a.agentRuntime == nil {
					a.agentRuntime = make(map[string]gen.AgentRuntimeState)
				}
				a.agentRuntime[a.Agents[i].ActorID] = gen.AgentRuntimeState{
					State:                    updates.Status,
					ActiveTurnRef:            updates.ActiveTurnRef,
					LastActivity:             updates.LastActivity,
					LastTurnCompletedAt:      lastTurnCompletedAt,
					ActiveWorkflowMapCardID:  activeWorkflowMapCardID,
					ActiveWorkflowWorktreeID: activeWorkflowWorktreeID,
				}
			}
			a.agentsMu.Unlock()
		}
	}
}

func (a *Actor) handleListAgents(ctx actor.PureContext, req domain.WorkspaceListAgentsReq) (domain.AgentRefListResp, error) {
	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	out := make([]domain.AgentRef, 0, len(a.Agents))
	query := strings.ToLower(req.Query)
	projectID := a.resolveProjectFilterID(req.ProjectID)
	// Child-agent filtering: an explicit ParentAgentId always wins; otherwise
	// ChildrenOnly falls back to the turn-engine-injected CallerAgentId. When
	// the filter is active but no parent can be resolved (ChildrenOnly with no
	// injected caller), return nothing rather than leaking all agents.
	var parentFilter string
	childFilterActive := false
	if req.ParentAgentID != "" {
		parentFilter = req.ParentAgentID
		childFilterActive = true
	} else if req.ChildrenOnly {
		parentFilter = req.CallerAgentID
		childFilterActive = true
	}
	projectNames := a.projectNameByActorID()
	for _, ag := range a.Agents {
		if projectID != "" && ag.ProjectID != projectID {
			continue
		}
		if childFilterActive && (parentFilter == "" || ag.ParentAgentID != parentFilter) {
			continue
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(ag.DisplayName), query) &&
				!strings.Contains(strings.ToLower(ag.Title), query) {
				continue
			}
		}
		// Enrich ProjectName from the mount table so agent-facing projections
		// (hot-context "Conversable Agents" rows) show a readable project name
		// instead of the raw project actor ID.
		if ag.ProjectName == "" {
			ag.ProjectName = projectNames[ag.ProjectID]
		}
		if ag.ActorID != "" {
			if rt, ok := a.agentRuntime[ag.ActorID]; ok {
				if rt.State != "" {
					ag.Status = rt.State
				}
				if rt.ActiveTurnRef != "" {
					ag.ActiveTurnRef = rt.ActiveTurnRef
				}
				if rt.LastActivity != "" {
					ag.LastActivity = rt.LastActivity
				}
			}
		}
		if req.CallerAgentID != "" && ag.ActorID == req.CallerAgentID {
			ag.DisplayName = ag.DisplayName + " (self)"
		}
		out = append(out, ag)
	}
	return domain.AgentRefListResp{Items: out}, nil
}

// projectNameByActorID maps project actor IDs to mount names under mountMu
// (same lock direction as resolveProjectFilterID: agentsMu → mountMu nesting
// is safe, the reverse is never taken). Callers enriching AgentRef
// projections use it to fill ProjectName.
func (a *Actor) projectNameByActorID() map[string]string {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	out := make(map[string]string, len(a.Mounts))
	for _, m := range a.Mounts {
		if m.ActorID != "" && m.Name != "" {
			out[m.ActorID] = m.Name
		}
	}
	return out
}

// resolveProjectFilterID lets list_agents accept a project mount name in
// place of its actor ID; unrecognized values pass through unchanged.
func (a *Actor) resolveProjectFilterID(projectID string) string {
	if projectID == "" {
		return ""
	}
	// Runs on PureContext goroutines (list_agents); read Mounts under
	// mountMu so the scan cannot race owner-loop mount mutations.
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if m.ActorID == projectID {
			return projectID
		}
	}
	for _, m := range a.Mounts {
		if m.Name == projectID && m.ActorID != "" {
			return m.ActorID
		}
	}
	return projectID
}

func (a *Actor) handleAgents(ctx actor.PureContext) (domain.AgentRefListResp, error) {
	a.agentsMu.RLock()
	out := make([]domain.AgentRef, len(a.Agents))
	copy(out, a.Agents)
	a.agentsMu.RUnlock()
	return domain.AgentRefListResp{Items: out}, nil
}

// orDefault returns s when non-empty, else fallback.
func orDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// canDeleteAgentRef is the single source of truth for whether a workspace
// agent may be deleted by the user through workspace.delete_agent. It is
// shared by the list projection (agentRefToListItem → CanDelete) and the
// delete handler so the advertised capability cannot drift from the enforced
// policy.
//
// Rule: user-creatable agent kinds are deletable; every system-managed
// built-in kind (reviewer, app-builder, explorer, general, dreamer, worker,
// scout, plugin) is not. App-bound agents (BoundAppID != "") are owned by the
// app lifecycle (remove_app_agent) and are never user-deletable. Coordinator
// is user-creatable and therefore deletable — its singleton constraint is
// enforced at creation time.
func (a *Actor) canDeleteAgentRef(ref domain.AgentRef) bool {
	if ref.BoundAppID != "" {
		return false
	}
	cfg, ok := a.findAgentKindConfig(ref.AgentKind)
	if !ok {
		return false
	}
	return cfg.UserCreatable
}

func (a *Actor) agentRefToListItem(ref domain.AgentRef) gen.AgentListItem {
	loadState := ref.LoadState
	if loadState == "" {
		loadState = "unloaded"
	}
	item := gen.AgentListItem{
		ID:             ref.ID,
		ActorID:        ref.ActorID,
		DisplayName:    ref.DisplayName,
		AgentKind:      ref.AgentKind,
		ProjectID:      ref.ProjectID,
		Primary:        ref.Primary,
		Fast:           ref.Fast,
		Execution:      ref.Execution,
		Review:         ref.Review,
		Summary:        ref.Summary,
		Degraded:       ref.Degraded,
		DegradedReason: ref.DegradedReason,
		LoadState:      loadState,
		Mode:           ref.Mode,
	}
	item.Title = ref.Title
	item.LastActivity = ref.LastActivity
	item.CompactionPolicy = ref.CompactionPolicy
	item.ParentAgentID = ref.ParentAgentID
	item.LifecycleScope = ref.LifecycleScope
	item.DeletionStatus = ref.DeletionStatus
	item.BoundAppID = ref.BoundAppID
	item.BoundAppSlot = ref.BoundAppSlot
	if ref.ActorID != "" {
		item.ConversationTarget = "actor:" + ref.ActorID
	}
	item.CanDelete = a.canDeleteAgentRef(ref)
	a.agentsMu.RLock()
	rt, ok := a.agentRuntime[ref.ActorID]
	a.agentsMu.RUnlock()
	if ok {
		item.Runtime = &rt
		if rt.ThinkLevel != "" {
			item.ThinkLevel = rt.ThinkLevel
		}
		item.PermissionMode = rt.PermissionMode
	} else if ref.Status != "" {
		item.Runtime = &gen.AgentRuntimeState{State: ref.Status}
	}
	return item
}

// deriveUnifiedChildren projects the read-only Children list for a parent
// agent from workflow children: workspace-owned agents whose ParentAgentId
// matches (persisted, recovered on restart).
//
// Agents marked for deletion (DeletionStatus="deleting" tombstones retained
// during async cascade teardown) are excluded, matching buildAgentListState's
// top-level filtering — otherwise a deleting child lingers under its parent's
// Children projection and stays visible in the sidebar.
//
// ParentAgentId stores the stable parent actor ID, so this projection remains
// correct even when neither agent actor is loaded.
func (a *Actor) deriveUnifiedChildren(parentActorID string) []gen.AgentChildRef {
	if parentActorID == "" {
		return nil
	}
	return a.buildAgentChildIndex()[parentActorID]
}

// buildAgentChildIndex projects every agent's read-only child ref grouped by
// ParentAgentID in a single pass over the agents snapshot, so buildAgentListState
// stays O(n) instead of rescanning the agent list per item.
func (a *Actor) buildAgentChildIndex() map[string][]gen.AgentChildRef {
	return a.buildAgentChildIndexFrom(a.agentSnapshot())
}

func (a *Actor) buildAgentChildIndexFrom(agents []domain.AgentRef) map[string][]gen.AgentChildRef {
	index := make(map[string][]gen.AgentChildRef)
	for _, ag := range agents {
		if ag.DeletionStatus == "deleting" || ag.ParentAgentID == "" {
			continue
		}
		child := gen.AgentChildRef{
			ID:             ag.ID,
			ActorID:        ag.ActorID,
			ParentAgentID:  ag.ParentAgentID,
			DisplayName:    ag.DisplayName,
			AgentKind:      ag.AgentKind,
			LifecycleScope: ag.LifecycleScope,
			Status:         ag.Status,
		}
		if ag.ActorID != "" {
			child.ConversationTarget = "actor:" + ag.ActorID
		}
		index[ag.ParentAgentID] = append(index[ag.ParentAgentID], child)
	}
	return index
}

func (a *Actor) buildAgentListState(full bool) gen.WorkspaceAgentListState {
	agents := a.agentSnapshot()
	items := make([]gen.AgentListItem, 0, len(agents))
	for _, ag := range agents {
		// Agents marked for deletion are excluded from the interactive
		// projection immediately (synchronous detach). They remain in
		// a.Agents as tombstones until async teardown converges.
		if ag.DeletionStatus == "deleting" {
			continue
		}
		// All agents use the same projection path. LoadState drives what
		// the frontend can do with each item (unloaded → click to load;
		// loaded → interactive). ActorID is stable and always present.
		items = append(items, a.agentRefToListItem(ag))
	}
	// Derive the read-only unified Children projection (workflow children)
	// from the single source of truth (ParentAgentId). This is a second pass
	// so all parent identities are known before projecting children. Children
	// are never persisted separately.
	childIndex := a.buildAgentChildIndexFrom(agents)
	for i := range items {
		items[i].Children = childIndex[items[i].ActorID]
	}
	state := gen.WorkspaceAgentListState{
		Version: a.agentListVersion.Add(1),
		Full:    full,
		Items:   items,
	}
	a.agentListSnapshot.Store(state)
	return state
}

func (a *Actor) emitAgentListState(ctx actor.PureContext, full bool) {
	a.emitAgentListStateValue(ctx, a.buildAgentListState(full))
}

func (a *Actor) emitAgentListStateValue(ctx actor.PureContext, state gen.WorkspaceAgentListState) {
	if err := ctx.EmitEvent("agent_list_state", gen.WorkspaceAgentListStateEvent{State: state}); err != nil {
		ctx.Logger().Error("workspace: emit agent_list_state failed", "error", err)
	}
}

// buildAgentListStateIfChanged builds the current projection but reports
// whether its visible Items differ from the last published snapshot. Runtime
// status updates are frequent and often repeat the same state; callers can
// still return the freshly built state while avoiding redundant broadcasts.
func (a *Actor) buildAgentListStateIfChanged(full bool) (gen.WorkspaceAgentListState, bool) {
	previous, published := a.agentListSnapshot.Load().(gen.WorkspaceAgentListState)
	state := a.buildAgentListState(full)
	itemsEqual := len(previous.Items) == len(state.Items) &&
		(len(previous.Items) == 0 || reflect.DeepEqual(previous.Items, state.Items))
	if published && itemsEqual {
		// Preserve the published version and Full flag when no visible change
		// occurred. The latest projection is equivalent by Items, so the
		// lock-free list handler remains consistent with the event stream.
		a.agentListSnapshot.Store(previous)
		return previous, false
	}
	return state, true
}

func (a *Actor) handleAgentListState(actor.PureContext) (gen.WorkspaceAgentListState, error) {
	// Lock-free snapshot read: the state is built and stored on the ownerLoop,
	// so this PureContext handler never touches live mutable fields.
	if v := a.agentListSnapshot.Load(); v != nil {
		return v.(gen.WorkspaceAgentListState), nil
	}
	return gen.WorkspaceAgentListState{Full: true}, nil
}

// aistatsState is the cached aistats discovery snapshot (see aistatsSnap).
type aistatsState struct {
	ref ref.Ref
	id  string
}

// handleAistatsActorID is pure: it only reads/stores the atomic aistats
// snapshot and LookupService is thread-safe, so it runs off the owner loop.
func (a *Actor) handleAistatsActorID(ctx actor.PureContext) (domain.WorkspaceAistatsActorIdResp, error) {
	// Live lookup: during batch Start, workspace.OnStart may run before
	// aistats exposes itself, so the OnStart cache can be empty. Re-check
	// on every call so late discovery still works.
	snap, _ := a.aistatsSnap.Load().(aistatsState)
	if snap.id == "" || snap.ref == nil {
		if r, ok := ctx.LookupService("aistats"); ok {
			snap = aistatsState{ref: r, id: r.ID().String()}
			a.aistatsSnap.Store(snap)
		}
	}
	if snap.id == "" {
		return domain.WorkspaceAistatsActorIdResp{Error: "aistats actor not available"}, nil
	}
	return domain.WorkspaceAistatsActorIdResp{ActorID: snap.id}, nil
}

// handleAgentLoaded is the async notification handler for timer-triggered
// local spawns on the project owner loop. The project spawns the agent
// directly (eliminating the workspace→project→workspace round trip that used
// to deadlock both owner loops) and then fire-and-forgets this callable so
// workspace can update its in-memory agent registry with the fresh ActorID and
// LoadState. The handler MUST NOT synchronously invoke any project callable —
// doing so would re-introduce the back-edge that the local-spawn refactor
// eliminates.
// handleAgentLoaded is a stateless handler: find-by-ID and mutate run in one
// agentsMu critical section (a pure handler cannot rely on owner-loop
// serialization, and a stale snapshot index could hit the wrong agent after
// a concurrent delete reshuffles the slice). EmitEvent and the persist write
// happen after the lock is released.
func (a *Actor) handleAgentLoaded(ctx actor.PureContext, req gen.WorkspaceAgentLoadedReq) (gen.WorkspaceAgentLoadedResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_loaded", req, func() (gen.WorkspaceAgentLoadedResp, error) {
		if req.AgentID == "" || req.ActorID == "" {
			return gen.WorkspaceAgentLoadedResp{}, fmt.Errorf("workspace.agent_loaded: AgentId and ActorId are required")
		}
		a.agentsMu.Lock()
		idx := -1
		for i := range a.Agents {
			if a.Agents[i].ID == req.AgentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			a.agentsMu.Unlock()
			// Agent absent from the registry (deleted concurrently); the spawn
			// already happened on the project side and owns the actor, so
			// there is nothing to update here.
			return gen.WorkspaceAgentLoadedResp{}, nil
		}
		a.Agents[idx].ActorID = req.ActorID
		a.Agents[idx].Degraded = false
		a.Agents[idx].DegradedReason = ""
		a.Agents[idx].LoadState = "loaded"
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		a.emitAgentListState(ctx, false)
		return gen.WorkspaceAgentLoadedResp{}, nil
	})
}

// handleAgentStatusUpdate is a stateless handler: all Agents/agentRuntime
// mutations run under agentsMu, the list projection is an atomic snapshot,
// and the tail (lifecycle enqueue, sub-map monitoring) only touches
// lock-guarded in-memory state and performs cross-actor invokes outside the
// locks.
func (a *Actor) handleAgentStatusUpdate(ctx actor.PureContext, req gen.WorkspaceAgentStatusUpdateReq) (gen.WorkspaceAgentListState, error) {
	rt := gen.AgentRuntimeState{
		State:                   req.State,
		ActiveTurnRef:           req.ActiveTurnRef,
		Error:                   req.Error,
		ApprovalPending:         req.ApprovalPending,
		PlanApprovalPending:     req.PlanApprovalPending,
		AskUserPending:          req.AskUserPending,
		GoalSubmitPending:       req.GoalSubmitPending,
		CurrentTaskSummary:      req.CurrentTaskSummary,
		BoundTaskCardID:         req.BoundTaskCardID,
		ActiveWorkflowMapCardID: req.ActiveWorkflowMapCardID,
		LastActivity:            req.LastActivity,
		LastTurnCompletedAt:     req.LastTurnCompletedAt,
		ThinkLevel:              req.ThinkLevel,
		WorktreeID:              req.WorktreeID,
		WorktreeStatus:          req.WorktreeStatus,
		WorktreeName:            req.WorktreeName,
		MemoryMounted:           req.MemoryMounted,
		PermissionMode:          req.PermissionMode,
	}
	a.agentsMu.Lock()
	if a.agentRuntime == nil {
		a.agentRuntime = make(map[string]gen.AgentRuntimeState)
	}
	previous := a.agentRuntime[req.AgentActorID]
	// LastAccessedAt is workspace-owned (set via workspace.agent_access); the
	// agent never reports it, so carry it over instead of clobbering it.
	rt.LastAccessedAt = previous.LastAccessedAt
	a.agentRuntime[req.AgentActorID] = rt
	var lifecycleAgent domain.AgentRef
	needFlush := false
	for i := range a.Agents {
		if a.Agents[i].ActorID == req.AgentActorID {
			lifecycleAgent = a.Agents[i]
			changed := false
			if a.Agents[i].Title != req.Title {
				a.Agents[i].Title = req.Title
				changed = true
			}
			// Persist a primary route chain the agent reports back
			// (promote/demote events). Only applied when the agent explicitly
			// carries a non-nil Primary; ordinary status ticks omit it, so a
			// stale snapshot cannot clobber a concurrent user reconfigure.
			// Deep-compared to avoid spurious flushes; deep-copied so
			// persisted state owns its storage.
			if req.Primary != nil && !modelSlotsEqual(a.Agents[i].Primary, req.Primary) {
				a.Agents[i].Primary = cloneModelSlotPtr(req.Primary)
				changed = true
			}
			// Persist LastActivity so lazy-loaded agents (ActorID cleared on
			// restart) still show their last active time without requiring a
			// click to re-spawn.
			if req.LastActivity != "" && a.Agents[i].LastActivity != req.LastActivity {
				a.Agents[i].LastActivity = req.LastActivity
				changed = true
			}
			if a.Agents[i].Status != req.State {
				a.Agents[i].Status = req.State
				changed = true
			}
			// Sync sidebar ModeState from the agent's reported runtime state.
			// Mode is the workspace-owned read model for sidebar rendering; it is
			// updated here on every status push so the UI is always consistent.
			// BoundTaskCardID and ActiveWorkflowMapCardID are cleared when the
			// agent completes its goal or workflow; MemoryMounted reflects the
			// agent's current memory graph state.
			modeChanged := false
			if a.Agents[i].Mode == nil {
				a.Agents[i].Mode = &gen.AgentModeState{}
			}
			if a.Agents[i].Mode.BoundTaskCardID != req.BoundTaskCardID {
				a.Agents[i].Mode.BoundTaskCardID = req.BoundTaskCardID
				modeChanged = true
			}
			// Capture previous map ID before overwriting to detect the
			// owner→stopped transition (non-empty → empty). Only workflow
			// owners carry a non-empty MapCardID; workers and forks never do.
			prevMapID := a.Agents[i].Mode.ActiveWorkflowMapCardID
			if a.Agents[i].Mode.ActiveWorkflowMapCardID != req.ActiveWorkflowMapCardID {
				a.Agents[i].Mode.ActiveWorkflowMapCardID = req.ActiveWorkflowMapCardID
				modeChanged = true
			}
			// When the workflow map transitions from non-empty to empty (owner
			// stopping), clear the worktree ID so the workspace Mode state does
			// not retain a stale reference to a merged-and-deleted worktree.
			// This works for all owner kinds (including sub-map owners with
			// AgentKind=="worker") because only owners ever have a non-empty
			// MapCardID. The D2 guard below still protects spawn-stamped child
			// worktree IDs — workers push MapCardID="" every tick, but since
			// their prevMapID is always "" too, this branch never fires for them.
			if prevMapID != "" && req.ActiveWorkflowMapCardID == "" && a.Agents[i].Mode.ActiveWorkflowWorktreeID != "" {
				a.Agents[i].Mode.ActiveWorkflowWorktreeID = ""
				modeChanged = true
			}
			// ActiveWorkflowWorktreeID: empty pushes must NOT overwrite the
			// spawn-stamped child worktree ID (executor_worker_task.go). Workers
			// report "" because RawSession.ActiveWorkflow is owner-only; an
			// unguarded overwrite would silently strip the merge target at
			// review-approve time and orphan the worker's commits.
			if req.ActiveWorkflowWorktreeID != "" && a.Agents[i].Mode.ActiveWorkflowWorktreeID != req.ActiveWorkflowWorktreeID {
				a.Agents[i].Mode.ActiveWorkflowWorktreeID = req.ActiveWorkflowWorktreeID
				modeChanged = true
			}
			if a.Agents[i].Mode.MemoryMounted != req.MemoryMounted {
				a.Agents[i].Mode.MemoryMounted = req.MemoryMounted
				modeChanged = true
			}
			// Sync LoadState from runtime state: "error" if the agent reports an
			// error; "loaded" otherwise. "loading" is set transiently by loadAgentByID.
			if req.Error != "" && a.Agents[i].LoadState != "error" {
				a.Agents[i].LoadState = "error"
				modeChanged = true
			} else if req.Error == "" && a.Agents[i].LoadState == "error" {
				a.Agents[i].LoadState = "loaded"
				modeChanged = true
			}
			if changed || modeChanged {
				needFlush = true
			}
			break
		}
	}
	a.agentsMu.Unlock()
	if needFlush {
		a.scheduleStateFlush(ctx)
	}
	if lifecycleAgent.ActorID != "" {
		if event, ok := lifecycleEventForAgent(previous, rt, lifecycleAgent, time.Now()); ok {
			a.enqueueAgentLifecycle(ctx, event)
		}
	}
	// sub_map monitoring: when a sub-map owner reports its workflow stopped
	// (idle + previously active workflow cleared) or a failed/error state,
	// propagate the result onto the parent task card.
	a.handleSubMapOwnerStatus(ctx, req.AgentActorID, req.State, req.ActiveWorkflowMapCardID, req.Error)
	a.sweepSubMapInstances(ctx)
	state, changed := a.buildAgentListStateIfChanged(false)
	if changed {
		a.emitAgentListStateValue(ctx, state)
	}
	return state, nil
}

// handleAgentAccess stamps AgentRuntimeState.LastAccessedAt when the user
// opens an agent's conversation, so recently-completed badges clear once the
// agent has actually been viewed. Stateless handler: agentsMu guards the
// map/slice mutation, the list projection is lock-free (atomic snapshot).
func (a *Actor) handleAgentAccess(ctx actor.PureContext, req gen.WorkspaceAgentAccessReq) (gen.WorkspaceAgentListState, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	a.agentsMu.Lock()
	if a.agentRuntime == nil {
		a.agentRuntime = make(map[string]gen.AgentRuntimeState)
	}
	rt, ok := a.agentRuntime[req.AgentActorID]
	if !ok {
		// No status push yet (agent unloaded): seed from the persisted status so
		// the projection keeps reflecting it instead of dropping to idle.
		for i := range a.Agents {
			if a.Agents[i].ActorID == req.AgentActorID {
				rt.State = a.Agents[i].Status
				rt.LastActivity = a.Agents[i].LastActivity
				break
			}
		}
	}
	rt.LastAccessedAt = now
	a.agentRuntime[req.AgentActorID] = rt
	a.agentsMu.Unlock()
	state, changed := a.buildAgentListStateIfChanged(false)
	if changed {
		a.emitAgentListStateValue(ctx, state)
	}
	return state, nil
}

func (a *Actor) handleListAgentKinds(ctx actor.PureContext) (domain.WorkspaceListAgentKindsResp, error) {
	configs := a.agentKindConfigsSnapshot()
	items := make([]domain.AgentKindInfo, 0, len(configs))
	for _, cfg := range configs {
		cfg = normalizeAgentKindConfig(cfg)
		items = append(items, domain.AgentKindInfo{
			Kind:          cfg.Kind,
			DisplayName:   cfg.DisplayName,
			UserCreatable: cfg.UserCreatable,
			SystemManaged: cfg.SystemManaged,
			Builtin:       isBuiltinAgentKind(cfg.Kind),
			NamePool:      cfg.NamePool,
			RandomName:    cfg.RandomName,
		})
	}
	return domain.WorkspaceListAgentKindsResp{Items: items}, nil
}

// isBuiltinAgentKind reports whether kind is one of the hardcoded
// domain.ValidAgentKinds() templates (as opposed to a user-defined kind). It is
// the single source of truth for AgentKindInfo.Builtin and the create/delete
// builtin guards; SystemManaged cannot be used because coder is a builtin with
// SystemManaged=false.
func isBuiltinAgentKind(kind string) bool {
	for _, k := range domain.ValidAgentKinds() {
		if k.Kind == kind {
			return true
		}
	}
	return false
}

// validateAgentKindSlug enforces the custom agent-kind slug grammar: non-empty,
// at most 64 chars, lowercase letters/digits separated by single hyphens (no
// leading, trailing, or doubled hyphens).
func validateAgentKindSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("kind is required")
	}
	if len(slug) > 64 {
		return fmt.Errorf("kind %q is too long (max 64 characters)", slug)
	}
	prevHyphen := true // treat start as a hyphen so a leading hyphen is rejected
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			prevHyphen = false
		case c == '-':
			if prevHyphen {
				return fmt.Errorf("kind %q must be lowercase letters/digits separated by single hyphens", slug)
			}
			prevHyphen = true
		default:
			return fmt.Errorf("kind %q may contain only lowercase letters, digits, and hyphens", slug)
		}
	}
	if prevHyphen {
		return fmt.Errorf("kind %q must not end with a hyphen", slug)
	}
	return nil
}

func (a *Actor) handleListAgentKindConfigs(ctx actor.PureContext) (domain.WorkspaceListAgentKindConfigsResp, error) {
	configs := a.agentKindConfigsSnapshot()
	items := make([]domain.AgentKindConfig, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, normalizeAgentKindConfig(cfg))
	}
	return domain.WorkspaceListAgentKindConfigsResp{Items: items}, nil
}

func (a *Actor) handleGetAgentKindConfig(ctx actor.PureContext, req domain.WorkspaceGetAgentKindConfigReq) (domain.AgentKindConfig, error) {
	cfg, ok := a.findAgentKindConfig(req.Kind)
	if !ok {
		return domain.AgentKindConfig{}, fmt.Errorf("workspace.get_agent_kind_config: unknown agent kind %q", req.Kind)
	}
	return cfg, nil
}

// handleSaveAgentKindConfig is a stateless handler: kindConfigsMu serializes
// the config replacement against the owner-loop seed and shell-preference
// refresh; the persist write happens after the lock is released.
func (a *Actor) handleSaveAgentKindConfig(ctx actor.PureContext, req domain.WorkspaceSaveAgentKindConfigReq) (domain.AgentKindConfig, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AgentKindConfig{}, err
	}
	cfg := normalizeAgentKindConfig(domain.AgentKindConfig{
		Kind:                req.Kind,
		DisplayName:         req.DisplayName,
		UserCreatable:       req.UserCreatable,
		SystemManaged:       req.SystemManaged,
		RolePromptRef:       req.RolePromptRef,
		SystemFragmentRefs:  req.SystemFragmentRefs,
		DefaultBundleIDs:    req.DefaultBundleIDs,
		AutoAllowTools:      req.AutoAllowTools,
		AutoAllowCandidates: req.AutoAllowCandidates,
		EnvironmentContext:  req.EnvironmentContext,
		NamePool:            req.NamePool,
		SkillIDs:            req.SkillIDs,
		Primary:             req.Primary,
		Fast:                req.Fast,
		Execution:           req.Execution,
		Review:              req.Review,
		Summary:             req.Summary,
		CompactionPolicy:    req.CompactionPolicy,
		StoragePolicy:       req.StoragePolicy,
		RandomName:          req.RandomName,
		MaxTurns:            req.MaxTurns,
	})
	// SystemManaged is a hardcoded property derived from ValidAgentKinds and
	// must never be overridden by client input.
	for _, k := range domain.ValidAgentKinds() {
		if k.Kind == cfg.Kind {
			cfg.SystemManaged = k.SystemManaged
			break
		}
	}
	if cfg.Kind == "" {
		return domain.AgentKindConfig{}, fmt.Errorf("workspace.save_agent_kind_config: kind is required")
	}
	if cfg.RolePromptRef.Key == "" {
		return domain.AgentKindConfig{}, fmt.Errorf("workspace.save_agent_kind_config: role prompt ref is required")
	}
	// Record which builtin default bundles the user explicitly removed. The
	// seed reconcile and the runtime fallback in findAgentKindConfig union
	// code-level defaults into persisted lists so newly introduced default
	// bundles propagate; without an explicit removal record they cannot
	// distinguish "persisted before the default existed" from "user removed
	// it" and would silently re-add the bundle on every read. A default
	// bundle added in code after this save is in neither list and still
	// propagates.
	for _, def := range defaultAgentKindConfigs() {
		if def.Kind != cfg.Kind {
			continue
		}
		selected := make(map[string]struct{}, len(req.DefaultBundleIDs))
		for _, id := range req.DefaultBundleIDs {
			selected[id] = struct{}{}
		}
		for _, id := range def.DefaultBundleIDs {
			if _, ok := selected[id]; !ok {
				cfg.RemovedBundleIDs = append(cfg.RemovedBundleIDs, id)
			}
		}
		break
	}
	a.kindConfigsMu.Lock()
	replaced := false
	for i, existing := range a.AgentKindConfigs {
		if existing.Kind != cfg.Kind {
			continue
		}
		a.AgentKindConfigs[i] = cfg
		replaced = true
		break
	}
	if !replaced {
		a.AgentKindConfigs = append(a.AgentKindConfigs, cfg)
	}
	a.kindConfigsMu.Unlock()
	a.saveAgentKindConfigsOrLog(ctx)
	return cfg, nil
}

// handleCreateAgentKind is a stateless handler: kindConfigsMu serializes the
// config append against the owner-loop seed and shell-preference refresh; the
// persist write happens after the lock is released.
func (a *Actor) handleCreateAgentKind(ctx actor.PureContext, req domain.WorkspaceCreateAgentKindReq) (domain.WorkspaceCreateAgentKindResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.WorkspaceCreateAgentKindResp{}, err
	}
	kind := strings.TrimSpace(req.Kind)
	if err := validateAgentKindSlug(kind); err != nil {
		return domain.WorkspaceCreateAgentKindResp{}, fmt.Errorf("workspace.create_agent_kind: %w", err)
	}
	if isBuiltinAgentKind(kind) {
		return domain.WorkspaceCreateAgentKindResp{}, fmt.Errorf("workspace.create_agent_kind: %q is a built-in agent kind", kind)
	}
	base := strings.TrimSpace(req.BaseKind)
	if base == "" {
		base = domain.AgentKindCoder
	}
	baseCfg, ok := a.findAgentKindConfig(base)
	if !ok {
		return domain.WorkspaceCreateAgentKindResp{}, fmt.Errorf("workspace.create_agent_kind: base kind %q is unknown", base)
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = kind
	}

	a.kindConfigsMu.Lock()
	for _, cfg := range a.AgentKindConfigs {
		if cfg.Kind == kind {
			a.kindConfigsMu.Unlock()
			return domain.WorkspaceCreateAgentKindResp{}, fmt.Errorf("workspace.create_agent_kind: agent kind %q already exists", kind)
		}
	}
	cfg := cloneAgentKindConfig(baseCfg)
	cfg.Kind = kind
	cfg.DisplayName = displayName
	// A user-defined template is always user-creatable and never
	// system-managed, regardless of what the cloned base carried.
	cfg.UserCreatable = true
	cfg.SystemManaged = false
	cfg = normalizeAgentKindConfig(cfg)
	a.AgentKindConfigs = append(a.AgentKindConfigs, cfg)
	a.kindConfigsMu.Unlock()
	a.saveAgentKindConfigsOrLog(ctx)
	return domain.WorkspaceCreateAgentKindResp{
		Kind:          cfg.Kind,
		DisplayName:   cfg.DisplayName,
		UserCreatable: cfg.UserCreatable,
		SystemManaged: cfg.SystemManaged,
		Builtin:       false,
	}, nil
}

// handleDeleteAgentKind is a stateless handler: kindConfigsMu serializes the
// config removal against the owner-loop seed; the persist write happens after
// the lock is released.
func (a *Actor) handleDeleteAgentKind(ctx actor.PureContext, req domain.WorkspaceDeleteAgentKindReq) (domain.WorkspaceDeleteAgentKindResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.WorkspaceDeleteAgentKindResp{}, err
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		return domain.WorkspaceDeleteAgentKindResp{}, fmt.Errorf("workspace.delete_agent_kind: kind is required")
	}
	if isBuiltinAgentKind(kind) {
		return domain.WorkspaceDeleteAgentKindResp{}, fmt.Errorf("workspace.delete_agent_kind: built-in agent kind %q cannot be deleted", kind)
	}
	// Instance guard: deleting a kind that live agents still reference would
	// strand them as ghosts (findAgentKindConfig miss → runtime degrades to a
	// tool-less agent). Refuse, reporting the count so the user can delete them
	// first.
	live := 0
	for _, ag := range a.agentSnapshot() {
		if ag.AgentKind == kind {
			live++
		}
	}
	if live > 0 {
		return domain.WorkspaceDeleteAgentKindResp{}, fmt.Errorf("workspace.delete_agent_kind: %d live agent(s) still use kind %q; delete them first", live, kind)
	}

	a.kindConfigsMu.Lock()
	found := false
	kept := a.AgentKindConfigs[:0:0]
	for _, cfg := range a.AgentKindConfigs {
		if cfg.Kind == kind {
			found = true
			continue
		}
		kept = append(kept, cfg)
	}
	a.AgentKindConfigs = kept
	a.kindConfigsMu.Unlock()
	if !found {
		return domain.WorkspaceDeleteAgentKindResp{}, fmt.Errorf("workspace.delete_agent_kind: unknown agent kind %q", kind)
	}
	a.saveAgentKindConfigsOrLog(ctx)
	return domain.WorkspaceDeleteAgentKindResp{Kind: kind, Removed: true}, nil
}

func resolveAgentModelSlots(req domain.WorkspaceCreateAgentReq, cfg domain.AgentKindConfig) (primary, fast, execution, review, summary *gen.ModelSlot) {
	primary = req.Primary
	if primary == nil {
		primary = cfg.Primary
	}
	fast = req.Fast
	if fast == nil {
		fast = cfg.Fast
	}
	execution = req.Execution
	if execution == nil {
		execution = cfg.Execution
	}
	review = req.Review
	if review == nil {
		review = cfg.Review
	}
	summary = req.Summary
	if summary == nil {
		summary = cfg.Summary
	}
	return primary, fast, execution, review, summary
}

func (a *Actor) handleCreateAgent(ctx actor.PureContext, req domain.WorkspaceCreateAgentReq) (domain.AgentRef, error) {
	return panicprobe.Guard(ctx, "workspace.create_agent", req, func() (domain.AgentRef, error) {
		// human/admin for direct UI calls; "system"/"agent" for agent tool
		// calls: project agents inherit the workspace manager's "system" role
		// via gospore buildSpawn role inheritance, global agents are stamped
		// "agent" (spawnGlobalAgent). create_agent is part of the agent tool
		// surface; per-agent reachability is enforced by the turn engine's
		// card-scope audit. Anonymous/glass/peer/zero-identity stay denied.
		if err := policy.RequireAgentOrHuman(ctx.Identity().Role); err != nil {
			return domain.AgentRef{}, err
		}
		cfg, ok := a.findAgentKindConfig(req.AgentKind)
		if !ok {
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: unknown agent kind %q", req.AgentKind)
		}
		if !cfg.UserCreatable {
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: agent kind %q is not user-creatable", req.AgentKind)
		}
		if cfg.Kind == domain.AgentKindCoder && req.ProjectID == "" {
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: coder requires a project")
		}
		if cfg.Kind == domain.AgentKindCoordinator {
			// Coordinator is the process-unique system butler agent, workspace-global
			// (not scoped to any project). A second coordinator is rejected so
			// nickname routing always resolves to exactly one target.
			if req.ProjectID != "" {
				return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: coordinator is a global agent and cannot be scoped to a project")
			}
			for _, ag := range a.agentSnapshot() {
				if ag.AgentKind == domain.AgentKindCoordinator {
					return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: coordinator already exists (process-unique singleton)")
				}
			}
		}
		// System meta projects are management nodes, not real repositories. Non-
		// global agent kinds cannot be created under a system meta project.
		if req.ProjectID != "" && cfg.Kind != domain.AgentKindCoordinator {
			if mounted, ok := a.findMountByActorID(req.ProjectID); ok && mounted.System {
				return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: agent kind %q is not available for the workspace project", cfg.Kind)
			}
		}
		if req.ProjectID != "" {
			foundProject := false
			// Pre-scan under mountMu.RLock (handler may run off the owner
			// loop once converted to PureContext).
			a.mountMu.RLock()
			for _, mounted := range a.Mounts {
				if mounted.ActorID == req.ProjectID {
					foundProject = true
					break
				}
			}
			a.mountMu.RUnlock()
			if !foundProject {
				return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: project %q not found", req.ProjectID)
			}
		}

		// Name allocation and registry reservation run in one agentsMu
		// critical section (scheduler-spawn precedent): display-name
		// generation, unique spawn-name allocation, and the registry
		// append are atomic against concurrent create_agent goroutines,
		// so two handlers cannot reserve the same spawn name. The
		// cross-actor spawn runs after the lock is released; the
		// reserved row is then patched with the spawn result (or rolled
		// back) under a short critical section. The section is
		// in-memory only — no cross-actor invoke happens under the lock.
		spawnName := ""
		a.agentsMu.Lock()
		displayName := req.DisplayName
		if displayName == "" {
			displayName = generateDisplayName(cfg.RandomName, a.Agents, req.ProjectID)
		}
		if displayName == "" {
			displayName = cfg.DisplayName
		}
		if err := validateNameSegment("workspace.create_agent", displayName); err != nil {
			a.agentsMu.Unlock()
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: %w", err)
		}
		for attempt := 0; attempt < 16; attempt++ {
			candidate := displayName + "#" + randomHexSuffix()
			conflict := false
			for i := range a.Agents {
				if a.Agents[i].ProjectID == req.ProjectID && a.Agents[i].ID == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				spawnName = candidate
				break
			}
		}
		if spawnName == "" {
			a.agentsMu.Unlock()
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: could not allocate unique agent name for %q after retries", displayName)
		}
		reserved := domain.AgentRef{
			ID:          spawnName,
			ProjectID:   req.ProjectID,
			DisplayName: displayName,
			AgentKind:   req.AgentKind,
			Status:      "idle",
			LoadState:   "loading",
		}
		a.Agents = append(a.Agents, reserved)
		a.agentsMu.Unlock()

		primary, fast, execution, review, summary := resolveAgentModelSlots(req, cfg)
		var ag domain.AgentRef
		var err error
		if req.ProjectID == "" {
			ag, err = a.spawnGlobalAgent(ctx, spawnName, req.AgentKind, displayName, primary, fast, execution, review, summary, "", a.globalPermissionMode())
		} else {
			ag, err = a.spawnAgentViaProject(ctx, req.ProjectID, spawnName, req.AgentKind, displayName, primary, fast, execution, review, summary, "", req.WorktreeID, "", nil, nil, "", a.globalPermissionMode())
		}
		if err != nil {
			a.agentsMu.Lock()
			for i := range a.Agents {
				if a.Agents[i].ID == spawnName {
					a.Agents = append(a.Agents[:i], a.Agents[i+1:]...)
					break
				}
			}
			a.agentsMu.Unlock()
			return domain.AgentRef{}, fmt.Errorf("workspace.create_agent: %w", err)
		}
		if cfg.CompactionPolicy != nil {
			cp := *cfg.CompactionPolicy
			ag.CompactionPolicy = &cp
		}
		if req.CompactionPolicy != nil {
			cp := *req.CompactionPolicy
			ag.CompactionPolicy = &cp
		}
		a.agentsMu.Lock()
		for i := range a.Agents {
			if a.Agents[i].ID == spawnName {
				a.Agents[i] = ag
				break
			}
		}
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		return ag, nil
	})
}

// handleUpdateAgent is a stateless handler. Find-by-ID and field mutation run
// in one agentsMu critical section (snapshot-then-mutateAgentAt would be a
// TOCTOU off the owner loop); the cross-actor agent_configure push, persist
// write, and event emission happen after the lock is released.
func (a *Actor) handleUpdateAgent(ctx actor.PureContext, req domain.WorkspaceUpdateAgentReq) (domain.AgentRef, error) {
	return panicprobe.Guard(ctx, "workspace.update_agent", req, func() (domain.AgentRef, error) {
		if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
			return domain.AgentRef{}, err
		}
		a.agentsMu.Lock()
		idx := -1
		for i := range a.Agents {
			if a.Agents[i].ID == req.AgentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			a.agentsMu.Unlock()
			return domain.AgentRef{}, fmt.Errorf("workspace.update_agent: agent %q not found", req.AgentID)
		}

		if req.DisplayName != "" {
			a.Agents[idx].DisplayName = req.DisplayName
		}
		// Mirror the title edit so configureAgentActor forwards it to the running
		// agent via agent.configure. The agent actor is the source of truth; the
		// workspace copy is reconciled again on the next status notification.
		if req.Title != "" {
			a.Agents[idx].Title = req.Title
		}
		// Always overwrite slot fields so the frontend can clear them by sending
		// empty values (e.g. switching a model back to "Auto").
		a.Agents[idx].Primary = req.Primary
		a.Agents[idx].Fast = req.Fast
		a.Agents[idx].Execution = req.Execution
		a.Agents[idx].Review = req.Review
		a.Agents[idx].Summary = req.Summary
		if req.CompactionPolicy != nil {
			cp := *req.CompactionPolicy
			a.Agents[idx].CompactionPolicy = &cp
		}
		updated := a.Agents[idx]
		a.agentsMu.Unlock()

		// Push configuration changes to the running agent actor via agent.configure.
		if updated.ActorID != "" {
			if cid, cerr := identity.ParseCanonicalID(updated.ActorID); cerr == nil {
				if agentRef, ok := ctx.LookupID(id.From(cid)); ok && agentRef != nil {
					a.configureAgentActor(ctx, agentRef, updated)
				}
			}
		}

		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		return updated, nil
	})
}

// handleDeleteAgent is a stateless handler: the find-by-ID reads the
// agentsMu-guarded snapshot and the cascade teardown runs off the handler
// goroutine via teardownSubtreeAsync.
func (a *Actor) handleDeleteAgent(ctx actor.PureContext, req domain.WorkspaceDeleteAgentReq) (domain.AgentRef, error) {
	return panicprobe.Guard(ctx, "workspace.delete_agent", req, func() (domain.AgentRef, error) {
		if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
			return domain.AgentRef{}, err
		}
		idx := -1
		for i, ag := range a.agentSnapshot() {
			if ag.ID == req.AgentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return domain.AgentRef{}, fmt.Errorf("workspace.delete_agent: agent %q not found", req.AgentID)
		}
		ag := a.agentAt(idx)

		// Built-in guard: system-managed kinds and app-bound agents are not
		// user-deletable. Enforced server-side with the same predicate the
		// list projection advertises, so a client that ignores CanDelete
		// still cannot tear down a built-in.
		if !a.canDeleteAgentRef(ag) {
			return domain.AgentRef{}, fmt.Errorf("workspace.delete_agent: agent %q is built-in and cannot be deleted (kind=%q, boundAppId=%q)", req.AgentID, ag.AgentKind, ag.BoundAppID)
		}

		// Unified cascade path: mark the whole subtree deleting (persistent
		// intent) and emit immediately so the frontend drops it from the
		// projection; teardownSubtreeAsync then runs turn_cancel → Destroy →
		// worktree release → state cleanup off the owner loop with bounded
		// retries and sweep recovery. This replaces the former ad-hoc goroutine
		// that captured the handler context and did a bare synchronous
		// ctx.Destroy without cascade bookkeeping.
		a.cascadeDelete(ctx, a.computeDeletionSubtree(ag.ID))
		return ag, nil
	})
}

// handleCreateAppAgent implements workspace.create_app_agent: idempotently
// provisions (or reconciles) the dedicated workspace-global agent for one
// plugin_agent binding of an app. The agent is created in the same registry
// and via the same spawn path as the Coordinator (ProjectID "", spawnGlobal
// agent), keyed by (BoundAppID, BoundAppSlot) — one agent per app and slot.
// Called by appmanager once per manifest AgentBinding.PluginAgents entry on
// register/reload/restore; not part of any tool surface.
func (a *Actor) handleCreateAppAgent(ctx actor.PureContext, req domain.WorkspaceCreateAppAgentReq) (domain.WorkspaceEnsureAppAgentResp, error) {
	return panicprobe.Guard(ctx, "workspace.create_app_agent", req, func() (domain.WorkspaceEnsureAppAgentResp, error) {
		// Internal lifecycle callable: only the appmanager manager ("system")
		// or an admin may provision app agents. Apps reach this only through
		// appmanager's registration path — the manifest declaration IS the
		// authorization.
		if role := ctx.Identity().Role; role != "system" && role != "admin" {
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("forbidden: requires system or admin role, got %q", role)
		}
		if req.AppID == "" {
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("workspace.create_app_agent: AppId is required")
		}
		slot := req.Slot
		if slot == "" {
			slot = "default" // rows from before slots existed
		}
		cfg, ok := a.findAgentKindConfig(domain.AgentKindPlugin)
		if !ok {
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("workspace.create_app_agent: agent kind %q unavailable", domain.AgentKindPlugin)
		}

		// Existing binding for this (app, slot): reconcile display name in
		// place and re-push the bind so the agent reconciles prompt overlay +
		// bundle mounts (reload path). Never create a second agent for the
		// same slot.
		for i, existing := range a.agentSnapshot() {
			if existing.BoundAppID != req.AppID || existing.BoundAppSlot != slot {
				continue
			}
			updated := existing
			if req.DisplayName != "" && req.DisplayName != existing.DisplayName {
				updated = a.mutateAgentAt(i, func(ag *domain.AgentRef) { ag.DisplayName = req.DisplayName })
			}
			// Reconcile a plugin_agent `model:` binding in place so a reload
			// that adds or changes `model:` actually flips the running agent
			// onto the new unit (and so the registry row survives a process
			// restart with the correct slot persisted). An empty or malformed
			// req.Model resolves to the host default (nil slot); a non-empty
			// value produces a hard-pinned [unit] slot. The desired slot is
			// always recomputed so dropping the binding clears the previous
			// pin instead of leaving a stale one behind.
			desired := pinnedUnitSlotFromUnitString(req.Model)
			primaryChanged := !modelSlotsEqual(existing.Primary, desired)
			if primaryChanged {
				if desired == nil {
					updated = a.mutateAgentAt(i, func(ag *domain.AgentRef) { ag.Primary = nil })
				} else {
					updated = a.mutateAgentAt(i, func(ag *domain.AgentRef) { ag.Primary = cloneModelSlotPtr(desired) })
				}
			}
			// After a process restart the bound agent row survives but its
			// actor is gone; create re-loads it (coordinator-style eager
			// availability) so the bind push reaches a live actor and bundle
			// changes from reload are actually reconciled.
			if updated.LoadState != "loaded" || updated.ActorID == "" {
				if loaded, _, lerr := a.loadAgentByID(ctx, updated.ID); lerr == nil {
					updated = loaded
				} else {
					ctx.Logger().Warn("workspace.create_app_agent: re-load failed", "agentId", updated.ID, "error", lerr)
				}
			} else if primaryChanged && updated.ActorID != "" {
				// Live actor + primary slot just changed: push agent_configure
				// so the in-memory slot flips without an unload/reload.
				// loadAgentByID above already spawns with the new Primary for
				// the not-loaded case.
				if cid, cerr := identity.ParseCanonicalID(updated.ActorID); cerr == nil {
					if agentRef, ok := ctx.LookupID(id.From(cid)); ok && agentRef != nil {
						a.configureAgentActor(ctx, agentRef, updated)
					}
				}
			}
			a.pushAppAgentBind(ctx, updated, req)
			a.saveOrLog(ctx)
			a.emitAgentsChanged(ctx)
			return domain.WorkspaceEnsureAppAgentResp{
				AgentID:     updated.ID,
				ActorID:     updated.ActorID,
				DisplayName: updated.DisplayName,
				Created:     false,
			}, nil
		}

		displayName := req.DisplayName
		if displayName == "" {
			displayName = req.AppID
		}
		// Name allocation mirrors create_agent: reserve a unique spawn name
		// and the registry row atomically, spawn after the lock is released,
		// then patch (or roll back) the row in a short critical section.
		spawnName := ""
		a.agentsMu.Lock()
		if err := validateNameSegment("workspace.create_app_agent", displayName); err != nil {
			a.agentsMu.Unlock()
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("workspace.create_app_agent: %w", err)
		}
		for attempt := 0; attempt < 16; attempt++ {
			candidate := displayName + "#" + randomHexSuffix()
			conflict := false
			for i := range a.Agents {
				if a.Agents[i].ID == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				spawnName = candidate
				break
			}
		}
		if spawnName == "" {
			a.agentsMu.Unlock()
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("workspace.create_app_agent: could not allocate unique agent name for %q after retries", displayName)
		}
		reserved := domain.AgentRef{
			ID:           spawnName,
			ProjectID:    "",
			DisplayName:  displayName,
			AgentKind:    domain.AgentKindPlugin,
			Status:       "idle",
			LoadState:    "loading",
			BoundAppID:   req.AppID,
			BoundAppSlot: slot,
		}
		a.Agents = append(a.Agents, reserved)
		a.agentsMu.Unlock()

		primary, fast, execution, review, summary := resolveAgentModelSlots(domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindPlugin}, cfg)
		// A plugin_agent `model:` binding overrides the kind-default primary
		// with a hard-pinned unit slot. Empty req.Model falls through to the
		// host default (cfg.Primary / [auto]).
		if pinned := pinnedUnitSlotFromUnitString(req.Model); pinned != nil {
			primary = pinned
		}
		ag, err := a.spawnGlobalAgent(ctx, spawnName, domain.AgentKindPlugin, displayName, primary, fast, execution, review, summary, "", a.globalPermissionMode())
		if err != nil {
			a.agentsMu.Lock()
			for i := range a.Agents {
				if a.Agents[i].ID == spawnName {
					a.Agents = append(a.Agents[:i], a.Agents[i+1:]...)
					break
				}
			}
			a.agentsMu.Unlock()
			return domain.WorkspaceEnsureAppAgentResp{}, fmt.Errorf("workspace.create_app_agent: %w", err)
		}
		ag.BoundAppID = req.AppID
		ag.BoundAppSlot = slot
		a.agentsMu.Lock()
		for i := range a.Agents {
			if a.Agents[i].ID == spawnName {
				a.Agents[i] = ag
				break
			}
		}
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		a.pushAppAgentBind(ctx, ag, req)
		return domain.WorkspaceEnsureAppAgentResp{
			AgentID:     ag.ID,
			ActorID:     ag.ActorID,
			DisplayName: ag.DisplayName,
			Created:     true,
		}, nil
	})
}

// handleRemoveAppAgent implements workspace.remove_app_agent: removes the
// agents bound to an app — every binding slot — (appmanager unregister path),
// keyed by the binding instead of an agent ID. Idempotent — removing an app
// with no bound agent is a no-op. Teardown reuses the unified cascade path
// (turn cancel → destroy → persisted-state cleanup).
func (a *Actor) handleRemoveAppAgent(ctx actor.PureContext, req domain.WorkspaceRemoveAppAgentReq) error {
	_, err := panicprobe.Guard(ctx, "workspace.remove_app_agent", req, func() (struct{}, error) {
		if role := ctx.Identity().Role; role != "system" && role != "admin" {
			return struct{}{}, fmt.Errorf("forbidden: requires system or admin role, got %q", role)
		}
		if req.AppID == "" {
			return struct{}{}, fmt.Errorf("workspace.remove_app_agent: AppId is required")
		}
		for _, ag := range a.agentSnapshot() {
			if ag.BoundAppID != req.AppID {
				continue
			}
			a.cascadeDelete(ctx, a.computeDeletionSubtree(ag.ID))
		}
		return struct{}{}, nil
	})
	return err
}

// pushAppAgentBind fire-and-forgets agent_bind_app to the bound agent actor
// (async Invoke, same pattern as configureAgentActor: awaiting the agent owner
// loop from a workspace handler risks cross-owner deadlocks). The agent
// reconciles its bound bundle mounts and prompt overlay on its own loop and
// persists them, so a dropped push self-heals on the next create call.
func (a *Actor) pushAppAgentBind(ctx actor.PureContext, ag domain.AgentRef, req domain.WorkspaceCreateAppAgentReq) {
	if ag.ActorID == "" {
		return
	}
	cid, err := identity.ParseCanonicalID(ag.ActorID)
	if err != nil {
		return
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return
	}
	_ = agentRef.Invoke(ctx.Lifecycle(), "agent_bind_app", gen.AgentBindAppReq{
		AppID:         req.AppID,
		SystemPrompt:  req.SystemPrompt,
		BundleCardIds: req.BundleCardIds,
	})
}

// globalPermissionMode reads the global permission mode from account
// preferences. Called on the owner loop (no lock needed). Returns "" when
// unset; the agent's normalizePermissionMode defaults that to "permission".
func (a *Actor) globalPermissionMode() string {
	if a.accountPrefs.Preferences == nil {
		return ""
	}
	return a.accountPrefs.Preferences["permissionMode"]
}

// loadAgentByID spawns the actor for a persisted agent if it is not already
// live. The handle is the agent's stable registry ID or, failing that, its
// ActorID (mirroring findAgentRef/agent_unload: callers that only hold the
// live actor id — e.g. an app resolving its own plugin agent — must still be
// able to load it). It returns the up-to-date AgentRef and a bool indicating
// whether the agent was already loaded.
func (a *Actor) loadAgentByID(ctx actor.PureContext, agentID string) (domain.AgentRef, bool, error) {
	idx := -1
	for i, ag := range a.agentSnapshot() {
		if ag.ID == agentID || (ag.ActorID != "" && ag.ActorID == agentID) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return domain.AgentRef{}, false, fmt.Errorf("workspace.load_agent: agent %q not found", agentID)
	}
	a.agentsMu.RLock()
	if idx >= len(a.Agents) {
		a.agentsMu.RUnlock()
		return domain.AgentRef{}, false, fmt.Errorf("workspace.load_agent: agent %q disappeared", agentID)
	}
	ag := a.Agents[idx]
	alreadyLoaded := ag.LoadState == "loaded" && ag.ActorID != ""
	a.agentsMu.RUnlock()
	if alreadyLoaded {
		return ag, true, nil
	}

	// If the agent still has a live, addressable actor, reuse it regardless of
	// LoadState. An agent may be marked "error" by a runtime status report while
	// its actor is still alive and registered in the tree; re-spawning would
	// collide with the live name (gospore/tree: name taken). After a process
	// restart all actors die and ActorID is cleared by OnStart, so the lookup
	// simply fails and we fall through to the (re)spawn path below.
	if ag.ActorID != "" {
		if cid, err := identity.ParseCanonicalID(ag.ActorID); err == nil {
			if _, ok := ctx.LookupID(id.From(cid)); ok {
				if ag.LoadState != "loaded" {
					ag = a.mutateAgentAt(idx, func(a *domain.AgentRef) { a.LoadState = "loaded" })
					a.saveOrLog(ctx)
					a.emitAgentsChanged(ctx)
				}
				return ag, true, nil
			}
		}
	}

	// Mark loading before spawn so concurrent callers see the transition.
	ag = a.mutateAgentAt(idx, func(a *domain.AgentRef) { a.LoadState = "loading" })
	a.saveOrLog(ctx)

	// ActorID is the stable identity used when re-spawning the lazy-loaded actor.
	origActorID := ag.ActorID

	var restored domain.AgentRef
	var err error
	if ag.ProjectID == "" {
		restored, err = a.spawnGlobalAgent(ctx, ag.ID, ag.AgentKind, ag.DisplayName, ag.Primary, ag.Fast, ag.Execution, ag.Review, ag.Summary, origActorID, a.globalPermissionMode())
	} else {
		restored, err = a.spawnAgentViaProject(ctx, ag.ProjectID, ag.ID, ag.AgentKind, ag.DisplayName, ag.Primary, ag.Fast, ag.Execution, ag.Review, ag.Summary, origActorID, "", "", nil, nil, ag.ParentAgentID, a.globalPermissionMode())
	}
	if err != nil {
		a.mutateAgentAt(idx, func(a *domain.AgentRef) { a.LoadState = "error" })
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		return domain.AgentRef{}, false, fmt.Errorf("workspace.load_agent: spawn agent %q failed: %w", agentID, err)
	}

	ag = a.mutateAgentAt(idx, func(a *domain.AgentRef) {
		a.ActorID = restored.ActorID
		a.Degraded = false
		a.DegradedReason = ""
		a.LoadState = "loaded"
	})
	a.saveOrLog(ctx)
	a.emitAgentsChanged(ctx)
	// Do not refresh status here: async-started agents call workspace from
	// OnStart, so waiting on agent_status would deadlock both owner loops. The
	// agent pushes its status through workspace.agent_status_update when ready.
	a.emitAgentListState(ctx, false)
	return ag, false, nil
}

// handleLoadAgent is a stateless handler; loadAgentByID's find-by-index,
// spawn, and mutate steps are each individually agentsMu-guarded.
func (a *Actor) handleLoadAgent(ctx actor.PureContext, req gen.WorkspaceLoadAgentReq) (domain.AgentRef, error) {
	return panicprobe.Guard(ctx, "workspace.load_agent", req, func() (domain.AgentRef, error) {
		ag, _, err := a.loadAgentByID(ctx, req.AgentID)
		return ag, err
	})
}

// handleCloneAgent is a stateless handler: snapshot reads, the spawn, the
// registry append, and the async session copy all use agentsMu-guarded state
// and thread-safe PureContext operations; the background goroutine captures
// only immutable capabilities (Lifecycle/Planner/Logger), never the handler
// context itself.
func (a *Actor) handleCloneAgent(ctx actor.PureContext, req domain.WorkspaceCloneAgentReq) (domain.AgentRef, error) {
	return panicprobe.Guard(ctx, "workspace.clone_agent", req, func() (domain.AgentRef, error) {
		if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
			return domain.AgentRef{}, err
		}
		var src domain.AgentRef
		srcFound := false
		for _, ag := range a.agentSnapshot() {
			if ag.ID == req.SourceAgentID {
				src = ag
				srcFound = true
				break
			}
		}
		if !srcFound {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: source agent %q not found", req.SourceAgentID)
		}
		if src.AgentKind == domain.AgentKindCoordinator {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: coordinator is process-unique and cannot be cloned")
		}
		cfg, ok := a.findAgentKindConfig(src.AgentKind)
		if !ok {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: unknown agent kind %q", src.AgentKind)
		}
		if !cfg.UserCreatable {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: agent kind %q is not user-creatable", src.AgentKind)
		}
		if src.ProjectID != "" {
			foundProject := false
			// Pre-scan under mountMu.RLock (handler may run off the owner
			// loop once converted to PureContext).
			a.mountMu.RLock()
			for _, mounted := range a.Mounts {
				if mounted.ActorID == src.ProjectID {
					foundProject = true
					break
				}
			}
			a.mountMu.RUnlock()
			if !foundProject {
				return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: project %q not found", src.ProjectID)
			}
		}

		primary := src.Primary
		if req.Primary != nil {
			primary = req.Primary
		}
		fast := src.Fast
		if req.Fast != nil {
			fast = req.Fast
		}
		execution := src.Execution
		if req.Execution != nil {
			execution = req.Execution
		}
		review := src.Review
		if req.Review != nil {
			review = req.Review
		}
		summary := src.Summary
		if req.Summary != nil {
			summary = req.Summary
		}

		displayName := req.DisplayName
		if displayName == "" {
			displayName = generateDisplayName(cfg.RandomName, a.agentSnapshot(), src.ProjectID)
		}
		if displayName == "" {
			displayName = cfg.DisplayName
		}
		if err := validateNameSegment("workspace.clone_agent", displayName); err != nil {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: %w", err)
		}
		spawnName, err := a.uniqueAgentSpawnName(src.ProjectID, displayName)
		if err != nil {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: %w", err)
		}

		if _, _, err := a.loadAgentByID(ctx, src.ID); err != nil {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: load source agent: %w", err)
		}
		// Refresh src after load (ActorID may have changed).
		src = a.agentAt(func() int {
			for i, ag := range a.agentSnapshot() {
				if ag.ID == req.SourceAgentID {
					return i
				}
			}
			return -1
		}())
		srcActorRef, ok := lookupAgentRef(ctx, src.ActorID)
		if !ok {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: source agent actor %s not found", src.ActorID)
		}

		var ag domain.AgentRef
		if src.ProjectID == "" {
			ag, err = a.spawnGlobalAgent(ctx, spawnName, src.AgentKind, displayName, primary, fast, execution, review, summary, "", a.globalPermissionMode())
		} else {
			ag, err = a.spawnAgentViaProject(ctx, src.ProjectID, spawnName, src.AgentKind, displayName, primary, fast, execution, review, summary, "", "", src.ActorID, nil, nil, "", a.globalPermissionMode())
		}
		if err != nil {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: %w", err)
		}
		compactionPolicy := src.CompactionPolicy
		if cfg.CompactionPolicy != nil {
			cp := *cfg.CompactionPolicy
			compactionPolicy = &cp
		}
		if req.CompactionPolicy != nil {
			cp := *req.CompactionPolicy
			compactionPolicy = &cp
		}
		ag.CompactionPolicy = compactionPolicy
		cloneActorRef, ok := lookupAgentRef(ctx, ag.ActorID)
		if !ok {
			return domain.AgentRef{}, fmt.Errorf("workspace.clone_agent: cloned agent actor %s not found", ag.ActorID)
		}
		a.agentsMu.Lock()
		a.Agents = append(a.Agents, ag)
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
		// Copy the source session off the owner loop. A synchronous
		// cloneSourceSessionInto here blocks the workspace owner loop for up to
		// ~120s (four 30s cross-actor awaits), which overflows the owner queue
		// under load and deadlocks when the agent loop calls back into
		// workspace.get_agent_kind_config. Capture all handler capabilities before
		// returning; the handler context itself must not be used by the goroutine.
		cloneCtx := ctx.Lifecycle()
		planner := ctx.Planner()
		logger := ctx.Logger()
		go func() {
			// Copy the source agent's user-activated component cards (modes,
			// skills, user-mounted bundles) before the session turns so the
			// clone renders with the same active configuration. Goal/workflow
			// mode *cards* are mounted, but their runtime state (active goal,
			// active workflow) is intentionally not copied — see the comment in
			// cloneSourceSessionInto. Worktree mode is system-managed
			// (per-agent worktree binding) and excluded.
			if merr := a.copyCloneComponentMounts(cloneCtx, planner, logger, srcActorRef, cloneActorRef); merr != nil {
				logger.Error("workspace: async clone component mount copy failed",
					"clone", ag.ID, "source", src.ID, "error", merr)
			}
			if cerr := a.cloneSourceSessionInto(cloneCtx, planner, logger, srcActorRef, cloneActorRef, src, ag, req.ForkAtTurnID); cerr != nil {
				logger.Error("workspace: async clone session copy failed",
					"clone", ag.ID, "source", src.ID, "error", cerr)
			}
		}()
		return ag, nil
	})
}

// uniqueAgentSpawnName generates "{displayName}#XXXX" until it does not
// collide with an existing agent inside the same project.
func (a *Actor) uniqueAgentSpawnName(projectID, displayName string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		candidate := displayName + "#" + randomHexSuffix()
		conflict := false
		for _, ag := range a.agentSnapshot() {
			if ag.ProjectID == projectID && ag.ID == candidate {
				conflict = true
				break
			}
		}
		if !conflict {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate unique agent name for %q after retries", displayName)
}

// explainSpawnFailure maps the terminal error shapes gospore's Call.Final
// can produce when spawnCtx expires onto a distinguishable cause. Since
// gospore 9f6ec27, ctx-driven cancellation is translated to the ctx's own
// error (context.Canceled / context.DeadlineExceeded) before surfacing; the
// bare invoke.ErrCallCancelled sentinel survives only for an explicit Cancel
// without a ctx cause, and io.EOF remains the void-End shape. All shapes are
// translated here using spawnCtx.Err() as the tie-breaker.
func explainSpawnFailure(spawnCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	wasCancelled := errors.Is(err, invoke.ErrCallCancelled) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
	if !wasCancelled && !errors.Is(err, io.EOF) {
		return err
	}
	switch spawnCtx.Err() {
	case context.DeadlineExceeded:
		return fmt.Errorf("timed out after %s (project actor did not answer project.spawn_agent in time)", domain.DefaultInvokeTimeout)
	case context.Canceled:
		return errors.New("cancelled (workspace lifecycle shutting down)")
	default:
		if wasCancelled {
			return err
		}
		return errors.New("target closed the call stream without a reply")
	}
}

// spawnAgentViaProject invokes project.spawn_agent on the target project actor.
func (a *Actor) spawnAgentViaProject(ctx actor.PureContext, projectID, spawnName, kind, displayName string, primary, fast, execution, review, summary *domain.ModelSlot, agentActorID, worktreeID, cloneSourceActorID string, extraBundleIDs []string, spawnGoal *domain.AgentInternalAssignGoalReq, parentAgentID string, permissionMode string) (domain.AgentRef, error) {
	var projectActorID string
	var projectAppKind string
	a.mountMu.RLock()
	for _, m := range a.Mounts {
		if m.ActorID == projectID {
			projectActorID = m.ActorID
			projectAppKind = m.AppKind
			break
		}
	}
	a.mountMu.RUnlock()
	if projectActorID == "" {
		return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: project %s not found", projectID)
	}
	cid, err := identity.ParseCanonicalID(projectActorID)
	if err != nil {
		return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: invalid project actor ID: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: project %s not found", projectID)
	}
	spawnCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(spawnCtx, "project.spawn_agent", domain.ProjectSpawnAgentReq{
		SpawnName:          spawnName,
		ProjectID:          projectID,
		AgentKind:          kind,
		WorkspaceID:        a.actorID,
		ActorID:            agentActorID,
		DisplayName:        displayName,
		Primary:            primary,
		Fast:               fast,
		Execution:          execution,
		Review:             review,
		Summary:            summary,
		WorktreeID:         worktreeID,
		CloneSourceActorID: cloneSourceActorID,
		ParentAgentID:      parentAgentID,
		PermissionMode:     permissionMode,
		// extraBundleIDs carries spawn-time bundles inherited from the
		// parent (e.g. plugin-dev when the owner agent mounted it);
		// extraBundlesForAppKind merges them idempotently with the
		// app-kind-derived bundle for dev-app projects.
		ExtraBundleIDs: extraBundlesForAppKind(projectAppKind, extraBundleIDs),
		// Thread spawn-time goal through the spawn chain so the agent
		// self-assigns it during OnStart (avoids cross-actor invoke race).
		GoalCondition:   spawnGoalStr(spawnGoal, func(g *domain.AgentInternalAssignGoalReq) string { return g.Condition }),
		InterpretedGoal: spawnGoalStr(spawnGoal, func(g *domain.AgentInternalAssignGoalReq) string { return g.InterpretedGoal }),
		BoundTaskCardID: spawnGoalStr(spawnGoal, func(g *domain.AgentInternalAssignGoalReq) string { return g.BoundTaskCardID }),
		GoalMaxTurns:    spawnGoalI32(spawnGoal, func(g *domain.AgentInternalAssignGoalReq) int32 { return g.MaxTurns }),
		PromptPrelude:   spawnGoalStr(spawnGoal, func(g *domain.AgentInternalAssignGoalReq) string { return g.PromptPrelude }),
	})
	// Final(spawnCtx) — not Value() — so that spawnCtx cancellation (timeout
	// or lifecycle shutdown) translates into call.Cancel(), unblocking Recv.
	// Value() calls Once(stream) → Recv() with no context awareness, so a
	// slow project.spawn_agent would park the workspace ownerLoop forever.
	defer call.Close()
	v, err := call.Final(spawnCtx)
	if err != nil {
		err = explainSpawnFailure(spawnCtx, err)
		ctx.Logger().Warn("workspace: spawn agent via project failed",
			"projectID", projectID, "spawnName", spawnName, "agentKind", kind, "error", err)
		return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: project.spawn_agent failed: %w", err)
	}
	if v == nil {
		return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: project.spawn_agent returned nil")
	}
	var resp domain.ProjectSpawnAgentResp
	switch x := v.(type) {
	case domain.ProjectSpawnAgentResp:
		resp = x
	case *domain.ProjectSpawnAgentResp:
		if x != nil {
			resp = *x
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: marshal response: %w", err)
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return domain.AgentRef{}, fmt.Errorf("spawnAgentViaProject: unmarshal response: %w", err)
		}
	}
	ag := domain.AgentRef{
		ID:             spawnName,
		ProjectID:      projectID,
		DisplayName:    displayName,
		AgentKind:      kind,
		Status:         "active",
		LoadState:      "loaded",
		ActorID:        resp.ActorID,
		Primary:        primary,
		Fast:           fast,
		Execution:      execution,
		Review:         review,
		Summary:        summary,
		ParentAgentID:  parentAgentID,
		LifecycleScope: "workflow",
	}
	return ag, nil
}

// spawnGlobalAgent spawns an agent directly under the workspace actor
// (no project parent). Used by workspace-global system agents such as the
// Coordinator. The Planner capability is required: these agents dispatch to
// the LLM via planner.Plan, exactly like project-scoped agents
// (project.spawn_agent grants WithPlanner). Without it ctx.Planner()
// returns a typed-nil interface that defeats the turn engine's nil guard
// and surfaces as "plan: spawn not available".
func (a *Actor) spawnGlobalAgent(ctx actor.PureContext, spawnName, kind, displayName string, primary, fast, execution, review, summary *domain.ModelSlot, agentActorID, permissionMode string) (domain.AgentRef, error) {
	props := actor.PropsFromFunc(agentactor.NewActor(
		a.actorID,
		kind,
		displayName,
		derefSlotWS(primary),
		derefSlotWS(fast),
		derefSlotWS(execution),
		derefSlotWS(review),
		derefSlotWS(summary),
		nil, // no spawn-time goal for global agents
		permissionMode,
		"",  // global agents are workspace-root agents with no parent agent
		nil, // no project → no app-directory extra bundles
	)).WithPlanner().WithAsyncStart().
		// RoleAgent stamps the agent's identity on every outbound call
		// (gospore.caller_role header). Internal callees like appmanager
		// treat zero-identity contexts as external (session-token wall);
		// without a role, agent tool calls into apps are rejected.
		WithRole(string(id.RoleAgent))
	if agentActorID != "" {
		cid, err := identity.ParseCanonicalID(agentActorID)
		if err != nil {
			return domain.AgentRef{}, fmt.Errorf("spawnGlobalAgent: invalid ActorId %q: %w", agentActorID, err)
		}
		props = props.WithID(id.From(cid))
	}
	childRef, err := ctx.Spawn(props, spawnName)
	if err != nil {
		return domain.AgentRef{}, fmt.Errorf("spawnGlobalAgent: %w", err)
	}
	// NOTE: we intentionally do NOT block here for OnStart to finish. The
	// agent's OnStart synchronously calls back into the workspace owner loop
	// (fetchAgentKindConfig → workspace.get_agent_kind_config), so blocking the
	// workspace loop until OnStart completes would deadlock. The cold-start
	// readiness race is instead closed in the pure session.summary handler,
	// which waits on the agent's onStartDone signal off the owner loop.
	ag := domain.AgentRef{
		ID:          spawnName,
		ProjectID:   "",
		DisplayName: displayName,
		AgentKind:   kind,
		Status:      "active",
		LoadState:   "loaded",
		ActorID:     childRef.ID().String(),
		Primary:     primary,
		Fast:        fast,
		Execution:   execution,
		Review:      review,
		Summary:     summary,
	}
	return ag, nil
}

// configureAgentActor pushes ModelSlots to a running agent actor. The agent
// actor does not persist these fields itself, so the workspace must re-push
// them after spawn and after restart.
//
// Fire-and-forget: a synchronous planner.Call().Await() here blocks the
// workspace owner loop while waiting for the agent owner loop to process
// agent.configure. But the agent owner loop may itself be blocked in
// startTurnWithName, which calls back to workspace.get_agent_kind_config —
// creating a circular wait that deadlocks both owner loops for up to 10-15s.
// Using Invoke (async) breaks the cycle.
// configureAgentActor takes PureContext: it only needs Lifecycle for the
// fire-and-forget agent_configure push.
func (a *Actor) configureAgentActor(ctx actor.PureContext, agentRef ref.Ref, ag domain.AgentRef) {
	_ = agentRef.Invoke(ctx.Lifecycle(), "agent_configure", domain.AgentConfigureReq{
		Primary:   ag.Primary,
		Fast:      ag.Fast,
		Execution: ag.Execution,
		Review:    ag.Review,
		Summary:   ag.Summary,
		Title:     ag.Title,
	})
}

// cloneSourceSessionInto copies the source agent's session into the cloned agent
// in chunks. The most recent 5 turns are copied synchronously so the clone is
// immediately usable; remaining history is copied in the background.
//
// When forkAtTurnId is non-empty, a fork snapshot (truncated at that turn) is
// imported instead of the full session history.
func (a *Actor) cloneSourceSessionInto(ctx context.Context, planner actor.Planner, logger actor.Logger, srcRef, cloneRef ref.Ref, src, cloned domain.AgentRef, forkAtTurnId string) error {
	// Fork mode: import a truncated session snapshot up to forkAtTurnId.
	if forkAtTurnId != "" {
		forkCtx, cancelFork := context.WithTimeout(ctx, 30*time.Second)
		defer cancelFork()
		raw, err := cloneCall(forkCtx, planner, srcRef, "session_fork",
			domain.AgentSessionForkReq{AtTurnID: forkAtTurnId})
		if err != nil {
			return fmt.Errorf("agent.session.fork on source: %w", err)
		}
		fork, ok := raw.(domain.AgentSessionForkResp)
		if !ok {
			return fmt.Errorf("agent.session.fork returned %T", raw)
		}
		importCtx, cancelImport := context.WithTimeout(ctx, 30*time.Second)
		defer cancelImport()
		// The clone must not inherit the source agent's active goal. The goal is
		// runtime state owned by the mounted `builtin:mode:goal` component; the
		// freshly spawned clone re-mounts that component (via seedBuiltinComponentMounts)
		// and should start goal-free so the user assigns its own goal.
		_, err = cloneCall(importCtx, planner, cloneRef, "session_import",
			domain.AgentSessionImportReq{
				Session:         fork.Session,
				SummarySegments: fork.SummarySegments,
				ExploreResults:  fork.ExploreResults,
				Steps:           fork.Steps,
				NextIdx:         fork.NextIdx,
				NextSeq:         fork.NextSeq,
				NextTurnOrder:   fork.NextTurnOrder,
			})
		if err != nil {
			return fmt.Errorf("agent.session.import on clone (fork): %w", err)
		}
		return nil
	}

	// Full-copy mode: copy the most recent 5 turns synchronously so the clone can render.
	exportCtx, cancelExport := context.WithTimeout(ctx, 30*time.Second)
	defer cancelExport()
	raw, err := cloneCall(exportCtx, planner, srcRef, "session_export_range",
		domain.AgentSessionExportRangeReq{Limit: 5})
	if err != nil {
		return fmt.Errorf("agent.session.export_range on source: %w", err)
	}
	export, ok := raw.(domain.AgentSessionExportRangeResp)
	if !ok {
		return fmt.Errorf("agent.session.export_range returned %T", raw)
	}
	if len(export.Turns) == 0 {
		return nil
	}

	importCtx, cancelImport := context.WithTimeout(ctx, 30*time.Second)
	defer cancelImport()
	if _, err := cloneCall(importCtx, planner, cloneRef, "session_import_turns",
		domain.AgentSessionImportTurnsReq{
			Turns:            export.Turns,
			Steps:            export.Steps,
			SummarySegments:  export.SummarySegments,
			ExploreResults:   export.ExploreResults,
			SourceAgentID:    src.ActorID,
			SourceTotalTurns: export.TotalTurns,
			NextSeq:          export.NextSeq,
			NextIdx:          export.NextIdx,
			IsFinal:          !export.HasMore,
		}); err != nil {
		return fmt.Errorf("agent.session.import_turns on clone: %w", err)
	}

	if !export.HasMore {
		return nil
	}

	a.clonePersistMu.Lock()
	if a.clones == nil {
		a.clones = make(map[string]domain.CloneState)
	}
	a.clones[cloned.ActorID] = domain.CloneState{
		SourceActorID:    src.ActorID,
		SourceTotalTurns: export.TotalTurns,
		PendingHistory:   true,
		SourceNextSeq:    export.NextSeq,
		SourceNextIdx:    export.NextIdx,
	}
	a.clonePersistMu.Unlock()
	if err := a.saveCloneState(); err != nil {
		logger.Error("workspace: failed to save clone state", "error", err)
	}

	go a.copyCloneHistoryInBackground(ctx, planner, srcRef, cloneRef, src.ActorID, cloned.ActorID, export.NextBeforeTurnID)
	return nil
}

// copyCloneComponentMounts mounts the source agent's user-activated component
// cards (mode cards such as builtin:mode:goal / builtin:mode:workflow, plus
// user-mounted skills and bundles) onto the freshly spawned clone. Without
// this the clone only carries the kind config's default builtin mounts and
// loses the agent's active modes.
//
// Scope rules: only scope "user" mounts are copied. "builtin" mounts are
// re-seeded by the clone itself (seedBuiltinComponentMounts / kind config) and
// "dependency" mounts are re-mounted automatically as dependencies of the mode
// card being mounted. Disabled mounts are skipped — the mount callable cannot
// preserve a disabled state on a fresh agent. builtin:mode:worktree is
// excluded because it is system-managed: mounting it would bind the clone to
// a new worktree (enterWorktreeMode), which a clone must not do implicitly.
func (a *Actor) copyCloneComponentMounts(ctx context.Context, planner actor.Planner, logger actor.Logger, srcRef, cloneRef ref.Ref) error {
	listCtx, cancelList := context.WithTimeout(ctx, 30*time.Second)
	defer cancelList()
	raw, err := cloneCall(listCtx, planner, srcRef, "component_list", domain.AgentComponentListReq{})
	if err != nil {
		return fmt.Errorf("agent.component.list on source: %w", err)
	}
	list, ok := raw.(domain.AgentComponentListResp)
	if !ok {
		return fmt.Errorf("agent.component.list returned %T", raw)
	}
	for _, m := range list.Items {
		if m.Scope != "user" || !m.Enabled || m.CardID == "builtin:mode:worktree" {
			continue
		}
		mountCtx, cancelMount := context.WithTimeout(ctx, 30*time.Second)
		_, err := cloneCall(mountCtx, planner, cloneRef, "component_mount", domain.AgentComponentMountReq{
			CardID:  m.CardID,
			Enabled: true,
			Order:   m.Order,
			Scope:   "user",
		})
		cancelMount()
		if err != nil {
			// A single uncloneable card (deleted since mount, flow conflict)
			// must not abort copying the remaining cards.
			logger.Warn("workspace: clone component mount skipped",
				"card", m.CardID, "error", err)
			continue
		}
	}
	return nil
}

func cloneCall(ctx context.Context, planner actor.Planner, target ref.Ref, callID string, payload any) (any, error) {
	if planner != nil {
		return planner.Call(ctx, target, callID, payload).Await()
	}
	call := target.Invoke(ctx, callID, payload)
	if call == nil {
		return nil, fmt.Errorf("%s returned nil call", callID)
	}
	return call.Final(ctx)
}

// copyCloneHistoryInBackground copies remaining source turns into the cloned
// agent in chunks. It runs in a detached goroutine and stops when the context is
// cancelled, the source runs out of turns, or an error occurs.
func (a *Actor) copyCloneHistoryInBackground(ctx context.Context, planner actor.Planner, srcRef, cloneRef ref.Ref, sourceActorID, cloneActorID, firstBeforeTurnID string) {
	const chunkSize = 20
	beforeTurnID := firstBeforeTurnID
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		exportCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		raw, err := cloneCall(exportCtx, planner, srcRef, "session_export_range",
			domain.AgentSessionExportRangeReq{BeforeTurnID: beforeTurnID, Limit: chunkSize})
		cancel()
		if err != nil {
			return
		}
		export, ok := raw.(domain.AgentSessionExportRangeResp)
		if !ok || len(export.Turns) == 0 {
			return
		}

		importCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = cloneCall(importCtx, planner, cloneRef, "session_import_turns",
			domain.AgentSessionImportTurnsReq{
				Turns:           export.Turns,
				Steps:           export.Steps,
				SummarySegments: export.SummarySegments,
				ExploreResults:  export.ExploreResults,
				IsFinal:         !export.HasMore,
			})
		cancel()
		if err != nil {
			return
		}

		a.clonePersistMu.Lock()
		state, tracked := a.clones[cloneActorID]
		if tracked {
			if export.HasMore {
				state.PendingHistory = true
				a.clones[cloneActorID] = state
			} else {
				delete(a.clones, cloneActorID)
			}
		}
		a.clonePersistMu.Unlock()
		if tracked {
			_ = a.saveCloneState()
		}
		if !tracked || !export.HasMore {
			return
		}
		beforeTurnID = export.NextBeforeTurnID
	}
}

// resumePendingClones restarts background history copy for clones that were
// interrupted by a process restart. It is called once during OnStart.
// actorIDToAgentID maps the pre-restart actor IDs (stored in clone state) to
// the corresponding agent IDs, because project-based agents are lazy-loaded
// and their ActorID fields are cleared on startup.
func (a *Actor) agentIDByActorID(actorID string) (string, bool) {
	for _, agent := range a.agentSnapshot() {
		if agent.ActorID == actorID {
			return agent.ID, true
		}
	}
	return "", false
}

func (a *Actor) resumePendingClones(ctx actor.Context, actorIDToAgentID map[string]string) {
	type resumeJob struct {
		cloneActorID string
		state        domain.CloneState
		srcRef       ref.Ref
		cloneRef     ref.Ref
	}

	a.clonePersistMu.Lock()
	pending := make(map[string]domain.CloneState, len(a.clones))
	for cloneActorID, state := range a.clones {
		pending[cloneActorID] = state
	}
	a.clonePersistMu.Unlock()

	var jobs []resumeJob
	for cloneActorID, state := range pending {
		if !state.PendingHistory {
			a.clonePersistMu.Lock()
			delete(a.clones, cloneActorID)
			a.clonePersistMu.Unlock()
			continue
		}

		srcAgentID, ok := actorIDToAgentID[state.SourceActorID]
		if !ok {
			srcAgentID, ok = a.agentIDByActorID(state.SourceActorID)
		}
		if !ok {
			ctx.Logger().Error("workspace: clone source actor not found; clearing clone state", "sourceActorID", state.SourceActorID)
			a.clonePersistMu.Lock()
			delete(a.clones, cloneActorID)
			a.clonePersistMu.Unlock()
			continue
		}
		cloneAgentID, ok := actorIDToAgentID[cloneActorID]
		if !ok {
			cloneAgentID, ok = a.agentIDByActorID(cloneActorID)
		}
		if !ok {
			ctx.Logger().Error("workspace: cloned agent actor not found; clearing clone state", "cloneActorID", cloneActorID)
			a.clonePersistMu.Lock()
			delete(a.clones, cloneActorID)
			a.clonePersistMu.Unlock()
			continue
		}

		srcAgent, _, err := a.loadAgentByID(ctx, srcAgentID)
		if err != nil {
			ctx.Logger().Error("workspace: failed to load clone source agent", "sourceAgentID", srcAgentID, "error", err)
			continue
		}
		srcRef, ok := lookupAgentRef(ctx, srcAgent.ActorID)
		if !ok {
			ctx.Logger().Error("workspace: clone source actor ref not found after load", "sourceAgentID", srcAgentID, "actorID", srcAgent.ActorID)
			continue
		}
		cloneAgent, _, err := a.loadAgentByID(ctx, cloneAgentID)
		if err != nil {
			ctx.Logger().Error("workspace: failed to load cloned agent", "cloneAgentID", cloneAgentID, "error", err)
			continue
		}
		cloneRef, ok := lookupAgentRef(ctx, cloneAgent.ActorID)
		if !ok {
			ctx.Logger().Error("workspace: cloned agent actor ref not found after load", "cloneAgentID", cloneAgentID, "actorID", cloneAgent.ActorID)
			continue
		}
		state.SourceActorID = srcAgent.ActorID
		a.clonePersistMu.Lock()
		delete(a.clones, cloneActorID)
		a.clones[cloneAgent.ActorID] = state
		a.clonePersistMu.Unlock()
		jobs = append(jobs, resumeJob{cloneActorID: cloneAgent.ActorID, state: state, srcRef: srcRef, cloneRef: cloneRef})
	}

	if err := a.saveCloneState(); err != nil {
		ctx.Logger().Error("workspace: failed to save clone state after resume", "error", err)
	}

	lifecycle := ctx.Lifecycle()
	planner := ctx.Planner()
	logger := ctx.Logger()
	for _, job := range jobs {
		go a.resumeCloneHistory(lifecycle, planner, logger, job.srcRef, job.cloneRef, job.state, job.cloneActorID)
	}
}

func (a *Actor) resumeCloneHistory(ctx context.Context, planner actor.Planner, logger actor.Logger, srcRef, cloneRef ref.Ref, state domain.CloneState, cloneActorID string) {
	reactCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, err := cloneCall(reactCtx, planner, cloneRef, "session_import_turns", domain.AgentSessionImportTurnsReq{
		SourceAgentID:    state.SourceActorID,
		SourceTotalTurns: state.SourceTotalTurns,
		NextSeq:          state.SourceNextSeq,
		NextIdx:          state.SourceNextIdx,
	})
	cancel()
	if err != nil {
		logger.Error("workspace: failed to reactivate clone agent", "cloneActorID", cloneActorID, "error", err)
		return
	}

	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	raw, err := cloneCall(getCtx, planner, cloneRef, "session_get", nil)
	cancel()
	if err != nil {
		logger.Error("workspace: failed to get clone session for resume", "cloneActorID", cloneActorID, "error", err)
		return
	}
	session, ok := raw.(domain.AgentGetSessionResp)
	if !ok {
		logger.Error("workspace: unexpected session response type", "cloneActorID", cloneActorID, "type", fmt.Sprintf("%T", raw))
		return
	}
	var beforeTurnID string
	if len(session.Turns) > 0 {
		beforeTurnID = session.Turns[0].ID
	}
	a.copyCloneHistoryInBackground(ctx, planner, srcRef, cloneRef, state.SourceActorID, cloneActorID, beforeTurnID)
}

func lookupAgentRef(ctx actor.PureContext, actorID string) (ref.Ref, bool) {
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return nil, false
	}
	return ctx.LookupID(id.From(cid))
}

// deleteAgentState removes an agent actor's persisted state directory
// (<ActorDataDir>/agent/<actorID>, including turns/steps/snapshots).
// The workspace store is rooted at .actors/workspace, so agent state must
// be deleted through an agent-prefixed store.
func (a *Actor) deleteAgentState(actorID string) {
	if actorID == "" {
		return
	}
	_ = persist.MustNew(config.PersistConfig("agent")).Delete(actorID)
}

// stopAgentsForProject marks all agents for a project as deleting and kicks off
// non-blocking cascade teardown. Previously this did synchronous ctx.Destroy
// calls in the workspace owner loop, which blocked on slow turns. Now it
// delegates to the unified async cascade-delete path.
func (a *Actor) stopAgentsForProject(ctx actor.PureContext, projectID string) {
	var subtree []domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.ProjectID == projectID {
			subtree = append(subtree, ag)
		}
	}
	if len(subtree) == 0 {
		return
	}
	a.cascadeDelete(ctx, subtree)
}

func (a *Actor) handleCurrentAccount(ctx actor.PureContext) (domain.AccountSnapshot, error) {
	id := ctx.Identity()
	accountID := id.Subject
	if accountID == "" {
		accountID = "anonymous"
	}
	role := string(id.Role)
	if role == "" {
		role = "anonymous"
	}
	return domain.AccountSnapshot{
		AccountID:   accountID,
		DisplayName: accountID,
		Roles:       []string{role},
		DefaultActor: domain.ActorContextSnapshot{
			ActorID:         a.actorID,
			ActorType:       "human",
			SessionID:       ctx.CallID(),
			TrustLevel:      role,
			AuthenticatedAs: accountID,
		},
	}, nil
}

func (a *Actor) handleCurrentSession(ctx actor.PureContext) (domain.SessionSnapshot, error) {
	id := ctx.Identity()
	accountID := id.Subject
	if accountID == "" {
		accountID = "anonymous"
	}
	role := string(id.Role)
	if role == "" {
		role = "anonymous"
	}
	return domain.SessionSnapshot{
		SessionID:    ctx.CallID(),
		ConnectionID: a.actorID,
		AccountID:    accountID,
		ClientKind:   "web",
		DisplayName:  accountID,
		TrustLevel:   role,
		ActorType:    "human",
		Online:       true,
	}, nil
}

func (a *Actor) handleGetAccountPreferences(ctx actor.PureContext) (domain.AccountPreferencesSnapshot, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.accountPrefs.Preferences == nil {
		return domain.AccountPreferencesSnapshot{
			AccountID:   "root",
			Version:     1,
			Preferences: map[string]string{},
		}, nil
	}
	return a.accountPrefs, nil
}

func (a *Actor) handleSaveAccountPreferences(ctx actor.PureContext, req domain.SaveAccountPreferencesCommand) (domain.AccountPreferencesSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accountPrefs = domain.AccountPreferencesSnapshot{
		AccountID:   req.AccountID,
		Version:     req.Version,
		Preferences: req.Preferences,
	}
	a.saveAccountPrefsOrLog(ctx)
	return a.accountPrefs, nil
}

// shellGitBashRecordKey records in account preferences whether git-bash was
// available during the previous run. Auto-adoption of git-bash fires only on
// the absent→present transition between runs, so an installation that already
// existed never overrides a manual shell choice.
const shellGitBashRecordKey = "shell_gitbash_present"

// decideGitBashAdoption is the pure decision for git-bash auto-adoption:
// adopt only when the previous run recorded git-bash as absent, it is present
// now, and the user has no explicit shell preference.
func decideGitBashAdoption(prevRecordedPresent, present bool, shellPref string) bool {
	return !prevRecordedPresent && present && shellPref == ""
}

// reconcileShellAdoption runs at workspace load (Windows only): it records
// current git-bash availability and, on a fresh absent→present transition
// with no explicit shell preference, adopts git-bash as the preferred shell.
// Returns true when the shell preference changed.
func (a *Actor) reconcileShellAdoption() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	present := false
	for _, c := range util.DetectShells() {
		if c.Kind == util.ShellGitBash && c.Available {
			present = true
			break
		}
	}
	if a.accountPrefs.Preferences == nil {
		a.accountPrefs.Preferences = map[string]string{}
	}
	prev := a.accountPrefs.Preferences[shellGitBashRecordKey] == "1"
	adopted := decideGitBashAdoption(prev, present, a.accountPrefs.Preferences["shell"])
	if adopted {
		a.accountPrefs.Preferences["shell"] = string(util.ShellGitBash)
	}
	if prev == present && !adopted {
		return false
	}
	a.accountPrefs.Preferences[shellGitBashRecordKey] = map[bool]string{true: "1", false: "0"}[present]
	// Best-effort persist, mirroring the migration write in Load: OnStop's
	// Save() retries on failure.
	_ = a.saveAccountPrefsCard()
	return adopted
}

// applyShellPreference reads the persisted "shell" preference from account
// preferences and injects it into util so that DetectShellInfo returns the
// user's chosen shell. Called on load and after a preference save.
func (a *Actor) applyShellPreference() {
	if a.accountPrefs.Preferences == nil {
		return
	}
	util.SetShellPreference(util.ShellKind(a.accountPrefs.Preferences["shell"]))
}

// refreshShellEnvironmentLocked rewrites the canonical os/shell_* keys in
// every agent kind config's EnvironmentContext from the current detection
// result (which reflects the user's shell preference), preserving extra
// user-defined keys. Without this, a shell preference change would only
// reach the agent prompt on the next workspace start (seedAgentKindConfigs),
// leaving the injected Environment block stale for the running session.
// Caller must hold a.mu (account preferences); the kind-config rewrite takes
// kindConfigsMu itself so the a.mu → kindConfigsMu ordering is the only
// nesting direction.
func (a *Actor) refreshShellEnvironmentLocked() {
	envCtx := util.DetectEnvironment()
	a.kindConfigsMu.Lock()
	defer a.kindConfigsMu.Unlock()
	for i := range a.AgentKindConfigs {
		merged := make(map[string]string, len(a.AgentKindConfigs[i].EnvironmentContext)+len(envCtx))
		for k, v := range a.AgentKindConfigs[i].EnvironmentContext {
			merged[k] = v
		}
		for k, v := range envCtx {
			merged[k] = v
		}
		a.AgentKindConfigs[i].EnvironmentContext = merged
	}
}

func (a *Actor) handleShellEnvProbe(ctx actor.PureContext) (domain.ShellEnvProbeResp, error) {
	candidates := util.DetectShells()
	out := make([]domain.ShellCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, domain.ShellCandidate{
			Kind:       string(c.Kind),
			Executable: c.Executable,
			ExtraBin:   c.ExtraBin,
			Available:  c.Available,
		})
	}
	info := util.DetectShellInfo()
	kind := util.ShellKind(info.Name)
	if k := util.ShellKindFromInfo(info); k != "" {
		kind = k
	}
	current := domain.ShellCandidate{
		Kind:       string(kind),
		Executable: info.Executable,
		ExtraBin:   info.ExtraBin,
		Available:  info.IsAvailable(),
	}
	return domain.ShellEnvProbeResp{
		Current:    current,
		Candidates: out,
		Platform:   runtime.GOOS,
	}, nil
}

func (a *Actor) handleShellPrefSave(ctx actor.PureContext, req domain.ShellPrefSaveReq) (domain.ShellPrefSaveResp, error) {
	a.mu.Lock()
	if a.accountPrefs.Preferences == nil {
		a.accountPrefs.Preferences = map[string]string{}
	}
	a.accountPrefs.Preferences["shell"] = req.Kind
	a.applyShellPreference()
	a.refreshShellEnvironmentLocked()
	a.saveAccountPrefsOrLog(ctx)
	a.saveAgentKindConfigsOrLog(ctx)
	a.mu.Unlock()

	util.SetShellPreference(util.ShellKind(req.Kind))
	info := util.DetectShellInfo()
	kind := util.ShellKind(info.Name)
	if k := util.ShellKindFromInfo(info); k != "" {
		kind = k
	}
	return domain.ShellPrefSaveResp{
		Kind: req.Kind,
		Current: domain.ShellCandidate{
			Kind:       string(kind),
			Executable: info.Executable,
			ExtraBin:   info.ExtraBin,
			Available:  info.IsAvailable(),
		},
	}, nil
}

func (a *Actor) handleGetWorkspaceUI(ctx actor.PureContext) (domain.WorkspaceUIModel, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.UI.WorkspaceID == "" {
		return domain.WorkspaceUIModel{
			WorkspaceID:   "default",
			Version:       1,
			SchemaVersion: 1,
		}, nil
	}
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspaceLayout(ctx actor.PureContext, req domain.SaveWorkspaceLayoutCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.Layout = req.Layout
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspacePanels(ctx actor.PureContext, req domain.SaveWorkspacePanelsCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.Panels = req.Panels
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspaceDock(ctx actor.PureContext, req domain.SaveWorkspaceDockCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.Dock = req.Dock
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspaceAIShell(ctx actor.PureContext, req domain.SaveWorkspaceAIShellCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.AIShell = req.AIShell
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspaceProjectCardBrowser(ctx actor.PureContext, req domain.SaveWorkspaceProjectCardBrowserCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.ProjectCardBrowser = req.ProjectCardBrowser
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

func (a *Actor) handleSaveWorkspaceExplorer(ctx actor.PureContext, req domain.SaveWorkspaceExplorerCommand) (domain.WorkspaceUIModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.UI.WorkspaceID == "" {
		a.UI.WorkspaceID = "default"
		a.UI.Version = firstPositive(a.UI.Version, 1)
		a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	}
	a.UI.WorkspaceID = firstNonEmpty(req.WorkspaceID, a.UI.WorkspaceID, "default")
	a.UI.Version = firstPositive(req.Version, a.UI.Version, 1)
	a.UI.SchemaVersion = firstPositive(a.UI.SchemaVersion, 1)
	a.UI.Explorer = req.Explorer
	a.saveUIOrLog(ctx)
	return a.UI, nil
}

// ── git helpers ──

func (a *Actor) findMountPath(projectID string) (string, error) {
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	if projectID != "" {
		for _, m := range a.Mounts {
			if m.ActorID == projectID {
				return m.Path, nil
			}
		}
		for _, m := range a.Mounts {
			if strings.EqualFold(m.Name, projectID) {
				return m.Path, nil
			}
		}
	}
	if len(a.Mounts) > 0 {
		return a.Mounts[0].Path, nil
	}
	return "", fmt.Errorf("project %q not found", projectID)
}

func openGitRepo(path string) (*git.Repository, error) {
	return git.PlainOpen(path)
}

// gitCLIRun executes `git <args...>` with cwd=dir and returns combined output.
// Used instead of go-git's Worktree.Add, which runs a full-worktree Status()
// per call (measured ~6s on the main repo vs ~30ms for `git add`).
func gitCLIRun(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := util.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return string(out), fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), 120*time.Second)
		}
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(string(out)))
	}
	return string(out), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// diffOp represents a single line-level diff operation.
type diffOp struct {
	kind    string // "eq", "add", "del"
	oldLine int
	newLine int
	text    string
}

// lcsDiff computes a line-level diff using simplified LCS (Dynamic Programming).
// Falls back to greedy diff for very large files to avoid OOM.
func lcsDiff(oldLines, newLines []string) []diffOp {
	m, n := len(oldLines), len(newLines)
	const maxDiffSize = 5000
	if m > maxDiffSize || n > maxDiffSize {
		return greedyDiff(oldLines, newLines)
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var ops []diffOp
	var backtrack func(i, j int)
	backtrack = func(i, j int) {
		if i == 0 && j == 0 {
			return
		}
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			backtrack(i-1, j-1)
			ops = append(ops, diffOp{kind: "eq", oldLine: i - 1, newLine: j - 1, text: oldLines[i-1]})
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			backtrack(i, j-1)
			ops = append(ops, diffOp{kind: "add", newLine: j - 1, text: newLines[j-1]})
		} else {
			backtrack(i-1, j)
			ops = append(ops, diffOp{kind: "del", oldLine: i - 1, text: oldLines[i-1]})
		}
	}
	backtrack(m, n)
	return ops
}

// greedyDiff is a fast fallback for very large files.
func greedyDiff(oldLines, newLines []string) []diffOp {
	var ops []diffOp
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			ops = append(ops, diffOp{kind: "eq", oldLine: i, newLine: j, text: oldLines[i]})
			i++
			j++
		} else {
			found := -1
			for k := j + 1; k < len(newLines) && k < j+10; k++ {
				if newLines[k] == oldLines[i] {
					found = k
					break
				}
			}
			if found >= 0 {
				for ; j < found; j++ {
					ops = append(ops, diffOp{kind: "add", newLine: j, text: newLines[j]})
				}
			} else {
				ops = append(ops, diffOp{kind: "del", oldLine: i, text: oldLines[i]})
				i++
			}
		}
	}
	for ; i < len(oldLines); i++ {
		ops = append(ops, diffOp{kind: "del", oldLine: i, text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		ops = append(ops, diffOp{kind: "add", newLine: j, text: newLines[j]})
	}
	return ops
}

// unifiedDiff generates a unified-diff string slice from two text contents.
func unifiedDiff(oldContent, newContent, oldFile, newFile string) []string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	if len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
		newLines = newLines[:len(newLines)-1]
	}

	ops := lcsDiff(oldLines, newLines)

	var result []string
	result = append(result, fmt.Sprintf("--- a/%s", oldFile))
	result = append(result, fmt.Sprintf("+++ b/%s", newFile))

	var changeIdx []int
	for i, op := range ops {
		if op.kind != "eq" {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return result
	}

	const ctx = 3
	var groups [][]int
	var cur []int
	for _, idx := range changeIdx {
		if len(cur) == 0 || idx-cur[len(cur)-1] <= 2*ctx {
			cur = append(cur, idx)
		} else {
			groups = append(groups, cur)
			cur = []int{idx}
		}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}

	for _, grp := range groups {
		start := grp[0] - ctx
		if start < 0 {
			start = 0
		}
		end := grp[len(grp)-1] + ctx
		if end >= len(ops) {
			end = len(ops) - 1
		}

		oldStart, newStart := -1, -1
		oldCnt, newCnt := 0, 0
		var lines []string
		for i := start; i <= end; i++ {
			op := ops[i]
			switch op.kind {
			case "eq":
				if oldStart < 0 {
					oldStart = op.oldLine
					newStart = op.newLine
				}
				lines = append(lines, " "+op.text)
				oldCnt++
				newCnt++
			case "add":
				if oldStart < 0 {
					oldStart = op.oldLine
					if oldStart < 0 {
						oldStart = 0
					}
					newStart = op.newLine
				}
				if newStart < 0 {
					newStart = op.newLine
				}
				lines = append(lines, "+"+op.text)
				newCnt++
			case "del":
				if oldStart < 0 {
					oldStart = op.oldLine
				}
				if newStart < 0 {
					newStart = op.newLine
					if newStart < 0 {
						newStart = 0
					}
				}
				lines = append(lines, "-"+op.text)
				oldCnt++
			}
		}
		if oldStart < 0 {
			oldStart = 0
		}
		if newStart < 0 {
			newStart = 0
		}
		result = append(result, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart+1, oldCnt, newStart+1, newCnt))
		result = append(result, lines...)
	}
	return result
}

func (a *Actor) handleGitStatus(ctx actor.PureContext, req domain.WorkspaceGitStatusReq) (domain.WorkspaceGitStatusResp, error) {
	// Serve from cache when fresh. worktree.Status() walks the entire
	// repo via merkle trie; without this cache every UI refresh re-walks
	// the tree and accumulates garbage that the user sees as memory
	// growth. The TTL is short so file changes surface quickly.
	a.gitStatusCacheMu.Lock()
	if a.gitStatusCache == nil {
		a.gitStatusCache = make(map[string]gitStatusCacheEntry)
	}
	if entry, ok := a.gitStatusCache[req.ProjectID+"::"+req.WorktreeID]; ok && time.Since(entry.at) < gitStatusCacheTTL {
		a.gitStatusCacheMu.Unlock()
		return entry.resp, nil
	}
	a.gitStatusCacheMu.Unlock()

	resp, err := a.computeGitStatus(ctx, req)
	if err != nil {
		return resp, err
	}

	a.gitStatusCacheMu.Lock()
	a.gitStatusCache[req.ProjectID+"::"+req.WorktreeID] = gitStatusCacheEntry{at: time.Now(), resp: resp}
	// Drop stale entries so the map doesn't grow unboundedly across
	// project churn.
	for pid, e := range a.gitStatusCache {
		if time.Since(e.at) > gitStatusCacheTTL*4 {
			delete(a.gitStatusCache, pid)
		}
	}
	a.gitStatusCacheMu.Unlock()
	return resp, nil
}

func (a *Actor) computeGitStatus(ctx actor.PureContext, req domain.WorkspaceGitStatusReq) (domain.WorkspaceGitStatusResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitStatusResp{}, err
	}
	// The CLI path is required for worktree-routed calls: go-git's
	// repo.Head() returns "reference not found" on linked worktrees (the
	// worktree HEAD lives under .git/worktrees/<id>/HEAD), so branch/status
	// resolution goes through git.
	if !gitRepoAt(path) {
		return domain.WorkspaceGitStatusResp{IsGit: false}, nil
	}
	branch := gitCLIOut(path, "rev-parse", "--abbrev-ref", "HEAD")
	// -z: NUL-terminated records, never quoted or escaped. Newline-mode
	// porcelain double-quotes non-ASCII paths, which used to render octal
	// escapes (e.g. CJK filenames) in the git panel and dropdown.
	statusZ, _ := gitCLIRaw(path, "--no-optional-locks", "status", "-z", "--porcelain")
	files := parseGitPorcelainZ(statusZ)
	isClean := len(files) == 0
	return domain.WorkspaceGitStatusResp{
		Branch:  branch,
		Files:   files,
		IsClean: isClean,
		IsGit:   true,
	}, nil
}

func (a *Actor) handleGitLog(ctx actor.PureContext, req domain.WorkspaceGitLogReq) (domain.WorkspaceGitLogResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitLogResp{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	args := []string{"log", "--format=%H|%h|%s|%an|%ae|%at|%P", "-n", strconv.Itoa(int(limit))}
	if req.All {
		args = append(args, "--all")
	} else if req.Branch != "" {
		args = append(args, req.Branch)
	}
	out := gitCLIOut(path, args...)
	var commits []domain.GitCommitInfo
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 7)
		if len(parts) < 6 {
			continue
		}
		hash := parts[1]
		if len(hash) > 7 {
			hash = hash[:7]
		}
		var parents []string
		if len(parts) >= 7 && parts[6] != "" {
			parents = strings.Fields(parts[6])
		}
		commits = append(commits, domain.GitCommitInfo{
			Hash:    parts[0],
			Short:   hash,
			Message: parts[2],
			Author:  parts[3],
			Email:   parts[4],
			When:    parts[5],
			Parents: parents,
		})
	}
	return domain.WorkspaceGitLogResp{Commits: commits}, nil
}

func parentHashes(c *object.Commit) []string {
	out := make([]string, 0, len(c.ParentHashes))
	for _, h := range c.ParentHashes {
		out = append(out, h.String())
	}
	return out
}

func (a *Actor) handleGitDiff(ctx actor.PureContext, req domain.WorkspaceGitDiffReq) (domain.WorkspaceGitDiffResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitDiffResp{}, err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return domain.WorkspaceGitDiffResp{}, err
	}

	if req.CommitHash != "" {
		var args []string
		if req.BaseCommitHash != "" {
			args = []string{"diff", req.BaseCommitHash, req.CommitHash, "--"}
		} else {
			args = []string{"diff", req.CommitHash, "--"}
		}
		if req.FilePath != "" {
			args = append(args, req.FilePath)
		}
		out, err := gitCLI(path, args...)
		if err != nil {
			return domain.WorkspaceGitDiffResp{}, fmt.Errorf("git diff failed: %w", err)
		}
		diff := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(diff) == 1 && diff[0] == "" {
			diff = []string{}
		}
		return domain.WorkspaceGitDiffResp{Diff: diff}, nil
	}

	// Working-tree diff (index vs working tree)
	worktree, err := repo.Worktree()
	if err != nil {
		return domain.WorkspaceGitDiffResp{}, err
	}
	status, err := worktree.Status()
	if err != nil {
		return domain.WorkspaceGitDiffResp{}, err
	}

	diff := make([]string, 0)
	for filePath, s := range status {
		if req.FilePath != "" && filePath != req.FilePath {
			continue
		}
		if s.Worktree == ' ' {
			continue // no unstaged changes
		}

		diff = append(diff, fmt.Sprintf("diff --git a/%s b/%s", filePath, filePath))

		// Read old content from index
		idx, err := repo.Storer.Index()
		if err != nil {
			continue
		}
		var oldContent string
		entry, err := idx.Entry(filePath)
		if err == nil {
			blob, err := repo.BlobObject(entry.Hash)
			if err == nil {
				r, err := blob.Reader()
				if err == nil {
					data, _ := io.ReadAll(r)
					r.Close()
					oldContent = string(data)
				}
			}
		}

		// Read new content from working tree
		newPath := filepath.Join(path, filePath)
		newData, err := os.ReadFile(newPath)
		if err != nil {
			continue
		}
		newContent := string(newData)

		diff = append(diff, unifiedDiff(oldContent, newContent, filePath, filePath)...)
	}
	return domain.WorkspaceGitDiffResp{Diff: diff}, nil
}

func (a *Actor) handleGitAdd(ctx actor.PureContext, req domain.WorkspaceGitAddReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	if len(req.Paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, req.Paths...)
	if _, err := gitCLIRun(path, args...); err != nil {
		return fmt.Errorf("workspace.git_add: %w", err)
	}
	return nil
}

func (a *Actor) handleGitCommit(ctx actor.PureContext, req domain.WorkspaceGitCommitReq) (domain.WorkspaceGitCommitResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitCommitResp{}, err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return domain.WorkspaceGitCommitResp{}, err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return domain.WorkspaceGitCommitResp{}, err
	}
	hash, err := worktree.Commit(req.Message, &git.CommitOptions{})
	if err != nil {
		return domain.WorkspaceGitCommitResp{}, fmt.Errorf("git commit failed: %w", err)
	}
	return domain.WorkspaceGitCommitResp{
		Hash:  hash.String(),
		Short: hash.String()[:7],
	}, nil
}

func (a *Actor) handleGitPush(ctx actor.PureContext, req domain.WorkspaceGitPushReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	if _, err := gitCLI(path, "push", remote); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitPull(ctx actor.PureContext, req domain.WorkspaceGitPullReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	if _, err := gitCLI(path, "pull", remote); err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitBranch(ctx actor.PureContext, req domain.WorkspaceGitBranchReq) (domain.WorkspaceGitBranchResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitBranchResp{}, err
	}
	// Local branches: refs/heads
	localOut := gitCLIOut(path, "for-each-ref", "refs/heads", "--format=%(refname:short)|%(objectname:short)|%(HEAD)")
	// Remote-tracking branches: refs/remotes
	remoteOut := gitCLIOut(path, "for-each-ref", "refs/remotes", "--format=%(refname:short)|%(objectname:short)|%(HEAD)")

	var branches []domain.GitBranchInfo
	for _, line := range strings.Split(localOut, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		hash := parts[1]
		if len(hash) > 7 {
			hash = hash[:7]
		}
		branches = append(branches, domain.GitBranchInfo{
			Name:    parts[0],
			Current: parts[2] == "*",
			Hash:    hash,
		})
	}
	for _, line := range strings.Split(remoteOut, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		hash := parts[1]
		if len(hash) > 7 {
			hash = hash[:7]
		}
		name := parts[0]
		remote := ""
		if idx := strings.IndexByte(name, '/'); idx >= 0 {
			remote = name[:idx]
		}
		branches = append(branches, domain.GitBranchInfo{
			Name:     name,
			Current:  false,
			Hash:     hash,
			IsRemote: true,
			Remote:   remote,
		})
	}
	return domain.WorkspaceGitBranchResp{Branches: branches}, nil
}

func (a *Actor) handleGitCheckout(ctx actor.PureContext, req domain.WorkspaceGitCheckoutReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"checkout"}
	if req.Create {
		args = append(args, "-b")
	}
	args = append(args, req.Branch)
	if out, err := gitCLI(path, args...); err != nil {
		if len(out) > 512 {
			out = out[:512] + "...(truncated)"
		}
		return fmt.Errorf("git checkout failed: %w: %s", err, out)
	}
	return nil
}

// ── new git callables ──

func (a *Actor) handleGitReset(ctx actor.PureContext, req domain.WorkspaceGitResetReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return err
	}
	// repo used for path lookup only; reset uses gitCLI (timeout-protected)
	_ = repo
	if len(req.Paths) == 0 {
		_, err = gitCLI(path, "reset", "HEAD")
		return err
	}
	for _, p := range req.Paths {
		if _, err := gitCLI(path, "reset", "HEAD", "--", p); err != nil {
			return fmt.Errorf("git reset %q failed: %w", p, err)
		}
	}
	return nil
}

func (a *Actor) handleGitStashSave(ctx actor.PureContext, req domain.WorkspaceGitStashSaveReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"stash", "push"}
	if req.Message != "" {
		args = append(args, "-m", req.Message)
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git stash save failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitStashPop(ctx actor.PureContext, req domain.WorkspaceGitStashPopReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"stash", "pop"}
	if req.Index > 0 {
		args = append(args, fmt.Sprintf("stash@{%d}", req.Index))
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git stash pop failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitStashList(ctx actor.PureContext, req domain.WorkspaceGitStashListReq) (domain.WorkspaceGitStashListResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitStashListResp{}, err
	}
	out, err := gitCLI(path, "stash", "list", "--format=%gd|%gs|%H|%at")
	if err != nil {
		// No stashes
		return domain.WorkspaceGitStashListResp{Stashes: nil}, nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var stashes []domain.GitStashInfo
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		idxStr := strings.TrimPrefix(parts[0], "stash@{")
		idxStr = strings.TrimSuffix(idxStr, "}")
		idx, _ := strconv.Atoi(idxStr)
		hash := parts[2]
		if len(hash) > 7 {
			hash = hash[:7]
		}
		stashes = append(stashes, domain.GitStashInfo{
			Index:   int32(idx),
			Message: parts[1],
			Hash:    hash,
			When:    parts[3],
		})
	}
	return domain.WorkspaceGitStashListResp{Stashes: stashes}, nil
}

func (a *Actor) handleGitStashDrop(ctx actor.PureContext, req domain.WorkspaceGitStashDropReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"stash", "drop"}
	if req.Index > 0 {
		args = append(args, fmt.Sprintf("stash@{%d}", req.Index))
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git stash drop failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitRemoteList(ctx actor.PureContext, req domain.WorkspaceGitRemoteListReq) (domain.WorkspaceGitRemoteListResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitRemoteListResp{}, err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return domain.WorkspaceGitRemoteListResp{}, err
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return domain.WorkspaceGitRemoteListResp{}, err
	}
	var out []domain.GitRemoteInfo
	for _, r := range remotes {
		out = append(out, domain.GitRemoteInfo{
			Name: r.Config().Name,
			Urls: r.Config().URLs,
		})
	}
	return domain.WorkspaceGitRemoteListResp{Remotes: out}, nil
}

func (a *Actor) handleGitRemoteAdd(ctx actor.PureContext, req domain.WorkspaceGitRemoteAddReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return err
	}
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{
		Name: req.Name,
		URLs: []string{req.URL},
	})
	if err != nil {
		return fmt.Errorf("git remote add failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitRemoteRemove(ctx actor.PureContext, req domain.WorkspaceGitRemoteRemoveReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(path)
	if err != nil {
		return err
	}
	if err := repo.DeleteRemote(req.Name); err != nil {
		return fmt.Errorf("git remote remove failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitBlame(ctx actor.PureContext, req domain.WorkspaceGitBlameReq) (domain.WorkspaceGitBlameResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitBlameResp{}, err
	}
	out, err := gitCLI(path, "blame", "--porcelain", "--", req.FilePath)
	if err != nil {
		return domain.WorkspaceGitBlameResp{}, fmt.Errorf("git blame failed: %w", err)
	}
	// Parse porcelain format
	var lines []domain.GitBlameLine
	currentLine := int32(1)
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Porcelain format: <hash> <src-line> <dst-line> <lines>
		fields := strings.Fields(line)
		if len(fields) >= 4 && len(fields[0]) == 40 {
			hash := fields[0][:7]
			lineNo, _ := strconv.Atoi(fields[2])
			lines = append(lines, domain.GitBlameLine{
				Hash:    hash,
				Line:    int32(lineNo),
				Content: strings.Join(fields[3:], " "),
			})
			currentLine++
		}
	}
	return domain.WorkspaceGitBlameResp{Lines: lines}, nil
}

func (a *Actor) handleGitConfigGet(ctx actor.PureContext, req domain.WorkspaceGitConfigGetReq) (domain.WorkspaceGitConfigGetResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitConfigGetResp{}, err
	}
	out, err := gitCLI(path, "config", "--get", req.Key)
	if err != nil {
		return domain.WorkspaceGitConfigGetResp{}, nil
	}
	return domain.WorkspaceGitConfigGetResp{Value: strings.TrimSpace(out)}, nil
}

func (a *Actor) handleGitConfigSet(ctx actor.PureContext, req domain.WorkspaceGitConfigSetReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	parts := strings.SplitN(req.Key, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid config key %q: expected section.key format", req.Key)
	}
	if req.Global {
		return fmt.Errorf("global config not supported")
	}
	if _, err := gitCLI(path, "config", req.Key, req.Value); err != nil {
		return fmt.Errorf("git config set failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitShow(ctx actor.PureContext, req domain.WorkspaceGitShowReq) (domain.WorkspaceGitShowResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitShowResp{}, err
	}
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}

	// Commit metadata via NUL-separated fields; use %aI for ISO author date.
	const format = "%H%x00%h%x00%s%x00%an%x00%ae%x00%aI%x00%P"
	metaOut, err := gitCLI(path, "-c", "core.quotepath=false", "show", "-s", "--format="+format, ref)
	if err != nil {
		return domain.WorkspaceGitShowResp{}, fmt.Errorf("git show metadata failed: %w", err)
	}
	commit, err := parseGitShowMetadata(metaOut)
	if err != nil {
		return domain.WorkspaceGitShowResp{}, fmt.Errorf("git show metadata parse: %w", err)
	}

	// Changed files via -z name-status: bare paths, never quoted.
	filesOut := gitCLIOut(path, "-c", "core.quotepath=false", "show", "-z", "--name-status", "--format=", ref)
	files := parseGitNameStatusZ(filesOut)

	return domain.WorkspaceGitShowResp{Commit: commit, Files: files}, nil
}

func parseGitShowMetadata(out string) (domain.GitCommitInfo, error) {
	parts := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	if len(parts) < 6 {
		return domain.GitCommitInfo{}, fmt.Errorf("expected 7 NUL-separated metadata fields, got %d", len(parts))
	}
	var parents []string
	if parts[6] != "" {
		parents = strings.Fields(parts[6])
	}
	return domain.GitCommitInfo{
		Hash:    parts[0],
		Short:   parts[1],
		Message: parts[2],
		Author:  parts[3],
		Email:   parts[4],
		When:    parts[5],
		Parents: parents,
	}, nil
}

func parseGitNameStatusZ(z string) []domain.GitShowFile {
	if z == "" {
		return nil
	}
	fields := strings.Split(strings.TrimRight(z, "\x00"), "\x00")
	var files []domain.GitShowFile
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		switch status[0] {
		case 'A':
			i++
			if i < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i], Status: "added"})
			}
		case 'M':
			i++
			if i < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i], Status: "modified"})
			}
		case 'D':
			i++
			if i < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i], Status: "deleted"})
			}
		case 'T':
			i++
			if i < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i], Status: "typechange"})
			}
		case 'R':
			i++
			if i+1 < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i+1], Status: "renamed", OldPath: fields[i]})
				i++
			}
		case 'C':
			i++
			if i+1 < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i+1], Status: "copied", OldPath: fields[i]})
				i++
			}
		case 'U':
			i++
			if i < len(fields) {
				files = append(files, domain.GitShowFile{Path: fields[i], Status: "unknown"})
			}
		}
	}
	return files
}

func (a *Actor) handleGitFetch(ctx actor.PureContext, req domain.WorkspaceGitFetchReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	if _, err := gitCLI(path, "fetch", remote); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitDiscard(ctx actor.PureContext, req domain.WorkspaceGitDiscardReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"checkout", "--"}
	if len(req.Paths) > 0 {
		args = append(args, req.Paths...)
	} else {
		args = append(args, ".")
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git discard failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitAmend(ctx actor.PureContext, req domain.WorkspaceGitAmendReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"commit", "--amend", "--allow-empty"}
	if req.NoEdit {
		args = append(args, "--no-edit")
	} else if req.Message != "" {
		args = append(args, "-m", req.Message)
	} else {
		return fmt.Errorf("git amend requires Message or NoEdit")
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git amend failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitTagList(ctx actor.PureContext, req domain.WorkspaceGitTagListReq) (domain.WorkspaceGitTagListResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitTagListResp{}, err
	}
	out := gitCLIOut(path, "for-each-ref", "refs/tags", "--format=%(refname:short)|%(objectname)|%(objecttype)|%(contents:subject)|%(taggerdate:iso8601)")
	var tags []domain.GitTagInfo
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 4 {
			continue
		}
		when := ""
		if len(parts) >= 5 {
			when = parts[4]
		}
		tags = append(tags, domain.GitTagInfo{
			Name:    parts[0],
			Hash:    parts[1],
			Message: parts[3],
			When:    when,
		})
	}
	return domain.WorkspaceGitTagListResp{Tags: tags}, nil
}

func (a *Actor) handleGitTagCreate(ctx actor.PureContext, req domain.WorkspaceGitTagCreateReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	args := []string{"tag"}
	if req.Force {
		args = append(args, "-f")
	}
	if req.Message != "" {
		args = append(args, "-a", "-m", req.Message)
	}
	args = append(args, req.Name)
	if req.CommitHash != "" {
		args = append(args, req.CommitHash)
	}
	if _, err := gitCLI(path, args...); err != nil {
		return fmt.Errorf("git tag create failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitTagDelete(ctx actor.PureContext, req domain.WorkspaceGitTagDeleteReq) error {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return err
	}
	if _, err := gitCLI(path, "tag", "-d", req.Name); err != nil {
		return fmt.Errorf("git tag delete failed: %w", err)
	}
	return nil
}

func (a *Actor) handleGitMerge(ctx actor.PureContext, req domain.WorkspaceGitMergeReq) (domain.WorkspaceGitMergeResp, error) {
	path, err := a.gitRootFor(ctx, req.ProjectID, req.WorktreeID)
	if err != nil {
		return domain.WorkspaceGitMergeResp{}, err
	}
	args := []string{"merge"}
	if req.NoFastForward {
		args = append(args, "--no-ff")
	}
	if req.Squash {
		args = append(args, "--squash")
	}
	args = append(args, req.Branch)
	out, err := gitCLI(path, args...)
	if err != nil {
		if strings.Contains(out, "Already up to date") {
			return domain.WorkspaceGitMergeResp{Status: "up_to_date"}, nil
		}
		conflictOut := gitCLIOut(path, "diff", "--name-only", "--diff-filter=U")
		var conflicts []string
		for _, line := range strings.Split(conflictOut, "\n") {
			if line == "" {
				continue
			}
			conflicts = append(conflicts, line)
		}
		if len(conflicts) == 0 {
			if _, abortErr := gitCLI(path, "merge", "--abort"); abortErr != nil {
				// ignore
			}
			return domain.WorkspaceGitMergeResp{}, fmt.Errorf("git merge failed: %w: %s", err, out)
		}
		if _, abortErr := gitCLI(path, "merge", "--abort"); abortErr != nil {
			// ignore
		}
		return domain.WorkspaceGitMergeResp{Status: "conflict", ConflictFiles: conflicts}, nil
	}
	if strings.Contains(out, "Already up to date") {
		return domain.WorkspaceGitMergeResp{Status: "up_to_date"}, nil
	}
	return domain.WorkspaceGitMergeResp{Status: "merged"}, nil
}

// ── agent messaging ──

// handleAgentSendMessage is a stateless handler: it reads the lock-guarded
// agent snapshot and performs one fire-and-forget invoke; no owner-loop
// serialization is needed.
func (a *Actor) handleAgentSendMessage(ctx actor.PureContext, req domain.AgentMessageSendReq) (domain.AgentMessageSendResp, error) {
	if req.Text == "" {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: text is required")
	}
	if req.ToAgentID == "" {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: ToAgentId is required")
	}

	// Find target agent
	var targetRef domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.ID == req.ToAgentID || ag.ActorID == req.ToAgentID {
			targetRef = ag
			break
		}
	}
	if targetRef.LoadState != "loaded" || targetRef.ActorID == "" {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: target agent %q is not loaded", req.ToAgentID)
	}

	// Authorization: owner/self, direct child (worker→owner notification), or
	// an explicit agent-chat:<target> mount grant. Developer/admin callers
	// (UI, no CallerAgentId) bypass. This handler previously had no caller
	// auth — the turn engine unconditionally overwrites CallerAgentId, so a
	// non-empty value here is the authoritative agent identity.
	if err := a.requireConversableGrant(ctx, "workspace.agent_send_message", req.CallerAgentID, targetRef); err != nil {
		return domain.AgentMessageSendResp{}, err
	}

	cid, err := identity.ParseCanonicalID(targetRef.ActorID)
	if err != nil {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: invalid target actor id: %w", err)
	}
	agentActorRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: target agent actor not available")
	}

	msgType := req.MessageType
	if msgType == "" {
		msgType = "chat"
	}

	// Resolve sender identity from the calling agent's stable ID and display name.
	// CallerAgentID is the calling agent's actor ID (injected by turn engine /
	// shimmed by admin UI); we look it up in the agent registry so the recipient
	// sees a human-readable name rather than a raw actor-ID string. When the
	// caller is anonymous (e.g. developer/UI without an agent identity) we leave
	// both fields empty; handleReceiveMessage degrades gracefully to a plain
	// "agent" meta in that case.
	fromAgentID := ""
	fromName := ""
	if req.CallerAgentID != "" {
		if sender, ok := a.findAgentRef(req.CallerAgentID); ok {
			fromAgentID = sender.ID
			fromName = sender.DisplayName
		} else {
			fromAgentID = req.CallerAgentID
			fromName = req.CallerAgentID
		}
	}

	receiveReq := domain.AgentMessageReceiveReq{
		FromAgentID: fromAgentID,
		FromName:    fromName,
		Text:        req.Text,
		MessageType: msgType,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	// Do not wait for the recipient owner loop: it may be processing a turn
	// that synchronously queries workspace, which would deadlock this owner loop.
	call := agentActorRef.Invoke(ctx.Lifecycle(), "message_receive", receiveReq)
	if call == nil {
		return domain.AgentMessageSendResp{}, fmt.Errorf("workspace.agent.send_message: invoke failed")
	}
	_ = call.Close()

	return domain.AgentMessageSendResp{Sent: true}, nil
}

// handleAgentReadMessage forwards workspace.agent_read_message to the target
// agent's pure message_read callable and awaits the result. Unlike
// send_message this must wait for the payload, which is safe here: the target
// handler is PureContext (snapshot read, no owner-loop involvement), the same
// awaited read pattern as agent_status in refreshAgentStatuses.

func (a *Actor) handleAgentReadMessage(ctx actor.PureContext, req domain.AgentMessageReadReq) (domain.AgentMessageReadResp, error) {
	if req.ToAgentID == "" {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: ToAgentId is required")
	}

	var targetRef domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.ID == req.ToAgentID || ag.ActorID == req.ToAgentID {
			targetRef = ag
			break
		}
	}
	if targetRef.LoadState != "loaded" || targetRef.ActorID == "" {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: target agent %q is not loaded", req.ToAgentID)
	}

	// Authorization: same conversable-grant scoping as send_message (owner/
	// self, direct child, or agent-chat:<target> mount). Developer/admin
	// callers (UI, no CallerAgentId) bypass.
	if err := a.requireConversableGrant(ctx, "workspace.agent_read_message", req.CallerAgentID, targetRef); err != nil {
		return domain.AgentMessageReadResp{}, err
	}

	cid, err := identity.ParseCanonicalID(targetRef.ActorID)
	if err != nil {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: invalid target actor id: %w", err)
	}
	agentActorRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentActorRef == nil {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: target agent actor not available")
	}

	readCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancel()
	call := agentActorRef.Invoke(readCtx, "message_read", req)
	if call == nil {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: invoke failed")
	}
	raw, err := call.Final(readCtx)
	call.Close()
	if err != nil {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: message_read failed: %w", err)
	}
	if raw == nil {
		return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: message_read returned nil")
	}

	var resp domain.AgentMessageReadResp
	if r, ok := raw.(domain.AgentMessageReadResp); ok {
		resp = r
	} else {
		body, merr := json.Marshal(raw)
		if merr != nil {
			return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: encode result: %w", merr)
		}
		if uerr := json.Unmarshal(body, &resp); uerr != nil {
			return domain.AgentMessageReadResp{}, fmt.Errorf("workspace.agent.read_message: decode result: %w", uerr)
		}
	}
	return resp, nil
}

// ── agent pause/resume forwarding ──

// handleAgentPause forwards a pause request to the target agent's local
// agent_pause callable. Authorization (requireOwnerOrSelf) runs before
// forwarding: the caller must be the target's direct owner, the target
// itself, or a trusted human/admin identity. The invoke is fire-and-forget
// (no Final wait) so the caller never blocks on the target's owner loop.
func (a *Actor) handleAgentPause(ctx actor.PureContext, req gen.AgentPauseReq) (gen.AgentPauseResp, error) {
	if err := a.forwardPauseResume(ctx, "workspace.agent_pause", "agent_pause", req.ToAgentID, req.CallerAgentID, gen.AgentPauseReq{}); err != nil {
		return gen.AgentPauseResp{}, err
	}
	return gen.AgentPauseResp{Sent: true}, nil
}

// handleAgentResume forwards a resume request to the target agent's local
// agent_resume callable. Same authorization and fire-and-forget semantics as
// handleAgentPause.
func (a *Actor) handleAgentResume(ctx actor.PureContext, req gen.AgentResumeReq) (gen.AgentResumeResp, error) {
	if err := a.forwardPauseResume(ctx, "workspace.agent_resume", "agent_resume", req.ToAgentID, req.CallerAgentID, gen.AgentResumeReq{}); err != nil {
		return gen.AgentResumeResp{}, err
	}
	return gen.AgentResumeResp{Sent: true}, nil
}

// forwardPauseResume resolves the target agent, authorizes the caller, and
// invokes the target's local pause/resume callable without waiting for the
// result. The invokePayload is passed through to the target callable.
// Stateless: snapshot read + one fire-and-forget invoke.
func (a *Actor) forwardPauseResume(ctx actor.PureContext, prefix, invokeTarget, toAgentID, callerAgentID string, invokePayload any) error {
	if toAgentID == "" {
		return fmt.Errorf("%s: ToAgentId is required", prefix)
	}

	// Find target agent
	var targetRef domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.ID == toAgentID || ag.ActorID == toAgentID {
			targetRef = ag
			break
		}
	}
	if targetRef.LoadState != "loaded" || targetRef.ActorID == "" {
		return fmt.Errorf("%s: target agent %q is not loaded", prefix, toAgentID)
	}

	// Authorization: owner, self, trusted human/admin, or an explicit
	// agent-chat:<target> mount grant (requireOwnerSelfOrChat). Direct
	// children are denied even with a mount — children must not pause
	// their parents.
	if err := a.requireOwnerSelfOrChat(ctx, prefix, callerAgentID, targetRef); err != nil {
		return err
	}

	cid, err := identity.ParseCanonicalID(targetRef.ActorID)
	if err != nil {
		return fmt.Errorf("%s: invalid target actor id: %w", prefix, err)
	}
	agentActorRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		return fmt.Errorf("%s: target agent actor not available", prefix)
	}

	// Fire-and-forget: do not wait for the recipient owner loop, which may
	// be processing a turn that synchronously queries workspace, which would
	// deadlock this owner loop.
	call := agentActorRef.Invoke(ctx.Lifecycle(), invokeTarget, invokePayload)
	if call == nil {
		return fmt.Errorf("%s: invoke failed", prefix)
	}
	_ = call.Close()

	return nil
}

// normalizeGlobalSystemAgents ensures the process-unique global system agent
// (Coordinator) carries an empty ProjectID and exists at most once. Older
// builds scoped it to the system meta project, and a weak dedup guard could
// leave duplicate coordinators (one global stub plus one project-scoped).
// This collapses such leftovers on startup. It prefers keeping an already-
// global record (the one the UI treated as the home agent) when one exists.
// Returns true when the agent list was modified.
func (a *Actor) normalizeGlobalSystemAgents() bool {
	changed := false
	for _, kind := range []domain.AgentKind{domain.AgentKindCoordinator} {
		keeper := -1
		for i := range a.Agents {
			if a.Agents[i].AgentKind != kind {
				continue
			}
			if keeper < 0 {
				keeper = i
			}
			if a.Agents[i].ProjectID == "" {
				keeper = i
				break
			}
		}
		if keeper < 0 {
			continue
		}
		var survivors []domain.AgentRef
		for i := range a.Agents {
			ag := a.Agents[i]
			if ag.AgentKind != kind {
				survivors = append(survivors, ag)
				continue
			}
			if i != keeper {
				// Drop a leftover duplicate of this kind.
				changed = true
				continue
			}
			if ag.ProjectID != "" {
				ag.ProjectID = ""
				changed = true
			}
			survivors = append(survivors, ag)
		}
		a.Agents = survivors
	}
	return changed
}

// ensureCoordinatorAgent creates the process-unique Coordinator agent if it
// does not already exist. The Coordinator is a workspace-global system agent
// (not scoped to any project); its DisplayName doubles as the coordinator
// nickname for mention routing. This function is retained for tests and
// potential system-managed creation paths; in production the Coordinator is
// user-created via the onboarding flow (workspace.create_agent).
func (a *Actor) ensureCoordinatorAgent(ctx actor.Context) {
	for _, ag := range a.agentSnapshot() {
		if ag.AgentKind == domain.AgentKindCoordinator {
			return
		}
	}
	ag := domain.AgentRef{
		ID:          ctx.NewID().String(),
		DisplayName: "Coordinator",
		AgentKind:   domain.AgentKindCoordinator,
		ProjectID:   "",
		Status:      "inactive",
		LoadState:   "unloaded",
	}
	a.agentsMu.Lock()
	a.Agents = append(a.Agents, ag)
	a.agentsMu.Unlock()
	a.saveOrLog(ctx)
	a.emitAgentsChanged(ctx)
	ctx.Logger().Info("workspace: ensured coordinator agent reference")
}

// handleCoordinatorLookup returns the process-unique Coordinator's nickname
// (its DisplayName) and a live actor id. Normal agents consult it to detect
// nickname mentions and to route intercepted chat.submit texts to the
// Coordinator via coordinator.route. If no Coordinator has been created yet
// (e.g. before onboarding completes), it returns Found=false without creating
// one — the Coordinator is user-created via the onboarding flow.
// handleCoordinatorLookup resolves the live Coordinator, loading (spawning)
// it when necessary. Callers that only need presence or the nickname must use
// the pure workspace.coordinator_peek instead so they never pay the spawn.
func (a *Actor) handleCoordinatorLookup(ctx actor.PureContext) (domain.WorkspaceCoordinatorLookupResp, error) {
	for _, ag := range a.agentSnapshot() {
		if ag.AgentKind != domain.AgentKindCoordinator {
			continue
		}
		if ag.LoadState != "loaded" || ag.ActorID == "" {
			loaded, _, err := a.loadAgentByID(ctx, ag.ID)
			if err != nil {
				return domain.WorkspaceCoordinatorLookupResp{}, fmt.Errorf("workspace.coordinator.lookup: load coordinator: %w", err)
			}
			ag = loaded
		}
		return domain.WorkspaceCoordinatorLookupResp{
			Found:    true,
			Nickname: ag.DisplayName,
			ActorID:  ag.ActorID,
		}, nil
	}
	return domain.WorkspaceCoordinatorLookupResp{Found: false}, nil
}

// handleCoordinatorPeek is the pure read-only counterpart of
// workspace.coordinator_lookup: it answers from the atomic agent-list
// snapshot and never loads (spawns) the Coordinator. ActorID is included
// only when the Coordinator is already loaded; callers that require a live
// actor must use workspace.coordinator_lookup instead.
func (a *Actor) handleCoordinatorPeek(actor.PureContext) (domain.WorkspaceCoordinatorLookupResp, error) {
	v := a.agentListSnapshot.Load()
	if v == nil {
		return domain.WorkspaceCoordinatorLookupResp{Found: false}, nil
	}
	for _, it := range v.(gen.WorkspaceAgentListState).Items {
		if it.AgentKind != domain.AgentKindCoordinator {
			continue
		}
		resp := domain.WorkspaceCoordinatorLookupResp{Found: true, Nickname: it.DisplayName}
		if it.LoadState == "loaded" {
			resp.ActorID = it.ActorID
		}
		return resp, nil
	}
	return domain.WorkspaceCoordinatorLookupResp{Found: false}, nil
}
