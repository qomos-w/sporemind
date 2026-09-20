package persist

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveCredentialEmptyRefIsCredentialless(t *testing.T) {
	// No resolver installed: an empty ref must still succeed — embedded and
	// AUTH-less backends never need one.
	c, err := ResolveCredential("")
	if err != nil {
		t.Fatalf("empty ref without resolver: %v", err)
	}
	if c != (Credential{}) {
		t.Fatalf("empty ref must yield zero credential, got %+v", c)
	}
}

func TestResolveCredentialRefWithoutResolverIsExplicitError(t *testing.T) {
	SetCredentialResolver(nil)
	_, err := ResolveCredential("profile-1")
	if err == nil {
		t.Fatal("non-empty ref without resolver must be an explicit error")
	}
	if !strings.Contains(err.Error(), "profile-1") || !strings.Contains(err.Error(), "resolver") {
		t.Fatalf("error should name the ref and the missing resolver: %v", err)
	}
}

func TestResolveCredentialDispatchAndErrors(t *testing.T) {
	t.Cleanup(func() { SetCredentialResolver(nil) })
	SetCredentialResolver(func(ref string) (Credential, error) {
		if ref == "boom" {
			return Credential{}, errors.New("upstream down")
		}
		return Credential{Username: "u", Password: "p"}, nil
	})
	c, err := ResolveCredential("ok")
	if err != nil || c.Username != "u" || c.Password != "p" {
		t.Fatalf("dispatch: %+v %v", c, err)
	}
	if _, err := ResolveCredential("boom"); err == nil || !strings.Contains(err.Error(), "resolve credential") {
		t.Fatalf("resolver errors must wrap with the ref: %v", err)
	}
	// Replacement (actor restart) takes effect immediately.
	SetCredentialResolver(func(ref string) (Credential, error) {
		return Credential{Token: "t2"}, nil
	})
	c, err = ResolveCredential("ok")
	if err != nil || c.Token != "t2" {
		t.Fatalf("replace: %+v %v", c, err)
	}
}
