package project

import (
	"fmt"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// mountSpec describes how a card of a given type auto-mounts to a virtual
// builtin node. The spec is mirrored into the virtual node's card data so the
// frontend can read the rules without hardcoding them.
type mountSpec struct {
	virtualNode string
	mountType   string
	mountStatus string // empty = catch-all for the type (unrouted by status)
	autoMount   bool
}

// canonicalTaskStatuses is the authoritative ordered status set. Each has a
// dedicated virtual node under the __builtin_task__ aggregator.
// defaultTaskStatusAliases is the single source of truth for mapping legacy,
// misspelled, and synonym status values to the canonical task status set. It is
// used by both the repair/validation path and the mount-routing path so that
// cards converge to canonical values regardless of how they were authored.
// normalizeTaskStatus maps a status value to its canonical form. Canonical
// values are normalized to lowercase, aliases are resolved via
// defaultTaskStatusAliases, and unknown values pass through unchanged.
func normalizeTaskStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if _, ok := taskStatusNode[s]; ok {
		return s
	}
	if mapped, ok := defaultTaskStatusAliases[s]; ok {
		return mapped
	}
	return status
}

// statusBuiltinVisuals provides the default accent and icon for the virtual
// status nodes so they render with consistent color and iconography.
// builtinMountSpecs is the authoritative table of type-driven virtual mount
// rules. Task cards route to a per-status node (one of the six canonical
// statuses) which is itself a child of __builtin_task__; tasks never mount
// directly to __builtin_task__. Concept cards opt out of auto-mounting.
// mountTargetForCard returns the virtual builtin node id a card auto-mounts to
// based on its canonical type (and status for tasks). Returns "" when the card
// type opts out of auto-mounting (e.g. concept) or the type is unknown. Task
// status is lowercased, checked against the canonical set, then resolved via
// aliases (defaulting to defaultTaskStatusAliases when nil) so legacy values
// (in_progress, completed, draft, ...) route to the same column as the board.
// Anything still unrecognized, including empty status, defaults to backlog.
func mountTargetForCard(cardType, cardStatus string, aliases map[string]string) string {
	if aliases == nil {
		aliases = defaultTaskStatusAliases
	}
	t := normalizeCardType(cardType)
	if t == "task" {
		status := canonicalTaskStatus(cardStatus, aliases)
		return taskStatusNode[status]
	}
	// Non-task types: use the first matching catch-all spec.
	for _, s := range builtinMountSpecs {
		if !s.autoMount || s.mountType != t || s.mountStatus != "" {
			continue
		}
		return s.virtualNode
	}
	return ""
}

// canonicalTaskStatus lowercases a status, keeps it if canonical, resolves it
// via the alias map, and falls back to "backlog" for anything unrecognized
// (including empty). This is the single normalization path used by mount
// routing; aliases come from resolveTaskStatusAliases so the editable
// __builtin_task_status_map__ card drives routing.
func canonicalTaskStatus(status string, aliases map[string]string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if _, ok := taskStatusNode[s]; ok {
		return s
	}
	if mapped, ok := aliases[s]; ok {
		if _, ok2 := taskStatusNode[mapped]; ok2 {
			return mapped
		}
	}
	return "backlog"
}

// statusMapCardID is the user-editable card carrying task status aliases. It is
// the single source of truth for alias overrides; defaultTaskStatusAliases is
// only the bootstrap fallback.
const statusMapCardID = "__builtin_task_status_map__"

// resolveTaskStatusAliases builds the effective task-status alias map: the
// default aliases overridden by entries parsed from the __builtin_task_status_map__
// card's data.aliases field (array of "from:to" strings, or a newline-joined
// string), mirroring the frontend parseStatusAliases.
func resolveTaskStatusAliases(items []gen.MonoCardListItem) map[string]string {
	out := make(map[string]string, len(defaultTaskStatusAliases))
	for k, v := range defaultTaskStatusAliases {
		out[k] = v
	}
	for _, c := range items {
		if c.ID != statusMapCardID {
			continue
		}
		raw, _ := c.Data["aliases"]
		parseAliasEntries(raw, out)
	}
	return out
}

// parseAliasEntries parses an aliases value ([]any of "from:to" strings, or a
// newline-separated string) into the target map, skipping malformed entries.
func parseAliasEntries(raw any, target map[string]string) {
	switch v := raw.(type) {
	case []any:
		for _, entry := range v {
			addAliasEntry(target, fmt.Sprint(entry))
		}
	case []string:
		for _, entry := range v {
			addAliasEntry(target, entry)
		}
	case string:
		for _, line := range strings.Split(v, "\n") {
			addAliasEntry(target, line)
		}
	}
}

