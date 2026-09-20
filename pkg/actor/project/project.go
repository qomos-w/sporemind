package project

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/util"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = config.PersistConfig // keep import after removing package-level store

// Actor is a multi-root project container. Each root is an independent
// module directory. Paths in file callables are resolved against roots.
//
// The Actor struct carries NO persistent-authority fields. All durable state
// lives in ground-truth cards (configCard + uiStateCard) written through
// persist.Persist. Callables read the card fresh on every invocation and
// write through on every mutation. Config and UI-state fields that were
// previously stored on the struct (Roots, MountedCardRefs, NoGitMode,
// OpenCards, protectedFiles) have been removed; see state_cards.go.
type Actor struct {
	actor.Host
	Graphs            map[string]gen.ProjectGraphEnvelopeResp `gospore:"component,public"`
	initialPath       string
	workspaceCards    bool
	workspaceCardsSet bool   // true when system identity was injected explicitly (not path-guessed)
	actorID           string // set in OnStart from Self().ID()
	store             CardStore
	persistStore      persist.Persist
	externalProviders []ExternalCardProvider
	fileWatcher       *fsnotify.Watcher
	fileWatcherDone   chan struct{}
	fileWatcherMu     sync.Mutex
	dynWatchRefs      map[string]int // parent-dir → viewer refcount for project.watch_file (guarded by fileWatcherMu)
	fileLocks         fileLockMap // per-path mutex for concurrent file operations
	worktrees         map[string]gen.ProjectWorktree
	agentWorktree     map[string]string // agentActorID → worktreeID; caller→worktree binding (方案 B)
	agentParent       map[string]string // agentActorID → parentAgentID; for review changeset authorization
	worktreeParentMu  sync.RWMutex      // protects worktrees and agentParent (concurrent PureContext reads + writes)
	bindingMu         sync.RWMutex      // protects agentWorktree (concurrent PureContext reads + occasional spawn/release/enter/exit writes)
	// Lock ordering: when both locks are needed, always acquire worktreeParentMu before bindingMu.
	reviewFileContents   map[string]map[string]string // transient, rebuildable per-file diff cache (keyed by agentActorID)
	reviewFileContentsMu sync.RWMutex                 // protects reviewFileContents
	// worktreeMergeMu serializes all child→parent merge operations so that
	// concurrent mergeChildIntoParent calls from different review-approve
	// paths do not compete for the same parent worktree's git lock. The
	// project actor's ownerLoop serializes message handling, but merge
	// operations are synchronous and may span multiple actor interactions;
	// this mutex ensures one merge at a time.
	worktreeMergeMu sync.Mutex
	// configMu serializes read-modify-write cycles on the config card (.rconfig
	// persist record). PureContext handlers (sync_roots, card_mount, card_unmount,
	// no_git_mode_set, set_protected_files) all run concurrently off the owner
	// queue and must not interleave their RMW cycles.
	configMu sync.RWMutex
	// uiStateMu serializes read-modify-write cycles on the UI-state card (.ropen
	// persist record) for the same reason: PureContext wiki_save_open_cards,
	// wiki_open_card, wiki_close_card run concurrently.
	uiStateMu sync.RWMutex
	// logger caches the actor context logger so best-effort helpers invoked
	// outside handler scope can log.
	logger actor.Logger
	// graphRevSeq is the monotonic graph snapshot revision counter. Seeded in
	// OnInit from wall-clock nano time so a restarted actor never re-issues
	// revisions already persisted in graphs.json; zero-constructed test
	// actors start at 1, which stays unique within the actor. A monotonic
	// counter (not wall clock) guarantees back-to-back saves always bump the
	// revision — optimistic-lock change detection depends on that.
	graphRevSeq int64
	// graphMu protects Graphs and graphRevSeq against concurrent access from
	// PureContext handlers (graph_get, graph_concept_get) and Context handlers.
	graphMu sync.RWMutex
	// templateRunsMu serializes template run log load-modify-save cycles.
	templateRunsMu sync.Mutex
	// includeAppendMu serializes create_task_card's map-card include append
	// (Get→append→Save). Concurrent creates on one map race the whole-card
	// write and silently drop the loser's append; the mutex closes that
	// lost-update window in-process.
	includeAppendMu sync.Mutex
}

func (a *Actor) statePath() string {
	// Legacy pre-refactor local state file; consulted only by
	// migrateLegacyStateCards.
	return filepath.Join(a.metaDir(), "state.json")
}

func (a *Actor) graphsPath() string {
	return filepath.Join(a.metaDir(), "graphs.json")
}

func (a *Actor) openCardsPath() string {
	// Legacy pre-refactor UI-state file; consulted only by
	// migrateLegacyStateCards.
	return filepath.Join(a.metaDir(), "wiki-state.json")
}

// protectedFilesPath returns the path of the legacy generated-files list.
// Pre-refactor the protected-files set was persisted to this raw file; it is
// now part of the config card (.rconfig persist record). The path survives
// only for migrateLegacyStateCards to import old data.
func (a *Actor) protectedFilesPath() string {
	return filepath.Join(a.metaDir(), "generated-files.json")
}

var _ persist.Persistent = (*Actor)(nil)

// metaDir returns the directory holding this project's metadata files
// (state.json, graphs.json, kanban.json, wiki-state.json) and the wiki/
// subdirectory. The system meta project stores these directly under its root
// (alongside app/, dev-app/, sporeapp/) rather than under a .sporecode
// subdirectory, matching the data-tier layout of config.DataDir()/data.
// Regular projects keep the conventional <project>/.sporecode layout.
func (a *Actor) metaDir() string {
	if a.workspaceCards {
		return a.initialPath
	}
	return filepath.Join(a.initialPath, ".sporecode")
}

// NewActor returns a factory closure that captures the initial root path.
// systemCards explicitly declares whether this actor hosts the system-owned
// card set (prompt profiles, builtin skill cards, builtin component cards).
// The workspace knows this from mount metadata (ProjectRef.System); path
// comparison in isWorkspaceCardStorePath is only a fallback for callers that
// do not pass the flag (tests, tooling).
func isWorkspaceCardStorePath(path string) bool {
	return filepath.Clean(path) == filepath.Clean(filepath.Join(config.DataDir(), "data"))
}

func NewActor(initialPath string, systemCards ...bool) func() actor.Actor {
	return func() actor.Actor {
		a := &Actor{
			initialPath:       initialPath,
			externalProviders: []ExternalCardProvider{newPluginExternalCardProvider(), newAgentExternalCardProvider(), newExternalSkillCardProvider(initialPath), newMcpExternalCardProvider()},
		}
		if len(systemCards) > 0 {
			a.workspaceCards = systemCards[0]
			a.workspaceCardsSet = true
		} else {
			a.workspaceCards = isWorkspaceCardStorePath(initialPath)
		}
		a.store = newFSCardStore(filepath.Join(a.metaDir(), "wiki"))
		a.initWorkflowTopoDirtyTracking()
		if a.workspaceCards {
			a.externalProviders = append([]ExternalCardProvider{newBuiltinComponentCardProvider()}, a.externalProviders...)
		}
		return a
	}
}

// OnInit loads persisted state and seeds the default root.
func (a *Actor) OnInit(ctx actor.Context) error {
	a.logger = ctx.Logger()
	// Seed the graph revision counter from wall-clock nano time: a restarted
	// actor must never re-issue a revision already persisted in graphs.json
	// (zero would collide with rev-1..N from a previous session).
	a.graphRevSeq = time.Now().UnixNano()
	if !a.workspaceCardsSet {
		a.workspaceCards = isWorkspaceCardStorePath(a.initialPath)
	}
	if a.workspaceCards && a.externalProviderFor("builtin:x") == nil {
		a.externalProviders = append([]ExternalCardProvider{newBuiltinComponentCardProvider()}, a.externalProviders...)
	}
	a.actorID = ctx.Self().ID().String()
	if a.persistStore == nil {
		var err error
		a.persistStore, err = persist.New(config.PersistConfig("project"))
		if err != nil {
			return err
		}
	}
	if err := a.Load(); err != nil {
		ctx.Logger().Error("project: load state failed", "error", err)
	}
	if err := a.loadGraphs(); err != nil {
		ctx.Logger().Error("project: load graphs failed", "error", err)
	}
	if err := a.loadWorktreeCache(); err != nil {
		ctx.Logger().Error("project: load worktrees failed", "error", err)
	}
	if a.reviewFileContents == nil {
		a.reviewFileContents = make(map[string]map[string]string)
	}
	// Seed the config card's default root on first start (empty card + known
	// initial path). The card is the ground truth; the seed is written through
	// so disk carries it from the start.
	if err := a.ensureConfigCardSeed(); err != nil {
		ctx.Logger().Error("project: seed config card failed", "error", err)
	}
	if a.Graphs == nil {
		a.Graphs = make(map[string]gen.ProjectGraphEnvelopeResp)
	}
	if a.store != nil {
		a.migrateProjectInfoCards()
		a.seedBuiltinCards()
		if a.workspaceCards {
			a.seedPromptCards()
			a.seedBuiltinSkillCards()
		} else {
			a.removeSystemCards()
		}
		a.ensureProjectInfoCard()
	}
	return nil
}

func (a *Actor) Type() string { return "project" }

