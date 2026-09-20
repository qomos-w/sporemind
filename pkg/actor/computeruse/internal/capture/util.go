package capture

import "fmt"

// Resize returns a nearest-neighbor scaled copy of raw RGBA pixels.
func Resize(src []byte, srcW, srcH, dstW, dstH int) ([]byte, error) {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d -> %dx%d", srcW, srcH, dstW, dstH)
	}
	if len(src) != srcW*srcH*4 {
		return nil, fmt.Errorf("src size mismatch: expected %d, got %d", srcW*srcH*4, len(src))
	}
	dst := make([]byte, dstW*dstH*4)
	xRatio := float64(srcW) / float64(dstW)
	yRatio := float64(srcH) / float64(dstH)
	for y := 0; y < dstH; y++ {
		srcY := int(float64(y) * yRatio)
		if srcY >= srcH {
			srcY = srcH - 1
		}
		for x := 0; x < dstW; x++ {
			srcX := int(float64(x) * xRatio)
			if srcX >= srcW {
				srcX = srcW - 1
			}
			srcIdx := (srcY*srcW + srcX) * 4
			dstIdx := (y*dstW + x) * 4
			copy(dst[dstIdx:dstIdx+4], src[srcIdx:srcIdx+4])
		}
	}
	return dst, nil
}

// Scale returns pixels scaled by factor (e.g., 0.5 for half size).
func Scale(src []byte, srcW, srcH int, factor float64) ([]byte, int, int, error) {
	if factor <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid scale factor: %f", factor)
	}
	dstW := int(float64(srcW) * factor)
	dstH := int(float64(srcH) * factor)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst, err := Resize(src, srcW, srcH, dstW, dstH)
	return dst, dstW, dstH, err
}

// ToGrayscale converts RGBA pixels to grayscale (R=G=B=luminance).
func ToGrayscale(src []byte) []byte {
	dst := make([]byte, len(src))
	for i := 0; i+3 < len(src); i += 4 {
		r := float64(src[i])
		g := float64(src[i+1])
		b := float64(src[i+2])
		l := uint8(0.299*r + 0.587*g + 0.114*b)
		dst[i] = l
		dst[i+1] = l
		dst[i+2] = l
		dst[i+3] = src[i+3]
	}
	return dst
}
