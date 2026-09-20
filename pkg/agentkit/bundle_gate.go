package agentkit

import (
	"fmt"
	"sort"
	"strings"
)

// BundleToolDescriptionGaps enforces the bundle-list description contract for
// the LLM tool surface: every builtin bundle/mode card tools entry that names
// a service-qualified callable (contains a dot) must resolve in the exported
// manifest with a non-empty description, because ToolSpecsFromCallables is the
// only description source for service callables — an empty one degrades the
// tool spec to a bare callable ID.
//
// Bare agent-local names (plan_submit, task_create, component_mount, ...) are
// skipped by design: their registry entries are metadata shells and the
// effective descriptions are curated by the agent tool-surface shims
// (ensureLocalInteractionCallables / ensureWorktreeCallables / engine-appended
// tools), not by the registry.
//
// descriptions maps the full callable ID (service "." name) to its manifest
// description. Each returned string is a "cardID: toolID — reason" violation.
func BundleToolDescriptionGaps(descriptions map[string]string) ([]string, error) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		return nil, fmt.Errorf("load builtin assets: %w", err)
	}
	var gaps []string
	for _, asset := range assets {
		if asset.Type != "bundle" && asset.Type != "mode" {
			continue
		}
		for _, tool := range asset.Tools {
			if !strings.Contains(tool, ".") {
				continue
			}
			desc, ok := descriptions[tool]
			switch {
			case !ok:
				gaps = append(gaps, fmt.Sprintf("%s: %s — not found in exported manifest", asset.Title, tool))
			case strings.TrimSpace(desc) == "":
				gaps = append(gaps, fmt.Sprintf("%s: %s — registered without actor.WithDescription", asset.Title, tool))
			}
		}
	}
	sort.Strings(gaps)
	return gaps, nil
}
