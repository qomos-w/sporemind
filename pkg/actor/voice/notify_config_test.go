package voice

import (
	"errors"
	"testing"

	"github.com/qomos-w/sporemind/pkg/persist"
)

func newNotifyTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "test-voice"}
}

func TestNotifyConfigDefaults(t *testing.T) {
	a := newNotifyTestActor(t)
	cfg, err := a.loadNotifyConfig()
	if err != nil {
		t.Fatalf("loadNotifyConfig: %v", err)
	}
	if !cfg.Enabled || !cfg.OnComplete || !cfg.OnError || !cfg.OnInteraction {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !cfg.OnAllComplete {
		t.Fatalf("OnAllComplete should default to true: %+v", cfg)
	}
}

func TestNotifyConfigRoundTrip(t *testing.T) {
	a := newNotifyTestActor(t)
	cfg := defaultNotifyConfig()
	cfg.OnAllComplete = false
	cfg.VolumeError = 30
	cfg.SoundError = "bell"
	if err := a.saveNotifyConfig(cfg); err != nil {
		t.Fatalf("saveNotifyConfig: %v", err)
	}
	loaded, err := a.loadNotifyConfig()
	if err != nil {
		t.Fatalf("loadNotifyConfig: %v", err)
	}
	if loaded.OnAllComplete != false {
		t.Fatalf("OnAllComplete round trip mismatch: %+v", loaded)
	}
	if loaded.VolumeError != 30 || loaded.SoundError != "bell" {
		t.Fatalf("per-event fields round trip mismatch: %+v", loaded)
	}
}

// Configs persisted before per-event sound/volume fields existed must load
// with the new-field defaults instead of zero values.
func TestNotifyConfigLegacyPayload(t *testing.T) {
	a := newNotifyTestActor(t)
	if err := a.store.Save(a.notifyConfigKey(), map[string]any{
		"enabled":       true,
		"volume":        float64(50),
		"sound":         "ding",
		"onComplete":    true,
		"onError":       false,
		"onInteraction": true,
	}); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}
	cfg, err := a.loadNotifyConfig()
	if err != nil {
		t.Fatalf("loadNotifyConfig: %v", err)
	}
	if cfg.Sound != "ding" || cfg.OnError {
		t.Fatalf("legacy fields not preserved: %+v", cfg)
	}
	if !cfg.OnAllComplete {
		t.Fatalf("OnAllComplete should fall back to default true: %+v", cfg)
	}
}

// TestNotifyConfigMigratesOldKey verifies that a notify config persisted under
// the old key (actorID/notify) is transparently migrated to the new key
// (actorID/notify-config) on the first load, so cascade Delete(actorID)
// reclaims it.
func TestNotifyConfigMigratesOldKey(t *testing.T) {
	a := newNotifyTestActor(t)
	// Seed the OLD key (actorID + "/notify") with a config.
	if err := a.store.Save(a.actorID+"/notify", map[string]any{
		"enabled":       true,
		"volume":        float64(40),
		"sound":         "chime",
		"onComplete":    true,
		"onError":       true,
		"onInteraction": false,
	}); err != nil {
		t.Fatalf("seed old notify key: %v", err)
	}
	cfg, err := a.loadNotifyConfig()
	if err != nil {
		t.Fatalf("loadNotifyConfig after migration: %v", err)
	}
	if !cfg.Enabled || cfg.Volume != 40 || cfg.Sound != "chime" {
		t.Fatalf("config not migrated correctly: %+v", cfg)
	}
	if cfg.OnInteraction {
		t.Fatalf("OnInteraction should be false: %+v", cfg)
	}
	// Old key must be gone.
	var probe map[string]any
	if err := a.store.Load(a.actorID+"/notify", &probe); !errors.Is(err, persist.ErrNotExist) {
		t.Fatalf("old notify key should be removed after migration (err=%v)", err)
	}
	// New key must exist.
	if err := a.store.Load(a.notifyConfigKey(), &probe); err != nil {
		t.Fatalf("new notify key should exist after migration: %v", err)
	}
	// Second load: migration is a no-op.
	cfg2, err := a.loadNotifyConfig()
	if err != nil {
		t.Fatalf("second loadNotifyConfig: %v", err)
	}
	if cfg2.Volume != 40 {
		t.Fatalf("second load volume mismatch: %+v", cfg2)
	}
}

// TestNotifyConfigCascadeDelete verifies that Delete(actorID) removes the
// notify config card that lives under the actorID/ sub-directory.
func TestNotifyConfigCascadeDelete(t *testing.T) {
	a := newNotifyTestActor(t)
	cfg := defaultNotifyConfig()
	cfg.Volume = 55
	if err := a.saveNotifyConfig(cfg); err != nil {
		t.Fatalf("saveNotifyConfig: %v", err)
	}
	// Confirm the card exists.
	var probe map[string]any
	if err := a.store.Load(a.notifyConfigKey(), &probe); err != nil {
		t.Fatalf("notify config card should exist: %v", err)
	}
	// Cascade delete must reclaim it.
	if err := a.store.Delete(a.actorID); err != nil {
		t.Fatalf("Delete(actorID): %v", err)
	}
	if err := a.store.Load(a.notifyConfigKey(), &probe); !errors.Is(err, persist.ErrNotExist) {
		t.Fatalf("notify config card should be gone after cascade delete (err=%v)", err)
	}
}