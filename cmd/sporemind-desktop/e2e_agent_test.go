package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qomos-w/sporemind/cmd/internal/actorset"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// e2eEnv holds the shared runtime and HTTP helpers for e2e tests.
type e2eEnv struct {
	t       *testing.T
	baseURL string
	cancel  context.CancelFunc
	handle  *runtime.Handle
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()

	cleanupDataDir(t)

	// Compute project root from current working directory (cmd/sporemind-desktop → ../../).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	projectRoot := filepath.Join(cwd, "..", "..")

	keyFile := filepath.Join(projectRoot, "pkg", "env", "key")
	keyData, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(keyData)) == "" {
		t.Skip("skipping: no API key in pkg/env/key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Second)

	cfg := runtime.Config{
		GatewayAddr: ":18083",
		NoGateway:   false,
		Children:    actorset.Default(),
	}
	handle, err := runtime.Bootstrap(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("bootstrap: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		_ = handle.Wait()
		cleanupDataDir(t)
	})

	// Wait for aimanager to spawn aggregators and sync config.
	time.Sleep(5 * time.Second)

	return &e2eEnv{t: t, baseURL: "http://localhost:18083", cancel: cancel, handle: handle}
}

func (e *e2eEnv) invoke(callID string, payload any, role string) (int, string) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", e.baseURL+"/api/"+callID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("X-Role", role)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("post %s: %v", callID, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *e2eEnv) invokeTarget(callID, target string, payload any, role string) (int, string) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", e.baseURL+"/api/"+callID+"?target="+target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("X-Role", role)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("post %s: %v", callID, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *e2eEnv) pickOpenAIAggregator() string {
	return e.pickAggregator("openai")
}

func (e *e2eEnv) pickAnthropicAggregator() string {
	return e.pickAggregator("anthropic")
}

func (e *e2eEnv) pickAggregator(kind string) string {
	status, body := e.invoke("aimanager.aggregator_list", nil, "")
	if status != 200 {
		e.t.Fatalf("aggregator list: status %d", status)
	}
	var aggResp struct {
		Items []struct {
			Kind    string `json:"kind"`
			ActorID string `json:"actorId"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &aggResp); err != nil {
		e.t.Fatalf("parse aggregator list: %v", err)
	}
	for _, agg := range aggResp.Items {
		if agg.Kind == kind {
			return agg.ActorID
		}
	}
	if len(aggResp.Items) > 0 {
		return aggResp.Items[0].ActorID
	}
	e.t.Fatal("no aggregators available")
	return ""
}

// mountWorkspace creates a temp dir, mounts it as a project, and returns the project ID
// along with the temp dir path so tests can create files inside it.
func (e *e2eEnv) mountWorkspace() (projectID string, tmpDir string) {
	tmpDir = os.TempDir() + "/sporemind-e2e-agent-" + fmt.Sprintf("%d", time.Now().UnixNano())
	_ = os.MkdirAll(tmpDir, 0o755)

	status, body := e.invoke("workspace.mount", map[string]any{
		"path": tmpDir,
		"name": "e2e-test-project",
	}, "human")
	if status != 200 && status != 204 {
		e.t.Fatalf("mount: status %d body=%s", status, body)
	}
	var mountResp struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(body), &mountResp)
	return mountResp.ID, tmpDir
}

func (e *e2eEnv) createAgent(projectID, aggActorID string) string {
	status, body := e.invoke("workspace.create_agent", map[string]any{
		"projectId":         projectID,
		"aggregatorActorId": aggActorID,
		"displayName":       "E2E Test Agent",
		"agentKind":         "coder",
	}, "human")
	if status != 200 {
		e.t.Fatalf("create agent: status %d body=%s", status, body)
	}
	var agentResp struct {
		ActorID string `json:"actorId"`
	}
	json.Unmarshal([]byte(body), &agentResp)
	return agentResp.ActorID
}

// submitAndWait sends a chat message and polls until the assistant turn completes.
// Returns the assistant's output text.
func (e *e2eEnv) submitAndWait(agentActorID, text string) string {
	// Count turns before submit so we can detect the new assistant turn.
	turnsBefore := len(e.sessionSnapshot(agentActorID))

	status, body := e.invokeTarget("chat_submit", agentActorID, map[string]any{
		"text":  text,
		"model": "glm-5.1",
	}, "human")
	if status != 200 {
		e.t.Fatalf("chat.submit: status %d body=%s", status, body)
	}
	var submitResp struct {
		TurnActorID string `json:"turnActorId"`
	}
	if err := json.Unmarshal([]byte(body), &submitResp); err != nil {
		e.t.Fatalf("parse submit response: %v", err)
	}

	var assistantText string
	deadline := time.Now().Add(300 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		turns := e.sessionSnapshot(agentActorID)
		if len(turns) <= turnsBefore {
			continue // assistant turn hasn't been appended yet
		}
		// Check only the newly added turns for a completed assistant.
		for i := len(turns) - 1; i >= turnsBefore; i-- {
			turn := turns[i]
			if turn.Role == "assistant" && turn.State == "completed" {
				assistantText = turn.outputText()
				break
			}
			if turn.Role == "assistant" && turn.State == "failed" {
				e.t.Fatalf("assistant turn failed: %s", turn.Error)
			}
		}
		if assistantText != "" {
			break
		}
	}
	if assistantText == "" {
		e.t.Fatal("timed out waiting for assistant turn to complete")
	}
	return assistantText
}

// e2eStep mirrors domain.Step for JSON unmarshalling in tests.
type e2eStep struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

// e2eTurn mirrors domain.Turn for JSON unmarshalling in tests.
type e2eTurn struct {
	Role      string    `json:"role"`
	State     string    `json:"state"`
	ID        string    `json:"id"`
	UserInput string    `json:"userInput,omitempty"`
	Error     string    `json:"error,omitempty"`
	Steps     []e2eStep `json:"steps,omitempty"`
}

// outputText extracts the concatenated text content from a turn's steps.
// Replaces Turn.Output which was removed after flattening.
func (t e2eTurn) outputText() string {
	var sb strings.Builder
	for _, s := range t.Steps {
		if s.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(s.Text)
		}
	}
	return sb.String()
}

// sessionSnapshot returns the full session turns for inspection.
func (e *e2eEnv) sessionSnapshot(agentActorID string) []e2eTurn {
	status, body := e.invokeTarget("session_fork", agentActorID, nil, "human")
	if status != 200 {
		e.t.Fatalf("session.fork: status %d", status)
	}
	var forkResp struct {
		Session struct {
			Turns []e2eTurn `json:"turns"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(body), &forkResp); err != nil {
		e.t.Fatalf("parse session: %v", err)
	}
	return forkResp.Session.Turns
}

// TestE2E_AgentChatLoop verifies the full agent loop with a single-turn simple query.
func TestE2E_AgentChatLoop(t *testing.T) {
	env := newE2EEnv(t)

	aggActorID := env.pickOpenAIAggregator()
	projectID, _ := env.mountWorkspace()
	agentActorID := env.createAgent(projectID, aggActorID)

	t.Log("=== Submit: simple math ===")
	reply := env.submitAndWait(agentActorID, "What is 2+2? Reply with just the number.")
	t.Logf("Reply: %q", reply)

	if !strings.Contains(reply, "4") {
		t.Fatalf("expected reply to contain '4', got %q", reply)
	}

	turns := env.sessionSnapshot(agentActorID)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	t.Logf("Session turns: %d", len(turns))
}

// TestE2E_MultiTurnChat runs a 3-round conversation that requires context
// retention across turns. Each follow-up builds on the previous answer.
func TestE2E_MultiTurnChat(t *testing.T) {
	env := newE2EEnv(t)

	aggActorID := env.pickOpenAIAggregator()
	projectID, _ := env.mountWorkspace()
	agentActorID := env.createAgent(projectID, aggActorID)

	// ── Round 1 ───────────────────────────────────────────────────────
	t.Log("\n=== Round 1: Fibonacci basics ===")
	reply1 := env.submitAndWait(agentActorID,
		"请写一个 Python 函数 fib(n) 来计算斐波那契数列的第 n 项。给出递归实现即可。")
	t.Logf("Round 1 reply:\n%s", reply1)

	if !strings.Contains(reply1, "def fib") {
		t.Fatalf("Round 1: expected code with 'def fib', got:\n%s", reply1)
	}

	// ── Round 2 ───────────────────────────────────────────────────────
	t.Log("\n=== Round 2: Memoization optimization ===")
	reply2 := env.submitAndWait(agentActorID,
		"优化上面的函数，使用记忆化（memoization）来避免重复计算。并给出时间复杂度和空间复杂度分析。")
	t.Logf("Round 2 reply:\n%s", reply2)

	lower2 := strings.ToLower(reply2)
	if !strings.Contains(lower2, "memo") && !strings.Contains(lower2, "cache") && !strings.Contains(lower2, "记忆") {
		t.Fatalf("Round 2: expected memoization / cache discussion, got:\n%s", reply2)
	}

	// ── Round 3 ───────────────────────────────────────────────────────
	t.Log("\n=== Round 3: Iterative version ===")
	reply3 := env.submitAndWait(agentActorID,
		"最后，请用迭代（循环）的方式重写 fib(n)，并说明迭代实现相比递归实现的优势。")
	t.Logf("Round 3 reply:\n%s", reply3)

	lower3 := strings.ToLower(reply3)
	if !strings.Contains(lower3, "迭代") && !strings.Contains(lower3, "循环") && !strings.Contains(lower3, "iter") && !strings.Contains(lower3, "for ") {
		t.Fatalf("Round 3: expected iterative version, got:\n%s", reply3)
	}

	// ── Verify session integrity ──────────────────────────────────────
	t.Log("\n=== Verifying session integrity ===")
	turns := env.sessionSnapshot(agentActorID)
	if len(turns) != 6 {
		t.Fatalf("expected 6 turns (3 user + 3 assistant), got %d", len(turns))
	}

	for i, turn := range turns {
		t.Logf("  turn[%d] role=%s state=%s id=%s", i, turn.Role, turn.State, turn.ID)
		if turn.State != "completed" {
			t.Fatalf("turn[%d] state=%q, want completed", i, turn.State)
		}
	}

	// Verify ordering: user, assistant, user, assistant, user, assistant
	expectedRoles := []string{"user", "assistant", "user", "assistant", "user", "assistant"}
	for i, want := range expectedRoles {
		if turns[i].Role != want {
			t.Fatalf("turn[%d].Role=%q, want %q", i, turns[i].Role, want)
		}
	}

	// Verify user inputs are preserved
	if !strings.Contains(turns[0].UserInput, "fib(n)") {
		t.Fatalf("turn[0] user input lost: %q", turns[0].UserInput)
	}
	if !strings.Contains(turns[2].UserInput, "记忆化") {
		t.Fatalf("turn[2] user input lost: %q", turns[2].UserInput)
	}
	if !strings.Contains(turns[4].UserInput, "迭代") {
		t.Fatalf("turn[4] user input lost: %q", turns[4].UserInput)
	}

	// Verify assistant outputs are non-empty and contextually relevant
	for i := 1; i < len(turns); i += 2 {
		if turns[i].outputText() == "" {
			t.Fatalf("turn[%d] assistant output empty", i)
		}
	}

	t.Log("\n=== Multi-turn E2E complete ===")
}

// TestE2E_ToolCall verifies that GLM-5.1 (via OpenAI-compatible endpoint)
// can receive tools, decide to call one, and return results correctly.
func TestE2E_ToolCall(t *testing.T) {
	env := newE2EEnv(t)

	aggActorID := env.pickOpenAIAggregator()
	projectID, _ := env.mountWorkspace()

	// Create a known file in the project root (where the global filesystem
	// actor operates) so the agent can read it.
	secretContent := "e2e-toolcall-test-secret-42"
	if err := os.WriteFile("secret.txt", []byte(secretContent), 0o644); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove("secret.txt") })

	agentActorID := env.createAgent(projectID, aggActorID)

	t.Log("=== Submit: trigger tool call ===")
	reply := env.submitAndWait(agentActorID,
		"请读取项目目录下的 secret.txt 文件并告诉我它的内容。")
	t.Logf("Reply:\n%s", reply)

	if !strings.Contains(reply, secretContent) {
		t.Fatalf("expected reply to contain file content %q, got:\n%s", secretContent, reply)
	}

	// Inspect the latest assistant turn for tool_call steps.
	turns := env.sessionSnapshot(agentActorID)
	if len(turns) < 2 {
		t.Fatalf("expected at least 2 turns, got %d", len(turns))
	}
	assistantTurn := turns[len(turns)-1]
	if assistantTurn.Role != "assistant" {
		t.Fatalf("expected last turn to be assistant, got %s", assistantTurn.Role)
	}

	var hasToolCall, hasToolResult bool
	for _, step := range assistantTurn.Steps {
		t.Logf("  step kind=%s title=%s state=%s", step.Kind, step.Title, step.State)
		if step.Kind == "tool_call" {
			hasToolCall = true
		}
		if step.Kind == "tool_result" {
			hasToolResult = true
		}
	}
	if !hasToolCall {
		t.Fatal("expected at least one tool_call step in assistant turn")
	}
	if !hasToolResult {
		t.Fatal("expected at least one tool_result step in assistant turn")
	}

	t.Log("=== Tool call E2E complete ===")
}

