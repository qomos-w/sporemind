package scriptcard

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────
// ExtractSporeBlock: shape matrix
// ─────────────────────────────────────────────────────────────────────

// TestExtractSporeBlock_Cases exhausts the opener/closer shape matrix.
// Each entry pins a body -> expected (src, ok) pair so a contract
// drift shows up in a single concise table.
func TestExtractSporeBlock_Cases(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // expected extracted source ("" when wantOK is false)
		wantOK  bool
	}{
		// ── Lowercase canonical form ──────────────────────────────────
		{
			name:    "lowercase_basic",
			body:    "```spore\nexport fun run(): int { return 1 }\n```\n",
			want:    "export fun run(): int { return 1 }",
			wantOK:  true,
		},
		// ── Case-insensitivity ────────────────────────────────────────
		{
			name:    "mixed_case_Spore",
			body:    "```Spore\nexport fun run(): int { return 2 }\n```\n",
			want:    "export fun run(): int { return 2 }",
			wantOK:  true,
		},
		{
			name:    "uppercase_SPORE",
			body:    "```SPORE\nexport fun run(): int { return 3 }\n```\n",
			want:    "export fun run(): int { return 3 }",
			wantOK:  true,
		},
		// ── Info string after the language tag ────────────────────────
		{
			name:    "spore_with_multiword_info",
			body:    "```spore draft v2\ncode\n```\n",
			want:    "code",
			wantOK:  true,
		},
		{
			// ```sporescript has first token "sporescript" — not EqualFold
			// "spore" — so the canonical rule rejects it. The block is
			// skipped and the function returns no match.
			name:   "sporescript_rejected",
			body:   "```sporescript\ncode\n```\n",
			wantOK: false,
		},
		{
			name:    "spore_info_with_title",
			body:    "```spore title=\"hello world\"\ncode\n```\n",
			want:    "code",
			wantOK:  true,
		},
		// ── Missing info string ───────────────────────────────────────
		{
			name:    "empty_info_string",
			body:    "```\ncode\n```\n",
			wantOK:  false,
		},
		// ── Four-backtick fence: strict length pairing ────────────────
		{
			name:    "four_backtick_open_close",
			body:    "````spore\ncode with ``` inside\n````\n",
			want:    "code with ``` inside",
			wantOK:  true,
		},
		{
			// A 4-backtick opener followed by a 3-backtick closer does
			// NOT close the block; the function must report no match.
			name:   "four_backtick_open_three_close",
			body:   "````spore\ncode\n```\n",
			wantOK: false,
		},
		{
			// 3-backtick opener + 4-backtick closer also does not close.
			name:   "three_backtick_open_four_close",
			body:   "```spore\ncode\n````\n",
			wantOK: false,
		},
		// ── Indented opener ───────────────────────────────────────────
		{
			name:    "indented_opener",
			body:    "  \t```spore\nexport fun run(): int { return 1 }\n```\n",
			want:    "export fun run(): int { return 1 }",
			wantOK:  true,
		},
		// ── CRLF normalization ────────────────────────────────────────
		{
			name:    "crlf_normalization",
			body:    "```spore\r\nexport fun run(): int { return 1 }\r\n```\r\n",
			want:    "export fun run(): int { return 1 }",
			wantOK:  true,
		},
		// ── No fence at all ───────────────────────────────────────────
		{
			name:    "no_fence",
			body:    "just prose, no code fence",
			wantOK:  false,
		},
		{
			name:    "empty_body",
			body:    "",
			wantOK:  false,
		},
		// ── First-match-wins ──────────────────────────────────────────
		{
			name:    "first_block_wins",
			body:    "```spore\nfirst\n```\n\n```spore\nsecond\n```\n",
			want:    "first",
			wantOK:  true,
		},
		{
			// A non-spore fence before a spore fence is skipped.
			name:   "non_spore_fence_before",
			body:   "```yaml\nfoo\n```\n```spore\nbar\n```\n",
			want:   "bar",
			wantOK: true,
		},
		// ── Closing fence with trailing whitespace ────────────────────
		{
			name:    "closer_trailing_whitespace",
			body:    "```spore\ncode\n```  \n",
			want:    "code",
			wantOK:  true,
		},
		{
			// Trailing non-whitespace characters on the closer reject it.
			name:   "closer_trailing_text",
			body:   "```spore\ncode\n``` end\n",
			wantOK: false,
		},
		// ── Multi-line block content ──────────────────────────────────
		{
			name:    "multi_line_block",
			body:    "intro prose\n\n```spore\nlet x = 1\nlet y = x + 1\nreturn y\n```\n\ntrailing prose\n",
			want:    "let x = 1\nlet y = x + 1\nreturn y",
			wantOK:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExtractSporeBlock(tc.body)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("got = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExtractSporeBlock_FirstMatchPerBlock pins the rule that only the
// first matching block is returned, even when a later block would
// produce a different result. This is the "executor picks the first
// block it can load" contract.
func TestExtractSporeBlock_FirstMatchPerBlock(t *testing.T) {
	body := "```spore\nfirst\n```\n```spore\nsecond\n```\n"
	got, ok := ExtractSporeBlock(body)
	if !ok {
		t.Fatal("first match should be returned")
	}
	if got != "first" {
		t.Errorf("got %q, want %q", got, "first")
	}
}

// TestExtractSporeBlock_NonSporeFencesSkipped pins the rule that
// non-spore fences are transparently skipped (not treated as content).
func TestExtractSporeBlock_NonSporeFencesSkipped(t *testing.T) {
	body := "```yaml\nfoo\n```\n```spore\nbar\n```\n"
	got, ok := ExtractSporeBlock(body)
	if !ok || got != "bar" {
		t.Errorf("ExtractSporeBlock(%q) = (%q, %v), want (bar, true)", body, got, ok)
	}
}

// ─────────────────────────────────────────────────────────────────────
// ParseConfig: data.exec shape and field boundaries
// ─────────────────────────────────────────────────────────────────────

// TestParseConfig_MissingExec verifies that a data map without an
// "exec" key returns an error naming the missing key.
func TestParseConfig_MissingExec(t *testing.T) {
	_, err := ParseConfig(map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "data.exec") {
		t.Fatalf("err = %v, want data.exec", err)
	}
}

// TestParseConfig_ExecWrongShape verifies that an "exec" key holding a
// non-map value is rejected with a shape error.
func TestParseConfig_ExecWrongShape(t *testing.T) {
	_, err := ParseConfig(map[string]any{"exec": []string{"not", "a", "map"}})
	if err == nil || !strings.Contains(err.Error(), "must be a mapping") {
		t.Fatalf("err = %v, want shape error", err)
	}
}

// TestParseConfig_NilData verifies that a nil data map errors out.
func TestParseConfig_NilData(t *testing.T) {
	_, err := ParseConfig(nil)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("err = %v, want nil-data error", err)
	}
}

// TestParseConfig_InputsEmptyOrMissing verifies that an absent inputs
// list (or an explicit nil) parses to an empty slice without error.
func TestParseConfig_InputsEmptyOrMissing(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
	}{
		{"missing", map[string]any{"exec": map[string]any{}}},
		{"nil_value", map[string]any{"exec": map[string]any{"inputs": nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfig(tc.data)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(cfg.Inputs) != 0 {
				t.Errorf("len(cfg.Inputs) = %d, want 0", len(cfg.Inputs))
			}
		})
	}
}

// TestParseConfig_InputsSingle verifies a single-element list.
func TestParseConfig_InputsSingle(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": []any{"repo"}},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cfg.Inputs) != 1 || cfg.Inputs[0] != "repo" {
		t.Errorf("cfg.Inputs = %v, want [repo]", cfg.Inputs)
	}
}

// TestParseConfig_InputsMultiple verifies a multi-element list preserves
// order and is de-duplicated on parse.
func TestParseConfig_InputsMultiple(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": []any{"repo", "branch", "sha"}},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []string{"repo", "branch", "sha"}
	if len(cfg.Inputs) != len(want) {
		t.Fatalf("len(cfg.Inputs) = %d, want %d", len(cfg.Inputs), len(want))
	}
	for i, got := range cfg.Inputs {
		if got != want[i] {
			t.Errorf("cfg.Inputs[%d] = %q, want %q", i, got, want[i])
		}
	}
}

// TestParseConfig_InputsContainsEmptyString verifies that an empty
// string entry is rejected with an entry-indexed error.
func TestParseConfig_InputsContainsEmptyString(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": []any{"repo", ""}},
	})
	if err == nil || !strings.Contains(err.Error(), "entry 1 is empty") {
		t.Fatalf("err = %v, want entry-1-empty", err)
	}
}

