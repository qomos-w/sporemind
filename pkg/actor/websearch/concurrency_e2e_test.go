package websearch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/websearch"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// TestWebSearchPureHandlersNotSerializedByDownload proves the owner-lane fix:
// while a websearch.download call is in flight (its HTTP response held open by
// a slow server), websearch.fetch and websearch.search invoked through the
// real runtime must complete without queueing behind it.
//
// Before the PureContext conversion all three handlers ran serialized on the
// owner lane: the fetch/search calls would wait for the download to finish —
// here forever, because the download server stalls until the test releases
// it. So this test deadlocks (times out) against the old stateful handlers
// and only passes with stateless dispatch.
func TestWebSearchPureHandlersNotSerializedByDownload(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// downloadStarted fires once the download handler's GET reaches the
	// server; release unblocks the stalled response body.
	downloadStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDownload := func() { releaseOnce.Do(func() { close(release) }) }

	dlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case downloadStarted <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, "head-of-payload")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hold the connection open until the test has proven that the other
		// callables complete while this download is still in flight.
		<-release
		fmt.Fprint(w, "-tail-of-payload")
	}))

	fetchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html lang="en"><head><title>Fast</title></head><body><p>quick fetch</p></body></html>`)
	}))

	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "t", "created": 1, "search_result": []any{}})
	}))

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		NoGateway: true,
		Children: []runtime.ChildSpec{
			{Name: "websearch", Factory: func() actor.Actor { return &websearch.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Teardown ordering (t.Cleanup runs LIFO): release the stalled download
	// FIRST so the owner/pure loops drain and the HTTP servers can close.
	t.Cleanup(func() {
		releaseDownload()
		_ = h.Wait()
		dlServer.Close()
		fetchServer.Close()
		searchServer.Close()
	})

	// Give the ordered start a moment to run OnStart for the websearch cell.
	time.Sleep(500 * time.Millisecond)

	app := h.App()
	wsRef, ok := app.LookupService("websearch")
	if !ok {
		t.Fatal("LookupService(\"websearch\") missed — websearch service not exposed")
	}

	// Seed a keyed account so websearch.search needs no aimanager fallback.
	createRaw, err := recvWithTimeout(wsRef, ctx, "websearch.account_create", gen.WebSearchAccountCreateReq{
		Name: "test", Provider: "zhipu", APIKey: "test-key", Endpoint: searchServer.URL,
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("account_create: %v", err)
	}
	var created gen.WebSearchAccountCreateResp
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode account_create resp %q: %v", string(createRaw), err)
	}
	if created.Account.ID == "" {
		t.Fatalf("account_create returned no account: %s", string(createRaw))
	}

	savePath := filepath.Join(t.TempDir(), "slow-download.bin")

	// Start the download. Its handler must reach the server and then stall
	// holding the connection open.
	dlDone := make(chan []byte, 1)
	go func() {
		call := wsRef.Invoke(ctx, "websearch.download", gen.WebDownloadReq{URL: dlServer.URL, SavePath: savePath})
		if call == nil {
			dlDone <- nil
			return
		}
		defer call.Close()
		raw, err := call.RecvRaw()
		if err != nil {
			dlDone <- []byte("ERROR: " + err.Error())
			return
		}
		dlDone <- raw
	}()

	select {
	case <-downloadStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("download never reached the server")
	}

	// While the download is in flight, fetch and search must both answer.
	fetchBudget := 10 * time.Second
	fetchRaw, err := recvWithTimeout(wsRef, ctx, "websearch.fetch", gen.WebFetchReq{URL: fetchServer.URL}, fetchBudget)
	if err != nil {
		t.Fatalf("websearch.fetch queued behind the in-flight download (or failed): %v", err)
	}
	if !strings.Contains(string(fetchRaw), "quick fetch") {
		t.Errorf("fetch resp missing expected text: %s", string(fetchRaw))
	}

	searchRaw, err := recvWithTimeout(wsRef, ctx, "websearch.search", gen.WebSearchReq{Query: "concurrency"}, fetchBudget)
	if err != nil {
		t.Fatalf("websearch.search queued behind the in-flight download (or failed): %v", err)
	}
	if !strings.Contains(string(searchRaw), `"Provider":"zhipu"`) {
		t.Errorf("search resp missing provider: %s", string(searchRaw))
	}

	// Now release the stalled download and verify it completes normally.
	releaseDownload()
	raw, ok := <-dlDone
	if !ok || len(raw) == 0 {
		t.Fatal("download call returned no result")
	}
	if strings.HasPrefix(string(raw), "ERROR") {
		t.Fatalf("download failed: %s", string(raw))
	}
	var dlResp gen.WebDownloadResp
	if err := json.Unmarshal(raw, &dlResp); err != nil {
		t.Fatalf("decode download resp %q: %v", string(raw), err)
	}
	if dlResp.SavedPath != savePath || dlResp.BytesDownloaded == 0 {
		t.Errorf("download resp = %+v", dlResp)
	}
}

// recvWithTimeout invokes callID on ref and waits for the raw response with a
// timeout, failing the test when the call does not answer in time.
func recvWithTimeout(r ref.Ref, ctx context.Context, callID string, payload any, timeout time.Duration) ([]byte, error) {
	call := r.Invoke(ctx, callID, payload)
	if call == nil {
		return nil, fmt.Errorf("%s invoke returned nil", callID)
	}
	defer call.Close()
	type result struct {
		raw []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		raw, err := call.RecvRaw()
		ch <- result{raw: raw, err: err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.raw, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s did not answer within %s", callID, timeout)
	}
}
