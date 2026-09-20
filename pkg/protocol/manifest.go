package protocol

import (
	"sort"

	sporeschema "github.com/qomos-w/spore/schema"
)

// visibilityRank orders the canonical visibility tokens widest-first:
// public > admin > diagnostic > internal. Unrecognized tokens rank lowest so
// malformed input can never widen an entry's exposure.
func visibilityRank(v string) int {
	switch v {
	case "public":
		return 3
	case "admin":
		return 2
	case "diagnostic":
		return 1
	default:
		return 0
	}
}

// maxVisibility returns the wider of two canonical visibility tokens.
func maxVisibility(a, b string) string {
	if visibilityRank(a) >= visibilityRank(b) {
		return a
	}
	return b
}

// NormalizeManifestSchemasToSystem rewrites core manifest schemas to the
// canonical protocol namespace and deduplicates entries. A struct declared in
// the shared pkg/domain/gen package is registered by every actor cell that
// references it, so the same Go type can surface under several runtime-assigned
// schema IDs (e.g. ComponentVisual at 128/129/132/133). Dedup therefore keys on
// schema ID first, then on (namespace, name) so each logical type keeps exactly
// one entry — the lowest schema ID wins, preserving stable references.
//
// Duplicates can carry different visibilities: the same Go type may be the
// payload of an internal parent-callable (walk namespace "mcp") and of a
// public event (walk namespace "mcpmanager"). The surviving entry keeps the
// WIDEST visibility seen — a narrower one would drop the type from frontend
// codegen while generated event clients still reference it.
func NormalizeManifestSchemasToSystem(schemas []sporeschema.ManifestSchema) []sporeschema.ManifestSchema {
	byID := make(map[uint64]bool, len(schemas))
	byName := make(map[string]uint64, len(schemas))
	posByID := make(map[uint64]int, len(schemas))
	out := make([]sporeschema.ManifestSchema, 0, len(schemas))
	for _, s := range schemas {
		if byID[s.SchemaID] {
			if i, ok := posByID[s.SchemaID]; ok {
				out[i].Visibility = maxVisibility(out[i].Visibility, s.Visibility)
			}
			continue
		}
		// Same struct (same name) registered under multiple cell-local IDs:
		// keep the first (lowest) occurrence and drop the duplicates.
		if s.Name != "" {
			if prev, ok := byName[s.Name]; ok && prev < s.SchemaID {
				if i, ok := posByID[prev]; ok {
					out[i].Visibility = maxVisibility(out[i].Visibility, s.Visibility)
				}
				continue
			}
			if prev, ok := byName[s.Name]; ok && prev > s.SchemaID {
				// Replace the previously kept higher-ID entry with this one,
				// carrying over the widest visibility.
				if i, ok := posByID[prev]; ok {
					s.Visibility = maxVisibility(s.Visibility, out[i].Visibility)
					out[i] = sporeschema.ManifestSchema{}
					delete(posByID, prev)
				}
			}
			byName[s.Name] = s.SchemaID
		}
		byID[s.SchemaID] = true
		posByID[s.SchemaID] = len(out)
		s.Namespace = SystemNamespace
		out = append(out, s)
	}

	// Compact out any nilled replacement slots.
	compacted := out[:0]
	for _, s := range out {
		if s.Name != "" || s.SchemaID != 0 {
			compacted = append(compacted, s)
		}
	}

	sort.Slice(compacted, func(i, j int) bool {
		if compacted[i].SchemaID != compacted[j].SchemaID {
			return compacted[i].SchemaID < compacted[j].SchemaID
		}
		return compacted[i].Name < compacted[j].Name
	})
	return compacted
}
