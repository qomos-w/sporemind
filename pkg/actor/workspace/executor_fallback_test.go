package workspace

import "testing"

// scriptFenceWithoutExecKind is the dispatcher's degradation tripwire: a
// ```spore fenced body with no data.exec.kind means a script card lost its
// exec block (depends_on rewrite historically stripped it) and would
// silently fall back to a worker_task spawn.
func TestScriptFenceWithoutExecKind(t *testing.T) {
	lostExec := `---
id: card-x
type: task
status: todo
data:
  category: "execute"
  depends_on:
    - up
---

` + "```spore" + `
export fun run(): int { return 0 }
` + "```" + `
`
	if !scriptFenceWithoutExecKind(lostExec) {
		t.Error("fence without exec.kind should be flagged")
	}

	withKind := `---
id: card-x
type: task
status: todo
data:
  exec:
    kind: script
---

` + "```spore" + `
export fun run(): int { return 0 }
` + "```" + `
`
	if scriptFenceWithoutExecKind(withKind) {
		t.Error("fence with exec.kind=script must not be flagged")
	}

	workerTask := `---
id: card-x
type: task
status: todo
data:
  category: "execute"
---

Implement the thing.
`
	if scriptFenceWithoutExecKind(workerTask) {
		t.Error("plain worker-task card must not be flagged")
	}
}
