package puppeteditor

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Param model & binding (Phase 2). This file owns the document-level
// animatable parameter model (PuppetParam: name / range / default / group),
// the param -> node property binding table (PuppetParamBinding), value
// validation and resolution, and the snapshot projection that makes bound
// values (e.g. opacity, z) take effect on read surfaces.
//
// Storage lives on the PuppetDocument itself (Params / ParamValues /
// Bindings), so every param change flows through the same audited edit path
// as node edits: puppet.edit -> handleEditWithAuditSource -> an immutable
// revision (author from caller context, monotonic sequence, AffectedGuids)
// plus a per-revision snapshot that makes param state revertible. Projection
// is derived, never stored: the authored document keeps explicit values, and
// read surfaces (document snapshot, viewport reprojection) apply bindings to
// a clone before returning it.

// Whitelisted param command kinds, dispatched by buildEditCommand in edit.go.
// For all five, the PuppetEditCommand TargetGuid carries the target param id.
const (
	cmdParamCreate     = "param_create"
	cmdParamUpdate     = "param_update"
	cmdParamRemove     = "param_remove"
	cmdSetParamValue   = "set_param_value"
	cmdSetParamBinding = "set_param_binding"
)

// Param command argument names (carried in PuppetEditCommand.Params).
const (
	paramArgName       = "name"
	paramArgKind       = "kind"
	paramArgDefault    = "default"
	paramArgMin        = "min"
	paramArgMax        = "max"
	paramArgStep       = "step"
	paramArgGroup      = "group"
	paramArgNoInherit  = "no_inherit"
	paramArgValue      = "value"
	paramArgAction     = "action"
	paramArgNode       = "node"
	paramArgProperty   = "property"
	paramArgMultiplier = "multiplier"
	paramArgOffset     = "offset"
)

// Binding action values for set_param_binding (mirrors the schema's
// PuppetParamBindingReq Action vocabulary).
const (
	bindingActionAdd    = "add"
	bindingActionUpdate = "update"
	bindingActionRemove = "remove"
)

// paramKindWhitelist is the closed set of PuppetParam.Kind values. The value
// grammar per kind:
//   - "float": a decimal number (strconv float64 grammar)
//   - "int":   a base-10 integer
//   - "bool":  strconv bool grammar ("true"/"false"/"1"/"0"/...)
// ---------------------------------------------------------------------------
// Value & model validation (pure)
// ---------------------------------------------------------------------------

// validateParamValue checks raw against the value grammar of kind. It does
// not apply range hints; see validateParamRange.
func validateParamValue(kind, raw string) error {
	switch kind {
	case "float":
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return fmt.Errorf("not a float: %q", raw)
		}
	case "int":
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return fmt.Errorf("not an int: %q", raw)
		}
	case "bool":
		if _, err := strconv.ParseBool(raw); err != nil {
			return fmt.Errorf("not a bool: %q", raw)
		}
	case "vec2":
		parts := strings.Split(raw, ",")
		if len(parts) != 2 {
			return fmt.Errorf("not a vec2 \"x,y\": %q", raw)
		}
		for _, p := range parts {
			if _, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err != nil {
				return fmt.Errorf("not a vec2 \"x,y\": %q", raw)
			}
		}
	case "color":
		hex := strings.TrimPrefix(raw, "#")
		if len(hex) != 6 && len(hex) != 8 {
			return fmt.Errorf("not a #RRGGBB[AABB] color: %q", raw)
		}
		for _, c := range hex {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return fmt.Errorf("not a #RRGGBB[AABB] color: %q", raw)
			}
		}
	default:
		return fmt.Errorf("unknown param kind %q", kind)
	}
	return nil
}

// numericParamValue coerces a stored param value to float64. Only numeric
// kinds ("float", "int") coerce; other kinds cannot drive bindings.
func numericParamValue(param gen.PuppetParam, raw string) (float64, error) {
	switch param.Kind {
	case "float":
		return strconv.ParseFloat(raw, 64)
	case "int":
		v, err := strconv.ParseInt(raw, 10, 64)
		return float64(v), err
	default:
		return 0, fmt.Errorf("param %q has kind %q; bindings require a numeric kind (float|int)", param.ID, param.Kind)
	}
}

