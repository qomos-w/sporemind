package persist

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// sqlContainerCase describes one SQL dialect's container for the shared suite.
type sqlContainerCase struct {
	name        string
	image       string
	port        string
	env         []string
	user        string
	pass        string
	database    string
	dialect     sqlDialect
	backendType BackendType
	// likeCaseInsensitive pins the dialect's default LIKE collation behaviour:
	// mysql's default utf8mb4 collation is case-insensitive, postgres' is
	// case-sensitive.
	likeCaseInsensitive bool
}

func mysqlContainerCase() sqlContainerCase {
	return sqlContainerCase{
		name:                "mysql",
		image:               "mysql:8.4",
		port:                "3306",
		env:                 []string{"MYSQL_ROOT_PASSWORD=rootpw", "MYSQL_DATABASE=sporemind"},
		user:                "root",
		pass:                "rootpw",
		database:            "sporemind",
		dialect:             mysqlDialect{},
		backendType:         BackendMySQL,
		likeCaseInsensitive: true,
	}
}

func postgresContainerCase() sqlContainerCase {
	return sqlContainerCase{
		name:                "postgres",
		image:               "postgres:16",
		port:                "5432",
		env:                 []string{"POSTGRES_USER=postgres", "POSTGRES_PASSWORD=rootpw", "POSTGRES_DB=sporemind"},
		user:                "postgres",
		pass:                "rootpw",
		database:            "sporemind",
		dialect:             postgresDialect{},
		backendType:         BackendPostgres,
		likeCaseInsensitive: false,
	}
}

// TestMySQLContainer runs the full backend verification suite against a real
// MySQL container twice: dialed through the (fake) tunnel so the TLS-off
// tunnel path is what gets verified, and dialed directly (loopback
// exemption) so the direct code path is what gets verified (B7 matrix).
// Skips without docker or in -short mode.
func TestMySQLContainer(t *testing.T) {
	runSQLContainerSuite(t, mysqlContainerCase())
}

// TestPostgresContainer runs the same suite against a real PostgreSQL
// container.
func TestPostgresContainer(t *testing.T) {
	runSQLContainerSuite(t, postgresContainerCase())
}

// runSQLContainerSuite boots one container for the dialect and runs every
// verification layer as subtests against it: the T1 contract suite, the
// Saver batch contract, the dialect-difference cases, byte fidelity, the
// Appender mapping, and a direct-dial (no tunnel) run of the T1 contract
// suite for the B7 direct × tunnel matrix.
func runSQLContainerSuite(t *testing.T, tc sqlContainerCase) {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("docker not available or -short mode; skipping container suite")
	}
	cfg, directCfg := startSQLContainer(t, tc)
	factory := func(t *testing.T) Persist {
		p, err := New(cfg)
		if err != nil {
			t.Fatalf("New(%s): %v", tc.backendType, err)
		}
		return p
	}
	directFactory := func(t *testing.T) Persist {
		p, err := New(directCfg)
		if err != nil {
			t.Fatalf("New(%s direct): %v", tc.backendType, err)
		}
		return p
	}

	t.Run("Contract", func(t *testing.T) { RunContractTests(t, factory) })
	t.Run("SaverContract", func(t *testing.T) { RunSaverContractTests(t, factory) })
	t.Run("Dialect", func(t *testing.T) { runSQLDialectCases(t, tc, factory) })
	t.Run("ByteFidelity", func(t *testing.T) { runSQLByteFidelity(t, factory) })
	t.Run("Append", func(t *testing.T) { runSQLAppend(t, tc, factory) })
	t.Run("DirectContract", func(t *testing.T) { RunContractTests(t, directFactory) })
}

// runSQLDialectCases covers the card's dialect-difference cases: JSON
// value-type roundtrip and LIKE case sensitivity.
func runSQLDialectCases(t *testing.T, tc sqlContainerCase, factory func(t *testing.T) Persist) {
	t.Helper()
	p := factory(t)

	type nested struct {
		S   string         `json:"s"`
		N   int            `json:"n"`
		Arr []int          `json:"arr"`
		M   map[string]any `json:"m"`
	}
	want := nested{S: "héllo \u0001", N: -7, Arr: []int{1, 2, 3}, M: map[string]any{"k": "v"}}
	if err := p.Save("dialect/json", want); err != nil {
		t.Fatalf("Save json: %v", err)
	}
	var got nested
	if err := p.Load("dialect/json", &got); err != nil {
		t.Fatalf("Load json: %v", err)
	}
	if got.S != want.S || got.N != want.N || fmt.Sprint(got.Arr) != fmt.Sprint(want.Arr) || got.M["k"] != "v" {
		t.Errorf("json roundtrip mismatch: got %+v, want %+v", got, want)
	}

	if err := p.Save("ws/alpha", 1); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("WS/beta", 2); err != nil {
		t.Fatal(err)
	}
	lower, err := p.(Lister).List("ws")
	if err != nil {
		t.Fatalf("List ws: %v", err)
	}
	if tc.likeCaseInsensitive {
		if len(lower) != 2 {
			t.Errorf("mysql LIKE is case-insensitive: List(%q) = %v, want both ws/alpha and WS/beta", "ws", lower)
		}
	} else {
		if len(lower) != 1 || lower[0] != "ws/alpha" {
			t.Errorf("postgres LIKE is case-sensitive: List(%q) = %v, want [ws/alpha]", "ws", lower)
		}
	}
}

