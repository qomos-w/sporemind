// Package main builds a sample sporemind plugin using the SDK.
//
// The same source builds both transports (build tags pick the entry point):
//
//	# prod: in-process c-shared library (FFI ABI, requires cgo)
//	go build -buildmode=c-shared -o plugin-hello.dll .
//
//	# dev: standalone subprocess executable (stdin/stdout framing)
//	CGO_ENABLED=0 go build -o plugin-hello.exe .
//
// The c-shared //export symbols live in main_cgo.go (//go:build cgo); the
// subprocess entry is main_nocgo.go (//go:build !cgo, calls sdk.RunProcess,
// which is the SDK's process_main.go). Use directory mode (".") so all files
// are selected — a file-list build would drop main_cgo.go's exports. The
// shared plugin definition (Register + lifecycle) stays here in the untagged
// main.go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// errString renders err for JSON payloads (nil -> "").
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func init() {
	sdk.Register(&sdk.Plugin{
		Manifest: sdk.Manifest{
			ID:          "com.example.hello",
			Name:        "Hello Plugin",
			Version:     "1.0.0",
			Permissions: []string{sdk.PermConfigRead, sdk.PermLLMInvoke},
			Callables: []sdk.Callable{
				{ID: "greet", RequestSchema: "GreetRequest", ResponseSchema: "GreetResponse"},
				{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse"},
				{ID: "reverse", RequestSchema: "ReverseRequest", ResponseSchema: "ReverseResponse"},
				{ID: "ask", RequestSchema: "AskRequest", ResponseSchema: "AskResponse"},
				{ID: "ask_cancel", RequestSchema: "AskRequest", ResponseSchema: "AskResponse"},
			},
			Entrypoints: []sdk.Entrypoint{
				{Kind: "page", ID: "hello.dashboard", Title: "Hello Dashboard", Route: "/"},
				{Kind: "panel", ID: "hello.panel", Title: "Hello Panel", Route: "/panel"},
				{Kind: "settings", ID: "hello.settings", Title: "Hello Settings", Route: "/settings"},
				{Kind: "command", ID: "hello.greet", Title: "Greet", Route: "greet"},
			},
		},
		OnLoad: func(ctx sdk.Context) error {
			ctx.RegisterCallable("greet", func(req sdk.Request) (sdk.Response, error) {
				var payload struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(req.Payload, &payload)
				ctx.Log(sdk.LogLevelInfo, "greet called for %s", payload.Name)
				msg := fmt.Sprintf("Hello, %s!", payload.Name)
				return sdk.Response{Payload: map[string]string{"message": msg}}, nil
			})
			ctx.RegisterCallable("ping", func(req sdk.Request) (sdk.Response, error) {
				ctx.Log(sdk.LogLevelDebug, "ping received")
				return sdk.Response{Payload: map[string]string{"pong": "ok"}}, nil
			})
			// reverse demonstrates a plugin-to-host reverse call (the
			// subprocess transport carries it as a 0x03/0x04 frame pair via
			// the injected IPC host; the c-shared transport uses the FFI
			// host bridge instead).
			ctx.RegisterCallable("reverse", func(req sdk.Request) (sdk.Response, error) {
				var p struct {
					CallID  string          `json:"callID"`
					Payload json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal(req.Payload, &p); err != nil {
					return sdk.Response{}, err
				}
				out, err := ctx.Host().Invoke(p.CallID, json.RawMessage(p.Payload))
				if err != nil {
					return sdk.Response{}, err
				}
				return sdk.Response{Payload: json.RawMessage(out)}, nil
			})
		// ask demonstrates streaming LLM consumption inside a handler: text
		// deltas arrive chunk-by-chunk (0x07 frames on the subprocess
		// transport; a single chunk on FFI/old hosts), while the terminal
		// return still carries the aggregated response. Handlers that don't
		// need incremental output can call Complete instead.
		ctx.RegisterCallable("ask", func(req sdk.Request) (sdk.Response, error) {
			var p struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return sdk.Response{}, err
			}
			var deltas int
			resp, err := ctx.Host().InvokeStream(
				"llm.complete",
				map[string]any{"prompt": p.Prompt},
				sdk.ForwardLLMChunks(
					func(c sdk.LLMChunk) error {
						if c.Kind == "text_delta" {
							deltas++
							ctx.Log(sdk.LogLevelDebug, "ask delta %d: %s", deltas, c.Text)
						}
						return nil
					}))
			if err != nil {
				return sdk.Response{}, err
			}
			// deltas lets e2e tests observe whether the host actually
			// streamed (deltas > 0) or degraded to unary (deltas == 0,
			// terminal still intact).
			return sdk.Response{Payload: map[string]any{
				"answer":  json.RawMessage(resp),
				"deltas":  deltas,
			}}, nil
		})
		// ask_cancel demonstrates mid-stream cancellation on transports
		// implementing sdk.CanceledHost: the ctx expiry sends a 0x09
		// reverse-cancel so the HOST aborts the upstream LLM dispatch
		// instead of streaming to completion on an abandoned consumer.
		// On transports without CanceledHost the behavior degrades to the
		// legacy local abandon (the host stream finishes; the plugin stops
		// waiting).
		ctx.RegisterCallable("ask_cancel", func(req sdk.Request) (sdk.Response, error) {
			var p struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return sdk.Response{}, err
			}
			ch, ok := ctx.Host().(sdk.CanceledHost)
			if !ok {
				return sdk.Response{Payload: map[string]any{"cancelled": false, "reason": "no-canceled-host"}}, nil
			}
			cctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, err := ch.InvokeStreamCtx(cctx, "llm.complete",
				map[string]any{"prompt": p.Prompt},
				sdk.ForwardLLMChunks(func(c sdk.LLMChunk) error { return nil }))
			return sdk.Response{Payload: map[string]any{
				"cancelled": err != nil,
				"err":       errString(err),
			}}, nil
		})
		return nil
	},
		OnUnload: func(ctx sdk.Context) error {
			ctx.UnregisterCallable("greet")
			ctx.UnregisterCallable("ping")
			return nil
		},
	})
}
