package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// schedulerCard builds a scheduler CardRecord from a raw frontmatter/data
// block and the card body (the prompt text for prompt-type schedulers).
func schedulerCard(id, dataBlock, body string) *CardRecord {
	raw := "---\nid: " + id + "\n"
	if dataBlock != "" {
		raw += "data:\n" + dataBlock + "\n"
	}
	raw += "---\n\n" + body
	return decodeCard(id, raw)
}

func TestScheduleTypeOf(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{name: "explicit workflow wins", data: "  schedule_type: workflow\n  workflow_template: tpl::a", want: scheduleTypeWorkflow},
		{name: "explicit prompt wins", data: "  schedule_type: prompt\n  workflow_template: tpl::stale", want: scheduleTypePrompt},
		{name: "prompt ignores stale template", data: "  schedule_type: prompt\n  workflow_template: tpl::stale", want: scheduleTypePrompt},
		{name: "explicit task with template", data: "  schedule_type: task\n  workflow_template: tpl::a", want: scheduleTypeTask},
		{name: "explicit task without template", data: "  schedule_type: task", want: scheduleTypeTask},
		{name: "legacy workflow derived from template", data: "  workflow_template: tpl::a", want: scheduleTypeWorkflow},
		{name: "empty derives prompt", data: "", want: scheduleTypePrompt},
		{name: "explicit agent_action", data: "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder", want: scheduleTypeAgentAction},
		{name: "invalid explicit value returned as-is", data: "  schedule_type: bogus", want: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:st", tc.data, "Body.")
			if got := scheduleTypeOf(card); got != tc.want {
				t.Fatalf("scheduleTypeOf(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestTaskModeOf(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{name: "task with bound template is template mode", data: "  schedule_type: task\n  workflow_template: tpl::a", want: taskModeTemplate},
		{name: "task without template is prompt mode", data: "  schedule_type: task", want: taskModePrompt},
		{name: "legacy workflow derives template mode", data: "  schedule_type: workflow\n  workflow_template: tpl::a", want: taskModeTemplate},
		{name: "legacy prompt derives prompt mode", data: "  schedule_type: prompt", want: taskModePrompt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:tm", tc.data, "Body.")
			if got := taskModeOf(card); got != tc.want {
				t.Fatalf("taskModeOf(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestSchedulerValidatorPromptBody(t *testing.T) {
	hasCode := func(errs []gen.CardValidationError, code string) bool {
		for _, e := range errs {
			if e.Code == code {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name      string
		data      string
		body      string
		wantEmpty bool // expect scheduler_prompt_body_empty
	}{
		{
			name:      "explicit prompt empty body errors",
			data:      "  schedule_type: prompt\n  executor: agent:coder",
			body:      "",
			wantEmpty: true,
		},
		{
			name:      "explicit prompt whitespace-only body errors",
			data:      "  schedule_type: prompt\n  executor: agent:coder",
			body:      "   \n\t",
			wantEmpty: true,
		},
		{
			name: "explicit prompt with body passes",
			data: "  schedule_type: prompt\n  executor: agent:coder",
			body: "Do the thing.",
		},
		{
			name: "legacy empty body without schedule_type passes",
			data: "  executor: agent:coder",
			body: "",
		},
		{
			name: "workflow empty body unaffected",
			data: "  schedule_type: workflow\n  workflow_template: tpl::a\n  executor: agent:coder",
			body: "",
		},
		{
			name: "agent_action empty body unaffected",
			data: "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder",
			body: "",
		},
		{
			name: "task bound to template allows empty body",
			data: "  schedule_type: task\n  workflow_template: tpl::a",
			body: "",
		},
		{
			name: "task without template requires body",
			data: "  schedule_type: task",
			body: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:pb", tc.data, tc.body)
			errs := (schedulerValidator{}).Validate(card)
			got := hasCode(errs, "scheduler_prompt_body_empty")
			if got != tc.wantEmpty {
				t.Fatalf("schedulerValidator.Validate() body-empty code present = %v, want %v (errs: %+v)", got, tc.wantEmpty, errs)
			}
		})
	}
}

func TestSchedulerValidatorTaskBody(t *testing.T) {
	hasCode := func(errs []gen.CardValidationError, code string) bool {
		for _, e := range errs {
			if e.Code == code {
				return true
			}
		}
		return false
	}
	cases := []struct {
		name      string
		data      string
		body      string
		wantEmpty bool // expect scheduler_task_body_empty
	}{
		{
			name: "template-bound task allows empty body",
			data: "  schedule_type: task\n  workflow_template: tpl::a",
			body: "",
		},
		{
			name:      "task without template requires non-empty body",
			data:      "  schedule_type: task",
			body:      "",
			wantEmpty: true,
		},
		{
			name:      "task without template whitespace-only body errors",
			data:      "  schedule_type: task",
			body:      "  \n",
			wantEmpty: true,
		},
		{
			name: "task without template with body passes",
			data: "  schedule_type: task",
			body: "Do the thing.",
		},
		{
			name: "task with template does not require executor",
			data: "  schedule_type: task\n  workflow_template: tpl::a",
			body: "",
		},
		{
			name: "task prompt mode does not require executor",
			data: "  schedule_type: task",
			body: "Body.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:tpb", tc.data, tc.body)
			errs := (schedulerValidator{}).Validate(card)
			if got := hasCode(errs, "scheduler_task_body_empty"); got != tc.wantEmpty {
				t.Fatalf("scheduler_task_body_empty present = %v, want %v (errs: %+v)", got, tc.wantEmpty, errs)
			}
			for _, e := range errs {
				if e.Code == "scheduler_executor_missing" {
					t.Fatalf("task card must not require executor, got %+v", errs)
				}
			}
		})
	}
}

func TestTimerFireBranch(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantKind timerFireKind
		wantTpl string
		wantErr string
	}{
		{
			name:    "workflow with template instantiates",
			data:    "  schedule_type: workflow\n  workflow_template: tpl::a",
			wantKind: fireWorkflow,
			wantTpl: "tpl::a",
		},
		{
			name:    "workflow without template errors",
			data:    "  schedule_type: workflow",
			wantErr: "schedule_type=workflow requires workflow_template",
		},
		{
			name: "prompt goes chat.submit and ignores stale template",
			data: "  schedule_type: prompt\n  workflow_template: tpl::stale",
			wantKind: firePrompt,
		},
		{
			name: "prompt without template goes chat.submit",
			data: "  schedule_type: prompt",
			wantKind: firePrompt,
		},
		{
			name: "task with template instantiates (template mode)",
			data: "  schedule_type: task\n  workflow_template: tpl::a",
			wantKind: fireWorkflow,
			wantTpl: "tpl::a",
		},
		{
			name: "task without template goes prompt (prompt mode)",
			data: "  schedule_type: task",
			wantKind: firePrompt,
		},
		{
			name:    "task with bind_mode bound opts into the dual-mode agent_task branch",
			data:    "  schedule_type: task\n  bind_mode: bound\n  bound_agent: agent:coder",
			wantKind: fireAgentTask,
		},
		{
			name:    "task with bind_mode ephemeral opts into the dual-mode agent_task branch",
			data:    "  schedule_type: task\n  bind_mode: ephemeral",
			wantKind: fireAgentTask,
		},
		{
			name:    "task with unknown bind_mode keeps the prompt branch",
			data:    "  schedule_type: task\n  bind_mode: bogus",
			wantKind: firePrompt,
		},
		{
			name:    "legacy card with template derives workflow",
			data:    "  workflow_template: tpl::a",
			wantKind: fireWorkflow,
			wantTpl: "tpl::a",
		},
		{
			name: "legacy card without template derives prompt",
			data: "",
			wantKind: firePrompt,
		},
		{
			name:    "agent_action fires pause/resume",
			data:    "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder",
			wantKind: fireAgentAction,
		},
		{
			name:    "invalid schedule_type errors",
			data:    "  schedule_type: bogus",
			wantErr: "must be task, workflow, prompt, agent_action, or agent_task",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:fb", tc.data, "Body.")
			kind, tpl, err := timerFireBranch(card)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("timerFireBranch(%q) error = %v, want containing %q", tc.data, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("timerFireBranch(%q) unexpected error: %v", tc.data, err)
			}
			if kind != tc.wantKind {
				t.Fatalf("timerFireBranch(%q) kind = %d, want %d", tc.data, kind, tc.wantKind)
			}
			if tpl != tc.wantTpl {
				t.Fatalf("timerFireBranch(%q) tpl = %q, want %q", tc.data, tpl, tc.wantTpl)
			}
		})
	}
}

// An action-only task card (empty body, non-empty agent_actions) must keep the
// prompt branch even when it carries a bind_mode: the prompt path's skipPrimary
// semantics skip the primary branch instead of submitting an empty body.
func TestTimerFireBranch_TaskBindModeActionOnlyKeepsPrompt(t *testing.T) {
	card := schedulerCard("scheduler:fb-action",
		"  schedule_type: task\n  bind_mode: bound\n  bound_agent: agent:coder\n  agent_actions: '[{\"action\":\"pause\",\"target\":\"agent:coder\"}]'",
		"")
	kind, _, err := timerFireBranch(card)
	if err != nil {
		t.Fatalf("timerFireBranch: %v", err)
	}
	if kind != firePrompt {
		t.Fatalf("timerFireBranch kind = %d, want %d (firePrompt)", kind, firePrompt)
	}
}
