package project

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

// truncateForError caps a user-supplied string echoed into an error message,
// following the openai respSnippet truncation convention. Card ids have no
// length limit at the schema level, so error paths must not assume one.
func truncateForError(s string) string {
	if len(s) > 512 {
		return s[:512] + "...(truncated)"
	}
	return s
}

// CardTypeValidator validates type-specific constraints for a decoded card.
// Universal constraints (canonical type, forbidden tags, parent-in-tags) are
// handled centrally by validateCardStructured; implementers only need to
// enforce rules that are specific to their card type.
type CardTypeValidator interface {
	// Validate returns type-specific errors for the given card.
	Validate(card *CardRecord) []gen.CardValidationError
}

// cardTypeValidators maps canonical card types to their validators.
// Register new type-specific validators here.
// validateCard is the canonical structural validator run on every card before
// it is persisted. It enforces:
//   - the top-level type is a member of the canonical type set (legacy types
//     are normalized; unknown types are rejected);
//   - type-specific constraints via the cardTypeValidators registry;
//   - for user cards (non-system ids): no builtin/__builtin_* tags, and the
//     parent field, when set, must appear in the card's tags.
//
// System cards (builtin virtual nodes, agent/skill/prompt/scheduler ids) are
// exempt from the tag/parent constraints because they own the reserved tags
// and define the hierarchy.
func validateCard(id, raw string) error {
	errs := validateCardStructured(id, raw)
	if len(errs) > 0 {
		return fmt.Errorf("card %q: %s", id, errs[0].Message)
	}
	return nil
}

// validateCardStructured performs the same checks as validateCard but returns
// all violations as structured errors. This powers project.wiki.validateCard
// and the frontend repair mode.
func validateCardStructured(id, raw string) []gen.CardValidationError {
	var errs []gen.CardValidationError

	if !hasFrontmatter(raw) {
		errs = append(errs, gen.CardValidationError{
			Code:    "missing_frontmatter",
			Field:   "-",
			Message: "card frontmatter is missing or malformed",
		})
		return errs
	}

	card := repairCardRecord(decodeCard(id, raw))
	cardType := normalizeCardType(card.Type)

	if !isCanonicalCardType(card.Type) {
		errs = append(errs, gen.CardValidationError{
			Code:    "invalid_type",
			Field:   "type",
			Message: fmt.Sprintf("invalid type %q", card.Type),
		})
	}

	if v, ok := cardTypeValidators[cardType]; ok {
		errs = append(errs, v.Validate(card)...)
	}

	if isSystemCardID(id) {
		return errs
	}

	for _, tag := range card.Tags {
		if tag == "builtin" || strings.HasPrefix(tag, builtinCardPrefix) {
			errs = append(errs, gen.CardValidationError{
				Code:    "forbidden_tag",
				Field:   "tags",
				Message: fmt.Sprintf("forbidden tag %q (builtin tags are reserved)", tag),
			})
		}
	}

	if card.Parent != "" && card.Parent != id && !containsString(card.Tags, card.Parent) {
		errs = append(errs, gen.CardValidationError{
			Code:    "parent_not_in_tags",
			Field:   "parent",
			Message: fmt.Sprintf("parent %q must be one of the card's tags", card.Parent),
		})
	}

	return errs
}

// knownExecKinds is the static allowlist of execKind values that a
// task card's frontmatter may declare. The actual executor function
// lives in the workspace's execKind → executor registry (built per-
// workspace); this list is the project-side gate for the declaration
// shape. New kinds (crawl, sub_map, ...) are added here AND to the
// registry in lockstep so the project validator and the runtime
// dispatcher stay in agreement.
//
// Today only worker_task is registered; the dispatcher maps the
// empty / missing kind to DefaultExecKind (= worker_task) so legacy
// cards continue to take the existing spawn-worker path bit-for-bit.
// validateCardExecKind enforces that data.exec.kind (when declared on
// a task card) is either empty (= use DefaultExecKind) or in the
// knownExecKinds allowlist. The declaration shape itself is parsed by
// the project's inline frontmatter parser (parseDataBlock → map); we
// only inspect the value here. Missing data.exec or empty kind both
// pass — they signal "use the default" and let the workspace's
// dispatcher fill in DefaultExecKind. A declared but unknown kind is
// a hard error so the card can be saved as todo but never claims
// (the dispatcher will surface ErrUnknownExecKind at advance time).
func validateCardExecKind(card *CardRecord) []gen.CardValidationError {
	if card == nil || card.Data == nil {
		return nil
	}
	execBlock, ok := card.Data["exec"].(map[string]any)
	if !ok {
		// data.exec absent — fall back to DefaultExecKind.
		return nil
	}
	rawKind, _ := execBlock["kind"].(string)
	kind := strings.TrimSpace(rawKind)
	if kind == "" {
		// data.exec declared but kind is empty — also fall back.
		return nil
	}
	if _, known := knownExecKinds[kind]; !known {
		return []gen.CardValidationError{{
			Code:    "task_exec_kind_unknown",
			Field:   "data.exec.kind",
			Message: fmt.Sprintf("exec kind %q is not registered (known: %s)", kind, knownExecKindsList()),
		}}
	}
	return nil
}

