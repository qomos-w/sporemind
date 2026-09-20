package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func readTestActor() *Actor {
	a := &Actor{}
	a.steps = []domain.Step{
		{ID: "u1", TurnID: "t1", Role: "user", Type: "text", Seq: 1, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "user question"}}},
		{ID: "r1", TurnID: "t1", Role: "assistant", Type: "reasoning", Seq: 2, Closed: true},
		{ID: "a1", TurnID: "t1", Role: "assistant", Type: "text", Seq: 3, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "assistant answer"}}},
		{ID: "tool1", TurnID: "t2", Role: "assistant", Type: "tool", Seq: 4, Closed: true,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "tu1", ToolName: "project.read", Input: `{"Path":"main.go"}`},
				{Type: domain.ContentBlockToolResult, ToolUseID: "tu1", Text: "file contents"},
			}},
		{ID: "d1", TurnID: "t2", Role: "assistant", Type: "text", Seq: 5, Closed: true, Discarded: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "discarded"}}},
		{ID: "open1", TurnID: "t2", Role: "assistant", Type: "text", Seq: 6, Closed: false,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "streaming"}}},
	}
	a.takeSnapshot()
	return a
}

func TestHandleMessageRead_FiltersAndRendersNewestFirst(t *testing.T) {
	a := readTestActor()

	resp, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{})
	if err != nil {
		t.Fatalf("handleMessageRead: %v", err)
	}
	wantSeqs := []int64{4, 3, 1}
	if len(resp.Items) != len(wantSeqs) {
		t.Fatalf("items = %d, want %d: %+v", len(resp.Items), len(wantSeqs), resp.Items)
	}
	for i, want := range wantSeqs {
		if resp.Items[i].Seq != want {
			t.Errorf("item[%d].Seq = %d, want %d", i, resp.Items[i].Seq, want)
		}
	}
	if got := resp.Items[0].Content; got != `project.read({"Path":"main.go"})` {
		t.Errorf("tool item content = %q, want tool call params only", got)
	}
	if strings.Contains(resp.Items[0].Content, "file contents") {
		t.Errorf("tool item must not include the tool result")
	}
	if resp.Items[1].Content != "assistant answer" || resp.Items[2].Content != "user question" {
		t.Errorf("unexpected text contents: %+v", resp.Items)
	}
	if resp.Items[1].Role != "assistant" || resp.Items[2].Role != "user" {
		t.Errorf("unexpected roles: %+v", resp.Items)
	}
	if resp.HasMore {
		t.Errorf("HasMore = true, want false")
	}
}

func TestHandleMessageRead_LimitAndBeforeSeqPaging(t *testing.T) {
	a := readTestActor()

	first, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].Seq != 4 || !first.HasMore {
		t.Fatalf("first page = %+v, want single seq=4 item with HasMore", first)
	}

	second, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{Limit: 1, BeforeSeq: 4})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Seq != 3 || !second.HasMore {
		t.Fatalf("second page = %+v, want single seq=3 item with HasMore", second)
	}

	third, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{BeforeSeq: 3})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if len(third.Items) != 1 || third.Items[0].Seq != 1 || third.HasMore {
		t.Fatalf("third page = %+v, want single seq=1 item without HasMore", third)
	}
}

func TestHandleMessageRead_TruncatesOversizedContent(t *testing.T) {
	a := &Actor{}
	a.steps = []domain.Step{{
		ID: "big", TurnID: "t1", Role: "assistant", Type: "text", Seq: 1, Closed: true,
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: strings.Repeat("x", messageReadItemMaxBytes+100)}},
	}}
	a.takeSnapshot()

	resp, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{})
	if err != nil {
		t.Fatalf("handleMessageRead: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if len(resp.Items[0].Content) != messageReadItemMaxBytes+len("...(truncated)") {
		t.Errorf("content length = %d, want %d", len(resp.Items[0].Content), messageReadItemMaxBytes+len("...(truncated)"))
	}
	if !strings.HasSuffix(resp.Items[0].Content, "...(truncated)") {
		t.Errorf("content missing truncation marker")
	}
}

