package automesh

import (
	"reflect"
	"testing"
)

// filledRectMask returns a binary mask of size w×h with the rectangle
// [x0..x1) × [y0..y1) set to 255.
func filledRectMask(w, h, x0, y0, x1, y1 int) []byte {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	buf := make([]byte, w*h)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			buf[y*w+x] = 255
		}
	}
	return buf
}

// annulusMask returns a mask of size w×h with a filled outer rectangle and
// an empty hole inside it.
func annulusMask(w, h, x0, y0, x1, y1, hx0, hy0, hx1, hy1 int) []byte {
	buf := filledRectMask(w, h, x0, y0, x1, y1)
	for y := hy0; y < hy1; y++ {
		for x := hx0; x < hx1; x++ {
			buf[y*w+x] = 0
		}
	}
	return buf
}

func TestFindContours_EmptyMask(t *testing.T) {
	c, h := FindContours(nil, 0, 0, RetrievalExternal, ApproxSimple)
	if len(c) != 0 || len(h) != 0 {
		t.Fatalf("expected empty, got %d contours / %d hierarchy", len(c), len(h))
	}
}

func TestFindContours_AllZero(t *testing.T) {
	mask := make([]byte, 16*16)
	c, h := FindContours(mask, 16, 16, RetrievalExternal, ApproxNone)
	if len(c) != 0 || len(h) != 0 {
		t.Fatalf("expected empty on all-zero, got %d contours", len(c))
	}
}

func TestFindContours_SinglePixel(t *testing.T) {
	mask := make([]byte, 4*4)
	mask[1*4+1] = 255
	c, _ := FindContours(mask, 4, 4, RetrievalExternal, ApproxNone)
	if len(c) != 1 {
		t.Fatalf("expected 1 contour, got %d", len(c))
	}
	if len(c[0]) < 1 || c[0][0] != (Point{1, 1}) {
		t.Fatalf("contour should start at (1,1), got %v", c[0])
	}
}

func TestFindContours_SquareReturnsClosedLoop(t *testing.T) {
	mask := filledRectMask(10, 10, 3, 3, 7, 7)
	c, hier := FindContours(mask, 10, 10, RetrievalExternal, ApproxNone)
	if len(c) != 1 {
		t.Fatalf("expected 1 outer contour, got %d", len(c))
	}
	if len(c[0]) < 4 {
		t.Fatalf("contour too short: %d vertices", len(c[0]))
	}
	if len(hier) != 1 || hier[0].Parent != -1 || hier[0].Child != -1 {
		t.Fatalf("outer contour should have no parent/child, got %+v", hier[0])
	}
}

func TestFindContours_ExternalModeDropsHoles(t *testing.T) {
	mask := annulusMask(20, 20, 2, 2, 18, 18, 7, 7, 13, 13)
	cExt, _ := FindContours(mask, 20, 20, RetrievalExternal, ApproxNone)
	cCC, hCC := FindContours(mask, 20, 20, RetrievalCCOMP, ApproxNone)
	if len(cExt) != 1 {
		t.Fatalf("EXTERNAL should drop the hole: got %d contours", len(cExt))
	}
	if len(cCC) != 2 {
		t.Fatalf("CCOMP should return outer + hole: got %d", len(cCC))
	}
	if len(hCC) != 2 {
		t.Fatalf("hierarchy should have 2 entries, got %d", len(hCC))
	}
	// Find the parent (no parent) and child (parent != -1).
	var outerIdx, holeIdx = -1, -1
	for i, h := range hCC {
		if h.Parent == -1 {
			outerIdx = i
		} else {
			holeIdx = i
		}
	}
	if outerIdx == -1 || holeIdx == -1 {
		t.Fatalf("expected one outer and one hole, got outer=%d hole=%d", outerIdx, holeIdx)
	}
	if hCC[holeIdx].Parent != outerIdx {
		t.Fatalf("hole's parent should be outer, got %+v", hCC[holeIdx])
	}
	if hCC[outerIdx].Child != holeIdx {
		t.Fatalf("outer's child should be hole, got %+v", hCC[outerIdx])
	}
}

func TestFindContours_MultipleDisconnected(t *testing.T) {
	mask := make([]byte, 30*30)
	// Two squares at opposite corners.
	for y := 2; y < 6; y++ {
		for x := 2; x < 6; x++ {
			mask[y*30+x] = 255
		}
	}
	for y := 20; y < 28; y++ {
		for x := 20; x < 28; x++ {
			mask[y*30+x] = 255
		}
	}
	c, _ := FindContours(mask, 30, 30, RetrievalExternal, ApproxNone)
	if len(c) != 2 {
		t.Fatalf("expected 2 contours, got %d", len(c))
	}
}

func TestFindContours_TrimsShortBufferSafely(t *testing.T) {
	// Mask is shorter than w*h — should not panic and should return no contours.
	mask := []byte{255, 255}
	c, h := FindContours(mask, 10, 10, RetrievalExternal, ApproxNone)
	if len(c) != 0 || len(h) != 0 {
		t.Fatalf("expected empty on truncated mask, got %d/%d", len(c), len(h))
	}
}

