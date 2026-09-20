package lspserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTSEngineLive is an opt-in integration test against a real
// typescript-language-server install. It is skipped unless TS_LIVE=1 with
// TS_LIVE_CLI and TS_LIVE_TYPESCRIPT pointing at the install (see the
// workflow research card for the managed-prefix install command):
//
//	npm install --prefix <dir> typescript-language-server@6 typescript@6
//	TS_LIVE=1 TS_LIVE_CLI=<dir>/node_modules/typescript-language-server/lib/cli.mjs \
//	TS_LIVE_TYPESCRIPT=<dir>/node_modules/typescript go test -run TestTSEngineLive -v ./pkg/actor/lspserver/
//
// On Windows, tsserver builds URIs with a lowercase drive letter and %3A
// encoding (file:///c%3A/...), unlike gopls' file:///C:/... — comparisons
// below normalize through the file name, mirroring the uriToPath convention in
// the frontend.
func TestTSEngineLive(t *testing.T) {
	cli := os.Getenv("TS_LIVE_CLI")
	tsDir := os.Getenv("TS_LIVE_TYPESCRIPT")
	if os.Getenv("TS_LIVE") != "1" || cli == "" || tsDir == "" {
		t.Skip("set TS_LIVE=1, TS_LIVE_CLI, TS_LIVE_TYPESCRIPT to run")
	}
	if _, err := os.Stat(cli); err != nil {
		t.Fatalf("TS_LIVE_CLI %s: %v", cli, err)
	}
	if _, err := os.Stat(filepath.Join(tsDir, "package.json")); err != nil {
		t.Fatalf("TS_LIVE_TYPESCRIPT %s: %v", tsDir, err)
	}

	// Scratch project with a tsconfig so tsserver builds a real project.
	dir, err := os.MkdirTemp("", "tsengine-live-*")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		for i := 0; i < 20; i++ { // Windows releases handles shortly after process exit
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = os.RemoveAll(dir)
	}
	t.Cleanup(cleanup)

	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true},"include":["*.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := "file:///" + filepath.ToSlash(filepath.Join(dir, "smoke.ts"))
	content := "export const value: number = 41;\nexport const next = value + 1;\n"
	if err := os.WriteFile(filepath.Join(dir, "smoke.ts"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	rootURI := "file:///" + filepath.ToSlash(dir)
	log := &testLogger{}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	e, err := NewTSEngine(ctx, log, nil, TSEngineConfig{
		CLIPath:        cli,
		TypeScriptPath: tsDir,
		LogLevel:       4,
	})
	if err != nil {
		t.Fatalf("NewTSEngine (spawn): %v", err)
	}
	t.Cleanup(func() { e.Close() })

	initRes, err := e.Initialize(ctx, rootURI)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !strings.Contains(string(initRes), "capabilities") {
		t.Fatalf("initialize result missing capabilities: %.200s", initRes)
	}
	if !waitLog(log, "tsserver version", 15*time.Second) {
		t.Log("note: tsserver version notification not seen")
	}

	if err := e.DidOpen(ctx, uri, "typescript", content, 1); err != nil {
		t.Errorf("DidOpen: %v", err)
	}

	// Let tsserver load the project before querying.
	time.Sleep(2 * time.Second)

	// Definition of `value` from `next = value + 1` (line 2, char ~15).
	if def, err := e.Definition(ctx, uri, 1, 15); err != nil {
		t.Errorf("Definition: %v", err)
	} else if !strings.Contains(string(def), "smoke.ts") || !strings.Contains(string(def), "targetUri") {
		t.Errorf("Definition did not resolve into the file: %.300s", def)
	}

	if hover, err := e.Hover(ctx, uri, 1, 15); err != nil {
		t.Errorf("Hover: %v", err)
	} else if !strings.Contains(string(hover), "\"contents\"") {
		t.Errorf("Hover result unexpected: %.300s", hover)
	}

	if completion, err := e.Completion(ctx, uri, 1, 5); err != nil {
		t.Errorf("Completion: %v", err)
	} else if !strings.Contains(string(completion), "items") {
		t.Errorf("Completion result unexpected: %.300s", completion)
	}

	if symbols, err := e.DocumentSymbol(ctx, uri); err != nil {
		t.Errorf("DocumentSymbol: %v", err)
	} else if !strings.Contains(string(symbols), "value") {
		t.Errorf("DocumentSymbol result unexpected: %.300s", symbols)
	}

	// prepareRename may return {range, placeholder} or a bare Range.
	if prepare, err := e.PrepareRename(ctx, uri, 1, 15); err != nil {
		t.Errorf("PrepareRename: %v", err)
	} else if !strings.Contains(string(prepare), "start") {
		t.Errorf("PrepareRename result unexpected: %.300s", prepare)
	}

	if rename, err := e.Rename(ctx, uri, 1, 15, "renamedValue"); err != nil {
		t.Errorf("Rename: %v", err)
	} else if !strings.Contains(string(rename), "changes") {
		t.Errorf("Rename result unexpected: %.300s", rename)
	}

	if refs, err := e.References(ctx, uri, 1, 15, true); err != nil {
		t.Errorf("References: %v", err)
	} else if !strings.Contains(string(refs), "smoke.ts") {
		t.Errorf("References result unexpected: %.300s", refs)
	}

	if actions, err := e.CodeAction(ctx, uri, 0, 0, 1, 5); err != nil {
		t.Errorf("CodeAction: %v", err)
	} else if !strings.Contains(string(actions), "\"title\"") {
		// code actions for a trivial file may be an empty array; require
		// only that an LSP payload (title-bearing or []) came back.
		if !strings.EqualFold(strings.TrimSpace(string(actions)), "[]") {
			t.Errorf("CodeAction result unexpected: %.300s", actions)
		}
	}

	if err := e.DidChange(ctx, uri, 2, []byte(`[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":26}},"text":"export const next = value + 2;"}]`)); err != nil {
		t.Errorf("DidChange: %v", err)
	}

	if !waitLog(log, "publishDiagnostics", 15*time.Second) {
		t.Log("note: no publishDiagnostics observed (tsserver diagnostics are debounced; routing is covered by TestTSEngineLifecycle)")
	}

	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
