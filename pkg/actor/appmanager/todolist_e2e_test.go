package appmanager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/appdef"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// --- todolist.appdef (matches the card specification) ---

const todolistAppDef = `// todolist.appdef
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

    callable list_todos {
        request:  ListTodosRequest
        response: array<TodoItem>
        effect:   "read"
        toolName: "todolist-list"
        service:  "appmanager"
    }
    callable add_todo {
        request:  AddTodoRequest
        response: AddTodoResponse
        effect:   "write"
        toolName: "todolist-add"
        service:  "appmanager"
    }
    callable remove_todo {
        request:  RemoveTodoRequest
        effect:   "mutate"
        toolName: "todolist-remove"
        service:  "appmanager"
    }

    entrypoint view main {
        title: "待办清单"
        route: "/"
    }

    event todo_changed {
        payload: TodoItem
    }

    bundle main {
        title: "待办清单工具"
        description: "增删查待办项"
        tools: [list_todos, add_todo, remove_todo]
    }
}
`

// counter.appdef declares a dependency on todolist, used for the
// dependency-tree integration scenario (step 11).
const counterAppDef = `// counter.appdef
app Counter {
    id:          "app.counter"
    name:        "Counter"
    version:     "0.1.0"
    namespace:   "counter"
    permissions: []

    struct IncrementRequest {
        Amount: int
    }
    struct IncrementResponse {
        Total: int
    }

    callable increment {
        request:  IncrementRequest
        response: IncrementResponse
        effect:   "write"
        toolName: "counter-increment"
        service:  "appmanager"
    }

    entrypoint view main {
        title: "Counter"
        route: "/counter"
    }

    bundle main {
        title: "Counter Tools"
        description: "Increment a counter"
        tools: [increment]
    }

    dependency app.todolist {
        version: "0.1.0"
    }
}
`

// todolistManifestJSON returns the on-disk app.manifest.json for the
// todolist app, derived from the .appdef via codegen.GenerateManifestFromAppDef.
func todolistManifestJSON(t *testing.T) string {
	t.Helper()
	m := todolistManifest(t)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal todolist manifest: %v", err)
	}
	return string(data)
}