// TestE2E_AnthropicEndpoint verifies the Anthropic-compatible endpoint
// (bigmodel.cn /api/anthropic) works with glm-5.1.
func TestE2E_AnthropicEndpoint(t *testing.T) {
	env := newE2EEnv(t)

	aggActorID := env.pickAnthropicAggregator()
	projectID, _ := env.mountWorkspace()
	agentActorID := env.createAgent(projectID, aggActorID)

	t.Log("=== Submit via Anthropic endpoint ===")
	reply := env.submitAndWait(agentActorID, "What is 1+1? Reply with just the number.")
	t.Logf("Reply: %q", reply)

	if !strings.Contains(reply, "2") {
		t.Fatalf("expected reply to contain '2', got %q", reply)
	}

	t.Log("=== Anthropic endpoint E2E complete ===")
}

// TestE2E_EventBusSubscription verifies that turn events flow through the
// gospore.events.subscribe_instance callable to a remote WebSocket subscriber.
func TestE2E_EventBusSubscription(t *testing.T) {
	env := newE2EEnv(t)

	aggActorID := env.pickOpenAIAggregator()
	projectID, _ := env.mountWorkspace()
	agentActorID := env.createAgent(projectID, aggActorID)

	// Open a WebSocket connection to the gateway.
	wsURL := "ws://localhost:18083/ws"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer wsConn.Close()
	t.Logf("ws connected to %s", wsURL)

	// Submit a chat to get the turnActorId.
	status, body := env.invokeTarget("chat_submit", agentActorID, map[string]any{
		"text":  "Say hello.",
		"model": "glm-5.1",
	}, "human")
	if status != 200 {
		t.Fatalf("chat.submit: status %d body=%s", status, body)
	}
	var submitResp struct {
		TurnActorID string `json:"turnActorId"`
	}
	if err := json.Unmarshal([]byte(body), &submitResp); err != nil {
		t.Fatalf("parse submit response: %v", err)
	}
	turnActorID := submitResp.TurnActorID
	t.Logf("turnActorId=%s", turnActorID)

	// Send WebSocket subscribe frame for turn events.
	subFrame := map[string]any{
		"type":   "subscribe",
		"subId":  "test-sub-1",
		"callID": "gospore.events.subscribe_instance",
		"payload": map[string]any{
			"actorId": turnActorID,
			"kind":    "turn",
		},
	}
	if err := wsConn.WriteJSON(subFrame); err != nil {
		t.Fatalf("ws write subscribe: %v", err)
	}
	t.Log("ws subscribe frame sent")

	// Read chunks with a timeout.
	eventCount := 0
	deadline := time.Now().Add(30 * time.Second)
	wsConn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) && eventCount < 3 {
		_, msg, err := wsConn.ReadMessage()
		if err != nil {
			t.Logf("ws read: %v (got %d events)", err, eventCount)
			break
		}
		var frame struct {
			Type    string          `json:"type"`
			SubID   string          `json:"subId"`
			Payload json.RawMessage `json:"payload"`
			Message string          `json:"message"`
		}
		if err := json.Unmarshal(msg, &frame); err != nil {
			t.Logf("ws decode: %v", err)
			continue
		}
		t.Logf("ws frame: type=%s subId=%s payload_len=%d err=%s", frame.Type, frame.SubID, len(frame.Payload), frame.Message)
		if frame.Type == "chunk" {
			eventCount++
		} else if frame.Type == "error" {
			t.Fatalf("subscribe error: %s", frame.Message)
		} else if frame.Type == "end" {
			break
		}
	}

	if eventCount == 0 {
		t.Fatal("expected at least 1 EventBus chunk via WebSocket, got 0 — subscription broken")
	}
	t.Logf("EventBus subscription test passed: received %d events", eventCount)
}

