package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestCallableToJSONSchema_PrimitivesAndObjects(t *testing.T) {
	ci := domain.CallableInterface{
		Name:        "plan_submit",
		Description: "Submit a plan",
		Params: []domain.CallableParam{
			{Name: "Title", Type: "string", Required: true, Description: "Plan title"},
			{Name: "Count", Type: "int"},
			{Name: "Policy", Type: "PlanPolicy", Description: "Approval policy"},
			{Name: "Tasks", Type: "PlanTaskRef[]", Description: "Plan tasks"},
		},
	}
	raw := CallableToJSONSchema(ci)
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatalf("invalid JSON schema: %v\n%s", err, raw)
	}

	props := schema["properties"].(map[string]any)

	// string type preserved
	title := props["Title"].(map[string]any)
	if title["type"] != "string" {
		t.Errorf("Title type: got %v, want string", title["type"])
	}

	// int → integer
	count := props["Count"].(map[string]any)
	if count["type"] != "integer" {
		t.Errorf("Count type: got %v, want integer", count["type"])
	}

	// Custom struct type → object (must NOT be "PlanPolicy")
	policy := props["Policy"].(map[string]any)
	if policy["type"] != "object" {
		t.Errorf("Policy type: got %v, want object", policy["type"])
	}

	// Array of custom struct → array with items.type object
	tasks := props["Tasks"].(map[string]any)
	if tasks["type"] != "array" {
		t.Errorf("Tasks type: got %v, want array", tasks["type"])
	}
	items := tasks["items"].(map[string]any)
	if items["type"] != "object" {
		t.Errorf("Tasks items type: got %v, want object", items["type"])
	}

	// Required fields
	required := schema["required"].([]any)
	if len(required) != 1 || required[0] != "Title" {
		t.Errorf("required: got %v, want [Title]", required)
	}

	// Sanity: no raw type names leak into schema
	if strings.Contains(raw, "PlanPolicy") || strings.Contains(raw, "PlanTaskRef") {
		t.Errorf("schema leaked custom type name: %s", raw)
	}
}

// TestToolSpecsFromCallables_DeepSchemaFromLayout pins the unified deep
// projection: a host callable whose ReqSchemaID resolves in the registry gets
// a field-level schema with nested struct expansion, while unresolvable
// callables keep the shallow interface-derived schema.
func TestToolSpecsFromCallables_DeepSchemaFromLayout(t *testing.T) {
	planSubmitID := int32(0)
	for id, name := range gen.SchemaIDs {
		if name == "PlanSubmitReq" {
			planSubmitID = int32(id)
			break
		}
	}
	if planSubmitID == 0 {
		t.Fatal("PlanSubmitReq not in registry")
	}
	specs := ToolSpecsFromCallables(map[string]domain.CallableInterface{
		"plan_submit": {
			Name:        "plan_submit",
			Description: "Submit a plan",
			ReqSchemaID: planSubmitID,
			Params: []domain.CallableParam{
				{Name: "Title", Description: "plan title"},
			},
		},
		"legacy": {
			Name:        "legacy",
			Description: "No schema id",
			Params: []domain.CallableParam{
				{Name: "Count", Type: "int", Required: true},
			},
		},
	}, []string{"plan_submit", "legacy"})
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	byName := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
	}

	// Deep: nested PlanPolicy expands to a typed object with Mode property.
	var deep map[string]any
	if err := json.Unmarshal([]byte(byName["plan_submit"].InputSchema), &deep); err != nil {
		t.Fatalf("deep schema invalid JSON: %v", err)
	}
	props := deep["properties"].(map[string]any)
	policy := props["Policy"].(map[string]any)
	policyProps := policy["properties"].(map[string]any)
	if _, ok := policyProps["Mode"]; !ok {
		t.Errorf("Policy.properties = %v, want expanded Mode field", policyProps)
	}
	tasks := props["Tasks"].(map[string]any)
	items := tasks["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	if _, ok := itemProps["Subject"]; !ok {
		t.Errorf("Tasks.items.properties = %v, want expanded Subject field", itemProps)
	}
	title := props["Title"].(map[string]any)
	if title["description"] != "plan title" {
		t.Errorf("Title.description = %v, want merged from params", title)
	}

	// Shallow fallback stays for layout-less callables.
	var shallow map[string]any
	if err := json.Unmarshal([]byte(byName["legacy"].InputSchema), &shallow); err != nil {
		t.Fatalf("shallow schema invalid JSON: %v", err)
	}
	sProps := shallow["properties"].(map[string]any)
	if sProps["Count"].(map[string]any)["type"] != "integer" {
		t.Errorf("legacy Count = %v, want integer from shallow projection", sProps["Count"])
	}
}

