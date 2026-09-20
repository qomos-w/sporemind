package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
)

type recordingPolicyStore struct {
	policies []actor.Policy
}

func (s *recordingPolicyStore) Evaluate(role actor.Role, scope string) (bool, bool) { return true, false }
func (s *recordingPolicyStore) Reload(policies []actor.Policy) error {
	s.policies = append(s.policies, policies...)
	return nil
}
func (s *recordingPolicyStore) Version() uint64 { return 0 }

type failWaitHost struct{}

func (h *failWaitHost) WaitForAllCellsStart(timeout time.Duration) error {
	return errors.New("wait timeout")
}
func (h *failWaitHost) LookupService(name string) (ref.Ref, bool) { return nil, false }
func (h *failWaitHost) PolicyStore() actor.PolicyStore           { return &recordingPolicyStore{} }

func TestRunPolicyBridge_WaitTimeoutContinues(t *testing.T) {
	// WaitForAllCellsStart failing must not stop the bridge from trying to
	// reload; here LookupService fails, so the observable behavior is that it
	// logs and returns without panicking.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runPolicyBridge(ctx, &failWaitHost{}, 10*time.Millisecond)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runPolicyBridge did not return")
	}
}

type okWaitHost struct {
	store *recordingPolicyStore
}

func (h *okWaitHost) WaitForAllCellsStart(timeout time.Duration) error { return nil }
func (h *okWaitHost) LookupService(name string) (ref.Ref, bool)         { return nil, false }
func (h *okWaitHost) PolicyStore() actor.PolicyStore                     { return h.store }

func TestRunPolicyBridge_BestEffortAfterWait(t *testing.T) {
	// Even when WaitForAllCellsStart succeeds, a missing user service is logged
	// and the function returns without panicking.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := &recordingPolicyStore{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPolicyBridge(ctx, &okWaitHost{store: store}, time.Second)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runPolicyBridge did not return")
	}

	if len(store.policies) != 0 {
		t.Errorf("expected no policies to be reloaded, got %+v", store.policies)
	}
}
