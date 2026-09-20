package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"log/slog"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// memorySaveMaxRunes caps the length of a single memory_save content.
const memorySaveMaxRunes = 500

// ── Mount state ──────────────────────────────────────────────────────────

// nodeHead returns a short head for a memory node.
// Prefers n.Head when non-empty, falls back to extracting from n.Label.
func nodeHead(n *memory.Node) string {
	if n.Head != "" {
		return n.Head
	}
	if n.Label == "" {
		return ""
	}
	// Use first line.
	if idx := strings.IndexByte(n.Label, '\n'); idx >= 0 {
		return n.Label[:idx]
	}
	// Truncate to 60 chars.
	runes := []rune(n.Label)
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	return n.Label
}

// nodesByType returns all non-marked nodes of the given type.
func nodesByType(g *memory.MemoryGraph, typ memory.NodeType) []*memory.Node {
	all := g.Nodes()
	if len(all) == 0 {
		return nil
	}
	out := make([]*memory.Node, 0, len(all))
	for _, n := range all {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// memoryOntologyBlock returns the ontology full-body block, ranked by
// RecallScore and truncated to the ontology injection cap. Prepended to the
// very front of inst.Base so the protected role anchor and long-term
// knowledge lead the system prompt.
func (a *Actor) memoryOntologyBlock() *domain.ContentBlock {
	g := a.graphRead()
	if g == nil {
		return nil
	}
	ontology, _, _ := g.RecallInjection()
	if len(ontology) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("## Active Memory (Ontology)\n\n")
	for _, n := range ontology {
		b.WriteString(n.Label)
		b.WriteByte('\n')
	}
	blk := domain.ContentBlock{Type: domain.ContentBlockText, Text: strings.TrimSpace(b.String())}
	return &blk
}

// memoryExperienceBlock returns the experience head-index block (ID + short
// head), ranked by RecallScore and truncated to the experience injection cap.
// Appended to the end of inst.Resolved so it sits at the system-prompt tail,
// just before hot context — close to the action surface but still inside the
// system prompt.
func (a *Actor) memoryExperienceBlock() *domain.ContentBlock {
	g := a.graphRead()
	if g == nil {
		return nil
	}
	_, experience, _ := g.RecallInjection()
	if len(experience) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("## Active Memory (Experience Heads)\n\n")
	for _, n := range experience {
		fmt.Fprintf(&b, "- [%s] %s\n", n.ID, nodeHead(n))
	}
	blk := domain.ContentBlock{Type: domain.ContentBlockText, Text: strings.TrimSpace(b.String())}
	return &blk
}

// memoryPreHotBlocks returns ontology + experience blocks for callers that
// need both (inspector previews, backward-compat). In the dispatch path these
// are split: ontology → Base front, experience → Resolved tail.
func (a *Actor) memoryPreHotBlocks() []domain.ContentBlock {
	var blocks []domain.ContentBlock
	if blk := a.memoryOntologyBlock(); blk != nil {
		blocks = append(blocks, *blk)
	}
	if blk := a.memoryExperienceBlock(); blk != nil {
		blocks = append(blocks, *blk)
	}
	return blocks
}

// memoryPostHotBlocks returns the session full body block, ranked by
// RecallScore and truncated to the session injection cap, injected after hot context.
func (a *Actor) memoryPostHotBlocks() []domain.ContentBlock {
	g := a.graphRead()
	if g == nil {
		return nil
	}
	_, _, session := g.RecallInjection()
	if len(session) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("## Active Memory (Session)\n\n")
	for _, n := range session {
		fmt.Fprintf(&b, "- [%s] %s\n", n.ID, n.Label)
	}
	return []domain.ContentBlock{{Type: domain.ContentBlockText, Text: strings.TrimSpace(b.String())}}
}

// appendMemoryBase prepends the ontology block to the beginning of Base so
// the protected role anchor and long-term knowledge lead the system prompt.
// Experience is injected separately via appendMemoryExperience (→ Resolved
// tail). Pure read — no Touch side effects.
func (a *Actor) appendMemoryBase(inst *domain.CompiledInstructions) {
	if inst == nil {
		return
	}
	blk := a.memoryOntologyBlock()
	if blk == nil || blk.Text == "" {
		return
	}
	inst.Base = append([]string{blk.Text}, inst.Base...)
}

// appendMemoryExperience appends the experience head-index block to the end
// of Resolved so it sits at the system-prompt tail, just before hot context.
// Pure read — no Touch side effects.
func (a *Actor) appendMemoryExperience(inst *domain.CompiledInstructions) {
	if inst == nil {
		return
	}
	blk := a.memoryExperienceBlock()
	if blk == nil || blk.Text == "" {
		return
	}
	inst.Resolved = append(inst.Resolved, blk.Text)
}

// memorySegmentKind detects whether text is a memory injection block and
// returns (kind, label) for artifact fragment tagging. Returns ("", "")
// for non-memory text. The kind values match the frontend segmentColor map.
func memorySegmentKind(text string) (kind, label string) {
	switch {
	case strings.HasPrefix(text, "## Active Memory (Ontology)"):
		return "memory-ontology", "Memory (Ontology)"
	case strings.HasPrefix(text, "## Active Memory (Experience Heads)"):
		return "memory-experience", "Memory (Experience)"
	case strings.HasPrefix(text, "## Active Memory (Session)"):
		return "memory-session", "Memory (Session)"
	}
	return "", ""
}

// touchProjectedMemory touches exactly the nodes that were projected into the
// current turn's context (the RecallScore-ranked, per-layer-capped subset),
// updating AccessedAt and triggering the antagonist siphon only for them.
// Non-injected nodes are left untouched, so their recency signal and siphon
// state are not perturbed every turn. Called only during real turn
// compilation, never from inspector previews.
func (a *Actor) touchProjectedMemory() {
	g := a.graphRead()
	if g == nil {
		return
	}
	ontology, experience, session := g.RecallInjection()
	for _, n := range ontology {
		g.Touch(n.ID)
	}
	for _, n := range experience {
		g.Touch(n.ID)
	}
	for _, n := range session {
		g.Touch(n.ID)
	}
}

// memoryHotContext returns the memory pre-hot blocks for backward compatibility
// with callers that inject a single block into the hot context slice.
func (a *Actor) memoryHotContext() *domain.ContentBlock {
	blocks := a.memoryPreHotBlocks()
	if len(blocks) == 0 {
		return nil
	}
	// Combine all pre-hot blocks into one for callers that append a single block.
	// For the full layered order use memoryPreHotBlocks/memoryPostHotBlocks directly.
	var parts []string
	for _, blk := range blocks {
		parts = append(parts, blk.Text)
	}
	return &domain.ContentBlock{
		Type: domain.ContentBlockText,
		Text: strings.Join(parts, "\n\n"),
	}
}

// graphRead returns the current memory graph under a read lock, or nil when
// memory is not mounted. Safe to call from any goroutine (pure handlers, exec
// loop, owner loop). The returned pointer is stable for the caller's use; the
// MemoryGraph's own mutex protects its internal data.
func (a *Actor) graphRead() *memory.MemoryGraph {
	a.graphMu.RLock()
	g := a.Graph
	a.graphMu.RUnlock()
	return g
}

// memoryEnabled returns true when builtin:mode:memory is present and enabled.
func (a *Actor) memoryEnabled() bool {
	return a.cardRefEnabled("builtin:mode:memory")
}

// ensureMemoryCardMounted ensures the memory card is mounted/disabled correctly.
func (a *Actor) ensureMemoryCardMounted(ctx actor.Context) bool {
	return a.ensureComponentCardMounted(ctx, "builtin:mode:memory", "user", a.Graph != nil)
}

// syncMemoryMountState ensures the memory graph is initialized or torn down
// to match the current card mount state. Called after load/seed startup and
// component changes (mount, unmount, set_enabled).
func (a *Actor) syncMemoryMountState(ctx actor.Context) {
	if a.memoryEnabled() {
		a.graphMu.Lock()
		fresh := a.Graph == nil
		if fresh {
			a.Graph = memory.New(memory.Config{})
		}
		a.graphMu.Unlock()
		if fresh {
			if err := a.restoreMemoryFromPersisted(); err != nil {
				if ctx != nil {
					ctx.Logger().Error("agent: restoreMemoryFromPersisted failed", "error", err, "actorId", a.actorID)
				}
			}
			a.seedRoleOntologyAnchor(ctx)
		}
	} else {
		a.unmountMemory(ctx)
	}
}

// seedRoleOntologyAnchor creates or updates a protected ontology anchor node
// for the agent's role prompt. The anchor ID is deterministic: role:<kind>.
// Called inside syncMemoryMountState after the graph is initialized.
func (a *Actor) seedRoleOntologyAnchor(ctx actor.Context) {
	g := a.graphRead()
	if g == nil || a.agentKind == "" {
		return
	}
	cfg := a.fetchAgentKindConfig(ctx)
	if cfg.Kind == "" || cfg.RolePromptRef.Key == "" {
		return
	}

	workspaceRef, ok := ctx.LookupService("workspace")
	if !ok {
		return
	}

	cardID := promptComponentID(cfg.RolePromptRef)
	contribution, ok := a.resolveWorkspacePrompt(ctx, workspaceRef, cardID)
	if !ok || contribution.Text == "" {
		return
	}

	anchorID := memory.NodeID("role:" + cfg.Kind)
	node := g.UpsertNode(anchorID, memory.NodeTypeOntology, contribution.Text, "Agent role: "+cfg.Kind, estimateTokensLocal(contribution.Text))
	if node != nil {
		node.Protected = true
		node.Energy = 1000.0
	}
}

// unmountMemory clears the graph and its staged durable snapshot.
// DeleteMonocards runs inside graphMu.Lock so it serializes with
// persistMemorySnapshot's graph read, preventing a concurrent
// checkpoint/unmount race.
func (a *Actor) unmountMemory(_ actor.Context) {
	a.graphMu.Lock()
	_ = memory.DeleteMonocards(a.memoryStore(), a.memoryPersisted)
	a.Graph = nil
	a.memoryPersisted = nil
	a.graphMu.Unlock()
}

func (a *Actor) memoryStore() persist.Persist {
	return persist.NewMarkdownPersist(config.ActorDataDir() + "/agent/" + a.agentStoreKey() + "/memory")
}

// persistMemorySnapshot checkpoints the current graph to durable monocard
// files and returns the updated manifest. On failure it clears
// memoryPersisted so the subsequent saveMailbox skips the stale manifest,
// preventing a dirty-state write. A nil graph is NOT a failure: during
// startup (before syncMemoryMountState) the loaded manifest must survive
// intermediate saves, otherwise state.json loses the memory pointer and the
// upcoming restore silently starts from an empty graph — permanently
// orphaning the monocard files. The only path that may clear
// memoryPersisted for a nil graph is unmountMemory, which deletes the
// monocard files together with it.
func (a *Actor) persistMemorySnapshot() error {
	g := a.graphRead()
	if g == nil {
		return nil
	}
	data, err := g.CheckpointMonocards(a.memoryStore(), a.memoryPersisted)
	if err != nil {
		a.memoryPersisted = nil
		return fmt.Errorf("persistMemorySnapshot: %w", err)
	}
	a.memoryPersisted = data
	return nil
}

// restoreMemoryFromPersisted restores the in-memory graph from durable
// monocard files referenced by the persisted manifest. Returns an error
// on failure so the caller can log or propagate it.
func (a *Actor) restoreMemoryFromPersisted() error {
	g := a.graphRead()
	if g == nil || len(a.memoryPersisted) == 0 {
		return nil
	}
	if err := g.RestoreMonocards(a.memoryStore(), a.memoryPersisted); err != nil {
		return fmt.Errorf("restoreMemoryFromPersisted: %w", err)
	}
	return nil
}

// ── Handlers ─────────────────────────────────────────────────────────────

// handleMemorySave persists a memory node. Returns error when unmounted.
// Defaults to session layer. Rejects awake save to experience/ontology.
func (a *Actor) handleMemorySave(ctx actor.Context, req domain.MemorySaveReq) (domain.MemorySaveResp, error) {
	if a.Graph == nil {
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: memory mode is not mounted")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: content is required")
	}

	layer := strings.ToLower(strings.TrimSpace(req.Layer))
	if layer == "" {
		layer = "session"
	}

	var nodeType memory.NodeType
	switch layer {
	case "session":
		nodeType = memory.NodeTypeSession
	case "experience":
		nodeType = memory.NodeTypeExperience
	case "ontology":
		nodeType = memory.NodeTypeOntology
	default:
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: unknown layer %q (use session, experience, or ontology)", req.Layer)
	}

	// Reject awake save to experience or ontology (only dreamer promotes).
	if nodeType != memory.NodeTypeSession {
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: cannot save directly to %q layer; only session is allowed for direct saves", layer)
	}

	// Enforce hard length limit (session only).
	if utf8.RuneCountInString(content) > memorySaveMaxRunes {
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: content exceeds %d characters (%d runes)", memorySaveMaxRunes, utf8.RuneCountInString(content))
	}

	tokens := estimateTokensLocal(content)
	n := a.Graph.AddNode("", nodeType, content, "", tokens)
	if n == nil {
		return domain.MemorySaveResp{}, fmt.Errorf("memory_save: failed to create node")
	}
	// A new node means the graph changed; new information resets the sleep
	// failure backoff so automatic dreaming retries are no longer skipped.
	a.sleepFailureCount = 0
	a.saveMailbox(ctx)
	a.startMemoryWeave(ctx, n)
	// Rebuild the cached prompt snapshots so the new memory is immediately
	// visible in the Prompt Context Snapshot and Compiled Prompt inspector
	// views (resolveInstructions re-reads the live memory graph each refresh).
	if ctx != nil {
		a.refreshPromptCaches(ctx)
	}

	return domain.MemorySaveResp{
		Node: convertNode(n),
	}, nil
}

func (a *Actor) startMemoryWeave(ctx actor.Context, node *memory.Node) {
	if ctx == nil || node == nil || a.Graph == nil {
		return
	}
	a.refreshAggRefs(ctx)
	agg, unit, _ := a.resolveTarget(ctx, a.fast)
	if agg == nil {
		return
	}
	candidates := a.Graph.RecallMixed(12)
	var b strings.Builder
	for _, candidate := range candidates {
		if candidate.ID == node.ID {
			continue
		}
		fmt.Fprintf(&b, "- %s [%s]: %s\n", candidate.ID, candidate.Type, candidate.Label)
	}
	if b.Len() == 0 {
		return
	}
	req := domain.SendSessionMessageReq{
		System:   "Choose relationships for the NEW node against the candidates. Reply only JSON: {\"edges\":[{\"target_node_id\":\"id\",\"decision\":\"peer|cross|antagonist|skip\"}]}. You may include multiple candidates. peer/antagonist require the same layer; cross requires ADJACENT differing layers (session↔experience, experience↔ontology; session↔ontology is forbidden).",
		Messages: []domain.ChatMessage{{Role: "user", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: fmt.Sprintf("NEW %s [%s]: %s\nCANDIDATES:\n%s", node.ID, node.Type, node.Label, b.String())}}}},
	}
	if unit.Model != "" {
		req.Unit = &unit
	}
	self := ctx.Self()
	life := ctx.Lifecycle()
	panicprobe.SafeGo(ctx, "memory_weave", func() {
		callCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := agg.Invoke(callCtx, "aiaggregator.tool_judge", req).Final(callCtx)
		if err != nil {
			slog.Warn("memory_weave: LLM invoke failed", "error", err, "actorId", a.actorID)
			return
		}
		edges := decodeMemoryWeaveDecisions(decodeIntentResult(result))
		for _, edge := range edges {
			if edge.Decision == "skip" || edge.TargetNodeID == "" {
				continue
			}
			applyCtx, applyCancel := context.WithTimeout(life, 5*time.Second)
			_ = self.Invoke(applyCtx, "memory_weave_apply", domain.MemoryWeaveApplyReq{NewNodeID: string(node.ID), Decision: edge.Decision, TargetNodeID: edge.TargetNodeID}).Close()
			applyCancel()
		}
	})
}

