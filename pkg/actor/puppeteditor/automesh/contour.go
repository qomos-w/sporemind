package automesh

import (
	"math"
	"sort"
)

// RetrievalMode mirrors OpenCV's contour retrieval modes. The Go port supports
// the subset that Inochi2D's AutoMesh pipeline actually requests (EXTERNAL
// and CCOMP). LIST and TREE are provided for completeness and unit testing.
type RetrievalMode int

const (
	// RetrievalExternal returns only outer contours. No holes.
	RetrievalExternal RetrievalMode = iota
	// RetrievalList returns all contours; no parent/child relationship is
	// recorded (Hierarchy.Parent and Hierarchy.Child stay -1).
	RetrievalList
	// RetrievalCCOMP returns a two-level hierarchy: every outer contour has
	// its directly-enclosed holes as children.
	RetrievalCCOMP
	// RetrievalTree returns the full hierarchy (used only in tests here).
	RetrievalTree
)

// ApproxMethod mirrors OpenCV's contour approximation methods.
type ApproxMethod int

const (
	// ApproxNone keeps every traced pixel.
	ApproxNone ApproxMethod = iota
	// ApproxSimple removes redundant points lying on a straight segment
	// (the OpenCV "SIMPLE" algorithm).
	ApproxSimple
	// ApproxTC89L1 applies Douglas-Peucker (epsilon = 2 px), matching the
	// TC89_L1 variant in nijigenerate.core.cv.contours.
	ApproxTC89L1
	// ApproxTC89KCos applies Douglas-Peucker (epsilon = 2 px), matching the
	// TC89_KCOS variant.
	ApproxTC89KCos
)

// Contour is a closed pixel-coordinate polyline returned by FindContours.
// Coordinates are integer pixel indices (top-left origin), tracing the
// perimeter of an outer boundary or hole in a binary mask.
type Contour []Point

// Hierarchy mirrors OpenCV's [next, prev, child, parent] tuple. A value of
// -1 means "no such relation".
type Hierarchy struct {
	Next, Prev, Child, Parent int
}

// inBounds reports whether (x, y) lies inside the w×h rectangle.
func inBounds(x, y, w, h int) bool { return x >= 0 && x < w && y >= 0 && y < h }

// 8-neighbor offsets in clockwise order (right-hand-rule), starting at E.
// Matches contours.d DIRECTIONS in nijigenerate.
// dirMap returns the index in `directions` for the relative offset (dx, dy),
// or -1 for (0, 0).
func dirMap(dx, dy int) int {
	switch {
	case dx == 1 && dy == 0:
		return 0
	case dx == 1 && dy == 1:
		return 1
	case dx == 0 && dy == 1:
		return 2
	case dx == -1 && dy == 1:
		return 3
	case dx == -1 && dy == 0:
		return 4
	case dx == -1 && dy == -1:
		return 5
	case dx == 0 && dy == -1:
		return 6
	case dx == 1 && dy == -1:
		return 7
	default:
		return -1
	}
}