func TestToolSpecsFromCallables_DotFoldAndDeclaredNames(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"task_create":           {Name: "task_create", Description: "create a task"},
		"task_update":           {Name: "task_update", Description: "update a task", ToolName: "update_task"},
		"plan_submit":           {Name: "plan_submit", Description: "submit a plan"},
		"memory_save":           {Name: "memory_save", Description: "save memory"},
		"memory_recall":         {Name: "memory_recall", Description: "recall memory"},
		"project.wiki_get_card": {Name: "project.wiki_get_card", Description: "get a card"},
		"project.read":          {Name: "project.read", Description: "read a file"},
	}
	specs := ToolSpecsFromCallables(callables, []string{
		"task_create", "task_update", "plan_submit", "memory_save", "memory_recall", "project.wiki_get_card", "project.read",
	})

	byCallable := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byCallable[s.CallableID] = s
	}

	// task_create: no declared ToolName → dot-fold (no dots, stays same)
	if got := byCallable["task_create"].Name; got != "task_create" {
		t.Errorf("task_create Name = %q, want task_create", got)
	}
	// task_update: declared ToolName "update_task" takes priority
	if got := byCallable["task_update"].Name; got != "update_task" {
		t.Errorf("task_update Name = %q, want update_task", got)
	}
	if got := byCallable["plan_submit"].Name; got != "plan_submit" {
		t.Errorf("plan_submit Name = %q, want plan_submit", got)
	}
	if got := byCallable["memory_save"].Name; got != "memory_save" {
		t.Errorf("memory_save Name = %q, want memory_save", got)
	}
	if got := byCallable["memory_recall"].Name; got != "memory_recall" {
		t.Errorf("memory_recall Name = %q, want memory_recall", got)
	}
	if got := byCallable["project.wiki_get_card"].Name; got != "project-wiki_get_card" {
		t.Errorf("project.wiki_get_card Name = %q, want project-wiki_get_card", got)
	}
	// Dot-fold: dots replaced with hyphens.
	if got := byCallable["project.read"].Name; got != "project-read" {
		t.Errorf("project.read Name = %q, want project-read", got)
	}
}

func TestToolSpecsFromCallables_WorktreeNamesAreExplicit(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"project.worktree_enter": {Name: "project.worktree_enter"},
		"project.worktree_exit":  {Name: "project.worktree_exit"},
	}
	specs := ToolSpecsFromCallables(callables, []string{"project.worktree_enter", "project.worktree_exit"})
	got := map[string]string{}
	for _, spec := range specs {
		got[spec.CallableID] = spec.Name
	}
	if got["project.worktree_enter"] != "project-worktree_enter" || got["project.worktree_exit"] != "project-worktree_exit" {
		t.Fatalf("unexpected worktree tool names: %#v", got)
	}
}