// OnStart registers callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	roots, rootsErr := a.rootsSnapshot()
	rootCount := len(roots)
	if rootsErr != nil {
		ctx.Logger().Warn("project: starting: read roots failed", "error", rootsErr)
	}
	ctx.Logger().Info("project: starting", "id", a.actorID, "roots", rootCount)

	// Ensure `merge=ours` driver is configured for the repository.
	// This makes .gitattributes `merge=ours` rules (for .sporecode/wiki/,
	// .sporecode/graphs.json, .sporecode/wiki-state.json) actually take
	// effect during child→parent merges. The command is idempotent.
	if root, err := a.rootPath(); err == nil {
		if _, err := gitRun(nil, root, "config", "merge.ours.driver", "true"); err != nil {
			ctx.Logger().Warn("project: configure merge.ours.driver failed", "error", err)
		}
	}
	if err := ctx.RegisterEventKind("file_changed", gen.ProjectFileChangedEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("project: register file_changed event: %w", err)
	}
	if err := ctx.RegisterEventKind("card_changed", gen.WikiCardChangedEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("project: register card_changed event: %w", err)
	}
	if err := ctx.RegisterEventKind("graph_changed", gen.GraphChangedEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("project: register graph_changed event: %w", err)
	}
	if err := ctx.Register("project.watch_file", a.handleWatchFile, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Add (Remove=false) or drop (Remove=true) an on-demand watch for a single file. The file watcher only watches the .sporecode subtree plus on-demand files; UI surfaces that need file_changed events for a file outside it (e.g. an open file viewer) must call this with the file's path. The parent directory is watched and shared between viewers, so add and remove calls must be paired."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "File path (project-relative or absolute) whose changes should be watched."},
			actor.ParamDesc{Name: "remove", Description: "true to drop a previously added watch."},
		),
	); err != nil {
		return fmt.Errorf("project: register watch_file: %w", err)
	}
	// A failing file watcher must never abort OnStart: registration happens
	// in one shot below, and aborting leaves the cell tree-registered with a
	// partial handler table — every later project.wiki_* call then answers
	// "call ID not registered" forever, which no caller retry can cure. The
	// degraded surface (watch_file reporting "file watcher not running",
	// no file_changed events) is recoverable; a zombie actor is not.
	if err := a.startFileWatcher(ctx); err != nil {
		ctx.Logger().Warn("project: start file watcher failed; file_changed events disabled until restart", "error", err)
	}
	if err := ctx.Register("project.list", a.handleFileList, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List directory contents as plain text (one entry per line, directories end with /). Depth controls how many levels to descend: 0 or omit for current directory only, 1 for one level, 2 for two levels, -1 for full recursion. Skips node_modules, .git, vendor, dist, build. Hidden entries (names starting with '.') are omitted by default. With detail=true each line appends mtime (YYYY-MM-DD HH:MM:SS ±HH:MM) and, for files, Size:N."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Directory to list. Relative paths resolve against the project root."},
			actor.ParamDesc{Name: "depth", Description: "Levels to descend: 0/omit=current dir only, 1=one level, 2=two levels, -1=full recursion (capped at 5000 entries)."},
			actor.ParamDesc{Name: "exclude", Description: "Comma-separated directory names to additionally skip; bypassed when no_ignore=true."},
			actor.ParamDesc{Name: "no_ignore", Description: "If true, bypass ALL ignore layers (.gitignore, default skip dirs like node_modules/.git/build/vendor, and exclude). Default false."},
			actor.ParamDesc{Name: "all", Description: "If true, include hidden entries whose names start with '.'. Default false."},
			actor.ParamDesc{Name: "detail", Description: "If true, each line carries mtime (YYYY-MM-DD HH:MM:SS ±HH:MM) and Size:N for files (parse the fixed tail right-anchored; names may contain spaces). Default false."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_list: %w", err)
	}
	if err := ctx.Register("project.read", a.handleFileRead, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Read a file with optional line offset/limit, tail (last N lines), and an in-file regex filter. Prefer it over shell cat; use offset/limit or tail for large files."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "File path to read."},
			actor.ParamDesc{Name: "offset", Description: "Line number to start reading from (1-based, matching StartLine in the response). Omit or use 1 to read from the beginning. For pagination, set offset = previous StartLine + NumLines."},
			actor.ParamDesc{Name: "limit", Description: "Maximum number of lines to read."},
			actor.ParamDesc{Name: "tail", Description: "Read the last N lines instead of from the start."},
			actor.ParamDesc{Name: "filter", Description: "Regular expression to filter lines. Only matching lines are returned."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_read: %w", err)
	}
	if err := ctx.Register("project.read_base64", a.handleFileReadBase64, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("project: register file_read_base64: %w", err)
	}
	if err := ctx.Register("project.read_chunk", a.handleFileReadChunk, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("project: register file_read_chunk: %w", err)
	}
	if err := ctx.Register("project.write", a.handleFileWrite, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Create or overwrite a file within a configured root."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to write. Relative paths resolve against the first root; absolute paths must be inside a configured root unless confirm=true."},
			actor.ParamDesc{Name: "content", Description: "Content to write"},
			actor.ParamDesc{Name: "confirm", Description: "Must be true when the path resolves outside all configured roots."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_write: %w", err)
	}
	if err := ctx.Register("project.edit", a.handleFileEdit, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Edit an existing file by replacing exact old_string text with new_string. By default, old_string must be non-empty, differ from new_string, and occur exactly once; use replace_all=true only when every occurrence should change. If old_string is not found, re-read the file and use the exact current text. Use overwrite=true to replace the entire existing file, or project.write to create a missing file. Errors are prefixed with project.edit; successful edits return replacement counts and hunks."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to edit. Relative paths resolve against the first root; absolute paths must be inside a configured root unless confirm=true."},
			actor.ParamDesc{Name: "old_string", Description: "Exact text to replace. Ignored when overwrite=true."},
			actor.ParamDesc{Name: "new_string", Description: "Replacement text. When overwrite=true, this becomes the entire new file content."},
			actor.ParamDesc{Name: "replace_all", Description: "If true, replace every occurrence. Default false. Ignored when overwrite=true."},
			actor.ParamDesc{Name: "overwrite", Description: "If true, replace the entire file content with new_string. Default false."},
			actor.ParamDesc{Name: "confirm", Description: "Must be true when the path resolves outside all configured roots."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_edit: %w", err)
	}
	if err := ctx.Register("project.glob", a.handleFileGlob, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Find files by glob pattern (e.g. **/*.go). Skips node_modules, .git, vendor, dist, build and .gitignored entries by default. Set no_ignore=true to bypass all ignore layers."),
		actor.WithParams(
			actor.ParamDesc{Name: "pattern", Description: "Glob pattern, optionally prefixed with a literal path (e.g. pkg/**/*.go). Regex patterns supported when they contain |+\\()$^."},
			actor.ParamDesc{Name: "path", Description: "Root directory to search; relative to project root. Empty = project root."},
			actor.ParamDesc{Name: "maxdepth", Description: "Maximum directory depth to descend."},
			actor.ParamDesc{Name: "no_ignore", Description: "If true, bypass ALL ignore layers (.gitignore, default skip dirs like node_modules/.git/build/vendor, and exclude). Default false."},
			actor.ParamDesc{Name: "exclude", Description: "Comma-separated directory names to additionally skip; bypassed when no_ignore=true."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_glob: %w", err)
	}
	if err := ctx.Register("project.grep", a.handleFileGrep, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Search file contents with a Go-compatible regular expression. Returns matching file paths by default; use output_mode=content for lines or output_mode=count for per-file counts. An empty result is a successful search with no matches. Errors are prefixed with project.grep; invalid pattern means the regex could not be compiled. Skips node_modules, .git, vendor, dist, build and .gitignored entries by default; set no_ignore=true to bypass all ignore layers."),
		actor.WithParams(
			actor.ParamDesc{Name: "pattern", Description: "Regex pattern to search for."},
			actor.ParamDesc{Name: "path", Description: "Root directory to search; relative to project root. Empty = project root."},
			actor.ParamDesc{Name: "output_mode", Description: "files | content | count. Default files."},
			actor.ParamDesc{Name: "glob", Description: "Glob filter for which files to search (e.g. *.go)."},
			actor.ParamDesc{Name: "no_ignore", Description: "If true, bypass ALL ignore layers (.gitignore, default skip dirs like node_modules/.git/build/vendor, and exclude). Default false."},
			actor.ParamDesc{Name: "exclude", Description: "Comma-separated directory names to additionally skip; bypassed when no_ignore=true."},
		),
	); err != nil {
		return fmt.Errorf("project: register file_grep: %w", err)
	}
	if err := ctx.Register("project.rm", a.handleFileRm, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Remove a file or directory. Two-phase by default: without Confirm/Force it returns a Preview of what would be removed; the actual delete happens only with Confirm=true (or Force). Directories additionally require Recursive=true. Irreversible."),
	); err != nil {
		return fmt.Errorf("project: register file_rm: %w", err)
	}
	if err := ctx.Register("project.archive_export", a.handleArchiveExport, actor.Public(),
		actor.WithDescription("Pack a source path into a base64 tar.gz archive. The source may be a regular file (the archive then contains exactly one entry named path.Base(source)) or a directory (recursively packed, top-level entry is the directory's base name). Only regular files and directories are carried; symlinks and other special files are skipped. The archive is capped at 50 MiB compressed, 500 MiB uncompressed and 10000 entries. The source path resolves within the project root scope."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Source regular file or directory to archive. Relative paths resolve against the project root."},
		),
	); err != nil {
		return fmt.Errorf("project: register archive_export: %w", err)
	}
	if err := ctx.Register("project.archive_import", a.handleArchiveImport, actor.Public(),
		actor.WithDescription("Safely extract a base64 tar.gz archive into a target directory. Rejects symlinks, hard links, absolute paths, \"..\" traversal, duplicate entries and conflicts with existing regular files (safe merge). Capped at 50 MiB compressed, 500 MiB uncompressed and 10000 entries. The target path resolves within the project root scope."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Target directory to extract into. Relative paths resolve against the project root."},
			actor.ParamDesc{Name: "content", Description: "Base64-encoded tar.gz archive bytes."},
		),
	); err != nil {
		return fmt.Errorf("project: register archive_import: %w", err)
	}
	if err := ctx.Register("project.shell_exec", a.handleShellExec, actor.Public(),
		actor.Streaming[domain.ShellChunk](),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription(`Execute a command in the project directory. External binaries run via the system shell.

Timeout is in milliseconds; default 30000 (30s). Values below 1000 are clamped to the default. Larger values are allowed, so long-running commands can request more time explicitly.`),
		actor.WithParams(
			actor.ParamDesc{Name: "command", Description: "The command to execute"},
			actor.ParamDesc{Name: "args", Description: "Arguments for the command"},
			actor.ParamDesc{Name: "dir", Description: "Working directory; defaults to project root"},
			actor.ParamDesc{Name: "timeout", Description: "Timeout in milliseconds; default 30000 (30s); values below 1000 are clamped to default; larger values are allowed"},
		),
	); err != nil {
		return fmt.Errorf("project: register shell_exec: %w", err)
	}
	if err := ctx.Register("project.git_status", a.handleGitStatus, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Show the working tree status of the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only). Workflow owners use Repo \"main\" to inspect blockers reported by workflow_stop."),
	); err != nil {
		return fmt.Errorf("project: register git_status: %w", err)
	}
	if err := ctx.Register("project.git_log", a.handleGitLog, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Show commit history for the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only)."),
	); err != nil {
		return fmt.Errorf("project: register git_log: %w", err)
	}
	if err := ctx.Register("project.git_diff", a.handleGitDiff, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Show changes for a file in the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only)."),
	); err != nil {
		return fmt.Errorf("project: register git_diff: %w", err)
	}
	if err := ctx.Register("project.git_add", a.handleGitAdd, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Stage files in the repository. Pass explicit paths rather than staging everything."),
	); err != nil {
		return fmt.Errorf("project: register git_add: %w", err)
	}
	if err := ctx.Register("project.git_commit", a.handleGitCommit, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Commit staged changes in the caller's bound worktree and return the new hash (empty Message defaults to \"worktree commit\"). Stage explicit paths with git_add first."),
	); err != nil {
		return fmt.Errorf("project: register git_commit: %w", err)
	}
	if err := ctx.Register("project.git_push", a.handleGitPush, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Push commits to a remote."),
	); err != nil {
		return fmt.Errorf("project: register git_push: %w", err)
	}
	if err := ctx.Register("project.git_pull", a.handleGitPull, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Pull from the named remote into the current branch."),
	); err != nil {
		return fmt.Errorf("project: register git_pull: %w", err)
	}
	if err := ctx.Register("project.git_branch", a.handleGitBranch, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("List repository branches (local and remote) with the current-branch marker."),
	); err != nil {
		return fmt.Errorf("project: register git_branch: %w", err)
	}
	if err := ctx.Register("project.git_checkout", a.handleGitCheckout, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Switch to a branch. Use create=true to create and switch. Rewrites the working tree — never checkout another branch in the user's main worktree without an explicit request."),
	); err != nil {
		return fmt.Errorf("project: register git_checkout: %w", err)
	}
	if err := ctx.Register("project.git_reset", a.handleGitReset, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_reset: %w", err)
	}
	if err := ctx.Register("project.git_stash_save", a.handleGitStashSave, actor.Public(),
		actor.WithDescription("Stash the working tree changes (including untracked files) of the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only). Workflow owners use Repo \"main\" to clear uncommitted blockers reported by workflow_stop.")); err != nil {
		return fmt.Errorf("project: register git_stash_save: %w", err)
	}
	if err := ctx.Register("project.git_stash_pop", a.handleGitStashPop, actor.Public(),
		actor.WithDescription("Restore a stash into the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only).")); err != nil {
		return fmt.Errorf("project: register git_stash_pop: %w", err)
	}
	if err := ctx.Register("project.git_stash_list", a.handleGitStashList, actor.Public(),
		actor.WithDescription("List stashes of the repository this git call operates on. Repo \"\" (default) selects the caller's bound worktree when attached, else the main repo; Repo \"main\" forces the main repo root (root agents only).")); err != nil {
		return fmt.Errorf("project: register git_stash_list: %w", err)
	}
	if err := ctx.Register("project.git_stash_drop", a.handleGitStashDrop, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_stash_drop: %w", err)
	}
	if err := ctx.Register("project.git_remote_list", a.handleGitRemoteList, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_remote_list: %w", err)
	}
	if err := ctx.Register("project.git_remote_add", a.handleGitRemoteAdd, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_remote_add: %w", err)
	}
	if err := ctx.Register("project.git_remote_remove", a.handleGitRemoteRemove, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_remote_remove: %w", err)
	}
	if err := ctx.Register("project.git_blame", a.handleGitBlame, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_blame: %w", err)
	}
	if err := ctx.Register("project.git_config_get", a.handleGitConfigGet, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_config_get: %w", err)
	}
	if err := ctx.Register("project.git_config_set", a.handleGitConfigSet, actor.Public()); err != nil {
		return fmt.Errorf("project: register git_config_set: %w", err)
	}
	if err := ctx.Register("project.info", a.handleInfo, actor.Public()); err != nil {
		return fmt.Errorf("project: register info: %w", err)
	}
	if err := ctx.Register("project.sync_roots", a.handleSyncRoots, actor.Public()); err != nil {
		return fmt.Errorf("project: register sync_roots: %w", err)
	}
	if err := ctx.Register("project.graph_get", a.handleGraphGet, actor.Public()); err != nil {
		return fmt.Errorf("project: register graph.get: %w", err)
	}
	if err := ctx.Register("project.graph_concept_get", a.handleGraphConceptGet, actor.Public()); err != nil {
		return fmt.Errorf("project: register graph.concept_get: %w", err)
	}
	if err := ctx.Register("project.graph_save", a.handleGraphSave, actor.Public()); err != nil {
		return fmt.Errorf("project: register graph.save: %w", err)
	}
	if err := ctx.Register("project.wiki_get_open_cards", a.handleWikiGetOpenCards, actor.Public()); err != nil {
		return fmt.Errorf("project: register wiki.get_open_cards: %w", err)
	}
	if err := ctx.Register("project.wiki_save_open_cards", a.handleWikiSaveOpenCards, actor.Public()); err != nil {
		return fmt.Errorf("project: register wiki.save_open_cards: %w", err)
	}
	if err := ctx.Register("project.wiki_open_card", a.handleWikiOpenCard, actor.Public(),
		actor.WithDescription("Open a card in the story river (prepend, idempotent)."),
	); err != nil {
		return fmt.Errorf("project: register wiki.open_card: %w", err)
	}
	if err := ctx.Register("project.wiki_close_card", a.handleWikiCloseCard, actor.Public(),
		actor.WithDescription("Close a card from the story river (remove if present)."),
	); err != nil {
		return fmt.Errorf("project: register wiki.close_card: %w", err)
	}
	if err := ctx.Register("project.wiki_get_starred", a.handleWikiGetStarred, actor.Public(),
		actor.WithDescription("Return the knowledge-base starred card ids for this project, most-recently-starred first. Starred is UI-only state persisted in the project's .ropen record; it never rewrites the starred card."),
	); err != nil {
		return fmt.Errorf("project: register wiki.get_starred: %w", err)
	}
	if err := ctx.Register("project.wiki_set_starred", a.handleWikiSetStarred, actor.Public(),
		actor.WithDescription("Star or un-star a knowledge-base card by id (idempotent). Returns the updated starred list, most-recently-starred first. Does not modify the card itself."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_starred: %w", err)
	}
	if err := ctx.Register("project.wiki_list_cards", a.handleWikiListCards, actor.Public(),
		actor.WithDescription("List project wiki cards. Tree mode (default): renders the hierarchy under RootId (default toc) as both an ASCII Tree and structured Nodes (id, type, status, modified for icon/click rendering); Depth/Limit/OrderBy cap and sort siblings. Flat mode (Flat=true): returns a flat array of cards with IncludeRaw/IncludeBuiltin/WorkflowFilter options, plus title Query and metadata filters (tags, status, type, source, list, parent, priority, due, time windows), sorted by OrderBy (default -modified), paginated by Limit (default 50), and Total count when Total=true. Workspace agent instance cards (agent:*) are hidden by default; include them via Type=\"agent\", Source=\"workspace\", or IncludeBuiltin=true."),
	); err != nil {
		return fmt.Errorf("project: register wiki.list_cards: %w", err)
	}
	if err := ctx.Register("project.wiki_search_card_content", a.handleWikiSearchCardContent, actor.Public(),
		actor.WithDescription("Search project mono cards by body content (substring or /regexp/) with the same metadata filters as search_cards (tags, status, type, source, list, parent, priority, due, modified/created/due time windows) plus optional OrderBy. Returns matching cards with a snippet and line number of the first match, and an untruncated Total. Use this to find which cards mention a concept; use search_cards for title/metadata queries."),
	); err != nil {
		return fmt.Errorf("project: register wiki.search_card_content: %w", err)
	}
	if err := ctx.Register("project.component_list", a.handleComponentList, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List MonoCard-backed prompt, tool, skill, mode, and capability components."),
	); err != nil {
		return fmt.Errorf("project: register component.list: %w", err)
	}
	if err := ctx.Register("project.component_get", a.handleComponentGet, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Get a MonoCard-backed component descriptor."),
	); err != nil {
		return fmt.Errorf("project: register component.get: %w", err)
	}
	if err := ctx.Register("project.card_mount", a.handleProjectCardMount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("project: register card.mount: %w", err)
	}
	if err := ctx.Register("project.card_unmount", a.handleProjectCardUnmount, actor.AdminOnly()); err != nil {
		return fmt.Errorf("project: register card.unmount: %w", err)
	}
	if err := ctx.Register("project.card_list", a.handleProjectCardList, actor.Public()); err != nil {
		return fmt.Errorf("project: register card.list: %w", err)
	}
	if err := ctx.Register("project.wiki_get_card_hierarchy", a.handleWikiGetCardHierarchy, actor.Public(),
		actor.WithDescription("Explore the card hierarchy around a given card. Returns a tree string with parents (↑) and children (↓) up to the requested level."),
	); err != nil {
		return fmt.Errorf("project: register wiki.get_card_hierarchy: %w", err)
	}
	if err := ctx.Register("project.wiki_get_card", a.handleWikiGetCard, actor.Public(),
		actor.WithDescription("Read a project wiki card by id: frontmatter fields plus the raw markdown body."),
	); err != nil {
		return fmt.Errorf("project: register wiki.get_card: %w", err)
	}
	if err := ctx.Register("project.wiki_get_cards_batch", a.handleWikiGetCardsBatch, actor.Public(),
		actor.WithDescription("Fetch raw mono cards by id list in one round trip. Missing cards return Found=false instead of an error."),
	); err != nil {
		return fmt.Errorf("project: register wiki.get_cards_batch: %w", err)
	}
	if err := ctx.Register("project.wiki_get_concept_tree", a.handleWikiGetConceptTree, actor.Internal(),
		actor.WithDescription("Get a normalized concept tree projection on demand."),
	); err != nil {
		return fmt.Errorf("project: register wiki.get_concept_tree: %w", err)
	}
	if err := ctx.Register("project.wiki_create_card", a.handleWikiCreateCard, actor.Public(),
		actor.WithDescription("Create a project wiki card. Required: Id, Raw (full markdown including frontmatter). Validate with wiki_validate_card when unsure; for map/task cards use the specialized wiki_create_map / wiki_create_task_card callables instead."),
	); err != nil {
		return fmt.Errorf("project: register wiki.create_card: %w", err)
	}
	if err := ctx.Register("project.wiki_edit_card", a.handleWikiEditCard, actor.Public(),
		actor.WithDescription("Edit an existing wiki card: full raw replacement (Raw) or frontmatter-preserving body patch (OldString + NewString). In patch mode a no-op and ambiguous OldString are rejected; narrow the match or set ReplaceAll."),
	); err != nil {
		return fmt.Errorf("project: register wiki.edit_card: %w", err)
	}
	if err := ctx.Register("project.wiki_delete_card", a.handleWikiDeleteCard, actor.Public(),
		actor.WithDescription("Delete a wiki card by id. Protected cards (builtin:*, __builtin_*__) are rejected."),
	); err != nil {
		return fmt.Errorf("project: register wiki.delete_card: %w", err)
	}
	if err := ctx.Register("project.wiki_validate_card", a.handleWikiValidateCard, actor.Public(),
		actor.WithDescription("Validate a card's raw markdown without persisting it. Returns structured validation errors."),
	); err != nil {
		return fmt.Errorf("project: register wiki.validate_card: %w", err)
	}
	if err := ctx.Register("project.wiki_set_status", a.handleWikiSetStatus, actor.Public(),
		actor.WithDescription("Atomically update a card's frontmatter status field without a full Raw rewrite. Lightweight path for programmatic status transitions."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_status: %w", err)
	}
	if err := ctx.Register("project.wiki_claim_task_card", a.handleWikiClaimTaskCard, actor.Public(),
		actor.WithDescription("Atomically claim a task card (CAS status from an expected set) and return the saved raw in one round trip. Fuses the spawn_assign/agent_assign claim sequence."),
	); err != nil {
		return fmt.Errorf("project: register wiki.claim_task_card: %w", err)
	}
	if err := ctx.Register("project.task_validate_outputs", a.handleTaskValidateOutputs, actor.Public(),
		actor.WithDescription("Validate a worker's produced outputs against a task card's data.outputs contract. A card with no data.outputs is always valid. Used by the review path before approving."),
	); err != nil {
		return fmt.Errorf("project: register task.validate_outputs: %w", err)
	}
	if err := ctx.Register("project.wiki_set_task_outputs", a.handleWikiSetTaskOutputs, actor.Public(),
		actor.WithDescription("Persist a worker's produced outputs onto a task card's data.task_outputs block so downstream tasks can resolve them via data bindings. Called by the review-approve path."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_task_outputs: %w", err)
	}
	if err := ctx.Register("project.wiki_frontier", a.handleWikiFrontier, actor.Public(),
		actor.WithDescription("Return the deterministic frontier for a workflow map: task cards that are unclaimed (status backlog/todo) and whose all depends_on dependencies are done."),
	); err != nil {
		return fmt.Errorf("project: register wiki.frontier: %w", err)
	}
	if err := ctx.Register("project.wiki_set_map_owner", a.handleWikiSetMapOwner, actor.Public(),
		actor.WithDescription("Bind an agent to a workflow map or task card by stamping ownerAgentId into its Data block. Map cards enforce single-binding; task cards allow overwrite (CAS guards ownership)."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_map_owner: %w", err)
	}
	if err := ctx.Register("project.wiki_unbind_agent", a.handleWikiUnbindAgent, actor.Internal(),
		actor.WithDescription("Internal: invoked by the workspace agent-deletion path when an agent is destroyed, so the project can clear the deleted agent's id from every card it was bound to (data.ownerAgentId), returning those cards to the unbound state."),
	); err != nil {
		return fmt.Errorf("project: register wiki.unbind_agent: %w", err)
	}
	if err := ctx.Register("project.wiki_create_map", a.handleWikiCreateMap, actor.Public(),
		actor.WithDescription("Create a root map card (type=map) with correct frontmatter and a body template. Id becomes the card id verbatim, so name it after the workflow's goal in a few kebab- or snake-case words (e.g. react-appdef-refactor), not an opaque code; keep it under 200 bytes. Replaces hand-written YAML for workflow maps."),
	); err != nil {
		return fmt.Errorf("project: register wiki.create_map: %w", err)
	}
	if err := ctx.Register("project.wiki_create_task_card", a.handleWikiCreateTaskCard, actor.Public(),
		actor.WithDescription("Create a task card under a map, atomically append its id to the map's include list, and optionally wire depends_on edges. Title becomes the card id verbatim, so make it describe the deliverable in a few kebab- or snake-case words (e.g. adopt-orphan-task-cards), not a bare sequence number like t3; keep it under 200 bytes. Replaces the error-prone two-step manual process."),
	); err != nil {
		return fmt.Errorf("project: register wiki.create_task_card: %w", err)
	}
	if err := ctx.Register("project.wiki_set_task_dependencies", a.handleWikiSetTaskDependencies, actor.Public(),
		actor.WithDescription("Replace a task card's data.depends_on list after validating its workflow scope."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_task_dependencies: %w", err)
	}
	if err := ctx.Register("project.wiki_template_save", a.handleWikiTemplateSave, actor.Public(),
		actor.WithDescription("Snapshot a workflow map's topology graph + task cards into a new template map (data.template:true). Runtime fields (status, owner, task_outputs, depends_on projection) are stripped; node ids are remapped under the template prefix."),
	); err != nil {
		return fmt.Errorf("project: register wiki.template_save: %w", err)
	}
	if err := ctx.Register("project.wiki_template_instantiate", a.handleWikiTemplateInstantiate, actor.Public(),
		actor.WithDescription("Copy a template map (data.template:true) into a new runnable instance map. Node ids are remapped under the instance prefix; each task card is stamped data.instance_of and given status:backlog + a depends_on projection from the instance graph. Atomic: on failure all created cards are rolled back."),
	); err != nil {
		return fmt.Errorf("project: register wiki.template_instantiate: %w", err)
	}
	if err := ctx.Register("project.wiki_automation_bind", a.handleWikiAutomationBind, actor.Public(),
		actor.WithDescription("Bind a workflow template to a scheduler card: snapshots a workflow map into a template (tpl::<MapId>) and writes the template id onto the scheduler card so subsequent timer fires instantiate fresh workflow instances instead of submitting the card body."),
	); err != nil {
		return fmt.Errorf("project: register wiki.automation_bind: %w", err)
	}
	if err := ctx.Register("project.wiki_list_templates", a.handleWikiListTemplates, actor.Public(),
		actor.WithDescription("List all workflow template map cards (data.template:true) in the project. Used by the scheduler editor's template selector."),
	); err != nil {
		return fmt.Errorf("project: register wiki.list_templates: %w", err)
	}
	if err := ctx.Register("project.wiki_list_template_runs", a.handleWikiListTemplateRuns, actor.Public(),
		actor.WithDescription("Return the most recent instantiation runs of a workflow template map, newest first."),
	); err != nil {
		return fmt.Errorf("project: register wiki.list_template_runs: %w", err)
	}
	if err := ctx.Register("project.wiki_set_map_inputs", a.handleWikiSetMapInputs, actor.Public(),
		actor.WithDescription("Set the map-level inputs table for a workflow map (channel a of typed-inputs). The caller parses startup/intake text into key-value pairs and writes them here; downstream tasks consume them via data-flow bindings whose FromNode is \"$map\"."),
	); err != nil {
		return fmt.Errorf("project: register wiki.set_map_inputs: %w", err)
	}
	if err := ctx.Register("project.wiki_promote_node_outputs", a.handleWikiPromoteNodeOutputs, actor.Public(),
		actor.WithDescription("Promote a task node's persisted outputs into the map-level inputs table (channel c of typed-inputs). Used after an intake grilling node is reviewed and its outputs are persisted — the node's output field names become map-level input keys."),
	); err != nil {
		return fmt.Errorf("project: register wiki.promote_node_outputs: %w", err)
	}
	if err := ctx.Register("project.wiki_list_dependencies", a.handleWikiListDependencies, actor.Public(),
		actor.WithDescription("List task-card data.depends_on edges (optionally filtered to one workflow map)."),
	); err != nil {
		return fmt.Errorf("project: register wiki.list_dependencies: %w", err)
	}
	if err := ctx.Register("project.spawn_agent", a.handleSpawnAgent, actor.AdminOnly()); err != nil {
		return fmt.Errorf("project: register spawn_agent: %w", err)
	}
	if err := ctx.Register("project.worktree_create", a.handleWorktreeCreate, actor.AdminOnly(),
		actor.WithDescription("Create a linked git worktree for isolated agent operations. Requires a name; creates a branch and checks out into .git/wt/<id>."),
	); err != nil {
		return fmt.Errorf("project: register worktree.create: %w", err)
	}
	if err := ctx.Register("project.worktree_list", a.handleWorktreeList, actor.Public(),
		actor.WithDescription("List all worktrees tracked by this project."),
	); err != nil {
		return fmt.Errorf("project: register worktree.list: %w", err)
	}
	if err := ctx.Register("project.worktree_get", a.handleWorktreeGet, actor.Public(),
		actor.WithDescription("Get a worktree by ID."),
	); err != nil {
		return fmt.Errorf("project: register worktree.get: %w", err)
	}
	if err := ctx.Register("project.worktree_discard", a.handleWorktreeDiscard, actor.AdminOnly(),
		actor.WithDescription("Discard (remove) a worktree. If the worktree has uncommitted changes, set force=true to override."),
	); err != nil {
		return fmt.Errorf("project: register worktree.discard: %w", err)
	}
	if err := ctx.Register("project.worktree_release_binding", a.handleWorktreeReleaseBinding, actor.Internal(),
		actor.WithDescription("Internal: invoked by workspace.delete_agent when an agent is destroyed, so the project can drop the agent→worktree binding and remove the worktree if no other agent remains bound."),
	); err != nil {
		return fmt.Errorf("project: register worktree.release_binding: %w", err)
	}
	if err := ctx.Register("project.worktree_bound_check", a.handleWorktreeBoundCheck, actor.Internal(),
		actor.WithDescription("Internal: reports whether a given agent has an active worktree binding. Consumed by appmanager's dev surface so a caller bound to a worktree is routed to that worktree for dev_generate/dev_gate/register_project."),
	); err != nil {
		return fmt.Errorf("project: register worktree.bound_check: %w", err)
	}
	if err := ctx.Register("project.worktree_copy", a.handleWorktreeCopy, actor.Public(),
		actor.WithDescription("Copy files from the main repo root into the caller's bound worktree. Sources are relative to the main repo root; DestDir is relative to the worktree root and defaults to the worktree root itself. Existing destination files are only overwritten when Force=true."),
	); err != nil {
		return fmt.Errorf("project: register worktree.copy: %w", err)
	}
	if err := ctx.Register("project.worktree_agent_bindings", a.handleWorktreeAgentBindings, actor.Public(),
		actor.WithDescription("List all agent→worktree bindings for topology visibility, discard safety checks, and restart recovery."),
	); err != nil {
		return fmt.Errorf("project: register worktree.agent_bindings: %w", err)
	}
	if err := ctx.Register("project.worktree_enter", a.handleWorktreeEnter, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "Name", Description: "Deprecated; ignored. The worktree name/branch is always <callerID>-<random> (per-agent exclusive, never collides)."},
			actor.ParamDesc{Name: "BaseRef", Description: "commit-ish the new branch starts from; empty = HEAD."},
		),
		actor.WithDescription("Enter (self-bind) a per-agent exclusive worktree for this session. Idempotent: returns the current binding if already bound. Name is ignored; the worktree is named after the caller to enforce one-agent-one-worktree isolation."),
	); err != nil {
		return fmt.Errorf("project: register worktree.enter: %w", err)
	}
	if err := ctx.Register("project.worktree_exit", a.handleWorktreeExit, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "Mode", Description: "discard | merge. discard deletes the worktree; merge lands its branch on the main repo's base branch."},
			actor.ParamDesc{Name: "Force", Description: "discard only: required when the worktree has uncommitted changes."},
			actor.ParamDesc{Name: "BaseBranch", Description: "merge only: target branch in the main repo; empty = repo default (main/master)."},
		),
		actor.WithDescription("Leave the caller's worktree and return to the main repo."),
	); err != nil {
		return fmt.Errorf("project: register worktree.exit: %w", err)
	}
	// ── No-git mode ──
	if err := ctx.Register("project.no_git_mode_get", a.handleNoGitModeGet, actor.Public(),
		actor.WithDescription("Get the project's no-git mode setting and whether the project root is an actual git repository."),
	); err != nil {
		return fmt.Errorf("project: register no_git_mode_get: %w", err)
	}
	if err := ctx.Register("project.no_git_mode_set", a.handleNoGitModeSet, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "NoGitMode", Description: "true = workflows run without git worktree isolation (owner works directly in the project root); false = restore worktree-backed workflows."},
		),
		actor.WithDescription("Set the project's no-git mode. When enabled, workflow activation skips owner-worktree creation and all downstream git operations are skipped via the empty-worktree-ID branch."),
	); err != nil {
		return fmt.Errorf("project: register no_git_mode_set: %w", err)
	}
	// ── Default extra bundles ──
	if err := ctx.Register("project.default_bundles_get", a.handleDefaultBundlesGet, actor.Public(),
		actor.WithDescription("Get the project-level default extra bundle card IDs merged into every spawn_agent request (applies to agents and sub-agents created under this project)."),
	); err != nil {
		return fmt.Errorf("project: register default_bundles_get: %w", err)
	}
	if err := ctx.Register("project.default_bundles_set", a.handleDefaultBundlesSet, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "BundleIDs", Description: "Full replacement list of mountable card IDs (builtin:bundle:*, app-bundle:*, mcp:* server cards, project cards). Empty array clears the defaults. Affects only agents spawned afterwards."},
		),
		actor.WithDescription("Set the project-level default extra bundle card IDs. spawn_agent merges these (deduped, before caller-supplied extras) into ExtraBundleIDs so new agents and sub-agents mount them by default."),
	); err != nil {
		return fmt.Errorf("project: register default_bundles_set: %w", err)
	}
	// ── Phase 2: workflow-worktree integration callables ──
	if err := ctx.Register("project.workflow_create_worktree", a.handleWorkflowCreateWorktree, actor.Internal(),
		actor.WithDescription("Internal: create a workflow owner worktree and bind the owner agent. Called by agent actor during activateWorkflow."),
	); err != nil {
		return fmt.Errorf("project: register workflow_create_worktree: %w", err)
	}
	if err := ctx.Register("project.worktree_merge_to_parent", a.handleWorktreeMergeToParent, actor.Internal(),
		actor.WithDescription("Internal: merge a child worktree into its parent worktree. Called by workspace actor during review approve."),
	); err != nil {
		return fmt.Errorf("project: register worktree_merge_to_parent: %w", err)
	}
	if err := ctx.Register("project.worktree_rebase_to_parent", a.handleWorktreeRebaseToParent, actor.Internal(),
		actor.WithDescription("Internal: rebase a child worktree onto its parent worktree's branch HEAD. Called by workspace actor during review reject."),
	); err != nil {
		return fmt.Errorf("project: register worktree_rebase_to_parent: %w", err)
	}
	if err := ctx.Register("project.worktree_verify_merged_to_parent", a.handleWorktreeVerifyMergedToParent, actor.Internal(),
		actor.WithDescription("Internal: verify a child worktree is already merged into its parent and clean it up if so. Called by workspace actor during review approve lineage check."),
	); err != nil {
		return fmt.Errorf("project: register worktree_verify_merged_to_parent: %w", err)
	}
	if err := ctx.Register("project.workflow_worktree_clean_check", a.handleWorkflowWorktreeCleanCheck, actor.Internal(),
		actor.WithDescription("Internal: check whether an agent's bound worktree has uncommitted changes. Called during workflow teardown."),
	); err != nil {
		return fmt.Errorf("project: register workflow_worktree_clean_check: %w", err)
	}
	if err := ctx.Register("project.workflow_stop_merge_worktree", a.handleWorkflowStopMergeWorktree, actor.Internal(),
		actor.WithDescription("Internal: merge a workflow owner worktree into the main repo's base branch. Called by agent actor during clearWorkflow."),
	); err != nil {
		return fmt.Errorf("project: register workflow_stop_merge_worktree: %w", err)
	}
	if err := ctx.Register("project.worktree_discard_by_id", a.handleWorktreeDiscardByID, actor.Internal(),
		actor.WithDescription("Internal: discard a worktree by ID with structured response. Called by workspace actor for orphan worktree cleanup."),
	); err != nil {
		return fmt.Errorf("project: register worktree_discard_by_id: %w", err)
	}
	if err := ctx.Register("project.review_changeset", a.handleReviewChangesetSummary, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "AgentActorId", Description: "The child agent whose changeset to read."},
			actor.ParamDesc{Name: "ForceRefresh", Description: "Re-generate the changeset even if a cached snapshot exists."},
		),
		actor.WithDescription("Generate or retrieve a frozen, read-only changeset summary for a workflow child agent's worktree. Includes baseline/HEAD, commit list, tracked file status with diff metadata, untracked file summaries, rename/deletion detection, binary/generated markers, and test results. Only the child's direct parent agent or a trusted human/admin may call this."),
	); err != nil {
		return fmt.Errorf("project: register review.changeset: %w", err)
	}
	if err := ctx.Register("project.review_file_content", a.handleReviewChangesetFileContent, actor.Public(),
		actor.WithParams(
			actor.ParamDesc{Name: "AgentActorId", Description: "The child agent whose changeset file to read."},
			actor.ParamDesc{Name: "FilePath", Description: "File path within the child's worktree."},
			actor.ParamDesc{Name: "Offset", Description: "Line offset (1-based) for pagination. Omit for start of file."},
			actor.ParamDesc{Name: "Limit", Description: "Maximum lines to return (default 200)."},
		),
		actor.WithDescription("Read paginated file content from a frozen review changeset. For tracked modified files, returns the unified diff; for untracked files, returns the full content. Large outputs are paginated via Offset/Limit."),
	); err != nil {
		return fmt.Errorf("project: register review.file_content: %w", err)
	}
	if err := ctx.Register("project.review_changeset_freeze", a.handleReviewChangesetFreeze, actor.Internal(),
		actor.WithParams(
			actor.ParamDesc{Name: "AgentActorId", Description: "The child agent whose changeset to freeze."},
			actor.ParamDesc{Name: "ForceRefresh", Description: "Re-generate even if a ready snapshot exists."},
			actor.ParamDesc{Name: "TestCommand", Description: "Optional shell command for test execution during freeze. Empty = skip tests. Human callers only."},
			actor.ParamDesc{Name: "TestTimeoutMs", Description: "Test timeout in ms (default 60000, max 300000)."},
			actor.ParamDesc{Name: "TaskCardId", Description: "Bound task card ID for custom-data projection."},
		),
		actor.WithDescription("Internal: kick off async changeset generation for a workflow child agent entering pending_review. Registers a preparing placeholder immediately and launches a background goroutine. Called by the agent's turn engine, not directly by LLMs."),
	); err != nil {
		return fmt.Errorf("project: register review.changeset_freeze: %w", err)
	}
	if err := ctx.Register("project.review_changeset_finalize", a.handleReviewChangesetFinalize, actor.Internal(),
		actor.WithDescription("Internal: apply the async generation result on the project owner loop. Called by the goroutine via self-invoke."),
	); err != nil {
		return fmt.Errorf("project: register review.changeset_finalize: %w", err)
	}
	if err := ctx.Register("project.review_changeset_clear", a.handleReviewChangesetClear, actor.Internal(),
		actor.WithDescription("Internal: drop the cached changeset for an agent. Called by workspace after approve/reject to free memory."),
	); err != nil {
		return fmt.Errorf("project: register review.changeset_clear: %w", err)
	}
	if err := ctx.RegisterDomain("project").ExposeChildren(); err != nil {
		return fmt.Errorf("project: expose to children: %w", err)
	}
	if err := ctx.Register("project.execute_timer_card", a.handleExecuteTimerCard, actor.Internal()); err != nil {
		return fmt.Errorf("project: register execute_timer_card: %w", err)
	}
	if err := ctx.Register("project.timer_check", a.handleTimerCheck, actor.Internal()); err != nil {
		return fmt.Errorf("project: register timer.check: %w", err)
	}
	if err := ctx.Register("project.wiki_trigger_timer_card", a.handleTriggerTimerCard, actor.Public(),
		actor.WithDescription("Trigger a scheduled wiki card immediately without changing its schedule."),
	); err != nil {
		return fmt.Errorf("project: register wiki.trigger_timer_card: %w", err)
	}
	if err := ctx.Register("project.wiki_dispatch_plan", a.handleWikiDispatchPlan, actor.Public()); err != nil {
		return fmt.Errorf("project: register wiki.dispatch_plan: %w", err)
	}
	if err := ctx.Register("project.wiki_list_timers", a.handleListTimers, actor.Public(),
		actor.WithDescription("List all scheduled timer cards and their next fire times."),
	); err != nil {
		return fmt.Errorf("project: register wiki.list_timers: %w", err)
	}
	if err := ctx.Register("project.wiki_toggle_timer", a.handleToggleTimer, actor.Public(),
		actor.WithDescription("Enable or disable a scheduled timer card."),
	); err != nil {
		return fmt.Errorf("project: register wiki.toggle_timer: %w", err)
	}
	if err := ctx.Register("project.set_protected_files", a.setProtectedFiles, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
	); err != nil {
		return fmt.Errorf("project: register set_protected_files: %w", err)
	}
	if err := ctx.After(monitorInterval, "project.timer_check", nil); err != nil {
		return fmt.Errorf("project: schedule timer check: %w", err)
	}
	if changed, err := a.reconcileWorktrees(); err != nil {
		ctx.Logger().Error("project: reconcile worktrees failed", "error", err)
	} else if changed {
		if err := a.persistAllWorktreeManifests(); err != nil {
			ctx.Logger().Error("project: persist reconciled worktrees failed", "error", err)
		}
	}
	return nil
}

