package filesystem

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/util"
)

const (
	maxChunkSize    = 512 * 1024        // 512 KB per read_chunk call
	maxBase64Size   = 16 * 1024 * 1024  // 16 MB hard cap for read_base64
	maxEditFileSize = 128 * 1024 * 1024 // 128 MB cap for edit
	maxReadLines    = 10000             // hard cap for read limit
	implicitLimit   = 2000              // implicit limit when none specified
	maxReadSizeMB   = 10                // files larger than this require pagination
	binaryCheckSize = 512               // bytes to check for NULL
	maxPreviewItems = 20                // max items in rm preview
	grepHeadLimit   = 250               // default grep head limit
	grepMaxLimit    = 1000              // hard cap for grep results
	maxFileSizeGrep = 2 * 1024 * 1024   // 2 MB per file for grep
	maxListEntries  = 5000              // hard cap for recursive list entries
)

// preprocessPattern converts POSIX character classes to Go regexp syntax and
// applies -w (word-regexp) wrapping.
func preprocessPattern(pattern string, wordRegexp bool) string {
	// 1. POSIX character classes.
	pattern = posixCharClassRe.ReplaceAllStringFunc(pattern, func(match string) string {
		m := posixCharClassRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		negated := m[1] == "^"
		class := m[2]
		replacement, ok := posixToGoClass[class]
		if !ok {
			return match
		}
		if negated {
			return `[^` + replacement + `]`
		}
		return `[` + replacement + `]`
	})
	// 2. Word-regexp wrapping.
	if wordRegexp {
		pattern = `(?:\b` + pattern + `\b)`
	}
	return pattern
}

// Actor provides direct OS filesystem access for agent tools.
type Actor struct {
	actor.Host
	mu           sync.RWMutex
	allowedRoots []string
	defaultRoot  string      // primary working directory for relative path resolution
	fileMu       fileLockMap // per-path mutex for concurrent file writes
}

// fileLockMap provides per-file-path mutual exclusion so concurrent
// PureContext handlers don't corrupt files during read-modify-write.
type fileLockMap struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (m *fileLockMap) get(path string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = make(map[string]*sync.Mutex)
	}
	l, ok := m.locks[path]
	if !ok {
		l = &sync.Mutex{}
		m.locks[path] = l
	}
	return l
}

func collectMountPaths(mounts []domain.ProjectRef) []string {
	var out []string
	for _, m := range mounts {
		if m.Path != "" {
			out = append(out, filepath.Clean(m.Path))
		}
		for _, sub := range m.Mounts {
			if sub.Path != "" {
				out = append(out, filepath.Clean(sub.Path))
			}
		}
	}
	return out
}

func (a *Actor) setAllowedRoots(roots []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowedRoots = make([]string, len(roots))
	copy(a.allowedRoots, roots)
	// defaultRoot = first non-empty root (primary working directory)
	a.defaultRoot = ""
	for _, r := range roots {
		if r != "" {
			a.defaultRoot = r
			break
		}
	}
}

func (a *Actor) getDefaultRoot() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.defaultRoot
}

// resolvePath resolves a tool path against the default root directory.
//   - Empty or "." → defaultRoot (or "." if none set)
//   - Absolute path → returned as-is
//   - MSYS-style path (/d/foo) → converted to Windows path (D:\foo) on Windows
//   - Relative path → joined with defaultRoot
func (a *Actor) resolvePath(p string) string {
	if p == "" || p == "." {
		if dr := a.getDefaultRoot(); dr != "" {
			return dr
		}
		return "."
	}
	p = fromMSYSPath(p)
	if filepath.IsAbs(p) {
		return p
	}
	if dr := a.getDefaultRoot(); dr != "" {
		return filepath.Join(dr, p)
	}
	return p
}

