package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// appStatePrefix is the persist namespace segment for plugin documents.
// The store itself is constructed through the backend registry
// (config.PersistConfig), so the backend choice (fs / goleveldb / webdav /
// ...) is a host deployment detail invisible to apps; only the document
// contract (state.*) is app-facing.
const appStatePrefix = "pluginhost/appstate"

// maxStateBatchKeys bounds one state.get_many / state.set_many call so a
// chatty or hostile app cannot amplify a single invoke into an unbounded
// batch (约束面: per-call work is explicit).
const maxStateBatchKeys = 256

// appStateName joins the server-injected plugin identity with an app-supplied
// key into the canonical "<pluginID>/<key>" document name. The plugin prefix
// is never app-controlled (the host bridge injects it), but the key is, so
// traversal is rejected here before the name reaches the persist backend:
// backslashes, root-relative keys, and any ".." component escape the
// per-app namespace and are refused.
func appStateName(plugin, key string) (string, error) {
	if plugin == "" || key == "" {
		return "", fmt.Errorf("pluginhost: state requires Plugin and Key")
	}
	if strings.ContainsRune(key, '\\') || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("pluginhost: state key %q must be a relative key inside the app namespace", key)
	}
	name := plugin + "/" + key
	if clean := path.Clean(name); clean != name || !strings.HasPrefix(name, plugin+"/") {
		return "", fmt.Errorf("pluginhost: state key %q escapes the app namespace", key)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("pluginhost: state key %q escapes the app namespace", key)
		}
	}
	return name, nil
}

// appStateQuota resolves the effective per-app quotas. Actor-level overrides
// (set by tests) win; otherwise the host config values apply.
type appStateQuota struct {
	maxDocsPerApp  int
	maxValueBytes  int64
	maxAppendBytes int64
}

func (a *Actor) appStateQuota() appStateQuota {
	q := appStateQuota{
		maxDocsPerApp:  config.AppStateMaxDocsPerApp(),
		maxValueBytes:  config.AppStateMaxValueBytes(),
		maxAppendBytes: config.AppStateMaxAppendBytes(),
	}
	if a.appStateMaxDocsPerApp > 0 {
		q.maxDocsPerApp = a.appStateMaxDocsPerApp
	}
	if a.appStateMaxValueBytes > 0 {
		q.maxValueBytes = a.appStateMaxValueBytes
	}
	if a.appStateMaxAppendBytes > 0 {
		q.maxAppendBytes = a.appStateMaxAppendBytes
	}
	return q
}

// ensureAppStateStore constructs the app-state store through the persist
// backend registry. The backend is whatever sporemind.yaml selects; the app
// contract is unchanged by the choice. Tests override a.appStateStore
// directly with a hermetic backend.
func (a *Actor) ensureAppStateStore() (persist.Persist, error) {
	if a.appStateStore == nil {
		store, err := persist.New(config.PersistConfig(appStatePrefix))
		if err != nil {
			return nil, fmt.Errorf("pluginhost: appstate store: %w", err)
		}
		a.appStateStore = store
	}
	return a.appStateStore, nil
}

// checkAppStateSetQuota enforces the per-value byte cap and the per-app
// document cap before a Save. Keys that already exist are overwrites and do
// not consume additional document quota. The document count relies on the
// optional Lister capability; backends without it degrade to no count quota
// (both built-in backends implement Lister).
func (a *Actor) checkAppStateSetQuota(store persist.Persist, plugin string, keys []string, valueBytes []int) error {
	q := a.appStateQuota()
	for _, n := range valueBytes {
		if q.maxValueBytes > 0 && int64(n) > q.maxValueBytes {
			return fmt.Errorf("pluginhost: state.set value exceeds per-document quota (%d > %d bytes)", n, q.maxValueBytes)
		}
	}
	if q.maxDocsPerApp <= 0 {
		return nil
	}
	lister, ok := store.(persist.Lister)
	if !ok {
		return nil
	}
	existing, err := lister.List(plugin + "/")
	if err != nil {
		return fmt.Errorf("pluginhost: state quota list: %w", err)
	}
	known := make(map[string]bool, len(existing))
	for _, name := range existing {
		known[name] = true
	}
	newDocs := 0
	for _, key := range keys {
		name, err := appStateName(plugin, key)
		if err != nil {
			return err
		}
		if !known[name] {
			newDocs++
		}
	}
	if len(existing)+newDocs > q.maxDocsPerApp {
		return fmt.Errorf("pluginhost: state.set exceeds per-app document quota (%d + %d > %d docs for plugin %q)", len(existing), newDocs, q.maxDocsPerApp, plugin)
	}
	return nil
}