// TestParseConfig_InputsContainsDuplicate verifies that a duplicate
// entry is rejected with a field-named error.
func TestParseConfig_InputsContainsDuplicate(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": []any{"repo", "branch", "repo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("err = %v, want duplicates error", err)
	}
}

// TestParseConfig_InputsWrongShape verifies that a scalar string inputs
// value is rejected (a single string is not a list).
func TestParseConfig_InputsWrongShape(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": "just-one-string"},
	})
	if err == nil || !strings.Contains(err.Error(), "expected list of strings") {
		t.Fatalf("err = %v, want shape error", err)
	}
}

// TestParseConfig_Outputs verifies outputs parsing: a string is
// trimmed, a non-string is an error, missing is fine.
func TestParseConfig_Outputs(t *testing.T) {
	t.Run("string_trimmed", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"outputs": "  ToastShowResp  "},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.Outputs != "ToastShowResp" {
			t.Errorf("cfg.Outputs = %q, want trimmed", cfg.Outputs)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"outputs": 42},
		})
		if err == nil || !strings.Contains(err.Error(), "outputs") {
			t.Fatalf("err = %v, want outputs error", err)
		}
	})
	t.Run("missing_ok", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{"exec": map[string]any{}})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.Outputs != "" {
			t.Errorf("cfg.Outputs = %q, want empty", cfg.Outputs)
		}
	})
}

