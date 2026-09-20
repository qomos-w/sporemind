package project

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestTemplateRunHistory_AndList(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Create a simple source map and save it as a template.
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src map: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "t1", Question: "Q"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	// Record a few runs manually (simulates scheduler/workspace/manual sources).
	for i := 0; i < 3; i++ {
		instID := "inst::" + string(rune('a'+i))
		source := "manual"
		schedulerID := ""
		if i == 1 {
			source = "scheduler"
			schedulerID = "scheduler:daily"
		}
		if err := a.recordTemplateRun("tpl", instID, schedulerID, source); err != nil {
			t.Fatalf("record run %d: %v", i, err)
		}
	}

	// Query default limit (10) returns all 3, newest first.
	resp, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl"})
	if err != nil {
		t.Fatalf("list_template_runs: %v", err)
	}
	if got, want := len(resp.Runs), 3; got != want {
		t.Fatalf("len(Runs) = %d, want %d", got, want)
	}
	if got, want := resp.Runs[0].InstanceMapID, "inst::c"; got != want {
		t.Errorf("newest run = %q, want %q", got, want)
	}
	if got, want := resp.Runs[0].Source, "manual"; got != want {
		t.Errorf("newest source = %q, want %q", got, want)
	}
	if got, want := resp.Runs[1].InstanceMapID, "inst::b"; got != want {
		t.Errorf("second run = %q, want %q", got, want)
	}
	if got, want := resp.Runs[1].SchedulerCardID, "scheduler:daily"; got != want {
		t.Errorf("second scheduler = %q, want %q", got, want)
	}
	if got, want := resp.Runs[2].InstanceMapID, "inst::a"; got != want {
		t.Errorf("oldest run = %q, want %q", got, want)
	}

	// Limit 2 returns the two newest.
	resp2, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl", Limit: 2})
	if err != nil {
		t.Fatalf("list_template_runs limit 2: %v", err)
	}
	if got, want := len(resp2.Runs), 2; got != want {
		t.Errorf("len(Runs) limit 2 = %d, want %d", got, want)
	}

	// Unknown template returns empty.
	resp3, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "unknown"})
	if err != nil {
		t.Fatalf("list_template_runs unknown: %v", err)
	}
	if got, want := len(resp3.Runs), 0; got != want {
		t.Errorf("len(Runs) unknown = %d, want %d", got, want)
	}

	// Missing template id is rejected.
	if _, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{}); err == nil {
		t.Errorf("expected error for missing TemplateMapId")
	}
}

func TestHandleWikiTemplateInstantiate_RecordsRun(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Build and template-save a workflow map.
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src map: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "t1", Question: "Q"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	// Instantiate with explicit provenance.
	_, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID:   "tpl",
		InstanceMapID:   "inst::tpl::1",
		Source:          "scheduler",
		SchedulerCardID: "scheduler:daily",
	})
	if err != nil {
		t.Fatalf("template_instantiate: %v", err)
	}

	resp, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl"})
	if err != nil {
		t.Fatalf("list_template_runs: %v", err)
	}
	if got, want := len(resp.Runs), 1; got != want {
		t.Fatalf("len(Runs) = %d, want %d", got, want)
	}
	if got, want := resp.Runs[0].InstanceMapID, "inst::tpl::1"; got != want {
		t.Errorf("InstanceMapID = %q, want %q", got, want)
	}
	if got, want := resp.Runs[0].Source, "scheduler"; got != want {
		t.Errorf("Source = %q, want %q", got, want)
	}
	if got, want := resp.Runs[0].SchedulerCardID, "scheduler:daily"; got != want {
		t.Errorf("SchedulerCardID = %q, want %q", got, want)
	}

	// Manual instantiation without provenance is recorded as source "manual".
	_, err = a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl",
		InstanceMapID: "inst::tpl::2",
	})
	if err != nil {
		t.Fatalf("manual template_instantiate: %v", err)
	}
	resp, err = a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl"})
	if err != nil {
		t.Fatalf("list_template_runs after manual: %v", err)
	}
	if got, want := len(resp.Runs), 2; got != want {
		t.Fatalf("len(Runs) = %d, want %d", got, want)
	}
	if got, want := resp.Runs[0].Source, "manual"; got != want {
		t.Errorf("manual source = %q, want %q", got, want)
	}

	// Instance map card carries data.instance_of back to the template.
	inst, err := a.store.Get("inst::tpl::2")
	if err != nil {
		t.Fatalf("get instance map: %v", err)
	}
	if got, want := cardInstanceOf(inst), "tpl"; got != want {
		t.Errorf("instance_of = %q, want %q", got, want)
	}
}