func (a *Actor) handleSpawnAgent(ctx actor.PureContext, req domain.ProjectSpawnAgentReq) (domain.ProjectSpawnAgentResp, error) {
	if req.SpawnName == "" {
		return domain.ProjectSpawnAgentResp{}, fmt.Errorf("project.spawn_agent: SpawnName required")
	}
	// Project-level default extra bundles: merged into the request's extras
	// (defaults first, deduped) so every agent spawned under this project —
	// top-level, fork child, or workflow worker — carries them in addition to
	// kind-config defaults. Read-only-kind filtering stays in the agent's
	// builtinCardsToSeed. A config-card read failure fails the spawn loudly
	// rather than silently dropping the user-configured defaults.
	defaults, err := a.defaultExtraBundlesSnapshot()
	if err != nil {
		return domain.ProjectSpawnAgentResp{}, fmt.Errorf("project.spawn_agent: read default extra bundles: %w", err)
	}
	req.ExtraBundleIDs = mergeExtraBundleIDs(defaults, req.ExtraBundleIDs)
	worktreeID := req.WorktreeID
	if worktreeID == "" && req.CloneSourceActorID != "" {
		a.bindingMu.RLock()
		worktreeID = a.agentWorktree[req.CloneSourceActorID]
		a.bindingMu.RUnlock()
	}
	// Fork-child worktree inheritance: a fork child (ChildConfig != nil)
	// inherits the parent agent's bound worktree so file/git/shell tools route
	// into the same worktree sandbox. The child does NOT mount worktree mode
	// (the agent skips syncWorktreeModeCard for child.Mode), so this is purely
	// a binding-level path-routing inheritance — the child sees no mode card,
	// cannot exit/merge/discard the worktree, and is transient. Workflow
	// worker spawns (ChildConfig == nil, explicit WorktreeID) are unaffected.
	if worktreeID == "" && req.ChildConfig != nil && req.ChildConfig.ParentActorID != "" {
		a.bindingMu.RLock()
		worktreeID = a.agentWorktree[req.ChildConfig.ParentActorID]
		a.bindingMu.RUnlock()
	}
	primary := derefSlotReq(req.Primary)
	fast := derefSlotReq(req.Fast)
	execution := derefSlotReq(req.Execution)
	review := derefSlotReq(req.Review)
	summary := derefSlotReq(req.Summary)

	// Thread spawn-time goal through to NewActor so the agent self-assigns
	// it during OnStart, avoiding the cross-actor invoke race.
	var spawnGoal *domain.AgentInternalAssignGoalReq
	if req.GoalCondition != "" {
		spawnGoal = &domain.AgentInternalAssignGoalReq{
			Condition:       req.GoalCondition,
			InterpretedGoal: req.InterpretedGoal,
			BoundTaskCardID: req.BoundTaskCardID,
			MaxTurns:        req.GoalMaxTurns,
			PromptPrelude:   req.PromptPrelude,
		}
	}

	var props actor.Props
	if req.ChildConfig != nil {
		// Fork-child path: resolve the parent agent's ref.Ref from the
		// ActorID so the child can deliver explore_complete back to the
		// parent via ref.Invoke.
		var parentRef ref.Ref
		if req.ChildConfig.ParentActorID != "" {
			if cid, err := identity.ParseCanonicalID(req.ChildConfig.ParentActorID); err == nil {
				if r, ok := ctx.LookupID(id.From(cid)); ok {
					parentRef = r
				}
			}
		}
		props = actor.PropsFromFunc(agent.NewChildActor(
			req.WorkspaceID,
			req.AgentKind,
			parentRef,
			req.ChildConfig.ParentTurnID,
			req.ChildConfig.ParentStepID,
			req.ChildConfig.ParentToolUseID,
			req.ChildConfig.Task,
			req.ChildConfig.Prompt,
			primary,
			fast,
			execution,
			review,
			summary,
			req.ChildConfig.MaxIterations,
			req.ChildConfig.HotContext,
			req.ExtraBundleIDs,
		)).WithPlanner().WithAsyncStart()
	} else {
		props = actor.PropsFromFunc(agent.NewActor(
			req.WorkspaceID,
			req.AgentKind,
			req.DisplayName,
			primary,
			fast,
			execution,
			review,
			summary,
			spawnGoal,
			req.PermissionMode,
			req.ParentAgentID,
			req.ExtraBundleIDs,
		)).WithPlanner().WithAsyncStart()
	}
	if req.ActorID != "" {
		cid, err := identity.ParseCanonicalID(req.ActorID)
		if err != nil {
			return domain.ProjectSpawnAgentResp{},
				fmt.Errorf("project.spawn_agent: invalid ActorId %q: %w", req.ActorID, err)
		}
		props = props.WithID(id.From(cid))
	}
	spawned, err := ctx.Spawn(props, req.SpawnName)
	if err != nil {
		return domain.ProjectSpawnAgentResp{}, fmt.Errorf("project.spawn_agent: %w", err)
	}
	resp := domain.ProjectSpawnAgentResp{}
	if spawned != nil {
		resp.ActorID = spawned.ID().String()
		// NOTE: we intentionally do NOT block for OnStart here. The agent's
		// OnStart synchronously calls back into the workspace owner loop
		// (fetchAgentKindConfig → workspace.get_agent_kind_config), and
		// load_agent — which drives this spawn — runs on that same workspace
		// loop. Blocking would deadlock the three-loop chain
		// workspace→project→agent. Readiness is handled by the pure
		// session.summary handler waiting on the agent's onStartDone signal.
		//
		// Worktree binding (方案 B): record agentActorID → worktree so the
		// caller-id interception in file/git/shell handlers routes this
		// agent's calls into its bound worktree. Done after spawn so the
		// agent's actor id is known. Failure to bind is non-fatal: the agent
		// still runs, just on the main root (log + clear WorktreeID in resp).
		boundWorktreeID := ""
		if worktreeID != "" {
			// Clear any pre-existing binding (e.g. from clone-source
			// inheritance or test setup) so the explicit WorktreeID
			// takes precedence. Without this, the existing-binding
			// check in bindWorktree rejects the override.
			a.bindingMu.Lock()
			delete(a.agentWorktree, spawned.ID().String())
			a.bindingMu.Unlock()
			if err := a.bindWorktree(spawned.ID().String(), worktreeID); err != nil {
				ctx.Logger().Warn("project.spawn_agent: worktree bind failed; agent runs on main root",
					"worktreeId", worktreeID, "error", err)
			} else {
				boundWorktreeID = worktreeID
			}
		}
		// Record the parent agent for review changeset authorization. Stored
		// under worktreeParentMu so concurrent reads from PureContext callables are safe.
		if req.ParentAgentID != "" {
			a.worktreeParentMu.Lock()
			if a.agentParent == nil {
				a.agentParent = make(map[string]string)
			}
			a.agentParent[spawned.ID().String()] = req.ParentAgentID
			a.worktreeParentMu.Unlock()
		}
		// Persist the binding to the worktree's ground-truth manifest so it
		// survives a restart: loadWorktreeCache rebuilds agentWorktree from
		// manifests, and the in-memory map alone would be lost on crash before
		// the next lifecycle persist (timer/exit/merge). Same contract the
		// owner bind paths honor. After the agentParent block: the manifest
		// carries parentage for the review-authorization lineage.
		if boundWorktreeID != "" {
			if err := a.persistWorktreeManifest(boundWorktreeID); err != nil {
				ctx.Logger().Warn("project.spawn_agent: persist worktree manifest failed",
					"worktreeId", boundWorktreeID, "error", err)
			}
		}
	}
	return resp, nil
}

