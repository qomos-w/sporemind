package persist

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// SQL backend type identifiers registered in the backend registry.
const (
	BackendMySQL    BackendType = "mysql"
	BackendPostgres BackendType = "postgres"
)

// sqlPersist is the shared implementation for the SQL family (mysql,
// postgres). One logical store per (Database, Prefix): documents live in the
// persist_docs table keyed by (ns, name) where ns is the actor-type Prefix;
// append-only records live in persist_append keyed by an auto-increment seq.
//
// Both dialects honor the Persist contract: Save upserts, Load selects
// (sql.ErrNoRows -> ErrNotExist), Delete cascades into the name/ subtree and
// its append records, List enumerates the prefix subtree via an escaped LIKE.
type sqlPersist struct {
	db      *sql.DB
	ns      string
	dialect sqlDialect
}

// sqlDialect captures the per-vendor differences: the database/sql driver
// name, DSN construction (with the decision-point-4 TLS rule: direct dial
// TLS on, tunnel dial TLS off), schema DDL, and placeholder style.
type sqlDialect interface {
	driverName() string
	dsn(cfg PersistConfig, cred Credential, tls bool) (string, error)
	createSchemaSQL() []string
	upsertDocsSQL() string
	placeholder(idx int) string
}

// newSQLPersist constructs a SQL persist for the given dialect. It resolves
// the tunnel (decision point 4: tunnel => local dial, TLS off; direct =>
// Endpoint, TLS on), resolves credentials at dial time, opens the pool, pings
// (startup failure is an explicit error), and creates the schema.
func newSQLPersist(cfg PersistConfig, d sqlDialect) (Persist, error) {
	if cfg.Prefix == "" {
		return nil, fmt.Errorf("persist: sql backend requires a non-empty Prefix")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("persist: sql backend requires an Endpoint (host:port)")
	}
	if _, _, err := splitEndpoint(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("persist: sql Endpoint %q: %w", cfg.Endpoint, err)
	}
	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return nil, err
	}
	// decision point 4: tunnel => local dial, TLS off; direct => Endpoint,
	// TLS on unless the endpoint is loopback (the dev/test topology exemption
	// shared with the kv backends, netkv.go).
	tlsOn := !isLoopbackEndpoint(cfg.Endpoint)
	dialAddr := cfg.Endpoint
	if cfg.TunnelRef != "" {
		local, err := ResolveTunnel(cfg.TunnelRef, cfg.Endpoint)
		if err != nil {
			return nil, err
		}
		dialAddr = local
		tlsOn = false
	}
	dsn, err := d.dsn(PersistConfig{
		Endpoint: dialAddr,
		Database: defaultDatabase(cfg.Database),
	}, cred, tlsOn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(d.driverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("persist: sql open (%s): %w", d.driverName(), err)
	}
	applyPoolDefaults(db)
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		cancel()
		_ = db.Close()
		return nil, fmt.Errorf("persist: sql ping %s (%s): %w", dialAddr, d.driverName(), err)
	}
	cancel()
	for _, stmt := range d.createSchemaSQL() {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("persist: sql schema (%s): %w", d.driverName(), err)
		}
	}
	return &sqlPersist{db: db, ns: cfg.Prefix, dialect: d}, nil
}

// applyPoolDefaults sets conservative connection-pool parameters shared by
// both dialects. Backends are read/write-heavy but low-concurrency (per-actor
// state), so a modest pool suffices; connMaxLifetime rotates connections past
// idle server-side timeouts (mysql wait_timeout etc.).
func applyPoolDefaults(db *sql.DB) {
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)
}

// Close releases the connection pool. Optional capability (not part of
// Persist); runtime callers usually keep the pool for the process lifetime.
func (p *sqlPersist) Close() error {
	return p.db.Close()
}

// Save upserts v under name. value is stored as the JSON bytes produced by
// encode, so the dialect's JSON/JSONB column holds the exact marshalled text;
// Load decodes those bytes back, preserving JSON roundtrip fidelity.
func (p *sqlPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	if _, err := p.db.Exec(p.dialect.upsertDocsSQL(), p.ns, name, data, time.Now().UTC()); err != nil {
		return fmt.Errorf("persist: sql upsert %q: %w", name, err)
	}
	return nil
}

