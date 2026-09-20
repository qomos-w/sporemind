package appmanager

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestHandleCallableInfo_All(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleCallableInfo(nil, gen.AppManagerCallableInfoReq{})
	if err != nil {
		t.Fatalf("handleCallableInfo: %v", err)
	}
	if len(resp.Items) == 0 {
		t.Fatal("callable_info returned no entries")
	}
	wantIDs := map[string]bool{
		"appmanager.list":             false,
		"appmanager.get":              false,
		"appmanager.invoke":           false,
		"appmanager.register_project": false,
		"appmanager.reload_project":   false,
		"appmanager.unregister":       false,
		"appmanager.retry_cleanup":    false,
		"appmanager.plugin_load":      false,
		"appmanager.plugin_unload":    false,
		"pluginhost.list_plugins":     false,
		"pluginhost.plugin_logs":      false,
		"pluginhost.plugin_dom":       false,
		"appmanager.callable_info":    false,
		"appmanager.dev_guide":        false,
		"appmanager.host_protocol":    false,
		"appmanager.dev_generate":     false,
		"appmanager.dev_gate":         false,
		"appmanager.sdk_vendor":       false,
		"appmanager.install_local":    false,
	}
	for _, item := range resp.Items {
		if _, ok := wantIDs[item.ID]; !ok {
			t.Errorf("unexpected callable %q in callable_info", item.ID)
			continue
		}
		wantIDs[item.ID] = true
		if item.Description == "" {
			t.Errorf("callable %q has empty description", item.ID)
		}
		if item.Effect == "" {
			t.Errorf("callable %q has empty effect", item.ID)
		}
		if item.Service == "" {
			t.Errorf("callable %q has empty service", item.ID)
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("callable_info missing %q", id)
		}
	}
}

func TestHandleCallableInfo_QueryFilter(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleCallableInfo(nil, gen.AppManagerCallableInfoReq{Query: "register_project"})
	if err != nil {
		t.Fatalf("handleCallableInfo: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("filter 'register_project' returned %d items, want 1: %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].ID != "appmanager.register_project" {
		t.Errorf("filter 'register_project' returned %q, want appmanager.register_project", resp.Items[0].ID)
	}
}

func TestHandleDevGuide_FullResponse(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	if len(resp.Prerequisites) == 0 {
		t.Error("dev_guide Prerequisites empty")
	}
	if len(resp.Workflow) == 0 {
		t.Fatal("dev_guide Workflow empty")
	}
	if len(resp.Workflow) < 5 {
		t.Errorf("dev_guide Workflow has %d steps, want >= 5 (appdef through register)", len(resp.Workflow))
	}
	for i, step := range resp.Workflow {
		if step.Order != int32(i) {
			t.Errorf("workflow step %d has Order %d, want %d (guide numbers steps 0-based)", i, step.Order, i)
		}
		if step.Title == "" || step.Detail == "" {
			t.Errorf("workflow step %d has empty Title or Detail", i)
		}
	}
	if len(resp.Security) == 0 {
		t.Error("dev_guide Security empty")
	}
	if len(resp.CommonErrors) == 0 {
		t.Error("dev_guide CommonErrors empty")
	}
	for _, e := range resp.CommonErrors {
		if e.Symptom == "" || e.Cause == "" || e.Remedy == "" {
			t.Errorf("common error has empty field: %+v", e)
		}
	}
}

func TestHandleDevGuide_TopicFilter(t *testing.T) {
	a := &Actor{}

	sec, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "security"})
	if err != nil {
		t.Fatalf("handleDevGuide(security): %v", err)
	}
	if len(sec.Security) == 0 {
		t.Error("topic=security returned empty Security")
	}
	if len(sec.Workflow) != 0 || len(sec.Prerequisites) != 0 || len(sec.CommonErrors) != 0 || len(sec.HostAPI) != 0 {
		t.Error("topic=security should not include other sections")
	}

	hostAPI, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "host_api"})
	if err != nil {
		t.Fatalf("handleDevGuide(host_api): %v", err)
	}
	if len(hostAPI.HostAPI) == 0 {
		t.Fatal("topic=host_api returned empty HostAPI")
	}
	joined := strings.Join(hostAPI.HostAPI, "\n")
	for _, want := range []string{"llm.complete", "config.get", "project.read_file", "provider.list", "aggregator.list", "state.get", "app.state", "workspace.list_projects"} {
		if !strings.Contains(joined, want) {
			t.Errorf("host_api topic missing %q", want)
		}
	}
	if len(hostAPI.Workflow) != 0 || len(hostAPI.Security) != 0 {
		t.Error("topic=host_api should not include other sections")
	}

	errs, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "errors"})
	if err != nil {
		t.Fatalf("handleDevGuide(errors): %v", err)
	}
	if len(errs.CommonErrors) == 0 {
		t.Error("topic=errors returned empty CommonErrors")
	}
	if len(errs.Workflow) != 0 || len(errs.Prerequisites) != 0 || len(errs.Security) != 0 {
		t.Error("topic=errors should not include other sections")
	}

	// Unknown topic falls through to full response.
	full, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "bogus"})
	if err != nil {
		t.Fatalf("handleDevGuide(bogus): %v", err)
	}
	if len(full.Workflow) == 0 || len(full.Security) == 0 {
		t.Error("unknown topic should fall back to full guide")
	}
}

