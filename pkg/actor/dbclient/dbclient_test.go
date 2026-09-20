package dbclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakeConn is a hand-rolled backend fake so the actor's handler plumbing
// can be exercised without docker. Pool state lives on the actor; tests
// drive behavior through the injected seams.
type fakeConn struct {
	pingVersion string
	pingErr     error
	treeFn      func(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error)
	readFn      func(ctx context.Context, path string, limit, offset int) (domain.DbRows, error)
	queryFn     func(ctx context.Context, text, mode string) (domain.DbRows, error)
	closeCount  int
}

func (f *fakeConn) Ping(ctx context.Context) (string, error) {
	return f.pingVersion, f.pingErr
}
func (f *fakeConn) Tree(ctx context.Context, path, cursor string) ([]domain.DbTreeNode, string, bool, error) {
	return f.treeFn(ctx, path, cursor)
}
func (f *fakeConn) Read(ctx context.Context, path string, limit, offset int) (domain.DbRows, error) {
	return f.readFn(ctx, path, limit, offset)
}
func (f *fakeConn) Query(ctx context.Context, text, mode string) (domain.DbRows, error) {
	return f.queryFn(ctx, text, mode)
}
func (f *fakeConn) Close() error { f.closeCount++; return nil }

func newTestActor(t *testing.T, conn backendConn) *Actor {
	t.Helper()
	a := &Actor{
		pool: map[string]*poolEntry{},
		lookupProfile: func(_ actor.Context, id string) (profileSummary, error) {
			return profileSummary{ID: id, Backend: "redis", Endpoint: "test://none", Database: "0"}, nil
		},
		dialBackend: func(_ context.Context, _ profileSummary, _ string) (backendConn, error) {
			if conn == nil {
				return nil, errors.New("fake: no conn")
			}
			return conn, nil
		},
	}
	return a
}

