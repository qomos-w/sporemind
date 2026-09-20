//go:build ignore

// validate-envelope.go — Mechanical validation of Project Graph Trace HCL envelope.
// Usage: go run scripts/validate-envelope.go PROJECT-GRAPH-TRIAL-V*.md
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Result struct {
	ID      string
	Check   string
	Passed  bool
	Details []string
}

type Validator struct {
	hclText     string
	primitives  []map[string]string
	concepts    []map[string]string
	edges       []map[string]string
	drifts      []map[string]string
	halts       []map[string]string
	nodeIds     map[string]bool
	conceptIds  map[string]bool
	primitiveIds map[string]bool
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/validate-envelope.go <envelope.md>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	v := NewValidator(string(data))
	results := v.Validate()

	passed, failed := 0, 0
	for _, r := range results {
		if r.Passed {
			passed++
		} else {
			failed++
		}
	}

	fmt.Printf("=== PROJECT GRAPH ENVELOPE VALIDATION ===\n")
	fmt.Printf("File: %s\n", os.Args[1])
	fmt.Printf("Primitives: %d\n", len(v.primitives))
	fmt.Printf("Concepts: %d\n", len(v.concepts))
	fmt.Printf("Edges: %d\n", len(v.edges))
	fmt.Printf("Drift: %d\n", len(v.drifts))
	fmt.Printf("Halt checks: %d\n\n", len(v.halts))

	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s\n", r.ID, r.Check)
		for _, d := range r.Details {
			fmt.Printf("  %s\n", d)
		}
		if !r.Passed {
			fmt.Printf("  >>> %s\n", status)
		}
		fmt.Println()
	}

	fmt.Printf("=== SUMMARY ===\n")
	fmt.Printf("Passed: %d / %d\n", passed, passed+failed)
	fmt.Printf("Failed: %d\n", failed)

	if failed > 0 {
		fmt.Println("\nFIX INSTRUCTIONS:")
		fixNum := 1
		for _, r := range results {
			if !r.Passed {
				for _, d := range r.Details {
					fmt.Printf("%d. [%s] %s\n", fixNum, r.ID, d)
					fixNum++
				}
			}
		}
		os.Exit(1)
	}
}

func NewValidator(mdText string) *Validator {
	v := &Validator{
		nodeIds:      make(map[string]bool),
		conceptIds:   make(map[string]bool),
		primitiveIds: make(map[string]bool),
	}

	// Extract HCL block from markdown
	hclRe := regexp.MustCompile("(?s)```hcl\\s*(.*?)\\s*```")
	matches := hclRe.FindAllStringSubmatch(mdText, -1)
	for _, m := range matches {
		if strings.Contains(m[1], "operation =") {
			v.hclText = m[1]
			break
		}
	}

	if v.hclText == "" {
		// Try without hcl tag
		hclRe2 := regexp.MustCompile("(?s)```\\s*(operation\\s*=.*?)\\s*```")
		m2 := hclRe2.FindStringSubmatch(mdText)
		if len(m2) > 1 {
			v.hclText = m2[1]
		}
	}

	v.parsePrimitives()
	v.parseConcepts()
	v.parseEdges()
	v.parseDrift()
	v.parseHaltChecks()

	return v
}

func (v *Validator) parseBlockArray(blockName string) []map[string]string {
	var results []map[string]string
	if v.hclText == "" {
		return results
	}

	var blockContent string

	// Try array syntax first: blockName = [ ... ] or blockName [ ... ]
	arrayStartRe := regexp.MustCompile(`(?m)` + regexp.QuoteMeta(blockName) + `\s*(?:=|)\s*\[`)
	startLoc := arrayStartRe.FindStringIndex(v.hclText)
	if startLoc != nil {
		startIdx := startLoc[1]
		depth := 1
		endIdx := startIdx
		for endIdx < len(v.hclText) && depth > 0 {
			switch v.hclText[endIdx] {
			case '[':
				depth++
			case ']':
				depth--
			}
			endIdx++
		}
		if depth == 0 {
			blockContent = v.hclText[startIdx : endIdx-1]
		}
	}

	// Fall back to block syntax: blockName { ... }
	if blockContent == "" {
		pattern := regexp.MustCompile("(?s)" + regexp.QuoteMeta(blockName) + "\\s*\\{(.*?)\\n\\s*\\}")
		match := pattern.FindStringSubmatchIndex(v.hclText)
		if len(match) >= 4 {
			blockContent = v.hclText[match[2]:match[3]]
		}
	}

	if blockContent == "" {
		return results
	}

	entries := splitBlockEntries(blockContent)
	for _, entry := range entries {
		item := make(map[string]string)
		// Extract key = "value" pairs anywhere in the entry
		kvRe := regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)
		for _, kv := range kvRe.FindAllStringSubmatch(entry, -1) {
			item[kv[1]] = kv[2]
		}
		// Extract key = true/false
		boolRe := regexp.MustCompile(`(\w+)\s*=\s*(true|false)`)
		for _, kv := range boolRe.FindAllStringSubmatch(entry, -1) {
			item[kv[1]] = kv[2]
		}
		// Extract arrays: constrains = [ "a", "b" ]
		arrRe := regexp.MustCompile(`(\w+)\s*=\s*\[\s*"([^"\]]*)"\s*\]`)
		for _, kv := range arrRe.FindAllStringSubmatch(entry, -1) {
			item[kv[1]] = kv[2]
		}
		if len(item) > 0 {
			results = append(results, item)
		}
	}

	return results
}

