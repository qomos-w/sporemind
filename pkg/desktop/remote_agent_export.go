package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// remoteAgentExportCallID* are the remote gateway callables the host process
// invokes over the HTTP JSON API (POST {base}/api/<callID>). They are the
// same public callables the frontend already uses; no new remote-side callable
// was required for agent context import.
const (
	remoteWorkspaceListAgentsCallID = "workspace.list_agents"
	remoteSessionForkCallID         = "local.session_fork"
)

// RemoteAgentBrief is the frontend-facing summary of one agent on a remote
// gateway. It is the projection of the remote workspace.list_agents AgentRef
// down to the fields the import UI needs.
type RemoteAgentBrief struct {
	ActorID      string `json:"actorId"`
	DisplayName  string `json:"displayName"`
	AgentKind    string `json:"agentKind"`
	Status       string `json:"status,omitempty"`
	ProjectName  string `json:"projectName,omitempty"`
	LastActivity string `json:"lastActivity,omitempty"`
}

// RemoteAgentContext is a remote agent's exported session snapshot. It is the
// exact shape the remote returns from local.session_fork, so no mapping layer
// is needed: the importer feeds it straight back into the local import
// callable. Goal is passed through untouched — keeping or dropping it is the
// importer's decision (the clone precedent drops it).
type RemoteAgentContext = domain.AgentSessionForkResp

// remoteSession resolves a saved connection to a live, authenticated remote
// gateway session: its base URL plus a short-lived bearer token. The token is
// minted per call and lives only in the caller's stack frame — it is never
// persisted, cached, or handed to the frontend. Any failure here is a login
// problem (credentials missing, unsealing failed, or the remote rejected the
// credentials).
func (a *App) remoteSession(connID string) (baseURL, token string, err error) {
	a.connMu.Lock()
	doc, err := a.connectionsLocked()
	if err != nil {
		a.connMu.Unlock()
		return "", "", err
	}
	var conn *remoteConnection
	for i := range doc.Connections {
		if doc.Connections[i].ID == connID {
			conn = &doc.Connections[i]
			break
		}
	}
	if conn == nil {
		a.connMu.Unlock()
		return "", "", fmt.Errorf("desktop: connection %q not found", connID)
	}
	if conn.PasswordEnc == "" || conn.Username == "" {
		a.connMu.Unlock()
		return "", "", fmt.Errorf("desktop: connection %q has no saved credentials", connID)
	}
	key, err := loadConnectionKey()
	if err != nil {
		a.connMu.Unlock()
		return "", "", err
	}
	password, err := openPassword(key, conn.PasswordEnc)
	if err != nil {
		a.connMu.Unlock()
		return "", "", err
	}
	baseURL = connectionBaseURL(conn.Host, conn.Port)
	username := conn.Username
	a.connMu.Unlock()

	auth, err := remoteAuthLogin(baseURL, username, password)
	if err != nil {
		return "", "", fmt.Errorf("desktop: remote login failed: %w", err)
	}
	if strings.TrimSpace(auth.Token) == "" {
		return "", "", fmt.Errorf("desktop: remote login for connection %q returned no token", connID)
	}
	return baseURL, auth.Token, nil
}

// remoteInvoke POSTs a JSON request to a remote gateway callable and decodes
// the JSON response. When target is non-empty it is attached as the ?target
// routing query (gateway actor addressing); a nil req sends an empty object.
func remoteInvoke(baseURL, token, callID, target string, req, resp any) error {
	body := []byte("{}")
	if req != nil {
		encoded, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("desktop: encode %s request: %w", callID, err)
		}
		body = encoded
	}
	endpoint := baseURL + "/api/" + callID
	if target != "" {
		endpoint += "?target=" + url.QueryEscape(target)
	}
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("desktop: build %s request: %w", callID, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: remoteAuthTimeout}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("desktop: remote call %s: %w", callID, err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<22))
	if err != nil {
		return fmt.Errorf("desktop: read %s response: %w", callID, err)
	}
	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusNoContent {
		return classifyRemoteError(callID, target, httpResp.StatusCode, raw)
	}
	if resp == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, resp); err != nil {
		return fmt.Errorf("desktop: decode %s response: %w", callID, err)
	}
	return nil
}

