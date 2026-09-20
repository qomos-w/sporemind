package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	appgen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// schemaIDByName looks a generated struct's registry id up by name so tests
// stay decoupled from numeric ids.
func schemaIDByName(t *testing.T, name string) int32 {
	t.Helper()
	for id, structName := range appgen.SchemaIDs {
		if structName == name {
			return int32(id)
		}
	}
	t.Fatalf("struct %q not in registry", name)
	return 0
}

func unmarshalSchema(t *testing.T, raw string) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v\nschema: %s", err, raw)
	}
	return schema
}

// TestResolveRequestLayout_HostDeep pins the host resolution path on a real
// registry struct with nested structs: WikiSetTaskOutputsReq for the simple
// shape, PlanSubmitReq for nesting.
func TestResolveRequestLayout_HostDeep(t *testing.T) {
	layout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "project.wiki_set_task_outputs",
		ReqSchemaID: schemaIDByName(t, "WikiSetTaskOutputsReq"),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	schema := unmarshalSchema(t, layout.JSONSchema())
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 2 {
		t.Fatalf("properties = %v, want CardId+Outputs", props)
	}
	if p, _ := props["CardId"].(map[string]any); p["type"] != "string" {
		t.Errorf("CardId = %v, want string", props["CardId"])
	}
	if p, _ := props["Outputs"].(map[string]any); p["type"] != "object" {
		t.Errorf("Outputs = %v, want object", props["Outputs"])
	}
	req, _ := schema["required"].([]any)
	if len(req) != 2 || req[0] != "CardId" || req[1] != "Outputs" {
		t.Errorf("required = %v, want [CardId Outputs]", req)
	}
}

func TestResolveRequestLayout_NestedStructs(t *testing.T) {
	layout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "plan_submit",
		ReqSchemaID: schemaIDByName(t, "PlanSubmitReq"),
		Params: []appgen.CallableParam{
			{Name: "Title", Description: "plan title"},
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	schema := unmarshalSchema(t, layout.JSONSchema())
	props, _ := schema["properties"].(map[string]any)

	// Tasks: []PlanTaskRef → array of expanded objects.
	tasks, _ := props["Tasks"].(map[string]any)
	if tasks["type"] != "array" {
		t.Fatalf("Tasks = %v, want array", tasks)
	}
	items, _ := tasks["items"].(map[string]any)
	if items["type"] != "object" {
		t.Fatalf("Tasks.items = %v, want object", items)
	}
	itemProps, _ := items["properties"].(map[string]any)
	if _, ok := itemProps["Subject"]; !ok {
		t.Errorf("Tasks.items.properties missing Subject: %v", itemProps)
	}

	// Policy: *PlanPolicy → expanded object with nested AllowedPrompts array.
	policy, _ := props["Policy"].(map[string]any)
	if policy["type"] != "object" {
		t.Fatalf("Policy = %v, want object", policy)
	}
	policyProps, _ := policy["properties"].(map[string]any)
	if _, ok := policyProps["Mode"]; !ok {
		t.Errorf("Policy.properties missing Mode: %v", policyProps)
	}

	// Description merge from manifest params.
	title, _ := props["Title"].(map[string]any)
	if title["description"] != "plan title" {
		t.Errorf("Title.description = %v, want merged from params", title)
	}

	// Policy and Tasks are omitempty → optional; Title/Body required.
	req, _ := schema["required"].([]any)
	if len(req) != 2 || req[0] != "Title" || req[1] != "Body" {
		t.Errorf("required = %v, want [Title Body]", req)
	}
}

func TestRequestLayout_ValidateRoundTrip(t *testing.T) {
	layout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "plan_submit",
		ReqSchemaID: schemaIDByName(t, "PlanSubmitReq"),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	valid := map[string]any{
		"Title": "t",
		"Body":  "b",
		"Tasks": []any{
			map[string]any{"Id": "1", "Subject": "s", "Status": "todo"},
		},
		"Policy": map[string]any{
			"Mode":           "strict",
			"AllowedPrompts": []any{map[string]any{"Tool": "bash", "Prompt": "run tests"}},
		},
	}
	if errs := layout.Validate(valid); len(errs) != 0 {
		t.Fatalf("valid payload rejected: %v", errs)
	}

	// Missing required + wrong scalar + wrong nested field type.
	invalid := map[string]any{
		"Title": 3,
		"Tasks": []any{map[string]any{"Id": "1", "Subject": 9, "Status": "todo"}},
	}
	errs := layout.Validate(invalid)
	joined := errorText(errs)
	for _, want := range []string{"Body is required", "Title: expected string", "Tasks[0].Subject: expected string"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q missing %q", joined, want)
		}
	}
}

