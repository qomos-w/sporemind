package workspace

import (
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// crawlTestFixture builds a workspace actor plus fake context wired for
// crawl executor tests. The fake crawl actor records its calls and returns
// scripted status/results sequences.
type crawlTestFixture struct {
	a               *Actor
	ctx             *testutil.FakeCtx
	projectID       string
	callerAgentID   string
	crawlCardID     string
	boundTaskCardID string

	// Fake crawl actor state.
	startCalled bool
	startedTaskID string
	statusSequence []gen.BrowserCrawlStatusResp
	statusIdx      int
	results        []gen.BrowserCrawlPageResult

	// Fake project actor state.
	statusSet  map[string]string
	outputsSet map[string]map[string]any
}

func newCrawlTestFixture(t *testing.T) *crawlTestFixture {
	t.Helper()
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	f := &crawlTestFixture{
		a:               a,
		ctx:             ctx,
		projectID:       projectID,
		callerAgentID:   callerAgentID,
		crawlCardID:     "crawl-def-1",
		boundTaskCardID: "task-crawl-1",
		statusSet:       make(map[string]string),
		outputsSet:      make(map[string]map[string]any),
	}

	ctx.LookupIDFn = f.lookupID
	ctx.LookupServiceFn = f.lookupService
	return f
}

func (f *crawlTestFixture) lookupID(aid id.ActorID) (ref.Ref, bool) {
	aidStr := aid.String()
	if aidStr == f.callerAgentID {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "agent_status" {
				return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
			}
			return nil
		}), true
	}
	return f.fakeProjectRef(aid), true
}

func (f *crawlTestFixture) lookupService(name string) (ref.Ref, bool) {
	if name == "crawl" {
		return testutil.NewFakeRef(testutil.GenActorID(), f.fakeCrawlInvoke), true
	}
	return nil, false
}

func (f *crawlTestFixture) fakeCrawlInvoke(callID string, payload any) any {
	switch callID {
	case "crawl.start":
		req, ok := payload.(gen.BrowserCrawlStartReq)
		if !ok {
			return fmt.Errorf("invalid start req")
		}
		if req.Card == "" {
			return fmt.Errorf("card is required")
		}
		f.startCalled = true
		f.startedTaskID = "crawl-task-42"
		return gen.BrowserCrawlStartResp{TaskID: f.startedTaskID}
	case "crawl.status":
		req, ok := payload.(gen.BrowserCrawlStatusReq)
		if !ok || req.TaskID != f.startedTaskID {
			return fmt.Errorf("unexpected task id")
		}
		if f.statusIdx >= len(f.statusSequence) {
			// Default to done if the test did not script a sequence.
			return gen.BrowserCrawlStatusResp{State: "done", Crawled: 1, Frontier: 0, MaxPages: 10}
		}
		resp := f.statusSequence[f.statusIdx]
		f.statusIdx++
		return resp
	case "crawl.results":
		req, ok := payload.(gen.BrowserCrawlResultsReq)
		if !ok || req.TaskID != f.startedTaskID {
			return fmt.Errorf("unexpected task id")
		}
		return gen.BrowserCrawlResultsResp{
			Results:    f.results,
			NextCursor: int32(len(f.results)),
			Done:       true,
		}
	}
	return nil
}

func (f *crawlTestFixture) fakeProjectRef(aid id.ActorID) ref.Ref {
	return testutil.NewFakeRef(aid, func(callID string, payload any) any {
		switch callID {
		case "project.wiki_get_card":
			req, ok := payload.(domain.WikiGetCardReq)
			if !ok {
				return nil
			}
			switch req.ID {
			case f.boundTaskCardID:
				return domain.WikiGetCardResp{ID: req.ID, Raw: f.boundTaskCardRaw()}
			case f.crawlCardID:
				return domain.WikiGetCardResp{ID: req.ID, Raw: f.crawlCardRaw()}
			}
			return fmt.Errorf("card %q not found", req.ID)
		case "project.wiki_claim_task_card":
			req, ok := payload.(gen.WikiClaimTaskCardReq)
			if !ok || req.ID != f.boundTaskCardID {
				return nil
			}
			f.statusSet[req.ID] = req.Status
			return gen.WikiClaimTaskCardResp{
				PreviousStatus: "backlog",
				Raw:            f.boundTaskCardRawDoing(),
			}
		case "project.wiki_set_status":
			req, ok := payload.(gen.WikiSetStatusReq)
			if ok {
				f.statusSet[req.ID] = req.Status
			}
			return domain.WikiSetStatusResp{}
		case "project.wiki_set_task_outputs":
			req, ok := payload.(domain.WikiSetTaskOutputsReq)
			if ok {
				f.outputsSet[req.CardID] = req.Outputs
			}
			return domain.WikiSetTaskOutputsResp{}
		}
		return nil
	})
}

