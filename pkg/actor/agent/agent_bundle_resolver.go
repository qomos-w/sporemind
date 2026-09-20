package agent

import (
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
)

// resolveBundleCallableIDs resolves the union of callable IDs declared by the
// given bundle card IDs, covering both sources:
//
//   - builtin:* IDs resolve from the embedded agentkit assets via
//     agentkit.CallableIDsForBundles (unchanged behavior — these never touch
//     the project actor).
//   - any other ID is treated as a project card (e.g. app-bundle:{appID}:{slug}
//     published by appmanager) and resolved via project.component_get's
//     data.tools. Cards that cannot be resolved (project unreachable or card
//     missing) are silently skipped so a stale DefaultBundleIDs entry never
//     breaks the agent's tool surface.
//
// agentkit is a pure package with no actor access, so project-card resolution
// must happen here, in the agent layer, where the actor context is available.
func (a *Actor) resolveBundleCallableIDs(ctx actor.Context, bundleIDs []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(bundleIDs))
	add := func(callables ...string) {
		for _, c := range callables {
			if c == "" {
				continue
			}
			if _, dup := seen[c]; dup {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}

	// Batch the builtin IDs into a single asset load.
	var builtinIDs []string
	for _, id := range bundleIDs {
		if strings.HasPrefix(id, "builtin:") {
			builtinIDs = append(builtinIDs, id)
			continue
		}
		descriptor, ok := a.componentDescriptor(ctx, id)
		if !ok {
			continue
		}
		for _, t := range descriptor.Tools {
			add(t.CallableID)
		}
	}
	if len(builtinIDs) > 0 {
		add(agentkit.CallableIDsForBundles(builtinIDs)...)
	}
	return out
}