// validateCardIO enforces the data.io declaration shape on task cards:
//
//	data.io:
//	  callable: <dotted callable ID>   # required, non-empty
//	  description: <string>            # optional guidance for LLM arg generation
//
// The IO contract turns the card into a turn-end forced toolcall: when the
// owning agent's turn is about to complete, the turn engine performs one extra
// LLM round trip with ToolChoice pinned to the callable and executes the
// result (see pkg/actor/agent/turn_engine_forced_io.go). Only agent-driven
// exec kinds can honor it — a card declaring a deterministic workspace
// executor (crawl / toolcall) never runs agent turns, so the combination is
// rejected rather than silently never firing. The callable's existence and
// schema are resolved at runtime from the topology registry; the card carries
// only the ID.
func validateCardIO(card *CardRecord) []gen.CardValidationError {
	if card == nil || card.Data == nil {
		return nil
	}
	raw, present := card.Data["io"]
	if !present || raw == nil {
		return nil
	}
	ioBlock, ok := raw.(map[string]any)
	if !ok {
		return []gen.CardValidationError{{
			Code:    "task_io_invalid",
			Field:   "data.io",
			Message: "data.io must be a mapping of {callable, description?}",
		}}
	}
	callable, _ := ioBlock["callable"].(string)
	if strings.TrimSpace(callable) == "" {
		return []gen.CardValidationError{{
			Code:    "task_io_callable_missing",
			Field:   "data.io.callable",
			Message: "data.io.callable is required and must be the callable's dotted ID",
		}}
	}
	if desc, exists := ioBlock["description"]; exists {
		if _, ok := desc.(string); !ok {
			return []gen.CardValidationError{{
				Code:    "task_io_description_invalid",
				Field:   "data.io.description",
				Message: "data.io.description must be a string",
			}}
		}
	}
	execBlock, _ := card.Data["exec"].(map[string]any)
	kind, _ := execBlock["kind"].(string)
	if kind == "crawl" || kind == "toolcall" || kind == "script" {
		return []gen.CardValidationError{{
			Code:  "task_io_exec_conflict",
			Field: "data.io",
			Message: fmt.Sprintf(
				"data.io (turn-end forced toolcall) cannot be combined with the deterministic exec kind %q; leave data.exec empty (worker_task) instead",
				kind),
		}}
	}
	return nil
}

// ExecKindScript is the exec kind for inline Spore script execution.
// The card body must carry at least one ```spore fenced block; the
// workspace-level script executor compiles the first such block per
// claim (card ID + content hash keyed cache) and invokes
// `export fun run(inputs...)` against the merged data-flow bindings.
// Registered in the workspace's execKind → executor registry alongside
// worker_task / sub_map / crawl / event_wait / gate; this validator
// enforces the static frontmatter shape and runs a compile-only probe
// over the fenced source so a syntax error surfaces at save time.
const ExecKindScript = "script"

// validateCardScript enforces the data.exec.kind = script shape on task
// cards. Returns nil when the kind is anything other than "script" so
// other exec kinds are unaffected. The script executor consumes the
// declared fields at claim time; this layer only checks their static
// shape (presence, types, ranges) without resolving callable existence
// or topology membership.
//
//	data.exec:
//	  kind: script                            # required (set by caller)
//	  capabilities:                           # required, non-empty array
//	    - <dotted callable ID>                # shape-checked; e.g. app.emit_state
//	  inputs:                                 # optional, ordered string list
//	    - <name>                              # e.g. repo
//	  outputs: <descriptor or callable ID>    # optional, single string
//	  budget:                                 # optional
//	    max_duration_sec: 1..300              # 0/缺失 -> DefaultMaxDurationSec
//	    max_host_calls: 1..64                 # 0/缺失 -> disabled
//	    max_instructions: >=1                 # 0/缺失 -> disabled
//	    max_output_bytes: >=1                 # 0/缺失 -> disabled
//	  gate_card: <card ID>                    # optional, single string
//
// Shape, range, and fence rules are delegated to pkg/scriptcard (the
// canonical contract shared with the workspace-side executor) so both
// sides agree on the same boundaries; only the dotted-ID syntax for
// capabilities stays local because the topology registry is not
// reachable from the static layer.
//
// The card body must contain at least one ```spore fenced block (the
// executor loads the first match as the script source); the existence
// check goes through scriptcard.ExtractSporeBlock. When a fence is
// present, the extracted source is additionally fed through a
// compile-only probe (probeScriptCompile) so a syntax error surfaces
// here at save time instead of at claim-time preflight.
func validateCardScript(card *CardRecord) []gen.CardValidationError {
	if card == nil || card.Data == nil {
		return nil
	}
	execBlock, ok := card.Data["exec"].(map[string]any)
	if !ok {
		return nil
	}
	kind, _ := execBlock["kind"].(string)
	if strings.TrimSpace(kind) != ExecKindScript {
		return nil
	}

	var errs []gen.CardValidationError
	errs = append(errs, validateScriptCapabilities(execBlock)...)
	errs = append(errs, validateScriptInputs(execBlock)...)
	errs = append(errs, validateScriptOutputs(execBlock)...)
	errs = append(errs, validateScriptBudget(execBlock)...)
	errs = append(errs, validateScriptGateCard(execBlock)...)
	src, fenceOK := scriptcard.ExtractSporeBlock(card.Body)
	if !fenceOK {
		errs = append(errs, gen.CardValidationError{
			Code:    "task_exec_script_body_missing_fence",
			Field:   "body",
			Message: "script card body must contain at least one ```spore fenced block",
		})
	} else if err := probeScriptCompile(src); err != nil {
		// Compile-only probe: the spore VM parser/linker rejects the
		// source. Echo the compiler's own message (truncated per the
		// log-field rule) so the card author sees the offending line /
		// column without a dispatch round-trip.
		errs = append(errs, gen.CardValidationError{
			Code:    "task_exec_script_compile_failed",
			Field:   "body",
			Message: fmt.Sprintf("script body failed to compile: %s", truncateForError(err.Error())),
		})
	}
	return errs
}

