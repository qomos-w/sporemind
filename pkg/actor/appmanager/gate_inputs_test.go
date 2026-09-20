package appmanager

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// collectGoSourceFiles must read subdirectory .go files: the recursive
// project.list contract returns the root-relative path (including
// the directory prefix), and the read path must preserve that prefix. If either
// side regresses to base names, subdir handlers silently vanish from the
// stub-filling gate.
func TestCollectGoSourceFilesSubdirectories(t *testing.T) {
	files := map[string]string{
		"handlers.go":           "package main",
		"internal/extra.go":     "package internal",
		"internal/deep/deep.go": "package deep",
		"notes.txt":             "not go",
	}
	var readPaths []string

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "project.list":
				req := payload.(gen.FileSystemListReq)
				if req.Depth != -1 {
					return nil, fmt.Errorf("expected Depth -1, got %d", req.Depth)
				}
				return listText(
					gen.FileEntry{Name: "handlers.go"},
					gen.FileEntry{Name: "internal", IsDir: true},
					gen.FileEntry{Name: "internal/extra.go"},
					gen.FileEntry{Name: "internal/deep", IsDir: true},
					gen.FileEntry{Name: "internal/deep/deep.go"},
					gen.FileEntry{Name: "notes.txt"},
				), nil
			case "project.read_base64":
				req := payload.(gen.FileSystemReadBase64Req)
				rel := req.Path[len("/proj/"):]
				content, ok := files[rel]
				if !ok {
					return nil, fmt.Errorf("unexpected read path %q", req.Path)
				}
				readPaths = append(readPaths, req.Path)
				return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString([]byte(content))}, nil
			default:
				return nil, fmt.Errorf("unexpected call %s", callID)
			}
		}}
	}

	caller := newProjectCaller(ctx, testutil.NewFakeRef(testutil.GenActorID(), nil))
	out, err := caller.collectGoSourceFiles("/proj")
	if err != nil {
		t.Fatalf("collectGoSourceFiles: %v", err)
	}

	if len(out) != 3 {
		t.Fatalf("collected %d files, want 3 (subdir files included): %v", len(out), keysOf(out))
	}
	for _, want := range []string{"handlers.go", "internal/extra.go", "internal/deep/deep.go"} {
		if out[want] != files[want] {
			t.Errorf("out[%q] content mismatch", want)
		}
	}

	seen := map[string]bool{}
	for _, p := range readPaths {
		seen[p] = true
	}
	for _, want := range []string{"/proj/internal/extra.go", "/proj/internal/deep/deep.go"} {
		if !seen[want] {
			t.Errorf("expected read path %q, got %v", want, readPaths)
		}
	}
}

// TestCollectGoSourceFilesSkipsVendoredSDK pins the round-8 regression: the
// stub-filling gate collected vendor-sdk/*.go, and the vendored SDK's own
// source legitimately defines the ErrNotImplemented constant — every vendored
// app (i.e. every template scaffold) failed the stub gate and registration.
func TestCollectGoSourceFilesSkipsVendoredSDK(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "project.list":
				return listText(
					gen.FileEntry{Name: "handlers.go"},
					gen.FileEntry{Name: "vendor-sdk", IsDir: true},
					gen.FileEntry{Name: "vendor-sdk/manifest.go"},
					gen.FileEntry{Name: "vendor-sdk/bridge.go"},
				), nil
			case "project.read_base64":
				return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString([]byte("package main"))}, nil
			default:
				return nil, fmt.Errorf("unexpected call %s", callID)
			}
		}}
	}

	caller := newProjectCaller(ctx, testutil.NewFakeRef(testutil.GenActorID(), nil))
	out, err := caller.collectGoSourceFiles("/proj")
	if err != nil {
		t.Fatalf("collectGoSourceFiles: %v", err)
	}
	if _, ok := out["vendor-sdk/manifest.go"]; ok {
		t.Error("vendored SDK file must not be collected for the stub gate")
	}
	if _, ok := out["vendor-sdk/bridge.go"]; ok {
		t.Error("vendored SDK file must not be collected for the stub gate")
	}
	if out["handlers.go"] != "package main" {
		t.Errorf("handlers.go missing from collection: %v", keysOf(out))
	}
	if len(out) != 1 {
		t.Errorf("collected %v, want only handlers.go", keysOf(out))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// listText builds project.list plain-text output (dirs keep a trailing "/")
// mirroring the FileEntry fixtures used across these tests.
func listText(entries ...gen.FileEntry) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			lines = append(lines, e.Name+"/")
		} else {
			lines = append(lines, e.Name)
		}
	}
	return strings.Join(lines, "\n")
}

// TestResolveSingleAppDefHashMatchesGenerate pins the "hash mismatch" root
// cause: project.read normalizes CRLF AND force-appends a trailing newline,
// so gate-hashed content drifted from the raw bytes dev_generate hashed
// whenever the .appdef lacked a trailing newline (common with LLM-authored
// files). resolveSingleAppDef now reads raw bytes via project.read_base64 and
// hashes the CRLF-normalized form — identical to codegen's
// contentHash(NormalizeLineEndings(...)). Regression: TOTP round-6 blockers.
func TestResolveSingleAppDefHashMatchesGenerate(t *testing.T) {
	// No trailing newline, CRLF endings, and a BOM-free body — the hostile
	// combination for any text-protocol read path.
	raw := "app X {\n    id: \"app.x\"\r\n    name: \"X\"\r\n}" // ends with }, no \n

	files := map[string]string{"x.appdef": raw}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "project.list":
				return listText(gen.FileEntry{Name: "x.appdef"}), nil
			case "project.read_base64":
				req := payload.(gen.FileSystemReadBase64Req)
				rel := req.Path[len("/proj/"):]
				content, ok := files[rel]
				if !ok {
					return nil, fmt.Errorf("unexpected read path %q", req.Path)
				}
				return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString([]byte(content))}, nil
			default:
				return nil, fmt.Errorf("unexpected call %s", callID)
			}
		}}
	}

	caller := newProjectCaller(ctx, testutil.NewFakeRef(testutil.GenActorID(), nil))
	content, gateHash, err := caller.resolveSingleAppDef("/proj")
	if err != nil {
		t.Fatalf("resolveSingleAppDef: %v", err)
	}
	if content != raw {
		t.Fatalf("content drifted from raw bytes:\n got %q\nwant %q", content, raw)
	}

	want := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}(codegen.NormalizeLineEndings(raw))
	if gateHash != want {
		t.Fatalf("gate hash %q != normalized-content hash %q", gateHash, want)
	}

	// The LF form of the same .appdef must hash identically: CRLF is not
	// content drift. (dev_generate's hash for the LF bytes must match the
	// gate hash for the CRLF bytes — that equivalence is the entire fix.)
	lfRaw := strings.ReplaceAll(raw, "\r\n", "\n")
	files["x.appdef"] = lfRaw
	_, lfGateHash, err := caller.resolveSingleAppDef("/proj")
	if err != nil {
		t.Fatalf("resolveSingleAppDef (LF): %v", err)
	}
	if lfGateHash != gateHash {
		t.Fatalf("LF hash %q != CRLF hash %q — line endings leaked into drift detection", lfGateHash, gateHash)
	}
}