func addAliasEntry(target map[string]string, entry string) {
	sep := strings.Index(entry, ":")
	if sep <= 0 {
		return
	}
	from := strings.ToLower(strings.TrimSpace(entry[:sep]))
	to := strings.ToLower(strings.TrimSpace(entry[sep+1:]))
	if from != "" && to != "" {
		target[from] = to
	}
}

// virtualMountBuiltinCards returns the builtin virtual-node cards derived from
// the mount specs. Each carries its mount rule in data for the frontend. Status
// nodes declare __builtin_task__ as their parent so they form a single subtree.
func virtualMountBuiltinCards() []*BuiltinCard {
	// Aggregator node: __builtin_task__ holds the six status nodes but never
	// receives task cards directly.
	cards := []*BuiltinCard{{
		Title:  builtinTaskID,
		Tags:   []string{},
		List:   []string{},
		Parent: "",
		Data: map[string]any{
			"builtinRole": "aggregator",
			"visual": map[string]any{
				"accent":   "blue",
				"icon":     "layers",
				"emphasis": "strong",
				"size":     32,
			},
		},
		Body: "",
	}}
	seen := map[string]struct{}{builtinTaskID: {}}
	for _, s := range builtinMountSpecs {
		if _, ok := seen[s.virtualNode]; ok {
			continue
		}
		seen[s.virtualNode] = struct{}{}
		// Named builtins (skill, prompt) have their own card definitions; their
		// mount data is injected by applyMountDataToNamedBuiltins so the spec
		// registry stays the single source of truth. Only synthesize the new
		// virtual nodes here.
		if lookupNamedBuiltin(s.virtualNode) != nil {
			continue
		}
		data, parent := mountDataForSpec(s)
		cards = append(cards, &BuiltinCard{
			Title:  s.virtualNode,
			Tags:   []string{},
			List:   []string{},
			Parent: parent,
			Data:   data,
			Body:   "",
		})
	}
	return cards
}

// mountDataForSpec builds the data block and parent for a mount spec. The data
// is what the frontend reads to derive the mount table (builtinRole/mountType/
// mountStatus/autoMount/visual), so it is the wire contract between the backend spec
// registry and the frontend. Task status nodes parent under __builtin_task__ and
// carry a visual; catch-all nodes (skill/prompt/...) have no status and no parent.
func mountDataForSpec(s mountSpec) (data map[string]any, parent string) {
	data = map[string]any{
		"builtinRole": "mount",
		"mountType":   s.mountType,
		"autoMount":   s.autoMount,
	}
	if s.mountStatus != "" {
		data["mountStatus"] = s.mountStatus
		parent = builtinTaskID
		if visual, ok := statusBuiltinVisuals[s.mountStatus]; ok {
			data["visual"] = visual
		}
		return data, parent
	}
	if visual, ok := builtinMountVisuals[s.virtualNode]; ok {
		data["visual"] = visual
	}
	return data, parent
}

// applyMountDataToNamedBuiltins injects mount-rule data into named builtin cards
// (skill, prompt) that are reused rather than synthesized by
// virtualMountBuiltinCards. Without this those cards ship with no data block,
// so the frontend cannot derive their mount rules and cards of those types never
// mount. builtinMountSpecs remains the single source of truth.
func applyMountDataToNamedBuiltins() {
	byID := make(map[string]*BuiltinCard, len(builtinCards))
	for _, b := range builtinCards {
		byID[b.Title] = b
	}
	for _, s := range builtinMountSpecs {
		b := byID[s.virtualNode]
		if b == nil || lookupNamedBuiltin(s.virtualNode) == nil {
			continue
		}
		data, _ := mountDataForSpec(s)
		if b.Data == nil {
			b.Data = map[string]any{}
		}
		for k, v := range data {
			b.Data[k] = v
		}
	}
}

// lookupNamedBuiltin returns the named builtin card definition for id, if any,
// so virtualMountBuiltinCards does not redefine toc/skill/prompt.
func lookupNamedBuiltin(id string) *BuiltinCard {
	for _, b := range []*BuiltinCard{
		&skillBuiltin, &promptBuiltin,
	} {
		if b.Title == id {
			return b
		}
	}
	return nil
}

