package topo

import (
	"testing"
)

func TestSortEmpty(t *testing.T) {
	result, err := Sort(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestSortSingle(t *testing.T) {
	result, err := Sort([]string{"a"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "a" {
		t.Fatalf("expected [a], got %v", result)
	}
}

func TestSortNoDeps(t *testing.T) {
	nodes := []string{"c", "a", "b"}
	result, err := Sort(nodes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should preserve input order when no deps.
	for i, n := range nodes {
		if result[i] != n {
			t.Fatalf("expected result[%d]=%q, got %q", i, n, result[i])
		}
	}
}

func TestSortLinear(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	deps := map[string][]string{
		"b": {"a"},
		"c": {"b"},
	}
	result, err := Sort(nodes, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"a", "b", "c"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, n := range expected {
		if result[i] != n {
			t.Fatalf("expected result[%d]=%q, got %q", i, n, result[i])
		}
	}
}

func TestSortDiamond(t *testing.T) {
	// a → b → d
	// a → c → d
	nodes := []string{"a", "b", "c", "d"}
	deps := map[string][]string{
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	result, err := Sort(nodes, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// a must be first, d must be last.
	if result[0] != "a" {
		t.Fatalf("expected a first, got %v", result)
	}
	if result[len(result)-1] != "d" {
		t.Fatalf("expected d last, got %v", result)
	}
	// b and c should appear between a and d.
	idxB, idxC := -1, -1
	for i, n := range result {
		if n == "b" {
			idxB = i
		}
		if n == "c" {
			idxC = i
		}
	}
	if idxB < 0 || idxC < 0 {
		t.Fatalf("b and c must be in result, got %v", result)
	}
	if idxB > sliceIndex(result, "d") || idxC > sliceIndex(result, "d") {
		t.Fatalf("b and c must come before d, got %v", result)
	}
}

func TestSortTwoNodeCycle(t *testing.T) {
	nodes := []string{"a", "b"}
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	_, err := Sort(nodes, deps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	if len(cycleErr.Path) < 2 {
		t.Fatalf("cycle path too short: %v", cycleErr.Path)
	}
	// The path should start and end with the same node.
	if cycleErr.Path[0] != cycleErr.Path[len(cycleErr.Path)-1] {
		t.Fatalf("cycle path should be circular, got %v", cycleErr.Path)
	}
}

func TestSortThreeNodeCycle(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	deps := map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"b"},
	}
	_, err := Sort(nodes, deps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	// The cycle should contain all three nodes.
	if len(cycleErr.Path) != 4 { // a, b, c, a
		t.Fatalf("expected 4-element cycle path, got %v", cycleErr.Path)
	}
}

func TestSortPartialCycle(t *testing.T) {
	// a has no deps, b → c → b (cycle among b,c)
	nodes := []string{"a", "b", "c"}
	deps := map[string][]string{
		"b": {"c"},
		"c": {"b"},
	}
	_, err := Sort(nodes, deps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	// Cycle should involve b and c.
	cycleSet := map[string]bool{}
	for _, n := range cycleErr.Path {
		cycleSet[n] = true
	}
	if !cycleSet["b"] || !cycleSet["c"] {
		t.Fatalf("cycle should include b and c, got %v", cycleErr.Path)
	}
}

func TestSortMissingDep(t *testing.T) {
	nodes := []string{"a", "b"}
	deps := map[string][]string{
		"a": {"nonexistent"},
	}
	_, err := Sort(nodes, deps)
	if err == nil {
		t.Fatal("expected missing error")
	}
	missingErr, ok := err.(*MissingError)
	if !ok {
		t.Fatalf("expected *MissingError, got %T: %v", err, err)
	}
	if missingErr.Missing != "nonexistent" {
		t.Fatalf("expected missing dep 'nonexistent', got %q", missingErr.Missing)
	}
	if missingErr.Node != "a" {
		t.Fatalf("expected node 'a', got %q", missingErr.Node)
	}
}

func TestSortStability(t *testing.T) {
	// Multiple valid orderings; the algorithm should always produce the same
	// one based on input order.
	nodes := []string{"d", "a", "c", "b"}
	deps := map[string][]string{
		"b": {"a"},
		"c": {"a"},
	}
	var firstResult []string
	for i := 0; i < 10; i++ {
		result, err := Sort(nodes, deps)
		if err != nil {
			t.Fatalf("unexpected error on run %d: %v", i, err)
		}
		if firstResult == nil {
			firstResult = result
			continue
		}
		for j := range result {
			if result[j] != firstResult[j] {
				t.Fatalf("non-deterministic: run 0 gave %v, run %d gave %v", firstResult, i, result)
			}
		}
	}
}

// helper
func sliceIndex(s []string, target string) int {
	for i, v := range s {
		if v == target {
			return i
		}
	}
	return -1
}
