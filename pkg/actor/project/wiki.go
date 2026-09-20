package project

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/timeutil"
)

const (
	builtinCardPrefix  = "__builtin_"
	builtinSkillID     = builtinCardPrefix + "skill__"
	builtinPluginID    = builtinCardPrefix + "plugin__"
	builtinAgentID     = builtinCardPrefix + "agent__"
	builtinComponentID = builtinCardPrefix + "component__"
	builtinPromptID    = builtinCardPrefix + "prompt__"
	// Type-driven virtual mount nodes. Cards auto-mount to these based on type
	// (and status for tasks). See card_mounting.go.
	builtinTaskID             = builtinCardPrefix + "task__"
	builtinBacklogID          = builtinCardPrefix + "backlog__"
	builtinTodoID             = builtinCardPrefix + "todo__"
	builtinDoingID            = builtinCardPrefix + "doing__"
	builtinDoneID             = builtinCardPrefix + "done__"
	builtinBlockedID          = builtinCardPrefix + "blocked__"
	builtinCancelledID        = builtinCardPrefix + "cancelled__"
	builtinFailedID           = builtinCardPrefix + "failed__"
	builtinPendingReviewID    = builtinCardPrefix + "pending_review__"
	builtinConceptID          = builtinCardPrefix + "concept__"
	builtinCallableID         = builtinCardPrefix + "callable__"
	builtinCapabilityModuleID = builtinCardPrefix + "capability_module__"
	builtinSchedulerID        = builtinCardPrefix + "scheduler__"
	builtinBundleID           = builtinCardPrefix + "bundle__"
	builtinMapID              = builtinCardPrefix + "workflow__"
)

// tocID is the special root card. It is intentionally NOT prefixed with
// __builtin_ so it appears as an ordinary top-level card in the wiki, while
// still being protected from deletion.
const tocID = "toc"

// BuiltinCard is the common abstraction for system-provided mono cards.
// Specific built-in cards are defined as instances below and registered in
// builtinCards. This registry is the single source of truth for cards that are
// auto-created and protected from deletion. The card id is both its identity
// and display field; localized display names live in the frontend.
type BuiltinCard struct {
	Title  string
	Tags   []string
	List   []string
	Data   map[string]any
	Parent string
	Body   string
}

// ToCardRecord converts the abstract built-in card into the concrete storage
// representation used by CardStore.
func (b BuiltinCard) ToCardRecord() *CardRecord {
	return &CardRecord{
		Title:    b.Title,
		Tags:     b.Tags,
		List:     b.List,
		Parent:   b.Parent,
		Data:     builtinCardData(b),
		Body:     b.Body,
		Raw:      formatBuiltinCard(b),
		Created:  "1970-01-01T00:00:00Z",
		Modified: "1970-01-01T00:00:00Z",
	}
}

func builtinCardData(b BuiltinCard) map[string]any {
	data := make(map[string]any, len(b.Data))
	for key, value := range b.Data {
		data[key] = value
	}
	return data
}

// formatBuiltinCard renders the YAML frontmatter and body for a built-in card.
func formatBuiltinCard(b BuiltinCard) string {
	tags := "[]"
	if len(b.Tags) > 0 {
		tags = "[" + strings.Join(b.Tags, ", ") + "]"
	}
	list := "[]"
	if len(b.List) > 0 {
		list = "[" + strings.Join(b.List, ", ") + "]"
	}
	raw := fmt.Sprintf(
		"---\nid: %s\n", b.Title,
	)
	// Emit top-level type: from Data["type"] when present so frontmatter
	// type is the single authority for card kind.
	if t, ok := b.Data["type"].(string); ok && t != "" {
		raw += "type: " + t + "\n"
	}
	raw += fmt.Sprintf(
		"tags: %s\nlist: %s\ncreated: \"1970-01-01T00:00:00Z\"\nmodified: \"1970-01-01T00:00:00Z\"\n",
		tags, list,
	)
	if b.Parent != "" {
		raw += "parent: " + b.Parent + "\n"
	}
	if len(b.Data) > 0 {
		raw += "data:\n"
		keys := make([]string, 0, len(b.Data))
		for k := range b.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			raw += fmt.Sprintf("  %s:%s\n", key, formatBuiltinDataValue(b.Data[key], "    "))
		}
	}
	return raw + "---\n\n" + b.Body
}

// formatBuiltinDataValue renders a data value for a built-in card frontmatter.
// Nested maps are emitted as indented YAML; scalar values use their Go default
// string form.
func formatBuiltinDataValue(value any, indent string) string {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			return " {}"
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			sb.WriteString("\n")
			sb.WriteString(indent)
			sb.WriteString(k)
			sb.WriteString(":")
			sb.WriteString(formatBuiltinDataValue(v[k], indent+"  "))
		}
		return sb.String()
	case bool:
		if v {
			return " true"
		}
		return " false"
	case string:
		return " " + v
	default:
		return " " + fmt.Sprintf("%v", v)
	}
}

func promptBuiltinCards() []*BuiltinCard {
	promptCards, err := agentkit.BuiltinPromptCards()
	if err != nil {
		panic(err)
	}
	cards := make([]*BuiltinCard, 0, len(promptCards))
	for _, prompt := range promptCards {
		tags := []string{"component", "prompt", "internal"}
		if strings.HasPrefix(prompt.Title, "prompt:profile:") {
			tags = []string{"component", "prompt", "profile"}
		} else if strings.HasPrefix(prompt.Title, "prompt:fragment:") {
			tags = []string{"component", "prompt", "fragment"}
		}
		cards = append(cards, &BuiltinCard{
			Title: prompt.Title, Tags: tags, Body: prompt.Body,
			Data: map[string]any{"type": "prompt", "componentKind": "prompt", "source": "builtin", "storage": "cardstore", "visibility": "component", "scope": prompt.Scope, "role": prompt.Role, "protected": true, "editable": false, "deletable": false, "priority": prompt.Priority},
		})
	}
	return cards
}

// builtinCards is the registry of all system-provided mono cards.
// Adding a new entry here automatically creates it in every project on actor
// init or via the project.wiki.ensure_builtins upgrade callable.
func init() {
	// Inject mount-rule data into named builtin cards (skill, prompt) so the
	// frontend can derive their mount rules; builtinMountSpecs stays the single
	// source of truth.
	applyMountDataToNamedBuiltins()
	// Register type-driven virtual mount nodes alongside the named builtins.
	builtinCards = append(builtinCards, virtualMountBuiltinCards()...)
}

// seedBuiltinCards creates any system cards that don't yet exist, removes
// obsolete builtin cards, and strips orphaned __builtin_* tags from user cards.
// Returns the IDs of cards that were newly created.
func (a *Actor) seedBuiltinCards() []string {
	var created []string
	// Legacy project-info builtins were migrated to regular cards; delete the
	// old protected files alongside other obsolete builtins.
	obsolete := append(append([]string{}, obsoleteBuiltinIDs...), legacyProjectInfoIDs...)
	for _, id := range obsolete {
		if err := a.store.Delete(id); err != nil {
			// Ignore not-found errors; card may already be gone.
		}
	}
	a.migrateStripBuiltinTags()
	for _, b := range builtinCards {
		existing, err := a.store.Get(b.Title)
		if err != nil {
			a.store.Save(b.ToCardRecord())
			created = append(created, b.Title)
			continue
		}
		// Builtin cards are protected (non-editable), so resync canonical
		// content (title/tags/body) from the registry when it drifts — e.g.
		// aggregator bodies were emptied in a later release. Preserve the
		// List field so any linked children survive.
		canonical := b.ToCardRecord()
		if existing.Raw == canonical.Raw {
			continue
		}
		canonical.List = existing.List
		a.store.Save(canonical)
	}
	return created
}

// migrateStripBuiltinTags removes any __builtin_* tags from all stored cards.
// Such tags are reserved for system virtual nodes and must not appear on user
// cards. The migration runs once per startup; cards without such tags are
// left untouched.
func (a *Actor) migrateStripBuiltinTags() {
	cards, err := a.store.List()
	if err != nil {
		return
	}
	for _, card := range cards {
		if isSystemCardID(card.Title) {
			continue
		}
		if !containsBuiltinTag(card.Tags) {
			continue
		}
		if updated := stripBuiltinTagsFromRaw(card.Raw); updated != card.Raw {
			card.Raw = updated
			_ = a.store.Save(card)
		}
	}
}

// containsBuiltinTag reports whether any tag starts with the builtin prefix.
func containsBuiltinTag(tags []string) bool {
	for _, tag := range tags {
		if strings.HasPrefix(tag, builtinCardPrefix) || tag == "builtin" {
			return true
		}
	}
	return false
}

