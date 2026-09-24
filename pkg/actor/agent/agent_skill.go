package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// isBuiltinSkill reports whether skillID (no "skill:" prefix) names an embed
// builtin skill.
func isBuiltinSkill(skillID string) bool {
	_, ok := builtinSkillIDSet()[skillID]
	return ok
}

// Card-ID prefixes for skill components.
const (
	skillCardIDPrefix  = "skill:"
	extSkillCardPrefix = "ext-skill:"
)

// skillCardIDFor converts a raw skill ID to its wiki card ID.
// Internal skills (bare name) are prefixed with "skill:"; external skills
// already carry the full "ext-skill:<source>:<name>" card ID and are returned
// unchanged so the routing path stays consistent.
func skillCardIDFor(skillID string) string {
	if strings.HasPrefix(skillID, extSkillCardPrefix) {
		return skillID
	}
	return skillCardIDPrefix + skillID
}

// isSkillCardID reports whether cardID refers to a skill component — either an
// internal "skill:" card or an external "ext-skill:" card.
func isSkillCardID(cardID string) bool {
	return strings.HasPrefix(cardID, skillCardIDPrefix) || strings.HasPrefix(cardID, extSkillCardPrefix)
}

// extSkillNameFromID extracts the skill name from an ext-skill:<source>:<name>
// card ID. Returns ok=false for malformed or non-external IDs.
func extSkillNameFromID(id string) (string, bool) {
	if !strings.HasPrefix(id, extSkillCardPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(id, extSkillCardPrefix)
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx >= len(rest)-1 {
		return "", false
	}
	return rest[idx+1:], true
}

// skillMountTitle returns a human-readable title for a skill mount from its
// card ID: the bare skill name for internal skills, the skill name (without the
// ext-skill:<source>: qualifier) for external skills.
func skillMountTitle(cardID string) string {
	if name, ok := extSkillNameFromID(cardID); ok {
		return name
	}
	return strings.TrimPrefix(cardID, skillCardIDPrefix)
}

type projectSkillCard struct {
	ID, Name, Description, Body, ProjectID string
	Tags, Tools                            []string
	// Runtime marks an executable skill. Empty (default) keeps the
	// prompt-fragment contract; "spore" runs the body's first ```spore
	// fence through the spore VM on agent_skill_use.
	Runtime string
	// SporeBudget carries the optional frontmatter budget overrides
	// (max_duration_sec / max_instructions / max_output_bytes). Only
	// meaningful when Runtime == "spore".
	SporeBudget sporeSkillBudget
}

func (a *Actor) getProjectSkillCard(ctx actor.Context, skillID string) (projectSkillCard, error) {
	cardID := skillCardIDFor(skillID)
	card, err := a.getCardFromStore(ctx, cardID)
	if err != nil {
		return projectSkillCard{}, err
	}
	if card.Raw == "" {
		return projectSkillCard{}, fmt.Errorf("skill %q not found", skillID)
	}
	cardRawBody := cardBody(card.Raw)
	result := projectSkillCard{ID: skillID, Name: skillID, Body: cardBody(cardRawBody)}
	if strings.HasPrefix(card.Raw, "---") {
		end := strings.Index(card.Raw[3:], "---")
		if end >= 0 {
			for _, line := range strings.Split(card.Raw[3:3+end], "\n") {
				key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					continue
				}
				value = strings.Trim(strings.TrimSpace(value), "[]\\\"")
				switch strings.TrimSpace(key) {
				case "name":
					result.Name = value
				case "description":
					result.Description = value
				case "tags":
					result.Tags = strings.Split(value, ",")
				case "tools":
					result.Tools = strings.Split(value, ",")
				case "projectId", "projectID":
					result.ProjectID = value
				case "runtime":
					result.Runtime = value
				case "max_duration_sec":
					if n, perr := strconv.Atoi(value); perr == nil {
						result.SporeBudget.MaxDurationSec = n
					}
				case "max_instructions":
					if n, perr := strconv.Atoi(value); perr == nil {
						result.SporeBudget.MaxInstructions = n
					}
				case "max_output_bytes":
					if n, perr := strconv.Atoi(value); perr == nil {
						result.SporeBudget.MaxOutputBytes = n
					}
				}
			}
		}
	}
	for i := range result.Tags {
		result.Tags[i] = strings.TrimSpace(result.Tags[i])
	}
	for i := range result.Tools {
		result.Tools[i] = strings.TrimSpace(result.Tools[i])
	}
	return result, nil
}

