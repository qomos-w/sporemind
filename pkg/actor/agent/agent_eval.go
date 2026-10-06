package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/sporebridge"
)

// evalSporeModule is the module name the ad-hoc eval source compiles under.
const evalSporeModule = "eval"

// evalSporeBudget is the fixed eval envelope — explicit here, not
// caller-tunable. Host reach (host.invoke) is bounded like every other
// budget axis: one eval may make at most 64 host calls.
var evalSporeBudget = sporeSkillBudget{
	MaxDurationSec:  10,
	MaxInstructions: 1_000_000,
	MaxOutputBytes:  64 * 1024,
	MaxHostCalls:    64,
}

// evalHostSeams adapts the agent actor's PureContext into the minimal
// sporebridge.ServiceHost surface (mirrors workspace.hostCallSeams).
type evalHostSeams struct {
	lookupService func(string) (ref.Ref, bool)
	self          ref.Ref
}

func (s evalHostSeams) LookupService(name string) (ref.Ref, bool) { return s.lookupService(name) }
func (s evalHostSeams) Self() ref.Ref                             { return s.self }

// handleEval backs the agent-local eval callable: it evaluates an ad-hoc
// spore script and returns the normalized result. The script must define the
// run() entry function (same contract as script cards and runtime:spore
// skills); Args are passed positionally and normalized through the skill
// path's integral-number promotion so spore `is int` holds on JSON-decoded
// arguments.
//
// Host reach lives INSIDE the script: the runtime binds host.invoke via
// sporebridge with the agent's own identity — every relayed call propagates
// the caller role and passes the target callable's own policy ladder, the
// exact semantics the removed relay tools (workspace.host_call /
// mcp.call_tool / appmanager.invoke) carried. "Reach, not permission": a
// denial means the target's policy said no. Per-call wall clock is capped at
// domain.DefaultInvokeTimeout by the bridge; the whole script by
// evalSporeBudget (MaxHostCalls included).
//
// Compile / runtime failures return resp.Error so the caller sees
// diagnostics instead of a transport error. Stateless (PureContext): the run
// executes on the forked goroutine, off every lane, so a seconds-long script
// cannot block the owner lane.
func (a *Actor) handleEval(ctx actor.PureContext, req domain.AgentEvalReq) (domain.AgentEvalResp, error) {
	if strings.TrimSpace(req.Script) == "" {
		return domain.AgentEvalResp{}, fmt.Errorf("eval: Script is required")
	}
	args := make([]any, len(req.Args))
	for i, v := range req.Args {
		args[i] = normalizeSporeSkillJSON(v)
	}

	bindHost := func(rt *script.Runtime) error {
		bridge := sporebridge.NewHost(evalHostSeams{
			lookupService: ctx.LookupService,
			self:          ctx.Self(),
		}).WithRole(string(ctx.Identity().Role))
		return bridge.BindTo(rt)
	}

	value, err := evalSporeSource(ctx.Lifecycle(), req.Script, args, bindHost)
	if err != nil {
		return domain.AgentEvalResp{Error: err.Error()}, nil
	}
	normalized := normalizeSporeSkillJSON(value)
	// MaxOutputBytes is not engine-enforced for plain returns; check it
	// host-side after normalization, mirroring the former workspace handler.
	if encoded, merr := json.Marshal(normalized); merr == nil && len(encoded) > evalSporeBudget.MaxOutputBytes {
		return domain.AgentEvalResp{Error: fmt.Sprintf("script output exceeds %d bytes (got %d)", evalSporeBudget.MaxOutputBytes, len(encoded))}, nil
	}
	return domain.AgentEvalResp{Result: normalized}, nil
}

// evalSporeSource compiles src and runs its exported run(...) under the fixed
// eval budget, returning the raw VM value. bindHost (optional) registers
// host bindings on the fresh runtime before compilation — eval passes the
// sporebridge host.invoke, the skill path passes nil. A fresh Runtime is
// built per call (the spore VM is not thread-safe) and closed on exit; a VM
// panic surfaces as an eval diagnostic, never a goroutine crash.
func evalSporeSource(lifecycle context.Context, src string, args []any, bindHost func(*script.Runtime) error) (value any, err error) {
	rt, err := script.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("build runtime: %v", err)
	}
	defer func() { _ = rt.Close() }()
	defer func() {
		if r := recover(); r != nil {
			value, err = nil, fmt.Errorf("script panicked: %v", r)
		}
	}()
	if bindHost != nil {
		if err := bindHost(rt); err != nil {
			return nil, fmt.Errorf("bind host functions: %v", err)
		}
	}
	if err := rt.LoadSource(evalSporeModule, src); err != nil {
		return nil, fmt.Errorf("script compile failed: %v", err)
	}
	callCtx := buildSporeSkillCallContext(lifecycle, evalSporeBudget)
	result, err := rt.CallContext(callCtx, "run", args...)
	if err != nil {
		return nil, fmt.Errorf("script execution failed: %v", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("script runtime error: %v", result.Error)
	}
	return result.Value, nil
}

