package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appdef"
)

// TestGenerateClientTSExposeWatchHooks pins the HTTP client: expose filtering
// (no wrapper for expose:agent), watched-list hooks from the watch field,
// SSE on<Event> wrappers, and the HTTP-native binary helpers.
func TestGenerateClientTSExposeWatchHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http.appdef"), []byte(testHTTPAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileClientGenTS))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Frontend-exposed wrappers exist; agent-only callable gets none.
	for _, fn := range []string{"getItem", "listItems", "addItem", "panelJob", "genText", "ping"} {
		if !strings.Contains(content, "function "+fn+"(") {
			t.Errorf("client.gen.ts missing wrapper %s:\n%s", fn, content)
		}
	}
	if strings.Contains(content, "function agentTask(") {
		t.Errorf("client.gen.ts must not wrap expose:agent callable agent_task:\n%s", content)
	}

	// Watched-list hooks: get_item and add_item watch item_changed and are
	// frontend-exposed; list_items has no watch and gets no hook.
	for _, want := range []string{
		"export function useGetItemList(",
		"export function useAddItemList(",
		"export interface WatchedQuery<T> {",
		`const kinds = ["item_changed"];`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "useListItemsList") {
		t.Errorf("client.gen.ts must not generate a hook for unwatched list_items:\n%s", content)
	}
	if strings.Contains(content, "useAgentTaskList") {
		t.Errorf("client.gen.ts must not generate a hook for expose:agent agent_task:\n%s", content)
	}

	// SSE subscription wrappers.
	if !strings.Contains(content, `export function onItemChanged(cb: (p: Item) => void): () => void`) {
		t.Errorf("client.gen.ts missing onItemChanged SSE wrapper:\n%s", content)
	}

	// Binary helpers (HTTP-native, no base64 codec).
	for _, want := range []string{"export async function uploadFile(", "export async function streamVideo("} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing %q:\n%s", want, content)
		}
	}

	// Direct HTTP only: no bridge remnants.
	for _, bad := range []string{"window as any).sporemind", "requires the host bridge", "bridge.invoke"} {
		if strings.Contains(content, bad) {
			t.Errorf("client.gen.ts must not contain bridge remnant %q", bad)
		}
	}
}

// TestGenerateClientTSAppBasePrefix pins the gateway mount-base prefixing:
// the __sporemindAppBase handshake gate, prefixed invoke/EventSource URLs (no
// double slash — the concatenation carries the single "/"), and base-aware
// binary helpers.
func TestGenerateClientTSAppBasePrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http.appdef"), []byte(testHTTPAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileClientGenTS))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		// Handshake gate: data calls must await the snippet's ready promise
		// before reading the base — the panel's first fetch races the shell
		// bootstrap otherwise and 404s at the gateway root.
		`function ensureAppBaseReady(): Promise<void> {`,
		`w.__sporemindAppBaseReady instanceof Promise`,
		`async function appBase(): Promise<string> {`,
		`await ensureAppBaseReady();`,
		`return (window as any).__sporemindAppBase || "";`,
		// Prefixed invoke URL — base carries no trailing slash, so the only
		// "/" comes from the literal: "" → "/invoke/x", "/plugin/a" →
		// "/plugin/a/invoke/x".
		`fetch((await appBase()) + "/invoke/" + encodeURIComponent(callID)`,
		// Event channel is a window-level singleton shared with the bridge
		// client (one connection per URL), WS-first — panels no longer starve
		// transient invokes from the browser's HTTP/1.1 pool — with SSE
		// fallback for refused handshakes.
		`function eventChannel(url: string): EventChannel {`,
		`sock = new WebSocket(wsUrl);`,
		`function eventBackoff(attempt: number): number {`,
		`eventChannel(base + "/events").subscribe("*", dispatchEvent);`,
		`es = new EventSource(url, { withCredentials: true });`,
		// Binary helpers go through withAppBase (root-relative paths only).
		`async function withAppBase(url: string): Promise<string> {`,
		`url = await withAppBase(url);`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing %q:\n%s", want, content)
		}
	}

	// No un-prefixed URL construction may remain.
	for _, bad := range []string{
		`fetch("/invoke/"`,
		`new EventSource("/events"`,
	} {
		if strings.Contains(content, bad) {
			t.Errorf("client.gen.ts must not contain un-prefixed URL %q", bad)
		}
	}
}

// TestExampleClientGenTSMatchesGenerator pins plugin-dev-example's
// client.gen.ts to the current generator output byte-for-byte: the example
// cannot be regenerated from a worktree-bound agent (dev_generate runs on
// the main tree), so this golden comparison is the drift gate. Set
// UPDATE_EXAMPLE_CLIENT=1 to rewrite the file from the generator.
func TestExampleClientGenTSMatchesGenerator(t *testing.T) {
	appdefPath := filepath.Join("..", "..", "plugin-dev-example", "app.appdef")
	raw, err := os.ReadFile(appdefPath)
	if err != nil {
		t.Skipf("example appdef not available: %v", err)
	}
	app, diags, err := appdef.ParseFile(string(raw))
	if err != nil || len(diags) > 0 {
		t.Fatalf("ParseFile: %v diags=%v", err, diags)
	}
	want := generateClientTS(app)

	clientPath := filepath.Join("..", "..", "plugin-dev-example", FileClientGenTS)
	if os.Getenv("UPDATE_EXAMPLE_CLIENT") != "" {
		if err := os.WriteFile(clientPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("plugin-dev-example/client.gen.ts drifts from generator output; rerun with UPDATE_EXAMPLE_CLIENT=1\n--- got (first 400 chars) ---\n%.400s\n--- want (first 400 chars) ---\n%.400s", got, want)
	}
}

// TestGenerateHTTPAppBuilds is the compile gate for the whole T3 surface: the
// generated server.gen.go, main_run.gen.go (ServeHTTP + RunProcess) and the
// improved handlers.go stubs must build standalone against the vendored SDK.
func TestGenerateHTTPAppBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full SDK")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http.appdef"), []byte(testHTTPAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated HTTP app build failed: %v\n%s", err, out)
	}
}
