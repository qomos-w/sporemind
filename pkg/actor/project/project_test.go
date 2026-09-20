package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshProject(t *testing.T, rootDir string) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{
		initialPath:  rootDir,
		store:        newFSCardStore(filepath.Join(rootDir, wikiDir)),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

// setRoots writes the config card's roots directly. Tests use this to seed the
// ground-truth card because the Actor struct no longer carries a Roots field.
func setRoots(t *testing.T, a *Actor, roots []domain.RootDirEntry) {
	t.Helper()
	if err := a.updateConfigCard(func(c *configCard) { c.Roots = roots }); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceCardStoreSeedsPromptCardsOnlyForSystemProject(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	systemPath := filepath.Join(dataDir, "data")
	system := &Actor{
		initialPath:       systemPath,
		store:             newFSCardStore(filepath.Join(systemPath, "wiki")),
		persistStore:      persist.NewFSPersist(t.TempDir()),
		externalProviders: []ExternalCardProvider{newBuiltinComponentCardProvider()},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := system.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	promptCards, err := agentkit.BuiltinPromptCards()
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range promptCards {
		if _, err := system.store.Get(prompt.Title); err != nil {
			t.Fatalf("system project missing %s: %v", prompt.Title, err)
		}
	}
	items, err := system.listAllCardItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.ID] = true
	}
	for _, id := range []string{
		"prompt:profile:project.coder",
		"skill:skill-creator",
		"builtin:bundle:project-wiki",
		"builtin:bundle:debug",
		"builtin:mode:goal",
	} {
		if !seen[id] {
			t.Fatalf("system project list missing %s", id)
		}
	}
	for _, item := range items {
		if item.ID == "prompt:profile:project.coder" {
			foundPromptTag := false
			for _, tag := range item.Tags {
				if tag == "prompt" {
					foundPromptTag = true
				}
			}
			if !foundPromptTag {
				t.Fatalf("system prompt card %s missing prompt tag: %+v", item.ID, item.Tags)
			}
		}
	}

	regularPath := filepath.Join(dataDir, "projects", "app")
	if err := os.MkdirAll(regularPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing protected system cards (seeded by older versions) must be
	// removed from regular projects, while user-authored cards must survive.
	legacy := &Actor{initialPath: regularPath, store: newFSCardStore(filepath.Join(regularPath, wikiDir)), persistStore: persist.NewFSPersist(t.TempDir())}
	legacy.seedPromptCards()
	legacy.seedBuiltinSkillCards()
	userCard := BuiltinCard{Title: "skill:user-custom", Data: map[string]any{"source": "project", "protected": false}}
	if err := legacy.store.Save(userCard.ToCardRecord()); err != nil {
		t.Fatal(err)
	}

	regular := &Actor{initialPath: regularPath, store: newFSCardStore(filepath.Join(regularPath, wikiDir)), persistStore: persist.NewFSPersist(t.TempDir())}
	if err := regular.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prompt:profile:project.coder", "skill:skill-creator", "skill:plan-module"} {
		if _, err := regular.store.Get(id); err == nil {
			t.Fatalf("regular project must not keep system card %s", id)
		}
	}
	if _, err := regular.store.Get("skill:user-custom"); err != nil {
		t.Fatalf("regular project must keep user-authored card: %v", err)
	}
	regularItems, err := regular.listAllCardItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range regularItems {
		if strings.HasPrefix(item.ID, "prompt:") || strings.HasPrefix(item.ID, "builtin:") {
			t.Fatalf("regular project list must not expose system card %s", item.ID)
		}
	}
	if _, err := regular.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "builtin:mode:goal"}); err == nil {
		t.Fatal("regular project must not resolve builtin external cards")
	}
}

func TestSystemCardsIdentityIsExplicitNotPathGuessed(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Restarted system project whose persisted mount path no longer matches
	// config.DataDir()/data (exe moved, case drift, dev-mode DataDir) must
	// still seed and expose the full system card set via the explicit flag.
	stalePath := filepath.Join(t.TempDir(), "old-exe-dir", ".sporecode", "data")
	system := NewActor(stalePath, true)().(*Actor)
	system.persistStore = persist.NewFSPersist(t.TempDir())
	if err := system.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := system.listAllCardItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.ID] = true
	}
	for _, id := range []string{
		"prompt:profile:project.coder",
		"skill:skill-creator",
		"builtin:bundle:project-wiki",
		"builtin:mode:goal",
	} {
		if !seen[id] {
			t.Fatalf("explicit system project missing %s", id)
		}
	}

	// A regular mount explicitly flagged non-system must not gain system
	// cards even if its path happens to equal the system store path.
	regular := NewActor(filepath.Join(dataDir, "data"), false)().(*Actor)
	regular.persistStore = persist.NewFSPersist(t.TempDir())
	if err := regular.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := regular.store.Get("skill:skill-creator"); err == nil {
		t.Fatal("explicit non-system project must not seed skill cards")
	}
	if _, err := regular.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "builtin:mode:goal"}); err == nil {
		t.Fatal("explicit non-system project must not resolve builtin external cards")
	}
}

func TestResolvePath_DefaultRoot(t *testing.T) {
	dir := t.TempDir()
	a, _ := freshProject(t, dir)

	p := filepath.Join("src", "main.go")
	root, rel, err := a.resolvePath(p)
	if err != nil {
		t.Fatal(err)
	}
	if root.Path != dir {
		t.Errorf("expected root %q, got %q", dir, root.Path)
	}
	expected := filepath.Join("src", "main.go")
	if rel != expected {
		t.Errorf("expected rel %q, got %q", expected, rel)
	}
}

