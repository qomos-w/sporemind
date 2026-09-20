package cookiebridge

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// pendingResponse is the GET /pending payload the Chrome extension polls.
type pendingResponse struct {
	Requests  []pendingRequest `json:"requests"`
	PollAfter int              `json:"pollAfterSeconds"`
}

// pushRequest is the POST /push payload from the Chrome extension.
type pushRequest struct {
	PendingID string         `json:"pendingId"`
	Cookies   []ChromeCookie `json:"cookies"`
}

// pushResponse reports counts only — never cookie values.
type pushResponse struct {
	Status   string `json:"status"` // ok | error
	Imported int64  `json:"imported,omitempty"`
	Skipped  int    `json:"skipped,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (a *Actor) handlePending(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pendingResponse{
		Requests:  a.pending.list(),
		PollAfter: 3,
	})
}

// handlePush receives the extension's confirmed cookie payload, converts it,
// imports it into the target instance, and unblocks the waiting import_site
// call. Payload bytes are decoded once and never logged.
func (a *Actor) handlePush(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, pushResponse{Status: "error", Error: "invalid payload: " + err.Error()})
		return
	}
	entry, ok := a.pending.get(req.PendingID)
	if !ok {
		writeJSON(w, http.StatusNotFound, pushResponse{Status: "error", Error: fmt.Sprintf("pending request %q not found (expired or already served)", req.PendingID)})
		return
	}

	groups, skipped := convertChromeCookies(req.Cookies, entry.req.Domain)
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if total == 0 {
		outcome := pushOutcome{err: fmt.Errorf("no cookies for domain %q were pushed", entry.req.Domain)}
		select {
		case entry.done <- outcome:
		default:
		}
		writeJSON(w, http.StatusBadRequest, pushResponse{Status: "error", Error: outcome.err.Error()})
		return
	}

	imported, err := a.importer.Import(r.Context(), entry.req.InstanceID, groups)
	if err != nil {
		outcome := pushOutcome{err: fmt.Errorf("browsermanager.import_cookies failed: %w", err)}
		select {
		case entry.done <- outcome:
		default:
		}
		writeJSON(w, http.StatusBadGateway, pushResponse{Status: "error", Error: outcome.err.Error()})
		return
	}
	outcome := pushOutcome{imported: imported, skipped: skipped, domains: domainsOf(groups)}
	select {
	case entry.done <- outcome:
	default:
	}
	writeJSON(w, http.StatusOK, pushResponse{Status: "ok", Imported: imported, Skipped: skipped})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
