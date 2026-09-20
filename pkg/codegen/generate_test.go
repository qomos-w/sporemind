package codegen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/appdef"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// --- Helpers ---

// testSDKPath returns the real SDK directory for testing.
func testSDKPath(t *testing.T) string {
	t.Helper()
	// Walk from pkg/codegen up to find sporemind-plugin-sdk.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "sporemind-plugin-sdk")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("sporemind-plugin-sdk not found; skipping test that requires real SDK")
	return ""
}

// testGenOpts returns Generate options with the real SDK path for testing.
func testGenOpts(t *testing.T) Options {
	t.Helper()
	return Options{SDKPath: testSDKPath(t)}
}

// --- Test: todolist.appdef generates all six artifacts ---

const testTodolistAppdef = `// todolist.appdef — test example
app TodoList {
    id:          "app.todolist"
    name:        "TodoList"
    version:     "0.1.0"
    namespace:   "todolist"
    permissions: ["project.read_file", "project.write_file"]

    struct TodoItem {
        Id: string
        Title: string
        optional Done: bool
    }
    struct ListTodosRequest {
        optional IncludeDone: bool
    }
    struct AddTodoRequest {
        Title: string
    }
    struct AddTodoResponse {
        Item: TodoItem
    }
    struct RemoveTodoRequest {
        Id: string
    }
    type TodoItemList = array<TodoItem>

    callable list_todos {
        request:  ListTodosRequest
        response: TodoItemList
        effect:   "read"
        toolName: "todolist-list"
    }
    callable add_todo {
        request:  AddTodoRequest
        response: AddTodoResponse
        effect:   "write"
        toolName: "todolist-add"
    }
    callable remove_todo {
        request:  RemoveTodoRequest
        effect:   "mutate"
        toolName: "todolist-remove"
    }

    entrypoint view main {
        title: "Todo List"
        route: "/"
    }

    event todo_changed {
        payload: TodoItem
        permission: "public"
    }

    bundle main {
        title: "Todo List Tools"
        description: "CRUD for todo items"
        icon: "shield-check"
        color: "#9333ea"
        tools: [list_todos, add_todo, remove_todo]
    }
}
`

func TestGenerateTodolistAllArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Non-template generate must also vendor: every generated app is
	// self-contained regardless of host mode (dev checkout or release embed).
	if _, err := os.Stat(filepath.Join(dir, "vendor-sdk", "go.mod")); err != nil {
		t.Fatalf("vendor-sdk/go.mod missing after non-template generate: %v", err)
	}
	if !GoModUsesVendoredSDK(dir) {
		t.Fatal("go.mod replace does not point at ./vendor-sdk after non-template generate")
	}
	if result.SDKPath != filepath.Join(dir, VendorSDKDir) {
		t.Fatalf("SDKPath = %q, want %q", result.SDKPath, filepath.Join(dir, VendorSDKDir))
	}

	// Re-generating with the vendored path passed back as override must not
	// self-destruct (self-reference guard).
	if _, err := Generate(dir, Options{SDKPath: filepath.Join(dir, VendorSDKDir)}); err != nil {
		t.Fatalf("re-Generate with vendored override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor-sdk", "go.mod")); err != nil {
		t.Fatalf("vendor-sdk/go.mod missing after re-generate: %v", err)
	}

	// Should produce 7 generated files.
	wantFiles := map[string]bool{
		FileMainGenGo:    false,
		FileMainRunGo:    false,
		FileServerGenGo:  false,
		FileHandlersGo:   false,
		FileManifestJSON: false,
		FileSchemasGenGo: false,
		FileClientGenTS:  false,
	}
	for _, f := range result.Files {
		if _, ok := wantFiles[f]; ok {
			wantFiles[f] = true
		}
	}
	for f, found := range wantFiles {
		if !found {
			t.Errorf("missing generated file %q", f)
		}
	}

	// Verify all files exist on disk.
	for _, f := range result.Files {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("file %s not on disk: %v", f, err)
		}
	}

	// Verify manifest hash is non-empty.
	if result.Manifest.AppDefHash == "" {
		t.Error("manifest AppDefHash is empty")
	}

	// Verify manifest files map has entries (seven base artifacts + the
	// hostproto.gen.go stub for the declared fs.read/fs.write permissions).
	if len(result.Manifest.Files) != 8 {
		t.Errorf("manifest has %d files, want 8", len(result.Manifest.Files))
	}
}

// --- Test: listen blocks generate event payload types, OnXxx stubs and
// RegisterEventListener wiring; unknown kinds fail at zero writes ---

