package project

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// mcpExternalPrefix is the card ID prefix for MCP server bundle cards. It
// intentionally does not collide with the other external providers
// ("plugin:", "agent:", "builtin:", "ext-skill:").
const mcpExternalPrefix = "mcp:"

// McpExternalCardProvider surfaces every configured MCP server as a
// read-only bundle external card (card ID "mcp:<server-id>"). Cards are
// derived on demand from the mcpmanager server config snapshot (the safe
// mcp.list_servers view), so add/update/remove server operations are
// reflected immediately: a removed server vanishes from List and Get returns
// an error. MCP cards are per-agent mount markers only — they deliberately
// carry NO data.tools (mcp.<server>.<tool> callable IDs are not part of the
// component topology; tools are injected per turn via resolveMCPTools) and
// cannot be saved or deleted.
type McpExternalCardProvider struct {
	// listServers returns the live server views. Production default queries
	// the mcpmanager service; tests inject a stub. Injecting the query keeps
	// the provider free of actor-tree dependencies and makes List/Get fully
	// unit-testable.
	listServers func(ctx actor.PureContext) []domain.McpServerView
}

func newMcpExternalCardProvider() *McpExternalCardProvider {
	return &McpExternalCardProvider{listServers: queryMcpServers}
}

func (p *McpExternalCardProvider) Prefix() string { return mcpExternalPrefix }

func mcpCardID(serverID string) string {
	return mcpExternalPrefix + sanitizePathSegment(serverID)
}

// mcpServerIDFromCardID extracts the (sanitized) server id segment from a
// card id. Returns ok=false for ids with the wrong prefix or an empty or
// path-like segment.
func mcpServerIDFromCardID(id string) (string, bool) {
	if !strings.HasPrefix(id, mcpExternalPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(id, mcpExternalPrefix)
	if rest == "" || rest == "." || rest == ".." || strings.ContainsAny(rest, `/\\`) {
		return "", false
	}
	return rest, true
}

// queryMcpServers invokes the mcpmanager's safe list view. It returns nil
// when the service is unreachable or the call fails — MCP cards are an
// additive surface, never a hard dependency of the wiki.
func queryMcpServers(ctx actor.PureContext) []domain.McpServerView {
	mcpRef, ok := ctx.LookupService("mcp")
	if !ok || mcpRef == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := mcpRef.Invoke(callCtx, "mcp.list_servers", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil || v == nil {
		return nil
	}
	var resp domain.McpListServersResp
	switch r := v.(type) {
	case domain.McpListServersResp:
		resp = r
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	return resp.Items
}

func mcpServerDisplayName(s domain.McpServerView) string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

func mcpCardItem(s domain.McpServerView, now string) domain.MonoCardListItem {
	data := map[string]any{
		"componentKind": "bundle",
		"source":        "mcpmanager",
		"storage":       "external",
		"visibility":    "component",
		"protected":     true,
		"editable":      false,
		"deletable":     false,
		"title":         mcpServerDisplayName(s),
		"icon":          "plug",
	}
	if s.ID != "" {
		data["mcpServerId"] = s.ID
	}
	if s.Name != "" {
		data["mcpServerName"] = s.Name
	}
	if s.Transport != "" {
		data["mcpTransport"] = s.Transport
	}
	return domain.MonoCardListItem{
		ID: mcpCardID(s.ID), Type: "bundle", Source: "mcpmanager", Storage: "external", Visibility: "component",
		Tags: []string{"bundle", "component", "mcp"}, List: []string{}, Created: now, Modified: now,
		Protected: true, Editable: false, Deletable: false, Raw: "",
		Data: data,
	}
}

func (p *McpExternalCardProvider) List(ctx actor.PureContext) ([]domain.MonoCardListItem, error) {
	now := time.Now().Format(time.RFC3339)
	servers := p.listServers(ctx)
	items := make([]domain.MonoCardListItem, 0, len(servers))
	for _, s := range servers {
		items = append(items, mcpCardItem(s, now))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (p *McpExternalCardProvider) Get(ctx actor.PureContext, id string) (string, error) {
	segment, ok := mcpServerIDFromCardID(id)
	if !ok {
		return "", fmt.Errorf("mcp external card: invalid id %q", id)
	}
	for _, s := range p.listServers(ctx) {
		if sanitizePathSegment(s.ID) == segment {
			return renderMCPCard(s), nil
		}
	}
	return "", fmt.Errorf("mcp external card: server %q not found", segment)
}

// Save is always rejected: MCP cards are derived from mcpmanager config.
func (p *McpExternalCardProvider) Save(_ actor.PureContext, _ string, _ string) error {
	return fmt.Errorf("mcp external cards are read-only: derived from mcpmanager server config")
}

// Delete is always rejected: MCP cards are derived from mcpmanager config.
func (p *McpExternalCardProvider) Delete(_ actor.PureContext, _ string) error {
	return fmt.Errorf("mcp external cards are read-only: derived from mcpmanager server config")
}

// renderMCPCard renders the card raw markdown (frontmatter only — no body).
// The MCP card is a pure mount marker: its sole purpose is to be referenced by
// an agent's component mounts so resolveMCPTools injects the server's tools
// per turn. The card body is deliberately empty so the generic placeholder
// text is NOT projected into the system prompt as a "policy"-section prompt
// contribution (which would pollute the prompt at section order 1). The
// server's tools — with their descriptions — are already available to the LLM
// via resolveMCPTools → ToolSpecs. Values are double-quoted so names with
// YAML-special characters (colons, etc.) stay parseable.
func renderMCPCard(s domain.McpServerView) string {
	id := mcpCardID(s.ID)
	display := mcpServerDisplayName(s)
	return fmt.Sprintf(
		"---\n"+
			"id: %q\n"+
			"type: bundle\n"+
			"title: %q\n"+
			"tags: [bundle, component, mcp]\n"+
			"data:\n"+
			"  componentKind: bundle\n"+
			"  source: mcpmanager\n"+
			"  storage: external\n"+
			"  visibility: component\n"+
			"  protected: true\n"+
			"  editable: false\n"+
			"  deletable: false\n"+
			"  title: %q\n"+
			"  icon: plug\n"+
			"  mcpServerId: %q\n"+
			"  mcpServerName: %q\n"+
			"  mcpTransport: %q\n"+
			"---\n",
		id, display, display, s.ID, s.Name, s.Transport,
	)
}