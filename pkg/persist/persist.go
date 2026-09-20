// Package persist defines the optional actor self-managed persistence
// contract. Actors that need durable state implement Persistent;
// the runtime calls Save/Load at coordinator-chosen moments. Format,
// storage backend, and location are entirely the actor's decision.
package persist

// Persistent is the opt-in interface for actors that manage their own
// durable state. Runtime only coordinates timing; the actor controls
// serialization format and storage backend.
type Persistent interface {
	Save() error
	Load() error
}