// validateParamModel validates a complete param definition: kind whitelist,
// default present and grammatically valid, range hints only on numeric kinds,
// min <= max, and default within [min, max]. This runs both when a param is
// created and after a param_update merge, so an update can never leave the
// model inconsistent.
func validateParamModel(p gen.PuppetParam) error {
	if p.ID == "" {
		return fmt.Errorf("param id must not be empty")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("param %q: name must not be empty", p.ID)
	}
	if !paramKindWhitelist[p.Kind] {
		return fmt.Errorf("param %q: unknown kind %q (whitelist: float, int, bool, vec2, color)", p.ID, p.Kind)
	}
	if err := validateParamValue(p.Kind, p.Default); err != nil {
		return fmt.Errorf("param %q: invalid default: %v", p.ID, err)
	}
	numeric := p.Kind == "float" || p.Kind == "int"
	var minV, maxV *float64
	for label, raw := range map[string]string{"min": p.Min, "max": p.Max, "step": p.Step} {
		if raw == "" {
			continue
		}
		if !numeric {
			return fmt.Errorf("param %q: %s is only valid for numeric kinds (float|int), not %q", p.ID, label, p.Kind)
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("param %q: invalid %s %q: %v", p.ID, label, raw, err)
		}
		switch label {
		case "min":
			m := v
			minV = &m
		case "max":
			m := v
			maxV = &m
		}
	}
	if minV != nil && maxV != nil && *minV > *maxV {
		return fmt.Errorf("param %q: min %s exceeds max %s", p.ID, p.Min, p.Max)
	}
	if numeric {
		def, err := numericParamValue(p, p.Default)
		if err != nil {
			return fmt.Errorf("param %q: invalid default: %v", p.ID, err)
		}
		if minV != nil && def < *minV {
			return fmt.Errorf("param %q: default %s below min %s", p.ID, p.Default, p.Min)
		}
		if maxV != nil && def > *maxV {
			return fmt.Errorf("param %q: default %s above max %s", p.ID, p.Default, p.Max)
		}
	}
	return nil
}