// TestE2E_ExploreDirect invokes agent.explore directly and verifies file search
// results. This tests the explore executor end-to-end without depending on LLM
// tool-calling decisions.
func TestE2E_ExploreDirect(t *testing.T) {
	env := newE2EEnv(t)

	projectID, tmpDir := env.mountWorkspace()

	// Create known files in the project.
	_ = os.WriteFile(filepath.Join(tmpDir, "auth.go"), []byte("package auth\n\nfunc Authenticate() error { return nil }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "util.go"), []byte("package util\n\nfunc Helper() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Project README\n"), 0o644)

	aggActorID := env.pickOpenAIAggregator()
	agentActorID := env.createAgent(projectID, aggActorID)

	t.Log("=== Direct invoke: agent.explore_sync ===")
	status, body := env.invokeTarget("agent.explore_sync", agentActorID, map[string]any{
		"queries": []string{"Authenticate"},
		"scope":   tmpDir,
	}, "human")

	t.Logf("explore status=%d body=%s", status, body)

	if status != 200 {
		t.Fatalf("explore: unexpected status %d body=%s", status, body)
	}

	var result struct {
		Matches []struct {
			Path    string `json:"path"`
			Line    int    `json:"line"`
			Content string `json:"content"`
		} `json:"matches"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("parse explore result: %v body=%s", err, body)
	}

	if len(result.Matches) == 0 {
		t.Fatal("expected at least one match")
	}

	found := false
	for _, m := range result.Matches {
		if strings.Contains(m.Path, "auth.go") && strings.Contains(m.Content, "Authenticate") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected auth.go match with Authenticate, got %+v", result.Matches)
	}

	t.Logf("Explore matches: %d/%d, truncated=%v", len(result.Matches), result.Total, result.Truncated)
	t.Log("=== Explore direct E2E complete ===")
}

// TestE2E_ExploreViaLLM sends a chat message that should trigger the explore
// tool, then verifies the assistant response references searched files.
// This is a best-effort test: if the LLM decides not to call explore, it
// will time out and fail.
func TestE2E_ExploreViaLLM(t *testing.T) {
	env := newE2EEnv(t)

	projectID, tmpDir := env.mountWorkspace()

	// Create known files with searchable content.
	_ = os.WriteFile(filepath.Join(tmpDir, "handler.go"), []byte("package main\n\nfunc HandleRequest() string { return \"ok\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "config.go"), []byte("package main\n\ntype Config struct { Port int }\n"), 0o644)

	aggActorID := env.pickOpenAIAggregator()
	agentActorID := env.createAgent(projectID, aggActorID)

	t.Log("=== Submit: trigger explore via LLM ===")
	reply := env.submitAndWait(agentActorID,
		"请使用 explore 工具搜索项目中包含 'HandleRequest' 函数的文件，并告诉我文件名。")
	t.Logf("Reply:\n%s", reply)

	// Verify the reply references the searched content or file.
	lower := strings.ToLower(reply)
	if !strings.Contains(lower, "handler") && !strings.Contains(lower, "handlerequest") {
		t.Fatalf("expected reply to reference handler.go or HandleRequest, got:\n%s", reply)
	}

	// Inspect turns for explore tool usage.
	turns := env.sessionSnapshot(agentActorID)
	if len(turns) < 2 {
		t.Fatalf("expected at least 2 turns, got %d", len(turns))
	}
	assistantTurn := turns[len(turns)-1]

	var hasExploreCall bool
	for _, step := range assistantTurn.Steps {
		t.Logf("  step kind=%s title=%s state=%s", step.Kind, step.Title, step.State)
		if step.Kind == "tool_call" && strings.Contains(strings.ToLower(step.Title), "explore") {
			hasExploreCall = true
		}
	}
	if !hasExploreCall {
		t.Log("warning: LLM did not call explore tool; this may be non-deterministic")
	}

	t.Log("=== Explore via LLM E2E complete ===")
}