// Embedded copies of the upstream spore language syntax reference
// (../spore/SYNTAX.md and SYNTAX.zh-CN.md). Drift is gated by
// `make check-spore-syntax`.
//
//go:embed sporedocs/SYNTAX.md
var evalSyntaxEN string

//go:embed sporedocs/SYNTAX.zh-CN.md
var evalSyntaxZH string

// handleEvalSyntax backs the agent-local eval_syntax: it returns the spore
// language syntax reference (the embedded upstream SYNTAX.md). Lang "en"
// (default) or "zh". Stateless (PureContext).
func (a *Actor) handleEvalSyntax(_ actor.PureContext, req domain.AgentEvalSyntaxReq) (domain.AgentEvalSyntaxResp, error) {
	switch lang := strings.ToLower(strings.TrimSpace(req.Lang)); lang {
	case "", "en":
		return domain.AgentEvalSyntaxResp{Lang: "en", Markdown: evalSyntaxEN}, nil
	case "zh", "zh-cn":
		return domain.AgentEvalSyntaxResp{Lang: "zh", Markdown: evalSyntaxZH}, nil
	default:
		return domain.AgentEvalSyntaxResp{}, fmt.Errorf("eval_syntax: unsupported Lang %q (use \"en\" or \"zh\")", req.Lang)
	}
}

const (
	evalCallablesDefaultLimit = 200
	evalCallablesMaxLimit     = 500
)

// handleEvalCallables backs the agent-local eval_callables: it flattens the
// agent's topology snapshot of the host callable catalog and filters it by a
// case-insensitive substring on name or description. Rows carry request
// params and service names, so scripts can discover a callable's ID and
// request shape before host.invoke.
//
// The snapshot is per-ACTOR and carries each actor-kind's full callable
// table, so one callable appears once per actor of its kind (e.g. every
// loaded project actor contributes project.read again); callablesMap dedups
// by Name. Name matches rank before description matches — searching
// "project.read" must surface the callable itself, not fifty other rows
// whose documentation merely mentions it. Total counts distinct matched
// rows before the limit applies. Discovery only — it never invokes
// anything. Stateless (PureContext).
func (a *Actor) handleEvalCallables(_ actor.PureContext, req domain.AgentEvalCallablesReq) (domain.AgentEvalCallablesResp, error) {
	if a.topo == nil {
		return domain.AgentEvalCallablesResp{}, fmt.Errorf("eval_callables: topology provider unavailable")
	}
	query := strings.ToLower(req.Query)
	limit := req.Limit
	if limit <= 0 {
		limit = evalCallablesDefaultLimit
	}
	if limit > evalCallablesMaxLimit {
		limit = evalCallablesMaxLimit
	}

	byName := a.callablesMap()
	nameMatches := make([]domain.CallableInterface, 0, len(byName))
	descMatches := make([]domain.CallableInterface, 0)
	for _, ci := range byName {
		if query != "" {
			switch {
			case strings.Contains(strings.ToLower(ci.Name), query):
			case strings.Contains(strings.ToLower(ci.Description), query):
				descMatches = append(descMatches, ci)
				continue
			default:
				continue
			}
		}
		nameMatches = append(nameMatches, ci)
	}
	sortByName := func(rows []domain.CallableInterface) {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	}
	sortByName(nameMatches)
	sortByName(descMatches)

	total := len(nameMatches) + len(descMatches)
	if len(nameMatches) > int(limit) {
		nameMatches = nameMatches[:limit]
	}
	items := make([]domain.CallableInterface, 0, int(limit))
	items = append(items, nameMatches...)
	if len(items) < int(limit) {
		items = append(items, descMatches[:min(len(descMatches), int(limit)-len(items))]...)
	}
	return domain.AgentEvalCallablesResp{Items: items, Total: int32(total)}, nil
}
