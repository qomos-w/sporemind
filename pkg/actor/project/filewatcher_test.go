package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestFileWatchHelpers(t *testing.T) {
	if isIgnoredWatchPath(filepath.Join("project", "src", "file.go")) {
		t.Fatal("source file should not be ignored")
	}
	if !isIgnoredWatchPath(filepath.Join("project", "node_modules", "pkg", "index.js")) {
		t.Fatal("node_modules should be ignored")
	}
	if got := fileWatchKind(fsnotify.Write); got != "write" {
		t.Fatalf("write kind = %q", got)
	}
	if got := fileWatchKind(fsnotify.Remove); got != "remove" {
		t.Fatalf("remove kind = %q", got)
	}
}

func TestAddWatchTreeSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err := addWatchTree(watcher, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "changed.go"), []byte("package changed"), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-watcher.Events:
		if event.Name != filepath.Join(root, "src", "changed.go") {
			t.Fatalf("event path = %q", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watched source event")
	}
}

func TestFileWatchDebounceWindow(t *testing.T) {
	if fileWatchDebounce <= 0 || fileWatchDebounce > time.Second {
		t.Fatalf("unexpected debounce window: %s", fileWatchDebounce)
	}
}

func newWatchTestActor(t *testing.T, root string) *Actor {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, sporemindDirName, "wiki"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Path: root}})
	return a
}

func TestIsSporemindSubtree(t *testing.T) {
	root := t.TempDir()
	a := newWatchTestActor(t, root)
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(root, sporemindDirName), true},
		{filepath.Join(root, sporemindDirName, "wiki"), true},
		{filepath.Join(root, sporemindDirName, "wiki", "card.md"), true},
		{filepath.Join(root, "src"), false},
		{filepath.Join(root, "sporemind-adjacent"), false},
		{root, false},
		{filepath.Join(root, "..", "outside"), false},
	}
	for _, c := range cases {
		if got := a.isSporemindSubtree(c.path); got != c.want {
			t.Errorf("isSporemindSubtree(%q) = %v, want %v", c.path, got, c.want)
		}
	}
	if !a.isRootPath(root) {
		t.Error("isRootPath(root) = false, want true")
	}
	if a.isRootPath(filepath.Join(root, "src")) {
		t.Error("isRootPath(src) = true, want false")
	}
}

// TestStartFileWatcherNarrowScope verifies the scope contract: events fire for
// files inside .sporecode and for root-level files (root is watched
// non-recursively), but NOT for files in unwatched subdirectories.
func TestStartFileWatcherNarrowScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, sporemindDirName, "wiki"), 0755); err != nil {
		t.Fatal(err)
	}

	watcher := newTestWatcher(t)
	defer watcher.Close()
	if err := watcher.Add(root); err != nil {
		t.Fatal(err)
	}
	if err := addWatchTree(watcher, filepath.Join(root, sporemindDirName)); err != nil {
		t.Fatal(err)
	}

	// .sporecode subtree file: must fire.
	if err := os.WriteFile(filepath.Join(root, sporemindDirName, "wiki", "card.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, watcher, filepath.Join(root, sporemindDirName, "wiki", "card.md"))

	// Deep unwatched directory: must NOT fire.
	if err := os.MkdirAll(filepath.Join(root, "src", "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "deep", "hidden.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	expectNoEvent(t, watcher, filepath.Join(root, "src", "deep", "hidden.go"))
}

func newTestWatcher(t *testing.T) *fsnotify.Watcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	return watcher
}

func waitForEvent(t *testing.T, watcher *fsnotify.Watcher, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-watcher.Events:
			if event.Name == want {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for event %q", want)
}

func expectNoEvent(t *testing.T, watcher *fsnotify.Watcher, path string) {
	t.Helper()
	select {
	case event := <-watcher.Events:
		if event.Name == path {
			t.Fatalf("unexpected event for unwatched path %q", path)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// drainEvents empties buffered watcher events. Windows emits several events
// per write (data + metadata); leftovers from a previous write would make a
// later expectNoEvent false-positive.
func drainEvents(watcher *fsnotify.Watcher, settle time.Duration) {
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		select {
		case <-watcher.Events:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

// TestWatchFileForDynamicWatch exercises the project.watch_file core: dynamic
// parent-dir watches deliver events, are shared via refcount, and paths inside
// .sporecode or the root are ignored (their watches are owned by the tree
// watcher and must not be torn down).
func TestWatchFileForDynamicWatch(t *testing.T) {
	root := t.TempDir()
	a := newWatchTestActor(t, root)
	watcher := newTestWatcher(t)
	defer watcher.Close()
	a.fileWatcher = watcher
	a.dynWatchRefs = make(map[string]int)

	srcDir := filepath.Join(root, "src")
	missing := filepath.Join(root, "does-not-exist", "main.go")

	// Two viewers of files in the same dir share one watch.
	if err := a.watchFileFor("caller", filepath.Join(srcDir, "a.go"), false); err != nil {
		t.Fatal(err)
	}
	if err := a.watchFileFor("caller", filepath.Join(srcDir, "b.go"), false); err != nil {
		t.Fatal(err)
	}
	if got := a.dynWatchRefs[srcDir]; got != 2 {
		t.Fatalf("refcount = %d, want 2", got)
	}

	// One remove keeps the watch alive.
	if err := a.watchFileFor("caller", filepath.Join(srcDir, "a.go"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, watcher, filepath.Join(srcDir, "b.go"))
	drainEvents(watcher, 300*time.Millisecond)

	// Last remove drops the watch; no further events. The fsnotify Windows
	// backend processes Remove asynchronously, so give it a beat first.
	if err := a.watchFileFor("caller", filepath.Join(srcDir, "b.go"), true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	drainEvents(watcher, 100*time.Millisecond)
	if err := os.WriteFile(filepath.Join(srcDir, "b.go"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	expectNoEvent(t, watcher, filepath.Join(srcDir, "b.go"))
	if _, ok := a.dynWatchRefs[srcDir]; ok {
		t.Fatal("refcount entry should be removed at zero")
	}

	// .sporecode and root paths are ignored — no refcount, no watch churn.
	if err := a.watchFileFor("caller", filepath.Join(root, sporemindDirName, "wiki", "card.md"), false); err != nil {
		t.Fatal(err)
	}
	if err := a.watchFileFor("caller", filepath.Join(root, "README.md"), false); err != nil {
		t.Fatal(err)
	}
	if len(a.dynWatchRefs) != 0 {
		t.Fatalf("dynWatchRefs = %v, want empty", a.dynWatchRefs)
	}

	// Add on a missing directory errors without touching refcounts.
	if err := a.watchFileFor("caller", missing, false); err == nil {
		t.Fatal("expected error for missing directory")
	}
	if len(a.dynWatchRefs) != 0 {
		t.Fatalf("dynWatchRefs = %v, want empty after failed add", a.dynWatchRefs)
	}
}