// TestToolSpecsFromCallables_DeclaredValuesOnly verifies that when
// CallableInterface provides declared values for EffectKind, ServiceName,
// and ToolName, those are used; when empty, EffectKind defaults to
// EffectNone and ServiceName defaults to empty.
func TestToolSpecsFromCallables_DeclaredValuesOnly(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"shell.bash": {
			Name:        "shell.bash",
			Description: "Run a bash command",
			EffectKind:  "custom_effect",
			ServiceName: "custom_svc",
			ToolName:    "custom_bash_tool",
		},
		"project.read": {
			Name:        "project.read",
			Description: "Read a file",
			EffectKind:  string(domain.EffectReversible),
			ServiceName: "fs",
			ToolName:    "read_file",
		},
	}
	specs := ToolSpecsFromCallables(callables, []string{"shell.bash", "project.read"})
	byCallable := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byCallable[s.CallableID] = s
	}

	// shell.bash: declared values override heuristics
	if got := byCallable["shell.bash"].Name; got != "custom_bash_tool" {
		t.Errorf("shell.bash Name = %q, want custom_bash_tool", got)
	}
	if got := byCallable["shell.bash"].EffectKind; got != "custom_effect" {
		t.Errorf("shell.bash EffectKind = %q, want custom_effect", got)
	}
	if got := byCallable["shell.bash"].ServiceName; got != "custom_svc" {
		t.Errorf("shell.bash ServiceName = %q, want custom_svc", got)
	}

	// project.read: declared values override heuristics
	if got := byCallable["project.read"].Name; got != "read_file" {
		t.Errorf("project.read Name = %q, want read_file", got)
	}
	if got := byCallable["project.read"].EffectKind; got != string(domain.EffectReversible) {
		t.Errorf("project.read EffectKind = %q, want %q", got, domain.EffectReversible)
	}
	if got := byCallable["project.read"].ServiceName; got != "fs" {
		t.Errorf("project.read ServiceName = %q, want fs", got)
	}
}

// TestToolSpecsFromCallables_EmptyDeclarationsUseDefaults verifies that when
// CallableInterface has zero-value declared fields, the new default strategy
// applies: EffectKind defaults to EffectNone, ServiceName defaults to empty,
// ToolName defaults to dot-folded callable ID.
func TestToolSpecsFromCallables_EmptyDeclarationsUseDefaults(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"shell.bash": {
			Name:        "shell.bash",
			Description: "Run a bash command",
			// EffectKind, ServiceName, ToolName all empty → defaults
		},
		"project.read": {
			Name:        "project.read",
			Description: "Read a file",
		},
		"task_create": {
			Name:        "task_create",
			Description: "create a task",
			// No ToolName → dot-fold (no dots, stays same)
		},
		"project.write": {
			Name:        "project.write",
			Description: "Write a file",
		},
	}
	specs := ToolSpecsFromCallables(callables, []string{
		"shell.bash", "project.read", "task_create", "project.write",
	})
	byCallable := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byCallable[s.CallableID] = s
	}

	// shell.bash: dot-folded name, empty ServiceName, EffectNone default
	if got := byCallable["shell.bash"].Name; got != "shell-bash" {
		t.Errorf("shell.bash Name = %q, want shell-bash", got)
	}
	if got := byCallable["shell.bash"].ServiceName; got != "" {
		t.Errorf("shell.bash ServiceName = %q, want empty", got)
	}
	if got := byCallable["shell.bash"].EffectKind; got != string(domain.EffectNone) {
		t.Errorf("shell.bash EffectKind = %q, want %q", got, domain.EffectNone)
	}

	// project.read: dot-folded name, empty ServiceName, EffectNone default
	if got := byCallable["project.read"].Name; got != "project-read" {
		t.Errorf("project.read Name = %q, want project-read", got)
	}
	if got := byCallable["project.read"].ServiceName; got != "" {
		t.Errorf("project.read ServiceName = %q, want empty", got)
	}
	if got := byCallable["project.read"].EffectKind; got != string(domain.EffectNone) {
		t.Errorf("project.read EffectKind = %q, want %q", got, domain.EffectNone)
	}

	// task_create: no dots → dot-fold keeps the name unchanged
	if got := byCallable["task_create"].Name; got != "task_create" {
		t.Errorf("task_create Name = %q, want task_create", got)
	}
	if got := byCallable["task_create"].ServiceName; got != "" {
		t.Errorf("task_create ServiceName = %q, want empty", got)
	}

	// project.write: EffectKind defaults to EffectNone when empty
	if got := byCallable["project.write"].EffectKind; got != string(domain.EffectNone) {
		t.Errorf("project.write EffectKind = %q, want %q", got, domain.EffectNone)
	}
}

