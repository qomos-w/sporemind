package agent

import (
	"strings"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// systemPromptParts collects all text parts that make up the system prompt,
// in canonical order: instruction Base, instruction Resolved, then hot
// context blocks. Empty hot-context blocks are skipped; empty instruction
// entries are preserved as-is (they are already filtered upstream by
// resolveInstructions, but the guard in assembleSystemPrompt handles them).
//
// When inst is nil and hotContext is non-empty, only hot context parts are
// returned.
func systemPromptParts(inst *domain.CompiledInstructions, hotContext []domain.ContentBlock) []string {
	if inst == nil && len(hotContext) == 0 {
		return nil
	}
	var parts []string
	if inst != nil {
		parts = make([]string, 0, len(inst.Base)+len(inst.Resolved)+len(hotContext))
		parts = append(parts, inst.Base...)
		parts = append(parts, inst.Resolved...)
	} else {
		parts = make([]string, 0, len(hotContext))
	}
	for _, block := range hotContext {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return parts
}

// assembleSystemPrompt builds the LLM-facing system prompt string and the
// corresponding SystemContentBlock slice from compiled instructions and hot
// context. This is the single source of truth for how the system prompt is
// assembled — every dispatch, token probe, and inspector view must route
// through here.
//
// The last non-empty block is marked CacheControl="ephemeral" to support
// provider caching strategies (e.g. Anthropic prompt caching).
func assembleSystemPrompt(inst *domain.CompiledInstructions, hotContext []domain.ContentBlock) (string, []domain.SystemContentBlock) {
	parts := systemPromptParts(inst, hotContext)
	if len(parts) == 0 {
		return "", nil
	}
	prompt := strings.Join(parts, "\n\n")
	var blocks []domain.SystemContentBlock
	for _, p := range parts {
		if p != "" {
			blocks = append(blocks, domain.SystemContentBlock{Type: "text", Text: p})
		}
	}
	if len(blocks) > 0 {
		blocks[len(blocks)-1].CacheControl = "ephemeral"
	}
	return prompt, blocks
}

// resolveFullHotContext returns the dynamic context blocks appended to the
// system prompt. The goal's full formatted block (## Active Goal …) is
// injected here via resolveHotContext → buildGoalBlock; goal is NOT in
// activeContextCards to avoid duplication. The active plan is not injected
// into the prompt at all. Worktree is compiled into
// Instructions.Resolved via activeContextCards.
//
// Memory blocks are injected in layered order around the system prompt:
//   - Ontology full body → prepended to inst.Base (leads the prompt)
//   - Experience head index → appended to inst.Resolved (system-prompt tail)
//   - Session full body → appended after hot context (post-hot)
//
// For child (forked/explore) agents, the parent-injected HotContext is used
// instead of resolveHotContext, since child agents cannot resolve context
// themselves.
func (a *Actor) resolveFullHotContext(ctx actor.Context) []domain.ContentBlock {
	if a.child.Mode && len(a.child.HotContext) > 0 {
		return append([]domain.ContentBlock(nil), a.child.HotContext...)
	}
	blocks := a.resolveHotContext(ctx)
	blocks = append(blocks, a.memoryPostHotBlocks()...)
	return blocks
}