// probeScriptCompile performs a compile-only check of a script card's
// source: it builds a throwaway spore Runtime, binds a no-op
// host.invoke (so `import { invoke } from "host"` resolves during
// linking), LoadSource the candidate, and closes the Runtime. It never
// runs the entry function — only the parser/linker is exercised — so a
// non-nil error means the card would fail the executor's claim-time
// preflight compile probe.
//
// The probe lives here, not in pkg/scriptcard: scriptcard is a
// zero-dependency contract package (stdlib only) shared with the
// workspace executor, and pulling the spore VM into it would widen that
// boundary. spore/script has no dependency on sporemind, so importing
// it from this layer introduces no cycle.
func probeScriptCompile(src string) error {
	rt, err := script.NewRuntime()
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()
	if err := rt.BindFunc("host", "invoke", func(string, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}); err != nil {
		return fmt.Errorf("bind probe host.invoke: %w", err)
	}
	return rt.LoadSource("probe", src)
}

// validateScriptCapabilities enforces that data.exec.capabilities is a
// non-empty list of dotted callable IDs. Each item is shape-checked
// (segment + dot) but its existence in the live topology is resolved at
// runtime by the script executor's whitelist check — the static layer
// never reaches across to the registry.
func validateScriptCapabilities(execBlock map[string]any) []gen.CardValidationError {
	raw, present := execBlock["capabilities"]
	if !present || raw == nil {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_capabilities_missing",
			Field:   "data.exec.capabilities",
			Message: "data.exec.capabilities is required for exec.kind=script",
		}}
	}
	ids, ok := scriptCapabilityStrings(raw)
	if !ok {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_capabilities_invalid",
			Field:   "data.exec.capabilities",
			Message: "data.exec.capabilities must be a list of dotted callable IDs (e.g. [\"app.emit_state\", \"project.report\"])",
		}}
	}
	if len(ids) == 0 {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_capabilities_empty",
			Field:   "data.exec.capabilities",
			Message: "data.exec.capabilities must declare at least one callable",
		}}
	}
	var errs []gen.CardValidationError
	for _, id := range ids {
		if !isDottedCallableID(id) {
			errs = append(errs, gen.CardValidationError{
				Code: "task_exec_script_capability_invalid",
				Field: "data.exec.capabilities",
				Message: fmt.Sprintf(
					"data.exec.capabilities entry %q is not a dotted callable ID (expected at least two identifier segments, e.g. \"app.emit\")",
					truncateForError(id)),
			})
		}
	}
	return errs
}

// validateScriptInputs enforces that data.exec.inputs (when declared)
// is an ordered list of non-empty, unique strings — the positional
// binding the spore VM uses for `export fun run(inputs...)`. The
// static layer only checks shape; the script executor later maps each
// name to an upstream binding at claim time. The shape rules (list of
// non-empty strings, no duplicates) are delegated to pkg/scriptcard so
// the static validator and the workspace-side executor share the same
// ParseConfig path — a card that passes preflight here will parse the
// same way at claim time. The earlier "value is a type name" check is
// intentionally dropped: the spore VM treats positional inputs as
// opaque names and does not consult a spore type table for them.
func validateScriptInputs(execBlock map[string]any) []gen.CardValidationError {
	raw, present := execBlock["inputs"]
	if !present || raw == nil {
		return nil
	}
	// Reuse scriptcard's shape / uniqueness rules. Other exec sub-
	// fields are absent in the synthetic data map; the parser treats
	// absent fields as defaults, so the inputs branch alone is
	// exercised here.
	if _, err := scriptcard.ParseConfig(map[string]any{
		"exec": map[string]any{"inputs": raw},
	}); err != nil {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_inputs_invalid",
			Field:   "data.exec.inputs",
			Message: truncateForError(err.Error()),
		}}
	}
	return nil
}

// validateScriptOutputs enforces that data.exec.outputs (when declared)
// is a single non-empty string — either a descriptor reference or a
// callable ID; the exact resolution is left to the script executor.
func validateScriptOutputs(execBlock map[string]any) []gen.CardValidationError {
	raw, present := execBlock["outputs"]
	if !present || raw == nil {
		return nil
	}
	out, ok := raw.(string)
	if !ok {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_outputs_invalid",
			Field:   "data.exec.outputs",
			Message: "data.exec.outputs must be a single string (descriptor or callable ID)",
		}}
	}
	if strings.TrimSpace(out) == "" {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_outputs_empty",
			Field:   "data.exec.outputs",
			Message: "data.exec.outputs must be a non-empty string (descriptor or callable ID)",
		}}
	}
	return nil
}

