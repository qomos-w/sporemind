package project

import (
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

const componentTag = "component"

// componentDescriptorFromCard converts a MonoCard into a declarative component
// descriptor. Callable schemas remain owned by the registered callable.
func componentDescriptorFromCard(card *CardRecord) (domain.ComponentDescriptor, bool, error) {
	if card == nil || !hasCardTag(card.Tags, componentTag) {
		return domain.ComponentDescriptor{}, false, nil
	}
	// Virtual mount nodes (__builtin_prompt__ etc.) carry the component tag
	// only as a hierarchy-parenting device; they are system aggregators, not
	// mountable components, and have no card type by design.
	switch cardDataString(card, "builtinRole") {
	case "mount", "aggregator":
		return domain.ComponentDescriptor{}, false, nil
	}
	projection, err := ProjectCardRecord(card)
	if err != nil {
		return domain.ComponentDescriptor{}, true, err
	}
	kind := projection.Card.Type
	ref := domain.ComponentRef{CardID: projection.Card.ID, Kind: kind, Source: projection.Card.Source}
	if projection.Card.Version > 0 {
		ref.Version = projection.Card.Version
	}
	// Prefer the card's declared display title (data.title) over the raw card
	// ID so composer badges show "filesystem" instead of "mcp:srv-0".
	title := projection.Card.ID
	if declared := cardDataString(card, "title"); declared != "" {
		title = declared
	}
	descriptor := domain.ComponentDescriptor{
		Ref:          ref,
		Title:        title,
		Icon:         cardDataString(card, "icon"),
		Visual:       componentVisualFromCardData(card.Data),
		Protected:    projection.Card.Protected,
		Prompts:      []domain.ComponentPromptContribution{},
		Dependencies: []domain.ComponentDependency{},
		Tools:        []domain.ComponentToolContribution{},
	}
	for _, contribution := range projection.Card.PromptContributions {
		placement := contribution.Section
		if placement == "" {
			placement = cardDataString(card, "placement")
		}
		descriptor.Prompts = append(descriptor.Prompts, domain.ComponentPromptContribution{
			ID: contribution.ID, CardID: projection.Card.ID, Title: projection.Card.ID,
			Text: contribution.Text, Placement: placement, Priority: contribution.Priority,
		})
	}
	for _, dependency := range projection.Card.Dependencies {
		descriptor.Dependencies = append(descriptor.Dependencies, domain.ComponentDependency{CardID: dependency.ID, Required: true})
	}
	for _, contribution := range projection.Card.ToolContributions {
		descriptor.Tools = append(descriptor.Tools, domain.ComponentToolContribution{
			ID: contribution.ID, CardID: projection.Card.ID, CallableID: contribution.CallableID,
			Usage: cardDataString(card, "usage"), Constraints: cardDataString(card, "constraints"),
		})
	}
	return descriptor, true, nil
}

func hasCardTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), wanted) {
			return true
		}
	}
	return false
}

// componentVisualFromCardData extracts a data.visual block (icon-library
// presentation hints) from a card's persisted data into the wire ComponentVisual
// so badges and the omnibox render colored lucide icons.
func componentVisualFromCardData(data map[string]any) *domain.ComponentVisual {
	if data == nil {
		return nil
	}
	visual, ok := data["visual"].(map[string]any)
	if !ok {
		return nil
	}
	str := func(key string) string {
		if s, ok := visual[key].(string); ok {
			return s
		}
		return ""
	}
	cv := &domain.ComponentVisual{}
	set := false
	if s := str("icon"); s != "" {
		cv.Icon, set = s, true
	}
	if s := str("accent"); s != "" {
		cv.Accent, set = s, true
	}
	if s := str("emphasis"); s != "" {
		cv.Emphasis, set = s, true
	}
	if s := str("color"); s != "" {
		cv.Color, set = s, true
	}
	if s := str("background"); s != "" {
		cv.Background, set = s, true
	}
	if s := str("border"); s != "" {
		cv.Border, set = s, true
	}
	switch n := visual["size"].(type) {
	case int:
		cv.Size, set = int32(n), true
	case int32:
		cv.Size, set = n, true
	case int64:
		cv.Size, set = int32(n), true
	case float64:
		cv.Size, set = int32(n), true
	}
	if !set {
		return nil
	}
	return cv
}

func cardDataString(card *CardRecord, key string) string {
	if card == nil || card.Data == nil {
		return ""
	}
	value, _ := card.Data[key].(string)
	return strings.TrimSpace(value)
}

func cardDataBool(card *CardRecord, key string) bool {
	if card == nil || card.Data == nil {
		return false
	}
	switch value := card.Data[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func cardDataInt(card *CardRecord, key string) int {
	if card == nil || card.Data == nil {
		return 0
	}
	switch value := card.Data[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		// The inline data-block parser stores all scalar values as strings
		// (see parseDataBlock → unquote), so unquoted YAML integers like
		// run_count: 3 round-trip as "3". Parse them here so callers see a
		// real int without every reader needing strconv.
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

func cardDataStrings(card *CardRecord, key string) []string {
	if card == nil || card.Data == nil {
		return nil
	}
	value, _ := card.Data[key].([]string)
	if value != nil {
		return value
	}
	if text, ok := card.Data[key].(string); ok {
		text = strings.Trim(strings.TrimSpace(text), "[]")
		if text == "" {
			return nil
		}
		items := strings.Split(text, ",")
		out := make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(strings.Trim(item, "\\\"'"))
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	items, _ := card.Data[key].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

// CardDataBool exports cardDataBool for cross-package callers (workspace, tests).
func CardDataBool(card *CardRecord, key string) bool {
	return cardDataBool(card, key)
}

// CardDataString exports cardDataString for cross-package callers.
func CardDataString(card *CardRecord, key string) string {
	return cardDataString(card, key)
}

// CardDataMap returns a nested map value from card.Data, or nil if absent or
// not a map. Exported for workspace sub_map executor configuration parsing.
func CardDataMap(card *CardRecord, key string) map[string]any {
	if card == nil || card.Data == nil {
		return nil
	}
	m, _ := card.Data[key].(map[string]any)
	return m
}
