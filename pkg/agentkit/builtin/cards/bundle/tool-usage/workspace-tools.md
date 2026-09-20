---
id: builtin:bundle:workspace-tools
type: bundle
title: Workspace Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: layers
  visual:
    icon: layers
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workspace.list_project
    - workspace.list_agents
    - workspace.account
---

### Workspace Tools

- **workspace.list_project** — List projects in the workspace.
- **workspace.list_agents** — List agents in the workspace. Your own entry is marked with "(self)". Optional filters: `Query` (substring on display name / title), `ProjectId` (actor ID or mount name), `ParentAgentId` (list only the direct children of that agent), `ChildrenOnly` (when true, uses your own actor ID as the parent — shorthand for "list my children").
- **account** — Get current account information.