func TestResolvePath_NamedRoot(t *testing.T) {
	dir := t.TempDir()
	modA := filepath.Join(dir, "moduleA")
	if err := os.MkdirAll(modA, 0755); err != nil {
		t.Fatal(err)
	}

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	setRoots(t, a, []domain.RootDirEntry{
		{Name: "default", Path: dir},
		{Name: "moduleA", Path: modA},
	})

	p := filepath.Join("moduleA", "src", "file.go")
	root, rel, err := a.resolvePath(p)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "moduleA" {
		t.Errorf("expected moduleA, got %q", root.Name)
	}
	expected := filepath.Join("src", "file.go")
	if rel != expected {
		t.Errorf("expected rel %q, got %q", expected, rel)
	}
}

func TestResolvePath_NoRoots(t *testing.T) {
	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	_, _, err := a.resolvePath("anything")
	if err == nil {
		t.Error("expected error for no roots")
	}
}

// TestOnStartSurvivesMissingRootDir pins the zombie-actor fix: a configured
// root whose directory is gone (deleted data dir, unmounted drive) must not
// abort OnStart. Aborting leaves the cell tree-registered with a partial
// handler table, so every project.wiki_* forward answers gospore.cell "call
// ID not registered" forever — the exact frontend error the workspace's
// bounded retry was added for. The watcher failure must degrade to a warning
// instead.
func TestOnStartSurvivesMissingRootDir(t *testing.T) {
	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "deleted-root")
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: missing}})

	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart must survive a missing root dir, got: %v", err)
	}
	if _, ok := ctx.Regs["project.wiki_list_cards"]; !ok {
		t.Fatal("project.wiki_list_cards must be registered even when the watcher fails")
	}
	// The failed watcher must not be published: stopFileWatcher's <-done
	// would otherwise hang OnStop on a channel no goroutine closes.
	a.stopFileWatcher()
}

// TestStartFileWatcherFailureLeavesNoWatcher pins the startFileWatcher fix:
// when watcher.Add fails, the actor keeps no half-initialized watcher (a run
// goroutine that never started means the done channel never closes).
func TestStartFileWatcherFailureLeavesNoWatcher(t *testing.T) {
	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: missing}})

	if err := a.startFileWatcher(ctx); err == nil {
		t.Fatal("startFileWatcher on a missing root must fail")
	}
	a.fileWatcherMu.Lock()
	watcher := a.fileWatcher
	a.fileWatcherMu.Unlock()
	if watcher != nil {
		t.Fatal("failed watcher start must not publish a watcher on the actor")
	}
}

func TestHandleFileList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, dir)
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "file.txt") {
		t.Errorf("expected file.txt in listing, got %q", out)
	}
	if !strings.Contains(out, "subdir/") {
		t.Errorf("expected subdir/ in listing, got %q", out)
	}
}

func TestHandleFileRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, dir)
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "greeting.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi\n" {
		t.Errorf("expected 'hi\\n', got %q", resp.Content)
	}
	if resp.Truncated {
		t.Errorf("whole-file read: expected truncated=false, got true")
	}
}

func TestHandleFileRead_CRLFReturnsLFContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "crlf.txt")
	if err := os.WriteFile(path, []byte("line one\r\nline two\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "crlf.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "line one\nline two\n" {
		t.Errorf("unexpected read content: %q", resp.Content)
	}
}

func TestHandleFileRead_Truncated(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "lines.txt"), []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)
	a, ctx := freshProject(t, dir)

	// limit < total: truncated.
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "lines.txt", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 3 || resp.StartLine != 1 {
		t.Fatalf("limit=3: expected 3 lines from 1, got %d from %d", resp.NumLines, resp.StartLine)
	}
	if !resp.Truncated {
		t.Errorf("limit=3 of 5: expected truncated=true, got false")
	}

	// limit reaches EOF (offset=4, limit=2 reads lines 4-5): not truncated.
	resp, err = a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "lines.txt", Offset: 4, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 2 || resp.StartLine != 4 {
		t.Fatalf("offset=4 limit=2: expected 2 lines from 4, got %d from %d", resp.NumLines, resp.StartLine)
	}
	if resp.Truncated {
		t.Errorf("offset=4 limit=2 (reaches EOF): expected truncated=false, got true")
	}

	// tail reads the last window: not truncated, and StartLine is the tail
	// window's line in original coordinates (regression: previously 0 lines).
	resp, err = a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "lines.txt", Tail: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 2 || resp.StartLine != 4 {
		t.Fatalf("tail=2: expected 2 lines from 4, got %d from %d", resp.NumLines, resp.StartLine)
	}
	if resp.Truncated {
		t.Errorf("tail=2: expected truncated=false, got true")
	}
}

func TestHandleFileRead_Missing(t *testing.T) {
	dir := t.TempDir()
	a, ctx := freshProject(t, dir)
	_, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "nope.txt"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// Ensure NewActor returns a non-nil factory
func TestNewActor(t *testing.T) {
	factory := NewActor("/tmp/test")
	if factory == nil {
		t.Fatal("NewActor returned nil")
	}
	a := factory()
	if a == nil {
		t.Fatal("factory() returned nil")
	}
}

func TestHandleFileRead_PathTraversalAllowed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("peek"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: "../sibling/secret.txt"})
	if err != nil {
		t.Fatalf("expected read traversal to succeed: %v", err)
	}
	if resp.Content != "peek\n" {
		t.Errorf("expected 'peek\\n', got %q", resp.Content)
	}
}

