package annotate

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// drawText renders label at pt, the top-left corner of the first glyph.
// It uses the built-in 7x13 bitmap font so annotations need no external fonts.
// font.Drawer's Dot is a baseline, so we offset by the face ascent to make pt
// the glyph top-left rather than the baseline.
func drawText(img *image.RGBA, pt image.Point, label string, c color.Color) {
	if img == nil || label == "" {
		return
	}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(pt.X, pt.Y+basicfont.Face7x13.Metrics().Ascent.Ceil()),
	}
	d.DrawString(label)
}

// drawOutlinedText renders label with a dark outline around each glyph so the
// text stays readable over arbitrary screenshot content, then draws the fill
// color on top.
func drawOutlinedText(img *image.RGBA, pt image.Point, label string, fill, outline color.Color) {
	offsets := [8][2]int{
		{-1, -1}, {0, -1}, {1, -1},
		{-1, 0}, {1, 0},
		{-1, 1}, {0, 1}, {1, 1},
	}
	for _, o := range offsets {
		drawText(img, image.Pt(pt.X+o[0], pt.Y+o[1]), label, outline)
	}
	drawText(img, pt, label, fill)
}
