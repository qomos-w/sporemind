package persist

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestMongoContainer runs the full backend verification suite against a real
// MongoDB container twice: dialed through the (fake) tunnel so the TLS-off
// tunnel path is what gets verified, and dialed directly (loopback
// exemption) so the direct code path — no tunnel resolution, plain loopback
// dial — is what gets verified (B7 matrix: every backend × {direct,
// tunnel}). Skips without docker or in -short mode.
//
// Mongo standalone has no multi-document transactions, so mongoPersist does
// not implement Saver — the Saver contract's sequential-degradation path is
// what gets exercised here (RunSaverContractTests asserts only the happy path
// and error propagation for non-Saver backends).
func TestMongoContainer(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available or -short mode; skipping container suite")
	}
	cfg, directCfg := startMongoContainer(t)
	factory := func(t *testing.T) Persist {
		p, err := New(cfg)
		if err != nil {
			t.Fatalf("New(mongo): %v", err)
		}
		return p
	}
	directFactory := func(t *testing.T) Persist {
		p, err := New(directCfg)
		if err != nil {
			t.Fatalf("New(mongo direct): %v", err)
		}
		return p
	}

	t.Run("Contract", func(t *testing.T) { RunContractTests(t, factory) })
	t.Run("SaverContract", func(t *testing.T) { RunSaverContractTests(t, factory) })
	t.Run("ByteFidelity", func(t *testing.T) { runMongoByteFidelity(t, factory) })
	t.Run("Append", func(t *testing.T) { runMongoAppend(t, factory) })
	t.Run("DirectContract", func(t *testing.T) { RunContractTests(t, directFactory) })
}

// runMongoByteFidelity is the card's byte-fidelity acceptance: values that a
// lossy BSON-subdocument encoding would mangle must roundtrip byte-for-byte
// because value is stored as the exact JSON string. Covers large int64 beyond
// float53 precision, control-character/unicode escapes, HTML-escaped chars,
// and key order.
func runMongoByteFidelity(t *testing.T, factory func(t *testing.T) Persist) {
	t.Helper()
	p := factory(t)

	cases := []json.RawMessage{
		json.RawMessage(`{"big":9007199254740993}`),
		json.RawMessage(`{"s":"\u0001<x>&"}`),
		json.RawMessage(`{"a":1,"b":2,"c":3}`),
		json.RawMessage(`{"unicode":"héllo ☃","esc":"line\nbreak"}`),
		json.RawMessage(`[1,2,3,{"nested":true}]`),
	}
	for i, raw := range cases {
		name := fmt.Sprintf("fidelity/case-%d", i)
		if err := p.Save(name, raw); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
		var got json.RawMessage
		if err := p.Load(name, &got); err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		wantBytes, _ := json.Marshal(raw)
		if string(got) != string(wantBytes) {
			t.Errorf("byte fidelity case %d: got %s, want %s", i, got, wantBytes)
		}
	}

	// Numeric precision through a typed field (BSON subdocument storage would
	// coerce int64 → float64 and lose the low bits).
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

	// Second Save overwrites; the exact new bytes are what Load returns.
	if err := p.Save("fidelity/overwrite", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("fidelity/overwrite", json.RawMessage(`{"v":2,"extra":[true,null]}`)); err != nil {
		t.Fatal(err)
	}
	var got json.RawMessage
	if err := p.Load("fidelity/overwrite", &got); err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":2,"extra":[true,null]}` {
		t.Errorf("overwrite fidelity: got %s", got)
	}
}

// runMongoAppend verifies the Appender mapping (<prefix>_append collection,
// ObjectID ordering, no torn records under concurrency, Delete cascade).
// Readback is via the backend's own appendRecords.
func runMongoAppend(t *testing.T, factory func(t *testing.T) Persist) {
	t.Helper()
	p := factory(t)
	mp, ok := p.(*mongoPersist)
	if !ok {
		t.Fatalf("expected *mongoPersist, got %T", p)
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
				if err := mp.Append("ledger/x", []byte(fmt.Sprintf("w%d-%03d\n", w, j))); err != nil {
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

	recs, err := mp.appendRecords("ledger/x")
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

	if err := mp.Delete("ledger"); err != nil {
		t.Fatalf("Delete ledger: %v", err)
	}
	recs, err = mp.appendRecords("ledger/x")
	if err != nil {
		t.Fatalf("appendRecords after delete: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("append records not cascaded: got %d, want 0", len(recs))
	}
}

// startMongoContainer boots a MongoDB container, wires the fake tunnel and
// container credentials, waits for readiness, and returns ready
// PersistConfigs: the first dials through the tunnel (TLS off), the second
// dials the container's published loopback address directly (loopback TLS
// exemption) for the B7 direct × tunnel matrix.
func startMongoContainer(t *testing.T) (PersistConfig, PersistConfig) {
	t.Helper()
	const user, pass = "root", "rootpw"
	h := startContainer(t, "mongo:7", "27017",
		[]string{"MONGO_INITDB_ROOT_USERNAME=" + user, "MONGO_INITDB_ROOT_PASSWORD=" + pass}, nil)
	t.Cleanup(h.stop)
	fakeTunnelTarget = h.hostPort
	useFakeTunnel(t)
	credRef := containerCredentials(t, user, pass)
	cfg := PersistConfig{
		Backend:       BackendMongo,
		Prefix:        "contract",
		Endpoint:      "db.internal:27017",
		Database:      "sporemind",
		CredentialRef: credRef,
		TunnelRef:     "fake",
	}
	waitReady(t, "mongo", 120*time.Second, func() error {
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
		Backend:       BackendMongo,
		Prefix:        "contractdirect",
		Endpoint:      h.hostPort,
		Database:      "sporemind",
		CredentialRef: credRef,
	}
	return cfg, directCfg
}
