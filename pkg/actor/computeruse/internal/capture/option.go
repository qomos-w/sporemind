package capture

import "image"

// CaptureOptions configures a single screenshot capture.
type CaptureOptions struct {
	Mode               CaptureMode
	DisplayID          int
	Region             image.Rectangle
	WindowID           int
	IncludeCursor      bool
	Quality            int
	EncodeFormat       string // "jpeg" | "png" | "raw"
	Annotate           AnnotateOptions
	PreviousFrame      *Frame
	DiffThreshold      float64
	DisableCompression bool
}

// AnnotateOptions configures visual overlays for LLM comprehension.
type AnnotateOptions struct {
	DrawGrid     bool
	GridSize     int // pixels per cell, default 100
	MarkElements bool
	DrawCursor   bool
}

// Validate normalizes option fields in place.
func (o *CaptureOptions) Validate() {
	if o.Quality < 1 {
		o.Quality = 85
	} else if o.Quality > 100 {
		o.Quality = 100
	}
	if o.EncodeFormat == "" {
		o.EncodeFormat = "jpeg"
	}
	if o.DiffThreshold <= 0 {
		o.DiffThreshold = 0.01
	}
	if o.Mode == ModeDelta && o.PreviousFrame == nil {
		o.Mode = ModeFull
	}
	if o.Annotate.GridSize == 0 {
		o.Annotate.GridSize = 100
	}
}