// getCardFromStore fetches a card by ID from the project card store, falling
// back to the workspace (system meta project) store for builtin cards that are
// not replicated into the project store (e.g. builtin skills).
//
// For builtin skills (id in the embed builtin set) the workspace is queried
// first so a stale project-level residue can never shadow the canonical embed
// version; project-level skills resolve against the project store first.
func (a *Actor) getCardFromStore(ctx actor.Context, cardID string) (domain.WikiGetCardResp, error) {
	if isBuiltinSkill(strings.TrimPrefix(cardID, "skill:")) {
		if resp, ok := lookupWikiCard(ctx, "workspace", cardID); ok {
			return resp, nil
		}
		if resp, ok := lookupWikiCard(ctx, "project", cardID); ok {
			return resp, nil
		}
	} else {
		if resp, ok := lookupWikiCard(ctx, "project", cardID); ok {
			return resp, nil
		}
		if resp, ok := lookupWikiCard(ctx, "workspace", cardID); ok {
			return resp, nil
		}
	}
	return domain.WikiGetCardResp{}, fmt.Errorf("card %q not found", cardID)
}

// lookupWikiCard fetches a card via <service>.wiki.get_card. It returns the
// response and true only when the card exists and carries a non-empty body.
func lookupWikiCard(ctx actor.Context, service, cardID string) (domain.WikiGetCardResp, bool) {
	planner := ctx.Planner()
	if planner == nil {
		return domain.WikiGetCardResp{}, false
	}
	serviceRef, ok := ctx.LookupService(service)
	if !ok {
		return domain.WikiGetCardResp{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, serviceRef, service+".wiki_get_card", domain.WikiGetCardReq{ID: cardID}).Await()
	if err != nil {
		return domain.WikiGetCardResp{}, false
	}
	var resp domain.WikiGetCardResp
	if !decodePromptResult(result, &resp) || resp.Raw == "" {
		return domain.WikiGetCardResp{}, false
	}
	return resp, true
}

// isSkillMounted reports whether a skill component is currently mounted and
// enabled on this agent. Used to enforce the "mount before use" constraint.
func (a *Actor) isSkillMounted(skillID string) bool {
	cardID := skillCardIDFor(skillID)
	for _, mount := range a.ComponentMounts {
		if mount.CardID == cardID && mount.Enabled {
			return true
		}
	}
	return false
}

// mountSkill ensures a skill component is present in ComponentMounts. It is
// idempotent: if the skill is already mounted (regardless of scope), it returns
// the existing MountID. Used by agent.skill.mount, slash commands, and any
// other path that obtains a skill body and wants the mount state to be
// reflected in the component snapshot.
func (a *Actor) mountSkill(ctx actor.Context, skillID string) string {
	cardID := skillCardIDFor(skillID)
	for i := range a.ComponentMounts {
		if a.ComponentMounts[i].CardID == cardID {
			return a.ComponentMounts[i].MountID
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	mountID := ctx.NewID().String()
	a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
		MountID:   mountID,
		CardID:    cardID,
		Kind:      "skill",
		Title:     skillMountTitle(cardID),
		Enabled:   true,
		Scope:     "user",
		MountedAt: now,
		UpdatedAt: now,
	})
	a.ComponentRevision++
	a.componentSnapshot.Store(nil)
	a.resolvedInstructions.Store(nil)
	a.saveMailbox(ctx)
	return mountID
}

// skillAlreadyUsed reports whether the skill was already invoked this
// session. See usedSkillsMu for the locking rationale.
func (a *Actor) skillAlreadyUsed(skillID string) bool {
	a.usedSkillsMu.Lock()
	defer a.usedSkillsMu.Unlock()
	_, used := a.usedSkills[skillID]
	return used
}

// markSkillUsed records the skill as invoked this session.
func (a *Actor) markSkillUsed(skillID string) {
	a.usedSkillsMu.Lock()
	defer a.usedSkillsMu.Unlock()
	if a.usedSkills == nil {
		a.usedSkills = make(map[string]struct{})
	}
	a.usedSkills[skillID] = struct{}{}
}

// handleSkillUse enforces the mount-before-use constraint: the skill must be
// present in ComponentMounts and enabled. If the skill is not mounted or is
// disabled, it returns an error instructing the caller to mount the skill
// first. The slash path (synthesizeSkillMount) bypasses this check because it
// is a user-initiated shortcut that auto-mounts on demand.
//
// runtime:spore skills branch away from the prompt contract: the body's first
// ```spore fence is executed and the JSON-encoded return value rides in
// AgentSkillUseResp.Result instead of Body.
func (a *Actor) handleSkillUse(ctx actor.Context, req domain.AgentSkillUseReq) (domain.AgentSkillUseResp, error) {
	if req.SkillID == "" {
		return domain.AgentSkillUseResp{}, fmt.Errorf("agent.skill.use: skillId is required")
	}
	if !a.isSkillMounted(req.SkillID) {
		return domain.AgentSkillUseResp{}, fmt.Errorf("agent.skill.use: skill %q is not mounted; mount the skill component before using it", req.SkillID)
	}
	used := a.skillAlreadyUsed(req.SkillID)
	var result domain.AgentSkillUseResp
	if card, err := a.getProjectSkillCard(ctx, req.SkillID); err == nil && card.Runtime == "spore" {
		encoded, runErr := runSporeSkillBody(ctx.Lifecycle(), card.Body, req.Args, card.SporeBudget)
		if runErr != nil {
			return domain.AgentSkillUseResp{}, runErr
		}
		result = domain.AgentSkillUseResp{MountID: a.mountSkill(ctx, card.ID), SkillID: card.ID, Result: encoded}
	} else {
		resp, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: req.SkillID, Args: req.Args, Context: req.Context, TurnID: req.TurnID})
		if err != nil {
			return domain.AgentSkillUseResp{}, err
		}
		result = domain.AgentSkillUseResp{MountID: resp.MountID, Body: resp.Body, SkillID: resp.SkillID}
	}
	a.markSkillUsed(req.SkillID)
	if used {
		result.Warning = fmt.Sprintf("agent.skill.use: skill %q has already been used in this session", req.SkillID)
		ctx.Logger().Warn("agent.skill.use: duplicate use", "skill_id", req.SkillID)
	}
	return result, nil
}

