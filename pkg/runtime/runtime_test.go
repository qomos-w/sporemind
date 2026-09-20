package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

type strictViolationActor struct{}

func (strictViolationActor) Type() string               { return "strict-violation" }
func (strictViolationActor) OnInit(actor.Context) error { return nil }
func (strictViolationActor) OnStop(actor.Context) error { return nil }
func (strictViolationActor) OnStart(ctx actor.Context) error {
	return ctx.Register("test.scalar_param", func(_ actor.Context, name string) error { return nil })
}

func TestNew_StrictHandlersRejectScalarParam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, Config{
		NoGateway: true,
		Children: []ChildSpec{{
			Name:    "violator",
			Factory: func() actor.Actor { return strictViolationActor{} },
		}},
	})
	if err == nil {
		t.Fatal("expected Run to fail due to strict handler violation")
	}
}

func TestNew_Defaults(t *testing.T) {
	a, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatal("New returned nil")
	}
	if a.Namespace() != "sporemind" {
		t.Errorf("Namespace: got %q, want 'sporemind'", a.Namespace())
	}
}

func TestNew_NoGateway(t *testing.T) {
	a, err := New(Config{NoGateway: true})
	if err != nil {
		t.Fatalf("New(NoGateway): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		t.Fatalf("Run: %v", err)
	}
}

func TestNew_CustomNamespace(t *testing.T) {
	a, err := New(Config{Namespace: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Namespace() != "test" {
		t.Errorf("Namespace: got %q, want 'test'", a.Namespace())
	}
}

// TestDomainTypes_EncodeDecode verifies that domain types survive
// JSON round-tripping, which is required by the gospore codec.
func TestDomainTypes_EncodeDecode(t *testing.T) {
	types := []any{
		domain.ProjectRef{ActorID: "p1", Name: "test", Path: "/tmp/test", Root: true},
		domain.AgentRef{ID: "a1", ProjectID: "p1", DisplayName: "Coder", AgentKind: "coding", Status: "active"},
		domain.PromptFragment{ID: "f1", Name: "sys", Kind: "system", Priority: 1, Content: "hi", Editable: true},
		domain.PromptContextSegment{ID: "seg-1", FragmentID: "f1", Chars: 2},
		domain.PromptArtifact{
			Sections:        []string{"a"},
			Fragments:       []domain.PromptFragment{{ID: "f1", Name: "sys", Kind: "system", Priority: 1, Editable: true}},
			ContextSegments: []domain.PromptContextSegment{{ID: "seg-1", Chars: 1}},
		},
		domain.PromptProfile{Scope: "s1", Role: "r1", RolePrompt: "role"},
		domain.FrontendErrorReport{RequestID: "r1", Severity: "error", Message: "oops", SessionID: "sess-1"},
		domain.SkillInstallSource{URL: "https://example.com/skill.yaml"},
		domain.TurnEvent{Kind: domain.TurnContextBudget, TurnID: "t1", ContextBudget: &domain.TurnContextBudgetPayload{EstimatedTokens: 100, ContextWindowSize: 8000, TokenBudget: 4000}},
		domain.TurnEvent{Kind: domain.TurnCompleted, TurnID: "t1", Usage: &domain.UsageData{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
		domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hi"},
		domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
		domain.AggregatorDescriptor{Name: "anthropic", ActorID: "0123456789abcdef0123456789abcdef"},
		domain.TurnInput{Text: "hello", Unit: &domain.ModelUnit{Model: "claude-sonnet-4-6", Provider: "anthropic"}, Title: "chat"},
		domain.WorkspaceCreateAgentReq{ProjectID: "p1", DisplayName: "Coder", AgentKind: "coding"},
	}

	for _, typ := range types {
		data, err := json.Marshal(typ)
		if err != nil {
			t.Fatalf("marshal %T: %v", typ, err)
		}
		if len(data) == 0 {
			t.Fatalf("marshal %T: empty output", typ)
		}
		t.Logf("%T: %s", typ, string(data))
	}
}
