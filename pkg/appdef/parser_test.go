package appdef

import (
	"strings"
	"testing"
)

// --- Valid Example: todolist.appdef ---

const validTodolistAppdef = `// todolist.appdef — valid test
app TodoList {
    id:          "app.todolist"
    name:        "TodoList"
    version:     "0.1.0"
    namespace:   "todolist"
    permissions: ["fs.read", "fs.write"]

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
        permission: "public"
    }

    listen app_lifecycle { }

    bundle main {
        title: "待办清单工具"
        description: "增删查待办项"
        icon: "list-checks"
        color: "#9333ea"
        tools: [list_todos, add_todo, remove_todo]
    }
}
`

func TestValidTodolistAppdef(t *testing.T) {

	app, diags, err := ParseFile(validTodolistAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// No parse diagnostics expected.
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("parse diagnostic: %s", d)
		}
	}

	// App metadata.
	if app.AppMeta.ID != "app.todolist" {
		t.Errorf("app meta id = %q, want %q", app.AppMeta.ID, "app.todolist")
	}
	if app.AppMeta.Name != "TodoList" {
		t.Errorf("app meta name = %q, want %q", app.AppMeta.Name, "TodoList")
	}
	if app.AppMeta.Version != "0.1.0" {
		t.Errorf("app meta version = %q, want %q", app.AppMeta.Version, "0.1.0")
	}
	if app.AppMeta.Namespace != "todolist" {
		t.Errorf("app meta namespace = %q, want %q", app.AppMeta.Namespace, "todolist")
	}
	if len(app.AppMeta.Permissions) != 2 || app.AppMeta.Permissions[0] != "fs.read" || app.AppMeta.Permissions[1] != "fs.write" {
		t.Errorf("app meta permissions = %v, want [fs.read fs.write]", app.AppMeta.Permissions)
	}

	// Structs.
	if len(app.Structs) != 5 {
		t.Fatalf("expected 5 structs, got %d", len(app.Structs))
	}
	structNames := []string{"TodoItem", "ListTodosRequest", "AddTodoRequest", "AddTodoResponse", "RemoveTodoRequest"}
	for i, name := range structNames {
		if app.Structs[i].Name != name {
			t.Errorf("struct[%d].Name = %q, want %q", i, app.Structs[i].Name, name)
		}
	}

	// TodoItem fields.
	todoItem := app.Structs[0]
	if len(todoItem.Fields) != 3 {
		t.Fatalf("TodoItem fields = %d, want 3", len(todoItem.Fields))
	}
	if todoItem.Fields[0].Name != "Id" || string(todoItem.Fields[0].Type.Kind) != "scalar" || todoItem.Fields[0].Type.Name != "string" {
		t.Errorf("TodoItem.Id field wrong: %+v", todoItem.Fields[0])
	}
	if !todoItem.Fields[2].Optional {
		t.Error("TodoItem.Done should be optional")
	}

	// Callables.
	if len(app.Callables) != 3 {
		t.Fatalf("expected 3 callables, got %d", len(app.Callables))
	}

	cd0 := app.Callables[0]
	if cd0.ID != "list_todos" {
		t.Errorf("callable[0].ID = %q, want %q", cd0.ID, "list_todos")
	}
	if cd0.Request != "ListTodosRequest" {
		t.Errorf("callable[0].Request = %q, want %q", cd0.Request, "ListTodosRequest")
	}
	if cd0.Response != "array<TodoItem>" {
		t.Errorf("callable[0].Response = %q, want %q", cd0.Response, "array<TodoItem>")
	}
	if cd0.Effect != "read" {
		t.Errorf("callable[0].Effect = %q, want %q", cd0.Effect, "read")
	}
	if cd0.ToolName != "todolist-list" {
		t.Errorf("callable[0].ToolName = %q, want %q", cd0.ToolName, "todolist-list")
	}

	// remove_todo has no response (void).
	cd2 := app.Callables[2]
	if cd2.ID != "remove_todo" {
		t.Errorf("callable[2].ID = %q, want %q", cd2.ID, "remove_todo")
	}
	if cd2.Response != "" {
		t.Errorf("callable[2].Response = %q, want empty (void)", cd2.Response)
	}

	// Entrypoints.
	if len(app.Entrypoints) != 1 {
		t.Fatalf("expected 1 entrypoint, got %d", len(app.Entrypoints))
	}
	ep := app.Entrypoints[0]
	if ep.Kind != "view" || ep.ID != "main" || ep.Title != "待办清单" || ep.Route != "/" {
		t.Errorf("entrypoint wrong: %+v", ep)
	}

	// Events.
	if len(app.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(app.Events))
	}
	ev := app.Events[0]
	if ev.ID != "todo_changed" || ev.Payload != "TodoItem" || ev.Permission != "public" {
		t.Errorf("event wrong: %+v", ev)
	}

	// Listens.
	if len(app.Listens) != 1 {
		t.Fatalf("expected 1 listen, got %d", len(app.Listens))
	}
	if app.Listens[0].Kind != "app_lifecycle" {
		t.Errorf("listen kind = %q, want app_lifecycle", app.Listens[0].Kind)
	}

	// Bundles.
	if len(app.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(app.Bundles))
	}
	bd := app.Bundles[0]
	if bd.Title != "待办清单工具" {
		t.Errorf("bundle title = %q, want %q", bd.Title, "待办清单工具")
	}
	if bd.Icon != "list-checks" {
		t.Errorf("bundle icon = %q, want %q", bd.Icon, "list-checks")
	}
	if bd.Color != "#9333ea" {
		t.Errorf("bundle color = %q, want %q", bd.Color, "#9333ea")
	}
	if len(bd.Tools) != 3 {
		t.Errorf("bundle tools count = %d, want 3", len(bd.Tools))
	}
	expectedTools := []string{"list_todos", "add_todo", "remove_todo"}
	for i, want := range expectedTools {
		if bd.Tools[i] != want {
			t.Errorf("bundle tools[%d] = %q, want %q", i, bd.Tools[i], want)
		}
	}
}