// buildCompactSnapshot creates a trimmed graph snapshot using the top-N
// recall-ranked nodes with edges filtered to only those between the top-N,
// plus communities computed on the same subset. Used when the full graph
// snapshot exceeds practical prompt size. When the graph is small enough
// (<= N nodes), it falls back to the full snapshot and communities.
func (a *Actor) buildCompactSnapshot(g *memory.MemoryGraph, n int) (snapshot []byte, communities [][]memory.NodeID) {
	topNodes := g.RecallMixed(n)
	if len(topNodes) == 0 {
		// Fallback: full snapshot.
		var err error
		snapshot, err = g.Snapshot()
		if err == nil {
			communities = g.Communities()
		}
		return
	}

	// Build set of top-N IDs.
	topIDs := make(map[memory.NodeID]struct{}, len(topNodes))
	for _, node := range topNodes {
		topIDs[node.ID] = struct{}{}
	}

	// Filter edges to only those between top-N nodes.
	allEdges := g.Edges()
	filteredEdges := make([]memory.Edge, 0, len(allEdges))
	for _, e := range allEdges {
		if _, fromOK := topIDs[e.From]; !fromOK {
			continue
		}
		if _, toOK := topIDs[e.To]; !toOK {
			continue
		}
		filteredEdges = append(filteredEdges, e)
	}

	// Build compact JSON snapshot (nodes + edges; TickCount omitted for brevity).
	compact := struct {
		Nodes []memory.Node `json:"nodes"`
		Edges []memory.Edge `json:"edges"`
	}{
		Nodes: make([]memory.Node, len(topNodes)),
		Edges: filteredEdges,
	}
	for i, node := range topNodes {
		compact.Nodes[i] = *node
	}
	snapshot, _ = json.Marshal(compact)

	// Build communities from the filtered subgraph.
	communities = buildFilteredCommunities(topIDs, filteredEdges)
	return
}