func TestRequestLayout_ValidateJSON(t *testing.T) {
	layout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "project.wiki_set_task_outputs",
		ReqSchemaID: schemaIDByName(t, "WikiSetTaskOutputsReq"),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if errs := layout.ValidateJSON([]byte(`{"CardId":"c","Outputs":{"score":1}}`)); len(errs) != 0 {
		t.Fatalf("valid JSON rejected: %v", errs)
	}
	errs := layout.ValidateJSON([]byte(`{"Outputs":{}}`))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "CardId is required") {
		t.Fatalf("errors = %v, want CardId required", errs)
	}
	if errs := layout.ValidateJSON([]byte(`[]`)); len(errs) == 0 {
		t.Fatal("non-object payload must fail")
	}
}

// TestRequestLayout_NormalizeCoercesCardScalars pins the card-frontmatter
// integration: unquoted YAML scalars arrive as strings, so numeric and
// boolean args must coerce to the layout's families, garbage must fail, and
// already-typed JSON values must pass through unchanged.
func TestRequestLayout_NormalizeCoercesCardScalars(t *testing.T) {
	layout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "plan_submit",
		ReqSchemaID: schemaIDByName(t, "PlanSubmitReq"),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Card-authored form: scalars as strings, block list as []string.
	payload := map[string]any{
		"Title": "t",
		"Body":  "b",
		"Tasks": []string{"placeholder-not-an-object"},
	}
	if _, errs := layout.Normalize(map[string]any{"Title": "t", "Body": "b"}); len(errs) != 0 {
		t.Fatalf("valid minimal payload rejected: %v", errs)
	}

	normalized, errs := layout.Normalize(payload)
	joined := errorText(errs)
	if !strings.Contains(joined, "Tasks[0]: expected object") {
		t.Errorf("errors %q missing Tasks[0] object mismatch", joined)
	}
	_ = normalized

	// Scalar coercion + garbage: BrowserCrawlResultsReq carries int fields.
	crawlLayout, err := ResolveRequestLayout(appgen.CallableInterface{
		Name:        "crawl.results",
		ReqSchemaID: schemaIDByName(t, "BrowserCrawlResultsReq"),
	})
	if err != nil {
		t.Fatalf("resolve crawl layout: %v", err)
	}
	coerced, errs := crawlLayout.Normalize(map[string]any{
		"TaskId": "t1",
		"Cursor": "40",
		"Limit":  50,
	})
	if len(errs) != 0 {
		t.Fatalf("string-authored ints must coerce: %v", errs)
	}
	if got, ok := coerced["Cursor"].(float64); !ok || got != 40 {
		t.Errorf("Cursor = %#v, want coerced float64(40)", coerced["Cursor"])
	}
	if got, ok := coerced["Limit"].(float64); !ok || got != 50 {
		t.Errorf("Limit = %#v, want passthrough float64(50)", coerced["Limit"])
	}

	if _, errs := crawlLayout.Normalize(map[string]any{"TaskId": "t1", "Limit": "abc"}); len(errs) == 0 {
		t.Error("non-numeric string for int field must fail")
	}
	if _, errs := crawlLayout.Normalize(map[string]any{"TaskId": "t1", "Limit": 1.5}); len(errs) == 0 {
		t.Error("non-integral number for int field must fail")
	}
}