// TestHandleDevGuide_LifecycleMatrixTeaching guards the S3 documentation
// surface: workflow step 8 must teach the transport matrix (subprocess all
// immediate / in-process staged to restart_pending) and the CommonErrors
// section must carry a restart_pending diagnostic entry.
func TestHandleDevGuide_LifecycleMatrixTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}

	step9 := false
	for _, s := range resp.Workflow {
		if s.Order != 9 {
			continue
		}
		step9 = true
		if !strings.Contains(s.Title, "Lifecycle") {
			t.Errorf("workflow step 9 title = %q, want lifecycle semantics", s.Title)
		}
		detail := strings.ToLower(s.Detail)
		for _, want := range []string{"subprocess", "inprocess", "restart_pending", "unload_pending", "hot swap"} {
			if !strings.Contains(detail, want) {
				t.Errorf("workflow step 9 missing %q in detail", want)
			}
		}
	}
	if !step9 {
		t.Fatal("workflow has no step 9 (lifecycle matrix)")
	}

	found := false
	for _, e := range resp.CommonErrors {
		if strings.Contains(strings.ToLower(e.Symptom), "restart_pending") {
			found = true
			for _, want := range []string{"restart", "plugin_load", "running"} {
				if !strings.Contains(strings.ToLower(e.Remedy), want) {
					t.Errorf("restart_pending common error remedy missing %q; got %q", want, e.Remedy)
				}
			}
		}
	}
	if !found {
		t.Error("CommonErrors has no restart_pending diagnostic entry")
	}
}

// TestHandleDevGuide_FreeAgentOnlyTeaching guards the free_agent-only model:
// the structured guide must not teach the removed agent_binding block, and
// the appdef workflow step must point at free_agent as the only agent-facing
// declaration (mirrors pkg/agentkit builtin card plugin-dev.md).
func TestHandleDevGuide_FreeAgentOnlyTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	texts := make([]string, 0, len(resp.Prerequisites)+len(resp.Security)+2*len(resp.Workflow)+3*len(resp.CommonErrors))
	texts = append(texts, resp.Prerequisites...)
	texts = append(texts, resp.Security...)
	for _, s := range resp.Workflow {
		texts = append(texts, s.Title, s.Detail)
	}
	for _, e := range resp.CommonErrors {
		texts = append(texts, e.Symptom, e.Cause, e.Remedy)
	}
	for _, txt := range texts {
		if strings.Contains(txt, "agent_binding") {
			t.Errorf("dev_guide still teaches removed agent_binding block: %q", txt)
		}
	}
	taught := false
	for _, s := range resp.Workflow {
		if s.Order == 1 && strings.Contains(s.Detail, "free_agent") {
			taught = true
		}
	}
	if !taught {
		t.Error("dev_guide workflow step 1 (appdef) does not teach free_agent")
	}
}

