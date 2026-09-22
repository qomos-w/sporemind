package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// Error classification — availability vs contract faults
// ---------------------------------------------------------------------------

// unavailableError marks an availability fault (network error, 429, 5xx,
// missing credential, unusable output). Only these trigger failover to the
// next backend. Contract faults (e.g. HTTP 400 from a malformed request we
// built) abort the chain loudly so drift is visible instead of masked.
type unavailableError struct{ err error }

func (e *unavailableError) Error() string { return e.err.Error() }
func (e *unavailableError) Unwrap() error { return e.err }

func unavailable(format string, args ...any) error {
	return &unavailableError{err: fmt.Errorf(format, args...)}
}

func isUnavailable(err error) bool {
	var u *unavailableError
	return err != nil && asUnwrap(err, &u)
}

// asUnwrap is errors.As restricted to *unavailableError (avoids importing
// errors just for the typed check in the hot failover path).
func asUnwrap(err error, target **unavailableError) bool {
	for err != nil {
		if e, ok := err.(*unavailableError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---------------------------------------------------------------------------
// Jev wire types (POST /v1/systemone)
// ---------------------------------------------------------------------------

type jevRequest struct {
	State     string                 `json:"state"`
	Model     string                 `json:"model"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	// Criteria is the option map (choice) or the ordered level list (score);
	// omitted for noul.
	Criteria any `json:"criteria,omitempty"`
}

type jevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]jevAnswer `json:"answers"`
	Usage   jevUsage             `json:"usage"`
}

type jevUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type jevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Noul          float64            `json:"noul"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// jevDecide evaluates the whole question batch in one Jev call.
func jevDecide(ctx context.Context, httpClient *http.Client, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
	if cfg.JevKey == "" {
		return nil, unavailable("jev: no API key configured")
	}

	body := jevRequest{
		State:     req.State,
		Model:     cfg.JevModel,
		Questions: make(map[string]jevQuestion, len(req.Questions)),
	}
	// Deterministic key order keeps the serialized body stable for tests.
	keys := make([]string, 0, len(req.Questions))
	for k := range req.Questions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		q := req.Questions[k]
		jq := jevQuestion{Type: q.Type, Instructions: q.Instructions}
		switch q.Type {
		case "choice":
			jq.Criteria = q.Choices
		case "score":
			jq.Criteria = q.Levels
		}
		body.Questions[k] = jq
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("jev: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.JevEndpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, unavailable("jev: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+cfg.JevKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, unavailable("jev: post: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, unavailable("jev: read response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, unavailable("jev: http %d: %s", resp.StatusCode, truncateErrBody(respBody))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, unavailable("jev: http %d (bad key): %s", resp.StatusCode, truncateErrBody(respBody))
	case resp.StatusCode != http.StatusOK:
		// 400-class: our request shape is off — a contract fault, do not mask
		// it with an LLM failover.
		return nil, fmt.Errorf("jev: http %d: %s", resp.StatusCode, truncateErrBody(respBody))
	}

	var jr jevResponse
	if err := json.Unmarshal(respBody, &jr); err != nil {
		return nil, fmt.Errorf("jev: decode response: %w", err)
	}

	answers := make(map[string]gen.PolicyAnswer, len(req.Questions))
	for key, q := range req.Questions {
		ja, ok := jr.Answers[key]
		if !ok {
			return nil, fmt.Errorf("jev: answer missing for question %q", key)
		}
		pa := gen.PolicyAnswer{
			Type:          q.Type,
			Probabilities: ja.Probabilities,
			Confidence:    clamp01(ja.Confidence),
			Backend:       backendJev,
			Calibrated:    true,
		}
		switch q.Type {
		case "choice":
			if _, ok := q.Choices[ja.Choice]; !ok {
				return nil, fmt.Errorf("jev: question %q: answer %q not in criteria", key, ja.Choice)
			}
			pa.Choice = ja.Choice
		case "score":
			n := float64(len(q.Levels) - 1)
			pa.Score = clampRange(ja.Score, 0, n)
		case "noul":
			pa.Noul = clamp01(ja.Noul)
		}
		answers[key] = pa
	}
	return answers, nil
}

// truncateErrBody keeps error diagnostics bounded.
func truncateErrBody(b []byte) string {
	const max = 200
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}

func clamp01(v float64) float64 { return clampRange(v, 0, 1) }

func clampRange(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