// TestToolSpecsFromCallables_StreamPassthrough verifies that the Stream flag
// from CallableInterface is carried into the resulting ToolSpec.
func TestToolSpecsFromCallables_StreamPassthrough(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"shell.bash": {
			Name:        "shell.bash",
			Description: "Run a bash command",
			Stream:      true,
		},
		"project.read": {
			Name:        "project.read",
			Description: "Read a file",
			Stream:      false,
		},
		"computeruse.stream": {
			Name:        "computeruse.stream",
			Description: "Stream computer use",
			Stream:      true,
		},
	}
	specs := ToolSpecsFromCallables(callables, []string{
		"shell.bash", "project.read", "computeruse.stream",
	})
	byCallable := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byCallable[s.CallableID] = s
	}

	if !byCallable["shell.bash"].Stream {
		t.Error("shell.bash Stream = false, want true")
	}
	if byCallable["project.read"].Stream {
		t.Error("project.read Stream = true, want false")
	}
	if !byCallable["computeruse.stream"].Stream {
		t.Error("computeruse.stream Stream = false, want true")
	}
}

// allowAllAppCallables builds a per-running-app allowed map that whitelists
// every callable, for tests that exercise spec synthesis rather than gating.
func allowAllAppCallables(resp gen.AppManagerListResp) map[string]map[string]bool {
	allowedByApp := make(map[string]map[string]bool, len(resp.Items))
	for _, app := range resp.Items {
		if app.State != "running" {
			continue
		}
		set := make(map[string]bool, len(app.Callables))
		for _, c := range app.Callables {
			callableID := app.ID + "." + c.ID
			if app.Runtime != "service" {
				callableID = "app." + callableID
			}
			set[callableID] = true
		}
		allowedByApp[app.ID] = set
	}
	return allowedByApp
}

func TestAppToolSpecsFromCatalog(t *testing.T) {
	resp := gen.AppManagerListResp{
		Items: []gen.AppStatus{
			{
				ID:    "demo-app",
				State: "running",
				Callables: []gen.AppCallableDescriptor{
					{
						ID:            "list_items",
						RequestSchema: `{"type":"object"}`,
						Effect:        "none",
						Service:       "appmanager",
					},
					{
						ID:        "add_item",
						Effect:    "irreversible",
						Service:   "appmanager",
						ToolName:  "demo-add-item",
						Streaming: true,
					},
				},
			},
			{
				ID:    "stopped-app",
				State: "stopped",
				Callables: []gen.AppCallableDescriptor{
					{ID: "do_something"},
				},
			},
		},
	}
	specs := appToolSpecsFromCatalog(resp, allowAllAppCallables(resp))
	if len(specs) != 2 {
		t.Fatalf("expected 2 app tool specs, got %d", len(specs))
	}

	// Verify the first tool spec.
	s0 := specs[0]
	if s0.CallableID != "app.demo-app.list_items" {
		t.Errorf("spec[0].CallableID = %q, want app.demo-app.list_items", s0.CallableID)
	}
	if s0.Name != "app-demo-app-list_items" {
		t.Errorf("spec[0].Name = %q, want app-demo-app-list_items", s0.Name)
	}
	if s0.EffectKind != "none" {
		t.Errorf("spec[0].EffectKind = %q, want none", s0.EffectKind)
	}
	if s0.InputSchema != `{"type":"object"}` {
		t.Errorf("spec[0].InputSchema = %q, want {\"type\":\"object\"}", s0.InputSchema)
	}
	if s0.ServiceName != "appmanager" {
		t.Errorf("spec[0].ServiceName = %q, want appmanager", s0.ServiceName)
	}

	// Verify the second tool spec respects ToolName override and Stream.
	s1 := specs[1]
	if s1.Name != "demo-add-item" {
		t.Errorf("spec[1].Name = %q, want demo-add-item (ToolName override)", s1.Name)
	}
	if s1.CallableID != "app.demo-app.add_item" {
		t.Errorf("spec[1].CallableID = %q, want app.demo-app.add_item", s1.CallableID)
	}
	if s1.EffectKind != "irreversible" {
		t.Errorf("spec[1].EffectKind = %q, want irreversible", s1.EffectKind)
	}
	if !s1.Stream {
		t.Error("spec[1].Stream = false, want true")
	}

	// Verify stopped app is not included.
	for _, s := range specs {
		if s.CallableID == "app.stopped-app.do_something" {
			t.Error("stopped app should not contribute tool specs")
		}
	}
}