// classifyRemoteError maps a non-2xx remote gateway response onto the three
// failure classes the import UI must tell apart:
//
//   - 401/403: the login was rejected or the account lacks the role — a login
//     / permission problem.
//   - unknown target actor: the gateway resolves ?target against the live
//     actor tree and answers 404 "service not found" when it is gone, so a
//     404 on a target-scoped call means the remote has no such agent.
//   - missing API: an older remote either does not serve the callable at all
//     (404 with no target) or resolves the actor but has no handler registered
//     for it (500 "... call ID ... not registered").
func classifyRemoteError(callID, target string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("desktop: remote login rejected calling %s (HTTP %d): %s", callID, status, msg)
	case http.StatusNotFound:
		if target != "" {
			return fmt.Errorf("desktop: remote has no agent %q (HTTP 404): %s", target, msg)
		}
		return fmt.Errorf("desktop: remote %s API missing (HTTP 404) — remote sporemind too old: %s", callID, msg)
	}
	if isUnknownRemoteCallable(msg) {
		return fmt.Errorf("desktop: remote %s API missing — remote sporemind too old: %s", callID, msg)
	}
	return fmt.Errorf("desktop: remote call %s failed (HTTP %d): %s", callID, status, msg)
}

// isUnknownRemoteCallable reports whether a gateway error body is the
// "callable not registered on a resolved actor" signal, which marks a remote
// that predates the callable rather than a routing or credential failure.
func isUnknownRemoteCallable(msg string) bool {
	return strings.Contains(msg, "not registered") || strings.Contains(msg, "service not found")
}

// RemoteAgentList returns the agents visible on a saved remote connection,
// mapped to the fields the import picker renders. The saved credentials never
// reach the frontend: the host logs in, calls workspace.list_agents with the
// bearer token, and returns only the projected briefs.
func (a *App) RemoteAgentList(connID string) ([]RemoteAgentBrief, error) {
	baseURL, token, err := a.remoteSession(connID)
	if err != nil {
		return nil, err
	}
	var resp domain.AgentRefListResp
	if err := remoteInvoke(baseURL, token, remoteWorkspaceListAgentsCallID, "", nil, &resp); err != nil {
		return nil, err
	}
	briefs := make([]RemoteAgentBrief, 0, len(resp.Items))
	for _, ref := range resp.Items {
		briefs = append(briefs, RemoteAgentBrief{
			ActorID:      ref.ActorID,
			DisplayName:  ref.DisplayName,
			AgentKind:    ref.AgentKind,
			Status:       ref.Status,
			ProjectName:  ref.ProjectName,
			LastActivity: ref.LastActivity,
		})
	}
	return briefs, nil
}

// RemoteAgentContextExport fetches a remote agent's full current session
// context in a single call. local.session_fork with an empty AtTurnID returns
// the complete snapshot (turns, steps, summaries, explore results, goal), so no
// paged export is needed. The call is Public on the remote — no admin role
// required — and the token exists only for the duration of this call.
func (a *App) RemoteAgentContextExport(connID, agentID string) (RemoteAgentContext, error) {
	if strings.TrimSpace(agentID) == "" {
		return RemoteAgentContext{}, fmt.Errorf("desktop: remote agent id is required")
	}
	baseURL, token, err := a.remoteSession(connID)
	if err != nil {
		return RemoteAgentContext{}, err
	}
	// An empty AtTurnID is the full-snapshot request; the typed zero value
	// marshals to {} and decodes back to the zero AtTurnID on the remote.
	var resp domain.AgentSessionForkResp
	if err := remoteInvoke(baseURL, token, remoteSessionForkCallID, agentID, domain.AgentSessionForkReq{}, &resp); err != nil {
		return RemoteAgentContext{}, err
	}
	return resp, nil
}
