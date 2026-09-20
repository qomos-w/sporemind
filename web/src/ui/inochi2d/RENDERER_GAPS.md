# WebGL DrawList Viewer — Renderer Gaps

This document records the gap between the spike viewer and a full Inochi2D
blend/mask renderer. The spike validates that inochi2d-go's DrawList output
(vertices, indices, textures, basic draw commands) can be consumed by a
WebGL frontend on a transparent canvas. The gaps below must be addressed
before the viewer can render production-grade puppets.

All file:line references point to the inochi2d-go source tree at
`the upstream inochi2d-go port` (the upstream pure-Go port, v0.9.0).

## What the spike implements

- **Vertex buffer**: interleaved `vec2 pos + vec2 uv` (16 bytes/vertex),
  mirroring `core.VtxData` (`core/mesh.go:26-29` — `Vtx numath.Vec2; UV numath.Vec2`).
  Uploaded as a single static VBO.
- **Index buffer**: `uint32` indices (`core/render/drawlist.go:116` — `idxs []uint32`),
  packed via `packIndexBuffer`. Prefers `OES_element_index_uint` (UNSIGNED_INT);
  when absent, re-packs to Uint16Array (UNSIGNED_SHORT) if all indices ≤ 65535,
  otherwise throws.
- **Draw commands**: `DrawStateNormal` geometry commands (`core/render/drawlist.go:27`)
  are drawn. Mask structural commands (`DefineMask`, `PushMask`, `PopMask`)
  are fully executed via FBO-based mask buffers (see Gap 2). Composite
  structural commands are classified but skipped (see Gap 3).
- **Textures**: data-URL textures loaded via `Image` → `texImage2D`, with
  CLAMP_TO_EDGE + LINEAR filtering. This stands in for the Go-side
  `render.Texture` (`core/render/texture.go:270-275` — `Resource.ID ResourceID;
  Data TextureData`) and `render.TextureCache.Find()`
  (`core/render/texture.go:402`).
- **Uniforms**: PartVars decoded — `tint.rgb`, `opacity` applied; flat-color
  fallback when no texture is bound. PartVars layout mirrors
  `packVarsLE` (`draw.go:25-31`) at offsets defined in `part.go:225-234`.
- **Blend modes**: All 19 Inochi2D blend modes are implemented. Separable
  and min-max/additive modes (Normal, Darken, Lighten, LinearDodge, AddGlow,
  Subtract, DestinationIn) use fixed-function `gl.blendFunc` /
  `gl.blendEquation` (WebGL2 native, or WebGL1 + `EXT_blend_minmax`). The
  remaining 12 non-separable / mask-clipping modes (Multiply, Screen, Overlay,
  ColorDodge, ColorBurn, HardLight, SoftLight, Difference, Exclusion, Inverse,
  SourceIn, SourceOut) use FBO render-to-texture + per-mode blend shaders.
  Source: `render.BlendMode` (`core/render/state.go:31-72`); pure math in
  `blendMode.ts`; renderer dispatch in `webglRenderer.ts`.
- **Projection**: orthographic matrix from model-space bounds → clip space,
  with optional Y-flip.
- **Canvas**: transparent (`alpha: true`, `premultipliedAlpha: true`).

## Gap 1: Blend Modes — IMPLEMENTED ✓

**Status:** All 19 Inochi2D blend modes are now implemented across two
rendering paths in `web/src/ui/inochi2d/blendMode.ts` and
`web/src/ui/inochi2d/webglRenderer.ts`.

**Implementation paths:**

| Path | Modes | Mechanism |
|------|-------|-----------|
| **Native** (fixed-function `gl.blendFunc` + `gl.blendEquation`) | Normal, Darken, Lighten, LinearDodge, AddGlow, Subtract, DestinationIn | `getNativeBlendParams` returns equation + factor pair; renderer drives the fixed-function blender. Requires WebGL2 (built-in MIN/MAX/FUNC_REVERSE_SUBTRACT) or WebGL1 + `EXT_blend_minmax` for Darken/Lighten. |
| **Shader** (FBO render-to-texture + per-mode blend shader) | Multiply, Screen, Overlay, ColorDodge, ColorBurn, HardLight, SoftLight, Difference, Exclusion, Inverse, SourceIn, SourceOut | `blendGlslBody` emits a GLSL fragment that samples `u_src` (FBO color attachment) and `u_dst` (copy of the canvas framebuffer via `copyTexSubImage2D`) and applies the mode's premultiplied-space formula. The composite is alpha-gated by `s.a` so the destination is preserved wherever the source geometry does not cover. |