// --- Validation: no errors on valid input ---

func TestValidateValidInput(t *testing.T) {
	app, _, err := ParseFile(validTodolistAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
	}
}

// --- Invalid: dangling struct reference in callable request ---

const danglingRequestRef = `app Test {
    struct Foo {
        X: string
    }

    callable my_func {
        request:  NonExistent
        response: Foo
        effect:   "read"
        toolName: "test-func"
        service:  "test"
    }
}
`

func TestValidateDanglingRequestRef(t *testing.T) {
	app, _, err := ParseFile(danglingRequestRef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "NonExistent") && strings.Contains(d.Message, "undefined struct") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about dangling NonExistent reference, got: %v", diags)
	}
}

// --- Invalid: dangling struct reference in callable response ---

const danglingResponseRef = `app Test {
    struct Bar {
        Y: int
    }

    callable my_func {
        request:  Bar
        response: NoSuchType
        effect:   "read"
        toolName: "test-func"
        service:  "test"
    }
}
`

func TestValidateDanglingResponseRef(t *testing.T) {
	app, _, err := ParseFile(danglingResponseRef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "NoSuchType") && strings.Contains(d.Message, "response") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about dangling response reference, got: %v", diags)
	}
}

// --- Invalid: illegal toolName ---

const illegalToolName = `app Test {
    struct Req {
        X: string
    }

    callable my_func {
        request:  Req
        effect:   "read"
        toolName: "INVALID TOOL NAME!"
        service:  "test"
    }
}
`

func TestValidateIllegalToolName(t *testing.T) {
	app, _, err := ParseFile(illegalToolName)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "invalid toolName") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about invalid toolName, got: %v", diags)
	}
}

// --- Invalid: dangling bundle tool reference ---

const danglingBundleTool = `app Test {
    struct Req {
        X: string
    }

    callable real_func {
        request:  Req
        effect:   "read"
        toolName: "real-func"
        service:  "test"
    }

    bundle my_bundle {
        title: "Test Bundle"
        tools: [real_func, nonexistent_func]
    }
}
`

func TestValidateDanglingBundleTool(t *testing.T) {
	app, _, err := ParseFile(danglingBundleTool)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "nonexistent_func") && strings.Contains(d.Message, "undefined callable") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about dangling bundle tool reference, got: %v", diags)
	}
}

// --- Invalid: duplicate callable IDs ---

const duplicateCallableIDs = `app Test {
    struct Req {
        X: string
    }

    callable my_func {
        request:  Req
        effect:   "read"
        toolName: "f1"
        service:  "test"
    }

    callable my_func {
        request:  Req
        effect:   "write"
        toolName: "f2"
        service:  "test"
    }
}
`

func TestValidateDuplicateCallableIDs(t *testing.T) {
	app, _, err := ParseFile(duplicateCallableIDs)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "duplicate callable id") && strings.Contains(d.Message, "my_func") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about duplicate callable ID, got: %v", diags)
	}
}

// --- Invalid: dangling event payload reference ---

const danglingEventPayload = `app Test {
    struct Item {
        Id: string
    }

    event my_event {
        payload: NonExistentType
        permission: "public"
    }
}
`

func TestValidateDanglingEventPayload(t *testing.T) {
	app, _, err := ParseFile(danglingEventPayload)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics, got none")
	}

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "NonExistentType") && strings.Contains(d.Message, "payload") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic about dangling event payload, got: %v", diags)
	}
}

// --- Invalid: missing app block ---

func TestParseMissingAppBlock(t *testing.T) {
	_, _, err := ParseFile("struct Foo { X: string }")
	if err == nil {
		t.Fatal("expected error for missing app block, got nil")
	}
}

// --- Invalid: empty file ---

func TestParseEmptyFile(t *testing.T) {
	_, _, err := ParseFile("")
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

// --- Invalid: malformed app block ---

func TestParseMalformedAppBlock(t *testing.T) {
	_, _, err := ParseFile("app Test")
	if err == nil {
		t.Fatal("expected error for malformed app block, got nil")
	}
}

// --- Test with map<K,V> type ---

const appdefWithMap = `app Test {
    struct Config {
        Settings: map<string, string>
    }

    callable get_config {
        request:  Config
        response: Config
        effect:   "read"
        toolName: "get-config"
        service:  "test"
    }
}
`

func TestParseMapType(t *testing.T) {
	app, _, err := ParseFile(appdefWithMap)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(app.Structs) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(app.Structs))
	}

	cfg := app.Structs[0]
	if len(cfg.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(cfg.Fields))
	}

	field := cfg.Fields[0]
	if field.Name != "Settings" {
		t.Errorf("field name = %q, want %q", field.Name, "Settings")
	}
	if field.Type.Kind != "map" {
		t.Errorf("field type kind = %q, want %q", field.Type.Kind, "map")
	}
}