// TestSporecallBundleCardDeclaresRelayTools locks the builtin:bundle:sporecall
// system card: it must resolve from embedded assets to exactly the three relay
// callable IDs (workspace.host_call / mcp.call_tool / appmanager.invoke) with
// no app-manager app behind it.
func TestSporecallBundleCardDeclaresRelayTools(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	got := a.resolveBundleCallableIDs(ctx, []string{"builtin:bundle:sporecall"})
	want := []string{"workspace.host_call", "mcp.call_tool", "appmanager.invoke"}
	if len(got) != len(want) {
		t.Fatalf("resolveBundleCallableIDs(builtin:bundle:sporecall) = %v, want %v", got, want)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("sporecall bundle missing relay callable %q; got %v", id, got)
		}
	}
}

func TestAppToolSpecsFromCatalog_ServiceCallablesRouteDirectly(t *testing.T) {
	resp := gen.AppManagerListResp{
		Items: []gen.AppStatus{
			{
				ID:      "browsermanager",
				Runtime: "service",
				State:   "running",
				Callables: []gen.AppCallableDescriptor{
					{ID: "open_global", RequestSchema: `{"type":"object"}`},
					{ID: "use", Effect: "irreversible"},
				},
			},
		},
	}
	specs := appToolSpecsFromCatalog(resp, allowAllAppCallables(resp))
	if len(specs) != 2 {
		t.Fatalf("expected 2 service tool specs, got %d", len(specs))
	}
	byCallable := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byCallable[s.CallableID] = s
	}
	openGlobal := byCallable["browsermanager.open_global"]
	if openGlobal.ServiceName != "browsermanager" {
		t.Errorf("open_global ServiceName = %q, want browsermanager", openGlobal.ServiceName)
	}
	if openGlobal.CallableID != "browsermanager.open_global" {
		t.Errorf("open_global CallableID = %q, want browsermanager.open_global", openGlobal.CallableID)
	}
	use := byCallable["browsermanager.use"]
	if use.ServiceName != "browsermanager" {
		t.Errorf("use ServiceName = %q, want browsermanager", use.ServiceName)
	}
	if use.EffectKind != "irreversible" {
		t.Errorf("use EffectKind = %q, want irreversible", use.EffectKind)
	}
}

