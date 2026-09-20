---
id: builtin:bundle:image-recognition
type: bundle
title: Image Recognition
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: eye
  visual:
    icon: eye
    accent: emerald
    color: "#059669"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - image_recognize
---

## Image Recognition

Use `recognize_image` to send an image to a vision-capable model and get its textual description back — this is how a text-only model "sees" an image. Pass `Image` as a file path, URL, or base64 data, and an optional `Prompt` when you want a specific answer ("count the cubes", "transcribe the text") instead of a full description. Optionally pin `Provider`/`Model` to a specific vision model, or pass `Aggregator` to route through a named aggregator pool (default: the system aggregator's first healthy vision-capable unit). Use this whenever the user attaches an image and your primary model cannot process image input, or when a generated/saved image needs to be verified against intent.
