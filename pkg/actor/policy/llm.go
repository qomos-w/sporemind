package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// LLM fallback backend — aiaggregator.dispatch + prompt + strict JSON output
// ---------------------------------------------------------------------------

// llmDecide emulates the policy primitives with one LLM call: the whole
// question batch is encoded into a single prompt and the model must answer
// with strict JSON. Probabilities/confidence are self-reported and therefore
// marked Calibrated=false — consumers must weight them more conservatively.
//
// Any fault here (missing unit, aggregator unavailable, stream error,
// unparseable or invalid output) is an availability fault: the chain fails
// open rather than retrying. callCtx is already timeout-bounded by the caller.
func llmDecide(ctx actor.PureContext, callCtx context.Context, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
	if cfg.LLMUnit == nil || cfg.LLMUnit.Model == "" {
		return nil, unavailable("llm: no fallback unit configured")
	}
	aggRef, ok := ctx.LookupService("aiaggregator")
	if !ok {
		return nil, unavailable("llm: aiaggregator service not available")
	}

	send := gen.SendSessionMessageReq{
		SessionID: ctx.NewID().String(),
		System:    llmSystemPrompt,
		Unit:      cfg.LLMUnit,
		Messages: []domain.ChatMessage{{
			ID:   ctx.NewID().String(),
			Role: domain.ChatRoleUser,
			Content: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: buildLLMUserPrompt(req),
			}},
		}},
		Temperature: 0,
	}

	call := aggRef.Invoke(callCtx, "aiaggregator.dispatch", send)
	if call == nil {
		return nil, unavailable("llm: dispatch invoke returned nil")
	}
	defer call.Close()

	var text strings.Builder
	for {
		v, err := call.Next(callCtx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, unavailable("llm: dispatch stream: %w", err)
		}
		chunk, derr := decodeAggregatorChunk(v)
		if derr != nil {
			continue
		}
		if chunk.Kind == domain.AggregatorChunkText {
			text.WriteString(chunk.Text)
		}
	}
	if _, err := call.Final(callCtx); err != nil {
		return nil, unavailable("llm: dispatch final: %w", err)
	}

	answers, err := parseLLMAnswers(text.String(), req.Questions)
	if err != nil {
		return nil, unavailable("llm: %w", err)
	}
	return answers, nil
}

const llmSystemPrompt = `You are a policy judge embedded in software. You evaluate a STATE against typed questions and answer with STRICT JSON only — no prose, no markdown fences, no explanations. Your probabilities must be honest self-assessments summing to ~1 per question; confidence ∈ [0,1] says how sure you are. Answer every question key exactly as specified.`

// buildLLMUserPrompt renders the state plus a deterministic (key-sorted)
// question specification with the exact JSON response shape demanded.
func buildLLMUserPrompt(req gen.PolicyDecideReq) string {
	var b strings.Builder
	b.WriteString("STATE:\n")
	b.WriteString(req.State)
	b.WriteString("\n\nQUESTIONS (answer every key):\n")

	keys := make([]string, 0, len(req.Questions))
	for k := range req.Questions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		q := req.Questions[k]
		fmt.Fprintf(&b, "- key %q (%s): %s\n", k, q.Type, q.Instructions)
		switch q.Type {
		case "choice":
			optKeys := sortedKeys(q.Choices)
			for i, ok := range optKeys {
				comma := ""
				if i > 0 {
					comma = ", "
				}
				fmt.Fprintf(&b, "    option %q: %s%s\n", ok, q.Choices[ok], comma)
			}
		case "score":
			for i, lvl := range q.Levels {
				fmt.Fprintf(&b, "    level %d: %s\n", i, lvl)
			}
		}
	}

	b.WriteString("\nRespond with ONLY this JSON object shape:\n{\"answers\":{")
	for i, k := range keys {
		q := req.Questions[k]
		if i > 0 {
			b.WriteString(",")
		}
		switch q.Type {
		case "choice":
			fmt.Fprintf(&b, "%q:{\"choice\":\"<one option>\",\"probabilities\":{\"<option>\":<0..1>,...},\"confidence\":<0..1>}", k)
		case "score":
			fmt.Fprintf(&b, "%q:{\"score\":<0..%d>,\"probabilities\":{\"0\":<0..1>,...},\"confidence\":<0..1>}", k, len(q.Levels)-1)
		case "noul":
			fmt.Fprintf(&b, "%q:{\"noul\":<0..1>}", k)
		}
	}
	b.WriteString("}}\n")
	return b.String()
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// LLM output parsing + validation
// ---------------------------------------------------------------------------

type llmAnswerRaw struct {
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score"`
	Noul          *float64           `json:"noul"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type llmRespRaw struct {
	Answers map[string]llmAnswerRaw `json:"answers"`
}

// parseLLMAnswers strips fences, decodes the strict-JSON payload and validates
// every answer against its question (choice ∈ options, score in range, noul in
// [0,1]). Any violation is an error — half-trusted output is not usable.
func parseLLMAnswers(text string, questions map[string]gen.PolicyQuestion) (map[string]gen.PolicyAnswer, error) {
	payload := stripCodeFences(text)
	start := strings.IndexByte(payload, '{')
	end := strings.LastIndexByte(payload, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in output: %.120s", text)
	}
	payload = payload[start : end+1]

	var raw llmRespRaw
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}

	answers := make(map[string]gen.PolicyAnswer, len(questions))
	for key, q := range questions {
		a, ok := raw.Answers[key]
		if !ok {
			return nil, fmt.Errorf("answer missing for question %q", key)
		}
		pa := gen.PolicyAnswer{
			Type:          q.Type,
			Probabilities: a.Probabilities,
			Confidence:    clamp01(a.Confidence),
			Backend:       backendLLM,
			Calibrated:    false,
		}
		switch q.Type {
		case "choice":
			if _, ok := q.Choices[a.Choice]; !ok {
				return nil, fmt.Errorf("question %q: choice %q not in options", key, a.Choice)
			}
			pa.Choice = a.Choice
		case "score":
			if a.Score == nil {
				return nil, fmt.Errorf("question %q: score missing", key)
			}
			pa.Score = clampRange(*a.Score, 0, float64(len(q.Levels)-1))
		case "noul":
			if a.Noul == nil {
				return nil, fmt.Errorf("question %q: noul missing", key)
			}
			pa.Noul = clamp01(*a.Noul)
		}
		answers[key] = pa
	}
	return answers, nil
}

// stripCodeFences removes ```json / ``` wrappers some models add despite
// instructions.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// decodeAggregatorChunk decodes one dispatch stream value.
func decodeAggregatorChunk(value any) (domain.AggregatorChunk, error) {
	switch cv := value.(type) {
	case domain.AggregatorChunk:
		return cv, nil
	case []byte:
		var chunk domain.AggregatorChunk
		if err := json.Unmarshal(cv, &chunk); err != nil {
			return domain.AggregatorChunk{}, fmt.Errorf("recv: %w", err)
		}
		return chunk, nil
	default:
		return domain.AggregatorChunk{}, fmt.Errorf("recv: unexpected chunk type %T", value)
	}
}
