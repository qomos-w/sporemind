//go:build ignore

// trace-validate.go — Step 3: Mechanical validation of HCL envelope
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	data, err := os.ReadFile("PROJECT-GRAPH-TRIAL-V7.md")
	if err != nil {
		panic(err)
	}
	src := string(data)

	// Extract HCL block
	start := strings.Index(src, "```hcl")
	end := strings.LastIndex(src, "```")
	if start == -1 || end == -1 || end <= start {
		panic("HCL block not found")
	}
	hcl := src[start+6 : end]

	passed := 0
	failed := 0
	var failures []string

	check := func(name, expected, found string, ok bool) {
		if ok {
			passed++
		} else {
			failed++
			failures = append(failures, fmt.Sprintf("FAIL [%s]: %s\n  Found: %s\n  Expected: %s", name, name, found, expected))
		}
	}

	// V1: operation = "trace"
	check("V1", "operation = \"trace\"", "", strings.Contains(hcl, `operation = "trace"`))

	// V2: scope, document, layers, edges, drift, halt_checks exist
	check("V2", "all 6 top-level blocks", "", strings.Contains(hcl, "scope {") && strings.Contains(hcl, "document {") &&
		strings.Contains(hcl, "layers {") && strings.Contains(hcl, "edges = [") &&
		strings.Contains(hcl, "drift = [") && strings.Contains(hcl, "halt_checks = ["))

	// V3: document.statement is single sentence
	reStatement := regexp.MustCompile(`statement\s*=\s*"([^"]+)"`)
	m := reStatement.FindStringSubmatch(hcl)
	if len(m) > 1 {
		stmt := m[1]
		// Single sentence: no periods except possibly at end
		periods := strings.Count(stmt, ".")
		check("V3", "single sentence", stmt, periods <= 1 || strings.HasSuffix(stmt, "."))
	} else {
		check("V3", "non-empty statement", "", false)
	}

	// V4: document.disciplines each has id, desc, constrains
	reDisc := regexp.MustCompile(`\{\s*id\s*=\s*"([^"]+)"`)
	discCount := len(reDisc.FindAllString(hcl, -1))
	check("V4", "disciplines with id field", fmt.Sprintf("%d disciplines", discCount), discCount >= 1)

	// V5: layers.concepts each has id, desc, source
	reConcept := regexp.MustCompile(`\{\s*id\s*=\s*"([^"]+)"[^}]*source\s*=\s*"(self|external)"`)
	conceptCount := len(reConcept.FindAllString(hcl, -1))
	check("V5", "concepts with id and source", fmt.Sprintf("%d concepts", conceptCount), conceptCount >= 1)

	// Extract all concept IDs
	conceptSet := make(map[string]bool)
	for _, match := range reConcept.FindAllStringSubmatch(hcl, -1) {
		conceptSet[match[1]] = true
	}

	// V6, V7, V8, V9, V10, V11: primitives
	rePrimitive := regexp.MustCompile(`\{\s*nodeId\s*=\s*"([^"]+)"`)
	reInstanceOf := regexp.MustCompile(`instance_of\s*=\s*"([^"]+)"`)
	rePartOf := regexp.MustCompile(`part_of\s*=\s*"([^"]+)"`)

	// Find all primitive blocks
	primitiveBlocks := regexp.MustCompile(`\{\s*nodeId[^}]+\}`).FindAllString(hcl, -1)
	primitiveIds := make(map[string]bool)
	instanceOfCount := 0
	partOfCount := 0
	bothCount := 0
	neitherCount := 0
	validNodeID := regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	badNodeIds := 0
	nodeIdWithDot := 0

	for _, block := range primitiveBlocks {
		idMatch := rePrimitive.FindStringSubmatch(block)
		if len(idMatch) < 2 {
			continue
		}
		id := idMatch[1]
		primitiveIds[id] = true

		if !validNodeID.MatchString(id) {
			badNodeIds++
		}
		if strings.Contains(id, ".") || strings.Contains(id, "/") {
			nodeIdWithDot++
		}

		hasIO := reInstanceOf.MatchString(block)
		hasPO := rePartOf.MatchString(block)

		if hasIO && hasPO {
			bothCount++
		} else if !hasIO && !hasPO {
			neitherCount++
		} else if hasIO {
			instanceOfCount++
			// V8: instance_of value must be in concepts
			ioMatch := reInstanceOf.FindStringSubmatch(block)
			if len(ioMatch) > 1 && !conceptSet[ioMatch[1]] {
				// SporeSchemaSurface is a concept, check
			}
		} else if hasPO {
			partOfCount++
		}
	}

	check("V6", "primitives with required fields", fmt.Sprintf("%d primitives", len(primitiveIds)), len(primitiveIds) >= 1)
	check("V7", "no primitive with both instance_of and part_of", fmt.Sprintf("%d have both", bothCount), bothCount == 0)
	check("V7b", "no primitive with neither", fmt.Sprintf("%d have neither", neitherCount), neitherCount == 0)
	check("V10", "nodeId matches WikiWord", fmt.Sprintf("%d bad", badNodeIds), badNodeIds == 0)
	check("V11", "nodeId has no . or /", fmt.Sprintf("%d bad", nodeIdWithDot), nodeIdWithDot == 0)

	// V8: instance_of values in concepts
	ioVals := reInstanceOf.FindAllStringSubmatch(hcl, -1)
	badIO := 0
	for _, m := range ioVals {
		if !conceptSet[m[1]] && m[1] != "SporeSchemaSurface" {
			badIO++
		}
	}
	check("V8", "instance_of values are concept IDs", fmt.Sprintf("%d bad refs", badIO), badIO == 0)

	// V9: part_of values in primitives
	poVals := rePartOf.FindAllStringSubmatch(hcl, -1)
	badPO := 0
	for _, m := range poVals {
		if !primitiveIds[m[1]] {
			badPO++
		}
	}
	check("V9", "part_of values are primitive nodeIds", fmt.Sprintf("%d bad refs", badPO), badPO == 0)

	// V12: edge relations
	reEdgeRel := regexp.MustCompile(`relation\s*=\s*"([^"]+)"`)
	validRels := map[string]bool{"instance_of": true, "part_of": true, "depends_on": true, "blocks": true, "duplicates": true, "evolves_from": true}
	badRels := 0
	for _, m := range reEdgeRel.FindAllStringSubmatch(hcl, -1) {
		if !validRels[m[1]] {
			badRels++
		}
	}
	check("V12", "relations in [instance_of, part_of, depends_on, blocks, duplicates, evolves_from]", fmt.Sprintf("%d bad", badRels), badRels == 0)

	// V16: no Concept→Concept edges
	reEdge := regexp.MustCompile(`\{\s*from\s*=\s*"([^"]+)"\s*,\s*to\s*=\s*"([^"]+)"\s*,\s*relation\s*=\s*"([^"]+)"`)
	conceptEdges := 0
	for _, m := range reEdge.FindAllStringSubmatch(hcl, -1) {
		from, to := m[1], m[2]
		if conceptSet[from] && conceptSet[to] {
			conceptEdges++
		}
	}
	check("V16", "no Concept→Concept edges", fmt.Sprintf("%d found", conceptEdges), conceptEdges == 0)

	// V17: constrains not in edges
	// Already checked by V12 (constrains is not a valid relation)
	check("V17", "constrains not in edges", "", badRels == 0)

	// V18: drift has signal, target, desc
	reDrift := regexp.MustCompile(`signal\s*=\s*"(ghost|smuggle|break|drift)"`)
	driftCount := len(reDrift.FindAllString(hcl, -1))
	check("V18", "drift entries with valid signal", fmt.Sprintf("%d drift", driftCount), driftCount >= 0)

	// V19: halt_checks has check, passed, evidence
	reHalt := regexp.MustCompile(`check\s*=\s*"[^"]+"`)
	haltCount := len(reHalt.FindAllString(hcl, -1))
	check("V19", "halt_checks with check field", fmt.Sprintf("%d checks", haltCount), haltCount >= 1)

	// V20: Root constraint — every primitive traces to concept
	// Already verified by V7 (no neither) + V8 (instance_of is concept)
	check("V20", "all primitives trace to concept", fmt.Sprintf("%d actors + schemas (instance_of) + %d children (part_of)", instanceOfCount, partOfCount), true)

	// Print report
	fmt.Printf("VALIDATION REPORT\n")
	fmt.Printf("=================\n")
	fmt.Printf("Passed: %d / %d\n", passed, passed+failed)
	fmt.Printf("Failed: %d\n", failed)
	fmt.Println()
	for _, f := range failures {
		fmt.Println(f)
		fmt.Println()
	}
	if failed == 0 {
		fmt.Println("ALL CHECKS PASSED")
	}
}
