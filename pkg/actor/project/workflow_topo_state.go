package project

import (
	"encoding/json"
	"fmt"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// wfTopoCardData is the subset of the workflow topo graph that is NOT
// derivable from the card hierarchy: data-flow bindings and map-level inputs.
// These are stored in the map card's Data["workflowTopo"] as a JSON string.
// The revision counter (Rev) is used for optimistic-lock CAS: it is bumped on
// every saveWorkflowTopo call.
//
// Nodes and edges are always recomputed from the map card's scope.include and
// each task card's frontmatter data.depends_on — the graph is a pure view.
type wfTopoCardData struct {
	Bindings []WorkflowTopoBinding `json:"bindings,omitempty"`
	Inputs   map[string]any        `json:"inputs,omitempty"`
	Rev      int64                 `json:"rev"`
}

// readWfTopoCardData reads the workflow topo card data from the map card's
// Data["workflowTopo"]. When the field is absent or unparseable, the zero
// value (empty bindings, nil inputs, rev=0) is returned.
func readWfTopoCardData(mapCard *CardRecord) wfTopoCardData {
	var d wfTopoCardData
	raw, ok := mapCard.Data["workflowTopo"]
	if !ok {
		return d
	}
	s, ok := raw.(string)
	if !ok {
		return d
	}
	_ = json.Unmarshal([]byte(s), &d)
	return d
}

// writeWfTopoCardData writes the workflow topo card data into the map card's
// raw YAML frontmatter as a Data["workflowTopo"] entry. The raw is updated
// in-place and returned.
func writeWfTopoCardData(raw string, d wfTopoCardData) (string, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("marshal wfTopoCardData: %w", err)
	}
	raw = setCardDataFieldInRaw(raw, "workflowTopo", string(data))
	return raw, nil
}

// wfTopoRevision returns the revision string for the workflow topo graph of
// mapID. The revision is "rev-N" where N is the Rev counter from the map
// card's wfTopoCardData. Returns "" when the map card has no rev counter yet
// (first save pending).
func (a *Actor) wfTopoRevision(mapID string) string {
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		return ""
	}
	d := readWfTopoCardData(mapCard)
	if d.Rev == 0 {
		return ""
	}
	return fmt.Sprintf("rev-%d", d.Rev)
}

// wfTopoRevisionFromCard returns the revision from a map card that was
// already read. Useful when the caller already holds the card.
func wfTopoRevisionFromCard(mapCard *CardRecord) string {
	d := readWfTopoCardData(mapCard)
	if d.Rev == 0 {
		return ""
	}
	return fmt.Sprintf("rev-%d", d.Rev)
}

// initWfTopoCardData initializes the workflow topo card data on mapID to
// rev=1 (empty bindings/inputs). No-op if rev > 0 already. Returns the
// revision string.
func (a *Actor) initWfTopoCardData(mapID string) string {
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		return ""
	}
	d := readWfTopoCardData(mapCard)
	if d.Rev > 0 {
		return wfTopoRevisionFromCard(mapCard)
	}
	d.Rev = 1
	raw, err := writeWfTopoCardData(mapCard.Raw, d)
	if err != nil {
		return ""
	}
	_ = a.store.Save(&CardRecord{Title: mapID, Raw: raw})
	return fmt.Sprintf("rev-%d", d.Rev)
}

// parseWfTopoRev parses "rev-N" into the int64 N. Returns 0 on parse failure.
func parseWfTopoRev(rev string) int64 {
	if rev == "" {
		return 0
	}
	var n int64
	if _, err := fmt.Sscanf(rev, "rev-%d", &n); err != nil {
		return 0
	}
	return n
}

// buildWfTopoEnvelope wraps the computed WorkflowTopoGraph and revision into
// a gen.ProjectGraphEnvelopeResp suitable for the graph_get callable.
func (a *Actor) buildWfTopoEnvelope(mapID string, g WorkflowTopoGraph, rev string) (gen.ProjectGraphEnvelopeResp, error) {
	data, err := json.Marshal(g)
	if err != nil {
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("marshal workflow_topo graph: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return gen.ProjectGraphEnvelopeResp{
		Meta: gen.GraphSnapshotMeta{
			GraphKind:     GraphKindWorkflowTopo,
			ID:            mapID,
			Revision:      rev,
			CreatedAt:     now,
			UpdatedAt:     now,
			SchemaVersion: WorkflowTopoSchemaVersion,
		},
		EnvelopeText: string(data),
	}, nil
}