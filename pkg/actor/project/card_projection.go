package project

import (
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// CardProjection is the read-only card view consumed by agent contexts. Ref is
// always the canonical card reference; callers must not derive another ID.
type CardProjection struct {
	Ref  gen.CardRef
	Card gen.MonoCard
}

// ReadOnlyCardProjector exposes card records without persistence operations.
type ReadOnlyCardProjector interface {
	Project(*CardRecord) (CardProjection, error)
}

type cardProjector struct{}

// NewCardProjector returns the project-store to MonoCard projection.
func NewCardProjector() ReadOnlyCardProjector { return cardProjector{} }

func (cardProjector) Project(record *CardRecord) (CardProjection, error) {
	if record == nil || strings.TrimSpace(record.Title) == "" {
		return CardProjection{}, fmt.Errorf("card projection: card id is required")
	}
	kind := strings.TrimSpace(record.Type)
	if kind == "" {
		kind = strings.TrimSpace(cardDataString(record, "type"))
	}
	if kind == "" {
		kind = legacyCardTypeFromTags(record)
	}
	if kind == "" {
		return CardProjection{}, fmt.Errorf("card projection %q: type is required", record.Title)
	}
	kind = normalizeCardType(kind)
	version := cardDataInt(record, "componentVersion")
	ref := gen.CardRef{ID: record.Title, Version: int32(version), Source: cardDataString(record, "source"), Scope: cardDataString(record, "scope")}
	card := gen.MonoCard{ID: record.Title, Type: kind, Version: int32(version), Source: ref.Source, Protected: cardDataBool(record, "protected"), OnDemand: cardDataBool(record, "onDemand"), Body: record.Body}
	if ref.Scope == "" {
		ref.Scope = "project"
	}
	for _, dependency := range cardDataStrings(record, "requires") {
		card.Dependencies = append(card.Dependencies, gen.CardRef{ID: dependency})
	}
	if record.Body != "" {
		section := cardDataString(record, "placement")
		if strings.HasPrefix(record.Title, "prompt:profile:") {
			section = "role"
		}
		if section == "" || section == "prompt" {
			section = "policy"
		}
		card.PromptContributions = append(card.PromptContributions, gen.CardPromptContribution{ID: record.Title + ":body", Section: section, Text: record.Body, Priority: int32(cardDataInt(record, "priority"))})
	}
	for _, callable := range cardDataStrings(record, "tools") {
		card.ToolContributions = append(card.ToolContributions, gen.CardToolContribution{ID: callable, CallableID: callable, Priority: int32(cardDataInt(record, "priority"))})
	}
	if callable := cardDataString(record, "callable"); callable != "" {
		card.ToolContributions = append(card.ToolContributions, gen.CardToolContribution{ID: callable, CallableID: callable, Priority: int32(cardDataInt(record, "priority"))})
	}
	return CardProjection{Ref: ref, Card: card}, nil
}

// GetProjectCard returns a projected project card through a read-only seam.
// It does not expose the actor's mutable CardStore or persistence operations.
func (a *Actor) GetProjectCard(id string) (gen.MonoCard, bool) {
	if a == nil || a.store == nil || id == "" {
		return gen.MonoCard{}, false
	}
	record, err := a.store.Get(id)
	if err != nil || record == nil {
		return gen.MonoCard{}, false
	}
	projection, err := ProjectCardRecord(record)
	if err != nil {
		return gen.MonoCard{}, false
	}
	return projection.Card, true
}

// ProjectCardRecord is the convenience form of NewCardProjector().Project.
func ProjectCardRecord(record *CardRecord) (CardProjection, error) {
	return NewCardProjector().Project(record)
}
