package dbclient

import (
	"context"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// backendConn is one dialed backend handle. Implementations encapsulate the
// per-driver quirks (tree semantics, row shaping, query parse) and let the
// actor stay lane- and pool-agnostic.
type backendConn interface {
	// Ping verifies the connection and returns the backend's server version
	// string (or "" if the backend has no version concept).
	Ping(ctx context.Context) (version string, err error)
	// Tree returns the immediate-children nodes under path (paginated for
	// redis via cursor; others ignore cursor). nextCursor is "" when the
	// page is final; hasMore is true when more nodes are available.
	Tree(ctx context.Context, path, cursor string) (nodes []domain.DbTreeNode, nextCursor string, hasMore bool, err error)
	// Read returns a tabular result (rows + columns). Cell values are
	// stringified and truncated to 512 chars; limit+1 is fetched so the
	// caller can flip Truncated when more rows are available.
	Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error)
	// Query runs a single backend-specific read-only query (sql statement,
	// redis command, mongo json, etc.). Modes other than "auto" must match
	// the backend; otherwise the call returns an explicit error.
	Query(ctx context.Context, text, mode string) (domain.DbRows, error)
	// Close releases the underlying transport. Subsequent calls panic.
	Close() error
}

// hasObjectStorage is the optional byte-stream surface that only the
// object-storage backends (oss, webdav) implement. The actor layer
// type-asserts against this interface so non-storage profiles (mysql,
// postgres, mongo, redis, etcd) get a clear "browse-only" error rather
// than dispatching through a no-op stub. New object-storage backends
// must satisfy both backendConn and hasObjectStorage.
type hasObjectStorage interface {
	ObjectList(ctx context.Context, path, cursor string, limit int) (entries []domain.DbObjectEntry, nextCursor string, hasMore bool, err error)
	ObjectRead(ctx context.Context, path string, maxBytes int64) (data []byte, truncated bool, contentType string, modified string, err error)
	ObjectWrite(ctx context.Context, path, contentType string, data []byte) (size int64, err error)
	ObjectDelete(ctx context.Context, path string) error
	ObjectMkdir(ctx context.Context, parent, name string) error
	ObjectStat(ctx context.Context, path string) (size int64, isDir bool, contentType string, modified string, err error)
}

// shared query helpers live here so every backend produces the same DbRows
// shape (columns / rows / truncated / 512-char cells).

func newRows(cols []string, rows [][]string, truncated bool) domain.DbRows {
	cut := make([][]string, len(rows))
	for i, r := range rows {
		cut[i] = make([]string, len(r))
		for j, c := range r {
			cut[i][j] = truncateCell(c)
		}
	}
	return domain.DbRows{Columns: cols, Rows: cut, Truncated: truncated}
}

// rowsWithTruncation trims len(rows) to limit and returns truncated=true
// when len(rows) exceeded limit. Callers typically fetch limit+1 to detect
// truncation cheaply.
func rowsWithTruncation(rows [][]string, limit int) ([][]string, bool) {
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}

// rowsWithTruncationRowsOnly is the same trim as rowsWithTruncation but
// discards the truncated flag for callers that have already decided the
// final Truncated value.
func rowsWithTruncationRowsOnly(rows [][]string, limit int) [][]string {
	if len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
