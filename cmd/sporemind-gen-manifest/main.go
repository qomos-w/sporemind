// Command sporemind-gen-manifest exports a GosporeManifest JSON from the
// sporemind runtime without starting the HTTP gateway.
//
// It starts the App, waits for the actor tree to settle, spawns one
// gen-only prototype "agent" child so dynamically-spawned actors'
// callable surfaces are captured, calls App.ExportGosporeManifest(),
// writes the JSON, and shuts down.
//
// Usage:
//
//	go run ./cmd/sporemind-gen-manifest -o gen/gmanifest.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/ref"
	gosporeschema "github.com/qomos-w/gospore/schema"
	spore "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/cmd/internal/actorset"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

func main() {
	outPath := flag.String("o", "gen/gmanifest.json", "output JSON path")
	flag.Parse()

	a, err := runtime.New(runtime.Config{
		NoGateway: true,
		Children:  actorset.ForManifest(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest:", err)
		os.Exit(1)
	}

	// Seed previously committed dynamic schema IDs before the runtime starts.
	// This makes repeated manifest generations byte-identical when the code
	// surface is unchanged; the static fragment already fixes the Turn schemas.
	if err := importExistingManifest(a, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: import existing manifest:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = a.Run(ctx)
	}()

	if err := waitForTreeQuiescent(a, 5*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest:", err)
		os.Exit(1)
	}

	// Spawn a gen-only prototype agent so its agent.chat.submit callable
	// is registered before manifest export. Production spawns agents
	// dynamically via workspace.create_agent; this prototype only exists
	// to populate the static codegen surface.
	if err := spawnAgentPrototype(a); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: spawn prototype:", err)
		os.Exit(1)
	}

	// Spawn a gen-only prototype project so project.* callables are
	// registered before manifest export. Production spawns projects
	// dynamically via workspace.mount; this prototype only exists to
	// populate the static codegen surface.
	if err := spawnProjectPrototype(a); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: spawn project prototype:", err)
		os.Exit(1)
	}

	if err := waitForTreeQuiescent(a, 5*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest:", err)
		os.Exit(1)
	}

	gm, err := a.ExportGosporeManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: export:", err)
		os.Exit(1)
	}

	gm = stripEmptyNameSchemas(gm)
	gm.Schemas = protocol.NormalizeManifestSchemasToSystem(gm.Schemas)
	sortManifest(&gm)

	if err := checkBundleToolDescriptions(gm); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: bundle tool description gate:", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(gm, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: marshal:", err)
		os.Exit(1)
	}

	if dir := filepath.Dir(*outPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: mkdir:", err)
			os.Exit(1)
		}
	}

	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: write:", err)
		os.Exit(1)
	}

	cancel()
	_ = a.Shutdown(ctx)
	fmt.Fprintf(os.Stderr, "sporemind-gen-manifest: wrote %s (%d schemas, %d callables, %d projections, %d events)\n",
		*outPath, len(gm.Schemas), len(gm.Callables), len(gm.Projections), len(gm.Events))
}

// checkBundleToolDescriptions enforces the bundle-list description contract:
// every service-qualified callable listed in a builtin bundle/mode card's
// tools frontmatter must appear in the exported manifest with a description
// (registered via actor.WithDescription). Bare agent-local names are curated
// by the agent tool-surface shims and are out of scope (see
// agentkit.BundleToolDescriptionGaps).
func checkBundleToolDescriptions(gm gosporeschema.GosporeManifest) error {
	descriptions := make(map[string]string, len(gm.Callables))
	for _, c := range gm.Callables {
		id := c.Name
		if c.Service != "" && !strings.HasPrefix(id, c.Service+".") {
			id = c.Service + "." + id
		}
		descriptions[id] = c.Description
	}
	gaps, err := agentkit.BundleToolDescriptionGaps(descriptions)
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d violation(s):\n", len(gaps))
	for _, gap := range gaps {
		fmt.Fprintf(&b, "  - %s\n", gap)
	}
	b.WriteString("register each listed callable with actor.WithDescription, or drop it from the card's tools frontmatter")
	return fmt.Errorf("%s", b.String())
}

// spawnAgentPrototype spawns a root-level prototype agent with empty (auto)
// slots and coordinator kind so coordinator_guidance_profile_* callables
// are registered for manifest export. The prototype's OnStart must succeed
// (registers agent.chat.submit and coordinator_guidance callables) for the
// manifest to include all agent callables. Agents discover aggregators
// themselves via the topology provider at OnStart, so no binding is needed here.
func spawnAgentPrototype(a app.App) error {
	// AsyncStart avoids deadlock: agent.OnStart calls Tree.LookupID
	// which needs t.mu.RLock, but a sync Spawn holds t.mu.Lock through
	// allocator → OnStart. AsyncStart returns Ref immediately and lets
	// OnStart run later, after the tree write lock has been released.
	//
	// Name has no hyphen so codegen can derive a valid JS identifier
	// when the actor's event source becomes a generated namespace.
	//
	// Use coordinator kind so coordinator_guidance_profile_* callables are
	// registered for manifest export (these are only registered when agentKind
	// == domain.AgentKindCoordinator).
	props := actor.PropsFromFunc(agentactor.NewActor("", "coordinator", "",
		domain.ModelSlot{}, domain.ModelSlot{}, domain.ModelSlot{},
		domain.ModelSlot{}, domain.ModelSlot{}, nil, "", "", nil)).WithAsyncStart()
	if _, err := a.Spawn(props, "agentproto"); err != nil {
		return fmt.Errorf("spawn agentproto: %w", err)
	}
	return nil
}

