package agent

import (
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/agentkit"
)

// ReviewerMaxIterations caps how many LLM dispatch iterations a reviewer child
// may run. Reviewers are read-only and bounded; without a cap a child with no
// natural stopping point will loop forever (the explore path enforces
// MaxIterations but the previous review spawn left it at 0).
const ReviewerMaxIterations int32 = 8

// BuildReviewPrompt assembles the reviewer's seed prompt. The reviewer is asked
// to produce a factual summary of what it verified and what it could not — NOT
// a goal-complete verdict. The main agent reads the summary and decides.
func BuildReviewPrompt(reviewText string, planEvidence []string, turnSummary string) string {
	var b strings.Builder
	b.WriteString(agentkit.GoalReviewPrompt)
	b.WriteString("\n\n## Goal\n")
	b.WriteString(strings.TrimSpace(reviewText))
	if len(planEvidence) > 0 {
		b.WriteString("\n\n## Plans (reference only)\n")
		for i, p := range planEvidence {
			fmt.Fprintf(&b, "\n### Plan %d\n%s\n", i+1, strings.TrimSpace(p))
		}
	}
	if s := strings.TrimSpace(turnSummary); s != "" {
		b.WriteString("\n\n## Agent report (untrusted)\n")
		b.WriteString(s)
	}
	return b.String()
}