func TestGenerateListenEmission(t *testing.T) {
	dir := t.TempDir()
	appdefSrc := `app Listen {
    id: "app.listen"
    name: "Listen"
    version: "0.1.0"
    namespace: "listen"

    callable ping {
        effect: "read"
    }

    listen app_lifecycle { }
    listen app_event { }
}`
	if err := os.WriteFile(filepath.Join(dir, "listen.appdef"), []byte(appdefSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	handlers, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handlers), "func OnAppLifecycle(ev AppLifecycleEvent) error") {
		t.Error("handlers.go missing OnAppLifecycle stub")
	}
	if !strings.Contains(string(handlers), "func OnAppEvent(ev AppEventMessage) error") {
		t.Error("handlers.go missing OnAppEvent stub")
	}

	mainGen, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainGen), `ctx.RegisterEventListener("app_lifecycle", func(payload json.RawMessage) error {`) {
		t.Error("main.gen.go missing RegisterEventListener(app_lifecycle) wrapper")
	}
	if !strings.Contains(string(mainGen), "return OnAppLifecycle(ev)") {
		t.Error("main.gen.go missing OnAppLifecycle dispatch")
	}
	if !strings.Contains(string(mainGen), `ctx.RegisterEventListener("app_event", func(payload json.RawMessage) error {`) {
		t.Error("main.gen.go missing RegisterEventListener(app_event) wrapper")
	}
	if !strings.Contains(string(mainGen), "return OnAppEvent(ev)") {
		t.Error("main.gen.go missing OnAppEvent dispatch")
	}

	hostProto, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatalf("hostproto.gen.go missing: %v", err)
	}
	if !strings.Contains(string(hostProto), "type AppLifecycleEvent struct") {
		t.Error("hostproto.gen.go missing AppLifecycleEvent payload type")
	}
	if !strings.Contains(string(hostProto), "type AppEventMessage struct") {
		t.Error("hostproto.gen.go missing AppEventMessage payload type")
	}

	manifestBytes, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Listens []string `json:"Listens"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	if len(manifest.Listens) != 2 || manifest.Listens[0] != "app_event" || manifest.Listens[1] != "app_lifecycle" {
		t.Errorf("manifest Listens = %v, want [app_event app_lifecycle]", manifest.Listens)
	}

	// Unknown kind is rejected at zero writes.
	dir2 := t.TempDir()
	badAppdef := `app ListenBad {
    id: "app.listenbad"
    name: "ListenBad"
    version: "0.1.0"
    namespace: "listenbad"

    listen not_a_host_event { }
}`
	if err := os.WriteFile(filepath.Join(dir2, "listenbad.appdef"), []byte(badAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Generate(dir2, testGenOpts(t))
	if err == nil || !strings.Contains(err.Error(), "not a subscribable host event kind") {
		t.Fatalf("unknown listen kind must fail, got err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir2, FileMainGenGo)); statErr == nil {
		t.Error("generate must fail before writing artifacts for an unknown listen kind")
	}
}

// TestGenerateListenAgentKinds pins codegen for the capability-gated agent
// listen kinds: `listen step` / `listen agent_message_received` generate
// payload types, OnXxx stubs and RegisterEventListener wiring, with or
// without the agent.observe permission — the capability gate is enforced at
// delivery time (pluginhost handleEventDeliver), not codegen.
func TestGenerateListenAgentKinds(t *testing.T) {
	genListen := func(t *testing.T, permissions string) string {
		t.Helper()
		dir := t.TempDir()
		appdefSrc := `app AgentListen {
    id: "app.agentlisten"
    name: "AgentListen"
    version: "0.1.0"
    namespace: "agentlisten"
` + permissions + `
    callable ping {
        effect: "read"
    }

    listen step { }
    listen agent_message_received { }
}`
		if err := os.WriteFile(filepath.Join(dir, "agentlisten.appdef"), []byte(appdefSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(dir, testGenOpts(t)); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return dir
	}

	// Without the capability: codegen succeeds (delivery-time gate).
	dir := genListen(t, "")
	handlers, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handlers), "func OnStep(ev StepEvent) error") {
		t.Error("handlers.go missing OnStep stub")
	}
	if !strings.Contains(string(handlers), "func OnAgentMessageReceived(ev AgentMessage) error") {
		t.Error("handlers.go missing OnAgentMessageReceived stub")
	}

	hostProto, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hostProto), "type StepEvent struct") {
		t.Error("hostproto.gen.go missing StepEvent payload type")
	}
	if !strings.Contains(string(hostProto), "type AgentMessage struct") {
		t.Error("hostproto.gen.go missing AgentMessage payload type")
	}

	mainGen, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainGen), `ctx.RegisterEventListener("step", func(payload json.RawMessage) error {`) {
		t.Error("main.gen.go missing RegisterEventListener(step) wrapper")
	}
	if !strings.Contains(string(mainGen), "return OnStep(ev)") {
		t.Error("main.gen.go missing OnStep dispatch")
	}
	if !strings.Contains(string(mainGen), `ctx.RegisterEventListener("agent_message_received", func(payload json.RawMessage) error {`) {
		t.Error("main.gen.go missing RegisterEventListener(agent_message_received) wrapper")
	}
	if !strings.Contains(string(mainGen), "return OnAgentMessageReceived(ev)") {
		t.Error("main.gen.go missing OnAgentMessageReceived dispatch")
	}

	manifestBytes, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Listens     []string `json:"Listens"`
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	if len(manifest.Listens) != 2 || manifest.Listens[0] != "agent_message_received" || manifest.Listens[1] != "step" {
		t.Errorf("manifest Listens = %v, want [agent_message_received step]", manifest.Listens)
	}
	if len(manifest.Permissions) != 0 {
		t.Errorf("manifest Permissions = %v, want empty (capability optional at codegen)", manifest.Permissions)
	}

	// With the capability declared: still generates, permission carried
	// verbatim into the manifest (load-time grant, no callID).
	dir2 := genListen(t, `    permissions: ["agent.observe"]
`)
	manifestBytes2, err := os.ReadFile(filepath.Join(dir2, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest2 struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(manifestBytes2, &manifest2); err != nil {
		t.Fatalf("manifest2 unmarshal: %v", err)
	}
	if len(manifest2.Permissions) != 1 || manifest2.Permissions[0] != "agent.observe" {
		t.Errorf("manifest2 Permissions = %v, want [agent.observe]", manifest2.Permissions)
	}
}

func TestGenerateMainGenGoContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify key elements.
	checks := []struct {
		name, substr string
	}{
		{"generated-hash", "@generated-hash:"},
		{"package main", "package main"},
		{"sdk import", `sdk "github.com/qomos-w/sporemind-plugin-sdk"`},
		{"sdk.Register", "sdk.Register("},
		{"app id", `"app.todolist"`},
		{"app name", `"TodoList"`},
		{"version", `"0.1.0"`},
		{"permissions", `"fs.read"`},
		{"callable list_todos", `"list_todos"`},
		{"callable add_todo", `"add_todo"`},
		{"callable remove_todo", `"remove_todo"`},
		{"effect read", `Effect: "read"`},
		{"effect write", `Effect: "write"`},
		{"effect mutate", `Effect: "mutate"`},
		{"toolName", `"todolist-list"`},
		{"service", `"app.todolist"`},
		{"entrypoint", `"view"`},
		{"handler list_todos", "handleListTodos"},
		{"handler add_todo", "handleAddTodo"},
		{"handler remove_todo", "handleRemoveTodo"},
		{"void response", `ResponseSchema: "void"`},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.substr) {
			t.Errorf("main.gen.go missing %s: %q", c.name, c.substr)
		}
	}

	// The registration-only main.gen.go must contain no cgo ABI machinery: the
	// //export symbols moved to the release cgo shim (staged by the build) and
	// the subprocess entry lives in main_run.gen.go.
	absent := []struct {
		name, substr string
	}{
		{"cgo import", `import "C"`},
		{"export directives", "//export"},
		{"func main", "func main()"},
		{"unsafe import", `"unsafe"`},
		{"HandlePluginLog", "sdk.HandlePluginLog"},
		{"HandleInvokeFramed", "sdk.HandleInvokeFramed"},
	}
	for _, c := range absent {
		if strings.Contains(content, c.substr) {
			t.Errorf("main.gen.go must not contain %s: %q", c.name, c.substr)
		}
	}
}

func TestGenerateMainRunGoContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// main_run.gen.go must exist on disk and be part of the generated/protected set.
	if _, err := os.Stat(filepath.Join(dir, FileMainRunGo)); err != nil {
		t.Fatalf("main_run.gen.go not on disk: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileMainRunGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Subprocess entry: built only when cgo is disabled; starts the
	// HTTP listener (gateway data-path backend, ephemeral port) in parallel to the frame protocol.
	checks := []struct {
		name, substr string
	}{
		{"generated marker", "Code generated from appdef. DO NOT EDIT"},
		{"no-cgo build tag", "//go:build !cgo"},
		{"package main", "package main"},
		{"sdk import", `sdk "github.com/qomos-w/sporemind-plugin-sdk"`},
		{"ServeHTTP", `sdk.ServeHTTP("127.0.0.1:0")`},
		{"RunProcess entry", "sdk.RunProcess()"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.substr) {
			t.Errorf("main_run.gen.go missing %s: %q", c.name, c.substr)
		}
	}

	// Zero cgo: no C import, no //export, and no Register (that lives in main.gen.go).
	for _, bad := range []string{`import "C"`, "//export", "sdk.Register("} {
		if strings.Contains(content, bad) {
			t.Errorf("main_run.gen.go must not contain %q", bad)
		}
	}
}

func TestGenerateHandlersGoCreatesStubs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify header.
	if !strings.Contains(content, "agent-owned afterwards") {
		t.Error("handlers.go missing agent-owned header")
	}

	// Verify all three stubs exist.
	for _, name := range []string{"handleListTodos", "handleAddTodo", "handleRemoveTodo"} {
		if !strings.Contains(content, "func "+name+"(") {
			t.Errorf("handlers.go missing stub for %s", name)
		}
	}

	// Stubs are compiling scaffolds: zero-value/echo responses, never
	// ErrNotImplemented (a fresh scaffold builds and runs end to end).
	if strings.Contains(content, "ErrNotImplemented") {
		t.Error("handlers.go stubs must not use ErrNotImplemented")
	}
	// list_todos: array-alias response gets an empty composite literal.
	if !strings.Contains(content, "Payload: TodoItemList{}") {
		t.Errorf("handlers.go missing zero-value TodoItemList response:\n%s", content)
	}
	// add_todo: no same-named request/response fields → zero-value struct.
	if !strings.Contains(content, "Payload: AddTodoResponse{") {
		t.Errorf("handlers.go missing zero-value AddTodoResponse stub:\n%s", content)
	}
	// remove_todo: void response.
	if !strings.Contains(content, "return sdk.Response{Payload: nil}, nil") {
		t.Errorf("handlers.go missing void-response stub:\n%s", content)
	}
}

func TestGenerateHandlersGoDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	// First generate.
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Implement one handler (replace stub with real code).
	original := `package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handleListTodos(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: "real implementation"}, nil
}

func handleAddTodo(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}

func handleRemoveTodo(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}
`
	if err := os.WriteFile(filepath.Join(dir, FileHandlersGo), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-generate (same appdef).
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// The implemented handler must be preserved.
	if !strings.Contains(content, `"real implementation"`) {
		t.Error("handlers.go: implemented handler was overwritten")
	}
}

func TestGenerateManifestContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}

	var manifest gen.AppManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest JSON parse: %v", err)
	}

	// Verify manifest fields.
	if manifest.ID != "app.todolist" {
		t.Errorf("manifest ID = %q, want %q", manifest.ID, "app.todolist")
	}
	if manifest.Name != "TodoList" {
		t.Errorf("manifest Name = %q", manifest.Name)
	}
	if manifest.Version != "0.1.0" {
		t.Errorf("manifest Version = %q", manifest.Version)
	}
	if manifest.Runtime != "native" {
		t.Errorf("manifest Runtime = %q", manifest.Runtime)
	}
	if manifest.ProtocolVersion != 2 {
		t.Errorf("manifest ProtocolVersion = %d", manifest.ProtocolVersion)
	}
	if manifest.SdkVersion != sdk.Version {
		t.Errorf("manifest SdkVersion = %q, want SDK %q", manifest.SdkVersion, sdk.Version)
	}
	if manifest.Namespace != "todolist" {
		t.Errorf("manifest Namespace = %q", manifest.Namespace)
	}

	// Callables.
	if len(manifest.Callables) != 3 {
		t.Fatalf("manifest callables = %d, want 3", len(manifest.Callables))
	}
	c0 := manifest.Callables[0]
	if c0.ID != "list_todos" {
		t.Errorf("callable[0] ID = %q", c0.ID)
	}
	if c0.RequestSchema != "ListTodosRequest" {
		t.Errorf("callable[0] RequestSchema = %q", c0.RequestSchema)
	}
	if c0.ResponseSchema != "TodoItemList" {
		t.Errorf("callable[0] ResponseSchema = %q, want %q", c0.ResponseSchema, "TodoItemList")
	}
	if c0.Effect != "read" {
		t.Errorf("callable[0] Effect = %q", c0.Effect)
	}
	if c0.ToolName != "todolist-list" {
		t.Errorf("callable[0] ToolName = %q", c0.ToolName)
	}
	if c0.Service != "app.todolist" {
		t.Errorf("callable[0] Service = %q", c0.Service)
	}

	// remove_todo response is void.
	c2 := manifest.Callables[2]
	if c2.ResponseSchema != "void" {
		t.Errorf("callable[2] ResponseSchema = %q, want void", c2.ResponseSchema)
	}

	// Entrypoints.
	if len(manifest.Entrypoints) != 1 {
		t.Fatalf("manifest entrypoints = %d, want 1", len(manifest.Entrypoints))
	}
	ep := manifest.Entrypoints[0]
	if ep.Kind != "view" || ep.ID != "main" {
		t.Errorf("entrypoint = %+v", ep)
	}

	// Events.
	if len(manifest.Events) != 1 {
		t.Fatalf("manifest events = %d, want 1", len(manifest.Events))
	}
	if manifest.Events[0].ID != "todo_changed" {
		t.Errorf("event ID = %q", manifest.Events[0].ID)
	}

	// Bundles.
	if len(manifest.Bundles) != 1 {
		t.Fatalf("manifest bundles = %d, want 1", len(manifest.Bundles))
	}
	b := manifest.Bundles[0]
	if b.Title != "Todo List Tools" {
		t.Errorf("bundle Title = %q", b.Title)
	}
	if b.Icon != "shield-check" {
		t.Errorf("bundle Icon = %q, want shield-check", b.Icon)
	}
	if b.Color != "#9333ea" {
		t.Errorf("bundle Color = %q, want #9333ea", b.Color)
	}
	if len(b.Tools) != 3 {
		t.Errorf("bundle Tools = %d, want 3", len(b.Tools))
	}
}

func TestGenerateSchemasGoContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileSchemasGenGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Verify struct definitions.
	structs := []string{"TodoItem", "ListTodosRequest", "AddTodoRequest", "AddTodoResponse", "RemoveTodoRequest"}
	for _, s := range structs {
		if !strings.Contains(content, "type "+s+" struct {") {
			t.Errorf("schemas_gen.go missing struct %s", s)
		}
	}

	// Verify optional field uses *bool with omitempty.
	if !strings.Contains(content, `Done *bool`) {
		t.Error("schemas_gen.go missing optional *bool for Done")
	}
	if !strings.Contains(content, `json:"Done,omitempty"`) {
		t.Error("schemas_gen.go missing omitempty for Done")
	}

	// Verify type alias is generated in Go.
	if !strings.Contains(content, "type TodoItemList = []TodoItem") {
		t.Error("schemas_gen.go missing type alias TodoItemList")
	}
}

func TestGenerateClientTS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
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

	// Verify interfaces.
	interfaces := []string{"TodoItem", "ListTodosRequest", "AddTodoRequest", "AddTodoResponse", "RemoveTodoRequest"}
	for _, s := range interfaces {
		if !strings.Contains(content, "export interface "+s+" {") {
			t.Errorf("client.gen.ts missing interface %s", s)
		}
	}

	// Verify invoke wrappers go over direct HTTP (fetch POST /invoke/{id},
	// same-origin credentials, JSON body) — no host bridge relay.
	wrappers := []struct {
		fnName     string
		callable   string
		responseTS string
	}{
		{"listTodos", "list_todos", "TodoItemList"},
		{"addTodo", "add_todo", "AddTodoResponse"},
		{"removeTodo", "remove_todo", "void"},
	}
	for _, w := range wrappers {
		if !strings.Contains(content, "export async function "+w.fnName+"(") {
			t.Errorf("client.gen.ts missing function %s", w.fnName)
		}
		// invoke<T> with the callable ID as the method argument.
		expected := fmt.Sprintf("invoke<%s>(%q, req as Record<string, unknown>)", w.responseTS, w.callable)
		if !strings.Contains(content, expected) {
			t.Errorf("client.gen.ts missing invoke for %s (need %q)", w.callable, expected)
		}
	}

	// Verify no @capacitor/core dependency.
	if strings.Contains(content, "@capacitor/core") {
		t.Error("client.gen.ts should not import from @capacitor/core")
	}

	// Verify the local invoke helper posts JSON same-origin to /invoke/{id}
	// under the injected gateway mount base (empty in standalone dev).
	for _, want := range []string{
		"async function invoke<T>",
		`fetch((await appBase()) + "/invoke/" + encodeURIComponent(callID)`,
		`credentials: "same-origin"`,
		`method: "POST"`,
		`"Content-Type": "application/json"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing %q", want)
		}
	}

	// The gateway data path must not reference the management bridge or
	// any standalone relay fallback.
	for _, bad := range []string{
		"window as any).sporemind",
		"/api/pluginhost.invoke",
		"__spore_plugin_invoke",
		"plugin.app.",
		"requires the host bridge",
	} {
		if strings.Contains(content, bad) {
			t.Errorf("client.gen.ts must not contain %q", bad)
		}
	}

	// Verify event subscription machinery: window-level channel shared with
	// the bridge client, WS-first (mount-base prefixed URL derived inside
	// the channel, capped-backoff reconnect), SSE fallback, per-kind
	// dispatch preserved.
	for _, want := range []string{
		`sock = new WebSocket(wsUrl);`,
		`subs.get("*")?.forEach((cb) => cb(env.event!, env.data));`,
		`es = new EventSource(url, { withCredentials: true });`,
		"function subscribeEvent(eventKind: string",
		"function eventBackoff(attempt: number): number {",
		`eventChannel(base + "/events").subscribe("*", dispatchEvent);`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing event support %q", want)
		}
	}

	// Verify binary helpers (HTTP-native, no base64 codec).
	for _, want := range []string{
		"export async function uploadFile(",
		"export async function streamVideo(",
		"MediaSource",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("client.gen.ts missing binary helper %q", want)
		}
	}

	// Verify type alias is generated in TS.
	if !strings.Contains(content, "export type TodoItemList = TodoItem[];") {
		t.Error("client.gen.ts missing type alias TodoItemList")
	}
}

