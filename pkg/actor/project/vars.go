package project

import (
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// --- Error sentinels ---

var ErrCardNotFound = errors.New("card not found")
var ErrCardExists = errors.New("card already exists")
var errOutsideRoots = fmt.Errorf("project: path is outside all configured roots")
var errWorktreeInactive = errors.New("project: bound worktree is not active; call project.worktree.exit to release the binding")

// --- Lookup tables ---

var sporeScalarTypes = map[string]struct{}{
	"bool":   {},
	"byte":   {},
	"short":  {},
	"ushort": {},
	"int":    {},
	"uint":   {},
	"long":   {},
	"ulong":  {},
	"float":  {},
	"double": {},
	"string": {},
	"bytes":  {},
	"object": {},
	"any":    {},
}

var (
	structNameIndex     map[string]struct{}
	structNameIndexOnce sync.Once
)

var canonicalCardTypes = map[string]struct{}{
	"agent":             {},
	"callable":          {},
	"capability_module": {},
	"concept":           {},
	"crawl":             {},
	"prompt":            {},
	"scheduler":         {},
	"skill":             {},
	"task":              {},
	"wiki":              {},
	"workflow":          {},
	"bundle":            {},
}

var canonicalTaskStatuses = []string{"backlog", "todo", "doing", "pending_review", "done", "blocked", "cancelled", "failed"}

var defaultTaskStatusAliases = map[string]string{
	"open":         "todo",
	"draft":        "todo",
	"approved":     "todo",
	"to-do":        "todo",
	"to_do":        "todo",
	"pending":      "todo",
	"wait":         "todo",
	"waiting":      "todo",
	"in_progress":  "doing",
	"in progress":  "doing",
	"inprogress":   "doing",
	"in-progress":  "doing",
	"in_gress":     "doing",
	"in gress":     "doing",
	"wip":          "doing",
	"progress":     "doing",
	"active":       "doing",
	"ongoing":      "doing",
	"experimental": "doing",
	"completed":    "done",
	"complete":     "done",
	"finished":     "done",
	"finish":       "done",
	"closed":       "done",
	"resolved":     "done",
	"back log":     "backlog",
	"back_log":     "backlog",
	"rejected":     "cancelled",
	"canceled":     "cancelled",
	"aborted":      "cancelled",
	"withdrawn":    "cancelled",
	"error":        "failed",
	"errored":      "failed",
	"failure":      "failed",
	"crashed":      "failed",
	"dead":         "failed",
}

var taskStatusNode = map[string]string{
	"backlog":        builtinBacklogID,
	"todo":           builtinTodoID,
	"doing":          builtinDoingID,
	"pending_review": builtinPendingReviewID,
	"done":           builtinDoneID,
	"blocked":        builtinBlockedID,
	"cancelled":      builtinCancelledID,
	"failed":         builtinFailedID,
}

var statusBuiltinVisuals = map[string]map[string]any{
	"backlog":        {"accent": "slate", "icon": "archive"},
	"todo":           {"accent": "blue", "icon": "circle"},
	"doing":          {"accent": "amber", "icon": "refresh-cw"},
	"pending_review": {"accent": "orange", "icon": "eye"},
	"done":           {"accent": "green", "icon": "check-circle"},
	"blocked":        {"accent": "red", "icon": "shield-alert"},
	"cancelled":      {"accent": "purple", "icon": "trash-2"},
	"failed":         {"accent": "rose", "icon": "x-octagon"},
}

var builtinMountSpecs = []mountSpec{
	{virtualNode: builtinBacklogID, mountType: "task", mountStatus: "backlog", autoMount: true},
	{virtualNode: builtinTodoID, mountType: "task", mountStatus: "todo", autoMount: true},
	{virtualNode: builtinDoingID, mountType: "task", mountStatus: "doing", autoMount: true},
	{virtualNode: builtinPendingReviewID, mountType: "task", mountStatus: "pending_review", autoMount: true},
	{virtualNode: builtinDoneID, mountType: "task", mountStatus: "done", autoMount: true},
	{virtualNode: builtinBlockedID, mountType: "task", mountStatus: "blocked", autoMount: true},
	{virtualNode: builtinCancelledID, mountType: "task", mountStatus: "cancelled", autoMount: true},
	{virtualNode: builtinFailedID, mountType: "task", mountStatus: "failed", autoMount: true},
	{virtualNode: builtinPromptID, mountType: "prompt", autoMount: true},
	{virtualNode: builtinSkillID, mountType: "skill", autoMount: true},
	{virtualNode: builtinAgentID, mountType: "agent", autoMount: true},
	{virtualNode: builtinConceptID, mountType: "concept", autoMount: false},
	{virtualNode: builtinCallableID, mountType: "callable", autoMount: true},
	{virtualNode: builtinCapabilityModuleID, mountType: "capability_module", autoMount: true},
	{virtualNode: builtinSchedulerID, mountType: "scheduler", autoMount: true},
	{virtualNode: builtinBundleID, mountType: "bundle", autoMount: true},
	{virtualNode: builtinMapID, mountType: "workflow", autoMount: true},
}

var builtinMountVisuals = map[string]map[string]any{
	builtinSkillID:            {"icon": "zap", "accent": "amber", "emphasis": "normal"},
	builtinPromptID:           {"icon": "terminal", "accent": "blue", "emphasis": "normal"},
	builtinAgentID:            {"icon": "users", "accent": "blue", "emphasis": "normal"},
	builtinCallableID:         {"icon": "play", "accent": "green", "emphasis": "normal"},
	builtinCapabilityModuleID: {"icon": "shield", "accent": "purple", "emphasis": "normal"},
	builtinSchedulerID:        {"icon": "clock", "accent": "amber", "emphasis": "normal"},
	builtinConceptID:          {"icon": "lightbulb", "accent": "amber", "emphasis": "normal"},
	builtinBundleID:           {"icon": "package", "accent": "cyan", "emphasis": "normal"},
	builtinMapID:              {"icon": "map", "accent": "cyan", "emphasis": "strong"},
}

var wellKnownCardParents = map[string]string{
	projectInfoCardID: tocID,
}

var cardTypeRepairers = map[string]CardRepairer{
	"task": taskRepairer{},
}

var cardTypeValidators = map[string]CardTypeValidator{
	"agent":             wikiValidator{},
	"wiki":              wikiValidator{},
	"task":              taskValidator{},
	"scheduler":         schedulerValidator{},
	"prompt":            promptValidator{},
	"skill":             skillValidator{},
	"concept":           conceptValidator{},
	"callable":          callableValidator{},
	"capability_module": capabilityModuleValidator{},
	"crawl":             crawlValidator{},
	"workflow":          wikiValidator{},
	"bundle":            wikiValidator{},
}

var knownExecKinds = map[string]struct{}{
	"worker_task": {},
	"sub_map":     {},
	"crawl":       {},
	"event_wait":  {},
	"gate":        {},
	"script":      {},
}

// CardCategories is the canonical set of values accepted by a task card's
// data.category field. The category is orthogonal to exec.kind: it controls
// which agent role is dispatched to handle the card (research/explore/etc.),
// while exec.kind controls the executor used at runtime.
var CardCategories = map[string]struct{}{
	"research": {},
	"explore":  {},
	"execute":  {},
	"code":     {},
	"review":   {},
}

var legacyProjectInfoIDs = []string{
	"__builtin_summary__",
	"__builtin_constraints__",
	"__builtin_project_info__",
}

// legacyProjectInfoTargets maps obsolete __builtin_* project-info card IDs to
// the regular card ID their user-authored content migrates into.
var legacyProjectInfoTargets = map[string]string{
	"__builtin_summary__":      "summary",
	"__builtin_constraints__":  "constraints",
	"__builtin_project_info__": "project_info",
}

var projectCardTemplates = []projectCardTemplate{
	{ID: projectInfoCardID, Body: "Project overview and key information."},
}

var externalSkillSources = []externalSkillSource{
	{Dir: ".claude/skills", Source: "claude"},
	{Dir: ".codex/skills", Source: "codex"},
	{Dir: ".agents/skills", Source: "agents"},
	{Dir: ".opencode/skills", Source: "opencode"},
	{Dir: ".pi/skills", Source: "pi"},
}

var ignoredWatchDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "vendor": {}, "dist": {}, "build": {},
}

var defaultSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	".svn":         true,
	"__pycache__":  true,
	".hg":          true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}

// --- Builtin card definitions ---

var (
	tocBuiltin = BuiltinCard{
		Title: "toc",
		Tags:  []string{},
		List:  []string{},
		Data:  map[string]any{"visual": map[string]any{"icon": "list", "accent": "slate", "emphasis": "normal"}},
		Body:  "",
	}

	skillBuiltin = BuiltinCard{
		Title: builtinSkillID,
		Tags:  []string{},
		List:  []string{},
	}

	pluginBuiltin = BuiltinCard{
		Title: builtinPluginID,
		Tags:  []string{},
		List:  []string{},
		Data:  map[string]any{"visual": map[string]any{"icon": "plug", "accent": "slate", "emphasis": "normal"}},
	}

	agentBuiltin = BuiltinCard{
		Title: builtinAgentID,
		Tags:  []string{},
		List:  []string{},
	}

	componentBuiltin = BuiltinCard{
		Title: builtinComponentID,
		Tags:  []string{},
		List:  []string{},
		Data:  map[string]any{"visual": map[string]any{"icon": "puzzle", "accent": "slate", "emphasis": "normal"}},
	}

	promptBuiltin = BuiltinCard{
		Title: builtinPromptID,
		Tags:  []string{"component"},
		List:  []string{},
	}
)