// runSQLByteFidelity verifies the JSON value column roundtrips values a lossy
// encoding would mangle (large int64 beyond float53, unicode escapes,
// HTML-escaped chars). The contract requires JSON-roundtrip equality, not byte
// identity: MySQL's JSON column normalizes whitespace and key order, so the
// test compares decoded values. (Byte-for-byte fidelity is the mongo
// backend's acceptance, where value is stored as the exact JSON string.)
func runSQLByteFidelity(t *testing.T, factory func(t *testing.T) Persist) {
	t.Helper()
	p := factory(t)

	raw := json.RawMessage(`{"big":9007199254740993,"s":" <x>&"}`)
	if err := p.Save("fidelity/raw", raw); err != nil {
		t.Fatalf("Save raw: %v", err)
	}
	var got json.RawMessage
	if err := p.Load("fidelity/raw", &got); err != nil {
		t.Fatalf("Load raw: %v", err)
	}
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(raw, &wantVal); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("json roundtrip mismatch: got %v, want %v", gotVal, wantVal)
	}

	if err := p.Save("fidelity/num", map[string]any{"n": 9007199254740993}); err != nil {
		t.Fatalf("Save num: %v", err)
	}
	var num struct {
		N int64 `json:"n"`
	}
	if err := p.Load("fidelity/num", &num); err != nil {
		t.Fatalf("Load num: %v", err)
	}
	if num.N != 9007199254740993 {
		t.Errorf("int64 precision: got %d, want 9007199254740993", num.N)
	}
}

// runSQLAppend verifies the SQL Appender mapping (persist_append table,
// auto-increment seq, ordered readback, no torn records under concurrency, and
// Delete cascade of append records). Readback is via the backend's own
// appendRecords (the shared contract suite verifies readback through
// BasePather, which SQL backends do not implement).
func runSQLAppend(t *testing.T, tc sqlContainerCase, factory func(t *testing.T) Persist) {
	t.Helper()
	p := factory(t)
	sp, ok := p.(*sqlPersist)
	if !ok {
		t.Fatalf("expected *sqlPersist, got %T", p)
	}

	const writers = 4
	const linesPerWriter = 25
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if err := sp.Append("ledger/x", []byte(fmt.Sprintf("w%d-%03d\n", w, j))); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Append: %v", err)
	}

	recs, err := sp.appendRecords("ledger/x")
	if err != nil {
		t.Fatalf("appendRecords: %v", err)
	}
	if len(recs) != writers*linesPerWriter {
		t.Fatalf("record count = %d, want %d", len(recs), writers*linesPerWriter)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		s := string(r)
		if len(s) == 0 || s[len(s)-1] != '\n' {
			t.Errorf("torn record (no trailing newline): %q", s)
		}
		seen[s] = true
	}
	for w := 0; w < writers; w++ {
		for j := 0; j < linesPerWriter; j++ {
			if !seen[fmt.Sprintf("w%d-%03d\n", w, j)] {
				t.Errorf("missing record w%d-%03d", w, j)
			}
		}
	}

	if err := sp.Delete("ledger"); err != nil {
		t.Fatalf("Delete ledger: %v", err)
	}
	recs, err = sp.appendRecords("ledger/x")
	if err != nil {
		t.Fatalf("appendRecords after delete: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("append records not cascaded: got %d, want 0", len(recs))
	}
}

// startSQLContainer boots the dialect's container, wires the fake tunnel and
// container credentials, waits for readiness, and returns ready
// PersistConfigs: the first dials through the tunnel (TLS off), the second
// dials the container's published loopback address directly (loopback TLS
// exemption) for the B7 direct × tunnel matrix.
func startSQLContainer(t *testing.T, tc sqlContainerCase) (PersistConfig, PersistConfig) {
	t.Helper()
	h := startContainer(t, tc.image, tc.port, tc.env, nil)
	t.Cleanup(h.stop)
	fakeTunnelTarget = h.hostPort
	useFakeTunnel(t)
	credRef := containerCredentials(t, tc.user, tc.pass)
	cfg := PersistConfig{
		Backend:       tc.backendType,
		Prefix:        "contract",
		Endpoint:      "db.internal:" + tc.port,
		Database:      tc.database,
		CredentialRef: credRef,
		TunnelRef:     "fake",
	}
	waitReady(t, tc.name, 150*time.Second, func() error {
		p, err := New(cfg)
		if err != nil {
			return err
		}
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		return nil
	})
	directCfg := PersistConfig{
		Backend:       tc.backendType,
		Prefix:        "contractdirect",
		Endpoint:      h.hostPort,
		Database:      tc.database,
		CredentialRef: credRef,
	}
	return cfg, directCfg
}
