package slashcmd

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var commands = map[string]Command{}
