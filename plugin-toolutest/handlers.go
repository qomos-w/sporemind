// handlers.go — created by generator; agent-owned afterwards.
// New callables get stubs appended by regenerate; existing functions are never overwritten.
package main

import (
	"encoding/json"
	"fmt"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// llmResp mirrors the host llm.complete terminal, including the ToolCalls
// field the forced-tool-call flow returns. Declared locally (raw host.Invoke
// payload) so the handler compiles against any host generation.
type llmResp struct {
	Text      string          `json:"Text"`
	Reasoning string          `json:"Reasoning,omitempty"`
	Usage     json.RawMessage `json:"Usage,omitempty"`
	ToolCalls []struct {
		ID        string `json:"Id"`
		Name      string `json:"Name"`
		Arguments string `json:"Arguments"`
	} `json:"ToolCalls"`
}

// handleForcedTool drives the live forced-tool-call flow end to end: it
// declares one tool, forces the model to call it via tool_choice, and reports
// the returned call for the plugin to execute itself.
func handleForcedTool(req sdk.Request) (sdk.Response, error) {
	var payload ForcedToolRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, fmt.Errorf("decode request: %w", err)
	}
	if payload.Prompt == "" {
		return sdk.Response{}, fmt.Errorf("Prompt is required")
	}
	toolName := "extract_meta"
	if payload.Choice != nil && *payload.Choice != "" {
		toolName = *payload.Choice
	}
	mode := "name"
	if payload.Mode != nil && *payload.Mode != "" {
		mode = *payload.Mode
	}
	var toolChoice any
	switch mode {
	case "name":
		toolChoice = toolName
	case "required":
		toolChoice = "required"
	case "auto":
		toolChoice = "auto"
	default:
		return sdk.Response{}, fmt.Errorf("Mode must be name, required or auto")
	}

	hostReq := map[string]any{
		"Prompt": payload.Prompt,
		"Tools": []map[string]any{{
			"Name":        toolName,
			"Description": "Extract structured metadata from the user text",
			"InputSchema": json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"language":{"type":"string"}},"required":["summary","language"]}`),
		}},
		"ToolChoice": toolChoice,
	}
	if payload.Provider != nil && *payload.Provider != "" {
		hostReq["Provider"] = *payload.Provider
	}
	if payload.Model != nil && *payload.Model != "" {
		hostReq["Model"] = *payload.Model
	}

	raw, err := sdk.ActiveHost().Invoke("llm.complete", hostReq)
	if err != nil {
		return sdk.Response{}, fmt.Errorf("llm.complete: %w", err)
	}
	var resp llmResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return sdk.Response{}, fmt.Errorf("llm.complete: decode: %w", err)
	}

	out := ForcedToolResponse{
		ToolName:  fmt.Sprintf("%d calls", len(resp.ToolCalls)),
		Arguments: resp.Text,
		Raw:       string(raw),
	}
	if len(resp.ToolCalls) > 0 {
		call := resp.ToolCalls[0]
		out.ToolName = call.Name
		out.Arguments = call.Arguments
		out.StopInfo = fmt.Sprintf("id=%s", call.ID)
		sdk.Log(sdk.LogLevelInfo, "tooltest: forced tool call name=%s id=%s args=%.200s", call.Name, call.ID, call.Arguments)
	} else {
		sdk.Log(sdk.LogLevelWarn, "tooltest: no tool call returned, text=%.200s", resp.Text)
	}
	return sdk.Response{Payload: out}, nil
}
