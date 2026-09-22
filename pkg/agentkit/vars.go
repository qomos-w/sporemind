package agentkit

import "embed"

// --- embedded card assets ---

//go:embed builtin/cards/**
var embeddedAssets embed.FS

// --- mechanism prompts ---

// Mechanism prompts are code-embedded infrastructure prompts for internal
// LLM calls (compaction, aggregation, goal review, bypass, explore summary).
// They are NOT mountable component cards — they serve specialized internal
// operations and are consumed directly by the code that drives those operations.

//go:embed prompts/*.md
var mechanismPromptFS embed.FS

var (
	GoalReviewPrompt              = mechanismPrompt("goal-review")
	GoalCompletionCheck           = mechanismPrompt("goal-completion-check")
	GoalCompletionCheckBound      = mechanismPrompt("goal-completion-check-bound")
	GoalPreviousAssessment        = mechanismPrompt("goal-previous-assessment")
	CompactionL1SummaryPrompt     = mechanismPrompt("compaction-l1-summary")
	CompactionL2PlusSummaryPrompt = mechanismPrompt("compaction-l2plus-summary")
	AggregatorIntent              = mechanismPrompt("aggregator-intent")
	BypassPermission              = mechanismPrompt("bypass-permission")
	ExploreSummaryExplorer        = mechanismPrompt("explore-summary-explorer")
	ExploreSummaryGeneral         = mechanismPrompt("explore-summary-general")
)

// --- builtin card registry ---

// PluginDevBundleID is the tool-usage bundle exposing the appmanager
// plugin development callables (dev_generate / dev_gate / register etc.).
// It is not part of any kind's DefaultBundleIDs; the workspace auto-attaches
// it to agents spawned in a dev-app project (see spawnAgentViaProject).
const PluginDevBundleID = "builtin:bundle:plugin-dev"

// SwarmBundleID is the tool-usage bundle exposing the agent-to-agent swarm
// surface (workspace.agent_spawn_swarm and its observation/termination
// companions). Kind configs list it in DefaultBundleIDs, and the swarm spawn
// handler re-attaches it to every spawned child so recursion works.
const SwarmBundleID = "builtin:bundle:swarm"

// BuiltinCards is the complete list of builtin component cards.
var BuiltinCards = []BuiltinCard{
	{Title: "builtin:bundle:project-wiki"},
	{Title: "builtin:bundle:workflow-tools"},
	{Title: "builtin:bundle:debug"},
	{Title: "builtin:bundle:image-gen"},
	{Title: "builtin:bundle:image-recognition"},
	{Title: "builtin:bundle:video-gen"},
	{Title: "builtin:mode:goal"},
	{Title: "builtin:mode:workflow"},
	{Title: "builtin:mode:worktree"},
	{Title: "builtin:bundle:file-tools"},
	{Title: "builtin:bundle:git-tools"},
	{Title: "builtin:bundle:shell-tools"},
	{Title: "builtin:bundle:ssh-tools"},
	{Title: "builtin:bundle:planning"},
	{Title: "builtin:bundle:goal"},
	{Title: "builtin:bundle:fork-explore"},
	{Title: "builtin:bundle:fork-review"},
	{Title: "builtin:bundle:fork-general"},
	{Title: "builtin:bundle:fork-dream"},
	{Title: "builtin:bundle:swarm"},
	{Title: "builtin:bundle:workspace-tools"},
	{Title: "builtin:bundle:app-tools"},
	{Title: "builtin:bundle:interface-controls"},
	{Title: "builtin:bundle:tutor"},
	{Title: "builtin:bundle:browser-tools"},
	{Title: "builtin:bundle:browser-use"},
	{Title: "builtin:bundle:browser-crawl"},
	{Title: "builtin:bundle:web-search"},
	{Title: "builtin:bundle:scheduler"},
	{Title: "builtin:bundle:worktree"},
	{Title: "builtin:bundle:computeruse-tools"},
	{Title: "builtin:bundle:coordinator-wearable"},
	{Title: "builtin:mode:memory"},
	{Title: "builtin:mode:scheduler"},
	{Title: "builtin:bundle:bundle-use"},
	{Title: "builtin:bundle:workbench-attention"},
	{Title: "builtin:bundle:plugin-dev"},
}

// BuiltinCardRenames maps retired builtin card IDs to their replacements so
// agents persisted before a rename can migrate their mounts on startup.
var BuiltinCardRenames = map[string]string{
	"builtin:bundle:omnibox":      "builtin:bundle:interface-controls",
	"builtin:prompt:debug":        "builtin:bundle:debug",
	"skill:frontdesign":           "builtin:bundle:interface-controls",
	"builtin:bundle:wiremark":     "builtin:bundle:interface-controls",
	"builtin:bundle:front-design": "builtin:bundle:interface-controls",
}