func todolistManifest(t *testing.T) gen.AppManifest {
	t.Helper()
	_, diags, err := parseAppDefHelper(t, todolistAppDef)
	if err != nil {
		t.Fatalf("parse todolist appdef: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("todolist appdef diagnostics: %v", diags)
	}
	m := manifestFromAppDef(t, todolistAppDef)
	// AgentBinding is required for invoke authorization.
	m.AgentBinding = &gen.AppAgentBinding{
		Surface: &gen.AgentSurfaceBinding{
			AgentID:    "todolist-agent",
			Entrypoint: "main",
		},
		Capability: &gen.AgentCapabilityBinding{
			Callables: []string{"list_todos", "add_todo", "remove_todo"},
		},
	}
	return m
}

func counterManifestJSON(t *testing.T) string {
	t.Helper()
	m := manifestFromAppDef(t, counterAppDef)
	m.AgentBinding = &gen.AppAgentBinding{
		Surface: &gen.AgentSurfaceBinding{
			AgentID:    "counter-agent",
			Entrypoint: "main",
		},
		Capability: &gen.AgentCapabilityBinding{
			Callables: []string{"increment"},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal counter manifest: %v", err)
	}
	return string(data)
}

// todolistHandlersGo is the filled handlers.go with in-memory todo state.
// All three stubs are implemented; none contain ErrNotImplemented.
const todolistHandlersFilled = `// handlers.go — agent-owned
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

var todos = []map[string]interface{}{
	{"Id": "1", "Title": "First", "Done": false},
}

func handleListTodos(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: todos}, nil
}

func handleAddTodo(req sdk.Request) (sdk.Response, error) {
	item := map[string]interface{}{"Id": "2", "Title": "Second", "Done": false}
	todos = append(todos, item)
	return sdk.Response{Payload: item}, nil
}

func handleRemoveTodo(req sdk.Request) (sdk.Response, error) {
	todos = todos[:0]
	return sdk.Response{}, nil
}
`

// todolistHandlersStub is the unmodified codegen output with all three
// stubs still returning ErrNotImplemented — used for the gate negative test.
const todolistHandlersStub = `// handlers.go — agent-owned
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

// TODO(agent): implement list_todos
func handleListTodos(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}

// TODO(agent): implement add_todo
func handleAddTodo(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}

// TODO(agent): implement remove_todo
func handleRemoveTodo(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, sdk.ErrNotImplemented
}
`

// counterHandlersFilled is the filled handler for the counter app.
const counterHandlersFilled = `package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

var total = 0

func handleIncrement(req sdk.Request) (sdk.Response, error) {
	total++
	return sdk.Response{Payload: map[string]interface{}{"Total": total}}, nil
}
`

// todolistE2EPlannerBuilder holds the file contents and state that the mock
// planner needs to respond to project.read / project.list calls.
type todolistE2EPlannerBuilder struct {
	t                   *testing.T
	calls               *[]string
	manifestJSON        string
	appdefContent       string
	handlersGo          string
	appID               string
	registeredCallables []string
	// loadedPlugins tracks which app IDs have been loaded by artifact_load,
	// so that the counter's dependency on todolist can be validated.
	loadedPlugins map[string]bool
	// pluginCallables tracks per-plugin callable IDs for list_plugins.
	pluginCallables map[string][]string
	// invokePayload is the response payload returned by pluginhost.invoke.
	invokePayload []byte
	// cards is the fake project card store: app-bundle cardID -> raw. It
	// backs project.component_get / wiki_create_card / wiki_edit_card /
	// wiki_delete_card so bundle-declaring apps can be published/revoked.
	cards map[string]string
	// cardsMu guards concurrent card store access.
	cardsMu sync.Mutex
}

func (b *todolistE2EPlannerBuilder) initCards() {
	if b.cards == nil {
		b.cards = make(map[string]string)
	}
}

func (b *todolistE2EPlannerBuilder) build() lifecyclePlanner {
	t := b.t
	if b.pluginCallables == nil {
		b.pluginCallables = make(map[string][]string)
	}
	abi := scaffoldAbi()
	// resolveFile returns the file content from b's current mutable fields,
	// so the planner always reflects the latest test state.
	resolveFile := func(name string) string {
		switch name {
		case "app.manifest.json":
			return b.manifestJSON
		case "todolist.appdef":
			return b.appdefContent
		case "main.gen.go":
			return "package main\n\nfunc main() {}\n"
		case "handlers.go":
			return b.handlersGo
		case "counter.appdef":
			return b.appdefContent
		default:
			return ""
		}
	}
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		*b.calls = append(*b.calls, callID)
		switch callID {
		case "project.component_get":
			req := payload.(gen.ProjectComponentGetReq)
			b.cardsMu.Lock()
			_, ok := b.cards[req.CardID]
			b.cardsMu.Unlock()
			if !ok {
				return gen.ProjectComponentGetResp{}, fmt.Errorf("component card %q not found", req.CardID)
			}
			return gen.ProjectComponentGetResp{Component: gen.ComponentDescriptor{
				Ref:   gen.ComponentRef{CardID: req.CardID, Kind: "bundle", Source: "appmanager"},
				Title: "bundle",
			}}, nil
		case "project.wiki_create_card":
			req := payload.(gen.WikiCreateCardReq)
			b.cardsMu.Lock()
			b.initCards()
			b.cards[req.ID] = req.Raw
			b.cardsMu.Unlock()
			return gen.WikiCreateCardResp{Card: gen.MonoCardListItem{ID: req.ID, Type: "bundle", Source: "appmanager"}}, nil
		case "project.wiki_edit_card":
			req := payload.(gen.WikiEditCardReq)
			b.cardsMu.Lock()
			b.initCards()
			b.cards[req.ID] = req.Raw
			b.cardsMu.Unlock()
			return gen.WikiEditCardResp{Card: gen.MonoCardListItem{ID: req.ID, Type: "bundle", Source: "appmanager"}}, nil
		case "project.wiki_delete_card":
			req := payload.(gen.WikiDeleteCardReq)
			b.cardsMu.Lock()
			delete(b.cards, req.ID)
			b.cardsMu.Unlock()
			return gen.WikiDeleteCardResp{ID: req.ID}, nil
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "todolist", Path: "/test/todolist"}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			name := req.Path[strings.LastIndex(req.Path, "/")+1:]
			content := resolveFile(name)
			if content == "" {
				return gen.FileSystemReadResp{}, nil
			}
			return gen.FileSystemReadResp{Content: content}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			name := req.Path[strings.LastIndex(req.Path, "/")+1:]
			content := resolveFile(name)
			return gen.FileSystemReadBase64Resp{Content: encodeBase64(content)}, nil
		case "project.list":
			req := payload.(gen.FileSystemListReq)
			if req.Depth == 1 {
				// resolveSingleAppDef uses Depth:1 to find the .appdef file.
				return listText(gen.FileEntry{Name: "todolist.appdef", IsDir: false}), nil
			}
			// collectGoSourceFiles uses Depth:-1 to enumerate .go files.
			return listText(
				gen.FileEntry{Name: "main.gen.go", IsDir: false},
				gen.FileEntry{Name: "handlers.go", IsDir: false},
			), nil
		case "project.set_protected_files":
			return gen.SetProtectedFilesResp{Count: 4}, nil
		case "pluginhost.native_build":
			return gen.NativeBuildResp{
				Result:       gen.NativeBuildResult{Success: true, ArtifactPath: "/test/build/plugin-app.so", ArtifactHash: "deadbeef0011"},
				ManifestPath: "/test/todolist/app.manifest.json",
				Abi:          abi,
			}, nil
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			// Dependency check: if manifest declares dependencies, verify
			// they are already loaded.
			for _, dep := range req.Manifest.Dependencies {
				if !b.loadedPlugins[dep.ID] {
					return gen.PluginArtifactLoadResp{}, fmt.Errorf(
						"pluginhost: dependencies not loaded for %q: %s",
						req.Manifest.ID, dep.ID,
					)
				}
			}
			b.loadedPlugins[req.Manifest.ID] = true
			// Record this plugin's callables for list_plugins.
			var cids []string
			for _, c := range req.Manifest.Callables {
				cids = append(cids, c.ID)
			}
			b.pluginCallables[req.Manifest.ID] = cids
			return gen.PluginArtifactLoadResp{
				PluginID:     req.Manifest.ID,
				ArtifactHash: "deadbeef0011",
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.list_plugins":
			// Return all loaded plugins with their per-plugin callables.
			var descs []pluginhostactor.PluginDescriptor
			for pid := range b.loadedPlugins {
				calls := b.pluginCallables[pid]
				if calls == nil {
					calls = b.registeredCallables
				}
				descs = append(descs, pluginhostactor.PluginDescriptor{
					ID: pid, Callables: calls,
				})
			}
			return pluginhostactor.ListPluginsResp{Plugins: descs}, nil
		case "pluginhost.invoke":
			if b.invokePayload != nil {
				return gen.PluginInvokeResp{Payload: b.invokePayload}, nil
			}
			return gen.PluginInvokeResp{Payload: []byte(`{}`)}, nil
		case "pluginhost.artifact_unload":
			req := payload.(gen.PluginArtifactUnloadReq)
			delete(b.loadedPlugins, req.PluginID)
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// todolistE2ECtx builds a FakeCtx wired with the todolist planner.
func todolistE2ECtx(t *testing.T, b *todolistE2EPlannerBuilder) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, true
	}
	planner := b.build()
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func newTodolistE2EActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-todolist-e2e",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

// parseAppDefHelper parses an appdef source string for test setup.
func parseAppDefHelper(t *testing.T, src string) (*appdef.AppDef, []appdef.Diagnostic, error) {
	t.Helper()
	return appdef.ParseFile(src)
}

// --- Step 5: Gate self-check with todolist.appdef ---

func TestTodolistGateSelfCheck_AllPass(t *testing.T) {
	manifest := manifestFromAppDef(t, todolistAppDef)
	hash := hashString(todolistAppDef)

	report := RunAllGates(GateDeps{
		SourceFiles:         map[string]string{"handlers.go": todolistHandlersFilled},
		AppDefContent:       todolistAppDef,
		AppDefHash:          hash,
		Manifest:            manifest,
		GeneratedManifest:   &codegen.GeneratedManifest{AppDefHash: hash},
		BuildResult:         gen.NativeBuildResult{Success: true},
		RegisteredCallables: []string{"list_todos", "add_todo", "remove_todo"},
	})
	if !report.Passed {
		t.Fatalf("expected all gates to pass, got: %+v", report)
	}
	for _, r := range report.Results {
		if !r.Passed {
			t.Errorf("gate %s failed: %v", r.Gate, r.Error)
		}
	}
}

// --- Step 8: Gate negative test — stubs unfilled -> gates fail ---

func TestTodolistGateSelfCheck_StubUnfilledFails(t *testing.T) {
	manifest := manifestFromAppDef(t, todolistAppDef)
	hash := hashString(todolistAppDef)

	report := RunAllGates(GateDeps{
		SourceFiles:         map[string]string{"handlers.go": todolistHandlersStub},
		AppDefContent:       todolistAppDef,
		AppDefHash:          hash,
		Manifest:            manifest,
		GeneratedManifest:   &codegen.GeneratedManifest{AppDefHash: hash},
		BuildResult:         gen.NativeBuildResult{Success: true},
		RegisteredCallables: []string{"list_todos", "add_todo", "remove_todo"},
	})
	if report.Passed {
		t.Fatal("expected gates to fail when stubs are unfilled")
	}
	found := false
	for _, r := range report.Results {
		if r.Gate == GateStubFill && !r.Passed {
			found = true
			if !strings.Contains(r.Error.Detail, "handlers.go") {
				t.Errorf("expected handlers.go in stub_filling detail, got %q", r.Error.Detail)
			}
			if !strings.Contains(r.Error.Detail, "ErrNotImplemented") {
				t.Errorf("expected ErrNotImplemented in stub_filling detail, got %q", r.Error.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("expected stub_filling gate failure, got: %+v", report)
	}
}

// --- Step 6: Register todolist (inline gates pass) ---

func TestTodolistE2ERegistration_AllGatesPass(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 50)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := todolistManifestJSON(t)

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        manifestJSON,
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersFilled,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("expected running state, got %q", regResp.Status.State)
	}
	if regResp.Status.ID != "app.todolist" {
		t.Fatalf("expected app.todolist, got %q", regResp.Status.ID)
	}
	if _, ok := a.Apps["app.todolist"]; !ok {
		t.Fatal("app.todolist not registered after passing gates")
	}

	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.native_build") {
		t.Fatalf("expected native_build in call sequence: %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.artifact_load") {
		t.Fatalf("expected artifact_load in call sequence: %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.list_plugins") {
		t.Fatalf("expected list_plugins (coverage gate) in call sequence: %s", callSeq)
	}
}

// --- Step 8 (lifecycle): Register with unfilled stubs -> must be rejected ---

func TestTodolistE2ERegistration_StubUnfilledRejected(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 51)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := todolistManifestJSON(t)

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        manifestJSON,
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersStub,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err == nil {
		t.Fatal("expected register_project to fail with unfilled stubs")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stub_filling") {
		t.Fatalf("expected stub_filling gate in error, got %q", msg)
	}
	if !strings.Contains(msg, "ErrNotImplemented") {
		t.Fatalf("expected ErrNotImplemented in error, got %q", msg)
	}
	if _, ok := a.Apps["app.todolist"]; ok {
		t.Fatal("failed registration must not leave app in registry")
	}

	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload compensation after gate failure: %s", callSeq)
	}
}

// --- Step 7: Invoke list_todos / add_todo / remove_todo ---

func TestTodolistE2EInvoke(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 52)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := todolistManifestJSON(t)

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        manifestJSON,
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersFilled,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}

	// Invoke list_todos.
	calls = nil
	b.invokePayload = []byte(`[{"Id":"1","Title":"First","Done":false}]`)
	listResp, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:       "app.todolist",
		Callable: "list_todos",
		AgentID:  "todolist-agent",
	})
	if err != nil {
		t.Fatalf("invoke list_todos: %v", err)
	}
	if len(listResp.Payload) == 0 {
		t.Fatal("list_todos returned empty payload")
	}
	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.invoke") {
		t.Fatalf("expected pluginhost.invoke in list_todos sequence: %s", callSeq)
	}

	// Invoke add_todo.
	calls = nil
	b.invokePayload = []byte(`{"Item":{"Id":"2","Title":"Second","Done":false}}`)
	addResp, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:       "app.todolist",
		Callable: "add_todo",
		AgentID:  "todolist-agent",
	})
	if err != nil {
		t.Fatalf("invoke add_todo: %v", err)
	}
	if len(addResp.Payload) == 0 {
		t.Fatal("add_todo returned empty payload")
	}

	// Invoke remove_todo — verify void response (empty payload).
	calls = nil
	b.invokePayload = nil
	_, err = a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:       "app.todolist",
		Callable: "remove_todo",
		AgentID:  "todolist-agent",
	})
	if err != nil {
		t.Fatalf("invoke remove_todo: %v", err)
	}
	// remove_todo has no response type → void. The manifest confirms this.
	manifest := a.Apps["app.todolist"]
	for _, c := range manifest.Callables {
		if c.ID == "remove_todo" {
			if c.ResponseSchema != "void" {
				t.Errorf("remove_todo response schema = %q, want void", c.ResponseSchema)
			}
		}
	}
}

