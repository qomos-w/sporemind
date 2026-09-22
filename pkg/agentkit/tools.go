package agentkit

import (
	"github.com/qomos-w/sporemind/pkg/domain"
)

// ── Bundle → Callable Resolution ──
//
// Bundle cards are the single source of truth for tool availability.
// Each bundle card's frontmatter declares data.tools (full callable IDs).
// CallableIDsForBundles resolves a list of bundle card IDs to the union
// of their declared callable IDs by loading the embedded card assets.

// CallableIDsForBundles resolves bundle card IDs to the callable IDs they
// declare via frontmatter data.tools. Unknown bundle IDs are silently skipped.
func CallableIDsForBundles(bundleIDs []string) []string {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil
	}
	assetMap := make(map[string]CardAsset, len(assets))
	for _, a := range assets {
		assetMap[a.Title] = a
	}
	seen := make(map[string]struct{})
	var out []string
	for _, id := range bundleIDs {
		asset, ok := assetMap[id]
		if !ok {
			continue
		}
		for _, tool := range asset.Tools {
			if _, dup := seen[tool]; dup {
				continue
			}
			seen[tool] = struct{}{}
			out = append(out, tool)
		}
	}
	return out
}

// ── Fork Tool Specs (bundle-driven) ──

// ForkToolSpecsFromBundles resolves fork tool specs from the data.fork
// declarations embedded in the given bundle card IDs. Each bundle card with a
// data.fork frontmatter entry produces one LLM-facing fork tool spec routed to
// workspace.agent_spawn_by_type. The bundle card is the single source
// of truth for fork tool metadata (toolName, childKind, description); the input
// schema is derived from the toolName (fork_review carries ReviewText/PlanEvidence,
// others carry Description/Prompt/Model/MaxIterations). Unknown bundle IDs and
// cards without a fork declaration are silently skipped.
func ForkToolSpecsFromBundles(bundleIDs []string) []domain.ToolSpec {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil
	}
	assetMap := make(map[string]CardAsset, len(assets))
	for _, a := range assets {
		assetMap[a.Title] = a
	}
	var out []domain.ToolSpec
	for _, id := range bundleIDs {
		asset, ok := assetMap[id]
		if !ok || asset.Fork == nil {
			continue
		}
		if IsInternalForkTool(asset.Fork.ToolName) {
			continue
		}
		out = append(out, forkToolSpec(asset.Fork))
	}
	if len(out) > 0 {
		// Any kind that can fork must also be able to harvest: agent_wait is
		// the collection counterpart of an Async=true fork spawn.
		out = append(out, AgentWaitToolSpec())
	}
	return out
}

// AgentWaitToolSpec returns the LLM-facing spec for agent_wait, the harvest
// counterpart of an async fork. The call is intercepted by the turn engine
// (phaseExecute) before dispatch and never reaches a real callable.
func AgentWaitToolSpec() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "agent_wait",
		Description: "Block the current turn until forked child agents finish (or a timeout elapses), then return their results. Waits for async-forked children spawned earlier in this turn; children that already finished (this turn or a previous one) return immediately with their stored results. Timeout is clamped to a 10s minimum and a 1h hard cap; omitted means 30s. On timeout the call still succeeds, returning partial results with TimedOut=true and the still-running children listed as running — decide whether to wait again, keep working, or abandon them.",
		InputSchema: `{"type":"object","properties":{"AgentIds":{"type":"array","items":{"type":"string"},"description":"Optional list of child agent IDs (as returned by the async fork call) to wait for. Omit to wait for all currently running fork children."},"TimeoutMs":{"type":"number","description":"How long to wait, in milliseconds. Minimum 10000, default 30000, hard cap 3600000."}},"required":[]}`,
		EffectKind:  string(domain.EffectNone),
		CallableID:  "agent_wait",
		ServiceName: "agent",
	}
}

