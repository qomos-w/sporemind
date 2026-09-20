package agent

import (
	"context"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit/cardcompiler"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"gopkg.in/yaml.v2"
)

// componentSnapshotCards adapts the legacy component snapshot contributions to
// the card compiler without changing the component protocol fields.
func componentSnapshotCards(snapshot domain.AgentComponentSnapshot) []gen.MonoCard {
	byID := make(map[string]gen.MonoCard)
	order := make([]string, 0, len(snapshot.Prompts)+len(snapshot.Tools))
	add := func(id string, card gen.MonoCard) *gen.MonoCard {
		if existing, ok := byID[id]; ok {
			return &existing
		}
		byID[id] = card
		order = append(order, id)
		return &card
	}
	for _, prompt := range snapshot.Prompts {
		section := prompt.Placement
		switch section {
		case "system":
			section = "policy"
		case "resolved":
			section = "task"
		case "role", "policy", "project_context", "active_goal", "active_plan", "worktree_status", "task", "skills", "tool_guidance", "leading", "trailing", "environment":
		default:
			section = "policy"
		}
		card := add(prompt.CardID, gen.MonoCard{ID: prompt.CardID, Type: "prompt"})
		card.PromptContributions = append(card.PromptContributions, gen.CardPromptContribution{
			ID: prompt.ID, Section: section,
			Text: prompt.Text, Priority: prompt.Priority,
		})
		byID[prompt.CardID] = *card
	}
	for _, tool := range snapshot.Tools {
		card := add(tool.CardID, gen.MonoCard{ID: tool.CardID, Type: "capability_module"})
		card.ToolContributions = append(card.ToolContributions, gen.CardToolContribution{
			ID: tool.ID, CallableID: tool.CallableID, Priority: tool.Priority,
		})
		byID[tool.CardID] = *card
	}
	cards := make([]gen.MonoCard, 0, len(order))
	for _, id := range order {
		cards = append(cards, byID[id])
	}
	return cards
}

func compileComponentSnapshot(snapshot domain.AgentComponentSnapshot, specs map[string][]gen.ToolSpec) cardcompiler.CompileResult {
	cards := componentSnapshotCards(snapshot)
	store := cardcompiler.CardMap{}
	refs := make([]gen.CardRef, 0, len(cards))
	for i, card := range cards {
		store[card.ID] = card
		refs = append(refs, gen.CardRef{ID: card.ID, Order: int32(i), Scope: "agent"})
	}
	for _, mount := range snapshot.Mounts {
		if !mount.Enabled || mount.CardID == "" {
			continue
		}
		if _, ok := store[mount.CardID]; ok {
			for i := range refs {
				if refs[i].ID == mount.CardID {
					refs[i].Order = mount.Order
				}
			}
		}
	}
	resolved, err := (cardcompiler.Resolver{Store: store}).Resolve(refs)
	if err != nil {
		return cardcompiler.CompileResult{ContextSnapshot: cardcompiler.ContextSnapshot{}}
	}
	return cardcompiler.Compile(resolved, cardcompiler.CompileOptions{ToolSpecs: specs})
}

func (a *Actor) projectCardStore(ctx actor.Context) cardcompiler.CardStore {
	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		return nil
	}
	planner := ctx.Planner()
	fetch := func(ids []string) map[string]string {
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		defer cancel()
		var response domain.WikiGetCardsBatchResp
		result, err := planner.Call(callCtx, projectRef, "project.wiki_get_cards_batch", domain.WikiGetCardsBatchReq{Ids: ids}).Await()
		if err != nil || !decodePromptResult(result, &response) {
			return nil
		}
		raws := make(map[string]string, len(response.Cards))
		for _, item := range response.Cards {
			if item.Found {
				raws[item.ID] = item.Raw
			}
		}
		return raws
	}
	fetchOne := func(id string) (gen.MonoCard, bool) {
		raws := fetch([]string{id})
		if raws == nil {
			return gen.MonoCard{}, false
		}
		raw, ok := raws[id]
		if !ok || strings.TrimSpace(raw) == "" {
			return gen.MonoCard{}, false
		}
		return projectCardFromRaw(id, raw)
	}
	fetchMany := func(ids []string) map[string]gen.MonoCard {
		raws := fetch(ids)
		if raws == nil {
			return nil
		}
		cards := make(map[string]gen.MonoCard, len(raws))
		for id, raw := range raws {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			if card, ok := projectCardFromRaw(id, raw); ok {
				cards[id] = card
			}
		}
		return cards
	}
	return cardcompiler.NewProjectCardStoreAdapter(projectCardReader{fetchOne: fetchOne, fetchMany: fetchMany})
}