**Per-mode coverage (math assertions in `blendMode.test.ts`, dispatch
assertions in `webglRenderer.test.ts`):**

| Mode | Hex | state.go line | Category | Path | Golden assertion |
|------|-----|---------------|----------|------|------------------|
| Multiply | 0x01 | :35 | Non-separable | Shader | red×green=black |
| Screen | 0x02 | :37 | Non-separable | Shader | red screen green=yellow |
| Overlay | 0x03 | :39 | Non-separable | Shader | gray-50 midpoint identity |
| Darken | 0x04 | :41 | Min/Max | Native (MIN) | per-channel min |
| Lighten | 0x05 | :43 | Min/Max | Native (MAX) | per-channel max = yellow |
| ColorDodge | 0x06 | :45 | Dodge/Burn | Shader | opaque white = white |
| LinearDodge | 0x07 | :47 | Additive | Native (FUNC_ADD, ONE/ONE) | 0.6+0.5=1 (clamped) |
| AddGlow | 0x08 | :49 | Additive | Native (FUNC_ADD, ONE/ONE) | identical to LinearDodge |
| ColorBurn | 0x09 | :51 | Dodge/Burn | Shader | opaque white preserves dst |
| HardLight | 0x0a | :53 | Non-separable | Shader | gray-50 midpoint identity |
| SoftLight | 0x0b | :55 | Non-separable | Shader (Pegtop) | Pegtop(0.5, d) = d |
| Difference | 0x0c | :57 | Subtractive | Shader | \|red-green\|=yellow |
| Exclusion | 0x0d | :59 | Subtractive | Shader | red excl green = (1,1,0,0) |
| Subtract | 0x0e | :61 | Subtractive | Native (FUNC_REVERSE_SUBTRACT) | 0.5-0.6=0 (clamped) |
| Inverse | 0x0f | :63 | Invert | Shader | opaque red → cyan over green |
| DestinationIn | 0x10 | :65 | Clip | Native (ZERO/SRC_ALPHA) | source gated by src alpha |
| SourceIn (ClipToLower) | 0x11 | :68 | Clip | Shader | source masked by dst.a |
| SourceOut (SliceFromLower) | 0x12 | :71 | Slice | Shader | source masked by (1 - dst.a) |
| Normal | 0x00 | :33 | Source-over | Native (ONE/ONE_MINUS_SRC_ALPHA) | source-over compositing |

**Notes on SourceIn / SourceOut:** Inochi2D's "ClipToLower" / "SliceFromLower"
semantically require the lower (previously rendered) area as a mask buffer.
Without the full mask-buffer infrastructure (still tracked as Gap 2), the
shader implementation uses the current framebuffer content as the "lower"
mask — i.e. `SourceIn = src * dst.a` and `SourceOut = src * (1 - dst.a)`.
This is the correct behavior when the lower rendering has already been
composited into the canvas, which is the case for the simple DrawList stream
the spike consumes. A full mask-stack implementation (Gap 2) would refine
this when nested masks are in play.

**Source reference:** The full 19-value `BlendMode` enum is at
`core/render/state.go:31-72`. String conversion is `BlendModeName()`
(`core/render/state.go:127-170`); name→enum is `ToBlendMode()`
(`core/render/state.go:76-117`). The `DrawCmd.BlendMode` field is set via
`DrawList.SetBlending()` (`core/render/drawlist.go:245-247`) and consumed in
`part.go:238` (`drawList.SetBlending(p.BlendingMode)`) and `composite.go:138`.

## Gap 2: Mask Operations — IMPLEMENTED ✓

**Status:** `DefineMask`, `PushMask`, `PopMask` commands are now fully
executed with FBO-based mask buffers, supporting both Mask and Dodge modes
and arbitrary nesting depth.