// forkToolSpec builds a single fork ToolSpec from a bundle ForkDecl. The schema
// differs by tool name: fork_review takes ReviewText/PlanEvidence; all others
// take Description/Prompt/Model/MaxIterations/Async.
func forkToolSpec(f *ForkDecl) domain.ToolSpec {
	schema := `{"type":"object","properties":{"Description":{"type":"string","description":"Short 3-5 word task summary"},"Prompt":{"type":"string","description":"Detailed instructions for the sub-agent"},"Model":{"type":"string","description":"Optional model override"},"MaxIterations":{"type":"number","description":"Max LLM dispatch iterations (default 5)"},"Async":{"type":"boolean","description":"Spawn without waiting for the child to finish. The call returns immediately with the child agent id; collect the child's result later with agent_wait. Use this when you want to keep working while the child runs and harvest results at a chosen point in the same turn."}},"required":["Description","Prompt"]}`
	if f.ToolName == "fork_review" {
		schema = `{"type":"object","properties":{"ReviewText":{"type":"string","description":"The text to review. Defaults to the active session goal if empty."},"PlanEvidence":{"type":"array","items":{"type":"string"},"description":"Optional plan card IDs to load as evidence."}},"required":[]}`
	}
	return domain.ToolSpec{
		Name:        f.ToolName,
		Description: f.Description,
		InputSchema: schema,
		EffectKind:  string(domain.EffectNone),
		CallableID:  "workspace.agent_spawn_by_type",
		ServiceName: "workspace",
	}
}

// IsInternalForkTool reports whether a fork tool name is reserved for system
// use and should not be exposed to the LLM tool surface.
func IsInternalForkTool(toolName string) bool {
	return toolName == "fork_dream"
}

// ForkKindMapFromBundles resolves a map from LLM-facing fork tool name to child
// agent kind, sourced from the data.fork declarations of the given bundle card
// IDs. Used by the turn engine to route a fork tool call to the right child role
// without consulting per-kind config.
func ForkKindMapFromBundles(bundleIDs []string) map[string]string {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil
	}
	assetMap := make(map[string]CardAsset, len(assets))
	for _, a := range assets {
		assetMap[a.Title] = a
	}
	out := make(map[string]string)
	for _, id := range bundleIDs {
		asset, ok := assetMap[id]
		if !ok || asset.Fork == nil {
			continue
		}
		out[asset.Fork.ToolName] = asset.Fork.ChildKind
	}
	return out
}

// ── Autonomous Tool Specs ──
//
// These tool specs are injected by appendAutonomousTools based on agent
// kind. They are defined here so they can be referenced uniformly.

// CoderAutonomousTools returns the kind-specific tool specs for a coder
// agent. Media tools are bundle-driven via MediaToolSpecsFromBundles.
func CoderAutonomousTools() []domain.ToolSpec {
	return nil
}

const (
	imageGenBundleID = "builtin:bundle:image-gen"
	videoGenBundleID = "builtin:bundle:video-gen"
)

// ImageRecognitionBundleID is exported so the agent actor can always expose
// recognize_image (see appendAutonomousTools): text-only models re-recognize
// with a task-specific prompt instead of the context-free automatic baseline,
// and vision models use it to inspect images they cannot load into their own
// context (local files, generated assets, MCP tool-result images).
const ImageRecognitionBundleID = "builtin:bundle:image-recognition"

// MediaToolSpecsFromBundles returns media tool specs only for mounted media
// bundles. The specs are hand-authored because their LLM-facing names and
// schemas differ from the callable topology metadata.
func MediaToolSpecsFromBundles(bundleIDs []string) []domain.ToolSpec {
	specs := mediaToolSpecs()
	byBundle := map[string]domain.ToolSpec{
		imageGenBundleID:         specs.imageGenerate,
		ImageRecognitionBundleID: specs.imageRecognize,
		videoGenBundleID:         specs.videoGenerate,
	}
	var out []domain.ToolSpec
	for _, id := range bundleIDs {
		if spec, ok := byBundle[id]; ok {
			out = append(out, spec)
		}
	}
	return out
}

