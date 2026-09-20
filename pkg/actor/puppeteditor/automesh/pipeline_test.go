package automesh

import (
	"image"
	"image/color"
	"image/png"
	"bytes"
	"testing"
)

// makeMaskPNG encodes an arbitrary mask (length w*h) into a PNG with alpha =
// 255 where mask != 0, 0 elsewhere.
func makeMaskPNG(t *testing.T, w, h int, mask []byte) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, m := range mask {
		if m == 0 {
			img.SetNRGBA(i%w, i/w, color.NRGBA{})
			continue
		}
		img.SetNRGBA(i%w, i/w, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode mask PNG: %v", err)
	}
	return buf.Bytes()
}

func TestRun_EmptyAlpha(t *testing.T) {
	res := Run(PipelineParams{})
	if len(res.Vertices) != 0 || len(res.Tris) != 0 || len(res.ImageVertices) != 0 {
		t.Fatalf("expected empty result, got %+v", res)
	}
}

func TestRun_ZeroSizedAlpha(t *testing.T) {
	res := Run(PipelineParams{Alpha: AlphaInput{W: 0, H: 5, Alpha: nil}})
	if len(res.Vertices) != 0 {
		t.Fatalf("expected empty, got %+v", res)
	}
}

func TestRun_AllZeroAlpha(t *testing.T) {
	in := AlphaFromAlphaBytes(8, 8, make([]byte, 64))
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 15,
		Sample:         DefaultContourParams(),
		Triangulate:    true,
	})
	if len(res.Vertices) != 0 {
		t.Fatalf("expected no vertices on all-zero alpha, got %d", len(res.Vertices))
	}
}

func TestRun_FilledSquareWithTriangulation(t *testing.T) {
	mask := filledRectMask(40, 40, 10, 10, 30, 30)
	in := AlphaFromAlphaBytes(40, 40, mask)
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1, // ensure binarization kicks in
		Approx:         ApproxNone,
		Sample: ContourParams{
			SamplingStep: 5,
			MinDistance:  0,
			MaxDistance:  10,
			Scales:       []float64{1},
		},
		Triangulate: true,
	})
	if len(res.Vertices) < 3 {
		t.Fatalf("expected ≥3 vertices, got %d", len(res.Vertices))
	}
	if len(res.Tris) == 0 {
		t.Fatal("expected triangles")
	}
	if !trianglesAreCCW(res.Vertices, res.Tris) {
		t.Fatal("CCW invariant violated")
	}
}

func TestRun_FilledSquareWithoutTriangulation(t *testing.T) {
	mask := filledRectMask(20, 20, 5, 5, 15, 15)
	in := AlphaFromAlphaBytes(20, 20, mask)
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1,
		Sample: ContourParams{
			SamplingStep: 4,
			MinDistance:  0,
			MaxDistance:  8,
			Scales:       []float64{1},
		},
		Triangulate: false,
	})
	if len(res.Vertices) == 0 {
		t.Fatal("expected vertices")
	}
	if len(res.Tris) != 0 {
		t.Fatalf("expected no triangles, got %d", len(res.Tris))
	}
}

func TestRun_TooFewPointsSkipTriangulation(t *testing.T) {
	// A very small mask that produces ≤ 2 layered points.
	mask := filledRectMask(8, 8, 3, 3, 5, 5)
	in := AlphaFromAlphaBytes(8, 8, mask)
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1,
		Sample: ContourParams{
			SamplingStep: 5,
			MinDistance:  0,
			MaxDistance:  10,
			Scales:       []float64{1},
		},
		Triangulate: true,
	})
	// No failure expected; Mesh may be empty or have vertices without tris.
	if len(res.Tris) > 0 && len(res.Vertices) < 3 {
		t.Fatalf("got %d tris but only %d vertices", len(res.Tris), len(res.Vertices))
	}
}

func TestRun_AppliesTextureOffset(t *testing.T) {
	mask := filledRectMask(20, 20, 5, 5, 15, 15)
	in := AlphaFromAlphaBytes(20, 20, mask)
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1,
		Sample: ContourParams{
			SamplingStep: 4,
			MinDistance:  0,
			MaxDistance:  8,
			Scales:       []float64{1},
		},
		Triangulate: true,
		Target:      TextureTarget([2]float64{100, 200}),
	})
	if len(res.Vertices) == 0 {
		t.Fatal("expected vertices")
	}
	for _, v := range res.Vertices {
		// ImageVertices should be centered around (0,0); final vertices
		// should be shifted by (100,200).
		imageV := res.ImageVertices[0]
		if v.X < imageV.X+99 || v.X > imageV.X+101 {
			t.Fatalf("vertex X not shifted by 100: %+v (img %+v)", v, imageV)
		}
		break
	}
}

