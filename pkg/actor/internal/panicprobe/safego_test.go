package panicprobe

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestSafeGo_PanicRecovered reports the diagnostic and never propagates.
func TestSafeGo_PanicRecovered(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	SafeGo(ctx, "boom", func() {
		panic("kaboom")
	})

	raw := oracleRef.waitForCall(t)
	var diag domain.OracleReportDiagnosticReq
	if err := json.Unmarshal(raw, &diag); err != nil {
		t.Fatalf("unmarshal diagnostic: %v", err)
	}
	if diag.Source != "goroutine.boom" {
		t.Errorf("Source = %q, want goroutine.boom", diag.Source)
	}
	if !strings.Contains(diag.RawData, "kaboom") {
		t.Errorf("RawData missing panic value: %q", diag.RawData)
	}
	if !strings.Contains(diag.RawData, "goroutine panic: boom") {
		t.Errorf("RawData missing goroutine panic marker: %q", diag.RawData)
	}
	if !strings.Contains(diag.Message, "boom") {
		t.Errorf("Message = %q, want mention boom", diag.Message)
	}
	if !strings.Contains(diag.Message, "goroutine boom") {
		t.Errorf("Message = %q, want mention goroutine name", diag.Message)
	}
}

// TestSafeGo_NormalReturn does not report.
func TestSafeGo_NormalReturn(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	var ran bool
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(ctx, "ok", func() {
		defer wg.Done()
		ran = true
	})
	wg.Wait()

	if !ran {
		t.Fatal("fn did not run")
	}
	select {
	case <-oracleRef.closeCh:
		t.Errorf("diagnostic was sent for a normal return path")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSafeGo_NilCtx recovers without crashing when ctx is nil.
func TestSafeGo_NilCtx(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(nil, "nilctx", func() {
		defer wg.Done()
		panic("ignored")
	})
	wg.Wait()
}

// TestSafeGoBackground_PanicRecovered does not crash the process.
func TestSafeGoBackground_PanicRecovered(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGoBackground("bg", func() {
		defer wg.Done()
		panic("bg boom")
	})
	wg.Wait()
}
