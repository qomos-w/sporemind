package panicprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// SafeGo runs fn in a new goroutine with panic recovery. On panic it:
//   - logs "panicprobe: goroutine panic" with name, panic value, stack;
//   - fires a diagnostic at the oracle service (best-effort, fire-and-forget);
//   - exits cleanly instead of crashing the process.
//
// ctx is the actor.Context that owns this goroutine. It is only used for
// logger lookup and oracle service resolution; the goroutine outlives the
// caller's handler (see reportToOracle for why a background context is used
// inside).
//
// Pass nil ctx only when there is genuinely no actor owner — prefer
// SafeGoBackground for that case so the call site documents intent.
func SafeGo(ctx actor.Context, name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if ctx != nil {
					ctx.Logger().Error("panicprobe: goroutine panic",
						"name", name,
						"panic", r,
						"stack", string(stack),
					)
					reportGoroutineToOracle(ctx, name, r, stack)
				} else {
					fmt.Printf("panicprobe: goroutine panic in %s: %v\n%s\n", name, r, stack)
				}
			}
		}()
		fn()
	}()
}

// SafeGoBackground is the no-actor variant. It recovers and logs to stdout
// but cannot report to the oracle because there is no actor context to
// resolve the service through. Use this for goroutines spawned from package
// init code, time wheels, log rings — anything outside the actor tree.
func SafeGoBackground(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("panicprobe: goroutine panic in %s: %v\n%s\n", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

func reportGoroutineToOracle(ctx actor.Context, name string, recovered any, stack []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("panicprobe: reportGoroutineToOracle panic: %v\n%s\n", r, debug.Stack())
		}
	}()
	oracleRef, ok := ctx.LookupService("oracle")
	if !ok || oracleRef == nil {
		return
	}
	rawData := fmt.Sprintf(
		"goroutine panic: %s\n\n--- panic ---\n%v\n\n--- stack ---\n%s\n",
		name,
		recovered,
		string(stack),
	)
	req := domain.OracleReportDiagnosticReq{
		Severity:   "error",
		Source:     "goroutine." + name,
		Message:    fmt.Sprintf("goroutine %s panicked: %v", name, recovered),
		CallableID: "goroutine." + name,
		RawData:    rawData,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("panicprobe: goroutine send panic: %v\n%s\n", r, debug.Stack())
			}
		}()
		bgCtx, cancel := context.WithTimeout(context.Background(), oracleReportTimeout)
		defer cancel()
		call := oracleRef.Invoke(bgCtx, "oracle.report_diagnostic", payload)
		if call != nil {
			_ = call.Close()
		}
	}()
}