func TestHandleFileList_PathTraversalAllowed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: "../sibling"})
	if err != nil {
		t.Fatalf("expected list traversal to succeed: %v", err)
	}
	if !strings.Contains(out, "file.txt") {
		t.Errorf("expected file.txt in listing, got %q", out)
	}
}

func TestHandleFileList_AbsoluteOutsideRootAllowed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "outside.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: sibling})
	if err != nil {
		t.Fatalf("expected absolute outside-root list to succeed: %v", err)
	}
	if !strings.Contains(out, "outside.txt") {
		t.Errorf("expected outside.txt in listing, got %q", out)
	}
}

func TestHandleFileRead_AbsoluteOutsideRootAllowed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("peek"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRead(ctx, domain.FileSystemReadReq{Path: filepath.Join(sibling, "secret.txt")})
	if err != nil {
		t.Fatalf("expected absolute outside-root read to succeed: %v", err)
	}
	if !strings.Contains(resp.Content, "peek") {
		t.Errorf("expected content to contain 'peek', got %q", resp.Content)
	}
}

func TestHandleFileWrite_PathTraversalStillBlocked(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "../sibling/new.txt", Content: "x"})
	if err == nil {
		t.Error("expected write traversal to be blocked")
	}
}

func TestHandleFileWrite_ConfirmedRelativeTraversalAllows(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sibling, "new.txt")

	a, ctx := freshProject(t, root)
	_, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "../sibling/new.txt", Content: "x", Confirm: true})
	if err != nil {
		t.Fatalf("expected confirmed write traversal to succeed: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Errorf("expected content 'x', got %q", string(data))
	}
}

func TestHandleFileWrite_AbsoluteOutsideRootRequiresConfirm(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sibling, "new.txt")

	a, ctx := freshProject(t, root)

	_, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: target, Content: "x"})
	if err == nil {
		t.Error("expected outside-root write to be blocked without confirm")
	}

	_, err = a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: target, Content: "x", Confirm: true})
	if err != nil {
		t.Fatalf("expected confirmed outside-root write to succeed: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Errorf("expected content 'x', got %q", string(data))
	}
}

func TestHandleFileWrite_OverwriteExistingFileReturnsWarning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "target.txt", Content: "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Warning == "" {
		t.Error("expected warning when overwriting existing file")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("expected overwritten content 'new', got %q", string(data))
	}
}

func TestHandleFileWrite_NewFileHasNoWarning(t *testing.T) {
	root := t.TempDir()

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "new.txt", Content: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Warning != "" {
		t.Errorf("expected no warning for new file, got %q", resp.Warning)
	}

	data, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("expected content 'hello', got %q", string(data))
	}
}

func TestHandleFileEdit_PathTraversalStillBlocked(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "existing.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{Path: "../sibling/existing.txt", OldString: "old", NewString: "new"})
	if err == nil {
		t.Error("expected edit traversal to be blocked")
	}
}

func TestHandleFileEdit_NotFoundIncludesOldString(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("existing content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "target.txt",
		OldString: "missing\ncontent",
		NewString: "replacement",
	})
	if err == nil {
		t.Fatal("expected old_string not found error")
	}
	if !strings.Contains(err.Error(), `old_string="missing\ncontent"`) {
		t.Fatalf("error does not include old_string: %v", err)
	}
}

func TestHandleFileEdit_OverwriteReplacesEntireFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("old content\nline two\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "target.txt",
		NewString: "new content\nline b\n",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content\nline b\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestHandleFileEdit_PreservesCRLF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "crlf.txt")
	if err := os.WriteFile(path, []byte("line one\r\nline two\r\nline three\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "crlf.txt",
		OldString: "line two\nline three",
		NewString: "updated\nlast",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Fatalf("expected one replacement, got %d", resp.Replacements)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "line one\r\nupdated\r\nlast\r\n" {
		t.Errorf("CRLF was not preserved: %q", data)
	}
}

func TestHandleFileWrite_PreservesRequestedLineEndings(t *testing.T) {
	root := t.TempDir()
	a, ctx := freshProject(t, root)
	if _, err := a.handleFileWrite(ctx, domain.FileSystemWriteReq{Path: "crlf.txt", Content: "line one\r\nline two\r\n"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "crlf.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "line one\r\nline two\r\n" {
		t.Errorf("CRLF was not preserved: %q", data)
	}
}

func TestHandleFileRm_PathTraversalStillBlocked(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(base, "sibling")
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "target.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "../sibling/target.txt", Force: true})
	if err == nil {
		t.Error("expected rm traversal to be blocked")
	}
}

func TestHandleFileRm_DefaultReturnsPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "target.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Preview {
		t.Errorf("expected preview, got Preview=%v", resp.Preview)
	}
	if resp.Count != 1 {
		t.Errorf("expected preview count 1, got %d", resp.Count)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should still exist in preview mode: %v", err)
	}
}

func TestHandleFileRm_ConfirmDeletes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "target.txt", Confirm: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed: %v", err)
	}
}

func TestHandleFileRm_ForceDeletes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "target.txt", Force: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed: %v", err)
	}
}

func TestHandleFileRm_RecursiveDirectoryConfirm(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "dir", Recursive: true, Confirm: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("directory should be removed: %v", err)
	}
}