// suzukiAbeContour traces a single contour using the Suzuki-Abe right-hand
// rule, exactly mirroring nijigenerate.core.cv.contours.suzukiAbeContour.
//
// mask is the binary mask (W*H bytes, each 0 or non-zero), labels is a working
// buffer of the same size. label is the integer id assigned to this contour
// (must be ≥ 1).
//
// The returned slice always includes the start pixel and ends when the
// algorithm closes the loop or no neighbor can be found. The slice may have
// length 1 in degenerate cases.
//
// isHole controls the initial "previous" point direction. For outer contours
// the tracer seeds p above the start (matching the standard Suzuki-Abe scan
// that encounters the boundary from the left). For hole contours the tracer
// seeds p to the left of the start (the background/hole-interior side), which
// reverses the trace direction so the hole perimeter is followed correctly
// instead of wandering along the outer boundary's inner edge.
func suzukiAbeContour(mask []byte, w, h int, labels []int, label int, startX, startY int, isHole bool) Contour {
	if !inBounds(startX, startY, w, h) || mask[startY*w+startX] == 0 {
		return Contour{}
	}

	b := Point{startX, startY}
	c := b
	// Initial p depends on contour type: above for outer, left for hole.
	var p Point
	if isHole {
		p = Point{startX - 1, startY}
	} else {
		p = Point{startX, startY - 1}
	}
	contour := make(Contour, 0, 256)
	contour = append(contour, b)
	labels[startY*w+startX] = label
	firstIteration := true

	for {
		dx := p.X - c.X
		dy := p.Y - c.Y
		startDir := 0
		if idx := dirMap(dx, dy); idx != -1 {
			startDir = (idx + 1) % 8
		}

		found := false
		var next Point
		nextDir := startDir
		for i := 0; i < 8; i++ {
			idx := (startDir + i) % 8
			nx := c.X + directions[idx][0]
			ny := c.Y + directions[idx][1]
			if !inBounds(nx, ny, w, h) {
				continue
			}
			if mask[ny*w+nx] != 0 {
				l := labels[ny*w+nx]
				// Allow: unprocessed (0), current label, OR the start pixel
				// during later iterations (matches contours.d:119 semantics).
				if l == 0 || l == label || (nx == b.X && ny == b.Y && !firstIteration) {
					next = Point{nx, ny}
					nextDir = idx
					found = true
					break
				}
			}
		}
		if !found {
			break
		}

		// Update p: shift to the (nextDir + 7) % 8 direction.
		shiftIdx := (nextDir + 7) % 8
		p = Point{c.X + directions[shiftIdx][0], c.Y + directions[shiftIdx][1]}
		c = next

		// Skip label update on revisits of the start pixel.
		if !(c.X == b.X && c.Y == b.Y) {
			labels[c.Y*w+c.X] = label
		}
		contour = append(contour, c)

		if !firstIteration && c.X == b.X && c.Y == b.Y {
			break
		}
		firstIteration = false
	}

	return contour
}