// --- Test: empty project generates full default scaffold ---

// dirEntryNames lists all entries in dir (sorted), for zero-write assertions.
func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestGenerateNoAppDefWithoutTemplateFails is the BP9 guard: a directory with
// no .appdef must fail loudly with a hint instead of silently scaffolding
// template files into it (2026-08-26 main-repo-root pollution).
func TestGenerateNoAppDefWithoutTemplateFails(t *testing.T) {
	dir := t.TempDir()

	result, err := Generate(dir, testGenOpts(t))
	if err == nil {
		t.Fatalf("Generate on appdef-less dir succeeded: %+v", result)
	}
	if !strings.Contains(err.Error(), "no .appdef found") || !strings.Contains(err.Error(), "pass Template") {
		t.Errorf("error = %q, want the scaffold hint", err.Error())
	}

	// Zero writes: the directory must be untouched.
	if names := dirEntryNames(t, dir); len(names) != 0 {
		t.Errorf("directory was written despite refusal: %v", names)
	}
}

// TestGenerateTemplateCreatesMissingDir is the BP-1 regression from the
// external TOTP run: scaffolding Template=true into a subdirectory the caller
// never created failed with "The system cannot find the path specified"
// because the appdef write never mkdir'ed the target.
func TestGenerateTemplateCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "totp-app")

	result, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true})
	if err != nil {
		t.Fatalf("Generate into missing dir: %v", err)
	}
	if !result.Template {
		t.Error("expected Template=true")
	}
	if _, err := os.Stat(filepath.Join(dir, FileAppDef)); err != nil {
		t.Errorf("app.appdef missing after scaffold: %v", err)
	}
}

