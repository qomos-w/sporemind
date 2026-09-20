---
id: builtin:bundle:video-gen
type: bundle
title: Video Generation
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: video
  visual:
    icon: video
    accent: indigo
    color: "#4f46e5"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - video_generate
---

## Video Generation

Use `generate_video` to create a video clip from a text prompt. Before generating, open the media settings page and activate a video media account. Generated files are saved under `assets/generated/`; use the returned path when referring to the asset in later work.