func (a *Actor) handleStateGet(_ actor.Context, req gen.PluginStateGetReq) (gen.PluginStateGetResp, error) {
	name, err := appStateName(req.Plugin, req.Key)
	if err != nil {
		return gen.PluginStateGetResp{}, err
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateGetResp{}, err
	}
	var value []byte
	err = store.Load(name, &value)
	if err != nil {
		if errors.Is(err, persist.ErrNotExist) {
			return gen.PluginStateGetResp{Value: nil, Found: false}, nil
		}
		return gen.PluginStateGetResp{}, fmt.Errorf("pluginhost: state.get: %w", err)
	}
	return gen.PluginStateGetResp{Value: value, Found: true}, nil
}

func (a *Actor) handleStateSet(_ actor.Context, req gen.PluginStateSetReq) (gen.PluginStateSetResp, error) {
	name, err := appStateName(req.Plugin, req.Key)
	if err != nil {
		return gen.PluginStateSetResp{}, err
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateSetResp{}, err
	}
	if err := a.checkAppStateSetQuota(store, req.Plugin, []string{req.Key}, []int{len(req.Value)}); err != nil {
		return gen.PluginStateSetResp{}, err
	}
	if err := store.Save(name, req.Value); err != nil {
		return gen.PluginStateSetResp{}, fmt.Errorf("pluginhost: state.set: %w", err)
	}
	return gen.PluginStateSetResp{}, nil
}

func (a *Actor) handleStateDelete(_ actor.Context, req gen.PluginStateDeleteReq) (gen.PluginStateDeleteResp, error) {
	name, err := appStateName(req.Plugin, req.Key)
	if err != nil {
		return gen.PluginStateDeleteResp{}, err
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateDeleteResp{}, err
	}
	if err := store.Delete(name); err != nil {
		return gen.PluginStateDeleteResp{}, fmt.Errorf("pluginhost: state.delete: %w", err)
	}
	return gen.PluginStateDeleteResp{Removed: true}, nil
}

// handleStateList enumerates the calling plugin's document keys (Lister
// semantics). Keys are returned relative to the plugin namespace; the
// optional Prefix narrows the scan. Append-only logs are not documents and
// are not listed.
func (a *Actor) handleStateList(_ actor.Context, req gen.PluginStateListReq) (gen.PluginStateListResp, error) {
	if req.Plugin == "" {
		return gen.PluginStateListResp{}, fmt.Errorf("pluginhost: state.list requires Plugin")
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateListResp{}, err
	}
	lister, ok := store.(persist.Lister)
	if !ok {
		return gen.PluginStateListResp{}, fmt.Errorf("pluginhost: state.list is not supported by the %T appstate backend (no Lister capability)", store)
	}
	scan := req.Plugin + "/"
	if req.Prefix != "" {
		sub, err := appStateName(req.Plugin, req.Prefix)
		if err != nil {
			return gen.PluginStateListResp{}, err
		}
		scan = sub + "/"
	}
	names, err := lister.List(scan)
	if err != nil {
		return gen.PluginStateListResp{}, fmt.Errorf("pluginhost: state.list: %w", err)
	}
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, strings.TrimPrefix(name, req.Plugin+"/"))
	}
	return gen.PluginStateListResp{Keys: keys}, nil
}

