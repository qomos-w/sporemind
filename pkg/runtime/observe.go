package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// inspectPagesTimeout bounds the optional agent.inspect.pages query so that
// a missing or unresponsive agent callable cannot block inspect.document.
const inspectPagesTimeout = 2 * time.Second

// ---------------------------------------------------------------------------
// Enrichment
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Inspector
// ---------------------------------------------------------------------------

// readProjectionField returns a single string field from the actor's
// projection store. Used to read display name, title, etc. without blocking
// on a synchronous callable invoke.
func readProjectionField(a app.App, an ActorNode, field string) string {
	if a == nil {
		return ""
	}
	aid, err := parseActorID(an.ID)
	if err != nil {
		return ""
	}
	projStore := a.Projections()
	if vp, ok := projStore.GetField(aid, field); ok {
		if v, _ := vp.Fields["value"].(string); v != "" {
			return v
		}
	}
	return ""
}

// readAgentKind returns the agent role display name (e.g. "Architect", "Coder")
// for an agent node by matching its actor ID against the workspace's published
// agents projection. Returns "" for non-agent nodes or when unavailable.
func readAgentKind(a app.App, an ActorNode) string {
	if a == nil || an.Kind != "agent" {
		return ""
	}
	workspaceRef, ok := a.LookupService("workspace")
	if !ok {
		return ""
	}
	projStore := a.Projections()
	if projStore == nil {
		return ""
	}
	var agents []domain.AgentRef
	if vp, ok := projStore.GetField(workspaceRef.ID(), "agents"); ok {
		agents = decodeAgentRefs(vp.Fields["value"])
	}
	for _, ag := range agents {
		if ag.ActorID == an.ID {
			return domain.AgentKindDisplayName(ag.AgentKind)
		}
	}
	return ""
}

// inspectActorNode builds an InspectDocument for an actor node from the topology snapshot.
func inspectActorNode(ctx actor.PureContext, topo *topologyProvider, ref domain.InspectRef) domain.InspectDocument {
	actorID := ref.ID
	var node ActorNode
	var found bool
	for _, n := range topo.Snapshot() {
		if n.ID == actorID {
			node = n
			found = true
			break
		}
	}

	if !found {
		return domain.InspectDocument{
			Ref:      ref,
			Title:    actorID,
			Subtitle: "external node",
			Sections: []domain.InspectSection{},
		}
	}

	// Enrich agent label and title from the projection store.
	displayName := node.Label
	if node.Kind == "agent" {
		if dn := readProjectionField(topo.app, node, "displayName"); dn != "" {
			displayName = dn
		}
	} else {
		if projLabel := readProjectionLabel(topo.app, node); projLabel != "" {
			displayName = projLabel
		}
	}
	title := ""
	if node.Kind == "agent" {
		title = readProjectionField(topo.app, node, "title")
	}

	enrichedNode := node
	enrichedNode.Label = displayName

	card := BuildCard(enrichedNode)
	sections := cardToInspectSections(card)

	// Inject title and type rows after Name in the Basic Info section.
	agentType := ""
	if node.Kind == "agent" {
		agentType = readAgentKind(topo.app, node)
	}
	for i := range sections {
		if sections[i].ID == "basic" {
			newRows := make([]domain.InspectRow, 0, len(sections[i].Rows)+2)
			for _, r := range sections[i].Rows {
				newRows = append(newRows, r)
				if r.Label == "Name" {
					newRows = append(newRows, domain.InspectRow{Label: "Title", Value: title})
					if agentType != "" {
						newRows = append(newRows, domain.InspectRow{Label: "Type", Value: agentType})
					}
				}
			}
			sections[i].Rows = newRows
			break
		}
	}

	var pages []domain.InspectPage
	if found && node.ID != "" {
		// Only agent actors register inspect.pages; calling a non-existent
		// callable on other kinds causes the invoke to hang silently.
		if node.Kind == "agent" {
			targetAID, err := parseActorID(node.ID)
			if err == nil {
				if actorRef, ok := ctx.LookupID(targetAID); ok {
					invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), inspectPagesTimeout)
					defer cancel()
					callResult := actorRef.Invoke(invokeCtx, "inspect_pages", nil)
					if v, err := callResult.Final(invokeCtx); err == nil && v != nil {
						pages = decodeInspectPages(v)
					}
				}
			}
		}
		// Fallback: for turn nodes, query the parent agent's inspect.pages.
		if len(pages) == 0 && node.Kind == "turn" && node.ParentID != "" {
			parentAID, err := parseActorID(node.ParentID)
			if err == nil {
				if parentRef, ok := ctx.LookupID(parentAID); ok {
					invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), inspectPagesTimeout)
					defer cancel()
					callResult := parentRef.Invoke(invokeCtx, "inspect_pages", nil)
					if v, err := callResult.Final(invokeCtx); err == nil && v != nil {
						pages = decodeInspectPages(v)
					}
				}
			}
		}
	}

	var actions []domain.InspectAction
	if node.Kind == "agent" {
		actions = append(actions, domain.InspectAction{
			ID:    "edit-agent",
			Label: "Edit Agent",
			Target: &domain.OpenTarget{
				View:   "agent-manager",
				Params: map[string]string{"action": "edit", "actorId": node.ID},
			},
		})
	}

	return domain.InspectDocument{
		Ref:      ref,
		Title:    displayName,
		Subtitle: node.ID,
		Status:   &domain.InspectStatus{State: "ok", Label: "running"},
		Sections: sections,
		Actions:  actions,
		Pages:    pages,
	}
}

