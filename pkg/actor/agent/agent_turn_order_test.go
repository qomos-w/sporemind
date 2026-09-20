package agent

import (
	"reflect"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestMigrateTurnOrdersPreservesNextOrderHighWaterMark(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextTurnOrder: 100}, Session: domain.Session{Turns: []domain.Turn{
		{ID: "u1", TurnOrder: 7},
		{ID: "a1", TurnOrder: 7},
	}}}
	if !a.migrateTurnOrders() {
		t.Fatal("expected migration")
	}
	if a.RawSession.NextTurnOrder != 100 {
		t.Fatalf("NextTurnOrder=%d, want 100", a.RawSession.NextTurnOrder)
	}
}

func TestMigrateTurnOrdersSplitsSharedOrdersByHistory(t *testing.T) {
	a := &Actor{Session: domain.Session{Turns: []domain.Turn{
		{ID: "u1", Role: "user", TurnOrder: 7},
		{ID: "a1", Role: "assistant", TurnOrder: 7},
		{ID: "u2", Role: "user", TurnOrder: 8},
	}}}
	if !a.migrateTurnOrders() {
		t.Fatal("expected migration")
	}
	if got := []int64{a.Session.Turns[0].TurnOrder, a.Session.Turns[1].TurnOrder, a.Session.Turns[2].TurnOrder}; !reflect.DeepEqual(got, []int64{7, 8, 9}) {
		t.Fatalf("orders=%v, want [7 8 9]", got)
	}
}

func TestMigrateTurnOrdersUsesSeqAndDeduplicates(t *testing.T) {
	a := &Actor{Session: domain.Session{Turns: []domain.Turn{
		{ID: "u1", Role: "user", Seq: 1},
		{ID: "a1", Role: "assistant", Seq: 2},
		{ID: "legacy", Role: "assistant", Seq: 0},
		{ID: "dup", Role: "user", Seq: 2},
	}}}
	if !a.migrateTurnOrders() {
		t.Fatal("expected migration")
	}
	if a.Session.Turns[0].TurnOrder != 1 || a.Session.Turns[1].TurnOrder != 2 {
		t.Fatalf("unexpected orders: %+v", a.Session.Turns)
	}
	if a.Session.Turns[2].TurnOrder <= 2 || a.Session.Turns[3].TurnOrder <= a.Session.Turns[2].TurnOrder {
		t.Fatalf("legacy/duplicate order not allocated: %+v", a.Session.Turns)
	}
	if a.migrateTurnOrders() {
		t.Fatal("migration should be idempotent")
	}
}

func TestNormalizeSessionTurnsUsesTurnOrderAndActiveRef(t *testing.T) {
	a := &Actor{ActiveTurnRef: "a", Session: domain.Session{Turns: []domain.Turn{
		{ID: "b", TurnOrder: 2},
		{ID: "a", TurnOrder: 1},
		{ID: "a", TurnOrder: 1},
	}}}
	if !a.normalizeSessionTurns() {
		t.Fatal("expected normalization")
	}
	if len(a.Session.Turns) != 2 || a.Session.Turns[0].ID != "a" || a.Session.Turns[1].ID != "b" {
		t.Fatalf("unexpected turns: %+v", a.Session.Turns)
	}
	if a.Session.ActiveHead != 0 {
		t.Fatalf("ActiveHead=%d, want 0", a.Session.ActiveHead)
	}
}

func TestTurnsBySeqPrefersTurnOrder(t *testing.T) {
	turns := turnsBySeq([]domain.Turn{{ID: "later", TurnOrder: 2, Seq: 1}, {ID: "first", TurnOrder: 1, Seq: 9}})
	if turns[0].ID != "first" || turns[1].ID != "later" {
		t.Fatalf("unexpected order: %+v", turns)
	}
}
