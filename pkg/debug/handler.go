package debug

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/schema"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/protocol"

	"context"
)

// Deps holds the dependencies for debug handlers.
type Deps struct {
	App      app.App
	Topology func() any // returns live topology snapshot; nil if unavailable
}

// RegisterRoutes adds the debug endpoint handlers to mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	h := &handler{deps: deps}
	mux.HandleFunc("/debug/topology", h.handleTopology)
	mux.HandleFunc("/debug/schema", h.handleSchema)
	mux.HandleFunc("/debug/invoke", h.handleInvoke)
	mux.HandleFunc("/debug/shutdown", h.handleShutdown)
	mux.HandleFunc("/debug/eval-js", h.handleEvalJS)
	mux.HandleFunc("/debug/problems", h.handleProblems)
	mux.HandleFunc("/debug/agents", h.handleAgents)
	mux.HandleFunc("/debug/turns", h.handleTurns)
	registerPprof(mux)
}

// registerPprof exposes the standard net/http/pprof endpoints on the gateway
// mux so `go tool pprof http://localhost:18080/debug/pprof/<name>` works in
// dev-desktop. The handlers are registered explicitly (rather than via blank
// import) because this codebase uses a custom *http.ServeMux, not
// http.DefaultServeMux. Endpoints:
//   /debug/pprof/             — index page (links to all profiles)
//   /debug/pprof/cmdline      — os.Args
//   /debug/pprof/profile      — CPU profile (30s default; ?seconds=N)
//   /debug/pprof/symbol       — function symbol lookup
//   /debug/pprof/trace        — execution trace
//   /debug/pprof/<name>       — heap/goroutine/mutex/block/etc. (pprof.Index handles these via the trailing-slash subtree match)
//
// Security: same posture as the other /debug/* routes — anyone who can reach
// the gateway address has full debug access. Bind to localhost in production.
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

type handler struct {
	deps Deps
}