// validateParamRange checks a raw value against the param's numeric range
// hints. Called by set_param_value after validateParamValue.
func validateParamRange(p gen.PuppetParam, raw string) error {
	if p.Kind != "float" && p.Kind != "int" {
		return nil
	}
	v, err := numericParamValue(p, raw)
	if err != nil {
		return err
	}
	if p.Min != "" {
		min, err := strconv.ParseFloat(p.Min, 64)
		if err == nil && v < min {
			return fmt.Errorf("value %s below min %s", raw, p.Min)
		}
	}
	if p.Max != "" {
		max, err := strconv.ParseFloat(p.Max, 64)
		if err == nil && v > max {
			return fmt.Errorf("value %s above max %s", raw, p.Max)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lookup helpers (pure, operate on a document)
// ---------------------------------------------------------------------------

// findParam returns a pointer to the param definition with the given id, or
// nil.
func findParam(doc *gen.PuppetDocument, id string) *gen.PuppetParam {
	for i := range doc.Params {
		if doc.Params[i].ID == id {
			return &doc.Params[i]
		}
	}
	return nil
}

// bindingIdentity is the natural key of a binding: one node property is
// driven by at most one param.
func bindingIdentity(b gen.PuppetParamBinding) string {
	return b.ParamID + "\x00" + b.NodeGuid + "\x00" + b.Property
}

// findBinding returns the index of the binding with the given identity, or
// -1.
func findBinding(doc *gen.PuppetDocument, paramID, nodeGuid, property string) int {
	for i := range doc.Bindings {
		if doc.Bindings[i].ParamID == paramID && doc.Bindings[i].NodeGuid == nodeGuid && doc.Bindings[i].Property == property {
			return i
		}
	}
	return -1
}

// boundNodesForParam collects the node guids bound to the given param id, in
// binding-table order without duplicates. This is the AffectedGuids set for
// set_param_value and param_remove.
func boundNodesForParam(doc *gen.PuppetDocument, paramID string) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range doc.Bindings {
		if b.ParamID == paramID && !seen[b.NodeGuid] {
			seen[b.NodeGuid] = true
			out = append(out, b.NodeGuid)
		}
	}
	return out
}

// resolveParamValue returns the effective raw value of a param: the explicit
// current value when set, otherwise the definition default.
func resolveParamValue(doc *gen.PuppetDocument, paramID string) string {
	if v, ok := doc.ParamValues[paramID]; ok {
		return v
	}
	if p := findParam(doc, paramID); p != nil {
		return p.Default
	}
	return ""
}

// ---------------------------------------------------------------------------
// Snapshot projection (pure; mutates only a caller-owned copy)
// ---------------------------------------------------------------------------

// applyParamProjection makes bound values take effect on a document
// projection: for every binding it resolves the param's current value,
// remaps it (value * multiplier + offset) and writes the effective value to
// the bound node — the Z field for property "z", otherwise the node's opaque
// Params bag (e.g. "opacity"). Callers must pass a clone: the authored
// document is never projected in place, so revert comparisons and audit
// snapshots stay stable. Dangling bindings (removed param or node) cannot be
// produced by the command layer (param_remove cascades; node removal does not
// exist yet) but are skipped defensively.
func applyParamProjection(doc *gen.PuppetDocument) {
	if len(doc.Bindings) == 0 || len(doc.Params) == 0 {
		return
	}
	for i := range doc.Bindings {
		b := doc.Bindings[i]
		param := findParam(doc, b.ParamID)
		if param == nil {
			continue
		}
		raw := resolveParamValue(doc, b.ParamID)
		if raw == "" {
			continue
		}
		v, err := numericParamValue(*param, raw)
		if err != nil {
			continue
		}
		// The remap runs in float32 space: Multiplier/Offset are float32 in
		// the schema, so keeping the whole chain at float32 avoids inventing
		// double-precision artifacts that were never stored.
		effective := float32(v)*b.Multiplier + b.Offset
		node := findNode(&doc.Root, b.NodeGuid)
		if node == nil {
			continue
		}
		applyBoundValue(node, b.Property, float64(effective))
	}
}

// applyBoundValue writes one effective value onto a node. "z" maps to the
// node's Z field (rounded to the int32 z-order); every other property name
// lands in the opaque Params property bag as the shortest round-trip float
// text, matching how the renderer reads e.g. opacity.
func applyBoundValue(node *gen.PuppetNode, property string, v float64) {
	if property == "z" {
		node.Z = int32(math.Round(v))
		return
	}
	if node.Params == nil {
		node.Params = make(map[string]string, 1)
	}
	node.Params[property] = formatParamFloat(v)
}

// formatParamFloat renders v as the shortest text that round-trips, so
// projected values are deterministic and readable (0.25, not 0.25000e+00).
// Values arrive through float32 storage (binding Multiplier/Offset), so the
// 32-bit shortest form is the faithful representation.
func formatParamFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 32)
}

// ---------------------------------------------------------------------------
// Param edit commands (editCommand implementations)
// ---------------------------------------------------------------------------

// --- param_create -----------------------------------------------------------

// paramCreateCmd appends a new param definition. The param id arrives as the
// command TargetGuid; duplicate ids are rejected in canExecute.
type paramCreateCmd struct {
	param gen.PuppetParam
}

func (c paramCreateCmd) kind() string { return cmdParamCreate }

func (c paramCreateCmd) canExecute(doc gen.PuppetDocument) error {
	if findParam(&doc, c.param.ID) != nil {
		return fmt.Errorf("param_create: param %q already exists", c.param.ID)
	}
	return nil
}