func TestGenerateEmptyProjectTemplate(t *testing.T) {
	dir := t.TempDir()

	result, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !result.Template {
		t.Error("expected Template=true for empty project")
	}

	// Should produce all 6 generated files + go.mod (7 total).
	// The template path writes app.appdef, then generateFromAppDef produces
	// the usual 6 artifacts + go.mod, and handlers.go is included in the list.
	expectedFiles := []string{
		FileAppDef,
		FileMainGenGo,
		FileMainRunGo,
		FileServerGenGo,
		FileHandlersGo,
		FileManifestJSON,
		FileSchemasGenGo,
		FileClientGenTS,
		FileGoMod,
	}
	for _, f := range expectedFiles {
		found := false
		for _, rf := range result.Files {
			if rf == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("result.Files missing %q", f)
		}
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("file %s not on disk: %v", f, err)
		}
	}

	// Verify handlers.go has the default template content.
	data, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "handlePing") {
		t.Error("handlers.go missing handlePing")
	}

	// Verify app.appdef content.
	data, err = os.ReadFile(filepath.Join(dir, FileAppDef))
	if err != nil {
		t.Fatal(err)
	}
	content = string(data)
	if !strings.Contains(content, `id:        "app.default"`) {
		t.Error("app.appdef missing app.default id")
	}
	if !strings.Contains(content, "callable ping") {
		t.Error("app.appdef missing ping callable")
	}
	if !strings.Contains(content, "entrypoint view main") {
		t.Error("app.appdef missing entrypoint")
	}
	// The template is the canonical syntax reference: named-struct
	// request/response references, the optional prefix modifier, and struct
	// blocks inside the app block must all be present.
	if !strings.Contains(content, "request:  PingRequest") || !strings.Contains(content, "response: PingResponse") {
		t.Error("app.appdef missing named-struct request/response reference")
	}
	if !strings.Contains(content, "optional Style:") {
		t.Error("app.appdef missing optional prefix modifier example")
	}
	if !strings.Contains(content, "struct PingRequest") {
		t.Error("app.appdef missing struct block")
	}

	// Verify main.gen.go exists and has the ping handler.
	data, err = os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	content = string(data)
	if !strings.Contains(content, "handlePing") {
		t.Error("main.gen.go missing handlePing registration")
	}

	// Verify app.manifest.json exists and is valid JSON.
	data, err = os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"app.default"`) {
		t.Error("app.manifest.json missing app.default id")
	}

	// Verify go.mod exists.
	data, err = os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "module app.default") {
		t.Errorf("go.mod missing module declaration: %s", string(data))
	}

	// Verify manifest hash is non-empty.
	if result.Manifest.AppDefHash == "" {
		t.Error("manifest AppDefHash is empty")
	}
}

// --- Test: template re-generate preserves existing handlers.go ---

// TestGenerateTemplateReGeneratePreservesHandlers: after a template scaffold,
// deleting the .appdef and re-generating must NOT silently re-scaffold over
// the now-populated directory. Both the implicit path (no Template) and the
// explicit path (Template=true, directory no longer Go-empty) refuse, and the
// agent-owned handlers.go is left untouched on disk.
func TestGenerateTemplateReGeneratePreservesHandlers(t *testing.T) {
	dir := t.TempDir()

	// First generate on empty dir produces the full scaffold.
	_, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true})
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	// Overwrite handlers.go with a custom implementation (no ErrNotImplemented).
	customHandlers := `package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handlePing(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]string{"custom": "implementation"}}, nil
}
`
	if err := os.WriteFile(filepath.Join(dir, FileHandlersGo), []byte(customHandlers), 0o644); err != nil {
		t.Fatal(err)
	}

	// Now delete the appdef so the template path would be triggered again.
	if err := os.Remove(filepath.Join(dir, FileAppDef)); err != nil {
		t.Fatal(err)
	}

	// Re-generate without Template: refused with the .appdef hint.
	if _, err := Generate(dir, testGenOpts(t)); err == nil {
		t.Fatal("re-generate without Template on appdef-less dir succeeded; want refusal")
	} else if !strings.Contains(err.Error(), "no .appdef found") {
		t.Errorf("error = %q, want no-.appdef refusal", err.Error())
	}

	// Re-generate with Template: still refused — the directory now contains
	// Go sources, scaffolding over it is the BP9 pollution scenario.
	if _, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true}); err == nil {
		t.Fatal("re-generate with Template over populated dir succeeded; want refusal")
	} else if !strings.Contains(err.Error(), "refused") {
		t.Errorf("error = %q, want non-empty refusal", err.Error())
	}

	data, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Custom implementation must be preserved.
	if !strings.Contains(content, `"custom"`) {
		t.Error("custom handler implementation was lost on re-generate")
	}
	// The default handlePing from the template must not be present.
	if strings.Contains(content, `"pong"`) {
		t.Error("default handlePing content leaked into preserved handlers.go")
	}
	// The appdef must not be re-created by the refused runs.
	if _, err := os.Stat(filepath.Join(dir, FileAppDef)); err == nil {
		t.Error("app.appdef was recreated by a refused generate")
	}
}

// TestGenerateTemplateRefusesGoProjectDir: even an explicit Template request
// must refuse to scaffold into a directory that already has go.mod or *.go —
// the exact shape of the 2026-08-26 main-repo-root pollution.
func TestGenerateTemplateRefusesGoProjectDir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(dir string)
	}{
		{"go.mod", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, FileGoMod), []byte("module example.com/polluted\n\ngo 1.25\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"go files", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)

			if _, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true}); err == nil {
				t.Fatal("Generate with Template over Go directory succeeded; want refusal")
			} else if !strings.Contains(err.Error(), "refused") {
				t.Errorf("error = %q, want refusal message", err.Error())
			}

			// Zero writes: only the pre-existing marker remains.
			names := dirEntryNames(t, dir)
			if len(names) != 1 {
				t.Errorf("directory must be untouched; entries = %v", names)
			}
		})
	}
}

// TestGenerateTemplateAllowsNonGoFiles: non-Go clutter (README, assets) does
// not block an explicit template request — only go.mod / *.go do.
func TestGenerateTemplateAllowsNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !result.Template {
		t.Error("expected Template=true")
	}
	if _, err := os.Stat(filepath.Join(dir, FileAppDef)); err != nil {
		t.Errorf("app.appdef not scaffolded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileMainGenGo)); err != nil {
		t.Errorf("main.gen.go not scaffolded: %v", err)
	}
}

// --- Test: go.mod create-if-absent ---

func TestPortableRelPathBareSibling(t *testing.T) {
	// Shapes that produced invalid go.mod replace directives (replacement
	// without ./ prefix): project root is an ancestor of the SDK directory.
	rel, err := portableRelPath("D:/dev/sporemind", "D:/dev/sporemind/sporemind-plugin-sdk")
	if err != nil || rel != "./sporemind-plugin-sdk" {
		t.Errorf("bare child rel = %q, %v; want ./sporemind-plugin-sdk", rel, err)
	}
	rel, err = portableRelPath("D:/dev/sporemind/plugin-dev-example", "D:/dev/sporemind/sporemind-plugin-sdk")
	if err != nil || rel != "../sporemind-plugin-sdk" {
		t.Errorf("parent rel = %q, %v; want ../sporemind-plugin-sdk", rel, err)
	}
}

func TestGenerateGoModCreated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "module app.todolist") {
		t.Errorf("go.mod missing module declaration: %s", content)
	}
	if !strings.Contains(content, "require github.com/qomos-w/sporemind-plugin-sdk") {
		t.Errorf("go.mod missing SDK require: %s", content)
	}
	if !strings.Contains(content, "replace github.com/qomos-w/sporemind-plugin-sdk =>") {
		t.Errorf("go.mod missing SDK replace directive: %s", content)
	}
	// The SDK is dependency-free (no spore): the spore replace must NOT be
	// written — it would point at a host-local path missing in user
	// environments. Bounded match: "qomos-w/spore " (trailing space) never
	// hits the sporemind-plugin-sdk line.
	if strings.Contains(content, "qomos-w/spore =>") || strings.Contains(content, "qomos-w/spore v") {
		t.Errorf("go.mod must not reference the spore module: %s", content)
	}
	// Verify the go directive satisfies the dependency closure (max of SDK
	// and spore versions — tidy would otherwise bump it after the fact).
	sdkPath := testGenOpts(t).SDKPath
	wantVer, err := readSDKGoVersion(sdkPath)
	if err != nil {
		t.Fatalf("readSDKGoVersion: %v", err)
	}
	if sporePath, serr := resolveSporePath(sdkPath); serr == nil {
		if sporeVer, verr := readGoDirective(filepath.Join(sporePath, FileGoMod)); verr == nil {
			wantVer = maxGoVersion(wantVer, sporeVer)
		}
	}
	if !strings.Contains(content, "go "+wantVer+"\n") {
		t.Errorf("go.mod missing or wrong go directive: %s", content)
	}
	// Verify no Windows backslashes in replace paths.
	if strings.Contains(content, `\`) {
		t.Errorf("go.mod contains backslashes (not portable): %s", content)
	}
}

func TestEnsureGoModContentGoDirectiveRaiseOnly(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, FileGoMod)

	// Existing directive newer than required must be preserved, not downgraded.
	existing := "module app.x\n\ngo 1.28.0\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n"
	if err := os.WriteFile(goModPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err := ensureGoModContent(dir, goModPath, "app.x", "1.25.0", "../sdk", "../spore")
	if err != nil {
		t.Fatalf("ensureGoModContent: %v", err)
	}
	if strings.Contains(content, "go 1.25.0") || !strings.Contains(content, "go 1.28.0") {
		t.Errorf("newer go directive was downgraded:\n%s", content)
	}

	// Older directive must be raised to the required version.
	if err := os.WriteFile(goModPath, []byte("module app.x\n\ngo 1.23.0\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err = ensureGoModContent(dir, goModPath, "app.x", "1.25.0", "../sdk", "../spore")
	if err != nil {
		t.Fatalf("ensureGoModContent: %v", err)
	}
	if !strings.Contains(content, "go 1.25.0") {
		t.Errorf("older go directive was not raised:\n%s", content)
	}
}

// TestEnsureGoModContentRewritesEmptyModuleName: a first generate with an
// empty appdef id writes a nameless `module ` directive. The next regenerate
// (now with a real id) must rewrite that line in place — prepending a fresh
// module line produced a duplicate-module go.mod (round-5 TOTP run).
func TestEnsureGoModContentRewritesEmptyModuleName(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, FileGoMod)

	existing := "module \n\ngo 1.25.0\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n"
	if err := os.WriteFile(goModPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	content, modified, err := ensureGoModContent(dir, goModPath, "app.totp", "1.25.0", "../sdk", "../spore")
	if err != nil {
		t.Fatalf("ensureGoModContent: %v", err)
	}
	if !modified {
		t.Fatal("expected rewrite of empty module name")
	}
	if got := strings.Count(content, "module"); got != 1 {
		t.Fatalf("module directive count = %d, want 1:\n%s", got, content)
	}
	if !strings.Contains(content, "module app.totp") {
		t.Fatalf("module name not rewritten:\n%s", content)
	}
}

func TestCompareGoVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.25.0", "1.27.0", -1},
		{"1.27.0", "1.25.0", 1},
		{"1.25", "1.25.0", 0},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
	}
	for _, c := range cases {
		if got := compareGoVersion(c.a, c.b); got != c.want {
			t.Errorf("compareGoVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if maxGoVersion("1.25.0", "1.27.0") != "1.27.0" {
		t.Error("maxGoVersion should pick 1.27.0")
	}
}

func TestGenerateGoModPreservesExistingDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create go.mod with user-added dependency.
	existingGoMod := `module app.todolist

