package logging

import (
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/resource"
)

// LogRingKey is the gospore resource key for the structured log ring buffer.
var LogRingKey = resource.Key[LogRing]{Name: "sporemind.logging.logring"}

// ConsoleLogSource is the surface required to read persisted frontend console
// logs from within an actor. It is satisfied by *ConsoleStore.
type ConsoleLogSource interface {
	Tail(n int) ([]ConsoleEntry, error)
	// QueryBefore returns up to n entries with Time strictly before the given
	// RFC3339Nano timestamp, in chronological order. It powers the Before
	// cursor paging of workspace.logs_query for Source="console".
	QueryBefore(before string, n int) ([]ConsoleEntry, error)
}

// ConsoleLogSourceKey is the gospore resource key for the frontend console
// log store.
var ConsoleLogSourceKey = resource.Key[ConsoleLogSource]{Name: "sporemind.logging.console"}

// LogStreamerKey is the gospore resource key for the batched live log
// streamer. The workspace actor subscribes to it in OnStart to emit the
// workspace.log event kind.
var LogStreamerKey = resource.Key[*LogStreamer]{Name: "sporemind.logging.streamer"}

// BackendLogSource is the surface required to read persisted backend logs
// from within an actor. It is satisfied by *FileStore and
// *LevelDBLogStore and lets workspace.logs_query page beyond the in-memory
// ring window (restart-safe freeze forensics).
type BackendLogSource interface {
	Tail(n int) ([]gateway.LogEntry, error)
	QueryBefore(before string, n int) ([]gateway.LogEntry, error)
}

// LogStore is the combined surface for a backend log store: it can write
// entries (Write, used by Ring.Append) and read them back (BackendLogSource).
// Both *FileStore and *LevelDBLogStore implement it, letting the cmd
// construction point select between fs and goleveldb backends at startup
// via config.ScopedBackend("logs").
type LogStore interface {
	BackendLogSource
	Write(gateway.LogEntry) error
	Close() error
	Cleanup(age time.Duration) error
}

// BackendLogSourceKey is the gospore resource key for the persisted backend
// log store.
var BackendLogSourceKey = resource.Key[BackendLogSource]{Name: "sporemind.logging.backend"}