func TestAppToolSpecsFromCatalog_ResolvesSchemaDescriptors(t *testing.T) {
	resp := gen.AppManagerListResp{
		Items: []gen.AppStatus{
			{
				ID:      "app.authenticator",
				Runtime: "native",
				State:   "running",
				SchemaDescriptors: map[string]gen.AppObjectDescriptor{
					"ProvisionRequest": {
						Kind: "struct", Name: "ProvisionRequest",
						Fields: []gen.AppFieldDescriptor{
							{Name: "Name", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}, Description: "account name"},
							{Name: "Secret", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}, Optional: true},
							{Name: "Digits", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "int64"}, Optional: true},
							{Name: "Inner", Type: gen.AppTypeDescriptor{Kind: "struct", Name: "Inner"}, Optional: true},
						},
					},
					"Inner": {
						Kind: "struct", Name: "Inner",
						Fields: []gen.AppFieldDescriptor{
							{Name: "Count", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "int"}},
						},
					},
				},
				Callables: []gen.AppCallableDescriptor{
					// Service carries the app's own namespace; the tool must
					// still route through appmanager for plugins.
					{ID: "provision", RequestSchema: "ProvisionRequest", Service: "app.authenticator"},
					{ID: "codes", RequestSchema: "MissingStruct", Service: "app.authenticator"},
					{ID: "remove", Service: "app.authenticator"},
				},
			},
		},
	}
	specs := appToolSpecsFromCatalog(resp, allowAllAppCallables(resp))
	if len(specs) != 3 {
		t.Fatalf("expected 3 tool specs, got %d", len(specs))
	}
	byName := make(map[string]domain.ToolSpec, len(specs))
	for _, s := range specs {
		byName[s.Name] = s
		if s.ServiceName != "appmanager" {
			t.Errorf("%s ServiceName = %q, want appmanager (plugins must not re-route via c.Service)", s.Name, s.ServiceName)
		}
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(byName["app-app_authenticator-provision"].InputSchema), &schema); err != nil {
		t.Fatalf("provision InputSchema is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("provision schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 4 {
		t.Fatalf("provision properties = %v, want 4 entries", props)
	}
	if name, _ := props["Name"].(map[string]any); name["type"] != "string" || name["description"] != "account name" {
		t.Errorf("Name property = %v, want typed string with description", props["Name"])
	}
	// Go-flavored scalar name (int64) projects to the same JSON family.
	if digits, _ := props["Digits"].(map[string]any); digits["type"] != "integer" {
		t.Errorf("Digits property = %v, want integer", props["Digits"])
	}
	// Nested struct expands deep across the descriptor table.
	inner, _ := props["Inner"].(map[string]any)
	innerProps, _ := inner["properties"].(map[string]any)
	if count, _ := innerProps["Count"].(map[string]any); count["type"] != "integer" {
		t.Errorf("Inner.Count = %v, want expanded integer property", innerProps["Count"])
	}
	req, _ := schema["required"].([]any)
	if len(req) != 1 || req[0] != "Name" {
		t.Errorf("required = %v, want [Name] (Secret/Digits/Inner optional)", req)
	}

	if got := byName["app-app_authenticator-codes"].InputSchema; got != emptyObjectInputSchema {
		t.Errorf("codes InputSchema = %q, want empty fallback for unresolvable struct name", got)
	}
	if got := byName["app-app_authenticator-remove"].InputSchema; got != emptyObjectInputSchema {
		t.Errorf("remove InputSchema = %q, want empty fallback for missing RequestSchema", got)
	}
}

func TestParseAppToolCallable(t *testing.T) {
	tests := []struct {
		input    string
		known    []string
		appID    string
		callable string
		ok       bool
	}{
		// Single-segment appID: naive split, no knowledge needed.
		{"app.myapp.list_items", nil, "myapp", "list_items", true},
		{"app.ns-app.do_thing", nil, "ns-app", "do_thing", true},
		// Callable containing dots: remainder joined back.
		{"app.id.a.b.c", nil, "id", "a.b.c", true},
		// Dotted appID: longest registered app ID prefix wins.
		{"app.builtin.ssh.list", []string{"builtin.browser", "builtin.ssh"}, "builtin.ssh", "list", true},
		{"app.builtin.browser.navigate", []string{"builtin.browser", "builtin.ssh"}, "builtin.browser", "navigate", true},
		// Dotted appID with a dotted callable stays unambiguous when the
		// appID is registered.
		{"app.builtin.ssh.a.b.c", []string{"builtin.ssh"}, "builtin.ssh", "a.b.c", true},
		// Longest known prefix wins over a shorter registered prefix.
		{"app.example.app.greet", []string{"example", "example.app"}, "example.app", "greet", true},
		// Registered single-segment appIDs keep their naive split.
		{"app.demo-app.add_item", []string{"demo-app", "builtin.ssh"}, "demo-app", "add_item", true},
		// Unknown/unregistered appID: naive fallback preserved.
		{"app.unregistered-app.list", []string{"builtin.ssh"}, "unregistered-app", "list", true},
		// Unrelated prefixes never match.
		{"app.other-app.list", []string{"builtin.ssh", "builtin.ssh.list"}, "other-app", "list", true},
		// Non-app callables are rejected.
		{"mcp.srv.tool", nil, "", "", false},
		{"app.", nil, "", "", false},
		{"app", nil, "", "", false},
		{"", nil, "", "", false},
	}
	for _, tt := range tests {
		appID, callable, ok := parseAppToolCallable(tt.input, tt.known)
		if ok != tt.ok {
			t.Errorf("parseAppToolCallable(%q, %v): ok = %v, want %v", tt.input, tt.known, ok, tt.ok)
			continue
		}
		if ok {
			if appID != tt.appID {
				t.Errorf("parseAppToolCallable(%q, %v): appID = %q, want %q", tt.input, tt.known, appID, tt.appID)
			}
			if callable != tt.callable {
				t.Errorf("parseAppToolCallable(%q, %v): callable = %q, want %q", tt.input, tt.known, callable, tt.callable)
			}
		}
	}
}

func TestAppIDsFromListResp(t *testing.T) {
	resp := gen.AppManagerListResp{Items: []gen.AppStatus{
		{ID: "builtin.ssh", State: "running"},
		{ID: "builtin.browser", State: "running"},
		{ID: "stopped-app", State: "stopped"},
	}}
	got := appIDsFromListResp(resp)
	want := []string{"builtin.ssh", "builtin.browser"}
	if len(got) != len(want) {
		t.Fatalf("appIDsFromListResp: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("appIDsFromListResp: got %v, want %v", got, want)
			break
		}
	}
}

// TestAppToolSpecsFromCatalog_MountedFilter verifies the allowedByApp gating:
// an app present in the map only emits callables in its set, and an empty set,
// a missing map entry, and a nil map all suppress the app entirely.
func TestAppToolSpecsFromCatalog_MountedFilter(t *testing.T) {
	resp := gen.AppManagerListResp{Items: []gen.AppStatus{
		{
			ID:    "demo-app",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "first"},
				{ID: "second"},
				{ID: "third"},
			},
		},
		{
			ID:    "gated-empty",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "hidden"},
			},
		},
		{
			ID:    "ungated-app",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "visible"},
			},
		},
	}}

	// demo-app: only "first" is exposed; gated-empty: nothing mounted → 0
	// tools; ungated-app: absent from the map → 0 tools (no passthrough).
	allowedByApp := map[string]map[string]bool{
		"demo-app":    {"app.demo-app.first": true},
		"gated-empty": {},
	}
	specs := appToolSpecsFromCatalog(resp, allowedByApp)

	var callableIDs []string
	for _, s := range specs {
		callableIDs = append(callableIDs, s.CallableID)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec (demo-app.first), got %d: %v", len(specs), callableIDs)
	}
	if specs[0].CallableID != "app.demo-app.first" {
		t.Errorf("unexpected gated catalog: %v", callableIDs)
	}

	// nil map → fail-closed: every app contributes zero tools.
	none := appToolSpecsFromCatalog(resp, nil)
	if len(none) != 0 {
		t.Errorf("nil filter must expose nothing, got %d: %v", len(none), none)
	}
}