// applyTypeMounting adds type-driven virtual mount edges to the index.
// Explicit parent edges remain alongside type-driven mount edges. Concept cards
// (autoMount:false) are left untouched. Task status aliases are resolved from the __builtin_task_status_map__
// card when present (falling back to defaultTaskStatusAliases) so user-edited
// aliases take effect in routing, matching the task board.
//
// Only the builtin virtual nodes themselves (the __builtin_* targets) are
// skipped as mount sources. Namespace-prefixed cards (skill:/prompt:/agent:)
// are deliberately NOT skipped: skill and prompt route via type-mount, while
// agent simply has no spec entry so mountTargetForCard returns "". Skipping
// cards by a bare ":" (as the old isSystemCardID check did) orphaned every
// skill/prompt card from its builtin node.
func (idx *cardIndex) applyTypeMounting(items []gen.MonoCardListItem) {
	aliases := resolveTaskStatusAliases(items)
	for _, c := range items {
		if isVirtualMountNode(c.ID) {
			continue
		}
		target := mountTargetForCard(c.Type, c.Status, aliases)
		if target == "" || target == c.ID {
			continue
		}
		if idx.hasChild(target, c.ID) {
			continue
		}
		idx.childrenOf[target] = append(idx.childrenOf[target], c.ID)
		idx.parentsOf[c.ID] = append(idx.parentsOf[c.ID], target)
	}
}

// isVirtualMountNode reports whether id is a builtin virtual mount/aggregator
// node (e.g. __builtin_skill__, __builtin_todo__). These are mount targets, not
// mount sources, so applyTypeMounting skips them. Unlike the broader
// isSystemCardID (which also matches any "namespace:" id), this does NOT match
// skill:/prompt:/agent: cards, which must participate in type mounting.
func isVirtualMountNode(id string) bool {
	return strings.HasPrefix(id, builtinCardPrefix)
}

func (idx *cardIndex) hasChild(parent, child string) bool {
	for _, id := range idx.childrenOf[parent] {
		if id == child {
			return true
		}
	}
	return false
}

// wellKnownCardParents defines the default tree position for well-known
// project cards that are not type-mounted. This is index-only: it never writes
// to storage and never touches card tags. A card with an explicit parent is
// left untouched so users can still re-parent these cards deliberately.
// applyWellKnownMounting attaches the project_info card to its default parent
// when it has no explicit parent in storage. Mirrors applyTypeMounting: edges
// are virtual, not persisted.
func (idx *cardIndex) applyWellKnownMounting(items []gen.MonoCardListItem) {
	for _, c := range items {
		parent, ok := wellKnownCardParents[c.ID]
		if !ok {
			continue
		}
		if c.Parent != "" || len(idx.parentsOf[c.ID]) > 0 {
			continue
		}
		if _, exists := idx.byID[parent]; !exists {
			continue
		}
		idx.childrenOf[parent] = append(idx.childrenOf[parent], c.ID)
		idx.parentsOf[c.ID] = append(idx.parentsOf[c.ID], parent)
	}
}

// applyDefaultTocMounting attaches parentless wiki cards to the toc container
// so newly created cards are visible in the project's knowledge base out of the
// box. It mirrors applyWellKnownMounting (index-only, never persisted) but is
// type-driven: every renderable card whose canonical type is wiki and that has
// no parent edge (Parent field or membership in another card's List) mounts
// under toc. Excluded: the toc container itself, virtual/named builtin mount
// nodes, builtin-source cards (wiki:*), and machinery cards (standalone or
// component/runtime visibility) — those are structure, not knowledge content.
// Runs last so explicit edges (List/Parent/well-known/type mounts) always win.
func (idx *cardIndex) applyDefaultTocMounting(items []gen.MonoCardListItem) {
	if _, ok := idx.byID[tocID]; !ok {
		return
	}
	for _, c := range items {
		if c.ID == tocID || isVirtualMountNode(c.ID) || c.Source == "builtin" {
			continue
		}
		if normalizeCardType(c.Type) != "wiki" {
			continue
		}
		if c.Parent != "" || len(idx.parentsOf[c.ID]) > 0 {
			continue
		}
		if c.Standalone || c.Visibility == "component" || c.Visibility == "runtime" {
			continue
		}
		idx.childrenOf[tocID] = append(idx.childrenOf[tocID], c.ID)
		idx.parentsOf[c.ID] = append(idx.parentsOf[c.ID], tocID)
	}
}