// SaveAll implements Saver inside a single transaction: every doc upserts,
// then COMMIT — all-or-nothing. A bad name or encode error aborts the
// transaction before any write; a mid-transaction Exec error rolls back so
// none of the docs persist.
func (p *sqlPersist) SaveAll(docs []Doc) error {
	if len(docs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("persist: sql begin tx: %w", err)
	}
	stmt := p.dialect.upsertDocsSQL()
	now := time.Now().UTC()
	for _, d := range docs {
		if err := validateName(d.Name); err != nil {
			_ = tx.Rollback()
			return err
		}
		data, err := encode(d.Value)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("persist: encode %q: %w", d.Name, err)
		}
		if _, err := tx.Exec(stmt, p.ns, d.Name, data, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("persist: sql upsert %q: %w", d.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("persist: sql commit save-all: %w", err)
	}
	return nil
}

// Load decodes the stored JSON for name into v. A missing row yields
// ErrNotExist, distinguishable from genuine I/O or decode errors.
func (p *sqlPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	var raw []byte
	err := p.db.QueryRow(loadSQL(p.dialect), p.ns, name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("persist: sql load %q: %w", name, err)
	}
	if err := decode(raw, v); err != nil {
		return fmt.Errorf("persist: decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document for name and cascades into its name/ subtree
// (sub-documents) plus the matching append records. Idempotent.
func (p *sqlPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	scan := strings.TrimSuffix(name, "/") + "/"
	pattern := escapeLike(scan) + "%"
	if _, err := p.db.Exec(deleteDocsSQL(p.dialect), p.ns, name, pattern); err != nil {
		return fmt.Errorf("persist: sql delete %q: %w", name, err)
	}
	if _, err := p.db.Exec(deleteAppendSQL(p.dialect), p.ns, name, pattern); err != nil {
		return fmt.Errorf("persist: sql delete append %q: %w", name, err)
	}
	return nil
}

// List implements Lister with the same hierarchical semantics as FSPersist:
// List("ws") and List("ws/") enumerate the ws/ subtree and never return the
// document "ws" itself; List("") enumerates everything. LIKE wildcards in the
// prefix are escaped so names containing % or _ match literally.
func (p *sqlPersist) List(prefix string) ([]string, error) {
	scan := ""
	if prefix != "" {
		scan = strings.TrimSuffix(prefix, "/") + "/"
	}
	pattern := escapeLike(scan) + "%"
	rows, err := p.db.Query(listSQL(p.dialect), p.ns, pattern)
	if err != nil {
		return nil, fmt.Errorf("persist: sql list %q: %w", prefix, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("persist: sql list scan: %w", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persist: sql list rows: %w", err)
	}
	return names, nil
}

// Append implements Appender. Each call inserts one record (ns, name, data)
// into persist_append; seq is auto-increment, so ORDER BY seq yields write
// order. A single INSERT is atomic; concurrent Appends serialize at the row
// insert level (no interleaving of individual records).
func (p *sqlPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := p.db.Exec(appendInsertSQL(p.dialect), p.ns, name, data); err != nil {
		return fmt.Errorf("persist: sql append %q: %w", name, err)
	}
	return nil
}

// appendRecords returns the ordered data records for name (test/inspection
// helper — the shared Appender contract suite verifies readback through
// BasePather for fs backends; SQL backends verify readback through here).
func (p *sqlPersist) appendRecords(name string) ([][]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	rows, err := p.db.Query(appendSelectSQL(p.dialect), p.ns, name)
	if err != nil {
		return nil, fmt.Errorf("persist: sql append records %q: %w", name, err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("persist: sql append scan: %w", err)
		}
		out = append(out, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persist: sql append rows: %w", err)
	}
	return out, nil
}

// loadSQL/deleteSQL/listSQL/appendSQL build the dialect-agnostic statement
// bodies, filling placeholders through the dialect so mysql uses ? and
// postgres uses $n. Args are passed positionally in the same order for both.
// The LIKE escape character is '!' (not backslash): it is literal in both
// dialects' string syntax, whereas 'ESCAPE '\' breaks MySQL (backslash is an
// escape character in MySQL string literals).

func loadSQL(d sqlDialect) string {
	return "SELECT value FROM persist_docs WHERE ns = " + d.placeholder(1) + " AND name = " + d.placeholder(2)
}

func deleteDocsSQL(d sqlDialect) string {
	return "DELETE FROM persist_docs WHERE ns = " + d.placeholder(1) + " AND (name = " + d.placeholder(2) + " OR name LIKE " + d.placeholder(3) + " ESCAPE '!')"
}

func deleteAppendSQL(d sqlDialect) string {
	return "DELETE FROM persist_append WHERE ns = " + d.placeholder(1) + " AND (name = " + d.placeholder(2) + " OR name LIKE " + d.placeholder(3) + " ESCAPE '!')"
}

func listSQL(d sqlDialect) string {
	return "SELECT name FROM persist_docs WHERE ns = " + d.placeholder(1) + " AND name LIKE " + d.placeholder(2) + " ESCAPE '!' ORDER BY name"
}

func appendInsertSQL(d sqlDialect) string {
	return "INSERT INTO persist_append (ns, name, data) VALUES (" + d.placeholder(1) + ", " + d.placeholder(2) + ", " + d.placeholder(3) + ")"
}

func appendSelectSQL(d sqlDialect) string {
	return "SELECT data FROM persist_append WHERE ns = " + d.placeholder(1) + " AND name = " + d.placeholder(2) + " ORDER BY seq"
}

// escapeLike escapes the LIKE wildcard characters (%, _) and the escape
// character itself (!) so the prefix matches literally. The ESCAPE '!'
// clause in the statements selects '!' as the escape character for both
// dialects.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}

// splitEndpoint validates and splits a host:port endpoint. It rejects URLs
// (SQL backends dial host:port, never a scheme) and missing ports.
func splitEndpoint(endpoint string) (host, port string, err error) {
	h, p, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("not host:port (use host:port, not a URL): %w", err)
	}
	if h == "" {
		return "", "", fmt.Errorf("empty host")
	}
	return h, p, nil
}

func defaultDatabase(db string) string {
	if db == "" {
		return "sporemind"
	}
	return db
}

// --- mysql dialect ---

type mysqlDialect struct{}

func (mysqlDialect) driverName() string { return "mysql" }

func (mysqlDialect) placeholder(int) string { return "?" }

func (mysqlDialect) createSchemaSQL() []string {
	// Column sizes respect InnoDB's 3072-byte index limit under utf8mb4
	// (4 bytes/char): (128 + 512) * 4 = 2560 bytes for the composite key.
	// Names longer than 512 chars are rejected by the server for mysql.
	return []string{
		`CREATE TABLE IF NOT EXISTS persist_docs (
  ns VARCHAR(128) NOT NULL,
  name VARCHAR(512) NOT NULL,
  value JSON NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (ns, name)
)`,
		`CREATE TABLE IF NOT EXISTS persist_append (
  seq BIGINT AUTO_INCREMENT PRIMARY KEY,
  ns VARCHAR(128) NOT NULL,
  name VARCHAR(512) NOT NULL,
  data BLOB NOT NULL,
  KEY persist_append_name_idx (ns, name, seq)
)`,
	}
}

func (mysqlDialect) upsertDocsSQL() string {
	// MySQL 8.0.19+ alias form (VALUES() is deprecated). ON DUPLICATE KEY
	// fires on the (ns, name) primary key.
	return "INSERT INTO persist_docs (ns, name, value, updated_at) VALUES (?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE value = new.value, updated_at = new.updated_at"
}

func (mysqlDialect) dsn(cfg PersistConfig, cred Credential, tls bool) (string, error) {
	host, port, err := splitEndpoint(cfg.Endpoint)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%s@tcp(%s)/%s?parseTime=false&multiStatements=false", cred.Username, cred.Password, net.JoinHostPort(host, port), cfg.Database)
	if tls {
		name := mysqlRegisterTLS(host)
		fmt.Fprintf(&b, "&tls=%s", name)
	}
	return b.String(), nil
}

// mysqlRegisterTLS registers (once per host) a TLS config with the mysql
// driver under a stable name, verifying the server certificate against the
// system roots and the host name (decision point 4: direct dial TLS on with
// real verification, not skip-verify).
var (
	mysqlTLSMu    sync.Mutex
	mysqlTLSNamed = map[string]string{}
)

func mysqlRegisterTLS(host string) string {
	mysqlTLSMu.Lock()
	defer mysqlTLSMu.Unlock()
	if name, ok := mysqlTLSNamed[host]; ok {
		return name
	}
	name := "sporemind-" + sanitizeTLSName(host)
	_ = mysql.RegisterTLSConfig(name, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	mysqlTLSNamed[host] = name
	return name
}

func sanitizeTLSName(host string) string {
	r := strings.NewReplacer(":", "_", ".", "_", "/", "_")
	return r.Replace(host)
}

// --- postgres dialect ---

type postgresDialect struct{}

func (postgresDialect) driverName() string { return "pgx" }

func (postgresDialect) placeholder(idx int) string {
	return fmt.Sprintf("$%d", idx)
}

func (postgresDialect) createSchemaSQL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS persist_docs (
  ns TEXT NOT NULL,
  name TEXT NOT NULL,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (ns, name)
)`,
		`CREATE TABLE IF NOT EXISTS persist_append (
  seq BIGSERIAL PRIMARY KEY,
  ns TEXT NOT NULL,
  name TEXT NOT NULL,
  data BYTEA NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS persist_append_name_idx ON persist_append (ns, name, seq)`,
	}
}

func (postgresDialect) upsertDocsSQL() string {
	return "INSERT INTO persist_docs (ns, name, value, updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT (ns, name) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at"
}

func (postgresDialect) dsn(cfg PersistConfig, cred Credential, tls bool) (string, error) {
	host, port, err := splitEndpoint(cfg.Endpoint)
	if err != nil {
		return "", err
	}
	sslmode := "disable"
	if tls {
		sslmode = "verify-full"
	}
	user := cred.Username
	pass := cred.Password
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s&application_name=sporemind",
		postgresURLEscape(user), postgresURLEscape(pass),
		net.JoinHostPort(host, port), cfg.Database, sslmode), nil
}

// postgresURLEscape percent-escapes a DSN field for the postgres:// URL
// scheme so passwords with special characters survive parsing.
func postgresURLEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range s {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteRune(r)
			continue
		}
		c := []byte{byte(r)}
		for _, by := range c {
			b.WriteByte('%')
			b.WriteByte(hex[by>>4])
			b.WriteByte(hex[by&0x0f])
		}
	}
	return b.String()
}