// buildFilteredCommunities runs BFS on a subset of nodes to find connected
// components, using only the provided edges. Results are deterministic.
func buildFilteredCommunities(nodeSet map[memory.NodeID]struct{}, edges []memory.Edge) [][]memory.NodeID {
	// Build undirected adjacency for the subset.
	adj := make(map[memory.NodeID][]memory.NodeID)
	for id := range nodeSet {
		adj[id] = nil
	}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	// Sort IDs for deterministic start order.
	allIDs := make([]memory.NodeID, 0, len(nodeSet))
	for id := range nodeSet {
		allIDs = append(allIDs, id)
	}
	sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })

	visited := make(map[memory.NodeID]bool, len(nodeSet))
	var components [][]memory.NodeID

	for _, start := range allIDs {
		if visited[start] {
			continue
		}
		comp := []memory.NodeID{start}
		visited[start] = true
		queue := []memory.NodeID{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, nb := range adj[cur] {
				if !visited[nb] {
					visited[nb] = true
					comp = append(comp, nb)
					queue = append(queue, nb)
				}
			}
		}
		sort.Slice(comp, func(i, j int) bool { return comp[i] < comp[j] })
		components = append(components, comp)
	}

	sort.Slice(components, func(i, j int) bool {
		return components[i][0] < components[j][0]
	})
	return components
}

