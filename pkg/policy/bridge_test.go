package policy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

type fakePolicyStore struct {
	policies []actor.Policy
	err      error
}

func (s *fakePolicyStore) Evaluate(role actor.Role, scope string) (bool, bool) { return true, false }
func (s *fakePolicyStore) Reload(policies []actor.Policy) error {
	if s.err != nil {
		return s.err
	}
	s.policies = policies
	return nil
}
func (s *fakePolicyStore) Version() uint64 { return 0 }

type fakeStream struct {
	body []byte
	err  error
}

func (s *fakeStream) Recv() (any, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.body == nil {
		return nil, errors.New("no more data")
	}
	b := s.body
	s.body = nil
	return b, nil
}

func (s *fakeStream) RecvRaw() ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.body == nil {
		return nil, errors.New("no more data")
	}
	b := s.body
	s.body = nil
	return b, nil
}

func (s *fakeStream) Close() error { return nil }

type fakeRef struct {
	matrix gen.PermissionMatrix
	err    error
}

func (r *fakeRef) ID() id.ActorID            { return id.ActorID{} }
func (r *fakeRef) Service() (string, bool)   { return userService, true }
func (r *fakeRef) Invoke(ctx context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	var body []byte
	if r.err == nil {
		body, _ = json.Marshal(r.matrix)
	}
	return invoke.NewCall(invoke.CallModeUnary, &fakeStream{body: body, err: r.err})
}

type fakeStoreHost struct {
	ref   ref.Ref
	store actor.PolicyStore
}

func (h *fakeStoreHost) LookupService(name string) (ref.Ref, bool) { return h.ref, h.ref != nil }
func (h *fakeStoreHost) PolicyStore() actor.PolicyStore           { return h.store }

func TestReloadPolicyStoreFromUser(t *testing.T) {
	matrix := gen.PermissionMatrix{
		Entries: []gen.PermissionEntry{
			{
				Role:    "viewer",
				Actions: map[string]bool{"workspace.create": false},
			},
		},
	}
	store := &fakePolicyStore{}
	host := &fakeStoreHost{ref: &fakeRef{matrix: matrix}, store: store}

	if err := ReloadPolicyStoreFromUser(context.Background(), host); err != nil {
		t.Fatalf("ReloadPolicyStoreFromUser: %v", err)
	}

	want := []actor.Policy{
		{Scope: "workspace.create", Role: actor.Role("viewer"), Allow: false},
	}
	if !reflect.DeepEqual(store.policies, want) {
		t.Errorf("policies mismatch:\ngot:  %+v\nwant: %+v", store.policies, want)
	}
}

func TestReloadPolicyStoreFromUser_UserServiceMissing(t *testing.T) {
	host := &fakeStoreHost{ref: nil, store: &fakePolicyStore{}}

	err := ReloadPolicyStoreFromUser(context.Background(), host)
	if err == nil {
		t.Fatal("expected error when user service is missing")
	}
	if !errors.Is(err, errors.New("user service not found")) && err.Error() != "user service not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReloadPolicyStoreFromUser_FetchFails(t *testing.T) {
	host := &fakeStoreHost{ref: &fakeRef{err: errors.New("invoke failed")}, store: &fakePolicyStore{}}

	err := ReloadPolicyStoreFromUser(context.Background(), host)
	if err == nil {
		t.Fatal("expected error when fetch fails")
	}
}

func TestReloadPolicyStoreFromUser_ReloadFails(t *testing.T) {
	matrix := gen.PermissionMatrix{
		Entries: []gen.PermissionEntry{
			{Role: "viewer", Actions: map[string]bool{"workspace.create": false}},
		},
	}
	store := &fakePolicyStore{err: errors.New("reload failed")}
	host := &fakeStoreHost{ref: &fakeRef{matrix: matrix}, store: store}

	err := ReloadPolicyStoreFromUser(context.Background(), host)
	if err == nil {
		t.Fatal("expected error when reload fails")
	}
}
