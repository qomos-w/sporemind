package project

import (
	"strings"
	"testing"
)

// Regression: setDependsOnInDataBlock historically consumed the sibling keys
// that follow depends_on's value lines (exec/capabilities/budget at the same
// 2-space indent as depends_on), deleting the entire rest of the data block
// on every depends_on rewrite (wiki_set_task_dependencies, topo projection).
// Observed live: script-orchestrate-get-card lost data.exec after a
// set_task_dependencies call and fell back to worker_task dispatch.
func TestSetDependsOnPreservesSiblingKeys(t *testing.T) {
	raw := `---
id: card-x
type: task
tags: [m]
status: todo
parent: map-1
data:
  category: "execute"
  depends_on:
    - old-dep
  exec:
    kind: script
    capabilities:
      - project.wiki_get_card
    budget:
      timeout_sec: 30
---

body
`
	out := setDependsOnInDataBlock(raw, []string{"new-dep"})
	for _, want := range []string{
		"- new-dep",
		"exec:",
		"kind: script",
		"- project.wiki_get_card",
		"timeout_sec: 30",
		"category: \"execute\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old-dep") {
		t.Errorf("old dep survived:\n%s", out)
	}
}

func TestSetDependsOnInsertWhenMissingKeepsBlock(t *testing.T) {
	raw := `---
id: card-y
type: task
status: todo
data:
  exec:
    kind: script
---

body
`
	out := setDependsOnInDataBlock(raw, []string{"dep-a"})
	if !strings.Contains(out, "- dep-a") {
		t.Errorf("dep missing:\n%s", out)
	}
	if !strings.Contains(out, "kind: script") {
		t.Errorf("exec block lost:\n%s", out)
	}
}
