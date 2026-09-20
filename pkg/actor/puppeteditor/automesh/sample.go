package automesh

import "math"

// ContourParams captures the SAMPLING_STEP / MAX_DISTANCE / MIN_DISTANCE /
// SCALES / mirror-axis semantics from ContourAutoMeshProcessor in contours.d
// (lines 26-104). All units are pixels.
//
// Field semantics:
//
//   - SamplingStep: baseline spacing between sampled contour vertices. Higher
//     values produce sparser meshes.
//   - MaskThreshold: alpha binarization cutoff (already applied upstream).
//   - MinDistance: minimum spacing between emitted mesh vertices; previously
//     emitted points closer than this are skipped.
//   - MaxDistance: cap for the per-scale sampling rate. If <=0 it is
//     auto-resolved to SamplingStep*2 (the contours.d:104 default).
//   - Scales: list of per-pass scale factors. scale > 0 shrinks/expands the
//     contour around its centroid; scale <= 0 emits the original centroid as
//     a single point (used for the "thinnest" passes).
//   - MirrorHoriz / MirrorVert + AxisHoriz / AxisVert: when MirrorHoriz is
//     true, the resampler seeds from the contour vertex closest to AxisHoriz
//     and skips any subsequent vertex whose sign crosses the axis (so only
//     one side of the symmetry plane contributes).
type ContourParams struct {
	SamplingStep  float64
	MaskThreshold int
	MinDistance   float64
	MaxDistance   float64
	Scales        []float64
	MirrorHoriz   bool
	AxisHoriz     float64
	MirrorVert    bool
	AxisVert      float64
}

// resolvedMaxDistance returns the effective MaxDistance for sampling,
// auto-resolving the contours.d:104 default (SamplingStep*2) when <= 0.
func (p ContourParams) resolvedMaxDistance() float64 {
	if p.MaxDistance <= 0 {
		return p.SamplingStep * 2
	}
	return p.MaxDistance
}

// CalcMoment returns the arithmetic centroid of a closed polyline.
// Equivalent to contours.d:125-128 calcMoment.
func CalcMoment(c []PointF) PointF {
	if len(c) == 0 {
		return PointF{}
	}
	sum := PointF{}
	for _, p := range c {
		sum.X += p.X
		sum.Y += p.Y
	}
	n := float64(len(c))
	return PointF{sum.X / n, sum.Y / n}
}

// ScaleContourAroundCenter applies the linear map (p - center) * scale + center
// to every vertex of the polyline, mirroring contours.d scaling() at line 129.
// It returns a freshly allocated slice; the input is not mutated.
func ScaleContourAroundCenter(c []PointF, center PointF, scale float64) []PointF {
	out := make([]PointF, len(c))
	for i, p := range c {
		dx := p.X - center.X
		dy := p.Y - center.Y
		out[i] = PointF{center.X + dx*scale, center.Y + dy*scale}
	}
	return out
}

// ResampleContour returns a polyline of vertices that are at least rate pixels
// apart, starting from `base` (the vertex closest to axisHoriz when
// MirrorHoriz is true). Vertices that cross the mirror axis are skipped so
// only one side of the symmetry plane is sampled. This mirrors contours.d
// lines 132-158.
//
// rate is the minimum spacing; the comparison uses squared distance to avoid
// sqrt in the inner loop. The returned slice always contains the seed vertex.
func ResampleContour(c []PointF, rate float64, mirrorHoriz bool, axisHoriz float64) []PointF {
	n := len(c)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return []PointF{c[0]}
	}

	base := 0
	if mirrorHoriz {
		minDist := math.Inf(1)
		for i, p := range c {
			d := p.X - axisHoriz
			if d < minDist {
				minDist = d
				base = i
			}
		}
	}

	sampled := make([]PointF, 0, n)
	sampled = append(sampled, c[base])
	var side float64 // 0 = unset, otherwise ±1
	rateSq := rate * rate

	for idx := 1; idx < n; idx++ {
		prev := sampled[len(sampled)-1]
		curr := c[(idx+base)%n]
		if curr.Sub(prev).LenSquared() <= rateSq {
			continue
		}
		if mirrorHoriz {
			s := sign(curr.X - axisHoriz)
			if side == 0 {
				side = s
			} else if s != side {
				continue
			}
		}
		sampled = append(sampled, curr)
	}

	return sampled
}

// samplingRateFor computes the effective sampling rate for one scale factor,
// matching contours.d:167-168:
//
//	samplingRate = SAMPLING_STEP
//	samplingRate = min(MAX_DISTANCE / scale, scale > 0 ? SAMPLING_STEP / (scale^2) : 1)
//
// When scale <= 0 the rate is clamped to 1, producing a single centroid-only
// sample layer.
func samplingRateFor(scale, samplingStep, maxDistance float64) float64 {
	if scale <= 0 {
		return 1
	}
	capped := maxDistance / scale
	shrunk := samplingStep / (scale * scale)
	if capped < shrunk {
		return capped
	}
	return shrunk
}

// GenerateLayeredPoints runs the full per-contour layered point generation
// used by ContourAutoMeshProcessor (contours.d:160-183):
//
//  1. Convert each contour to floats and compute its centroid (CalcMoment).
//  2. For every scale in SCALES:
//     a. Compute the effective sampling rate.
//     b. Resample the contour around the mirror axis (if any).
//     c. Scale the resampled points around the centroid.
//     d. Subtract imgCenter to obtain image-centered coordinates.
//     e. Dedupe against already-emitted points using MinDistance.
//
// The returned slice is fresh and may be empty (e.g. for an empty input, a
// contour with fewer than 2 vertices, or a degenerate scale configuration).
func GenerateLayeredPoints(contours []Contour, imgCenter PointF, params ContourParams) []PointF {
	if params.SamplingStep <= 0 || len(params.Scales) == 0 {
		return nil
	}
	maxDist := params.resolvedMaxDistance()
	var out []PointF
	for _, c := range contours {
		if len(c) == 0 {
			continue
		}
		fc := make([]PointF, len(c))
		for i, p := range c {
			fc[i] = PointF{float64(p.X), float64(p.Y)}
		}
		moment := CalcMoment(fc)
		for _, scale := range params.Scales {
			rate := samplingRateFor(scale, params.SamplingStep, maxDist)
			sampled := ResampleContour(fc, rate, params.MirrorHoriz, imgCenter.X+params.AxisHoriz)
			scaled := ScaleContourAroundCenter(sampled, moment, scale)
			for _, p := range scaled {
				rel := p.Sub(imgCenter)
				if len(out) == 0 {
					out = append(out, rel)
					continue
				}
				if params.MinDistance > 0 {
					minDist := math.Inf(1)
					for _, q := range out {
						if d := rel.Dist(q); d < minDist {
							minDist = d
						}
					}
					if minDist <= params.MinDistance {
						continue
					}
				}
				out = append(out, rel)
			}
		}
	}
	return out
}