// TestResolveAppTools_BundleMountFilter exercises the full resolveAppTools
// path: an app whose manifest declares bundles (an app-bundle:{appID}: card
// exists in the project catalog) only exposes the callables of the
// app-bundle cards this agent has MOUNTED; none mounted → 0 tools. An app
// whose manifest declares no bundles at all (no mountable card exists) also
// contributes 0 tools — no legacy passthrough.
func TestResolveAppTools_BundleMountFilter(t *testing.T) {
	appList := gen.AppManagerListResp{Items: []gen.AppStatus{
		{
			ID:    "demo-app",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "first"},
				{ID: "second"},
			},
		},
		{
			ID:    "plain-app",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "list_items"},
			},
		},
	}}

	// demo-app's bundle card declares one callable (app.demo-app.first).
	bundleDescriptor := domain.ComponentDescriptor{
		Ref:   domain.ComponentRef{CardID: "app-bundle:demo-app:tools", Kind: "bundle", Source: "appmanager"},
		Title: "Demo App Tools",
		Tools: []domain.ComponentToolContribution{
			{ID: "t1", CardID: "app-bundle:demo-app:tools", CallableID: "app.demo-app.first"},
		},
	}

	fake := actor.Planner(fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "appmanager.list":
			return appList, nil
		case "appmanager.component_get":
			var req gen.AppManagerComponentGetReq
			switch p := payload.(type) {
			case gen.AppManagerComponentGetReq:
				req = p
			case []byte:
				_ = json.Unmarshal(p, &req)
			}
			if req.CardID != "app-bundle:demo-app:tools" {
				return nil, fmt.Errorf("unexpected card %q", req.CardID)
			}
			return gen.AppManagerComponentGetResp{Component: bundleDescriptor}, nil
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	}})

	scenarios := []struct {
		name     string
		cardRefs []gen.CardRef
		want     map[string]bool // expected present callable IDs
		notWant  map[string]bool // expected absent callable IDs
	}{
		{
			name:     "bundle declared and mounted",
			cardRefs: []gen.CardRef{{ID: "app-bundle:demo-app:tools"}},
			want:     map[string]bool{"app.demo-app.first": true},
			notWant:  map[string]bool{"app.demo-app.second": true, "app.plain-app.list_items": true},
		},
		{
			name:     "bundle declared but not mounted",
			cardRefs: nil,
			want:     map[string]bool{},
			notWant:  map[string]bool{"app.demo-app.first": true, "app.demo-app.second": true, "app.plain-app.list_items": true},
		},
		{
			// Regression for the app-novel leak: an app with no bundle
			// declaration must contribute zero tools to any agent.
			name:     "no bundle declared anywhere → zero app tools",
			cardRefs: []gen.CardRef{{ID: "builtin:bundle:file-tools"}},
			want:     map[string]bool{},
			notWant:  map[string]bool{"app.demo-app.first": true, "app.demo-app.second": true, "app.plain-app.list_items": true},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			a := &Actor{cardRefs: sc.cardRefs}
			ctx := testutil.AnonCtx(testutil.GenActorID())
			ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
			ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
				if name == "appmanager" {
					return testutil.NewFakeRef(testutil.GenActorID(), nil), true
				}
				return nil, false
			}
			ctx.PlannerFn = func() actor.Planner { return fake }

			specs := a.resolveAppTools(ctx)
			seen := make(map[string]bool, len(specs))
			for _, s := range specs {
				seen[s.CallableID] = true
			}
			for id := range sc.want {
				if !seen[id] {
					t.Errorf("missing expected callable %q in %v", id, seen)
				}
			}
			for id := range sc.notWant {
				if seen[id] {
					t.Errorf("unexpected excluded callable %q present in %v", id, seen)
				}
			}
			if len(sc.want) == 0 && len(specs) != 0 {
				t.Errorf("expected zero tools, got %d: %v", len(specs), seen)
			}
		})
	}
}