**Source reference:** The three mask `DrawState` values are
`DrawStateDefineMask=1` (`core/render/drawlist.go:30`),
`DrawStatePushMask=2` (`core/render/drawlist.go:33`),
`DrawStatePopMask=3` (`core/render/drawlist.go:36`). They are emitted by
`DrawList.SetMasking()` (`core/render/drawlist.go:267-270`),
`DrawList.PushMask()` (`core/render/drawlist.go:225-229`), and
`DrawList.PopMask()` (`core/render/drawlist.go:232-235`). The `MaskingMode`
enum (`MaskMask=0`, `MaskDodge=1`) is at `core/render/state.go:174-181`.
The mask drawing path in a Part node is `PartNode.OnDrawMask()`
(`part.go:243-248`) which calls `SetMasking(mode)` and `Next()`.

**Command sequence (from `mask.go:114-143` MaskNode.OnDraw):**

| Step | Code path | DrawState emitted |
|------|-----------|-------------------|
| 1 | `MaskNode.OnDraw` → `PushMask(mode)` | `DrawStatePushMask` |
| 2 | Source `PartNode.OnDrawMask` → `SetMasking` + `Next` | `DrawStateDefineMask` |
| 3 | Child `PartNode.OnDraw` → `Next` | `DrawStateNormal` |
| 4 | `MaskNode.OnDraw` → `PopMask()` | `DrawStatePopMask` |

**Implementation:**

The renderer maintains a stack of `MaskLevel` entries, each with two
canvas-sized FBOs:

- **Mask buffer FBO** — accumulates `DefineMask` geometry (its alpha
  channel defines the mask shape).
- **Content buffer FBO** — accumulates `Normal` geometry within the masked
  region.

At `PushMask`: both FBOs are allocated and cleared to transparent; a new
level is pushed onto the mask stack with the command's `MaskingMode`.

At `DefineMask`: the command's geometry is rendered into the mask buffer
using source-over blend (premultiplied alpha accumulation).

At `Normal` geometry (when a mask is active): geometry is redirected to
the content buffer FBO instead of the main canvas. The shader-blend path
is target-aware — it copies from and composites back to the content FBO.

At `PopMask`: a mask-composite fullscreen shader pass multiplies the
content buffer by the mask buffer's alpha (Mask mode) or its inverse
(Dodge mode), then source-over composites the result onto the parent
render target (the main canvas, or the parent mask's content FBO for
nested masks). The popped level's GPU resources are then freed.

The mask-composite shader (`MASK_COMPOSITE_FRAG_SHADER` in `mask.ts`)
samples three textures: `u_content`, `u_mask`, and `u_dst` (a copy of the
parent target), plus a `u_dodge` uniform. It reuses the same
`VERT_SHADER_FULLSCREEN` / `drawFullscreenQuad` infrastructure as the
blend path.

**FBO infrastructure reuse:** The mask path reuses the blend path's
`ensureBlendTargets` for the `dstTexture` copy and the same
fullscreen-quad VBO and vertex shader. Mask-specific FBOs are allocated
per-level via `ensureMaskTargets` and freed at `PopMask` time.

**Nested mask support:** When `PushMask` is encountered while a mask is
already active, the inner content FBO serves as the render target for
inner geometry. At inner `PopMask`, the composite result is written to
the outer content FBO (not the canvas), so the outer mask applies to the
combined content. At outer `PopMask`, the final composite lands on the
canvas. The `restoreCanvasTarget` method correctly redirects to the
parent content FBO or the canvas depending on stack depth.

**Pixel-level unit tests** (`mask.test.ts`, 33 tests):
- `applyMaskPixel`: Mask mode (opaque/transparent/50% mask alpha) and
  Dodge mode (inverted), including premultiplied content.
- `maskCompositePixel`: source-over compositing of masked content onto
  destination.
- `maskPipelinePixel`: full non-nested pipeline (mask + composite)
  across Mask/Dodge modes, various mask/content/dst combinations.
- `nestedMaskPipelinePixel`: 7 nested scenarios including both-masks-
  opaque, inner-mask-transparent, outer-mask-transparent, 50%×50%
  quartering, inner-Mask+outer-Dodge, inner-Dodge+outer-Mask, and
  content-accumulation-then-mask.
- `MaskStackState` machine: push/pop/define/depth/active/mode tracking,
  LIFO nesting, define-affects-top-only.

**Files changed:** `mask.ts` (new), `mask.test.ts` (new),
`webglRenderer.ts` (mask handling in `renderCommand`, mask methods,
target-aware shader-blend path, dispose cleanup).