func TestTruncateCell(t *testing.T) {
	if got := truncateCell("hi"); got != "hi" {
		t.Fatalf("short string must pass through: %q", got)
	}
	long := strings.Repeat("x", cellTruncate+10)
	got := truncateCell(long)
	if !strings.HasPrefix(got, strings.Repeat("x", cellTruncate)) {
		t.Fatalf("truncated cell should start with %d x's, got len=%d", cellTruncate, len(got))
	}
	if !strings.Contains(got, "...(truncated)") {
		t.Fatalf("truncated cell must end with the sentinel, got %q", got[len(got)-20:])
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct {
		in   int32
		want int
	}{
		{0, defaultLimit},
		{-5, defaultLimit},
		{50, 50},
		{maxLimit, maxLimit},
		{maxLimit + 100, maxLimit},
	}
	for _, c := range cases {
		if got := clampLimit(c.in); got != c.want {
			t.Errorf("clampLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCheckSQLAllowed(t *testing.T) {
	pass := []string{
		"SELECT 1",
		"  -- pick one\n  SELECT 1",
		"/* who */ SELECT * FROM t",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SHOW DATABASES",
		"EXPLAIN SELECT 1",
		"SELECT 1;",
	}
	for _, p := range pass {
		if err := checkSQLAllowed(p); err != nil {
			t.Errorf("must allow %q: %v", p, err)
		}
	}
	fail := []string{
		"",
		"DROP TABLE x",
		"INSERT INTO x VALUES (1)",
		"SELECT 1; SELECT 2",
		"UPDATE x SET a=1",
		"DELETE FROM x",
	}
	for _, f := range fail {
		if err := checkSQLAllowed(f); err == nil {
			t.Errorf("must reject %q", f)
		}
	}
}

func TestRedisWhitelistCoversReadCommands(t *testing.T) {
	allowed := []string{"GET", "HGETALL", "ZRANGE", "XRANGE", "PFCOUNT", "INFO"}
	blocked := []string{"SET", "DEL", "HSET", "LPUSH", "ZADD", "INCR", "FLUSHALL", "KEYS", "CONFIG", "DEBUG", "EVAL"}
	for _, c := range allowed {
		if !redisReadCmds[c] {
			t.Errorf("expected %q on whitelist", c)
		}
	}
	for _, c := range blocked {
		if redisReadCmds[c] {
			t.Errorf("expected %q to be blocked", c)
		}
	}
}

func TestDbKeyFor(t *testing.T) {
	cases := []struct {
		backend, path, db string
		want              string
	}{
		{"redis", "", "", "0"},
		{"redis", "0", "", "0"},
		{"redis", "0/foo", "", "0"},
		{"redis", "3/key/sub", "", "3"},
		{"postgres", "", "", "postgres"},
		{"postgres", "app", "", "app"},
		{"postgres", "app.users", "", "app"},
		{"postgres", "schema.users", "", "schema"},
		{"mysql", "", "", ""},
		{"mysql", "db.table", "", ""},
		{"mongo", "db.coll", "", ""},
		{"etcd", "", "", ""},
		{"oss", "bucket/dir", "", ""},
		{"webdav", "", "", ""},
	}
	for _, c := range cases {
		p := profileSummary{Backend: c.backend, Database: c.db}
		if got := dbKeyFor(p, c.path); got != c.want {
			t.Errorf("dbKeyFor(%s, %q, db=%q) = %q, want %q", c.backend, c.path, c.db, got, c.want)
		}
	}
}

func TestPoolKey(t *testing.T) {
	if got := poolKey("p1", ""); got != "p1" {
		t.Errorf("empty dbKey must collapse, got %q", got)
	}
	if got := poolKey("p1", "0"); got != "p1\x000" {
		t.Errorf("non-empty dbKey must join, got %q", got)
	}
}

func TestHandlersRejectAnonymous(t *testing.T) {
	a := newTestActor(t, &fakeConn{pingVersion: "v1"})
	if err := a.OnStart(testutil.AdminCtx(testutil.GenActorID())); err != nil {
		t.Fatal(err)
	}
	anon := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleDialTest(anon, domain.DbDialTestReq{ProfileID: "p"}); err == nil {
		t.Fatal("dial_test: anon must be rejected")
	}
	if _, err := a.handleTree(anon, domain.DbTreeReq{ProfileID: "p"}); err == nil {
		t.Fatal("tree: anon must be rejected")
	}
	if _, err := a.handleRead(anon, domain.DbReadReq{ProfileID: "p"}); err == nil {
		t.Fatal("read: anon must be rejected")
	}
	if _, err := a.handleQuery(anon, domain.DbQueryReq{ProfileID: "p"}); err == nil {
		t.Fatal("query: anon must be rejected")
	}
	if _, err := a.handleDescribe(anon, domain.DbDescribeReq{ProfileID: "p", Path: "db.t"}); err == nil {
		t.Fatal("describe: anon must be rejected")
	}
	if _, err := a.handleClose(anon, domain.DbCloseReq{ProfileID: "p"}); err == nil {
		t.Fatal("close: anon must be rejected")
	}
}

func TestDialTestReturnsVersionAndLatency(t *testing.T) {
	conn := &fakeConn{pingVersion: "7.2.4"}
	a := newTestActor(t, conn)
	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleDialTest(ctx, domain.DbDialTestReq{ProfileID: "p"})
	if err != nil {
		t.Fatalf("dial_test: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("Ok=false; Error=%q", resp.Error)
	}
	if resp.ServerVersion != "7.2.4" {
		t.Errorf("ServerVersion = %q, want 7.2.4", resp.ServerVersion)
	}
	if resp.LatencyMs < 0 || resp.LatencyMs > 5*60*1000 {
		t.Errorf("LatencyMs out of range: %d", resp.LatencyMs)
	}
}

func TestDialTestReportsPingFailureWithoutError(t *testing.T) {
	conn := &fakeConn{pingErr: errors.New("boom")}
	a := newTestActor(t, conn)
	resp, err := a.handleDialTest(testutil.AdminCtx(testutil.GenActorID()), domain.DbDialTestReq{ProfileID: "p"})
	if err != nil {
		t.Fatalf("dial_test: unexpected error %v", err)
	}
	if resp.Ok {
		t.Fatal("Ok must be false when ping fails")
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "boom") {
		t.Errorf("Error must include the cause, got %q", resp.Error)
	}
}

func TestTreeAndReadAndQueryPlumb(t *testing.T) {
	called := struct {
		tree, read, query int
	}{}
	conn := &fakeConn{
		pingVersion: "x",
		treeFn: func(_ context.Context, path, _ string) ([]domain.DbTreeNode, string, bool, error) {
			called.tree++
			return []domain.DbTreeNode{{Path: path, Kind: "key", Label: "x"}}, "", false, nil
		},
		readFn: func(_ context.Context, path string, limit, offset int) (domain.DbRows, error) {
			called.read++
			return newRows([]string{"k", "v"}, [][]string{{path, "data"}}, false), nil
		},
		queryFn: func(_ context.Context, text, _ string) (domain.DbRows, error) {
			called.query++
			return newRows([]string{"result"}, [][]string{{text}}, false), nil
		},
	}
	a := newTestActor(t, conn)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleTree(ctx, domain.DbTreeReq{ProfileID: "p", Path: "0/foo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRead(ctx, domain.DbReadReq{ProfileID: "p", Path: "0/foo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleQuery(ctx, domain.DbQueryReq{ProfileID: "p", Text: "GET x"}); err != nil {
		t.Fatal(err)
	}
	if called.tree != 1 || called.read != 1 || called.query != 1 {
		t.Fatalf("call counts: %+v", called)
	}
}

func TestCloseReleasesAllHandlesForProfile(t *testing.T) {
	conn := &fakeConn{pingVersion: "v"}
	a := newTestActor(t, conn)
	ctx := testutil.AdminCtx(testutil.GenActorID())
	// populate two pool entries (different dbKeys) for the same profile.
	a.pool["p\x000"] = &poolEntry{profileID: "p", dbKey: "0", conn: conn, lastUsed: time.Now()}
	a.pool["p\x001"] = &poolEntry{profileID: "p", dbKey: "1", conn: conn, lastUsed: time.Now()}
	a.pool["q\x000"] = &poolEntry{profileID: "q", dbKey: "0", conn: conn, lastUsed: time.Now()}

	if _, err := a.handleClose(ctx, domain.DbCloseReq{ProfileID: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.pool["p\x000"]; ok {
		t.Fatal("p/0 must be released")
	}
	if _, ok := a.pool["p\x001"]; ok {
		t.Fatal("p/1 must be released")
	}
	if _, ok := a.pool["q\x000"]; !ok {
		t.Fatal("q/0 must be untouched")
	}
	if conn.closeCount != 2 {
		t.Fatalf("closeCount=%d, want 2", conn.closeCount)
	}
}

func TestDecodeInvokeResult(t *testing.T) {
	type out struct{ X int }
	cases := []struct {
		name string
		in   any
		want out
	}{
		{"bytes", []byte(`{"X":7}`), out{7}},
		{"map", map[string]any{"X": 9}, out{9}},
		{"nil", nil, out{}},
	}
	for _, c := range cases {
		var got out
		err := decodeInvokeResult(c.in, &got)
		if c.name == "nil" {
			if err == nil {
				t.Errorf("%s: nil input should error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// fakeDescribeConn augments fakeConn with the optional hasDescribe surface.
type fakeDescribeConn struct {
	fakeConn
	describeFn func(ctx context.Context, path string) (domain.DbDescribeResp, error)
}

func (f *fakeDescribeConn) Describe(ctx context.Context, path string) (domain.DbDescribeResp, error) {
	return f.describeFn(ctx, path)
}

func TestDescribeReturnsColumnsAndIndexes(t *testing.T) {
	want := domain.DbDescribeResp{
		Columns: []domain.DbColumnDef{{Name: "id", DataType: "bigint", Key: "PRI", Extra: "auto_increment"}},
		Indexes: []domain.DbIndexDef{{Name: "PRIMARY", Columns: "id", Unique: true}},
	}
	conn := &fakeDescribeConn{
		describeFn: func(_ context.Context, path string) (domain.DbDescribeResp, error) {
			if path != "mydb.users" {
				t.Errorf("describe path = %q, want mydb.users", path)
			}
			return want, nil
		},
	}
	a := newTestActor(t, conn)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	got, err := a.handleDescribe(ctx, domain.DbDescribeReq{ProfileID: "p", Path: "mydb.users"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Columns) != 1 || got.Columns[0].Name != "id" || got.Columns[0].Key != "PRI" {
		t.Fatalf("columns = %+v", got.Columns)
	}
	if len(got.Indexes) != 1 || !got.Indexes[0].Unique {
		t.Fatalf("indexes = %+v", got.Indexes)
	}
}

func TestDescribeRejectsNonSQLBackendAndBadPath(t *testing.T) {
	a := newTestActor(t, &fakeConn{})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleDescribe(ctx, domain.DbDescribeReq{ProfileID: "p", Path: "db.t"}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("non-sql backend must be rejected with a clear error, got %v", err)
	}
	if _, err := a.handleDescribe(ctx, domain.DbDescribeReq{ProfileID: "p"}); err == nil {
		t.Fatal("empty path must be rejected")
	}
}

func TestPgIndexColumns(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CREATE UNIQUE INDEX u ON t USING btree (a, b)", "a, b"},
		{"CREATE INDEX i ON t USING btree (lower(email))", "lower(email)"},
		{"CREATE INDEX i ON t", "CREATE INDEX i ON t"},
	}
	for _, c := range cases {
		if got := pgIndexColumns(c.in); got != c.want {
			t.Errorf("pgIndexColumns(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}