// maybeStartMemorySleep spawns the dreamer child to consolidate memory.
// When force is true, the IsSleepDue gate is skipped — used by /dream
// and post-compaction triggers where the caller already decided dreaming
// should run.
func (a *Actor) maybeStartMemorySleep(ctx actor.Context, turnID string, force bool) bool {
	g := a.graphRead()
	if ctx == nil || g == nil || a.memorySleeping {
		return false
	}
	if !force && !g.IsSleepDue() {
		return false
	}
	// Backoff: after 3 consecutive ApplySleep failures, skip automatic sleep
	// to avoid an infinite retry loop. force=true (e.g. /dream, post-compaction)
	// still triggers regardless.
	if !force && a.sleepFailureCount >= 3 {
		return false
	}
	// Per-turn latch: auto-compaction can fire multiple times within one turn
	// (pre-tool, prompt-too-long retry, user /compact). Only spawn the dreamer
	// once per turn.
	if turnID != "" && a.memorySleepTurn == turnID {
		return false
	}
	var snapshot []byte
	var communities [][]memory.NodeID
	var err error
	if g.NodeCount() > 60 {
		snapshot, communities = a.buildCompactSnapshot(g, 60)
	} else {
		snapshot, err = g.Snapshot()
		if err != nil {
			return false
		}
		communities = g.Communities()
	}

	// Layer counts for pressure-guided sleep.
	pressureSummary := g.LayerPressureSummary()

	prompt := fmt.Sprintf(`You are an isolated memory dreamer. Analyze only this memory graph and its deterministic communities. Return only a JSON array of instructions. Each instruction has action (merge|promote|connect), target_id, and optional fields.
- merge: provide new head and content synthesizing both nodes; merge_id is the node absorbed into target_id
- promote: provide new head+content for the promoted node
- connect: create an edge between target_id and merge_id; provide edge_type ("peer"|"cross"|"antagonist"). peer/antagonist require same layer; cross requires ADJACENT differing layers (session↔experience, experience↔ontology; session↔ontology is forbidden).
Use only existing node IDs. An empty array is valid.

Consolidation directions per layer:
- Session (short-term): merge duplicate or closely related events, connect related items, promote stable recurring patterns upward to experience.
- Experience (long-term, the largest pool): merge related skills and reusable knowledge, connect cross-layer links to ontology, promote durable evidence upward to ontology.
- Ontology (core, the smallest pool): prefer merge to disambiguate and reshape. Promote to ontology only with durable, long-stable evidence.

Do NOT delete nodes. Cleanup is handled automatically by energy decay.

GRAPH:
%s
COMMUNITIES:
%v
PRESSURE: %s`, snapshot, communities, pressureSummary)

	a.memorySleeping = true
	_, err = a.spawnDreamChild(ctx, prompt)
	if err != nil {
		a.memorySleeping = false
		return false
	}
	if turnID != "" {
		a.memorySleepTurn = turnID
	}
	return true
}

