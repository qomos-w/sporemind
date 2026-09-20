package actorset

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/runtime/topo"
)

func TestDefaultSetOrderEquivalent(t *testing.T) {
	specs := Default()
	nodes := make([]string, 0, len(specs))
	deps := make(map[string][]string, len(specs))
	seen := make(map[string]bool, len(specs))

	for i, spec := range specs {
		nodes = append(nodes, spec.Name)
		if seen[spec.Name] {
			t.Fatalf("duplicate actor name %q at index %d", spec.Name, i)
		}
		seen[spec.Name] = true
		if len(spec.DependsOn) > 0 {
			deps[spec.Name] = append([]string(nil), spec.DependsOn...)
		}
	}

	// Verify all dependency names are known and precede the dependent in
	// the original list.
	for i, spec := range specs {
		for _, dep := range spec.DependsOn {
			if !seen[dep] {
				t.Fatalf("actor %q depends on unknown actor %q", spec.Name, dep)
			}
			depIdx := -1
			for j, s := range specs {
				if s.Name == dep {
					depIdx = j
					break
				}
			}
			if depIdx >= i {
				t.Fatalf("actor %q (index %d) depends on %q (index %d), which is not before it", spec.Name, i, dep, depIdx)
			}
		}
	}

	// The topological sort must reproduce the original order because
	// dependencies only point backwards and the sort is stable.
	result, err := topo.Sort(nodes, deps)
	if err != nil {
		t.Fatalf("topo.Sort failed: %v", err)
	}
	if len(result) != len(specs) {
		t.Fatalf("topo.Sort returned %d nodes, expected %d", len(result), len(specs))
	}
	for i, name := range result {
		if name != specs[i].Name {
			t.Fatalf("topo.Sort order differs at index %d: got %q, expected %q", i, name, specs[i].Name)
		}
	}

	// Actors whose handlers call ctx.Planner() (cross-actor lookups via
	// planner.Call) must be spawned with Planner: true; otherwise those
	// callables return "planner not available" at runtime (or silently
	// degrade). appmanager's dev surface, oracle's search_services /
	// capability_discover, im's default route, and workspace's clone /
	// agent-lifecycle dispatch all depend on it.
	plannerActors := []string{"appmanager", "browsermanager", "sshmanager", "glassinteract", "pluginhost", "oracle", "im", "workspace", "dbclient"}
	byName := make(map[string]runtime.ChildSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}
	for _, name := range plannerActors {
		if !byName[name].Planner {
			t.Errorf("actor %q must be spawned with Planner: true (dev/runtime surfaces call ctx.Planner())", name)
		}
	}

	// The workbench attention actor owns durable layout state, so it must be
	// present and persistent (score state is restored across restarts).
	if wb, ok := byName["workbench"]; !ok {
		t.Error("workbench actor missing from Default()")
	} else if !wb.RequirePersistent {
		t.Error("workbench actor must be RequirePersistent")
	}
}
