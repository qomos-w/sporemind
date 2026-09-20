package memory

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/persist"
)

type monocardManifest struct {
	Version   int               `json:"version"`
	TickCount int               `json:"tick_count"`
	Nodes     []monocardNodeRef `json:"nodes"`
}

type monocardNodeRef struct {
	ID    NodeID   `json:"id"`
	Layer NodeType `json:"layer"`
}

type monocardEdge struct {
	To   NodeID   `json:"to"`
	Type EdgeType `json:"type"`
}

func (g *MemoryGraph) CheckpointMonocards(store persist.Persist, previous []byte) ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	manifest := monocardManifest{Version: 1, TickCount: g.TickCount, Nodes: make([]monocardNodeRef, 0, len(g.nodes))}
	docs := make(map[string]string, len(g.nodes))
	for _, node := range g.nodes {
		ref := monocardNodeRef{ID: node.ID, Layer: node.Type}
		manifest.Nodes = append(manifest.Nodes, ref)
		edges := make([]monocardEdge, 0)
		for _, edge := range g.edges {
			if edge.From == node.ID {
				edges = append(edges, monocardEdge{To: edge.To, Type: edge.Type})
			}
		}
		edgeJSON, err := json.Marshal(edges)
		if err != nil {
			return nil, err
		}
		docs[monocardPath(ref)] = encodeMonocard(node, edgeJSON)
	}

	// Phase 1: write all new/replacement files while still holding RLock
	// so the graph snapshot stays consistent with the persisted state.
	for name, raw := range docs {
		if err := store.Save(name, persist.Markdown{Raw: raw}); err != nil {
			return nil, fmt.Errorf("memory checkpoint %s: %w", name, err)
		}
	}

	// Phase 2: garbage-collect files no longer referenced by the new
	// manifest — only after every referenced file is persisted, so a crash
	// mid-checkpoint never leaves a node fileless. Prefer a directory sweep
	// when the store supports listing: the previous-manifest diff cannot
	// reach orphans left behind by an unparseable, stale, or empty previous
	// manifest (e.g. the first checkpoint after a restart with a fresh graph).
	if lister, ok := store.(persist.Lister); ok {
		names, err := lister.List("")
		if err != nil {
			return nil, fmt.Errorf("memory checkpoint sweep: %w", err)
		}
		for _, name := range names {
			if _, exists := docs[name]; !exists {
				if err := store.Delete(name); err != nil {
					return nil, fmt.Errorf("memory checkpoint delete %s: %w", name, err)
				}
			}
		}
	} else {
		var old monocardManifest
		_ = json.Unmarshal(previous, &old)
		for _, ref := range old.Nodes {
			name := monocardPath(ref)
			if _, exists := docs[name]; !exists {
				if err := store.Delete(name); err != nil {
					return nil, fmt.Errorf("memory checkpoint delete %s: %w", name, err)
				}
			}
		}
	}
	return json.Marshal(manifest)
}