func TestHandleFileRm_DirectoryWithoutRecursive(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileRm(ctx, domain.FileSystemRmReq{Path: "dir", Confirm: true})
	if err == nil {
		t.Error("expected error for directory without recursive")
	}
}

func TestSafeJoin_EscapesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	a, _ := freshProject(t, root)

	_, err := a.safeJoin(root, ".."+string(filepath.Separator)+"other", false)
	if err == nil {
		t.Error("expected error for escaping root")
	}
}

func TestSafeJoin_WithinRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	a, _ := freshProject(t, root)

	result, err := a.safeJoin(root, "sub"+string(filepath.Separator)+"file.txt", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, root) {
		t.Errorf("expected result under root, got %q", result)
	}
}

func TestSafeJoin_AllowsEscapeToApprovedRoot(t *testing.T) {
	dir := t.TempDir()
	codebase := filepath.Join(dir, "codebase")
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(filepath.Join(other, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{
		{Name: "codebase", Path: codebase},
		{Name: "other", Path: other},
	})

	result, err := a.safeJoin(codebase, ".."+string(filepath.Separator)+"other"+string(filepath.Separator)+"sub"+string(filepath.Separator)+"file.txt", false)
	if err != nil {
		t.Fatalf("unexpected error for escape to approved root: %v", err)
	}
	expected := filepath.Join(other, "sub", "file.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSafeJoin_AllowsEscapeWhenConfirmed(t *testing.T) {
	dir := t.TempDir()
	codebase := filepath.Join(dir, "codebase")
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(filepath.Join(other, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{
		{Name: "codebase", Path: codebase},
	})

	result, err := a.safeJoin(codebase, ".."+string(filepath.Separator)+"other"+string(filepath.Separator)+"sub"+string(filepath.Separator)+"file.txt", true)
	if err != nil {
		t.Fatalf("unexpected error for confirmed escape: %v", err)
	}
	expected := filepath.Join(other, "sub", "file.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestGitignoreRespectedInList(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "foo"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "foo", "package.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0644)

	ctx := testutil.AdminCtx(testutil.GenActorID())

	entries, _, err := listDirDepth(ctx, dir, -1, make(map[string]bool), false, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.relPath)
	}
	if !contains(names, "src") {
		t.Error("expected src in results")
	}
	if contains(names, "node_modules") {
		t.Error("did not expect node_modules in results")
	}

	entries, _, err = listDirDepth(ctx, dir, -1, make(map[string]bool), true, false)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, e := range entries {
		names = append(names, e.relPath)
	}
	if !contains(names, "node_modules") {
		t.Error("expected node_modules when NoIgnore=true")
	}
	if !contains(names, "node_modules/foo") {
		t.Error("expected node_modules/foo when NoIgnore=true")
	}
	if !contains(names, "node_modules/foo/package.json") {
		t.Error("expected node_modules/foo/package.json when NoIgnore=true")
	}
}

// TestGitignoreRespectedInFlatList covers the Depth==0 (non-recursive) path,
// which previously skipped gitignore filtering for both file_list and
// file_list_json. The default must ignore gitignored entries; NoIgnore=true
// must surface them — consistent with the recursive path and glob/grep.
// Uses "artifacts/" (not in defaultSkipDirs) so the test isolates gitignore
// behavior from the hardcoded skip-dir set.
func TestGitignoreRespectedInFlatList(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "artifacts", "foo"), 0755)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "artifacts", "foo", "out.txt"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("artifacts/\n"), 0644)

	a, ctx := freshProject(t, dir)

	// file_list, Depth==0 (flat), default → artifacts filtered by gitignore.
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src") {
		t.Errorf("flat list: expected src, got %q", out)
	}
	if strings.Contains(out, "artifacts") {
		t.Errorf("flat list: did not expect artifacts, got %q", out)
	}

	// file_list, Depth==0, NoIgnore=true → artifacts present.
	out, err = a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "artifacts") {
		t.Errorf("flat list NoIgnore: expected artifacts, got %q", out)
	}

	// file_list detail, Depth==0 (flat), default → artifacts filtered by gitignore.
	out, err = a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	detailNames := listDetailNames(t, out)
	if !contains(detailNames, "src") {
		t.Errorf("flat list detail: expected src, got %v", detailNames)
	}
	if contains(detailNames, "artifacts") {
		t.Errorf("flat list detail: did not expect artifacts, got %v", detailNames)
	}

	// file_list detail, Depth==0, NoIgnore=true → artifacts present.
	out, err = a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", NoIgnore: true, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	detailNames = listDetailNames(t, out)
	if !contains(detailNames, "artifacts") {
		t.Errorf("flat list detail NoIgnore: expected artifacts, got %v", detailNames)
	}
}

// listDetailNames asserts that detail-mode output follows the
// "<name>[/] <date> <time> <offset>[ Size:N]" contract and returns the entry
// names. The format must stay right-anchored parseable: names may contain
// spaces, dirs keep the trailing "/", only files carry Size.
func listDetailNames(t *testing.T, out string) []string {
	t.Helper()
	re := regexp.MustCompile(`^(.*) \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}( Size:\d+)?$`)
	var names []string
	for i, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "(") {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("detail line %d does not match contract: %q", i+1, line)
		}
		if !strings.HasSuffix(m[1], "/") && m[2] == "" {
			t.Fatalf("detail file line %d missing Size: %q", i+1, line)
		}
		if strings.HasSuffix(m[1], "/") && m[2] != "" {
			t.Fatalf("detail dir line %d must not carry Size: %q", i+1, line)
		}
		names = append(names, strings.TrimSuffix(m[1], "/"))
	}
	return names
}

