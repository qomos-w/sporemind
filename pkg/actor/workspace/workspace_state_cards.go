package workspace

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Card name suffixes for per-concern ground-truth persist records. Each
// suffix produces a sub-path card name (actorID + "/rmounts" etc.) so the
// persist layer's cascade Delete(actorID) reclaims every subordinate
// document in one call — no orphans.
const (
	mountsCardSuffix   = "/rmounts"
	kindsCardSuffix    = "/rkinds"
	prefsCardSuffix    = "/rprefs"
	uiCardSuffix       = "/rui"
	delRetryCardSuffix = "/rdel"
	subMapsCardSuffix  = "/rsubmaps"
	eventWaitsSuffix   = "/reventwaits"
	scatterCardSuffix  = "/rscatter"
)

// deletionRetryState is the persist record for the deletion retry card.
type deletionRetryState struct {
	Errors   map[string]string `json:"errors"`
	Attempts map[string]int    `json:"attempts"`
}

func (a *Actor) ensureStore() {
	if a.store == nil {
		a.store = persist.MustNew(config.PersistConfig("workspace"))
	}
}

func (a *Actor) mountsCardName() string   { return a.actorID + mountsCardSuffix }
func (a *Actor) kindsCardName() string    { return a.actorID + kindsCardSuffix }
func (a *Actor) prefsCardName() string    { return a.actorID + prefsCardSuffix }
func (a *Actor) uiCardName() string       { return a.actorID + uiCardSuffix }
func (a *Actor) delRetryCardName() string    { return a.actorID + delRetryCardSuffix }
func (a *Actor) subMapsCardName() string     { return a.actorID + subMapsCardSuffix }
func (a *Actor) eventWaitsCardName() string  { return a.actorID + eventWaitsSuffix }
func (a *Actor) scatterCardName() string     { return a.actorID + scatterCardSuffix }

// ---------------------------------------------------------------------------
// Mounts card
// ---------------------------------------------------------------------------

func (a *Actor) saveMountsCard() error {
	a.ensureStore()
	return a.store.Save(a.mountsCardName(), a.mountsSnapshot())
}

// ---------------------------------------------------------------------------
// Agent kind configs card
// ---------------------------------------------------------------------------

func (a *Actor) saveAgentKindConfigsCard() error {
	a.ensureStore()
	return a.store.Save(a.kindsCardName(), a.agentKindConfigsSnapshot())
}

// ---------------------------------------------------------------------------
// Account preferences card
// ---------------------------------------------------------------------------

func (a *Actor) saveAccountPrefsCard() error {
	a.ensureStore()
	if err := a.store.Save(a.prefsCardName(), a.accountPrefs); err != nil {
		return err
	}
	// Write-through pre-paint cache (derived data; see writeBootThemeCache).
	_ = a.writeBootThemeCache()
	return nil
}