// TestAppToolSpecsFromCatalog_ExposeFilter verifies the appdef expose field
// gating on the agent surface: expose: "frontend" callables (panel-only) are
// never projected as LLM tools, while "agent", "both", and the empty default
// remain reachable.
func TestAppToolSpecsFromCatalog_ExposeFilter(t *testing.T) {
	resp := gen.AppManagerListResp{Items: []gen.AppStatus{
		{
			ID:    "demo-app",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "panel_only", Expose: "frontend"},
				{ID: "agent_only", Expose: "agent"},
				{ID: "both_ways", Expose: "both"},
				{ID: "default_expose"},
			},
		},
	}}
	allowedByApp := map[string]map[string]bool{
		"demo-app": {
			"app.demo-app.panel_only":     true,
			"app.demo-app.agent_only":     true,
			"app.demo-app.both_ways":      true,
			"app.demo-app.default_expose": true,
		},
	}
	specs := appToolSpecsFromCatalog(resp, allowedByApp)

	got := make(map[string]bool, len(specs))
	for _, s := range specs {
		got[s.CallableID] = true
	}
	if got["app.demo-app.panel_only"] {
		t.Errorf("expose:frontend callable must not be an agent tool: %v", got)
	}
	for _, want := range []string{"app.demo-app.agent_only", "app.demo-app.both_ways", "app.demo-app.default_expose"} {
		if !got[want] {
			t.Errorf("callable %s must stay agent-reachable, got %v", want, got)
		}
	}
	if len(specs) != 3 {
		t.Errorf("expected 3 specs, got %d: %v", len(specs), got)
	}
}
