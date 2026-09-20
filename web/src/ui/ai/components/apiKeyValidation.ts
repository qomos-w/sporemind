/**
 * Browser-side judgement of a typed API key (ported from deepseek-harness
 * ui-settings-models/apiKey.ts): a paste of an `export NAME=value` shell line,
 * a quoted string, or anything with spaces/controls is rejected with a
 * human-readable reason instead of being silently sent to the provider.
 */

const LEGAL_API_KEY = /^[\x21-\x7E]+$/

// Upper-case NAME= heuristics: `sk-` forms break at the hyphen, and `=` must
// not be followed by another `=` so base64 padding on an all-upper-case key
// (`ABCD==`) is not mistaken for an assignment.
const ENV_LINE = /^[A-Z][A-Z0-9_]*=[^=]/

/** Whether a value is wrapped in one matching pair of quotes. */
function isQuoted(value: string): boolean {
  const first = value[0]
  if (first !== '"' && first !== '\'' && first !== '`') return false
  return value.length > 1 && value.endsWith(first)
}

export type ApiKeyFailure = 'blank' | 'illegal'

/**
 * An empty field is not a failure (the field starts empty). A field holding
 * only whitespace is a failure so typed input is never silently discarded.
 */
export function apiKeyFailure(draft: string): ApiKeyFailure | undefined {
  if (draft.length === 0) return undefined
  const value = draft.trim()
  if (value.length === 0) return 'blank'
  if (ENV_LINE.test(value) || isQuoted(value)) return 'illegal'
  if (!LEGAL_API_KEY.test(value)) return 'illegal'
  return undefined
}
