package persist

// Doc is one entry of a batch save: a document name and its value. Value is
// encoded with the same JSON roundtrip as Save.
type Doc struct {
	Name  string
	Value any
}

// Saver is an optional Persist capability (决策点 5): backends that can
// write multiple documents atomically implement it — SQL backends inside one
// transaction, embedded batch stores in one batch write. A failed SaveAll
// persists none of the documents (all-or-nothing).
//
// Backends without a multi-document atomic primitive (fs, mongo standalone)
// must NOT implement Saver: callers go through SaveAll, which degrades to
// sequential per-document Save in order. On failure the sequential path has
// persisted a prefix and returns the first error; the atomic path has
// persisted nothing. Callers that need all-or-nothing across backends must
// therefore only rely on Saver implementations.
type Saver interface {
	SaveAll(docs []Doc) error
}

// SaveAll saves docs through p. If p implements Saver the batch is one
// atomic operation; otherwise it degrades to sequential Save calls in order.
// docs must be non-empty and names must pass the same validation as Save
// (the atomic path validates inside its transaction; the sequential path
// fails at the offending document, leaving a persisted prefix).
func SaveAll(p Persist, docs []Doc) error {
	if len(docs) == 0 {
		return nil
	}
	if s, ok := p.(Saver); ok {
		return s.SaveAll(docs)
	}
	for _, d := range docs {
		if err := p.Save(d.Name, d.Value); err != nil {
			return err
		}
	}
	return nil
}