func DeleteMonocards(store persist.Persist, data []byte) error {
	var manifest monocardManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != 1 {
		return nil
	}
	for _, ref := range manifest.Nodes {
		if err := store.Delete(monocardPath(ref)); err != nil {
			return err
		}
	}
	// Unmount tears the memory store down entirely; sweep any strays a
	// previously missed orphan cleanup left behind so unmount/remount
	// cycles do not leak files. Unknown manifest versions are left alone.
	if lister, ok := store.(persist.Lister); ok {
		names, err := lister.List("")
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := store.Delete(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *MemoryGraph) RestoreMonocards(store persist.Persist, data []byte) error {
	var manifest monocardManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("memory monocard manifest: %w", err)
	}
	if manifest.Version != 1 {
		return fmt.Errorf("memory monocard manifest: unsupported version %d", manifest.Version)
	}
	nodes := make(map[NodeID]*Node, len(manifest.Nodes))
	edges := make([]Edge, 0)
	for _, ref := range manifest.Nodes {
		var doc persist.Markdown
		if err := store.Load(monocardPath(ref), &doc); err != nil {
			// Skip individual unreadable files instead of aborting the
			// entire restore — partial recovery is better than none.
			continue
		}
		node, outgoing, err := decodeMonocard(doc.Raw)
		if err != nil {
			// Skip files with undecodable content.
			continue
		}
		if node.ID != ref.ID || node.Type != ref.Layer {
			// Manifest metadata does not match the persisted file — skip
			// rather than abort so remaining nodes can still restore.
			continue
		}
		nodes[node.ID] = node
		for _, edge := range outgoing {
			edges = append(edges, Edge{From: node.ID, To: edge.To, Type: edge.Type})
		}
	}
	g.mu.Lock()
	g.nodes = nodes
	g.edges = edges
	g.TickCount = manifest.TickCount
	g.mu.Unlock()
	return nil
}

func monocardPath(ref monocardNodeRef) string {
	return ref.Layer.String() + "/" + sanitizeFileName(string(ref.ID))
}

func sanitizeFileName(s string) string {
	return fileNameSanitizer.Replace(s)
}

func encodeMonocard(node *Node, edges []byte) string {
	id := strings.NewReplacer("\n", " ", "\r", " ").Replace(string(node.ID))
	head, _ := json.Marshal(node.Head)
	labelJSON, _ := json.Marshal(node.Label)
	return fmt.Sprintf("---\nid: %s\nlayer: %s\ntokens: %d\nenergy: %s\nbase_energy: %s\nmin_energy: %s\ncreated_at: %s\naccessed_at: %s\nlast_access_tick: %d\nmarked_for_death: %t\nprotected: %t\nhead_json: %s\nlabel_json: %s\nedges: %s\n---\n%s\n",
		id, node.Type, node.Tokens,
		strconv.FormatFloat(node.Energy, 'g', -1, 64),
		strconv.FormatFloat(node.BaseEnergy, 'g', -1, 64),
		strconv.FormatFloat(node.MinEnergy, 'g', -1, 64),
		node.CreatedAt.Format(time.RFC3339Nano),
		node.AccessedAt.Format(time.RFC3339Nano),
		node.LastAccessTick, node.MarkDeath, node.Protected,
		head, labelJSON, edges, node.Label)
}

func decodeMonocard(raw string) (*Node, []monocardEdge, error) {
	if !strings.HasPrefix(raw, "---\n") {
		return nil, nil, fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("unterminated frontmatter")
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(parts[0], "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok {
			fields[key] = value
		}
	}
	layer, err := parseNodeType(fields["layer"])
	if err != nil {
		return nil, nil, err
	}
	tokens, err := strconv.Atoi(fields["tokens"])
	if err != nil {
		return nil, nil, err
	}
	energy, err := strconv.ParseFloat(fields["energy"], 64)
	if err != nil {
		return nil, nil, err
	}
	created, err := time.Parse(time.RFC3339Nano, fields["created_at"])
	if err != nil {
		return nil, nil, err
	}
	accessed, err := time.Parse(time.RFC3339Nano, fields["accessed_at"])
	if err != nil {
		return nil, nil, err
	}
	marked, err := strconv.ParseBool(fields["marked_for_death"])
	if err != nil {
		return nil, nil, err
	}
	protected := false
	if v, ok := fields["protected"]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			protected = b
		}
	}
	lastAccessTick := 0
	if v, ok := fields["last_access_tick"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			lastAccessTick = n
		}
	}
	baseEnergy := 0.0
	if v, ok := fields["base_energy"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			baseEnergy = f
		}
	}
	minEnergy := 0.0
	if v, ok := fields["min_energy"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			minEnergy = f
		}
	}
	var edges []monocardEdge
	if err := json.Unmarshal([]byte(fields["edges"]), &edges); err != nil {
		return nil, nil, err
	}
	head := fields["head"]
	if encoded, ok := fields["head_json"]; ok {
		if err := json.Unmarshal([]byte(encoded), &head); err != nil {
			return nil, nil, err
		}
	}

	// Decode label: prefer JSON-escaped label_json (safe against \n---\n
	// corruption in the body); fall back to raw body for backward compat.
	label := strings.TrimSuffix(parts[1], "\n")
	if encoded, ok := fields["label_json"]; ok {
		var decoded string
		if err := json.Unmarshal([]byte(encoded), &decoded); err == nil {
			label = decoded
		}
	}

	return &Node{ID: NodeID(fields["id"]), Type: layer, Head: head, Label: label, Tokens: tokens, Energy: energy, BaseEnergy: baseEnergy, MinEnergy: minEnergy, CreatedAt: created, AccessedAt: accessed, LastAccessTick: lastAccessTick, MarkDeath: marked, Protected: protected}, edges, nil
}

func parseNodeType(value string) (NodeType, error) {
	switch value {
	case "session":
		return NodeTypeSession, nil
	case "experience":
		return NodeTypeExperience, nil
	case "ontology":
		return NodeTypeOntology, nil
	default:
		return 0, fmt.Errorf("unknown memory layer %q", value)
	}
}