func (a *Actor) handleSkillInject(ctx actor.Context, req domain.AgentSkillMountReq) (domain.AgentSkillMountResp, error) {
	if req.SkillID == "" {
		return domain.AgentSkillMountResp{}, fmt.Errorf("agent.skill.mount: skillId is required")
	}

	body, err := a.getProjectSkillCard(ctx, req.SkillID)
	if err != nil {
		return domain.AgentSkillMountResp{}, fmt.Errorf("agent.skill.mount: card lookup failed: %w", err)
	}
	if body.ProjectID != "" && body.ProjectID != parentProjectID(ctx) {
		return domain.AgentSkillMountResp{}, fmt.Errorf("agent.skill.mount: skill %q is scoped to project %q and cannot be injected from this agent", req.SkillID, body.ProjectID)
	}
	if req.Args != "" {
		body.Body = strings.ReplaceAll(body.Body, "${args}", req.Args)
		body.Body = strings.ReplaceAll(body.Body, "$args", req.Args)
	}
	mountID := a.mountSkill(ctx, body.ID)
	return domain.AgentSkillMountResp{MountID: mountID, Body: body.Body, SkillID: body.ID}, nil
}

// skillExists reports whether the skill card exists in the project CardStore.
func (a *Actor) skillExists(ctx actor.Context, skillID string) bool {
	if skillID == "" {
		return false
	}
	_, err := a.getProjectSkillCard(ctx, skillID)
	return err == nil
}

