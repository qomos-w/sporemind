package project

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/textdiff"
	"github.com/qomos-w/sporemind/pkg/util"
)

const (
	maxChunkSize    = 512 * 1024
	maxBase64Size   = 16 * 1024 * 1024
	maxEditFileSize = 128 * 1024 * 1024
	maxReadLines    = 10000
	implicitLimit   = 2000
	maxReadSizeMB   = 10
	binaryCheckSize = 512
	maxPreviewItems = 20
	grepHeadLimit   = 250
	grepMaxLimit    = 1000
	maxFileSizeGrep = 2 * 1024 * 1024
	maxListEntries  = 5000
)

// defaultSkipDirs are directories always skipped during recursive walks.
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

// isHiddenEntry reports whether a file or directory name is hidden (starts with ".").
func isHiddenEntry(name string) bool {
	return strings.HasPrefix(name, ".")
}

// buildSkipSet builds the directory-skip set for a list/glob/grep operation.
// When noIgnore is true, all filtering (defaultSkipDirs + Exclude) is bypassed
// and the returned set is nil. Otherwise the set is seeded with defaultSkipDirs
// and augmented with the comma-separated names in exclude.
func buildSkipSet(exclude string, noIgnore bool) map[string]bool {
	if noIgnore {
		return nil
	}
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

func loadGitignorePatterns(absDir, root string) []gitignore.Pattern {
	path := filepath.Join(absDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rel, _ := filepath.Rel(root, absDir)
	rel = util.NormalizePath(rel)
	domain := []string{}
	if rel != "" && rel != "." {
		domain = strings.Split(rel, "/")
	}
	var patterns []gitignore.Pattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Git semantics: a trailing "/**" matches everything INSIDE the
		// directory, never the directory itself. go-git's ** matches zero
		// segments too, which would prune the directory during walks and
		// break negation re-includes like `build/**` + `!build/config.yml`.
		// Rewriting to "/**/*" requires at least one inner segment.
		if !strings.HasPrefix(line, "!") && strings.HasSuffix(line, "/**") {
			line += "/*"
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

func isGitignored(patterns []gitignore.Pattern, relPath string, isDir bool) bool {
	if len(patterns) == 0 {
		return false
	}
	parts := strings.Split(relPath, "/")
	return gitignore.NewMatcher(patterns).Match(parts, isDir)
}

func (a *Actor) resolveAbsolutePath(p string) (string, error) {
	return a.resolveAbsolutePathConfirm(p, false)
}

func (a *Actor) resolveAbsolutePathConfirm(p string, confirm bool) (string, error) {
	root, rel, err := a.resolvePath(p)
	if err != nil {
		if confirm && errors.Is(err, errOutsideRoots) && filepath.IsAbs(p) {
			return filepath.Abs(p)
		}
		return "", err
	}
	return a.safeJoin(root.Path, rel, confirm)
}

// resolveAbsolutePathConfirmFor is the caller-scoped variant: resolves against
// the caller's bound worktree when present. Used by write/edit/rm handlers.
func (a *Actor) resolveAbsolutePathConfirmFor(callerID, p string, confirm bool) (string, error) {
	// BOUND caller: hard containment. Writes/edits/rm cannot escape the
	// worktree to the main repo or another agent's worktree, even with
	// confirm=true (which otherwise permits absolute-path escapes on the
	// main root for unbound callers). A bound caller whose worktree is not
	// active is denied entirely (no fallthrough to main repo).
	switch state, wtPath := a.classifyBinding(callerID); state {
	case bindingActive:
		return a.resolveContainedWorktree(wtPath, p)
	case bindingInactive:
		return "", errWorktreeInactive
	}
	root, rel, err := a.resolvePathFor(callerID, p)
	if err != nil {
		if confirm && errors.Is(err, errOutsideRoots) && filepath.IsAbs(p) {
			return filepath.Abs(p)
		}
		return "", err
	}
	return a.safeJoin(root.Path, rel, confirm)
}

// resolveReadPath resolves a path for read-only file operations.
// In addition to normal root-relative resolution, it allows paths that escape
// the first root using leading ".." components (e.g. "../sibling/file").
// Write/edit/rm operations must continue to use resolveAbsolutePath.
func (a *Actor) resolveReadPath(p string) (string, error) {
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) && strings.HasPrefix(clean, "..") {
		roots, rootsErr := a.rootsSnapshot()
		if rootsErr != nil {
			return "", rootsErr
		}
		if len(roots) == 0 {
			return "", fmt.Errorf("project: no roots configured")
		}
		joined := filepath.Join(roots[0].Path, clean)
		abs, err := filepath.Abs(joined)
		if err != nil {
			return "", fmt.Errorf("project: resolve read path: %w", err)
		}
		return abs, nil
	}
	return a.resolveAbsolutePath(p)
}

// resolveReadPathFor is the caller-scoped variant of resolveReadPath.
func (a *Actor) resolveReadPathFor(callerID, p string) (string, error) {
	roots := a.effectiveRoots(callerID)
	if len(roots) == 0 {
		return "", fmt.Errorf("project: no roots configured")
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) && strings.HasPrefix(clean, "..") {
		joined := filepath.Join(roots[0].Path, clean)
		abs, err := filepath.Abs(joined)
		if err != nil {
			return "", fmt.Errorf("project: resolve read path: %w", err)
		}
		return abs, nil
	}
	return a.resolveAbsolutePathConfirmFor(callerID, p, false)
}

// resolveBrowsePath resolves a path for browse-style read operations
// (file_list / file_glob / file_grep / file_read*). Unlike resolveReadPath it accepts any
// absolute filesystem path, even outside the configured roots — agents often
// need to inspect sibling repositories, system headers, or dependency caches.
// Write/edit/rm operations must continue to use resolveAbsolutePath.
func (a *Actor) resolveBrowsePath(p string) (string, error) {
	roots, rootsErr := a.rootsSnapshot()
	if rootsErr != nil {
		return "", rootsErr
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("project: no roots configured")
	}
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	return a.resolveReadPath(p)
}

// resolveBrowsePathFor is the caller-scoped variant of resolveBrowsePath.
// Used by file_list/file_glob/file_grep/file_read* handlers to route to the
// caller's bound worktree when present.
//
// BOUND caller: reads are browse-anywhere. Relative paths still route against
// the bound worktree (that is the caller's working root), but absolute paths
// and ".." walks may leave it — reading the main repo or another checkout for
// reference is allowed. Handlers call worktreeReadNote so the tool result
// tool result carries an advisory note when the resolved path landed outside
// the worktree (the content does not belong to the caller's isolated branch).
// Writes stay hard-contained via resolveAbsolutePathConfirmFor. A bound caller
// whose worktree is not active is denied entirely (no fallthrough to the main
// repo, which would leak).
func (a *Actor) resolveBrowsePathFor(callerID, p string) (string, error) {
	switch state, wtPath := a.classifyBinding(callerID); state {
	case bindingActive:
		if contained, err := a.resolveContainedWorktree(wtPath, p); err == nil {
			return contained, nil
		}
		clean := filepath.Clean(p)
		if filepath.IsAbs(clean) {
			return clean, nil
		}
		abs, err := filepath.Abs(filepath.Join(wtPath, clean))
		if err != nil {
			return "", fmt.Errorf("project: resolve browse path: %w", err)
		}
		return abs, nil
	case bindingInactive:
		return "", errWorktreeInactive
	}
	roots := a.effectiveRoots(callerID)
	if len(roots) == 0 {
		return "", fmt.Errorf("project: no roots configured")
	}
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	return a.resolveReadPathFor(callerID, p)
}

// worktreeReadNote returns an advisory note to append to a read-tool result
// when callerID is bound to an active worktree but resolvedPath lies outside
// it (main repo, another agent's worktree, or an unrelated directory). The
// note tells the agent the content does not belong to its isolated branch and
// writes remain confined. Empty when the path is inside the worktree or the
// caller is unbound.
func (a *Actor) worktreeReadNote(callerID, resolvedPath string) string {
	state, wtPath := a.classifyBinding(callerID)
	if state != bindingActive {
		return ""
	}
	sep := string(filepath.Separator)
	if resolvedPath == wtPath || strings.HasPrefix(resolvedPath, wtPath+sep) {
		return ""
	}
	return fmt.Sprintf("[worktree note] %s is outside your bound worktree, it does not belong to your isolated branch and may differ from your checkout. Treat it as read-only reference; your writes still land in the worktree.", resolvedPath)
}

// resolveContainedWorktree resolves p against a bound worktree root, denying
// any path that escapes the worktree: absolute paths into the main repo or
// another worktree, and ".." walks above the worktree root. The returned path
// is absolute and confined to wtRoot. Enforces hard worktree isolation.
func (a *Actor) resolveContainedWorktree(wtRoot, p string) (string, error) {
	clean := filepath.Clean(p)
	var abs string
	if filepath.IsAbs(clean) {
		abs = clean
	} else {
		abs = filepath.Clean(filepath.Join(wtRoot, clean))
	}
	sep := string(filepath.Separator)
	if abs != wtRoot && !strings.HasPrefix(abs, wtRoot+sep) {
		return "", fmt.Errorf("project: worktree isolation: path %q escapes worktree root", p)
	}
	return abs, nil
}

func (a *Actor) handleFileList(ctx actor.PureContext, req domain.FileSystemListReq) (string, error) {
	cid := callerID(ctx)
	root, err := a.resolveBrowsePathFor(cid, req.Path)
	if err != nil {
		return "", err
	}
	note := a.worktreeReadNote(cid, root)
	decorate := func(result string, err error) (string, error) {
		if err != nil || note == "" {
			return result, err
		}
		if result == "" {
			return note, nil
		}
		return result + "\n\n" + note, nil
	}
	skipSet := buildSkipSet(req.Exclude, req.NoIgnore)

	if req.Depth == 0 {
		if req.Detail {
			out, err := listDirFlatDetail(root, req.All, skipSet, req.NoIgnore)
			return decorate(out, err)
		}
		out, err := listDirFlat(root, req.All, skipSet, req.NoIgnore)
		return decorate(out, err)
	}

	entries, truncated, err := listDirDepth(ctx, root, int(req.Depth), skipSet, req.NoIgnore, req.All)
	if err != nil {
		return "", fmt.Errorf("project.list: %w", err)
	}
	if len(entries) == 0 {
		return decorate("(empty directory)", nil)
	}

	var lines []string
	for _, e := range entries {
		lines = append(lines, listEntryLine(e.entry, e.relPath, req.Detail))
	}

	result := strings.Join(lines, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n[truncated: showing first %d entries]", maxListEntries)
	}
	return decorate(result, nil)
}

// listDetailTimeLayout renders entry mtimes in list detail output. The fixed
// "<date> <time> <offset>" tail lets consumers split name from metadata with a
// right-anchored parse even when file names contain spaces.
const listDetailTimeLayout = "2006-01-02 15:04:05 -07:00"

// listEntryLine formats one list output line. Plain mode is the bare
// root-relative name (dirs keep a trailing "/"); detail mode appends mtime
// and, for files, "Size:N".
func listEntryLine(e os.DirEntry, relPath string, detail bool) string {
	name := relPath
	if e.IsDir() {
		name += "/"
	}
	if !detail {
		return name
	}
	info, err := e.Info()
	if err != nil {
		return name
	}
	line := name + " " + info.ModTime().Format(listDetailTimeLayout)
	if !info.IsDir() {
		line += fmt.Sprintf(" Size:%d", info.Size())
	}
	return line
}

// listDirFlatEntries lists the immediate children of a directory
// (non-recursive), applying skip/hidden/gitignore filters.
func listDirFlatEntries(root string, all bool, skipSet map[string]bool, noIgnore bool) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("project.list: %w", err)
	}
	var patterns []gitignore.Pattern
	if !noIgnore {
		patterns = loadGitignorePatterns(root, root)
	}
	var out []os.DirEntry
	for _, e := range entries {
		if skipSet[e.Name()] {
			continue
		}
		if !all && isHiddenEntry(e.Name()) {
			continue
		}
		if !noIgnore && isGitignored(patterns, e.Name(), e.IsDir()) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// listDirFlat lists the immediate children of a directory (non-recursive).
// Hidden entries (names starting with ".") are omitted unless all is true.
// Gitignored entries are omitted unless noIgnore is true (consistent with the
// recursive listDirDepth path and with file_glob/file_grep).
func listDirFlat(root string, all bool, skipSet map[string]bool, noIgnore bool) (string, error) {
	entries, err := listDirFlatEntries(root, all, skipSet, noIgnore)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, listEntryLine(e, e.Name(), false))
	}
	return strings.Join(lines, "\n"), nil
}

// listDirFlatDetail is the detail variant of listDirFlat: each line carries
// mtime and file size.
func listDirFlatDetail(root string, all bool, skipSet map[string]bool, noIgnore bool) (string, error) {
	entries, err := listDirFlatEntries(root, all, skipSet, noIgnore)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, listEntryLine(e, e.Name(), true))
	}
	return strings.Join(lines, "\n"), nil
}