// validateScriptBudget enforces the numeric ranges and key names on
// data.exec.budget:
//
//	max_duration_sec: [scriptcard.MinMaxDurationSec, scriptcard.MaxMaxDurationSec]
//	max_host_calls:   [scriptcard.MinMaxHostCalls, scriptcard.MaxMaxHostCalls]
//	max_instructions: >= scriptcard.MinMaxInstructions
//	max_output_bytes: >= scriptcard.MinMaxOutputBytes
//
// 0 / missing is "limit disabled" (the executor keeps the cap off) for
// the last three, and "fall back to scriptcard.DefaultMaxDurationSec"
// for max_duration_sec. Bounds are imported from pkg/scriptcard so the
// validator and the executor quote the same numbers in error messages
// and a contract drift is caught by tests in one place.
//
// The shape / range / integer-coercion rules are delegated to
// scriptcard.ParseConfig, which is the same parser the workspace-side
// executor uses; preflight here guarantees the claim-time parse will
// not surface a fresh error. The deprecated timeout_sec key is
// silently ignored (any positive integer in its slot will be
// re-interpreted as the canonical max_duration_sec — but since it is
// not under the canonical key, we accept wide and let the executor
// ignore it; this preserves existing cards while tightening the
// canonical contract).
func validateScriptBudget(execBlock map[string]any) []gen.CardValidationError {
	raw, present := execBlock["budget"]
	if !present || raw == nil {
		return nil
	}
	// Run the budget sub-block through scriptcard.ParseConfig. The
	// synthetic data map mirrors the canonical shape (data.exec.budget
	// with the user-supplied raw value) so the parser exercises the
	// same code path the executor will at claim time; other exec sub-
	// fields are absent and resolve to scriptcard defaults.
	if _, err := scriptcard.ParseConfig(map[string]any{
		"exec": map[string]any{"budget": raw},
	}); err != nil {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_budget_invalid",
			Field:   "data.exec.budget",
			Message: truncateForError(err.Error()),
		}}
	}
	return nil
}

// validateScriptGateCard enforces that data.exec.gate_card (when
// declared) is a single non-empty string — the referenced card ID.
// The static layer does not verify existence or whether the referenced
// card is actually a gate; the script executor resolves the gate at
// claim time (parallel to executor_gate.gateToolCallEffect). The field
// is only meaningful when capabilities include a mutating callable;
// the static layer does not enforce that coupling.
func validateScriptGateCard(execBlock map[string]any) []gen.CardValidationError {
	raw, present := execBlock["gate_card"]
	if !present || raw == nil {
		return nil
	}
	gate, ok := raw.(string)
	if !ok {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_gate_card_invalid",
			Field:   "data.exec.gate_card",
			Message: "data.exec.gate_card must be a single string (card ID)",
		}}
	}
	if strings.TrimSpace(gate) == "" {
		return []gen.CardValidationError{{
			Code:    "task_exec_script_gate_card_empty",
			Field:   "data.exec.gate_card",
			Message: "data.exec.gate_card must be a non-empty card ID",
		}}
	}
	return nil
}

// isDottedCallableID reports whether s is a non-empty dotted callable
// ID with at least two identifier segments. The shape check mirrors
// pkg/actor/toast.validateActionPayload (must contain a dot), with a
// tighter regex to reject empty segments, leading/trailing dots,
// whitespace, dots adjacent to whitespace, and unsupported characters.
// Existence and topology registration are resolved at runtime — the
// static layer never reaches across to the registry.
func isDottedCallableID(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	return dottedCallableIDRe.MatchString(s)
}

// dottedCallableIDRe matches a two-or-more-segment dotted identifier:
// each segment starts with a letter and contains letters, digits,
// underscores, or hyphens. Matches the surface syntax accepted by the
// runtime topology (e.g. "app.emit_state", "project.report",
// "workflow.gate.approve", "mcp.<server>.<tool>"); stricter character
// classes are enforced by the registry at runtime, not here.
var dottedCallableIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(?:\.[A-Za-z][A-Za-z0-9_-]*)+$`)

// scriptCapabilityStrings normalizes the YAML spellings for
// data.exec.capabilities (block list or flow-style array) into a flat
// []string. The inline frontmatter parser stores block lists as
// []string, flow arrays ([a, b]) as []any, and an empty block
// (`capabilities:` with no children) as the empty string "" — the
// last is interpreted here as an empty list so the empty-list
// validator can take over with a precise error message. Any other
// scalar string (e.g. `capabilities: app.emit`) is rejected as
// malformed: a single ID is not a non-empty array. Empty strings
// inside the source are dropped silently, so an array of all blanks
// surfaces as an empty list rather than a malformed list.
func scriptCapabilityStrings(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		// The inline parser collapses an empty block (`key:`) to "".
		// Treat that as an empty list so the caller can emit a precise
		// "empty" error rather than the generic "not a list" error.
		if strings.TrimSpace(v) == "" {
			return []string{}, true
		}
		// Any non-empty scalar string is rejected — a single ID is
		// not a non-empty array.
		return nil, false
	}
	return nil, false
}

// knownExecKindsList renders the allowlist in deterministic order so the
// validation message never depends on map iteration order.
func knownExecKindsList() string {
	kinds := make([]string, 0, len(knownExecKinds))
	for k := range knownExecKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

// validateCardCategory enforces that data.category, when declared on a task
// card, is one of the canonical CardCategories values. Missing or empty
// category is allowed for backward compatibility with existing cards.
func validateCardCategory(card *CardRecord) []gen.CardValidationError {
	if card == nil || card.Data == nil {
		return nil
	}
	rawCategory, _ := card.Data["category"].(string)
	category := strings.TrimSpace(rawCategory)
	if category == "" {
		return nil
	}
	if _, known := CardCategories[category]; !known {
		return []gen.CardValidationError{{
			Code:    "task_category_unknown",
			Field:   "data.category",
			Message: fmt.Sprintf("category %q is not one of the known categories (code, explore, execute, research, review)", category),
		}}
	}
	return nil
}

// hasFrontmatter reports whether raw looks like it has a YAML frontmatter block.
func hasFrontmatter(raw string) bool {
	if !strings.HasPrefix(raw, "---") {
		return false
	}
	end := strings.Index(raw[3:], "---")
	return end >= 0
}

// isSystemCardID reports whether id belongs to a system-managed card. Such
// cards may carry reserved builtin tags and are exempt from user-card
// structural constraints. Mirrors the frontend isSystemCardId check.
func isSystemCardID(id string) bool {
	return strings.HasPrefix(id, builtinCardPrefix) || strings.Contains(id, ":")
}

// --- type-specific validators ------------------------------------------------

type wikiValidator struct{}

func (wikiValidator) Validate(_ *CardRecord) []gen.CardValidationError { return nil }

type taskValidator struct{}

func (taskValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	status := strings.ToLower(strings.TrimSpace(card.Status))
	if status != "" {
		switch status {
		case "backlog", "todo", "doing", "pending_review", "done", "blocked", "cancelled":
			// recognized status
		default:
			errs = append(errs, gen.CardValidationError{
				Code:    "task_status_invalid",
				Field:   "status",
				Message: fmt.Sprintf("task status %q is not recognized", card.Status),
			})
		}
	}
	if strings.TrimSpace(card.Due) != "" {
		if _, err := time.Parse(time.RFC3339, card.Due); err != nil {
			errs = append(errs, gen.CardValidationError{
				Code:    "task_due_invalid",
				Field:   "due",
				Message: "task due date must be ISO8601/RFC3339",
			})
		}
	}
	priority := strings.ToLower(strings.TrimSpace(card.Priority))
	if priority != "" {
		switch priority {
		case "low", "medium", "high", "urgent":
			// recognized priority
		default:
			errs = append(errs, gen.CardValidationError{
				Code:    "task_priority_invalid",
				Field:   "priority",
				Message: fmt.Sprintf("task priority %q is not recognized", card.Priority),
			})
		}
	}
	// I/O contract (inputs/outputs) — reuses spore types, no JSON Schema.
	errs = append(errs, validateCardContract(card)...)
	// execKind declaration — must be in the known execKind allowlist.
	// The dispatcher uses the same set when looking up the executor at
	// dispatch time; cards that omit data.exec.kind fall back to the
	// workspace default (worker_task), so an empty declaration is OK.
	errs = append(errs, validateCardExecKind(card)...)
	// script-kind shape — capabilities/inputs/outputs/budget/gate_card
	// and the body ```spore fence. No-op when data.exec.kind != script.
	errs = append(errs, validateCardScript(card)...)
	// IO contract declaration — data.io = {callable, description?}. Turns
	// the card into a turn-end forced toolcall for the owning agent.
	errs = append(errs, validateCardIO(card)...)
	// category declaration — must be one of the canonical task categories.
	// Cards that omit data.category remain valid for backward compatibility.
	errs = append(errs, validateCardCategory(card)...)
	// Template/instance lifecycle markers.
	errs = append(errs, validateCardTemplateRole(card)...)
	return errs
}

