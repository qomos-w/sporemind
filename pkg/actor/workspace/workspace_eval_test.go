package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

func TestHandleSporeSyntax(t *testing.T) {
	a := &Actor{}

	resp, err := a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{})
	if err != nil {
		t.Fatalf("default lang: %v", err)
	}
	if resp.Lang != "en" || !strings.Contains(resp.Markdown, "sporescript language reference") {
		t.Errorf("default resp = lang %q, %d bytes; want en reference", resp.Lang, len(resp.Markdown))
	}

	resp, err = a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{Lang: "zh"})
	if err != nil {
		t.Fatalf("zh lang: %v", err)
	}
	if resp.Lang != "zh" || !strings.Contains(resp.Markdown, "sporescript 语言参考") {
		t.Errorf("zh resp = lang %q; want zh reference", resp.Lang)
	}

	if _, err := a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{Lang: "fr"}); err == nil {
		t.Fatal("unsupported Lang must error")
	}
}

func TestHandleSearchCallablesFiltersAndLimits(t *testing.T) {
	a := &Actor{}
	a.topo = &fakeTopology{nodes: []runtime.ActorNode{
		{
			ID: "toast-actor",
			Callables: []domain.CallableInterface{
				{Name: "toast.show", Description: "show a toast", ServiceName: "toast"},
				{Name: "toast.read", Description: "read toast state", ServiceName: "toast"},
			},
		},
		{
			ID: "demo-actor",
			Callables: []domain.CallableInterface{
				{Name: "demo.ping", Description: "ping the demo actor", ServiceName: "demo"},
			},
		},
	}}

	// Query filter matches name and description, case-insensitively.
	resp, err := a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Query: "TOAST"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 2 || resp.Total != 2 {
		t.Fatalf("toast query = %d items (total %d), want 2/2", len(resp.Items), resp.Total)
	}

	// Description match across services.
	resp, err = a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Query: "ping"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "demo.ping" {
		t.Fatalf("ping query items = %+v", resp.Items)
	}

	// Limit truncates Items but Total still counts every match.
	resp, err = a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Total != 3 {
		t.Fatalf("limited search = %d items (total %d), want 1/3", len(resp.Items), resp.Total)
	}
}

func TestHandleSearchCallablesWithoutTopology(t *testing.T) {
	if _, err := (&Actor{}).handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{}); err == nil {
		t.Fatal("missing topology provider must error")
	}
}