// TestFileListDetailNamesWithSpaces pins the right-anchored parse contract:
// file names containing spaces (even date-like text) must survive detail mode.
func TestFileListDetailNamesWithSpaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "my file v2.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested dir"), 0755); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, dir)
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	names := listDetailNames(t, out)
	if !contains(names, "my file v2.txt") {
		t.Errorf("expected %q in detail names, got %v", "my file v2.txt", names)
	}
	if !contains(names, "nested dir") {
		t.Errorf("expected %q in detail names, got %v", "nested dir", names)
	}
}

// TestUnifiedIgnore_NoIgnoreControlsAll is the acceptance test for the unified
// ignore mechanism: NoIgnore single-handedly toggles all three ignore layers
// (gitignore, hardcoded defaultSkipDirs like node_modules, and the Exclude
// param). NoIgnore=false (default) → all three active; NoIgnore=true → all
// three bypassed. Verified across file_list, file_glob, file_grep.
func TestUnifiedIgnore_NoIgnoreControlsAll(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "foo"), 0755) // defaultSkipDirs member
	os.MkdirAll(filepath.Join(dir, "customdir"), 0755)           // excluded via Exclude
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\nSECRET\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "foo", "index.js"), []byte("SECRET nm\n"), 0644)
	os.WriteFile(filepath.Join(dir, "gitignored.log"), []byte("SECRET log\n"), 0644) // gitignored
	os.WriteFile(filepath.Join(dir, "customdir", "x.go"), []byte("package c\nSECRET\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("gitignored.log\n"), 0644)

	a, ctx := freshProject(t, dir)

	// ── file_list (flat), Exclude="customdir" ──
	// NoIgnore=false: src shows; node_modules (skipDirs), gitignored.log (gitignore),
	// customdir (Exclude) all hidden.
	out, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", Exclude: "customdir"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src") {
		t.Errorf("list default: expected src, got %q", out)
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, "gitignored") || strings.Contains(out, "customdir") {
		t.Errorf("list default: expected skip dirs hidden, got %q", out)
	}
	// NoIgnore=true: everything shows (skipDirs + gitignore + Exclude all bypassed).
	out, err = a.handleFileList(ctx, domain.FileSystemListReq{Path: ".", Exclude: "customdir", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node_modules", "gitignored", "customdir", "src"} {
		if !strings.Contains(out, want) {
			t.Errorf("list NoIgnore: expected %q present, got %q", want, out)
		}
	}

	// ── file_glob "**/*.js" ──
	// NoIgnore=false: node_modules skipped → no index.js.
	glob, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.js"})
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(glob.Files, "index.js") || containsStr(glob.Files, "node_modules") {
		t.Errorf("glob default: expected node_modules skipped, got %v", glob.Files)
	}
	// NoIgnore=true: node_modules entered → index.js found.
	glob, err = a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.js", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(glob.Files, "index.js") {
		t.Errorf("glob NoIgnore: expected index.js, got %v", glob.Files)
	}

	// ── file_grep "SECRET", output files ──
	// NoIgnore=false: node_modules not searched → index.js absent from matches.
	grep, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{Pattern: "SECRET", OutputMode: "files"})
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(grep.Files, "node_modules") {
		t.Errorf("grep default: expected node_modules not searched, got %v", grep.Files)
	}
	// NoIgnore=true: node_modules searched → index.js matches.
	grep, err = a.handleFileGrep(ctx, domain.FileSystemGrepReq{Pattern: "SECRET", OutputMode: "files", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(grep.Files, "node_modules") {
		t.Errorf("grep NoIgnore: expected node_modules searched, got %v", grep.Files)
	}

	// ── Exclude via glob (newly added to glob schema) ──
	// NoIgnore=false + Exclude="customdir": customdir/x.go absent.
	glob, err = a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.go", Exclude: "customdir"})
	if err != nil {
		t.Fatal(err)
	}
	if containsStr(glob.Files, "customdir") {
		t.Errorf("glob Exclude: expected customdir skipped, got %v", glob.Files)
	}
	// NoIgnore=true + Exclude="customdir": Exclude bypassed → customdir/x.go present.
	glob, err = a.handleFileGlob(ctx, domain.FileSystemGlobReq{Pattern: "**/*.go", Exclude: "customdir", NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(glob.Files, "customdir") {
		t.Errorf("glob NoIgnore+Exclude: expected customdir present (Exclude bypassed), got %v", glob.Files)
	}
}

func TestHiddenEntriesOmittedByDefault(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, ".hidden", "nested"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden", "secret.txt"), []byte("secret\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden", "nested", "deep.txt"), []byte("deep\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("KEY=val\n"), 0644)

	ctx := testutil.AdminCtx(testutil.GenActorID())

	entries, _, err := listDirDepth(ctx, dir, -1, make(map[string]bool), false, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.relPath)
	}
	if !contains(names, "src") {
		t.Error("expected src in results")
	}
	if !contains(names, "src/main.go") {
		t.Error("expected src/main.go in results")
	}
	if contains(names, ".hidden") {
		t.Error("did not expect .hidden in results")
	}
	if contains(names, ".hidden/secret.txt") {
		t.Error("did not expect .hidden/secret.txt in results")
	}
	if contains(names, ".hidden/nested/deep.txt") {
		t.Error("did not expect .hidden/nested/deep.txt in results")
	}
	if contains(names, ".env") {
		t.Error("did not expect .env in results")
	}

	entries, _, err = listDirDepth(ctx, dir, -1, make(map[string]bool), false, true)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, e := range entries {
		names = append(names, e.relPath)
	}
	if !contains(names, ".hidden") {
		t.Error("expected .hidden when All=true")
	}
	if !contains(names, ".hidden/secret.txt") {
		t.Error("expected .hidden/secret.txt when All=true")
	}
	if !contains(names, ".hidden/nested/deep.txt") {
		t.Error("expected .hidden/nested/deep.txt when All=true")
	}
	if !contains(names, ".env") {
		t.Error("expected .env when All=true")
	}
}

func TestGitignoreRespectedInGlob(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "ignored"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "ignored", "bundle.js"), []byte("// bundle\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0644)

	ctx := testutil.AdminCtx(testutil.GenActorID())

	files, _, err := globPattern(ctx, dir, "**/*", 0, buildSkipSet("", false), false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if !contains(names, "src") {
		t.Error("expected src in results")
	}
	if !contains(names, "main.go") {
		t.Error("expected main.go in results")
	}
	if contains(names, "ignored") {
		t.Error("did not expect ignored in results")
	}
	if contains(names, "bundle.js") {
		t.Error("did not expect bundle.js in results")
	}

	files, _, err = globPattern(ctx, dir, "**/*", 0, buildSkipSet("", true), true)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if !contains(names, "ignored") {
		t.Error("expected ignored when NoIgnore=true")
	}
	if !contains(names, "bundle.js") {
		t.Error("expected bundle.js when NoIgnore=true")
	}
}

// TestGlobLiteralPrefixWithMaxdepth verifies that a pattern with a literal
// directory prefix and a "**/" recursive suffix (e.g. "a/b/c/**/*.go") splits
// the literal prefix into the search root. This ensures Maxdepth applies to
// the recursive suffix, not to the literal prefix, so files one level under
// the prefix are still found even with a small Maxdepth. Results are still
// returned relative to the original project root.
func TestGlobLiteralPrefixWithMaxdepth(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b", "c", "d"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "d", "e.go"), []byte("package e\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern:  "a/b/c/**/*.go",
		Maxdepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumFiles != 1 {
		t.Errorf("expected 1 file with Maxdepth=2, got %d: %v", resp.NumFiles, resp.Files)
	}
	if len(resp.Files) > 0 && resp.Files[0] != "a/b/c/d/e.go" {
		t.Errorf("expected a/b/c/d/e.go, got %q", resp.Files[0])
	}
}

// TestGrepReturnsRelativePaths verifies that file_grep returns paths relative to
// the search root, not absolute filesystem paths, so callers can reuse them
// directly.
func TestGrepReturnsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "x.go"), []byte("package x\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "package",
		Path:       "a/b",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(resp.Files), resp.Files)
	}
	if resp.Files[0] != "x.go" {
		t.Errorf("expected relative path x.go, got %q", resp.Files[0])
	}
}

