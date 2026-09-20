package cardcompiler

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

type projectCardReaderFake map[string]gen.MonoCard

func (s projectCardReaderFake) GetProjectCard(id string) (gen.MonoCard, bool) {
	card, ok := s[id]
	return card, ok
}

func TestProjectCardStoreAdapterProjectsRecordsForCompiler(t *testing.T) {
	adapter := NewProjectCardStoreAdapter(projectCardReaderFake{
		"base": {
			ID: "base", Type: "prompt",
		},
		"project:skill": {
			ID: "project:skill", Type: "skill", Version: 2,
			Dependencies:        []gen.CardRef{{ID: "base"}},
			PromptContributions: []gen.CardPromptContribution{{ID: "body", Text: "Use the project files."}},
		},
	})
	card, ok := adapter.GetCard("project:skill")
	if !ok || card.ID != "project:skill" || card.Type != "skill" || card.Version != 2 || len(card.Dependencies) != 1 {
		t.Fatalf("unexpected projected card: %+v, ok=%v", card, ok)
	}
	cards, err := (Resolver{Store: adapter}).Resolve([]gen.CardRef{{ID: "project:skill"}})
	if err != nil || len(cards) != 2 || cards[1].ID != "project:skill" {
		t.Fatalf("compiler could not resolve project card: cards=%+v err=%v", cards, err)
	}
}

func TestProjectCardStoreAdapterIsReadOnlyAndRejectsInvalidReaders(t *testing.T) {
	adapter := NewProjectCardStoreAdapter(projectCardReaderFake{"project:prompt": {ID: "project:prompt", Type: "prompt"}})
	if _, ok := adapter.GetCard("missing"); ok {
		t.Fatal("missing project card must not resolve")
	}
	if _, ok := adapter.GetCard(""); ok {
		t.Fatal("empty project card ID must not resolve")
	}
	if _, ok := NewProjectCardStoreAdapter(nil).GetCard("project:prompt"); ok {
		t.Fatal("nil project reader must not resolve")
	}
}

func TestProjectCardReaderFuncProvidesActorHandlerSeam(t *testing.T) {
	adapter := NewProjectCardStoreAdapter(ProjectCardReaderFunc(func(id string) (gen.MonoCard, bool) {
		return gen.MonoCard{ID: id, Type: "prompt"}, id == "project:prompt"
	}))
	card, ok := adapter.GetCard("project:prompt")
	if !ok || card.ID != "project:prompt" {
		t.Fatalf("handler seam did not return projected card: %+v, %v", card, ok)
	}
}

func TestProjectCardStoreAdapterBatch(t *testing.T) {
	// Reader without the batch seam must report batching unavailable.
	single := NewProjectCardStoreAdapter(projectCardReaderFake{"a": {ID: "a", Type: "prompt"}})
	if batch := single.(BatchCardStore).GetCards([]string{"a"}); batch != nil {
		t.Fatalf("non-batch reader must return nil, got %v", batch)
	}

	reader := &batchCardReaderFake{projectCardReaderFake: projectCardReaderFake{"a": {ID: "a", Type: "prompt"}}}
	adapter := NewProjectCardStoreAdapter(reader)
	batch := adapter.(BatchCardStore).GetCards([]string{"a", "missing"})
	if batch == nil || len(batch) != 1 || batch["a"].ID != "a" {
		t.Fatalf("batch fetch mismatch: %+v", batch)
	}
	if len(reader.got) != 2 {
		t.Fatalf("batch ids forwarded = %v", reader.got)
	}
}

type batchCardReaderFake struct {
	projectCardReaderFake
	got []string
}

func (b *batchCardReaderFake) GetProjectCards(ids []string) map[string]gen.MonoCard {
	b.got = append(b.got, ids...)
	out := map[string]gen.MonoCard{}
	for _, id := range ids {
		if card, ok := b.projectCardReaderFake[id]; ok {
			out[id] = card
		}
	}
	return out
}