func decodeSleepInstructions(raw string) ([]memory.SleepInstruction, error) {
	type sleepItem struct {
		Action   string `json:"action"`
		TargetID string `json:"target_id"`
		MergeID  string `json:"merge_id"`
		Head     string `json:"head,omitempty"`
		Content  string `json:"content,omitempty"`
		EdgeType string `json:"edge_type,omitempty"`
	}
	instructions := []memory.SleepInstruction{}
	for _, cand := range sleepJSONCandidates(raw) {
		var parsed []sleepItem
		if err := json.Unmarshal([]byte(cand), &parsed); err != nil {
			continue
		}
		instructions = instructions[:0]
		for _, item := range parsed {
			instructions = append(instructions, memory.SleepInstruction{
				Action:   memory.SleepAction(item.Action),
				TargetID: memory.NodeID(item.TargetID),
				MergeID:  memory.NodeID(item.MergeID),
				Head:     item.Head,
				Content:  item.Content,
				EdgeType: item.EdgeType,
			})
		}
		return instructions, nil
	}
	// Surface the raw shape in the error so future dreamer regressions are
	// diagnosable from the dream step text alone.
	snippet := raw
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return nil, fmt.Errorf("no sleep instruction array found in dreamer output: %q", snippet)
}

// sleepJSONCandidates returns JSON-array candidate strings from a dreamer
// response, best-first. Dreamer models are ordinary chat models: responses
// may be wrapped in <think> blocks, fenced code blocks, or prose with
// incidental brackets. Strategy: strip <think> blocks, then yield balanced
// top-level arrays, then fenced-block contents, then the naive
// first-'['-to-last-']' slice.
func sleepJSONCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	for {
		s := strings.Index(raw, "<think>")
		if s < 0 {
			break
		}
		e := strings.Index(raw[s:], "</think>")
		if e < 0 {
			raw = raw[:s]
			break
		}
		raw = raw[:s] + raw[s+e+len("</think>"):]
	}
	candidates := balancedArrays(raw)
	candidates = append(candidates, fencedBlocks(raw)...)
	if start, end := strings.IndexByte(raw, '['), strings.LastIndexByte(raw, ']'); start >= 0 && end >= start {
		candidates = append(candidates, raw[start:end+1])
	}
	return candidates
}

