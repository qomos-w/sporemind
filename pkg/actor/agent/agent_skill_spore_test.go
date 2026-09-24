package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func sporeBody(src string) string {
	return "# skill body\n\n```spore\n" + src + "\n```\n"
}

func TestRunSporeSkillBodyEcho(t *testing.T) {
	src := "export fun run(input: any): any {\n\treturn input\n}"
	got, err := runSporeSkillBody(context.Background(), sporeBody(src), `{"a":1,"b":[true,null]}`, sporeSkillBudget{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"a":1,"b":[true,null]}` {
		t.Fatalf("got %s", got)
	}
}

// JSON integers must arrive as spore int, not float64 (UseNumber regression).
func TestRunSporeSkillBodyIntNormalization(t *testing.T) {
	src := "export fun run(input: any): any {\n\tvar m: map = input as map\n\treturn m[\"n\"] is int\n}"
	got, err := runSporeSkillBody(context.Background(), sporeBody(src), `{"n":42}`, sporeSkillBudget{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "true" {
		t.Fatalf("got %s, want true", got)
	}
}

func TestRunSporeSkillBodyMapEnumeration(t *testing.T) {
	src := "export fun run(input: any): any {\n\tvar keys: string = \"\"\n\tvar m: map = input as map\n\tfor (k in m) {\n\t\tkeys = keys + k\n\t}\n\treturn {\"count\": len(m), \"keys\": keys}\n}"
	got, err := runSporeSkillBody(context.Background(), sporeBody(src), `{"b":1,"a":2}`, sporeSkillBudget{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != `{"count":2,"keys":"ba"}` {
		t.Fatalf("got %s", got)
	}
}

// A script returning explicit null (legal for a nullable `: any` return)
// encodes as "null". Void scripts (no return type) are rejected upstream by
// the invoke payload validator, so the skill contract is "declare a return
// type"; this pins the null-value path instead.
func TestRunSporeSkillBodyNullReturn(t *testing.T) {
	src := "export fun run(input: any): any {\n\treturn null\n}"
	got, err := runSporeSkillBody(context.Background(), sporeBody(src), `1`, sporeSkillBudget{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "null" {
		t.Fatalf("got %s, want null", got)
	}
}

func TestRunSporeSkillBodyNoFence(t *testing.T) {
	_, err := runSporeSkillBody(context.Background(), "# just prose\n", `1`, sporeSkillBudget{})
	if err == nil || !strings.Contains(err.Error(), "no ```spore fenced block") {
		t.Fatalf("want fence error, got %v", err)
	}
}

func TestRunSporeSkillBodyInvalidArgs(t *testing.T) {
	src := "export fun run(input: any): any {\n\treturn input\n}"
	_, err := runSporeSkillBody(context.Background(), sporeBody(src), `not json`, sporeSkillBudget{})
	if err == nil || !strings.Contains(err.Error(), "must be a JSON value") {
		t.Fatalf("want args error, got %v", err)
	}
}

// A script declaring run(input) called without args must fail with a usage
// error, not a cryptic VM "cannot be nil".
func TestRunSporeSkillBodyMissingArgs(t *testing.T) {
	src := "export fun run(input: any): any {\n\treturn input\n}"
	_, err := runSporeSkillBody(context.Background(), sporeBody(src), ``, sporeSkillBudget{})
	if err == nil || !strings.Contains(err.Error(), "no args were supplied") {
		t.Fatalf("want missing-args error, got %v", err)
	}
}

// A script that declares run() (no params) runs fine without args.
func TestRunSporeSkillBodyZeroParamScript(t *testing.T) {
	src := "export fun run(): any {\n\treturn 6 * 7\n}"
	got, err := runSporeSkillBody(context.Background(), sporeBody(src), ``, sporeSkillBudget{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %s, want 42", got)
	}
}

// host imports must not resolve: the skill runner binds no host surface, so a
// script reaching for host.invoke fails at compile/load, never at call time.
func TestRunSporeSkillBodyHostImportRejected(t *testing.T) {
	src := "import { invoke } from \"host\"\nexport fun run(input: any): any {\n\treturn invoke(\"x.y\", {})\n}"
	_, err := runSporeSkillBody(context.Background(), sporeBody(src), `1`, sporeSkillBudget{})
	if err == nil {
		t.Fatalf("want host import rejection, got success")
	}
}

// An unbounded loop must be cut off by the duration budget.
func TestRunSporeSkillBodyDurationBudget(t *testing.T) {
	src := "export fun run(input: any): any {\n\tvar n: long = 0\n\twhile true {\n\t\tn = n + 1\n\t}\n\treturn n\n}"
	_, err := runSporeSkillBody(context.Background(), sporeBody(src), `1`, sporeSkillBudget{MaxDurationSec: 1})
	if err == nil {
		t.Fatalf("want budget cutoff, got success")
	}
}

// sporeSkillPlanner serves one runtime:spore skill card through the fake
// planner surface (list + get_card), reusing the manifestPlanner response
// shape but with a spore frontmatter.
func sporeSkillPlanner(raw string) skillPlannerFunc {
	return func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "project.wiki_list_cards":
			return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{
				{ID: "skill:calc-double", Tags: []string{"component", "skill", "math"}, Raw: raw},
			}}, nil
		case "project.wiki_get_card":
			return domain.WikiGetCardResp{ID: "skill:calc-double", Raw: raw}, nil
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	}
}

const calcDoubleRaw = "---\n" +
	"name: calc-double\n" +
	"description: Double a number via spore.\n" +
	"tags: [component, skill, math]\n" +
	"runtime: spore\n" +
	"---\n" +
	"\n" +
	"```spore\n" +
	"export fun run(input: any): any {\n" +
	"\tvar n: int = input as int\n" +
	"\treturn n * 2\n" +
	"}\n" +
	"```\n"

func TestHandleSkillUseSporeBranch(t *testing.T) {
	ctx, _ := newSkillTestCtx(t, sporeSkillPlanner(calcDoubleRaw))
	a := &Actor{}
	if _, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"math"}}); err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}

	resp, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "calc-double", Args: "21"})
	if err != nil {
		t.Fatalf("handleSkillUse: %v", err)
	}
	if resp.Result != "42" {
		t.Fatalf("Result: got %q, want 42", resp.Result)
	}
	if resp.Body != "" {
		t.Fatalf("Body must stay empty for spore skills, got %q", resp.Body)
	}
	if resp.SkillID != "calc-double" {
		t.Fatalf("SkillID: got %q", resp.SkillID)
	}
	// The skill must end up mounted (mountSkill ran) and marked used.
	if !a.isSkillMounted("calc-double") {
		t.Fatal("spore skill was not mounted by handleSkillUse")
	}
	if !a.skillAlreadyUsed("calc-double") {
		t.Fatal("spore skill was not marked used")
	}
}

func TestHandleSkillUseSporeBranchArgError(t *testing.T) {
	ctx, _ := newSkillTestCtx(t, sporeSkillPlanner(calcDoubleRaw))
	a := &Actor{}
	if _, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"math"}}); err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}

	_, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "calc-double", Args: "oops"})
	if err == nil || !strings.Contains(err.Error(), "must be a JSON value") {
		t.Fatalf("want args error, got %v", err)
	}
}
