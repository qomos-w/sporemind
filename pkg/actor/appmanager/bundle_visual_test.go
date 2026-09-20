package appmanager

import (
	"strconv"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestBundleVisualExplicitColorOverride: a non-empty bundle.Color is used
// verbatim and its palette accent is derived from the hex value (nearest RGB).
func TestBundleVisualExplicitColorOverride(t *testing.T) {
	cases := []struct {
		name       string
		color      string
		wantAccent string
	}{
		{"exact palette match blue", "#2563eb", "blue"},
		{"exact palette match purple", "#9333ea", "purple"},
		{"leading hash optional", "0891b2", "cyan"},
		{"three digit shorthand", "#f00", "red"},
		{"near palette match", "#1e40af", "blue"},
		{"arbitrary nearest", "#ff0000", "red"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := bundleVisual("app.any", "any-slug", tc.color, "package")
			if v == nil {
				t.Fatal("visual is nil")
			}
			if v.Color != tc.color {
				t.Errorf("color = %q, want verbatim %q", v.Color, tc.color)
			}
			if v.Accent != tc.wantAccent {
				t.Errorf("accent = %q, want %q", v.Accent, tc.wantAccent)
			}
			if v.Icon != "package" || v.Emphasis != "normal" {
				t.Errorf("icon/emphasis = %q/%q, want package/normal", v.Icon, v.Emphasis)
			}
		})
	}
}

// TestBundleVisualUnparseableColorKeepsHashedAccent: an explicit color that is
// not a hex value is still used verbatim, while the accent falls back to the
// stable hash (both fields stay populated).
func TestBundleVisualUnparseableColorKeepsHashedAccent(t *testing.T) {
	v := bundleVisual("app.bundledemo", "core-tools", "rebeccapurple", "package")
	if v.Color != "rebeccapurple" {
		t.Errorf("color = %q, want verbatim rebeccapurple", v.Color)
	}
	if want := bundleHashedAccent("app.bundledemo", "core-tools"); v.Accent != want {
		t.Errorf("accent = %q, want hashed %q", v.Accent, want)
	}
}

// TestBundleVisualHashFallback: an empty color selects accent+color by stable
// hash; the chosen color is always the palette hex of the selected accent.
func TestBundleVisualHashFallback(t *testing.T) {
	v := bundleVisual("app.bundledemo", "core-tools", "", "shield-check")
	if v.Accent != "purple" {
		t.Errorf("hashed accent = %q, want purple (golden)", v.Accent)
	}
	if want := bundleColorForAccent(v.Accent); v.Color != want {
		t.Errorf("fallback color = %q, want palette color %q for accent %q", v.Color, want, v.Accent)
	}
	if v.Accent == "" || v.Color == "" {
		t.Errorf("fallback must populate both accent and color: %+v", v)
	}
}

// TestBundleHashedAccentStable locks the hash contract: the same input always
// selects the same accent, and specific inputs map to specific accents (a change
// to the hash algorithm or palette order must edit this table deliberately).
func TestBundleHashedAccentStable(t *testing.T) {
	golden := []struct {
		appID, slug, want string
	}{
		{"app.bundledemo", "core-tools", "purple"},
		{"app.bundledemo", "extra-tools", "red"},
		{"app.a", "x", "cyan"},
		{"app.bundledemo", "authenticator-tools", "amber"},
	}
	for _, g := range golden {
		if got := bundleHashedAccent(g.appID, g.slug); got != g.want {
			t.Errorf("bundleHashedAccent(%q, %q) = %q, want %q", g.appID, g.slug, got, g.want)
		}
		for i := 0; i < 100; i++ {
			if got := bundleHashedAccent(g.appID, g.slug); got != g.want {
				t.Fatalf("bundleHashedAccent(%q, %q) not stable on call %d: %q", g.appID, g.slug, i, got)
			}
		}
	}
}

// TestBundleHashedAccentCoversPalette: the hash distributes across every accent
// in the palette (no bucket is unreachable).
func TestBundleHashedAccentCoversPalette(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 500; i++ {
		seen[bundleHashedAccent("app.dist."+strconv.Itoa(i), "tools")]++
	}
	if len(seen) != len(bundleAccentPalette) {
		t.Fatalf("accent coverage = %d/%d: %v", len(seen), len(bundleAccentPalette), seen)
	}
	for _, p := range bundleAccentPalette {
		if seen[p.Accent] == 0 {
			t.Errorf("palette accent %q never selected by hash", p.Accent)
		}
	}
}

// TestBundleDescriptorVisualProjection: the virtual component descriptor carries
// the resolved visual end-to-end (explicit color override, hashed fallback) —
// no hardcoded purple.
func TestBundleDescriptorVisualProjection(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Color: "#16a34a", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
		{Title: "Extra Tools", Tools: []gen.AppBundleTool{{CallableID: "extra"}}},
	}))
	a := newBundleCardsActor(t)
	if _, err := a.handleRegisterProject(env.ctx(projectID), gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("register: %v", err)
	}
	list, err := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if err != nil {
		t.Fatalf("component_list: %v", err)
	}
	byID := map[string]gen.ComponentDescriptor{}
	for _, d := range list.Items {
		byID[d.Ref.CardID] = d
	}

	explicit, ok := byID["app-bundle:app.bundledemo:core-tools"]
	if !ok {
		t.Fatalf("missing explicit-color descriptor, got %v", byID)
	}
	if explicit.Visual == nil || explicit.Visual.Color != "#16a34a" || explicit.Visual.Accent != "green" {
		t.Errorf("explicit visual = %+v, want green/#16a34a", explicit.Visual)
	}

	hashed, ok := byID["app-bundle:app.bundledemo:extra-tools"]
	if !ok {
		t.Fatalf("missing hashed descriptor, got %v", byID)
	}
	want := bundleVisual("app.bundledemo", "extra-tools", "", "package")
	if hashed.Visual == nil || hashed.Visual.Accent != want.Accent || hashed.Visual.Color != want.Color {
		t.Errorf("hashed visual = %+v, want %+v", hashed.Visual, want)
	}
}
