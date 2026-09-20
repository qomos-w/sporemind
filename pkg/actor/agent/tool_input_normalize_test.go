package agent

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestNormalizeToolInputKeys_TopLevelCaseInsensitive(t *testing.T) {
	schema := `{"type":"object","properties":{"SkillId":{"type":"string"},"Args":{"type":"string"},"Context":{"type":"string"}},"required":["SkillId"]}`
	input := `{"skillId":"grill-me","args":"x","context":"inline"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := `{"Args":"x","Context":"inline","SkillId":"grill-me"}`
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestNormalizeToolInputKeys_PreservesUnknownKeys(t *testing.T) {
	schema := `{"type":"object","properties":{"SkillId":{"type":"string"}}}`
	input := `{"skillId":"grill-me","extra":"keep"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := `{"SkillId":"grill-me","extra":"keep"}`
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestNormalizeToolInputKeys_NoSchema(t *testing.T) {
	input := `{"skillId":"grill-me"}`
	out, err := normalizeToolInputKeys(input, "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if out != input {
		t.Fatalf("got %q, want %q", out, input)
	}
}

func TestNormalizeToolInputKeys_InvalidInput(t *testing.T) {
	out, err := normalizeToolInputKeys("not-json", `{"properties":{"SkillId":{}}}`)
	if err != nil {
		t.Fatalf("expected nil error for invalid input, got %v", err)
	}
	if out != "not-json" {
		t.Fatalf("expected original input on parse failure, got %q", out)
	}
}

func TestNormalizeToolInputKeys_CoercesStringInt(t *testing.T) {
	schema := `{"type":"object","properties":{"Limit":{"type":"integer"},"Path":{"type":"string"}}}`
	input := `{"Limit":"5","Path":"/x"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Limit int32  `json:"Limit"`
		Path  string `json:"Path"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.Limit != 5 {
		t.Fatalf("Limit: got %d, want 5", res.Limit)
	}
	if res.Path != "/x" {
		t.Fatalf("Path: got %q, want %q", res.Path, "/x")
	}
}

func TestNormalizeToolInputKeys_CoercesStringNumber(t *testing.T) {
	schema := `{"type":"object","properties":{"Ratio":{"type":"number"}}}`
	input := `{"Ratio":"1.5"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Ratio float64 `json:"Ratio"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.Ratio != 1.5 {
		t.Fatalf("Ratio: got %v, want 1.5", res.Ratio)
	}
}

func TestNormalizeToolInputKeys_CoercesStringBool(t *testing.T) {
	schema := `{"type":"object","properties":{"Confirm":{"type":"boolean"}}}`
	input := `{"Confirm":"true"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Confirm bool `json:"Confirm"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if !res.Confirm {
		t.Fatalf("Confirm: got false, want true")
	}
}

func TestNormalizeToolInputKeys_LeavesGenuineTypeError(t *testing.T) {
	schema := `{"type":"object","properties":{"Limit":{"type":"integer"}}}`
	input := `{"Limit":"not-a-number"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var res struct {
		Limit int32 `json:"Limit"`
	}
	if err := json.Unmarshal([]byte(out), &res); err == nil {
		t.Fatalf("expected decode error for non-numeric string, got nil (out=%s)", out)
	}
}

func TestNormalizeToolInputKeys_CoercionIsCaseInsensitive(t *testing.T) {
	schema := `{"type":"object","properties":{"Limit":{"type":"integer"}}}`
	input := `{"limit":"5"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Limit int32 `json:"Limit"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.Limit != 5 {
		t.Fatalf("Limit: got %d, want 5", res.Limit)
	}
}

func TestNormalizeToolInputKeys_CamelCaseToSnakeCase(t *testing.T) {
	schema := `{"type":"object","properties":{"old_string":{"type":"string"},"new_string":{"type":"string"},"path":{"type":"string"}}}`
	input := `{"OldString":"hello","NewString":"world","Path":"/x"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.OldString != "hello" {
		t.Fatalf("old_string: got %q, want %q", res.OldString, "hello")
	}
	if res.NewString != "world" {
		t.Fatalf("new_string: got %q, want %q", res.NewString, "world")
	}
	if res.Path != "/x" {
		t.Fatalf("path: got %q, want %q", res.Path, "/x")
	}
}

func TestNormalizeToolInputKeys_CoercesStringifiedArray(t *testing.T) {
	// Real-world LLM double-encoding: Args passed as a JSON-stringified string
	// instead of a real array (2026-09 shell_exec decode failure).
	schema := `{"type":"object","properties":{"Command":{"type":"string"},"Args":{"type":"array","items":{"type":"string"}}}}`
	input := `{"Args":"[\"-c\", \"echo hi\"]","Command":"bash"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Command string   `json:"Command"`
		Args    []string `json:"Args"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.Command != "bash" {
		t.Fatalf("Command: got %q, want %q", res.Command, "bash")
	}
	if len(res.Args) != 2 || res.Args[0] != "-c" || res.Args[1] != "echo hi" {
		t.Fatalf("Args: got %#v, want [-c echo hi]", res.Args)
	}
}

func TestNormalizeToolInputKeys_CoercesStringifiedObject(t *testing.T) {
	schema := `{"type":"object","properties":{"Payload":{"type":"object"}}}`
	input := `{"Payload":"{\"Service\":\"workspace\"}"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	var res struct {
		Payload struct {
			Service string `json:"Service"`
		} `json:"Payload"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal normalized output: %v (out=%s)", err, out)
	}
	if res.Payload.Service != "workspace" {
		t.Fatalf("Payload.Service: got %q, want %q", res.Payload.Service, "workspace")
	}
}

func TestNormalizeToolInputKeys_LeavesNonJSONArrayString(t *testing.T) {
	schema := `{"type":"object","properties":{"Args":{"type":"array","items":{"type":"string"}}}}`
	input := `{"Args":"echo hi"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var res struct {
		Args json.RawMessage `json:"Args"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(res.Args) != `"echo hi"` {
		t.Fatalf("expected non-JSON string left unchanged, got %s", res.Args)
	}
}

func TestNormalizeToolInputKeys_ArrayStringIntoObjectFieldRejected(t *testing.T) {
	// A stringified array must not be unwrapped into a schema-declared object
	// field: the mismatch must stay visible to the strict decoder.
	schema := `{"type":"object","properties":{"Payload":{"type":"object"}}}`
	input := `{"Payload":"[\"a\"]"}`

	out, err := normalizeToolInputKeys(input, schema)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var res struct {
		Payload map[string]any `json:"Payload"`
	}
	if err := json.Unmarshal([]byte(out), &res); err == nil {
		t.Fatalf("expected decode error for array-into-object, got nil (out=%s)", out)
	}
}

func TestToolSpecByCallableID(t *testing.T) {
	e := &turnEngine{
		allTools: []domain.ToolSpec{
			{CallableID: "skill_use", Name: "agent_skill_use"},
		},
	}
	if got := e.toolSpecByCallableID("skill_use"); got == nil {
		t.Fatal("expected to find agent.skill.use")
	}
	if got := e.toolSpecByCallableID("missing"); got != nil {
		t.Fatal("expected nil for missing callable")
	}
}