// stripBuiltinTagsFromRaw rewrites the tags: line in raw frontmatter,
// removing any __builtin_* or "builtin" entries. If all tags are removed the
// line becomes "tags: []".
func stripBuiltinTagsFromRaw(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) != "tags" {
			continue
		}
		tags := parseTags(strings.TrimSpace(value))
		filtered := tags[:0]
		for _, tag := range tags {
			if strings.HasPrefix(tag, builtinCardPrefix) || tag == "builtin" {
				continue
			}
			filtered = append(filtered, tag)
		}
		lines[i] = "tags: [" + strings.Join(filtered, ", ") + "]"
	}
	return raw[:3] + strings.Join(lines, "\n") + raw[3+end:]
}

func (a *Actor) seedPromptCards() {
	for _, id := range obsoletePromptCardIDs {
		_ = a.store.Delete(id)
	}
	for _, card := range promptBuiltinCards() {
		_ = a.store.Save(card.ToCardRecord())
	}
}

// obsoletePromptCardIDs lists builtin prompt cards (profiles/fragments) that
// are no longer embedded and should be removed from existing projects.
// removeSystemCards removes system-owned cards (prompt profiles/fragments and
// builtin skill cards) from non-system projects.
//
// Two cases are covered:
//   - protected cards (prompt:* / skill:* with a builtin marker) are deleted;
//   - builtin skill residues whose protected marker was stripped by the legacy
//     SkillEditor migration are matched against the embed builtin skill id set
//     and deleted too.
//
// Non-builtin, user-authored cards (including project-level skills whose id is
// not an embed builtin) are always preserved.
func (a *Actor) removeSystemCards() {
	builtinSkillIDs := make(map[string]struct{}, 8)
	for _, c := range builtinSkillCards() {
		builtinSkillIDs[c.Title] = struct{}{}
	}
	cards, err := a.store.List()
	if err != nil {
		return
	}
	for _, card := range cards {
		if !isSystemOwnedCardTitle(card.Title) {
			continue
		}
		_, _, _, _, protected, _, _ := cardMetadata(card)
		if protected {
			_ = a.store.Delete(card.Title)
			continue
		}
		if _, isBuiltin := builtinSkillIDs[card.Title]; isBuiltin {
			_ = a.store.Delete(card.Title)
		}
	}
}

func isSystemOwnedCardTitle(id string) bool {
	return strings.HasPrefix(id, "prompt:") || strings.HasPrefix(id, "skill:")
}

func (a *Actor) seedBuiltinSkillCards() {
	for _, card := range builtinSkillCards() {
		canonical := card.ToCardRecord()
		if existing, err := a.store.Get(card.Title); err == nil {
			// Builtin skills are non-editable; resync canonical content when it
			// drifts, preserving any linked children. This also closes the gap
			// where Get succeeded via the legacy "skill$" fallback: without
			// resync the Delete below would erase the only on-disk copy and the
			// skill would silently vanish from list_all_cards.
			if existing.Raw != canonical.Raw {
				canonical.List = existing.List
				_ = a.store.Save(canonical)
			}
		} else {
			_ = a.store.Save(canonical)
		}
		_ = a.store.Delete(strings.Replace(card.Title, "skill:", "skill$", 1))
	}
}

func builtinSkillCards() []*BuiltinCard {
	assets, err := agentkit.LoadSkillAssets()
	if err != nil {
		panic(err)
	}
	cards := make([]*BuiltinCard, 0, len(assets))
	for _, asset := range assets {
		cards = append(cards, builtinSkillCard(asset.Title, asset.Raw))
	}
	return cards
}

func builtinSkillCard(id, raw string) *BuiltinCard {
	title := "skill:" + id
	description := ""
	body := raw
	if strings.HasPrefix(raw, "---") {
		end := strings.Index(raw[3:], "---")
		if end >= 0 {
			for _, line := range strings.Split(raw[3:3+end], "\n") {
				key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), "[]\"")
				switch strings.TrimSpace(key) {
				case "description":
					description = value
				}
			}
		}
	}
	return &BuiltinCard{
		Title: title, Tags: []string{"component", "skill"}, Body: body,
		Data: map[string]any{"type": "skill", "componentKind": "skill", "source": "builtin", "storage": "cardstore", "visibility": "component", "builtinKey": id, "description": description, "protected": true, "editable": false, "deletable": false},
	}
}

// enforceBuiltinTags ensures the raw markdown of a built-in card keeps its
// canonical tags. Built-in card tags are immutable; users may edit body/title
// but not tags.
func enforceBuiltinTags(id, raw string) string {
	for _, b := range builtinCards {
		if b.Title != id {
			continue
		}
		tags := "[]"
		if len(b.Tags) > 0 {
			tags = "[" + strings.Join(b.Tags, ", ") + "]"
		}
		re := regexp.MustCompile(`(?m)^tags:\s*\[[^\]]*\]$`)
		if re.MatchString(raw) {
			return re.ReplaceAllString(raw, "tags: "+tags)
		}
		// Tags line missing: insert after the first line (frontmatter delimiter).
		if idx := strings.Index(raw, "\n"); idx != -1 {
			return raw[:idx+1] + "tags: " + tags + "\n" + raw[idx+1:]
		}
		return raw
	}
	return raw
}

