package sdk

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func beginDrop(t *testing.T, base, dropID, body string) (int, []byte) {
	t.Helper()
	return postJSON(t, base+FileDropRoutePrefix+"/"+dropID+"/begin", body, nil)
}

func uploadRaw(t *testing.T, url string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func endDrop(t *testing.T, base, dropID string) (int, []byte) {
	t.Helper()
	return postJSON(t, base+FileDropRoutePrefix+"/"+dropID+"/end", `{}`, nil)
}

// TestFileDropNoListenerRefused pins the opt-in contract: with no backend
// listener registered, begin is refused before any byte is buffered.
func TestFileDropNoListenerRefused(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[{"name":"a.txt","relPath":"a.txt","isDir":false,"size":3}]}`)
	if code != http.StatusOK {
		t.Fatalf("begin status = %d, want 200; body=%s", code, body)
	}
	var resp fileDropBeginResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode begin resp: %v (%s)", err, body)
	}
	if resp.Accepted || resp.Reason != "no-listener" {
		t.Fatalf("begin resp = %+v, want accepted=false reason=no-listener", resp)
	}

	// No pending batch was created: end reports unknown drop.
	if code, body := endDrop(t, base, "0123456789abcdef"); code != http.StatusNotFound {
		t.Fatalf("end after refused begin status = %d, want 404; body=%s", code, body)
	}
}

func TestFileDropFullDelivery(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	var got []FileDropEntry
	RegisterFileDropListener(func(entries []FileDropEntry) error {
		got = entries
		return nil
	})

	sse, cancel := connectSSE(t, base)
	defer cancel()
	waitForConns(t, srv, 1)

	code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[`+
		`{"name":"proj","relPath":"proj","isDir":true,"size":0},`+
		`{"name":"a.txt","relPath":"proj/a.txt","isDir":false,"size":5},`+
		`{"name":"b.bin","relPath":"b.bin","isDir":false,"size":0}]}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"accepted":true`) {
		t.Fatalf("begin = %d %s, want 200 accepted", code, body)
	}

	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/1", []byte("hello")); code != http.StatusNoContent {
		t.Fatalf("upload file status = %d, want 204", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/2", nil); code != http.StatusNoContent {
		t.Fatalf("upload empty file status = %d, want 204", code)
	}
	if code, body := endDrop(t, base, "0123456789abcdef"); code != http.StatusNoContent {
		t.Fatalf("end status = %d, want 204; body=%s", code, body)
	}

	if len(got) != 3 {
		t.Fatalf("listener entries = %d, want 3", len(got))
	}
	if !got[0].IsDir || got[0].RelPath != "proj" || got[0].Data != nil {
		t.Errorf("dir entry = %+v, want proj dir with nil Data", got[0])
	}
	if got[1].RelPath != "proj/a.txt" || string(got[1].Data) != "hello" {
		t.Errorf("file entry = %+v, want proj/a.txt with hello", got[1])
	}
	if got[2].RelPath != "b.bin" || len(got[2].Data) != 0 {
		t.Errorf("empty file entry = %+v, want b.bin with empty Data", got[2])
	}

	// The /events mirror carries metadata only — no bytes on the JSON wire.
	select {
	case ev := <-sse:
		if ev.kind != FileDropEventKind {
			t.Errorf("mirror kind = %q, want %s", ev.kind, FileDropEventKind)
		}
		if !strings.Contains(ev.data, `"relPath":"proj/a.txt"`) || strings.Contains(ev.data, "hello") {
			t.Errorf("mirror data = %q, want metadata with relPath and no bytes", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file_drop SSE mirror")
	}

	// Batch consumed: a second end is unknown.
	if code, _ := endDrop(t, base, "0123456789abcdef"); code != http.StatusNotFound {
		t.Fatalf("second end status = %d, want 404", code)
	}
}

// TestFileDropHostDelivery covers host-browser-origin drops: entries carry
// path references with no bytes; the listener runs once and the /events
// mirror fires; refusal without a listener mirrors begin's accepted=false.
func TestFileDropHostDelivery(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	var got []FileDropEntry
	RegisterFileDropListener(func(entries []FileDropEntry) error {
		got = entries
		return nil
	})

	sse, cancel := connectSSE(t, base)
	defer cancel()
	waitForConns(t, srv, 1)

	code, body := postJSON(t, base+FileDropRoutePrefix+"/host", `{"entries":[`+
		`{"name":"hero.aseprite","isDir":false,"path":"assets/hero.aseprite","projectId":"proj-1","origin":"local"}]}`, nil)
	if code != http.StatusNoContent {
		t.Fatalf("host drop status = %d, want 204; body=%s", code, body)
	}
	if len(got) != 1 {
		t.Fatalf("listener entries = %d, want 1", len(got))
	}
	e := got[0]
	if e.Origin != "host-browser" || e.Path != "assets/hero.aseprite" || e.ProjectID != "proj-1" || e.IsDir || e.Data != nil {
		t.Errorf("entry = %+v, want host-browser reference with path/projectId and nil Data", e)
	}

	select {
	case ev := <-sse:
		if ev.kind != FileDropEventKind || !strings.Contains(ev.data, `"path":"assets/hero.aseprite"`) {
			t.Errorf("mirror = %s %q, want %s with path metadata", ev.kind, ev.data, FileDropEventKind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file_drop SSE mirror")
	}

	// No-listener refusal: accepted=false, not an error status.
	clearFileDropState()
	code, body = postJSON(t, base+FileDropRoutePrefix+"/host", `{"entries":[{"name":"a","path":"a"}]}`, nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"reason":"no-listener"`) {
		t.Fatalf("no-listener host drop = %d %s, want 200 no-listener", code, body)
	}
}

// TestFileDropHostValidation pins the metadata bounds: empty batches,
// pathless entries, and oversize batches are rejected before the listener.
func TestFileDropHostValidation(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func(entries []FileDropEntry) error { return nil })

	if code, _ := postJSON(t, base+FileDropRoutePrefix+"/host", `{"entries":[]}`, nil); code != http.StatusBadRequest {
		t.Errorf("empty batch status = %d, want 400", code)
	}
	if code, _ := postJSON(t, base+FileDropRoutePrefix+"/host", `{"entries":[{"name":"a","path":""}]}`, nil); code != http.StatusBadRequest {
		t.Errorf("pathless entry status = %d, want 400", code)
	}
	many := `{"entries":[` + strings.Repeat(`{"name":"a","path":"a"},`, maxHostFileDropEntries) + `{"name":"z","path":"z"}]}`
	if code, _ := postJSON(t, base+FileDropRoutePrefix+"/host", many, nil); code != http.StatusBadRequest {
		t.Errorf("oversize batch status = %d, want 400", code)
	}
}

// TestFileDropHostAuth pins the gateway gate: the host route rejects
// unauthenticated requests exactly like begin/upload/end.
func TestFileDropHostAuth(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)
	SetSessionSecret("test-secret")

	if code, _ := postJSON(t, base+FileDropRoutePrefix+"/host", `{"entries":[{"name":"a","path":"a"}]}`, nil); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated host drop status = %d, want 401", code)
	}
}

func TestFileDropEndRejectsMissingData(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	if code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[`+
		`{"name":"a","relPath":"a","isDir":false,"size":1},`+
		`{"name":"b","relPath":"b","isDir":false,"size":1}]}`); code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, body)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/0", []byte("x")); code != http.StatusNoContent {
		t.Fatalf("upload status = %d", code)
	}
	// b was never uploaded: end must refuse instead of silently delivering a
	// half drop to the listener.
	code, body := endDrop(t, base, "0123456789abcdef")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "entry 1 (b)") {
		t.Fatalf("end = %d %s, want 400 naming missing entry b", code, body)
	}
}

