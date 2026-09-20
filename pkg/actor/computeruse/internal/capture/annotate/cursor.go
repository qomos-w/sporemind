package annotate

import (
	"image"
	"image/color"
)

// CursorDrawer overlays a highlight at the cursor position and an optional
// trail of recent positions.
type CursorDrawer struct {
	Color       color.Color
	Radius      int
	TrailColor  color.Color
	TrailRadius int
}

// NewCursorDrawer returns a drawer with default green highlight.
func NewCursorDrawer() *CursorDrawer {
	return &CursorDrawer{
		Color:       color.RGBA{R: 0, G: 255, B: 0, A: 200},
		Radius:      8,
		TrailColor:  color.RGBA{R: 0, G: 255, B: 0, A: 120},
		TrailRadius: 3,
	}
}

// DrawCursor highlights one cursor position.
func (d *CursorDrawer) DrawCursor(img *image.RGBA, pt image.Point) {
	DrawCircle(img, pt, d.Radius, d.Color)
	// Empty crosshair in the middle so the click target stays visible.
	DrawCircle(img, pt, 2, color.RGBA{R: 0, G: 0, B: 0, A: 255})
}

// DrawTrail draws a series of fading dots along the recent cursor path.
func (d *CursorDrawer) DrawTrail(img *image.RGBA, pts []image.Point) {
	n := len(pts)
	if n == 0 {
		return
	}
	for i, p := range pts {
		alpha := uint8(60 + (180 * i / max1(n)))
		col := color.RGBA{R: 0, G: 255, B: 0, A: alpha}
		DrawCircle(img, p, d.TrailRadius, col)
		if i > 0 {
			DrawLine(img, pts[i-1], p, col)
		}
	}
}

func max1(x int) int {
	if x < 1 {
		return 1
	}
	return x
}