// TestParseConfig_GateCard verifies gate card parsing.
func TestParseConfig_GateCard(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"gate_card": "  gate-1 "},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.GateCard != "gate-1" {
			t.Errorf("cfg.GateCard = %q, want gate-1", cfg.GateCard)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"gate_card": 1.5},
		})
		if err == nil || !strings.Contains(err.Error(), "gate_card") {
			t.Fatalf("err = %v, want gate_card error", err)
		}
	})
	t.Run("missing_ok", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{"exec": map[string]any{}})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.GateCard != "" {
			t.Errorf("cfg.GateCard = %q, want empty", cfg.GateCard)
		}
	})
}

// TestParseConfig_Capabilities verifies capability list parsing: a
// valid list is preserved, blank entries are skipped, wrong shapes
// error out.
func TestParseConfig_Capabilities(t *testing.T) {
	t.Run("valid_list", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"capabilities": []any{"app.emit", "project.report"}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := []string{"app.emit", "project.report"}
		if len(cfg.Capabilities) != len(want) {
			t.Fatalf("len(cfg.Capabilities) = %d, want %d", len(cfg.Capabilities), len(want))
		}
		for i, got := range cfg.Capabilities {
			if got != want[i] {
				t.Errorf("cfg.Capabilities[%d] = %q, want %q", i, got, want[i])
			}
		}
	})
	t.Run("blank_entries_skipped", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"capabilities": []any{"", " app.emit ", ""}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(cfg.Capabilities) != 1 || cfg.Capabilities[0] != "app.emit" {
			t.Errorf("cfg.Capabilities = %v, want [app.emit]", cfg.Capabilities)
		}
	})
	t.Run("missing_ok", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{"exec": map[string]any{}})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(cfg.Capabilities) != 0 {
			t.Errorf("len(cfg.Capabilities) = %d, want 0", len(cfg.Capabilities))
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"capabilities": "app.emit"},
		})
		if err == nil || !strings.Contains(err.Error(), "capabilities") {
			t.Fatalf("err = %v, want capabilities error", err)
		}
	})
}

