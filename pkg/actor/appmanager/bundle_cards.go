package appmanager

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// slugify converts a bundle Title into a URL-friendly slug: lowercase, every
// character outside [a-z0-9] becomes '-', then the result is trimmed of '-'.
// This is the canonical slug rule for app-bundle component ids.
func slugify(title string) string {
	title = strings.ToLower(title)
	var b strings.Builder
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// bundleCardID returns the virtual component id for an app bundle:
// app-bundle:{appID}:{slug}.
func bundleCardID(appID, slug string) string {
	return "app-bundle:" + appID + ":" + slug
}

// App bundles are virtual components: the bundle definitions live inside the
// artifact manifest and are projected on demand from the running registry.
// No card entity is written to any wiki — a bundle is visible exactly while
// its app's artifact is loaded, for every project on the host, and disappears
// on unload/unregister/reload-replacement automatically because the registry
// is the only source.

// bundleCardIDs derives the virtual card ids for one manifest's bundles,
// applying the same slug dedup rule (title slug, then -2, -3, ...).
func bundleCardIDs(appID string, bundles []gen.AppBundle) []string {
	used := map[string]bool{}
	ids := make([]string, 0, len(bundles))
	for _, bundle := range bundles {
		ids = append(ids, bundleCardID(appID, claimBundleSlug(bundle.Title, used)))
	}
	return ids
}

// bundleSlugMap maps each bundle's canonical slug — the same title-slug +
// dedup rule bundleCardIDs applies — to the callable IDs its Tools declare.
// It is the expansion source for plugin.<depID>.<bundleSlug> permissions.
func bundleSlugMap(bundles []gen.AppBundle) map[string][]string {
	used := map[string]bool{}
	out := make(map[string][]string, len(bundles))
	for _, bundle := range bundles {
		calls := make([]string, 0, len(bundle.Tools))
		for _, t := range bundle.Tools {
			calls = append(calls, t.CallableID)
		}
		out[claimBundleSlug(bundle.Title, used)] = calls
	}
	return out
}

// claimBundleSlug returns the canonical slug for one bundle title and marks
// it used: the slugified title (falling back to "bundle"), with -2, -3, ...
// suffixes when the base slug was already claimed.
func claimBundleSlug(title string, used map[string]bool) string {
	slug := slugify(title)
	if slug == "" {
		slug = "bundle"
	}
	base := slug
	for i := 2; used[slug]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	used[slug] = true
	return slug
}

// bundleComponentDescriptor projects one manifest bundle into a component
// descriptor whose shape mirrors what a materialized bundle card produced:
// tool contributions carry the app-tool ids (app.{appID}.{CallableID}) and the
// description becomes the tool_guidance prompt contribution (the card body).
// External app bundles carry an explicit priority floor so they always compile
// after every system (builtin) bundle contribution in the same section.
//
// The card visual is never hardcoded: an explicit bundle.Color wins and its
// palette accent is derived from the hex, otherwise a stable hash of the app
// id + bundle slug selects an accent+color pair from bundleAccentPalette.
const appBundlePromptPriority = 1000

func bundleComponentDescriptor(appID, slug string, bundle gen.AppBundle, cardID string) gen.ComponentDescriptor {
	icon := bundleIconOrFallback(bundle.Icon)
	descriptor := gen.ComponentDescriptor{
		Ref:          gen.ComponentRef{CardID: cardID, Kind: "bundle", Source: "appmanager"},
		Title:        bundle.Title,
		Icon:         icon,
		Visual:       bundleVisual(appID, slug, bundle.Color, icon),
		Dependencies: []gen.ComponentDependency{},
		Prompts:      []gen.ComponentPromptContribution{},
		Tools:        []gen.ComponentToolContribution{},
	}
	if desc := strings.TrimSpace(bundle.Description); desc != "" {
		descriptor.Prompts = append(descriptor.Prompts, gen.ComponentPromptContribution{
			ID:        cardID + ":body",
			CardID:    cardID,
			Title:     cardID,
			Text:      desc,
			Placement: "tool_guidance",
			Priority:  appBundlePromptPriority,
		})
	}
	for _, tool := range bundle.Tools {
		callable := "app." + appID + "." + tool.CallableID
		descriptor.Tools = append(descriptor.Tools, gen.ComponentToolContribution{
			ID:         callable,
			CardID:     cardID,
			CallableID: callable,
			Name:       bundle.Title,
		})
	}
	return descriptor
}

// bundleAccentPalette is the Go mirror of the web card accent palette
// (web/src/ui/ai/components/cardVisual.ts ACCENT_STYLES light border values,
// exposed to the UI as CARD_ACCENT_PRESET_HEX). Each entry pairs a CardAccent
// name with its canonical light-theme hex so a hashed or color-derived accent
// and its color stay in sync with the frontend. The order is part of the
// stable-hash contract (index = hash mod len) and must not be reshuffled.
var bundleAccentPalette = []struct {
	Accent string
	Color  string
}{
	{"blue", "#2563eb"},
	{"green", "#16a34a"},
	{"amber", "#d97706"},
	{"red", "#dc2626"},
	{"purple", "#9333ea"},
	{"pink", "#db2777"},
	{"slate", "#475569"},
	{"cyan", "#0891b2"},
}

// bundleVisual resolves the card visual for one bundle. An explicit color is
// used verbatim and its accent is derived by nearest-RGB palette match; an
// empty color falls back to a stable hash of "{appID}:{slug}" over the palette,
// so the same bundle always renders the same accent+color across reloads and
// hosts. Only parseable hex colors can be distance-matched — an unparseable
// explicit color keeps the hashed accent.
func bundleVisual(appID, slug, color, icon string) *gen.ComponentVisual {
	accent := bundleHashedAccent(appID, slug)
	visual := &gen.ComponentVisual{
		Icon:     icon,
		Accent:   accent,
		Color:    bundleColorForAccent(accent),
		Emphasis: "normal",
	}
	if c := strings.TrimSpace(color); c != "" {
		visual.Color = c
		if matched, ok := bundleAccentForColor(c); ok {
			visual.Accent = matched
		}
	}
	return visual
}

// bundleHashedAccent picks a palette accent from a stable FNV-1a hash of the
// bundle identity. The same input always yields the same accent.
func bundleHashedAccent(appID, slug string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(appID + ":" + slug))
	return bundleAccentPalette[h.Sum32()%uint32(len(bundleAccentPalette))].Accent
}