// balancedArrays returns every top-level, correctly bracket-balanced
// substring of s starting at '[' and ending at ']', ignoring brackets
// inside JSON string literals.
func balancedArrays(s string) []string {
	var out []string
	depth := 0
	inStr, esc := false, false
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && c == '[':
			if depth == 0 {
				start = i
			}
			depth++
		case !inStr && c == ']':
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i+1])
				start = -1
			}
			if depth < 0 {
				return out
			}
		}
	}
	return out
}

// fencedBlocks returns the inner text of ``` (or ```json) fenced code blocks.
func fencedBlocks(s string) []string {
	var out []string
	for {
		f := strings.Index(s, "```")
		if f < 0 {
			return out
		}
		rest := s[f+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 && strings.TrimSpace(rest[:nl]) != "" {
			rest = rest[nl+1:] // skip the language tag line
		}
		e := strings.Index(rest, "```")
		if e < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:e]))
		s = rest[e+3:]
	}
}

func (a *Actor) finishMemorySleep(ctx actor.Context, raw string) {
	defer func() {
		a.memorySleeping = false
		a.maybeAutoStartTurn(ctx)
	}()
	if a.Graph == nil {
		a.closeDreamStep(ctx, "Memory graph not available.")
		return
	}
	instructions, err := decodeSleepInstructions(raw)
	if err != nil {
		a.closeDreamStep(ctx, fmt.Sprintf("Dream failed: invalid instructions (%v).", err))
		return
	}
	if applyErr := a.Graph.ApplySleep(instructions); applyErr != nil {
		a.sleepFailureCount++
		a.closeDreamStep(ctx, fmt.Sprintf("Dream failed: %v.", applyErr))
		return
	}
	a.sleepFailureCount = 0
	a.saveMailbox(ctx)
	if ctx != nil {
		a.refreshPromptCaches(ctx)
	}
	a.closeDreamStep(ctx, summarizeDream(instructions))
}

// closeDreamStep emits a text block and step.closed for the /dream assistant
// step, if one is open. No-op when dreamStepID is empty (compaction-triggered
// dreams don't open a step).
func (a *Actor) closeDreamStep(ctx actor.Context, text string) {
	if a.dreamStepID == "" {
		return
	}
	stepID := a.dreamStepID
	a.dreamStepID = ""
	turnID := stepID
	if idx := strings.LastIndex(stepID, "-dream"); idx > 0 {
		turnID = stepID[:idx]
	}
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:            "step.opened",
		StepID:          stepID,
		TurnID:          turnID,
		StepType:        "text",
		Role:            "assistant",
		Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: text},
		OriginMessageID: turnID,
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: stepID,
		TurnID: turnID,
	})
}

// summarizeDream produces a human-readable summary of dream instructions.
func summarizeDream(instructions []memory.SleepInstruction) string {
	if len(instructions) == 0 {
		return "Memory dream complete. No changes needed."
	}
	merges, promotes, connects := 0, 0, 0
	for _, inst := range instructions {
		switch inst.Action {
		case memory.SleepMerge:
			merges++
		case memory.SleepPromote:
			promotes++
		case memory.SleepConnect:
			connects++
		}
	}
	parts := []string{"Memory dream complete."}
	if merges > 0 {
		parts = append(parts, fmt.Sprintf("%d merge(s).", merges))
	}
	if promotes > 0 {
		parts = append(parts, fmt.Sprintf("%d promote(s).", promotes))
	}
	if connects > 0 {
		parts = append(parts, fmt.Sprintf("%d connect(s).", connects))
	}
	return strings.Join(parts, " ")
}

type weaveEdge struct {
	Decision     string
	TargetNodeID string
}