// filterBuiltinVirtualCards drops __builtin_*__ virtual mount nodes from the
// flat list. These nodes carry no content (empty body, no tags) and serve
// only as type-driven mount targets for the tree renderer (list_cards) and
// the frontend topology graph, which opts in via IncludeBuiltin=true. The
// "builtin:" prefix (external component cards like builtin:bundle:*) is
// intentionally NOT matched — those are real component descriptors.
func filterBuiltinVirtualCards(items []domain.MonoCardListItem) []domain.MonoCardListItem {
	out := items[:0:0]
	for _, c := range items {
		if isVirtualMountNode(c.ID) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// filterAgentInstanceCards drops workspace agent instance cards (agent:*,
// synthesized by the agent external provider) from the flat list. They are
// live topology nodes, not wiki content: the frontend topology graph opts in
// via IncludeBuiltin=true, and callers that actually want them filter with
// Type "agent" or Source "workspace". Persisted cards of type "agent" are
// unaffected — only external-provider rows are dropped.
func filterAgentInstanceCards(items []domain.MonoCardListItem) []domain.MonoCardListItem {
	out := items[:0:0]
	for _, c := range items {
		if c.Type == "agent" && c.Storage == "external" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// filterMcpExternalCards drops MCP server bundle cards (mcp:*) from the card
// listing. They are per-agent mount markers derived from mcpmanager config,
// not wiki content: their Modified timestamp is regenerated to now on every
// list call, so with the default -modified sort they always surface at the
// top. IncludeBuiltin=true (topology graph) keeps them so the component
// catalog and topology view still discover MCP bundles.
func filterMcpExternalCards(items []domain.MonoCardListItem) []domain.MonoCardListItem {
	out := items[:0:0]
	for _, c := range items {
		if strings.HasPrefix(c.ID, mcpExternalPrefix) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// stripListCardBodies implements the metadata-only contract shared by list and
// search endpoints: drop the full Raw body and leave only lightweight metadata
// in Data, so callers can summarize cards without fetching each body.
//
// In addition to description/visual, the builtin mount-rule keys
// (builtinRole/mountType/mountStatus/autoMount) are preserved: the frontend
// derives the type-driven mount table from these at runtime (the backend
// registry is the single source of truth), so dropping them would orphan every
// task card from its status node in the topology graph.
func stripListCardBodies(items []domain.MonoCardListItem) {
	for i := range items {
		desc := ""
		if d, ok := items[i].Data["description"].(string); ok && d != "" {
			desc = d
		}
		if desc == "" {
			desc = parseFrontmatterString(items[i].Raw, "description")
		}
		visual, hasVisual := items[i].Data["visual"]
		data := map[string]any{}
		if desc != "" {
			data["description"] = desc
		}
		if hasVisual {
			data["visual"] = visual
		}
		// Preserve builtin mount-rule structural keys so the frontend can derive
		// the mount table from the cards (data is the single source of truth).
		for _, k := range builtinMountDataKeys {
			if v, ok := items[i].Data[k]; ok {
				data[k] = v
			}
		}
		// Preserve componentKind so the frontend omnibox can filter bundle/mode
		// component cards for the mount picker. Builtin component providers set
		// this key; without it every builtin component card is dropped from the
		// /-command list. icon/title are carried so the omnibox can render the
		// card's own badge glyph and friendly name without the raw body.
		for _, k := range []string{"componentKind", "icon", "title", "tools", "requires", "settingsVisible", "settingsProtected", "modeManaged", "lifecycleManaged", "flow", "devOnly", "depends_on", "scope", "ownerAgentId", "Destination", "workflow_template", "category"} {
			if v, ok := items[i].Data[k]; ok {
				data[k] = v
			}
		}
		// Template/instance provenance keys: the workflow view's category bands
		// (template, planned) and the topology context-menu guards read these
		// from list items. Without them every classification that depends on
		// template/instance identity survives only until the next metadata-only
		// reload (the card save response carries full data, the list does not),
		// so templates appear to vanish after an app restart.
		for _, k := range []string{"template", "instance_of", "scheduler_card_id", "source_map"} {
			if v, ok := items[i].Data[k]; ok {
				data[k] = v
			}
		}
		// Scheduler card config keys: the Scheduled view and the schedule
		// modal read these from list items to render the cron summary, bound
		// agent, and task-mode badges without a per-card detail fetch. They
		// survive the create/update save response (full data) but vanish on the
		// next metadata-only list reload; with the schedule gone the cron
		// editor re-seeds from an empty cron and defaults to "0 9 * * *"
		// (mono-store createSchedulerCard default), and the bound agent reads
		// as unbound. These keys only appear on scheduler cards, so preserving
		// them unconditionally cannot leak into builtin/template/instance cards.
		// executor is intentionally left stripped (legacy non-essential field).
		for _, k := range []string{"schedule", "schedule_type", "task_mode", "bind_mode", "bound_agent", "agent_kind", "model_slots", "agent_actions", "agent_action", "target_agent"} {
			if v, ok := items[i].Data[k]; ok {
				data[k] = v
			}
		}
		items[i].Data = data
		if len(data) == 0 {
			items[i].Data = nil
		}
		items[i].Raw = ""
	}
}

// builtinMountDataKeys are the lightweight scalar keys a builtin virtual node
// carries in its Data block to describe its mount rule. stripListCardBodies
// keeps them so the frontend can derive mount routing without the Raw body.
const defaultWikiSearchLimit = 50

// compileWikiTitleQuery parses a title query. A value of the form
// /pattern/flags compiles to a regexp (only i/m/s flags are honoured). An
// invalid regexp yields invalidRegexp=true meaning "matches nothing". Any other
// (or empty) value is a plain case-insensitive substring match.
func compileWikiTitleQuery(query string) (re *regexp.Regexp, isRegexp, invalidRegexp bool) {
	m := wikiTitleQueryRe.FindStringSubmatch(query)
	if m == nil {
		return nil, false, false
	}
	prefix := ""
	for _, f := range m[2] {
		switch f {
		case 'i':
			prefix += "(?i)"
		case 'm':
			prefix += "(?m)"
		case 's':
			prefix += "(?s)"
		}
	}
	compiled, err := regexp.Compile(prefix + m[1])
	if err != nil {
		return nil, true, true
	}
	return compiled, true, false
}

func parseWikiSearchTime(s string) (time.Time, error) {
	return timeutil.ParseFlexible(s)
}

// cardHasAllTags reports whether every requested tag is present in have
// (case-insensitive). Used so multi-tag filters narrow (AND) rather than widen.
func cardHasAllTags(have, want []string) bool {
	haveLower := make(map[string]bool, len(have))
	for _, t := range have {
		haveLower[strings.ToLower(t)] = true
	}
	for _, w := range want {
		if !haveLower[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// cardTitleMatches applies the title/id query (case-insensitive substring, or
// /regexp/) used by search_cards. An empty query matches everything; an invalid
// regexp matches nothing. search_card_content ignores this and matches the body.
func cardTitleMatches(card domain.MonoCardListItem, query string, titleRe *regexp.Regexp, isRegexp, invalidRegexp bool) bool {
	if query == "" {
		return true
	}
	if invalidRegexp {
		return false
	}
	if isRegexp {
		return titleRe.MatchString(card.ID)
	}
	return strings.Contains(strings.ToLower(card.ID), strings.ToLower(query))
}

// wikiCardFilterInput carries the shared metadata-filter field values that both
// search_cards and search_card_content accept. Each handler populates it from
// its (identical) request field set.
type wikiCardFilterInput struct {
	Tags                          []string
	Status, Type, Source          string
	List, Parent, Priority, Due   string
	ModifiedAfter, ModifiedBefore string
	CreatedAfter, CreatedBefore   string
	DueAfter, DueBefore           string
}

// wikiCardFilter is the parsed, validated form of wikiCardFilterInput: time
// bounds become millis with active flags; scalar fields keep their raw form for
// case-insensitive comparison.
type wikiCardFilter struct {
	tags                              []string
	status, cardType, source          string
	list, parent, priority, due       string
	modAfterMs, modBeforeMs           int64
	hasModAfter, hasModBefore         bool
	createdAfterMs, createdBeforeMs   int64
	hasCreatedAfter, hasCreatedBefore bool
	dueAfterMs, dueBeforeMs           int64
	hasDueAfter, hasDueBefore         bool
}

// parseWikiCardFilter parses raw filter inputs. Empty time bounds are inactive;
// a malformed RFC3339 bound is an error.
func parseWikiCardFilter(in wikiCardFilterInput) (wikiCardFilter, error) {
	f := wikiCardFilter{
		tags: in.Tags, status: in.Status, cardType: in.Type, source: in.Source,
		list: in.List, parent: in.Parent, priority: in.Priority, due: in.Due,
	}
	var err error
	if f.modAfterMs, f.hasModAfter, err = parseFilterTimeBound(in.ModifiedAfter); err != nil {
		return f, fmt.Errorf("invalid ModifiedAfter: %w", err)
	}
	if f.modBeforeMs, f.hasModBefore, err = parseFilterTimeBound(in.ModifiedBefore); err != nil {
		return f, fmt.Errorf("invalid ModifiedBefore: %w", err)
	}
	if f.createdAfterMs, f.hasCreatedAfter, err = parseFilterTimeBound(in.CreatedAfter); err != nil {
		return f, fmt.Errorf("invalid CreatedAfter: %w", err)
	}
	if f.createdBeforeMs, f.hasCreatedBefore, err = parseFilterTimeBound(in.CreatedBefore); err != nil {
		return f, fmt.Errorf("invalid CreatedBefore: %w", err)
	}
	if f.dueAfterMs, f.hasDueAfter, err = parseFilterTimeBound(in.DueAfter); err != nil {
		return f, fmt.Errorf("invalid DueAfter: %w", err)
	}
	if f.dueBeforeMs, f.hasDueBefore, err = parseFilterTimeBound(in.DueBefore); err != nil {
		return f, fmt.Errorf("invalid DueBefore: %w", err)
	}
	return f, nil
}

// parseFilterTimeBound parses an optional RFC3339 bound. Returns (millis,
// active, error); an empty string is inactive.
func parseFilterTimeBound(s string) (int64, bool, error) {
	if s == "" {
		return 0, false, nil
	}
	t, err := parseWikiSearchTime(s)
	if err != nil {
		return 0, false, err
	}
	return t.UnixMilli(), true, nil
}

// cardHasAnyString reports whether have contains want (case-insensitive). Used
// for the List filter, since a card may belong to multiple lists.
func cardHasAnyString(have []string, want string) bool {
	w := strings.ToLower(want)
	for _, h := range have {
		if strings.ToLower(h) == w {
			return true
		}
	}
	return false
}

// withinTimeWindow applies an inclusive [after, before] millis window to a card
// timestamp. Inactive bounds pass; an active bound on an empty/unparseable
// timestamp excludes the card.
func withinTimeWindow(stamp string, afterMs, beforeMs int64, hasAfter, hasBefore bool) bool {
	if !hasAfter && !hasBefore {
		return true
	}
	if stamp == "" {
		return false
	}
	t, err := parseWikiSearchTime(stamp)
	if err != nil {
		return false
	}
	ms := t.UnixMilli()
	if hasAfter && ms < afterMs {
		return false
	}
	if hasBefore && ms > beforeMs {
		return false
	}
	return true
}

// matches reports whether card satisfies every active metadata filter. Scalars
// compare case-insensitively; tags use AND; time windows are inclusive.
func (f wikiCardFilter) matches(card domain.MonoCardListItem) bool {
	if f.status != "" && !strings.EqualFold(card.Status, f.status) {
		return false
	}
	if f.cardType != "" && !strings.EqualFold(card.Type, f.cardType) {
		return false
	}
	if f.source != "" && !strings.EqualFold(card.Source, f.source) {
		return false
	}
	if f.list != "" && !cardHasAnyString(card.List, f.list) {
		return false
	}
	if f.parent != "" && !strings.EqualFold(card.Parent, f.parent) {
		return false
	}
	if f.priority != "" && !strings.EqualFold(card.Priority, f.priority) {
		return false
	}
	if f.due != "" && !strings.EqualFold(card.Due, f.due) {
		return false
	}
	if len(f.tags) > 0 && !cardHasAllTags(card.Tags, f.tags) {
		return false
	}
	if !withinTimeWindow(card.Modified, f.modAfterMs, f.modBeforeMs, f.hasModAfter, f.hasModBefore) {
		return false
	}
	if !withinTimeWindow(card.Created, f.createdAfterMs, f.createdBeforeMs, f.hasCreatedAfter, f.hasCreatedBefore) {
		return false
	}
	if !withinTimeWindow(card.Due, f.dueAfterMs, f.dueBeforeMs, f.hasDueAfter, f.hasDueBefore) {
		return false
	}
	return true
}

// parseOrderBy splits an OrderBy spec into a lowercased field and a descending
// flag. A leading "-" means descending. Empty input yields an empty field
// (no reordering).
func parseOrderBy(orderBy string) (field string, desc bool) {
	s := strings.TrimSpace(orderBy)
	if strings.HasPrefix(s, "-") {
		return strings.ToLower(strings.TrimSpace(s[1:])), true
	}
	return strings.ToLower(s), false
}

// priorityRank maps known priority labels to a sort rank (smaller = higher
// priority); unknown labels rank last. Used only by OrderBy=priority.
func priorityValue(s string) int {
	if r, ok := priorityRank[strings.ToLower(s)]; ok {
		return r
	}
	return 3
}

func cardTimeMillis(stamp string) (int64, bool) {
	if stamp == "" {
		return 0, false
	}
	t, err := parseWikiSearchTime(stamp)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// compareCardTimeDir orders two cards by their primary timestamps, falling
// back to the secondary stamp when the primary is empty/unparseable. Cards
// with no parseable stamp at all sort last in BOTH directions — the
// descending flip only applies between two parseable times, otherwise
// "-modified" would surface timestamp-less cards at the top.
func compareCardTimeDir(aStamp, aFallback, bStamp, bFallback string, desc bool) int {
	a, aok := cardTimeMillis(aStamp)
	if !aok {
		a, aok = cardTimeMillis(aFallback)
	}
	b, bok := cardTimeMillis(bStamp)
	if !bok {
		b, bok = cardTimeMillis(bFallback)
	}
	if !aok && !bok {
		return 0
	}
	if !aok {
		return 1
	}
	if !bok {
		return -1
	}
	cmp := 0
	switch {
	case a < b:
		cmp = -1
	case a > b:
		cmp = 1
	}
	if desc {
		cmp = -cmp
	}
	return cmp
}

// wikiItemCompare orders two cards by the given OrderBy field and direction
// (returns negative when a ranks first). Supported: modified, created, due
// (by time, missing stamps last in both directions), title/id (lexical),
// priority (by rank then id). Unknown fields return 0 (no defined order).
func wikiItemCompare(a, b domain.MonoCardListItem, field string, desc bool) int {
	cmp := 0
	switch field {
	case "modified":
		return compareCardTimeDir(a.Modified, a.Created, b.Modified, b.Created, desc)
	case "created":
		return compareCardTimeDir(a.Created, a.Modified, b.Created, b.Modified, desc)
	case "due":
		return compareCardTimeDir(a.Due, "", b.Due, "", desc)
	case "title", "id":
		cmp = strings.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID))
	case "priority":
		cmp = priorityValue(a.Priority) - priorityValue(b.Priority)
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID))
		}
	default:
		return 0
	}
	if desc {
		cmp = -cmp
	}
	return cmp
}

// sortWikiCardItems reorders items in place by OrderBy. An empty or unrecognized
// field leaves the natural order untouched. Equal elements keep their relative
// order (stable).
func sortWikiCardItems(items []domain.MonoCardListItem, orderBy string) {
	field, desc := parseOrderBy(orderBy)
	if field == "" {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return wikiItemCompare(items[i], items[j], field, desc) < 0
	})
}

// sortWikiCardContentMatches reorders content matches by their embedded card,
// using the same OrderBy rules as sortWikiCardItems.
func sortWikiCardContentMatches(matches []domain.WikiCardContentMatch, orderBy string) {
	field, desc := parseOrderBy(orderBy)
	if field == "" {
		return
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return wikiItemCompare(matches[i].Card, matches[j].Card, field, desc) < 0
	})
}

func (a *Actor) handleWikiSearchCardContent(ctx actor.PureContext, req domain.WikiSearchCardContentReq) (domain.WikiSearchCardContentResp, error) {
	if req.Query == "" {
		return domain.WikiSearchCardContentResp{}, fmt.Errorf("project.wiki.searchCardContent: query is required")
	}
	cards, err := a.store.List()
	if err != nil {
		return domain.WikiSearchCardContentResp{}, fmt.Errorf("project.wiki.searchCardContent: %w", err)
	}

	filter, err := parseWikiCardFilter(wikiCardFilterInput{
		Tags: req.Tags, Status: req.Status, Type: req.Type, Source: req.Source,
		List: req.List, Parent: req.Parent, Priority: req.Priority, Due: req.Due,
		ModifiedAfter: req.ModifiedAfter, ModifiedBefore: req.ModifiedBefore,
		CreatedAfter: req.CreatedAfter, CreatedBefore: req.CreatedBefore,
		DueAfter: req.DueAfter, DueBefore: req.DueBefore,
	})
	if err != nil {
		return domain.WikiSearchCardContentResp{}, fmt.Errorf("project.wiki.searchCardContent: %w", err)
	}

	bodyRe, isRegexp, invalidRegexp := compileWikiTitleQuery(req.Query)

	matched := make([]domain.WikiCardContentMatch, 0, len(cards))
	for _, card := range cards {
		item := cardToListItem(card)
		if item.Storage == "runtime" || item.Visibility == "runtime" {
			continue
		}
		if !filter.matches(item) {
			continue
		}
		body := card.Body
		if body == "" {
			// No parsed body (e.g. no frontmatter separator); fall back to raw.
			body = card.Raw
		}
		line, snippet, ok := matchCardBody(body, req.Query, bodyRe, isRegexp, invalidRegexp)
		if !ok {
			continue
		}
		matched = append(matched, domain.WikiCardContentMatch{
			Card:    item,
			Line:    line,
			Snippet: snippet,
		})
	}

	total := int32(len(matched))
	sortWikiCardContentMatches(matched, req.OrderBy)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = defaultWikiSearchLimit
	}
	if int(total) > limit {
		matched = matched[:limit]
	}

	// Strip card bodies from the returned list items (metadata + snippet only).
	items := make([]domain.MonoCardListItem, len(matched))
	for i := range matched {
		items[i] = matched[i].Card
	}
	stripListCardBodies(items)
	for i := range matched {
		matched[i].Card = items[i]
	}

	return domain.WikiSearchCardContentResp{Matches: matched, Total: total}, nil
}

// matchCardBody reports whether query matches body and returns the 1-based line
// number and a snippet of the first match. queryMode mirrors compileWikiTitleQuery:
// a /pattern/flags value is a regexp; any other value is a case-insensitive
// substring match. The snippet is the matching line trimmed to a bounded window.
func matchCardBody(body, query string, re *regexp.Regexp, isRegexp, invalidRegexp bool) (line int32, snippet string, ok bool) {
	if invalidRegexp {
		return 0, "", false
	}
	var loc []int
	if isRegexp {
		loc = re.FindStringIndex(body)
	} else {
		q := strings.ToLower(query)
		idx := strings.Index(strings.ToLower(body), q)
		if idx >= 0 {
			loc = []int{idx, idx + len(q)}
		}
	}
	if loc == nil {
		return 0, "", false
	}
	line = 1 + int32(strings.Count(body[:loc[0]], "\n"))
	lineStart := loc[0]
	if i := strings.LastIndex(body[:loc[0]], "\n"); i >= 0 {
		lineStart = i + 1
	}
	lineEnd := strings.Index(body[loc[0]:], "\n")
	if lineEnd < 0 {
		lineEnd = len(body) - loc[0]
	}
	raw := body[lineStart : loc[0]+lineEnd]
	const maxSnippet = 160
	s := strings.TrimSpace(raw)
	if rs := []rune(s); len(rs) > maxSnippet {
		s = string(rs[:maxSnippet])
	}
	return line, s, true
}

// defaultListCardLimit caps the number of rendered nodes when the caller
// omits Limit. This prevents a large wiki from dumping hundreds of card rows
// into an agent's context in a single call.
const defaultListCardLimit = 200

// defaultListCardOrderBy is the sort applied to siblings when the caller
// omits OrderBy. "-modified" = newest-first by modified (falling back to
// created), so the most recently touched cards surface at the top.
const defaultListCardOrderBy = "-modified"

func (a *Actor) handleWikiListCards(ctx actor.PureContext, req domain.WikiListCardsReq) (domain.WikiListCardsResp, error) {
	items, err := a.listAllCardItems(ctx)
	if err != nil {
		return domain.WikiListCardsResp{}, fmt.Errorf("project.wiki.listCards: %w", err)
	}

	orderBy := strings.TrimSpace(req.OrderBy)
	if orderBy == "" {
		orderBy = defaultListCardOrderBy
	}

	// MCP external cards (mcp:*) are per-agent mount markers, not wiki
	// content. Exclude them from the default listing; IncludeBuiltin=true
	// (topology graph) keeps them so the component catalog still works.
	if !req.IncludeBuiltin {
		items = filterMcpExternalCards(items)
	}

	if req.Flat {
		return a.handleWikiListCardsFlat(ctx, req, items, orderBy)
	}

	// Tree mode
	rootID := req.RootID
	if rootID == "" {
		rootID = tocID
	}
	depth := int(req.Depth)
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultListCardLimit
	}
	resp := domain.WikiListCardsResp{
		Tree:  renderTreeFromRoot(items, rootID, depth, limit, orderBy),
		Nodes: buildTreeNodes(items, rootID, depth, limit, orderBy),
	}
	return resp, nil
}

func (a *Actor) handleWikiListCardsFlat(ctx actor.PureContext, req domain.WikiListCardsReq, items []domain.MonoCardListItem, orderBy string) (domain.WikiListCardsResp, error) {
	if !req.IncludeRaw {
		stripListCardBodies(items)
	}
	if !req.IncludeBuiltin {
		items = filterBuiltinVirtualCards(items)
	}
	// Agent instance cards (agent:*) are excluded by default: they are
	// workspace runtime nodes, not wiki cards. IncludeBuiltin=true (topology
	// graph) keeps them, and explicit Type/Source filters still reach them.
	if !req.IncludeBuiltin && !strings.EqualFold(req.Type, "agent") && !strings.EqualFold(req.Source, "workspace") {
		items = filterAgentInstanceCards(items)
	}
	items = filterWorkflowFoldVisible(items, req.WorkflowFilter, time.Now())

	// Search/filter (same logic as the former search_cards handler)
	filter, err := parseWikiCardFilter(wikiCardFilterInput{
		Tags: req.Tags, Status: req.Status, Type: req.Type, Source: req.Source,
		List: req.List, Parent: req.Parent, Priority: req.Priority, Due: req.Due,
		ModifiedAfter: req.ModifiedAfter, ModifiedBefore: req.ModifiedBefore,
		CreatedAfter: req.CreatedAfter, CreatedBefore: req.CreatedBefore,
		DueAfter: req.DueAfter, DueBefore: req.DueBefore,
	})
	if err != nil {
		return domain.WikiListCardsResp{}, fmt.Errorf("project.wiki.listCards: %w", err)
	}

	titleRe, isRegexp, invalidRegexp := compileWikiTitleQuery(req.Query)

	matched := make([]domain.MonoCardListItem, 0, len(items))
	for _, card := range items {
		if cardTitleMatches(card, req.Query, titleRe, isRegexp, invalidRegexp) && filter.matches(card) {
			matched = append(matched, card)
		}
	}

	total := int32(len(matched))
	sortWikiCardItems(matched, orderBy)
	// Limit: -1 returns every matching card (the former list_all_cards
	// contract — the topology/workflow graphs, sidebar and agent manifest
	// collect rely on it). An omitted/zero Limit keeps the default search cap.
	limit := int(req.Limit)
	if limit == 0 {
		limit = defaultWikiSearchLimit
	}
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	if !req.Total {
		total = 0
	}
	return domain.WikiListCardsResp{Cards: matched, Total: total}, nil
}

func (a *Actor) handleWikiGetCardHierarchy(ctx actor.PureContext, req domain.WikiGetCardHierarchyReq) (domain.WikiGetCardHierarchyResp, error) {
	items, err := a.listAllCardItems(ctx)
	if err != nil {
		return domain.WikiGetCardHierarchyResp{}, fmt.Errorf("project.wiki.getCardHierarchy: %w", err)
	}
	level := req.Level
	if level <= 0 {
		level = 2
	}
	return domain.WikiGetCardHierarchyResp{
		Tree: renderCardHierarchy(items, req.ID, int(level)),
	}, nil
}

// listAllCardItems returns canonical persisted and external cards. Runtime cards
// are intentionally excluded because they are only valid during turn compilation.
func (a *Actor) listAllCardItems(ctx actor.PureContext) ([]domain.MonoCardListItem, error) {
	cards, err := a.store.List()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.MonoCardListItem, len(cards))
	order := make([]string, 0, len(cards))
	for _, card := range cards {
		item := cardToListItem(card)
		if item.Storage == "runtime" || item.Visibility == "runtime" {
			continue
		}
		byID[item.ID] = item
		order = append(order, item.ID)
	}
	for _, provider := range a.externalProviders {
		externalItems, err := provider.List(ctx)
		if err != nil {
			ctx.Logger().Warn("project.wiki.listAllCards: external provider failed", "error", err)
			continue
		}
		for i := range externalItems {
			item := &externalItems[i]
			item.Tags = dedupStrings(item.Tags)
			normalizeExternalCardMetadata(item)
			if item.Storage == "runtime" || item.Visibility == "runtime" {
				continue
			}
			if _, exists := byID[item.ID]; exists {
				continue
			}
			byID[item.ID] = *item
			order = append(order, item.ID)
		}
	}
	items := make([]domain.MonoCardListItem, 0, len(order))
	for _, id := range order {
		items = append(items, byID[id])
	}
	return items, nil
}