type schedulerValidator struct{}

func (schedulerValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	schedule, _ := card.Data["schedule"].(map[string]any)
	cron := stringValue(schedule["cron"])
	expression := stringValue(schedule["expression"])

	if cron != "" && len(strings.Fields(cron)) != 5 {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_cron_invalid",
			Field:   "data.schedule.cron",
			Message: "scheduler: cron must contain 5 fields",
		})
	}
	if cron != "" && expression != "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_cron_expression_conflict",
			Field:   "data.schedule",
			Message: "scheduler: set either cron or expression, not both",
		})
	}
	// expression carries the natural-duration form ("every 30 minutes" /
	// "at 2026-09-12 09:00:00"). The scheduler parses it with its own
	// parseNaturalDuration and silently falls back to a 1h interval when the
	// parse fails or yields a non-positive duration (scheduler.go:260), so an
	// unparseable expression is accepted here only to fire hourly forever with
	// no feedback. Mirror the parser's grammar (cycle-free duplicate) and reject
	// the bad forms up front.
	if expression != "" {
		if msg := validateScheduleExpression(expression); msg != "" {
			errs = append(errs, gen.CardValidationError{
				Code:    "scheduler_expression_invalid",
				Field:   "data.schedule.expression",
				Message: msg,
			})
		}
	}

	// executor is required for workflow/prompt cards (the agent that runs the
	// task). agent_action cards use target_agent instead and must omit
	// executor (validated in validateAgentActionCard); agent_task cards
	// (explicit, or prompt cards opting in via bind_mode) carry bind_mode /
	// bound_agent instead of executor; unified task cards always create a
	// fresh agent per fire and need no executor at all.
	scheduleType := cardDataString(card, "schedule_type")
	executor := strings.TrimSpace(cardDataString(card, "executor"))
	if executor == "" {
		executor = strings.TrimSpace(frontmatterValue(card.Raw, "executor"))
	}
	if scheduleType != scheduleTypeAgentAction && scheduleType != scheduleTypeTask && !isAgentTaskCard(card) && executor == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_executor_missing",
			Field:   "data.executor",
			Message: "scheduler: executor is required",
		})
	}

	// schedule_type: when declared it must be one of the canonical values
	// (task | workflow | prompt | agent_action | agent_task); workflow cards
	// additionally require a bound workflow_template; agent_action cards
	// require target_agent and agent_action and must not carry executor;
	// agent_task cards require an explicit bind_mode. An empty schedule_type
	// is valid — the reader derives it from workflow_template for backward
	// compatibility with old cards.
	if scheduleType != "" && scheduleType != scheduleTypeTask && scheduleType != scheduleTypeWorkflow && scheduleType != scheduleTypePrompt && scheduleType != scheduleTypeAgentAction && scheduleType != scheduleTypeAgentTask {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_schedule_type_invalid",
			Field:   "data.schedule_type",
			Message: fmt.Sprintf("scheduler: schedule_type %q must be task, workflow, prompt, agent_action, or agent_task", scheduleType),
		})
	}
	// An explicitly declared prompt scheduler fires by chat_submitting the card
	// body (timer_exec.go), so an empty body would push a blank string through.
	// Only enforce this when schedule_type is explicitly "prompt" — legacy cards
	// with no schedule_type keep the old derivation behavior (empty body allowed).
	if scheduleType == scheduleTypePrompt && strings.TrimSpace(card.Body) == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_prompt_body_empty",
			Field:   "body",
			Message: "scheduler: schedule_type=prompt requires a non-empty card body",
		})
	}
	// Unified task cards: the body (prompt) may be empty when a template is
	// bound — template mode fires the workflow template and never submits the
	// body. Without a template the body IS the task and must be non-empty, unless
	// the card carries a unified agent_actions list (pause/resume commands).
	if isTaskCard(card) && taskModeOf(card) == taskModePrompt && strings.TrimSpace(card.Body) == "" && len(agentActionsOf(card)) == 0 {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_task_body_empty",
			Field:   "body",
			Message: "scheduler: schedule_type=task without a bound workflow_template or agent_actions requires a non-empty card body",
		})
	}
	// agent_task cards fire the card body via chat_submit on a bound or
	// ephemeral agent, so they need a body too.
	if isAgentTaskCard(card) && strings.TrimSpace(card.Body) == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_agent_task_body_empty",
			Field:   "body",
			Message: "scheduler: schedule_type=agent_task requires a non-empty card body",
		})
	}
	if isAgentTaskCard(card) {
		errs = append(errs, validateAgentTaskCard(card)...)
	}
	if scheduleType == scheduleTypeWorkflow && workflowTemplate(card) == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_workflow_template_missing",
			Field:   "data.workflow_template",
			Message: "scheduler: schedule_type=workflow requires workflow_template",
		})
	}
	if scheduleType == scheduleTypeAgentAction {
		errs = append(errs, validateAgentActionCard(card)...)
	}
	return errs
}

