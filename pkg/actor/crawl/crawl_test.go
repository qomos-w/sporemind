package crawl

import (
	"errors"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func sampleConfig() Config {
	return Config{
		Seeds:    []string{"https://example.com"},
		MaxDepth: 3,
		MaxPages: 100,
		Mode:     "hidden_window",
	}
}

func TestSubmit_Running(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Task.ID != "crawl-0" {
		t.Errorf("expected id crawl-0, got %q", resp.Task.ID)
	}
	if resp.Task.State != StateRunning {
		t.Errorf("expected state running, got %q", resp.Task.State)
	}
	if len(resp.Task.Frontier) != 1 || resp.Task.Frontier[0] != "https://example.com" {
		t.Errorf("expected frontier seeded with start url, got %v", resp.Task.Frontier)
	}
	if resp.Task.Config.Mode != "hidden_window" {
		t.Errorf("expected mode hidden_window, got %q", resp.Task.Config.Mode)
	}
	if a.nextID != 1 {
		t.Errorf("expected nextID 1, got %d", a.nextID)
	}
	if len(a.tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(a.tasks))
	}
}

func TestSubmit_DefaultMode(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := sampleConfig()
	cfg.Mode = ""
	resp, err := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Task.Config.Mode != "hidden_window" {
		t.Errorf("expected default mode hidden_window, got %q", resp.Task.Config.Mode)
	}
}

func TestSubmit_DeduplicatesSeeds(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := Config{
		Seeds:    []string{"https://example.com", "https://example.com", "https://example.com/about"},
		MaxDepth: 2,
		MaxPages: 10,
		Mode:     "hidden_window",
	}
	resp, err := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Task.Frontier) != 2 {
		t.Errorf("expected 2 unique seeds, got %v", resp.Task.Frontier)
	}
}

func TestSubmit_MissingSeeds(t *testing.T) {
	a, ctx := freshActor(t)
	_, err := a.handleSubmit(ctx, SubmitReq{Config: Config{}})
	if err == nil {
		t.Fatal("expected error for missing seeds")
	}
}

func TestSubmit_InvalidBounds(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := sampleConfig()
	cfg.MaxPages = 0
	_, err := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	if err == nil {
		t.Fatal("expected error for max_pages=0")
	}
}

func TestSubmit_InvalidMode(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := sampleConfig()
	cfg.Mode = "stealth"
	_, err := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestLoginDone(t *testing.T) {
	a, ctx := freshActor(t)
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})

	// Move the task into awaiting_login as the runtime would when it detects a wall.
	_, err := a.handleUpdate(ctx, UpdateReq{ID: resp.Task.ID, State: StateAwaitingLogin})
	if err != nil {
		t.Fatal(err)
	}
	if a.tasks[0].State != StateAwaitingLogin {
		t.Fatalf("expected awaiting_login, got %q", a.tasks[0].State)
	}

	updated, err := a.handleLoginDone(ctx, LoginDoneReq{ID: resp.Task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateRunning {
		t.Errorf("expected running after login_done, got %q", updated.State)
	}
	if a.tasks[0].State != StateRunning {
		t.Errorf("persisted task not updated")
	}
}

func TestCancel(t *testing.T) {
	a, ctx := freshActor(t)
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})
	updated, err := a.handleCancel(ctx, CancelReq{ID: resp.Task.ID, Reason: "user request"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateCancelled {
		t.Errorf("expected cancelled, got %q", updated.State)
	}
	if updated.Error != "user request" {
		t.Errorf("expected reason preserved, got %q", updated.Error)
	}
	// cancelling again should fail because the state is terminal.
	_, err = a.handleCancel(ctx, CancelReq{ID: resp.Task.ID})
	if err == nil {
		t.Fatal("expected error when cancelling a terminal task")
	}
}

func TestMarkFailed(t *testing.T) {
	a, ctx := freshActor(t)
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})
	updated, err := a.handleUpdate(ctx, UpdateReq{
		ID:    resp.Task.ID,
		State: StateFailed,
		Error: "navigation timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateFailed {
		t.Errorf("expected failed, got %q", updated.State)
	}
	if updated.Error != "navigation timeout" {
		t.Errorf("expected error preserved, got %q", updated.Error)
	}
}