// Regression: byte-boundary truncation of multibyte content produced invalid
// UTF-8 that the wire codec refuses to marshal, failing the whole callable.

func TestHandleMessageRead_TruncationStaysRuneAligned(t *testing.T) {
	a := &Actor{}
	a.steps = []domain.Step{{
		ID: "cjk", TurnID: "t1", Role: "assistant", Type: "text", Seq: 1, Closed: true,
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: strings.Repeat("好", messageReadItemMaxBytes/3+50)}},
	}}
	a.takeSnapshot()

	resp, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{})
	if err != nil {
		t.Fatalf("handleMessageRead: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	if !utf8.ValidString(resp.Items[0].Content) {
		t.Errorf("truncated content is not valid UTF-8: %q", resp.Items[0].Content[:64])
	}
	if !strings.HasSuffix(resp.Items[0].Content, "...(truncated)") {
		t.Errorf("content missing truncation marker")
	}
}

// Regression: stored step text can carry invalid UTF-8 from upstream tool
// output; the read response must sanitize it instead of failing codec marshal.

func TestHandleMessageRead_SanitizesInvalidUTF8(t *testing.T) {
	a := &Actor{}
	a.steps = []domain.Step{
		{ID: "bad", TurnID: "t1", Role: "assistant", Type: "text", Seq: 1, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "ok\x80\x81text"}}},
		{ID: "badtool", TurnID: "t1", Role: "assistant", Type: "tool", Seq: 2, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockToolUse, ToolName: "shell", Input: "{\"Cmd\":\"echo \xff\""}}},
	}
	a.takeSnapshot()

	resp, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{})
	if err != nil {
		t.Fatalf("handleMessageRead: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	for _, item := range resp.Items {
		if !utf8.ValidString(item.Content) {
			t.Errorf("item seq=%d content is not valid UTF-8: %q", item.Seq, item.Content)
		}
	}
	if !strings.Contains(resp.Items[0].Content, "shell(") {
		t.Errorf("tool item lost its call rendering: %q", resp.Items[0].Content)
	}
	if resp.Items[1].Content != "ok\ufffdtext" {
		t.Errorf("text item = %q, want invalid bytes replaced with U+FFFD", resp.Items[1].Content)
	}
}

func TestTruncateStatusField_RuneAlignedAndSanitized(t *testing.T) {
	long := strings.Repeat("好", statusStepTextLimit/3+50)
	got := truncateStatusField(long)
	if !utf8.ValidString(got) {
		t.Errorf("truncated status field is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("status field missing ellipsis")
	}
	if got := truncateStatusField("bad\x80bytes"); got != "bad\ufffdbytes" {
		t.Errorf("sanitize = %q, want invalid bytes replaced", got)
	}
}

func TestHandleMessageRead_DefaultAndMaxLimit(t *testing.T) {
	a := &Actor{}
	for i := 0; i < 250; i++ {
		a.steps = append(a.steps, domain.Step{
			ID: fmt.Sprintf("s%d", i), TurnID: "t1", Role: "assistant", Type: "text",
			Seq: int64(i + 1), Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "x"}},
		})
	}
	a.takeSnapshot()

	def, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{})
	if err != nil {
		t.Fatalf("default limit: %v", err)
	}
	if len(def.Items) != messageReadDefaultLimit || !def.HasMore {
		t.Fatalf("default items = %d (HasMore=%v), want %d with HasMore", len(def.Items), def.HasMore, messageReadDefaultLimit)
	}

	capped, err := a.handleMessageRead(nil, domain.AgentMessageReadReq{Limit: 1000})
	if err != nil {
		t.Fatalf("max limit: %v", err)
	}
	if len(capped.Items) != messageReadMaxLimit || !capped.HasMore {
		t.Fatalf("capped items = %d (HasMore=%v), want %d with HasMore", len(capped.Items), capped.HasMore, messageReadMaxLimit)
	}
	if capped.Items[0].Seq != 250 {
		t.Errorf("newest item Seq = %d, want 250", capped.Items[0].Seq)
	}
}