// cardDisplayTitle returns the human-readable title for rendering. Builtin
// cards carry a display name in Data["builtin_title"]; everything else falls
// back to the card ID (routing key).
func cardDisplayTitle(card domain.MonoCardListItem) string {
	if title, ok := card.Data["builtin_title"].(string); ok && title != "" {
		return title
	}
	return card.ID
}

// formatTreeNode renders a tree node label, collapsing the redundant
// "id (id)" case (when the display title equals the ID) to just the ID.
func formatTreeNode(label, id string) string {
	if label == id {
		return id
	}
	return label + " (" + id + ")"
}

// formatTreeCardLine renders the inline metadata for a tree node:
//
//	title (id) [type] [status] @modified
//
// type and status are omitted when empty. modified is shortened to the date
// portion (YYYY-MM-DD) so the line stays compact. Builtin virtual mount
// nodes (which have no meaningful type/status/modified) render as just
// title (id).
func formatTreeCardLine(card domain.MonoCardListItem) string {
	label := formatTreeNode(cardDisplayTitle(card), card.ID)
	if isVirtualMountNode(card.ID) {
		return label
	}
	var sb strings.Builder
	sb.WriteString(label)
	if card.Type != "" && card.Type != "wiki" {
		sb.WriteString(" [" + card.Type + "]")
	}
	if card.Status != "" {
		sb.WriteString(" [" + card.Status + "]")
	}
	if card.Modified != "" {
		sb.WriteString(" @" + shortCardDate(card.Modified))
	}
	return sb.String()
}