// fromMSYSPath converts MSYS/Git Bash style paths (/d/dev/project) to
// Windows native paths (D:\dev\project). No-op on non-Windows platforms
// or when the path doesn't match the MSYS pattern.
func fromMSYSPath(p string) string {
	// MSYS paths: /d/foo, /D/foo (single-letter drive, slash prefix)
	if len(p) < 4 || p[0] != '/' {
		return p
	}
	drive := p[1]
	if !((drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')) {
		return p
	}
	if p[2] != '/' {
		return p
	}
	return string(drive) + ":" + p[2:]
}

func (a *Actor) isPathAllowed(ctx actor.PureContext, p string) bool {
	if ctx.Identity().IsZero() {
		return true
	}
	a.mu.RLock()
	roots := a.allowedRoots
	a.mu.RUnlock()
	if len(roots) == 0 {
		return false
	}
	cp := filepath.Clean(p)
	for _, root := range roots {
		if cp == root || strings.HasPrefix(cp, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// requireRootConfirm reports whether p is outside all configured roots for an
// external caller. Internal callers bypass, and when no roots are configured
// the check is skipped so legacy/tests without a workspace still work.
func (a *Actor) requireRootConfirm(ctx actor.PureContext, p string) bool {
	if ctx.Identity().IsZero() {
		return false
	}
	a.mu.RLock()
	roots := a.allowedRoots
	a.mu.RUnlock()
	if len(roots) == 0 {
		return false
	}
	cp := filepath.Clean(p)
	for _, root := range roots {
		if cp == root || strings.HasPrefix(cp, root+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func (a *Actor) syncRootsFromWorkspace(ctx actor.Context) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := wsRef.Invoke(callCtx, "workspace.list_project", nil)
	defer call.Close()
	result, _ := call.Final(callCtx)
	if result == nil {
		return
	}
	var resp domain.ProjectRefListResp
	switch v := result.(type) {
	case []byte:
		_ = json.Unmarshal(v, &resp)
	case domain.ProjectRefListResp:
		resp = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &resp)
	}
	paths := collectMountPaths(resp.Items)
	a.setAllowedRoots(paths)
	ctx.Logger().Info("filesystem: synced roots", "count", len(paths))
}

func (a *Actor) handleSyncRoots(ctx actor.Context, req domain.FileSystemSyncRootsReq) error {
	a.setAllowedRoots(req.Roots)
	ctx.Logger().Info("filesystem: roots updated", "count", len(req.Roots))
	return nil
}

func (a *Actor) Type() string { return "filesystem" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("filesystem: starting", "id", ctx.Self().ID().String())
	// fs_ops is the dedicated stateful lane for the internal root-sync write.
	// sync_roots mutates the allowed-root table (candidate roots + default
	// root), so it cannot be stateless; isolating it on its own lane keeps the
	// owner lane free for reads while preserving single-writer semantics for
	// the root table.
	if err := ctx.RegisterLoop("fs_ops", actor.ModeStateful); err != nil {
		return fmt.Errorf("filesystem: register fs_ops loop: %w", err)
	}
	if err := ctx.Register("filesystem.list", a.handleList, actor.Public(),
		actor.WithDescription("List directory contents as plain text (one entry per line, directories end with /). Depth controls how many levels to descend: 0 or omit for current directory only, 1 for one level, 2 for two levels, -1 for full recursion. Skips node_modules, .git, vendor, dist, build. Hidden entries (names starting with '.') are omitted by default."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Directory to list. Defaults to the project's primary root directory; relative paths resolve against it."},
			actor.ParamDesc{Name: "depth", Description: "Levels to descend: 0/omit=current dir only, 1=one level, 2=two levels, -1=full recursion (capped at 5000 entries)."},
			actor.ParamDesc{Name: "exclude", Description: "Comma-separated directory names to skip in addition to defaults."},
			actor.ParamDesc{Name: "all", Description: "If true, include hidden entries whose names start with '.'. Default false."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register list: %w", err)
	}
	if err := ctx.Register("filesystem.read", a.handleRead, actor.Public(),
		actor.WithDescription("Read a text file"),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to read. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "offset", Description: "First line to read (1-indexed). Omit to start from the beginning."},
			actor.ParamDesc{Name: "limit", Description: "Maximum number of lines to read. Omit to read the entire file."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register read: %w", err)
	}
	if err := ctx.Register("filesystem.read_base64", a.handleReadBase64, actor.Public(),
		actor.WithDescription("Read a file as base64"),
		actor.WithParams(actor.ParamDesc{Name: "path", Description: "Path of the file to read as base64. Relative paths resolve against the project's primary root directory. Use for small binary files like images."}),
	); err != nil {
		return fmt.Errorf("filesystem: register read_base64: %w", err)
	}
	if err := ctx.Register("filesystem.read_chunk", a.handleReadChunk, actor.Public(),
		actor.WithDescription("Read a chunk of a file"),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to read. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "offset", Description: "Byte offset to start reading at"},
			actor.ParamDesc{Name: "length", Description: "Number of bytes to read (max 524288)"},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register read_chunk: %w", err)
	}
	if err := ctx.Register("filesystem.write", a.handleWrite, actor.Public(),
		actor.WithDescription("Create a new file. Rejects if file already exists."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to create. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "content", Description: "Content to write"},
			actor.ParamDesc{Name: "confirm", Description: "Must be true when path resolves outside allowed roots."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register write: %w", err)
	}
	if err := ctx.Register("filesystem.write_base64", a.handleWriteBase64, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Write a file from base64-encoded content, overwriting any existing file. Use for binary files (images, archives) that cannot be expressed as text."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to write. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "content", Description: "Base64-encoded file content (standard encoding)."},
			actor.ParamDesc{Name: "confirm", Description: "Must be true when path resolves outside allowed roots."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register write_base64: %w", err)
	}
	if err := ctx.Register("filesystem.edit", a.handleEdit, actor.Public(),
		actor.WithDescription("Edit an existing file by replacing text. Use as the sole modification channel."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file to edit. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "old_string", Description: "Exact text to replace. Ignored when overwrite=true."},
			actor.ParamDesc{Name: "new_string", Description: "Replacement text. When overwrite=true, this becomes the entire new file content."},
			actor.ParamDesc{Name: "replace_all", Description: "If true, replace every occurrence. Default false. Ignored when overwrite=true."},
			actor.ParamDesc{Name: "overwrite", Description: "If true, replace the entire file content with new_string. Default false."},
			actor.ParamDesc{Name: "confirm", Description: "Must be true when path resolves outside allowed roots."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register edit: %w", err)
	}
	if err := ctx.Register("filesystem.list_json", a.handleListJSON, actor.Public(),
		actor.WithDescription("List directory contents as JSON entries. Depth controls how many levels to descend: 0 or omit for current directory only, 1 for one level, 2 for two levels, -1 for full recursion. Skips node_modules, .git, vendor, dist, build. Hidden entries (names starting with '.') are omitted by default."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Directory to list. Defaults to the project's primary root directory; relative paths resolve against it."},
			actor.ParamDesc{Name: "depth", Description: "Levels to descend: 0/omit=current dir only, 1=one level, 2=two levels, -1=full recursion (capped at 5000 entries)."},
			actor.ParamDesc{Name: "exclude", Description: "Comma-separated directory names to skip in addition to defaults."},
			actor.ParamDesc{Name: "all", Description: "If true, include hidden entries whose names start with '.'. Default false."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register list_json: %w", err)
	}
	if err := ctx.Register("filesystem.roots", a.handleRoots, actor.Public(),
		actor.WithDescription("List browsable filesystem roots: the user home directory and every volume root (drive letters on Windows, \"/\" elsewhere). Used by the web/mobile file picker, which has no native folder dialog and cannot enumerate volumes client-side."),
	); err != nil {
		return fmt.Errorf("filesystem: register roots: %w", err)
	}
	if err := ctx.Register("filesystem.glob", a.handleGlob, actor.Public(),
		actor.WithDescription("Find files matching a glob pattern."),
		actor.WithParams(
			actor.ParamDesc{Name: "pattern", Description: `Glob pattern. Supports ** for recursive matching, comma-separated multiple patterns ("*.ts,*.tsx"), and brace expansion ("*.{ts,tsx}").`},
			actor.ParamDesc{Name: "path", Description: "Directory to search in. Defaults to the project's primary root directory."},
			actor.ParamDesc{Name: "type", Description: `Filter by type: "f" for files, "d" for directories.`},
			actor.ParamDesc{Name: "maxdepth", Description: "Maximum directory depth to recurse into."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register glob: %w", err)
	}
	if err := ctx.Register("filesystem.grep", a.handleGrep, actor.Public(),
		actor.WithDescription("Search files for a pattern. Default output_mode is files."),
		actor.WithParams(
			actor.ParamDesc{Name: "pattern", Description: "Regular expression pattern to search for."},
			actor.ParamDesc{Name: "path", Description: "Directory or file to search in. Defaults to the project's primary root directory."},
			actor.ParamDesc{Name: "recursive", Description: "Search recursively through subdirectories. Default true."},
			actor.ParamDesc{Name: "ignore_case", Description: "Case-insensitive matching."},
			actor.ParamDesc{Name: "output_mode", Description: `Result format: "files" (default, list of matching files), "content" (lines with matches), "count" (per-file match counts).`},
			actor.ParamDesc{Name: "glob", Description: `Glob patterns to filter files. Supports comma-separated patterns, brace expansion ("*.{ts,tsx}"), negation ("!*_test.go"), and path-aware patterns ("sub/*.go", "**/*.go"). Patterns without '/' or '**' match filenames at any depth, like grep --include.`},
			actor.ParamDesc{Name: "head_limit", Description: "Maximum number of matches to return. Default 250, hard cap 1000."},
			actor.ParamDesc{Name: "context", Description: "Lines of context to include before and after each match."},
			actor.ParamDesc{Name: "before_context", Description: "Lines of context before each match."},
			actor.ParamDesc{Name: "after_context", Description: "Lines of context after each match."},
			actor.ParamDesc{Name: "multiline", Description: "Enable multiline regexp mode (?s)."},
			actor.ParamDesc{Name: "invert_match", Description: "Invert match: return lines that do NOT match the pattern."},
			actor.ParamDesc{Name: "word_regexp", Description: "Match whole words only."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register grep: %w", err)
	}
	if err := ctx.Register("filesystem.rm", a.handleRm, actor.Public(),
		actor.WithDescription("Remove files or directories. Directories require recursive=true."),
		actor.WithParams(
			actor.ParamDesc{Name: "path", Description: "Path of the file or directory to remove. Relative paths resolve against the project's primary root directory."},
			actor.ParamDesc{Name: "recursive", Description: "Remove directories recursively. Required for directories."},
			actor.ParamDesc{Name: "force", Description: "Skip confirmation for protected paths."},
		),
	); err != nil {
		return fmt.Errorf("filesystem: register rm: %w", err)
	}
	if err := ctx.Register("filesystem.sync_roots", a.handleSyncRoots, actor.Internal(), actor.WithLoop("fs_ops")); err != nil {
		return fmt.Errorf("filesystem: register sync_roots: %w", err)
	}

	// Initial sync from workspace (workspace starts before filesystem per actorset order)
	a.syncRootsFromWorkspace(ctx)

	if err := ctx.RegisterDomain("filesystem").Expose(); err != nil {
		return fmt.Errorf("filesystem: expose service: %w", err)
	}
	return nil
}

// volumeRoots lists browsable filesystem roots: mounted drive letters on
// Windows, the single "/" root elsewhere.
func volumeRoots() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}
	var out []string
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

func (a *Actor) handleRoots(_ actor.PureContext) (domain.FileSystemRootsResp, error) {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = a.getDefaultRoot()
	}
	return domain.FileSystemRootsResp{Home: home, Roots: volumeRoots()}, nil
}

func (a *Actor) handleListJSON(ctx actor.PureContext, req domain.FileSystemListReq) (domain.FileEntryListResp, error) {
	root := filepath.Clean(a.resolvePath(req.Path))
	skipSet := buildSkipSet(req.Exclude)

	if req.Depth == 0 {
		entries, err := os.ReadDir(root)
		if err != nil {
			return domain.FileEntryListResp{}, fmt.Errorf("filesystem.list_json: %w", err)
		}
		var out []domain.FileEntry
		for _, e := range entries {
			if skipSet[e.Name()] {
				continue
			}
			if !req.All && isHiddenEntry(e.Name()) {
				continue
			}
			out = append(out, fileEntryFromDirEntry(e))
		}
		return domain.FileEntryListResp{
			Items:      out,
			NumEntries: int32(len(out)),
			Truncated:  false,
		}, nil
	}

	entries, truncated, err := listDirDepth(ctx, root, int(req.Depth), skipSet, req.All)
	if err != nil {
		return domain.FileEntryListResp{}, fmt.Errorf("filesystem.list_json: %w", err)
	}

	var out []domain.FileEntry
	for _, e := range entries {
		entry := fileEntryFromDirEntry(e.entry)
		entry.Name = e.relPath
		out = append(out, entry)
	}

	return domain.FileEntryListResp{
		Items:      out,
		NumEntries: int32(len(out)),
		Truncated:  truncated,
	}, nil
}

func (a *Actor) handleList(ctx actor.PureContext, req domain.FileSystemListReq) (string, error) {
	root := filepath.Clean(a.resolvePath(req.Path))
	skipSet := buildSkipSet(req.Exclude)

	if req.Depth == 0 {
		return listDirFlat(root, req.All)
	}

	entries, truncated, err := listDirDepth(ctx, root, int(req.Depth), skipSet, req.All)
	if err != nil {
		return "", fmt.Errorf("filesystem.list: %w", err)
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}

	var lines []string
	for _, e := range entries {
		name := e.relPath
		if e.entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}

	result := strings.Join(lines, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n[truncated: showing first %d entries]", maxListEntries)
	}
	return result, nil
}

// listDirFlat lists the immediate children of a directory (non-recursive).
// Hidden entries (names starting with ".") are omitted unless all is true.
func listDirFlat(root string, all bool) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("filesystem.list: %w", err)
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var lines []string
	for _, e := range entries {
		if !all && isHiddenEntry(e.Name()) {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	if len(lines) == 0 {
		return "(empty directory)", nil
	}
	return strings.Join(lines, "\n"), nil
}

type walkStackItem struct {
	absPath string
	depth   int
}

type dirEntryWithRelPath struct {
	entry   os.DirEntry
	relPath string
}

// listDirDepth walks the directory tree up to maxDepth levels.
// maxDepth: -1 = unlimited recursion, 0 = current dir only (caller fast path), >0 = limited levels.
// Hidden entries (names starting with ".") are omitted unless all is true.
// Returns entries in DFS alphabetical order, with relative paths from root.
func listDirDepth(ctx actor.PureContext, root string, maxDepth int, skipSet map[string]bool, all bool) ([]dirEntryWithRelPath, bool, error) {
	var out []dirEntryWithRelPath
	truncated := false
	cancelErr := fmt.Errorf("walk cancelled")

	stack := []walkStackItem{{absPath: root, depth: 0}}

	for len(stack) > 0 && !truncated {
		if len(out)%500 == 0 {
			select {
			case <-ctx.Done():
				return out, truncated, cancelErr
			default:
			}
		}

		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(current.absPath)
		if err != nil {
			continue
		}

		var subDirs []walkStackItem
		for _, e := range entries {
			if skipSet[e.Name()] {
				continue
			}
			if !all && isHiddenEntry(e.Name()) {
				continue
			}

			rel, _ := filepath.Rel(root, filepath.Join(current.absPath, e.Name()))
			rel = util.NormalizePath(rel)
			out = append(out, dirEntryWithRelPath{entry: e, relPath: rel})

			if len(out) >= maxListEntries {
				truncated = true
				break
			}

			if e.IsDir() && (maxDepth == -1 || current.depth < maxDepth) {
				subDirs = append(subDirs, walkStackItem{
					absPath: filepath.Join(current.absPath, e.Name()),
					depth:   current.depth + 1,
				})
			}
		}

		// Push subdirs in reverse order so DFS processes them alphabetically.
		for i := len(subDirs) - 1; i >= 0; i-- {
			stack = append(stack, subDirs[i])
		}
	}

	return out, truncated, nil
}

// isHiddenEntry reports whether a file or directory name is hidden (starts with ".").
func isHiddenEntry(name string) bool {
	return strings.HasPrefix(name, ".")
}

func buildSkipSet(exclude string) map[string]bool {
	set := make(map[string]bool, len(defaultSkipDirs))
	for k, v := range defaultSkipDirs {
		set[k] = v
	}
	if exclude != "" {
		for _, part := range strings.Split(exclude, ",") {
			name := strings.TrimSpace(part)
			if name != "" {
				set[name] = true
			}
		}
	}
	return set
}

func fileEntryFromDirEntry(e os.DirEntry) domain.FileEntry {
	entry := domain.FileEntry{
		Name:  e.Name(),
		IsDir: e.IsDir(),
	}
	if info, err := e.Info(); err == nil {
		entry.Size = info.Size()
		entry.ModTime = info.ModTime().Format(time.RFC3339Nano)
	}
	return entry
}

// resolvePathSearch resolves a path against the default root, but if the file
// does not exist there it falls back to searching every mounted root.  This
// makes log caller paths clickable even when the source file lives in a
// different mounted project (e.g. gospore files under a separate mount).
func (a *Actor) resolvePathSearch(p string) string {
	p = fromMSYSPath(p)
	if filepath.IsAbs(p) {
		return p
	}

	// Fast path: default root.
	if dr := a.getDefaultRoot(); dr != "" {
		candidate := filepath.Join(dr, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Fallback: scan all mounted roots.
	a.mu.RLock()
	roots := a.allowedRoots
	a.mu.RUnlock()
	for _, root := range roots {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Nothing found — return the default-root join so the caller gets a
	// clean "not found" error from the default root.
	if dr := a.getDefaultRoot(); dr != "" {
		return filepath.Join(dr, p)
	}
	return p
}

func (a *Actor) handleRead(ctx actor.PureContext, req domain.FileSystemReadReq) (domain.FileSystemReadResp, error) {
	p := filepath.Clean(a.resolvePathSearch(req.Path))

	// Binary detection.
	if isBinaryFile(p) {
		return domain.FileSystemReadResp{}, fmt.Errorf("filesystem.read: binary file, cannot display as text")
	}

	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemReadResp{}, fmt.Errorf("[warning] file not found: %s", req.Path)
		}
		return domain.FileSystemReadResp{}, fmt.Errorf("filesystem.read: %w", err)
	}

	// Large file guard: >10MB with no pagination → reject.
	if fi.Size() > maxReadSizeMB*1024*1024 && req.Offset <= 0 && req.Limit <= 0 && req.Tail <= 0 {
		totalLines := countFileLines(p)
		return domain.FileSystemReadResp{}, fmt.Errorf("filesystem.read: file size %d MB exceeds %d MB limit; use offset+limit or tail (estimated %d lines)", fi.Size()/(1024*1024), maxReadSizeMB, totalLines)
	}

	// Read full file for processing.
	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemReadResp{}, fmt.Errorf("filesystem.read: %w", err)
	}

	// CRLF → LF normalization.
	content := normalizeLineEndings(string(data))
	allLines := strings.Split(content, "\n")
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1] // trailing newline
	}
	totalLines := int32(len(allLines))

	// Apply Offset/Limit.
	startLine := int(req.Offset)
	if startLine <= 0 {
		startLine = 1
	}

	// Apply Tail if specified (overrides Offset). Keep allLines intact and set
	// startLine to the tail window's 1-based line in original coordinates so the
	// read loop iterates the original slice with consistent indexing. (Slicing
	// allLines here previously left startLine pointing past the shortened slice,
	// so a tail read returned zero lines.)
	if req.Tail > 0 {
		startLine = len(allLines) - int(req.Tail) + 1
		if startLine < 1 {
			startLine = 1
		}
	}
	if startLine > len(allLines) {
		startLine = len(allLines) + 1
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = implicitLimit
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}

	var lines []string
	truncated := false
	for i := startLine - 1; i < len(allLines); i++ {
		if len(lines) >= limit {
			// Hit the line cap with at least allLines[i] still unread, so the
			// caller can tell the file window was capped (more content exists).
			truncated = true
			break
		}
		lines = append(lines, allLines[i])
	}

	// Apply Filter if specified.
	if req.Filter != "" {
		re, err := regexp.Compile(req.Filter)
		if err != nil {
			return domain.FileSystemReadResp{}, fmt.Errorf("filesystem.read: invalid filter regex: %w", err)
		}
		var filtered []string
		for _, line := range lines {
			if re.MatchString(line) {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
	}

	content = strings.Join(lines, "\n")
	// Ensure trailing newline for non-empty content to make line-based
	// processing consistent. The converter strips it back if needed.
	if len(lines) > 0 {
		content += "\n"
	}

	return domain.FileSystemReadResp{
		Content:    content,
		TotalLines: totalLines,
		StartLine:  int32(startLine),
		NumLines:   int32(len(lines)),
		Truncated:  truncated,
	}, nil
}

func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func isBinaryFile(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, binaryCheckSize)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func countFileLines(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}

func (a *Actor) handleReadBase64(ctx actor.PureContext, req domain.FileSystemReadBase64Req) (domain.FileSystemReadBase64Resp, error) {
	p := filepath.Clean(a.resolvePathSearch(req.Path))
	fi, err := os.Stat(p)
	if err != nil {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("filesystem.read_base64: %w", err)
	}
	if fi.Size() > maxBase64Size {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("filesystem.read_base64: file size %d exceeds %d byte cap; use filesystem.read_chunk", fi.Size(), maxBase64Size)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("filesystem.read_base64: %w", err)
	}
	return domain.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
}

func (a *Actor) handleWriteBase64(ctx actor.PureContext, req domain.FileSystemWriteBase64Req) (domain.FileSystemWriteResp, error) {
	if req.Path == "" {
		return domain.FileSystemWriteResp{}, fmt.Errorf("filesystem.write_base64: path is required")
	}
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		return domain.FileSystemWriteResp{}, fmt.Errorf("filesystem.write_base64: invalid base64 content: %w", err)
	}
	p := filepath.Clean(a.resolvePathSearch(req.Path))
	if a.requireRootConfirm(ctx, p) && !req.Confirm {
		return domain.FileSystemWriteResp{}, fmt.Errorf("filesystem.write_base64: path %q is outside allowed roots; set confirm=true to proceed", req.Path)
	}
	fl := a.fileMu.get(p)
	fl.Lock()
	defer fl.Unlock()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return domain.FileSystemWriteResp{}, fmt.Errorf("filesystem.write_base64: mkdir: %w", err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return domain.FileSystemWriteResp{}, fmt.Errorf("filesystem.write_base64: %w", err)
	}
	return domain.FileSystemWriteResp{}, nil
}

func (a *Actor) handleReadChunk(ctx actor.PureContext, req domain.FileSystemReadChunkReq) (domain.FileSystemReadChunkResp, error) {
	if req.Length <= 0 {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("filesystem.read_chunk: length must be positive, got %d", req.Length)
	}
	if req.Offset < 0 {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("filesystem.read_chunk: offset must be non-negative, got %d", req.Offset)
	}
	if req.Length > maxChunkSize {
		req.Length = maxChunkSize
	}

	f, err := os.Open(filepath.Clean(a.resolvePathSearch(req.Path)))
	if err != nil {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("filesystem.read_chunk: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("filesystem.read_chunk: stat: %w", err)
	}
	total := fi.Size()

	if req.Offset >= total {
		return domain.FileSystemReadChunkResp{Offset: req.Offset, Length: 0, Total: total}, nil
	}

	remaining := total - req.Offset
	if int64(req.Length) > remaining {
		req.Length = int32(remaining)
	}

	buf := make([]byte, req.Length)
	n, err := f.ReadAt(buf, req.Offset)
	if err != nil && err != io.EOF {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("filesystem.read_chunk: read: %w", err)
	}

	return domain.FileSystemReadChunkResp{
		Offset: req.Offset,
		Length: int32(n),
		Total:  total,
		Data:   base64.StdEncoding.EncodeToString(buf[:n]),
	}, nil
}

func (a *Actor) handleWrite(ctx actor.PureContext, req domain.FileSystemWriteReq) error {
	if req.Path == "" {
		return fmt.Errorf("filesystem.write: path is required")
	}
	p := filepath.Clean(a.resolvePath(req.Path))
	if a.requireRootConfirm(ctx, p) && !req.Confirm {
		return fmt.Errorf("filesystem.write: path %q is outside allowed roots; set confirm=true to proceed", req.Path)
	}
	fl := a.fileMu.get(p)
	fl.Lock()
	defer fl.Unlock()
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("filesystem.write: file exists: %s, use filesystem.edit to modify existing files", req.Path)
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("filesystem.write: mkdir: %w", err)
	}
	content := normalizeLineEndings(req.Content)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return fmt.Errorf("filesystem.write: %w", err)
	}
	return nil
}

func (a *Actor) handleEdit(ctx actor.PureContext, req domain.FileSystemEditReq) (domain.FileSystemEditResp, error) {
	if req.Path == "" {
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: path is required")
	}
	p := filepath.Clean(a.resolvePath(req.Path))
	if a.requireRootConfirm(ctx, p) && !req.Confirm {
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: path %q is outside allowed roots; set confirm=true to proceed", req.Path)
	}
	fl := a.fileMu.get(p)
	fl.Lock()
	defer fl.Unlock()

	// write is the only file creation channel; edit only modifies.
	oldString := normalizeLineEndings(req.OldString)
	newString := normalizeLineEndings(req.NewString)
	if !req.Overwrite {
		if oldString == "" {
			return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: old_string is empty; use filesystem.write to create new files or set overwrite=true to replace the whole file")
		}
		if oldString == newString {
			return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: old_string and new_string must differ")
		}
	}

	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: file not found: %s, use filesystem.write to create new files", req.Path)
		}
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: %w", err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: %w", err)
	}

	if len(data) > maxEditFileSize {
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: file size %d exceeds %d MB limit; use filesystem.write for large files", len(data), maxEditFileSize/(1024*1024))
	}

	content := normalizeLineEndings(string(data))

	var newContent string
	var count int
	if req.Overwrite {
		newContent = newString
		count = 1
	} else {
		// Quote-normalized search.
		actualOld, found := findActualString(content, oldString)
		if !found {
			ctx := getMismatchContext(content, oldString)
			return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: old_string not found in %s\n%s", req.Path, ctx)
		}

		count = strings.Count(content, actualOld)
		if count == 0 {
			ctx := getMismatchContext(content, oldString)
			return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: old_string not found in %s\n%s", req.Path, ctx)
		}

		if !req.ReplaceAll && count > 1 {
			return fmtMismatchMultiple(req.Path, count, content, actualOld)
		}

		// If old_string needed C-unescape to match, new_string likely has the
		// same double-escaping; unescape it too.
		if actualOld != oldString && actualOld != normalizeQuotes(oldString) {
			if unescaped, changed := unescapeC(newString); changed {
				newString = unescaped
			}
		}

		if req.ReplaceAll {
			newContent = strings.ReplaceAll(content, actualOld, newString)
		} else {
			newContent = strings.Replace(content, actualOld, newString, 1)
		}
	}

	hunks, additions, deletions := buildHunks(content, newContent)

	if err := os.WriteFile(p, []byte(newContent), 0644); err != nil {
		return domain.FileSystemEditResp{}, fmt.Errorf("filesystem.edit: write: %w", err)
	}

	return domain.FileSystemEditResp{
		Replacements: int32(count),
		Additions:    int32(additions),
		Deletions:    int32(deletions),
		Hunks:        hunks,
	}, nil
}

func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, "“", "\"")
	s = strings.ReplaceAll(s, "”", "\"")
	s = strings.ReplaceAll(s, "‘", "'")
	s = strings.ReplaceAll(s, "’", "'")
	return s
}

func findActualString(content, oldString string) (string, bool) {
	if strings.Contains(content, oldString) {
		return oldString, true
	}
	normalized := normalizeQuotes(oldString)
	if normalized != oldString && strings.Contains(content, normalized) {
		return normalized, true
	}
	// Line-ending normalization: if the file uses CRLF but the old_string
	// was produced by Read (which normalizes to LF), try swapping the
	// line endings in old_string.
	if strings.Contains(content, "\r\n") && !strings.Contains(oldString, "\r\n") {
		crlf := strings.ReplaceAll(oldString, "\n", "\r\n")
		if strings.Contains(content, crlf) {
			return crlf, true
		}
	}
	if !strings.Contains(content, "\r\n") && strings.Contains(oldString, "\r\n") {
		lf := strings.ReplaceAll(oldString, "\r\n", "\n")
		if strings.Contains(content, lf) {
			return lf, true
		}
	}
	// C-escape fallback: LLMs may double-escape JSON strings, producing
	// literal \n \t \" \\ in old_string instead of real characters.
	if unescaped, changed := unescapeC(oldString); changed {
		unescaped = normalizeLineEndings(unescaped)
		if strings.Contains(content, unescaped) {
			return unescaped, true
		}
	}
	// Whitespace-tolerant fallback: tabs vs spaces. Try the raw search and
	// the unescaped variant.
	candidates := []string{oldString}
	if u, changed := unescapeC(oldString); changed {
		candidates = append(candidates, normalizeLineEndings(u))
	}
	for _, c := range candidates {
		if actual, ok := findWhitespaceTolerant(content, c); ok {
			return actual, true
		}
	}
	return "", false
}

// findWhitespaceTolerant searches for search inside content, treating runs of
// horizontal whitespace (spaces and tabs) as interchangeable. When a match is
// found it returns the exact substring from content (preserving the original
// whitespace) so that subsequent replacement operates on literal text.
func findWhitespaceTolerant(content, search string) (string, bool) {
	normContent, idxMap := collapseWhitespaceWithMap(content)
	normSearch := collapseWhitespace(search)
	if normSearch == "" {
		return "", false
	}
	idx := strings.Index(normContent, normSearch)
	if idx < 0 {
		return "", false
	}
	startOrig := idxMap[idx]
	endNorm := idx + len(normSearch)
	var endOrig int
	if endNorm < len(idxMap) {
		endOrig = idxMap[endNorm]
	} else {
		endOrig = len(content)
	}
	return content[startOrig:endOrig], true
}

// collapseWhitespaceWithMap replaces every run of spaces/tabs with a single
// space and returns a map from normalized byte index to original byte offset.
func collapseWhitespaceWithMap(s string) (string, []int) {
	var out strings.Builder
	var idxMap []int
	i := 0
	for i < len(s) {
		if s[i] == ' ' || s[i] == '\t' {
			out.WriteByte(' ')
			idxMap = append(idxMap, i)
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
		} else {
			out.WriteByte(s[i])
			idxMap = append(idxMap, i)
			i++
		}
	}
	return out.String(), idxMap
}

func collapseWhitespace(s string) string {
	out, _ := collapseWhitespaceWithMap(s)
	return out
}

// unescapeC converts C-style escape sequences (\n \t \r \" \\) to their
// actual character values. Returns the unescaped string and whether any
// change was made. Safe to call even when no escaping is present.
func unescapeC(s string) (string, bool) {
	if !strings.ContainsAny(s, "\\") {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				changed = true
				i++
				continue
			case 't':
				b.WriteByte('\t')
				changed = true
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				changed = true
				i++
				continue
			case '"':
				b.WriteByte('"')
				changed = true
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				changed = true
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	if !changed {
		return s, false
	}
	return b.String(), true
}

func hunksLinesFromStrings(lines []string) []string {
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

func buildHunks(oldContent, newContent string) ([]domain.FileSystemEditHunk, int, int) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var hunks []domain.FileSystemEditHunk
	additions, deletions := 0, 0

	o, n := 0, 0
	for o < len(oldLines) || n < len(newLines) {
		for o < len(oldLines) && n < len(newLines) && oldLines[o] == newLines[n] {
			o++
			n++
		}
		if o >= len(oldLines) && n >= len(newLines) {
			break
		}

		diffStartOld, diffStartNew := o, n

		// Greedy resync.
		syncFound := false
		const maxLook = 100
		for look := 1; look <= maxLook; look++ {
			for oOff := 0; oOff <= look; oOff++ {
				nOff := look - oOff
				if o+oOff < len(oldLines) && n+nOff < len(newLines) && oldLines[o+oOff] == newLines[n+nOff] {
					o += oOff
					n += nOff
					syncFound = true
					break
				}
			}
			if syncFound {
				break
			}
		}
		if !syncFound {
			o = len(oldLines)
			n = len(newLines)
		}

		oldCount := o - diffStartOld
		newCount := n - diffStartNew

		// Context lines.
		ctxBefore := 3
		ctxStartOld := diffStartOld - ctxBefore
		if ctxStartOld < 0 {
			ctxStartOld = 0
		}
		ctxAfter := 3
		ctxEndOld := o + ctxAfter
		if ctxEndOld > len(oldLines) {
			ctxEndOld = len(oldLines)
		}

		var lines []string
		for i := ctxStartOld; i < diffStartOld && i < len(oldLines); i++ {
			lines = append(lines, " "+oldLines[i])
		}
		for i := diffStartOld; i < o && i < len(oldLines); i++ {
			lines = append(lines, "-"+oldLines[i])
			deletions++
		}
		for i := diffStartNew; i < n && i < len(newLines); i++ {
			lines = append(lines, "+"+newLines[i])
			additions++
		}
		for i := o; i < ctxEndOld && i < len(oldLines); i++ {
			lines = append(lines, " "+oldLines[i])
		}

		hunkOldStart := ctxStartOld + 1
		hunkNewStart := diffStartNew - (diffStartOld - ctxStartOld) + 1
		if diffStartNew < diffStartOld-ctxStartOld {
			hunkNewStart = 1
		}

		hunks = append(hunks, domain.FileSystemEditHunk{
			OldStart: int32(hunkOldStart),
			OldLines: int32(oldCount),
			NewStart: int32(hunkNewStart),
			NewLines: int32(newCount),
			Lines:    hunksLinesFromStrings(lines),
		})
	}

	return hunks, additions, deletions
}

func (a *Actor) handleGlob(ctx actor.PureContext, req domain.FileSystemGlobReq) (domain.FileSystemGlobResp, error) {
	pattern := req.Pattern
	if pattern == "" {
		return domain.FileSystemGlobResp{}, fmt.Errorf("filesystem.glob: pattern is required")
	}

	rootPath := req.Path
	if rootPath == "" {
		if p, pat := splitPatternPath(pattern); p != "" {
			rootPath = p
			pattern = pat
		}
	}
	root := filepath.Clean(a.resolvePath(rootPath))

	allMatches, err := doublestarGlob(root, pattern, int(req.Maxdepth), "", req.Exclude)
	if err != nil {
		return domain.FileSystemGlobResp{}, fmt.Errorf("filesystem.glob: %w", err)
	}

	// Sort by modification time (newest first). Pre-compute ModTime per
	// match to avoid O(n log n) os.Stat calls inside the sort comparator.
	type mt struct{ path string; t time.Time; ok bool }
	entries := make([]mt, len(allMatches))
	for i, m := range allMatches {
		if fi, err := os.Stat(filepath.Join(root, m)); err == nil {
			entries[i] = mt{path: m, t: fi.ModTime(), ok: true}
		} else {
			entries[i] = mt{path: m}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ok && entries[j].ok {
			return entries[i].t.After(entries[j].t)
		}
		return entries[i].path < entries[j].path
	})
	for i, e := range entries {
		allMatches[i] = e.path
	}

	return domain.FileSystemGlobResp{
		Files:     allMatches,
		NumFiles:  int32(len(allMatches)),
		Truncated: false,
	}, nil
}

// handleGrep is a stateless (PureContext) read: it walks the filesystem and
// only emits a fire-and-forget oracle diagnostic on an empty result (a
// cross-actor Invoke, which PureContext permits). It never mutates actor
// state, so it runs on the forked pure loop.
func (a *Actor) handleGrep(ctx actor.PureContext, req domain.FileSystemGrepReq) (domain.FileSystemGrepResp, error) {
	pattern := req.Pattern
	if pattern == "" {
		return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: pattern is required")
	}

	rootPath := req.Path
	glob := req.Glob
	if rootPath == "" && glob != "" {
		if p, pat := splitPatternPath(glob); p != "" {
			rootPath = p
			glob = pat
		}
	}
	root := filepath.Clean(a.resolvePath(rootPath))

	// Output-mode: explicit > default "files".
	outputMode := req.OutputMode
	if outputMode == "" {
		outputMode = "files"
	}

	// Build regex.
	reFlags := ""
	if req.IgnoreCase {
		reFlags += "(?i)"
	}
	if req.Multiline {
		reFlags += "(?s)"
	}
	pattern = preprocessPattern(pattern, req.WordRegexp)
	re, err := regexp.Compile(reFlags + pattern)
	if err != nil {
		return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: invalid pattern: %w", err)
	}

	headLimit := int(req.HeadLimit)
	if headLimit <= 0 {
		headLimit = grepHeadLimit
	}
	if headLimit > grepMaxLimit {
		headLimit = grepMaxLimit
	}

	// Build include glob matchers against relative paths from root.
	var matchers []globMatcher
	if glob != "" {
		matchers, err = compileGlobPatterns(glob)
		if err != nil {
			return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: %w", err)
		}
	}

	skipDirs := map[string]bool{
		"node_modules": true,
		".git":         true,
		".svn":         true,
		"__pycache__":  true,
		".hg":          true,
		"vendor":       true,
	}

	var matches []domain.FileSystemGrepMatch
	fileCounts := map[string]int{}
	truncated := false
	scannedCount := 0

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if truncated {
			return fs.SkipAll
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = util.NormalizePath(relPath)
		baseName := filepath.Base(path)

		if len(matchers) > 0 && !matchGrepGlob(matchers, relPath, baseName) {
			return nil
		}
		scannedCount++
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSizeGrep {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := normalizeLineEndings(scanner.Text())
			isMatch := re.MatchString(line)
			if req.InvertMatch {
				isMatch = !isMatch
			}
			if isMatch {
				matches = append(matches, domain.FileSystemGrepMatch{
					File:    relPath,
					Line:    int32(lineNum),
					Content: line,
				})
				fileCounts[relPath]++
				if len(matches) >= headLimit {
					truncated = true
					return nil
				}
			}
		}
		return nil
	}

	// If root is a single file, search it directly regardless of Recursive.
	rootInfo, err := os.Stat(root)
	if err != nil {
		return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: %w", err)
	}
	if !rootInfo.IsDir() {
		entry, err := fs.Stat(os.DirFS(filepath.Dir(root)), filepath.Base(root))
		if err != nil {
			return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: %w", err)
		}
		_ = walkFn(root, fs.FileInfoToDirEntry(entry), nil)
	} else {
		err = filepath.WalkDir(root, walkFn)
	}
	if err != nil {
		return domain.FileSystemGrepResp{}, fmt.Errorf("filesystem.grep: %w", err)
	}

	if req.Glob != "" && scannedCount == 0 {
		return domain.FileSystemGrepResp{}, fmt.Errorf("[warning] no files matched glob %q", req.Glob)
	}

	// Report empty results as a diagnostic so they surface in the Problems panel.
	if len(matches) == 0 && !req.InvertMatch {
		a.reportEmptyGrep(ctx, req.Pattern, root)
	}

	// Apply context lines (before_context/after_context/context).
	ctxBefore := int(req.BeforeContext)
	ctxAfter := int(req.AfterContext)
	if req.Context > 0 {
		ctxBefore = int(req.Context)
		ctxAfter = int(req.Context)
	}

	switch outputMode {
	case "files":
		files := make([]string, 0, len(fileCounts))
		for f := range fileCounts {
			files = append(files, f)
		}
		sort.Strings(files)
		return domain.FileSystemGrepResp{
			Files:      files,
			NumMatches: int32(len(matches)),
			Truncated:  truncated,
			OutputMode: outputMode,
		}, nil
	case "count":
		counts := make([]domain.FileSystemGrepCount, 0, len(fileCounts))
		for f, c := range fileCounts {
			counts = append(counts, domain.FileSystemGrepCount{File: f, Count: int32(c)})
		}
		return domain.FileSystemGrepResp{
			Counts:     counts,
			NumMatches: int32(len(matches)),
			Truncated:  truncated,
			OutputMode: outputMode,
		}, nil
	default:
		if ctxBefore > 0 || ctxAfter > 0 {
			matches = applyContextBA(matches, root, ctxBefore, ctxAfter)
		}
		return domain.FileSystemGrepResp{
			Matches:    matches,
			NumMatches: int32(len(matches)),
			Truncated:  truncated,
			OutputMode: outputMode,
		}, nil
	}
}

// reportEmptyGrep emits an info-level diagnostic to the oracle service when a
// grep returns no matches, making empty results visible in the Problems panel.
func (a *Actor) reportEmptyGrep(ctx actor.PureContext, pattern, root string) {
	oracleRef, ok := ctx.LookupService("oracle")
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := oracleRef.Invoke(callCtx, "oracle.report_diagnostic", domain.OracleReportDiagnosticReq{
		Severity:   "info",
		Source:     "filesystem",
		Message:    fmt.Sprintf(`grep %q in %s: no matches`, pattern, root),
		CallableID: "filesystem.grep",
	})
	call.Close()
}

func (a *Actor) handleRm(ctx actor.PureContext, req domain.FileSystemRmReq) (domain.FileSystemRmResp, error) {
	p := req.Path
	if p == "" {
		return domain.FileSystemRmResp{}, fmt.Errorf("filesystem.rm: path is required")
	}
	p = filepath.Clean(a.resolvePath(p))
	if a.requireRootConfirm(ctx, p) && !req.Confirm {
		return domain.FileSystemRmResp{}, fmt.Errorf("filesystem.rm: path %q is outside allowed roots; set confirm=true to proceed", req.Path)
	}
	fl := a.fileMu.get(p)
	fl.Lock()
	defer fl.Unlock()

	// Whitelist check.
	if err := rmWhitelistCheck(p); err != nil {
		return domain.FileSystemRmResp{}, err
	}

	// Check if exists.
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemRmResp{}, fmt.Errorf("rm: cannot remove '%s': No such file or directory", p)
		}
		return domain.FileSystemRmResp{}, fmt.Errorf("filesystem.rm: %w", err)
	}

	// Directory: requires recursive=true.
	if fi.IsDir() && !req.Recursive {
		return domain.FileSystemRmResp{}, fmt.Errorf("rm: cannot remove '%s': Is a directory (use recursive=true)", p)
	}

	// Build preview / count.
	var targets []string
	var count int32
	if fi.IsDir() {
		targets, count, err = collectRmTargets(p, true)
		if err != nil {
			return domain.FileSystemRmResp{}, fmt.Errorf("filesystem.rm: %w", err)
		}
	} else {
		targets = []string{filepath.Base(p)}
		count = 1
	}

	// Preview mode unless confirmed or forced.
	if !req.Confirm && !req.Force {
		return domain.FileSystemRmResp{
			Removed: targets,
			Count:   count,
			Preview: true,
		}, nil
	}

	// Confirmed: perform deletion.
	if err := os.RemoveAll(p); err != nil {
		return domain.FileSystemRmResp{}, fmt.Errorf("filesystem.rm: %w", err)
	}
	return domain.FileSystemRmResp{
		Removed: []string{p},
		Count:   count,
		Preview: false,
	}, nil
}

// applyContextBA adds before/after context lines to grep matches.
func applyContextBA(matches []domain.FileSystemGrepMatch, root string, before, after int) []domain.FileSystemGrepMatch {
	if len(matches) == 0 {
		return matches
	}
	var out []domain.FileSystemGrepMatch
	for _, m := range matches {
		start := int(m.Line) - before
		if start < 1 {
			start = 1
		}
		end := int(m.Line) + after

		p := filepath.Join(root, m.File)
		f, err := os.Open(p)
		if err != nil {
			out = append(out, m)
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum >= start && lineNum <= end {
				out = append(out, domain.FileSystemGrepMatch{
					File:    m.File,
					Line:    int32(lineNum),
					Content: scanner.Text(),
				})
			}
			if lineNum > end {
				break
			}
		}
		f.Close()
	}
	return out
}

// doublestarGlob supports ** patterns using filepath.WalkDir.
func doublestarGlob(root, pattern string, maxDepth int, typeFilter, exclude string) ([]string, error) {
	matchers, err := compileGlobPatterns(pattern)
	if err != nil {
		return nil, err
	}
	if len(matchers) == 0 {
		return nil, nil
	}

	var excludeMatchers []globMatcher
	if exclude != "" {
		excludeMatchers, err = compileGlobPatterns(exclude)
		if err != nil {
			return nil, fmt.Errorf("invalid exclude pattern %q: %w", exclude, err)
		}
	}
	isExcluded := func(relPath string) bool {
		for _, m := range excludeMatchers {
			if m.re.MatchString(relPath) {
				return true
			}
		}
		return false
	}

	// Fast path: one positive pattern without **.
	if len(matchers) == 1 && !matchers[0].negate && !strings.Contains(matchers[0].raw, "**") {
		abs, err := filepath.Glob(filepath.Join(root, matchers[0].raw))
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(abs))
		for _, p := range abs {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				continue
			}
			rel = util.NormalizePath(rel)
			if isExcluded(rel) {
				continue
			}
			out = append(out, rel)
		}
		return filterByType(root, out, typeFilter), nil
	}

	// General path: walk the tree and match against relative paths.
	// Directories matching the exclude patterns are skipped entirely
	// (fs.SkipDir) so subtrees like node_modules are never descended into.
	var matches []string
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = util.NormalizePath(rel)
		if d.IsDir() {
			if isExcluded(rel) {
				return fs.SkipDir
			}
			// Depth check.
			if maxDepth > 0 {
				depth := strings.Count(path[len(root)+1:], string(filepath.Separator))
				if depth >= maxDepth {
					return fs.SkipDir
				}
			}
			return nil
		}
		// Depth check for files.
		if maxDepth > 0 {
			depth := strings.Count(path[len(root)+1:], string(filepath.Separator))
			if depth >= maxDepth {
				return nil
			}
		}
		if !seen[rel] && matchGlob(matchers, rel) && !isExcluded(rel) {
			seen[rel] = true
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return filterByType(root, matches, typeFilter), nil
}

// globMatcher is a compiled glob pattern with optional negation semantics.
type globMatcher struct {
	re     *regexp.Regexp
	negate bool
	raw    string
}

// compileGlobPatterns compiles a comma-separated list of glob patterns.
// Each pattern may contain bash-style brace expansion and a leading '!'
// for negation. Returns matchers in order; a matching negation pattern
// excludes the path regardless of earlier positive matches.
func compileGlobPatterns(raw string) ([]globMatcher, error) {
	var matchers []globMatcher
	for _, p := range splitAlternatives(raw) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		expanded, err := expandBraces(p)
		if err != nil {
			return nil, err
		}
		for _, e := range expanded {
			negate := false
			if strings.HasPrefix(e, "!") {
				negate = true
				e = e[1:]
			}
			re, err := regexp.Compile(globToRegex(e))
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %q: %w", e, err)
			}
			matchers = append(matchers, globMatcher{re: re, negate: negate, raw: e})
		}
	}
	return matchers, nil
}

// matchGlob reports whether relPath matches the compiled pattern list.
// Any matching negation pattern excludes the path; otherwise any matching
// positive pattern includes it.
func matchGlob(matchers []globMatcher, relPath string) bool {
	for _, m := range matchers {
		if m.re.MatchString(relPath) && m.negate {
			return false
		}
	}
	for _, m := range matchers {
		if !m.negate && m.re.MatchString(relPath) {
			return true
		}
	}
	return false
}

// matchGrepGlob reports whether a file should be included in a grep search.
// Patterns without '/' or '**' are matched against the filename only (like
// grep's --include), so "*.go" matches foo.go at any depth. Patterns that
// contain '/' or '**' are matched against the full relative path from root.
// Any matching negation pattern excludes the file.
func matchGrepGlob(matchers []globMatcher, relPath, baseName string) bool {
	targetFor := func(m globMatcher) string {
		if strings.Contains(m.raw, "/") || strings.Contains(m.raw, "**") {
			return relPath
		}
		return baseName
	}
	for _, m := range matchers {
		if m.re.MatchString(targetFor(m)) && m.negate {
			return false
		}
	}
	for _, m := range matchers {
		if !m.negate && m.re.MatchString(targetFor(m)) {
			return true
		}
	}
	return false
}

// expandBraces expands bash-style brace patterns like "*.{ts,tsx}" into
// ["*.ts", "*.tsx"]. Supports nested braces and respects backslash escapes.
// Escaped braces/commas are unescaped in the returned patterns.
func expandBraces(pattern string) ([]string, error) {
	start := indexOfUnescaped(pattern, '{')
	if start == -1 {
		return []string{unescapeGlobChars(pattern)}, nil
	}
	end := matchingBrace(pattern, start)
	if end == -1 {
		return nil, fmt.Errorf("unmatched '{' in glob pattern %q", pattern)
	}
	prefix := unescapeGlobChars(pattern[:start])
	suffix := unescapeGlobChars(pattern[end+1:])
	alternatives := splitAlternatives(pattern[start+1 : end])
	var results []string
	for _, opt := range alternatives {
		expanded, err := expandBraces(opt)
		if err != nil {
			return nil, err
		}
		for _, e := range expanded {
			nested, err := expandBraces(prefix + e + suffix)
			if err != nil {
				return nil, err
			}
			results = append(results, nested...)
		}
	}
	return results, nil
}

// indexOfUnescaped returns the index of the first unescaped ch in s, or -1.
func indexOfUnescaped(s string, ch byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == ch {
			return i
		}
	}
	return -1
}

// matchingBrace finds the matching unescaped '}' for the '{' at start.
func matchingBrace(s string, start int) int {
	depth := 1
	for i := start + 1; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitAlternatives splits brace alternatives, respecting nested braces and escapes.
func splitAlternatives(s string) []string {
	var parts []string
	start := 0
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
		} else if s[i] == ',' && depth == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// unescapeGlobChars removes glob backslash escapes that were used to suppress
// brace/comma expansion, leaving the literal character.
func unescapeGlobChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '{', '}', ',', '\\':
				b.WriteByte(s[i+1])
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// globToRegex converts a glob pattern with ** to a regex.
func globToRegex(pattern string) string {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// ** matches any number of directories.
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					sb.WriteString("(?:.*/)?")
					i += 2
				} else {
					sb.WriteString(".*")
					i++
				}
			} else {
				sb.WriteString("[^/]*")
			}
		case '?':
			sb.WriteString("[^/]")
		case '.':
			sb.WriteString(`\.`) // escape dots so "*.go" matches literal .go
		case '/':
			sb.WriteString(`/`)
		default:
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
				sb.WriteByte(c)
			} else {
				sb.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
	}
	sb.WriteString("$")
	return sb.String()
}

