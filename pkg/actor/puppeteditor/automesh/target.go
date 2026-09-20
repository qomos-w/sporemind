package automesh

// TargetLocal describes how the image-centered mesh should be transformed into
// the deformable node's local coordinate space. It mirrors the two branches of
// common.d mapImageCenteredMeshToTargetLocal (line 95):
//
//   - FromProvider == true (provider-based alpha):
//       world = vertex + ProviderWorldCenter
//       local = InvMatrix · (world, 0, 1)
//   - FromProvider == false (texture-based alpha, target is Projectable):
//       local = vertex + TextureOffset
//
// InvMatrix is stored as a 2×3 affine matrix in row-major order:
//
//	[a b tx]
//	[c d ty]
//
// i.e. it applies (x', y') = (a·x + b·y + tx, c·x + d·y + ty). This is the
// Go-side equivalent of the affine part of nijigenerate's mat4 inverse, and
// is sufficient because the pipeline operates in 2D.
//
// A zero-valued TargetLocal is the identity: it shifts vertices by their
// own coordinates' values, i.e. effectively a no-op for image-centered
// meshes that already live in the node's local frame.
type TargetLocal struct {
	FromProvider   bool
	InvMatrix      [6]float64 // {a, b, tx, c, d, ty}
	TextureOffset  [2]float64
	WorldCenter    [2]float64 // ProviderWorldCenter
}

// IdentityTarget returns a TargetLocal that performs no transformation.
// Useful for tests and for callers that only want raw mesh data.
func IdentityTarget() TargetLocal {
	return TargetLocal{InvMatrix: [6]float64{1, 0, 0, 0, 1, 0}}
}

// AffineTarget builds a TargetLocal from an explicit affine matrix and a
// provider world center. Used when the caller has already computed
// (target.transform.matrix)^{-1} on the D side and wants to feed it in.
func AffineTarget(a, b, tx, c, d, ty float64, worldCenter [2]float64) TargetLocal {
	return TargetLocal{
		FromProvider: true,
		InvMatrix:    [6]float64{a, b, tx, c, d, ty},
		WorldCenter:  worldCenter,
	}
}

// TextureTarget builds a TargetLocal for a Projectable whose mesh was built
// directly from its own texture (no provider). Only the texture offset is
// applied; the inverse matrix is left as identity.
func TextureTarget(offset [2]float64) TargetLocal {
	return TargetLocal{
		InvMatrix:     [6]float64{1, 0, 0, 0, 1, 0},
		TextureOffset: offset,
	}
}

// MapToNodeLocal applies TargetLocal to the given image-centered vertex set,
// producing the node-local coordinates consumed by the rest of the AutoMesh
// pipeline. The input is not mutated; the returned slice is fresh.
//
// For a FromProvider target, the transform is world := v + WorldCenter,
// then local := InvMatrix · (world, 1). For a texture-based Projectable, the
// only adjustment is local := v + TextureOffset. The identity target
// returns the input unchanged.
func MapToNodeLocal(verts []PointF, t TargetLocal) []PointF {
	if len(verts) == 0 {
		return nil
	}
	out := make([]PointF, len(verts))
	if t.FromProvider {
		// Apply (v + worldCenter) through the affine inverse.
		a, b, tx := t.InvMatrix[0], t.InvMatrix[1], t.InvMatrix[2]
		c, d, ty := t.InvMatrix[3], t.InvMatrix[4], t.InvMatrix[5]
		for i, v := range verts {
			wx := v.X + t.WorldCenter[0]
			wy := v.Y + t.WorldCenter[1]
			out[i] = PointF{a*wx + b*wy + tx, c*wx + d*wy + ty}
		}
		return out
	}
	// Texture path: just shift.
	ox, oy := t.TextureOffset[0], t.TextureOffset[1]
	for i, v := range verts {
		out[i] = PointF{v.X + ox, v.Y + oy}
	}
	return out
}