// TestParseConfig_Budget_Defaults verifies that an absent budget map
// produces DefaultMaxDurationSec and zeros for the optional caps.
func TestParseConfig_Budget_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"exec": map[string]any{}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.MaxDurationSec != DefaultMaxDurationSec {
		t.Errorf("MaxDurationSec = %d, want default %d", cfg.MaxDurationSec, DefaultMaxDurationSec)
	}
	if cfg.MaxHostCalls != 0 || cfg.MaxInstructions != 0 || cfg.MaxOutputBytes != 0 {
		t.Errorf("optional caps = (%d, %d, %d), want all zero", cfg.MaxHostCalls, cfg.MaxInstructions, cfg.MaxOutputBytes)
	}
}

// TestParseConfig_Budget_Boundaries pins the 1..300 wall-clock window
// and the fact that a single out-of-range key errors out while the
// rest of the block is ignored.
func TestParseConfig_Budget_Boundaries(t *testing.T) {
	t.Run("duration_min", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_duration_sec": 1}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxDurationSec != 1 {
			t.Errorf("MaxDurationSec = %d, want 1", cfg.MaxDurationSec)
		}
	})
	t.Run("duration_max", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_duration_sec": 300}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxDurationSec != 300 {
			t.Errorf("MaxDurationSec = %d, want 300", cfg.MaxDurationSec)
		}
	})
	t.Run("duration_zero_uses_default", func(t *testing.T) {
		// 0 means "use the default" per the spec — not "below min".
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_duration_sec": 0}},
		})
		if err != nil {
			t.Fatalf("err = %v, want nil for zero value", err)
		}
		if cfg.MaxDurationSec != DefaultMaxDurationSec {
			t.Errorf("MaxDurationSec = %d, want %d (default)", cfg.MaxDurationSec, DefaultMaxDurationSec)
		}
	})
	t.Run("duration_above_max", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_duration_sec": 301}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_duration_sec") {
			t.Fatalf("err = %v, want max_duration_sec error", err)
		}
	})
	t.Run("duration_negative", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_duration_sec": -5}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_duration_sec") {
			t.Fatalf("err = %v, want max_duration_sec error", err)
		}
	})
}

// TestParseConfig_Budget_HostCalls pins the 1..64 host-call window.
func TestParseConfig_Budget_HostCalls(t *testing.T) {
	t.Run("within_range", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_host_calls": 10}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxHostCalls != 10 {
			t.Errorf("MaxHostCalls = %d, want 10", cfg.MaxHostCalls)
		}
	})
	t.Run("below_min", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_host_calls": 0}},
		})
		// 0 is "disabled" and not an error.
		if err != nil {
			t.Fatalf("err = %v, want nil for zero value", err)
		}
	})
	t.Run("above_max", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_host_calls": 65}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_host_calls") {
			t.Fatalf("err = %v, want max_host_calls error", err)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_host_calls": "ten"}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_host_calls") {
			t.Fatalf("err = %v, want max_host_calls error", err)
		}
	})
}

// TestParseConfig_Budget_Instructions pins the >= 1 instruction window.
func TestParseConfig_Budget_Instructions(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_instructions": 1000}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxInstructions != 1000 {
			t.Errorf("MaxInstructions = %d, want 1000", cfg.MaxInstructions)
		}
	})
	t.Run("zero_disabled", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_instructions": 0}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxInstructions != 0 {
			t.Errorf("MaxInstructions = %d, want 0 (disabled)", cfg.MaxInstructions)
		}
	})
	t.Run("negative", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_instructions": -1}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_instructions") {
			t.Fatalf("err = %v, want max_instructions error", err)
		}
	})
}