type walkStackItem struct {
	absPath  string
	depth    int
	patterns []gitignore.Pattern
}

type dirEntryWithRelPath struct {
	entry   os.DirEntry
	relPath string
}

// listDirDepth walks the directory tree up to maxDepth levels.
// Hidden entries (names starting with ".") are omitted unless all is true.
func listDirDepth(ctx actor.PureContext, root string, maxDepth int, skipSet map[string]bool, noIgnore bool, all bool) ([]dirEntryWithRelPath, bool, error) {
	var out []dirEntryWithRelPath
	truncated := false
	cancelErr := fmt.Errorf("walk cancelled")

	var rootPatterns []gitignore.Pattern
	if !noIgnore {
		rootPatterns = loadGitignorePatterns(root, root)
	}

	stack := []walkStackItem{{absPath: root, depth: 0, patterns: rootPatterns}}

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

			if !noIgnore && isGitignored(current.patterns, rel, e.IsDir()) {
				continue
			}

			out = append(out, dirEntryWithRelPath{entry: e, relPath: rel})

			if len(out) >= maxListEntries {
				truncated = true
				break
			}

			if e.IsDir() && (maxDepth == -1 || current.depth < maxDepth) {
				dirPatterns := loadGitignorePatterns(filepath.Join(current.absPath, e.Name()), root)
				newPatterns := append(append([]gitignore.Pattern(nil), current.patterns...), dirPatterns...)
				subDirs = append(subDirs, walkStackItem{
					absPath:  filepath.Join(current.absPath, e.Name()),
					depth:    current.depth + 1,
					patterns: newPatterns,
				})
			}
		}

		for i := len(subDirs) - 1; i >= 0; i-- {
			stack = append(stack, subDirs[i])
		}
	}

	return out, truncated, nil
}

