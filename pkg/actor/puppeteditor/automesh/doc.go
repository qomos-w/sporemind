// Package automesh is a pure-Go port of the Inochi2D AutoMesh contour pipeline.
//
// It is intentionally free of actor, gospore, and global state so it can be
// unit-tested in isolation and reused from any layer (editor, headless CLI,
// desktop panel). The package mirrors the semantics of nijigenerate's
// viewport/vertex/automesh/{contours,common,alpha_provider,mesh} modules:
//
//   - alpha.go      — image/png decoding, alpha-channel extraction, binary
//                     thresholding (the alpha mask passed to findContours).
//   - contour.go    — Suzuki-Abe findContours plus the four OpenCV
//                     approximation methods (NONE / SIMPLE / TC89_L1 /
//                     TC89_KCOS).
//   - sample.go     — contour resampling, scaling, and layered point
//                     generation; semantics align with ContourAutoMeshProcessor
//                     in contours.d (SAMPLING_STEP, MAX_DISTANCE, MIN_DISTANCE,
//                     SCALES, mirror axes).
//   - triangulate.go — Bowyer-Watson Delaunay triangulation mirroring
//                     mesh.d autoTriangulate.
//   - target.go     — image-centered mesh → node-local space mapping mirroring
//                     common.d mapImageCenteredMeshToTargetLocal.
//   - pipeline.go   — Run() ties the above into the AutoMesh pipeline so a
//                     caller can obtain a vertex/triangle mesh from a single
//                     PNG byte slice or alpha buffer.
//
// The package makes no goroutine, filesystem, network, or actor calls; every
// entry point is a pure function (input slices are never mutated).
package automesh
