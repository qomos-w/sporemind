// Package annotate overlays grids, cursors, and bounding-box marks on
// captured frames to make screen state more legible to LLMs.
package annotate

import (
	"image"
	"image/color"
)

// DrawRect draws an outlined rectangle on the image with the given border width.
func DrawRect(img *image.RGBA, r image.Rectangle, c color.Color, borderWidth int) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	if borderWidth < 1 {
		borderWidth = 1
	}
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	// Top + bottom horizontal lines.
	for i := 0; i < borderWidth; i++ {
		top := r.Min.Y + i
		bottom := r.Max.Y - 1 - i
		for x := r.Min.X; x < r.Max.X; x++ {
			if top < r.Max.Y {
				blendPixel(img, x, top, rgba)
			}
			if bottom >= r.Min.Y {
				blendPixel(img, x, bottom, rgba)
			}
		}
	}
	// Left + right vertical lines.
	for i := 0; i < borderWidth; i++ {
		left := r.Min.X + i
		right := r.Max.X - 1 - i
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if left < r.Max.X {
				blendPixel(img, left, y, rgba)
			}
			if right >= r.Min.X {
				blendPixel(img, right, y, rgba)
			}
		}
	}
}

// FillRect fills a rectangle with the given color.
func FillRect(img *image.RGBA, r image.Rectangle, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			blendPixel(img, x, y, rgba)
		}
	}
}

// DrawCircle draws a filled circle of given radius at center.
func DrawCircle(img *image.RGBA, center image.Point, radius int, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	if radius <= 0 {
		return
	}
	bounds := img.Bounds()
	r2 := radius * radius
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy > r2 {
				continue
			}
			x := center.X + dx
			y := center.Y + dy
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}
			blendPixel(img, x, y, rgba)
		}
	}
}

// DrawLine draws a 1px Bresenham line.
func DrawLine(img *image.RGBA, a, b image.Point, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	x0, y0 := a.X, a.Y
	x1, y1 := b.X, b.Y
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := 1
	if x0 >= x1 {
		sx = -1
	}
	sy := 1
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy
	bounds := img.Bounds()
	for {
		if x0 >= bounds.Min.X && x0 < bounds.Max.X && y0 >= bounds.Min.Y && y0 < bounds.Max.Y {
			blendPixel(img, x0, y0, rgba)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// blendPixel writes c onto the existing pixel at (x, y) using source-over
// alpha blending, so semi-transparent annotations overlay (rather than erase)
// the underlying screen content. For opaque colors (A=255) it is equivalent
// to a plain Set.
func blendPixel(img *image.RGBA, x, y int, c color.RGBA) {
	a := int(c.A)
	if a == 0 {
		return
	}
	if a == 255 {
		img.SetRGBA(x, y, c)
		return
	}
	dst := img.RGBAAt(x, y)
	outR := (int(c.R)*a + int(dst.R)*(255-a)) / 255
	outG := (int(c.G)*a + int(dst.G)*(255-a)) / 255
	outB := (int(c.B)*a + int(dst.B)*(255-a)) / 255
	img.SetRGBA(x, y, color.RGBA{R: uint8(outR), G: uint8(outG), B: uint8(outB), A: 255})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