// handleStateAppend appends bytes to a plugin's append-only log (Appender
// semantics). The payload is opaque; the caller delimits records. There is
// no read-back through state.get — the surface is a log sink.
func (a *Actor) handleStateAppend(_ actor.Context, req gen.PluginStateAppendReq) (gen.PluginStateAppendResp, error) {
	name, err := appStateName(req.Plugin, req.Key)
	if err != nil {
		return gen.PluginStateAppendResp{}, err
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateAppendResp{}, err
	}
	appender, ok := store.(persist.Appender)
	if !ok {
		return gen.PluginStateAppendResp{}, fmt.Errorf("pluginhost: state.append is not supported by the %T appstate backend (no Appender capability)", store)
	}
	q := a.appStateQuota()
	if q.maxAppendBytes > 0 && int64(len(req.Data)) > q.maxAppendBytes {
		return gen.PluginStateAppendResp{}, fmt.Errorf("pluginhost: state.append payload exceeds per-call quota (%d > %d bytes)", len(req.Data), q.maxAppendBytes)
	}
	if err := appender.Append(name, req.Data); err != nil {
		return gen.PluginStateAppendResp{}, fmt.Errorf("pluginhost: state.append: %w", err)
	}
	return gen.PluginStateAppendResp{}, nil
}

// handleStateGetMany batches reads so chatty apps do not amplify one invoke
// per key. Missing keys are absent from Entries.
func (a *Actor) handleStateGetMany(_ actor.Context, req gen.PluginStateGetManyReq) (gen.PluginStateGetManyResp, error) {
	if req.Plugin == "" {
		return gen.PluginStateGetManyResp{}, fmt.Errorf("pluginhost: state.get_many requires Plugin")
	}
	if len(req.Keys) > maxStateBatchKeys {
		return gen.PluginStateGetManyResp{}, fmt.Errorf("pluginhost: state.get_many accepts at most %d keys (got %d)", maxStateBatchKeys, len(req.Keys))
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateGetManyResp{}, err
	}
	entries := make(map[string][]byte, len(req.Keys))
	for _, key := range req.Keys {
		name, err := appStateName(req.Plugin, key)
		if err != nil {
			return gen.PluginStateGetManyResp{}, err
		}
		var value []byte
		err = store.Load(name, &value)
		if err != nil {
			if errors.Is(err, persist.ErrNotExist) {
				continue
			}
			return gen.PluginStateGetManyResp{}, fmt.Errorf("pluginhost: state.get_many %q: %w", key, err)
		}
		entries[key] = value
	}
	return gen.PluginStateGetManyResp{Entries: entries}, nil
}

// handleStateSetMany batches writes under the same quotas as state.set.
func (a *Actor) handleStateSetMany(_ actor.Context, req gen.PluginStateSetManyReq) (gen.PluginStateSetManyResp, error) {
	if req.Plugin == "" {
		return gen.PluginStateSetManyResp{}, fmt.Errorf("pluginhost: state.set_many requires Plugin")
	}
	if len(req.Entries) > maxStateBatchKeys {
		return gen.PluginStateSetManyResp{}, fmt.Errorf("pluginhost: state.set_many accepts at most %d entries (got %d)", maxStateBatchKeys, len(req.Entries))
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStateSetManyResp{}, err
	}
	keys := make([]string, 0, len(req.Entries))
	valueBytes := make([]int, 0, len(req.Entries))
	for key, value := range req.Entries {
		keys = append(keys, key)
		valueBytes = append(valueBytes, len(value))
	}
	if err := a.checkAppStateSetQuota(store, req.Plugin, keys, valueBytes); err != nil {
		return gen.PluginStateSetManyResp{}, err
	}
	for key, value := range req.Entries {
		name, err := appStateName(req.Plugin, key)
		if err != nil {
			return gen.PluginStateSetManyResp{}, err
		}
		if err := store.Save(name, value); err != nil {
			return gen.PluginStateSetManyResp{}, fmt.Errorf("pluginhost: state.set_many %q: %w", key, err)
		}
	}
	return gen.PluginStateSetManyResp{}, nil
}

// handleStatePurge reclaims a plugin's whole app-state subtree (documents
// and append logs) via the cascade Delete("<pluginID>") convention, plus the
// plugin's app.data grant directory. It is the explicit data reclamation
// callable — unregister deliberately does NOT fire it (app data survives
// re-register), and artifact unload/reload never purges (BP13).
func (a *Actor) handleStatePurge(_ actor.Context, req gen.PluginStatePurgeReq) (gen.PluginStatePurgeResp, error) {
	if req.PluginID == "" {
		return gen.PluginStatePurgeResp{}, fmt.Errorf("pluginhost: state.purge requires PluginId")
	}
	store, err := a.ensureAppStateStore()
	if err != nil {
		return gen.PluginStatePurgeResp{}, err
	}
	if err := store.Delete(req.PluginID); err != nil {
		return gen.PluginStatePurgeResp{}, fmt.Errorf("pluginhost: state.purge: %w", err)
	}
	if dir := a.appDataDirOf(req.PluginID); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return gen.PluginStatePurgeResp{}, fmt.Errorf("pluginhost: state.purge appdata: %w", err)
		}
	}
	a.mu.Lock()
	_, hadGrant := a.AppDataGrants[req.PluginID]
	delete(a.AppDataGrants, req.PluginID)
	a.mu.Unlock()
	if hadGrant {
		_ = a.Save()
	}
	return gen.PluginStatePurgeResp{Removed: true}, nil
}