type projectCardReader struct {
	fetchOne  func(id string) (gen.MonoCard, bool)
	fetchMany func(ids []string) map[string]gen.MonoCard
}

func (r projectCardReader) GetProjectCard(id string) (gen.MonoCard, bool) {
	if r.fetchOne == nil {
		return gen.MonoCard{}, false
	}
	return r.fetchOne(id)
}

func (r projectCardReader) GetProjectCards(ids []string) map[string]gen.MonoCard {
	if r.fetchMany == nil {
		return nil
	}
	return r.fetchMany(ids)
}

func projectCardFromRaw(id, raw string) (gen.MonoCard, bool) {
	card := gen.MonoCard{ID: id}
	body := raw
	if strings.HasPrefix(raw, "---") {
		if end := strings.Index(raw[3:], "---"); end >= 0 {
			front := raw[3 : 3+end]
			body = strings.TrimSpace(raw[3+end+3:])
			card.Type = frontmatterDataValue(front, "type")
			if card.Type == "" {
				card.Type = frontmatterDataValue(front, "componentKind")
			}
			card.Source = frontmatterDataValue(front, "source")
			for _, callable := range frontmatterDataStrings(front, "tools") {
				card.ToolContributions = append(card.ToolContributions, gen.CardToolContribution{ID: callable, CallableID: callable})
			}
			section := frontmatterDataValue(front, "section")
			if section != "" {
				card.PromptContributions = []gen.CardPromptContribution{{ID: id + ":body", Section: section, Text: body}}
			}
		}
	}
	if card.Type == "" {
		if isSkillCardID(id) {
			card.Type = "skill"
		} else if strings.HasPrefix(id, "prompt:") {
			card.Type = "prompt"
		} else {
			card.Type = "component"
		}
	}
	if card.Type == "skill" {
		body = cardBody(body)
	}
	card.Body = body
	if body != "" && len(card.PromptContributions) == 0 {
		section := "policy"
		if card.Type == "prompt" && strings.Contains(frontmatterDataValue(raw, "tags"), "profile") {
			section = "role"
		}
		card.PromptContributions = []gen.CardPromptContribution{{ID: id + ":body", Section: section, Text: body}}
	}
	return card, true
}

func frontmatterDataValue(front, key string) string {
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, key+":")), "'\"")
		}
	}
	return ""
}

func frontmatterDataStrings(front, key string) []string {
	lines := strings.Split(front, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
		if value != "" {
			var parsed []string
			if err := yaml.Unmarshal([]byte(value), &parsed); err == nil {
				return nonEmptyStrings(parsed)
			}
			return nonEmptyStrings(strings.Split(strings.Trim(value, "[]"), ","))
		}
		var values []string
		for _, next := range lines[i+1:] {
			item := strings.TrimSpace(next)
			if item == "" {
				continue
			}
			if !strings.HasPrefix(item, "-") {
				break
			}
			values = append(values, strings.Trim(strings.TrimSpace(strings.TrimPrefix(item, "-")), "'\""))
		}
		return nonEmptyStrings(values)
	}
	return nil
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.Trim(value, "'\""))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// activeContextCards returns the dynamic per-turn context cards that are
// compiled into Instructions.Resolved. Plan cards are not included anywhere
// — the active plan is deliberately not injected into the prompt. Goal and
// worktree are represented by mounted mode cards
// (builtin:mode:goal and builtin:mode:worktree), so their prompts are
// contributed through the component snapshot instead of synthetic cards. Goal
// is additionally excluded here because its full formatted block is injected
// via HotContext by buildGoalBlock.
func (a *Actor) activeContextCards() []gen.MonoCard {
	return nil
}

func compileTurnCardContext(snapshot domain.AgentComponentSnapshot, tools []domain.ToolSpec, messages []domain.ChatMessage, synth []gen.MonoCard, dynamic ...[]gen.MonoCard) cardcompiler.CompileResult {
	return compileTurnCardContextWithStoreAndFilters(nil, snapshot, tools, messages, synth, nil, dynamic...)
}

