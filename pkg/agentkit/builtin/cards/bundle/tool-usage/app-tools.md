---
id: builtin:bundle:app-tools
type: bundle
title: App Manager Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: smartphone
  visual:
    icon: smartphone
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - appmanager.list
    - appmanager.get
    - appmanager.invoke
    - appmanager.component_list
    - appmanager.component_get
    - appmanager.register
    - appmanager.reload
---

### App Manager Tools

- **list** — List registered apps.
- **get** — Get app details.
- **invoke** — Invoke an app callable.
- **component_list** — List every virtual app-bundle component currently projected by the running app registry. Use this to see which bundles each app exposes for mounting.
- **component_get** — Get one virtual app-bundle component descriptor by cardId, including its title, icon, declared tools, and dependencies. Use this to inspect a bundle before deciding to mount it.
- **register** — Register a new app.
- **reload** — Reload an app configuration.