func splitBlockEntries(content string) []string {
	var entries []string
	var current strings.Builder
	depth := 0
	inEntry := false

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if inEntry {
				current.WriteString(line)
				current.WriteString("\n")
			}
			continue
		}

		openCount := strings.Count(line, "{")
		closeCount := strings.Count(line, "}")

		if depth == 0 && openCount > 0 {
			inEntry = true
			current.Reset()
		}

		if inEntry {
			current.WriteString(line)
			current.WriteString("\n")
		}

		depth += openCount - closeCount

		if depth == 0 && inEntry {
			entries = append(entries, current.String())
			inEntry = false
		}
	}

	return entries
}

func (v *Validator) parsePrimitives() {
	v.primitives = v.parseBlockArray("primitives")
	for _, p := range v.primitives {
		if id, ok := p["nodeId"]; ok {
			v.nodeIds[id] = true
			v.primitiveIds[id] = true
		}
	}
}

func (v *Validator) parseConcepts() {
	v.concepts = v.parseBlockArray("concepts")
	for _, c := range v.concepts {
		if id, ok := c["id"]; ok {
			v.nodeIds[id] = true
			v.conceptIds[id] = true
		}
	}
}

func (v *Validator) parseEdges() {
	v.edges = v.parseBlockArray("edges")
}

func (v *Validator) parseDrift() {
	v.drifts = v.parseBlockArray("drift")
}

func (v *Validator) parseHaltChecks() {
	v.halts = v.parseBlockArray("halt_checks")
}

func (v *Validator) Validate() []Result {
	var results []Result

	// === STRUCTURE CHECKS ===
	results = append(results, v.checkS1())
	results = append(results, v.checkS2())
	results = append(results, v.checkS3())
	results = append(results, v.checkS5())
	results = append(results, v.checkS6())
	results = append(results, v.checkS7())
	results = append(results, v.checkS8())
	results = append(results, v.checkS9())
	results = append(results, v.checkS10())

	// === CONTENT CHECKS ===
	results = append(results, v.checkC1())
	results = append(results, v.checkC2())
	results = append(results, v.checkC4())
	results = append(results, v.checkC6())
	results = append(results, v.checkC7())
	results = append(results, v.checkC8())
	results = append(results, v.checkC9())
	results = append(results, v.checkC10())
	results = append(results, v.checkC11())

	// === REFERENCE CHECKS ===
	results = append(results, v.checkR1())
	results = append(results, v.checkR2())
	results = append(results, v.checkR5())
	results = append(results, v.checkR6())

	// === GRAPH CHECKS ===
	results = append(results, v.checkG1())
	results = append(results, v.checkG2())
	results = append(results, v.checkG3())
	results = append(results, v.checkG4())
	results = append(results, v.checkG6())
	results = append(results, v.checkG7())

	return results
}

// S1: operation = "trace"
func (v *Validator) checkS1() Result {
	r := Result{ID: "S1", Check: "operation = \"trace\""}
	if v.hclText == "" {
		r.Passed = false
		r.Details = append(r.Details, "No HCL envelope found in markdown")
		return r
	}
	if strings.Contains(v.hclText, `operation = "trace"`) {
		r.Passed = true
	} else if strings.Contains(v.hclText, `operation = `) {
		r.Passed = false
		re := regexp.MustCompile(`operation\s*=\s*"([^"]+)"`)
		m := re.FindStringSubmatch(v.hclText)
		if len(m) > 1 {
			r.Details = append(r.Details, fmt.Sprintf("Expected 'trace', got '%s'", m[1]))
		} else {
			r.Details = append(r.Details, "operation value not found or malformed")
		}
	} else {
		r.Passed = false
		r.Details = append(r.Details, "operation field missing")
	}
	return r
}

