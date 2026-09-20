---
id: builtin:bundle:image-gen
type: bundle
title: Image Generation
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: image
  visual:
    icon: image
    accent: indigo
    color: "#4f46e5"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - image_generate
---

## Image Generation

Use `generate_image` to create or edit images from a text prompt. When an image-generation media account is activated it is used directly; otherwise the request falls back to the system aggregator's image-generation unit, so no manual account activation is required. Pass `InputImage` as a file path, URL, or base64 data to edit an existing image with a single reference, or pass `ReferenceImages` (an array of file paths, URLs, or base64 data) for multi-image edits — both may be combined and are merged into one reference list. Generated files are saved under `assets/generated/`; use the returned path when referring to the asset in later work. The returned file path is automatically previewed in the chat timeline — do NOT call `show_page_thumbnail` or `open_global_browser` on generated image/video outputs.