func decodeMemoryWeaveDecisions(raw string) []weaveEdge {
	raw = strings.TrimSpace(raw)
	if start, end := strings.IndexByte(raw, '{'), strings.LastIndexByte(raw, '}'); start >= 0 && end >= start {
		raw = raw[start : end+1]
	}
	var parsed struct {
		Edges []struct {
			Decision     string `json:"decision"`
			TargetNodeID string `json:"target_node_id"`
		} `json:"edges"`
		// Backward compat: old single-decision format.
		Decision     string `json:"decision"`
		TargetNodeID string `json:"target_node_id"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}
	if len(parsed.Edges) == 0 && parsed.Decision != "" {
		return []weaveEdge{{Decision: strings.ToLower(parsed.Decision), TargetNodeID: parsed.TargetNodeID}}
	}
	edges := make([]weaveEdge, len(parsed.Edges))
	for i, e := range parsed.Edges {
		edges[i] = weaveEdge{Decision: strings.ToLower(e.Decision), TargetNodeID: e.TargetNodeID}
	}
	return edges
}

func (a *Actor) handleMemoryWeaveApply(ctx actor.Context, req domain.MemoryWeaveApplyReq) error {
	if a.Graph == nil || a.Graph.Node(memory.NodeID(req.NewNodeID)) == nil {
		return nil
	}
	var edgeType memory.EdgeType
	switch strings.ToLower(req.Decision) {
	case "peer":
		edgeType = memory.EdgeTypePeer
	case "cross":
		edgeType = memory.EdgeTypeCross
	case "antagonist":
		edgeType = memory.EdgeTypeAntagonist
	default:
		return nil
	}
	if req.TargetNodeID == "" || a.Graph.AddEdge(memory.NodeID(req.NewNodeID), memory.NodeID(req.TargetNodeID), edgeType) != nil {
		return nil
	}
	// A new edge means the graph changed; reset the sleep failure backoff so
	// automatic dreaming retries are no longer skipped.
	a.sleepFailureCount = 0
	a.saveMailbox(ctx)
	// Edge additions change recall ranking; refresh inspector caches.
	if ctx != nil {
		a.refreshPromptCaches(ctx)
	}
	return nil
}

// handleMemoryRecall returns nodes from the graph. Optional layer and query.
// Query supports exact ID recall (one-hop expansion) or content substring.
// Excludes MarkDeath nodes via graph behavior.
func (a *Actor) handleMemoryRecall(_ actor.PureContext, req domain.MemoryRecallReq) (domain.MemoryRecallResp, error) {
	g := a.graphRead()
	if g == nil {
		return domain.MemoryRecallResp{}, fmt.Errorf("memory_recall: memory mode is not mounted")
	}

	// Parse optional layer filter.
	var filterType *memory.NodeType
	if req.Layer != "" {
		var nt memory.NodeType
		switch strings.ToLower(req.Layer) {
		case "session":
			nt = memory.NodeTypeSession
		case "experience":
			nt = memory.NodeTypeExperience
		case "ontology":
			nt = memory.NodeTypeOntology
		default:
			return domain.MemoryRecallResp{}, fmt.Errorf("memory_recall: unknown layer %q", req.Layer)
		}
		filterType = &nt
	}

	query := strings.TrimSpace(req.Query)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}

	// Exact ID recall with one-hop expansion.
	if query != "" {
		if n := g.RecallByID(memory.NodeID(query)); n != nil {
			g.Touch(n.ID) // Touch the recalled node
			nodes := []domain.MemoryNode{convertNode(n)}
			// One-hop expansion.
			neighbors, _ := g.RecallOneHop(n.ID)
			for _, nb := range neighbors {
				if filterType != nil && nb.Type != *filterType {
					continue
				}
				g.Touch(nb.ID) // Touch expanded neighbors
				nodes = append(nodes, convertNode(nb))
			}
			return domain.MemoryRecallResp{Nodes: nodes}, nil
		}
	}

	// Content substring query: scan all non-marked nodes.
	if query != "" {
		type recallCandidate struct {
			node  *memory.Node
			score float64
		}
		now := g.CurrentTick()
		decayK := g.TickDecayK()
		var matches []recallCandidate
		all := g.Nodes()
		for _, n := range all {
			if filterType != nil && n.Type != *filterType {
				continue
			}
			if strings.Contains(strings.ToLower(n.Label), strings.ToLower(query)) {
				matches = append(matches, recallCandidate{
					node:  n,
					score: memory.RecallScore(n, now, decayK),
				})
			}
		}
		// Rank by RecallScore descending, deterministic tie-break by ID.
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			return matches[i].node.ID < matches[j].node.ID
		})
		if len(matches) > limit {
			matches = matches[:limit]
		}
		out := make([]domain.MemoryNode, 0, len(matches))
		for _, c := range matches {
			g.Touch(c.node.ID) // Touch only the nodes returned
			out = append(out, convertNode(c.node))
		}
		return domain.MemoryRecallResp{Nodes: out}, nil
	}

	// No query: return top-ranked nodes by layer.
	if filterType != nil {
		ranked := g.Recall(*filterType, limit)
		out := make([]domain.MemoryNode, len(ranked))
		for i, n := range ranked {
			g.Touch(n.ID) // Touch ranked nodes
			out[i] = convertNode(n)
		}
		return domain.MemoryRecallResp{Nodes: out}, nil
	}

	// Mixed recall.
	ranked := g.RecallMixed(limit)
	out := make([]domain.MemoryNode, len(ranked))
	for i, n := range ranked {
		g.Touch(n.ID) // Touch ranked nodes
		out[i] = convertNode(n)
	}
	return domain.MemoryRecallResp{Nodes: out}, nil
}

// handleMemorySnapshot returns the current graph state. Always safe to call
// (returns Mounted=false when not mounted). Runs as a pure (stateless) handler
// off the owner queue so it stays responsive during turn execution.
func (a *Actor) handleMemorySnapshot(_ actor.PureContext, _ domain.MemorySnapshotReq) (domain.MemorySnapshotResp, error) {
	g := a.graphRead()
	if g == nil {
		return domain.MemorySnapshotResp{Mounted: false}, nil
	}
	nodes := g.Nodes()
	edges := g.Edges()

	// Sort deterministically by (from, to, type).
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Type < edges[j].Type
	})

	snapNodes := make([]domain.MemoryNode, 0, len(nodes))
	for _, n := range nodes {
		snapNodes = append(snapNodes, convertNode(n))
	}
	snapEdges := make([]domain.MemoryEdge, len(edges))
	for i, e := range edges {
		snapEdges[i] = domain.MemoryEdge{
			From: string(e.From),
			To:   string(e.To),
			Type: e.Type.String(),
		}
	}
	return domain.MemorySnapshotResp{
		Mounted:  true,
		SleepDue: g.IsSleepDue(),
		Nodes:    snapNodes,
		Edges:    snapEdges,
	}, nil
}

// handleMemoryDream triggers a memory consolidation cycle (merge/promote/connect).
// The dreamer runs asynchronously; this returns immediately with started=true.
func (a *Actor) handleMemoryDream(ctx actor.Context) (map[string]any, error) {
	if a.Graph == nil {
		return map[string]any{"started": false, "error": "memory mode is not mounted"}, nil
	}
	if a.memorySleeping {
		return map[string]any{"started": false, "error": "a dream is already in progress"}, nil
	}
	// Open a visible dream step so tool-triggered dreams (unlike /dream,
	// which opens its own step in agent_chat) surface their outcome —
	// success or failure — in the chat timeline instead of finishing
	// silently.
	if turnRef := a.getActiveTurnRef(); turnRef != "" && a.dreamStepID == "" {
		stepID := turnRef + "-dream"
		a.dreamStepID = stepID
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:            "step.opened",
			StepID:          stepID,
			TurnID:          turnRef,
			StepType:        "text",
			Role:            "assistant",
			Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: "Dreaming…"},
			OriginMessageID: turnRef,
		})
	}
	started := a.maybeStartMemorySleep(ctx, "", true)
	if !started && a.dreamStepID != "" {
		// Spawn failed (slot/snapshot/spawn error) and finishMemorySleep
		// will never fire — close the step instead of leaving it hanging.
		a.closeDreamStep(ctx, "Memory dream failed to start.")
	}
	return map[string]any{"started": started}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// estimateTokensLocal estimates token count for content using a simple
// deterministic approximation: 1 token per 4 ASCII chars, 1 token per
// 1.5 CJK chars, clamped at 1 minimum. This is used when the production
// tokenizer is unavailable.
func estimateTokensLocal(content string) int {
	if content == "" {
		return 1
	}
	var count int
	for _, r := range content {
		if r > 127 {
			// Non-ASCII: approximate as 1 token per rune.
			count++
		} else {
			// ASCII: 1 token per 4 chars.
			count++
		}
	}
	// Rough approximation: ~1 token per 4 chars average.
	t := count / 4
	if t < 1 {
		return 1
	}
	// Sanity cap.
	runes := utf8.RuneCountInString(content)
	if t > runes {
		t = runes
	}
	return t
}

func convertNode(n *memory.Node) domain.MemoryNode {
	return domain.MemoryNode{
		ID:      string(n.ID),
		Layer:   n.Type.String(),
		Content: n.Label,
		Head:    n.Head,
		Tokens:  int32(n.Tokens),
		Energy:  float32(n.Energy),
		CreatedAt: func() string {
			if n.CreatedAt.IsZero() {
				return ""
			}
			return n.CreatedAt.Format(time.RFC3339Nano)
		}(),
		AccessedAt: func() string {
			if n.AccessedAt.IsZero() {
				return ""
			}
			return n.AccessedAt.Format(time.RFC3339Nano)
		}(),
		MarkedForDeath: n.MarkDeath,
		Protected:      n.Protected,
		LastAccessTick: int32(n.LastAccessTick),
	}
}
