package appmanager

import (
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// manifestCapabilitySet converts the manifest's declared Permissions into the
// runtime granted-capability set. Under the declaration-is-authorization
// model this is the only source: the spore app actor's host-call gate and the
// native pluginhost bridge both consume it directly.
func manifestCapabilitySet(perms []string) map[string]struct{} {
	out := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

// validateManifestSecurity enforces registration-time security invariants:
//
//  1. Every declared permission must be a known host capability. Under the
//     declaration-is-authorization model the app's own Permissions are its
//     granted set — there is no separate host allowlist — so the only gate
//     here is the capability vocabulary itself: a typo or invented capability
//     string is rejected instead of silently accepted.
//  2. callable/event Permission refs must resolve against manifest.Permissions.
//  3. dependencies must reference already-registered apps with matching
//     version (and package hash when the dependency pins one).
//  4. schema refs must be well-formed (name + hash present).
//
// All violations return stable-coded DeniedError values so callers and audits
// share the same diagnostic codes.
func validateManifestSecurity(m gen.AppManifest, records map[string]appRecord) error {
	for _, p := range m.Permissions {
		if p == "" {
			continue
		}
		if !appbinding.IsKnownHostCapability(p) {
			return appbinding.Deny(appbinding.CodePermissionDenied,
				fmt.Sprintf("manifest declares unknown host capability %q; see the host capability catalog for recognized values", p))
		}
	}
	permSet := map[string]bool{}
	for _, p := range m.Permissions {
		permSet[p] = true
	}
	callableIDs := map[string]bool{}
	for _, c := range m.Callables {
		callableIDs[c.ID] = true
		if c.Permission != "" && !permSet[c.Permission] {
			return appbinding.Deny(appbinding.CodePermissionDenied,
				fmt.Sprintf("callable %q requires undeclared permission %q", c.ID, c.Permission))
		}
	}
	for _, e := range m.Events {
		if e.Permission != "" && !permSet[e.Permission] {
			return appbinding.Deny(appbinding.CodePermissionDenied,
				fmt.Sprintf("event %q requires undeclared permission %q", e.ID, e.Permission))
		}
	}
	for _, p := range m.Permissions {
		if !strings.HasPrefix(p, "plugin.") {
			continue
		}
		// A plugin.* permission is a per-callID bundle gate; the target
		// plugin must be a declared dependency (plugin.<depID>.<callable>).
		declared := false
		for _, dep := range m.Dependencies {
			if strings.HasPrefix(p, "plugin."+dep.ID+".") {
				declared = true
				break
			}
		}
		if !declared {
			return appbinding.Deny(appbinding.CodeDependencyMissing,
				fmt.Sprintf("permission %q references a plugin that is not a declared dependency; add a dependency block for it", p))
		}
	}
	for _, dep := range m.Dependencies {
		rec, ok := records[dep.ID]
		if !ok {
			return appbinding.Deny(appbinding.CodeDependencyMissing, "dependency not registered: "+dep.ID)
		}
		if dep.Version != "" && rec.Manifest.Version != dep.Version {
			return appbinding.Deny(appbinding.CodeDependencyMissing,
				fmt.Sprintf("dependency %q version %q does not match registered %q", dep.ID, dep.Version, rec.Manifest.Version))
		}
		if dep.Hash != "" && rec.PackageHash != dep.Hash {
			return appbinding.Deny(appbinding.CodeDependencyMissing,
				fmt.Sprintf("dependency %q hash mismatch", dep.ID))
		}
	}
	// Dependency cycles are rejected: A→B→A would deadlock load ordering.
	// DFS from the new manifest through registered apps' dependencies.
	if err := detectDependencyCycle(m, records); err != nil {
		return err
	}
	// Bundle-level plugin permissions (plugin.<depID>.<bundleSlug>) resolve
	// against the dependency's registered manifest; validate them here so an
	// unknown or ambiguous name fails at register time with the cause instead
	// of at first invoke.
	if _, err := expandBundlePermissions(m, records); err != nil {
		return err
	}
	for _, s := range m.Schemas {
		if s.Name == "" {
			return appbinding.Deny(appbinding.CodeManifestInvalid, "schema ref missing name")
		}
	}
	// Entrypoints must be well-formed: kind + id are required so the
	// workbench can route into the app.
	for _, ep := range m.Entrypoints {
		if ep.Kind == "" || ep.ID == "" {
			return appbinding.Deny(appbinding.CodeManifestInvalid, "entrypoint requires kind and id")
		}
	}
	return nil
}

// detectDependencyCycle walks the dependency graph starting from the new
// manifest through registered apps' dependencies, rejecting any path that
// returns to a node already on the current stack. Registrations themselves
// are validated at their own register time, so cycles only emerge when the
// NEW manifest closes a loop through already-registered apps.
func detectDependencyCycle(m gen.AppManifest, records map[string]appRecord) error {
	var visit func(id string, stack []string) error
	visit = func(id string, stack []string) error {
		for _, s := range stack {
			if s == id {
				return appbinding.Deny(appbinding.CodeDependencyMissing,
					fmt.Sprintf("dependency cycle detected: %s -> %s", strings.Join(stack, " -> "), id))
			}
		}
		stack = append(stack, id)
		var deps []gen.AppDependency
		if id == m.ID {
			deps = m.Dependencies
		} else if rec, ok := records[id]; ok {
			deps = rec.Manifest.Dependencies
		} else {
			return nil // existence checked separately above
		}
		for _, dep := range deps {
			if err := visit(dep.ID, stack); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(m.ID, nil)
}

// expandBundlePermissions resolves plugin.* permissions that name a
// dependency bundle (plugin.<depID>.<bundleSlug>) into the exact
// per-callable plugin.* callIDs the host bridge gates on. Callable-level
// permissions (plugin.<depID>.<callableID>) pass through verbatim — their
// authorization stays the host bridge's exact-match check — as does every
// non-plugin entry, so a manifest without bundle-level permissions returns
// its own list unchanged. A bundle slug that is also a callable ID of the
// dependency is rejected (ambiguous). The output is deduplicated and
// order-preserving.
func expandBundlePermissions(m gen.AppManifest, records map[string]appRecord) ([]string, error) {
	hasPlugin := false
	for _, p := range m.Permissions {
		if strings.HasPrefix(p, "plugin.") {
			hasPlugin = true
			break
		}
	}
	if !hasPlugin {
		return m.Permissions, nil
	}
	out := make([]string, 0, len(m.Permissions))
	seen := make(map[string]bool, len(m.Permissions))
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range m.Permissions {
		if !strings.HasPrefix(p, "plugin.") {
			add(p)
			continue
		}
		depID, rest, ok := splitPluginPermission(p, m.Dependencies)
		if !ok {
			return nil, appbinding.Deny(appbinding.CodeDependencyMissing,
				fmt.Sprintf("permission %q references a plugin that is not a declared dependency; add a dependency block for it", p))
		}
		rec, registered := records[depID]
		if !registered {
			return nil, appbinding.Deny(appbinding.CodeDependencyMissing, "dependency not registered: "+depID)
		}
		calls, isBundle := bundleSlugMap(rec.Manifest.Bundles)[rest]
		if !isBundle {
			// Callable-level gate: exact-callID authorization is enforced by
			// the host bridge, and register-time existence checking is
			// deliberately not done here (the dependency may reload its
			// callables later).
			add(p)
			continue
		}
		if manifestHasCallable(rec.Manifest, rest) {
			return nil, appbinding.Deny(appbinding.CodePermissionDenied,
				fmt.Sprintf("permission %q is ambiguous: %q is both a bundle and a callable of dependency %q; rename one of them", p, rest, depID))
		}
		if len(calls) == 0 {
			return nil, appbinding.Deny(appbinding.CodeDependencyMissing,
				fmt.Sprintf("permission %q grants nothing: bundle %q of dependency %q declares no callables", p, rest, depID))
		}
		for _, c := range calls {
			add("plugin." + depID + "." + c)
		}
	}
	return out, nil
}

// splitPluginPermission splits a plugin.* permission into its declared
// dependency ID and the trailing bundle-or-callable segment. Dependency IDs
// may themselves contain dots (app.translator), so when several declared
// dependencies prefix-match the permission the longest — most specific — one
// wins.
func splitPluginPermission(p string, deps []gen.AppDependency) (depID, rest string, ok bool) {
	best := ""
	for _, dep := range deps {
		prefix := "plugin." + dep.ID + "."
		if len(p) > len(prefix) && strings.HasPrefix(p, prefix) && len(dep.ID) > len(best) {
			best = dep.ID
		}
	}
	if best == "" {
		return "", "", false
	}
	return best, p[len("plugin."+best+"."):], true
}

// manifestHasCallable reports whether id is a declared callable of m.
func manifestHasCallable(m gen.AppManifest, id string) bool {
	for _, c := range m.Callables {
		if c.ID == id {
			return true
		}
	}
	return false
}

// loadAuthManifest returns the manifest form sent to the pluginhost at
// artifact load/prepare boundaries: bundle-level plugin.* permissions
// expanded against the currently registered dependencies into the exact
// callIDs the host bridge gates on. The appmanager record keeps the declared
// form; the expansion persists only inside the pluginhost's ArtifactLoads,
// so a host restart restores the same authorization set without re-resolving
// bundles against (possibly changed) dependencies.
func (a *Actor) loadAuthManifest(m gen.AppManifest) (gen.AppManifest, error) {
	var perms []string
	var expandErr error
	a.withMu(func() {
		perms, expandErr = expandBundlePermissions(m, a.Records)
	})
	if expandErr != nil {
		return gen.AppManifest{}, expandErr
	}
	out := m
	out.Permissions = perms
	return out, nil
}

// validateEntrypointExports checks that every surface binding entrypoint is
// declared in the manifest's entrypoints. With agent_binding removed from
// appdef, surface bindings only exist in legacy manifests; this validation
// is a no-op when AgentBinding is nil or has no Surface.
func validateEntrypointExports(m gen.AppManifest) error {
	if m.AgentBinding == nil || m.AgentBinding.Surface == nil {
		return nil
	}
	entry := m.AgentBinding.Surface.Entrypoint
	if entry == "" {
		return nil
	}
	for _, ep := range m.Entrypoints {
		if ep.ID == entry {
			return nil
		}
	}
	return appbinding.Deny(appbinding.CodeManifestInvalid,
		"surface binding references undeclared entrypoint: "+entry)
}
