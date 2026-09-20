package persist

import (
	"fmt"
	"sync"
)

// Credential is the dial-time credential resolved from a CredentialRef.
// It is deliberately backend-agnostic: kv backends use Username/Password
// (Redis AUTH, etcd user), SQL backends Username/Password, S3-compatible
// stores AccessKey/Secret, WebDAV Username/Password or Token (Bearer).
// Backends read the fields they need and ignore the rest.
type Credential struct {
	Username  string
	Password  string
	AccessKey string
	Secret    string
	Token     string
}

// CredentialResolver resolves a CredentialRef (a dbmanager connection
// profile id) to its credential. Implementations must not cache: resolution
// happens at dial time so a rotated credential takes effect on reconnect.
type CredentialResolver func(ref string) (Credential, error)

var (
	credMu       sync.RWMutex
	credResolver CredentialResolver
)

// SetCredentialResolver installs the process-wide credential resolver. It is
// called once at startup by the dbmanager actor (which owns connection
// profiles); persist itself never imports an actor. Installing over an
// existing resolver is allowed and replaces it (tests and actor restarts).
func SetCredentialResolver(r CredentialResolver) {
	credMu.Lock()
	defer credMu.Unlock()
	credResolver = r
}

// ResolveCredential returns the credential for ref. An empty ref is the
// credentialless path (embedded or AUTH-less stores) and yields a zero
// Credential. A non-empty ref with no resolver installed is an explicit
// error — never a silent empty credential that would dial as anonymous.
func ResolveCredential(ref string) (Credential, error) {
	if ref == "" {
		return Credential{}, nil
	}
	credMu.RLock()
	r := credResolver
	credMu.RUnlock()
	if r == nil {
		return Credential{}, fmt.Errorf("persist: CredentialRef %q set but no credential resolver installed (dbmanager not started?)", ref)
	}
	c, err := r(ref)
	if err != nil {
		return Credential{}, fmt.Errorf("persist: resolve credential %q: %w", ref, err)
	}
	return c, nil
}