func TestRun_AppliesAffineTarget(t *testing.T) {
	mask := filledRectMask(20, 20, 5, 5, 15, 15)
	in := AlphaFromAlphaBytes(20, 20, mask)
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1,
		Sample: ContourParams{
			SamplingStep: 4,
			MinDistance:  0,
			MaxDistance:  8,
			Scales:       []float64{1},
		},
		Triangulate: true,
		Target:      AffineTarget(1, 0, 0, 0, 1, 0, [2]float64{100, 200}),
	})
	if len(res.Vertices) == 0 {
		t.Fatal("expected vertices")
	}
	for _, v := range res.Vertices {
		if v.X < 90 || v.X > 110 || v.Y < 190 || v.Y > 210 {
			t.Fatalf("vertex not shifted into the [90,110]×[190,210] band: %+v", v)
		}
	}
}

func TestRun_PureFunctionRepeat(t *testing.T) {
	// Two consecutive calls with the same input must produce identical
	// output. Pure-function guarantee.
	mask := filledRectMask(20, 20, 5, 5, 15, 15)
	in := AlphaFromAlphaBytes(20, 20, mask)
	params := PipelineParams{
		Alpha:          in,
		AlphaThreshold: 1,
		Sample: ContourParams{
			SamplingStep: 4,
			MinDistance:  0,
			MaxDistance:  8,
			Scales:       []float64{1, 0.5},
		},
		Triangulate: true,
	}
	r1 := Run(params)
	r2 := Run(params)
	if len(r1.Vertices) != len(r2.Vertices) {
		t.Fatalf("vertex count mismatch: %d vs %d", len(r1.Vertices), len(r2.Vertices))
	}
	for i := range r1.Vertices {
		if r1.Vertices[i] != r2.Vertices[i] {
			t.Fatalf("vertex %d differs between runs: %+v vs %+v", i, r1.Vertices[i], r2.Vertices[i])
		}
	}
	if len(r1.Tris) != len(r2.Tris) {
		t.Fatalf("triangle count mismatch: %d vs %d", len(r1.Tris), len(r2.Tris))
	}
}

func TestRun_PNGRoundtrip(t *testing.T) {
	mask := filledRectMask(30, 30, 5, 5, 25, 25)
	payload := makeMaskPNG(t, 30, 30, mask)
	in, err := DecodeAlphaPNG(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	res := Run(PipelineParams{
		Alpha:          in,
		AlphaThreshold: 128,
		Sample: ContourParams{
			SamplingStep: 5,
			MinDistance:  0,
			MaxDistance:  10,
			Scales:       []float64{1},
		},
		Triangulate: true,
	})
	if len(res.Vertices) < 3 {
		t.Fatalf("expected ≥3 vertices after PNG roundtrip, got %d", len(res.Vertices))
	}
}

func TestDefaultContourParams(t *testing.T) {
	p := DefaultContourParams()
	if p.SamplingStep != 50 || p.MaxDistance != 100 || p.MinDistance != 16 {
		t.Fatalf("default params = %+v", p)
	}
	if len(p.Scales) != 8 || p.Scales[0] != 1 || p.Scales[7] != 0 {
		t.Fatalf("default scales = %v", p.Scales)
	}
}

func TestDetailedContourParams(t *testing.T) {
	p := DetailedContourParams()
	if p.SamplingStep != 32 || p.MaxDistance != 64 {
		t.Fatalf("detailed params = %+v", p)
	}
}

func TestThinContourParams(t *testing.T) {
	p := ThinContourParams()
	if p.SamplingStep != 12 || p.MaxDistance != 24 || len(p.Scales) != 1 || p.Scales[0] != 1 {
		t.Fatalf("thin params = %+v", p)
	}
}

func TestIsIdentityTarget(t *testing.T) {
	cases := []struct {
		name string
		t    TargetLocal
		want bool
	}{
		{"zero-value", TargetLocal{}, true},
		{"explicit identity", IdentityTarget(), true},
		{"from provider", AffineTarget(1, 0, 0, 0, 1, 0, [2]float64{1, 2}), false},
		{"with offset", TextureTarget([2]float64{1, 0}), false},
		{"non-identity affine from provider", TargetLocal{FromProvider: true, InvMatrix: [6]float64{2, 0, 0, 0, 2, 0}}, false},
		{"non-identity matrix but not from provider", TargetLocal{InvMatrix: [6]float64{2, 0, 0, 0, 2, 0}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isIdentityTarget(tc.t); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
