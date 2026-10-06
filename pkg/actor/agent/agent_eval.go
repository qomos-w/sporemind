package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// evalSporeModule is the module name the ad-hoc eval source compiles under.
const evalSporeModule = "eval"

// evalSporeBudget is the fixed eval envelope — explicit here, not
// caller-tunable. It mirrors the former workspace.eval surface so the
// pure-computation contract survives the move onto the agent unchanged.
var evalSporeBudget = sporeSkillBudget{
	MaxDurationSec:  10,
	MaxInstructions: 1_000_000,
	MaxOutputBytes:  64 * 1024,
}

// handleEval backs the agent-local eval callable (moved from workspace.eval):
// it evaluates an ad-hoc spore script and returns the normalized result. The
// script must define the run() entry function (same contract as script cards
// and runtime:spore skills); Args are passed positionally and normalized
// through the skill path's integral-number promotion so spore `is int` holds
// on JSON-decoded arguments. The VM gets NO host bindings — host reach stays
// on the sporeeval relay tools, which route through the target's own policy —
// so eval is a pure data-in/data-out surface. Compile / runtime failures
// return resp.Error so the caller sees diagnostics instead of a transport
// error. Stateless (PureContext): the run executes on the forked goroutine,
// off every lane, so a seconds-long script cannot block the owner lane.
func (a *Actor) handleEval(ctx actor.PureContext, req domain.AgentEvalReq) (domain.AgentEvalResp, error) {
	if strings.TrimSpace(req.Script) == "" {
		return domain.AgentEvalResp{}, fmt.Errorf("eval: Script is required")
	}
	args := make([]any, len(req.Args))
	for i, v := range req.Args {
		args[i] = normalizeSporeSkillJSON(v)
	}

	value, err := evalSporeSource(ctx.Lifecycle(), req.Script, args)
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
// eval budget, returning the raw VM value. A fresh Runtime is built per call
// (the spore VM is not thread-safe) and closed on exit; a VM panic surfaces
// as an eval diagnostic, never a goroutine crash.
func evalSporeSource(lifecycle context.Context, src string, args []any) (value any, err error) {
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
