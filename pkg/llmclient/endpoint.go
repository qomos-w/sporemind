package llmclient

import (
	"context"
	"io"
	"net/http"
	"strings"
)

// EndpointClient is a minimal, provider-kind-agnostic client for calling
// any OpenAI Chat-Completions-compatible endpoint.
//
// Unlike OpenAIClient, it accepts a full endpoint URL (not just a base URL)
// and does not append a hardcoded path suffix. This allows pointing directly
// at any external service that speaks the OpenAI streaming SSE protocol.
type EndpointClient struct {
	Endpoint   string // full URL, e.g. "https://api.example.com/v1/chat/completions"
	APIKey     string
	HTTPClient *http.Client
}

// NewEndpointClient constructs a client pointing at the given full endpoint URL.
func NewEndpointClient(endpoint, apiKey string) *EndpointClient {
	return &EndpointClient{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}
}

// SetHTTPClient replaces the default HTTP client (llmclient.HTTPClientCarrier).
func (c *EndpointClient) SetHTTPClient(hc *http.Client) { c.HTTPClient = hc }

// Stream implements Client. It POSTs an OpenAI-compatible chat-completions
// request to the configured endpoint and returns an SSE event stream.
func (c *EndpointClient) Stream(ctx context.Context, req Request) (Stream, error) {
	payload, err := lowerOpenAIChat(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	ua := req.UserAgent
	if ua == "" {
		ua = userAgent
	}
	httpReq.Header.Set("User-Agent", ua)
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = httpClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, UpstreamErrorFromResponse(resp, body)
	}

	s := &endpointStream{
		body:   resp.Body,
		events: make(chan Event, 16),
		model:  req.Model,
		reqID:  resp.Header.Get("X-Request-Id"),
		respID: resp.Header.Get("X-Response-Id"),
	}
	go s.run()
	return s, nil
}

// endpointStream reuses the OpenAI SSE parser — the protocol is identical.
type endpointStream struct {
	body   io.ReadCloser
	events chan Event
	inner  *openaiStream
	model  string
	reqID  string
	respID string
}

func (s *endpointStream) Events() <-chan Event { return s.events }
func (s *endpointStream) Close() error         { return s.body.Close() }
func (s *endpointStream) Telemetry() RequestTelemetry {
	if s.inner != nil {
		return s.inner.Telemetry()
	}
	return RequestTelemetry{}
}

func (s *endpointStream) run() {
	// openaiStream.run() already closes events and body; do not double-close.
	wrapped := &openaiStream{body: s.body, events: s.events, toolCallIDs: make(map[int]string)}
	wrapped.telemetry.setStart()
	wrapped.telemetry.setIDs("", s.model, "", s.reqID, s.respID, "")
	s.inner = wrapped
	wrapped.run()
}

// ---------------------------------------------------------------------------
// Convenience helpers for one-off calls
// ---------------------------------------------------------------------------

// CallEndpoint streams a request to an arbitrary OpenAI-compatible endpoint.
// No provider registration or actor setup is required.
func CallEndpoint(ctx context.Context, endpoint, apiKey, model, systemPrompt, userText string) (Stream, error) {
	c := NewEndpointClient(endpoint, apiKey)
	return c.Stream(ctx, Request{
		Model:    model,
		System:   systemPrompt,
		UserText: userText,
	})
}

// CallEndpointText makes a blocking call and returns the assembled text.
// It is the minimal surface for ad-hoc external LLM calls.
func CallEndpointText(ctx context.Context, endpoint, apiKey, model, systemPrompt, userText string) (string, error) {
	stream, err := CallEndpoint(ctx, endpoint, apiKey, model, systemPrompt, userText)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var sb strings.Builder
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			sb.WriteString(ev.Text)
		case EventError:
			return sb.String(), ev.Err
		}
	}
	return sb.String(), nil
}