func (c paramCreateCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	doc.Params = append(doc.Params, c.param)
	rangeDesc := ""
	if c.param.Min != "" || c.param.Max != "" {
		rangeDesc = fmt.Sprintf(" range [%s,%s]", orDash(c.param.Min), orDash(c.param.Max))
	}
	groupDesc := ""
	if c.param.Group != "" {
		groupDesc = fmt.Sprintf(" group %q", c.param.Group)
	}
	return nil, fmt.Sprintf("param_create %s: kind %s default %s%s%s",
		c.param.ID, c.param.Kind, c.param.Default, rangeDesc, groupDesc)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// --- param_update -----------------------------------------------------------

// paramUpdateCmd merges provided fields into an existing param definition.
// Only keys present in the command Params map are replaced; for the optional
// string fields an explicitly provided empty string clears the value. The
// merged model is validated in canExecute, so an update can never leave an
// inconsistent param (e.g. default outside the new range).
type paramUpdateCmd struct {
	paramID   string
	name      *string
	newKind   *string
	def       *string
	min       *string
	max       *string
	step      *string
	group     *string
	noInherit *bool
}

func (c paramUpdateCmd) kind() string { return cmdParamUpdate }

// merged returns the param definition as it would look after applying the
// update. Pure: it never touches the document.
func (c paramUpdateCmd) merged(existing gen.PuppetParam) gen.PuppetParam {
	m := existing
	if c.name != nil {
		m.Name = *c.name
	}
	if c.newKind != nil {
		m.Kind = *c.newKind
	}
	if c.def != nil {
		m.Default = *c.def
	}
	if c.min != nil {
		m.Min = *c.min
	}
	if c.max != nil {
		m.Max = *c.max
	}
	if c.step != nil {
		m.Step = *c.step
	}
	if c.group != nil {
		m.Group = *c.group
	}
	if c.noInherit != nil {
		m.NoInherit = *c.noInherit
	}
	return m
}

func (c paramUpdateCmd) canExecute(doc gen.PuppetDocument) error {
	existing := findParam(&doc, c.paramID)
	if existing == nil {
		return fmt.Errorf("param_update: param %q not found", c.paramID)
	}
	return validateParamModel(c.merged(*existing))
}

func (c paramUpdateCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	existing := findParam(doc, c.paramID)
	merged := c.merged(*existing)
	*existing = merged
	return nil, fmt.Sprintf("param_update %s: name %q kind %s default %s range [%s,%s] group %q",
		merged.ID, merged.Name, merged.Kind, merged.Default, orDash(merged.Min), orDash(merged.Max), merged.Group)
}

// --- param_remove -----------------------------------------------------------

// paramRemoveCmd deletes a param definition, its explicit current value, and
// cascades every binding that sources from it so no dangling reference can
// survive. AffectedGuids are the nodes that lost a binding.
type paramRemoveCmd struct {
	paramID string
}

func (c paramRemoveCmd) kind() string { return cmdParamRemove }

func (c paramRemoveCmd) canExecute(doc gen.PuppetDocument) error {
	if findParam(&doc, c.paramID) == nil {
		return fmt.Errorf("param_remove: param %q not found", c.paramID)
	}
	return nil
}

func (c paramRemoveCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	params := doc.Params[:0]
	for _, p := range doc.Params {
		if p.ID != c.paramID {
			params = append(params, p)
		}
	}
	doc.Params = params

	delete(doc.ParamValues, c.paramID)

	affected := boundNodesForParam(doc, c.paramID)
	bindings := doc.Bindings[:0]
	for _, b := range doc.Bindings {
		if b.ParamID != c.paramID {
			bindings = append(bindings, b)
		}
	}
	doc.Bindings = bindings

	return affected, fmt.Sprintf("param_remove %s: removed param, %d binding(s) cascaded", c.paramID, len(affected))
}

// --- set_param_value --------------------------------------------------------

// setParamValueCmd sets the explicit current value of a param. The value is
// validated against the param kind grammar and range hints. AffectedGuids
// are the nodes bound to the param.
type setParamValueCmd struct {
	paramID string
	value   string
}

func (c setParamValueCmd) kind() string { return cmdSetParamValue }

func (c setParamValueCmd) canExecute(doc gen.PuppetDocument) error {
	p := findParam(&doc, c.paramID)
	if p == nil {
		return fmt.Errorf("set_param_value: param %q not found", c.paramID)
	}
	if err := validateParamValue(p.Kind, c.value); err != nil {
		return fmt.Errorf("set_param_value: param %q: %v", c.paramID, err)
	}
	if err := validateParamRange(*p, c.value); err != nil {
		return fmt.Errorf("set_param_value: param %q: %v", c.paramID, err)
	}
	return nil
}

func (c setParamValueCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	old := resolveParamValue(doc, c.paramID)
	if doc.ParamValues == nil {
		doc.ParamValues = make(map[string]string, 1)
	}
	doc.ParamValues[c.paramID] = c.value
	rangeDesc := ""
	if p := findParam(doc, c.paramID); p != nil && (p.Min != "" || p.Max != "") {
		rangeDesc = fmt.Sprintf(" (range [%s,%s])", orDash(p.Min), orDash(p.Max))
	}
	return boundNodesForParam(doc, c.paramID),
		fmt.Sprintf("set_param_value %s: %s -> %s%s", c.paramID, old, c.value, rangeDesc)
}

// --- set_param_binding --------------------------------------------------------

// setParamBindingCmd mutates one row of the binding table. The binding is
// identified by the (param, node, property) triple; action selects
// add/update/remove. Only numeric params (float|int) can drive a binding,
// because the effective value is a remapped number. On add, omitted
// multiplier/offset default to 1.0/0.0; on update, omitted fields keep their
// current values (an explicit 0 multiplier is honored — presence is tracked
// at parse time, not by comparing against zero).
type setParamBindingCmd struct {
	paramID    string
	action     string
	nodeGuid   string
	property   string
	multiplier *float64
	offset     *float64
}

func (c setParamBindingCmd) kind() string { return cmdSetParamBinding }

func (c setParamBindingCmd) canExecute(doc gen.PuppetDocument) error {
	p := findParam(&doc, c.paramID)
	if p == nil {
		return fmt.Errorf("set_param_binding: param %q not found", c.paramID)
	}
	if p.Kind != "float" && p.Kind != "int" {
		return fmt.Errorf("set_param_binding: param %q has kind %q; bindings require a numeric kind (float|int)", c.paramID, p.Kind)
	}
	if findNode(&doc.Root, c.nodeGuid) == nil {
		return fmt.Errorf("set_param_binding: node %q not found", c.nodeGuid)
	}
	idx := findBinding(&doc, c.paramID, c.nodeGuid, c.property)
	switch c.action {
	case bindingActionAdd:
		if idx >= 0 {
			return fmt.Errorf("set_param_binding: node %q property %q is already bound (update it instead)", c.nodeGuid, c.property)
		}
	case bindingActionUpdate, bindingActionRemove:
		if idx < 0 {
			return fmt.Errorf("set_param_binding: no binding from param %q to node %q property %q", c.paramID, c.nodeGuid, c.property)
		}
	}
	return nil
}

func (c setParamBindingCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	idx := findBinding(doc, c.paramID, c.nodeGuid, c.property)
	affected := []string{c.nodeGuid}
	switch c.action {
	case bindingActionAdd:
		b := gen.PuppetParamBinding{
			ParamID:  c.paramID,
			NodeGuid: c.nodeGuid,
			Property: c.property,
		}
		if c.multiplier != nil {
			b.Multiplier = float32(*c.multiplier)
		} else {
			b.Multiplier = 1
		}
		if c.offset != nil {
			b.Offset = float32(*c.offset)
		}
		doc.Bindings = append(doc.Bindings, b)
		return affected, fmt.Sprintf("set_param_binding add: param %q -> node %q.%s (x%g +%g)",
			c.paramID, c.nodeGuid, c.property, b.Multiplier, b.Offset)
	case bindingActionUpdate:
		b := &doc.Bindings[idx]
		if c.multiplier != nil {
			b.Multiplier = float32(*c.multiplier)
		}
		if c.offset != nil {
			b.Offset = float32(*c.offset)
		}
		return affected, fmt.Sprintf("set_param_binding update: param %q -> node %q.%s (x%g +%g)",
			c.paramID, c.nodeGuid, c.property, b.Multiplier, b.Offset)
	default: // bindingActionRemove — canExecute already gated the action value
		doc.Bindings = append(doc.Bindings[:idx], doc.Bindings[idx+1:]...)
		return affected, fmt.Sprintf("set_param_binding remove: param %q no longer drives node %q.%s",
			c.paramID, c.nodeGuid, c.property)
	}
}

// ---------------------------------------------------------------------------
// Command factory (param whitelist gate)
// ---------------------------------------------------------------------------

// buildParamEditCommand parses the param command envelope (Kind is one of the
// five param_* / set_param_* kinds; TargetGuid carries the param id). This is
// the param half of buildEditCommand's whitelist: argument-shape errors are
// rejected here, document-context validation in canExecute. It needs no actor
// state, so it is a plain function.
func buildParamEditCommand(env gen.PuppetEditCommand) (editCommand, error) {
	if env.TargetGuid == "" {
		return nil, fmt.Errorf("%s: param id (TargetGuid) must not be empty", env.Kind)
	}
	switch env.Kind {
	case cmdParamCreate:
		param := gen.PuppetParam{
			ID:      env.TargetGuid,
			Name:    env.Params[paramArgName],
			Kind:    env.Params[paramArgKind],
			Default: env.Params[paramArgDefault],
			Min:     env.Params[paramArgMin],
			Max:     env.Params[paramArgMax],
			Step:    env.Params[paramArgStep],
			Group:   env.Params[paramArgGroup],
		}
		if raw, ok := env.Params[paramArgNoInherit]; ok && raw != "" {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("param_create: invalid %s=%q: %v", paramArgNoInherit, raw, err)
			}
			param.NoInherit = b
		}
		if err := validateParamModel(param); err != nil {
			return nil, fmt.Errorf("param_create: %v", err)
		}
		return paramCreateCmd{param: param}, nil

	case cmdParamUpdate:
		cmd := paramUpdateCmd{paramID: env.TargetGuid}
		if v, ok := env.Params[paramArgName]; ok {
			cmd.name = &v
		}
		if v, ok := env.Params[paramArgKind]; ok {
			cmd.newKind = &v
		}
		if v, ok := env.Params[paramArgDefault]; ok {
			cmd.def = &v
		}
		if v, ok := env.Params[paramArgMin]; ok {
			cmd.min = &v
		}
		if v, ok := env.Params[paramArgMax]; ok {
			cmd.max = &v
		}
		if v, ok := env.Params[paramArgStep]; ok {
			cmd.step = &v
		}
		if v, ok := env.Params[paramArgGroup]; ok {
			cmd.group = &v
		}
		if v, ok := env.Params[paramArgNoInherit]; ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("param_update: invalid %s=%q: %v", paramArgNoInherit, v, err)
			}
			cmd.noInherit = &b
		}
		if cmd.name == nil && cmd.newKind == nil && cmd.def == nil && cmd.min == nil &&
			cmd.max == nil && cmd.step == nil && cmd.group == nil && cmd.noInherit == nil {
			return nil, fmt.Errorf("param_update: no fields to update (provide at least one of name, kind, default, min, max, step, group, no_inherit)")
		}
		return cmd, nil

	case cmdParamRemove:
		return paramRemoveCmd{paramID: env.TargetGuid}, nil

	case cmdSetParamValue:
		value, ok := env.Params[paramArgValue]
		if !ok || value == "" {
			return nil, fmt.Errorf("set_param_value: missing param %q", paramArgValue)
		}
		return setParamValueCmd{paramID: env.TargetGuid, value: value}, nil

	case cmdSetParamBinding:
		cmd := setParamBindingCmd{paramID: env.TargetGuid}
		cmd.action = env.Params[paramArgAction]
		switch cmd.action {
		case bindingActionAdd, bindingActionUpdate, bindingActionRemove:
		default:
			return nil, fmt.Errorf("set_param_binding: invalid action %q (want add|update|remove)", cmd.action)
		}
		cmd.nodeGuid = env.Params[paramArgNode]
		if cmd.nodeGuid == "" {
			return nil, fmt.Errorf("set_param_binding: missing param %q", paramArgNode)
		}
		cmd.property = env.Params[paramArgProperty]
		if cmd.property == "" {
			return nil, fmt.Errorf("set_param_binding: missing param %q", paramArgProperty)
		}
		if raw, ok := env.Params[paramArgMultiplier]; ok {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("set_param_binding: invalid %s=%q: %v", paramArgMultiplier, raw, err)
			}
			cmd.multiplier = &v
		}
		if raw, ok := env.Params[paramArgOffset]; ok {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("set_param_binding: invalid %s=%q: %v", paramArgOffset, raw, err)
			}
			cmd.offset = &v
		}
		return cmd, nil

	default:
		return nil, fmt.Errorf("buildParamEditCommand: kind %q is not a param command", env.Kind)
	}
}
