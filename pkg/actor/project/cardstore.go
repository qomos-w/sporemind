package project

import (
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/timeutil"
)

// Sentinel errors returned by CardStore implementations.
// CardRecord is the canonical structured representation of a mono card.
// It is the contract type between the wiki actor and the storage layer.
// Title is the unique identity; there is no separate ID field.
type CardRecord struct {
	Title      string
	Type       string
	Tags       []string
	List       []string
	Created    string
	Modified   string
	Due        string
	Priority   string
	Status     string
	Parent     string
	Data       map[string]any
	Standalone bool
	Body       string
	Raw        string

	// Repaired marks a record whose non-canonical status was fixed in memory
	// during decode (see fsCardStore.Get). Read paths must never persist this
	// repair themselves: a Save triggered inside Get races concurrent writers
	// and silently reverts fresher content (lost update). Instead the caller
	// decides whether to persist; any later Get→mutate→Save flow lands the
	// normalized form as part of its own legitimate write. The flag is only
	// set on returned copies, never on cached records.
	Repaired bool
}

// CardStore abstracts mono card persistence.
// Implementations: fsCardStore (.md files, today), dbCardStore (database, future).
// All methods use title as the unique identity key.
type CardStore interface {
	Get(title string) (*CardRecord, error)
	List() ([]*CardRecord, error)
	Save(card *CardRecord) error
	Delete(title string) error
}

// dedupStrings returns a copy of values with duplicates removed, preserving the
// order of first occurrence. Comparison is case-sensitive. Returns the input
// slice unchanged when it has zero or one elements.
func dedupStrings(values []string) []string {
	if len(values) <= 1 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

// cardToListItem maps a CardRecord to the wire-protocol list item type.
func cardToListItem(card *CardRecord) domain.MonoCardListItem {
	cardType, source, storage, visibility, protected, editable, deletable := cardMetadata(card)
	item := domain.MonoCardListItem{
		ID:         card.Title,
		Type:       cardType,
		Source:     source,
		Storage:    storage,
		Visibility: visibility,
		Tags:       dedupStrings(card.Tags),
		List:       card.List,
		Created:    timeutil.ToLocalISO(card.Created),
		Modified:   timeutil.ToLocalISO(card.Modified),
		Protected:  protected,
		Editable:   editable,
		Deletable:  deletable,
		Due:        timeutil.ToLocalISO(card.Due),
		Priority:   card.Priority,
		Status:     card.Status,
		Parent:     card.Parent,
		Data:       card.Data,
		Standalone: card.Standalone,
		Raw:        card.Raw,
	}
	return item
}
