package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// pagesAgent is an agent-kind actor that registers agent.inspect.pages and
// returns a fixed page list, used to verify the happy path still works.
type pagesAgent struct{}

func (pagesAgent) Type() string              { return "agent" }
func (pagesAgent) OnInit(actor.Context) error { return nil }
func (pagesAgent) OnStart(ctx actor.Context) error {
	return ctx.Register("inspect_pages", func(actor.PureContext) (domain.InspectPagesResp, error) {
		return domain.InspectPagesResp{
			Items: []domain.InspectPage{
				{ID: "prompt-context", Label: "Prompt Context"},
			},
		}, nil
	}, actor.Public())
}
func (pagesAgent) OnStop(actor.Context) error { return nil }

func TestInspectDocument_IncludesAgentPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := New(Config{
		Namespace: "sporemind",
		NoGateway: true,
		Children: []ChildSpec{{
			Name:    "pages-agent",
			Factory: func() actor.Actor { return pagesAgent{} },
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = a.Run(ctx) }()

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	for {
		callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := a.Self().Invoke(callCtx, "unified_graph.history", nil).Final(callCtx)
		callCancel()
		if err == nil {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-readyCtx.Done():
			t.Fatal("runtime root did not become ready")
		}
	}

	var agentID string
	a.Tree().Walk(func(r ref.Ref) bool {
		if a.ActorType(r.ID()) == "agent" {
			agentID = r.ID().String()
			return false
		}
		return true
	})
	if agentID == "" {
		t.Fatal("agent actor not found in tree")
	}

	req := domain.InspectDocumentReq{
		Kind:  "actor_node",
		ID:    agentID,
		Scope: map[string]string{"actor_id": agentID, "agent_id": "test-agent"},
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()
	resp, err := a.Self().Invoke(callCtx, "inspect.document", req).Final(callCtx)
	if err != nil {
		t.Fatalf("inspect.document: %v", err)
	}
	doc, ok := resp.(domain.InspectDocument)
	if !ok {
		t.Fatalf("unexpected response type %T", resp)
	}
	if len(doc.Pages) != 1 || doc.Pages[0].ID != "prompt-context" {
		t.Errorf("expected one prompt-context page, got %+v", doc.Pages)
	}
}

// noPagesAgent is an agent-kind actor that deliberately does not register
// agent.inspect.pages. This lets us verify inspect.document does not hang when
// the optional pages callable is missing or unresponsive.
type noPagesAgent struct{}

func (noPagesAgent) Type() string               { return "agent" }
func (noPagesAgent) OnInit(actor.Context) error  { return nil }
func (noPagesAgent) OnStart(actor.Context) error { return nil }
func (noPagesAgent) OnStop(actor.Context) error  { return nil }

func TestInspectDocument_DoesNotHangOnMissingAgentPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := New(Config{
		Namespace: "sporemind",
		NoGateway: true,
		Children: []ChildSpec{{
			Name:    "no-pages-agent",
			Factory: func() actor.Actor { return noPagesAgent{} },
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = a.Run(ctx) }()

	// Wait until the runtime root is ready.
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	for {
		callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, err := a.Self().Invoke(callCtx, "unified_graph.history", nil).Final(callCtx)
		callCancel()
		if err == nil {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-readyCtx.Done():
			t.Fatal("runtime root did not become ready")
		}
	}

	var agentID string
	a.Tree().Walk(func(r ref.Ref) bool {
		if a.ActorType(r.ID()) == "agent" {
			agentID = r.ID().String()
			return false
		}
		return true
	})
	if agentID == "" {
		t.Fatal("agent actor not found in tree")
	}

	req := domain.InspectDocumentReq{
		Kind:  "actor_node",
		ID:    agentID,
		Scope: map[string]string{"actor_id": agentID, "agent_id": "test-agent"},
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()
	resp, err := a.Self().Invoke(callCtx, "inspect.document", req).Final(callCtx)
	if err != nil {
		t.Fatalf("inspect.document: %v", err)
	}
	doc, ok := resp.(domain.InspectDocument)
	if !ok {
		t.Fatalf("unexpected response type %T", resp)
	}
	if doc.Title == "" {
		t.Error("expected non-empty document title")
	}
}
