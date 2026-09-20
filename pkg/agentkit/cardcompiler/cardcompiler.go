package cardcompiler

import (
	"fmt"
	"sort"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// CardStore supplies cards by their schema-defined ID.
type CardStore interface {
	GetCard(id string) (gen.MonoCard, bool)
}

// BatchCardStore is implemented by stores that can fetch a known id set in
// one round trip (e.g. one IPC call instead of N sequential ones). GetCards
// returns nil when the store cannot batch, in which case callers must fall
// back to per-id GetCard.
type BatchCardStore interface {
	GetCards(ids []string) map[string]gen.MonoCard
}

type CardMap map[string]gen.MonoCard

func (m CardMap) GetCard(id string) (gen.MonoCard, bool) { c, ok := m[id]; return c, ok }

type ResolveOptions struct {
	IncludeOnDemand  bool
	ExcludeKnowledge bool
	ExcludeConcept   bool
}

type Resolver struct {
	Store   CardStore
	Options ResolveOptions
}

func (r Resolver) Resolve(refs []gen.CardRef) ([]gen.MonoCard, error) {
	cards, _, err := r.resolve(refs)
	return cards, err
}

func (r Resolver) resolve(refs []gen.CardRef) ([]gen.MonoCard, []ContextFilterRecord, error) {
	seen := map[string]bool{}
	active := map[string]bool{}
	var out []gen.MonoCard
	var filtered []ContextFilterRecord
	var visit func(gen.CardRef) error
	visit = func(ref gen.CardRef) error {
		if ref.Disabled || ref.ID == "" {
			if ref.ID != "" && ref.Disabled {
				filtered = append(filtered, ContextFilterRecord{ID: ref.ID, Version: ref.Version, Source: ref.Source, Reason: "disabled"})
			}
			return nil
		}
		if seen[ref.ID] {
			return nil
		}
		if active[ref.ID] {
			return fmt.Errorf("card dependency cycle at %q", ref.ID)
		}
		card, ok := r.Store.GetCard(ref.ID)
		if !ok {
			return fmt.Errorf("card %q not found", ref.ID)
		}
		if card.OnDemand && !r.Options.IncludeOnDemand {
			filtered = append(filtered, ContextFilterRecord{ID: card.ID, Source: card.Source, Version: card.Version, Reason: "on_demand"})
			return nil
		}
		if card.Type == "knowledge" && r.Options.ExcludeKnowledge {
			filtered = append(filtered, ContextFilterRecord{ID: card.ID, Source: card.Source, Version: card.Version, Reason: "knowledge_excluded"})
			return nil
		}
		if card.Type == "concept" && r.Options.ExcludeConcept {
			filtered = append(filtered, ContextFilterRecord{ID: card.ID, Source: card.Source, Version: card.Version, Reason: "concept_excluded"})
			return nil
		}
		active[ref.ID] = true
		deps := append([]gen.CardRef(nil), card.Dependencies...)
		sort.SliceStable(deps, func(i, j int) bool {
			if deps[i].Order != deps[j].Order {
				return deps[i].Order < deps[j].Order
			}
			return deps[i].ID < deps[j].ID
		})
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(active, ref.ID)
		seen[ref.ID] = true
		out = append(out, card)
		return nil
	}
	refs = append([]gen.CardRef(nil), refs...)
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Order != refs[j].Order {
			return refs[i].Order < refs[j].Order
		}
		return refs[i].ID < refs[j].ID
	})
	for _, ref := range refs {
		if err := visit(ref); err != nil {
			return nil, filtered, err
		}
	}
	return out, filtered, nil
}

type CompileOptions struct {
	ResolveOptions
	// ToolSpecs maps a CallableID to one or more tool specs. Multiple specs
	// under the same CallableID are fork aliases (fork_explore/fork_review/
	// fork_general all share workspace.agent_spawn_by_type) — all are emitted so the LLM
	// receives every intent-revealing name.
	ToolSpecs         map[string][]gen.ToolSpec
	ToolFilterReasons map[string]string
	Messages          []gen.ChatMessage
}

type CompileResult struct {
	SystemBlocks []gen.SystemContentBlock
	ToolSpec     []gen.ToolSpec
	Messages     []gen.ChatMessage
	ContextSnapshot
}

type ContextCardRecord struct {
	ID       string
	Source   string
	Version  int32
	Included bool
	Reason   string
}

type ContextSectionRecord struct {
	ID             string
	Source         string
	Version        int32
	Section        string
	ContributionID string
}

type ContextToolRecord struct {
	ID         string
	Source     string
	Version    int32
	CallableID string
	Included   bool
	Reason     string
}

