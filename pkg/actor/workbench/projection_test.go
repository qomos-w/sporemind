package workbench

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/projection"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestSnapshotProjectionSlot asserts the compile-time projection wiring the
// frontend relies on: the actor exposes exactly one public component slot named
// "Snapshot" typed gen.WorkbenchSnapshot. The generated codegen
// (Projections.workbench.Snapshot → watchSnapshot) is derived from this slot;
// a regression here (renamed field, dropped tag, wrong visibility) would break
// the frontend subscription stream without any Go compile error.
func TestSnapshotProjectionSlot(t *testing.T) {
	slots, err := projection.ScanComponents(&Actor{})
	if err != nil {
		t.Fatalf("ScanComponents: %v", err)
	}
	var snapshot *projection.ComponentSlot
	for i := range slots {
		if slots[i].Name == "Snapshot" {
			snapshot = &slots[i]
		}
	}
	if snapshot == nil {
		t.Fatalf("no Snapshot component slot; slots=%v", slots)
	}
	if snapshot.Type != reflect.TypeOf(gen.WorkbenchSnapshot{}) {
		t.Fatalf("Snapshot slot type = %v", snapshot.Type)
	}
	if snapshot.Visibility != actor.VisibilityPublic {
		t.Fatalf("Snapshot slot visibility = %v, want public", snapshot.Visibility)
	}
}