// synthesizeSkillMount produces a tool_call step that is shape-identical to
// an LLM-initiated agent_skill_use call (tool_use + tool_result blocks) and
// appends it to a.steps with TurnID = turnName (the assistant turn that will
// start next). It does NOT emit step events — the caller decides when (and
// under which turn ID) to surface them:
//
//   - success path: deferred to startTurnWithName, which emits the step after
//     turn.started so the synthesized round renders inside the assistant turn
//     envelope rather than as a standalone envelope between user and assistant.
//   - failure path: emit immediately with the userTurnID via
//     emitSynthStepEvents since no assistant turn will start.
//
// Returns the synthesized step ID (needed by callers to emit events).
func (a *Actor) synthesizeSkillMount(ctx actor.Context, turnName, userTurnID, skillID, args string) (string, error) {
	stepID := fmt.Sprintf("%s-tool-%03d", turnName, a.allocSeq())
	toolUseID := ctx.NewID().String()

	inputJSON, _ := json.Marshal(map[string]string{
		"SkillId": skillID,
		"Args":    args,
		"TurnId":  turnName,
	})

	resp, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{
		SkillID: skillID,
		Args:    args,
		TurnID:  userTurnID,
	})

	var resultText string
	var isErr bool
	if err != nil {
		resultText = fmt.Sprintf("skill mount failed: %v", err)
		isErr = true
	} else {
		resultText = resp.Body
		a.markSkillUsed(skillID)
	}

	step := domain.Step{
		ID:        stepID,
		Role:      "assistant",
		Type:      "tool_call",
		TurnID:    turnName,
		Closed:    true,
		Seq:       a.allocSeq(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Meta:      a.agentMeta(),
		Content: []domain.ContentBlock{
			{
				Type:      domain.ContentBlockToolUse,
				ToolUseID: toolUseID,
				ToolName:  "agent_skill_use",
				Input:     string(inputJSON),
			},
			{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: toolUseID,
				Text:      resultText,
				IsError:   isErr,
			},
		},
	}
	if isErr {
		step.Error = resultText
	}
	a.appendStep(step)
	a.takeSnapshot()
	a.saveMailbox(ctx)
	return stepID, err
}

// emitStepEventsForTurn emits the canonical event sequence (step.opened,
// block.appended per extra block, step.closed / step.error) for an existing
// step in a.steps, anchored to the given turnID. Used by:
//   - startTurnWithName: emits a pending slash-skill step (created with
//     TurnID = turnName) after turn.started, and re-anchors legacy system
//     skill-mount steps from the user turn to the assistant turn.
//   - synthesizeSkillMount failure path: emits with userTurnID when no
//     assistant turn will start, so the error round still surfaces.
func (a *Actor) emitStepEventsForTurn(ctx actor.Context, stepID, turnID string) {
	var step *domain.Step
	for i := range a.steps {
		if a.steps[i].ID == stepID {
			step = &a.steps[i]
			break
		}
	}
	if step == nil {
		return
	}
	if len(step.Content) > 0 {
		first := step.Content[0]
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:     "step.opened",
			StepID:   step.ID,
			TurnID:   turnID,
			StepType: step.Type,
			Role:     step.Role,
			Seq:      step.Seq,
			Block:    &first,
		})
		for i := 1; i < len(step.Content); i++ {
			blk := step.Content[i]
			_ = a.emitActorStepEvent(ctx, domain.StepEvent{
				Kind:   "block.appended",
				StepID: step.ID,
				TurnID: turnID,
				Block:  &blk,
			})
		}
	}
	if step.Error != "" {
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "step.error",
			StepID: step.ID,
			TurnID: turnID,
			Error:  step.Error,
		})
	} else {
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "step.closed",
			StepID: step.ID,
			TurnID: turnID,
		})
	}
}

