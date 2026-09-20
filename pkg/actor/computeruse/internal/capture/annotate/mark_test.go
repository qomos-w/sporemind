package annotate

import (
	"image"
	"testing"
)

func TestMarkDrawerDrawsLabelText(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	// Opaque white background with no annotations.
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	m := NewMarkDrawer()
	// Label rect is (10,10)..(34,26) filled blue; the white text glyphs are
	// drawn inside it starting at (12,12). The white border outline lies on
	// the rect edge, outside the scan region below.
	m.drawLabel(img, image.Pt(10, 10), "7")
	r := image.Rect(12, 12, 19, 25).Intersect(img.Bounds())
	white := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if cr>>8 == 255 && cg>>8 == 255 && cb>>8 == 255 {
				white++
			}
		}
	}
	if white == 0 {
		t.Fatal("expected white label text pixels inside the label rect, found none")
	}
}
