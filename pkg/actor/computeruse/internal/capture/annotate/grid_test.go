package annotate

import (
	"image"
	"image/color"
	"testing"
)

func TestGridCoordSingleLetter(t *testing.T) {
	pt, err := GridCoordToPoint("A1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if pt.X != 50 || pt.Y != 50 {
		t.Fatalf("A1 = %v want (50,50)", pt)
	}
}

func TestGridCoordBeyondZ(t *testing.T) {
	// AA = column 26 → x range 2600..2700 → center 2650.
	pt, err := GridCoordToPoint("AA1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if pt.X != 2650 || pt.Y != 50 {
		t.Fatalf("AA1 = %v want (2650, 50)", pt)
	}
}

func TestGridCoordCustomCellSize(t *testing.T) {
	pt, err := GridCoordToPoint("B3", 50)
	if err != nil {
		t.Fatal(err)
	}
	// B=col 1, row 3 → row index 2. Center: (1*50+25, 2*50+25) = (75, 125).
	if pt.X != 75 || pt.Y != 125 {
		t.Fatalf("B3 cell=50 = %v want (75,125)", pt)
	}
}

func TestGridCoordLowercaseAndWhitespace(t *testing.T) {
	pt, err := GridCoordToPoint("  c5  ", 100)
	if err != nil {
		t.Fatal(err)
	}
	if pt.X != 250 || pt.Y != 450 {
		t.Fatalf("c5 = %v want (250, 450)", pt)
	}
}

func TestGridCoordRejects(t *testing.T) {
	bad := []string{
		"",
		"1A",
		"A",
		"AB",
		"A0",
		"A1.5",
		"!A1",
	}
	for _, b := range bad {
		if _, err := GridCoordToPoint(b, 100); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestGridCoordDefaultCellSize(t *testing.T) {
	// cellSize <= 0 falls back to 100.
	pt, err := GridCoordToPoint("A1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pt.X != 50 || pt.Y != 50 {
		t.Fatalf("A1 default = %v want (50,50)", pt)
	}
}

func TestDrawerPaintsLines(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	// Fill background opaque white so we can detect grid changes.
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	d := NewGridDrawer()
	d.CellSize = 100
	d.LineColor = color.RGBA{R: 255, G: 0, B: 0, A: 255}
	d.Draw(img)
	// Vertical line at x=100 should now have red pixels.
	r, g, b, _ := img.At(100, 50).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 {
		t.Fatalf("expected red at (100,50), got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
	// Off-line pixel should still be white.
	r, g, b, _ = img.At(50, 50).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Fatalf("expected white at (50,50), got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}

func TestDrawerPaintsLabelPixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	// Opaque white background with no annotations.
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	d := NewGridDrawer()
	d.CellSize = 100
	d.Draw(img)
	// The "A1" label is drawn at cell (0,0) corner + 2px margin. Its glyphs
	// span ~14x13 pixels, so the region (2,2)..(16,15) must contain pixels
	// that differ from the white background (the black outline in particular).
	r := image.Rect(2, 2, 2+2*7+2, 2+13+2).Intersect(img.Bounds())
	nonWhite := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if cr>>8 != 255 || cg>>8 != 255 || cb>>8 != 255 {
				nonWhite++
			}
		}
	}
	if nonWhite == 0 {
		t.Fatal("expected label text pixels in the A1 cell region, found none")
	}
}

func TestColLabelSymmetry(t *testing.T) {
	// Generation: colLabel(col) must equal the expected spreadsheet letters.
	for col, want := range map[int]string{
		0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB",
		51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA",
	} {
		if got := colLabel(col); got != want {
			t.Errorf("colLabel(%d) = %q, want %q", col, got, want)
		}
		// Round-trip: GridCoordToPoint must parse the generated label back to
		// the same 0-based column index (cell center at col*100+50).
		pt, err := GridCoordToPoint(want+"1", 100)
		if err != nil {
			t.Fatalf("GridCoordToPoint(%q1): %v", want, err)
		}
		if got := pt.X / 100; got != col {
			t.Errorf("GridCoordToPoint(%q1) column = %d, want %d", want, got, col)
		}
	}
}
