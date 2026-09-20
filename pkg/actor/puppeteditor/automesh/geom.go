package automesh

import "math"

// Point is an integer pixel coordinate, used internally by findContours and
// the approximation passes. Conversion to/from PointF happens at the pipeline
// boundary.
type Point struct {
	X, Y int
}

// PointF is a 2D float point in image coordinates (X right, Y down). It is the
// in-package representation of contours after findContours returns them as
// integer pixels, so all subsequent pipeline stages work in the same space.
type PointF struct {
	X, Y float64
}

// Add returns p + q.
func (p PointF) Add(q PointF) PointF { return PointF{p.X + q.X, p.Y + q.Y} }

// Sub returns p - q.
func (p PointF) Sub(q PointF) PointF { return PointF{p.X - q.X, p.Y - q.Y} }

// Scale returns p * s.
func (p PointF) Scale(s float64) PointF { return PointF{p.X * s, p.Y * s} }

// Len returns the Euclidean length of the vector p.
func (p PointF) Len() float64 { return math.Hypot(p.X, p.Y) }

// LenSquared returns the squared Euclidean length of p. It is the cheap
// comparison primitive used by resampling to avoid sqrt in hot loops.
func (p PointF) LenSquared() float64 { return p.X*p.X + p.Y*p.Y }

// Dist returns the Euclidean distance between p and q.
func (p PointF) Dist(q PointF) float64 { return math.Hypot(p.X-q.X, p.Y-q.Y) }

// Equal reports whether two points are within eps in both axes. Useful in
// tests and during point dedup.
func (p PointF) Equal(q PointF, eps float64) bool {
	return math.Abs(p.X-q.X) <= eps && math.Abs(p.Y-q.Y) <= eps
}

// sign returns the sign of x, matching D's sign(0) → 0 semantics used by
// contours.d mirror-axis filtering.
func sign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}