// S2: Required top-level keys
func (v *Validator) checkS2() Result {
	r := Result{ID: "S2", Check: "Required top-level keys present"}
	required := []string{"scope", "document", "layers", "edges", "drift", "halt_checks"}
	for _, key := range required {
		if !strings.Contains(v.hclText, key) {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("Missing top-level key: %s", key))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S3: document.statement is single sentence
func (v *Validator) checkS3() Result {
	r := Result{ID: "S3", Check: "document.statement is single sentence"}
	re := regexp.MustCompile(`statement\s*=\s*"([^"]*)"`)
	m := re.FindStringSubmatch(v.hclText)
	if len(m) < 2 {
		r.Passed = false
		r.Details = append(r.Details, "document.statement not found")
		return r
	}
	stmt := m[1]
	periods := strings.Count(stmt, ".")
	if stmt == "" {
		r.Passed = false
		r.Details = append(r.Details, "document.statement is empty")
	} else if periods > 1 {
		r.Passed = false
		r.Details = append(r.Details, fmt.Sprintf("Multiple sentences detected (%d periods): %s", periods, stmt))
	} else {
		r.Passed = true
	}
	return r
}

// S5: document.disciplines
func (v *Validator) checkS5() Result {
	r := Result{ID: "S5", Check: "document.disciplines format"}
	// Count disciplines blocks
	blocks := v.parseBlockArray("disciplines")
	if len(blocks) == 0 {
		r.Passed = false
		r.Details = append(r.Details, "No disciplines found")
		return r
	}
	for i, d := range blocks {
		if _, ok := d["id"]; !ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("discipline[%d] missing 'id'", i))
		}
		if _, ok := d["desc"]; !ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("discipline[%d] missing 'desc'", i))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S6: layers.concepts
func (v *Validator) checkS6() Result {
	r := Result{ID: "S6", Check: "layers.concepts format"}
	if len(v.concepts) == 0 {
		r.Passed = false
		r.Details = append(r.Details, "No concepts found")
		return r
	}
	for i, c := range v.concepts {
		if _, ok := c["id"]; !ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("concept[%d] missing 'id'", i))
		}
		if _, ok := c["desc"]; !ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("concept[%d] missing 'desc'", i))
		}
		if _, ok := c["source"]; !ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("concept[%d] missing 'source'", i))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S7: layers.primitives
