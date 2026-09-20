---
id: builtin:bundle:fork-review
type: bundle
title: Fork Review
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: eye
  visual:
    icon: eye
    accent: purple
    color: "#9333ea"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - fork_agent
  fork:
    toolName: fork_review
    childKind: reviewer
    description: "Fork an independent reviewer child agent to evaluate the codebase against the current goal or provided acceptance criteria. The reviewer reads code, tests, schema/codegen, and wiring using read-only tools, then returns a factual SUMMARY of what it verified and what it could not (with file:line evidence). The reviewer does NOT emit a goal-complete verdict."
---

### Review Goal

**fork_review** — Fork an independent reviewer child agent to evaluate the codebase against the current goal or provided acceptance criteria. The reviewer reads code, tests, schema/codegen, and wiring using read-only tools, then returns a factual SUMMARY of what it verified and what it could not (with file:line evidence).

The reviewer does NOT emit a goal-complete verdict — you (the main agent) read its summary and decide yourself whether to call goal submit, iterate, or finish. Use this to get a second opinion before reporting done, or to verify that a goal is truly complete. If no goal text is provided, the active session goal is reviewed automatically.
