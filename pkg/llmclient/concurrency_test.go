package llmclient

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubStream is a minimal Stream implementation for testing.
type stubStream struct {
	events chan Event
	closed atomic.Bool
}

func newStubStream() *stubStream {
	return &stubStream{events: make(chan Event, 1)}
}

func (s *stubStream) Events() <-chan Event { return s.events }
func (s *stubStream) Close() error {
	s.closed.Store(true)
	return nil
}
func (s *stubStream) Telemetry() RequestTelemetry { return RequestTelemetry{} }

// stubClient records how many Stream calls are in-flight.
type stubClient struct {
	mu       sync.Mutex
	inflight int32
	streams  []*stubStream
}

func (c *stubClient) Stream(_ context.Context, _ Request) (Stream, error) {
	s := newStubStream()
	c.mu.Lock()
	c.inflight++
	c.streams = append(c.streams, s)
	c.mu.Unlock()
	return s, nil
}

func (c *stubClient) inflightCount() int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

func (c *stubClient) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.streams {
		s.Close()
	}
	c.inflight = 0
}

func TestConcurrencyClient_LimitsInflight(t *testing.T) {
	// Clean up the global semPool after test.
	defer semPool.Range(func(key, _ any) bool { semPool.Delete(key); return true })

	const max = 2
	inner := &stubClient{}
	client := NewConcurrencyClient(inner, "https://api.test.com", "sk-1", max)

	// Acquire both slots.
	s1, err := client.Stream(context.Background(), Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := client.Stream(context.Background(), Request{Model: "m2"})
	if err != nil {
		t.Fatal(err)
	}

	// Third call should block.
	done := make(chan struct{})
	go func() {
		s3, err := client.Stream(context.Background(), Request{Model: "m3"})
		if err != nil {
			t.Error(err)
			return
		}
		s3.Close()
		close(done)
	}()

	// Give the goroutine time to block.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("third Stream should have blocked")
	default:
	}

	// Release one slot.
	s1.Close()

	// Now the third call should complete.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("third Stream should have unblocked after release")
	}

	s2.Close()
}

func TestConcurrencyClient_SharedKey(t *testing.T) {
	defer semPool.Range(func(key, _ any) bool { semPool.Delete(key); return true })

	const max = 1
	inner := &stubClient{}
	c1 := NewConcurrencyClient(inner, "https://api.test.com", "sk-1", max)
	c2 := NewConcurrencyClient(inner, "https://api.test.com", "sk-1", max)

	// c1 acquires the only slot.
	s1, err := c1.Stream(context.Background(), Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}

	// c2 (different wrapper instance, same key) should block.
	done := make(chan struct{})
	go func() {
		s2, err := c2.Stream(context.Background(), Request{Model: "m2"})
		if err != nil {
			t.Error(err)
			return
		}
		s2.Close()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("c2.Stream should have blocked — same key shares semaphore")
	default:
	}

	s1.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("c2.Stream should have unblocked")
	}
}

func TestConcurrencyClient_NoLimitWhenZero(t *testing.T) {
	defer semPool.Range(func(key, _ any) bool { semPool.Delete(key); return true })

	inner := &stubClient{}
	client := NewConcurrencyClient(inner, "https://api.test.com", "sk-1", 0)

	// No semaphore registered → all calls go through immediately.
	for i := 0; i < 10; i++ {
		s, err := client.Stream(context.Background(), Request{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}