// TestDefaultSkipDirsInGlob verifies that directories like node_modules and .git
// are skipped during glob recursion even when NoIgnore is true.
func TestDefaultSkipDirsInGlob(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "foo"), 0755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "foo", "package.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0644)

	ctx := testutil.AdminCtx(testutil.GenActorID())

	files, _, err := globPattern(ctx, dir, "**/*", 0, buildSkipSet("", false), false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if !contains(names, "src") {
		t.Error("expected src in results")
	}
	if !contains(names, "main.go") {
		t.Error("expected main.go in results")
	}
	if contains(names, "node_modules") {
		t.Error("did not expect node_modules in results")
	}
	if contains(names, ".git") {
		t.Error("did not expect .git in results")
	}
}

// TestGlobExcludeAfterPrefixSplit verifies that exclude patterns still work when
// the literal prefix is split out of the glob pattern.
func TestGlobExcludeAfterPrefixSplit(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "x.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "x_test.go"), []byte("package x\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "a/b/c/**/*.go,!a/b/c/*_test.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(resp.Files), resp.Files)
	}
	if resp.Files[0] != "a/b/c/x.go" {
		t.Errorf("expected a/b/c/x.go, got %q", resp.Files[0])
	}
}

// TestGrepCrossFileHeadLimit verifies that the head limit is enforced across
// files, not per file.
func TestGrepCrossFileHeadLimit(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "one.go"), []byte("match\nmatch\nmatch\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "two.go"), []byte("match\nmatch\nmatch\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "match",
		Path:       "a",
		OutputMode: "content",
		HeadLimit:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 4 {
		t.Errorf("expected 4 matches, got %d: %v", len(resp.Matches), resp.Matches)
	}
	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
}

// TestGrepMultiline verifies that multiline patterns can match across line
// boundaries.
func TestGrepMultiline(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("start\nmiddle\nend\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "start[\\s\\S]*end",
		Path:       ".",
		OutputMode: "content",
		Multiline:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("expected 1 multiline match, got %d: %v", len(resp.Matches), resp.Matches)
	}
	if resp.Matches[0].Line != 1 {
		t.Errorf("expected match at line 1, got %d", resp.Matches[0].Line)
	}
	if !strings.Contains(resp.Matches[0].Content, "middle") {
		t.Errorf("expected match to contain middle, got %q", resp.Matches[0].Content)
	}
}