// validateScheduleExpression mirrors pkg/actor/scheduler's natural-duration
// grammar (parseNaturalDuration / parseEvery / parseAt) so that a card the
// scheduler would reject and silently re-schedule hourly is instead surfaced as
// a validation error. It returns an empty string for a schedulable expression,
// or a human-readable reason otherwise.
//
// Importing pkg/actor/scheduler would create an import cycle (that actor depends
// on the project package's card records), so the minimal grammar is duplicated:
//
//	every <n> second[s] | minute[s] | hour[s] | day[s]   (n must be > 0)
//	at <YYYY-MM-DD HH:MM:SS>                              (must parse and be future)
//
// It is intentionally stricter than parseEvery, which accepts n <= 0 and leaves
// the caller's `interval > 0` guard to discard it (scheduler.go:254).
func validateScheduleExpression(expr string) string {
	trimmed := strings.ToLower(strings.TrimSpace(expr))
	display := truncateForError(trimmed)

	switch {
	case strings.HasPrefix(trimmed, "every "):
		parts := strings.Fields(strings.TrimPrefix(trimmed, "every "))
		if len(parts) < 2 {
			return fmt.Sprintf("scheduler: expression %q must be \"every <n> <unit>\"", display)
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil || n <= 0 {
			return fmt.Sprintf("scheduler: expression %q must use a positive interval", display)
		}
		switch parts[1] {
		case "second", "seconds", "minute", "minutes", "hour", "hours", "day", "days":
			return ""
		default:
			return fmt.Sprintf("scheduler: expression %q uses unsupported unit %q", display, truncateForError(parts[1]))
		}
	case strings.HasPrefix(trimmed, "at "):
		ts := strings.TrimPrefix(trimmed, "at ")
		t, err := time.Parse("2006-01-02 15:04:05", ts)
		if err != nil {
			return fmt.Sprintf("scheduler: expression %q must be \"at YYYY-MM-DD HH:MM:SS\"", display)
		}
		if !t.After(time.Now()) {
			return fmt.Sprintf("scheduler: expression %q time is in the past", display)
		}
		return ""
	default:
		return fmt.Sprintf("scheduler: unrecognised expression %q", display)
	}
}

// validateAgentActionCard checks the agent_action-specific data fields:
// agent_action must be pause|resume, target_agent is required, and the legacy
// executor field is rejected (its semantics yield to target_agent).
func validateAgentActionCard(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError

	action := cardDataString(card, "agent_action")
	switch action {
	case "pause", "resume":
	default:
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_agent_action_invalid",
			Field:   "data.agent_action",
			Message: fmt.Sprintf("scheduler: agent_action %q must be pause or resume", action),
		})
	}

	target := cardDataString(card, "target_agent")
	if target == "" {
		target = strings.TrimSpace(frontmatterValue(card.Raw, "target_agent"))
	}
	if target == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_target_agent_missing",
			Field:   "data.target_agent",
			Message: "scheduler: target_agent is required for agent_action cards",
		})
	}

	if executor := cardDataString(card, "executor"); executor != "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_executor_conflict",
			Field:   "data.executor",
			Message: "scheduler: executor is not used by agent_action cards; set target_agent instead",
		})
	}
	return errs
}