func (a *Actor) handleFileRead(ctx actor.PureContext, req domain.FileSystemReadReq) (domain.FileSystemReadResp, error) {
	cid := callerID(ctx)
	p, err := a.resolveBrowsePathFor(cid, req.Path)
	if err != nil {
		return domain.FileSystemReadResp{}, err
	}
	p = filepath.Clean(p)

	if isBinaryFile(p) {
		return domain.FileSystemReadResp{}, fmt.Errorf("project.read: binary file, cannot display as text")
	}

	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemReadResp{}, fmt.Errorf("[warning] file not found: %s", req.Path)
		}
		return domain.FileSystemReadResp{}, fmt.Errorf("project.read: %w", err)
	}

	if fi.Size() > maxReadSizeMB*1024*1024 && req.Offset <= 0 && req.Limit <= 0 && req.Tail <= 0 {
		totalLines := countFileLines(p)
		return domain.FileSystemReadResp{}, fmt.Errorf("project.read: file size %d MB exceeds %d MB limit; use offset+limit or tail (estimated %d lines)", fi.Size()/(1024*1024), maxReadSizeMB, totalLines)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemReadResp{}, fmt.Errorf("project.read: %w", err)
	}

	content := normalizeLineEndings(string(data))
	allLines := strings.Split(content, "\n")
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}
	totalLines := int32(len(allLines))

	startLine := int(req.Offset)
	if startLine <= 0 {
		startLine = 1
	}

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

	if req.Filter != "" {
		re, err := regexp.Compile(req.Filter)
		if err != nil {
			return domain.FileSystemReadResp{}, fmt.Errorf("project.read: invalid filter regex: %w", err)
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
	if len(lines) > 0 {
		content += "\n"
	}

	return domain.FileSystemReadResp{
		Content:    content,
		TotalLines: totalLines,
		StartLine:  int32(startLine),
		NumLines:   int32(len(lines)),
		Truncated:  truncated,
		Note:       a.worktreeReadNote(cid, p),
	}, nil
}

func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
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

func (a *Actor) handleFileReadBase64(ctx actor.PureContext, req domain.FileSystemReadBase64Req) (domain.FileSystemReadBase64Resp, error) {
	cid := callerID(ctx)
	p, err := a.resolveBrowsePathFor(cid, req.Path)
	if err != nil {
		return domain.FileSystemReadBase64Resp{}, err
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(p)
	if err != nil {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("project.read_base64: %w", err)
	}
	if fi.Size() > maxBase64Size {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("project.read_base64: file size %d exceeds %d byte cap; use project.read_chunk", fi.Size(), maxBase64Size)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemReadBase64Resp{}, fmt.Errorf("project.read_base64: %w", err)
	}
	return domain.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data), Note: a.worktreeReadNote(cid, p)}, nil
}

func (a *Actor) handleFileReadChunk(ctx actor.PureContext, req domain.FileSystemReadChunkReq) (domain.FileSystemReadChunkResp, error) {
	if req.Length <= 0 {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("project.read_chunk: length must be positive, got %d", req.Length)
	}
	if req.Offset < 0 {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("project.read_chunk: offset must be non-negative, got %d", req.Offset)
	}
	if req.Length > maxChunkSize {
		req.Length = maxChunkSize
	}

	cid := callerID(ctx)
	p, err := a.resolveBrowsePathFor(cid, req.Path)
	if err != nil {
		return domain.FileSystemReadChunkResp{}, err
	}

	f, err := os.Open(filepath.Clean(p))
	if err != nil {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("project.read_chunk: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("project.read_chunk: stat: %w", err)
	}
	total := fi.Size()

	if req.Offset >= total {
		return domain.FileSystemReadChunkResp{Offset: req.Offset, Length: 0, Total: total, Note: a.worktreeReadNote(cid, p)}, nil
	}

	remaining := total - req.Offset
	if int64(req.Length) > remaining {
		req.Length = int32(remaining)
	}

	buf := make([]byte, req.Length)
	n, err := f.ReadAt(buf, req.Offset)
	if err != nil && err != io.EOF {
		return domain.FileSystemReadChunkResp{}, fmt.Errorf("project.read_chunk: read: %w", err)
	}

	return domain.FileSystemReadChunkResp{
		Offset: req.Offset,
		Length: int32(n),
		Total:  total,
		Data:   base64.StdEncoding.EncodeToString(buf[:n]),
		Note:   a.worktreeReadNote(cid, p),
	}, nil
}

func (a *Actor) handleFileWrite(ctx actor.PureContext, req domain.FileSystemWriteReq) (domain.FileSystemWriteResp, error) {
	if req.Path == "" {
		return domain.FileSystemWriteResp{}, fmt.Errorf("project.write: path is required")
	}
	if err := a.denyUnboundSpawnChild("project.write", callerID(ctx)); err != nil {
		return domain.FileSystemWriteResp{}, err
	}
	if !ctx.Identity().IsZero() {
		if err := policy.RequireAgentOrHuman(ctx.Identity().Role); err != nil {
			return domain.FileSystemWriteResp{}, err
		}
	}
	p, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Path, req.Confirm)
	if err != nil {
		return domain.FileSystemWriteResp{}, err
	}
	p = filepath.Clean(p)
	fl := a.fileLocks.get(p)
	fl.Lock()
	defer fl.Unlock()

	// --- generated-file write guard ---
	{
		roots := a.effectiveRoots(callerID(ctx))
		if len(roots) > 0 {
			rel, err := filepath.Rel(roots[0].Path, p)
			if err == nil {
				if _, blocked := a.isProtected(callerID(ctx), roots[0].Path, rel); blocked {
					return domain.FileSystemWriteResp{}, fmt.Errorf(
						"project.write: %s is a generated file and cannot be hand-written; modify the .appdef and re-run generate",
						filepath.Base(p),
					)
				}
			}
		}
	}

	var resp domain.FileSystemWriteResp
	if info, err := os.Stat(p); err == nil {
		if info.IsDir() {
			return domain.FileSystemWriteResp{}, fmt.Errorf("project.write: path is a directory: %s", req.Path)
		}
		resp.Warning = fmt.Sprintf("file already exists: %s; contents overwritten", req.Path)
	}

	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return domain.FileSystemWriteResp{}, fmt.Errorf("project.write: mkdir: %w", err)
	}
	if err := os.WriteFile(p, []byte(req.Content), 0644); err != nil {
		return domain.FileSystemWriteResp{}, fmt.Errorf("project.write: %w", err)
	}
	return resp, nil
}

