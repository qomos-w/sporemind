package workspace

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// Embedded copies of the upstream spore language syntax reference
// (../spore/SYNTAX.md and SYNTAX.zh-CN.md). Drift is gated by
// `make check-spore-syntax`.
//
//go:embed sporedocs/SYNTAX.md
var sporeSyntaxEN string

//go:embed sporedocs/SYNTAX.zh-CN.md
var sporeSyntaxZH string

// Fixed eval budget. The eval surface is pure computation (no host
// bindings), so these constants are the whole constraint envelope —
// explicit here rather than caller-tunable.
const (
	evalMaxDurationSec  = 10
	evalMaxInstructions = 1_000_000
	evalMaxOutputBytes  = 64 * 1024
)

// handleEval backs workspace.eval: it evaluates an ad-hoc spore script
// snippet and returns the normalized result. The script must define the
// run() entry function (same contract as script cards); Args are passed
// positionally. The VM gets NO host bindings — host reach stays on
// workspace.host_call, which routes through the target's own policy —
// so eval is a pure data-in/data-out surface. A fresh Runtime is built
// per call (the spore VM is not thread-safe) and closed on exit.
// Compile / runtime failures return resp.Error so the caller sees the
// diagnostics instead of a transport error. Stateless (PureContext).
func (a *Actor) handleEval(ctx actor.PureContext, req domain.WorkspaceEvalReq) (domain.WorkspaceEvalResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.WorkspaceEvalResp{}, err
	}
	if strings.TrimSpace(req.Script) == "" {
		return domain.WorkspaceEvalResp{}, fmt.Errorf("workspace.eval: Script is required")
	}

	rt, err := script.NewRuntime()
	if err != nil {
		return domain.WorkspaceEvalResp{}, fmt.Errorf("workspace.eval: build runtime: %w", err)
	}
	defer func() { _ = rt.Close() }()

	if err := rt.LoadSource("eval", req.Script); err != nil {
		return domain.WorkspaceEvalResp{Error: fmt.Sprintf("script compile failed: %v", err)}, nil
	}

	// MaxOutputBytes is not engine-enforced; it is checked host-side
	// after normalization below. MaxHostCalls stays 0 (limit disabled)
	// because no host functions are bound — there is nothing to cap.
	callCtx := buildCallContext(ctx, evalMaxInstructions, evalMaxDurationSec, 0, 0)
	result, err := rt.CallContext(callCtx, "run", req.Args...)
	if err != nil {
		return domain.WorkspaceEvalResp{Error: fmt.Sprintf("script execution failed: %v", err)}, nil
	}
	if result.Error != nil {
		return domain.WorkspaceEvalResp{Error: fmt.Sprintf("script runtime error: %v", result.Error)}, nil
	}

	normalized, nerr := normalizeScriptResult(result.Value)
	if nerr != nil {
		return domain.WorkspaceEvalResp{Error: fmt.Sprintf("script result normalization failed: %v", nerr)}, nil
	}
	if encoded, merr := json.Marshal(normalized); merr == nil && len(encoded) > evalMaxOutputBytes {
		return domain.WorkspaceEvalResp{Error: fmt.Sprintf("script output exceeds %d bytes (got %d)", evalMaxOutputBytes, len(encoded))}, nil
	}
	return domain.WorkspaceEvalResp{Result: normalized}, nil
}

// handleSporeSyntax backs workspace.spore_syntax: it returns the spore
// language syntax reference (the embedded upstream SYNTAX.md). Lang
// "en" (default) or "zh". Stateless (PureContext).
func (a *Actor) handleSporeSyntax(_ actor.PureContext, req domain.WorkspaceSporeSyntaxReq) (domain.WorkspaceSporeSyntaxResp, error) {
	switch lang := strings.ToLower(strings.TrimSpace(req.Lang)); lang {
	case "", "en":
		return domain.WorkspaceSporeSyntaxResp{Lang: "en", Markdown: sporeSyntaxEN}, nil
	case "zh", "zh-cn":
		return domain.WorkspaceSporeSyntaxResp{Lang: "zh", Markdown: sporeSyntaxZH}, nil
	default:
		return domain.WorkspaceSporeSyntaxResp{}, fmt.Errorf("workspace.spore_syntax: unsupported Lang %q (use \"en\" or \"zh\")", req.Lang)
	}
}

const (
	searchCallablesDefaultLimit = 200
	searchCallablesMaxLimit     = 500
)

// handleSearchCallables backs workspace.search_callables: it flattens the
// live topology snapshot's CallableInterface rows and filters them by a
// case-insensitive substring on name or description (mirroring the agent
// actor's list_callables). Rows carry request params and service names, so
// callers can discover a callable's ID and request shape before invoking it
// via workspace.host_call. Total counts matches before the limit applies.
// Stateless (PureContext).
func (a *Actor) handleSearchCallables(_ actor.PureContext, req domain.WorkspaceSearchCallablesReq) (domain.WorkspaceSearchCallablesResp, error) {
	if a.topo == nil {
		return domain.WorkspaceSearchCallablesResp{}, fmt.Errorf("workspace.search_callables: topology provider unavailable")
	}
	query := strings.ToLower(req.Query)
	limit := req.Limit
	if limit <= 0 {
		limit = searchCallablesDefaultLimit
	}
	if limit > searchCallablesMaxLimit {
		limit = searchCallablesMaxLimit
	}

	items := make([]domain.CallableInterface, 0, limit)
	total := 0
	for _, node := range a.topo.Snapshot() {
		for _, ci := range node.Callables {
			if query != "" &&
				!strings.Contains(strings.ToLower(ci.Name), query) &&
				!strings.Contains(strings.ToLower(ci.Description), query) {
				continue
			}
			total++
			if len(items) < int(limit) {
				items = append(items, ci)
			}
		}
	}
	return domain.WorkspaceSearchCallablesResp{Items: items, Total: int32(total)}, nil
}