## Gap 3: Composite Operations (not implemented)

**Status:** `CompositeBegin`, `CompositeEnd`, `CompositeBlit` commands are
classified and counted but not executed.

**Source reference:** The three composite `DrawState` values are
`DrawStateCompositeBegin=4` (`core/render/drawlist.go:39`),
`DrawStateCompositeEnd=5` (`core/render/drawlist.go:42`),
`DrawStateCompositeBlit=6` (`core/render/drawlist.go:45`). They are emitted by
`DrawList.BeginComposite()` (`core/render/drawlist.go:204-207`),
`DrawList.EndComposite()` (`core/render/drawlist.go:211-214`), and
`DrawList.Blit()` (`core/render/drawlist.go:218-221`). The composite node
driving this is `CompositeNode.OnDraw()` (`composite.go:104-140`), which calls
`BeginComposite()` → draws children → `EndComposite()` → `SetVariables` →
`SetMesh` → `SetBlending` → `Blit()`.

CompositeVars layout (different from PartVars): `vec3 tint, vec3 screenTint,
void[4] padding, float opacity` — packed at `composite.go:115-123`. The blit
uses a shared full-screen NDC quad: `compositeScreenSpaceVtx`
(`draw.go:47-52`, 4 vertices at NDC ±1) and `compositeScreenSpaceIdx`
(`draw.go:56`, indices `[0,1,2,2,1,3]`).

1. `CompositeBegin` — redirect rendering to a composite FBO.
2. (Children render into the composite FBO.)
3. `CompositeEnd` — finalize the composite texture.
4. `CompositeBlit` — draw the composite texture back to the parent target
   using the composite's blend mode and `CompositeVars` (tint, screenTint,
   opacity).

**Implementation requirements:**
- A pool of FBOs for nested composites (composite nodes can nest; see
  `DrawList.targetsStack` at `drawlist.go:123`).
- CompositeVars uniform decoding (different layout from PartVars — no
  `emissionStrength` field; see `composite.go:115`).
- A fullscreen blit quad using the Go `compositeScreenSpaceVtx`/`Idx`
  geometry at `draw.go:47-56`.
- Proper render-target switching between the composite FBO and the
  main canvas.

## Gap 4: Multiple Texture Attachments

**Status:** Only `sourceIds[0]` is consumed; additional attachment slots
are ignored.

**Source reference:** `MaxAttachments = 8` (`core/render/drawlist.go:19`).
Each `DrawCmd` carries `Sources [MaxAttachments]*Texture`
(`core/render/drawlist.go:69`). Part nodes populate `Textures [MaxAttachments]*render.Texture`
(`part.go:37`), indexed by `TextureUsage`, resolved from the puppet's
`TextureCache` during deserialization (`part.go:282-300`). Textures are set
via `DrawList.SetSources()` (`drawlist.go:239-241`).

A full implementation needs:
- Multi-texture sampling in the fragment shader.
- Per-attachment UV coordinate handling (same UV per vertex, different samplers).
- Texture usage semantics (base color, emission, etc.) — currently the Go side
  uses opaque slots without explicit usage tags.

## Gap 5: Node Transforms / Mesh Deformation

**Status:** Vertices are used as-is; no per-node transform matrix or
deformation is applied.

**Source reference:** The DrawList receives already-deformed vertices from
`DeformedMesh` (`core/mesh.go:151-155`). A Part allocates its deformed mesh
into the DrawList via `DrawList.Allocate(p.deformed.Vertices(), p.deformed.Indices())`
(`part.go:206`), called in `OnPostUpdate`. `DeformedMesh.Vertices()` returns
the deformed positions (`core/mesh.go:193`); `DeformedMesh.Indices()` returns
the parent mesh's indices (`core/mesh.go:197`). The deformation delta buffer
is `DeformedMesh.delta` (`core/mesh.go:154`).

The spike's `bufferData(..., STATIC_DRAW)` is correct for a single static pose.
For animated puppets where deformation changes every frame:
- Switch to `DYNAMIC_DRAW` and re-upload the VBO each frame.
- The deformed vertices are recomputed by the Go-side physics/bone system
  before each `OnPostUpdate` call.

## Gap 6: Emission and Screen Tint

**Status:** `emissionStrength` and `screenTint` from PartVars are decoded
but not applied in the shader.