// TestResolveRequestLayout_Errors pins the degrade conditions: callers fall
// back to a shallow schema when ErrNoRequestLayout is returned.
func TestResolveRequestLayout_Errors(t *testing.T) {
	if _, err := ResolveRequestLayout(appgen.CallableInterface{Name: "x"}); err == nil {
		t.Fatal("no ReqSchemaID must error")
	}
	if _, err := ResolveRequestLayout(appgen.CallableInterface{Name: "x", ReqSchemaID: 999999}); err == nil {
		t.Fatal("unknown schema id must error")
	}
	_, err := ResolveRequestLayout(appgen.CallableInterface{Name: "x"})
	if err != nil && !strings.Contains(err.Error(), ErrNoRequestLayout.Error()) {
		t.Fatalf("error should wrap ErrNoRequestLayout, got %v", err)
	}
}

func TestLayoutFromAppObjects_DeepAndCycle(t *testing.T) {
	objects := map[string]appgen.AppObjectDescriptor{
		"ProvisionRequest": {
			Kind: "struct", Name: "ProvisionRequest", SchemaID: 9001,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "Name", Type: appgen.AppTypeDescriptor{Kind: "scalar", Name: "string"}, Description: "account name"},
				{Name: "Inner", Type: appgen.AppTypeDescriptor{Kind: "struct", Name: "Inner"}, Optional: true},
				{Name: "Chain", Type: appgen.AppTypeDescriptor{Kind: "struct", Name: "ChainNode"}, Optional: true},
			},
		},
		"Inner": {
			Kind: "struct", Name: "Inner", SchemaID: 9002,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "Count", Type: appgen.AppTypeDescriptor{Kind: "scalar", Name: "int"}},
				{Name: "Secret", Type: appgen.AppTypeDescriptor{Kind: "scalar", Name: "string"}, Private: true},
			},
		},
		// Self-referencing shape must not loop the expansion.
		"ChainNode": {
			Kind: "struct", Name: "ChainNode", SchemaID: 9003,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "Next", Type: appgen.AppTypeDescriptor{Kind: "struct", Name: "ChainNode"}, Optional: true},
			},
		},
	}
	layout, err := LayoutFromAppObjects("ProvisionRequest", objects)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	schema := unmarshalSchema(t, layout.JSONSchema())
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 3 {
		t.Fatalf("properties = %v, want 3 (private Secret excluded from Inner)", props)
	}
	inner, _ := props["Inner"].(map[string]any)
	innerProps, _ := inner["properties"].(map[string]any)
	if len(innerProps) != 1 {
		t.Fatalf("Inner.properties = %v, want only Count (Secret is private)", innerProps)
	}
	if innerProps["Count"].(map[string]any)["type"] != "integer" {
		t.Errorf("Inner.Count = %v, want integer", innerProps["Count"])
	}

	if errs := layout.Validate(map[string]any{
		"Name":  "a",
		"Inner": map[string]any{"Count": "x"},
		"Chain": map[string]any{"Next": map[string]any{}},
	}); len(errs) != 1 || !strings.Contains(errs[0].Error(), "Inner.Count: expected integer") {
		t.Fatalf("errors = %v, want Inner.Count type violation", errs)
	}

	// Unresolvable root name errors.
	if _, err := LayoutFromAppObjects("Missing", objects); err == nil {
		t.Fatal("missing object must error")
	}
}

