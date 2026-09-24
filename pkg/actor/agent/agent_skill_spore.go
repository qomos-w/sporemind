package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

// A runtime:spore skill replaces the prompt-fragment body contract with an
// executable one: the body's first ```spore fence is compiled and run on
// agent_skill_use, and the deterministic return value is handed back to the
// LLM as the tool result. This file holds that execution path.
//
// The skill runner is deliberately narrower than the workflow script
// executor (pkg/actor/workspace/executor_script.go): no host.invoke is bound
// and no std is registered, so a skill script is pure computation over its
// input by construction — zero side-effect surface, no capability gate
// needed. Scripts that import "host" fail at LoadSource because the host
// namespace is never bound.

// sporeSkillBudget is the execution envelope for one spore skill run, parsed
// from the skill card's frontmatter. Zero fields fall back to skill defaults
// (tighter than task-card defaults: a skill runs inline in an agent turn).
type sporeSkillBudget struct {
	MaxDurationSec  int
	MaxInstructions int
	MaxOutputBytes  int
}

const (
	// defaultSkillSporeDurationSec bounds one skill run. Task cards default
	// to 60s because a workflow can wait; a skill blocks the agent_exec
	// lane of a live turn, so the default is tighter.
	defaultSkillSporeDurationSec = 10
	// defaultSkillSporeOutputBytes caps the encoded result so a runaway
	// script cannot flood the LLM context. Task cards default to
	// unlimited; skills have a live consumer on the other end.
	defaultSkillSporeOutputBytes = 64 * 1024
)

// skillSporeModule is the module name spore skill sources load under. Fresh
// Runtime per run, so the name is cosmetic — kept distinct from the
// executor's script_<cardID> names for clearer error messages.
const skillSporeModule = "skill_spore"

// parseSporeSkillArgs decodes the skill_use Args string into the single value
// handed to the script's run(input) parameter. Empty args mean "no input"
// (nil); anything else must be valid JSON — a friendly error beats a silent
// string-coercion mismatch.
func parseSporeSkillArgs(args string) (any, error) {
	if args == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("spore skill args must be a JSON value (the script receives it as run(input)): %v", err)
	}
	return normalizeSporeSkillJSON(value), nil
}

// normalizeSporeSkillJSON converts json.Number leaves to int64 (when integral)
// or float64. Go's default json.Decode types every number float64, which would
// make spore `is int` false for every JSON integer; UseNumber + this walk
// restores the author's obvious intent.
func normalizeSporeSkillJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeSporeSkillJSON(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeSporeSkillJSON(e)
		}
		return t
	default:
		return v
	}
}

// buildSporeSkillCallContext assembles the spore VM budget envelope, applying
// skill defaults for unset fields and clamping the duration into the shared
// scriptcard bounds so frontmatter typos cannot request absurd budgets.
func buildSporeSkillCallContext(lifecycle context.Context, budget sporeSkillBudget) script.CallContext {
	dur := budget.MaxDurationSec
	if dur <= 0 {
		dur = defaultSkillSporeDurationSec
	}
	if dur < scriptcard.MinMaxDurationSec {
		dur = scriptcard.MinMaxDurationSec
	}
	if dur > scriptcard.MaxMaxDurationSec {
		dur = scriptcard.MaxMaxDurationSec
	}
	out := budget.MaxOutputBytes
	if out <= 0 {
		out = defaultSkillSporeOutputBytes
	}
	if lifecycle == nil {
		lifecycle = context.Background()
	}
	return script.CallContext{
		Context: lifecycle,
		Budget: script.ExecutionBudget{
			MaxInstructions: uint64(budget.MaxInstructions),
			MaxDuration:     time.Duration(dur) * time.Second,
			MaxOutputBytes:  uint64(out),
		},
	}
}

// marshalSporeSkillResult normalizes the script return value through a JSON
// round-trip and returns the encoded string placed into AgentSkillUseResp.
// Result. A void return encodes as "null".
func marshalSporeSkillResult(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("spore skill result not encodable: %v", err)
	}
	return string(encoded), nil
}

// runSporeSkillSource compiles src and runs its exported run(input) under the
// budget envelope, returning the JSON-encoded result. It is the seam between
// the skill card body and the spore VM; all skill-use error messages carry
// the skillSporeErrorPrefix so the LLM can tell runner failures from script
// logic errors.
const skillSporeErrorPrefix = "agent.skill.spore:"

// runSporeSkillBody extracts the spore source from a skill body and executes
// it with argsJSON as the single input. lifecycle propagates actor shutdown
// into the VM run; nil falls back to Background.
func runSporeSkillBody(lifecycle context.Context, body, argsJSON string, budget sporeSkillBudget) (result string, err error) {
	src, ok := scriptcard.ExtractSporeBlock(body)
	if !ok {
		return "", fmt.Errorf("%s skill body has no ```spore fenced block", skillSporeErrorPrefix)
	}
	input, err := parseSporeSkillArgs(argsJSON)
	if err != nil {
		return "", fmt.Errorf("%s %v", skillSporeErrorPrefix, err)
	}
	defer func() {
		// The spore VM is a third-party execution engine; a VM panic must
		// surface as a skill error, not take down the agent lane.
		if r := recover(); r != nil {
			result, err = "", fmt.Errorf("%s script panicked: %v", skillSporeErrorPrefix, r)
		}
	}()
	return runSporeSkillSource(lifecycle, src, input, budget)
}

func runSporeSkillSource(lifecycle context.Context, src string, input any, budget sporeSkillBudget) (string, error) {
	rt, err := script.NewRuntime()
	if err != nil {
		return "", fmt.Errorf("%s build runtime: %v", skillSporeErrorPrefix, err)
	}
	defer func() { _ = rt.Close() }()
	if err := rt.LoadSource(skillSporeModule, src); err != nil {
		return "", fmt.Errorf("%s script compile failed: %v", skillSporeErrorPrefix, err)
	}
	callCtx := buildSporeSkillCallContext(lifecycle, budget)
	if input != nil {
		result, err := rt.CallContext(callCtx, "run", input)
		return finishSporeSkillCall(result, err)
	}
	// No args were supplied. The spore VM rejects a nil argument for a
	// non-optional parameter, so consult the exported signature: a script
	// that declares run() gets a plain call, one that declares run(input)
	// gets a clear usage error instead of a cryptic VM message.
	info, err := rt.LookupCallable(skillSporeModule, "run")
	if err != nil {
		return "", fmt.Errorf("%s %v", skillSporeErrorPrefix, err)
	}
	if len(info.Parameters) == 0 {
		result, err := rt.CallContext(callCtx, "run")
		return finishSporeSkillCall(result, err)
	}
	return "", fmt.Errorf("%s skill declares run(input) but no args were supplied; pass a JSON value as Args", skillSporeErrorPrefix)
}

func finishSporeSkillCall(result script.Result, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("%s script execution failed: %v", skillSporeErrorPrefix, err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("%s script runtime error: %v", skillSporeErrorPrefix, result.Error)
	}
	encoded, err := marshalSporeSkillResult(result.Value)
	if err != nil {
		return "", fmt.Errorf("%s %v", skillSporeErrorPrefix, err)
	}
	return encoded, nil
}