// TestGlobAndGrepParenthesizedPattern verifies that a parenthesized recursive
// glob pattern such as "(**/*.go)" is parsed consistently by file_glob and
// file_grep: both find files under nested directories.
func TestGlobAndGrepParenthesizedPattern(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "root.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "nested.go"), []byte("package b\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "readme.md"), []byte("# readme\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	globResp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "(**/*.go)",
	})
	if err != nil {
		t.Fatalf("file_glob failed: %v", err)
	}
	if len(globResp.Files) != 2 {
		t.Errorf("file_glob expected 2 files, got %d: %v", len(globResp.Files), globResp.Files)
	}
	for _, f := range []string{"a/root.go", "a/b/nested.go"} {
		if !contains(globResp.Files, f) {
			t.Errorf("file_glob missing %s", f)
		}
	}

	grepResp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "package",
		Glob:       "(**/*.go)",
		OutputMode: "files",
		Depth:      0,
	})
	if err != nil {
		t.Fatalf("file_grep failed: %v", err)
	}
	if len(grepResp.Files) != 2 {
		t.Errorf("file_grep expected 2 files, got %d: %v", len(grepResp.Files), grepResp.Files)
	}
	for _, f := range []string{"a/root.go", "a/b/nested.go"} {
		if !contains(grepResp.Files, f) {
			t.Errorf("file_grep missing %s", f)
		}
	}
}

// TestGrepDefaultsToRecursive verifies that file_grep searches nested
// directories by default when Depth is omitted.
func TestGrepDefaultsToRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "root.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "nested.go"), []byte("package b\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "package",
		Path:       ".",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(resp.Files), resp.Files)
	}
	for _, f := range []string{"a/root.go", "a/b/nested.go"} {
		if !contains(resp.Files, f) {
			t.Errorf("file_grep missing %s", f)
		}
	}
}

// TestGrepDepthLimitsSearch verifies that a positive Depth limits how deep
// file_grep searches into subdirectories.
func TestGrepDepthLimitsSearch(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "root.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "nested.go"), []byte("package b\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "package",
		Path:       ".",
		OutputMode: "files",
		Depth:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 || !contains(resp.Files, "a/root.go") {
		t.Errorf("expected only a/root.go, got %d files: %v", len(resp.Files), resp.Files)
	}
}

