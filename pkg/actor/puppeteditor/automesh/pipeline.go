package automesh

// PipelineParams bundles the inputs of the AutoMesh pipeline. The zero value
// is "everything default" and is safe to use in tests.
type PipelineParams struct {
	// Alpha is the input alpha buffer. PipelineParams.AlphaThreshold may be
	// left zero in which case the raw alpha is used (no binarization); pass
	// a non-zero threshold to mirror contours.d:108-111.
	Alpha          AlphaInput
	AlphaThreshold int
	// Retrieval selects the contour retrieval mode (default External).
	Retrieval RetrievalMode
	// Approx selects the contour approximation method (default Simple).
	Approx ApproxMethod
	// Sample describes the resampling/scaling/dedup configuration (see
	// ContourParams). If SamplingStep <= 0 the sampler is skipped.
	Sample ContourParams
	// Triangulate controls whether to run Delaunay on the sampled points.
	// If false, Mesh.Tris is empty and only vertices are returned. This
	// mirrors the cast(Drawable)target check at contours.d:185 where the
	// grid path does not triangulate.
	Triangulate bool
	// Target maps the image-centered mesh into the deformable's local
	// space. IdentityTarget leaves the vertices as-is.
	Target TargetLocal
}

// Result is the output of Run: the image-centered vertex set (after target
// mapping) and the optional triangle index list.
type Result struct {
	Mesh
	// ImageVertices are the vertices BEFORE the target mapping, kept for
	// callers that want to apply a different transformation downstream
	// (e.g. animated deformables).
	ImageVertices []PointF
}

// DefaultContourParams returns the ContourParams that mirror the D-side
// "Normal parts" preset (contours.d:42-49): sampling step 50, min distance
// 16, max distance 100 (= step * 2), and the standard scale ladder.
func DefaultContourParams() ContourParams {
	return ContourParams{
		SamplingStep: 50,
		MinDistance:  16,
		MaxDistance:  100,
		Scales:       []float64{1, 1.1, 0.9, 0.7, 0.4, 0.2, 0.1, 0},
	}
}

// DetailedContourParams is the "Detailed mesh" preset (contours.d:51-58).
func DetailedContourParams() ContourParams {
	return ContourParams{
		SamplingStep: 32,
		MinDistance:  16,
		MaxDistance:  64,
		Scales:       []float64{1, 1.1, 0.9, 0.7, 0.4, 0.2, 0.1, 0},
	}
}

// ThinContourParams is the "Thin and minimum parts" preset (contours.d:78-85).
func ThinContourParams() ContourParams {
	return ContourParams{
		SamplingStep: 12,
		MinDistance:  4,
		MaxDistance:  24,
		Scales:       []float64{1},
	}
}

// Run executes the full AutoMesh pipeline:
//
//  1. (optional) binarize the alpha mask.
//  2. findContours (Suzuki-Abe) with the chosen retrieval mode.
//  3. For each contour: layered point generation (resample + scale +
//     dedupe) around the image center.
//  4. (optional) Delaunay triangulation.
//  5. Map the image-centered mesh into node-local coordinates.
//
// Every step is a pure function on the previous step's output, so the
// pipeline itself is referentially transparent.
func Run(p PipelineParams) Result {
	if !p.Alpha.Valid() {
		return Result{}
	}

	mask := p.Alpha.Alpha[:p.Alpha.W*p.Alpha.H]
	if p.AlphaThreshold > 0 {
		mask = ThresholdAlpha(p.Alpha, p.AlphaThreshold)
	}

	contours, _ := FindContours(mask, p.Alpha.W, p.Alpha.H, p.Retrieval, p.Approx)
	if len(contours) == 0 {
		return Result{}
	}

	imgCenter := p.Alpha.Center()
	imageVerts := GenerateLayeredPoints(contours, imgCenter, p.Sample)
	if len(imageVerts) == 0 {
		return Result{}
	}

	res := Result{ImageVertices: imageVerts}

	if p.Triangulate && len(imageVerts) >= 3 {
		res.Mesh = AutoTriangulate(imageVerts)
	} else {
		res.Mesh = Mesh{Vertices: imageVerts}
	}

	// Apply target mapping as the last step.
	if !isIdentityTarget(p.Target) {
		res.Mesh.Vertices = MapToNodeLocal(imageVerts, p.Target)
	}
	return res
}

// isIdentityTarget returns true when the TargetLocal describes a no-op
// transform. Avoids an extra copy of the vertex slice for the common case.
//
// In the non-provider path the inverse matrix is never applied (see
// MapToNodeLocal), so a zero-value or identity-matrix target with no texture
// offset is a no-op.
func isIdentityTarget(t TargetLocal) bool {
	if t.FromProvider {
		return false
	}
	if t.TextureOffset != [2]float64{} {
		return false
	}
	// When FromProvider is false, the InvMatrix is not consulted by
	// MapToNodeLocal (it uses the texture-offset path), so a zero-valued
	// matrix is acceptable as "no transform". When FromProvider is true
	// we already returned false above.
	return true
}