// fetchFilteredSkillManifest lists all skill cards from the project card store,
// the workspace (system meta project) card store, and external skill
// directories (surfaced by the project actor's external providers). Skills are
// deduplicated by NAME with priority:
//
//	project-internal (user) > system-internal (builtin) > project-external (ext-skill)
//
// An external skill that is not shadowed by a higher-priority same-named skill
// is included with its full "ext-skill:<source>:<name>" ID so that
// agent_skill_use(SkillId) routes to the external provider.
//
// Only metadata (name, description) is extracted from the list response — no
// full card bodies are fetched. The full body is loaded on-demand when the
// agent calls agent_skill_use.
func (a *Actor) fetchFilteredSkillManifest(ctx actor.Context, planner actor.Planner, cfg domain.AgentKindConfig) ([]domain.SkillManifest, error) {
	if planner == nil {
		return nil, fmt.Errorf("planner unavailable")
	}

	// Priority classes for same-name resolution.
	const (
		classExternal = 1 // ext-skill:<source>:<name>
		classSystem   = 2 // builtin skills (workspace canonical + project residue)
		classProject  = 3 // project-authored user skills
	)

	// skillClassFor assigns a priority class to a "skill:" card from the given
	// store. Workspace cards are always system (canonical builtins). For project
	// cards, explicit source metadata is authoritative:
	//   - source == "builtin" → system (a builtin residue in the project store;
	//     demoted so the workspace canonical wins ties).
	//   - source == "project"/"user" → project-internal, even when the name
	//     collides with a builtin (project-internal must override system-internal).
	// Only when the source metadata is missing does the name-based heuristic
	// (isBuiltinSkill) classify a legacy builtin residue as system.
	skillClassFor := func(name, store, source string) int {
		if store == "workspace" {
			return classSystem
		}
		switch source {
		case "builtin":
			return classSystem
		case "project", "user":
			return classProject
		}
		// Source metadata missing: fall back to name-based heuristic so legacy
		// builtin residues without explicit source are treated as system.
		if isBuiltinSkill(name) {
			return classSystem
		}
		return classProject
	}

	type entry struct {
		manifest domain.SkillManifest
		class    int
	}
	best := make(map[string]entry)

	// register classifies a card and keeps the highest-priority entry per skill
	// name. On ties (same class, same name) the first registered entry wins, so
	// the collection order below (workspace → project) lets the workspace
	// canonical shadow a same-class project residue.
	register := func(card domain.MonoCardListItem, store string) {
		var skillName, manifestID, displayName string
		var class int

		switch {
		case strings.HasPrefix(card.ID, extSkillCardPrefix):
			name, ok := extSkillNameFromID(card.ID)
			if !ok {
				return
			}
			skillName = name
			manifestID = card.ID // preserve full ext-skill: ID for routing
			displayName = name
			class = classExternal
		case strings.HasPrefix(card.ID, skillCardIDPrefix):
			name := strings.TrimPrefix(card.ID, skillCardIDPrefix)
			skillName = name
			manifestID = name
			displayName = name
			source := card.Source
			if source == "" {
				if s, ok := card.Data["source"].(string); ok {
					source = s
				}
			}
			class = skillClassFor(name, store, source)
		default:
			return
		}

		desc := ""
		if d, ok := card.Data["description"].(string); ok && d != "" {
			desc = d
		}
		if desc == "" {
			desc = parseFrontmatterString(card.Raw, "description")
		}
		params := parseFrontmatterList(card.Raw, "arguments")
		if len(params) == 0 {
			if p, ok := card.Data["arguments"].([]any); ok {
				for _, v := range p {
					if s, ok := v.(string); ok {
						params = append(params, s)
					}
				}
			}
		}

		candidate := entry{
			manifest: domain.SkillManifest{ID: manifestID, Name: displayName, Description: desc, Params: params},
			class:    class,
		}
		if prev, ok := best[skillName]; !ok || candidate.class > prev.class {
			best[skillName] = candidate
		}
	}

	collect := func(store string) {
		serviceRef, ok := ctx.LookupService(store)
		if !ok {
			return
		}
		callable := store + ".wiki_list_cards"
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		defer cancel()
		// Limit: -1 = unlimited — the skill manifest must see every skill card,
		// not the 50 most recently modified.
		result, err := planner.Call(callCtx, serviceRef, callable, domain.WikiListCardsReq{Flat: true, Limit: -1}).Await()
		if err != nil {
			return
		}
		var listed domain.WikiListCardsResp
		if !decodePromptResult(result, &listed) {
			return
		}
		for _, card := range listed.Cards {
			register(card, store)
		}
	}

	// Collect workspace first so canonical builtin entries register before any
	// project residue (same class → first wins), then the project store, which
	// also surfaces external ext-skill: cards via its external providers.
	collect("workspace")
	collect("project")

	items := make([]domain.SkillManifest, 0, len(best))
	for _, e := range best {
		items = append(items, e.manifest)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// parseFrontmatterString extracts a single string value from YAML-like
// frontmatter (---\nkey: value\n---) without loading the full card body.
func parseFrontmatterString(raw, key string) string {
	if !strings.HasPrefix(raw, "---") {
		return ""
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(raw[3:3+end], "\n") {
		k, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(value), "\"[]")
		}
	}
	return ""
}

// parseFrontmatterList extracts a YAML list value from frontmatter, supporting
// both block style ("- item" lines) and inline style ("[a, b, c]").
func parseFrontmatterList(raw, key string) []string {
	if !strings.HasPrefix(raw, "---") {
		return nil
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return nil
	}
	fm := raw[3 : 3+end]
	lines := strings.Split(fm, "\n")
	for i, line := range lines {
		k, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			// Inline list: [a, b, c]
			inner := strings.Trim(value, "[]")
			if inner == value {
				return nil
			}
			parts := strings.Split(inner, ",")
			result := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(strings.Trim(p, "\""))
				if p != "" {
					result = append(result, p)
				}
			}
			return result
		}
		// Block list: collect following "- item" lines
		result := make([]string, 0)
		for j := i + 1; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(trimmed, "- ") {
				break
			}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			item = strings.Trim(item, "\"")
			if item != "" {
				result = append(result, item)
			}
		}
		return result
	}
	return nil
}