go 1.25.0

require (
	github.com/qomos-w/sporemind-plugin-sdk v0.0.0
	github.com/some/extra v1.0.0
)

replace github.com/qomos-w/sporemind-plugin-sdk => /some/path
`
	if err := os.WriteFile(filepath.Join(dir, FileGoMod), []byte(existingGoMod), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// User dependency must be preserved.
	if !strings.Contains(content, "github.com/some/extra") {
		t.Error("go.mod lost user-added dependency")
	}
	// Spore replace must NOT be injected — the SDK is dependency-free, so a
	// spore replace would point at a host-local path missing in user
	// environments.
	if strings.Contains(content, "qomos-w/spore =>") || strings.Contains(content, "qomos-w/spore v") {
		t.Errorf("go.mod must not reference the spore module: %s", content)
	}
}

// --- Test: stale spore directives are stripped ---

func TestGenerateGoModRemovesStaleSporeDirectives(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create go.mod carrying a stale spore replace (legacy layout where
	// the SDK still depended on spore).
	existingGoMod := `module app.todolist

go 1.25.0

require (
	github.com/qomos-w/sporemind-plugin-sdk v0.0.0
	github.com/some/extra v1.0.0
)

replace github.com/qomos-w/sporemind-plugin-sdk => /some/path

replace github.com/qomos-w/spore => ../spore
`
	if err := os.WriteFile(filepath.Join(dir, FileGoMod), []byte(existingGoMod), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if strings.Contains(content, "qomos-w/spore =>") || strings.Contains(content, "qomos-w/spore v") {
		t.Errorf("stale spore replace must be removed: %s", content)
	}
	if !strings.Contains(content, "replace github.com/qomos-w/sporemind-plugin-sdk =>") {
		t.Errorf("go.mod lost SDK replace directive: %s", content)
	}
	if !strings.Contains(content, "github.com/some/extra") {
		t.Error("go.mod lost user-added dependency")
	}
}

// --- Test: callableIDToHandlerName ---

func TestCallableIDToHandlerName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"ping", "handlePing"},
		{"list_todos", "handleListTodos"},
		{"add_todo", "handleAddTodo"},
		{"remove_todo", "handleRemoveTodo"},
		{"get_config", "handleGetConfig"},
	}
	for _, tt := range tests {
		got := CallableIDToHandlerName(tt.input)
		if got != tt.want {
			t.Errorf("CallableIDToHandlerName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCallableIDToTSFuncName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"ping", "ping"},
		{"list_todos", "listTodos"},
		{"add_todo", "addTodo"},
		{"remove_todo", "removeTodo"},
	}
	for _, tt := range tests {
		got := CallableIDToTSFuncName(tt.input)
		if got != tt.want {
			t.Errorf("CallableIDToTSFuncName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Test: mergeHandlers preserves existing + appends new ---

func TestMergeHandlersNewFile(t *testing.T) {
	dir := t.TempDir()
	handlersPath := filepath.Join(dir, FileHandlersGo)

	callables := []appdef.CallableDecl{
		{ID: "foo"},
		{ID: "bar"},
	}

	result, err := mergeHandlers(handlersPath, callables, nil, &appdef.AppDef{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "handleFoo") {
		t.Error("missing handleFoo stub")
	}
	if !strings.Contains(result, "handleBar") {
		t.Error("missing handleBar stub")
	}
	// New-style stubs: compiling zero-value responses, no ErrNotImplemented.
	if strings.Contains(result, "ErrNotImplemented") {
		t.Error("stubs must not use ErrNotImplemented")
	}
	if !strings.Contains(result, "return sdk.Response{Payload: nil}, nil") {
		t.Errorf("void stubs missing zero-value response:\n%s", result)
	}
}

func TestMergeHandlersExistingFile(t *testing.T) {
	dir := t.TempDir()
	handlersPath := filepath.Join(dir, FileHandlersGo)

	// Existing file with one implemented handler and one stub.
	existing := `package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handleFoo(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: "implemented!"}, nil
}

