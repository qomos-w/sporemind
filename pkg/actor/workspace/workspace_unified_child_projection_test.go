package workspace

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestDeriveUnifiedChildren_EmptyParentReturnsNil verifies that an empty
// parentActorID returns nil, and a parent with no children returns nil.
func TestDeriveUnifiedChildren_EmptyParentReturnsNil(t *testing.T) {
	a, _ := freshActor(t)

	if children := a.deriveUnifiedChildren(""); children != nil {
		t.Errorf("expected nil for empty parentActorID, got %v", children)
	}

	orphanParent := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: orphanParent, AgentKind: domain.AgentKindCoder},
	}
	if children := a.deriveUnifiedChildren(orphanParent); children != nil {
		t.Errorf("expected nil for parent with no children, got %v", children)
	}
}

// TestDeriveUnifiedChildren_ExcludesDeletingTombstone verifies that a workflow
// child marked for deletion (DeletionStatus="deleting" tombstone retained
// during async cascade teardown) does NOT appear under its parent's Children
// projection. buildAgentListState already excludes tombstones from the
// top-level item list; deriveUnifiedChildren must apply the same filter or a
// deleting child lingers in the sidebar (regression: deleted workflow child
// still shown).
func TestDeriveUnifiedChildren_ExcludesDeletingTombstone(t *testing.T) {
	a, _ := freshActor(t)

	parentActorID := genID()
	liveChildID := genID()
	deletingChildID := genID()

	a.Agents = []domain.AgentRef{
		{ID: "Parent#1", ActorID: parentActorID, AgentKind: domain.AgentKindCoordinator},
		{ID: liveChildID, ActorID: genID(), ParentAgentID: parentActorID, AgentKind: domain.AgentKindWorker, LifecycleScope: "workflow"},
		{ID: deletingChildID, ActorID: genID(), ParentAgentID: parentActorID, AgentKind: domain.AgentKindWorker, LifecycleScope: "workflow", DeletionStatus: "deleting"},
	}

	children := a.deriveUnifiedChildren(parentActorID)
	if len(children) != 1 {
		t.Fatalf("expected only the live workflow child, got %d children: %+v", len(children), children)
	}
	if children[0].ID != liveChildID {
		t.Errorf("expected live child %q, got %q", liveChildID, children[0].ID)
	}
}
