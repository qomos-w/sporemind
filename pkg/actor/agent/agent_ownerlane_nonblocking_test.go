package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestLongToolPureContextDoesNotBlockChatSubmit verifies the owner-lane
// non-blocking migration: capture_profile (a PureContext long tool, previously
// a 30-120s owner-lane blocker) runs concurrently while chat_submit — the
// owner-lane control plane — returns immediately. Both handlers are called
// directly on the same actor instance, which mimics the production topology
// (stateless PureContext goroutine for the tool, stateful owner lane for
// chat_submit). The test is intentionally simple: production lane scheduling
// serializes the same Actor struct access.
func TestLongToolPureContextDoesNotBlockChatSubmit(t *testing.T) {
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// Start a 1-second CPU profile on the stateless pool (PureContext).
	// In production this runs on a dedicated goroutine; the unit test
	// mirrors that by calling the handler directly in a goroutine.
	profileDone := make(chan error, 1)
	go func() {
		_, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{Profile: "cpu", Seconds: 1})
		profileDone <- err
	}()

	// Immediately submit a chat message (owner-lane handler).
	// This must return before the 1s CPU profile completes.
	chatDone := make(chan struct{})
	go func() {
		_, _ = a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "still responsive"})
		close(chatDone)
	}()

	select {
	case <-chatDone:
		// chat_submit returned while the profile was still running — success
	case <-time.After(500 * time.Millisecond):
		// 500ms < 1s profile duration → chat_submit blocked
		t.Fatal("chat_submit did not return while capture_profile was running (owner lane blocked)")
	}

	// Wait for the profile to finish so t.Cleanup doesn't leak goroutines.
	select {
	case err := <-profileDone:
		if err != nil {
			t.Errorf("capture_profile: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture_profile did not finish within 5s")
	}
}