// enumTestObjects builds an app descriptor table carrying an enum declaration
// (ProjectKind) referenced from a request struct directly, through array
// elements, through map values, and from a nested struct — the four placements
// the projection and the validators must treat identically.
func enumTestObjects() map[string]appgen.AppObjectDescriptor {
	enumType := appgen.AppTypeDescriptor{Kind: "enum", Name: "ProjectKind"}
	return map[string]appgen.AppObjectDescriptor{
		"CreateProjectReq": {
			Kind: "struct", Name: "CreateProjectReq", SchemaID: 9101,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "Name", Type: appgen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
				{Name: "ProjectKind", Type: enumType},
				{Name: "Tags", Type: appgen.AppTypeDescriptor{Kind: "array", Name: "array", Element: &enumType}, Optional: true},
				{Name: "Meta", Type: appgen.AppTypeDescriptor{Kind: "map", Name: "map", Key: &appgen.AppTypeDescriptor{Kind: "scalar", Name: "string"}, Value: &enumType}, Optional: true},
				{Name: "Policy", Type: appgen.AppTypeDescriptor{Kind: "struct", Name: "ProjectPolicy"}, Optional: true},
			},
		},
		"ProjectPolicy": {
			Kind: "struct", Name: "ProjectPolicy", SchemaID: 9102,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "Mode", Type: enumType},
			},
		},
		"ProjectKind": {
			Kind: "enum", Name: "ProjectKind", SchemaID: 9103,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "code"},
				{Name: "notes"},
				{Name: "general"},
			},
		},
	}
}

// TestLayoutFromAppObjects_EnumProjection pins the enum projection on the app
// descriptor face: every enum-typed placement projects {"type":"string",
// "enum":[members]} so form renderers can offer a dropdown, and Validate /
// Normalize enforce exactly the member set the schema declared (single-shape
// round trip: what JSONSchema advertises is what the host checks).
func TestLayoutFromAppObjects_EnumProjection(t *testing.T) {
	layout, err := LayoutFromAppObjects("CreateProjectReq", enumTestObjects())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	schema := unmarshalSchema(t, layout.JSONSchema())
	props, _ := schema["properties"].(map[string]any)

	// Direct field: string + allowed member names in declaration order.
	kind, _ := props["ProjectKind"].(map[string]any)
	if kind["type"] != "string" {
		t.Fatalf("ProjectKind = %v, want string", kind)
	}
	enumAssertMembers(t, kind, "ProjectKind")

	// Array element placement.
	tags, _ := props["Tags"].(map[string]any)
	items, _ := tags["items"].(map[string]any)
	enumAssertMembers(t, items, "Tags.items")

	// Map value placement.
	meta, _ := props["Meta"].(map[string]any)
	additional, _ := meta["additionalProperties"].(map[string]any)
	enumAssertMembers(t, additional, "Meta.additionalProperties")

	// Nested struct field placement.
	policy, _ := props["Policy"].(map[string]any)
	policyProps, _ := policy["properties"].(map[string]any)
	mode, _ := policyProps["Mode"].(map[string]any)
	enumAssertMembers(t, mode, "Policy.Mode")

	// Round trip: a payload using only declared members validates clean in
	// every placement.
	valid := map[string]any{
		"Name":        "p",
		"ProjectKind": "code",
		"Tags":        []any{"notes", "general"},
		"Meta":        map[string]any{"tier": "general"},
		"Policy":      map[string]any{"Mode": "notes"},
	}
	if errs := layout.Validate(valid); len(errs) != 0 {
		t.Fatalf("valid payload rejected: %v", errs)
	}
	normalized, errs := layout.Normalize(valid)
	if len(errs) != 0 {
		t.Fatalf("valid payload rejected by Normalize: %v", errs)
	}
	if normalized["ProjectKind"] != "code" {
		t.Errorf("Normalize must pass enum strings through unchanged, got %v", normalized["ProjectKind"])
	}

	// Unknown members are rejected with the field path and the allowed set,
	// in every placement.
	invalid := map[string]any{
		"Name":        "p",
		"ProjectKind": "bogus",
		"Tags":        []any{"notes", "nope"},
		"Meta":        map[string]any{"tier": "nah"},
		"Policy":      map[string]any{"Mode": "zzz"},
	}
	errs = layout.Validate(invalid)
	joined := errorText(errs)
	for _, want := range []string{
		`ProjectKind: "bogus" is not a valid ProjectKind value (allowed: code, notes, general)`,
		`Tags[1]: "nope" is not a valid ProjectKind value`,
		`Meta.tier: "nah" is not a valid ProjectKind value`,
		`Policy.Mode: "zzz" is not a valid ProjectKind value`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q missing %q", joined, want)
		}
	}
	if len(errs) != 4 {
		t.Errorf("errors = %d (%q), want exactly 4 membership violations", len(errs), joined)
	}

	// Normalize reports the same violation shape.
	_, errs = layout.Normalize(map[string]any{"Name": "p", "ProjectKind": "bogus"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `ProjectKind: "bogus" is not a valid ProjectKind value`) {
		t.Fatalf("Normalize errors = %v, want membership violation with path", errs)
	}

	// Non-string values keep the pre-existing type-mismatch error.
	errs = layout.Validate(map[string]any{"Name": "p", "ProjectKind": float64(3)})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "ProjectKind: expected string (enum), got number") {
		t.Fatalf("errors = %v, want enum type mismatch", errs)
	}
}

