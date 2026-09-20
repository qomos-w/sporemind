import { MODEL_CONTEXT_DEFAULTS } from '../../../../../const/modelContextDefaults'
import type { ModelDefault } from '../../../gen-clients/system/types'

// A model defaults "MaxTokens" is considered unset when absent or zero,
// matching the Go schema's `omitempty int32` (0 => omitted).
export function isUnsetMaxTokens(v: number | undefined): boolean {
  return v === undefined || v <= 0
}

// Modality is considered unset when absent, empty, or the implicit 'chat'
// default — matching the existing isImageModel convention.
export function isUnsetModality(v: string | undefined): boolean {
  return v === undefined || v === '' || v === 'chat'
}

// isBuiltinDefault reports whether a defaults-table row is a pure built-in
// baseline copy (the static MODEL_CONTEXT_DEFAULTS const): same prefix and
// context length, and no user-set overrides on any optional field.
// Such rows are displayed but skipped when persisting via model_defaults_set,
// so the shipped baseline stays the single source of truth for them and is
// not frozen into user state.
export function isBuiltinDefault(row: ModelDefault): boolean {
  const base = MODEL_CONTEXT_DEFAULTS[row.Prefix]
  if (base === undefined) return false
  if (row.MaxContextLength !== base) return false
  if (row.CostInput !== undefined) return false
  if (row.CostOutput !== undefined) return false
  return isUnsetMaxTokens(row.MaxTokens) && isUnsetModality(row.Modality)
}

// modelDefaultsLookupTable builds the prefix -> max context length table used
// by lookupModelDefault when applying defaults to newly fetched models. The
// shipped MODEL_CONTEXT_DEFAULTS const forms the built-in baseline; user-edited
// rows override baseline entries by prefix and augment the table with new
// prefixes. Rows with empty prefix or non-positive context are ignored.
export function modelDefaultsLookupTable(rows: ModelDefault[]): Record<string, number> {
  const table: Record<string, number> = { ...MODEL_CONTEXT_DEFAULTS }
  for (const r of rows) {
    if (r.Prefix && r.MaxContextLength > 0) table[r.Prefix] = r.MaxContextLength
  }
  return table
}
