package puppeteditor

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Viewport capture policy: the WebGL canvas lives in the frontend, so the
// backend cannot screenshot it. Until a frontend capture bridge exists,
// puppet.viewport.capture returns a document-level reprojection — a
// deterministic svg schematic of the current document — explicitly labeled as
// such. It never fabricates a raster screenshot: png/jpeg requests are
// rejected rather than silently answered with a schematic.
const (
	viewportSourceReprojection = "document_reprojection"
	viewportMimeSVG            = "image/svg+xml"

	viewportDefaultWidth  = int32(800)
	viewportDefaultHeight = int32(600)
	viewportMinDim        = int32(64)
	viewportMaxDim        = int32(4096)

	viewportRowHeight = int32(20)
	viewportHeaderY   = int32(62) // first node row baseline
)

// handleViewportCapture is the public puppet.viewport.capture callable. It is
// read-only: no document mutation, no revision. The response Uri is a data
// URL of the reprojection svg; Source identifies it as a reprojection so a
// caller can never mistake it for a real canvas capture.
func (a *Actor) handleViewportCapture(_ actor.PureContext, req gen.PuppetViewportCaptureReq) (gen.PuppetViewportCaptureResp, error) {
	if req.Format != "" && req.Format != "svg" {
		return gen.PuppetViewportCaptureResp{}, fmt.Errorf("puppet.viewport.capture: raster format %q unavailable: the canvas lives in the frontend and no fabricated screenshot is served; use format \"svg\" for the document reprojection", req.Format)
	}
	width, height := normalizeViewportSize(req.Width, req.Height)

	a.mu.RLock()
	doc := cloneDocument(a.state.Document)
	applyParamProjection(&doc) // bound values (e.g. z) take effect in the reprojection too
	svg := renderDocumentReprojection(doc, int32(len(a.state.Revisions)), width, height)
	a.mu.RUnlock()

	return gen.PuppetViewportCaptureResp{
		Uri:      "data:" + viewportMimeSVG + ";base64," + base64.StdEncoding.EncodeToString([]byte(svg)),
		Width:    width,
		Height:   height,
		MimeType: viewportMimeSVG,
		Source:   viewportSourceReprojection,
	}, nil
}

// normalizeViewportSize applies the documented defaults and clamps. Zero or
// negative dimensions fall back to the default; anything beyond the clamp
// range is capped so a caller cannot force a multi-megabyte svg.
func normalizeViewportSize(width, height int32) (int32, int32) {
	if width <= 0 {
		width = viewportDefaultWidth
	}
	if height <= 0 {
		height = viewportDefaultHeight
	}
	width = clampInt32(width, viewportMinDim, viewportMaxDim)
	height = clampInt32(height, viewportMinDim, viewportMaxDim)
	return width, height
}

func clampInt32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderDocumentReprojection renders the document as a deterministic svg
// schematic: a header identifying the projection as a reprojection (never a
// canvas screenshot) plus the node tree as an indented, pre-ordered stack
// with z-order, kind, texture/mesh markers, and disabled flags. It is pure —
// derived only from the document — so identical documents always render
// identical bytes.
func renderDocumentReprojection(doc gen.PuppetDocument, revisionCount, width, height int32) string {
	infos := make([]gen.PuppetNodeInfo, 0, 16)
	walkNodeInfos(&doc.Root, "", 0, func(info gen.PuppetNodeInfo) {
		infos = append(infos, info)
	})

	rowsFit := (height - viewportHeaderY - 10) / viewportRowHeight
	if rowsFit < 0 {
		rowsFit = 0
	}
	shown := infos
	var omitted int32
	if int32(len(infos)) > rowsFit {
		shown = infos[:rowsFit]
		omitted = int32(len(infos)) - rowsFit
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="monospace">`, width, height, width, height)
	b.WriteString(`<rect width="100%" height="100%" fill="#14181d"/>`)
	b.WriteString(`<text x="12" y="22" fill="#e8e8e8" font-size="13">document reprojection (not a canvas screenshot)</text>`)
	fmt.Fprintf(&b, `<text x="12" y="42" fill="#9aa4b0" font-size="12">%s &#8212; revision %d &#8212; %d revisions</text>`,
		xmlEscape(doc.Name), doc.Revision, revisionCount)

	y := viewportHeaderY
	for _, info := range shown {
		markers := ""
		if info.TextureAssetID != "" {
			markers += " [tex]"
		}
		if info.HasMesh {
			markers += " [mesh]"
		}
		if !info.Enabled {
			markers += " [disabled]"
		}
		x := 12 + info.Depth*14
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="16" fill="#1d232b" rx="3"/>`, x, y-12, width-x-12-16*2)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#c8d0da" font-size="12">z=%d %s [%s]%s</text>`,
			x+6, y, info.Z, xmlEscape(info.Name), xmlEscape(info.Kind), markers)
		y += viewportRowHeight
	}
	if omitted > 0 {
		fmt.Fprintf(&b, `<text x="12" y="%d" fill="#8a94a0" font-size="12">+%d more nodes</text>`, y, omitted)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// xmlEscape neutralizes the XML metacharacters that can appear in
// user-controlled node/document names.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
