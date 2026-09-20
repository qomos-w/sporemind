package agent

// This file consolidates package-level variable declarations for the agent
// package. Each section groups related vars by responsibility.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient/nativetools"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// --- Persistent store & tool registry ---

// TODO(actor-ownership): migrate to actor-owned state
var agentStore persist.Persist = persist.MustNew(config.PersistConfig("agent"))

// TODO(actor-ownership): migrate to actor-owned state
var nativeToolRegistry = nativetools.DefaultRegistry()

// --- Explorer / general name pools ---

// TODO(actor-ownership): migrate to actor-owned state
var explorerNamePool = []string{
	"Scout Ant", "Probe Bee", "Radar Moth", "Compass Spider", "Trail Beetle",
	"Sonar Bat", "Range Owl", "Patrol Hawk", "Drift Fox", "Roam Wolf",
	"Seeker Cat", "Glance Fish", "Gaze Frog", "Browse Deer", "Scan Crow",
	"Survey Ape", "Chart Whale", "Map Rabbit", "Trace Hound", "Path Lynx",
	"Signal Raven", "Scope Seal", "Loom Crab", "Grid Eel", "Mark Dove",
	"Hint Wren", "Clue Mink", "Trek Pony", "Venture Boar", "Pioneer Bear",
}

// TODO(actor-ownership): migrate to actor-owned state
var generalNamePool = []string{
	"Surge Ant", "Forge Bee", "Pulse Moth", "Anvil Spider", "Spark Beetle",
	"Bolt Bat", "Gear Owl", "Rivet Hawk", "Flux Fox", "Hammer Wolf",
	"Drill Cat", "Mesh Fish", "Switch Frog", "Toggle Deer", "Cache Crow",
	"Weld Ape", "Tinker Whale", "Latch Rabbit", "Wire Hound", "Node Lynx",
	"Cog Raven", "Pivot Seal", "Grit Crab", "Helix Eel", "Beam Dove",
	"Solder Wren", "Frame Mink", "Latch Pony", "Piston Boar", "Atlas Bear",
}

// --- Builtin skill ID cache ---

// builtinSkillIDSet returns the set of embed builtin skill IDs (without the
// "skill:" prefix), cached after first computation. It is the authority for
// deciding whether a skill is builtin (owned by the workspace system project)
// or project-level (owned by the current project store).
//
// TODO(actor-ownership): migrate to actor-owned state
var builtinSkillIDSet = sync.OnceValue(func() map[string]struct{} {
	assets, err := agentkit.LoadSkillAssets()
	if err != nil {
		return map[string]struct{}{}
	}
	set := make(map[string]struct{}, len(assets))
	for _, a := range assets {
		set[a.Title] = struct{}{}
	}
	return set
})

// --- Injectable test hooks ---

// summarizeViaPlan calls aiaggregator.summarize via a Plan node with context
// cancellation support, avoiding the Call().Await() deadlock risk.
// The package-level variable allows tests to inject mock or real LLM behavior.
//
// TODO(actor-ownership): migrate to actor-owned state
var summarizeViaPlan = summarizeViaPlanImpl

// probeActualTokensFn is the real implementation, kept as an injectable var so
// tests can substitute deterministic provider-reported token counts without
// going through the aggregator. It must never fall back to an estimate — only
// real provider-reported tokens are authoritative for the budget bar and the
// compaction threshold.
//
// TODO(actor-ownership): migrate to actor-owned state
var probeActualTokensFn = func(a *Actor, ctx actor.Context, turnID string) (int64, error) {
	cfg := a.fetchAgentKindConfig(ctx)
	unit := a.status.Unit
	if unit.Model == "" {
		unit = slotFirstUnit(a.primary)
	}
	if unit.Model == "" {
		return 0, fmt.Errorf("probe: no model unit configured")
	}
	aggRef, _, err := a.resolveTarget(ctx, a.primary)
	if err != nil {
		return 0, fmt.Errorf("probe: aggregator not available: %w", err)
	}
	planner := ctx.Planner()
	if planner == nil {
		return 0, fmt.Errorf("probe: planner unavailable")
	}

	// Build system prompt + system blocks (same logic as buildDispatchRequest).
	inst := a.resolveInstructions(ctx)
	a.appendMemoryBase(inst)
	a.appendMemoryExperience(inst)
	hotContext := a.resolveFullHotContext(ctx)

	systemPrompt, systemBlocks := assembleSystemPrompt(inst, hotContext)

	tools := a.resolveTools(ctx, cfg, a.callablesMap())

	probeReq := domain.SendSessionMessageReq{
		SessionID:    turnID,
		Unit:         &unit,
		Title:        a.title,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
		Messages:     a.compileMessages(true),
		Tools:        tools,
	}

	probeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()

	node, err := planner.Plan(aggRef, "aiaggregator.probe_tokens", probeReq)
	if err != nil {
		return 0, fmt.Errorf("probe: plan: %w", err)
	}
	if err := node.Start(probeCtx); err != nil {
		return 0, fmt.Errorf("probe: start: %w", err)
	}
	defer func() {
		_ = node.Stop()
		_ = ctx.Destroy(node.Ref())
	}()

	type result struct {
		tokens int64
		err    error
	}
	ch := make(chan result, 1)
	panicprobe.SafeGo(ctx, "probe_tokens_recv", func() {
		v, err := node.Recv()
		if err != nil {
			ch <- result{err: err}
			return
		}
		switch v := v.(type) {
		case domain.ProbeTokensResp:
			// Real occupancy = non-cache input + cache read + cache creation,
			// all reported by the provider. Using InputTokens alone would
			// under-report as cache hit rate rises.
			occupied := v.Usage.InputTokens + v.Usage.CacheReadInputTokens + v.Usage.CacheCreationInputTokens
			if occupied > 0 {
				ch <- result{tokens: occupied}
				return
			}
			ch <- result{err: fmt.Errorf("probe: zero input tokens returned")}
		default:
			ch <- result{err: fmt.Errorf("probe: unexpected response type %T", v)}
		}
	})

	select {
	case r := <-ch:
		return r.tokens, r.err
	case <-probeCtx.Done():
		return 0, fmt.Errorf("probe: timeout: %w", probeCtx.Err())
	}
}

