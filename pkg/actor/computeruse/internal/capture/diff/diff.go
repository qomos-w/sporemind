// Package diff provides frame differencing for delta capture mode.
package diff

import (
	"fmt"
	"image"
)

// Detector compares two RGBA frames.
type Detector struct {
	BlockSize        int // default 16
	Threshold        int // per-pixel difference threshold (0-255), default 30
	MinChangedBlocks int // minimum to consider a frame "different"
}

// NewDetector creates a detector with defaults.
func NewDetector() *Detector {
	return &Detector{BlockSize: 16, Threshold: 30, MinChangedBlocks: 1}
}

// Result is the difference analysis between two frames.
type Result struct {
	IsIdentical   bool
	ChangedBounds image.Rectangle
	ChangedBlocks int
	TotalBlocks   int
	ChangePercent float64
}

// Compare two equally-sized RGBA buffers.
func (d *Detector) Compare(prev, curr []byte, width, height int) (*Result, error) {
	if len(prev) != len(curr) {
		return nil, fmt.Errorf("frame size mismatch: %d vs %d", len(prev), len(curr))
	}
	if len(prev) != width*height*4 {
		return nil, fmt.Errorf("invalid pixel data size: expected %d, got %d", width*height*4, len(prev))
	}

	bs := d.BlockSize
	if bs < 1 {
		bs = 16
	}
	blocksX := (width + bs - 1) / bs
	blocksY := (height + bs - 1) / bs
	totalBlocks := blocksX * blocksY

	changedBlocks := 0
	minX, minY := width, height
	maxX, maxY := 0, 0

	for by := 0; by < blocksY; by++ {
		for bx := 0; bx < blocksX; bx++ {
			startY := by * bs
			endY := startY + bs
			if endY > height {
				endY = height
			}
			startX := bx * bs
			endX := startX + bs
			if endX > width {
				endX = width
			}
			blockChanged := false
			for y := startY; y < endY && !blockChanged; y++ {
				for x := startX; x < endX && !blockChanged; x++ {
					idx := (y*width + x) * 4
					if pixelDiff(prev[idx:idx+4], curr[idx:idx+4], d.Threshold) {
						blockChanged = true
						changedBlocks++
						if x < minX {
							minX = x
						}
						if y < minY {
							minY = y
						}
						if x > maxX {
							maxX = x
						}
						if y > maxY {
							maxY = y
						}
					}
				}
			}
		}
	}

	changePercent := float64(changedBlocks) / float64(totalBlocks)
	if changedBlocks < d.MinChangedBlocks {
		return &Result{IsIdentical: true, TotalBlocks: totalBlocks, ChangedBlocks: changedBlocks, ChangePercent: changePercent}, nil
	}

	// Pad changed region by one block.
	pad := bs
	minX -= pad
	minY -= pad
	maxX += pad
	maxY += pad
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= width {
		maxX = width - 1
	}
	if maxY >= height {
		maxY = height - 1
	}

	return &Result{
		IsIdentical:   false,
		ChangedBounds: image.Rect(minX, minY, maxX+1, maxY+1),
		ChangedBlocks: changedBlocks,
		TotalBlocks:   totalBlocks,
		ChangePercent: changePercent,
	}, nil
}

// ExtractRegion copies a sub-rectangle out of raw RGBA pixels.
func ExtractRegion(pixels []byte, srcW, srcH int, region image.Rectangle) ([]byte, error) {
	if region.Empty() {
		return nil, fmt.Errorf("empty region")
	}
	if region.Min.X < 0 || region.Min.Y < 0 || region.Max.X > srcW || region.Max.Y > srcH {
		return nil, fmt.Errorf("region %v exceeds frame bounds %dx%d", region, srcW, srcH)
	}
	dstW := region.Dx()
	dstH := region.Dy()
	dst := make([]byte, dstW*dstH*4)
	for y := 0; y < dstH; y++ {
		srcY := region.Min.Y + y
		srcOff := (srcY*srcW + region.Min.X) * 4
		dstOff := y * dstW * 4
		copy(dst[dstOff:dstOff+dstW*4], pixels[srcOff:srcOff+dstW*4])
	}
	return dst, nil
}

// TileChange describes one changed tile in a frame. Bounds are clipped
// to the frame so edge tiles smaller than tileSize are reported as-is.
type TileChange struct {
	Rect image.Rectangle
}

// TileCompare partitions the frame into tileSize-aligned tiles and
// returns the list of tiles that contain at least one pixel exceeding
// the threshold. Threshold default and pixel-comparison semantics match
// Compare(). When prev is nil or sized differently, every tile is
// returned (full keyframe).
func TileCompare(prev, curr []byte, width, height, tileSize, threshold int) []TileChange {
	if tileSize <= 0 {
		tileSize = 64
	}
	if threshold <= 0 {
		threshold = 30
	}
	tilesX := (width + tileSize - 1) / tileSize
	tilesY := (height + tileSize - 1) / tileSize

	// Full keyframe path.
	if len(prev) != width*height*4 || len(curr) != width*height*4 || len(prev) != len(curr) {
		out := make([]TileChange, 0, tilesX*tilesY)
		for ty := 0; ty < tilesY; ty++ {
			y0 := ty * tileSize
			y1 := y0 + tileSize
			if y1 > height {
				y1 = height
			}
			for tx := 0; tx < tilesX; tx++ {
				x0 := tx * tileSize
				x1 := x0 + tileSize
				if x1 > width {
					x1 = width
				}
				out = append(out, TileChange{Rect: image.Rect(x0, y0, x1, y1)})
			}
		}
		return out
	}

	out := make([]TileChange, 0, 16)
	for ty := 0; ty < tilesY; ty++ {
		y0 := ty * tileSize
		y1 := y0 + tileSize
		if y1 > height {
			y1 = height
		}
		for tx := 0; tx < tilesX; tx++ {
			x0 := tx * tileSize
			x1 := x0 + tileSize
			if x1 > width {
				x1 = width
			}
			if tileChanged(prev, curr, width, x0, y0, x1, y1, threshold) {
				out = append(out, TileChange{Rect: image.Rect(x0, y0, x1, y1)})
			}
		}
	}
	return out
}

func tileChanged(prev, curr []byte, width, x0, y0, x1, y1, threshold int) bool {
	for y := y0; y < y1; y++ {
		base := y * width * 4
		for x := x0; x < x1; x++ {
			idx := base + x*4
			if pixelDiff(prev[idx:idx+4], curr[idx:idx+4], threshold) {
				return true
			}
		}
	}
	return false
}

// pixelDiff returns true if two pixels differ by more than threshold under
// perceptual weights (R=299, G=587, B=114 / 1000).
func pixelDiff(a, b []byte, threshold int) bool {
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	dr := int(a[0]) - int(b[0])
	dg := int(a[1]) - int(b[1])
	db := int(a[2]) - int(b[2])
	d := (dr*dr*299 + dg*dg*587 + db*db*114) / 1000
	return d > threshold*threshold
}
