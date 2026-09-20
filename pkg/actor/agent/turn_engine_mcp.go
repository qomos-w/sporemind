package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// parseMCPToolCallable splits the synthetic MCP callable ID
// ("mcp.<serverID>.<toolName>") into its server and tool parts. Only the
// first two segments are structural — server IDs are srv-N (no dots) while
// tool names may contain dots, so the remainder is joined back. Real
// mcpmanager callables (e.g. "mcp.call_tool", two segments) never match.
func parseMCPToolCallable(callableID string) (serverID, toolName string, ok bool) {
	parts := strings.Split(callableID, ".")
	if len(parts) < 3 || parts[0] != "mcp" {
		return "", "", false
	}
	return parts[1], strings.Join(parts[2:], "."), true
}

// runMCPToolCall routes an MCP tool invocation to mcpmanager.call_tool and
// converts the McpCallToolResp content into the tool frame text. The LLM's
// arguments (the MCP tool's inputSchema shape) are wrapped into the
// McpCallToolReq wire request; the response text is backfilled into the tool
// frame by the caller via applySingleToolResult.
func (e *turnEngine) runMCPToolCall(ctx actor.Context, planner actor.Planner, svcRefs map[string]ref.Ref, call pendingToolCall, serverID, toolName string) toolExecutionResult {
	svcRef, ok := svcRefs["mcp"]
	if !ok {
		return toolExecutionResult{call: call, out: fmt.Sprintf("mcp service not available for %q", call.LLMName), isErr: true}
	}
	var args map[string]any
	if trimmed := strings.TrimSpace(call.Input); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return toolExecutionResult{call: call, out: fmt.Sprintf("MCP tool %q: invalid arguments JSON: %v", call.LLMName, err), isErr: true}
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	payload, err := json.Marshal(domain.McpCallToolReq{ID: serverID, Tool: toolName, Arguments: args})
	if err != nil {
		return toolExecutionResult{call: call, out: fmt.Sprintf("MCP tool %q: marshal request: %v", call.LLMName, err), isErr: true}
	}

	out, isErr, raw := callTool(ctx.Lifecycle(), planner, svcRef, "mcp.call_tool", string(payload))
	var observation *domain.ChatMessage
	if resp, ok := decodeMcpCallToolResp(raw); ok && !isErr {
		// Image content blocks are embedded as a follow-up user-role
		// observation (same path as user chat images / screenshots), so
		// vision primaries see the image directly and text-only primaries
		// get it through the one-shot recognition pass.
		attachedIdx := -1
		if !resp.IsError {
			attachedIdx, observation = mcpImageObservation(resp)
		}
		if text := mcpRespTextAttached(resp, attachedIdx); text != "" {
			out = text
		}
		isErr = resp.IsError
	}
	if !isErr && e.onToolExecuted != nil {
		e.onToolExecuted(ctx, call.CallableID, call.Input)
	}
	return toolExecutionResult{call: call, out: out, isErr: isErr, observation: observation, observationMeta: "mcp_image"}
}

// decodeMcpCallToolResp extracts a typed McpCallToolResp from a raw callable
// result ([]byte / typed / map), mirroring the manager's decoder.
func decodeMcpCallToolResp(v any) (domain.McpCallToolResp, bool) {
	switch x := v.(type) {
	case domain.McpCallToolResp:
		return x, true
	case *domain.McpCallToolResp:
		if x != nil {
			return *x, true
		}
		return domain.McpCallToolResp{}, false
	case []byte:
		if len(x) == 0 {
			return domain.McpCallToolResp{}, false
		}
		var r domain.McpCallToolResp
		if err := json.Unmarshal(x, &r); err != nil {
			return domain.McpCallToolResp{}, false
		}
		return r, true
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.McpCallToolResp{}, false
		}
		var r domain.McpCallToolResp
		if err := json.Unmarshal(body, &r); err != nil {
			return domain.McpCallToolResp{}, false
		}
		return r, true
	}
}

// mcpRespText joins an MCP call result's content blocks into one tool-frame
// text. Text blocks (including the diagnostic placeholders the child produces
// for audio/resource blocks) are joined directly. Image blocks carry their
// base64 payload in Data/MimeType, which the text frame cannot render, so they
// are reduced to a compact marker so the LLM stays aware of the image without
// megabytes of base64 flooding the context window.
func mcpRespText(resp domain.McpCallToolResp) string {
	return mcpRespTextAttached(resp, -1)
}

// mcpRespTextAttached is mcpRespText with an attached image block index: when
// attached >= 0, that image block's marker additionally says the image is
// delivered in the follow-up observation message (see mcpImageObservation).
func mcpRespTextAttached(resp domain.McpCallToolResp, attached int) string {
	var b strings.Builder
	for i, c := range resp.Content {
		if c.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
			continue
		}
		if c.Type == "image" && c.Data != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			mime := c.MimeType
			if mime == "" {
				mime = "unknown"
			}
			if i == attached {
				fmt.Fprintf(&b, "[image content block: type=%s, %d base64 chars, attached in the following message]", mime, len(c.Data))
			} else {
				fmt.Fprintf(&b, "[image content block: type=%s, %d base64 chars]", mime, len(c.Data))
			}
		}
	}
	return b.String()
}

// mcpImageObservationMaxBytes caps the decoded size of an MCP tool-result
// image eligible for observation embedding. Larger images keep the compact
// marker only: MCP servers may return diagnostic images of arbitrary size and
// the wire request grows with the payload.
const mcpImageObservationMaxBytes = 4 << 20

// mcpImageObservationDisabled reports an explicit opt-out of the image
// observation embedding via environment (SPOREMIND_MCP_IMAGE_OBSERVATION=0).
func mcpImageObservationDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SPOREMIND_MCP_IMAGE_OBSERVATION"))) {
	case "0", "false", "off":
		return true
	}
	return false
}

// mcpImageObservation converts the first eligible image content block of an
// MCP tool result into a user-role observation message carrying the image as
// a data-URL ContentBlockImage — the exact same embedding shape as
// user-submitted chat images and screenshot observations, so vision primaries
// see it via the image_url path while text-only primaries get it through the
// one-shot recognition pass. Gates: first image block only, decodable base64,
// decoded size under mcpImageObservationMaxBytes, env opt-out. Returns the
// attached block index (-1 with a nil message when no block qualifies).
func mcpImageObservation(resp domain.McpCallToolResp) (int, *domain.ChatMessage) {
	if mcpImageObservationDisabled() {
		return -1, nil
	}
	for i, c := range resp.Content {
		if c.Type != "image" || c.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(c.Data)
		if err != nil || len(data) == 0 || len(data) > mcpImageObservationMaxBytes {
			return -1, nil
		}
		mime := c.MimeType
		if mime == "" {
			mime = "image/png"
		}
		obs := &domain.ChatMessage{
			Role: domain.ChatRoleUser,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: fmt.Sprintf("[mcp tool image: %s, %d bytes]", mime, len(data))},
				{Type: domain.ContentBlockImage, ImageURL: "data:" + mime + ";base64," + c.Data, MimeType: mime},
			},
		}
		return i, obs
	}
	return -1, nil
}
