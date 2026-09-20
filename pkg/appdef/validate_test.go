package appdef

import (
	"strings"
	"testing"
)

func TestValidateCallableTimeoutRange(t *testing.T) {
	cases := []struct {
		name      string
		timeout   string
		wantMatch string
	}{
		{
			name:      "too_short",
			timeout:   `timeout: "29s"`,
			wantMatch: "below minimum 30s",
		},
		{
			name:      "too_long",
			timeout:   `timeout: "16m"`,
			wantMatch: "exceeds maximum 15min",
		},
		{
			name:      "minimum_ok",
			timeout:   `timeout: "30s"`,
			wantMatch: "",
		},
		{
			name:      "maximum_ok",
			timeout:   `timeout: "15m"`,
			wantMatch: "",
		},
		{
			name:      "ms_too_short",
			timeout:   `timeout_ms: 29000`,
			wantMatch: "below minimum 30s",
		},
		{
			name:      "ms_too_long",
			timeout:   `timeout_ms: 900001`,
			wantMatch: "exceeds maximum 15min",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `app Timeouts {
    id: "app.timeouts"
    version: "0.1.0"
    namespace: "timeouts"

    callable c {
        ` + tc.timeout + `
    }
}`
			app, diags, err := ParseFile(src)
			if err != nil {
				t.Fatalf("ParseFile failed: %v", err)
			}
			if len(diags) > 0 {
				t.Fatalf("unexpected parse diagnostics: %v", diags)
			}
			validateDiags := Validate(app)
			if tc.wantMatch == "" {
				for _, d := range validateDiags {
					if strings.Contains(d.Message, "timeout") {
						t.Errorf("unexpected timeout diagnostic: %s", d.Message)
					}
				}
				return
			}
			found := false
			for _, d := range validateDiags {
				if strings.Contains(d.Message, tc.wantMatch) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected diagnostic containing %q, got: %v", tc.wantMatch, validateDiags)
			}
		})
	}
}

// TestValidateStreamingRequiresResponse pins the rule that a streaming
// callable must declare a response type — chunks are Response-typed (each a
// partial of the terminal), so a void response leaves the chunks untyped.
func TestValidateStreamingRequiresResponse(t *testing.T) {
	src := `app Stream {
    id: "app.stream"
    name: "Stream"
    version: "0.1.0"
    namespace: stream

    struct Chunk {
        text: string
    }
    callable gen_text {
        response: Chunk
        streaming: true
    }
    callable bad_stream {
        streaming: true
    }
}`
	app, diags, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected parse diagnostics: %v", diags)
	}
	validateDiags := Validate(app)
	found := false
	for _, d := range validateDiags {
		if strings.Contains(d.Message, "bad_stream") && strings.Contains(d.Message, "streaming requires a response type") {
			found = true
		}
		if strings.Contains(d.Message, "gen_text") && strings.Contains(d.Message, "streaming") {
			t.Errorf("gen_text should not produce a streaming diagnostic: %s", d.Message)
		}
	}
	if !found {
		t.Errorf("expected streaming-requires-response diagnostic for bad_stream, got: %v", validateDiags)
	}
}

// TestValidateExposeVocabulary pins the closed expose vocabulary: only
// frontend/agent/both are accepted, an empty (undeclared) expose is the
// default and passes, and any other spelling fails validation.
func TestValidateExposeVocabulary(t *testing.T) {
	cases := []struct {
		name      string
		expose    string
		wantMatch string
	}{
		{name: "frontend", expose: `"frontend"`, wantMatch: ""},
		{name: "agent", expose: `"agent"`, wantMatch: ""},
		{name: "both", expose: `"both"`, wantMatch: ""},
		{name: "empty_default", expose: ``, wantMatch: ""},
		{name: "invalid", expose: `"public"`, wantMatch: `invalid expose "public"`},
		{name: "empty_string", expose: `""`, wantMatch: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exposeLine := ""
			if tc.expose != "" {
				exposeLine = "expose: " + tc.expose
			}
			src := `app Surfaces {
    id: "app.surfaces"
    version: "0.1.0"
    namespace: "surfaces"

    struct Ping {
        ok: bool
    }

    callable c {
        request:  Ping
        response: Ping
        ` + exposeLine + `
    }
}`
			app, diags, err := ParseFile(src)
			if err != nil {
				t.Fatalf("ParseFile failed: %v", err)
			}
			if len(diags) > 0 {
				t.Fatalf("unexpected parse diagnostics: %v", diags)
			}
			validateDiags := Validate(app)
			if tc.wantMatch == "" {
				for _, d := range validateDiags {
					if strings.Contains(d.Message, "expose") {
						t.Errorf("unexpected expose diagnostic: %s", d.Message)
					}
				}
				return
			}
			found := false
			for _, d := range validateDiags {
				if strings.Contains(d.Message, tc.wantMatch) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected diagnostic containing %q, got: %v", tc.wantMatch, validateDiags)
			}
		})
	}
}

// TestValidateWatchReferences pins that every watch entry must reference an
// event declared in the same .appdef — a dangling event id is a validation
// error, while declared events and an absent watch list pass.
func TestValidateWatchReferences(t *testing.T) {
	src := `app Watched {
    id: "app.watched"
    version: "0.1.0"
    namespace: "watched"

    struct Ping {
        ok: bool
    }

    event item_changed {
        payload: Ping
    }
    event item_removed {
    }

    callable good {
        request:  Ping
        response: Ping
        watch:    ["item_changed", "item_removed"]
    }
    callable dangling {
        request:  Ping
        response: Ping
        watch:    ["item_changed", "never_declared"]
    }
    callable no_watch {
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
	validateDiags := Validate(app)

	var foundDangling bool
	for _, d := range validateDiags {
		if strings.Contains(d.Message, "dangling") && strings.Contains(d.Message, `undefined event "never_declared"`) {
			foundDangling = true
		}
		if strings.Contains(d.Message, "good") && strings.Contains(d.Message, "watch") {
			t.Errorf("good should not produce a watch diagnostic: %s", d.Message)
		}
		if strings.Contains(d.Message, "no_watch") && strings.Contains(d.Message, "watch") {
			t.Errorf("no_watch should not produce a watch diagnostic: %s", d.Message)
		}
	}
	if !foundDangling {
		t.Errorf("expected dangling watch diagnostic, got: %v", validateDiags)
	}
}

// TestValidateRejectsUnknownScalarField verifies that a struct field typed
// with an unsupported scalar (e.g. float64) fails validation instead of
// silently degrading to interface{} in the generated Go code.
func TestValidateRejectsUnknownScalarField(t *testing.T) {
	src := `app ScalarTest {
    id: "app.scalar"
    name: "Scalar"
    version: "0.1.0"
    namespace: "scalar"
    struct Req {
        score: float64
        ratio: double
        labels: map<string, float>
    }
    callable get {
        request: Req
    }
}
`
	app, _, err := ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	diags := Validate(app)
	var hit bool
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown scalar") && strings.Contains(d.Message, "float64") {
			hit = true
		}
		if strings.Contains(d.Message, "Req.ratio") && strings.Contains(d.Message, "unknown") {
			t.Errorf("double should not be rejected: %s", d.Message)
		}
		if strings.Contains(d.Message, "Req.labels") && strings.Contains(d.Message, "unknown") {
			t.Errorf("float map value should not be rejected: %s", d.Message)
		}
	}
	if !hit {
		t.Fatalf("expected unknown-scalar diagnostic for float64, got: %v", diags)
	}
}