func TestFileDropUploadMismatches(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	if code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[`+
		`{"name":"d","relPath":"d","isDir":true,"size":0},`+
		`{"name":"a","relPath":"a","isDir":false,"size":3}]}`); code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, body)
	}

	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/1", []byte("four")); code != http.StatusBadRequest {
		t.Fatalf("oversized upload status = %d, want 400 (declared size mismatch)", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/0", []byte("x")); code != http.StatusBadRequest {
		t.Fatalf("dir upload status = %d, want 400", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/5", []byte("x")); code != http.StatusBadRequest {
		t.Fatalf("out-of-range upload status = %d, want 400", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/ffffffffffffffff/0", []byte("x")); code != http.StatusNotFound {
		t.Fatalf("unknown drop upload status = %d, want 404", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/1", []byte("abc")); code != http.StatusNoContent {
		t.Fatalf("good upload status = %d, want 204", code)
	}
	if code := uploadRaw(t, base+FileDropRoutePrefix+"/0123456789abcdef/1", []byte("abc")); code != http.StatusConflict {
		t.Fatalf("re-upload status = %d, want 409", code)
	}
}

func TestFileDropTooLargeRefused(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[{"name":"big","relPath":"big","isDir":false,"size":`+
		jsonInt64(MaxFileDropBytes+1)+`}]}`)
	if code != http.StatusOK {
		t.Fatalf("begin status = %d; body=%s", code, body)
	}
	var resp fileDropBeginResp
	_ = json.Unmarshal(body, &resp)
	if resp.Accepted || resp.Reason != "too-large" {
		t.Fatalf("begin resp = %+v, want accepted=false reason=too-large", resp)
	}
}