func enumAssertMembers(t *testing.T, prop map[string]any, where string) {
	t.Helper()
	members, ok := prop["enum"].([]any)
	if !ok || len(members) != 3 || members[0] != "code" || members[1] != "notes" || members[2] != "general" {
		t.Errorf("%s.enum = %v, want [code notes general]", where, prop["enum"])
	}
}

// TestLayoutFromAppObjects_EnumDegradesWithoutDescriptor pins the lenient
// degrade: an enum-typed field whose declaration is absent from the descriptor
// table projects a plain string and accepts any string — missing descriptor
// information never blocks. An enum name as the request root is rejected (a
// request payload is an object).
func TestLayoutFromAppObjects_EnumDegradesWithoutDescriptor(t *testing.T) {
	objects := map[string]appgen.AppObjectDescriptor{
		"Req": {
			Kind: "struct", Name: "Req", SchemaID: 9110,
			Fields: []appgen.AppFieldDescriptor{
				{Name: "State", Type: appgen.AppTypeDescriptor{Kind: "enum", Name: "UnresolvedState"}},
			},
		},
	}
	layout, err := LayoutFromAppObjects("Req", objects)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	schema := unmarshalSchema(t, layout.JSONSchema())
	props, _ := schema["properties"].(map[string]any)
	state, _ := props["State"].(map[string]any)
	if state["type"] != "string" {
		t.Fatalf("State = %v, want plain string", state)
	}
	if _, has := state["enum"]; has {
		t.Errorf("State = %v, unresolved enum must not emit an enum list", state)
	}
	if errs := layout.Validate(map[string]any{"State": "anything-goes"}); len(errs) != 0 {
		t.Fatalf("unresolved enum must accept any string, got %v", errs)
	}
	if _, errs := layout.Normalize(map[string]any{"State": "anything-goes"}); len(errs) != 0 {
		t.Fatalf("unresolved enum must normalize any string, got %v", errs)
	}

	// Enum entry used as request root is not a payload object: rejected.
	if _, err := LayoutFromAppObjects("ProjectKind", enumTestObjects()); err == nil {
		t.Fatal("enum root object must be rejected")
	}

	// Enum member entries without names are rejected at conversion.
	bad := map[string]appgen.AppObjectDescriptor{
		"Req":   objects["Req"],
		"State": {Kind: "enum", Name: "State", Fields: []appgen.AppFieldDescriptor{{Type: appgen.AppTypeDescriptor{Kind: "scalar", Name: "int"}}}},
	}
	if _, err := LayoutFromAppObjects("Req", bad); err == nil {
		t.Fatal("unnamed enum member must be rejected")
	}
}

func errorText(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
