package automesh

import (
	"math"
	"reflect"
	"sort"
	"testing"
)

// uniquePoints returns the vertex indices in a triangle list sorted in
// ascending order.
func uniquePoints(tris [][3]int) []int {
	seen := map[int]struct{}{}
	for _, t := range tris {
		for _, i := range t {
			seen[i] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

func TestDelaunay_FewerThanThreePoints(t *testing.T) {
	cases := [][]PointF{
		nil,
		{},
		{{1, 1}},
		{{0, 0}, {1, 1}},
	}
	for i, pts := range cases {
		verts, tris := Delaunay(pts)
		if verts != nil || tris != nil {
			t.Fatalf("case %d: expected (nil, nil), got (%v, %v)", i, verts, tris)
		}
	}
}

func TestDelaunay_Collinear(t *testing.T) {
	verts, tris := Delaunay([]PointF{{0, 0}, {1, 0}, {2, 0}, {3, 0}})
	if len(tris) != 0 {
		t.Fatalf("expected no triangles on collinear input, got %d", len(tris))
	}
	// Delaunay returns a defensive copy of the input vertices even when
	// triangulation produces no triangles.
	if len(verts) != 4 {
		t.Fatalf("expected 4 verts (defensive copy), got %d", len(verts))
	}
}

func TestDelaunay_DuplicatePoints(t *testing.T) {
	// After deduplication, {0,0}, {10,0}, {10,10}, {0,10} form a valid
	// square, so the triangulator should produce 2 triangles. The
	// duplicate {10,10} is silently collapsed.
	pts := []PointF{{0, 0}, {10, 0}, {10, 10}, {10, 10}, {0, 10}}
	_, tris := Delaunay(pts)
	if len(tris) != 2 {
		t.Fatalf("expected 2 triangles after dedup, got %d", len(tris))
	}
}

func TestDelaunay_Square(t *testing.T) {
	verts, tris := Delaunay([]PointF{{0, 0}, {10, 0}, {10, 10}, {0, 10}})
	if len(tris) != 2 {
		t.Fatalf("square should yield exactly 2 triangles, got %d", len(tris))
	}
	// Every vertex should be used.
	if got := uniquePoints(tris); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("triangle vertex indices = %v, want all 4", got)
	}
	if !trianglesAreCCW(verts, tris) {
		t.Fatal("triangles should be CCW")
	}
}

func TestDelaunay_DoesNotMutateInput(t *testing.T) {
	in := []PointF{{0, 0}, {1, 0}, {0, 1}}
	snap := append([]PointF(nil), in...)
	_, _ = Delaunay(in)
	if !reflect.DeepEqual(in, snap) {
		t.Fatalf("input mutated: got %v, want %v", in, snap)
	}
}

func TestDelaunay_ConvexHullIsUsed(t *testing.T) {
	// 5 points forming a convex pentagon. The triangulation should use all
	// 5 vertices.
	pts := []PointF{
		{0, 0}, {10, 0}, {13, 5}, {5, 13}, {-3, 5},
	}
	_, tris := Delaunay(pts)
	if got := uniquePoints(tris); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("expected all 5 vertices used, got %v", got)
	}
	// Total area should equal the pentagon's area (no overlap, no gaps).
	area := 0.0
	for _, tr := range tris {
		area += math.Abs(TriArea(pts[tr[0]], pts[tr[1]], pts[tr[2]]))
	}
	wantArea := math.Abs(polygonArea(pts))
	if math.Abs(area-wantArea) > 1e-6 {
		t.Fatalf("triangulated area = %v, want pentagon area %v", area, wantArea)
	}
}

// polygonArea returns the signed polygon area (CCW = positive).
func polygonArea(pts []PointF) float64 {
	a := 0.0
	n := len(pts)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		a += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return a / 2
}

func TestDelaunay_InteriorPoint(t *testing.T) {
	// Square with a single interior point — the pentagon must split into
	// 4 triangles touching the interior point.
	pts := []PointF{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, 5}}
	_, tris := Delaunay(pts)
	if len(tris) != 4 {
		t.Fatalf("square + interior → 4 tris, got %d", len(tris))
	}
	// The interior vertex (index 4) must appear in every triangle.
	for i, tr := range tris {
		has4 := tr[0] == 4 || tr[1] == 4 || tr[2] == 4
		if !has4 {
			t.Fatalf("triangle %d does not contain interior vertex: %v", i, tr)
		}
	}
}

func TestAutoTriangulate_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		pts       []PointF
		wantTris  int
	}{
		{"empty", nil, 0},
		{"one point", []PointF{{0, 0}}, 0},
		{"collinear", []PointF{{0, 0}, {1, 0}, {2, 0}}, 0},
		{"triangle", []PointF{{0, 0}, {1, 0}, {0, 1}}, 1},
		{"square", []PointF{{0, 0}, {1, 0}, {1, 1}, {0, 1}}, 2},
		{"pentagon", []PointF{{0, 0}, {4, 0}, {5, 3}, {2, 5}, {-1, 3}}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AutoTriangulate(tc.pts)
			if len(m.Tris) != tc.wantTris {
				t.Fatalf("tris = %d, want %d", len(m.Tris), tc.wantTris)
			}
			if len(m.Tris) > 0 {
				if !trianglesAreCCW(m.Vertices, m.Tris) {
					t.Fatal("CCW invariant violated")
				}
			}
		})
	}
}

func TestEdgesUnique_NoDuplicates(t *testing.T) {
	tris := [][3]int{
		{0, 1, 2},
		{0, 2, 3},
	}
	edges := edgesUnique(tris)
	want := []edge{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {2, 3}}
	if !reflect.DeepEqual(edges, want) {
		t.Fatalf("got %v, want %v", edges, want)
	}
}

func TestTriArea(t *testing.T) {
	// Counter-clockwise triangle (image coordinates are Y-down; here we use
	// math orientation).
	cases := []struct {
		name string
		a, b, c PointF
		want float64
	}{
		{"CCW unit", PointF{0, 0}, PointF{1, 0}, PointF{0, 1}, 0.5},
		{"CW unit", PointF{0, 0}, PointF{0, 1}, PointF{1, 0}, -0.5},
		{"collinear", PointF{0, 0}, PointF{1, 0}, PointF{2, 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TriArea(tc.a, tc.b, tc.c)
			if math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPointKey_DeterministicForNearPoints(t *testing.T) {
	a := pointKey(PointF{1.0000001, 2.0000002}, 1e-6)
	b := pointKey(PointF{1.0000002, 2.0000001}, 1e-6)
	if a != b {
		t.Fatalf("near-duplicate points produced different keys: %x vs %x", a, b)
	}
	c := pointKey(PointF{1.001, 2.0}, 1e-6)
	if a == c {
		t.Fatal("far-apart points should not collide")
	}
}

func TestAllCollinear(t *testing.T) {
	if !allCollinear([]PointF{{0, 0}, {1, 1}, {2, 2}}) {
		t.Fatal("diagonal line should be collinear")
	}
	if allCollinear([]PointF{{0, 0}, {1, 0}, {0, 1}}) {
		t.Fatal("right triangle should not be collinear")
	}
	if !allCollinear(nil) {
		t.Fatal("nil should be considered collinear (vacuously)")
	}
	if !allCollinear([]PointF{{0, 0}}) {
		t.Fatal("single point should be collinear")
	}
}