// TestHandleDevGuide_PluginAgentTeaching guards the plugin_agent contract:
// the appdef workflow step must teach the block, and no teaching may claim
// free_agent is the *only* agent-facing declaration anymore.
func TestHandleDevGuide_PluginAgentTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	taught := false
	for _, s := range resp.Workflow {
		if s.Order == 1 {
			if strings.Contains(s.Detail, "plugin_agent") {
				taught = true
			}
			// Multi-slot teaching: the guide must show the named-block form
			// and the default-slot equivalence, not just the bare keyword.
			for _, want := range []string{"plugin_agent reviewer", "unnamed block binds"} {
				if !strings.Contains(s.Detail, want) {
					t.Errorf("dev_guide workflow step 1 does not teach multi-slot syntax (missing %q)", want)
				}
			}
			if strings.Contains(s.Detail, "only agent-facing declaration") {
				t.Error("dev_guide still claims free_agent is the only agent-facing declaration")
			}
		}
	}
	if !taught {
		t.Error("dev_guide workflow step 1 (appdef) does not teach plugin_agent")
	}
}

// TestHandleDevGuide_TemplateExplicitTeaching guards the BP9 contract: the
// guide must teach that template scaffolding is opt-in via Template=true and
// document both refusal errors (no .appdef; non-empty directory).
func TestHandleDevGuide_TemplateExplicitTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}

	taught := false
	for _, s := range resp.Workflow {
		if s.Order == 2 && strings.Contains(s.Detail, "Template=true") {
			taught = true
		}
	}
	if !taught {
		t.Error("dev_guide workflow step 2 (generate) does not teach the Template=true opt-in")
	}

	symptoms := make([]string, 0, len(resp.CommonErrors))
	for _, e := range resp.CommonErrors {
		symptoms = append(symptoms, e.Symptom)
	}
	for _, want := range []string{"no .appdef found", "template scaffold refused"} {
		found := false
		for _, s := range symptoms {
			if strings.Contains(s, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("dev_guide CommonErrors missing %q symptom; got %v", want, symptoms)
		}
	}
}

// TestHandleCallableInfo_NewTeaching guards the D2/D4 teaching surface: the
// three added callable_info entries (dev_generate, plugin_load, plugin_unload)
// must teach the key semantics — Template scaffolds only into a directory with
// no go.mod and no *.go; plugin_load re-installs the recorded artifact;
// plugin_unload keeps the app record (unlike unregister); stopped state does
// not survive a host restart.
func TestHandleCallableInfo_NewTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleCallableInfo(nil, gen.AppManagerCallableInfoReq{})
	if err != nil {
		t.Fatalf("handleCallableInfo: %v", err)
	}
	byID := map[string]gen.AppManagerCallableInfoEntry{}
	for _, item := range resp.Items {
		byID[item.ID] = item
	}

	for _, want := range []string{"appmanager.dev_generate", "appmanager.plugin_load", "appmanager.plugin_unload"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("callable_info missing %q", want)
		}
	}

	genEntry := byID["appmanager.dev_generate"]
	if !strings.Contains(genEntry.Description, "worktree") {
		t.Error("dev_generate description does not teach the worktree rejection")
	}
	paramNames := make([]string, 0, len(genEntry.Params))
	for _, p := range genEntry.Params {
		paramNames = append(paramNames, p.Name)
	}
	for _, want := range []string{"ProjectId", "AppDir", "Template"} {
		found := false
		for _, n := range paramNames {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("dev_generate params missing %q; got %v", want, paramNames)
		}
	}
	tpl := ""
	for _, p := range genEntry.Params {
		if p.Name == "Template" {
			tpl = strings.ToLower(p.Description)
		}
	}
	for _, want := range []string{"go.mod", "*.go"} {
		if !strings.Contains(tpl, want) {
			t.Errorf("dev_generate Template param does not teach %q condition; got %q", want, tpl)
		}
	}

	load := strings.ToLower(byID["appmanager.plugin_load"].Description)
	unload := strings.ToLower(byID["appmanager.plugin_unload"].Description)
	for _, f := range []string{"native", "idempotent"} {
		if !strings.Contains(load, f) {
			t.Errorf("plugin_load description missing %q; got %q", f, load)
		}
	}
	for _, f := range []string{"native", "idempotent"} {
		if !strings.Contains(unload, f) {
			t.Errorf("plugin_unload description missing %q; got %q", f, unload)
		}
	}
	if !strings.Contains(unload, "record") || !strings.Contains(unload, "plugin_load") {
		t.Errorf("plugin_unload description must teach record retention + plugin_load restart; got %q", unload)
	}
	if !strings.Contains(unload, "restart") {
		t.Errorf("plugin_unload description must teach stopped-state restart behavior; got %q", unload)
	}
}