func filterByType(root string, paths []string, typeFilter string) []string {
	if typeFilter == "" {
		return paths
	}
	var out []string
	for _, p := range paths {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			continue
		}
		switch typeFilter {
		case "f":
			if !fi.IsDir() {
				out = append(out, p)
			}
		case "d":
			if fi.IsDir() {
				out = append(out, p)
			}
		default:
			out = append(out, p)
		}
	}
	return out
}

// splitPatternPath extracts a leading directory path from a glob pattern when
// the path component is empty. If the pattern contains a '/' and the part
// before the last '/' has no glob metacharacters, that part is returned as the
// path and the remainder as the pattern. Otherwise both are returned unchanged.
func splitPatternPath(pattern string) (path, pat string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", ""
	}
	// If pattern is wrapped in parentheses, unwrap and treat as a bare path if no glob meta.
	if strings.HasPrefix(pattern, "(") && strings.HasSuffix(pattern, ")") {
		inner := pattern[1 : len(pattern)-1]
		if !hasGlobMeta(inner) {
			return inner, ""
		}
		pattern = inner
	}
	lastSlash := strings.LastIndex(pattern, "/")
	if lastSlash <= 0 {
		return "", pattern
	}
	prefix := pattern[:lastSlash]
	if hasGlobMeta(prefix) {
		return "", pattern
	}
	return prefix, pattern[lastSlash+1:]
}