func handleBar(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}
`
	if err := os.WriteFile(handlersPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	callables := []appdef.CallableDecl{
		{ID: "foo"}, // already exists
		{ID: "bar"}, // already exists
		{ID: "baz"}, // new
	}

	result, err := mergeHandlers(handlersPath, callables, nil, &appdef.AppDef{})
	if err != nil {
		t.Fatal(err)
	}

	// Existing code must be preserved.
	if !strings.Contains(result, `"implemented!"`) {
		t.Error("existing implementation was lost")
	}

	// New stub must be appended.
	if !strings.Contains(result, "handleBaz") {
		t.Error("missing handleBaz stub")
	}

	// Should not have duplicate handleFoo or handleBar.
	count := strings.Count(result, "func handleFoo(")
	if count != 1 {
		t.Errorf("handleFoo appears %d times, want 1", count)
	}
}

// --- Test: validation errors propagate ---

func TestGenerateInvalidAppdef(t *testing.T) {
	dir := t.TempDir()
	bad := `app Bad {
    struct Foo { X: string }
    callable my_func {
        request: NoSuchType
    }
}`
	if err := os.WriteFile(filepath.Join(dir, "bad.appdef"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Generate(dir, testGenOpts(t))
	if err == nil {
		t.Fatal("expected error for invalid appdef")
	}
	if !strings.Contains(err.Error(), "appdef diagnostics") {
		t.Errorf("expected diagnostics in error, got: %v", err)
	}
}

// --- Test: manifest hash is consistent ---

func TestGenerateManifestFreeAgent(t *testing.T) {
	src := `app Bound {
    id: "app.bound"
    name: "Bound"
    version: "0.1.0"
    namespace: "bound"

    callable ping {
        effect: "read"
    }

    entrypoint view main {
        title: "Main"
        route: "/"
    }

    event changed {
        payload: void
    }

    free_agent {
        allow_create: true
        allow_switch: false
        allow_message: true
        agent_kinds: [coder, reviewer]
    }
}
`
	app, _, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if diags := appdef.Validate(app); len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
		t.Fatal("validation failed")
	}

	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}

	binding := manifest.AgentBinding
	if binding == nil {
		t.Fatal("manifest.AgentBinding = nil, want binding")
	}
	if binding.Surface != nil {
		t.Errorf("Surface = %+v, want nil (no static agent binding)", binding.Surface)
	}
	if binding.Capability != nil {
		t.Errorf("Capability = %+v, want nil (agents mount bundles dynamically)", binding.Capability)
	}
	if binding.FreeAgent == nil {
		t.Fatal("manifest.AgentBinding.FreeAgent = nil")
	}
	if !binding.FreeAgent.AllowCreate || binding.FreeAgent.AllowSwitch || !binding.FreeAgent.AllowMessage {
		t.Errorf("FreeAgent flags = %+v", binding.FreeAgent)
	}
	if len(binding.FreeAgent.AgentKinds) != 2 || binding.FreeAgent.AgentKinds[0] != "coder" {
		t.Errorf("FreeAgent.AgentKinds = %v", binding.FreeAgent.AgentKinds)
	}

	// JSON round-trip must preserve the binding contract (the host reads the
	// same file at register time).
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var rt gen.AppManifest
	if err := json.Unmarshal(data, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.AgentBinding == nil || rt.AgentBinding.FreeAgent == nil || !rt.AgentBinding.FreeAgent.AllowCreate {
		t.Errorf("JSON round-trip lost free agent binding: %+v", rt.AgentBinding)
	}
}

func TestGenerateManifestPluginAgent(t *testing.T) {
	src := `app Plugged {
    id: "app.plugged"
    name: "Plugged"
    version: "0.1.0"
    namespace: "plugged"

    callable ping {
        effect: "read"
    }

    bundle "Assistant Tools" {
        tools: [ping]
    }

    plugin_agent {
        display_name: "Plugged Assistant"
        system_prompt: "You operate the Plugged plugin."
        bundles: ["builtin:bundle:web-search"]
    }

    plugin_agent reviewer {
        display_name: "Plugged Reviewer"
    }
}
`
	app, _, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if diags := appdef.Validate(app); len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
		t.Fatal("validation failed")
	}

	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}

	binding := manifest.AgentBinding
	if binding == nil {
		t.Fatal("manifest.AgentBinding = nil, want binding")
	}
	// The unnamed block must round-trip as the "default" slot (HEAD
	// equivalence: a manifest written before slots existed describes the same
	// agent once the host normalizes the slot to "default").
	if len(binding.PluginAgents) != 2 {
		t.Fatalf("PluginAgents = %d, want 2", len(binding.PluginAgents))
	}
	def := binding.PluginAgents[0]
	if def.Name != "default" {
		t.Errorf("PluginAgents[0].Name = %q, want default", def.Name)
	}
	if def.DisplayName != "Plugged Assistant" {
		t.Errorf("PluginAgents[0].DisplayName = %q", def.DisplayName)
	}
	if def.SystemPrompt != "You operate the Plugged plugin." {
		t.Errorf("PluginAgents[0].SystemPrompt = %q", def.SystemPrompt)
	}
	if len(def.Bundles) != 1 || def.Bundles[0] != "builtin:bundle:web-search" {
		t.Errorf("PluginAgents[0].Bundles = %v", def.Bundles)
	}
	rev := binding.PluginAgents[1]
	if rev.Name != "reviewer" || rev.DisplayName != "Plugged Reviewer" {
		t.Errorf("PluginAgents[1] = %+v", rev)
	}
	// plugin_agent does not imply free_agent
	if binding.FreeAgent != nil {
		t.Errorf("FreeAgent = %+v, want nil", binding.FreeAgent)
	}

	// JSON round-trip must preserve the binding contract (the host reads the
	// same file at register time).
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var rt gen.AppManifest
	if err := json.Unmarshal(data, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.AgentBinding == nil || len(rt.AgentBinding.PluginAgents) != 2 ||
		rt.AgentBinding.PluginAgents[0].Name != "default" ||
		rt.AgentBinding.PluginAgents[0].DisplayName != "Plugged Assistant" ||
		rt.AgentBinding.PluginAgents[1].Name != "reviewer" {
		t.Errorf("JSON round-trip lost plugin agent bindings: %+v", rt.AgentBinding)
	}
}

func TestGenerateManifestPluginAgentSingleUnnamedDefaultsSlot(t *testing.T) {
	// An app with exactly one unnamed plugin_agent block produces
	// PluginAgents == [{Name: "default", ...}]: the manifest shape differs
	// from HEAD (list + explicit slot) but describes the identical binding.
	src := `app Solo {
    id: "app.solo"
    name: "Solo"
    version: "0.1.0"
    namespace: "solo"

    plugin_agent {
        display_name: "Solo Assistant"
    }
}
`
	app, _, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if diags := appdef.Validate(app); len(diags) > 0 {
		t.Fatalf("validation: %v", diags)
	}
	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}
	if manifest.AgentBinding == nil || len(manifest.AgentBinding.PluginAgents) != 1 ||
		manifest.AgentBinding.PluginAgents[0].Name != "default" ||
		manifest.AgentBinding.PluginAgents[0].DisplayName != "Solo Assistant" {
		t.Fatalf("AgentBinding = %+v", manifest.AgentBinding)
	}
}

func TestGenerateManifestNoAgentBinding(t *testing.T) {
	app, _, err := appdef.ParseFile(testTodolistAppdef)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}
	if manifest.AgentBinding != nil {
		t.Errorf("manifest.AgentBinding = %+v, want nil when no free_agent declared", manifest.AgentBinding)
	}
}

const docCommentManifestAppdef = `app Doc {
    id: "app.doc"
    name: "Doc"
    version: "0.1.0"
    namespace: "doc"

    // 全网搜索工具
    callable search {
        effect: "read"
    }

    callable plain {
        effect: "read"
    }

    entrypoint view main {
        title: "Main"
        route: "/"
    }

    // 注释回落描述
    bundle main {
        title: "Doc tools"
        tools: [search, plain]
    }
}
`

// TestGenerateManifestCallableDescription verifies the doc-comment flows
// into BOTH manifest surfaces: app.manifest.json (GenerateManifestFromAppDef)
// and the runtime PluginManifest source embedded in main.gen.go
// (generateMainGo). A drift between the two is caught by the
// manifest-consistency gate, so both write points must carry the field.
func TestGenerateManifestCallableDescription(t *testing.T) {
	app, diags, err := appdef.ParseFile(docCommentManifestAppdef)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected diagnostic: %s", d)
		}
	}

	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}
	if len(manifest.Callables) != 2 {
		t.Fatalf("manifest callables = %d, want 2", len(manifest.Callables))
	}
	if got := manifest.Callables[0].Description; got != "全网搜索工具" {
		t.Errorf("search Description = %q, want %q", got, "全网搜索工具")
	}
	if got := manifest.Callables[1].Description; got != "" {
		t.Errorf("plain Description = %q, want empty", got)
	}
	if len(manifest.Bundles) != 1 {
		t.Fatalf("manifest bundles = %d, want 1", len(manifest.Bundles))
	}
	if got := manifest.Bundles[0].Description; got != "注释回落描述" {
		t.Errorf("bundle Description = %q, want %q", got, "注释回落描述")
	}

	mainGo := generateMainGo(app, "hash")
	if !strings.Contains(mainGo, `Description: "全网搜索工具"`) {
		t.Errorf("main.gen.go callable literal missing Description — runtime PluginManifest would drift from app.manifest.json")
	}
}

func TestGenerateManifestHashConsistent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todolist.appdef"), []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	r1, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatal(err)
	}

	if r1.Manifest.AppDefHash != r2.Manifest.AppDefHash {
		t.Errorf("hash not consistent: %q vs %q", r1.Manifest.AppDefHash, r2.Manifest.AppDefHash)
	}
}

// CRLF line endings must not change the .appdef drift hash: dev_generate reads
// raw bytes while dev_gate reads through project.read (which normalizes CRLF
// to LF). The stored hash must be computed on the normalized form so a CRLF
// .appdef can pass manifest_consistency without manual line-ending fixes.
// Regression: 2026-09-01, sporecloud TOTP round-6 smoke test blocker.
func TestGenerateAppDefHashCRLFNormalized(t *testing.T) {
	lf := testTodolistAppdef
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	dirLF := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirLF, "todolist.appdef"), []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}
	rLF, err := Generate(dirLF, testGenOpts(t))
	if err != nil {
		t.Fatal(err)
	}

	dirCRLF := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirCRLF, "todolist.appdef"), []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}
	rCRLF, err := Generate(dirCRLF, testGenOpts(t))
	if err != nil {
		t.Fatal(err)
	}

	if rLF.Manifest.AppDefHash != rCRLF.Manifest.AppDefHash {
		t.Errorf("CRLF vs LF appdef hash mismatch: LF=%q CRLF=%q", rLF.Manifest.AppDefHash, rCRLF.Manifest.AppDefHash)
	}

	want := fmt.Sprintf("%x", sha256.Sum256([]byte(lf)))
	if rCRLF.Manifest.AppDefHash != want {
		t.Errorf("CRLF hash %q != LF content hash %q", rCRLF.Manifest.AppDefHash, want)
	}
}

func TestSmokeWorkflow(t *testing.T) {
	in := "same input"
	h1, h2 := contentHash(in), contentHash(in)
	if h1 != h2 {
		t.Errorf("same input hashed differently: %q vs %q", h1, h2)
	}
	if h1 == "" {
		t.Error("contentHash returned empty string")
	}

	diff := contentHash("different input")
	if diff == h1 {
		t.Errorf("different input hashed the same: %q", diff)
	}
}

// TestGenerateOrphanHandlerWarning reproduces the round-6 external TOTP run:
// generate with a ping appdef, then rewrite .appdef without ping and
// regenerate. handlers.go is append-only, so handlePing survives while
// schemas_gen.go drops PingRequest/PingResponse — the build breaks with no
// pointer to the cause. Generate must surface an explicit orphan warning,
// and the go.mod module line must upgrade off the template placeholder.
func TestGenerateOrphanHandlerWarning(t *testing.T) {
	dir := t.TempDir()
	appdefPath := filepath.Join(dir, FileAppDef)

	if err := os.WriteFile(appdefPath, []byte(defaultAppdefContent), 0o644); err != nil {
		t.Fatal(err)
	}
	r1, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	for _, w := range r1.Warnings {
		if strings.Contains(w, "handlePing") {
			t.Errorf("declared handler warned as orphan on first generate: %q", w)
		}
	}

	if err := os.WriteFile(appdefPath, []byte(testTodolistAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	r2, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	found := false
	for _, w := range r2.Warnings {
		if strings.Contains(w, "handlePing") && strings.Contains(w, "orphan handler") {
			found = true
		}
	}
	if !found {
		t.Errorf("regenerate missing orphan-handler warning for handlePing: %v", r2.Warnings)
	}

	goMod, err := os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "module app.todolist") {
		t.Errorf("go.mod module line not upgraded off the template placeholder:\n%s", goMod)
	}
}

// TestEnsureGoModContentTemplatePlaceholderUpgrade: a go.mod still carrying
// the template placeholder module name is upgraded to the real app id, while
// an agent-authored module name is preserved.
func TestEnsureGoModContentTemplatePlaceholderUpgrade(t *testing.T) {
	dir := t.TempDir()
	goModPath := filepath.Join(dir, FileGoMod)

	placeholder := "module app.default\n\ngo 1.25.0\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n"
	if err := os.WriteFile(goModPath, []byte(placeholder), 0o644); err != nil {
		t.Fatal(err)
	}
	content, modified, err := ensureGoModContent(dir, goModPath, "com.sporecloud.totp", "1.25.0", "../sdk", "../spore")
	if err != nil {
		t.Fatalf("ensureGoModContent: %v", err)
	}
	if !modified {
		t.Fatal("placeholder module name was not rewritten")
	}
	if got := strings.Count(content, "module"); got != 1 {
		t.Fatalf("module directive count = %d, want 1:\n%s", got, content)
	}
	if !strings.Contains(content, "module com.sporecloud.totp") {
		t.Fatalf("module name not upgraded:\n%s", content)
	}

	authored := "module com.example.authored\n\ngo 1.25.0\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n\nreplace github.com/qomos-w/sporemind-plugin-sdk => ../sdk\n\nreplace github.com/qomos-w/spore => ../spore\n"
	if err := os.WriteFile(goModPath, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	content, modified, err = ensureGoModContent(dir, goModPath, "com.sporecloud.totp", "1.25.0", "../sdk", "../spore")
	if err != nil {
		t.Fatalf("ensureGoModContent (authored): %v", err)
	}
	if modified {
		t.Fatalf("agent-authored module name was rewritten:\n%s", content)
	}
	if !strings.Contains(content, "module com.example.authored") {
		t.Fatalf("agent-authored module name not preserved:\n%s", content)
	}
}

// --- Callable timeout integration ---

const testTimeoutAppdef = `app Timeouts {
    id: "app.timeouts"
    name: "Timeouts"
    version: "0.1.0"
    namespace: "timeouts"

    callable with_timeout {
        timeout: "300s"
    }
    callable with_timeout_ms {
        timeout_ms: 120000
    }
    callable no_timeout {
    }
}
`

func TestGenerateCallableTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "timeouts.appdef"), []byte(testTimeoutAppdef), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mainData, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	mainContent := string(mainData)
	if !strings.Contains(mainContent, "TimeoutMs: 300000") {
		t.Errorf("main.gen.go missing TimeoutMs: 300000:\n%s", mainContent)
	}
	if !strings.Contains(mainContent, "TimeoutMs: 120000") {
		t.Errorf("main.gen.go missing TimeoutMs: 120000:\n%s", mainContent)
	}
	if !strings.Contains(mainContent, "TimeoutMs: 0") {
		t.Errorf("main.gen.go missing TimeoutMs: 0 for undeclared callable:\n%s", mainContent)
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("manifest JSON parse: %v", err)
	}
	if len(manifest.Callables) != 3 {
		t.Fatalf("expected 3 callables, got %d", len(manifest.Callables))
	}
	want := map[string]int64{
		"with_timeout":    300000,
		"with_timeout_ms": 120000,
		"no_timeout":      0,
	}
	got := make(map[string]int64, len(manifest.Callables))
	for _, c := range manifest.Callables {
		got[c.ID] = c.TimeoutMs
	}
	for id, ms := range want {
		if got[id] != ms {
			t.Errorf("callable %q TimeoutMs = %d, want %d", id, got[id], ms)
		}
	}
}

const testStreamingAppdef = `app StreamApp {
    id: "app.streamapp"
    name: "StreamApp"
    version: "0.1.0"
    namespace: streamapp

    struct Chunk {
        text: string
    }
    callable gen_text {
        request: Chunk
        response: Chunk
        streaming: true
    }
    callable ping {
        response: Chunk
    }
}
`

// TestGenerateStreamingCallable pins the streaming codegen path: a callable
// declared streaming: true registers via RegisterCallableStream, gets a
// HandlerStream stub in handlers.go, carries Streaming in the SDK Callable
// literal and the manifest, and emits an onChunk wrapper in client.gen.ts.
func TestGenerateStreamingCallable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.appdef"), []byte(testStreamingAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mainData, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	mainContent := string(mainData)
	for _, want := range []string{
		`Streaming: true, TimeoutMs`,
		`ctx.RegisterCallableStream("gen_text", handleGenText)`,
		`ctx.RegisterCallable("ping", handlePing)`,
	} {
		if !strings.Contains(mainContent, want) {
			t.Errorf("main.gen.go missing %q:\n%s", want, mainContent)
		}
	}

	handlersData, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	handlersContent := string(handlersData)
	if !strings.Contains(handlersContent, "func handleGenText(req sdk.Request, emit func(sdk.Response) error) (sdk.Response, error)") {
		t.Errorf("handlers.go missing streaming stub signature:\n%s", handlersContent)
	}
	if !strings.Contains(handlersContent, "func handlePing(req sdk.Request) (sdk.Response, error)") {
		t.Errorf("handlers.go missing unary stub signature:\n%s", handlersContent)
	}

	tsData, err := os.ReadFile(filepath.Join(dir, FileClientGenTS))
	if err != nil {
		t.Fatal(err)
	}
	tsContent := string(tsData)
	if !strings.Contains(tsContent, "export function genText(req: Chunk, onChunk: (c: Chunk) => void): Promise<Chunk>") {
		t.Errorf("client.gen.ts missing streaming wrapper:\n%s", tsContent)
	}
	if !strings.Contains(tsContent, "invokeStream<Chunk, Chunk>") {
		t.Errorf("client.gen.ts missing invokeStream call:\n%s", tsContent)
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("manifest JSON parse: %v", err)
	}
	streaming := false
	for _, c := range manifest.Callables {
		if c.ID == "gen_text" {
			streaming = c.Streaming
		}
	}
	if !streaming {
		t.Errorf("manifest: gen_text.Streaming = false, want true")
	}
}

const testEventAppdef = `app EventApp {
    id: "app.eventapp"
    name: "EventApp"
    version: "0.1.0"
    namespace: eventapp

    struct TodoChanged {
        done: bool
    }
    event todo_changed { payload: TodoChanged }
    event pinged { }
}
`

// TestGenerateEventEmitAndSubscribe pins the appdef event emit/on codegen: a
// declared event derives the app.emit capability into the SDK manifest
// literal and the app manifest JSON, gets a typed EmitXxx wrapper in
// main.gen.go, and an onXxx subscription wrapper in client.gen.ts.
func TestGenerateEventEmitAndSubscribe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "event.appdef"), []byte(testEventAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mainData, err := os.ReadFile(filepath.Join(dir, FileMainGenGo))
	if err != nil {
		t.Fatal(err)
	}
	mainContent := string(mainData)
	for _, want := range []string{
		`func EmitTodoChanged(payload TodoChanged) error {`,
		`return sdk.EmitEvent("todo_changed", payload)`,
		`func EmitPinged(payload struct{}) error {`,
		`return sdk.EmitEvent("pinged", nil)`,
	} {
		if !strings.Contains(mainContent, want) {
			t.Errorf("main.gen.go missing %q:\n%s", want, mainContent)
		}
	}
	if !strings.Contains(mainContent, `"app.emit"`) {
		t.Errorf("main.gen.go missing derived app.emit permission:\n%s", mainContent)
	}

	tsData, err := os.ReadFile(filepath.Join(dir, FileClientGenTS))
	if err != nil {
		t.Fatal(err)
	}
	tsContent := string(tsData)
	for _, want := range []string{
		`function subscribeEvent(eventKind: string, cb: EventHandler): () => void`,
		`export function onTodoChanged(cb: (p: TodoChanged) => void): () => void`,
		`return subscribeEvent("todo_changed", (raw) => cb(raw as TodoChanged));`,
		`export function onPinged(cb: () => void): () => void`,
	} {
		if !strings.Contains(tsContent, want) {
			t.Errorf("client.gen.ts missing %q:\n%s", want, tsContent)
		}
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("manifest JSON parse: %v", err)
	}
	hasEmit := false
	for _, p := range manifest.Permissions {
		if p == appbinding.CapAppEmit {
			hasEmit = true
		}
	}
	if !hasEmit {
		t.Errorf("manifest permissions missing derived %q: %v", appbinding.CapAppEmit, manifest.Permissions)
	}
	found := false
	for _, ev := range manifest.Events {
		if ev.ID == "todo_changed" && ev.PayloadSchema == "TodoChanged" {
			found = true
		}
	}
	if !found {
		t.Errorf("manifest events missing todo_changed/TodoChanged: %+v", manifest.Events)
	}
}

// TestGenerateManifestExposeWatch verifies that the optional callable expose
// and watch declarations flow into the generated AppCallableDescriptor, and
// that the generated main.gen.go carries them on the sdk.Callable literal.
func TestGenerateManifestExposeWatch(t *testing.T) {
	const src = `app Surfaces {
    id:          "app.surfaces"
    name:        "Surfaces"
    version:     "0.1.0"
    namespace:   "surfaces"

    struct Ping {
        ok: bool
    }

    event item_changed {
        payload: Ping
    }

    callable panel_only {
        request:  Ping
        response: Ping
        expose:   "frontend"
        watch:    ["item_changed"]
    }
    callable agent_only {
        request:  Ping
        response: Ping
        expose:   "agent"
    }
    callable default_expose {
        request:  Ping
        response: Ping
    }
}`
	app, diags, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected parse diagnostics: %v", diags)
	}

	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}

	byID := make(map[string]gen.AppCallableDescriptor, len(manifest.Callables))
	for _, c := range manifest.Callables {
		byID[c.ID] = c
	}
	if got := byID["panel_only"].Expose; got != "frontend" {
		t.Errorf("panel_only Expose = %q, want frontend", got)
	}
	if got := byID["panel_only"].Watch; len(got) != 1 || got[0] != "item_changed" {
		t.Errorf("panel_only Watch = %v, want [item_changed]", got)
	}
	if got := byID["agent_only"].Expose; got != "agent" {
		t.Errorf("agent_only Expose = %q, want agent", got)
	}
	// The undeclared default stays empty (consumers treat empty as "both");
	// it must NOT be normalized in the manifest.
	if got := byID["default_expose"].Expose; got != "" {
		t.Errorf("default_expose Expose = %q, want empty (default both)", got)
	}
	if got := byID["default_expose"].Watch; got != nil {
		t.Errorf("default_expose Watch = %v, want nil", got)
	}

	// main.gen.go must carry the values on the sdk.Callable literal so the
	// runtime PluginManifest() reports them.
	mainSrc := generateMainGo(app, "hash")
	if !strings.Contains(mainSrc, `Expose: "frontend", Watch: []string{"item_changed"}`) {
		t.Errorf("main.gen.go missing panel_only expose/watch literal:\n%s", mainSrc)
	}
	if !strings.Contains(mainSrc, `Expose: "agent", Watch: nil`) {
		t.Errorf("main.gen.go missing agent_only expose literal:\n%s", mainSrc)
	}
	if !strings.Contains(mainSrc, `Expose: "", Watch: nil`) {
		t.Errorf("main.gen.go missing default_expose empty literal:\n%s", mainSrc)
	}
}

// --- Direct-HTTP data path (T3): server.gen.go, stub improvements, ---
// --- HTTP client.gen.ts ---

// testHTTPAppdef exercises every T3 codegen path in one fixture: echo stubs,
// mutate+watch EmitEvent stubs, array-alias zero responses, void responses,
// streaming callables, and all three expose variants.
const testHTTPAppdef = `app HTTPSurfaces {
    id:        "app.httpsurfaces"
    name:      "HTTPSurfaces"
    version:   "0.1.0"
    namespace: httpsurfaces

    struct Item {
        Id:    string
        Title: string
    }
    struct GetItemRequest {
        Id: string
    }
    struct GetItemResponse {
        Id:    string
        Title: string
        Count: int
    }
    struct AddItemRequest {
        Title: string
    }
    struct AddItemResponse {
        Item: Item
    }
    type ItemList = array<Item>

    event item_changed {
        payload: Item
    }

    callable get_item {
        request:  GetItemRequest
        response: GetItemResponse
        effect:   "read"
        watch:    ["item_changed"]
    }
    callable list_items {
        response: ItemList
        effect:   "read"
    }
    callable add_item {
        request:  AddItemRequest
        response: AddItemResponse
        effect:   "mutate"
        watch:    ["item_changed"]
    }
    callable agent_task {
        request:  GetItemRequest
        response: GetItemResponse
        effect:   "read"
        expose:   "agent"
    }
    callable panel_job {
        request:  GetItemRequest
        response: GetItemResponse
        effect:   "read"
        expose:   "frontend"
    }
    callable gen_text {
        request:   GetItemRequest
        response:  GetItemResponse
        effect:    "read"
        streaming: true
    }
    callable ping {
        effect: "none"
    }

    entrypoint view main {
        title: "HTTP Surfaces"
        route: "/"
    }
}
`

// TestGenerateServerGenGoHTTPRoutes pins the server.gen.go artifact: one
// sdk.RegisterHTTPHandler per frontend-exposed callable (expose: agent is
// excluded), unary and streaming adapter shapes, and an empty-init file when
// nothing is frontend-exposed.
func TestGenerateServerGenGoHTTPRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http.appdef"), []byte(testHTTPAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// server.gen.go must be generated and write-protected.
	foundProtected := false
	for _, f := range result.ProtectedFiles {
		if f == FileServerGenGo {
			foundProtected = true
		}
	}
	if !foundProtected {
		t.Errorf("server.gen.go missing from ProtectedFiles: %v", result.ProtectedFiles)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileServerGenGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"Code generated from appdef. DO NOT EDIT",
		"package main",
		`import (
	"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)`,
		"func init() {",
		`sdk.RegisterHTTPHandler("get_item", func(payload json.RawMessage) (any, error) {`,
		`resp, err := handleGetItem(sdk.Request{Payload: payload})`,
		`sdk.RegisterHTTPHandler("gen_text", func(payload json.RawMessage) (any, error) {`,
		`resp, err := handleGenText(sdk.Request{Payload: payload}, func(sdk.Response) error { return nil })`,
		`sdk.RegisterHTTPHandler("panel_job", func(payload json.RawMessage) (any, error) {`,
		"return resp.Payload, nil",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("server.gen.go missing %q:\n%s", want, content)
		}
	}

	// expose: "agent" must not be served over HTTP.
	if strings.Contains(content, "agent_task") {
		t.Errorf("server.gen.go must not register expose:agent callable agent_task:\n%s", content)
	}

	// An agent-only app gets an explicit empty registration body.
	agentOnly := `app AgentOnly {
    id: "app.agentonly"
    name: "AgentOnly"
    version: "0.1.0"
    namespace: agentonly
    struct P { ok: bool }
    callable hidden {
        request: P
        response: P
        expose:  "agent"
    }
}`
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "agent.appdef"), []byte(agentOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir2, testGenOpts(t)); err != nil {
		t.Fatalf("Generate agent-only app: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(dir2, FileServerGenGo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data2), "No frontend-exposed callables") {
		t.Errorf("agent-only server.gen.go missing empty-set comment:\n%s", data2)
	}
	if strings.Contains(string(data2), "RegisterHTTPHandler(\"hidden\"") {
		t.Errorf("agent-only server.gen.go must not register hidden:\n%s", data2)
	}
}