// spawnProjectPrototype creates a root-level project actor so its callable
// surface (project.read/write/edit, project.shell_exec, project.git_*, etc.) is
// included in the exported manifest for TypeScript client generation.
func spawnProjectPrototype(a app.App) error {
	props := actor.PropsFromFunc(project.NewActor(""))
	if _, err := a.Spawn(props, "projectproto"); err != nil {
		return fmt.Errorf("spawn projectproto: %w", err)
	}
	return nil
}

// waitForTreeQuiescent blocks until the actor tree's node count has
// remained stable for a short window, indicating that Spawn / Register
// have settled. No hard-coded actor list — the runtime topology in
// pkg/runtime is the single source of truth.
func waitForTreeQuiescent(a app.App, timeout time.Duration) error {
	// Tree node count alone does not guarantee OnStart has finished and
	// callables are registered (especially for actors using AsyncStart).
	// Wait for all cells to reach a started/stopped state first.
	if sw, ok := a.(interface{ WaitForAllCellsStart(time.Duration) error }); ok {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: waiting for all cells to start")
		if err := sw.WaitForAllCellsStart(timeout); err != nil {
			return fmt.Errorf("wait for cells to start: %w", err)
		}
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: all cells started")
	} else {
		fmt.Fprintln(os.Stderr, "sporemind-gen-manifest: WaitForAllCellsStart not available")
	}
	const (
		pollInterval     = 10 * time.Millisecond
		stableIterations = 5
	)

	deadline := time.Now().Add(timeout)
	stable := 0
	prev := -1

	for time.Now().Before(deadline) {
		count := 0
		a.Tree().Walk(func(ref.Ref) bool {
			count++
			return true
		})

		if count > 0 && count == prev {
			stable++
			if stable >= stableIterations {
				return nil
			}
		} else {
			stable = 0
		}
		prev = count
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timeout waiting for actor tree to settle (last count: %d)", prev)
}

// stripEmptyNameSchemas removes schema entries whose Name is empty.
// These are namespace markers produced by gospore that gospore-gen-ts
// cannot handle (it needs a valid identifier to emit a TS type).
func stripEmptyNameSchemas(gm gosporeschema.GosporeManifest) gosporeschema.GosporeManifest {
	n := 0
	for _, s := range gm.Schemas {
		if s.Name != "" {
			gm.Schemas[n] = s
			n++
		}
	}
	gm.Schemas = gm.Schemas[:n]
	return gm
}

// sortManifest makes the export deterministic: tree-walk order depends on
// async actor start timing, so without sorting, repeated exports of an
// unchanged surface produce diffs that flap gen-manifest-check.
func sortManifest(gm *gosporeschema.GosporeManifest) {
	sort.Slice(gm.Schemas, func(i, j int) bool {
		a, b := gm.Schemas[i], gm.Schemas[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	sort.Slice(gm.Callables, func(i, j int) bool {
		a, b := gm.Callables[i], gm.Callables[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	sort.Slice(gm.Projections, func(i, j int) bool {
		a, b := gm.Projections[i], gm.Projections[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Component < b.Component
	})
	sort.Slice(gm.Events, func(i, j int) bool {
		a, b := gm.Events[i], gm.Events[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Kind < b.Kind
	})
}

// importExistingManifest seeds the App's schema set with the schemas from the
// previously committed gmanifest. Locking in those IDs makes repeated manifest
// generations byte-identical when the code surface is unchanged; only newly
// added or changed runtime types receive fresh auto-allocated IDs. Schemas that
// conflict with the static fragment or with a changed runtime shape are
// skipped so the runtime can auto-allocate a fresh ID for them.
func importExistingManifest(a app.App, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var gm gosporeschema.GosporeManifest
	if err := json.Unmarshal(data, &gm); err != nil {
		return fmt.Errorf("parse existing manifest: %w", err)
	}
	seen := make(map[string]struct{})
	for _, ms := range gm.Schemas {
		if ms.Name == "" {
			continue
		}
		if ms.Namespace != protocol.SystemNamespace {
			continue
		}
		if _, ok := seen[ms.Name]; ok {
			continue
		}
		seen[ms.Name] = struct{}{}
		desc := spore.TypeDesc{
			Kind:      ms.Object.Kind,
			Name:      ms.Object.Name,
			ClassName: ms.Object.Name,
			ClassID:   ms.SchemaID,
		}
		if err := a.Schemas().Register(ms.SchemaID, ms.Name, desc, ms.Object); err != nil {
			if errors.Is(err, gosporeschema.ErrSchemaIDTaken) || errors.Is(err, gosporeschema.ErrSchemaNameTaken) {
				continue
			}
			return fmt.Errorf("import existing manifest schema %q (id %d): %w", ms.Name, ms.SchemaID, err)
		}
	}
	return nil
}
