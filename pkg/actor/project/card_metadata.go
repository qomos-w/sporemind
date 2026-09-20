package project

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func cardMetadata(card *CardRecord) (cardType, source, storage, visibility string, protected, editable, deletable bool) {
	cardType = strings.TrimSpace(card.Type)
	if cardType == "" {
		cardType = cardDataString(card, "type")
	}
	cardType = normalizeCardType(cardType)

	source = cardDataString(card, "source")
	if source == "" {
		source = "project"
		if strings.HasPrefix(card.Title, "__builtin_") || strings.HasPrefix(card.Title, "wiki:") {
			source = "builtin"
		}
	}
	storage = cardDataString(card, "storage")
	if storage == "" {
		storage = "cardstore"
	}
	visibility = cardDataString(card, "visibility")
	if visibility == "" {
		if storage == "runtime" {
			visibility = "runtime"
		} else if cardDataString(card, "componentKind") != "" || cardType == "prompt" || cardType == "skill" {
			visibility = "component"
		} else {
			visibility = "wiki"
		}
	}

	protected = cardDataBool(card, "protected") || source == "builtin"
	editable = !protected
	if value, ok := card.Data["editable"].(bool); ok {
		editable = value
	}
	deletable = !protected
	if value, ok := card.Data["deletable"].(bool); ok {
		deletable = value
	}
	return
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

// canonicalCardTypes is the authoritative set of card types after the
// monocard type-driven refactor. plan/goal merged into task; reminder merged
// into scheduler; note/comment/knowledge merged into wiki.
// normalizeCardType maps legacy type values to the canonical type set and
// defaults the empty string to "wiki". Unknown values are returned unchanged
// so validation can reject them.
func normalizeCardType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "":
		return "wiki"
	case "note", "comment", "knowledge":
		return "wiki"
	case "reminder":
		return "scheduler"
	case "plan", "goal", "kanban-task":
		return "task"
	}
	return strings.ToLower(strings.TrimSpace(t))
}

// isCanonicalCardType reports whether t is a member of the canonical type set.
func isCanonicalCardType(t string) bool {
	_, ok := canonicalCardTypes[normalizeCardType(t)]
	return ok
}

// legacyCardTypeFromTags derives a type from tags for cards that do not yet
// have a canonical top-level type field. It is kept during the migration
// period and will be removed once all historical cards have been migrated.
func legacyCardTypeFromTags(card *CardRecord) string {
	for _, tag := range card.Tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "prompt", "skill", "concept", "goal", "plan", "callable", "capability_module":
			return strings.ToLower(strings.TrimSpace(tag))
		}
	}
	return ""
}

// normalizeExternalCardMetadata applies the same wire metadata contract to a
// card supplied by an external provider.
func normalizeExternalCardMetadata(item *domain.MonoCardListItem) {
	if item.Type == "" {
		item.Type = stringData(item.Data, "type")
	}
	if item.Type == "" {
		item.Type = "wiki"
	}
	if item.Source == "" {
		item.Source = stringData(item.Data, "source")
	}
	if item.Source == "" {
		item.Source = "external"
	}
	if item.Storage == "" {
		item.Storage = "external"
	}
	if item.Visibility == "" {
		item.Visibility = "component"
	}
	if !item.Protected {
		item.Protected = boolData(item.Data, "protected")
	}
	item.Editable = boolDataDefault(item.Data, "editable", !item.Protected)
	item.Deletable = boolDataDefault(item.Data, "deletable", !item.Protected)
}

func stringData(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func boolData(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	value, _ := data[key].(bool)
	return value
}

func boolDataDefault(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	value, ok := data[key].(bool)
	if !ok {
		return fallback
	}
	return value
}