func compileTurnCardContextWithStore(store cardcompiler.CardStore, snapshot domain.AgentComponentSnapshot, tools []domain.ToolSpec, messages []domain.ChatMessage, synth []gen.MonoCard, dynamic ...[]gen.MonoCard) cardcompiler.CompileResult {
	return compileTurnCardContextWithStoreAndFilters(store, snapshot, tools, messages, synth, nil, dynamic...)
}

func toolFilterReasons(snapshot domain.AgentComponentSnapshot, tools []domain.ToolSpec) map[string]string {
	allowed := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		id := tool.CallableID
		if id == "" {
			id = tool.Name
		}
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	reasons := make(map[string]string)
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "" {
			continue
		}
		if _, ok := allowed[tool.CallableID]; !ok {
			reasons[tool.CallableID] = "policy_filtered"
		}
	}
	return reasons
}

func compileTurnCardContextWithStoreAndFilters(store cardcompiler.CardStore, snapshot domain.AgentComponentSnapshot, tools []domain.ToolSpec, messages []domain.ChatMessage, synth []gen.MonoCard, filterReasons map[string]string, dynamic ...[]gen.MonoCard) cardcompiler.CompileResult {
	cards := componentSnapshotCards(snapshot)
	if store != nil {
		projectCards := make(map[string]gen.MonoCard)
		projectCardOrder := make([]string, 0, len(snapshot.Mounts))
		// Prefer one batch fetch over N per-card round trips when the store
		// supports it; a nil batch means batching is unavailable and we fall
		// back to per-id GetCard.
		var batch map[string]gen.MonoCard
		if bs, ok := store.(cardcompiler.BatchCardStore); ok {
			ids := make([]string, 0, len(snapshot.Mounts))
			seen := make(map[string]bool, len(snapshot.Mounts))
			for _, mount := range snapshot.Mounts {
				if !mount.Enabled || mount.CardID == "" || isSkillCardID(mount.CardID) {
					continue
				}
				if seen[mount.CardID] {
					continue
				}
				seen[mount.CardID] = true
				ids = append(ids, mount.CardID)
			}
			if len(ids) > 0 {
				batch = bs.GetCards(ids)
			}
		}
		for _, mount := range snapshot.Mounts {
			if !mount.Enabled || mount.CardID == "" {
				continue
			}
			if isSkillCardID(mount.CardID) {
				continue
			}
			var card gen.MonoCard
			var ok bool
			if batch != nil {
				card, ok = batch[mount.CardID]
			} else {
				card, ok = store.GetCard(mount.CardID)
			}
			if ok {
				if _, exists := projectCards[mount.CardID]; !exists {
					projectCardOrder = append(projectCardOrder, mount.CardID)
				}
				projectCards[mount.CardID] = card
			}
		}
		if len(projectCards) > 0 {
			filtered := cards[:0]
			for _, card := range cards {
				if _, ok := projectCards[card.ID]; !ok {
					filtered = append(filtered, card)
				}
			}
			cards = filtered
			for _, id := range projectCardOrder {
				cards = append(cards, projectCards[id])
			}
		}
	}
	cards = append(cards, synth...)
	for _, extra := range dynamic {
		cards = append(cards, extra...)
	}
	specs := make(map[string][]gen.ToolSpec, len(tools))
	for _, tool := range tools {
		id := tool.CallableID
		if id == "" {
			id = tool.Name
		}
		if id == "" {
			continue
		}
		specs[id] = append(specs[id], tool)
		cards = append(cards, gen.MonoCard{
			ID:   "callable:" + id,
			Type: "callable",
			ToolContributions: []gen.CardToolContribution{{
				ID: "callable:" + id, CallableID: id,
			}},
		})
	}
	cardMap := cardcompiler.CardMap{}
	refs := make([]gen.CardRef, 0, len(cards))
	for i, card := range cards {
		if card.ID == "" {
			continue
		}
		cardMap[card.ID] = card
		refs = append(refs, gen.CardRef{ID: card.ID, Order: int32(i), Scope: "agent"})
	}
	resolved, err := (cardcompiler.Resolver{Store: cardMap, Options: cardcompiler.ResolveOptions{ExcludeKnowledge: true, ExcludeConcept: true}}).Resolve(refs)
	if err != nil {
		return cardcompiler.CompileResult{ContextSnapshot: cardcompiler.ContextSnapshot{}}
	}
	return cardcompiler.Compile(resolved, cardcompiler.CompileOptions{ToolSpecs: specs, ToolFilterReasons: filterReasons, Messages: messages})
}