func (a *Actor) syncDefaultCardRefs(cfg domain.AgentKindConfig) bool {
	wanted := make(map[string]gen.CardRef, len(cfg.DefaultCardRefs))
	for _, ref := range cfg.DefaultCardRefs {
		if ref.ID != "" {
			wanted[ref.ID] = ref
		}
	}
	changed := false
	kept := make([]domain.AgentComponentMount, 0, len(a.ComponentMounts))
	for _, mount := range a.ComponentMounts {
		if mount.Scope == skillScopeKindConfig && mount.Kind == "card-ref" {
			if _, ok := wanted[mount.CardID]; !ok {
				changed = true
				continue
			}
		}
		kept = append(kept, mount)
	}
	a.ComponentMounts = kept
	existing := make(map[string]bool, len(a.ComponentMounts))
	for _, mount := range a.ComponentMounts {
		existing[mount.CardID] = true
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for cardID := range wanted {
		mountID := cardID
		if existing[mountID] {
			continue
		}
		a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
			MountID: ctxNewMountID(a), CardID: mountID, Kind: "card-ref",
			Enabled: true, Scope: skillScopeKindConfig, Title: cardID,
			MountedAt: now, UpdatedAt: now,
		})
		existing[mountID] = true
		changed = true
	}
	if changed {
		a.ComponentRevision++
		a.componentSnapshot.Store(nil)
		a.resolvedInstructions.Store(nil)
	}
	return changed
}

func ctxNewMountID(a *Actor) string {
	return fmt.Sprintf("card-mount-%d", len(a.ComponentMounts)+1)
}

// syncSkillMounts. Only entries carrying this scope are reconciled; user-mounted
// skills (Scope == "user" etc.) are preserved untouched.
const skillScopeKindConfig = "kind-config"

