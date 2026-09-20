package cardcompiler

var sectionOrder = map[string]int{
	"leading": -1, "role": 0, "policy": 1, "project_context": 2, "active_goal": 3,
	"active_plan": 4, "worktree_status": 5, "task": 6, "component_status": 7,
	"tool_guidance": 8, "trailing": 9, "environment": 10, "skills": 11,
}