// TestGenerateHandlersEchoMutateEmit pins the improved stubs: same-named
// request fields echo into the response, unmatched response fields stay zero
// (omitted from the literal), effect:mutate callables with watch events emit
// them via sdk.EmitEvent, and handlers.go carries the encoding/json import
// exactly when a stub decodes the request.
func TestGenerateHandlersEchoMutateEmit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http.appdef"), []byte(testHTTPAppdef), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileHandlersGo))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Echo stub: GetItemResponse.Id matches GetItemRequest.Id (echoed);
	// Title/Count have no request counterpart and must be omitted (zero by
	// omission).
	echoStub := `func handleGetItem(req sdk.Request) (sdk.Response, error) {
	var payload GetItemRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	// Stub: echoes same-named request fields; all other response fields are zero values.
	return sdk.Response{Payload: GetItemResponse{
		Id: payload.Id,
	}}, nil
}`
	if !strings.Contains(content, echoStub) {
		t.Errorf("handlers.go missing echo stub handleGetItem:\n%s", content)
	}

	// Mutate + watch: emits the watched event with a nil payload before the
	// zero-value response.
	if !strings.Contains(content, `sdk.EmitEvent("item_changed", nil)`) {
		t.Errorf("handlers.go missing watched-event emit in handleAddItem:\n%s", content)
	}
	if !strings.Contains(content, "Payload: AddItemResponse{") {
		t.Errorf("handlers.go missing zero-value AddItemResponse in handleAddItem:\n%s", content)
	}

	// Array-alias response: empty composite literal.
	if !strings.Contains(content, "Payload: ItemList{}") {
		t.Errorf("handlers.go missing zero-value ItemList in handleListItems:\n%s", content)
	}

	// Void response.
	if !strings.Contains(content, "return sdk.Response{Payload: nil}, nil") {
		t.Errorf("handlers.go missing void-response stub handlePing:\n%s", content)
	}

	// Streaming stub keeps the emit signature.
	if !strings.Contains(content, "func handleGenText(req sdk.Request, emit func(sdk.Response) error) (sdk.Response, error)") {
		t.Errorf("handlers.go missing streaming stub handleGenText:\n%s", content)
	}

	// No not-implemented placeholders anywhere.
	if strings.Contains(content, "ErrNotImplemented") {
		t.Errorf("handlers.go must not contain ErrNotImplemented:\n%s", content)
	}

	// encoding/json import present (echo stub decodes the request).
	if !strings.Contains(content, `"encoding/json"`) {
		t.Errorf("handlers.go missing encoding/json import:\n%s", content)
	}
}
