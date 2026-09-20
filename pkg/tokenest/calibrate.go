package tokenest

import "sync"

// Calibration bounds keep per-class coefficients from drifting to absurd
// values when the feedback signal is noisy or compared across mismatched
// scopes (e.g. full prompt tokens vs message text tokens only).
const (
	minLatinCoef = 0.1
	maxLatinCoef = 0.8
	minCJKCoef   = 0.5
	maxCJKCoef   = 3.0
	minOtherCoef = 0.1
	maxOtherCoef = 0.8

	minRatio = 0.5
	maxRatio = 2.0
)

// CharClass classifies characters for per-class token estimation.
type CharClass int

const (
	ClassLatin CharClass = iota // A-Z, a-z
	ClassCJK                    // CJK + Hiragana + Katakana + Hangul
	ClassOther                  // digits, punctuation, whitespace, symbols
)

// Classify returns the CharClass for a rune.
func Classify(r rune) CharClass {
	if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
		return ClassLatin
	}
	if isCJK(r) {
		return ClassCJK
	}
	return ClassOther
}

// CharCounts records per-class character counts from one or more texts.
type CharCounts struct {
	Latin int
	CJK   int
	Other int
}

// CountChars sums character classes across all provided texts.
func CountChars(texts ...string) CharCounts {
	var cc CharCounts
	for _, t := range texts {
		for _, r := range t {
			switch Classify(r) {
			case ClassLatin:
				cc.Latin++
			case ClassCJK:
				cc.CJK++
			case ClassOther:
				cc.Other++
			}
		}
	}
	return cc
}

// Calibration holds per-class token estimation coefficients that are
// continuously refined from actual LLM usage feedback.
type Calibration struct {
	mu        sync.RWMutex
	LatinCoef float64 // tokens per Latin char, default 0.25
	CJKCoef   float64 // tokens per CJK char, default 1.5
	OtherCoef float64 // tokens per other char, default 0.25
	Count     int64   // number of calibration samples applied

	// CumEstimatedTokens and CumActualTokens accumulate the estimated and
	// actual token counts across all Update calls. The ratio between them is
	// used to drive coefficient updates, which smooths out per-dispatch noise
	// and allows the calibration to persist its running totals.
	CumEstimatedTokens int64
	CumActualTokens    int64
}

// CalibrationSnapshot is the serializable form of Calibration.
type CalibrationSnapshot struct {
	LatinCoef          float64 `json:"latinCoef"`
	CJKCoef            float64 `json:"cjkCoef"`
	OtherCoef          float64 `json:"otherCoef"`
	Count              int64   `json:"count"`
	CumEstimatedTokens int64   `json:"cumEstimatedTokens"`
	CumActualTokens    int64   `json:"cumActualTokens"`
}

// NewCalibration returns a Calibration with default coefficients.
func NewCalibration() *Calibration {
	return &Calibration{
		LatinCoef: 0.25,
		CJKCoef:   1.5,
		OtherCoef: 0.25,
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// EstimateTokens estimates token count using the tiktoken base count corrected
// by the cumulative actual/estimated ratio. tiktoken already encodes per-class
// differences, so calibration now applies a single global scale factor rather
// than per-class coefficients (those fields are retained only for backward
// compatibility with persisted snapshots). With no calibration samples it
// returns the plain tiktoken estimate.
func (c *Calibration) EstimateTokens(text string) int {
	base := EstimateTokens(text)
	if c == nil {
		return base
	}
	c.mu.RLock()
	est, act := c.CumEstimatedTokens, c.CumActualTokens
	c.mu.RUnlock()
	if est <= 0 || act <= 0 {
		return base
	}
	scale := clamp(float64(act)/float64(est), minRatio, maxRatio)
	return int(float64(base) * scale)
}

// Update calibrates the estimator using real token count feedback from the
// LLM. estimatedLayout is the tiktoken-based layout estimate for the scope
// matching actualInputTokens; actualInputTokens is the real token count from
// the LLM usage response. The cumulative ratio of actual/estimated is applied
// as a single global scale factor in EstimateTokens, so Update only needs to
// accumulate running totals. charCounts is accepted for API stability but no
// longer drives a per-class adjustment (tiktoken already handles class
// differences).
func (c *Calibration) Update(charCounts CharCounts, estimatedLayout, actualInputTokens int) {
	if c == nil || estimatedLayout <= 0 || actualInputTokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.CumEstimatedTokens += int64(estimatedLayout)
	c.CumActualTokens += int64(actualInputTokens)
	c.Count++
}

// Snapshot returns a serializable snapshot of the calibration state.
func (c *Calibration) Snapshot() CalibrationSnapshot {
	if c == nil {
		return CalibrationSnapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CalibrationSnapshot{
		LatinCoef:          c.LatinCoef,
		CJKCoef:            c.CJKCoef,
		OtherCoef:          c.OtherCoef,
		Count:              c.Count,
		CumEstimatedTokens: c.CumEstimatedTokens,
		CumActualTokens:    c.CumActualTokens,
	}
}

// Load restores calibration state from a snapshot. If the snapshot has zero
// Count, coefficients are left unchanged (defensive against empty data).
// Coefficients outside safe ranges are clamped; grossly invalid snapshots are
// fully reset to defaults so a corrupted mailbox cannot poison future estimates.
func (c *Calibration) Load(snap CalibrationSnapshot) {
	if c == nil || snap.Count == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.LatinCoef = clamp(snap.LatinCoef, minLatinCoef, maxLatinCoef)
	c.CJKCoef = clamp(snap.CJKCoef, minCJKCoef, maxCJKCoef)
	c.OtherCoef = clamp(snap.OtherCoef, minOtherCoef, maxOtherCoef)
	c.Count = snap.Count
	if snap.CumEstimatedTokens > 0 && snap.CumActualTokens > 0 {
		c.CumEstimatedTokens = snap.CumEstimatedTokens
		c.CumActualTokens = snap.CumActualTokens
	} else {
		c.CumEstimatedTokens = 0
		c.CumActualTokens = 0
	}
}
