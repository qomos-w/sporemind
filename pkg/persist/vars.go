package persist

import (
	"errors"
	"sync"
)

// Error sentinels

// ErrNotExist is returned by Load when no persisted state exists for the
// named actor. Callers should treat this as "first start" (legitimate empty
// state), distinct from a real I/O or decode failure.
//
// Previously Load returned nil for the missing-file case, which made it
// impossible for callers to distinguish "new actor" from "lost persistence
// due to identity drift" — the silent-empty state caused agents to appear
// to load successfully while their conversation history was gone.
var ErrNotExist = errors.New("persist: state does not exist")

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state

// fileLockRegistry serializes concurrent file access per absolute path within
// the process. On Windows, os.Rename fails with a sharing violation if the
// target is open by any handle (even another goroutine in the same process);
// without per-path locking, concurrent Save/Load from different actor
// lifecycles (e.g. an old instance shutting down while a new one starts) can
// collide on state.json and state.json.tmp and surface "being used by another
// process" errors.
var (
	fileLocksMu sync.Mutex
	fileLocks   = make(map[string]*sync.RWMutex)
)
