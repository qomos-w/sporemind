package scriptcard

import (
	"fmt"
	"strings"
)

// Config is the parsed shape of a script card's data.exec block. It is
// the contract the project-side validator and the workspace-side script
// executor agree on; both sides read the same struct, so a card that
// passes static validation will load with the same budget, capability
// whitelist, and input bindings the executor hands to the spore VM.
//
// Fields are flat (Budget is not a nested struct) so callers can read
// individual scalars without going through an extra layer of indirection.
// The struct is meant to be value-copied: it carries only primitives and
// string slices, no pointers or maps, so passing it across an actor
// boundary is safe without further encoding.
type Config struct {
	// Inputs is the ordered list of upstream binding keys the script's
	// `export fun run(...)` parameters bind to. Each entry is one
	// positional parameter; runtime calls consult ClaimReq.Inputs in
	// this order. An empty slice means `fun run()` — no inputs.
	Inputs []string

	// Outputs is an optional schema name (descriptor or callable ID)
	// the script's return value is validated against. Empty disables
	// output validation; non-empty must resolve in the live schema
	// registry at claim time.
	Outputs string

	// GateCard names the upstream gate task card id that approves a
	// mutating capability. Required when any capability is mutating;
	// empty for read-only capabilities only.
	GateCard string

	// Capabilities is the per-card whitelist of callable IDs the script
	// may invoke through host.invoke. Each entry must resolve in the
	// live topology; shape validation here only enforces non-empty
	// strings — full dotted-id format and existence checks happen in
	// the executor's gate path.
	Capabilities []string

	// MaxDurationSec is the wall-clock budget (in seconds) for one
	// script invocation. Bounded to [MinMaxDurationSec, MaxMaxDurationSec]
	// when present; missing/0 falls back to DefaultMaxDurationSec.
	MaxDurationSec int

	// MaxHostCalls caps how many host.invoke calls a single script
	// invocation may issue. Bounded to [MinMaxHostCalls, MaxMaxHostCalls]
	// when present; missing/0 leaves the limit disabled.
	MaxHostCalls int

	// MaxInstructions caps the VM instruction count for one script
	// invocation. Must be >= MinMaxInstructions when present; missing/0
	// leaves the limit disabled.
	MaxInstructions int

	// MaxOutputBytes caps the script's accumulated output payload.
	// Must be >= MinMaxOutputBytes when present; missing/0 leaves the
	// limit disabled.
	MaxOutputBytes int
}

// Budget boundaries. These are exported so the static validator and the
// runtime executor can quote the same numbers in error messages and
// can be reused by UI surfaces (e.g. card editors that warn on
// out-of-range values).
const (
	// DefaultMaxDurationSec is the wall-clock budget applied when
	// data.exec.budget.max_duration_sec is absent or explicitly 0.
	DefaultMaxDurationSec = 60

	// MinMaxDurationSec is the smallest MaxDurationSec the contract
	// accepts; smaller values are rejected with a field-named error.
	MinMaxDurationSec = 1

	// MaxMaxDurationSec caps the wall-clock budget; larger values are
	// rejected so a typo (e.g. 3600 instead of 300) does not silently
	// turn a script card into a runaway.
	MaxMaxDurationSec = 300

	// MinMaxHostCalls is the smallest host.invoke cap; 0 is "limit
	// disabled" and is treated as missing.
	MinMaxHostCalls = 1

	// MaxMaxHostCalls caps the host.invoke budget.
	MaxMaxHostCalls = 64

	// MinMaxInstructions is the smallest instruction count cap; 0
	// is "limit disabled".
	MinMaxInstructions = 1

	// MinMaxOutputBytes is the smallest output payload cap; 0 is
	// "limit disabled".
	MinMaxOutputBytes = 1
)

