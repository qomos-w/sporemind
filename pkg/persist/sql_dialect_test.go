package persist

import (
	"strings"
	"testing"
)

// TestSQLDialect_DSN verifies direct-vs-tunnel DSN construction (decision
// point 4): direct dial enables TLS with real server verification, tunnel dial
// disables TLS. No connection is made; this is construction-only.
func TestSQLDialect_DSN(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		d := mysqlDialect{}
		cfg := PersistConfig{Endpoint: "db.example:3306", Database: "sporemind"}
		cred := Credential{Username: "u", Password: "p"}
		direct, err := d.dsn(cfg, cred, true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(direct, "@tcp(db.example:3306)/sporemind") {
			t.Errorf("direct mysql dsn missing host/db: %q", direct)
		}
		if !strings.Contains(direct, "&tls=sporemind-db_example") {
			t.Errorf("direct mysql dsn must enable TLS with a registered config: %q", direct)
		}
		tunnel, err := d.dsn(cfg, cred, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(tunnel, "&tls=") {
			t.Errorf("tunnel mysql dsn must have TLS off: %q", tunnel)
		}
		if !strings.Contains(tunnel, "@tcp(db.example:3306)/sporemind") {
			t.Errorf("tunnel mysql dsn missing host/db: %q", tunnel)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		d := postgresDialect{}
		cfg := PersistConfig{Endpoint: "db.example:5432", Database: "sporemind"}
		cred := Credential{Username: "u", Password: "p@ss/word"}
		direct, err := d.dsn(cfg, cred, true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(direct, "sslmode=verify-full") {
			t.Errorf("direct postgres dsn must use sslmode=verify-full: %q", direct)
		}
		// URL-escaping: a raw '@' or '/' in the password would break DSN parsing.
		if strings.Contains(direct, "p@ss/word@db.example") {
			t.Errorf("postgres password with special chars must be URL-escaped: %q", direct)
		}
		tunnel, err := d.dsn(cfg, cred, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(tunnel, "sslmode=disable") {
			t.Errorf("tunnel postgres dsn must use sslmode=disable: %q", tunnel)
		}
	})
}

// TestSQLDialect_Schema verifies the shared SQL shape and the dialect-specific
// value/upsert types (acceptance: dialect difference cases, upsert/JSON type).
func TestSQLDialect_Schema(t *testing.T) {
	m := mysqlDialect{}
	p := postgresDialect{}

	mDocs := strings.Join(m.createSchemaSQL(), "\n")
	if !strings.Contains(mDocs, "value JSON NOT NULL") {
		t.Errorf("mysql docs table must use JSON value type:\n%s", mDocs)
	}
	if !strings.Contains(mDocs, "PRIMARY KEY (ns, name)") {
		t.Errorf("mysql docs table must have (ns,name) primary key:\n%s", mDocs)
	}
	if !strings.Contains(mDocs, "AUTO_INCREMENT") {
		t.Errorf("mysql append table must use AUTO_INCREMENT seq:\n%s", mDocs)
	}

	pDocs := strings.Join(p.createSchemaSQL(), "\n")
	if !strings.Contains(pDocs, "value JSONB NOT NULL") {
		t.Errorf("postgres docs table must use JSONB value type:\n%s", pDocs)
	}
	if !strings.Contains(pDocs, "BIGSERIAL") {
		t.Errorf("postgres append table must use BIGSERIAL seq:\n%s", pDocs)
	}

	mUp := m.upsertDocsSQL()
	if !strings.Contains(mUp, "ON DUPLICATE KEY UPDATE") {
		t.Errorf("mysql upsert must use ON DUPLICATE KEY:\n%s", mUp)
	}
	pUp := p.upsertDocsSQL()
	if !strings.Contains(pUp, "ON CONFLICT (ns, name) DO UPDATE") {
		t.Errorf("postgres upsert must use ON CONFLICT:\n%s", pUp)
	}
}

// TestSQLDialect_PlaceholdersAndEscape verifies the placeholder style and
// LIKE-escape handling are dialect-correct.
func TestSQLDialect_PlaceholdersAndEscape(t *testing.T) {
	m := mysqlDialect{}
	p := postgresDialect{}

	if got := m.placeholder(3); got != "?" {
		t.Errorf("mysql placeholder(3) = %q, want ?", got)
	}
	if got := p.placeholder(3); got != "$3" {
		t.Errorf("postgres placeholder(3) = %q, want $3", got)
	}

	l := listSQL(p)
	if !strings.Contains(l, "$1") || !strings.Contains(l, "$2") || !strings.Contains(l, "ESCAPE") {
		t.Errorf("postgres list SQL missing placeholders/escape: %q", l)
	}
	d := deleteDocsSQL(m)
	if !strings.Contains(d, "OR name LIKE") || !strings.Contains(d, "ESCAPE") {
		t.Errorf("mysql delete cascade SQL missing LIKE escape: %q", d)
	}

	// LIKE metacharacters in a name must be escaped literally; '!' is doubled
	// first so the escape prefixes added for % and _ are not re-escaped.
	got := escapeLike(`a%b_c!d`)
	want := `a!%b!_c!!d`
	if got != want {
		t.Errorf("escapeLike = %q, want %q", got, want)
	}
}

// TestNewSQL_TunnelWiring verifies that a TunnelRef routes the dial through
// the tunnel resolver (TLS off) and that a missing resolver is an explicit
// error — without needing a live database.
func TestNewSQL_TunnelWiring(t *testing.T) {
	// No resolver installed -> explicit error naming the ref.
	if _, err := newSQLPersist(PersistConfig{
		Prefix: "t", Endpoint: "db:5432", TunnelRef: "host-1",
	}, postgresDialect{}); err == nil || !strings.Contains(err.Error(), "host-1") {
		t.Fatalf("expected tunnel-ref resolution error, got %v", err)
	}

	// Resolver provides a local addr -> the factory dials that addr (ping fails
	// there, proving tunnel resolution) and reports it.
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		if targetAddr != "db:5432" {
			return "", &unexpectedTargetError{want: "db:5432", got: targetAddr}
		}
		return "127.0.0.1:1", nil // unreachable; ping must fail and name this addr
	})
	_, err := newSQLPersist(PersistConfig{
		Prefix: "t", Endpoint: "db:5432", TunnelRef: "host-1",
	}, postgresDialect{})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("expected ping error naming the tunnel local addr, got %v", err)
	}
}

type unexpectedTargetError struct{ want, got string }

func (e *unexpectedTargetError) Error() string {
	return "resolver target = " + e.got + ", want " + e.want
}