// mediaSpecs bundles the hand-authored media tool specs under stable names so
// bundle gating can reference them without positional indexing.
type mediaSpecs struct {
	imageGenerate  domain.ToolSpec
	imageRecognize domain.ToolSpec
	videoGenerate  domain.ToolSpec
}

func mediaToolSpecs() mediaSpecs {
	return mediaSpecs{
		imageGenerate: domain.ToolSpec{
			Name:        "generate_image",
			Description: "Generate an image from a text prompt using a configured image-generation model (Nano Banana / gpt-image-2). The image is saved to assets/generated/ and the file path is returned. The returned file path is automatically previewed in the chat timeline — do NOT call show_page_thumbnail or open_global_browser on it. Optionally pass InputImage (a single file path, URL, or base64 data) or ReferenceImages (an array of file paths, URLs, or base64 data) to edit an existing image with one or more reference images.",
			InputSchema: `{"type":"object","properties":{"Prompt":{"type":"string","description":"Text description of the image to generate"},"Provider":{"type":"string","description":"Optional provider name to use"},"Model":{"type":"string","description":"Optional specific model name"},"Quality":{"type":"string","enum":["fast","balanced","quality"],"description":"Quality preset (default fast)"},"Size":{"type":"string","enum":["1K","2K","4K"],"description":"Output resolution (default 1K)"},"AspectRatio":{"type":"string","description":"Aspect ratio e.g. 1:1, 16:9, 9:16, 3:2, 4:3"},"InputImage":{"type":"string","description":"Source image for editing: a file path, URL, or base64 data"},"ReferenceImages":{"type":"array","items":{"type":"string"},"description":"Reference images for editing: file paths, URLs, or base64 data"}},"required":["Prompt"]}`,
			EffectKind:  string(domain.EffectReversible), CallableID: "image_generate", ServiceName: "agent",
		},
		imageRecognize: domain.ToolSpec{
			Name:        "recognize_image",
			Description: "Send an image to a vision-capable model and get its textual answer — the way a text-only model sees image content, and the way any model inspects an image it cannot otherwise load into its own context (local file, generated asset, MCP tool-result image). Image accepts a file path, URL, base64 data, or a msg reference: wire text for conversation images ends with [image ref: msg:<id>:<n>] — pass that ref back verbatim to re-examine the SAME image with a targeted Prompt. Always prefer a specific Prompt (your question, with any task context) over the default full description; the automatic baseline description that may already be present is generic and context-free. Optionally pin Provider/Model, or pass Aggregator to route through a named aggregator pool (default: system aggregator picks the first healthy vision-capable unit).",
			InputSchema: `{"type":"object","properties":{"Image":{"type":"string","description":"The image to recognize: a file path, URL, base64 data, or a msg:<id>:<n> reference from an [image ref: msg:...] handle"},"Prompt":{"type":"string","description":"Question or instruction about the image, with any task context; default is a full detailed description"},"Provider":{"type":"string","description":"Optional provider name of the vision model to pin"},"Model":{"type":"string","description":"Optional specific vision model name to pin"},"Aggregator":{"type":"string","description":"Optional aggregator pool ID to route through; default system"}},"required":["Image"]}`,
			EffectKind:  string(domain.EffectNone), CallableID: "image_recognize", ServiceName: "agent",
		},
		videoGenerate: domain.ToolSpec{
			Name:        "generate_video",
			Description: "Generate a video clip from a text prompt using the active video media account. The video is saved to assets/generated/ and the file path is returned. The returned file path is automatically previewed in the chat timeline — do NOT call show_page_thumbnail or open_global_browser on it. Seedance (ark) supports multi-reference input: mention them in the prompt as [Image 1], [Video 1], or [Audio 1] in the order given.",
			InputSchema: `{"type":"object","properties":{"Prompt":{"type":"string","description":"Text description of the video to generate"},"Model":{"type":"string","description":"Optional specific model name"},"Duration":{"type":"string","description":"Target clip length, for example 5s"},"AspectRatio":{"type":"string","description":"Aspect ratio, for example 16:9 or 9:16"},"ReferenceImages":{"type":"array","items":{"type":"string"},"description":"Reference image asset paths, URLs, or base64 data"},"ReferenceVideos":{"type":"array","items":{"type":"string"},"description":"Reference video URLs or asset paths"},"ReferenceAudios":{"type":"array","items":{"type":"string"},"description":"Reference audio asset paths, URLs, or base64 data"}},"required":["Prompt"]}`,
			EffectKind:  string(domain.EffectReversible), CallableID: "video_generate", ServiceName: "agent",
		},
	}
}

