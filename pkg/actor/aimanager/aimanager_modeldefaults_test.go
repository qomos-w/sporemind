package aimanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestBuiltinModelDefaults_Sorted(t *testing.T) {
	baseline := builtinModelDefaults()
	if len(baseline) == 0 {
		t.Fatal("should have baseline entries")
	}
	for i := 1; i < len(baseline); i++ {
		if baseline[i-1].Prefix >= baseline[i].Prefix {
			t.Fatalf("entries must be sorted by prefix; got %q before %q",
				baseline[i-1].Prefix, baseline[i].Prefix)
		}
	}
	// Verify a known entry exists.
	opus4 := findDefault(baseline, "claude-opus-4-7")
	if opus4 == nil {
		t.Fatal("missing baseline entry claude-opus-4-7")
	}
	if opus4.MaxContextLength != 1000000 {
		t.Fatalf("claude-opus-4-7 context = %d, want 1000000", opus4.MaxContextLength)
	}
}

func TestMergeModelDefaults_BaselineOnly(t *testing.T) {
	baseline := builtinModelDefaults()
	merged := mergeModelDefaults(baseline, nil)
	if len(merged) != len(baseline) {
		t.Fatalf("merged len = %d, want %d (baseline)", len(merged), len(baseline))
	}
}

func TestMergeModelDefaults_OverrideExisting(t *testing.T) {
	baseline := builtinModelDefaults()
	// Override claude-sonnet-4-5 context to a different value.
	user := []domain.ModelDefault{
		{Prefix: "claude-sonnet-4-5", MaxContextLength: 99999, MaxTokens: 8192},
	}
	merged := mergeModelDefaults(baseline, user)
	entry := findDefault(merged, "claude-sonnet-4-5")
	if entry == nil {
		t.Fatal("missing overridden entry")
	}
	if entry.MaxContextLength != 99999 {
		t.Fatalf("context = %d, want 99999 (user override should win)", entry.MaxContextLength)
	}
	if entry.MaxTokens != 8192 {
		t.Fatalf("MaxTokens = %d, want 8192", entry.MaxTokens)
	}
}

func TestMergeModelDefaults_AddNew(t *testing.T) {
	baseline := builtinModelDefaults()
	user := []domain.ModelDefault{
		{Prefix: "custom-model-v1", MaxContextLength: 65536, Modality: "chat"},
	}
	merged := mergeModelDefaults(baseline, user)
	entry := findDefault(merged, "custom-model-v1")
	if entry == nil {
		t.Fatal("missing new entry")
	}
	if entry.MaxContextLength != 65536 {
		t.Fatalf("context = %d, want 65536", entry.MaxContextLength)
	}
	if entry.Modality != "chat" {
		t.Fatalf("modality = %q, want chat", entry.Modality)
	}
	// Baseline should still be present.
	if findDefault(merged, "claude-opus-4-7") == nil {
		t.Fatal("baseline entries must survive")
	}
}

func TestMergeModelDefaults_SkipsEmptyPrefix(t *testing.T) {
	baseline := builtinModelDefaults()
	user := []domain.ModelDefault{
		{Prefix: "", MaxContextLength: 100},
		{Prefix: "valid", MaxContextLength: 200},
	}
	merged := mergeModelDefaults(baseline, user)
	if findDefault(merged, "valid") == nil {
		t.Fatal("valid entry should be present")
	}
	for _, d := range merged {
		if d.Prefix == "" {
			t.Fatal("empty prefix should not be in merged result")
		}
	}
}