func (v *Validator) checkS7() Result {
	r := Result{ID: "S7", Check: "layers.primitives format"}
	if len(v.primitives) == 0 {
		r.Passed = false
		r.Details = append(r.Details, "No primitives found")
		return r
	}
	required := []string{"nodeId", "class", "evolution", "desc", "boundary"}
	for i, p := range v.primitives {
		for _, field := range required {
			if _, ok := p[field]; !ok {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s') missing '%s'", i, p["nodeId"], field))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S8: edges format
func (v *Validator) checkS8() Result {
	r := Result{ID: "S8", Check: "edges format"}
	for i, e := range v.edges {
		for _, field := range []string{"from", "to", "relation", "confidence"} {
			if _, ok := e[field]; !ok {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("edge[%d] missing '%s'", i, field))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S9: drift format
func (v *Validator) checkS9() Result {
	r := Result{ID: "S9", Check: "drift format"}
	for i, d := range v.drifts {
		for _, field := range []string{"signal", "target", "desc"} {
			if _, ok := d[field]; !ok {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("drift[%d] missing '%s'", i, field))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// S10: halt_checks format
func (v *Validator) checkS10() Result {
	r := Result{ID: "S10", Check: "halt_checks format"}
	for i, h := range v.halts {
		for _, field := range []string{"check", "passed", "evidence"} {
			if _, ok := h[field]; !ok {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("halt_check[%d] missing '%s'", i, field))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C1: nodeId PascalCase
func (v *Validator) checkC1() Result {
	r := Result{ID: "C1", Check: "nodeId PascalCase ^[A-Z][A-Za-z0-9]*$"}
	re := regexp.MustCompile("^[A-Z][A-Za-z0-9]*$")
	for i, p := range v.primitives {
		id := p["nodeId"]
		if !re.MatchString(id) {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d].nodeId = '%s'", i, id))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C2: nodeId no dots or slashes
func (v *Validator) checkC2() Result {
	r := Result{ID: "C2", Check: "nodeId contains no . or /"}
	for i, p := range v.primitives {
		id := p["nodeId"]
		if strings.Contains(id, ".") || strings.Contains(id, "/") {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d].nodeId = '%s'", i, id))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C4: Concept id PascalCase
func (v *Validator) checkC4() Result {
	r := Result{ID: "C4", Check: "Concept id PascalCase ^[A-Z][A-Za-z0-9]*$"}
	re := regexp.MustCompile("^[A-Z][A-Za-z0-9]*$")
	for i, c := range v.concepts {
		id := c["id"]
		if !re.MatchString(id) {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("concept[%d].id = '%s'", i, id))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C6: class enum
func (v *Validator) checkC6() Result {
	r := Result{ID: "C6", Check: "primitive.class is info|io"}
	for i, p := range v.primitives {
		cls := p["class"]
		if cls != "info" && cls != "io" {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s').class = '%s'", i, p["nodeId"], cls))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C7: evolution enum
func (v *Validator) checkC7() Result {
	r := Result{ID: "C7", Check: "primitive.evolution is real|step|plan"}
	for i, p := range v.primitives {
		evo := p["evolution"]
		if evo != "real" && evo != "step" && evo != "plan" {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s').evolution = '%s'", i, p["nodeId"], evo))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C8: edge relation enum
func (v *Validator) checkC8() Result {
	r := Result{ID: "C8", Check: "edge.relation is in allowed set"}
	valid := map[string]bool{
		"instance_of": true, "part_of": true, "depends_on": true,
		"blocks": true, "duplicates": true, "evolves_from": true,
	}
	for i, e := range v.edges {
		rel := e["relation"]
		if !valid[rel] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d]: relation = '%s'", i, rel))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C9: confidence enum
func (v *Validator) checkC9() Result {
	r := Result{ID: "C9", Check: "edge.confidence is low|medium|high"}
	for i, e := range v.edges {
		c := e["confidence"]
		if c != "low" && c != "medium" && c != "high" {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d]: confidence = '%s'", i, c))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C10: drift signal enum
func (v *Validator) checkC10() Result {
	r := Result{ID: "C10", Check: "drift.signal is ghost|smuggle|break|drift"}
	valid := map[string]bool{"ghost": true, "smuggle": true, "break": true, "drift": true}
	for i, d := range v.drifts {
		sig := d["signal"]
		if !valid[sig] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("drift[%d]: signal = '%s'", i, sig))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// C11: instance_of XOR part_of
func (v *Validator) checkC11() Result {
	r := Result{ID: "C11", Check: "primitive has exactly one of instance_of or part_of"}
	for i, p := range v.primitives {
		hasIO := p["instance_of"] != ""
		hasPO := p["part_of"] != ""
		if hasIO && hasPO {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s') has BOTH instance_of and part_of", i, p["nodeId"]))
		} else if !hasIO && !hasPO {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s') has NEITHER instance_of nor part_of", i, p["nodeId"]))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// R1: instance_of references valid Concept
func (v *Validator) checkR1() Result {
	r := Result{ID: "R1", Check: "instance_of references valid Concept"}
	for i, p := range v.primitives {
		if io, ok := p["instance_of"]; ok && io != "" {
			if !v.conceptIds[io] {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s').instance_of = '%s' — not a known Concept", i, p["nodeId"], io))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// R2: part_of references valid Primitive
func (v *Validator) checkR2() Result {
	r := Result{ID: "R2", Check: "part_of references valid Primitive"}
	for i, p := range v.primitives {
		if po, ok := p["part_of"]; ok && po != "" {
			if !v.primitiveIds[po] {
				r.Passed = false
				r.Details = append(r.Details, fmt.Sprintf("primitive[%d] ('%s').part_of = '%s' — not a known Primitive", i, p["nodeId"], po))
			}
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// R5: unique nodeIds
func (v *Validator) checkR5() Result {
	r := Result{ID: "R5", Check: "All primitive nodeIds unique"}
	seen := make(map[string]int)
	for i, p := range v.primitives {
		id := p["nodeId"]
		if prev, ok := seen[id]; ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("Duplicate nodeId '%s' at primitive[%d] and primitive[%d]", id, prev, i))
		} else {
			seen[id] = i
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// R6: unique Concept ids
func (v *Validator) checkR6() Result {
	r := Result{ID: "R6", Check: "All Concept ids unique"}
	seen := make(map[string]int)
	for i, c := range v.concepts {
		id := c["id"]
		if prev, ok := seen[id]; ok {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("Duplicate Concept id '%s' at concept[%d] and concept[%d]", id, prev, i))
		} else {
			seen[id] = i
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G1: instance_of edge from Concept to Primitive
func (v *Validator) checkG1() Result {
	r := Result{ID: "G1", Check: "instance_of edge: from=Concept, to=Primitive"}
	for i, e := range v.edges {
		if e["relation"] != "instance_of" {
			continue
		}
		from, to := e["from"], e["to"]
		if !v.conceptIds[from] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] instance_of: from='%s' is not a Concept", i, from))
		}
		if !v.primitiveIds[to] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] instance_of: to='%s' is not a Primitive", i, to))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G2: part_of edge both ends are Primitive
func (v *Validator) checkG2() Result {
	r := Result{ID: "G2", Check: "part_of edge: both ends are Primitive"}
	for i, e := range v.edges {
		if e["relation"] != "part_of" {
			continue
		}
		from, to := e["from"], e["to"]
		if !v.primitiveIds[from] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] part_of: from='%s' is not a Primitive", i, from))
		}
		if !v.primitiveIds[to] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] part_of: to='%s' is not a Primitive", i, to))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G3: depends_on/blocks/duplicates/evolves_from both ends are Primitive
func (v *Validator) checkG3() Result {
	r := Result{ID: "G3", Check: "Non-instance_of edges: both ends are Primitive"}
	nonIO := map[string]bool{"part_of": true, "depends_on": true, "blocks": true, "duplicates": true, "evolves_from": true}
	for i, e := range v.edges {
		rel := e["relation"]
		if !nonIO[rel] {
			continue
		}
		from, to := e["from"], e["to"]
		if !v.primitiveIds[from] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] %s: from='%s' is not a Primitive", i, rel, from))
		}
		if !v.primitiveIds[to] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] %s: to='%s' is not a Primitive", i, rel, to))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G4: No Concept→Concept edges
func (v *Validator) checkG4() Result {
	r := Result{ID: "G4", Check: "No Concept→Concept edges"}
	for i, e := range v.edges {
		from, to := e["from"], e["to"]
		if v.conceptIds[from] && v.conceptIds[to] {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d]: {from='%s', to='%s'} — both are Concepts", i, from, to))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G6: constrains not in edges
func (v *Validator) checkG6() Result {
	r := Result{ID: "G6", Check: "constrains does not appear in edges"}
	for i, e := range v.edges {
		if e["relation"] == "constrains" {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("edge[%d] has forbidden relation 'constrains'", i))
		}
	}
	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}

// G7: Root constraint — every Primitive traces to a Concept
func (v *Validator) checkG7() Result {
	r := Result{ID: "G7", Check: "Root constraint: all primitives trace to Concept"}

	// Build adjacency for part_of (child -> parent)
	partOfParent := make(map[string]string)
	for _, e := range v.edges {
		if e["relation"] == "part_of" {
			partOfParent[e["to"]] = e["from"] // child -> parent
		}
	}

	// For each primitive, trace up
	for _, p := range v.primitives {
		id := p["nodeId"]
		reachesConcept := false

		// Check direct instance_of
		if p["instance_of"] != "" && v.conceptIds[p["instance_of"]] {
			reachesConcept = true
		}

		// Check part_of chain
		if !reachesConcept && p["part_of"] != "" {
			visited := make(map[string]bool)
			current := p["part_of"]
			for current != "" && !visited[current] {
				visited[current] = true
				// Find the primitive
				var parentPrimitive map[string]string
				for _, pr := range v.primitives {
					if pr["nodeId"] == current {
						parentPrimitive = pr
						break
					}
				}
				if parentPrimitive == nil {
					break
				}
				if parentPrimitive["instance_of"] != "" && v.conceptIds[parentPrimitive["instance_of"]] {
					reachesConcept = true
					break
				}
				current = parentPrimitive["part_of"]
			}
		}

		if !reachesConcept {
			r.Passed = false
			r.Details = append(r.Details, fmt.Sprintf("Primitive '%s' cannot trace to any Concept via instance_of or part_of chain", id))
		}
	}

	if len(r.Details) == 0 {
		r.Passed = true
	}
	return r
}