func (f *crawlTestFixture) boundTaskCardRaw() string {
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"data:\n" +
		"  exec:\n" +
		"    kind: crawl\n" +
		"    crawl_card_id: " + f.crawlCardID + "\n---\n\nCrawl task body."
}

func (f *crawlTestFixture) boundTaskCardRawDoing() string {
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: doing\n" +
		"data:\n" +
		"  exec:\n" +
		"    kind: crawl\n" +
		"    crawl_card_id: " + f.crawlCardID + "\n---\n\nCrawl task body."
}

func (f *crawlTestFixture) crawlCardRaw() string {
	return "---\nid: " + f.crawlCardID + "\ntype: crawl\ndata:\n" +
		"  seeds:\n" +
		"    - https://example.com\n" +
		"  max_depth: 1\n" +
		"  max_pages: 10\n" +
		"  mode: hidden_window\n" +
		"  extract:\n" +
		"    schema: BrowserCrawlPageResult\n---\n\nCrawl definition."
}

func TestCrawlExecutor_Registered(t *testing.T) {
	f := newCrawlTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindCrawl)
	if !ok {
		t.Fatal("crawl executor not registered")
	}
	if exec.Kind() != ExecKindCrawl {
		t.Fatalf("expected kind crawl, got %q", exec.Kind())
	}
}

func TestCrawlExecutor_Preflight_RequiresCrawlCardID(t *testing.T) {
	f := newCrawlTestFixture(t)
	e := newCrawlExecutor(f.a)
	req := ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         "---\nid: task\ntype: task\nstatus: backlog\ndata:\n  exec:\n    kind: crawl\n---\n\nBody.",
	}
	_, err := e.Preflight(f.ctx, req)
	if err == nil {
		t.Fatal("expected error for missing crawl_card_id")
	}
}

func TestCrawlExecutor_Preflight_RequiresCrawlCardType(t *testing.T) {
	f := newCrawlTestFixture(t)

	// Override project get to return a non-crawl card for the crawl_card_id.
	badCrawlCardID := "not-a-crawl-card"
	f.crawlCardID = badCrawlCardID
	f.ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == f.callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			if callID == "project.wiki_get_card" {
				req, ok := payload.(domain.WikiGetCardReq)
				if !ok {
					return nil
				}
				if req.ID == badCrawlCardID {
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\nid: " + req.ID + "\ntype: wiki\n---\n\nNot a crawl card."}
				}
				if req.ID == f.boundTaskCardID {
					return domain.WikiGetCardResp{ID: req.ID, Raw: f.boundTaskCardRaw()}
				}
			}
			return nil
		}), true
	}

	e := newCrawlExecutor(f.a)
	_, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(),
	})
	if err == nil {
		t.Fatal("expected error for non-crawl definition card")
	}
}

func TestCrawlExecutor_Execute_Done_WritesOutputsAndSetsDone(t *testing.T) {
	f := newCrawlTestFixture(t)
	// Speed up polling for the test.
	crawlPollInterval = 1 * time.Millisecond
	defer func() { crawlPollInterval = 500 * time.Millisecond }()

	f.statusSequence = []gen.BrowserCrawlStatusResp{
		{State: "running", Crawled: 0, Frontier: 1, MaxPages: 10},
		{State: "done", Crawled: 1, Frontier: 0, MaxPages: 10},
	}
	f.results = []gen.BrowserCrawlPageResult{
		{URL: "https://example.com", Title: "Example", Text: "hello"},
	}

	e := newCrawlExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(),
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	_, err = e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRawDoing(),
		Body:            "Crawl task body.",
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !f.startCalled {
		t.Fatal("crawl.start was not called")
	}
	if f.startedTaskID != "crawl-task-42" {
		t.Errorf("started task id = %q, want %q", f.startedTaskID, "crawl-task-42")
	}
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("task card status = %q, want done", f.statusSet[f.boundTaskCardID])
	}
	outputs, ok := f.outputsSet[f.boundTaskCardID]
	if !ok {
		t.Fatal("task outputs were not written")
	}
	results, ok := outputs["results"].([]gen.BrowserCrawlPageResult)
	if !ok || len(results) != 1 {
		t.Fatalf("expected one result, got %+v", outputs)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("result URL = %q, want https://example.com", results[0].URL)
	}
}

