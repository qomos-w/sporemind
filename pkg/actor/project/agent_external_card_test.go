package project

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestAgentCardItemUsesLastActivityAsModified(t *testing.T) {
	now := "2026-08-07T12:00:00Z"
	lastActivity := "2026-08-01T09:30:00Z"
	item := agentCardItem(domain.AgentRef{ID: "agent-1", LastActivity: lastActivity}, now)

	if item.Created != now {
		t.Fatalf("Created = %q, want %q", item.Created, now)
	}
	want := toLocal(lastActivity)
	if item.Modified != want {
		t.Fatalf("Modified = %q, want local %q", item.Modified, want)
	}
}

func TestAgentCardItemUsesNowWhenLastActivityMissing(t *testing.T) {
	now := "2026-08-07T12:00:00Z"
	item := agentCardItem(domain.AgentRef{ID: "agent-1"}, now)

	if item.Modified != now {
		t.Fatalf("Modified = %q, want now %q", item.Modified, now)
	}
}

func toLocal(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format(time.RFC3339)
}
