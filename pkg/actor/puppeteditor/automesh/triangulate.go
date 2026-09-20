package automesh

import (
	"math"
	"sort"
)

// Mesh is the AutoMesh output: a unique vertex set plus an indexed triangle
// list. It mirrors IncMesh's vertex/triangle layout as produced by
// mesh.d autoTriangulate (contours.d:185, 275).
//
// Vertices and Tris may be empty when the pipeline yields too few points or
// when the points are degenerate. The slice contents are always fresh (the
// caller can mutate them).
type Mesh struct {
	Vertices []PointF
	Tris     [][3]int
}

// edge is an undirected edge between two vertex indices, kept canonical
// (smaller index first) so it can be looked up symmetrically.
type edge struct{ A, B int }

// tri is a working record for Bowyer-Watson: vertex indices in CCW order,
// the three directed edges, the circumcircle, and a validity flag for
// degenerate input.
type tri struct {
	I     [3]int
	E     [3]edge
	C     PointF
	R     float64
	valid bool
}

// orient2 returns >0 if (a, b, c) is CCW, <0 if CW, 0 if collinear.
func orient2(a, b, c PointF) float64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// inCircumcircle reports whether p lies strictly inside the circumcircle of
// triangle t (within eps).
func inCircumcircle(t tri, p PointF, eps float64) bool {
	dx := p.X - t.C.X
	dy := p.Y - t.C.Y
	return dx*dx+dy*dy < t.R-eps
}

// circumcircle computes the circumcenter and squared radius of the triangle
// (a, b, c). Returns ok=false for collinear points.
func circumcircle(a, b, c PointF) (PointF, float64, bool) {
	ax, ay := a.X, a.Y
	bx, by := b.X, b.Y
	cx, cy := c.X, c.Y
	d := 2 * (ax*(by-cy) + bx*(cy-ay) + cx*(ay-by))
	if math.Abs(d) < 1e-20 {
		return PointF{}, 0, false
	}
	ax2ay2 := ax*ax + ay*ay
	bx2by2 := bx*bx + by*by
	cx2cy2 := cx*cx + cy*cy
	ux := (ax2ay2*(by-cy) + bx2by2*(cy-ay) + cx2cy2*(ay-by)) / d
	uy := (ax2ay2*(cx-bx) + bx2by2*(ax-cx) + cx2cy2*(bx-ax)) / d
	center := PointF{ux, uy}
	r2 := (ax-ux)*(ax-ux) + (ay-uy)*(ay-uy)
	return center, r2, true
}

// delaunayCore implements the Bowyer-Watson incremental algorithm. pts must
// have at least 3 points (caller checks). It returns the triangle indices
// referencing the supplied slice.
func delaunayCore(pts []PointF) [][3]int {
	n := len(pts)
	if n < 3 {
		return nil
	}

	// Deduplicate near-coincident points first so we don't have to handle
	// degenerate insertion.
	unique := make([]PointF, 0, n)
	seen := make(map[uint64]struct{}, n)
	for _, p := range pts {
		key := pointKey(p, 1e-6)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, p)
	}
	if len(unique) < 3 {
		return nil
	}
	if allCollinear(unique) {
		return nil
	}

	// Compute a super-triangle large enough to enclose all points.
	minX, minY, maxX, maxY := unique[0].X, unique[0].Y, unique[0].X, unique[0].Y
	for _, p := range unique {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	dx := maxX - minX
	dy := maxY - minY
	if dx == 0 {
		dx = 1
	}
	if dy == 0 {
		dy = 1
	}
	midX := (minX + maxX) / 2
	midY := (minY + maxY) / 2
	// A generous super-triangle (Delaunay implementations rely on the
	// "superset" property — the super-triangle is removed at the end).
	dMax := math.Hypot(dx, dy) * 20
	stA := PointF{midX - 2*dMax, midY - dMax}
	stB := PointF{midX + 2*dMax, midY - dMax}
	stC := PointF{midX, midY + 2*dMax}

	// Working vertex set: unique points + 3 super-triangle points. The
	// unique points occupy indices [0, len(unique)); super-triangle is
	// appended at the end so we can identify it later.
	all := make([]PointF, 0, len(unique)+3)
	all = append(all, unique...)
	stOff := len(all)
	all = append(all, stA, stB, stC)

	seed := newTri([3]int{stOff + 0, stOff + 1, stOff + 2}, all)
	if !seed.valid {
		return nil
	}
	tris := []*tri{seed}

	const eps = 1e-12

	for _, p := range unique {
		bad := make([]*tri, 0, 8)
		for _, t := range tris {
			if inCircumcircle(*t, p, eps) {
				bad = append(bad, t)
			}
		}
		if len(bad) == 0 {
			continue
		}

		// Collect the polygon boundary: edges that appear in exactly one
		// bad triangle.
		edgeCount := make(map[edge]int)
		for _, t := range bad {
			for _, e := range t.E {
				edgeCount[canonical(e)]++
			}
		}

		// Remove bad triangles.
		keep := tris[:0]
		for _, t := range tris {
			isBad := false
			for _, b := range bad {
				if b == t {
					isBad = true
					break
				}
			}
			if !isBad {
				keep = append(keep, t)
			}
		}
		tris = keep

		// Find the index of p in `all`. p is a copy of a point in `unique`,
		// which is the prefix of `all`, so we can locate it directly.
		pIdx := -1
		for i, q := range all[:stOff] {
			if q == p {
				pIdx = i
				break
			}
		}
		if pIdx == -1 {
			continue
		}

		// Re-triangulate the cavity using the boundary edges.
		for e, cnt := range edgeCount {
			if cnt != 1 {
				continue
			}
			nt := newTri([3]int{e.A, e.B, pIdx}, all)
			if nt.valid {
				tris = append(tris, nt)
			}
		}
	}

	// Drop triangles that share a vertex with the super-triangle.
	finalTris := make([][3]int, 0, len(tris))
	for _, t := range tris {
		if t.I[0] >= stOff || t.I[1] >= stOff || t.I[2] >= stOff {
			continue
		}
		finalTris = append(finalTris, t.I)
	}
	return finalTris
}