// derefSlotReq returns the pointed-to slot, or the zero value when nil.
func derefSlotReq(s *domain.ModelSlot) domain.ModelSlot {
	if s == nil {
		return domain.ModelSlot{}
	}
	return *s
}

func (a *Actor) OnStop(ctx actor.Context) error {
	a.stopFileWatcher()
	// Release the fsCardStore cache watcher (if the backing store is the fs
	// implementation). Other CardStore implementations without Close are
	// unaffected.
	if closer, ok := a.store.(interface{ Close() }); ok {
		closer.Close()
	}
	// Worktree state is written through to per-worktree manifests on every
	// mutation (worktree_manifest.go); no bulk flush needed on stop.
	return a.Save()
}

// OnDestroy cascades worktree cleanup when the project actor is destroyed
// (e.g. via unmount). Unlike OnStop (which may be a restart), destroy is
// final: all worktrees must be removed from git's registry and their
// physical directories deleted. The worktree base directory is then
// removed to clean up any residual files.
func (a *Actor) OnDestroy(ctx actor.Context) error {
	root, err := a.rootPath()
	if err != nil {
		ctx.Logger().Warn("project: OnDestroy: no roots, skipping worktree cleanup", "error", err)
		return nil
	}
	a.worktreeParentMu.RLock()
	worktrees := make([]gen.ProjectWorktree, 0, len(a.worktrees))
	for _, wt := range a.worktrees {
		worktrees = append(worktrees, wt)
	}
	a.worktreeParentMu.RUnlock()
	for _, wt := range worktrees {
		if wt.Status == "discarded" || wt.Path == "" {
			continue
		}
		if err := gitWorktreeRemove(root, wt.Path); err != nil {
			ctx.Logger().Warn("project: OnDestroy: remove worktree", "path", wt.Path, "error", err)
		}
		deleteWorktreeBranchRef(root, wt.Branch)
	}
	// Remove the worktree base directory to clean up any residual files
	// (orphan dirs from crashed processes, stale checkouts, etc.).
	baseDir := a.worktreeBaseDir()
	if err := os.RemoveAll(baseDir); err != nil {
		ctx.Logger().Warn("project: OnDestroy: remove worktree base dir", "dir", baseDir, "error", err)
	}
	return nil
}