// shortCardDate trims a timestamp to its date portion for inline display.
// Falls back to the input string if it does not contain a "T" separator.
func shortCardDate(ts string) string {
	if i := strings.IndexByte(ts, 'T'); i > 0 {
		return ts[:i]
	}
	return ts
}

// cardIndex holds the maps needed for hierarchy traversal.
type cardIndex struct {
	byID       map[string]domain.MonoCardListItem
	childrenOf map[string][]string
	parentsOf  map[string][]string
}

func buildCardIndex(items []domain.MonoCardListItem) *cardIndex {
	idx := &cardIndex{
		byID:       make(map[string]domain.MonoCardListItem, len(items)),
		childrenOf: make(map[string][]string, len(items)),
		parentsOf:  make(map[string][]string, len(items)),
	}
	for _, c := range items {
		idx.byID[c.ID] = c
		// Start from explicit List field.
		children := append([]string{}, c.List...)
		idx.childrenOf[c.ID] = children
		for _, childID := range c.List {
			idx.parentsOf[childID] = append(idx.parentsOf[childID], c.ID)
		}
	}
	// Bidirectional: also derive childrenOf from the Parent field so cards
	// that declare parent without the parent listing them still appear in
	// the hierarchy.
	for _, c := range items {
		if c.Parent == "" || c.Parent == c.ID {
			continue
		}
		alreadyLinked := false
		for _, existing := range idx.childrenOf[c.Parent] {
			if existing == c.ID {
				alreadyLinked = true
				break
			}
		}
		if !alreadyLinked {
			idx.childrenOf[c.Parent] = append(idx.childrenOf[c.Parent], c.ID)
			idx.parentsOf[c.ID] = append(idx.parentsOf[c.ID], c.Parent)
		}
	}
	// Type-driven virtual mounting: cards without an explicit parent are
	// attached to their type's virtual builtin node (e.g. task→todo/done).
	idx.applyTypeMounting(items)
	// Well-known cards (summary/constraints/project_info) default-mount under
	// toc/project_info when they have no explicit parent.
	idx.applyWellKnownMounting(items)
	// Parentless wiki cards default-mount under toc so new cards are visible in
	// the knowledge base out of the box (explicit edges above take precedence).
	idx.applyDefaultTocMounting(items)
	return idx
}