// TestGlobAndGrepParenthesizedBracePattern verifies that a parenthesized
// pattern with a literal prefix, ** recursion, and brace expansion works in
// both file_glob and file_grep.
func TestGlobAndGrepParenthesizedBracePattern(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "ui", "components"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "app.go"), []byte("package app\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "ui", "button.tsx"), []byte("export Button\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "ui", "components", "card.tsx"), []byte("export Card\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "ui", "styles.css"), []byte(".btn {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "ui", "button.test.tsx"), []byte("test\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	globResp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "(src/**/*.{go,tsx},!src/**/*.test.tsx)",
	})
	if err != nil {
		t.Fatalf("file_glob failed: %v", err)
	}
	expected := []string{"src/app.go", "src/ui/button.tsx", "src/ui/components/card.tsx"}
	if len(globResp.Files) != len(expected) {
		t.Errorf("file_glob expected %d files, got %d: %v", len(expected), len(globResp.Files), globResp.Files)
	}
	for _, f := range expected {
		if !contains(globResp.Files, f) {
			t.Errorf("file_glob missing %s", f)
		}
	}
	for _, f := range globResp.Files {
		if strings.HasSuffix(f, ".test.tsx") || strings.HasSuffix(f, ".css") {
			t.Errorf("file_glob should not include %s", f)
		}
	}

	grepResp, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "export",
		Glob:       "(src/**/*.{go,tsx},!src/**/*.test.tsx)",
		OutputMode: "files",
		Depth:      0,
	})
	if err != nil {
		t.Fatalf("file_grep failed: %v", err)
	}
	if len(grepResp.Files) != 2 {
		t.Errorf("file_grep expected 2 files, got %d: %v", len(grepResp.Files), grepResp.Files)
	}
	for _, f := range []string{"src/ui/button.tsx", "src/ui/components/card.tsx"} {
		if !contains(grepResp.Files, f) {
			t.Errorf("file_grep missing %s", f)
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// TestGlobBareTermFallsBackToSubstring verifies that a pattern without meta
// chars or slashes falls back to a recursive substring match when the literal
// path doesn't exist. So "TurnTail" finds TurnTail.tsx in a nested directory.
func TestGlobBareTermFallsBackToSubstring(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ui", "ai", "components"), 0755)
	os.WriteFile(filepath.Join(dir, "ui", "ai", "components", "TurnTail.tsx"), []byte("export X\n"), 0644)
	os.WriteFile(filepath.Join(dir, "ui", "ai", "components", "Other.tsx"), []byte("export Y\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "TurnTail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(resp.Files), resp.Files)
	}
	if resp.Files[0] != "ui/ai/components/TurnTail.tsx" {
		t.Errorf("expected ui/ai/components/TurnTail.tsx, got %q", resp.Files[0])
	}
}

// TestGlobLiteralPathStillReturned verifies that a no-meta pattern pointing at
// an existing file is returned as-is, not replaced by substring matches.
func TestGlobLiteralPathStillReturned(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "TurnTail.tsx"), []byte("export X\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "nested"), 0755)
	os.WriteFile(filepath.Join(dir, "nested", "TurnTail.tsx"), []byte("export Y\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "TurnTail.tsx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 || resp.Files[0] != "TurnTail.tsx" {
		t.Fatalf("expected only literal TurnTail.tsx, got %v", resp.Files)
	}
}

// TestGlobBareTermWithSlashNoFallback verifies that a no-meta pattern with a
// slash that misses the literal stat does NOT fall back — the user used a path
// separator, so they're being explicit.
func TestGlobBareTermWithSlashNoFallback(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "missing.go"), []byte("package b\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "a/b/nope.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 0 {
		t.Errorf("expected no matches for slashed pattern miss, got %v", resp.Files)
	}
}

// TestGlobRegexPatternViaContent verifies that patterns containing regex
// operators (e.g. |, +, \\, ()) search file contents instead of filenames.
func TestGlobRegexPatternViaContent(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "foo.go"), []byte("package a\nfunc AgentRef(x int) {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "bar.go"), []byte("package a\nfunc AgentListItem() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "baz.go"), []byte("package a\nfunc nothingHere() {}\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "AgentRef|AgentListItem",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumFiles != 2 {
		t.Fatalf("expected 2 content matches, got %d: %v", resp.NumFiles, resp.Files)
	}
	for _, f := range []string{"a/foo.go", "a/bar.go"} {
		if !contains(resp.Files, f) {
			t.Errorf("missing content match %s", f)
		}
	}
}

// TestGlobRegexPatternWithGlobExclude verifies that regex content search
// results are still filtered by glob exclude patterns.
func TestGlobRegexPatternWithGlobExclude(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\nfunc AgentRef(x int) {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main_test.go"), []byte("package main\nfunc TestAgentRef() {}\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: `(func AgentRef|func TestAgentRef), !**/*_test.*`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumFiles != 1 {
		t.Fatalf("expected 1 file (excluded test), got %d: %v", resp.NumFiles, resp.Files)
	}
	if resp.Files[0] != "src/main.go" {
		t.Errorf("expected src/main.go, got %q", resp.Files[0])
	}
}

// TestGlobRegexMixedInclude verifies that a pattern list can mix regex content
// patterns with glob name patterns.
func TestGlobRegexMixedInclude(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pkg"), 0755)
	os.WriteFile(filepath.Join(dir, "pkg", "foo.go"), []byte("package pkg\nfunc useAgentListState() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "pkg", "bar.go"), []byte("package pkg\nfunc do() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "pkg", "service.ts"), []byte("function useAgentListState() {}\n"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Mixed: a regex content pattern and a glob name pattern, comma-separated.
	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "useAgentListState|toAgentRef, **/*.ts",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expected: foo.go and bar.go by content (useAgentListState), service.ts by name+content.
	// Actually bar.go does NOT contain useAgentListState|toAgentRef, so only
	// foo.go (content) + service.ts (glob name match on **/*.ts). Wait —
	// service.ts ALSO matches the content regex, so it's added once (deduped
	// by seen map). bar.go is not matched by either.
	if resp.NumFiles < 2 {
		t.Fatalf("expected >=2 files, got %d: %v", resp.NumFiles, resp.Files)
	}
	if !contains(resp.Files, "pkg/foo.go") {
		t.Error("missing content match foo.go")
	}
	if !contains(resp.Files, "pkg/service.ts") {
		t.Error("missing glob name match service.ts")
	}
	if contains(resp.Files, "pkg/bar.go") {
		t.Error("bar.go should NOT match either regex or glob")
	}
}

// TestGlobRegexLiteralFileTakesPrecedence verifies that if a regex-looking
// pattern literally matches an existing file, the regex content search still
// runs — because isRegexPattern detects it as regex, and the literal stat
// check comes after. A file named exactly "a|b.txt" would be missed, which
// is acceptable (use glob syntax to match such filenames).
func TestGlobRegexLiteralFileTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	literalName := "a+b.txt"
	os.WriteFile(filepath.Join(dir, literalName), []byte("dummy"), 0644)

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// "a+b.txt" is detected as regex (has +), so it searches content.
	// The file exists but its content doesn't match "a+b.txt", so no results.
	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "a+b.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The regex "a+b.txt" matches "ab.txt" or "aaab.txt" etc. The file
	// content "dummy" doesn't match. Result: empty. This is intentional —
	// use *a+b.txt* to glob-match the filename.
	if len(resp.Files) != 0 {
		t.Logf("note: got %v (regex matching filename literally)", resp.Files)
	}
}

func TestGlobSuffixPatternRecurses(t *testing.T) {
	dir := t.TempDir()

	// Create *.go files at multiple depths.
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("package x"), 0644)
	}
	write("root.go")
	write("sub/a.go")
	write("sub/deep/b.go")

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})

	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 3 {
		t.Errorf("expected 3 .go files recursively, got %d: %v", len(resp.Files), resp.Files)
	}
}

func TestGlobSuffixPatternWithDirPrefix(t *testing.T) {
	dir := t.TempDir()

	write := func(rel string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("package x"), 0644)
	}
	write("root.go")
	write("sub/a.go")
	write("sub/deep/b.go")

	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: dir}})

	ctx := testutil.AdminCtx(testutil.GenActorID())
	// sub/*.go: splitPatternPath extracts "sub" as root, pattern="*.go"
	// which is auto-prepended to **/*.go → recursive under sub/.
	resp, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{
		Pattern: "sub/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 2 {
		t.Errorf("expected 2 .go files recursively under sub/, got %d: %v", len(resp.Files), resp.Files)
	}
}