func TestApproximateContour_TableDriven(t *testing.T) {
	// A polyline with three collinear points in the middle.
	pts := Contour{
		{0, 0}, {2, 0}, {4, 0}, {6, 0}, {6, 3},
		{3, 6}, {0, 3},
	}
	cases := []struct {
		name   string
		method ApproxMethod
		// Min number of remaining vertices (NONE preserves all; SIMPLE
		// removes the three collinear interior points; TC89 collapses
		// aggressively).
		minLen int
		maxLen int
	}{
		{"NONE keeps all", ApproxNone, 7, 7},
		{"SIMPLE removes collinear", ApproxSimple, 4, 5},
		{"TC89_L1 collapses", ApproxTC89L1, 3, 5},
		{"TC89_KCOS collapses", ApproxTC89KCos, 3, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApproximateContour(pts, tc.method)
			if len(got) < tc.minLen || len(got) > tc.maxLen {
				t.Fatalf("len = %d, want in [%d,%d]", len(got), tc.minLen, tc.maxLen)
			}
			// Always start/end at the original first/last pixel.
			if got[0] != pts[0] {
				t.Fatalf("first point mutated: %+v vs %+v", got[0], pts[0])
			}
			if got[len(got)-1] != pts[len(pts)-1] {
				t.Fatalf("last point mutated: %+v vs %+v", got[len(got)-1], pts[len(pts)-1])
			}
		})
	}
}

func TestApproximateContour_HandlesShortInput(t *testing.T) {
	got := ApproximateContour(Contour{{0, 0}, {1, 1}}, ApproxSimple)
	if !reflect.DeepEqual(got, Contour{{0, 0}, {1, 1}}) {
		t.Fatalf("got %v, want original", got)
	}
	got = ApproximateContour(Contour{{5, 5}}, ApproxTC89L1)
	if !reflect.DeepEqual(got, Contour{{5, 5}}) {
		t.Fatalf("got %v, want original", got)
	}
}

func TestRamerDouglasPeucker_CollinearStaysTwoPoints(t *testing.T) {
	pts := []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}}
	got := ramerDouglasPeucker(pts, 0.5)
	want := []Point{{0, 0}, {4, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collinear DP = %v, want %v", got, want)
	}
}

func TestPointInPolygon_TableDriven(t *testing.T) {
	square := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"inside", Point{5, 5}, true},
		{"outside", Point{20, 20}, false},
		{"on-edge (cast as outside)", Point{10, 5}, false},
		{"below", Point{5, -5}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pointInPolygon(tc.p, square); got != tc.want {
				t.Fatalf("pointInPolygon(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestBoundingBox(t *testing.T) {
	c := Contour{{3, 4}, {1, 0}, {-2, 9}, {7, 2}}
	mnx, mny, mxx, mxy := boundingBox(c)
	if mnx != -2 || mny != 0 || mxx != 7 || mxy != 9 {
		t.Fatalf("bbox = (%d,%d,%d,%d), want (-2,0,7,9)", mnx, mny, mxx, mxy)
	}
	if mnx, mny, mxx, mxy = boundingBox(nil); mnx != 0 || mny != 0 || mxx != -1 || mxy != -1 {
		t.Fatalf("empty bbox = (%d,%d,%d,%d), want (0,0,-1,-1)", mnx, mny, mxx, mxy)
	}
}

func TestSortContoursCCW_TrivialInput(t *testing.T) {
	if got := SortContoursCCW(nil); len(got) != 0 {
		t.Fatalf("nil contour: got %v, want nil", got)
	}
	square := Contour{{0, 0}, {2, 0}, {2, 2}, {0, 2}}
	sorted := SortContoursCCW(square)
	if len(sorted) != 4 {
		t.Fatalf("expected 4 vertices, got %d", len(sorted))
	}
}

func TestFindContours_AnnulusPixelCounts(t *testing.T) {
	// Sanity check: a wide annulus should yield exactly 2 CCOMP contours.
	mask := annulusMask(40, 40, 4, 4, 36, 36, 14, 14, 26, 26)
	c, h := FindContours(mask, 40, 40, RetrievalCCOMP, ApproxNone)
	if len(c) != 2 || len(h) != 2 {
		t.Fatalf("got %d contours / %d hier", len(c), len(h))
	}
	// The outer contour should have no parent; the hole should have the
	// outer as its parent.
	var outerIdx, holeIdx = -1, -1
	for i, hi := range h {
		if hi.Parent == -1 {
			outerIdx = i
		} else {
			holeIdx = i
		}
	}
	if outerIdx == -1 || holeIdx == -1 {
		t.Fatalf("expected one outer and one hole, got outer=%d hole=%d", outerIdx, holeIdx)
	}
	if h[holeIdx].Parent != outerIdx {
		t.Fatalf("hole parent = %d, want %d", h[holeIdx].Parent, outerIdx)
	}
	// The hole contour's bbox should be strictly inside the outer's bbox.
	oMinX, oMinY, oMaxX, oMaxY := boundingBox(c[outerIdx])
	hMinX, hMinY, hMaxX, hMaxY := boundingBox(c[holeIdx])
	if hMinX <= oMinX || hMinY <= oMinY || hMaxX >= oMaxX || hMaxY >= oMaxY {
		t.Fatalf("hole bbox (%d,%d,%d,%d) not strictly inside outer bbox (%d,%d,%d,%d)",
			hMinX, hMinY, hMaxX, hMaxY, oMinX, oMinY, oMaxX, oMaxY)
	}
}
