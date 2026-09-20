// Package panicprobe provides a Guard wrapper that converts handler panics
// into business errors. gospore's supervisor package (see
// github.com/qomos-w/gospore/supervisor) documents that handler-returned
// errors do NOT consult the supervisor; only panics do. By recovering the
// panic, reporting it as a Diagnostic to the oracle actor, and returning the
// recovered value as an error, Guard prevents the supervisor Restart path
// that would otherwise re-run OnStart and (in the workspace actor) rewrite
// every running agent's Status to "paused".
package panicprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// oracleReportTimeout caps how long a fire-and-forget diagnostic send waits
// for the oracle actor to acknowledge. The oracle actor's handleReportDiagnostic
// is a fast in-memory append; 5s is generous while still bounding goroutine
// lifetime if oracle is wedged.
const oracleReportTimeout = 5 * time.Second

// Guard wraps fn. On panic it:
//   - logs "panicprobe: recovered" with source, panic value, and full stack;
//   - fires a domain.OracleReportDiagnosticReq at the oracle service from a
//     background goroutine (Tell mode with nested recover — can never block
//     or re-panic into the caller);
//   - returns the recovered value as a wrapped error.
//
// Returning an error instead of re-panicking means gospore treats this as a
// business error and does NOT consult the supervisor — the actor stays alive,
// avoiding the workspace OnStart re-run that would otherwise flip every
// running agent to "paused".
//
// source is the callable ID (e.g. "workspace.create_agent"). req is the
// caller's request payload; it is JSON-marshaled into Diagnostic.RawData
// alongside the stack trace for /debug/problems inspection.
// Guard can wrap both stateful (Context) and stateless (PureContext)
// handlers: it only needs Logger and LookupService, both thread-safe.
func Guard[TReq any, TResp any](
	ctx actor.PureContext,
	source string,
	req TReq,
	fn func() (TResp, error),
) (resp TResp, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			ctx.Logger().Error("panicprobe: recovered",
				"source", source,
				"panic", r,
				"stack", string(stack),
			)
			reportToOracle(ctx, source, req, r, stack)
			err = fmt.Errorf("%s: panic: %v", source, r)
			resp = *new(TResp)
		}
	}()
	return fn()
}

// reportToOracle sends a diagnostic to the oracle service. Best-effort:
// - Uses context.Background() because ctx.Lifecycle() is cancelled by the
//   time the handler returns and we need the send to outlive the handler.
// - Runs in a panic-proof goroutine so an unreachable oracle or a panic
//   during marshaling can never re-enter the business path.
func reportToOracle[TReq any](ctx actor.PureContext, source string, req TReq, recovered any, stack []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("panicprobe: reportToOracle panic: %v\n%s\n", r, debug.Stack())
		}
	}()
	oracleRef, ok := ctx.LookupService("oracle")
	if !ok || oracleRef == nil {
		return
	}
	payload, err := json.Marshal(buildDiagnosticReq(source, req, recovered, stack))
	if err != nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("panicprobe: goroutine panic: %v\n%s\n", r, debug.Stack())
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

func buildDiagnosticReq[TReq any](source string, req TReq, recovered any, stack []byte) domain.OracleReportDiagnosticReq {
	rawData := fmt.Sprintf(
		"panic source: %s\n\n--- panic ---\n%v\n\n--- stack ---\n%s\n\n--- request ---\n%s\n",
		source,
		recovered,
		string(stack),
		marshalReq(req),
	)
	return domain.OracleReportDiagnosticReq{
		Severity:   "error",
		Source:     source,
		Message:    fmt.Sprintf("handler %s panicked: %v", source, recovered),
		CallableID: source,
		RawData:    rawData,
	}
}

func marshalReq[TReq any](req TReq) string {
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(b)
}
