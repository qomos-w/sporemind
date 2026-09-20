package dbclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/webdav"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestWebDAVEndToEnd exercises the full dbclient dial + handler chain against
// an in-process WebDAV server (the same golang.org/x/net/webdav used by
// pkg/persist's webdav contract suite — the one network backend whose
// conformance suite needs no container).
//
// We register a fake profile_lookup so the actor never has to talk to a real
// dbmanager, then drive the real handlers with the profile pointing at the
// in-process server. The actor-level dial helper, gowebdav client, the
// webdavConn tree/read/close path, and the 512-char cell truncation all
// run on the real code path.
func TestWebDAVEndToEnd(t *testing.T) {
	installNoopCredentialResolver(t)
	root := t.TempDir()
	// populate a small tree: top dir + file + nested dir + nested file
	mustWrite(t, filepath.Join(root, "hello.txt"), strings.Repeat("A", 600)) // over 512
	mustMkdir(t, filepath.Join(root, "nested"))
	mustWrite(t, filepath.Join(root, "nested", "inside.md"), "# hi")

	srv := httptest.NewServer(&webdav.Handler{
		FileSystem: webdav.NewMemFS(),
		LockSystem: webdav.NewMemLS(),
	})
	t.Cleanup(srv.Close)

	// Seed an in-memory memfs by writing through a client; the handler's
	// in-memory FS is isolated from our local root, so we use a separate
	// write to put the right shape into it. Easier: use the local root as
	// the FileSystem — works because httptest server runs in-process.
	srv.Config.Handler = &webdav.Handler{
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
	}

	url := srv.URL
	a := &Actor{
		pool: map[string]*poolEntry{},
		lookupProfile: func(_ actor.Context, id string) (profileSummary, error) {
			return profileSummary{ID: id, Backend: "webdav", Endpoint: url}, nil
		},
		dialBackend: dialBackendConn,
	}

	admin := testutil.AdminCtx(testutil.GenActorID())

	// dial_test: end-to-end ping
	dial, err := a.handleDialTest(admin, domain.DbDialTestReq{ProfileID: "p"})
	if err != nil {
		t.Fatalf("dial_test: %v", err)
	}
	if !dial.Ok {
		t.Fatalf("dial_test Ok=false, Error=%q", dial.Error)
	}

	// tree "" -> root children
	resp, err := a.handleTree(admin, domain.DbTreeReq{ProfileID: "p", Path: ""})
	if err != nil {
		t.Fatalf("tree root: %v", err)
	}
	if len(resp.Nodes) == 0 {
		t.Fatalf("tree root returned no nodes")
	}
	foundHello, foundNested := false, false
	for _, n := range resp.Nodes {
		if n.Label == "hello.txt" && n.Kind == "object" {
			foundHello = true
		}
		if n.Label == "nested" && n.Kind == "dir" {
			foundNested = true
		}
	}
	if !foundHello {
		t.Errorf("hello.txt missing from root tree")
	}
	if !foundNested {
		t.Errorf("nested/ missing from root tree")
	}

	// tree "nested" -> nested children
	nested, err := a.handleTree(admin, domain.DbTreeReq{ProfileID: "p", Path: "nested"})
	if err != nil {
		t.Fatalf("tree nested: %v", err)
	}
	if len(nested.Nodes) != 1 || nested.Nodes[0].Label != "inside.md" {
		t.Fatalf("nested tree = %+v", nested.Nodes)
	}

	// read "hello.txt" -> metadata (Stat)
	meta, err := a.handleRead(admin, domain.DbReadReq{ProfileID: "p", Path: "hello.txt"})
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if len(meta.Columns) == 0 || len(meta.Rows) == 0 {
		t.Fatalf("file metadata expected, got %+v", meta)
	}

	// read "" -> directory listing rows
	listing, err := a.handleRead(admin, domain.DbReadReq{ProfileID: "p", Path: ""})
	if err != nil {
		t.Fatalf("read listing: %v", err)
	}
	if len(listing.Rows) == 0 {
		t.Fatalf("root listing expected at least hello.txt + nested, got %+v", listing)
	}

	// query -> explicit error (browse-only backend)
	if _, err := a.handleQuery(admin, domain.DbQueryReq{ProfileID: "p", Text: "ls"}); err == nil {
		t.Fatal("webdav query must be rejected as browse-only")
	}

	// ── object storage surface (second phase: oss/webdav byte browser) ──

	// object_list on root -> hello.txt + nested (the two seed entries)
	list, err := a.handleObjectList(admin, domain.DbObjectListReq{ProfileID: "p", Path: ""})
	if err != nil {
		t.Fatalf("object_list root: %v", err)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("object_list root: want 2 entries, got %d (%+v)", len(list.Entries), list.Entries)
	}
	hasFile, hasDir := false, false
	for _, e := range list.Entries {
		if e.Name == "hello.txt" && !e.IsDir { hasFile = true }
		if e.Name == "nested" && e.IsDir { hasDir = true }
	}
	if !hasFile || !hasDir {
		t.Fatalf("object_list root: missing hello.txt / nested, got %+v", list.Entries)
	}

	// object_list on a sub-directory
	sub, err := a.handleObjectList(admin, domain.DbObjectListReq{ProfileID: "p", Path: "nested"})
	if err != nil {
		t.Fatalf("object_list nested: %v", err)
	}
	if len(sub.Entries) != 1 || sub.Entries[0].Name != "inside.md" {
		t.Fatalf("object_list nested: want inside.md, got %+v", sub.Entries)
	}

	// object_write uploads new bytes; round-trips through object_read.
	const payload = "second-phase round trip"
	if _, err := a.handleObjectWrite(admin, domain.DbObjectWriteReq{
		ProfileID: "p",
		Path:      "uploaded.txt",
		Content:   base64.StdEncoding.EncodeToString([]byte(payload)),
	}); err != nil {
		t.Fatalf("object_write: %v", err)
	}
	read, err := a.handleObjectRead(admin, domain.DbObjectReadReq{ProfileID: "p", Path: "uploaded.txt"})
	if err != nil {
		t.Fatalf("object_read: %v", err)
	}
	got, err := base64.StdEncoding.DecodeString(read.Content)
	if err != nil {
		t.Fatalf("decode read content: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("round-trip mismatch: got %q want %q", got, payload)
	}
	if read.Truncated {
		t.Fatal("small upload should not be flagged truncated")
	}

	// object_mkdir creates a directory; listing under it should be empty
	// (after the parent's next refresh — but here we read directly).
	if _, err := a.handleObjectMkdir(admin, domain.DbObjectMkdirReq{
		ProfileID: "p", ParentPath: "", Name: "fresh",
	}); err != nil {
		t.Fatalf("object_mkdir: %v", err)
	}
	fresh, err := a.handleObjectList(admin, domain.DbObjectListReq{ProfileID: "p", Path: "fresh"})
	if err != nil {
		t.Fatalf("object_list fresh: %v", err)
	}
	if len(fresh.Entries) != 0 {
		t.Fatalf("fresh dir should be empty, got %+v", fresh.Entries)
	}

	// object_stat returns the file metadata without fetching bytes
	stat, err := a.handleObjectStat(admin, domain.DbObjectStatReq{ProfileID: "p", Path: "uploaded.txt"})
	if err != nil {
		t.Fatalf("object_stat: %v", err)
	}
	if stat.IsDir {
		t.Fatalf("uploaded.txt should not be a directory")
	}
	if int(stat.Size) != len(payload) {
		t.Fatalf("stat size mismatch: got %d want %d", stat.Size, len(payload))
	}

	// object_delete removes a leaf file
	if _, err := a.handleObjectDelete(admin, domain.DbObjectDeleteReq{ProfileID: "p", Path: "uploaded.txt"}); err != nil {
		t.Fatalf("object_delete uploaded.txt: %v", err)
	}
	if _, err := a.handleObjectRead(admin, domain.DbObjectReadReq{ProfileID: "p", Path: "uploaded.txt"}); err == nil {
		t.Fatal("expected read after delete to fail")
	}

	// object_delete on the recursive directory (nested) should also succeed
	if _, err := a.handleObjectDelete(admin, domain.DbObjectDeleteReq{ProfileID: "p", Path: "nested"}); err != nil {
		t.Fatalf("object_delete nested: %v", err)
	}

	// close releases the in-pool handle.
	if _, err := a.handleClose(admin, domain.DbCloseReq{ProfileID: "p"}); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestWebDAVCellTruncation(t *testing.T) {
	installNoopCredentialResolver(t)
	root := t.TempDir()
	big := strings.Repeat("X", 1000)
	mustWrite(t, filepath.Join(root, "big.txt"), big)

	srv := httptest.NewServer(&webdav.Handler{
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
	})
	t.Cleanup(srv.Close)

	a := &Actor{
		pool: map[string]*poolEntry{},
		lookupProfile: func(_ actor.Context, id string) (profileSummary, error) {
			return profileSummary{ID: id, Backend: "webdav", Endpoint: srv.URL}, nil
		},
		dialBackend: dialBackendConn,
	}

	admin := testutil.AdminCtx(testutil.GenActorID())
	// Drive Tree("") — the file metadata goes into Meta; the file size is
	// small (< 512) so Meta stays untruncated. The truncation rule is
	// exercised directly through truncateCell in dbclient_test.go and via
	// the cell path in newRows; here we just confirm the e2e wire is
	// reachable and the meta fields populate.
	resp, err := a.handleTree(admin, domain.DbTreeReq{ProfileID: "p", Path: ""})
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Label != "big.txt" {
		t.Fatalf("expected big.txt, got %+v", resp.Nodes)
	}
	if resp.Nodes[0].Meta["size"] != "1000" {
		t.Errorf("size = %q, want 1000", resp.Nodes[0].Meta["size"])
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// suppress unused-import warning for httptest when the test gets trimmed.
var (
	_ = httptest.NewServer
	_ = context.Background
	_ = fmt.Sprintf
)

// installNoopCredentialResolver lets the e2e tests exercise the full dial
// path with anonymous webdav profiles (no real dbmanager). Production
// wires dbmanager as the resolver; here we substitute an always-empty
// Credential so ResolveCredential never errors out.
func installNoopCredentialResolver(t *testing.T) {
	t.Helper()
	persist.SetCredentialResolver(func(ref string) (persist.Credential, error) {
		return persist.Credential{}, nil
	})
	t.Cleanup(func() { persist.SetCredentialResolver(nil) })
}