func (a *Actor) handleFileEdit(ctx actor.PureContext, req domain.FileSystemEditReq) (domain.FileSystemEditResp, error) {
	if req.Path == "" {
		return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: path is required")
	}
	if err := a.denyUnboundSpawnChild("project.edit", callerID(ctx)); err != nil {
		return domain.FileSystemEditResp{}, err
	}
	p, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Path, req.Confirm)
	if err != nil {
		return domain.FileSystemEditResp{}, err
	}
	p = filepath.Clean(p)
	fl := a.fileLocks.get(p)
	fl.Lock()
	defer fl.Unlock()

	// --- generated-file write guard ---
	{
		roots := a.effectiveRoots(callerID(ctx))
		if len(roots) > 0 {
			rel, err := filepath.Rel(roots[0].Path, p)
			if err == nil {
				if _, blocked := a.isProtected(callerID(ctx), roots[0].Path, rel); blocked {
					return domain.FileSystemEditResp{}, fmt.Errorf(
						"project.edit: %s is a generated file and cannot be hand-written; modify the .appdef and re-run generate",
						filepath.Base(p),
					)
				}
			}
		}
	}

	if !req.Overwrite {
		if req.OldString == "" {
			return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: old_string is empty; use project.write to create new files or set overwrite=true to replace the whole file")
		}
		if req.OldString == req.NewString {
			return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: old_string and new_string must differ")
		}
	}

	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: file not found: %s, use project.write to create new files", req.Path)
		}
		return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: %w", err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: %w", err)
	}

	if len(data) > maxEditFileSize {
		return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: file size %d exceeds %d MB limit; use project.write for large files", len(data), maxEditFileSize/(1024*1024))
	}

	usesCRLF := bytes.Contains(data, []byte("\r\n"))
	content := normalizeLineEndings(string(data))
	newString := strings.ReplaceAll(req.NewString, "\r\n", "\n")
	newString = strings.ReplaceAll(newString, "\r", "")

	var newContent string
	var count int
	if req.Overwrite {
		newContent = newString
		count = 1
	} else {
		oldString := strings.ReplaceAll(req.OldString, "\r", "")

		actualOld, found := findActualString(content, oldString)
		if !found {
			ctx := getMismatchContext(content, oldString)
			return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: old_string=%q not found in %s\n%s", req.OldString, req.Path, ctx)
		}

		count = strings.Count(content, actualOld)
		if count == 0 {
			ctx := getMismatchContext(content, oldString)
			return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: old_string=%q not found in %s\n%s", req.OldString, req.Path, ctx)
		}

		if !req.ReplaceAll && count > 1 {
			return fmtMismatchMultiple(req.Path, count, content, actualOld)
		}

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

	hunks, additions, deletions := textdiff.BuildHunks(content, newContent)
	if usesCRLF {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	if err := os.WriteFile(p, []byte(newContent), 0644); err != nil {
		return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: write: %w", err)
	}

	return domain.FileSystemEditResp{
		Replacements: int32(count),
		Additions:    int32(additions),
		Deletions:    int32(deletions),
		Hunks:        hunks,
	}, nil
}

func findActualString(content, search string) (string, bool) {
	// 1. Exact match.
	if strings.Contains(content, search) {
		return search, true
	}
	// 2. Smart-quote normalization (curly → straight).
	normalized := normalizeQuotes(search)
	if normalized != search && strings.Contains(content, normalized) {
		return normalized, true
	}
	// 3. C-escape unescaping: LLMs may send literal \t \n etc. in old_string.
	unescaped, unescapedChanged := unescapeC(search)
	if unescapedChanged && strings.Contains(content, unescaped) {
		return unescaped, true
	}
	// 4. Whitespace-tolerant fallback: tabs vs spaces. Try the raw search and
	//    the unescaped variant.
	candidates := []string{search}
	if unescapedChanged {
		candidates = append(candidates, unescaped)
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

func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, "“", "\"")
	s = strings.ReplaceAll(s, "”", "\"")
	s = strings.ReplaceAll(s, "‘", "'")
	s = strings.ReplaceAll(s, "’", "'")
	return s
}

func getMismatchContext(content, oldString string) string {
	const snippetLines = 3
	lines := strings.Split(content, "\n")
	var context []string
	for i, line := range lines {
		if i < snippetLines {
			context = append(context, fmt.Sprintf("%d: %s", i+1, line))
		}
	}
	return fmt.Sprintf("(first %d lines of file)\n%s", snippetLines, strings.Join(context, "\n"))
}

func fmtMismatchMultiple(path string, count int, content, actualOld string) (domain.FileSystemEditResp, error) {
	indices := findAllIndexes(content, actualOld)
	var snippets []string
	lines := strings.Split(content, "\n")
	for _, idx := range indices {
		lineNum := 1 + strings.Count(content[:idx], "\n")
		if lineNum > 0 && lineNum <= len(lines) {
			snippets = append(snippets, fmt.Sprintf("line %d: %s", lineNum, lines[lineNum-1]))
		}
	}
	return domain.FileSystemEditResp{}, fmt.Errorf("project.edit: old_string occurs %d times in %s; set replace_all=true to replace all, or narrow to one occurrence. Occurrences:\n%s", count, path, strings.Join(snippets, "\n"))
}

func findAllIndexes(content, substr string) []int {
	var out []int
	start := 0
	for {
		idx := strings.Index(content[start:], substr)
		if idx < 0 {
			break
		}
		out = append(out, start+idx)
		start += idx + len(substr)
	}
	return out
}

func unescapeC(s string) (string, bool) {
	if !strings.ContainsAny(s, "\\") {
		return s, false
	}
	var out strings.Builder
	out.Grow(len(s))
	changed := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				out.WriteByte('\n')
				i++
				changed = true
			case 't':
				out.WriteByte('\t')
				i++
				changed = true
			case 'r':
				out.WriteByte('\r')
				i++
				changed = true
			case '"':
				out.WriteByte('"')
				i++
				changed = true
			case '\\':
				out.WriteByte('\\')
				i++
				changed = true
			default:
				out.WriteByte(s[i])
			}
		} else {
			out.WriteByte(s[i])
		}
	}
	if !changed {
		return s, false
	}
	return out.String(), true
}

