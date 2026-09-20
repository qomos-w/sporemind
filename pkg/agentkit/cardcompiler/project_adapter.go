package cardcompiler

import "github.com/qomos-w/sporemind/pkg/domain/gen"

// ProjectCardReader is the read-only seam used to expose project cards to the
// compiler without importing the project actor package.
type ProjectCardReader interface {
	GetProjectCard(id string) (gen.MonoCard, bool)
}

// ProjectBatchCardReader is the optional batch seam: one round trip for a
// known id set. Returning nil signals "cannot batch".
type ProjectBatchCardReader interface {
	GetProjectCards(ids []string) map[string]gen.MonoCard
}

type projectCardStoreAdapter struct {
	reader ProjectCardReader
}

var (
	_ CardStore      = projectCardStoreAdapter{}
	_ BatchCardStore = projectCardStoreAdapter{}
)

// NewProjectCardStoreAdapter adapts a project read-only card reader for card
// compilation. The adapter retains only the interface and no actor state.
func NewProjectCardStoreAdapter(reader ProjectCardReader) CardStore {
	return projectCardStoreAdapter{reader: reader}
}

func (a projectCardStoreAdapter) GetCard(id string) (gen.MonoCard, bool) {
	if a.reader == nil || id == "" {
		return gen.MonoCard{}, false
	}
	return a.reader.GetProjectCard(id)
}

func (a projectCardStoreAdapter) GetCards(ids []string) map[string]gen.MonoCard {
	batch, ok := a.reader.(ProjectBatchCardReader)
	if !ok {
		return nil
	}
	return batch.GetProjectCards(ids)
}

// ProjectCardReaderFunc makes the actor handler seam convenient to inject.
type ProjectCardReaderFunc func(id string) (gen.MonoCard, bool)

func (f ProjectCardReaderFunc) GetProjectCard(id string) (gen.MonoCard, bool) {
	return f(id)
}