// pointKey hashes a 2D point with a small grid size so near-duplicate points
// collapse to the same bucket.
func pointKey(p PointF, grid float64) uint64 {
	const mask uint64 = 1 << 63
	if grid <= 0 {
		grid = 1e-6
	}
	gx := int64(math.Round(p.X / grid))
	gy := int64(math.Round(p.Y / grid))
	return (mask ^ uint64(gx)) ^ (uint64(gy) * 0x9E3779B97F4A7C15)
}

// canonical returns the edge in (min, max) order so an undirected lookup is
// symmetric.
func canonical(e edge) edge {
	if e.A < e.B {
		return e
	}
	return edge{e.B, e.A}
}

// allCollinear returns true if every triple of points is collinear. Used as a
// short-circuit for degenerate point clouds.
func allCollinear(pts []PointF) bool {
	if len(pts) < 3 {
		return true
	}
	a, b := pts[0], pts[1]
	for i := 2; i < len(pts); i++ {
		if orient2(a, b, pts[i]) != 0 {
			return false
		}
	}
	return true
}

// newTri builds a tri record from a vertex index triple and a vertex pool.
// It picks the canonical CCW orientation, computes the circumcircle, and
// orders the directed edges accordingly. Returns valid=false for degenerate
// (collinear) input.
func newTri(idx [3]int, all []PointF) *tri {
	a := all[idx[0]]
	b := all[idx[1]]
	c := all[idx[2]]
	o := orient2(a, b, c)
	if o == 0 {
		return &tri{I: idx, valid: false}
	}
	if o < 0 {
		// Swap b and c so the triangle is CCW.
		idx = [3]int{idx[0], idx[2], idx[1]}
		a, b, c = all[idx[0]], all[idx[1]], all[idx[2]]
	}
	cc, r2, ok := circumcircle(a, b, c)
	if !ok {
		return &tri{I: idx, valid: false}
	}
	return &tri{
		I:     idx,
		E:     [3]edge{{idx[0], idx[1]}, {idx[1], idx[2]}, {idx[2], idx[0]}},
		C:     cc,
		R:     r2,
		valid: true,
	}
}

// Delaunay runs Bowyer-Watson on the given point set. The input is not
// mutated. The returned triangle indices reference the returned Vertices
// slice (which is a defensive copy of the input), so callers may freely
// modify either without affecting the other.
//
// For fewer than 3 unique, non-collinear points the function returns
// (nil, nil).
func Delaunay(pts []PointF) (verts []PointF, tris [][3]int) {
	if len(pts) < 3 {
		return nil, nil
	}
	verts = make([]PointF, len(pts))
	copy(verts, pts)
	tris = delaunayCore(verts)
	if len(tris) == 0 {
		// Return the deduplicated vertex slice so callers still see a
		// consistent (empty-triangle) result.
		return verts, nil
	}
	return verts, tris
}

// AutoTriangulate is the public façade mirroring mesh.d autoTriangulate. It
// returns a Mesh whose Vertices are the (deduplicated) input points and
// whose Tris reference them.
//
// For fewer than 3 vertices, or when the points are all collinear, the
// returned Mesh has no triangles.
func AutoTriangulate(pts []PointF) Mesh {
	verts, tris := Delaunay(pts)
	return Mesh{Vertices: verts, Tris: tris}
}

// TriArea returns the signed area of the triangle (a, b, c). Positive means
// CCW in standard math (Y-up) coordinates; negative means CW. Convenience
// helper for tests.
func TriArea(a, b, c PointF) float64 {
	return 0.5 * orient2(a, b, c)
}

// trianglesAreCCW reports whether each triangle in tris is wound CCW when
// viewed in the standard image-coordinate frame (Y-down). Used by tests.
func trianglesAreCCW(verts []PointF, tris [][3]int) bool {
	for _, t := range tris {
		o := orient2(verts[t[0]], verts[t[1]], verts[t[2]])
		if o <= 0 {
			return false
		}
	}
	return true
}

// edgesUnique returns the set of undirected unique edges in the triangle
// list. Used by tests to verify the mesh is a proper triangulation.
func edgesUnique(tris [][3]int) []edge {
	set := make(map[edge]struct{}, 3*len(tris))
	for _, t := range tris {
		set[canonical(edge{t[0], t[1]})] = struct{}{}
		set[canonical(edge{t[1], t[2]})] = struct{}{}
		set[canonical(edge{t[2], t[0]})] = struct{}{}
	}
	out := make([]edge, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}