// bundleColorForAccent returns the palette hex for an accent, defaulting to the
// first entry for an unknown accent.
func bundleColorForAccent(accent string) string {
	for _, p := range bundleAccentPalette {
		if p.Accent == accent {
			return p.Color
		}
	}
	return bundleAccentPalette[0].Color
}

// bundleAccentForColor derives the palette accent closest to a hex color by
// squared RGB distance. It reports false when the color is not a 3/6-digit hex.
func bundleAccentForColor(color string) (string, bool) {
	r, g, b, ok := parseHexColor(color)
	if !ok {
		return "", false
	}
	best := bundleAccentPalette[0].Accent
	bestDist := math.MaxInt32
	for _, p := range bundleAccentPalette {
		pr, pg, pb, _ := parseHexColor(p.Color)
		dr, dg, db := r-pr, g-pg, b-pb
		if dist := dr*dr + dg*dg + db*db; dist < bestDist {
			best, bestDist = p.Accent, dist
		}
	}
	return best, true
}

// parseHexColor parses "#rrggbb" or "#rgb" (leading '#' optional) into 8-bit RGB
// components.
func parseHexColor(s string) (int, int, int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(s) {
	case 3:
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

// appBundleDescriptors projects every loaded (running) app's manifest bundles
// into virtual component descriptors, sorted by card id.
// bundleRecordLive reports whether an app record's artifact is currently
// serving: "running" is the spore-runtime state, "active" the native/plugin
// load state. Other states (stopped, unload_failed, restart_pending) must not
// project their bundles.
func (a *Actor) appBundleDescriptors() []gen.ComponentDescriptor {
	a.mu.Lock()
	defer a.mu.Unlock()
	var descriptors []gen.ComponentDescriptor
	for appID, record := range a.Records {
		if !recordLive(record.State) || len(record.Manifest.Bundles) == 0 {
			continue
		}
		used := map[string]bool{}
		for _, bundle := range record.Manifest.Bundles {
			slug := claimBundleSlug(bundle.Title, used)
			descriptors = append(descriptors, bundleComponentDescriptor(appID, slug, bundle, bundleCardID(appID, slug)))
		}
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].Ref.CardID < descriptors[j].Ref.CardID
	})
	return descriptors
}

// handleComponentList serves appmanager.component_list: the host-wide virtual
// bundle catalog projected from running apps.
func (a *Actor) handleComponentList(_ actor.PureContext, _ gen.AppManagerComponentListReq) (gen.AppManagerComponentListResp, error) {
	return gen.AppManagerComponentListResp{Items: a.appBundleDescriptors()}, nil
}

// handleComponentGet serves appmanager.component_get: resolves one virtual
// app-bundle component by card id. Unknown ids (or ids whose app is no longer
// loaded) return an error.
func (a *Actor) handleComponentGet(_ actor.PureContext, req gen.AppManagerComponentGetReq) (gen.AppManagerComponentGetResp, error) {
	for _, descriptor := range a.appBundleDescriptors() {
		if descriptor.Ref.CardID == req.CardID {
			return gen.AppManagerComponentGetResp{Component: descriptor}, nil
		}
	}
	return gen.AppManagerComponentGetResp{}, fmt.Errorf("appmanager: component %q not found (app bundles are virtual: the id exists only while its app artifact is loaded)", req.CardID)
}