// --- Step 9: Unregister removes app from registry and calls artifact_unload ---

func TestTodolistE2EUnregister(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 53)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := todolistManifestJSON(t)

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        manifestJSON,
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersFilled,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}

	// Unregister.
	calls = nil
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: "app.todolist"}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := a.Apps["app.todolist"]; ok {
		t.Fatal("app should be removed from Apps after unregister")
	}
	if _, ok := a.Records["app.todolist"]; ok {
		t.Fatal("record should be removed after unregister")
	}
	if _, ok := a.children["app.todolist"]; ok {
		t.Fatal("child routing should be removed after unregister")
	}
	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload in unregister sequence: %s", callSeq)
	}
}

// --- Step 11: Dependency tree scenario ---

// TestTodolistDependencyTree_ForwardOrder verifies that registering todolist
// first, then counter (which declares dependency todolist), succeeds because
// the dependency is already loaded.
func TestTodolistDependencyTree_ForwardOrder(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 54)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        todolistManifestJSON(t),
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersFilled,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	// Register todolist first.
	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register todolist: %v", err)
	}
	if !b.loadedPlugins["app.todolist"] {
		t.Fatal("todolist should be loaded after registration")
	}

	// Now register counter (which depends on todolist).
	// We reuse the same planner but swap the file contents for counter.
	b.manifestJSON = counterManifestJSON(t)
	b.appdefContent = counterAppDef
	b.handlersGo = counterHandlersFilled
	b.appID = "app.counter"
	b.registeredCallables = []string{"increment"}

	counterProjectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 55)
	if err != nil {
		t.Fatalf("create counter project CID: %v", err)
	}
	counterProjectID := counterProjectCID.String()

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: counterProjectID})
	if err != nil {
		t.Fatalf("register counter (dependency satisfied): %v", err)
	}
	if _, ok := a.Apps["app.counter"]; !ok {
		t.Fatal("app.counter should be registered after todolist")
	}
	if !b.loadedPlugins["app.counter"] {
		t.Fatal("counter should be loaded after registration")
	}
}