// appDataDirOf resolves the plugin's app.data grant directory: the persisted
// grant ledger first (it survives unload and host restarts — the artifact
// load record does not), then the derived form from a live load record.
// Empty when neither is on file — purge then reclaims the persist subtree
// only, never a guessed location.
func (a *Actor) appDataDirOf(pluginID string) string {
	a.mu.RLock()
	dir, ok := a.AppDataGrants[pluginID]
	load, hasLoad := a.ArtifactLoads[pluginID]
	a.mu.RUnlock()
	if ok {
		return dir
	}
	if !hasLoad {
		return ""
	}
	appDir := appbinding.AppDirFromArtifactPath(load.ArtifactPath)
	if appDir == "" {
		return ""
	}
	return filepath.Join(appDir, ".sporecode", "appdata")
}

// recordAppDataGrant adds a non-empty granted dataDir from an OnLoad config
// to the grant ledger. Caller holds a.mu.
func (a *Actor) recordAppDataGrant(pluginID string, onLoadConfig []byte) {
	dir := parseOnLoadDataDir(onLoadConfig)
	if dir == "" {
		return
	}
	if a.AppDataGrants == nil {
		a.AppDataGrants = map[string]string{}
	}
	a.AppDataGrants[pluginID] = dir
}

// parseOnLoadDataDir extracts the dataDir field from the plugin's OnLoad
// config JSON. Anything unparseable means no grant — fail closed.
func parseOnLoadDataDir(onLoadConfig []byte) string {
	if len(onLoadConfig) == 0 {
		return ""
	}
	var cfg struct {
		DataDir string `json:"dataDir"`
	}
	if err := json.Unmarshal(onLoadConfig, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.DataDir)
}

// appDataSoftLimitBytes is the per-grant soft limit surfaced by
// appdata_usage. Observation only: the plugin owns every write into its
// grant directory, so the host cannot enforce a hard quota.
const appDataSoftLimitBytes int64 = 1 << 30 // 1 GiB

// handleAppDataUsage reports the on-disk footprint of app.data grants
// (walked at query time) against the soft limit. Registered on its own lane:
// a large grant tree can take seconds to walk and must not block the owner
// lane or the app_state lane.
func (a *Actor) handleAppDataUsage(_ actor.Context, req gen.PluginAppDataUsageReq) (gen.PluginAppDataUsageResp, error) {
	a.mu.RLock()
	grants := make(map[string]string, len(a.AppDataGrants))
	for id, dir := range a.AppDataGrants {
		if req.PluginID == "" || req.PluginID == id {
			grants[id] = dir
		}
	}
	loaded := make(map[string]bool, len(a.ArtifactLoads))
	for id := range a.ArtifactLoads {
		loaded[id] = true
	}
	a.mu.RUnlock()

	resp := gen.PluginAppDataUsageResp{SoftLimitBytes: appDataSoftLimitBytes}
	ids := make([]string, 0, len(grants))
	for id := range grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		dir := grants[id]
		bytes, files := dirFootprint(dir)
		resp.Items = append(resp.Items, gen.PluginAppDataUsageItem{
			PluginID: id,
			DataDir:  dir,
			Bytes:    bytes,
			Files:    files,
			Loaded:   loaded[id],
		})
		resp.TotalBytes += bytes
	}
	return resp, nil
}

// dirFootprint sums regular file sizes and counts files under dir. A missing
// or unreadable directory reports zeros — the grant entry itself stays.
func dirFootprint(dir string) (bytes, files int64) {
	if dir == "" {
		return 0, 0
	}
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		files++
		bytes += info.Size()
		return nil
	})
	return bytes, files
}