// boundingBox computes the axis-aligned bounding box of a contour.
func boundingBox(c Contour) (minX, minY, maxX, maxY int) {
	if len(c) == 0 {
		return 0, 0, -1, -1
	}
	minX, minY = c[0].X, c[0].Y
	maxX, maxY = minX, minY
	for _, p := range c {
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
	return
}

// pointInPolygon returns true if p lies inside the (closed) polygon using the
// standard even-odd ray-casting test.
func pointInPolygon(p Point, poly []Point) bool {
	n := len(poly)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		a := poly[i]
		b := poly[j]
		if (a.Y > p.Y) != (b.Y > p.Y) {
			denom := float64(b.Y - a.Y)
			if denom == 0 {
				j = i
				continue
			}
			atX := float64(a.X) + (float64(p.Y-a.Y)/denom)*float64(b.X-a.X)
			if float64(p.X) < atX {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// ramerDouglasPeucker is the Douglas-Peucker simplification used for TC89_L1
// and TC89_KCOS. epsilon is the max perpendicular distance kept.
//
// Recursive for clarity; tests stay on small contours.
func ramerDouglasPeucker(points []Point, epsilon float64) []Point {
	if len(points) < 3 {
		out := make([]Point, len(points))
		copy(out, points)
		return out
	}

	// Find the point with the max distance to line (first → last).
	first := points[0]
	last := points[len(points)-1]
	maxDist := 0.0
	index := 0
	for i := 1; i < len(points)-1; i++ {
		d := distanceToSegment(points[i], first, last)
		if d > maxDist {
			maxDist = d
			index = i
		}
	}

	if maxDist > epsilon {
		left := ramerDouglasPeucker(points[:index+1], epsilon)
		right := ramerDouglasPeucker(points[index:], epsilon)
		return append(left[:len(left)-1], right...)
	}
	return []Point{first, last}
}

// distanceToSegment computes the perpendicular distance from p to segment
// (p1, p2).
func distanceToSegment(p, p1, p2 Point) float64 {
	ax := float64(p.X - p1.X)
	ay := float64(p.Y - p1.Y)
	cx := float64(p2.X - p1.X)
	cy := float64(p2.Y - p1.Y)
	lenSq := cx*cx + cy*cy
	param := -1.0
	if lenSq != 0 {
		param = (ax*cx + ay*cy) / lenSq
	}
	var xx, yy float64
	switch {
	case param < 0:
		xx, yy = float64(p1.X), float64(p1.Y)
	case param > 1:
		xx, yy = float64(p2.X), float64(p2.Y)
	default:
		xx = float64(p1.X) + param*cx
		yy = float64(p1.Y) + param*cy
	}
	dx := float64(p.X) - xx
	dy := float64(p.Y) - yy
	return math.Hypot(dx, dy)
}

// ApproximateContour applies the chosen OpenCV-compatible method to a single
// contour. SIMPLE removes collinear points; TC89_L1 / TC89_KCOS run
// Douglas-Peucker with epsilon = 2 px. NONE returns the input unchanged.
func ApproximateContour(c Contour, method ApproxMethod) Contour {
	switch method {
	case ApproxNone:
		out := make(Contour, len(c))
		copy(out, c)
		return out
	case ApproxSimple:
		if len(c) < 3 {
			out := make(Contour, len(c))
			copy(out, c)
			return out
		}
		out := make(Contour, 0, len(c))
		out = append(out, c[0])
		for i := 1; i < len(c)-1; i++ {
			prev := c[i-1]
			curr := c[i]
			next := c[i+1]
			// Keep curr unless it lies on the line segment prev→next.
			if (curr.X-prev.X)*(next.Y-curr.Y) == (curr.Y-prev.Y)*(next.X-curr.X) {
				continue
			}
			out = append(out, curr)
		}
		out = append(out, c[len(c)-1])
		return out
	case ApproxTC89L1, ApproxTC89KCos:
		return ramerDouglasPeucker(c, 2.0)
	default:
		out := make(Contour, len(c))
		copy(out, c)
		return out
	}
}

// fillContourInterior marks all pixels strictly inside the contour with -1
// so subsequent scan iterations skip them. Ported from contours.d
// fillContourInterior (scanline fill between pairs of edge intersections).
func fillContourInterior(labels []int, contour Contour, w, h int, mask []byte) {
	if len(contour) == 0 {
		return
	}
	_, minY, _, maxY := boundingBox(contour)
	for y := minY; y <= maxY; y++ {
		var inters []float64
		for i := 0; i < len(contour); i++ {
			a := contour[i]
			b := contour[(i+1)%len(contour)]
			if (a.Y <= y && b.Y > y) || (a.Y > y && b.Y <= y) {
				atX := float64(a.X) + (float64(y-a.Y)/float64(b.Y-a.Y))*float64(b.X-a.X)
				inters = append(inters, atX)
			}
		}
		if len(inters) < 2 {
			continue
		}
		sort.Float64s(inters)
		for i := 0; i+1 < len(inters); i += 2 {
			start := int(math.Ceil(inters[i]))
			end := int(math.Floor(inters[i+1]))
			for x := start; x <= end; x++ {
				if inBounds(x, y, w, h) && labels[y*w+x] == 0 && mask[y*w+x] == 0 {
					labels[y*w+x] = -1
				}
			}
		}
	}
}

// FindContours runs Suzuki-Abe on a binary mask (W*H bytes; non-zero means
// "foreground") and returns the contours and per-contour hierarchy. The mask
// is treated as read-only.
//
// The implementation mirrors nijigenerate.core.cv.contours.findContours:
//   - The scan loop only starts a trace when the pixel's left mask neighbor
//     is background; every traced contour's interior is then filled with -1
//     (via fillContourInterior) so interior pixels are never re-traced.
//   - EXTERNAL filters out contours whose hierarchy parent is set.
//   - CCOMP / TREE build the parent/child hierarchy via containment tests.
//   - LIST returns the flat contour list with an empty hierarchy.
//
// Hierarchy indices are -1 when no relation exists. ApproximateContour is
// applied per-contour using `method`.
func FindContours(mask []byte, w, h int, mode RetrievalMode, method ApproxMethod) ([]Contour, []Hierarchy) {
	if w <= 0 || h <= 0 || len(mask) < w*h {
		return nil, nil
	}

	labels := make([]int, w*h)
	var contours []Contour
	nextLabel := 1

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if mask[idx] == 0 {
				continue
			}
			leftMask := byte(0)
			leftLabel := 0
			if x > 0 {
				leftMask = mask[y*w+(x-1)]
				leftLabel = labels[y*w+(x-1)]
			}
			if leftMask != 0 {
				// Interior pixel of an already-traced region.
				if labels[idx] == 0 {
					labels[idx] = -1
				}
				continue
			}
			// leftMask == 0: potential boundary start.
			if labels[idx] != 0 && labels[idx] != -1 {
				// Already traced (part of an existing contour).
				continue
			}
			if leftLabel != 0 && mode != RetrievalExternal {
				// Hole boundary: left neighbor is background inside a
				// previously-traced outer region. Skip in EXTERNAL mode.
				contour := suzukiAbeContour(mask, w, h, labels, nextLabel, x, y, true)
				if len(contour) > 0 {
					contours = append(contours, contour)
					nextLabel++
				}
			} else if leftLabel != 0 {
				// EXTERNAL mode: mark as interior, don't trace hole.
				labels[idx] = -1
			} else {
				// Outer boundary of a new region: trace + fill interior.
				contour := suzukiAbeContour(mask, w, h, labels, nextLabel, x, y, false)
				if len(contour) > 0 {
					fillContourInterior(labels, contour, w, h, mask)
					contours = append(contours, contour)
					nextLabel++
				}
			}
		}
	}

	hier := make([]Hierarchy, len(contours))
	for i := range hier {
		hier[i] = Hierarchy{Next: -1, Prev: -1, Child: -1, Parent: -1}
	}
	if mode == RetrievalTree || mode == RetrievalCCOMP {
		buildHierarchy(contours, hier)
	}

	if mode == RetrievalExternal {
		var extContours []Contour
		var extHier []Hierarchy
		for i := range contours {
			if hier[i].Parent == -1 {
				extContours = append(extContours, contours[i])
				extHier = append(extHier, hier[i])
			}
		}
		contours, hier = extContours, extHier
	}

	// Approximate each contour with the chosen method. We replace in place so
	// caller indices line up with hier entries.
	for i := range contours {
		contours[i] = ApproximateContour(contours[i], method)
	}

	return contours, hier
}

// buildHierarchy computes the OpenCV-style parent/child/sibling links between
// contours. It uses bounding boxes as a cheap prefilter before the more
// expensive point-in-polygon test, matching contours.d:214-269.
func buildHierarchy(contours []Contour, hier []Hierarchy) {
	n := len(contours)
	if n == 0 {
		return
	}

	type bbox struct{ minX, minY, maxX, maxY int }
	boxes := make([]bbox, n)
	for i, c := range contours {
		mnx, mny, mxx, mxy := boundingBox(c)
		boxes[i] = bbox{mnx, mny, mxx, mxy}
	}

	// Compute parent for each contour.
	for i := 0; i < n; i++ {
		rep := contours[i][0]
		parentIdx := -1
		_ = boxes[i] // boxes[i] kept for symmetry with future tests; only boxes[j] is needed below.
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			bbJ := boxes[j]
			if rep.X < bbJ.minX || rep.X > bbJ.maxX || rep.Y < bbJ.minY || rep.Y > bbJ.maxY {
				continue
			}
			if !pointInPolygon(rep, contours[j]) {
				continue
			}
			// Choose the smallest enclosing contour as the parent.
			if parentIdx == -1 || len(contours[j]) < len(contours[parentIdx]) {
				parentIdx = j
			}
		}
		hier[i].Parent = parentIdx
		if parentIdx != -1 {
			if hier[parentIdx].Child == -1 {
				hier[parentIdx].Child = i
			} else {
				sib := hier[parentIdx].Child
				for hier[sib].Next != -1 {
					sib = hier[sib].Next
				}
				hier[sib].Next = i
				hier[i].Prev = sib
			}
		}
	}
}

// SortContoursCCW sorts the points of a contour in counter-clockwise order
// using the signed area heuristic. Useful in tests to compare against
// reference polygons. Not used by the pipeline itself.
func SortContoursCCW(c Contour) Contour {
	out := make(Contour, len(c))
	copy(out, c)
	if len(out) < 3 {
		return out
	}
	cx, cy := 0.0, 0.0
	for _, p := range out {
		cx += float64(p.X)
		cy += float64(p.Y)
	}
	cx /= float64(len(out))
	cy /= float64(len(out))
	sort.SliceStable(out, func(i, j int) bool {
		ai := math.Atan2(float64(out[i].Y)-cy, float64(out[i].X)-cx)
		aj := math.Atan2(float64(out[j].Y)-cy, float64(out[j].X)-cx)
		return ai < aj
	})
	return out
}