func (a *Actor) handleFileGlob(ctx actor.PureContext, req domain.FileSystemGlobReq) (domain.FileSystemGlobResp, error) {
	rootPath := req.Path
	pattern := req.Pattern
	var relPrefix string
	// Skip path-splitting for regex patterns (e.g. AgentRef|AgentListItem),
	// but still split parenthesized wrapper patterns like (**/*.go) or
	// (src/**/*.tsx). The wrapper is our syntax, not a regex group.
	if rootPath == "" && pattern != "" && (!isRegexPattern(pattern) || (strings.HasPrefix(pattern, "(") && strings.HasSuffix(pattern, ")"))) {
		p, pat := splitPatternPath(pattern)
		if p != "" {
			rootPath = p
			relPrefix = util.NormalizePath(p) + "/"
		}
		pattern = pat
	}

	basePath, err := a.resolveBrowsePathFor(callerID(ctx), rootPath)
	if err != nil {
		return domain.FileSystemGlobResp{}, err
	}
	basePath = filepath.Clean(basePath)
	note := a.worktreeReadNote(callerID(ctx), basePath)
	skipSet := buildSkipSet(req.Exclude, req.NoIgnore)

	patterns := splitPatterns(pattern)
	var include, exclude []string
	prefixToStrip := ""
	if rootPath != "" {
		prefixToStrip = util.NormalizePath(rootPath) + "/"
	}
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if strings.HasPrefix(pat, "!") {
			pat = pat[1:]
			if prefixToStrip != "" && strings.HasPrefix(util.NormalizePath(pat)+"/", prefixToStrip) {
				pat = strings.TrimPrefix(util.NormalizePath(pat), prefixToStrip)
			}
			exclude = append(exclude, pat)
		} else {
			include = append(include, pat)
		}
	}
	if len(include) == 0 {
		include = append(include, "*")
	}

	maxDepth := int(req.Maxdepth)

	var matches []string
	seen := make(map[string]bool)
	truncated := false

	for _, pat := range include {
		if truncated {
			break
		}
		files, trun, err := globPattern(ctx, basePath, pat, maxDepth, skipSet, req.NoIgnore)
		if err != nil {
			return domain.FileSystemGlobResp{}, fmt.Errorf("project.glob: %w", err)
		}
		if trun {
			truncated = true
		}
		for _, f := range files {
			rel, _ := filepath.Rel(basePath, f)
			rel = util.NormalizePath(rel)
			if relPrefix != "" {
				rel = relPrefix + rel
			}
			if !seen[rel] {
				seen[rel] = true
				matches = append(matches, rel)
			}
		}
	}

	for _, pat := range exclude {
		if truncated {
			break
		}
		files, _, err := globPattern(ctx, basePath, pat, maxDepth, skipSet, req.NoIgnore)
		if err != nil {
			return domain.FileSystemGlobResp{}, fmt.Errorf("project.glob: %w", err)
		}
		for _, f := range files {
			rel, _ := filepath.Rel(basePath, f)
			rel = util.NormalizePath(rel)
			if relPrefix != "" {
				rel = relPrefix + rel
			}
			delete(seen, rel)
		}
	}

	matches = matches[:0]
	for rel := range seen {
		matches = append(matches, rel)
	}
	if req.OrderBy == "-modified" {
		sortGlobMatchesByMtimeDesc(basePath, relPrefix, matches)
	} else {
		sort.Strings(matches)
	}

	const hardLimit = 10000
	if len(matches) > hardLimit {
		matches = matches[:hardLimit]
		truncated = true
	}

	return domain.FileSystemGlobResp{
		Files:     matches,
		NumFiles:  int32(len(matches)),
		Truncated: truncated,
		Note:      note,
	}, nil
}

// sortGlobMatchesByMtimeDesc sorts slash-normalized project-relative glob
// matches by file mtime, newest first; ties stay lexicographic and entries
// whose stat fails sink to the end. relPrefix is the path prefix that was
// prepended to results when the walk was rooted at a subdirectory — it is
// stripped again before resolving the filesystem path.
func sortGlobMatchesByMtimeDesc(basePath, relPrefix string, matches []string) {
	sort.Strings(matches)
	mods := make(map[string]time.Time, len(matches))
	for _, rel := range matches {
		relNoPrefix := strings.TrimPrefix(rel, relPrefix)
		p := filepath.Join(basePath, filepath.FromSlash(relNoPrefix))
		if info, err := os.Stat(p); err == nil {
			mods[rel] = info.ModTime()
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return mods[matches[i]].After(mods[matches[j]])
	})
}