func cardToInspectSections(card *domain.TopologyCard) []domain.InspectSection {
	if card == nil || len(card.DetailSections) == 0 {
		return []domain.InspectSection{}
	}
	sections := make([]domain.InspectSection, 0, len(card.DetailSections))
	for _, s := range card.DetailSections {
		rows := make([]domain.InspectRow, 0, len(s.Rows))
		for _, r := range s.Rows {
			rows = append(rows, cardRowToInspectRow(r))
		}
		sections = append(sections, domain.InspectSection{
			Type:  "kv",
			ID:    s.Key,
			Title: s.Title,
			Rows:  rows,
		})
	}
	return sections
}

func cardRowToInspectRow(r domain.TopologyCardRow) domain.InspectRow {
	row := domain.InspectRow{
		Label:   r.Label,
		Value:   r.Value,
		Mono:    r.Mono,
		Badge:   r.Badge,
		Tooltip: r.Tooltip,
	}
	if len(r.ExpandedRows) > 0 {
		row.ExpandedRows = make([]domain.InspectRow, 0, len(r.ExpandedRows))
		for _, child := range r.ExpandedRows {
			row.ExpandedRows = append(row.ExpandedRows, cardRowToInspectRow(child))
		}
	}
	return row
}

// ---------------------------------------------------------------------------
// Decode helpers
// ---------------------------------------------------------------------------

func decodeInspectPages(v any) []domain.InspectPage {
	switch x := v.(type) {
	case domain.InspectPagesResp:
		return x.Items
	case []byte:
		if len(x) == 0 {
			return nil
		}
		var resp domain.InspectPagesResp
		if err := json.Unmarshal(x, &resp); err == nil {
			return resp.Items
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var resp domain.InspectPagesResp
		if err := json.Unmarshal(body, &resp); err == nil {
			return resp.Items
		}
	}
	return nil
}

// parseActorID converts a 32-character hex string to an id.ActorID.
func parseActorID(s string) (id.ActorID, error) {
	canonical, err := identity.ParseCanonicalID(s)
	if err != nil {
		return id.ActorID{}, err
	}
	return id.From(canonical), nil
}

// readProjectionLabel returns the DAG label for an actor by reading its
// gospore component from the projection store. Returns "" when the store
// has no label data for this actor (caller should fall back).
func readProjectionLabel(a app.App, an ActorNode) string {
	if a == nil {
		return ""
	}
	switch an.Kind {
	case "agent":
		return readProjectionField(a, an, "displayName")
	case "plan":
		aid, err := parseActorID(an.ID)
		if err != nil {
			return ""
		}
		projStore := a.Projections()
		snap, ok := projStore.Get(aid)
		if !ok {
			return ""
		}
		comp, ok := snap.Fields["comp"]
		if !ok {
			return ""
		}
		callID, _ := comp.Fields["callID"].(string)
		return callID
	}
	return ""
}
