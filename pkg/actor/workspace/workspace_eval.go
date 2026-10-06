package workspace

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
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
