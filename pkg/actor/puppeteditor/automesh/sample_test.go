package automesh

import (
	"math"
	"reflect"
	"testing"
)

func TestCalcMoment(t *testing.T) {
	cases := []struct {
		name string
		in   []PointF
		want PointF
	}{
		{"empty", nil, PointF{}},
		{"single", []PointF{{1, 2}}, PointF{1, 2}},
		{"triangle centroid", []PointF{{0, 0}, {6, 0}, {0, 6}}, PointF{2, 2}},
		{"square centroid", []PointF{{0, 0}, {4, 0}, {4, 4}, {0, 4}}, PointF{2, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcMoment(tc.in)
			if !got.Equal(tc.want, 1e-9) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestScaleContourAroundCenter(t *testing.T) {
	c := []PointF{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	center := PointF{5, 5}

	t.Run("identity scale is no-op", func(t *testing.T) {
		got := ScaleContourAroundCenter(c, center, 1)
		if !reflect.DeepEqual(got, c) {
			t.Fatalf("got %v, want %v", got, c)
		}
	})

	t.Run("half scale shrinks toward center", func(t *testing.T) {
		got := ScaleContourAroundCenter(c, center, 0.5)
		want := []PointF{{2.5, 2.5}, {7.5, 2.5}, {7.5, 7.5}, {2.5, 7.5}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		snapshot := append([]PointF(nil), c...)
		_ = ScaleContourAroundCenter(c, center, 0.25)
		if !reflect.DeepEqual(c, snapshot) {
			t.Fatalf("input was mutated")
		}
	})
}

func TestResampleContour(t *testing.T) {
	// A long horizontal line of 11 points.
	pts := []PointF{}
	for i := 0; i < 11; i++ {
		pts = append(pts, PointF{float64(i * 10), 0})
	}

	t.Run("single point returns it", func(t *testing.T) {
		got := ResampleContour([]PointF{{7, 9}}, 1, false, 0)
		if !reflect.DeepEqual(got, []PointF{{7, 9}}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("rate bigger than spacing returns only the base", func(t *testing.T) {
		got := ResampleContour(pts, 1e6, false, 0)
		if len(got) != 1 || got[0] != pts[0] {
			t.Fatalf("expected just the seed point, got %v", got)
		}
	})

	t.Run("rate=10 enforces spacing", func(t *testing.T) {
		got := ResampleContour(pts, 10, false, 0)
		// Every retained vertex should be at least 10 px from the previous one.
		for i := 1; i < len(got); i++ {
			if d := got[i].Sub(got[i-1]).Len(); d < 9.999 {
				t.Fatalf("consecutive vertices %d, %d too close (%.3f)", i-1, i, d)
			}
		}
	})

	t.Run("MirrorHoriz picks base closest to axis", func(t *testing.T) {
		axis := 50.0
		got := ResampleContour(pts, 1e6, true, axis)
		// Only the seed should be retained since rate is huge.
		if len(got) != 1 {
			t.Fatalf("expected 1 vertex, got %v", got)
		}
		// Find which point was chosen as base.
		baseIdx := -1
		for i, p := range pts {
			if p == got[0] {
				baseIdx = i
				break
			}
		}
		if baseIdx < 0 {
			t.Fatalf("seed point not found in input: %v", got[0])
		}
		// The D code uses signed difference (vertex.x - axis), so the
		// minimum is the most-negative value (leftmost point, X=0, diff=-50).
		if baseIdx != 0 {
			t.Fatalf("baseIdx = %d, want 0 (signed-diff semantics)", baseIdx)
		}
	})

	t.Run("MirrorHoriz skips opposite-side vertices", func(t *testing.T) {
		// Polyline crosses the axis multiple times; only the left half should
		// survive. Points go from (-50,0) → (0,0) → (50,0).
		pts := []PointF{{-50, 0}, {-40, 0}, {-30, 0}, {-20, 0}, {-10, 0}, {0, 0}, {10, 0}, {20, 0}, {30, 0}, {40, 0}, {50, 0}}
		got := ResampleContour(pts, 5, true, 0)
		for _, p := range got {
			if p.X > 0 {
				t.Fatalf("positive-X vertex survived MirrorHoriz: %+v", p)
			}
		}
	})
}

func TestResampleContour_EmptyInput(t *testing.T) {
	if got := ResampleContour(nil, 1, false, 0); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestSamplingRateFor(t *testing.T) {
	cases := []struct {
		name        string
		scale       float64
		sampling    float64
		maxDist     float64
		want        float64
	}{
		{"scale > 0 takes min of two branches", 0.5, 32, 64, 32 / (0.5 * 0.5)}, // 32/0.25 = 128 > 64/0.5 = 128 → 128
		{"scale > 0 capped by maxDist / scale", 2, 32, 16, 16.0 / 2},
		{"scale = 0 clamps to 1", 0, 32, 64, 1},
		{"scale < 0 clamps to 1", -1, 32, 64, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := samplingRateFor(tc.scale, tc.sampling, tc.maxDist)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenerateLayeredPoints_EmptyContours(t *testing.T) {
	got := GenerateLayeredPoints(nil, PointF{0, 0}, DefaultContourParams())
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestGenerateLayeredPoints_ZeroSamplingStep(t *testing.T) {
	params := ContourParams{SamplingStep: 0, Scales: []float64{1}}
	got := GenerateLayeredPoints([]Contour{{{0, 0}, {10, 0}, {10, 10}}}, PointF{0, 0}, params)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestGenerateLayeredPoints_NoScales(t *testing.T) {
	got := GenerateLayeredPoints([]Contour{{{0, 0}, {10, 0}, {10, 10}}}, PointF{0, 0}, ContourParams{SamplingStep: 5})
	if got != nil {
		t.Fatalf("expected nil without scales, got %v", got)
	}
}

func TestGenerateLayeredPoints_SingleContour(t *testing.T) {
	// Square around (50,50); imgCenter (50,50); single scale 1.0.
	contour := Contour{{20, 20}, {80, 20}, {80, 80}, {20, 80}}
	params := ContourParams{
		SamplingStep: 10,
		MinDistance:  0,
		MaxDistance:  20,
		Scales:       []float64{1.0},
	}
	pts := GenerateLayeredPoints([]Contour{contour}, PointF{50, 50}, params)
	if len(pts) < 3 {
		t.Fatalf("expected at least 3 points, got %d", len(pts))
	}
	// All points must be image-centered (shifted by -50).
	for _, p := range pts {
		if p.X < -50-1e-6 || p.X > 50+1e-6 {
			t.Fatalf("vertex X out of expected range: %+v", p)
		}
	}
}

func TestGenerateLayeredPoints_DedupesViaMinDistance(t *testing.T) {
	// A very long polyline; MinDistance=20 should limit how many points get
	// added.
	pts := make([]PointF, 50)
	for i := range pts {
		pts[i] = PointF{float64(i), 0}
	}
	contour := make(Contour, len(pts))
	for i, p := range pts {
		contour[i] = Point{X: int(p.X), Y: int(p.Y)}
	}
	params := ContourParams{
		SamplingStep: 1,
		MinDistance:  20,
		MaxDistance:  2,
		Scales:       []float64{1.0},
	}
	got := GenerateLayeredPoints([]Contour{contour}, PointF{0, 0}, params)
	if len(got) == 0 {
		t.Fatal("expected some points")
	}
	for i := 1; i < len(got); i++ {
		d := got[i].Dist(got[i-1])
		if d < params.MinDistance-1e-9 {
			t.Fatalf("consecutive points %d, %d too close: %.3f < %.3f", i-1, i, d, params.MinDistance)
		}
	}
}

func TestContourParams_ResolvedMaxDistance(t *testing.T) {
	p := ContourParams{SamplingStep: 32, MaxDistance: 0}
	if got := p.resolvedMaxDistance(); got != 64 {
		t.Fatalf("got %v, want 64", got)
	}
	p = ContourParams{SamplingStep: 32, MaxDistance: -10}
	if got := p.resolvedMaxDistance(); got != 64 {
		t.Fatalf("got %v, want 64", got)
	}
	p = ContourParams{SamplingStep: 32, MaxDistance: 50}
	if got := p.resolvedMaxDistance(); got != 50 {
		t.Fatalf("got %v, want 50", got)
	}
}

func TestPointFHelpers(t *testing.T) {
	p := PointF{3, 4}
	if p.Len() != 5 {
		t.Fatalf("Len = %v, want 5", p.Len())
	}
	if p.LenSquared() != 25 {
		t.Fatalf("LenSquared = %v, want 25", p.LenSquared())
	}
	q := PointF{6, 8}
	if p.Dist(q) != math.Sqrt(9+16) {
		t.Fatalf("Dist = %v, want 5", p.Dist(q))
	}
	if !p.Equal(q, 5) {
		t.Fatal("Equal with eps=5 should match")
	}
	if p.Equal(q, 1) {
		t.Fatal("Equal with eps=1 should not match")
	}
	{
		got := PointF{1, 2}.Add(PointF{3, 4})
		if !got.Equal(PointF{4, 6}, 0) {
			t.Fatal("Add wrong")
		}
	}
	{
		got := PointF{4, 6}.Sub(PointF{1, 2})
		if !got.Equal(PointF{3, 4}, 0) {
			t.Fatal("Sub wrong")
		}
	}
	{
		got := PointF{2, 3}.Scale(2)
		if !got.Equal(PointF{4, 6}, 0) {
			t.Fatal("Scale wrong")
		}
	}
}