func hasGlobMeta(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

// getMismatchContext returns surrounding lines when old_string doesn't match.
func getMismatchContext(content, oldString string) string {
	lines := strings.Split(content, "\n")
	// Find first line that contains any part of oldString.
	firstLine := -1
	for i, line := range lines {
		if strings.Contains(oldString, line) && len(line) > 3 {
			firstLine = i
			break
		}
	}
	if firstLine < 0 {
		// No overlap — just show first 10 lines.
		firstLine = 0
	}
	start := firstLine - 5
	if start < 0 {
		start = 0
	}
	end := firstLine + 5
	if end > len(lines) {
		end = len(lines)
	}
	var sb strings.Builder
	sb.WriteString("Context around expected location:\n")
	for i := start; i < end && i < len(lines); i++ {
		fmt.Fprintf(&sb, "line %d│%s\n", i+1, lines[i])
	}
	return sb.String()
}

// fmtMismatchMultiple returns an error listing all match positions.
func fmtMismatchMultiple(path string, count int, content, actualOld string) (domain.FileSystemEditResp, error) {
	lines := strings.Split(content, "\n")
	var positions []string
	for i, line := range lines {
		if strings.Contains(line, actualOld) {
			positions = append(positions, strconv.Itoa(i+1))
		}
	}
	return domain.FileSystemEditResp{}, fmt.Errorf(
		"filesystem.edit: old_string matches %d times in %s; use replace_all to replace all occurrences. Match at lines: %s",
		count, path, strings.Join(positions, ", "))
}

// rmWhitelistCheck verifies the path is within allowed boundaries.
func rmWhitelistCheck(p string) error {
	cp := filepath.Clean(p)
	if cp == string(filepath.Separator) || cp == "." || cp == ".." {
		return fmt.Errorf("rm: cannot remove '%s': outside project scope", p)
	}
	if strings.HasPrefix(cp, ".."+string(filepath.Separator)) {
		return fmt.Errorf("rm: cannot remove '%s': outside project scope", p)
	}
	// Check for version control directories.
	base := filepath.Base(cp)
	if base == ".git" || base == ".hg" || base == ".svn" {
		return fmt.Errorf("rm: cannot remove '%s': protected directory", p)
	}
	return nil
}

// collectRmTargets collects all files/dirs that would be removed.
func collectRmTargets(path string, recursive bool) ([]string, int32, error) {
	var targets []string
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if !fi.IsDir() {
		return []string{path}, 1, nil
	}
	if !recursive {
		return []string{path}, 1, nil
	}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		targets = append(targets, p)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return targets, int32(len(targets)), nil
}
