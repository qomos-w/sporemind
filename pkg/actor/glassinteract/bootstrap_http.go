package glassinteract

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// BootstrapRoute is the HTTP path MentraOS posts the bootstrap exchange to.
// pkg/runtime registers it on the gateway; being more specific than the
// gateway's generic /api/ proxy, it shadows that route so this endpoint only
// ever invokes glass_interact.bootstrap.
const BootstrapRoute = "/api/glass_interact.bootstrap"

// glassService is the service name the actor exposes via ctx.Expose.
const glassService = "glass_interact"

const (
	// bootstrapBodyLimit caps the JSON request body. GlassBootstrapReq is a
	// key plus optional device/capability fields; 16 KiB is generous.
	bootstrapBodyLimit = 16 << 10
	// bootstrapInvokeTimeout bounds the actor invocation for one request.
	bootstrapInvokeTimeout = 15 * time.Second
)

// bootstrapApp is the minimal app surface the handler needs: resolve the
// glassinteract service and invoke its callable through normal actor
// invocation.
type bootstrapApp interface {
	LookupService(name string) (ref.Ref, bool)
}

// BootstrapHandler returns an http.Handler for POST BootstrapRoute. It accepts
// a JSON GlassBootstrapReq, forwards it to the actor's bootstrap callable via
// service lookup + ref.Invoke, and returns a JSON GlassBootstrapResp.
//
// The pre-shared key is validated by the actor, never here, so the endpoint
// adds no auth bypass and no token/session logic of its own.
func BootstrapHandler(a bootstrapApp) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, bootstrapBodyLimit)
		var req gen.GlassBootstrapReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "request body too large"})
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}

		target, ok := a.LookupService(glassService)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "glassinteract service unavailable"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), bootstrapInvokeTimeout)
		defer cancel()
		call := target.Invoke(ctx, callableBootstrap, req)
		if call == nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invoke failed"})
			return
		}
		defer call.Close()

		raw, err := call.RecvRaw()
		if err != nil {
			w.WriteHeader(bootstrapErrorStatus(err))
			_ = json.NewEncoder(w).Encode(map[string]string{"error": bootstrapErrorMessage(err)})
			return
		}

		var resp gen.GlassBootstrapResp
		if err := json.Unmarshal(raw, &resp); err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid bootstrap response"})
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// bootstrapErrorStatus maps the actor's bootstrap failure to an HTTP status.
// Classification is by the actor's error text; the bodies never carry secret
// material.
func bootstrapErrorStatus(err error) int {
	switch {
	case strings.Contains(err.Error(), "invalid key"):
		return http.StatusUnauthorized
	case strings.Contains(err.Error(), "no bootstrap key configured"):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

// bootstrapErrorMessage returns a sanitized body message for an invoke error.
func bootstrapErrorMessage(err error) string {
	switch bootstrapErrorStatus(err) {
	case http.StatusUnauthorized:
		return "invalid bootstrap key"
	case http.StatusServiceUnavailable:
		return "bootstrap not configured"
	default:
		return "bootstrap failed"
	}
}