var builtinCards = []*BuiltinCard{
	&tocBuiltin,
	&skillBuiltin,
	&pluginBuiltin,
	&agentBuiltin,
	&componentBuiltin,
	&promptBuiltin,
}

var obsoleteBuiltinIDs = []string{
	"__builtin_system__",
	"__builtin_goal__",
	"__builtin_kanban__",
	"__builtin_automation__",
	"__builtin_plan__",
	"__builtin_kanban_backlog__",
	"__builtin_kanban_todo__",
	"__builtin_kanban_doing__",
	"__builtin_kanban_done__",
	"__builtin_kanban_blocked__",
	"__builtin_knowledge__",
	"__builtin_toc__",
}

var obsoletePromptCardIDs = []string{
	"prompt:fragment:builtin-dao-primer",
	"prompt:profile:project.warden",
}

var builtinMountDataKeys = []string{
	"builtinRole",
	"mountType",
	"mountStatus",
	"autoMount",
}

var priorityRank = map[string]int{
	"high": 0, "urgent": 0, "p0": 0,
	"medium": 1, "normal": 1, "p1": 1,
	"low": 2, "p2": 2,
}

// --- Compiled regexps ---

var wikiTitleQueryRe = regexp.MustCompile(`^/(.+)/([gimsuy]*)$`)
var ownerAgentIDLine = regexp.MustCompile(`(?m)^[ \t]*ownerAgentId:[ \t]*\r?\n`)

// --- Other ---

// reviewChangesetGenSem limits concurrent review-changeset generation jobs.
var reviewChangesetGenSem = make(chan struct{}, 4)

var generatedFilePatterns = []string{
	".gen.go", ".gen.ts", ".pb.go", ".pb.ts",
	"_gen.go", "_gen.ts", ".generated.go",
	".min.js", ".min.css",
}

// ensure gen import is not flagged unused (gen types are referenced by
// cardTypeValidators and other vars above).
var _ gen.MonoCardListItem