// --- Turn engine dispatch ---

// errPaused is returned by runDispatch when the user requests a pause while the
// LLM stream is in progress. phaseDispatch checks for this sentinel and sets
// loopState to LoopDispatch (not LoopFailed) so the outer loop's pause check
// fires immediately without treating the abort as a dispatch failure.
var errPaused = errors.New("turnEngine: paused by user")

// dispatchRetryBackoffs 是可重试错误的退避序列：5s → 10s → 20s。
// 短退避让用户在 provider 抖动时更快看到恢复。默认只执行 1 次
// dispatch 重试；序列仍保留更高次数供测试或调用方显式配置。
//
// TODO(actor-ownership): migrate to actor-owned state
var dispatchRetryBackoffs = []time.Duration{
	5 * time.Second,
	7 * time.Second,
	10 * time.Second,
	13 * time.Second,
	16 * time.Second,
	17 * time.Second,
	20 * time.Second,
}

// --- Tool interception tables ---

// fileMutatingCallables lists tools that may need confirm=true after approval
// because their target path can fall outside configured roots.
var fileMutatingCallables = map[string]bool{
	"filesystem.write":   true,
	"filesystem.edit":    true,
	"filesystem.rm":      true,
	"project.write":      true,
	"project.edit":       true,
	"project.rm":         true,
	"project.shell_exec": true,
}

// shellFilesystemIntercepts maps common shell commands to project file callables
// and the payload builder that converts a shell command string into the
// project request type. Used by both shell.bash and shell.exec interception.
var shellFilesystemIntercepts = map[string]struct {
	callableID   string
	buildPayload func(string) ([]byte, error)
}{
	"rm":   {"project.rm", buildRmPayload},
	"grep": {"project.grep", buildGrepPayload},
	"ls":   {"project.list", buildListPayload},
	"find": {"project.glob", buildFindPayload},
}

// externalBinaryAllowlist contains binaries that project.shell_exec / shell.exec
// may delegate to shell.bash. Keep this list explicit and auditable.
var externalBinaryAllowlist = map[string]bool{
	"go": true, "python": true, "python3": true, "npm": true, "node": true,
	"git": true, "make": true, "pip": true, "pip3": true,
	"cargo": true, "rustc": true, "javac": true, "java": true,
	"dotnet": true, "cmake": true, "npx": true, "yarn": true,
	"pnpm": true, "tsc": true, "vite": true, "eslint": true,
	"prettier": true, "black": true, "flake8": true, "pytest": true,
	"g++": true, "gcc": true, "clang": true, "clang++": true,
	"docker": true, "docker-compose": true, "kubectl": true,
	"helm": true, "terraform": true, "ansible-playbook": true,
	"gradle": true, "mvn": true, "ant": true, "ruby": true,
	"bundle": true, "gem": true, "php": true, "composer": true,
	"gofmt": true, "golint": true, "staticcheck": true,
	"buf": true, "protoc": true,
}

// vfsBuiltins lists commands handled natively by shell.exec.
var vfsBuiltins = map[string]bool{
	"ls": true, "cat": true, "mkdir": true, "rm": true, "cp": true, "mv": true,
	"pwd": true, "echo": true, "grep": true, "find": true, "write": true,
}
