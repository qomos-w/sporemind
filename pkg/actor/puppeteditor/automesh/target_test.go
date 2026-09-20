package automesh

import (
	"math"
	"reflect"
	"testing"
)

func TestMapToNodeLocal_EmptyInput(t *testing.T) {
	if got := MapToNodeLocal(nil, IdentityTarget()); got != nil {
		t.Fatalf("nil input: got %v, want nil", got)
	}
	if got := MapToNodeLocal([]PointF{}, IdentityTarget()); got != nil {
		t.Fatalf("empty input: got %v, want nil", got)
	}
}

func TestMapToNodeLocal_Identity(t *testing.T) {
	verts := []PointF{{1, 2}, {3, 4}}
	got := MapToNodeLocal(verts, IdentityTarget())
	if !reflect.DeepEqual(got, verts) {
		t.Fatalf("identity should be a no-op, got %v", got)
	}
}

func TestMapToNodeLocal_ProviderTranslation(t *testing.T) {
	// World center (10, 20), inverse matrix = identity (so world==local).
	verts := []PointF{{0, 0}, {1, 1}}
	tgt := AffineTarget(1, 0, 0, 0, 1, 0, [2]float64{10, 20})
	got := MapToNodeLocal(verts, tgt)
	want := []PointF{{10, 20}, {11, 21}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMapToNodeLocal_ProviderRotation(t *testing.T) {
	// Rotate by -90° about the world center using the affine inverse.
	cosA, sinA := math.Cos(math.Pi/2), math.Sin(math.Pi/2)
	// 90° CCW rotation matrix [cos -sin; sin cos].
	tgt := AffineTarget(cosA, -sinA, 0, sinA, cosA, 0, [2]float64{0, 0})
	verts := []PointF{{1, 0}, {0, 1}, {-1, 0}}
	got := MapToNodeLocal(verts, tgt)
	// (1,0) → (0,1); (0,1) → (-1,0); (-1,0) → (0,-1).
	want := []PointF{{0, 1}, {-1, 0}, {0, -1}}
	for i := range got {
		if math.Abs(got[i].X-want[i].X) > 1e-9 || math.Abs(got[i].Y-want[i].Y) > 1e-9 {
			t.Fatalf("vertex %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMapToNodeLocal_TextureOffset(t *testing.T) {
	verts := []PointF{{1, 2}, {3, 4}}
	got := MapToNodeLocal(verts, TextureTarget([2]float64{7, -1}))
	want := []PointF{{8, 1}, {10, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("texture offset: got %v, want %v", got, want)
	}
}

func TestMapToNodeLocal_DoesNotMutateInput(t *testing.T) {
	verts := []PointF{{1, 2}, {3, 4}}
	snap := append([]PointF(nil), verts...)
	_ = MapToNodeLocal(verts, AffineTarget(2, 0, 5, 0, 2, 7, [2]float64{1, 1}))
	if !reflect.DeepEqual(verts, snap) {
		t.Fatalf("input mutated")
	}
}

func TestIdentityTarget_HasExpectedShape(t *testing.T) {
	tgt := IdentityTarget()
	want := [6]float64{1, 0, 0, 0, 1, 0}
	if tgt.InvMatrix != want {
		t.Fatalf("identity matrix = %v, want %v", tgt.InvMatrix, want)
	}
	if tgt.FromProvider {
		t.Fatal("identity should not be from-provider")
	}
}