func (a *Actor) Save() error {
	// All project actor state is written through to ground-truth cards on every
	// mutation (config card via updateConfigCard, UI-state card via
	// updateOpenCards). There is no in-memory authoritative copy to flush, so
	// Save() is intentionally a no-op. The persist.Persistent interface is
	// satisfied for the runtime's lifecycle coordination.
	return nil
}

// saveGraphs persists Graphs to disk. Caller must hold graphMu (at least RLock).
func (a *Actor) saveGraphs() error {
	if a.Graphs == nil {
		return nil
	}
	dir := a.metaDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("project: mkdir meta dir: %w", err)
	}
	data, err := json.Marshal(a.Graphs)
	if err != nil {
		return fmt.Errorf("project: marshal graphs: %w", err)
	}
	if err := os.WriteFile(a.graphsPath(), data, 0644); err != nil {
		return fmt.Errorf("project: write graphs: %w", err)
	}
	return nil
}

func (a *Actor) Load() error {
	// The Actor struct holds no persistent-authority state, so Load() only
	// ensures the ground-truth cards exist: it performs the one-time import of
	// pre-refactor state (the "<actorID>" persist blob, .sporecode/state.json,
	// generated-files.json, wiki-state.json) into the config and UI-state
	// cards. Subsequent reads go straight to the cards.
	return a.migrateLegacyStateCards()
}

