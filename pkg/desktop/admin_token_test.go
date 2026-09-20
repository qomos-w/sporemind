package desktop

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/user"
)

// delayedRegistry fakes resource.Registry: the issuer key only resolves
// after readyAfter Get calls, simulating the user actor publishing it some
// time after the Wails frontend already called GetAdminToken.
type delayedRegistry struct {
	mu          sync.Mutex
	readyAfter  int
	getCalls    int
	frozenState bool
}

func (d *delayedRegistry) Set(key any, value any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.frozenState {
		return errFrozen
	}
	return nil
}

func (d *delayedRegistry) Get(key any) (any, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.getCalls++
	if d.readyAfter > 0 && d.getCalls >= d.readyAfter {
		return user.DesktopTokenIssuer(func() (string, error) { return "admin-token", nil }), true
	}
	return nil, false
}

func (d *delayedRegistry) Has(key any) bool {
	v, ok := d.Get(key)
	return ok && v != nil
}

func (d *delayedRegistry) Frozen() bool { return false }

var errFrozen = staticError("registry frozen")

type staticError string

func (s staticError) Error() string { return string(s) }

func TestWaitDesktopTokenIssuer_ImmediateRegistration(t *testing.T) {
	reg := &delayedRegistry{readyAfter: 1}
	start := time.Now()
	issuer, err := waitDesktopTokenIssuer(reg, time.Second, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("expected immediate issuer, got error: %v", err)
	}
	tok, err := issuer()
	if err != nil || tok != "admin-token" {
		t.Fatalf("issuer returned %q, %v", tok, err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("immediate registration should not wait, took %s", elapsed)
	}
}

func TestWaitDesktopTokenIssuer_LateRegistration(t *testing.T) {
	// Issuer appears on the 3rd Get: first call misses, one poll sleeps, then hit.
	reg := &delayedRegistry{readyAfter: 3}
	issuer, err := waitDesktopTokenIssuer(reg, 2*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("expected issuer after polling, got error: %v", err)
	}
	if tok, err := issuer(); err != nil || tok != "admin-token" {
		t.Fatalf("issuer returned %q, %v", tok, err)
	}
}

func TestWaitDesktopTokenIssuer_TimeoutDiagnostic(t *testing.T) {
	reg := &delayedRegistry{readyAfter: 0} // never registers
	_, err := waitDesktopTokenIssuer(reg, 50*time.Millisecond, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error when issuer never registers")
	}
	if !strings.Contains(err.Error(), "user token issuer not registered") {
		t.Fatalf("error should name the missing issuer: %v", err)
	}
	if !strings.Contains(err.Error(), "user actor may have failed to start") {
		t.Fatalf("error should carry the diagnostic hint: %v", err)
	}
}
