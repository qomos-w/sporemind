package sdk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// File drop: OS files dragged onto the plugin panel iframe are intercepted by
// the bridge bootstrap snippet (injected into every HTML document the SDK or
// the host gateway serves), expanded into a flat entry list, announced to the
// plugin frontend as a DOM event, and — when the plugin backend has registered
// a listener — uploaded to this process over the same gateway-authenticated
// HTTP listener that serves /invoke and /events. Metadata travels as JSON,
// file bytes as raw POST bodies (the wire rule: no base64, no binary codec in
// JSON envelopes).

// FileDropEntry is one entry of a dropped file tree. OS drops fill Name/
// RelPath/IsDir/Size/Data; host-browser drops (dragged from the host's
// FileBrowser or SSH FTP panel) fill Name/IsDir/Path/ProjectID/SessionID and
// carry no bytes. RelPath is the '/'-separated path relative to the drop
// origin; directory entries precede their children. Data holds the file
// bytes for non-directory entries and is nil for directories and for host-
// browser references (the plugin reads those by path). Data is JSON-opaque
// on purpose: event payloads carry metadata only, never bytes.
type FileDropEntry struct {
	Name      string `json:"name"`
	RelPath   string `json:"relPath"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	Data      []byte `json:"-"`
	Path      string `json:"path,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Origin    string `json:"origin,omitempty"`
}

// FileDropListener receives one completed drop (every file entry's Data
// uploaded). It runs on the HTTP handler goroutine; a slow listener delays
// only the panel's fetch, never other traffic — spawn a goroutine for long
// processing.
type FileDropListener func(entries []FileDropEntry) error

// MaxFileDropBytes caps the total declared size of one drop batch. Enforced
// at begin time (before any byte is uploaded) so an oversized drop is refused
// without buffering it first.
const MaxFileDropBytes = 256 << 20

// FileDropEventKind is the event kind broadcast on the plugin's /events
// channel (WS-first, SSE fallback) after a drop was delivered to the backend
// listener. Payload is {"entries":[...]} metadata — no file bytes.
const FileDropEventKind = "file_drop"

// FileDropRoutePrefix is the conventional route family the bootstrap snippet
// uploads through (same listener, same gateway token / cookie auth as
// /invoke).
const FileDropRoutePrefix = "/__sdk/file-drop"

// fileDropTTL bounds how long an unfinished drop batch may linger: the
// snippet aborts silently on upload errors, so stale pendings are swept
// lazily instead of being awaited.
const fileDropTTL = 10 * time.Minute

var fileDropState = struct {
	sync.Mutex
	listener FileDropListener
	pending  map[string]*pendingFileDrop
}{pending: make(map[string]*pendingFileDrop)}

type pendingFileDrop struct {
	entries []FileDropEntry
	created time.Time
}

// RegisterFileDropListener registers (or replaces) the backend listener for
// file drops on the plugin panel. Registration is the opt-in: while no
// listener is registered, the snippet's begin call is refused and no file
// bytes are ever uploaded. Clear on unload together with the callable
// registry.
func RegisterFileDropListener(fn FileDropListener) {
	fileDropState.Lock()
	defer fileDropState.Unlock()
	fileDropState.listener = fn
}

func clearFileDropState() {
	fileDropState.Lock()
	defer fileDropState.Unlock()
	fileDropState.listener = nil
	fileDropState.pending = make(map[string]*pendingFileDrop)
}

// validDropID accepts the snippet-generated identifiers ([0-9a-zA-Z]{8,64})
// and nothing else, so a hostile frontend cannot park unbounded map keys.
func validDropID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return true
}

// sweepFileDropsLocked drops batches older than the TTL. Caller holds the lock.
func sweepFileDropsLocked() {
	now := time.Now()
	for id, p := range fileDropState.pending {
		if now.Sub(p.created) > fileDropTTL {
			delete(fileDropState.pending, id)
		}
	}
}

// fileDropBeginBody is the JSON body of the begin request: the flat entry
// metadata exactly as the frontend expanded it.
type fileDropBeginBody struct {
	Entries []FileDropEntry `json:"entries"`
}

// fileDropBeginResp is the begin response. accepted=false always carries a
// machine-readable reason ("no-listener" | "too-large").
type fileDropBeginResp struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// handleFileDropBegin opens one drop batch. It refuses — before any byte is
// buffered — when no backend listener is registered or the declared total
// size exceeds MaxFileDropBytes.
func handleFileDropBegin(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	dropID := r.PathValue("dropID")
	if !validDropID(dropID) {
		writeHTTPError(w, http.StatusBadRequest, "invalid drop id")
		return
	}
	var body fileDropBeginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	var total int64
	for i := range body.Entries {
		e := &body.Entries[i]
		if e.Size < 0 {
			writeHTTPError(w, http.StatusBadRequest, "negative entry size")
			return
		}
		total += e.Size
		if e.Origin == "" {
			e.Origin = "os"
		}
	}

	fileDropState.Lock()
	sweepFileDropsLocked()
	if fileDropState.listener == nil {
		fileDropState.Unlock()
		writeJSON(w, http.StatusOK, fileDropBeginResp{Accepted: false, Reason: "no-listener"})
		return
	}
	if total > MaxFileDropBytes {
		fileDropState.Unlock()
		writeJSON(w, http.StatusOK, fileDropBeginResp{Accepted: false, Reason: "too-large"})
		return
	}
	fileDropState.pending[dropID] = &pendingFileDrop{entries: body.Entries, created: time.Now()}
	fileDropState.Unlock()
	writeJSON(w, http.StatusOK, fileDropBeginResp{Accepted: true})
}