type ContextFilterRecord struct {
	ID      string
	Source  string
	Version int32
	Reason  string
}

type ContextSnapshot struct {
	SystemBlocks []gen.SystemContentBlock
	ToolSpec     []gen.ToolSpec
	Messages     []gen.ChatMessage
	Cards        []ContextCardRecord
	Sections     []ContextSectionRecord
	Tools        []ContextToolRecord
	Filtered     []ContextFilterRecord
}

func Compile(cards []gen.MonoCard, opts CompileOptions) CompileResult {
	result := CompileResult{}
	seenPrompts := map[string]bool{}
	seenTools := map[string]bool{}
	type promptWithCard struct {
		cardID       string
		cardSource   string
		cardVersion  int32
		contribution gen.CardPromptContribution
	}
	type toolWithCard struct {
		cardID      string
		cardSource  string
		cardVersion int32
		tool        gen.CardToolContribution
	}
	var prompts []promptWithCard
	var tools []toolWithCard
	for _, card := range cards {
		result.Cards = append(result.Cards, ContextCardRecord{ID: card.ID, Source: card.Source, Version: card.Version, Included: true})
		for _, p := range card.PromptContributions {
			key := p.ID
			if key == "" {
				key = card.ID + "\x00" + p.Section + "\x00" + p.Text
			}
			if !seenPrompts[key] {
				seenPrompts[key] = true
				prompts = append(prompts, promptWithCard{cardID: card.ID, cardSource: card.Source, cardVersion: card.Version, contribution: p})
			}
		}
		for _, t := range card.ToolContributions {
			key := t.CallableID
			if key != "" && !seenTools[key] {
				seenTools[key] = true
				tools = append(tools, toolWithCard{cardID: card.ID, cardSource: card.Source, cardVersion: card.Version, tool: t})
				reason := "registry_missing"
				if filterReason := opts.ToolFilterReasons[t.CallableID]; filterReason != "" {
					reason = filterReason
				}
				result.Tools = append(result.Tools, ContextToolRecord{ID: card.ID, Source: card.Source, Version: card.Version, CallableID: t.CallableID, Included: false, Reason: reason})
			}
		}
	}
	sort.SliceStable(prompts, func(i, j int) bool {
		pi, pj := prompts[i].contribution, prompts[j].contribution
		si, ok := sectionOrder[pi.Section]
		if !ok {
			si = len(sectionOrder)
		}
		sj, ok := sectionOrder[pj.Section]
		if !ok {
			sj = len(sectionOrder)
		}
		if si != sj {
			return si < sj
		}
		if pi.Priority != pj.Priority {
			return pi.Priority < pj.Priority
		}
		if prompts[i].cardID != prompts[j].cardID {
			return prompts[i].cardID < prompts[j].cardID
		}
		return pi.ID < pj.ID
	})
	for _, p := range prompts {
		result.Sections = append(result.Sections, ContextSectionRecord{ID: p.cardID, Source: p.cardSource, Version: p.cardVersion, Section: p.contribution.Section, ContributionID: p.contribution.ID})
		result.SystemBlocks = append(result.SystemBlocks, gen.SystemContentBlock{Type: "text", Text: p.contribution.Text})
	}
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].tool.Priority != tools[j].tool.Priority {
			return tools[i].tool.Priority < tools[j].tool.Priority
		}
		return tools[i].tool.CallableID < tools[j].tool.CallableID
	})
	for _, t := range tools {
		specs, ok := opts.ToolSpecs[t.tool.CallableID]
		if !ok {
			continue
		}
		for _, spec := range specs {
			result.ToolSpec = append(result.ToolSpec, spec)
		}
		for i := range result.Tools {
			if result.Tools[i].CallableID == t.tool.CallableID {
				result.Tools[i].Included = true
				result.Tools[i].Reason = "included"
				break
			}
		}
	}
	result.Messages = append([]gen.ChatMessage(nil), opts.Messages...)
	result.ContextSnapshot = ContextSnapshot{SystemBlocks: result.SystemBlocks, ToolSpec: result.ToolSpec, Messages: result.Messages, Cards: result.Cards, Sections: result.Sections, Tools: result.Tools}
	return result
}

func (r Resolver) Compile(refs []gen.CardRef, opts CompileOptions) (CompileResult, error) {
	cards, filtered, err := (Resolver{Store: r.Store, Options: opts.ResolveOptions}).resolve(refs)
	if err != nil {
		return CompileResult{}, err
	}
	result := Compile(cards, opts)
	result.Filtered = filtered
	result.ContextSnapshot.Filtered = filtered
	return result, nil
}
