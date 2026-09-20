package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

type recordingPolicyStore struct {
	policies []actor.Policy
	err      error
}

func (s *recordingPolicyStore) Evaluate(role actor.Role, scope string) (bool, bool) { return true, false }
func (s *recordingPolicyStore) Reload(policies []actor.Policy) error {
	if s.err != nil {
		return s.err
	}
	s.policies = append(s.policies, policies...)
	return nil
}
func (s *recordingPolicyStore) Version() uint64 { return 0 }

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
}

func (r *fakeRef) ID() id.ActorID          { return id.ActorID{} }
func (r *fakeRef) Service() (string, bool) { return "user", true }
func (r *fakeRef) Invoke(ctx context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	body, _ := json.Marshal(r.matrix)
	return invoke.NewCall(invoke.CallModeUnary, &fakeStream{body: body})
}

type fakeStoreHost struct {
	ref   ref.Ref
	store actor.PolicyStore
}

func (h *fakeStoreHost) LookupService(name string) (ref.Ref, bool) { return h.ref, h.ref != nil }
func (h *fakeStoreHost) PolicyStore() actor.PolicyStore           { return h.store }

func TestPolicyReloadInterceptor_IgnoresOtherCalls(t *testing.T) {
	store := &recordingPolicyStore{}
	i := NewPolicyReloadInterceptor(&fakeStoreHost{ref: &fakeRef{}, store: store})

	req := &gateway.GatewayRequest{CallID: "user.permission_get"}
	resp := &gateway.GatewayResponse{}
	i.After(context.Background(), req, resp)

	if len(store.policies) != 0 {
		t.Errorf("expected no policies, got %+v", store.policies)
	}
}

func TestPolicyReloadInterceptor_SkipsFailedUpdate(t *testing.T) {
	store := &recordingPolicyStore{}
	i := NewPolicyReloadInterceptor(&fakeStoreHost{ref: &fakeRef{}, store: store})

	req := &gateway.GatewayRequest{CallID: permissionUpdateCallable}
	resp := &gateway.GatewayResponse{Error: errors.New("denied")}
	i.After(context.Background(), req, resp)

	if len(store.policies) != 0 {
		t.Errorf("expected no policies after failed update, got %+v", store.policies)
	}
}

func TestPolicyReloadInterceptor_ReloadsOnSuccess(t *testing.T) {
	matrix := gen.PermissionMatrix{
		Entries: []gen.PermissionEntry{
			{Role: "viewer", Actions: map[string]bool{"workspace.create": false}},
		},
	}
	store := &recordingPolicyStore{}
	host := &fakeStoreHost{ref: &fakeRef{matrix: matrix}, store: store}
	i := NewPolicyReloadInterceptor(host)

	req := &gateway.GatewayRequest{CallID: permissionUpdateCallable}
	resp := &gateway.GatewayResponse{}
	i.After(context.Background(), req, resp)

	// After runs a detached goroutine; wait briefly for it to finish.
	time.Sleep(50 * time.Millisecond)

	want := []actor.Policy{{Scope: "workspace.create", Role: actor.Role("viewer"), Allow: false}}
	if !reflect.DeepEqual(store.policies, want) {
		t.Errorf("policies mismatch:\ngot:  %+v\nwant: %+v", store.policies, want)
	}
}

func TestPolicyReloadInterceptor_NoHost(t *testing.T) {
	i := NewPolicyReloadInterceptor(nil)

	req := &gateway.GatewayRequest{CallID: permissionUpdateCallable}
	resp := &gateway.GatewayResponse{}
	// Must not panic when host is nil.
	i.After(context.Background(), req, resp)
}

func TestPolicyReloadInterceptor_BeforePasses(t *testing.T) {
	i := NewPolicyReloadInterceptor(nil)
	if err := i.Before(context.Background(), &gateway.GatewayRequest{}); err != nil {
		t.Errorf("Before returned %v; want nil", err)
	}
}