func TestHandleModelDefaultsGet_ReturnsMerged(t *testing.T) {
	a := &Actor{
		modelDefaults: []domain.ModelDefault{
			{Prefix: "test-model", MaxContextLength: 50000},
		},
	}
	resp, err := a.handleModelDefaultsGet(nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	entry := findDefault(resp.Items, "test-model")
	if entry == nil {
		t.Fatal("missing user entry in response")
	}
	if entry.MaxContextLength != 50000 {
		t.Fatalf("context = %d, want 50000", entry.MaxContextLength)
	}
	// Baseline should also be present.
	if findDefault(resp.Items, "claude-opus-4-7") == nil {
		t.Fatal("baseline must be present")
	}
}

func TestHandleModelDefaultsSet_RequiresHuman(t *testing.T) {
	a := &Actor{}
	// Anonymous context should be rejected.
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleModelDefaultsSet(anonCtx, domain.AIManagerModelDefaultsSetReq{
		Items: []domain.ModelDefault{{Prefix: "x", MaxContextLength: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ok {
		t.Fatal("anonymous user should be rejected")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error for anonymous user")
	}
	if !strings.Contains(resp.Error, "forbidden") {
		t.Fatalf("error %q should contain forbidden", resp.Error)
	}
}

func TestHandleModelDefaultsSet_StoresSorted(t *testing.T) {
	a := &Actor{
		store:   persist.NewFSPersist(t.TempDir()),
		actorID: "test-actor",
	}
	humanCtx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleModelDefaultsSet(humanCtx, domain.AIManagerModelDefaultsSetReq{
		Items: []domain.ModelDefault{
			{Prefix: "z-last", MaxContextLength: 1},
			{Prefix: "a-first", MaxContextLength: 2},
		},
	})
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("set failed: %s", resp.Error)
	}
	if a.modelDefaults[0].Prefix != "a-first" || a.modelDefaults[1].Prefix != "z-last" {
		t.Fatalf("entries not sorted: %+v", a.modelDefaults)
	}
}

func TestHandleModelDefaultsSet_StripsEmptyPrefix(t *testing.T) {
	a := &Actor{
		store:   persist.NewFSPersist(t.TempDir()),
		actorID: "test-actor",
	}
	humanCtx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleModelDefaultsSet(humanCtx, domain.AIManagerModelDefaultsSetReq{
		Items: []domain.ModelDefault{
			{Prefix: "", MaxContextLength: 999},
			{Prefix: "good", MaxContextLength: 100},
		},
	})
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("set failed: %s", resp.Error)
	}
	if len(a.modelDefaults) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(a.modelDefaults))
	}
	if a.modelDefaults[0].Prefix != "good" {
		t.Fatalf("expected good, got %q", a.modelDefaults[0].Prefix)
	}
}

func TestModelDefaultsPersistence(t *testing.T) {
	// Create Actor, set defaults, Save, verify via Load into another Actor.
	dir := t.TempDir()
	a1 := &Actor{
		store:   persist.NewFSPersist(dir),
		actorID: "persist-test",
	}
	a1.modelDefaults = []domain.ModelDefault{
		{Prefix: "persisted-model", MaxContextLength: 77777, MaxTokens: 4096, Modality: "chat"},
	}
	if err := a1.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Load into a fresh actor.
	a2 := &Actor{
		store:              persist.NewFSPersist(dir),
		actorID:            "persist-test",
		aggregators:        make(map[string]domain.AIManagerAggregatorGetResp),
		aggActorIDs:        make(map[string]string),
		persistedHealth:    make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		assignments:        make(map[string]string),
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	entry := findDefault(a2.modelDefaults, "persisted-model")
	if entry == nil {
		t.Fatal("persisted entry missing after load")
	}
	if entry.MaxContextLength != 77777 {
		t.Fatalf("context = %d, want 77777", entry.MaxContextLength)
	}
	if entry.MaxTokens != 4096 {
		t.Fatalf("MaxTokens = %d, want 4096", entry.MaxTokens)
	}
	if entry.Modality != "chat" {
		t.Fatalf("modality = %q, want chat", entry.Modality)
	}
}

// findDefault is a helper to find a ModelDefault by prefix in a slice.
func findDefault(slice []domain.ModelDefault, prefix string) *domain.ModelDefault {
	for i, d := range slice {
		if d.Prefix == prefix {
			return &slice[i]
		}
	}
	return nil
}
