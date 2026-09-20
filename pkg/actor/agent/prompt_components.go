package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func (a *Actor) configuredPromptComponents(ctx actor.Context, cfg domain.AgentKindConfig) []domain.ComponentPromptContribution {
	return a.configuredPromptComponentsFiltered(ctx, cfg, nil)
}

// configuredPromptComponentsFiltered resolves agent kind config prompt refs
// via the workspace system CardStore and returns them as prompt contributions. If a ref's
// corresponding prompt card is already mounted (present in mountedPromptCards),
// it is skipped to avoid duplicate injection — the card descriptor path
// already provides its contribution.
func (a *Actor) configuredPromptComponentsFiltered(ctx actor.Context, cfg domain.AgentKindConfig, mountedPromptCards map[string]bool) []domain.ComponentPromptContribution {
	if cfg.Kind == "" || ctx.Planner() == nil {
		return nil
	}
	workspaceRef, ok := ctx.LookupService("workspace")
	if !ok {
		return nil
	}
	prompts := make([]domain.ComponentPromptContribution, 0, 1+len(cfg.SystemFragmentRefs))
	resolvedCards := make(map[string]domain.ComponentPromptContribution, 1+len(cfg.SystemFragmentRefs))
	resolveCard := func(cardID string) (domain.ComponentPromptContribution, bool) {
		if resolved, ok := resolvedCards[cardID]; ok {
			return resolved, true
		}
		resolved, ok := a.resolveWorkspacePrompt(ctx, workspaceRef, cardID)
		if ok {
			resolvedCards[cardID] = resolved
		}
		return resolved, ok
	}
	if cfg.RolePromptRef.Key != "" && !a.memoryEnabled() {
		// When memory mode is mounted, the role prompt lives as a protected
		// ontology anchor and is injected via appendMemoryBase. Skip the
		// normal role contribution to avoid duplication.
		cardID := promptComponentID(cfg.RolePromptRef)
		if resolved, ok := resolveCard(cardID); ok {
			resolved.ID = promptContributionID(cfg.RolePromptRef, "role")
			resolved.CardID = cardID
			resolved.Title = cfg.RolePromptRef.Key
			resolved.Placement = "role"
			resolved.Priority = -1000
			prompts = append(prompts, resolved)
		}
	}
	for _, promptRef := range cfg.SystemFragmentRefs {
		// SystemFragmentRefs always reference fragment cards, but the ref Kind
		// carries the UI's semantic kind vocabulary (instructions/system/git/…)
		// rather than the literal "fragment". promptComponentID maps any other
		// kind to the prompt:profile: prefix, which misses the fragment card
		// stored at prompt:fragment:<key> and silently drops the snippet.
		// Normalize the derivation ref so bare keys resolve as fragments; a Key
		// that is already a full card ID still wins via the verbatim branch.
		ref := domain.PromptRef{Kind: "fragment", Key: promptRef.Key, Placement: promptRef.Placement}
		cardID := promptComponentID(ref)
		if mountedPromptCards[cardID] {
			continue
		}
		if resolved, ok := resolveCard(cardID); ok {
			resolved.ID = promptContributionID(ref, "fragment")
			resolved.CardID = cardID
			resolved.Title = ref.Key
			resolved.Placement = fragmentPlacement(promptRef.Placement)
			prompts = append(prompts, resolved)
		}
	}
	return prompts
}

// fragmentPlacement maps a user-configured PromptRef.Placement to the
// contribution placement consumed by componentSnapshotCards. Empty/unknown
// falls back to "system" (→ policy section, after role), preserving the
// pre-placement-selection behavior. "leading" places the fragment before the
// role prompt; "trailing" places it at the end of the system prompt, ahead of
// the conversation history.
func fragmentPlacement(p string) string {
	switch p {
	case "leading", "trailing":
		return p
	default:
		return "system"
	}
}

func promptComponentID(ref domain.PromptRef) string {
	// The frontend prompt-card-store may store the full card ID (e.g.
	// "prompt:fragment:builtin-example") as the ref Key rather than a
	// bare key. Use it verbatim so the workspace card lookup matches
	// without re-deriving — sanitizePromptKey would mangle the ":" into
	// "-" and produce a non-existent card ID.
	if strings.HasPrefix(ref.Key, "prompt:fragment:") || strings.HasPrefix(ref.Key, "prompt:profile:") {
		return ref.Key
	}
	key := sanitizePromptKey(ref.Key)
	if ref.Kind == "fragment" {
		return "prompt:fragment:" + key
	}
	return "prompt:profile:" + key
}

func sanitizePromptKey(key string) string {
	var out strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '~', r == '-':
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}
	return out.String()
}

func promptContributionID(ref domain.PromptRef, placement string) string {
	return fmt.Sprintf("%s:%s:%s", promptComponentID(ref), placement, ref.Key)
}

func (a *Actor) resolveWorkspacePrompt(ctx actor.Context, workspaceRef ref.Ref, cardID string) (domain.ComponentPromptContribution, bool) {
	payload, _ := json.Marshal(domain.WikiGetCardReq{ID: cardID})
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := ctx.Planner().Call(callCtx, workspaceRef, "workspace.wiki_get_card", payload).Await()
	cancel()
	if err != nil {
		slog.Debug("resolveWorkspacePrompt: rpc error", "cardID", cardID, "err", err)
		return domain.ComponentPromptContribution{}, false
	}
	var response domain.WikiGetCardResp
	if !decodePromptResult(result, &response) || response.Raw == "" {
		slog.Debug("resolveWorkspacePrompt: empty or undecodable card response", "cardID", cardID)
		return domain.ComponentPromptContribution{}, false
	}
	body := cardBody(response.Raw)
	if body == "" {
		slog.Debug("resolveWorkspacePrompt: empty card body", "cardID", cardID)
		return domain.ComponentPromptContribution{}, false
	}
	placement := "system"
	if strings.HasPrefix(cardID, "prompt:profile:") {
		placement = "role"
	}
	return domain.ComponentPromptContribution{Text: body, Placement: placement, Priority: int32(cardPriority(response.Raw))}, true
}

func cardPriority(raw string) int {
	if !strings.HasPrefix(raw, "---") {
		return 0
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return 0
	}
	for _, line := range strings.Split(raw[3:3+end], "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.TrimSpace(key) == "priority" {
			priority, _ := strconv.Atoi(strings.TrimSpace(value))
			return priority
		}
	}
	return 0
}

func cardBody(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return strings.TrimSpace(raw)
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(raw[3+end+3:])
}

func decodePromptResult(result any, target any) bool {
	if result == nil || target == nil {
		return false
	}
	// Handle typed struct values returned directly by in-process actor calls.
	// gospore may return the handler's concrete return type (e.g. domain.PromptResolvedRef)
	// rather than a JSON byte slice or map, so we must support direct assignment.
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Ptr {
		return false
	}
	targetElem := targetValue.Elem()
	resultValue := reflect.ValueOf(result)
	if resultValue.Kind() == reflect.Ptr {
		if resultValue.IsNil() {
			return false
		}
		resultValue = resultValue.Elem()
	}
	if resultValue.IsValid() && resultValue.Type() == targetElem.Type() {
		targetElem.Set(resultValue)
		return true
	}

	var payload []byte
	switch value := result.(type) {
	case []byte:
		payload = value
	case map[string]interface{}:
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		payload = encoded
	default:
		return false
	}
	return json.Unmarshal(payload, target) == nil
}
