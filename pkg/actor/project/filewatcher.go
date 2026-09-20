package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const fileWatchDebounce = 100 * time.Millisecond

// sporemindDirName is the project-internal state directory.
const sporemindDirName = ".sporecode"

// startFileWatcher watches a deliberately narrow surface:
//
//   - each root directory non-recursively (so a fresh project that does not yet
//     have .sporecode still notices its creation), and
//   - the .sporecode subtree recursively (the only always-on file_changed
//     consumer filters for .sporecode/wiki/*.md).
//
// The previous design watched EVERY directory under each root (~500 handles,
// ~30MB kernel buffers on a repo this size). Arbitrary files outside
// .sporecode are watched on demand via project.watch_file.
func (a *Actor) startFileWatcher(ctx actor.Context) error {
	roots, rootsErr := a.rootsSnapshot()
	if rootsErr != nil {
		return rootsErr
	}
	if len(roots) == 0 {
		return nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	// Register all watches BEFORE publishing the watcher on the actor: a
	// failed Add used to return with a.fileWatcher already set and no run
	// goroutine, so the later stopFileWatcher wait (<-done) hung OnStop
	// forever on a channel nobody closes.
	for _, root := range roots {
		if err := watcher.Add(root.Path); err != nil {
			_ = watcher.Close()
			return fmt.Errorf("watch root %s: %w", root.Path, err)
		}
		if dir := filepath.Join(root.Path, sporemindDirName); dirExists(dir) {
			if err := addWatchTree(watcher, dir); err != nil {
				_ = watcher.Close()
				return fmt.Errorf("watch %s subtree: %w", dir, err)
			}
		}
	}
	done := make(chan struct{})
	a.fileWatcherMu.Lock()
	a.fileWatcher = watcher
	a.fileWatcherDone = done
	a.dynWatchRefs = make(map[string]int)
	a.fileWatcherMu.Unlock()
	go a.runFileWatcher(ctx, watcher, done)
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (a *Actor) stopFileWatcher() {
	a.fileWatcherMu.Lock()
	watcher := a.fileWatcher
	done := a.fileWatcherDone
	a.fileWatcher = nil
	a.fileWatcherDone = nil
	a.dynWatchRefs = nil
	a.fileWatcherMu.Unlock()
	if watcher != nil {
		_ = watcher.Close()
	}
	if done != nil {
		<-done
	}
}

func (a *Actor) runFileWatcher(ctx actor.Context, watcher *fsnotify.Watcher, done chan struct{}) {
	defer close(done)
	last := make(map[string]time.Time)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if isIgnoredWatchPath(event.Name) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					// Only grow the recursive watch inside the .sporecode
					// subtree; new top-level directories stay unwatched
					// (scope contract, see startFileWatcher).
					if a.isSporemindSubtree(event.Name) {
						_ = addWatchTree(watcher, event.Name)
					}
					continue
				}
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			path := a.projectEventPath(event.Name)
			now := time.Now()
			if previous, ok := last[path]; ok && now.Sub(previous) < fileWatchDebounce {
				continue
			}
			last[path] = now
			_ = ctx.EmitEvent("file_changed", gen.ProjectFileChangedEvent{
				Path: path,
				Kind: fileWatchKind(event.Op),
			})
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// handleWatchFile implements project.watch_file: adds (Remove=false) or drops
// (Remove=true) an on-demand watch so file_changed events keep flowing for a
// file outside the always-watched .sporecode subtree.
func (a *Actor) handleWatchFile(ctx actor.PureContext, req gen.ProjectWatchFileReq) error {
	if req.Path == "" {
		return fmt.Errorf("project.watch_file: path is required")
	}
	cid := callerID(ctx)
	p, err := a.resolveBrowsePathFor(cid, req.Path)
	if err != nil {
		return err
	}
	return a.watchFileFor(cid, filepath.Clean(p), req.Remove)
}

// watchFileFor is the testable core of project.watch_file. The file's PARENT
// directory is watched (not the file itself) so editors that save via
// temp-file + rename keep the watch alive across the recreate. Refcounts let
// several viewers share one directory watch; the watch is dropped when the
// last viewer goes away. Paths already covered by the recursive subtree or
// root watch are ignored (adding then removing them would tear down watches
// owned by the tree watcher).
func (a *Actor) watchFileFor(callerID, path string, remove bool) error {
	dir := filepath.Dir(path)
	if isIgnoredWatchPath(dir) {
		return nil
	}
	if a.isSporemindSubtree(dir) || a.isRootPath(dir) {
		return nil
	}
	a.fileWatcherMu.Lock()
	watcher := a.fileWatcher
	if remove {
		if n := a.dynWatchRefs[dir]; n > 1 {
			a.dynWatchRefs[dir] = n - 1
			a.fileWatcherMu.Unlock()
			return nil
		}
		delete(a.dynWatchRefs, dir)
		a.fileWatcherMu.Unlock()
		if watcher != nil {
			_ = watcher.Remove(dir)
		}
		return nil
	}
	a.fileWatcherMu.Unlock()
	if watcher == nil {
		return fmt.Errorf("project.watch_file: file watcher not running")
	}
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("project.watch_file: %w", err)
	}
	a.fileWatcherMu.Lock()
	a.dynWatchRefs[dir]++
	a.fileWatcherMu.Unlock()
	return nil
}

// isRootPath reports whether path is exactly one of the configured roots.
func (a *Actor) isRootPath(path string) bool {
	clean := filepath.Clean(path)
	c, err := a.configSnapshot()
	if err != nil {
		return false
	}
	for _, root := range c.Roots {
		if filepath.Clean(root.Path) == clean {
			return true
		}
	}
	return false
}

// isSporemindSubtree reports whether path is the .sporecode directory of some
// root or lives underneath it.
func (a *Actor) isSporemindSubtree(path string) bool {
	clean := filepath.Clean(path)
	c, err := a.configSnapshot()
	if err != nil {
		return false
	}
	for _, root := range c.Roots {
		rel, err := filepath.Rel(filepath.Clean(root.Path), clean)
		if err != nil {
			continue
		}
		if rel == sporemindDirName || strings.HasPrefix(rel, sporemindDirName+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (a *Actor) projectEventPath(path string) string {
	clean := filepath.Clean(path)
	c, err := a.configSnapshot()
	if err != nil {
		return filepath.ToSlash(clean)
	}
	for _, root := range c.Roots {
		rel, err := filepath.Rel(filepath.Clean(root.Path), clean)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(clean)
}

func addWatchTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root && isIgnoredWatchDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

func isIgnoredWatchPath(path string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if isIgnoredWatchDir(part) {
			return true
		}
	}
	return false
}

func isIgnoredWatchDir(name string) bool {
	_, ok := ignoredWatchDirs[strings.ToLower(name)]
	return ok
}

func fileWatchKind(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Remove != 0:
		return "remove"
	case op&fsnotify.Rename != 0:
		return "rename"
	case op&fsnotify.Create != 0:
		return "create"
	default:
		return "write"
	}
}