// TestPackageStorageTeaching guards the T1-T3 package-inventory semantics on
// both teaching surfaces of dev_guide: the install_local callable_info entry
// must describe the content-addressed package store and lifecycle, and the
// workflow must carry the packaged distribution step.
func TestPackageStorageTeaching(t *testing.T) {
	a := &Actor{}

	// Callable-info surface.
	info, err := a.handleCallableInfo(nil, gen.AppManagerCallableInfoReq{})
	if err != nil {
		t.Fatalf("handleCallableInfo: %v", err)
	}
	var installEntry *gen.AppManagerCallableInfoEntry
	for i := range info.Items {
		if info.Items[i].ID == "appmanager.install_local" {
			installEntry = &info.Items[i]
			break
		}
	}
	if installEntry == nil {
		t.Fatal("callable_info missing appmanager.install_local entry")
	}
	install := strings.ToLower(installEntry.Description)
	for _, want := range []string{
		".actors/appmanager/packages/", // content-addressed zip store
		"artifacts/",                   // native artifact extraction target
		"packagepath",                  // record pointer to the stored zip
		"artifactpath",                 // record pointer to the extracted artifact
		"garbage-collected",            // old-file GC on same-ID reinstall
		"update",                       // same-ID reinstall = update
		"unregister",                   // cascade cleanup
		"self-heal",                    // missing-artifact restart restore
	} {
		if !strings.Contains(install, want) {
			t.Errorf("install_local callable_info description missing %q; got %q", want, installEntry.Description)
		}
	}

	// Workflow surface: step 11 teaches packaged distribution + storage.
	guide, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	stepFound := false
	for _, s := range guide.Workflow {
		if s.Order != 11 {
			continue
		}
		stepFound = true
		detail := strings.ToLower(s.Detail)
		for _, want := range []string{"app_export", "install_local", "packages/", "artifacts/", "self-heal"} {
			if !strings.Contains(detail, want) {
				t.Errorf("workflow step 11 missing %q in detail", want)
			}
		}
	}
	if !stepFound {
		t.Fatal("dev_guide workflow has no step 11 (packaged distribution)")
	}
}

// TestHandleDevGuide_Round6BreakpointTeaching guards the round-6 external
// TOTP-run findings: workflow step 1 must teach namespace as one of the
// required meta fields, and CommonErrors must cover the missing-namespace
// rejection plus orphan handlers left behind when a callable disappears
// from .appdef.
func TestHandleDevGuide_Round6BreakpointTeaching(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}

	if len(resp.Workflow) == 0 {
		t.Fatal("dev_guide Workflow empty")
	}
	// Step 1 ("Write/edit .appdef", Order 1 — the slice index is 1) must teach
	// the required meta fields. Step 0 is the bundle-discovery preamble and
	// intentionally does not.
	step1 := strings.ToLower(resp.Workflow[1].Title + " " + resp.Workflow[1].Detail)
	for _, want := range []string{"id", "name", "version", "namespace"} {
		if !strings.Contains(step1, want) {
			t.Errorf("workflow step 1 does not teach required meta field %q: %q", want, resp.Workflow[1].Detail)
		}
	}

	hasNamespaceErr := false
	hasOrphanErr := false
	for _, e := range resp.CommonErrors {
		body := strings.ToLower(e.Symptom + " " + e.Cause + " " + e.Remedy)
		if strings.Contains(body, "missing namespace") {
			hasNamespaceErr = true
		}
		if strings.Contains(body, "orphan") && strings.Contains(body, "handler") {
			hasOrphanErr = true
		}
	}
	if !hasNamespaceErr {
		t.Error("CommonErrors missing entry for the namespace-missing rejection")
	}
	if !hasOrphanErr {
		t.Error("CommonErrors missing entry for orphan handlers after .appdef rewrite")
	}
}