// --- Test type alias ---

const appdefWithTypeAlias = `app Test {
    type ID = string

    struct Item {
        Id: ID
        Name: string
    }

    callable get_item {
        request:  Item
        response: Item
        effect:   "read"
        toolName: "get-item"
        service:  "test"
    }
}
`

func TestParseTypeAlias(t *testing.T) {
	app, _, err := ParseFile(appdefWithTypeAlias)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(app.TypeAliases) != 1 {
		t.Fatalf("expected 1 type alias, got %d", len(app.TypeAliases))
	}
	alias := app.TypeAliases[0]
	if alias.Name != "ID" || alias.Target != "string" {
		t.Errorf("type alias = %+v, want Name=ID Target=string", alias)
	}
	if alias.Line == 0 {
		t.Errorf("type alias should have a line number")
	}

	// Check that Item struct is parsed.
	found := false
	for _, s := range app.Structs {
		if s.Name == "Item" {
			found = true
			if len(s.Fields) != 2 {
				t.Errorf("Item fields = %d, want 2", len(s.Fields))
			}
			break
		}
	}
	if !found {
		t.Error("Item struct not found in parsed structs")
	}

	if !strings.Contains(app.SchemaSource, "type ID = string") {
		t.Errorf("SchemaSource should include type alias text, got:\n%s", app.SchemaSource)
	}
}

// --- Test toolName validation helper ---

const appdefWithAliasInCallable = `app Test {
    id:        "app.test"
    name:      "Test"
    version:   "0.1.0"
    namespace: "test"

    type UserID = string

    struct User {
        Id: UserID
        Name: string
    }

    callable get_user {
        request:  UserID
        response: User
        effect:   "read"
        toolName: "get-user"
        service:  "test"
    }
}
`

func TestAliasInCallableSignature(t *testing.T) {
	app, _, err := ParseFile(appdefWithAliasInCallable)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(app.TypeAliases) != 1 || app.TypeAliases[0].Name != "UserID" {
		t.Fatalf("expected UserID alias, got %v", app.TypeAliases)
	}

	if len(app.Callables) != 1 {
		t.Fatalf("expected 1 callable, got %d", len(app.Callables))
	}
	c := app.Callables[0]
	if c.Request != "UserID" || c.Response != "User" {
		t.Errorf("callable signature wrong: request=%q response=%q", c.Request, c.Response)
	}

	diags := Validate(app)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
	}
}

func TestUnterminatedBlock(t *testing.T) {
	const src = `app Test {
    struct Req { X: string }

    callable broken {
        request:  Req
        effect:   "read"
        toolName: "broken-tool"
        service:  "test"
`
	_, diags, err := ParseFile(src)
	if err == nil {
		t.Fatal("expected error for unterminated app block")
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostic with line number for unterminated block, got none")
	}
	found := false
	for _, d := range diags {
		if d.Line > 0 && strings.Contains(d.Message, "unterminated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected diagnostic with line number for unterminated block, got %v", diags)
	}
}

func TestMalformedCallableBlock(t *testing.T) {
	const src = `app Test {
    callable {
        effect: "read"
        toolName: "x"
        service: "s"
    }
}
`
	_, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile returned unexpected error: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for malformed callable block, got none")
	}
	found := false
	for _, d := range diags {
		if d.Line > 0 && strings.Contains(d.Message, "callable") && strings.Contains(d.Message, "expected id") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected diagnostic for malformed callable block, got %v", diags)
	}
}