// validateAgentTaskCard checks the agent_task-specific data fields: bind_mode
// must be explicitly declared as bound|ephemeral; the executor field is
// rejected (agent_task routes through bound_agent / an ephemeral agent
// instead). The bound mode no longer requires data.bound_agent up front — the
// fire path spawns and rebinds a fresh agent when the binding is missing or
// stale, so an unbound card is valid and self-heals on its first run.
func validateAgentTaskCard(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError

	mode, explicit := explicitBindMode(card)
	if !explicit {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_bind_mode_missing",
			Field:   "data.bind_mode",
			Message: "scheduler: schedule_type=agent_task requires an explicit data.bind_mode (bound or ephemeral)",
		})
		return errs
	}
	switch mode {
	case schedulerBindModeBound, schedulerBindModeEphemeral:
		// valid
	default:
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_bind_mode_invalid",
			Field:   "data.bind_mode",
			Message: fmt.Sprintf("scheduler: bind_mode %q must be bound or ephemeral", mode),
		})
		return errs
	}

	bound := boundAgentOf(card)
	if mode == schedulerBindModeEphemeral && bound != "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_ephemeral_bound_agent_conflict",
			Field:   "data.bound_agent",
			Message: "scheduler: bind_mode=ephemeral must not carry data.bound_agent",
		})
	}
	if executor := cardDataString(card, "executor"); executor != "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "scheduler_executor_conflict",
			Field:   "data.executor",
			Message: "scheduler: executor is not used by agent_task cards; set data.bind_mode and data.bound_agent instead",
		})
	}
	return errs
}

type promptValidator struct{}

func (promptValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	placement := strings.ToLower(strings.TrimSpace(cardDataString(card, "placement")))
	if placement != "" {
		switch placement {
		case "role", "policy", "system", "context", "prompt":
			// recognized placement
		default:
			errs = append(errs, gen.CardValidationError{
				Code:    "prompt_placement_invalid",
				Field:   "data.placement",
				Message: fmt.Sprintf("prompt placement %q is not recognized", cardDataString(card, "placement")),
			})
		}
	}
	return errs
}

type skillValidator struct{}

func (skillValidator) Validate(card *CardRecord) []gen.CardValidationError {
	return validateReferences(card, "skill", "requires", "tools", "callable")
}

type conceptValidator struct{}

func (conceptValidator) Validate(_ *CardRecord) []gen.CardValidationError { return nil }

type callableValidator struct{}

func (callableValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	callable := strings.TrimSpace(cardDataString(card, "callable"))
	if callable == "" {
		callable = strings.TrimSpace(frontmatterValue(card.Raw, "callable"))
	}
	if callable == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "callable_id_missing",
			Field:   "data.callable",
			Message: "callable: callable id is required",
		})
	}
	return errs
}

type capabilityModuleValidator struct{}

func (capabilityModuleValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	if strings.TrimSpace(cardDataString(card, "source")) == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "capability_module_source_missing",
			Field:   "data.source",
			Message: "capability_module: source is required",
		})
	}
	errs = append(errs, validateReferences(card, "capability_module", "requires", "tools", "callable")...)
	return errs
}

// --- crawl validator ---------------------------------------------------------

// crawlValidator enforces the type: crawl definition-card contract.
// A crawl card is a pure configuration card: seeds, bounds, extraction schema,
// profile/mode. It must not carry workflow orchestration fields (workflow,
// step, depends_on) because orchestration lives in workflow/task cards that
// reference this definition card. The validator only checks the authored card;
// runtime snapshot semantics (start copies config, card edits do not affect a
// running instance, deleting the card does not cancel the task) are implemented
// by the crawl executor, not here.
type crawlValidator struct{}

func (crawlValidator) Validate(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	errs = append(errs, validateCrawlRequiredFields(card)...)
	errs = append(errs, validateCrawlFieldTypes(card)...)
	errs = append(errs, validateCrawlZeroOrchestrationFields(card)...)
	return errs
}

func validateCrawlRequiredFields(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError

	if len(cardDataStrings(card, "seeds")) == 0 {
		if card == nil || card.Data == nil || card.Data["seeds"] == nil {
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_seeds_missing",
				Field:   "data.seeds",
				Message: "crawl: seeds is required",
			})
		} else {
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_seeds_empty",
				Field:   "data.seeds",
				Message: "crawl: seeds must contain at least one URL",
			})
		}
	}

	if card == nil || card.Data == nil {
		errs = append(errs,
			gen.CardValidationError{Code: "crawl_max_depth_missing", Field: "data.max_depth", Message: "crawl: max_depth is required"},
			gen.CardValidationError{Code: "crawl_max_pages_missing", Field: "data.max_pages", Message: "crawl: max_pages is required"},
			gen.CardValidationError{Code: "crawl_mode_missing", Field: "data.mode", Message: "crawl: mode is required"},
			gen.CardValidationError{Code: "crawl_extract_schema_missing", Field: "data.extract.schema", Message: "crawl: extract.schema is required"},
		)
		return errs
	}

	if _, ok := card.Data["max_depth"]; !ok {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_max_depth_missing",
			Field:   "data.max_depth",
			Message: "crawl: max_depth is required",
		})
	}
	if _, ok := card.Data["max_pages"]; !ok {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_max_pages_missing",
			Field:   "data.max_pages",
			Message: "crawl: max_pages is required",
		})
	}
	if _, ok := card.Data["mode"]; !ok {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_mode_missing",
			Field:   "data.mode",
			Message: "crawl: mode is required",
		})
	}
	if schema, ok := crawlExtractSchema(card); !ok || schema == "" {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_extract_schema_missing",
			Field:   "data.extract.schema",
			Message: "crawl: extract.schema is required",
		})
	}

	return errs
}