**Source reference:** PartVars layout in `part.go:225-234`:
- `tint` (vec3) at offset 0 — **applied** ✓
- `screenTint` (vec3) at offset 3 — decoded, **not applied**
- `opacity` (float) at offset 7 — **applied** ✓
- `emissionStrength` (float) at offset 8 — decoded, **not applied**

The Part node also carries `EmissionStrength` (`part.go:46`) which is
multiplied by `PropEmissionStrength` (`part.go:223`, `part.go:262`) before
packing into PartVars.

The fragment shader only applies `tint` and `opacity`. A complete shader
should:
- Add emissive light from `emissionStrength * emissionTexture` (requires
  binding the emission attachment slot from `sourceIds`).
- Apply screen-space tint (`screenTint`) as a post-multiply additive term.

## Gap 7: Texture Format and Compression

**Status:** Textures are loaded as data URLs (PNG/JPEG decoded by `Image`).

**Source reference:** The Go side defines `TextureFormat` at
`core/render/texture.go:26-35` with values `TextureFormatRGBA8Unorm` (1) and
`TextureFormatR8` (2). `TextureData` (`core/render/texture.go:57-62`) holds
raw pixels with `Width`, `Height`, `Format`, `Data`. `LoadTextureData()`
(`core/render/texture.go:74-102`) decodes via Go `image.Decode` and converts
to straight-alpha RGBA8 (matching upstream `LOAD_NO_PREMUL`).
`TextureData.Premultiply()` (`core/render/texture.go:106-120`) does in-place
premultiplication.

The spike uses data URLs for convenience, but the Go types support raw RGBA8
and R8 (mask) byte arrays. A production pipeline needs:
- Raw RGBA pixel data transport (more efficient than base64 PNG).
- R8 single-channel format support for mask textures
  (`TextureFormatR8`, `texture.go:34`).
- Compressed texture formats (ASTC/ETC for mobile, S3TC for desktop).
- Mipmap generation for minification.
- Texture atlas / array support.

## Gap 8: Coordinate System Conventions

**Status:** The viewer accepts bounds `[minX, minY, maxX, maxY]` and builds
an orthographic projection. The `flipY` flag is exposed but defaults to
`false` (OpenGL Y-up).

**Source reference:** `VtxData.Vtx` is a `numath.Vec2` (`core/mesh.go:27`)
containing model-space 2D coordinates. The composite blit quad
`compositeScreenSpaceVtx` (`draw.go:47-52`) uses NDC coordinates directly
(±1.0), bypassing the projection — this means the full renderer has two
coordinate paths: model-space for Parts, NDC for composite blits.

The model-space origin and Y direction depend on how the inp file and mesh
data are authored. This needs integration testing with real inochi2d-go
puppet output to determine:
- Whether the model-space Y axis points up or down.
- The correct projection bounds (puppet canvas vs. individual mesh bounds).
- Whether the NDC blit quad should be used as-is (no projection needed) or
  projected.

## Summary Priority

| Gap | Impact | Effort | Key source references |
|-----|--------|--------|-----------------------|
| 1. Blend modes ✓ | High — visual fidelity | Medium — shader-based | `state.go:31-72`, `drawlist.go:245`; `blendMode.ts`, `webglRenderer.ts` |
| 2. Masks ✓ | High — puppet structure | High — FBO + stack | `drawlist.go:30,33,36,225-235,267-270`; `state.go:174-181`; `part.go:243-248`; `mask.ts`, `webglRenderer.ts` |
| 3. Composites | High — node grouping | High — FBO pool | `drawlist.go:39,42,45,204-221`; `composite.go:104-140`; `draw.go:47-56` |
| 4. Multi-texture | Medium — effects | Low — shader change | `drawlist.go:19,69`; `part.go:37`; `drawlist.go:239` |
| 5. Deformation | Medium — animation | Low — DYNAMIC_DRAW | `mesh.go:151-155,193,197`; `part.go:206` |
| 6. Emission/tint | Low — cosmetic | Low — shader change | `part.go:225-234`; `part.go:46` |
| 7. Texture format | Medium — performance | Medium — transport | `texture.go:26-35,57-62,74-102` |
| 8. Coordinates | High — correctness | Low — verify convention | `mesh.go:27`; `draw.go:47-52` |
