// Package peerserver exposes an HTTP surface that allows external sporemind
// apps (clients) to handshake and invoke services this app has marked as
// cross-app exposed.
package peerserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

const (
	defaultRequestTimeout = 30 * time.Second
	// HeaderPeerCallID carries the callID for binary /peer/invoke requests.
	HeaderPeerCallID = "X-Peer-Call-ID"
)

// PeerAuthenticator authenticates an incoming peer HTTP request. It returns the
// peerID that the request is acting as, or ok=false if authentication fails.
// Implementations can use headers, query params, TLS certs, signed challenges,
// etc.
type PeerAuthenticator interface {
	Authenticate(r *http.Request) (peerID string, ok bool)
}

// Server is the peer HTTP server. It registers two routes:
//   POST /peer/handshake — exchange version and exported service surface.
//   POST /peer/invoke    — invoke an exposed callable on behalf of a peer.
type Server struct {
	app         app.App
	protoMgr    *protocol.Manager
	auth        PeerAuthenticator
	serverID    string
	serverVersion string
}

// New creates a peer server. serverID identifies this app instance to peers;
// serverVersion is used for same-version system-namespace sharing.
func New(a app.App, protoMgr *protocol.Manager, auth PeerAuthenticator, serverID, serverVersion string) *Server {
	if auth == nil {
		auth = &AllowAllAuthenticator{}
	}
	return &Server{
		app:           a,
		protoMgr:      protoMgr,
		auth:          auth,
		serverID:      serverID,
		serverVersion: serverVersion,
	}
}

// RegisterRoutes adds the peer HTTP handlers to mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/peer/handshake", s.handleHandshake)
	mux.HandleFunc("/peer/invoke", s.handleInvoke)
}

// HandshakeReq is the body of POST /peer/handshake.
type HandshakeReq struct {
	ClientID string `json:"clientID"`
	Version  string `json:"version"`
}

// HandshakeResp is the body of POST /peer/handshake.
type HandshakeResp struct {
	ServerID     string                    `json:"serverID"`
	SharedSystem bool                      `json:"sharedSystem"`
	Surface      []protocol.ExportedService `json:"surface"`
}

// InvokeReq is the body of POST /peer/invoke.
type InvokeReq struct {
	CallID  string `json:"callID"`
	Payload any    `json:"payload,omitempty"`
}

func (s *Server) handleHandshake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	peerID, ok := s.auth.Authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad body")
		return
	}
	var req HandshakeReq
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	if req.ClientID == "" {
		req.ClientID = peerID
	}

	if err := s.protoMgr.RegisterSporemindPeer(req.ClientID, req.Version); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("register peer: %v", err))
		return
	}

	surface, err := s.protoMgr.ExportForPeer(req.ClientID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("export surface: %v", err))
		return
	}

	_, sharedSystem, _ := s.protoMgr.IsSporemindPeer(req.ClientID)
	resp := HandshakeResp{
		ServerID:     s.serverID,
		SharedSystem: sharedSystem,
		Surface:      surface,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	peerID, ok := s.auth.Authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req InvokeReq
	var rawBody []byte
	isBinary := r.Header.Get("Content-Type") == "application/octet-stream"
	if isBinary {
		req.CallID = r.Header.Get(HeaderPeerCallID)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad body")
			return
		}
		rawBody = body
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad body")
			return
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
	}
	if req.CallID == "" {
		writeError(w, http.StatusBadRequest, "missing callID")
		return
	}

	service := callIDNamespace(req.CallID)
	if service == "" {
		writeError(w, http.StatusBadRequest, "invalid callID")
		return
	}
	if !s.protoMgr.IsServiceExposedToPeer(peerID, service) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("service %q not exposed to peer %q", service, peerID))
		return
	}

	targetRef, ok := resolveTarget(s.app, req.CallID, "")
	if !ok {
		writeError(w, http.StatusNotFound, "target actor not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	var payload any = req.Payload
	if isBinary {
		payload = rawBody
	}

	call := targetRef.Invoke(ctx, req.CallID, payload, map[string]string{
		"gospore.caller_role":   "peer",
		"gospore.peer_id":       peerID,
		"gospore.peer_service":  service,
	})
	if call == nil {
		writeError(w, http.StatusInternalServerError, "invoke failed")
		return
	}
	defer call.Close()

	switch call.Mode() {
	case invoke.CallModeTell:
		w.WriteHeader(http.StatusAccepted)

	case invoke.CallModeUnary:
		raw, err := call.RecvRaw()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if isBinary {
			w.Header().Set("Content-Type", "application/octet-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		if raw == nil {
			w.WriteHeader(http.StatusNoContent)
		} else {
			_, _ = w.Write(raw)
		}

	case invoke.CallModeStream:
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		go func() {
			<-r.Context().Done()
			call.Cancel()
		}()
		for {
			raw, err := call.RecvRaw()
			if err == io.EOF {
				fmt.Fprintf(w, "data: %s\n\n", `{"done":true}`)
				flusher.Flush()
				break
			}
			if err != nil {
				fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"error":%q}`, err.Error()))
				flusher.Flush()
				break
			}
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
	}
}

func callIDNamespace(callID string) string {
	if i := strings.LastIndex(callID, "."); i > 0 {
		return callID[:i]
	}
	return ""
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func resolveTarget(a app.App, callID, target string) (ref.Ref, bool) {
	// Prefer resolving via service when target is empty.
	if target == "" {
		svc := callIDNamespace(callID)
		if svc != "" {
			if r, ok := a.LookupService(svc); ok {
				return r, true
			}
		}
	}
	if target == "" {
		if self := a.Self(); self != nil {
			return self, true
		}
	}
	return nil, false
}
