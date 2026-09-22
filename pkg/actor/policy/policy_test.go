package policy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func sampleReq() gen.PolicyDecideReq {
	return gen.PolicyDecideReq{
		State: "customer cannot log in since yesterday, demo in two hours",
		Questions: map[string]gen.PolicyQuestion{
			"team": {
				Type:         "choice",
				Instructions: "Which team should handle this?",
				Choices:      map[string]string{"technical": "Bugs", "account": "Login issues"},
			},
			"urgency": {
				Type:         "noul",
				Instructions: "The message conveys urgency",
			},
			"frustration": {
				Type:         "score",
				Instructions: "How frustrated the customer appears",
				Levels:       []string{"Calm", "Frustrated", "Angry"},
			},
		},
	}
}

// TestJevDecideHappyPath verifies the wire shape we send and the typed mapping
// we get back (choice / score / noul + probabilities + confidence + provenance).
func TestJevDecideHappyPath(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k-1" {
			t.Errorf("auth header = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "jev-1.13.0",
			"answers": {
				"team": {"type": "choice", "choice": "account", "confidence": 0.78, "probabilities": {"account": 0.9, "technical": 0.1}},
				"urgency": {"type": "noul", "noul": 0.83},
				"frustration": {"type": "score", "score": 1.2, "confidence": 0.66, "probabilities": {"0": 0.1, "1": 0.7, "2": 0.2}}
			},
			"usage": {"input_tokens": 100, "output_tokens": 10}
		}`))
	}))
	defer srv.Close()

	cfg := PolicySnapshot{Mode: backendAuto, JevKey: "k-1", JevModel: "jev-latest", JevEndpoint: srv.URL}
	answers, err := jevDecide(context.Background(), srv.Client(), cfg, sampleReq())
	if err != nil {
		t.Fatalf("jevDecide: %v", err)
	}

	if got := gotBody["model"]; got != "jev-latest" {
		t.Errorf("request model = %v", got)
	}
	questions := gotBody["questions"].(map[string]any)
	if _, ok := questions["team"].(map[string]any)["criteria"]; !ok {
		t.Errorf("choice question missing criteria: %v", questions["team"])
	}
	if _, ok := questions["urgency"].(map[string]any)["criteria"]; ok {
		t.Errorf("noul question must not carry criteria")
	}

	if answers["team"].Choice != "account" || answers["team"].Backend != backendJev || !answers["team"].Calibrated {
		t.Errorf("team answer = %+v", answers["team"])
	}
	if answers["team"].Confidence != 0.78 {
		t.Errorf("team confidence = %v", answers["team"].Confidence)
	}
	if answers["urgency"].Noul != 0.83 {
		t.Errorf("urgency noul = %v", answers["urgency"].Noul)
	}
	if answers["frustration"].Score != 1.2 {
		t.Errorf("frustration score = %v", answers["frustration"].Score)
	}
}

// TestJevDecideAvailabilityFaults: 429/5xx/network/missing key are availability
// faults (failover-eligible).
func TestJevDecideAvailabilityFaults(t *testing.T) {
	cfg := PolicySnapshot{JevKey: "k", JevEndpoint: "http://127.0.0.1:1"} // unroutable
	if _, err := jevDecide(context.Background(), http.DefaultClient, cfg, sampleReq()); !isUnavailable(err) {
		t.Errorf("network error should be availability, got %v", err)
	}

	for _, code := range []int{http.StatusTooManyRequests, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		cfg := PolicySnapshot{JevKey: "k", JevEndpoint: srv.URL}
		_, err := jevDecide(context.Background(), srv.Client(), cfg, sampleReq())
		if !isUnavailable(err) {
			t.Errorf("http %d should be availability, got %v", code, err)
		}
		srv.Close()
	}

	if _, err := jevDecide(context.Background(), http.DefaultClient, PolicySnapshot{JevEndpoint: "http://x"}, sampleReq()); !isUnavailable(err) {
		t.Errorf("missing key should be availability, got %v", err)
	}
}

// TestJevDecideContractFaults: 400 and malformed answers are contract faults —
// they must abort the failover chain loudly, not mask via the LLM backend.
func TestJevDecideContractFaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "bad question"}`))
	}))
	defer srv.Close()
	cfg := PolicySnapshot{JevKey: "k", JevEndpoint: srv.URL}
	if _, err := jevDecide(context.Background(), srv.Client(), cfg, sampleReq()); isUnavailable(err) {
		t.Errorf("http 400 must not be availability, got %v", err)
	}

	// Answer referencing an option outside the criteria → contract fault.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"team":{"type":"choice","choice":"nope"}}}`))
	}))
	defer srv2.Close()
	cfg.JevEndpoint = srv2.URL
	if _, err := jevDecide(context.Background(), srv2.Client(), cfg, sampleReq()); isUnavailable(err) {
		t.Errorf("unknown choice must not be availability, got %v", err)
	}
}

