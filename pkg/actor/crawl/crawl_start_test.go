package crawl

import (
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func crawlCardRaw() string {
	return `---
id: test-crawl
type: crawl
tags: []
data:
  seeds:
    - https://example.com
  max_depth: 1
  max_pages: 5
  same_domain: true
  rate_limit: 0s
  extract:
    schema: BrowserCrawlPageResult
  mode: hidden_window
---

Crawl body.
`
}

func TestBrowserCrawlStart_ValidCard(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{Card: crawlCardRaw()})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !strings.HasPrefix(resp.TaskID, "crawl-") {
		t.Errorf("expected task id to start with crawl-, got %q", resp.TaskID)
	}
}

// TestBrowserCrawlStart_RoleGate pins the identity gate every crawl.start
// caller passes through. Project agents' turn-engine tool calls arrive with
// role "system" (inherited from the project cell) and must pass; workspace
// global agents arrive with role "agent" and are rejected — the exact split
// exercised end-to-end by the agent package's browser-chat crawl chain test.
func TestBrowserCrawlStart_RoleGate(t *testing.T) {
	cases := []struct {
		role    string
		wantErr string
	}{
		{role: "developer", wantErr: ""},
		{role: "system", wantErr: ""},
		{role: "agent", wantErr: "forbidden"},
		{role: "", wantErr: "forbidden"},
	}
	for _, tc := range cases {
		a, ctx := freshActor(t)
		ctx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: id.Role(tc.role)}
		_, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{Card: crawlCardRaw()})
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("role %q: unexpected error: %v", tc.role, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("role %q: want error containing %q, got %v", tc.role, tc.wantErr, err)
		}
	}
}

func TestBrowserCrawlStart_InvalidType(t *testing.T) {
	a, ctx := freshActor(t)
	raw := strings.Replace(crawlCardRaw(), "type: crawl", "type: concept", 1)
	_, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{Card: raw})
	if err == nil {
		t.Fatal("expected error for non-crawl card")
	}
}

func TestBrowserCrawlStart_UnsupportedExtractSchema(t *testing.T) {
	a, ctx := freshActor(t)
	raw := strings.Replace(crawlCardRaw(), "schema: BrowserCrawlPageResult", "schema: UnknownStruct", 1)
	_, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{Card: raw})
	if err == nil {
		t.Fatal("expected error for unsupported extract schema")
	}
}

func TestBrowserCrawlStart_Overrides(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Card: crawlCardRaw(),
		Overrides: map[string]any{
			"max_pages": 7,
		},
	})
	if err != nil {
		t.Fatalf("start with overrides failed: %v", err)
	}
	status, err := a.handleBrowserCrawlStatus(ctx, gen.BrowserCrawlStatusReq{TaskID: resp.TaskID})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.MaxPages != 7 {
		t.Errorf("expected max_pages=7 from override, got %d", status.MaxPages)
	}
}

func TestBrowserCrawlStart_StartToolParamsDocumentInlineConfig(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	opts, ok := ctx.RegOpts["crawl.start"]
	if !ok {
		t.Fatal("crawl.start not registered")
	}
	descs := actor.ResolveParamDescs(opts...)
	if len(descs) != 3 {
		t.Fatalf("expected 3 param descriptions (Card/Config/Overrides), got %v", descs)
	}
	if d := descs["Config"]; !strings.Contains(d, "no card required") {
		t.Errorf("Config param must state that inline config suffices, got %q", d)
	}
	if d := descs["Card"]; !strings.Contains(d, "Optional") {
		t.Errorf("Card param must be marked optional, got %q", d)
	}

	// The LLM-facing tool schema is projected from these param descriptions
	// (topology → ResolveRequestLayout → mergeParamDescriptions); without them
	// the model sees a bare Card:string property and asks the user for a
	// crawl card instead of filling Config inline.
	ci := gen.CallableInterface{
		Name:        "crawl.start",
		ReqSchemaID: 4240,
	}
	for name, desc := range descs {
		ci.Params = append(ci.Params, gen.CallableParam{Name: name, Description: desc})
	}
	layout, err := protocol.ResolveRequestLayout(ci)
	if err != nil {
		t.Fatal(err)
	}
	schema := layout.JSONSchema()
	if !strings.Contains(schema, `"Card":{"description":"Optional`) {
		t.Errorf("projected schema missing Card description: %s", schema)
	}
	if !strings.Contains(schema, `"Config":{"description":"Inline crawl configuration`) {
		t.Errorf("projected schema missing Config description: %s", schema)
	}
}