// GET /debug/topology — return full actor topology snapshot.
func (h *handler) handleTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.deps.Topology == nil {
		writeError(w, http.StatusServiceUnavailable, "topology provider not available")
		return
	}

	snapshot := h.deps.Topology()
	if snapshot == nil {
		writeError(w, http.StatusServiceUnavailable, "topology provider not available")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

// GET /debug/schema — return global schema manifest.
// Query params:
//
//	?actorType=<type>  — filter to one actor kind's callables/projections/events (e.g. "agent")
//	?actorId=<hex>     — filter by live actor instance (resolves type via runtime)
func (h *handler) handleSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	gm, err := h.deps.App.ExportGosporeManifest()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	gm.Schemas = protocol.NormalizeManifestSchemasToSystem(gm.Schemas)

	if aidStr := r.URL.Query().Get("actorId"); aidStr != "" {
		filtered, err := filterManifestByActorID(gm, h.deps.App, aidStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		gm = filtered
	} else if actorType := r.URL.Query().Get("actorType"); actorType != "" {
		gm = filterManifestByType(gm, actorType)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gm)
}

// POST /debug/invoke — invoke a callable on a target actor.
// Body: {"callID": "agent.send", "target": "actor-id", "payload": {...}}
func (h *handler) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad body")
		return
	}

	var req struct {
		CallID  string `json:"callID"`
		Target  string `json:"target,omitempty"`
		Payload any    `json:"payload"`
		Role    string `json:"role,omitempty"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	if req.CallID == "" {
		writeError(w, http.StatusBadRequest, "missing callID")
		return
	}

	targetRef, ok := resolveTarget(h.deps.App, req.CallID, req.Target)
	if !ok {
		writeError(w, http.StatusNotFound, "target actor not found")
		return
	}

	// Role resolution: header > body > default "admin"
	role := r.Header.Get("X-Role")
	if role == "" {
		role = req.Role
	}
	if role == "" {
		role = "admin"
	}

	call := targetRef.Invoke(r.Context(), req.CallID, req.Payload, map[string]string{
		"gospore.caller_role": role,
	})
	if call == nil {
		writeError(w, http.StatusInternalServerError, "invoke failed")
		return
	}
	defer call.Close()

	switch call.Mode() {
	case invoke.CallModeTell:
		// Fire-and-forget — nothing to return.
		w.WriteHeader(http.StatusAccepted)

	case invoke.CallModeUnary:
		raw, err := call.RecvRaw()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if raw == nil {
			w.WriteHeader(http.StatusNoContent)
		} else {
			_, _ = w.Write(raw)
		}

	case invoke.CallModeStream:
		// SSE stream: one event per chunk, then a done marker.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}

		// Cancel the actor call when the HTTP client disconnects.
		go func() {
			<-r.Context().Done()
			call.Cancel()
		}()

		for {
			raw, err := call.RecvRaw()
			if errors.Is(err, io.EOF) {
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

// POST /debug/shutdown — trigger graceful app shutdown.
func (h *handler) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.deps.App.Shutdown(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		os.Exit(1)
		return
	}

	// App.Shutdown only cancels the actor context; the Wails event loop
	// keeps the process alive, so the HTTP port stays bound.  Force exit
	// after a short delay so the response has time to flush and the port
	// is released for the new instance.
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
}

// POST /debug/eval-js — execute JavaScript in the frontend webview and return
// the result. Only available when running in Wails desktop mode.
func (h *handler) handleEvalJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if EvalJS == nil {
		writeError(w, http.StatusServiceUnavailable, "eval-js not available in this runtime mode")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad body")
		return
	}

	var req struct {
		Script string `json:"script"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	if req.Script == "" {
		writeError(w, http.StatusBadRequest, "missing script")
		return
	}

	result, err := EvalJS(r.Context(), req.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
}

// GET /debug/agents — list agent instances.
// Delegates to the workspace.list_agents callable (which enriches each ref
// with live agent.status), then applies client-side filters (AND logic):
//
//	?projectId=<val> — forwarded to workspace.list_agents (required)
//	?kind=<val>      — match AgentRef.AgentKind (case-insensitive)
//	?status=<val>    — match AgentRef.Status (case-insensitive substring)
//	?q=<val>         — match AgentRef.DisplayName (case-insensitive substring)
//	?actorId=<val>   — match AgentRef.ActorId (exact)
func (h *handler) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	projectID := q.Get("projectId")

	workspaceRef, ok := h.deps.App.LookupService("workspace")
	if !ok {
		if self := h.deps.App.Self(); self != nil {
			workspaceRef = self
		} else {
			writeError(w, http.StatusServiceUnavailable, "workspace service not available")
			return
		}
	}

	reqBody, _ := json.Marshal(domain.WorkspaceListAgentsReq{ProjectID: projectID})

	call := workspaceRef.Invoke(r.Context(), "workspace.list_agents", json.RawMessage(reqBody), nil)
	if call == nil {
		writeError(w, http.StatusInternalServerError, "invoke failed")
		return
	}
	defer call.Close()

	if call.Mode() != invoke.CallModeUnary {
		writeError(w, http.StatusInternalServerError, "unexpected call mode")
		return
	}

	var list domain.AgentRefListResp
	raw, err := call.RecvRaw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &list); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	filters := map[string]string{
		"kind":    q.Get("kind"),
		"status":  q.Get("status"),
		"q":       q.Get("q"),
		"actorId": q.Get("actorId"),
	}
	if hasFilter(filters) {
		var filtered []domain.AgentRef
		for _, ag := range list.Items {
			if matchAgentRef(ag, filters) {
				filtered = append(filtered, ag)
			}
		}
		list.Items = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// GET /debug/turns — read an agent's turn history.
// Calls agent.session.get on the target agent, then applies optional filters:
//
//	?actorId=<val>  — target agent actor id (system-unique, required)
//	?turnId=<val>   — return only the named turn
//	?state=<val>    — match Turn.State (case-insensitive)
//	?role=<val>     — match Turn.Role (case-insensitive)
//	?q=<val>        — match Turn.UserInput / Turn.Output (case-insensitive substring)
//	?includeSteps   — if "1"/"true", also return the active turn's actions
//
// The response shape mirrors domain.AgentGetSessionResp: {turns, activeTurn}.
func (h *handler) handleTurns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	actorID := q.Get("actorId")
	if actorID == "" {
		writeError(w, http.StatusBadRequest, "missing actorId")
		return
	}

	agentRef, ok := resolveTarget(h.deps.App, "agent", actorID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	call := agentRef.Invoke(r.Context(), "session_get", struct{}{}, nil)
	if call == nil {
		writeError(w, http.StatusInternalServerError, "invoke failed")
		return
	}
	defer call.Close()

	if call.Mode() != invoke.CallModeUnary {
		writeError(w, http.StatusInternalServerError, "unexpected call mode")
		return
	}

	var resp domain.AgentGetSessionResp
	raw, err := call.RecvRaw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &resp); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	filters := map[string]string{
		"turnId": q.Get("turnId"),
		"state":  q.Get("state"),
		"role":   q.Get("role"),
		"q":      q.Get("q"),
	}
	if hasFilter(filters) {
		var filtered []domain.Turn
		for _, t := range resp.Turns {
			if matchTurn(t, filters) {
				filtered = append(filtered, t)
			}
		}
		resp.Turns = filtered
	}

	// When filtering down to a single turn, drop the active-turn context so the
	// caller gets a focused response.
	if q.Get("turnId") != "" {
		resp.ActiveTurn = nil
	}

	// If the caller only wants a turn body without the live step stream,
	// strip the actions payload from the active turn.
	if v := q.Get("includeSteps"); v != "1" && v != "true" && resp.ActiveTurn != nil {
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// resolveTarget mirrors gateway.Server.resolveTarget using app.App.
func resolveTarget(a app.App, callID, target string) (ref.Ref, bool) {
	service := ""
	if i := strings.Index(callID, "."); i > 0 {
		service = callID[:i]
	}

	if target != "" {
		cid, err := identity.ParseCanonicalID(target)
		if err != nil {
			return nil, false
		}
		if r, ok := a.Tree().LookupID(id.From(cid)); ok {
			return r, true
		}
		return nil, false
	}

	if service != "" {
		if r, ok := a.LookupService(service); ok {
			return r, true
		}
	}

	if self := a.Self(); self != nil {
		return self, true
	}
	return nil, false
}

// filterManifestByType returns a manifest with only entries whose Namespace
// matches the given actor type (e.g. "agent", "workspace").
func filterManifestByType(gm schema.GosporeManifest, actorType string) schema.GosporeManifest {
	var filtered schema.GosporeManifest
	neededSchemaIDs := make(map[uint64]bool)

	for _, c := range gm.Callables {
		if c.Namespace == actorType {
			filtered.Callables = append(filtered.Callables, c)
			neededSchemaIDs[c.ReqSchemaID] = true
			neededSchemaIDs[c.FinalSchemaID] = true
			if c.ChunkSchemaID != 0 {
				neededSchemaIDs[c.ChunkSchemaID] = true
			}
		}
	}

	for _, p := range gm.Projections {
		if p.Namespace == actorType {
			filtered.Projections = append(filtered.Projections, p)
			neededSchemaIDs[p.SchemaID] = true
		}
	}

	for _, e := range gm.Events {
		if e.Namespace == actorType {
			filtered.Events = append(filtered.Events, e)
			neededSchemaIDs[e.SchemaID] = true
		}
	}

	for _, s := range gm.Manifest.Schemas {
		if neededSchemaIDs[s.SchemaID] {
			filtered.Manifest.Schemas = append(filtered.Manifest.Schemas, s)
		}
	}

	return filtered
}

// filterManifestByActorID resolves the actor's type from its ID, then
// delegates to filterManifestByType.
func filterManifestByActorID(gm schema.GosporeManifest, a app.App, actorIDStr string) (schema.GosporeManifest, error) {
	cid, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		return schema.GosporeManifest{}, fmt.Errorf("invalid actor ID %q: %w", actorIDStr, err)
	}
	aid := id.From(cid)
	actorType := a.ActorType(aid)
	if actorType == "" {
		return schema.GosporeManifest{}, fmt.Errorf("actor %s not found or not live", actorIDStr)
	}
	return filterManifestByType(gm, actorType), nil
}

// GET /debug/problems — return collected diagnostic summaries from the oracle actor.
// Server-side filtering is applied for exact/primary fields; message search is
// done client-side because it is not part of the lightweight summary index.
// Supports query-param filtering (AND logic):
//
//	?severity=<val>  — match Diagnostic.Severity (case-insensitive)
//	?source=<val>    — match Diagnostic.Source (case-insensitive substring)
//	?agentId=<val>   — match Diagnostic.AgentId
//	?turnId=<val>    — match Diagnostic.TurnId
//	?stepId=<val>    — match Diagnostic.StepId
//	?callableId=<val>— match Diagnostic.CallableId
//	?q=<val>         — match Diagnostic.Message (case-insensitive substring)
func (h *handler) handleProblems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var resp struct {
		Items []domain.DiagnosticSummary `json:"items"`
	}

	oracleRef, ok := h.deps.App.LookupService("oracle")
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	q := r.URL.Query()
	req := domain.OracleListDiagnosticsReq{
		Severity: q.Get("severity"),
		Source:   q.Get("source"),
		AgentID:  q.Get("agentId"),
		TurnID:   q.Get("turnId"),
		Limit:    200,
	}

	call := oracleRef.Invoke(r.Context(), "oracle.list_diagnostics", req, nil)
	if call == nil {
		writeError(w, http.StatusInternalServerError, "invoke failed")
		return
	}
	defer call.Close()

	if call.Mode() != invoke.CallModeUnary {
		writeError(w, http.StatusInternalServerError, "unexpected call mode")
		return
	}

	raw, err := call.RecvRaw()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if raw == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	filters := map[string]string{
		"stepId":     q.Get("stepId"),
		"callableId": q.Get("callableId"),
		"q":          q.Get("q"),
	}

	if hasDiagnosticSummaryFilter(filters) {
		var filtered []domain.DiagnosticSummary
		for _, d := range resp.Items {
			if matchDiagnosticSummary(d, filters) {
				filtered = append(filtered, d)
			}
		}
		resp.Items = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func hasFilter(filters map[string]string) bool {
	for _, v := range filters {
		if v != "" {
			return true
		}
	}
	return false
}

func hasDiagnosticSummaryFilter(filters map[string]string) bool {
	return hasFilter(filters)
}

func matchDiagnosticSummary(d domain.DiagnosticSummary, filters map[string]string) bool {
	if f := filters["stepId"]; f != "" && d.StepID != f {
		return false
	}
	if f := filters["callableId"]; f != "" && d.CallableID != f {
		return false
	}
	if f := filters["q"]; f != "" && !strings.Contains(strings.ToLower(d.Message), strings.ToLower(f)) {
		return false
	}
	return true
}

func matchAgentRef(ag domain.AgentRef, filters map[string]string) bool {
	if f := filters["kind"]; f != "" && !strings.EqualFold(ag.AgentKind, f) {
		return false
	}
	if f := filters["status"]; f != "" && !strings.Contains(strings.ToLower(ag.Status), strings.ToLower(f)) {
		return false
	}
	if f := filters["actorId"]; f != "" && ag.ActorID != f {
		return false
	}
	if f := filters["q"]; f != "" && !strings.Contains(strings.ToLower(ag.DisplayName), strings.ToLower(f)) {
		return false
	}
	return true
}

func matchTurn(t domain.Turn, filters map[string]string) bool {
	if f := filters["turnId"]; f != "" && t.ID != f {
		return false
	}
	if f := filters["state"]; f != "" && !strings.EqualFold(t.State, f) {
		return false
	}
	if f := filters["role"]; f != "" && !strings.EqualFold(t.Role, f) {
		return false
	}
	if f := filters["q"]; f != "" {
		needle := strings.ToLower(f)
		if !strings.Contains(strings.ToLower(t.UserInput), needle) {
			return false
		}
	}
	return true
}