func compiledInstructions(result cardcompiler.CompileResult) *domain.CompiledInstructions {
	instructions := &domain.CompiledInstructions{}
	for i, block := range result.SystemBlocks {
		if block.Text == "" {
			continue
		}
		section := ""
		if i < len(result.Sections) {
			section = result.Sections[i].Section
		}
		switch section {
		case "leading", "role", "policy", "project_context":
			instructions.Base = append(instructions.Base, block.Text)
		default:
			instructions.Resolved = append(instructions.Resolved, block.Text)
		}
	}
	return instructions
}

// compileTurnInstructions is the single orchestration entry point for building
// the CompiledInstructions (Base + Resolved) from the card compiler. It feeds
// the same card sources that the real dispatch path uses - component snapshot
// cards, synthetic turn cards (tool guidance / skills / environment), and
// dynamic active-context cards (worktree / plan) - so that the preview paths
// (Prompt Context Snapshot, Compiled Prompt) and the dispatch path produce
// identical instruction blocks.
//
// Goal is deliberately excluded from activeContextCards here: the goal's
// full formatted block (## Active Goal) is injected via HotContext by
// buildGoalBlock, and having it in Resolved too would duplicate it in the
// final system prompt.
//
// tools and messages are passed as nil because this function serves the
// instruction layer only; the dispatch path still calls
// compileTurnCardContextWithStoreAndFilters directly when it needs the
// ToolSpec / Messages post-processing.
func (a *Actor) compileTurnInstructions(ctx actor.Context, cfg domain.AgentKindConfig, snapshot domain.AgentComponentSnapshot) *domain.CompiledInstructions {
	synth := a.turnSynthCards(ctx, cfg, snapshot)
	dynamic := a.activeContextCards()
	store := a.projectCardStore(ctx)
	result := compileTurnCardContextWithStoreAndFilters(store, snapshot, nil, nil, synth, nil, dynamic)
	return compiledInstructions(result)
}

// turnSynthCards produces synthetic prompt cards for content that is not
// sourced from a card store but must still enter the compiled system prompt:
// tool guidance, tool-usage prompts, skill manifest summary, and static
// environment. Each maps to the canonical section defined in the goal spec.
// This replaces the resolveInstructions fallback path so the compiler is the
// single assembly boundary.
func (a *Actor) turnSynthCards(ctx actor.Context, cfg domain.AgentKindConfig, snapshot domain.AgentComponentSnapshot) []gen.MonoCard {
	var cards []gen.MonoCard
	add := func(id, section, text string) {
		if text == "" {
			return
		}
		cards = append(cards, gen.MonoCard{
			ID: id, Type: "prompt",
			PromptContributions: []gen.CardPromptContribution{{ID: id, Section: section, Text: text}},
		})
	}
	add("agent:tool-guidance", "tool_guidance", componentToolGuidance(snapshot))
	if planner := ctx.Planner(); planner != nil {
		if summary := a.skillManifestSummary(ctx, planner, cfg); summary != "" {
			add("agent:skill-manifest", "skills", summary)
		}
		if status := a.componentStatusSection(ctx, snapshot); status != "" {
			add("agent:component-status", "component_status", status)
		}
		if env := a.resolveStaticEnvironment(ctx); env != "" {
			add("agent:environment", "environment", env)
		}
	}
	return cards
}

func componentSnapshotToolIDs(snapshot domain.AgentComponentSnapshot) []string {
	specs := make(map[string][]gen.ToolSpec, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		if tool.CallableID != "" {
			specs[tool.CallableID] = append(specs[tool.CallableID], gen.ToolSpec{Name: tool.CallableID, CallableID: tool.CallableID})
		}
	}
	result := compileComponentSnapshot(snapshot, specs)
	ids := make([]string, 0, len(result.ToolSpec))
	for _, spec := range result.ToolSpec {
		if spec.CallableID != "" {
			ids = append(ids, spec.CallableID)
		} else {
			ids = append(ids, spec.Name)
		}
	}
	return ids
}