func (a *Actor) loadGraphs() error {
	if a.Graphs == nil {
		a.Graphs = make(map[string]gen.ProjectGraphEnvelopeResp)
	}
	// 优先读取新的 graphs.json
	data, err := os.ReadFile(a.graphsPath())
	if err == nil {
		var graphs map[string]gen.ProjectGraphEnvelopeResp
		if err := json.Unmarshal(data, &graphs); err != nil {
			return fmt.Errorf("project: load graphs: %w", err)
		}
		a.Graphs = graphs
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("project: read graphs: %w", err)
	}
	// 回退到旧的 state.json 中的 graphs（兼容旧数据）
	data, err = os.ReadFile(a.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("project: read state for graphs fallback: %w", err)
	}
	var oldState struct {
		Graphs map[string]gen.ProjectGraphEnvelopeResp `json:"graphs"`
	}
	if err := json.Unmarshal(data, &oldState); err != nil {
		return fmt.Errorf("project: unmarshal old state graphs: %w", err)
	}
	if oldState.Graphs != nil {
		a.Graphs = oldState.Graphs
	}
	return nil
}

func graphKey(graphKind, graphID string) string {
	return graphKind + "\x00" + graphID
}

// nextGraphRevision issues the next graph snapshot revision: strictly
// monotonic within the actor, so every successful save bumps the revision
// and change detection (before != after) can never miss a write. Compare
// the previous wall-clock implementation, where two saves within one timer
// tick produced identical revisions and silently broke emit-on-heal tests.
func (a *Actor) nextGraphRevision() string {
	return fmt.Sprintf("rev-%d", atomic.AddInt64(&a.graphRevSeq, 1))
}