// TestCapabilityQuickReference_CatalogAligned guards the "重要能力速览" entry in
// the HostAPI section: the quick reference must cover EVERY capability in the
// authoritative CapabilityCatalog — no hand-written second list is allowed to
// drift from it. For each catalog entry, the quick reference must contain the
// capability ID, the risk level, the en-US title, and the en-US description.
func TestCapabilityQuickReference_CatalogAligned(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "host_api"})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	if len(resp.HostAPI) == 0 {
		t.Fatal("host_api topic returned empty HostAPI")
	}

	// The quick reference is the first HostAPI entry.
	qr := resp.HostAPI[0]
	if !strings.Contains(qr, "重要能力速览") {
		t.Fatalf("HostAPI[0] is not the capability quick reference: %q", qr[:min(120, len(qr))])
	}

	for id, cap := range appbinding.CapabilityCatalog {
		if !strings.Contains(qr, id) {
			t.Errorf("quick reference missing capability %q", id)
		}
		if !strings.Contains(qr, cap.RiskLevel) {
			t.Errorf("quick reference missing risk level %q for %q", cap.RiskLevel, id)
		}
		en := cap.Locales["en-US"]
		if en.Title != "" && !strings.Contains(qr, en.Title) {
			t.Errorf("quick reference missing en-US title %q for %q", en.Title, id)
		}
		if en.Description != "" && !strings.Contains(qr, en.Description) {
			t.Errorf("quick reference missing en-US description for %q", id)
		}
		// Every capability in the catalog must appear exactly once (no duplicates).
		if n := strings.Count(qr, id); n != 1 {
			t.Errorf("capability %q appears %d times in quick reference, want 1", id, n)
		}
	}
}

// TestCapabilityQuickReference_KnownHighFrequency guards that the high-frequency
// capabilities called out in the guide's narrative are present in the catalog
// AND visible in the quick reference text.
func TestCapabilityQuickReference_KnownHighFrequency(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleDevGuide(nil, gen.AppManagerDevGuideReq{Topic: "host_api"})
	if err != nil {
		t.Fatalf("handleDevGuide: %v", err)
	}
	qr := resp.HostAPI[0]

	for _, id := range []string{"registry.read", "llm.invoke", "fs.read", "fs.write", "shell.exec", "app.state", "app.emit"} {
		if _, ok := appbinding.CapabilityCatalog[id]; !ok {
			t.Errorf("high-frequency capability %q missing from CapabilityCatalog", id)
		}
		if !strings.Contains(qr, id) {
			t.Errorf("high-frequency capability %q missing from quick reference", id)
		}
	}
}

// TestHostProtocol_QueryRegistryVisible guards the host_protocol query surface:
// Query:"registry" must surface the registry.read capability.
func TestHostProtocol_QueryRegistryVisible(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen.AppManagerHostProtocolReq{Query: "registry"})
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	found := false
	for _, cap := range resp.Capabilities {
		if cap.Capability == "registry.read" {
			found = true
			if cap.RiskLevel != "low" {
				t.Errorf("registry.read risk level = %q, want low", cap.RiskLevel)
			}
			var callIDs []string
			for _, c := range cap.Calls {
				callIDs = append(callIDs, c.CallID)
				if c.RequestType != "RegistryQueryReq" {
					t.Errorf("registry.query request type = %q, want RegistryQueryReq", c.RequestType)
				}
				if c.ResponseType != "RegistryQueryResp" {
					t.Errorf("registry.query response type = %q, want RegistryQueryResp", c.ResponseType)
				}
			}
			if len(callIDs) == 0 {
				t.Error("registry.read has no gated callIDs")
			}
		}
	}
	if !found {
		var ids []string
		for _, c := range resp.Capabilities {
			ids = append(ids, c.Capability)
		}
		t.Errorf("Query \"registry\" did not surface registry.read; got %v", fmt.Sprint(ids))
	}
}