func splitPatterns(patterns string) []string {
	var out []string
	braceExpanded, _ := expandBraces(patterns)
	for _, p := range strings.Split(braceExpanded, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func globPattern(ctx actor.PureContext, basePath, pattern string, maxDepth int, skipSet map[string]bool, noIgnore bool) ([]string, bool, error) {
	if isRegexPattern(pattern) {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, false, nil
		}
		return contentGlob(ctx, basePath, re, maxDepth, skipSet, noIgnore)
	}
	// Reject patterns that walk above basePath via ".." segments. The bare
	// non-meta branch joins the pattern verbatim (filepath.Join) and would
	// escape basePath; walkGlob already ignores ".." segments (ReadDir never
	// yields them), so rejecting upfront closes the non-meta escape uniformly.
	if patternContainsParentRef(pattern) {
		return nil, false, nil
	}
	if hasMeta(pattern) {
		p := filepath.ToSlash(pattern)
		// *.ext without a directory prefix matches only the current
		// directory; prepend **/ so it recurses like **/*.ext.
		if !strings.Contains(p, "/") && !strings.HasPrefix(p, "**") {
			p = "**/" + p
		}
		parts := strings.Split(p, "/")
		return walkGlob(ctx, basePath, parts, maxDepth, skipSet, noIgnore)
	}
	full := filepath.Join(basePath, pattern)
	if info, err := os.Stat(full); err == nil {
		if !noIgnore {
			rootPatterns := loadGitignorePatterns(basePath, basePath)
			rel, _ := filepath.Rel(basePath, full)
			rel = util.NormalizePath(rel)
			if isGitignored(rootPatterns, rel, info.IsDir()) {
				return nil, false, nil
			}
		}
		return []string{full}, false, nil
	}
	// Bare search term (no meta, no slashes): fall back to recursive substring
	// match so a query like "TurnTail" finds ui/.../TurnTail.tsx.
	if !strings.ContainsAny(pattern, `/\`) {
		parts := strings.Split(filepath.ToSlash("**/*"+pattern+"*"), "/")
		return walkGlob(ctx, basePath, parts, maxDepth, skipSet, noIgnore)
	}
	return nil, false, nil
}

// isRegexPattern returns true when pattern contains regex-specific syntax that
// has no equivalent in glob: | (alternation), + (quantifier), \ (escape), ()
// (groups), {n,m} (count), ^/$ (anchors).
func isRegexPattern(p string) bool {
	if strings.ContainsAny(p, "|+\\()$^") {
		return true
	}
	if re := regexp.MustCompile(`\{\d+[,\}]\d*\}`); re.MatchString(p) {
		return true
	}
	return false
}

// patternContainsParentRef reports whether a glob pattern has any ".." path
// segment. Such segments would escape basePath when joined verbatim in the
// non-meta glob branch, violating worktree containment.
func patternContainsParentRef(pattern string) bool {
	for _, part := range strings.Split(filepath.ToSlash(pattern), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// contentGlob walks files under basePath and returns those whose content matches re.
func contentGlob(ctx actor.PureContext, basePath string, re *regexp.Regexp, maxDepth int, skipSet map[string]bool, noIgnore bool) ([]string, bool, error) {
	var results []string
	truncated := false

	var gitignorePatterns []gitignore.Pattern
	if !noIgnore {
		gitignorePatterns = loadGitignorePatterns(basePath, basePath)
	}

	walkMaxDepth := maxDepth
	if walkMaxDepth == 0 {
		walkMaxDepth = -1
	}

	walkDir(ctx, basePath, 0, walkMaxDepth, skipSet, noIgnore, basePath, gitignorePatterns, func(path string, isDir bool) bool {
		if isDir {
			return true
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxFileSizeGrep {
			return true
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return true
		}
		if re.Match(data) {
			results = append(results, path)
			if len(results) >= maxListEntries {
				truncated = true
				return false
			}
		}
		return true
	})

	return results, truncated, nil
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// splitPatternPath extracts a leading directory path from a glob pattern when
// the path component is empty. It prefers to split at the last "/**/" segment
// whose prefix is a literal directory path (no glob metacharacters). This turns
// patterns like "pkg/domain/gen/**/*.go" into path="pkg/domain/gen" and
// pat="**/*.go", so Maxdepth applies to the recursive suffix instead of being
// consumed by the literal prefix. Falls back to splitting at the last '/' when
// there is no such "**/" segment.
func splitPatternPath(pattern string) (path, pat string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", ""
	}
	// If pattern is wrapped in parentheses, unwrap and treat as a bare path if no glob meta.
	if strings.HasPrefix(pattern, "(") && strings.HasSuffix(pattern, ")") {
		inner := pattern[1 : len(pattern)-1]
		if !hasMeta(inner) {
			return inner, ""
		}
		pattern = inner
	}
	// Prefer splitting at "**/" so the literal prefix becomes the search root.
	if idx := strings.LastIndex(pattern, "/**/"); idx >= 0 {
		prefix := pattern[:idx]
		if prefix == "" || !hasMeta(prefix) {
			return prefix, pattern[idx+1:]
		}
	}
	lastSlash := strings.LastIndex(pattern, "/")
	if lastSlash <= 0 {
		return "", pattern
	}
	prefix := pattern[:lastSlash]
	if hasMeta(prefix) {
		return "", pattern
	}
	return prefix, pattern[lastSlash+1:]
}

func walkGlob(ctx actor.PureContext, basePath string, parts []string, maxDepth int, skipSet map[string]bool, noIgnore bool) ([]string, bool, error) {
	var results []string
	truncated := false
	cancelErr := fmt.Errorf("walk cancelled")

	type stackItem struct {
		dir      string
		depth    int
		idx      int
		patterns []gitignore.Pattern
	}
	var rootPatterns []gitignore.Pattern
	if !noIgnore {
		rootPatterns = loadGitignorePatterns(basePath, basePath)
	}
	stack := []stackItem{{dir: basePath, depth: 0, idx: 0, patterns: rootPatterns}}

	for len(stack) > 0 {
		if len(results)%500 == 0 {
			select {
			case <-ctx.Done():
				return results, truncated, cancelErr
			default:
			}
		}

		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if cur.idx >= len(parts) {
			continue
		}
		part := parts[cur.idx]
		isLast := cur.idx == len(parts)-1

		if part == "**" {
			if isLast {
				// Match all files and directories recursively.
				matches, trun, err := collectAll(ctx, cur.dir, maxDepth, cur.depth, skipSet, noIgnore, basePath, cur.patterns)
				if err != nil {
					return results, truncated, err
				}
				if trun {
					truncated = true
				}
				results = append(results, matches...)
			} else {
				// **/rest: try matching rest at this depth and deeper.
				stack = append(stack, stackItem{dir: cur.dir, depth: cur.depth, idx: cur.idx + 1, patterns: cur.patterns})
				if maxDepth == 0 || cur.depth < maxDepth {
					entries, err := os.ReadDir(cur.dir)
					if err != nil {
						continue
					}
					for _, e := range entries {
						if !e.IsDir() || skipSet[e.Name()] {
							continue
						}
						full := filepath.Join(cur.dir, e.Name())
						if isSymlink(full) {
							continue
						}
						if !noIgnore {
							rel, _ := filepath.Rel(basePath, full)
							rel = util.NormalizePath(rel)
							if isGitignored(cur.patterns, rel, true) {
								continue
							}
						}
						dirPatterns := loadGitignorePatterns(full, basePath)
						newPatterns := append(append([]gitignore.Pattern(nil), cur.patterns...), dirPatterns...)
						stack = append(stack, stackItem{dir: full, depth: cur.depth + 1, idx: cur.idx, patterns: newPatterns})
					}
				}
			}
			continue
		}

		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			matched, err := filepath.Match(part, name)
			if err != nil {
				continue
			}
			if !matched {
				continue
			}
			full := filepath.Join(cur.dir, name)
			if isLast {
				if e.IsDir() && skipSet[e.Name()] {
					continue
				}
				if !noIgnore {
					rel, _ := filepath.Rel(basePath, full)
					rel = util.NormalizePath(rel)
					if isGitignored(cur.patterns, rel, e.IsDir()) {
						continue
					}
				}
				results = append(results, full)
				if len(results) >= maxListEntries {
					truncated = true
					return results, truncated, nil
				}
			} else if e.IsDir() {
				if skipSet[e.Name()] || isSymlink(full) {
					continue
				}
				if maxDepth == 0 || cur.depth < maxDepth {
					dirPatterns := loadGitignorePatterns(full, basePath)
					newPatterns := append(append([]gitignore.Pattern(nil), cur.patterns...), dirPatterns...)
					stack = append(stack, stackItem{dir: full, depth: cur.depth + 1, idx: cur.idx + 1, patterns: newPatterns})
				}
			}
		}
	}

	return results, truncated, nil
}

func collectAll(ctx actor.PureContext, dir string, maxDepth, depth int, skipSet map[string]bool, noIgnore bool, root string, patterns []gitignore.Pattern) ([]string, bool, error) {
	var results []string
	truncated := false
	cancelErr := fmt.Errorf("walk cancelled")

	if maxDepth > 0 && depth > maxDepth {
		return results, truncated, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return results, truncated, nil
	}

	dirPatterns := loadGitignorePatterns(dir, root)
	newPatterns := append(append([]gitignore.Pattern(nil), patterns...), dirPatterns...)

	for _, e := range entries {
		if len(results)%500 == 0 {
			select {
			case <-ctx.Done():
				return results, truncated, cancelErr
			default:
			}
		}

		if !noIgnore {
			rel, _ := filepath.Rel(root, filepath.Join(dir, e.Name()))
			rel = util.NormalizePath(rel)
			if isGitignored(newPatterns, rel, e.IsDir()) {
				continue
			}
		}

		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if skipSet[e.Name()] || isSymlink(full) {
				continue
			}
			results = append(results, full)
			if len(results) >= maxListEntries {
				return results, true, nil
			}
			if maxDepth == 0 || depth < maxDepth {
				sub, trun, err := collectAll(ctx, full, maxDepth, depth+1, skipSet, noIgnore, root, newPatterns)
				if err != nil {
					return results, truncated, err
				}
				if trun {
					truncated = true
					return results, truncated, nil
				}
				results = append(results, sub...)
				if len(results) >= maxListEntries {
					return results, true, nil
				}
			}
		} else {
			results = append(results, full)
			if len(results) >= maxListEntries {
				return results, true, nil
			}
		}
	}
	return results, truncated, nil
}

func expandBraces(pattern string) (string, error) {
	var results []string
	expandRecursive(pattern, &results)
	if len(results) == 0 {
		return pattern, nil
	}
	return strings.Join(results, ","), nil
}

func expandRecursive(pattern string, out *[]string) {
	start := strings.Index(pattern, "{")
	if start < 0 {
		*out = append(*out, pattern)
		return
	}
	end := strings.Index(pattern[start:], "}")
	if end < 0 {
		*out = append(*out, pattern)
		return
	}
	end += start
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	alternatives := strings.Split(pattern[start+1:end], ",")
	for _, alt := range alternatives {
		expandRecursive(prefix+alt+suffix, out)
	}
}

func (a *Actor) handleFileGrep(ctx actor.PureContext, req domain.FileSystemGrepReq) (domain.FileSystemGrepResp, error) {
	rootPath := req.Path
	glob := req.Glob
	if rootPath == "" && glob != "" {
		p, pat := splitPatternPath(glob)
		if p != "" {
			rootPath = p
		}
		glob = pat
	}

	basePath, err := a.resolveBrowsePathFor(callerID(ctx), rootPath)
	if err != nil {
		return domain.FileSystemGrepResp{}, err
	}
	basePath = filepath.Clean(basePath)
	note := a.worktreeReadNote(callerID(ctx), basePath)
	skipSet := buildSkipSet(req.Exclude, req.NoIgnore)

	pattern := preprocessPattern(req.Pattern, req.WordRegexp, req.Multiline)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return domain.FileSystemGrepResp{}, fmt.Errorf("project.grep: invalid pattern: %w", err)
	}

	headLimit := int(req.HeadLimit)
	if headLimit <= 0 {
		headLimit = grepHeadLimit
	}
	if headLimit > grepMaxLimit {
		headLimit = grepMaxLimit
	}

	outputMode := req.OutputMode
	if outputMode == "" {
		outputMode = "files"
	}

	globPatterns := splitPatterns(glob)
	if len(globPatterns) == 0 {
		globPatterns = []string{"*"}
	}

	var files []string
	var counts []domain.FileSystemGrepCount
	var matches []domain.FileSystemGrepMatch
	numMatches := 0
	truncated := false
	var mu sync.Mutex

	fileC := make(chan string, 100)
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	relPath := func(abs string) string {
		rel, err := filepath.Rel(basePath, abs)
		if err != nil {
			return abs
		}
		return util.NormalizePath(rel)
	}

	// file_grep Depth: 0 or negative = recursive/unlimited (default); positive = directory levels.
	go func() {
		defer close(fileC)
		collectGrepFiles(ctx, basePath, globPatterns, int(req.Context), int(req.Depth), skipSet, req.NoIgnore, func(path string) bool {
			select {
			case fileC <- path:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()

	workers := 4
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range fileC {
				select {
				case <-ctx.Done():
					return
				default:
				}

				mu.Lock()
				budget := headLimit - numMatches
				mu.Unlock()
				if budget <= 0 {
					continue
				}

				fileMatches, fileCounts, fileFiles, fileNum, err := grepFile(path, re, outputMode, budget, req.Multiline, int(req.Context), int(req.BeforeContext), int(req.AfterContext), req.InvertMatch)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					continue
				}

				// Convert absolute paths returned by grepFile to paths relative to
				// the search root so callers get stable, portable results.
				for i := range fileFiles {
					fileFiles[i] = relPath(fileFiles[i])
				}
				for i := range fileCounts {
					fileCounts[i].File = relPath(fileCounts[i].File)
				}
				for i := range fileMatches {
					fileMatches[i].File = relPath(fileMatches[i].File)
				}

				mu.Lock()
				if outputMode == "files" {
					files = append(files, fileFiles...)
				} else if outputMode == "count" {
					counts = append(counts, fileCounts...)
				} else {
					matches = append(matches, fileMatches...)
				}
				numMatches += fileNum
				if numMatches >= headLimit {
					truncated = true
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return domain.FileSystemGrepResp{}, fmt.Errorf("project.grep: %w", err)
	default:
	}

	// Enforce the cross-file head limit on the returned result set so a single
	// file cannot consume the whole budget and leave nothing for the rest.
	switch outputMode {
	case "files":
		if len(files) > headLimit {
			files = files[:headLimit]
			truncated = true
		}
	case "count":
		if numMatches > headLimit {
			truncated = true
		}
	default:
		if len(matches) > headLimit {
			matches = matches[:headLimit]
			truncated = true
		}
	}
	if numMatches > headLimit {
		numMatches = headLimit
	}

	resp := domain.FileSystemGrepResp{
		NumMatches: int32(numMatches),
		Truncated:  truncated,
		OutputMode: outputMode,
		Note:       note,
	}
	switch outputMode {
	case "files":
		resp.Files = files
	case "count":
		resp.Counts = counts
	default:
		resp.Matches = matches
	}
	return resp, nil
}

func collectGrepFiles(ctx actor.PureContext, basePath string, patterns []string, contextLines int, depth int, skipSet map[string]bool, noIgnore bool, yield func(string) bool) {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}

	info, err := os.Stat(basePath)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if matchAnyPattern(filepath.Base(basePath), filepath.Base(basePath), patterns) {
			yield(basePath)
		}
		return
	}

	// Depth: 0 or negative = recursive/unlimited; positive = specific levels.
	maxDepth := -1
	if depth > 0 {
		maxDepth = depth
	}
	if !(len(patterns) == 1 && patterns[0] == "*") {
		maxDepth = -1
	}

	var gitignorePatterns []gitignore.Pattern
	if !noIgnore {
		gitignorePatterns = loadGitignorePatterns(basePath, basePath)
	}

	walkDir(ctx, basePath, 0, maxDepth, skipSet, noIgnore, basePath, gitignorePatterns, func(path string, isDir bool) bool {
		if isDir {
			return true
		}
		rel, _ := filepath.Rel(basePath, path)
		rel = util.NormalizePath(rel)
		if matchAnyPattern(rel, filepath.Base(path), patterns) {
			return yield(path)
		}
		return true
	})
}

func matchAnyPattern(relPath, baseName string, patterns []string) bool {
	for _, pat := range patterns {
		if pat == "*" {
			return true
		}
		if matched, _ := filepath.Match(pat, baseName); matched {
			return true
		}
		if !strings.Contains(pat, "/") && !strings.Contains(pat, "**") {
			continue
		}
		if matched := matchGlobPattern(pat, relPath); matched {
			return true
		}
	}
	return false
}

func matchGlobPattern(pattern, path string) bool {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	return matchGlobParts(parts, pathParts)
}

func matchGlobParts(parts, pathParts []string) bool {
	if len(parts) == 0 && len(pathParts) == 0 {
		return true
	}
	if len(parts) == 0 {
		return false
	}
	if parts[0] == "**" {
		if len(parts) == 1 {
			return true
		}
		for i := 0; i <= len(pathParts); i++ {
			if matchGlobParts(parts[1:], pathParts[i:]) {
				return true
			}
		}
		return false
	}
	if len(pathParts) == 0 {
		return false
	}
	matched, _ := filepath.Match(parts[0], pathParts[0])
	if !matched {
		return false
	}
	return matchGlobParts(parts[1:], pathParts[1:])
}

func walkDir(ctx actor.PureContext, dir string, depth, maxDepth int, skipSet map[string]bool, noIgnore bool, root string, patterns []gitignore.Pattern, visitor func(string, bool) bool) {
	if maxDepth > 0 && depth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	dirPatterns := loadGitignorePatterns(dir, root)
	newPatterns := append(append([]gitignore.Pattern(nil), patterns...), dirPatterns...)

	for _, e := range entries {
		select {
		case <-ctx.Done():
			return
		default:
		}
		full := filepath.Join(dir, e.Name())
		if !noIgnore {
			rel, _ := filepath.Rel(root, full)
			rel = util.NormalizePath(rel)
			if isGitignored(newPatterns, rel, e.IsDir()) {
				continue
			}
		}
		if e.IsDir() {
			if skipSet[e.Name()] || isSymlink(full) {
				continue
			}
			if !visitor(full, true) {
				return
			}
			if maxDepth == -1 || depth < maxDepth {
				walkDir(ctx, full, depth+1, maxDepth, skipSet, noIgnore, root, newPatterns, visitor)
			}
		} else {
			if !visitor(full, false) {
				return
			}
		}
	}
}

func grepFile(path string, re *regexp.Regexp, outputMode string, budget int, multiline bool, context, beforeContext, afterContext int, invert bool) ([]domain.FileSystemGrepMatch, []domain.FileSystemGrepCount, []string, int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, nil, 0, nil
	}
	if info.IsDir() || info.Size() > maxFileSizeGrep {
		return nil, nil, nil, 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, 0, nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, nil, 0, nil
	}

	if budget <= 0 {
		return nil, nil, nil, 0, nil
	}

	if multiline {
		content := string(data)
		locs := re.FindAllStringIndex(content, budget)
		count := len(locs)
		if count == 0 {
			return nil, nil, nil, 0, nil
		}
		if outputMode == "count" {
			return nil, []domain.FileSystemGrepCount{{File: path, Count: int32(count)}}, nil, count, nil
		}
		if outputMode == "files" {
			return nil, nil, []string{path}, count, nil
		}
		matches := make([]domain.FileSystemGrepMatch, 0, count)
		for _, loc := range locs {
			line := 1 + strings.Count(content[:loc[0]], "\n")
			matches = append(matches, domain.FileSystemGrepMatch{
				File:    path,
				Line:    int32(line),
				Content: content[loc[0]:loc[1]],
			})
		}
		return matches, nil, nil, count, nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if beforeContext == 0 && afterContext == 0 && context > 0 {
		beforeContext = context
		afterContext = context
	}

	var matches []domain.FileSystemGrepMatch
	var count int
	lastPrinted := -1

	for i, line := range lines {
		matched := re.MatchString(line)
		if invert {
			matched = !matched
		}
		if !matched {
			continue
		}
		count++
		if outputMode == "count" {
			continue
		}
		if outputMode == "files" {
			return nil, nil, []string{path}, count, nil
		}

		start := i - beforeContext
		if start < 0 {
			start = 0
		}
		end := i + afterContext
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for j := start; j <= end; j++ {
			if j <= lastPrinted {
				continue
			}
			matches = append(matches, domain.FileSystemGrepMatch{
				File:    path,
				Line:    int32(j + 1),
				Content: lines[j],
			})
			lastPrinted = j
		}
		if count >= budget {
			break
		}
	}

	if outputMode == "count" {
		return nil, []domain.FileSystemGrepCount{{File: path, Count: int32(count)}}, nil, count, nil
	}
	return matches, nil, nil, count, nil
}

// preprocessPattern converts POSIX character classes to Go regexp syntax and
// applies -w (word-regexp) wrapping and multiline dotall mode.
func preprocessPattern(pattern string, wordRegexp, multiline bool) string {
	posixCharClassRe := regexp.MustCompile(`\[\[:([\^]?)([a-z]+):\]\]`)
	posixToGoClass := map[string]string{
		"alnum":  `a-zA-Z0-9`,
		"alpha":  `a-zA-Z`,
		"ascii":  `\x00-\x7F`,
		"blank":  `\t `,
		"cntrl":  `\x00-\x1F\x7F`,
		"digit":  `0-9`,
		"graph":  `\x21-\x7E`,
		"lower":  `a-z`,
		"print":  `\x20-\x7E`,
		"punct":  `!"#$%&'()*+,\-./:;<=>?@\[\\\]^_{|}~`,
		"space":  ` \t\n\r\f\v`,
		"upper":  `A-Z`,
		"word":   `a-zA-Z0-9_`,
		"xdigit": `0-9a-fA-F`,
	}
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
	if multiline {
		pattern = "(?s:" + pattern + ")"
	}
	if wordRegexp {
		pattern = `(?:\b` + pattern + `\b)`
	}
	return pattern
}