func (a *Actor) handleGraphConceptGet(_ actor.PureContext, req gen.ProjectGraphConceptGetReq) (gen.ProjectGraphConceptGetResp, error) {
	if req.GraphKind == "" || req.ID == "" || req.ConceptID == "" {
		return gen.ProjectGraphConceptGetResp{}, fmt.Errorf("project.graph.concept_get: GraphKind, Id and ConceptId are required")
	}
	a.graphMu.RLock()
	resp, ok := a.Graphs[graphKey(req.GraphKind, req.ID)]
	a.graphMu.RUnlock()
	if !ok {
		return gen.ProjectGraphConceptGetResp{}, fmt.Errorf("project.graph.concept_get: graph %s/%s not found", req.GraphKind, req.ID)
	}
	var graph gen.TargetGraph
	if err := json.Unmarshal([]byte(resp.EnvelopeText), &graph); err != nil {
		return gen.ProjectGraphConceptGetResp{}, fmt.Errorf("project.graph.concept_get: decode graph: %w", err)
	}
	for _, concept := range graph.Concepts {
		if concept.ID == req.ConceptID {
			return gen.ProjectGraphConceptGetResp{Concept: concept, GraphKind: req.GraphKind, ID: req.ID, Revision: resp.Meta.Revision}, nil
		}
	}
	return gen.ProjectGraphConceptGetResp{}, fmt.Errorf("project.graph.concept_get: concept %q not found", req.ConceptID)
}

// initWorkflowTopoDirtyTracking was the callback-based cache invalidation
// hook for the old a.Graphs workflow_topo snapshot. The card-backed
// implementation recomputes from cards on every read, so no dirty tracking is
// needed. The function is retained as a no-op so OnInit and tests that call it
// compile without churn; future cleanup can remove the call sites.
func (a *Actor) initWorkflowTopoDirtyTracking() {}

func (a *Actor) handleGraphGet(ctx actor.PureContext, req gen.ProjectGraphGetReq) (gen.ProjectGraphEnvelopeResp, error) {
	if req.GraphKind == "" || req.ID == "" {
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.get: GraphKind and Id are required")
	}
	if req.GraphKind == GraphKindWorkflowTopo {
		// workflow_topo is always recomputed from the card hierarchy.
		g, rev := a.loadWorkflowTopo(req.ID)
		resp, err := a.buildWfTopoEnvelope(req.ID, g, rev)
		if err != nil {
			return gen.ProjectGraphEnvelopeResp{}, err
		}
		if req.Revision != "" && req.Revision != resp.Meta.Revision {
			return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.get: revision %q not found for %s/%s", req.Revision, req.GraphKind, req.ID)
		}
		return resp, nil
	}
	key := graphKey(req.GraphKind, req.ID)
	a.graphMu.RLock()
	resp, ok := a.Graphs[key]
	a.graphMu.RUnlock()
	if !ok {
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.get: graph %s/%s not found", req.GraphKind, req.ID)
	}
	if req.Revision != "" && req.Revision != resp.Meta.Revision {
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.get: revision %q not found for %s/%s", req.Revision, req.GraphKind, req.ID)
	}
	return resp, nil
}

