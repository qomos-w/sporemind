package annotate

import (
	"fmt"
	"image"
	"image/color"
	"sort"
)

// MarkDrawer draws Set-of-Marks annotations on images for LLM grounding.
type MarkDrawer struct {
	BoxColor    color.Color
	LabelColor  color.Color
	TextColor   color.Color
	BorderWidth int
	LabelSize   image.Point
	MaxLabels   int
}

// NewMarkDrawer returns a drawer with sensible defaults.
func NewMarkDrawer() *MarkDrawer {
	return &MarkDrawer{
		BoxColor:    color.RGBA{R: 255, G: 255, B: 0, A: 200},
		LabelColor:  color.RGBA{R: 0, G: 0, B: 255, A: 200},
		TextColor:   color.RGBA{R: 255, G: 255, B: 255, A: 255},
		BorderWidth: 2,
		LabelSize:   image.Point{X: 24, Y: 16},
		MaxLabels:   50,
	}
}

// Element is a detected interactive region.
type Element struct {
	ID       int
	Bounds   image.Rectangle
	Type     string // "button", "input", "link", "text", "icon"
	Text     string
	Priority int // higher = more important
}

// DrawElements draws numbered labels and bounding boxes on img.
func (m *MarkDrawer) DrawElements(img *image.RGBA, elements []Element) {
	sorted := make([]Element, len(elements))
	copy(sorted, elements)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})
	if len(sorted) > m.MaxLabels {
		sorted = sorted[:m.MaxLabels]
	}
	for i, elem := range sorted {
		label := fmt.Sprintf("%d", i+1)
		DrawRect(img, elem.Bounds, m.BoxColor, m.BorderWidth)
		labelPos := image.Point{
			X: elem.Bounds.Min.X,
			Y: elem.Bounds.Min.Y - m.LabelSize.Y,
		}
		if labelPos.Y < 0 {
			labelPos.Y = elem.Bounds.Min.Y
		}
		m.drawLabel(img, labelPos, label)
	}
}

func (m *MarkDrawer) drawLabel(img *image.RGBA, pos image.Point, label string) {
	labelRect := image.Rectangle{
		Min: pos,
		Max: image.Point{X: pos.X + m.LabelSize.X, Y: pos.Y + m.LabelSize.Y},
	}
	FillRect(img, labelRect, m.LabelColor)
	DrawRect(img, labelRect, m.TextColor, 1)
	if label != "" {
		drawText(img, image.Pt(pos.X+2, pos.Y+2), label, m.TextColor)
	}
}

// DrawActionHint draws a pulsing circle plus a text-anchor box at target.
func (m *MarkDrawer) DrawActionHint(img *image.RGBA, _ string, target image.Point) {
	for r := 10; r <= 30; r += 5 {
		alpha := uint8(255 - (r-10)*8)
		c := color.RGBA{R: 0, G: 255, B: 0, A: alpha}
		DrawCircle(img, target, r, c)
	}
	textRect := image.Rectangle{
		Min: image.Point{X: target.X + 20, Y: target.Y - 10},
		Max: image.Point{X: target.X + 120, Y: target.Y + 10},
	}
	FillRect(img, textRect, color.RGBA{R: 0, G: 0, B: 0, A: 180})
}

// DetectElementsByColor is a lightweight grid-based contrast heuristic.
func DetectElementsByColor(img *image.RGBA, minSize, maxSize int) []Element {
	bounds := img.Bounds()
	var elements []Element
	gridSize := 50
	for y := bounds.Min.Y; y < bounds.Max.Y; y += gridSize {
		for x := bounds.Min.X; x < bounds.Max.X; x += gridSize {
			if !isHighContrastRegion(img, x, y, gridSize) {
				continue
			}
			elem := Element{
				ID:     len(elements) + 1,
				Bounds: image.Rect(x, y, min(x+gridSize, bounds.Max.X), min(y+gridSize, bounds.Max.Y)),
				Type:   "unknown",
			}
			if size := elem.Bounds.Dx() * elem.Bounds.Dy(); size >= minSize && size <= maxSize {
				elements = append(elements, elem)
			}
		}
	}
	return elements
}

func isHighContrastRegion(img *image.RGBA, x, y, size int) bool {
	bounds := img.Bounds()
	var totalDiff int
	count := 0
	for dy := 0; dy < size && y+dy < bounds.Max.Y; dy++ {
		for dx := 0; dx < size && x+dx < bounds.Max.X; dx++ {
			if x+dx+1 >= bounds.Max.X {
				continue
			}
			idx := ((y+dy)*bounds.Dx() + (x + dx)) * 4
			next := idx + 4
			if next+3 >= len(img.Pix) {
				continue
			}
			dr := int(img.Pix[idx]) - int(img.Pix[next])
			dg := int(img.Pix[idx+1]) - int(img.Pix[next+1])
			db := int(img.Pix[idx+2]) - int(img.Pix[next+2])
			totalDiff += dr*dr + dg*dg + db*db
			count++
		}
	}
	if count == 0 {
		return false
	}
	return totalDiff/count > 1000
}

// ElementPositions maps an element's ID to the center of its bounds.
func ElementPositions(elements []Element) map[int]image.Point {
	positions := make(map[int]image.Point)
	for _, e := range elements {
		positions[e.ID] = image.Point{
			X: (e.Bounds.Min.X + e.Bounds.Max.X) / 2,
			Y: (e.Bounds.Min.Y + e.Bounds.Max.Y) / 2,
		}
	}
	return positions
}