// sortCardIndexChildren reorders every parent's children slice by the
// OrderBy spec. The __builtin_task__ aggregator node is exempt because its
// six status children (backlog→todo→doing→…→cancelled) have a fixed semantic
// sequence that must not be reshuffled by timestamp. All other parents —
// including status bucket nodes (__builtin_todo__, __builtin_done__, etc.)
// — sort their children so the most recently touched cards surface first.
func sortCardIndexChildren(idx *cardIndex, orderBy string) {
	field, desc := parseOrderBy(orderBy)
	if field == "" {
		return
	}
	for parentID, children := range idx.childrenOf {
		if len(children) <= 1 {
			continue
		}
		if parentID == builtinTaskID {
			continue
		}
		sort.SliceStable(children, func(i, j int) bool {
			ci, oki := idx.byID[children[i]]
			cj, okj := idx.byID[children[j]]
			if !oki || !okj {
				return false
			}
			return wikiItemCompare(ci, cj, field, desc) < 0
		})
		idx.childrenOf[parentID] = children
	}
}

// renderTreeFromRoot renders the card hierarchy starting at rootID as an ASCII
// tree using box-drawing characters (├─, └─, │). Nodes are rendered only once;
// if a node is reached again through another path it is marked "(see above)".
// maxDepth (>0) caps descent depth; maxNodes (>0) caps the rendered node count
// and appends a truncation marker when exceeded. orderBy sorts the children
// of every node before rendering (same spec as search_cards: "modified",
// "created", "title", "priority", "-" prefix for descending).
func renderTreeFromRoot(items []domain.MonoCardListItem, rootID string, maxDepth, maxNodes int, orderBy string) string {
	idx := buildCardIndex(items)
	sortCardIndexChildren(idx, orderBy)
	root, ok := idx.byID[rootID]
	if !ok {
		return ""
	}
	state := &treeRenderState{
		visited:  make(map[string]bool, len(items)),
		maxDepth: maxDepth,
		maxNodes: maxNodes,
	}
	lines := state.renderNode(root, idx, "", true, 0)
	if state.truncated {
		lines = append(lines, "... (truncated)")
	}
	return strings.Join(lines, "\n")
}

// buildTreeNodes renders the card hierarchy starting at rootID as a structured
// tree, mirroring renderTreeFromRoot's traversal (same depth/limit/orderBy
// semantics). It powers the frontend folder-tree view: each node carries its
// ID (click-to-open), Type (icon), Status, and Modified date, with nested
// Children for expand/collapse. The returned slice has exactly one element —
// the root card — whose Children nest the descendant hierarchy.
func buildTreeNodes(items []domain.MonoCardListItem, rootID string, maxDepth, maxNodes int, orderBy string) []domain.WikiCardTreeNode {
	idx := buildCardIndex(items)
	sortCardIndexChildren(idx, orderBy)
	root, ok := idx.byID[rootID]
	if !ok {
		return nil
	}
	state := &treeNodeState{
		visited:  make(map[string]bool, len(items)),
		maxDepth: maxDepth,
		maxNodes: maxNodes,
	}
	return []domain.WikiCardTreeNode{state.buildNode(root, idx, 0)}
}

type treeNodeState struct {
	visited   map[string]bool
	maxDepth  int // <= 0 unlimited
	maxNodes  int // <= 0 unlimited
	count     int
	truncated bool
}

func (s *treeNodeState) buildNode(card domain.MonoCardListItem, idx *cardIndex, depth int) domain.WikiCardTreeNode {
	node := domain.WikiCardTreeNode{
		ID:       card.ID,
		Title:    cardDisplayTitle(card),
		Type:     card.Type,
		Status:   card.Status,
		Modified: shortCardDate(card.Modified),
	}
	if s.visited[card.ID] {
		return node
	}

	s.visited[card.ID] = true
	s.count++
	if node.Title == "" {
		node.Title = card.ID
	}

	if (s.maxNodes > 0 && s.count >= s.maxNodes) || (s.maxDepth > 0 && depth >= s.maxDepth) {
		s.truncated = true
		return node
	}

	children := idx.childrenOf[card.ID]
	node.Children = make([]domain.WikiCardTreeNode, 0, len(children))
	for _, childID := range children {
		child, ok := idx.byID[childID]
		if !ok {
			continue
		}
		childNode := s.buildNode(child, idx, depth+1)
		if childNode.ID != "" {
			node.Children = append(node.Children, childNode)
		}
	}
	return node
}

type treeRenderState struct {
	visited   map[string]bool
	maxDepth  int // <= 0 unlimited
	maxNodes  int // <= 0 unlimited
	count     int
	truncated bool
}

func (s *treeRenderState) renderNode(card domain.MonoCardListItem, idx *cardIndex, prefix string, isLast bool, depth int) []string {
	if s.truncated {
		return nil
	}
	marker := "├─ "
	if isLast {
		marker = "└─ "
	}
	if s.visited[card.ID] {
		return []string{prefix + marker + formatTreeNode(cardDisplayTitle(card), card.ID) + " [see above]"}
	}
	s.visited[card.ID] = true
	lines := []string{prefix + marker + formatTreeCardLine(card)}

	if s.maxNodes > 0 && s.count+1 >= s.maxNodes {
		s.count++
		s.truncated = true
		return lines
	}
	s.count++

	if s.maxDepth > 0 && depth >= s.maxDepth {
		return lines
	}

	childPrefix := prefix
	if isLast {
		childPrefix += "   "
	} else {
		childPrefix += "│  "
	}
	children := idx.childrenOf[card.ID]
	for i, childID := range children {
		if child, ok := idx.byID[childID]; ok {
			lines = append(lines, s.renderNode(child, idx, childPrefix, i == len(children)-1, depth+1)...)
		}
	}
	return lines
}

// hierarchyNode represents one card in the bidirectional hierarchy tree.
type hierarchyNode struct {
	cardID    string
	direction string // "up" for parent/ancestor, "down" for child/descendant
	children  []*hierarchyNode
}

// renderCardHierarchy renders a card and its ancestors (↑) and descendants (↓)
// up to the given level as an ASCII tree. A node is rendered only once; if it
// appears in both directions it is silently dropped after the first visit.
func renderCardHierarchy(items []domain.MonoCardListItem, targetID string, level int) string {
	idx := buildCardIndex(items)
	if _, ok := idx.byID[targetID]; !ok {
		return ""
	}

	visited := make(map[string]bool, len(items))
	visited[targetID] = true
	root := &hierarchyNode{cardID: targetID}

	if level > 0 {
		for _, parentID := range idx.parentsOf[targetID] {
			if node := collectAncestors(parentID, idx, level-1, visited); node != nil {
				root.children = append(root.children, node)
			}
		}
		for _, childID := range idx.childrenOf[targetID] {
			if node := collectDescendants(childID, idx, level-1, visited); node != nil {
				root.children = append(root.children, node)
			}
		}
	}

	lines := renderHierarchyNode(root, idx, true, "")
	return strings.Join(lines, "\n")
}

func collectAncestors(id string, idx *cardIndex, level int, visited map[string]bool) *hierarchyNode {
	if level < 0 || visited[id] {
		return nil
	}
	visited[id] = true
	node := &hierarchyNode{cardID: id, direction: "up"}
	for _, parentID := range idx.parentsOf[id] {
		if child := collectAncestors(parentID, idx, level-1, visited); child != nil {
			node.children = append(node.children, child)
		}
	}
	return node
}

func collectDescendants(id string, idx *cardIndex, level int, visited map[string]bool) *hierarchyNode {
	if level < 0 || visited[id] {
		return nil
	}
	visited[id] = true
	node := &hierarchyNode{cardID: id, direction: "down"}
	for _, childID := range idx.childrenOf[id] {
		if child := collectDescendants(childID, idx, level-1, visited); child != nil {
			node.children = append(node.children, child)
		}
	}
	return node
}

func renderHierarchyNode(node *hierarchyNode, idx *cardIndex, isLast bool, prefix string) []string {
	card, ok := idx.byID[node.cardID]
	if !ok {
		return nil
	}

	marker := "├─ "
	if isLast {
		marker = "└─ "
	}

	var lines []string
	status := card.Status
	if status == "" {
		status = "unknown"
	}
	if node.direction == "" {
		lines = append(lines, prefix+marker+formatTreeNode(cardDisplayTitle(card), node.cardID)+" ["+status+"] [current]")
		lines = append(lines, prefix+marker+"↑ = parent/ancestor, ↓ = child/descendant")
	} else {
		symbol := "↑"
		if node.direction == "down" {
			symbol = "↓"
		}
		lines = append(lines, prefix+marker+symbol+" "+formatTreeNode(cardDisplayTitle(card), node.cardID)+" ["+status+"]")
	}

	childPrefix := prefix
	if isLast {
		childPrefix += "   "
	} else {
		childPrefix += "│  "
	}
	for i, child := range node.children {
		lines = append(lines, renderHierarchyNode(child, idx, i == len(node.children)-1, childPrefix)...)
	}
	return lines
}
func (a *Actor) handleWikiGetCard(ctx actor.PureContext, req domain.WikiGetCardReq) (domain.WikiGetCardResp, error) {
	if provider := a.externalProviderFor(req.ID); provider != nil {
		raw, err := provider.Get(ctx, req.ID)
		if err != nil {
			return domain.WikiGetCardResp{}, fmt.Errorf("project.wiki.getCard %q: %w", req.ID, err)
		}
		return domain.WikiGetCardResp{ID: req.ID, Raw: raw}, nil
	}
	card, err := a.store.Get(req.ID)
	if err != nil {
		return domain.WikiGetCardResp{}, fmt.Errorf("project.wiki.getCard %q: %w", req.ID, err)
	}
	return domain.WikiGetCardResp{ID: req.ID, Raw: card.Raw}, nil
}

