package annotate

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
)

// GridDrawer overlays a coordinate grid on a screenshot. Cells are labeled
// alphanumerically ("A1", "B3", ...) so an LLM can refer to a region by
// label rather than pixel coordinates.
type GridDrawer struct {
	CellSize  int
	LineColor color.Color
}

// NewGridDrawer returns a drawer with default 100px cells in red.
func NewGridDrawer() *GridDrawer {
	return &GridDrawer{
		CellSize:  100,
		LineColor: color.RGBA{R: 255, G: 0, B: 0, A: 120},
	}
}

// Draw paints grid lines onto img. The grid is anchored at img.Bounds().Min.
// Each cell is labeled with its coordinate ("A1", "B3", ...) in the top-left
// corner so an LLM can reference cells without counting grid lines.
func (g *GridDrawer) Draw(img *image.RGBA) {
	cs := g.CellSize
	if cs <= 0 {
		cs = 100
	}
	bounds := img.Bounds()
	// Vertical lines.
	for x := bounds.Min.X; x <= bounds.Max.X; x += cs {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			img.Set(x, y, g.LineColor)
		}
	}
	// Horizontal lines.
	for y := bounds.Min.Y; y <= bounds.Max.Y; y += cs {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.Set(x, y, g.LineColor)
		}
	}
	// Cell labels: column letters + 1-based row number, white with a black
	// outline so they stay visible over any screenshot background.
	labelColor := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	outlineColor := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	for row := 0; row*cs < bounds.Dy(); row++ {
		for col := 0; col*cs < bounds.Dx(); col++ {
			label := colLabel(col) + strconv.Itoa(row+1)
			pt := image.Pt(bounds.Min.X+col*cs+2, bounds.Min.Y+row*cs+2)
			drawOutlinedText(img, pt, label, labelColor, outlineColor)
		}
	}
}

// colLabel converts a 0-based column index to its spreadsheet-style letters:
// 0→"A", 1→"B", 25→"Z", 26→"AA", 27→"AB", ... It is the inverse of the
// column parsing in GridCoordToPoint.
func colLabel(col int) string {
	if col < 0 {
		return ""
	}
	var b [8]byte
	i := len(b)
	for {
		i--
		b[i] = byte('A' + col%26)
		col = col/26 - 1
		if col < 0 {
			break
		}
	}
	return string(b[i:])
}

// GridCoordToPoint converts a label like "B3" or "AA12" to the center
// pixel of that cell, assuming the canonical anchor at (0,0).
//
// Columns: A=0, B=1, ..., Z=25, AA=26, AB=27, ...
// Rows:    1-based positive integers.
func GridCoordToPoint(coord string, cellSize int) (image.Point, error) {
	if cellSize <= 0 {
		cellSize = 100
	}
	s := strings.TrimSpace(strings.ToUpper(coord))
	if s == "" {
		return image.Point{}, fmt.Errorf("empty grid coordinate")
	}
	col := 0
	i := 0
	letters := 0
	for ; i < len(s); i++ {
		ch := s[i]
		if ch < 'A' || ch > 'Z' {
			break
		}
		col = col*26 + int(ch-'A'+1)
		letters++
	}
	if letters == 0 {
		return image.Point{}, fmt.Errorf("missing column letters in %q", coord)
	}
	col-- // convert to 0-based
	if i >= len(s) {
		return image.Point{}, fmt.Errorf("missing row digits in %q", coord)
	}
	row := 0
	for ; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' {
			return image.Point{}, fmt.Errorf("invalid character %q in %q", string(ch), coord)
		}
		row = row*10 + int(ch-'0')
	}
	if row < 1 {
		return image.Point{}, fmt.Errorf("row must be >= 1 in %q", coord)
	}
	row-- // convert to 0-based
	cx := col*cellSize + cellSize/2
	cy := row*cellSize + cellSize/2
	return image.Point{X: cx, Y: cy}, nil
}