func TestMarkDone(t *testing.T) {
	a, ctx := freshActor(t)
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})
	updated, err := a.handleUpdate(ctx, UpdateReq{ID: resp.Task.ID, State: StateDone})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StateDone {
		t.Errorf("expected done, got %q", updated.State)
	}
}

func TestUpdate_RecordCrawlAndFrontier(t *testing.T) {
	a, ctx := freshActor(t)
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})
	id := resp.Task.ID

	updated, err := a.handleUpdate(ctx, UpdateReq{
		ID:         id,
		CrawledURL: "https://example.com",
		Discovered: []string{
			"https://example.com/about",
			"https://example.com/contact",
			"https://example.com", // duplicate, should be ignored
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.CrawledURLs) != 1 || updated.CrawledURLs[0] != "https://example.com" {
		t.Errorf("expected 1 crawled url, got %v", updated.CrawledURLs)
	}
	if updated.ResultCursor != 1 {
		t.Errorf("expected result cursor 1, got %d", updated.ResultCursor)
	}
	if len(updated.Frontier) != 2 {
		t.Errorf("expected frontier with 2 new urls, got %v", updated.Frontier)
	}

	// Record the second page and make sure the start URL is not requeued.
	updated, err = a.handleUpdate(ctx, UpdateReq{
		ID:         id,
		CrawledURL: "https://example.com/about",
		Discovered: []string{"https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.CrawledURLs) != 2 {
		t.Errorf("expected 2 crawled urls, got %v", updated.CrawledURLs)
	}
	if updated.ResultCursor != 2 {
		t.Errorf("expected result cursor 2, got %d", updated.ResultCursor)
	}
	for _, u := range updated.Frontier {
		if u == "https://example.com" {
			t.Errorf("crawled url should not be requeued")
		}
	}
}

func TestUpdate_SameDomainFilter(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := sampleConfig()
	cfg.SameDomain = true
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	id := resp.Task.ID

	updated, err := a.handleUpdate(ctx, UpdateReq{
		ID:         id,
		CrawledURL: "https://example.com",
		Discovered: []string{
			"https://example.com/page",
			"https://other.com/page",
			"/relative", // relative, should be treated as same domain
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Frontier) != 2 {
		t.Errorf("expected 2 frontier urls (same-domain + relative), got %v", updated.Frontier)
	}
	for _, u := range updated.Frontier {
		if u == "https://other.com/page" {
			t.Errorf("cross-domain url should be filtered out when same_domain=true")
		}
	}
	// A relative URL should resolve to the base host and be allowed.
	foundRelative := false
	for _, u := range updated.Frontier {
		if u == "https://example.com/relative" {
			foundRelative = true
		}
	}
	if !foundRelative {
		t.Errorf("relative URL should be resolved and kept: got %v", updated.Frontier)
	}
}

func TestUpdate_CrossDomainAllowed(t *testing.T) {
	a, ctx := freshActor(t)
	cfg := sampleConfig()
	cfg.SameDomain = false
	resp, _ := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	id := resp.Task.ID

	updated, err := a.handleUpdate(ctx, UpdateReq{
		ID:         id,
		CrawledURL: "https://example.com",
		Discovered: []string{"https://other.com/page"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Frontier) != 1 || updated.Frontier[0] != "https://other.com/page" {
		t.Errorf("cross-domain url should be allowed when same_domain=false: got %v", updated.Frontier)
	}
}

func TestPopFrontier(t *testing.T) {
	task := NewTask("x", Config{Seeds: []string{"https://a"}, MaxDepth: 2, MaxPages: 10, Mode: "hidden_window"})
	if task.PopFrontier() != "https://a" {
		t.Errorf("expected start url")
	}
	if task.PopFrontier() != "" {
		t.Errorf("expected empty frontier")
	}
	if len(task.Frontier) != 0 {
		t.Errorf("frontier should be empty")
	}
}

func TestGetAndList(t *testing.T) {
	a, ctx := freshActor(t)
	a.handleSubmit(ctx, SubmitReq{Config: sampleConfig()})

	got, err := a.handleGet(ctx, GetReq{ID: "crawl-0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "crawl-0" {
		t.Errorf("get returned wrong id")
	}

	_, err = a.handleGet(ctx, GetReq{ID: "missing"})
	if err == nil {
		t.Fatal("expected error for missing task")
	}

	list, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 1 {
		t.Errorf("expected 1 task in list, got %d", len(list.Tasks))
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	actorID := testutil.GenActorID()

	// First actor lifecycle: create a task and save it.
	a1 := &Actor{store: persist.NewFSPersist(dir)}
	ctx1 := testutil.HumanCtx(actorID)
	ctx1.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	if err := a1.OnStart(ctx1); err != nil {
		t.Fatal(err)
	}
	resp, err := a1.handleSubmit(ctx1, SubmitReq{Config: sampleConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a1.OnStop(ctx1); err != nil {
		t.Fatal(err)
	}

	if err := persist.NewFSPersist(dir).Load(actorID.String(), &snapshot{}); errors.Is(err, persist.ErrNotExist) {
		t.Fatal("state file should exist after save")
	}

	// Second actor lifecycle: same actorID must restore the task.
	a2 := &Actor{store: persist.NewFSPersist(dir)}
	ctx2 := testutil.HumanCtx(actorID)
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := a2.OnStart(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.tasks) != 1 {
		t.Fatalf("expected 1 restored task, got %d", len(a2.tasks))
	}
	if a2.tasks[0].ID != resp.Task.ID {
		t.Errorf("expected restored id %q, got %q", resp.Task.ID, a2.tasks[0].ID)
	}
	if a2.tasks[0].State != StateRunning {
		t.Errorf("expected restored state running, got %q", a2.tasks[0].State)
	}
	if a2.tasks[0].Config.Mode != "hidden_window" {
		t.Errorf("expected restored mode hidden_window, got %q", a2.tasks[0].Config.Mode)
	}
	if a2.nextID != 1 {
		t.Errorf("expected restored nextID 1, got %d", a2.nextID)
	}
}

func TestInvalidTransition(t *testing.T) {
	task := NewTask("x", Config{Seeds: []string{"https://a"}, MaxDepth: 2, MaxPages: 10, Mode: "hidden_window"})
	if err := task.Transition(StateDone, ""); err != nil {
		t.Fatalf("running -> done should be allowed: %v", err)
	}
	if err := task.Transition(StateRunning, ""); err == nil {
		t.Fatal("done -> running should be forbidden")
	}
}

func TestAwaitingLoginTransitions(t *testing.T) {
	task := NewTask("x", Config{Seeds: []string{"https://a"}, MaxDepth: 2, MaxPages: 10, Mode: "hidden_window"})

	// running -> awaiting_login is the login-wall handoff entry.
	if err := task.Transition(StateAwaitingLogin, ""); err != nil {
		t.Fatalf("running -> awaiting_login should be allowed: %v", err)
	}
	// awaiting_login -> done is illegal: the task must resume running first.
	if err := task.Transition(StateDone, ""); err == nil {
		t.Fatal("awaiting_login -> done should be forbidden")
	}
	// awaiting_login -> failed and cancelled are legal terminal exits.
	if err := task.Transition(StateFailed, "boom"); err != nil {
		t.Fatalf("awaiting_login -> failed should be allowed: %v", err)
	}
}

func TestAwaitingLoginResumeAndLoginWallURL(t *testing.T) {
	task := NewTask("x", Config{Seeds: []string{"https://a"}, MaxDepth: 2, MaxPages: 10, Mode: "hidden_window"})
	if err := task.Transition(StateAwaitingLogin, ""); err != nil {
		t.Fatalf("running -> awaiting_login: %v", err)
	}
	task.LoginWallURL = "https://example.com/login"
	if task.LoginWallURL == "" {
		t.Fatal("login wall url should be persisted on the task")
	}
	// LoginDone resumes the task to running so the hidden engine can continue.
	if err := task.LoginDone(); err != nil {
		t.Fatalf("login_done should resume running: %v", err)
	}
	if task.State != StateRunning {
		t.Fatalf("expected running after login_done, got %q", task.State)
	}
}