func validateCrawlFieldTypes(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	if card == nil || card.Data == nil {
		return errs
	}

	if d, ok := crawlDataInt(card, "max_depth"); ok && d < 0 {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_max_depth_invalid",
			Field:   "data.max_depth",
			Message: "crawl: max_depth must be >= 0",
		})
	}
	if p, ok := crawlDataInt(card, "max_pages"); ok && p <= 0 {
		errs = append(errs, gen.CardValidationError{
			Code:    "crawl_max_pages_invalid",
			Field:   "data.max_pages",
			Message: "crawl: max_pages must be > 0",
		})
	}

	if mode := cardDataString(card, "mode"); mode != "" {
		switch mode {
		case "hidden_window", "performance":
			// recognized
		default:
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_mode_invalid",
				Field:   "data.mode",
				Message: fmt.Sprintf("crawl: mode %q is not recognized (hidden_window, performance)", mode),
			})
		}
	}

	if schema, ok := crawlExtractSchema(card); ok && schema != "" {
		if !knownStructName(schema) {
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_extract_schema_unknown",
				Field:   "data.extract.schema",
				Message: fmt.Sprintf("crawl: extract.schema %q is not a known codegen struct", schema),
			})
		}
	}

	if rate := cardDataString(card, "rate_limit"); rate != "" {
		if d, err := time.ParseDuration(rate); err != nil || d <= 0 {
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_rate_limit_invalid",
				Field:   "data.rate_limit",
				Message: "crawl: rate_limit must be a positive duration (e.g. 1s, 500ms)",
			})
		}
	}

	return errs
}

func validateCrawlZeroOrchestrationFields(card *CardRecord) []gen.CardValidationError {
	var errs []gen.CardValidationError
	forbidden := []string{"workflow", "step", "depends_on"}
	forbiddenSet := map[string]struct{}{"workflow": {}, "step": {}, "depends_on": {}}

	if card != nil && card.Data != nil {
		for _, key := range forbidden {
			if _, present := card.Data[key]; present {
				errs = append(errs, gen.CardValidationError{
					Code:    "crawl_orchestration_field_forbidden",
					Field:   "data." + key,
					Message: fmt.Sprintf("crawl card must not contain orchestration field %q", key),
				})
			}
		}
	}

	if card == nil || !hasFrontmatter(card.Raw) {
		return errs
	}
	end := strings.Index(card.Raw[3:], "---")
	if end < 0 {
		return errs
	}
	front := card.Raw[3 : 3+end]
	for _, line := range strings.Split(front, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, isForbidden := forbiddenSet[key]; isForbidden {
			errs = append(errs, gen.CardValidationError{
				Code:    "crawl_orchestration_field_forbidden",
				Field:   key,
				Message: fmt.Sprintf("crawl card must not contain orchestration field %q", key),
			})
		}
	}
	return errs
}

// crawlExtractSchema returns the string value at data.extract.schema and a
// boolean indicating whether the nested path was present and a string.
func crawlExtractSchema(card *CardRecord) (string, bool) {
	if card == nil || card.Data == nil {
		return "", false
	}
	extract, ok := card.Data["extract"].(map[string]any)
	if !ok {
		return "", false
	}
	schema, ok := extract["schema"].(string)
	return strings.TrimSpace(schema), ok
}

// crawlDataInt returns the integer value at data[key], parsing it from a string
// when the custom frontmatter parser stores it that way. The second result is
// true when the key is present and parseable.
func crawlDataInt(card *CardRecord, key string) (int, bool) {
	if card == nil || card.Data == nil {
		return 0, false
	}
	v, ok := card.Data[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}

// validateReferences checks that the named data/frontmatter fields contain
// non-empty string values. It returns one error per empty field.
func validateReferences(card *CardRecord, typ string, fields ...string) []gen.CardValidationError {
	var errs []gen.CardValidationError
	for _, field := range fields {
		value := strings.TrimSpace(cardDataString(card, field))
		if value == "" {
			value = strings.TrimSpace(frontmatterValue(card.Raw, field))
		}
		if value == "" {
			continue
		}
		// Each field may be a single reference or a list of references.
		for _, ref := range parseReferenceList(value) {
			if strings.TrimSpace(ref) == "" {
				errs = append(errs, gen.CardValidationError{
					Code:    typ + "_" + field + "_empty",
					Field:   "data." + field,
					Message: fmt.Sprintf("%s: %s reference must not be empty", typ, field),
				})
			}
		}
	}
	return errs
}

// parseReferenceList splits a value that may be a YAML list literal or a single
// comma/space-separated string into individual reference tokens.
func parseReferenceList(value string) []string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '[' && value[len(value)-1] == ']' {
		value = value[1 : len(value)-1]
	}
	parts := strings.Split(value, ",")
	if len(parts) == 1 {
		parts = strings.Fields(parts[0])
	}
	return parts
}

// validateBuiltinCard is retained for backward compatibility with existing
// callers/tests; new code should call validateCard.
func validateBuiltinCard(id, raw string) error {
	return validateCard(id, raw)
}

// ValidateCardStructured is the exported counterpart of
// validateCardStructured. Same semantics (full structural validation,
// returns all violations as structured errors); powers the
// project.wiki_validate_card handler and is reusable from sibling
// packages (e.g. the workspace executor's cross-layer contract tests)
// without round-tripping through the actor invoke surface.
//
// Returns an empty slice when the card passes every check.
func ValidateCardStructured(id, raw string) []gen.CardValidationError {
	return validateCardStructured(id, raw)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