func TestBrowserCrawlStatusAndResults(t *testing.T) {
	pages := map[string]*gen.BrowserPageObservation{
		"https://example.com": sampleObservation("https://example.com", "Home", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Card: crawlCardRaw(),
		Overrides: map[string]any{
			"max_pages": 1,
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	task := waitForState(t, a, ctx, resp.TaskID, StateDone, 5*time.Second)

	status, err := a.handleBrowserCrawlStatus(ctx, gen.BrowserCrawlStatusReq{TaskID: resp.TaskID})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.State != "done" {
		t.Errorf("expected state done, got %q", status.State)
	}
	if status.Crawled != 1 {
		t.Errorf("expected crawled=1, got %d", status.Crawled)
	}

	results, err := a.handleBrowserCrawlResults(ctx, gen.BrowserCrawlResultsReq{TaskID: resp.TaskID, Limit: 10})
	if err != nil {
		t.Fatalf("results failed: %v", err)
	}
	if len(results.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results.Results))
	}
	if results.Results[0].Title != "Home" {
		t.Errorf("expected title Home, got %v", results.Results[0].Title)
	}
	if !results.Done {
		t.Errorf("expected results done when task is done")
	}
	if len(task.Results) != 1 {
		t.Errorf("expected task to record 1 result, got %d", len(task.Results))
	}
}

func TestBrowserCrawlResults_MissingTask(t *testing.T) {
	a, ctx := freshActor(t)
	_, err := a.handleBrowserCrawlResults(ctx, gen.BrowserCrawlResultsReq{TaskID: "crawl-missing"})
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestBrowserCrawlStart_ConfigOnly(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Config: &gen.BrowserCrawlConfig{
			Seeds:      []string{"https://example.com"},
			MaxPages:   50,
			MaxDepth:   2,
			SameDomain: true,
		},
	})
	if err != nil {
		t.Fatalf("start with config-only failed: %v", err)
	}
	status, err := a.handleBrowserCrawlStatus(ctx, gen.BrowserCrawlStatusReq{TaskID: resp.TaskID})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.MaxPages != 50 {
		t.Errorf("expected max_pages=50 from config, got %d", status.MaxPages)
	}
}

func TestBrowserCrawlStart_ConfigWithOverrides(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Config: &gen.BrowserCrawlConfig{
			Seeds:    []string{"https://example.com"},
			MaxPages: 50,
		},
		Overrides: map[string]any{
			"max_pages": 99,
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	status, _ := a.handleBrowserCrawlStatus(ctx, gen.BrowserCrawlStatusReq{TaskID: resp.TaskID})
	if status.MaxPages != 99 {
		t.Errorf("expected overrides to win max_pages=99, got %d", status.MaxPages)
	}
}

func TestBrowserCrawlStart_CardWithConfigOverlay(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Card: crawlCardRaw(), // max_pages: 5
		Config: &gen.BrowserCrawlConfig{
			MaxPages: 42,
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	status, _ := a.handleBrowserCrawlStatus(ctx, gen.BrowserCrawlStatusReq{TaskID: resp.TaskID})
	if status.MaxPages != 42 {
		t.Errorf("expected config overlay max_pages=42, got %d", status.MaxPages)
	}
}

func TestBrowserCrawlStart_ConfigMissingSeeds(t *testing.T) {
	a, ctx := freshActor(t)
	_, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Config: &gen.BrowserCrawlConfig{
			MaxPages: 50,
		},
	})
	if err == nil {
		t.Fatal("expected error for config without seeds")
	}
}

func TestBrowserCrawlStart_ConfigInstanceID(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Config: &gen.BrowserCrawlConfig{
			Seeds:      []string{"https://example.com"},
			MaxPages:   50,
			InstanceID: "mounted-window-9",
		},
	})
	if err != nil {
		t.Fatalf("start with config instance_id failed: %v", err)
	}
	task, err := a.handleGet(ctx, GetReq{ID: resp.TaskID})
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if task.Config.InstanceID != "mounted-window-9" {
		t.Errorf("expected instance_id to flow into task config, got %q", task.Config.InstanceID)
	}
}

func TestBrowserCrawlStart_CardInstanceID(t *testing.T) {
	a, ctx := freshActor(t)
	raw := strings.Replace(crawlCardRaw(), "  mode: hidden_window", "  mode: hidden_window\n  instance_id: mounted-window-8", 1)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{Card: raw})
	if err != nil {
		t.Fatalf("start with card instance_id failed: %v", err)
	}
	task, err := a.handleGet(ctx, GetReq{ID: resp.TaskID})
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if task.Config.InstanceID != "mounted-window-8" {
		t.Errorf("expected card instance_id to flow into task config, got %q", task.Config.InstanceID)
	}
}

func TestBrowserCrawlStart_ConfigInstanceIDOverlayWins(t *testing.T) {
	a, ctx := freshActor(t)
	raw := strings.Replace(crawlCardRaw(), "  mode: hidden_window", "  mode: hidden_window\n  instance_id: mounted-from-card", 1)
	resp, err := a.handleBrowserCrawlStart(ctx, gen.BrowserCrawlStartReq{
		Card: raw,
		Config: &gen.BrowserCrawlConfig{
			InstanceID: "mounted-from-config",
		},
	})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	task, err := a.handleGet(ctx, GetReq{ID: resp.TaskID})
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if task.Config.InstanceID != "mounted-from-config" {
		t.Errorf("expected config overlay to win, got %q", task.Config.InstanceID)
	}
}
