# Glass render-spec export (for the sporemind debug canvas)

Verbatim, zero-drift snapshot of the phone-side text measurement/wrapping
pipeline that the sporemind `GlassScenePreview` canvas should mirror. The
device (Even Realities G2) receives PRE-WRAPPED text — wrapping/clipping
happens on the phone with these exact tables, firmware in-box wrap is only a
fallback.

## Files (verbatim copies — do not edit here; source of truth below)

| Export file | Source (mobile/modules/engine/src/utils/display/) |
| --- | --- |
| `profiles/types.ts` | `profiles/types.ts` |
| `profiles/g1.ts` | `profiles/g1.ts` (glyph table from G1FontLoaderKt; G2 inherits — same hardware/font) |
| `profiles/g2.ts` | `profiles/g2.ts` (`maxLines: 8`, `lineHeightPx: 40` hardware-calibrated) |
| `measurer/script-detection.ts` | `measurer/script-detection.ts` |
| `measurer/TextMeasurer.ts` | `measurer/TextMeasurer.ts` |
| `wrapper/types.ts` | `wrapper/types.ts` (`DEFAULT_WRAP_OPTIONS`, BreakMode union) |
| `wrapper/TextWrapper.ts` | `wrapper/TextWrapper.ts` |

Pure functions, no React Native dependencies. Import closure is exactly these
seven files (TextWrapper → TextMeasurer + script-detection + wrapper/types;
profiles standalone).

## glyph-table.json

Machine-generated from the LIVE profiles by `gen-glyph-table.ts` — no
hand-transcription. Regenerate from the repo root:

```
bun run notes/render-spec-export/gen-glyph-table.ts
```

- `fontMetrics.glyphWidths` are BITMAP glyph px; rendered px =
  `renderFormula(w) = (w + 1) * 2`.
- `renderedWidths` is the per-char rendered-px convenience map.
- `uniformScripts` (CJK/hiragana/katakana/cyrillic 18, Korean 24) and
  `fallback.latinMaxWidth: 16` are already rendered px.

## Facts not in these files

- Scene canvas 576×288 and the element pools (`maxTextElements: 6` shared by
  text+rect, `maxImageElements: 4`) live in the capability file
  `mobile/modules/engine/src/types/capabilities/even-realities-g2.ts`.
- Budget order: elements are Z-sorted ascending, budget consumes in array
  order, so the TOPMOST-Z elements drop first when over budget
  (`scene/process.ts`).