// writeBootThemeCache projects the ai-shell theme preference verbatim into
// the boot cache that pkg/desktop InitialTheme reads before the actor tree
// starts — a single file read instead of a full-tree wait. The account
// preferences card remains the source of truth, so failures here are
// non-fatal: the next boot falls back to the legacy tree-wait path.
// Lock discipline matches saveAccountPrefsCard: handler callers hold a.mu.
func (a *Actor) writeBootThemeCache() error {
	pref := a.accountPrefs.Preferences[domain.AccountPrefKeyAiShellTheme]
	path := config.BootThemeCachePath()
	if pref == "" {
		// No theme preference: prune any stale cache so pre-paint falls
		// back to light instead of showing a theme that no longer exists.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return persist.WriteFileAtomic(path, []byte(pref), 0o644)
}

// ---------------------------------------------------------------------------
// UI card
// ---------------------------------------------------------------------------

func (a *Actor) saveUICard() error {
	a.ensureStore()
	return a.store.Save(a.uiCardName(), a.UI)
}

// ---------------------------------------------------------------------------
// Deletion retry card
// ---------------------------------------------------------------------------

func (a *Actor) saveDeletionRetryCard() error {
	a.ensureStore()
	return a.store.Save(a.delRetryCardName(), a.deletionRetrySnapshot())
}

// deletionRetrySnapshot copies the retry maps under the read lock so the
// persisted JSON cannot interleave with a concurrent finalize mutation and
// deletionMu is never held across the persist call.
func (a *Actor) deletionRetrySnapshot() deletionRetryState {
	a.deletionMu.RLock()
	defer a.deletionMu.RUnlock()
	state := deletionRetryState{
		Errors:   make(map[string]string, len(a.deletionErrors)),
		Attempts: make(map[string]int, len(a.deletionAttempts)),
	}
	for k, v := range a.deletionErrors {
		state.Errors[k] = v
	}
	for k, v := range a.deletionAttempts {
		state.Attempts[k] = v
	}
	return state
}

// ---------------------------------------------------------------------------
// Sub-map instances card
// ---------------------------------------------------------------------------

func (a *Actor) saveSubMapInstancesCard() error {
	a.ensureStore()
	return a.store.Save(a.subMapsCardName(), a.subMapInstancesSnapshot())
}

// subMapInstancesSnapshot copies the tracked instances under the read lock so
// persist I/O does not hold subMapMu.
func (a *Actor) subMapInstancesSnapshot() []subMapInstance {
	a.subMapMu.RLock()
	defer a.subMapMu.RUnlock()
	out := make([]subMapInstance, len(a.subMapInstances))
	copy(out, a.subMapInstances)
	return out
}

// saveSubMapInstancesSnapshot persists a caller-provided snapshot of the
// tracked instances. Callers that already hold subMapMu capture the snapshot
// there, release the lock, then call this — persist never runs under it.
func (a *Actor) saveSubMapInstancesSnapshot(instances []subMapInstance) error {
	a.ensureStore()
	return a.store.Save(a.subMapsCardName(), instances)
}

// ---------------------------------------------------------------------------
// Event-wait activations card
// ---------------------------------------------------------------------------

// eventWaitsSnapshot copies the active event_wait records under the read lock
// so persist I/O does not hold eventWaitMu. Only active waits are tracked
// (terminal ones are removed from the map on completion), so the persisted
// card never accumulates history — the task card itself carries the outcome.
func (a *Actor) eventWaitsSnapshot() []eventWaitRecord {
	a.eventWaitMu.RLock()
	defer a.eventWaitMu.RUnlock()
	return a.eventWaitsSnapshotLocked()
}

// saveEventWaitsSnapshot persists a caller-provided snapshot of the active
// waits. Callers that already hold eventWaitMu capture the snapshot there,
// release the lock, then call this — persist never runs under it.
func (a *Actor) saveEventWaitsSnapshot(records []eventWaitRecord) error {
	a.ensureStore()
	return a.store.Save(a.eventWaitsCardName(), records)
}

// ---------------------------------------------------------------------------
// Scatter fan-outs card
// ---------------------------------------------------------------------------

// scatterFanoutsSnapshot copies the tracked fan-outs under the read lock so
// persist I/O does not hold scatterMu.
func (a *Actor) scatterFanoutsSnapshot() []scatterFanout {
	a.scatterMu.RLock()
	defer a.scatterMu.RUnlock()
	out := make([]scatterFanout, len(a.scatterFanouts))
	copy(out, a.scatterFanouts)
	return out
}

// saveScatterFanoutsSnapshot persists a caller-provided snapshot of the
// tracked fan-outs. Callers capture the snapshot under scatterMu, release
// the lock, then call this — persist never runs under it.
func (a *Actor) saveScatterFanoutsSnapshot(fanouts []scatterFanout) error {
	a.ensureStore()
	return a.store.Save(a.scatterCardName(), fanouts)
}

// ---------------------------------------------------------------------------
// OrLog wrappers (write-through per callable)
// ---------------------------------------------------------------------------

func (a *Actor) saveMountsOrLog(ctx actor.PureContext) {
	if err := a.saveMountsCard(); err != nil {
		ctx.Logger().Error("workspace: save mounts card failed", "error", err)
	}
}

func (a *Actor) saveAgentKindConfigsOrLog(ctx actor.PureContext) {
	if err := a.saveAgentKindConfigsCard(); err != nil {
		ctx.Logger().Error("workspace: save agent kind configs card failed", "error", err)
	}
}

func (a *Actor) saveAccountPrefsOrLog(ctx actor.PureContext) {
	if err := a.saveAccountPrefsCard(); err != nil {
		ctx.Logger().Error("workspace: save account prefs card failed", "error", err)
	}
}

func (a *Actor) saveUIOrLog(ctx actor.PureContext) {
	if err := a.saveUICard(); err != nil {
		ctx.Logger().Error("workspace: save ui card failed", "error", err)
	}
}

func (a *Actor) saveDeletionRetryOrLog(ctx actor.PureContext) {
	if err := a.saveDeletionRetryCard(); err != nil {
		ctx.Logger().Error("workspace: save deletion retry card failed", "error", err)
	}
}

func (a *Actor) saveSubMapInstancesOrLog() {
	_ = a.saveSubMapInstancesCard()
}

// migrateCardIfNeeded is a ONE-TIME migration from the legacy flat-file card
// name (actorID + ".rmounts" etc.) to the new sub-path card name
// (actorID + "/rmounts" etc.). If the new card already exists the call is a
// no-op; if the old card exists its raw JSON is copied to the new name and
// the old card is deleted, so subsequent loads skip this path entirely.
//
// ONE-TIME MIGRATION — remove after verification.
func migrateCardIfNeeded(store persist.Persist, oldName, newName string) {
	// Fast path: new card already exists.
	var probe json.RawMessage
	if err := store.Load(newName, &probe); err == nil {
		return
	} else if !errors.Is(err, persist.ErrNotExist) {
		return
	}
	// Old card exists? Load its raw bytes.
	var data json.RawMessage
	if err := store.Load(oldName, &data); err != nil {
		return
	}
	_ = store.Save(newName, data)
	_ = store.Delete(oldName)
}