// ParseConfig parses the data.exec sub-block of a script task card into
// a Config value. The data argument is expected to be the entire
// data:... frontmatter map (not just the exec slice); ParseConfig looks
// up the "exec" key internally so callers can pass the frontmatter map
// directly without re-extracting the sub-block.
//
// A missing "exec" key returns an error — the script kind mandates an
// exec block. A present-but-wrong-shape exec (e.g. a list instead of a
// mapping) is also an error. Within the exec block, the inputs /
// outputs / gate_card / capabilities / budget sub-fields are all
// optional (the script declares no inputs by default; an absent budget
// disables every optional cap and falls back to DefaultMaxDurationSec
// for MaxDurationSec).
//
// Out-of-range budget values return an error naming the offending
// field. The error is built with %w wrapping so callers can use
// errors.Is/As against a sentinel if needed.
func ParseConfig(data map[string]any) (Config, error) {
	if data == nil {
		return Config{}, fmt.Errorf("data.exec: data map is nil")
	}
	execBlock, present := data["exec"]
	if !present || execBlock == nil {
		return Config{}, fmt.Errorf("data.exec is required for script cards")
	}
	m, ok := execBlock.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("data.exec must be a mapping, got %T", execBlock)
	}
	return parseExec(m)
}

// parseExec parses a pre-extracted exec block. The split between
// ParseConfig (looks up "exec" in a data map) and parseExec (takes the
// inner map directly) lets callers that already have the inner map
// skip the lookup without going through fmt-typed any-assertions of
// their own.
func parseExec(exec map[string]any) (Config, error) {
	cfg := Config{}

	// Inputs: ordered list of non-empty, unique strings. Missing or
	// nil is allowed (script declares no inputs). A wrong shape
	// (string, map, number) is a hard error.
	inputs, err := parseInputs(exec["inputs"])
	if err != nil {
		return Config{}, fmt.Errorf("data.exec.inputs: %w", err)
	}
	cfg.Inputs = inputs

	// Outputs: single string (descriptor or callable id). Missing or
	// nil becomes "". A non-string value is an error.
	if raw, ok := exec["outputs"]; ok && raw != nil {
		out, ok := raw.(string)
		if !ok {
			return Config{}, fmt.Errorf("data.exec.outputs: must be a single string, got %T", raw)
		}
		cfg.Outputs = strings.TrimSpace(out)
	}

	// Gate card: single string. Same shape rules as outputs.
	if raw, ok := exec["gate_card"]; ok && raw != nil {
		gate, ok := raw.(string)
		if !ok {
			return Config{}, fmt.Errorf("data.exec.gate_card: must be a single string, got %T", raw)
		}
		cfg.GateCard = strings.TrimSpace(gate)
	}

	// Capabilities: list of strings. Missing/nil is allowed. A wrong
	// shape is an error; per-entry duplicates and blank entries are
	// tolerated (the executor de-dupes via the gate whitelist).
	caps, err := parseCapabilities(exec["capabilities"])
	if err != nil {
		return Config{}, fmt.Errorf("data.exec.capabilities: %w", err)
	}
	cfg.Capabilities = caps

	// Budget: nested map with four optional integer-valued keys. Wrong
	// shape on the map itself is an error; each key's range check
	// produces a field-named error.
	budgetRaw, present := exec["budget"]
	if present && budgetRaw != nil {
		budget, ok := budgetRaw.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("data.exec.budget: must be a mapping, got %T", budgetRaw)
		}
		if err := applyBudget(&cfg, budget); err != nil {
			return Config{}, err
		}
	}

	// MaxDurationSec falls back to the default when absent or zero;
	// the other three keep 0 = "disabled".
	if cfg.MaxDurationSec == 0 {
		cfg.MaxDurationSec = DefaultMaxDurationSec
	}

	return cfg, nil
}

