package agentkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

// builtinSporeSkillRaw fetches the raw markdown of a builtin skill by name.
func builtinSporeSkillRaw(t *testing.T, name string) string {
	t.Helper()
	assets, err := LoadSkillAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets {
		if a.Title == name {
			return a.Raw
		}
	}
	t.Fatalf("builtin skill %q not found", name)
	return ""
}

// runSporeFence compiles and runs a ```spore fence with one JSON input,
// mirroring the agent-side skill runner (which lives in pkg/actor/agent and
// is not importable here without a cycle).
func runSporeFence(t *testing.T, raw, argsJSON string) map[string]any {
	t.Helper()
	src, ok := scriptcard.ExtractSporeBlock(raw)
	if !ok {
		t.Fatalf("no spore fence in skill body")
	}
	var input any
	dec := json.NewDecoder(strings.NewReader(argsJSON))
	dec.UseNumber()
	if err := dec.Decode(&input); err != nil {
		t.Fatalf("args: %v", err)
	}
	input = normalizeTestJSON(input)
	rt, err := script.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	if err := rt.LoadSource("test_skill", src); err != nil {
		t.Fatalf("compile: %v", err)
	}
	result, err := rt.CallContext(script.CallContext{
		Context: context.Background(),
		Budget:  script.ExecutionBudget{MaxDuration: 10 * time.Second},
	}, "run", input)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("runtime error: %v", result.Error)
	}
	encoded, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// normalizeTestJSON matches the agent runner's number handling: JSON integers
// become int64 so spore `is int` holds.
func normalizeTestJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeTestJSON(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeTestJSON(e)
		}
		return t
	default:
		return v
	}
}

func TestBuiltinDataShapeSkill(t *testing.T) {
	raw := builtinSporeSkillRaw(t, "data-shape")
	if !strings.Contains(raw, "runtime: spore") {
		t.Fatal("data-shape must declare runtime: spore in frontmatter")
	}

	shape := runSporeFence(t, raw, `{"name":"sporemind","port":8080,"live":true}`)
	if shape["type"] != "map" {
		t.Fatalf("type: %v", shape["type"])
	}
	if shape["size"] != float64(3) {
		t.Fatalf("size: %v", shape["size"])
	}
	fieldTypes, _ := shape["field_types"].(map[string]any)
	if fieldTypes["name"] != "string" || fieldTypes["port"] != "int" || fieldTypes["live"] != "bool" {
		t.Fatalf("field_types: %v", fieldTypes)
	}

	arrShape := runSporeFence(t, raw, `[1,"a",true,null]`)
	if arrShape["type"] != "array" || arrShape["size"] != float64(4) {
		t.Fatalf("array shape: %v", arrShape)
	}
	elemTypes, _ := arrShape["element_types"].(map[string]any)
	if elemTypes["int"] != float64(1) || elemTypes["string"] != float64(1) || elemTypes["null"] != float64(1) {
		t.Fatalf("element_types: %v", elemTypes)
	}

	strShape := runSporeFence(t, raw, `"hello"`)
	if strShape["type"] != "string" || strShape["length"] != float64(5) {
		t.Fatalf("string shape: %v", strShape)
	}
}