// handleWikiGetCardsBatch fetches many raw cards in one round trip. Missing
// cards are reported per-id (Found=false) instead of failing the whole batch,
// mirroring how consumers tolerate single-card misses. External provider
// cards and fs cards are both served, like the single-card getter.
func (a *Actor) handleWikiGetCardsBatch(ctx actor.PureContext, req domain.WikiGetCardsBatchReq) (domain.WikiGetCardsBatchResp, error) {
	resp := domain.WikiGetCardsBatchResp{Cards: make([]domain.WikiCardRaw, 0, len(req.Ids))}
	for _, id := range req.Ids {
		if id == "" {
			continue
		}
		item := domain.WikiCardRaw{ID: id}
		if provider := a.externalProviderFor(id); provider != nil {
			if raw, err := provider.Get(ctx, id); err == nil {
				item.Raw, item.Found = raw, true
			}
		} else if card, err := a.store.Get(id); err == nil {
			item.Raw, item.Found = card.Raw, true
		}
		resp.Cards = append(resp.Cards, item)
	}
	return resp, nil
}

func (a *Actor) handleWikiGetConceptTree(_ actor.PureContext, req domain.WikiGetConceptTreeReq) (domain.WikiGetConceptTreeResp, error) {
	card, err := a.store.Get(req.ID)
	if err != nil {
		return domain.WikiGetConceptTreeResp{}, fmt.Errorf("project.wiki.getConceptTree: %w", err)
	}
	return domain.WikiGetConceptTreeResp{Root: domain.WikiConceptNode{ID: card.Title, ParentID: card.Parent}, Source: "project.wiki"}, nil
}

func (a *Actor) handleWikiCreateCard(ctx actor.PureContext, req domain.WikiCreateCardReq) (domain.WikiCreateCardResp, error) {
	if len(req.ID) > maxCardIDLen {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: Id is too long (max %d bytes): %q", maxCardIDLen, truncateForError(req.ID))
	}
	if err := validateCard(req.ID, req.Raw); err != nil {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: %w", err)
	}
	if strings.HasPrefix(req.ID, builtinCardPrefix) {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: cannot create builtin card")
	}
	if a.externalProviderFor(req.ID) != nil {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: cannot create external card")
	}
	if _, err := a.store.Get(req.ID); err == nil {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: card already exists")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	raw := normalizeCardStatusInRaw(req.Raw)
	raw = stripFrontmatterFields(raw, "created", "modified")
	raw = ensureCardMeta(req.ID, raw, now)
	card := &CardRecord{Title: req.ID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiCreateCardResp{}, fmt.Errorf("project.wiki.createCard: %w", err)
	}
	saved, _ := a.store.Get(req.ID)
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	a.syncSchedulerCard(ctx, req.ID, req.Raw)
	// Workflow membership heal: a task card created here with its parent
	// pointing at a workflow map gets the scope side effects create_task_card
	// would have applied (include append + topo node). No-op off the workflow
	// path.
	a.healTaskCardMapMembership(ctx, saved)
	return domain.WikiCreateCardResp{
		Card: cardToListItem(saved),
	}, nil
}

func (a *Actor) handleWikiEditCard(ctx actor.PureContext, req domain.WikiEditCardReq) (domain.WikiEditCardResp, error) {
	patchMode := req.OldString != ""

	if !patchMode {
		if err := validateCard(req.ID, req.Raw); err != nil {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
		}
	}

	if provider := a.externalProviderFor(req.ID); provider != nil {
		if patchMode {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: patch mode (OldString) is not supported for external provider cards")
		}
		item, err := a.externalCardItem(ctx, req.ID, provider)
		if err != nil {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
		}
		normalizeExternalCardMetadata(&item)
		if item.Protected || !item.Editable {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: card %q is read-only", req.ID)
		}
		if err := provider.Save(ctx, req.ID, req.Raw); err != nil {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
		}
		return domain.WikiEditCardResp{
			Card: item,
		}, nil
	}

	existing, err := a.store.Get(req.ID)
	if err != nil {
		if errors.Is(err, ErrCardNotFound) {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: card not found")
		}
		return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
	}

	var raw string
	if patchMode {
		raw, err = applyCardEdit(existing.Raw, req.OldString, req.NewString, req.ReplaceAll)
		if err != nil {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
		}
		if err := validateCard(req.ID, raw); err != nil {
			return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
		}
	} else {
		raw = req.Raw
	}

	now := time.Now().UTC().Format(time.RFC3339)
	raw = normalizeCardStatusInRaw(raw)
	if strings.HasPrefix(req.ID, builtinCardPrefix) {
		raw = enforceBuiltinTags(req.ID, raw)
	} else {
		raw = ensureCardMeta(req.ID, raw, now)
	}
	card := &CardRecord{Title: req.ID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiEditCardResp{}, fmt.Errorf("project.wiki.editCard: %w", err)
	}
	saved, _ := a.store.Get(req.ID)
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	// Binding lifecycle: if the edit moved/removed the binding, unbind the
	// previous agent before syncSchedulerCard applies the new binding.
	a.maybeUnbindOldSchedulerAgent(ctx, existing.Raw, raw)
	a.syncSchedulerCard(ctx, req.ID, raw)
	a.syncCardMirrorToTopo(ctx, req.ID)
	return domain.WikiEditCardResp{
		Card: cardToListItem(saved),
	}, nil
}

func (a *Actor) handleWikiValidateCard(ctx actor.PureContext, req domain.WikiValidateCardReq) (domain.WikiValidateCardResp, error) {
	errs := validateCardStructured(req.ID, req.Raw)
	return domain.WikiValidateCardResp{
		Valid:  len(errs) == 0,
		Errors: errs,
	}, nil
}

// handleTaskValidateOutputs validates a worker's produced outputs against the
// bound task card's declared data.outputs contract. The review path
// (workspace.agent.review) calls this before approving: a card with no
// data.outputs declares no contract and is always valid. PureContext because it
// only reads the card store — it does not mutate actor state.
func (a *Actor) handleTaskValidateOutputs(_ actor.PureContext, req gen.ProjectTaskValidateOutputsReq) (gen.ProjectTaskValidateOutputsResp, error) {
	if req.CardID == "" {
		return gen.ProjectTaskValidateOutputsResp{}, fmt.Errorf("project.task_validate_outputs: CardID is required")
	}
	card, err := a.store.Get(req.CardID)
	if err != nil {
		return gen.ProjectTaskValidateOutputsResp{}, fmt.Errorf("project.task_validate_outputs %q: %w", req.CardID, err)
	}
	errs := validateOutputsAgainstContract(card, req.Outputs)
	return gen.ProjectTaskValidateOutputsResp{
		Valid:  len(errs) == 0,
		Errors: errs,
	}, nil
}

func isProtectedBuiltinCardID(id string) bool {
	if id == tocID {
		return true
	}
	if strings.HasPrefix(id, builtinCardPrefix) || strings.HasPrefix(id, "builtin:") {
		return true
	}
	for _, card := range promptBuiltinCards() {
		if card.Title == id {
			return true
		}
	}
	for _, card := range builtinSkillCards() {
		if card.Title == id {
			return true
		}
	}
	return false
}

func (a *Actor) handleWikiDeleteCard(ctx actor.PureContext, req domain.WikiDeleteCardReq) (domain.WikiDeleteCardResp, error) {
	if strings.TrimSpace(req.ID) == "" {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: empty card id")
	}
	if len(req.ID) > maxCardIDLen {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: card id too long (%d bytes)", len(req.ID))
	}
	refs, refsErr := a.mountedCardRefsSnapshot()
	if refsErr != nil {
		return domain.WikiDeleteCardResp{}, refsErr
	}
	for _, ref := range refs {
		if ref.ID == req.ID {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: card %q is mounted; unmount it first", req.ID)
		}
	}

	if isProtectedBuiltinCardID(req.ID) {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: cannot delete builtin card")
	}
	card, getErr := a.store.Get(req.ID)
	if getErr != nil && !errors.Is(getErr, ErrCardNotFound) {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: cannot read card %q: %w", req.ID, getErr)
	}
	if getErr == nil {
		_, _, _, _, protected, _, deletable := cardMetadata(card)
		if protected || !deletable {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: card %q is protected", req.ID)
		}
	}
	if provider := a.externalProviderFor(req.ID); provider != nil {
		item, err := a.externalCardItem(ctx, req.ID, provider)
		if err != nil {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: %w", err)
		}
		normalizeExternalCardMetadata(&item)
		if item.Protected || !item.Deletable {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: card %q is read-only", req.ID)
		}
		if err := provider.Delete(ctx, req.ID); err != nil {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: %w", err)
		}
		_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
			ID:       req.ID,
			Modified: time.Now().UTC().Format(time.RFC3339),
		})
		return domain.WikiDeleteCardResp{
			ID: req.ID,
		}, nil
	}
	// Guard: refuse to delete a workflow instance that still belongs to a
	// living scheduler card. Orphan instances (scheduler card already deleted)
	// are allowed so cleanup cascades do not get stuck.
	if getErr == nil && card.Type == "workflow" {
		if schedID := cardDataString(card, "scheduler_card_id"); schedID != "" {
			if _, schedErr := a.store.Get(schedID); schedErr == nil {
				return domain.WikiDeleteCardResp{},
					fmt.Errorf("project.wiki.deleteCard: instance workflow %q belongs to scheduler %q; delete the scheduler card instead", req.ID, schedID)
			}
		}
	}

	// Unregister from scheduler before deleting, then cascade-delete all
	// instance workflows that belong to this scheduler.
	if strings.HasPrefix(req.ID, schedulerCardPrefix) {
		if schedRef, ok := ctx.LookupService("scheduler"); ok && schedRef != nil {
			a.unregisterFromScheduler(ctx, schedRef, req.ID)
		}
		// Binding lifecycle: deleting the scheduler card cuts the agent
		// binding — unmount the scheduler mode + clear the ActiveScheduler
		// entry on the bound agent (the card raw is still in hand here).
		if getErr == nil {
			a.applySchedulerUnbind(ctx, req.ID, card.Raw)
			// In-flight run (audit #13): a card deleted mid-run must not leave
			// its agent executing a turn for a card that no longer exists.
			// turn_cancel the recorded executor and unload an ephemeral run
			// agent; bound agents stay resident.
			a.cancelSchedulerCardRun(ctx, card)
		}
		if err := a.cascadeDeleteSchedulerInstances(ctx, req.ID); err != nil {
			return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: cascade delete instances: %w", err)
		}
	}
	if err := a.store.Delete(req.ID); err != nil {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("project.wiki.deleteCard: %w", err)
	}
	if getErr == nil {
		a.removeCardNodeFromTopo(ctx, card.Parent, req.ID)
	}
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       req.ID,
		Modified: time.Now().UTC().Format(time.RFC3339),
	})
	return domain.WikiDeleteCardResp{
		ID: req.ID,
	}, nil
}