func jsonInt64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestFileDropListenerErrorAndPanic pins the error surfaces: a listener error
// is a 500 to the panel fetch; a panicking listener is recovered and never
// kills the process. Both consume the batch.
func TestFileDropListenerErrorAndPanic(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func([]FileDropEntry) error { return errTestDrop })
	if code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[]}`); code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, body)
	}
	if code, body := endDrop(t, base, "0123456789abcdef"); code != http.StatusInternalServerError || !strings.Contains(string(body), "boom") {
		t.Fatalf("end = %d %s, want 500 boom", code, body)
	}

	RegisterFileDropListener(func([]FileDropEntry) error { panic("listener exploded") })
	if code, body := beginDrop(t, base, "1111111111111111", `{"entries":[]}`); code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, body)
	}
	if code, _ := endDrop(t, base, "1111111111111111"); code != http.StatusInternalServerError {
		t.Fatalf("panic end status = %d, want 500 (recovered)", code)
	}
}

var errTestDrop = &testError{"boom: drop rejected"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestFileDropAuth(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)
	SetSessionSecret("test-secret")

	body := `{"entries":[{"name":"a","relPath":"a","isDir":false,"size":1}]}`
	drop := base + FileDropRoutePrefix + "/0123456789abcdef/begin"

	// No credentials: refused at the door.
	if code, _ := postJSON(t, drop, body, nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated begin status = %d, want 401", code)
	}

	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	// Valid session cookie authorizes (the panel iframe path).
	cookie := &http.Cookie{Name: SessionCookieName, Value: MintSessionToken(sessionSecret(), "s1")}
	if code, respBody := postJSON(t, drop, body, cookie); code != http.StatusOK || !strings.Contains(string(respBody), `"accepted":true`) {
		t.Fatalf("cookie begin = %d %s, want 200 accepted", code, respBody)
	}

	// Gateway token header authorizes (the reverse-proxy path), including
	// the upload and end legs.
	if code := uploadAuthed(t, base+FileDropRoutePrefix+"/0123456789abcdef/0", []byte("x")); code != http.StatusNoContent {
		t.Fatalf("gateway-token upload status = %d, want 204", code)
	}
	req, err := http.NewRequest(http.MethodPost, base+FileDropRoutePrefix+"/0123456789abcdef/end", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(GatewayTokenHeader, MintSessionToken(sessionSecret(), "gateway-proxy"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("gateway-token end status = %d, want 204", resp.StatusCode)
	}
}

func uploadAuthed(t *testing.T, url string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(GatewayTokenHeader, MintSessionToken(sessionSecret(), "gateway-proxy"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestFileDropIDValidation(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)
	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	cases := []struct {
		id   string
		want int
	}{
		{"short", http.StatusBadRequest},
		{"has spaces and!", http.StatusBadRequest},
		{"0123456789abcdef", http.StatusOK},
		{"ABCDEFGHIJKLMNOP", http.StatusOK},
	}
	for _, c := range cases {
		if code, _ := beginDrop(t, base, c.id, `{"entries":[]}`); code != c.want {
			t.Errorf("dropID %q begin status = %d, want %d", c.id, code, c.want)
		}
	}
}

func TestFileDropTTLSweep(t *testing.T) {
	srv := serveTestHTTP(t)
	base := baseURL(srv)
	t.Cleanup(clearFileDropState)

	RegisterFileDropListener(func([]FileDropEntry) error { return nil })

	if code, body := beginDrop(t, base, "0123456789abcdef", `{"entries":[]}`); code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, body)
	}
	// Age the batch past the TTL, then trigger a sweep via another begin.
	fileDropState.Lock()
	fileDropState.pending["0123456789abcdef"].created = time.Now().Add(-fileDropTTL - time.Second)
	fileDropState.Unlock()

	if code, body := beginDrop(t, base, "1111111111111111", `{"entries":[]}`); code != http.StatusOK {
		t.Fatalf("second begin = %d %s", code, body)
	}
	fileDropState.Lock()
	_, stale := fileDropState.pending["0123456789abcdef"]
	_, live := fileDropState.pending["1111111111111111"]
	fileDropState.Unlock()
	if stale {
		t.Error("stale drop batch survived the TTL sweep")
	}
	if !live {
		t.Error("fresh drop batch was swept")
	}
}
