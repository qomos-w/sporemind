// Package sporebridge exposes sporemind actor callables to sporescripts.
package sporebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/script"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// Bridge wires a host's service registry into a sporescript Runtime so that
// script code can invoke actor callables via a host-bound function.
type Bridge struct {
	host ServiceHost
	role string // propagated as gospore.caller_role on every invoke
}

// ServiceHost is the minimal seam set the Bridge needs from its host:
// service-name resolution plus a self-reference fallback. gospore's
// app.App satisfies it; an actor can adapt its actor.Context seams
// (LookupService / Self) without holding the whole App.
type ServiceHost interface {
	LookupService(name string) (ref.Ref, bool)
	Self() ref.Ref
}

// New creates a Bridge backed by the given gospore App.
// The default caller role is "developer" because the canonical Spore host
// is the desktop frontend.  Callers that bind the Bridge inside an actor
// (e.g. an agent executor) should use WithRole to match the actor's identity.
func New(a app.App) *Bridge {
	return &Bridge{host: a, role: "developer"}
}

// NewHost creates a Bridge from a minimal ServiceHost — e.g. an actor-held
// adapter over its actor.Context seams — with the same default role.
func NewHost(h ServiceHost) *Bridge {
	return &Bridge{host: h, role: "developer"}
}

// WithRole returns a copy of the Bridge with the given caller role.
// The role is forwarded as gospore.caller_role on every actor invocation.
func (b *Bridge) WithRole(role string) *Bridge {
	return &Bridge{host: b.host, role: role}
}

// BindTo registers host.invoke(callID, payload) on the Spore Runtime.
// After binding, sporescripts can write:
//
//	import { invoke } from "host"
//	let graph = invoke("unified_graph.snapshot", {})
//
// Both payload and return value are map<string, any> — structured data
// with no manual JSON (de)serialization in the script.
func (b *Bridge) BindTo(rt *script.Runtime) error {
	if err := rt.BindFunc("host", "invoke", b.invoke); err != nil {
		return fmt.Errorf("bind host.invoke: %w", err)
	}
	return nil
}

// invoke resolves an actor callable by callID, forwards the payload,
// and returns the decoded JSON response as a map.
func (b *Bridge) invoke(callID string, payload map[string]any) (map[string]any, error) {
	return b.InvokeCtx(context.Background(), callID, payload)
}

// InvokeCtx is the context-aware invoke: cancellation from ctx propagates
// into the actor call, while the per-call wall clock stays capped at
// domain.DefaultInvokeTimeout. A nil ctx is treated as Background.
func (b *Bridge) InvokeCtx(ctx context.Context, callID string, payload map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, domain.DefaultInvokeTimeout)
	defer cancel()

	targetRef, ok := resolveActorRef(b.host, callID)
	if !ok {
		return nil, fmt.Errorf("callable %q not found", callID)
	}

	var body []byte
	if payload != nil && len(payload) > 0 {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
	}

	call := targetRef.Invoke(callCtx, callID, body, map[string]string{
		"gospore.caller_role": b.role,
	})
	if call == nil {
		return nil, fmt.Errorf("invoke %q returned nil call", callID)
	}
	defer call.Close()

	raw, err := call.RecvRaw()
	if err != nil {
		// io.EOF on unary calls with no body is normal (void / tell-style).
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("recv raw: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return result, nil
}

// InvokeMap is an alias for invoke; kept for backward compatibility.
func (b *Bridge) InvokeMap(callID string, payload map[string]any) (map[string]any, error) {
	return b.invoke(callID, payload)
}

// resolveActorRef mirrors the logic in pkg/desktop/app.go.
func resolveActorRef(h ServiceHost, callID string) (ref.Ref, bool) {
	service := serviceFromCallID(callID)
	if service != "" {
		if r, ok := h.LookupService(service); ok {
			return r, true
		}
	}
	if r := h.Self(); r != nil {
		return r, true
	}
	return nil, false
}

func serviceFromCallID(callID string) string {
	if i := strings.Index(callID, "."); i > 0 {
		return callID[:i]
	}
	return callID
}