// PlanningTools returns the plan_submit, create_task, update_task tool
// specs shared by coder and oracle.
func PlanningTools() []domain.ToolSpec {
	return []domain.ToolSpec{
		{
			Name:        "plan_submit",
			Description: "Submit a completed plan for user approval. Before calling this tool, first mount and use the plan-module skill via agent_skill_use (unless its body is already in context). Do not call before the planning skill is loaded. Always provide the required non-empty Title and complete markdown Body; call this once as the final action of the planning turn. Tasks are created atomically after approval; do not create them separately during planning.",
			InputSchema: `{"type":"object","properties":{"Title":{"type":"string","description":"Short, human-readable title for the plan card and plan identifier."},"Body":{"type":"string","description":"Complete markdown plan body."},"Tasks":{"type":"array","items":{"type":"object","properties":{"Id":{"type":"string"},"Subject":{"type":"string"},"Description":{"type":"string"},"DependsOn":{"type":"array","items":{"type":"string"}},"ActiveForm":{"type":"string"},"Status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]}}}}},"Policy":{"type":"object","properties":{"Mode":{"type":"string","enum":["default","auto","accept_edits"]},"AllowedPrompts":{"type":"array","items":{"type":"object","properties":{"Tool":{"type":"string"},"Prompt":{"type":"string"}}}}}},"required":["Title","Body"]}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "plan_submit",
			ServiceName: "agent",
		},
		{
			Name:        "create_task",
			Description: "Create a new task in the task list. Use this to break down a plan into concrete implementation steps. Always provide a non-empty Subject; do not call with {} or an empty Subject. Example correct call: {\"Subject\":\"Add retry logic\"}.",
			InputSchema: `{"type":"object","properties":{"Subject":{"type":"string","minLength":1,"description":"Short imperative task title (e.g. 'Add retry logic')"},"ActiveForm":{"type":"string","description":"Optional present-continuous description shown while in progress (e.g. 'Adding retry logic')"}},"required":["Subject"]}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "task_create",
			ServiceName: "agent",
		},
		{
			Name:        "update_task",
			Description: "Update a task's status. Call this when you start or finish a task.",
			InputSchema: `{"type":"object","properties":{"Id":{"type":"string","description":"Task ID"},"Status":{"type":"string","description":"New status: pending | in_progress | completed | cancelled","enum":["pending","in_progress","completed","cancelled"]}},"required":["Id","Status"]}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "task_update",
			ServiceName: "agent",
		},
		{
			Name:        "cancel_task",
			Description: "Cancel a task by ID. Use this when a task is no longer relevant or cannot be completed.",
			InputSchema: `{"type":"object","properties":{"Id":{"type":"string","description":"Task ID to cancel"}},"required":["Id"]}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "task_cancel",
			ServiceName: "agent",
		},
	}
}

// debugBundleID is the bundle card that declares debug/introspection tools.
const debugBundleID = "builtin:bundle:debug"

