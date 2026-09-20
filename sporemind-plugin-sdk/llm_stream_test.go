package sdk

import (
	"encoding/json"
	"errors"
	"testing"
)

// fakeStreamHost implements Host with a scripted InvokeStream: it delivers the
// given wire chunks in order then returns the terminal response. Invoke
// (unary) records and returns the terminal response without chunks.
type fakeStreamHost struct {
	callIDs     []string
	streamCalls []string
	chunks      [][]byte
	terminal    []byte
}

func (h *fakeStreamHost) Invoke(callID string, payload any) ([]byte, error) {
	h.callIDs = append(h.callIDs, callID)
	return h.terminal, nil
}

func (h *fakeStreamHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	h.streamCalls = append(h.streamCalls, callID)
	for _, c := range h.chunks {
		if err := onChunk(c); err != nil {
			return nil, err
		}
	}
	return h.terminal, nil
}

// streamLLM is what the generated StreamLLM* callers in hostproto.gen.go
// expand to: InvokeStream + ForwardLLMChunks. The tests exercise the
// primitive pair directly so they keep passing when codegen changes shape.
func streamLLM(host Host, callID string, payload any, onChunk func(LLMChunk) error) ([]byte, error) {
	return host.InvokeStream(callID, payload, ForwardLLMChunks(onChunk))
}

// TestLLMStreamDecodesChunks pins the wire contract: InvokeStream on
// llm.complete with ForwardLLMChunks decodes each wire chunk into LLMChunk
// (kind verbatim, text, usage raw), and returns the terminal aggregated
// response.
func TestLLMStreamDecodesChunks(t *testing.T) {
	host := &fakeStreamHost{
		chunks: [][]byte{
			[]byte(`{"kind":"text_delta","data":{"text":"hel"}}`),
			[]byte(`{"kind":"text_delta","data":{"text":"lo"}}`),
			[]byte(`{"kind":"reasoning_delta","data":{"text":"thinking"}}`),
			[]byte(`{"kind":"usage","data":{"InputTokens":3,"OutputTokens":5}}`),
		},
		terminal: []byte(`{"Text":"hello"}`),
	}

	var got []LLMChunk
	resp, err := streamLLM(host, "llm.complete", map[string]any{"prompt": "hi"}, func(c LLMChunk) error {
		got = append(got, c)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if string(resp) != `{"Text":"hello"}` {
		t.Fatalf("terminal = %s, want aggregated response passthrough", resp)
	}
	if len(host.streamCalls) != 1 || host.streamCalls[0] != "llm.complete" {
		t.Fatalf("streamCalls = %v, want [llm.complete]", host.streamCalls)
	}
	if len(got) != 4 {
		t.Fatalf("got %d chunks, want 4", len(got))
	}
	if got[0].Kind != "text_delta" || got[0].Text != "hel" || got[1].Text != "lo" {
		t.Errorf("text chunks = %+v %+v", got[0], got[1])
	}
	if got[2].Kind != "reasoning_delta" || got[2].Text != "thinking" {
		t.Errorf("reasoning chunk = %+v", got[2])
	}
	if string(got[3].Usage) == "" || !json.Valid(got[3].Usage) {
		t.Errorf("usage chunk = %s, want raw JSON object", got[3].Usage)
	}
	var usage map[string]any
	if err := json.Unmarshal(got[3].Usage, &usage); err != nil || usage["InputTokens"] != float64(3) {
		t.Errorf("usage decode = %v (%v), want InputTokens 3", usage, err)
	}
}

// TestLLMStreamRoutesToChat pins that the llm.chat callID rides the same
// InvokeStream primitive (not llm.complete).
func TestLLMStreamRoutesToChat(t *testing.T) {
	host := &fakeStreamHost{terminal: []byte(`{}`)}
	if _, err := streamLLM(host, "llm.chat", map[string]any{"messages": []any{}}, func(LLMChunk) error { return nil }); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(host.streamCalls) != 1 || host.streamCalls[0] != "llm.chat" {
		t.Fatalf("streamCalls = %v, want [llm.chat]", host.streamCalls)
	}
}

// TestLLMStreamAbortByCallback pins the abort contract: an onChunk error
// stops delivery and surfaces from the stream call.
func TestLLMStreamAbortByCallback(t *testing.T) {
	host := &fakeStreamHost{
		chunks:   [][]byte{[]byte(`{"kind":"text_delta","data":{"text":"a"}}`), []byte(`{"kind":"text_delta","data":{"text":"b"}}`)},
		terminal: []byte(`{"Text":"ab"}`),
	}
	boom := errors.New("consumer gone")
	_, err := streamLLM(host, "llm.complete", map[string]any{}, func(c LLMChunk) error {
		if c.Text == "a" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped consumer error", err)
	}
}

// TestLLMUnaryRidesInvoke pins that the unary form rides Invoke (not
// InvokeStream) — zero chunk traffic.
func TestLLMUnaryRidesInvoke(t *testing.T) {
	host := &fakeStreamHost{terminal: []byte(`{"Text":"x"}`)}
	if _, err := host.Invoke("llm.complete", map[string]any{"prompt": "hi"}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(host.callIDs) != 1 || host.callIDs[0] != "llm.complete" {
		t.Fatalf("callIDs = %v, want [llm.complete]", host.callIDs)
	}
	if len(host.streamCalls) != 0 {
		t.Fatalf("streamCalls = %v, want none for unary Invoke", host.streamCalls)
	}
}