// TestParseLLMAnswers covers the strict-JSON contract of the fallback backend.
func TestParseLLMAnswers(t *testing.T) {
	req := sampleReq()

	// fenced, extra prose around — must still parse
	out := "Here you go:\n```json\n{\"answers\":{\n" +
		"\"team\":{\"choice\":\"technical\",\"probabilities\":{\"technical\":0.8,\"account\":0.2},\"confidence\":0.7},\n" +
		"\"urgency\":{\"noul\":0.9},\n" +
		"\"frustration\":{\"score\":2,\"probabilities\":{\"0\":0,\"1\":0.1,\"2\":0.9},\"confidence\":0.5}}}\n```\n"
	answers, err := parseLLMAnswers(out, req.Questions)
	if err != nil {
		t.Fatalf("parseLLMAnswers: %v", err)
	}
	if answers["team"].Choice != "technical" || answers["team"].Backend != backendLLM || answers["team"].Calibrated {
		t.Errorf("team = %+v", answers["team"])
	}
	if answers["urgency"].Noul != 0.9 || answers["urgency"].Calibrated {
		t.Errorf("urgency = %+v", answers["urgency"])
	}
	if answers["frustration"].Score != 2 {
		t.Errorf("frustration = %+v", answers["frustration"])
	}

	// missing key
	if _, err := parseLLMAnswers(`{"answers":{"team":{"choice":"technical"}}}`, req.Questions); err == nil {
		t.Error("missing answer key must error")
	}
	// invalid choice
	if _, err := parseLLMAnswers(`{"answers":{"team":{"choice":"zzz"},"urgency":{"noul":1},"frustration":{"score":0}}}`, req.Questions); err == nil {
		t.Error("unknown choice must error")
	}
	// missing score
	if _, err := parseLLMAnswers(`{"answers":{"team":{"choice":"technical"},"urgency":{"noul":1},"frustration":{}}}`, req.Questions); err == nil {
		t.Error("missing score must error")
	}
	// out-of-range values clamp instead of erroring
	answers, err = parseLLMAnswers(`{"answers":{"team":{"choice":"technical"},"urgency":{"noul":2.5},"frustration":{"score":9}}}`, req.Questions)
	if err != nil {
		t.Fatalf("clamp parse: %v", err)
	}
	if answers["urgency"].Noul != 1 || answers["frustration"].Score != 2 {
		t.Errorf("clamped values wrong: %+v %+v", answers["urgency"], answers["frustration"])
	}
	// no JSON at all
	if _, err := parseLLMAnswers("I cannot answer that", req.Questions); err == nil {
		t.Error("non-JSON output must error")
	}
}

// TestBackendOrder pins the failover chain semantics.
func TestBackendOrder(t *testing.T) {
	if got := backendOrder(backendAuto, ""); len(got) != 2 || got[0] != backendJev || got[1] != backendLLM {
		t.Errorf("auto order = %v", got)
	}
	if got := backendOrder(backendJev, ""); len(got) != 1 || got[0] != backendJev {
		t.Errorf("jev mode = %v", got)
	}
	if got := backendOrder(backendLLM, ""); len(got) != 1 || got[0] != backendLLM {
		t.Errorf("llm mode = %v", got)
	}
	if got := backendOrder(backendAuto, backendLLM); len(got) != 1 || got[0] != backendLLM {
		t.Errorf("per-call override = %v", got)
	}
}

func TestValidateDecideReq(t *testing.T) {
	if err := validateDecideReq(gen.PolicyDecideReq{State: "s"}); err == nil {
		t.Error("no questions must error")
	}
	if err := validateDecideReq(gen.PolicyDecideReq{Questions: map[string]gen.PolicyQuestion{"a": {Type: "noul", Instructions: "x"}}}); err == nil {
		t.Error("no state must error")
	}
	if err := validateDecideReq(gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{"a": {Type: "spin", Instructions: "x"}}}); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("unknown type: %v", err)
	}
	if err := validateDecideReq(gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{"a": {Type: "choice", Instructions: "x"}}}); err == nil {
		t.Error("choice without choices must error")
	}
	if err := validateDecideReq(gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{"a": {Type: "score", Instructions: "x", Levels: []string{"one"}}}}); err == nil {
		t.Error("score with <2 levels must error")
	}
}