func TestToolNameValidation(t *testing.T) {

	tests := []struct {
		name string
		want bool
	}{
		{"todolist-list", true},
		{"my_tool", true},
		{"tool123", true},
		{"a-b_c", true},
		{"INVALID TOOL NAME!", false},
		{"has spaces", false},
		{"has@special", false},
		{"", false},
		{"valid-name_123", true},
	}

	for _, tt := range tests {
		got := ValidateToolName(tt.name)
		if got != tt.want {
			t.Errorf("ValidateToolName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// --- Test ExtractTypeReferences ---

func TestExtractTypeReferences(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"TodoItem", []string{"TodoItem"}},
		{"array<TodoItem>", []string{"TodoItem"}},
		{"map<string, TodoItem>", []string{"TodoItem"}},
		{"optional TodoItem", []string{"TodoItem"}},
		{"map<ID, ID>", []string{"ID"}},
	}

	for _, tt := range tests {
		got := ExtractTypeReferences(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ExtractTypeReferences(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ExtractTypeReferences(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- Test struct field extraction with various types ---

const complexStructs = `app Test {
    struct Simple {
        Name: string
        Count: int
        Active: bool
        Score: float
        Data: long
    }

    struct OptionalFields {
        optional Name: string
        optional Tags: array<string>
        optional Meta: map<string, string>
    }

    struct NestedRef {
        Items: array<Simple>
        Lookup: map<string, OptionalFields>
    }

    callable test_func {
        request:  Simple
        response: NestedRef
        effect:   "read"
        toolName: "test-func"
        service:  "test"
    }
}
`

func TestComplexStructs(t *testing.T) {
	app, _, err := ParseFile(complexStructs)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(app.Structs) != 3 {
		t.Fatalf("expected 3 structs, got %d", len(app.Structs))
	}

	// Simple struct: 5 scalar fields.
	simple := app.Structs[0]
	if len(simple.Fields) != 5 {
		t.Errorf("Simple fields = %d, want 5", len(simple.Fields))
	}

	// OptionalFields: all optional.
	opt := app.Structs[1]
	for _, f := range opt.Fields {
		if !f.Optional {
			t.Errorf("OptionalFields.%s should be optional", f.Name)
		}
	}

	// NestedRef: array and map references.
	nested := app.Structs[2]
	if len(nested.Fields) != 2 {
		t.Errorf("NestedRef fields = %d, want 2", len(nested.Fields))
	}
	if nested.Fields[0].Type.Kind != "array" {
		t.Errorf("NestedRef.Items kind = %q, want array", nested.Fields[0].Type.Kind)
	}
	if nested.Fields[1].Type.Kind != "map" {
		t.Errorf("NestedRef.Lookup kind = %q, want map", nested.Fields[1].Type.Kind)
	}
}

// --- Test struct as text extraction for codegen ---

func TestStructExtractionForCodegen(t *testing.T) {
	app, _, err := ParseFile(validTodolistAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// The structs should be parseable by the spore parser and usable for codegen.
	// Verify that the ObjectDesc structures are well-formed.
	for _, s := range app.Structs {
		if s.Kind != "struct" {
			t.Errorf("struct %s kind = %q, want %q", s.Name, s.Kind, "struct")
		}
		if s.Name == "" {
			t.Error("struct has empty name")
		}
		if len(s.Fields) == 0 {
			t.Errorf("struct %s has no fields", s.Name)
		}
		for _, f := range s.Fields {
			if f.Name == "" {
				t.Errorf("struct %s has field with empty name", s.Name)
			}
			if f.Type.Kind == "" {
				t.Errorf("struct %s field %s has empty type kind", s.Name, f.Name)
			}
		}
	}
}

// --- Test multiple bundles ---

const multiBundle = `app Test {
    id:        "app.test"
    name:      "Test"
    version:   "0.1.0"
    namespace: "test"

    struct Item {
        Id: string
    }

    callable func_a {
        request:  Item
        effect:   "read"
        toolName: "func-a"
        service:  "test"
    }

    callable func_b {
        request:  Item
        effect:   "write"
        toolName: "func-b"
        service:  "test"
    }

    bundle bundle1 {
        title: "Bundle 1"
        tools: [func_a]
    }

    bundle bundle2 {
        title: "Bundle 2"
        tools: [func_a, func_b]
    }
}
`

func TestMultipleBundles(t *testing.T) {
	app, _, err := ParseFile(multiBundle)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
	}

	if len(app.Bundles) != 2 {
		t.Fatalf("expected 2 bundles, got %d", len(app.Bundles))
	}
}

// --- Bundle description markdown paragraphing ---

const descriptionEscapeAppdef = `app Esc {
    id:        "app.esc"
    name:      "Esc"
    version:   "0.1.0"
    namespace: "esc"

    callable ping {
        effect: "read"
    }

    bundle esc_tools {
        title:       "Esc Tools"
        description: "Purpose paragraph.\n\n- **ping** — call it to check liveness; path\\name stays literal."
        icon:        "shield-check"
        tools:       [ping]
    }
}
`

func TestBundleDescriptionNewlineUnescape(t *testing.T) {
	app, diags, err := ParseFile(descriptionEscapeAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected validation diagnostic: %s", d)
		}
	}
	if len(app.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(app.Bundles))
	}
	got := app.Bundles[0].Description
	want := "Purpose paragraph.\n\n- **ping** — call it to check liveness; path\\name stays literal."
	if got != want {
		t.Fatalf("description = %q\nwant            %q", got, want)
	}
}

const docCommentAppdef = `app Docs {
    id: "app.docs"
    name: "Docs"
    version: "0.1.0"
    namespace: "docs"

    // 全网搜索工具
    callable search {
        effect: "read"
    }

    callable plain {
        effect: "read"
    }

    // 多行注释第一行
    // 多行注释第二行
    callable multi {
        effect: "read"
    }

    // 被空行隔断的注释

    callable detached {
        effect: "read"
    }

    bundle kv_first {
        title: "KV wins"
        description: "kv description"
        tools: [search]
    }

    // 注释回落描述
    bundle comment_only {
        title: "Comment fallback"
        tools: [plain]
    }
}
`

func TestCallableDocCommentDescription(t *testing.T) {
	app, diags, err := ParseFile(docCommentAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected diagnostic: %s", d)
		}
	}
	byID := map[string]CallableDecl{}
	for _, c := range app.Callables {
		byID[c.ID] = c
	}
	if got := byID["search"].Description; got != "全网搜索工具" {
		t.Errorf("search Description = %q, want %q", got, "全网搜索工具")
	}
	if got := byID["plain"].Description; got != "" {
		t.Errorf("plain Description = %q, want empty (no doc comment)", got)
	}
	if got := byID["multi"].Description; got != "多行注释第一行 多行注释第二行" {
		t.Errorf("multi Description = %q, want two lines joined by space", got)
	}
	if got := byID["detached"].Description; got != "" {
		t.Errorf("detached Description = %q, want empty (blank line detaches)", got)
	}
}

func TestBundleDocCommentFallback(t *testing.T) {
	app, _, err := ParseFile(docCommentAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(app.Bundles) != 2 {
		t.Fatalf("expected 2 bundles, got %d", len(app.Bundles))
	}
	if got := app.Bundles[0].Description; got != "kv description" {
		t.Errorf("kv_first Description = %q, want %q (KV is authoritative)", got, "kv description")
	}
	if got := app.Bundles[1].Description; got != "注释回落描述" {
		t.Errorf("comment_only Description = %q, want %q (comment fallback)", got, "注释回落描述")
	}
}

// --- Agent Binding ---

const agentBindingAppdef = `app Bound {
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

// TestParseListenBlock covers `listen <kind> { }` parsing: valid kind
// extraction, rejection of unexpected fields, and duplicate-kind validation.
func TestParseListenBlock(t *testing.T) {
	app, diags, err := ParseFile(`app L {
    id: "app.l"
    name: "L"
    version: "0.1.0"
    namespace: "l"

    listen app_lifecycle { }
    listen app_event { }
}`)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected parse diagnostic: %s", d)
		}
	}
	if len(app.Listens) != 2 || app.Listens[0].Kind != "app_lifecycle" || app.Listens[1].Kind != "app_event" {
		t.Fatalf("Listens = %+v, want [app_lifecycle app_event]", app.Listens)
	}
	for _, d := range Validate(app) {
		t.Errorf("unexpected validation diagnostic: %s", d)
	}

	// Duplicate kind fails validation.
	app, diags, err = ParseFile(`app L2 {
    id: "app.l2"
    name: "L2"
    version: "0.1.0"
    namespace: "l2"

    listen app_lifecycle { }
    listen app_lifecycle { }
}`)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected parse diagnostic: %s", d)
		}
	}
	found := false
	for _, d := range Validate(app) {
		if strings.Contains(d.Message, "duplicate listen kind") {
			found = true
		}
	}
	if !found {
		t.Error("duplicate listen kind must fail validation")
	}

	// Phase-1 takes no fields: unknown fields are parse errors, not no-ops.
	_, diags, err = ParseFile(`app L3 {
    id: "app.l3"
    name: "L3"
    version: "0.1.0"
    namespace: "l3"

    listen app_lifecycle {
        filter: "x"
    }
}`)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	found = false
	for _, d := range diags {
		if strings.Contains(d.Message, "unexpected field") {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown listen field must produce a diagnostic, got %v", diags)
	}
}

func TestParseFreeAgentStandalone(t *testing.T) {
	app, diags, err := ParseFile(agentBindingAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected parse diagnostic: %s", d)
		}
	}

	fa := app.FreeAgent
	if fa == nil {
		t.Fatal("expected FreeAgent to be parsed")
	}
	if !fa.AllowCreate || fa.AllowSwitch || !fa.AllowMessage {
		t.Errorf("FreeAgent flags = create:%v switch:%v message:%v, want true/false/true", fa.AllowCreate, fa.AllowSwitch, fa.AllowMessage)
	}
	if len(fa.AgentKinds) != 2 || fa.AgentKinds[0] != "coder" || fa.AgentKinds[1] != "reviewer" {
		t.Errorf("FreeAgent.AgentKinds = %v, want [coder reviewer]", fa.AgentKinds)
	}

	for _, d := range Validate(app) {
		t.Errorf("unexpected validation diagnostic: %s", d)
	}
}

const invalidAgentBindingAppdef = `app Bad {
    id: "app.bad"
    name: "Bad"
    version: "0.1.0"
    namespace: "bad"

    callable ping {
        effect: "read"
    }

    agent_binding {
        entrypoint: nowhere
        events: [missing_event]
        callables: [missing_callable]
    }

    free_agent {
        allow_create: true
    }
}
`

func TestValidateAgentBinding(t *testing.T) {
	app, parseDiags, err := ParseFile(invalidAgentBindingAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// agent_binding is no longer a recognized block; the parser should report
	// it as unexpected content so agents get a clear migration signal.
	foundUnexpected := false
	for _, d := range parseDiags {
		if strings.Contains(d.Message, "unexpected content") {
			foundUnexpected = true
		}
	}
	if !foundUnexpected {
		t.Errorf("expected 'unexpected content' diagnostic for agent_binding block, got: %v", parseDiags)
	}

	// Validation must pass for the free_agent-only model.
	diags := Validate(app)
	for _, d := range diags {
		t.Errorf("unexpected validation diagnostic: %s", d)
	}
}

// --- Plugin Agent ---

const pluginAgentAppdef = `app Plugged {
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
        system_prompt: "You operate the Plugged plugin.\nBe terse."
        bundles: ["builtin:bundle:web-search"]
        model: "openai|gpt-5"
    }
}
`

func TestParsePluginAgentBlock(t *testing.T) {
	app, diags, err := ParseFile(pluginAgentAppdef)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected parse diagnostic: %s", d)
		}
	}

	pa := app.PluginAgents
	if len(pa) != 1 {
		t.Fatalf("expected 1 PluginAgent, got %d", len(pa))
	}
	if pa[0].Name != "default" {
		t.Errorf("PluginAgent.Name = %q, want default for unnamed block", pa[0].Name)
	}
	if pa[0].DisplayName != "Plugged Assistant" {
		t.Errorf("PluginAgent.DisplayName = %q", pa[0].DisplayName)
	}
	if pa[0].SystemPrompt != "You operate the Plugged plugin.\nBe terse." {
		t.Errorf("PluginAgent.SystemPrompt = %q (want \\n unescaped)", pa[0].SystemPrompt)
	}
	if len(pa[0].Bundles) != 1 || pa[0].Bundles[0] != "builtin:bundle:web-search" {
		t.Errorf("PluginAgent.Bundles = %v", pa[0].Bundles)
	}
	if pa[0].Model != "openai|gpt-5" {
		t.Errorf("PluginAgent.Model = %q, want %q", pa[0].Model, "openai|gpt-5")
	}
	for _, d := range Validate(app) {
		t.Errorf("unexpected validation diagnostic: %s", d)
	}
}

func TestParsePluginAgentModelField(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantModel string
		wantDiags int
	}{
		{
			name: "empty model keeps host default",
			src: `app Empty {
                id: "app.empty"
                version: "0.1.0"
                namespace: "empty"

                plugin_agent { display_name: "x" }
            }`,
			wantModel: "",
		},
		{
			name: "valid Provider|Model round-trips",
			src: `app Ok {
                id: "app.ok"
                version: "0.1.0"
                namespace: "ok"

                plugin_agent { model: "minimax|MiniMax-M3" }
            }`,
			wantModel: "minimax|MiniMax-M3",
		},
		{
			name: "missing pipe is rejected",
			src: `app NoPipe {
                id: "app.np"
                version: "0.1.0"
                namespace: "np"

                plugin_agent { model: "no-pipe-here" }
            }`,
			wantModel: "no-pipe-here",
			wantDiags: 1,
		},
		{
			name: "empty provider is rejected",
			src: `app NoProv {
                id: "app.nprov"
                version: "0.1.0"
                namespace: "nprov"

                plugin_agent { model: "|MiniMax-M3" }
            }`,
			wantModel: "|MiniMax-M3",
			wantDiags: 1,
		},
		{
			name: "empty model is rejected",
			src: `app NoMod {
                id: "app.nm"
                version: "0.1.0"
                namespace: "nm"

                plugin_agent { model: "minimax|" }
            }`,
			wantModel: "minimax|",
			wantDiags: 1,
		},
		{
			name: "extra pipe in model is rejected",
			src: `app Extra {
                id: "app.ex"
                version: "0.1.0"
                namespace: "ex"

                plugin_agent { model: "openai|gpt|5" }
            }`,
			wantModel: "openai|gpt|5",
			wantDiags: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _, err := ParseFile(tc.src)
			if err != nil {
				t.Fatalf("ParseFile failed: %v", err)
			}
			if len(app.PluginAgents) != 1 {
				t.Fatalf("expected 1 PluginAgent, got %d", len(app.PluginAgents))
			}
			if app.PluginAgents[0].Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", app.PluginAgents[0].Model, tc.wantModel)
			}
			diags := Validate(app)
			if len(diags) != tc.wantDiags {
				t.Errorf("Validate produced %d diagnostics (%v), want %d", len(diags), diags, tc.wantDiags)
			}
		})
	}
}

func TestParsePluginAgentNamedBlocks(t *testing.T) {
	src := `app Multi {
    id: "app.multi"
    version: "0.1.0"
    namespace: "multi"

    plugin_agent {
        display_name: "Main Assistant"
    }
    plugin_agent reviewer {
        display_name: "Review Assistant"
        system_prompt: "You review.\nBe strict."
        bundles: ["builtin:bundle:web-search"]
    }
    plugin_agent preview-2 {
        display_name: "Preview Assistant"
    }
}
`
	app, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected parse diagnostic: %s", d)
		}
	}
	if len(app.PluginAgents) != 3 {
		t.Fatalf("PluginAgents = %d, want 3", len(app.PluginAgents))
	}
	wantNames := []string{"default", "reviewer", "preview-2"}
	for i, want := range wantNames {
		if app.PluginAgents[i].Name != want {
			t.Errorf("PluginAgents[%d].Name = %q, want %q", i, app.PluginAgents[i].Name, want)
		}
	}
	if app.PluginAgents[1].DisplayName != "Review Assistant" ||
		app.PluginAgents[1].SystemPrompt != "You review.\nBe strict." ||
		len(app.PluginAgents[1].Bundles) != 1 {
		t.Errorf("reviewer block = %+v", app.PluginAgents[1])
	}
	if app.PluginAgents[2].DisplayName != "Preview Assistant" {
		t.Errorf("preview-2 DisplayName = %q", app.PluginAgents[2].DisplayName)
	}
	for _, d := range Validate(app) {
		t.Errorf("unexpected validation diagnostic: %s", d)
	}
}

func TestParsePluginAgentDuplicateSlot(t *testing.T) {
	// Two unnamed blocks collide on the default slot.
	src := `app Dup {
    id: "app.dup"
    version: "0.1.0"
    namespace: "dup"

    plugin_agent { display_name: "One" }
    plugin_agent { display_name: "Two" }
}
`
	_, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `duplicate slot "default"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate-slot diagnostic, got: %v", diags)
	}

	// Two blocks with the same explicit name collide too.
	src = `app Dup2 {
    id: "app.dup2"
    version: "0.1.0"
    namespace: "dup2"

    plugin_agent reviewer { display_name: "One" }
    plugin_agent reviewer { display_name: "Two" }
}
`
	_, diags, err = ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	found = false
	for _, d := range diags {
		if strings.Contains(d.Message, `duplicate slot "reviewer"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate-slot diagnostic, got: %v", diags)
	}
}

func TestValidatePluginAgentSlotNames(t *testing.T) {
	app := &AppDef{
		AppMeta: AppMeta{ID: "app.slots", Name: "Slots", Version: "0.1.0", Namespace: "slots"},
		PluginAgents: []*PluginAgentDecl{
			{Name: "default", Line: 1},
			{Name: "Bad_Name", Line: 2},
			{Name: "9lead", Line: 3},
			{Name: "ok-name-2", Line: 4},
		},
	}
	diags := Validate(app)
	var invalid int
	for _, d := range diags {
		if strings.Contains(d.Message, "invalid slot name") {
			invalid++
		}
	}
	if invalid != 2 {
		t.Errorf("want 2 invalid-slot diagnostics, got %d: %v", invalid, diags)
	}
}

func TestValidatePluginAgentDuplicateSlotHandbuilt(t *testing.T) {
	// Validate also catches duplicate slots on hand-built AppDefs (the parser
	// is the first line of defense, but tests and other callers build ASTs
	// directly).
	app := &AppDef{
		AppMeta: AppMeta{ID: "app.dup", Name: "Dup", Version: "0.1.0", Namespace: "dup"},
		PluginAgents: []*PluginAgentDecl{
			{Name: "reviewer", Line: 10},
			{Name: "reviewer", Line: 20},
		},
	}
	diags := Validate(app)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `duplicate slot "reviewer" (first declared at line 10)`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate-slot diagnostic, got: %v", diags)
	}
}

func TestValidatePluginAgentLimitsPerBlock(t *testing.T) {
	long := strings.Repeat("x", 8193)
	app := &AppDef{
		AppMeta: AppMeta{ID: "app.big", Name: "Big", Version: "0.1.0", Namespace: "big"},
		PluginAgents: []*PluginAgentDecl{
			{
				Name:         "default",
				SystemPrompt: long,
				Bundles:      []string{"builtin:bundle:web-search", "builtin:bundle:web-search", ""},
			},
			{
				Name:         "reviewer",
				SystemPrompt: long,
			},
		},
	}
	diags := Validate(app)
	var oversize, dup, empty bool
	for _, d := range diags {
		if strings.Contains(d.Message, "system_prompt exceeds") {
			oversize = true
		}
		if strings.Contains(d.Message, "duplicate bundle") {
			dup = true
		}
		if strings.Contains(d.Message, "empty entry") {
			empty = true
		}
	}
	if !oversize || !dup || !empty {
		t.Errorf("want oversize/duplicate/empty diagnostics, got: %v", diags)
	}
	// The per-slot cap applies to each block independently: an 8193-byte
	// prompt in EACH of the two blocks yields exactly two oversize diags, and
	// a block at exactly the cap is fine.
	var oversizeCount int
	for _, d := range diags {
		if strings.Contains(d.Message, "system_prompt exceeds") {
			oversizeCount++
		}
	}
	if oversizeCount != 2 {
		t.Errorf("oversize diagnostics = %d, want 2 (one per block)", oversizeCount)
	}
	ok := &AppDef{
		AppMeta: AppMeta{ID: "app.ok", Name: "Ok", Version: "0.1.0", Namespace: "ok"},
		PluginAgents: []*PluginAgentDecl{
			{Name: "default", SystemPrompt: strings.Repeat("y", 8192)},
			{Name: "reviewer", SystemPrompt: strings.Repeat("z", 8192)},
		},
	}
	if diags := Validate(ok); len(diags) != 0 {
		t.Errorf("blocks at exactly the cap must pass, got: %v", diags)
	}
}

// --- Effect / Entrypoint Kind Vocabularies ---

func TestValidateEffectVocabulary(t *testing.T) {
	src := `app Effects {
    id: "app.effects"
    version: "0.1.0"
    namespace: "effects"

    callable good_host {
        effect: "irreversible"
    }
    callable good_legacy {
        effect: "mutate"
    }
    callable good_none {
        effect: "none"
    }
    callable bad_effect {
        effect: "writes"
    }
    callable bad_empty_ok {
    }
}
`
	app, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected parse diagnostics: %v", diags)
	}

	validateDiags := Validate(app)
	if len(validateDiags) != 1 {
		t.Fatalf("expected exactly 1 validation diagnostic, got %v", validateDiags)
	}
	if !strings.Contains(validateDiags[0].Message, `invalid effect "writes"`) || !strings.Contains(validateDiags[0].Message, "irreversible") {
		t.Errorf("unexpected diagnostic: %s", validateDiags[0])
	}
}

// --- Callable timeout parsing ---

func TestCallableTimeoutParsing(t *testing.T) {
	src := `app Timeouts {
    id: "app.timeouts"
    version: "0.1.0"
    namespace: "timeouts"

    callable with_seconds {
        timeout: "300s"
    }
    callable with_ms {
        timeout_ms: 300000
    }
    callable no_timeout {
    }
}`
	app, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected parse diagnostics: %v", diags)
	}

	want := map[string]int64{
		"with_seconds": 300000,
		"with_ms":      300000,
		"no_timeout":   0,
	}
	got := make(map[string]int64, len(app.Callables))
	for _, c := range app.Callables {
		got[c.ID] = c.TimeoutMs
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d callables, got %d", len(want), len(got))
	}
	for id, ms := range want {
		if got[id] != ms {
			t.Errorf("callable %q: expected timeout_ms=%d, got %d", id, ms, got[id])
		}
	}
}

func TestCallableTimeoutParsingErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "invalid duration",
			src: `app Timeouts {
    id: "app.timeouts"
    version: "0.1.0"
    namespace: "timeouts"
    callable bad { timeout: "not-a-duration" }
}`,
			want: "invalid timeout",
		},
		{
			name: "invalid ms",
			src: `app Timeouts {
    id: "app.timeouts"
    version: "0.1.0"
    namespace: "timeouts"
    callable bad { timeout_ms: "abc" }
}`,
			want: "invalid timeout_ms",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, diags, err := ParseFile(tc.src)
			if err != nil {
				t.Fatalf("ParseFile failed: %v", err)
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.Message, tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected diagnostic containing %q, got: %v", tc.want, diags)
			}
		})
	}
}

func TestValidateEntrypointKindVocabulary(t *testing.T) {
	src := `app Kinds {
    id: "app.kinds"
    version: "0.1.0"
    namespace: "kinds"

    entrypoint view main {
        title: "Main"
        route: "/"
    }
    entrypoint panel side {
        title: "Side"
    }
    entrypoint command act {
        title: "Act"
    }
    entrypoint page legacy {
        title: "Bad"
    }
}
`
	app, _, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	diags := Validate(app)
	if len(diags) != 1 {
		t.Fatalf("expected exactly 1 validation diagnostic, got %v", diags)
	}
	if !strings.Contains(diags[0].Message, `invalid kind "page"`) || !strings.Contains(diags[0].Message, "view, panel, command") {
		t.Errorf("unexpected diagnostic: %s", diags[0])
	}
}

// TestCallableExposeWatchParsing covers parsing of the two optional callable
// fields added for consumer-surface control (expose) and cache-invalidation
// wiring (watch): declared values are captured verbatim, and an undeclared
// expose/watch leaves the zero value (empty = the default "both" / no watch).
func TestCallableExposeWatchParsing(t *testing.T) {
	src := `app Surfaces {
    id: "app.surfaces"
    version: "0.1.0"
    namespace: "surfaces"

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
    }
    callable agent_only {
        request:  Ping
        response: Ping
        expose:   "agent"
    }
    callable both_ways {
        request:  Ping
        response: Ping
        expose:   "both"
    }
    callable watched {
        request:  Ping
        response: Ping
        watch:    ["item_changed"]
    }
    callable undeclared {
        request:  Ping
        response: Ping
    }
}`
	app, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected parse diagnostics: %v", diags)
	}

	byID := make(map[string]CallableDecl, len(app.Callables))
	for _, c := range app.Callables {
		byID[c.ID] = c
	}
	if got := byID["panel_only"].Expose; got != "frontend" {
		t.Errorf("panel_only Expose = %q, want frontend", got)
	}
	if got := byID["agent_only"].Expose; got != "agent" {
		t.Errorf("agent_only Expose = %q, want agent", got)
	}
	if got := byID["both_ways"].Expose; got != "both" {
		t.Errorf("both_ways Expose = %q, want both", got)
	}
	if got := byID["watched"].Watch; len(got) != 1 || got[0] != "item_changed" {
		t.Errorf("watched Watch = %v, want [item_changed]", got)
	}

	// Undeclared fields stay at their zero value: empty expose means the
	// default "both", nil watch means no invalidation wiring.
	if got := byID["undeclared"].Expose; got != "" {
		t.Errorf("undeclared Expose = %q, want empty (default both)", got)
	}
	if got := byID["undeclared"].Watch; got != nil {
		t.Errorf("undeclared Watch = %v, want nil", got)
	}
}

func TestCallableWatchParsingError(t *testing.T) {
	src := `app BadWatch {
    id: "app.badwatch"
    version: "0.1.0"
    namespace: "badwatch"

    struct Ping {
        ok: bool
    }

    callable watched {
        request:  Ping
        response: Ping
        watch:    "not-an-array"
    }
}`
	_, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "invalid watch list") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an 'invalid watch list' diagnostic, got: %v", diags)
	}
}

// TestParseFileTrailingContentHardFails pins the admin-tools migration bug:
// a stray '}' balanced the app block early and the parser silently dropped
// everything after it (9/28 callables parsed, misleading orphan warnings).
// Now trailing content after the app block is a hard error.
func TestParseFileTrailingContentHardFails(t *testing.T) {
	src := "app demo {\n" +
		"    version: 1.0.0\n" +
		"    callable a { request: Ping, response: Ping }\n" +
		"}\n" +
		"callable b { request: Ping, response: Ping }\n"
	_, _, err := ParseFile(src)
	if err == nil {
		t.Fatal("trailing content after app block must hard-fail")
	}
	if !strings.Contains(err.Error(), "after app block") {
		t.Fatalf("error should name the truncation, got: %v", err)
	}
}