func (a *Actor) handleFileRm(ctx actor.PureContext, req domain.FileSystemRmReq) (domain.FileSystemRmResp, error) {
	if err := a.denyUnboundSpawnChild("project.rm", callerID(ctx)); err != nil {
		return domain.FileSystemRmResp{}, err
	}
	p, err := a.resolveAbsolutePathConfirmFor(callerID(ctx), req.Path, req.Confirm)
	if err != nil {
		return domain.FileSystemRmResp{}, err
	}
	p = filepath.Clean(p)
	fl := a.fileLocks.get(p)
	fl.Lock()
	defer fl.Unlock()

	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: path not found: %s", req.Path)
		}
		return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: %w", err)
	}

	if info.IsDir() && !req.Recursive {
		return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: %s is a directory; set recursive=true", req.Path)
	}

	if !req.Confirm && !req.Force {
		preview, err := previewRm(ctx, p, info.IsDir())
		if err != nil {
			return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: preview: %w", err)
		}
		return domain.FileSystemRmResp{Preview: true, Removed: preview, Count: int32(len(preview))}, nil
	}

	var removed []string
	if info.IsDir() {
		removed, err = rmDirRecursive(ctx, p)
		if err != nil {
			return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: %w", err)
		}
	} else {
		if err := os.Remove(p); err != nil {
			return domain.FileSystemRmResp{}, fmt.Errorf("project.rm: %w", err)
		}
		rel, _ := filepath.Rel(filepath.Dir(p), p)
		removed = append(removed, rel)
	}

	return domain.FileSystemRmResp{
		Removed: removed,
		Count:   int32(len(removed)),
		Preview: false,
	}, nil
}

func previewRm(ctx actor.PureContext, path string, isDir bool) ([]string, error) {
	if !isDir {
		return []string{filepath.Base(path)}, nil
	}
	var out []string
	walkDir(ctx, path, 0, 1, nil, true, path, nil, func(p string, isDir bool) bool {
		if !isDir {
			rel, _ := filepath.Rel(path, p)
			out = append(out, rel)
		}
		if len(out) >= maxPreviewItems {
			return false
		}
		return true
	})
	return out, nil
}

func rmDirRecursive(ctx actor.PureContext, dir string) ([]string, error) {
	var removed []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}
		if path == dir {
			return nil
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			removed = append(removed, rel)
		}
		return nil
	})
	if err != nil {
		return removed, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return removed, err
	}
	return removed, nil
}

func parseInt32(s string) (int32, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(v), nil
}