// handleFileDropUpload fills one file entry's bytes. The body is the raw file
// content; the uploaded length must match the declared size exactly, so a
// tampered metadata block cannot blow past the begin-time cap.
func handleFileDropUpload(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	dropID := r.PathValue("dropID")
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeHTTPError(w, http.StatusBadRequest, "invalid entry index")
		return
	}
	fileDropState.Lock()
	p, ok := fileDropState.pending[dropID]
	fileDropState.Unlock()
	if !ok {
		writeHTTPError(w, http.StatusNotFound, "unknown drop")
		return
	}
	if index >= len(p.entries) {
		writeHTTPError(w, http.StatusBadRequest, "entry index out of range")
		return
	}
	e := &p.entries[index]
	if e.IsDir {
		writeHTTPError(w, http.StatusBadRequest, "directory entry has no data")
		return
	}
	if e.Data != nil {
		writeHTTPError(w, http.StatusConflict, "entry already uploaded")
		return
	}
	// Read at most Size+1 bytes: one extra byte detects a length mismatch.
	data, err := io.ReadAll(io.LimitReader(r.Body, e.Size+1))
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if int64(len(data)) != e.Size {
		writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: body is %d bytes, %d declared", index, len(data), e.Size))
		return
	}
	e.Data = data
	w.WriteHeader(http.StatusNoContent)
}

// handleFileDropEnd completes one drop batch: the registered listener runs
// with every entry (bytes filled), the batch is removed, and the metadata is
// broadcast on the /events channel so subscribed frontends observe the
// backend-side delivery. A listener error is returned as 500 — the frontend
// surfaces it in the panel console; the batch is still consumed.
func handleFileDropEnd(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	dropID := r.PathValue("dropID")
	fileDropState.Lock()
	p, ok := fileDropState.pending[dropID]
	if ok {
		delete(fileDropState.pending, dropID)
	}
	listener := fileDropState.listener
	fileDropState.Unlock()
	if !ok {
		writeHTTPError(w, http.StatusNotFound, "unknown drop")
		return
	}
	for i, e := range p.entries {
		if !e.IsDir && e.Data == nil {
			writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("entry %d (%s) has no uploaded data", i, e.RelPath))
			return
		}
	}
	if listener == nil {
		writeHTTPError(w, http.StatusConflict, "no file drop listener registered")
		return
	}
	entries := p.entries
	if err := runFileDropListener(listener, entries); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	meta, _ := json.Marshal(fileDropBeginBody{Entries: entries})
	broadcastSSE(FileDropEventKind, meta)
	w.WriteHeader(http.StatusNoContent)
}

// runFileDropListener invokes the plugin listener with panic capture — a
// panicking listener must never take down the plugin process (mirrors
// dispatchRequest's recover contract).
func runFileDropListener(fn FileDropListener, entries []FileDropEntry) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = panicError(rec)
		}
	}()
	return fn(entries)
}

// maxHostFileDropEntries bounds one host-browser drop batch (metadata only,
// so the cap is about map-ish abuse, not memory).
const maxHostFileDropEntries = 64

// handleFileDropHost delivers a host-browser-origin drop: the drag came from
// the host's file panels (FileBrowser / SSH FTP) and carries path
// references, not bytes — the snippet parsed the "x-sporemind-dnd:" text
// payload and POSTs the metadata here. The same listener opt-in applies; a
// refused drop answers accepted=false (mirroring begin) rather than an
// error, because the frontend treats refusal as "panel not listening".
func handleFileDropHost(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	var body fileDropBeginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	if len(body.Entries) == 0 {
		writeHTTPError(w, http.StatusBadRequest, "no entries")
		return
	}
	if len(body.Entries) > maxHostFileDropEntries {
		writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("too many entries (%d > %d)", len(body.Entries), maxHostFileDropEntries))
		return
	}
	for i := range body.Entries {
		e := &body.Entries[i]
		if e.Path == "" {
			writeHTTPError(w, http.StatusBadRequest, fmt.Sprintf("entry %d has no path", i))
			return
		}
		if len(e.Path) > 4096 || len(e.ProjectID) > 128 || len(e.SessionID) > 128 {
			writeHTTPError(w, http.StatusBadRequest, "entry field too long")
			return
		}
		e.Origin = "host-browser"
	}

	fileDropState.Lock()
	listener := fileDropState.listener
	fileDropState.Unlock()
	if listener == nil {
		writeJSON(w, http.StatusOK, fileDropBeginResp{Accepted: false, Reason: "no-listener"})
		return
	}
	if err := runFileDropListener(listener, body.Entries); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	meta, _ := json.Marshal(fileDropBeginBody{Entries: body.Entries})
	broadcastSSE(FileDropEventKind, meta)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