// TestTodolistDependencyTree_ReverseOrder verifies that registering counter
// first (which depends on todolist) is rejected with a structured
// missingDependencies error.
func TestTodolistDependencyTree_ReverseOrder(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 56)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        counterManifestJSON(t),
		appdefContent:       counterAppDef,
		handlersGo:          counterHandlersFilled,
		appID:               "app.counter",
		registeredCallables: []string{"increment"},
		loadedPlugins:       map[string]bool{}, // todolist NOT loaded
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err == nil {
		t.Fatal("expected register counter to fail when todolist is not loaded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "dependencies not loaded") {
		t.Fatalf("expected missingDependencies error, got %q", msg)
	}
	if !strings.Contains(msg, "app.todolist") {
		t.Fatalf("expected 'app.todolist' in error, got %q", msg)
	}
	if _, ok := a.Apps["app.counter"]; ok {
		t.Fatal("failed registration must not leave counter in registry")
	}
}

// TestTodolistDependencyTree_UnloadDependent verifies that unregistering
// todolist while counter depends on it does not crash; the current semantics
// are observed and recorded (counter is not automatically unloaded).
func TestTodolistDependencyTree_UnloadDependent(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 57)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        todolistManifestJSON(t),
		appdefContent:       todolistAppDef,
		handlersGo:          todolistHandlersFilled,
		appID:               "app.todolist",
		registeredCallables: []string{"list_todos", "add_todo", "remove_todo"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	// Register todolist.
	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register todolist: %v", err)
	}

	// Register counter.
	b.manifestJSON = counterManifestJSON(t)
	b.appdefContent = counterAppDef
	b.handlersGo = counterHandlersFilled
	b.appID = "app.counter"
	b.registeredCallables = []string{"increment"}

	counterProjectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 58)
	if err != nil {
		t.Fatalf("create counter project CID: %v", err)
	}
	counterProjectID := counterProjectCID.String()

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: counterProjectID})
	if err != nil {
		t.Fatalf("register counter: %v", err)
	}

	// Unregister todolist — current semantics: counter is NOT automatically
	// unloaded; it remains in registry but its dependency is now missing.
	// This test records the current behavior, not a desired mechanism.
	calls = nil
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: "app.todolist"}); err != nil {
		t.Fatalf("unregister todolist: %v", err)
	}
	if _, ok := a.Apps["app.todolist"]; ok {
		t.Fatal("todolist should be removed after unregister")
	}
	// Counter remains in registry — no cascade unload.
	if _, ok := a.Apps["app.counter"]; !ok {
		t.Fatal("counter should still be in registry after todolist unload (current semantics: no cascade)")
	}
	// Record: todolist is no longer loaded in the mock.
	if b.loadedPlugins["app.todolist"] {
		t.Error("todolist should be marked as unloaded in the mock")
	}
}