// DebugToolSpecsFromBundles returns the introspection tool specs when the
// debug bundle is mounted. Like fork tools, these specs are hand-authored
// (custom names + enum-bearing schemas) and cannot be derived from topology
// metadata via data.tools/ToolSpecsFromCallables. bundleIDs is the agent's
// effective bundle set — DefaultBundleIDs unioned with actually-mounted cards
// (see appendAutonomousTools), so both kind-config and runtime/migrated mounts
// enable the specs.
func DebugToolSpecsFromBundles(bundleIDs []string) []domain.ToolSpec {
	for _, id := range bundleIDs {
		if id == debugBundleID {
			return debugToolSpecs()
		}
	}
	return nil
}

// debugToolSpecs returns the hand-authored introspection tool specs. Kept
// private; callers resolve presence via DebugToolSpecsFromBundles.
func debugToolSpecs() []domain.ToolSpec {
	return []domain.ToolSpec{
		{
			Name:        "get_problems",
			Description: "Fetch problems (diagnostics) recorded by the system — errors, warnings, and info events produced during agent turns, tool-call failures, compaction, filesystem, and other actor operations. Use this to analyze the current problems in the system before diagnosing or fixing them. Results can be filtered by severity, source, agent, turn, and recency.",
			InputSchema: `{"type":"object","properties":{"Severity":{"type":"string","enum":["error","warning","info"],"description":"Filter by severity"},"Source":{"type":"string","description":"Filter by source (e.g. dispatch, execute, audit, compaction, filesystem)"},"AgentId":{"type":"string","description":"Filter by agent id"},"TurnId":{"type":"string","description":"Filter by turn id"},"Since":{"type":"string","description":"ISO 8601 timestamp; only return problems newer than this"},"Limit":{"type":"number","description":"Max number of problems to return (default 50)"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "oracle.list_diagnostics",
			ServiceName: "oracle",
		},
		{
			Name:        "get_system_logs",
			Description: "Fetch recent system logs. By default it reads the backend Go logs (gospore/stdlib) produced by the sporemind runtime. Set Source=\"console\" to instead read persisted frontend (browser/desktop UI) console logs — essential when diagnosing UI, rendering, WebSocket client, or React state issues that never reach the backend. Results can be filtered by severity level (error, warn, info, debug), caller substring, message substring, and timestamp. Use Tail to read the most recent N persisted entries; use Before to page backwards through older logs. The response is capped at 200 entries; when truncated, the result includes a continuation hint with the Before timestamp to fetch the next older segment. Default Source is backend.",
			InputSchema: `{"type":"object","properties":{"Source":{"type":"string","description":"Which log stream to read: \"backend\" (default, Go runtime/gospore logs) or \"console\" (persisted frontend browser/desktop UI console logs)"},"Level":{"type":"string","description":"Filter by log level (e.g. error, warn, info, debug)"},"Caller":{"type":"string","description":"Filter by caller substring (e.g. workspace, agent, oracle); for console logs this matches the source location"},"Message":{"type":"string","description":"Filter by message substring"},"Before":{"type":"string","description":"ISO 8601/RFC3339Nano timestamp; only return entries strictly older than this"},"Limit":{"type":"number","description":"Max entries to return (default 200)"},"Tail":{"type":"number","description":"Return the last N persisted entries; when set, Limit is ignored for the initial fetch"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "workspace.logs_query",
			ServiceName: "workspace",
		},
		{
			Name:        "search_services",
			Description: "Search exposed App-level services (actors registered with ctx.Expose). Use this to discover which service namespaces are available in the running system, such as workspace, oracle, filesystem, project, etc. Results can be filtered by service name substring.",
			InputSchema: `{"type":"object","properties":{"Query":{"type":"string","description":"Substring to filter service names (e.g. workspace, oracle)"},"Limit":{"type":"number","description":"Max number of services to return (default 50)"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "oracle.search_services",
			ServiceName: "oracle",
		},
		{
			Name:        "list_callables",
			Description: "List all callable interfaces (tools) available in the running system. Use this to discover exact callable IDs, their parameters, and descriptions before invoking them. Results can be filtered by name or description substring.",
			InputSchema: `{"type":"object","properties":{"Query":{"type":"string","description":"Substring to filter callable names or descriptions"},"Limit":{"type":"number","description":"Max number of callables to return (default 200)"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "list_callables",
			ServiceName: "agent",
		},
		{
			Name:        "invoke_callable",
			Description: "Invoke any callable by its service name and call ID with a JSON payload. Use this to call tools that are not explicitly listed in your tool set, e.g. when debugging or exploring the system. Prefer well-known tools when available; use this only for dynamic exploration.",
			InputSchema: `{"type":"object","required":["Service","CallID"],"properties":{"Service":{"type":"string","description":"Exposed service name of the target actor (e.g. workspace, oracle, filesystem)"},"CallID":{"type":"string","description":"Full callable ID (e.g. workspace.list_agents)"},"Payload":{"type":"object","description":"JSON request payload object; keys must match the target callable's request fields"}}}`,
			EffectKind:  string(domain.EffectReversible),
			CallableID:  "invoke_callable",
			ServiceName: "agent",
		},
		{
			Name:        "frontend_debug",
			Description: "Execute JavaScript in the current frontend webview (desktop runtime only) or read basic page info. Use this when diagnosing UI, rendering, or client-state issues that never reach the backend. In headless mode the hook is unavailable.",
			InputSchema: `{"type":"object","properties":{"Operation":{"type":"string","enum":["eval","info"],"description":"eval runs Script; info returns page url/title/userAgent/viewport"},"Script":{"type":"string","description":"JavaScript to evaluate when Operation=eval"}}}`,
			EffectKind:  string(domain.EffectReversible),
			CallableID:  "frontend_debug",
			ServiceName: "agent",
		},
		{
			Name:        "inspect_actor",
			Description: "Inspect an actor's runtime details: lifecycle state (created/initialized/started/idle/stopped), business state (for agents), pipeline water level (owner/system/reply queue depth vs capacity), and the count of stuck (pending/outbound) invocations awaiting response. All reads are non-blocking point-in-time snapshots. Use this to diagnose why an actor is slow, backlogged, or appears hung.",
			InputSchema: `{"type":"object","required":["ActorPath"],"properties":{"ActorPath":{"type":"string","description":"Target actor path: a canonical ULID, a service name (e.g. workspace, oracle), or an actor type (e.g. agent). Same resolution as gospore.cell.stats."}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "inspect_actor",
			ServiceName: "agent",
		},
		{
			Name:        "capture_profile",
			Description: "Capture a Go runtime pprof profile of the running process and return it as readable text. Profiles: \"goroutine\" (stack traces of all goroutines — diagnose deadlocks, leaks, stuck actors), \"heap\" (memory allocations — diagnose leaks, large objects), \"cpu\" (CPU hotspots over a time window; captures for Seconds, default 30), \"mutex\" (lock contention; requires runtime.SetMutexProfileFraction > 0), \"block\" (blocking on channels/mutexes; requires runtime.SetBlockProfileRate > 0), \"threadcreate\" (OS thread creation). Modes: Summary (default) parses and sorts entries by count, showing top TopN (default 20) with function frames — compact and actionable. Diff (Diff=true, named profiles only) captures two snapshots Seconds apart (default 10) and returns only entries that changed (NEW/GROWING/SHRINKING) — use this to confirm goroutine leaks or memory growth that a single snapshot cannot reveal. Raw stacks (Debug=2) returns full WriteTo output. Use goroutine first when diagnosing hangs; use heap for memory growth; use cpu for performance analysis; use Diff=true to detect growth over time.",
			InputSchema: `{"type":"object","required":["Profile"],"properties":{"Profile":{"type":"string","enum":["cpu","heap","goroutine","mutex","block","threadcreate"],"description":"Profile type to capture"},"Seconds":{"type":"number","description":"cpu: capture duration in seconds (default 30, max 120); diff: interval between snapshots (default 10, max 60); ignored otherwise"},"Debug":{"type":"number","description":"Text detail for named profiles: 1=aggregated summary (default), 2=full stack traces; ignored for cpu and diff"},"Diff":{"type":"boolean","description":"Named profiles only: capture two snapshots Seconds apart, return only changed entries (new/growing/shrinking); not supported for cpu"},"TopN":{"type":"number","description":"Limit output to top N entries by count (default 20, max 500); for cpu passed as -nodecount to go tool pprof"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "capture_profile",
			ServiceName: "agent",
		},
	}
}

// SkillUseTool returns the agent_skill_use tool spec available to all
// agent kinds.
func SkillUseTool() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "agent_skill_use",
		Description: "Use a mounted skill by id. The skill body is returned in the tool_result and becomes part of the conversation history. Repeated use succeeds with a warning. Mount the skill component separately to make its description and tools available.",
		InputSchema: `{"type":"object","properties":{"SkillId":{"type":"string","description":"Skill id from the available skills list"},"Args":{"type":"string","description":"Optional arguments for parameterized skills"},"Context":{"type":"string","enum":["inline","fork"],"description":"Override execution context; default is the skill's own context frontmatter or inline"}},"required":["SkillId"]}`,
		EffectKind:  string(domain.EffectNone),
		CallableID:  "skill_use",
		ServiceName: "agent",
	}
}

// OpenGlobalBrowserTool returns the open_global_browser tool spec. It opens a
// URL or local file in the sporemind desktop's shared global browser tab.
func OpenGlobalBrowserTool() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "open_global_browser",
		Description: "Open a URL or local file in the sporemind desktop's shared global browser (the right-panel global browser tab). Supports http://, https://, file:// URLs and project-relative paths. Only available in the desktop client; in headless mode an open_global event is emitted but no browser window will appear.",
		InputSchema: `{"type":"object","properties":{"Url":{"type":"string","description":"URL or local file path to open. Supports http://, https://, file:// and project-relative paths (resolved against the project root)."}}}`,
		EffectKind:  string(domain.EffectNone),
		CallableID:  "open_global_browser",
		ServiceName: "agent",
	}
}

// ShowPageThumbnailTool returns the show_page_thumbnail tool spec. It renders a
// thumbnail card of a web page or local file in the ai-step timeline; the page
// opens in the shared global browser tab only when the user clicks the card.
func ShowPageThumbnailTool() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "show_page_thumbnail",
		Description: "Show a thumbnail card of a web page or local file in the conversation timeline. The tool only renders the preview; the page opens in the right-panel global browser tab when the user clicks the card. Supports http://, https://, file:// URLs and project-relative paths (resolved against the project root).",
		InputSchema: `{"type":"object","properties":{"Url":{"type":"string","description":"URL or local file path to preview. Supports http://, https://, file:// and project-relative paths (resolved against the project root)."},"Title":{"type":"string","description":"Optional display title for the thumbnail card."}},"required":["Url"]}`,
		EffectKind:  string(domain.EffectNone),
		CallableID:  "show_page_thumbnail",
		ServiceName: "agent",
	}
}

// AutonomousToolsForKind returns the kind-specific tool specs that should be
// appended to an agent's tool set based on its config. Fork tools are now
// bundle-driven via ForkToolSpecsFromBundles; this function only carries the
// non-fork autonomous tools (image generation, browser, skill use). Debug
// introspection tools are bundle-driven via DebugToolSpecsFromBundles.
func AutonomousToolsForKind(cfg domain.AgentKindConfig) []domain.ToolSpec {
	if cfg.Kind == domain.AgentKindDreamer {
		return nil
	}
	var tools []domain.ToolSpec

	// Kind-specific non-fork autonomous tools.
	switch cfg.Kind {
	case domain.AgentKindCoder, "app-builder":
		tools = append(tools, CoderAutonomousTools()...)
	}
	if cfg.Kind == domain.AgentKindCoder {
		tools = append(tools, OpenGlobalBrowserTool())
		tools = append(tools, ShowPageThumbnailTool())
	}
	tools = append(tools, SkillUseTool())

	return tools
}
