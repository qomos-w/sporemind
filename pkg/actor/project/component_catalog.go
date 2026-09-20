package project

import (
	"fmt"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// listComponentDescriptors resolves persisted and external MonoCards through
// the same component parser and returns a stable catalog order.
// When a persisted card has the same ID as an external card (e.g. builtin),
// the persisted version wins. The winning descriptor receives a ShadowedById
// marker so callers can see that a local override is active.
func (a *Actor) listComponentDescriptors(ctx actor.PureContext) ([]domain.ComponentDescriptor, error) {
	cards, err := a.store.List()
	if err != nil {
		return nil, fmt.Errorf("component catalog: list cards: %w", err)
	}
	byID := make(map[string]*CardRecord, len(cards))
	persistedIDs := make(map[string]bool, len(cards))
	for _, card := range cards {
		byID[card.Title] = card
		persistedIDs[card.Title] = true
	}

	// Collect which persisted cards shadow external cards (same ID).
	// This must happen during the merge pass so we know which external
	// cards were dropped in favour of a persisted version.
	shadowed := make(map[string]string) // card ID → card ID (marker presence)
	for _, provider := range a.externalProviders {
		items, err := provider.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("component catalog: list external cards: %w", err)
		}
		for _, item := range items {
			if persistedIDs[item.ID] {
				// Persisted card shadows this external card.
				shadowed[item.ID] = item.ID
				continue
			}
			if _, exists := byID[item.ID]; exists {
				continue
			}
			raw, err := provider.Get(ctx, item.ID)
			if err != nil {
				return nil, fmt.Errorf("component catalog: get %s: %w", item.ID, err)
			}
			byID[item.ID] = decodeCard(item.ID, raw)
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	descriptors := make([]domain.ComponentDescriptor, 0, len(ids))
	for _, id := range ids {
		descriptor, ok, err := componentDescriptorFromCard(byID[id])
		if err != nil {
			// One malformed persisted card (e.g. a legacy system record with
			// the component tag but no type) must not take down the whole
			// catalog — same policy as componentDescriptor's external-first
			// path, which exists precisely to survive unrelated bad cards.
			continue
		}
		if ok {
			if shadowID, exists := shadowed[id]; exists {
				descriptor.ShadowedByID = shadowID
			}
			descriptors = append(descriptors, descriptor)
		}
	}
	return descriptors, nil
}

func (a *Actor) componentDescriptor(ctx actor.PureContext, cardID string) (domain.ComponentDescriptor, error) {
	// Resolve external cards directly first. This keeps builtin components
	// available even when an unrelated persisted card is malformed and causes
	// the aggregate catalog scan to fail.
	if provider := a.externalProviderFor(cardID); provider != nil {
		raw, err := provider.Get(ctx, cardID)
		if err != nil {
			return domain.ComponentDescriptor{}, err
		}
		card := decodeCard(cardID, raw)
		descriptor, ok, err := componentDescriptorFromCard(card)
		if err != nil {
			return domain.ComponentDescriptor{}, err
		}
		if ok {
			return descriptor, nil
		}
		return domain.ComponentDescriptor{}, fmt.Errorf("component card %q is not a component", cardID)
	}

	descriptors, err := a.listComponentDescriptors(ctx)
	if err != nil {
		return domain.ComponentDescriptor{}, err
	}
	for _, descriptor := range descriptors {
		if descriptor.Ref.CardID == cardID {
			return descriptor, nil
		}
	}
	return domain.ComponentDescriptor{}, fmt.Errorf("component card %q not found", cardID)
}