func TestCrawlExecutor_Execute_Failed_SetsFailed(t *testing.T) {
	f := newCrawlTestFixture(t)
	crawlPollInterval = 1 * time.Millisecond
	defer func() { crawlPollInterval = 500 * time.Millisecond }()

	f.statusSequence = []gen.BrowserCrawlStatusResp{
		{State: "failed", Crawled: 0, Frontier: 0, MaxPages: 10, Error: "navigation timeout"},
	}

	e := newCrawlExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(),
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	_, err = e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRawDoing(),
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("task card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	outputs := f.outputsSet[f.boundTaskCardID]
	if outputs == nil || outputs["error"] != "navigation timeout" {
		t.Errorf("expected error output, got %+v", outputs)
	}
}

func TestCrawlExecutor_Execute_Cancelled_SetsCancelled(t *testing.T) {
	f := newCrawlTestFixture(t)
	crawlPollInterval = 1 * time.Millisecond
	defer func() { crawlPollInterval = 500 * time.Millisecond }()

	f.statusSequence = []gen.BrowserCrawlStatusResp{
		{State: "cancelled", Crawled: 0, Frontier: 0, MaxPages: 10},
	}

	e := newCrawlExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(),
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	_, err = e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRawDoing(),
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if f.statusSet[f.boundTaskCardID] != "cancelled" {
		t.Errorf("task card status = %q, want cancelled", f.statusSet[f.boundTaskCardID])
	}
}

func TestCrawlExecutor_Execute_AwaitingLogin_ContinuesPollingToDone(t *testing.T) {
	f := newCrawlTestFixture(t)
	crawlPollInterval = 1 * time.Millisecond
	defer func() { crawlPollInterval = 500 * time.Millisecond }()

	// awaiting_login is transient: after handoff the crawl resumes running
	// and eventually reaches done. The executor must keep polling and not
	// short-circuit into a terminal-ish task status.
	f.statusSequence = []gen.BrowserCrawlStatusResp{
		{State: "awaiting_login", Crawled: 0, Frontier: 1, MaxPages: 10, Error: "login wall detected"},
		{State: "running", Crawled: 1, Frontier: 0, MaxPages: 10},
		{State: "done", Crawled: 2, Frontier: 0, MaxPages: 10},
	}
	f.results = []gen.BrowserCrawlPageResult{
		{URL: "https://example.com", Title: "Example"},
		{URL: "https://example.com/page2", Title: "Page 2"},
	}

	e := newCrawlExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(),
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	_, err = e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRawDoing(),
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Task must end done (not blocked/awaiting) with results written.
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("task card status = %q, want done (polling must continue through awaiting_login)", f.statusSet[f.boundTaskCardID])
	}
	outputs, ok := f.outputsSet[f.boundTaskCardID]
	if !ok {
		t.Fatal("task outputs were not written after polling through awaiting_login")
	}
	results, ok := outputs["results"].([]gen.BrowserCrawlPageResult)
	if !ok || len(results) != 2 {
		t.Fatalf("expected two results after resuming from awaiting_login, got %+v", outputs)
	}
}

// TestHandleAgentSpawnAssign_CrawlExecKind verifies the dispatcher routes a
// task card declaring data.exec.kind = crawl to the crawl executor,
// advancing the task card and producing crawl outputs without spawning a
// worker agent.
func TestHandleAgentSpawnAssign_CrawlExecKind(t *testing.T) {
	f := newCrawlTestFixture(t)
	crawlPollInterval = 1 * time.Millisecond
	defer func() { crawlPollInterval = 500 * time.Millisecond }()

	f.statusSequence = []gen.BrowserCrawlStatusResp{
		{State: "done", Crawled: 1, Frontier: 0, MaxPages: 10},
	}
	f.results = []gen.BrowserCrawlPageResult{
		{URL: "https://example.com", Title: "Example"},
	}

	resp, err := f.a.handleAgentSpawnAssign(f.ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: f.boundTaskCardID,
		ProjectID:       f.projectID,
		CallerAgentID:   f.callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	if resp.AgentActorID != "crawl-task-42" {
		t.Errorf("AgentActorID = %q, want crawl-task-42", resp.AgentActorID)
	}
	if resp.DisplayName != "CrawlExecutor" {
		t.Errorf("DisplayName = %q, want CrawlExecutor", resp.DisplayName)
	}
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("task card status = %q, want done", f.statusSet[f.boundTaskCardID])
	}
	outputs := f.outputsSet[f.boundTaskCardID]
	if outputs == nil {
		t.Fatal("task outputs were not written")
	}
	results, ok := outputs["results"].([]gen.BrowserCrawlPageResult)
	if !ok || len(results) != 1 {
		t.Fatalf("expected one result, got %+v", outputs)
	}
}