// syncSkillMounts reconciles this agent's ComponentMounts against all skill
// cards in the parent project's CardStore. It is called at startup and turn
// start so newly created or removed project skills take effect without a restart.
//
// Reconciliation rules:
//   - wanted := all project skill cards, mapped to "skill:<id>" card IDs.
//   - Mounts with Scope == "kind-config" and a skill CardID ("skill:*" or
//     "ext-skill:*") are treated as managed. Missing wanted skills are appended;
//     stale managed mounts whose skill is no longer wanted are removed.
//   - Mounts with any other scope (e.g. user-mounted "skill:other") are
//     preserved verbatim so manual mounts survive re-sync.
//
// On success it returns true if any mount was added or removed (callers may use
// the signal to refresh downstream caches). Errors from the manifest fetch are
// returned without mutating state.
func (a *Actor) syncSkillMounts(ctx actor.Context, cfg domain.AgentKindConfig) (bool, error) {
	planner := ctx.Planner()
	if planner == nil {
		return false, nil
	}
	items, err := a.fetchFilteredSkillManifest(ctx, planner, cfg)
	if err != nil {
		return false, err
	}
	wanted := make(map[string]struct{}, len(items))
	for _, m := range items {
		if m.ID == "" {
			continue
		}
		wanted[skillCardIDFor(m.ID)] = struct{}{}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	changed := false

	// Pass 1: drop managed mounts whose skill is no longer wanted.
	kept := make([]domain.AgentComponentMount, 0, len(a.ComponentMounts))
	for _, m := range a.ComponentMounts {
		isManaged := m.Scope == skillScopeKindConfig && isSkillCardID(m.CardID)
		if isManaged {
			if _, ok := wanted[m.CardID]; !ok {
				changed = true
				continue // remove
			}
		}
		kept = append(kept, m)
	}
	a.ComponentMounts = kept

	// Pass 2: append wanted skills that are not yet mounted (under any scope).
	existing := make(map[string]bool, len(a.ComponentMounts))
	for _, m := range a.ComponentMounts {
		existing[m.CardID] = true
	}
	for cardID := range wanted {
		if existing[cardID] {
			continue
		}
		a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
			MountID:   ctx.NewID().String(),
			CardID:    cardID,
			Kind:      "skill",
			Title:     skillMountTitle(cardID),
			Enabled:   true,
			Scope:     skillScopeKindConfig,
			MountedAt: now,
			UpdatedAt: now,
		})
		existing[cardID] = true
		changed = true
	}

	if !changed {
		return false, nil
	}
	a.ComponentRevision++
	a.componentSnapshot.Store(nil)
	a.resolvedInstructions.Store(nil)
	a.saveMailbox(ctx)
	return true, nil
}

// skillManifestSummary builds the dynamic skill usage system prompt fragment.
// Format:
//   - One-line usage instruction
//   - Skill names array (compact)
//   - Detailed list (skillId, description, params)
func (a *Actor) skillManifestSummary(ctx actor.Context, planner actor.Planner, cfg domain.AgentKindConfig) string {
	items, err := a.fetchFilteredSkillManifest(ctx, planner, cfg)
	if err != nil || len(items) == 0 {
		return ""
	}
	const budget = 4096
	var b strings.Builder
	b.WriteString("Use mounted skills via agent_skill_use(SkillId) when their workflow applies.\n")

	// Compact names-only array.
	names := make([]string, 0, len(items))
	for _, m := range items {
		names = append(names, m.ID)
	}
	b.WriteString(fmt.Sprintf("Skills: %s\n\n", strings.Join(names, ", ")))

	// Detailed list.
	b.WriteString("Skill details:\n")
	used := b.Len()
	for _, m := range items {
		line := fmt.Sprintf("- %s", m.ID)
		if m.Description != "" {
			line += fmt.Sprintf(" — %s", m.Description)
		}
		if len(m.Params) > 0 {
			line += fmt.Sprintf(" (params: %s)", strings.Join(m.Params, ", "))
		}
		line += "\n"
		if used+len(line) > budget {
			b.WriteString("- ...\n")
			break
		}
		b.WriteString(line)
		used += len(line)
	}
	return strings.TrimSpace(b.String())
}