func (a *Actor) handleGraphSave(ctx actor.PureContext, req gen.ProjectGraphSaveReq) (gen.ProjectGraphEnvelopeResp, error) {
	if req.GraphKind == "" || req.ID == "" {
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.save: GraphKind and Id are required")
	}
	if req.GraphKind == GraphKindWorkflowTopo {
		// workflow_topo graph is derived from cards (nodes/edges from card
		// hierarchy, bindings/inputs from map card Data). The generic
		// graph.save callable cannot store it; use the dedicated
		// wiki_set_map_inputs / wiki_set_task_dependencies callables instead.
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.save: workflow_topo graph is derived from cards and cannot be saved via graph.save")
	}
	a.graphMu.Lock()
	if a.Graphs == nil {
		a.Graphs = make(map[string]gen.ProjectGraphEnvelopeResp)
	}
	key := graphKey(req.GraphKind, req.ID)
	current, exists := a.Graphs[key]
	if req.ExpectedRevision != "" && (!exists || current.Meta.Revision != req.ExpectedRevision) {
		a.graphMu.Unlock()
		return gen.ProjectGraphEnvelopeResp{}, fmt.Errorf("project.graph.save: expected revision %q does not match current revision", req.ExpectedRevision)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	meta := req.Meta
	meta.GraphKind = req.GraphKind
	meta.ID = req.ID
	if meta.ProjectID == "" {
		meta.ProjectID = req.ProjectID
	}
	if meta.Revision == "" || meta.Revision == req.ExpectedRevision {
		meta.Revision = a.nextGraphRevision()
	}
	if meta.CreatedAt == "" {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now
	resp := gen.ProjectGraphEnvelopeResp{Meta: meta, EnvelopeText: req.EnvelopeText}
	a.Graphs[key] = resp
	if err := a.saveGraphs(); err != nil {
		a.graphMu.Unlock()
		return gen.ProjectGraphEnvelopeResp{}, err
	}
	a.graphMu.Unlock()
	_ = ctx.EmitEvent("graph_changed", gen.GraphChangedEvent{
		GraphKind: meta.GraphKind,
		ID:        meta.ID,
		Revision:  meta.Revision,
	})
	return resp, nil
}

// ── wiki open cards ──

func (a *Actor) handleWikiGetOpenCards(_ actor.PureContext, _ domain.WikiOpenCardsReq) (domain.WikiOpenCardsResp, error) {
	cards, err := a.openCardsSnapshot()
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	return domain.WikiOpenCardsResp{
		OpenCards: cards,
	}, nil
}

func (a *Actor) handleWikiSaveOpenCards(_ actor.PureContext, req domain.WikiSaveOpenCardsReq) (domain.WikiOpenCardsResp, error) {
	err := a.updateOpenCards(func(cards *[]string) {
		*cards = req.OpenCards
	})
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	return domain.WikiOpenCardsResp{
		OpenCards: req.OpenCards,
	}, nil
}

func (a *Actor) handleWikiOpenCard(_ actor.PureContext, req domain.WikiOpenCardReq) (domain.WikiOpenCardsResp, error) {
	if req.ID == "" {
		return domain.WikiOpenCardsResp{}, fmt.Errorf("project: open_card requires CardId")
	}
	err := a.updateOpenCards(func(cards *[]string) {
		for _, id := range *cards {
			if id == req.ID {
				return
			}
		}
		*cards = append([]string{req.ID}, *cards...)
	})
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	cards, err := a.openCardsSnapshot()
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	return domain.WikiOpenCardsResp{
		OpenCards: cards,
	}, nil
}

func (a *Actor) handleWikiCloseCard(_ actor.PureContext, req domain.WikiCloseCardReq) (domain.WikiOpenCardsResp, error) {
	if req.ID == "" {
		return domain.WikiOpenCardsResp{}, fmt.Errorf("project: close_card requires CardId")
	}
	err := a.updateOpenCards(func(cards *[]string) {
		next := make([]string, 0, len(*cards))
		for _, id := range *cards {
			if id != req.ID {
				next = append(next, id)
			}
		}
		*cards = next
	})
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	cards, err := a.openCardsSnapshot()
	if err != nil {
		return domain.WikiOpenCardsResp{}, err
	}
	return domain.WikiOpenCardsResp{
		OpenCards: cards,
	}, nil
}

// ── knowledge-base starred cards ──

func (a *Actor) handleWikiGetStarred(_ actor.PureContext, _ domain.WikiGetStarredReq) (domain.WikiStarredResp, error) {
	starred, err := a.starredSnapshot()
	if err != nil {
		return domain.WikiStarredResp{}, err
	}
	return domain.WikiStarredResp{Starred: starred}, nil
}

func (a *Actor) handleWikiSetStarred(_ actor.PureContext, req domain.WikiSetStarredReq) (domain.WikiStarredResp, error) {
	if req.ID == "" {
		return domain.WikiStarredResp{}, fmt.Errorf("project: set_starred requires Id")
	}
	err := a.updateStarred(func(starred *[]string) {
		if req.Starred {
			for _, id := range *starred {
				if id == req.ID {
					return
				}
			}
			*starred = append([]string{req.ID}, *starred...)
			return
		}
		next := make([]string, 0, len(*starred))
		for _, id := range *starred {
			if id != req.ID {
				next = append(next, id)
			}
		}
		*starred = next
	})
	if err != nil {
		return domain.WikiStarredResp{}, err
	}
	starred, err := a.starredSnapshot()
	if err != nil {
		return domain.WikiStarredResp{}, err
	}
	return domain.WikiStarredResp{Starred: starred}, nil
}

// rootPath returns the primary root path for git/shell operations.
func (a *Actor) rootPath() (string, error) {
	roots, err := a.rootsSnapshot()
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("project: no roots configured")
	}
	return roots[0].Path, nil
}

// errOutsideRoots is returned by resolvePath when an absolute path does not
// fall inside any configured root.
// resolvePath resolves a caller path against the project's configured roots.
// Supported forms:
//   - "moduleName/rel/path" or "moduleName\" (Windows) → root named moduleName
//   - absolute path inside a root → matching root
//   - relative path without module prefix → first root
func (a *Actor) resolvePath(p string) (domain.RootDirEntry, string, error) {
	roots, rootsErr := a.rootsSnapshot()
	if rootsErr != nil {
		return domain.RootDirEntry{}, "", rootsErr
	}
	if len(roots) == 0 {
		return domain.RootDirEntry{}, "", fmt.Errorf("project: no roots configured")
	}
	p = filepath.Clean(p)

	// Explicit root-name prefix (e.g. "moduleA/src").
	for _, r := range roots {
		prefix := r.Name + string(filepath.Separator)
		if strings.HasPrefix(p, prefix) || p == r.Name {
			rel := strings.TrimPrefix(p, prefix)
			if rel == "" {
				rel = "."
			}
			return r, rel, nil
		}
	}

	// Absolute path that falls inside a configured root.
	if filepath.IsAbs(p) {
		for _, r := range roots {
			absRoot, err := filepath.Abs(r.Path)
			if err != nil {
				continue
			}
			if absRoot == p || strings.HasPrefix(p, absRoot+string(filepath.Separator)) {
				rel, err := filepath.Rel(absRoot, p)
				if err != nil {
					continue
				}
				return r, rel, nil
			}
		}
		return domain.RootDirEntry{}, "", fmt.Errorf("%w: %q", errOutsideRoots, p)
	}

	// Default: relative to first root.
	return roots[0], p, nil
}

const maxGitStatusChars = 2000

func (a *Actor) handleInfo(actor.PureContext) (domain.ProjectInfoResp, error) {
	roots, err := a.rootsSnapshot()
	if err != nil {
		return domain.ProjectInfoResp{}, err
	}
	infoRoots := make([]domain.ProjectInfoRoot, len(roots))
	for i, r := range roots {
		infoRoots[i] = domain.ProjectInfoRoot{Name: r.Name, Path: util.NormalizePath(r.Path)}
	}
	return domain.ProjectInfoResp{Roots: infoRoots}, nil
}

func (a *Actor) handleSyncRoots(_ actor.PureContext, req domain.ProjectSyncRootsReq) error {
	if len(req.Roots) == 0 {
		return nil
	}
	roots := make([]domain.RootDirEntry, 0, len(req.Roots))
	seen := make(map[string]bool)
	for _, r := range req.Roots {
		if r.Name == "" || r.Path == "" {
			continue
		}
		key := r.Name + "\x00" + util.NormalizePath(r.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, domain.RootDirEntry{Name: r.Name, Path: util.NormalizePath(r.Path)})
	}
	return a.updateConfigCard(func(c *configCard) {
		c.Roots = roots
	})
}

func (a *Actor) handleGitStatus(ctx actor.PureContext, req domain.ProjectGitStatusReq) (domain.ProjectGitStatusResp, error) {
	root, err := a.gitRootFor(callerID(ctx), req.Repo, "project.git_status")
	if err != nil {
		return domain.ProjectGitStatusResp{}, err
	}

	if !isGitRepo(root) {
		return domain.ProjectGitStatusResp{IsGit: false}, nil
	}

	done := ctx.Done()
	// Branch, status, log and user come from the git CLI. go-git's repo.Head()
	// returns "reference not found" on linked worktrees (the worktree's HEAD
	// lives under .git/worktrees/<id>/HEAD, which PlainOpen does not resolve),
	// so the CLI path is required when the caller is bound to a worktree.
	branch := gitOutputDone(done, root, "rev-parse", "--abbrev-ref", "HEAD")
	// -z: NUL-terminated records, never quoted or escaped. Newline-mode
	// porcelain double-quotes non-ASCII paths, which used to render octal
	// escapes (e.g. CJK filenames) in the git panel and dropdown.
	porcelainZ := gitOutputRawDone(done, root, "--no-optional-locks", "status", "-z", "--porcelain")
	records := parseGitPorcelainZ(porcelainZ)
	statusStr := gitShortStatus(records)
	if statusStr == "" {
		statusStr = "(clean)"
	}
	files := make([]domain.ProjectGitFileStatus, 0, len(records))
	for _, r := range records {
		files = append(files, domain.ProjectGitFileStatus{
			Path:     r.path,
			Staging:  r.staging,
			Worktree: r.worktree,
		})
	}
	logLines := gitOutputDone(done, root, "--no-optional-locks", "log", "--oneline", "-n", "5")
	userName := gitOutputDone(done, root, "config", "user.name")

	truncatedStatus := statusStr
	if len(truncatedStatus) > maxGitStatusChars {
		truncatedStatus = truncatedStatus[:maxGitStatusChars] + "\n... (truncated)"
	}

	return domain.ProjectGitStatusResp{
		Branch:     branch,
		MainBranch: gitDefaultBranchDone(done, root),
		Status:     truncatedStatus,
		Log:        logLines,
		Files:      files,
		UserName:   userName,
		IsGit:      true,
	}, nil
}

// porcelainRecord is a parsed row from `git status -z --porcelain` output.
// The XY status occupies the first two bytes; the path starts at byte 3.
// Rename/copy records (status starts with R or C) carry the original path
// as a second bare NUL-terminated field.
type porcelainRecord struct {
	staging  string
	worktree string
	path     string
	oldPath  string
}

// parseGitPorcelainZ parses `git status -z --porcelain` output into records.
// -z produces NUL-terminated records with bare (never quoted) paths, unlike
// newline-mode porcelain which double-quotes non-ASCII paths.
func parseGitPorcelainZ(z string) []porcelainRecord {
	if z == "" {
		return nil
	}
	fields := strings.Split(strings.TrimRight(z, "\x00"), "\x00")
	var records []porcelainRecord
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		r := porcelainRecord{
			staging:  string(f[0]),
			worktree: string(f[1]),
			path:     f[3:],
		}
		if f[0] == 'R' || f[0] == 'C' {
			if i+1 < len(fields) && fields[i+1] != "" {
				r.oldPath = fields[i+1]
			}
			i++
		}
		records = append(records, r)
	}
	return records
}

// gitShortStatus renders porcelainRecords into the `git status --short`
// string format (XY path) for display in the status text field.
func gitShortStatus(records []porcelainRecord) string {
	var b strings.Builder
	for _, r := range records {
		b.WriteString(r.staging)
		b.WriteString(r.worktree)
		b.WriteByte(' ')
		if r.oldPath != "" {
			b.WriteString(r.oldPath)
			b.WriteString(" -> ")
		}
		b.WriteString(r.path)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func isGitRepo(dir string) bool {
	if !isSafeGitDir(dir) {
		return false
	}
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func gitOutput(dir string, args ...string) string {
	return gitOutputDone(nil, dir, args...)
}

// gitOutputDone is the context-aware variant; done may be nil.
func gitOutputDone(done <-chan struct{}, dir string, args ...string) string {
	if !isSafeGitDir(dir) {
		return ""
	}
	ctx, cancel := gitCtx(done)
	defer cancel()
	cmd := gitCmd(ctx, dir, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitOutputRawDone is gitOutputDone WITHOUT whitespace trimming. Porcelain -z
// records legitimately start with a space (" M <path>" is a worktree-only
// modification); TrimSpace eats that byte, shifts the XY columns and makes
// the parser report unstaged changes as staged (and clip the path's first
// byte) — observed in the round-6 external TOTP run.
func gitOutputRawDone(done <-chan struct{}, dir string, args ...string) string {
	if !isSafeGitDir(dir) {
		return ""
	}
	ctx, cancel := gitCtx(done)
	defer cancel()
	cmd := gitCmd(ctx, dir, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func isSafeGitDir(dir string) bool {
	return dir != "" && dir != "." && !strings.HasPrefix(dir, "-")
}

func gitDefaultBranch(dir string) string {
	return gitDefaultBranchDone(nil, dir)
}

func gitDefaultBranchDone(done <-chan struct{}, dir string) string {
	// Try origin/HEAD first
	if out := gitOutputDone(done, dir, "symbolic-ref", "refs/remotes/origin/HEAD"); out != "" {
		if idx := strings.LastIndex(out, "/"); idx >= 0 {
			return out[idx+1:]
		}
	}
	// Fallback to init.defaultBranch config
	if out := gitOutputDone(done, dir, "config", "init.defaultBranch"); out != "" {
		return out
	}
	return "main"
}

// safeJoin joins root and rel, then verifies the result stays within the
// selected root or any other configured root. This allows paths that escape
// one root to land in a sibling root that has already been approved. When
// confirm is true, an escaping path is allowed unconditionally because the
// caller has already been authorized by the engine.
func (a *Actor) safeJoin(root, rel string, confirm bool) (string, error) {
	joined := filepath.Join(root, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("project: resolve path: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("project: resolve root: %w", err)
	}
	if abs == absRoot || strings.HasPrefix(abs, absRoot+string(filepath.Separator)) {
		return abs, nil
	}
	if confirm {
		return abs, nil
	}
	// Path escapes the selected root; check if it falls within another
	// configured root that has already been approved.
	cfg, cfgErr := a.configSnapshot()
	if cfgErr != nil {
		return "", cfgErr
	}
	for _, r := range cfg.Roots {
		if r.Path == "" {
			continue
		}
		otherRoot, err := filepath.Abs(r.Path)
		if err != nil {
			continue
		}
		if abs == otherRoot || strings.HasPrefix(abs, otherRoot+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("project: path %q escapes root %q", rel, root)
}