// applyBudget mutates cfg with the four budget fields from m. Each
// field is independent: an error on one stops parsing and the
// partially-decoded cfg is discarded by the caller. The map keys
// mirror the YAML/dict keys exactly so error messages quote the same
// name the card author sees.
func applyBudget(cfg *Config, m map[string]any) error {
	if raw, ok := m["max_duration_sec"]; ok {
		n, err := parseBudgetInt(raw)
		if err != nil {
			return fmt.Errorf("data.exec.budget.max_duration_sec: %w", err)
		}
		if n != 0 && (n < MinMaxDurationSec || n > MaxMaxDurationSec) {
			return fmt.Errorf("data.exec.budget.max_duration_sec=%d is out of range [%d, %d]", n, MinMaxDurationSec, MaxMaxDurationSec)
		}
		cfg.MaxDurationSec = n
	}
	if raw, ok := m["max_host_calls"]; ok {
		n, err := parseBudgetInt(raw)
		if err != nil {
			return fmt.Errorf("data.exec.budget.max_host_calls: %w", err)
		}
		if n != 0 && (n < MinMaxHostCalls || n > MaxMaxHostCalls) {
			return fmt.Errorf("data.exec.budget.max_host_calls=%d is out of range [%d, %d]", n, MinMaxHostCalls, MaxMaxHostCalls)
		}
		cfg.MaxHostCalls = n
	}
	if raw, ok := m["max_instructions"]; ok {
		n, err := parseBudgetInt(raw)
		if err != nil {
			return fmt.Errorf("data.exec.budget.max_instructions: %w", err)
		}
		if n != 0 && n < MinMaxInstructions {
			return fmt.Errorf("data.exec.budget.max_instructions=%d is below the minimum %d", n, MinMaxInstructions)
		}
		cfg.MaxInstructions = n
	}
	if raw, ok := m["max_output_bytes"]; ok {
		n, err := parseBudgetInt(raw)
		if err != nil {
			return fmt.Errorf("data.exec.budget.max_output_bytes: %w", err)
		}
		if n != 0 && n < MinMaxOutputBytes {
			return fmt.Errorf("data.exec.budget.max_output_bytes=%d is below the minimum %d", n, MinMaxOutputBytes)
		}
		cfg.MaxOutputBytes = n
	}
	return nil
}

// parseInputs decodes data.exec.inputs into a []string. Accepted shapes:
//
//   - nil / absent    -> (nil, nil)         (no inputs declared)
//   - []any of string -> ([]string, nil)
//   - []string        -> (cloned, nil)
//
// Any other shape (a map, a single string, a number) is rejected: the
// YAML spelling for inputs is a sequence of strings, never a scalar.
// Empty entries within a non-empty list are rejected because the
// runtime binding by name would lose ordering; duplicates are also
// rejected because they double-bind a single positional parameter.
func parseInputs(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		seen := make(map[string]struct{}, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d is not a string (got %T)", i, item)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, fmt.Errorf("entry %d is empty", i)
			}
			if _, dup := seen[s]; dup {
				return nil, fmt.Errorf("entry %d duplicates %q", i, s)
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(v))
		seen := make(map[string]struct{}, len(v))
		for i, s := range v {
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, fmt.Errorf("entry %d is empty", i)
			}
			if _, dup := seen[s]; dup {
				return nil, fmt.Errorf("entry %d duplicates %q", i, s)
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected list of strings, got %T", raw)
	}
}

// parseCapabilities decodes data.exec.capabilities into a []string.
// Missing / nil -> (nil, nil). Empty strings inside the list are
// skipped silently (the inline parser collapses an empty YAML block
// into the empty string "", and the user can pad the list with blank
// lines without breaking the contract). Duplicates are tolerated — the
// executor's gate path de-dupes — so a card author can list the same
// capability under two aliases without an error here.
func parseCapabilities(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d is not a string (got %T)", i, item)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected list of strings, got %T", raw)
	}
}

// parseBudgetInt coerces a YAML / JSON numeric into an int. The shapes
// supported mirror what the inline frontmatter parser produces:
//
//   - int / int32 / int64     -> value
//   - float64 (whole-number)  -> value   (float is a side effect of JSON
//   |                                  |    decoding numbers without an
//   |                                  |    explicit type)
//   - string of decimal digits -> value  (YAML may quote scalars)
//
// Booleans, partial digits, and out-of-range integers are rejected
// with a field-agnostic error so applyBudget can wrap it with the
// concrete budget key.
func parseBudgetInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("not an integer: %v", v)
		}
		return int(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, nil
		}
		// Disallow signs and exponents: budget values are non-negative
		// whole integers; -1 is not a meaningful "disabled" because
		// 0 is reserved for that.
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, fmt.Errorf("not an unsigned integer: %q", s)
			}
		}
		// Reject leading zeros (e.g. "00") — YAML quoting lets users
		// ship those by accident and they rarely mean anything sensible.
		if len(s) > 1 && s[0] == '0' {
			return 0, fmt.Errorf("leading-zero integer: %q", s)
		}
		var n int
		for i := 0; i < len(s); i++ {
			n = n*10 + int(s[i]-'0')
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", raw)
	}
}