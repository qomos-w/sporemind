---
id: builtin:bundle:browser-tools
type: bundle
title: Browser Tool
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: globe
  visual:
    icon: globe
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - open_global_browser
---

## Browser Tool

Open a URL or local file in Sporemind's shared global browser tab with `open_global_browser`. Use it to bring a web page or local artifact into view for the user. This opens the page but does not automate its DOM; use Computer Use only when direct visual interaction with the browser is necessary.