// cascadeDeleteSchedulerInstances deletes all instance workflow maps, their
// task cards, workflow_topo graph snapshots, and template-run log entries
// that belong to the given scheduler card. Called from handleWikiDeleteCard
// before the scheduler card itself is removed from the store.
func (a *Actor) cascadeDeleteSchedulerInstances(ctx actor.PureContext, schedulerCardID string) error {
	allCards, err := a.store.List()
	if err != nil {
		return fmt.Errorf("list cards: %w", err)
	}

	// Find instance maps that belong to this scheduler.
	var instanceMapIDs []string
	for _, card := range allCards {
		if card.Type == "workflow" && cardDataString(card, "scheduler_card_id") == schedulerCardID {
			instanceMapIDs = append(instanceMapIDs, card.Title)
		}
	}
	if len(instanceMapIDs) == 0 {
		return nil
	}

	// Collect task IDs for each instance map (parent-based + scope/include).
	items := make([]domain.MonoCardListItem, 0, len(allCards))
	for _, c := range allCards {
		items = append(items, cardToListItem(c))
	}
	allTaskIDs := make(map[string]bool)
	for _, instID := range instanceMapIDs {
		var mapItem domain.MonoCardListItem
		for _, item := range items {
			if item.ID == instID {
				mapItem = item
				break
			}
		}
		for _, tid := range workflowTaskIDs(mapItem, items) {
			allTaskIDs[tid] = true
		}
	}

	// Delete task cards first (emit events for each).
	now := time.Now().UTC().Format(time.RFC3339)
	for tid := range allTaskIDs {
		if err := a.store.Delete(tid); err != nil {
			return fmt.Errorf("delete task card %q: %w", tid, err)
		}
		_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
			ID:       tid,
			Modified: now,
		})
	}

	// Delete instance maps (emit events for each).
	for _, id := range instanceMapIDs {
		if err := a.store.Delete(id); err != nil {
			return fmt.Errorf("delete instance map %q: %w", id, err)
		}
		_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
			ID:       id,
			Modified: now,
		})
	}

	// Remove run log entries for the deleted instances.
	if err := a.removeTemplateRunsByInstance(instanceMapIDs); err != nil {
		return fmt.Errorf("remove template runs: %w", err)
	}

	return nil
}

// applyCardEdit performs an old_string/new_string replacement on the body of a
// mono card (the part after the YAML frontmatter). It preserves the frontmatter
// and only touches the markdown body. Behavior mirrors the file_edit tool:
// line endings are normalized, a no-op (old==new) is rejected, multiple
// occurrences error unless replaceAll is set, and misses report context.
func applyCardEdit(raw, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == "" {
		return "", fmt.Errorf("old_string is empty")
	}
	if oldString == newString {
		return "", fmt.Errorf("old_string and new_string must differ")
	}

	// Normalize the whole raw so the frontmatter separator lookup and body
	// matching are robust to CRLF, matching the file_edit tool's behavior.
	raw = normalizeLineEndings(raw)
	oldString = strings.ReplaceAll(oldString, "\r", "")
	newString = strings.ReplaceAll(newString, "\r\n", "\n")
	newString = strings.ReplaceAll(newString, "\r", "")

	const sep = "\n---\n"
	idx := strings.Index(raw, sep)
	var frontmatter, body string
	if idx == -1 {
		frontmatter = ""
		body = raw
	} else {
		frontmatter = raw[:idx+len(sep)]
		body = raw[idx+len(sep):]
	}

	count := strings.Count(body, oldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in card body\n%s", cardMismatchContext(body))
	}
	if count > 1 && !replaceAll {
		return "", fmt.Errorf("old_string occurs %d times in card body; set replace_all=true to replace all, or narrow to one occurrence. Occurrences:\n%s", count, cardMismatchMultiple(body, oldString))
	}

	var newBody string
	if replaceAll {
		newBody = strings.ReplaceAll(body, oldString, newString)
	} else {
		newBody = strings.Replace(body, oldString, newString, 1)
	}
	return frontmatter + newBody, nil
}

// cardMismatchContext returns the first few lines of a card body as a diagnostic
// when old_string is not found, mirroring the file_edit tool's getMismatchContext.
func cardMismatchContext(body string) string {
	const snippetLines = 3
	lines := strings.Split(body, "\n")
	var context []string
	for i, line := range lines {
		if i >= snippetLines {
			break
		}
		context = append(context, fmt.Sprintf("%d: %s", i+1, line))
	}
	return fmt.Sprintf("(first %d lines of card body)\n%s", snippetLines, strings.Join(context, "\n"))
}

// cardMismatchMultiple lists each line where old_string occurs, mirroring the
// file_edit tool's fmtMismatchMultiple so callers can disambiguate.
func cardMismatchMultiple(body, oldString string) string {
	indices := findAllIndexes(body, oldString)
	lines := strings.Split(body, "\n")
	var snippets []string
	for _, idx := range indices {
		lineNum := 1 + strings.Count(body[:idx], "\n")
		if lineNum > 0 && lineNum <= len(lines) {
			snippets = append(snippets, fmt.Sprintf("line %d: %s", lineNum, lines[lineNum-1]))
		}
	}
	return strings.Join(snippets, "\n")
}

// externalProviderFor returns the provider responsible for id, if any.
func (a *Actor) externalProviderFor(id string) ExternalCardProvider {
	for _, provider := range a.externalProviders {
		if strings.HasPrefix(id, provider.Prefix()) {
			return provider
		}
	}
	return nil
}

// externalCardItem returns the list item for an external card by re-listing the provider.
func (a *Actor) externalCardItem(ctx actor.PureContext, id string, provider ExternalCardProvider) (domain.MonoCardListItem, error) {
	items, err := provider.List(ctx)
	if err != nil {
		return domain.MonoCardListItem{}, err
	}
	for _, item := range items {
		if item.ID == id {
			item.Tags = dedupStrings(item.Tags)
			return item, nil
		}
	}
	return domain.MonoCardListItem{}, fmt.Errorf("external card not found")
}