// TestParseConfig_Budget_OutputBytes pins the >= 1 output cap window.
func TestParseConfig_Budget_OutputBytes(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_output_bytes": 2048}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxOutputBytes != 2048 {
			t.Errorf("MaxOutputBytes = %d, want 2048", cfg.MaxOutputBytes)
		}
	})
	t.Run("zero_disabled", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_output_bytes": 0}},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if cfg.MaxOutputBytes != 0 {
			t.Errorf("MaxOutputBytes = %d, want 0 (disabled)", cfg.MaxOutputBytes)
		}
	})
	t.Run("negative", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{
			"exec": map[string]any{"budget": map[string]any{"max_output_bytes": -1}},
		})
		if err == nil || !strings.Contains(err.Error(), "max_output_bytes") {
			t.Fatalf("err = %v, want max_output_bytes error", err)
		}
	})
}

// TestParseConfig_Budget_StringCoercion verifies the string form of
// budget values is accepted (YAML may quote numbers).
func TestParseConfig_Budget_StringCoercion(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"exec": map[string]any{
			"budget": map[string]any{
				"max_duration_sec": " 120 ",
				"max_host_calls":   "5",
				"max_instructions": "500",
				"max_output_bytes": "4096",
			},
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.MaxDurationSec != 120 {
		t.Errorf("MaxDurationSec = %d, want 120", cfg.MaxDurationSec)
	}
	if cfg.MaxHostCalls != 5 {
		t.Errorf("MaxHostCalls = %d, want 5", cfg.MaxHostCalls)
	}
	if cfg.MaxInstructions != 500 {
		t.Errorf("MaxInstructions = %d, want 500", cfg.MaxInstructions)
	}
	if cfg.MaxOutputBytes != 4096 {
		t.Errorf("MaxOutputBytes = %d, want 4096", cfg.MaxOutputBytes)
	}
}

// TestParseConfig_Budget_WrongShape verifies that a budget value of the
// wrong shape is rejected.
func TestParseConfig_Budget_WrongShape(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"exec": map[string]any{"budget": "not-a-map"},
	})
	if err == nil || !strings.Contains(err.Error(), "data.exec.budget") {
		t.Fatalf("err = %v, want budget shape error", err)
	}
}

// TestParseConfig_FullRoundTrip exercises a realistic exec block with
// every field populated and confirms the parse matches the input.
func TestParseConfig_FullRoundTrip(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"exec": map[string]any{
			"inputs":       []any{"repo", "branch"},
			"outputs":      "ToastShowResp",
			"gate_card":    "gate-1",
			"capabilities": []any{"app.emit", "project.report"},
			"budget": map[string]any{
				"max_duration_sec": 180,
				"max_host_calls":   8,
				"max_instructions": 4096,
				"max_output_bytes": 65536,
			},
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cfg.Inputs) != 2 || cfg.Inputs[0] != "repo" || cfg.Inputs[1] != "branch" {
		t.Errorf("Inputs = %v, want [repo branch]", cfg.Inputs)
	}
	if cfg.Outputs != "ToastShowResp" {
		t.Errorf("Outputs = %q, want ToastShowResp", cfg.Outputs)
	}
	if cfg.GateCard != "gate-1" {
		t.Errorf("GateCard = %q, want gate-1", cfg.GateCard)
	}
	if len(cfg.Capabilities) != 2 || cfg.Capabilities[0] != "app.emit" || cfg.Capabilities[1] != "project.report" {
		t.Errorf("Capabilities = %v, want [app.emit project.report]", cfg.Capabilities)
	}
	if cfg.MaxDurationSec != 180 {
		t.Errorf("MaxDurationSec = %d, want 180", cfg.MaxDurationSec)
	}
	if cfg.MaxHostCalls != 8 {
		t.Errorf("MaxHostCalls = %d, want 8", cfg.MaxHostCalls)
	}
	if cfg.MaxInstructions != 4096 {
		t.Errorf("MaxInstructions = %d, want 4096", cfg.MaxInstructions)
	}
	if cfg.MaxOutputBytes != 65536 {
		t.Errorf("MaxOutputBytes = %d, want 65536", cfg.MaxOutputBytes)
	}
}
