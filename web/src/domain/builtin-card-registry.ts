export type BuiltinCardDataKind = 'scheduler' | 'task'

export interface BuiltinCardDataBinding {
  tag: string
  kind: BuiltinCardDataKind
}

export const BUILTIN_CARD_DATA_BINDINGS: BuiltinCardDataBinding[] = [
  { tag: 'scheduler', kind: 'scheduler' },
]

export function builtinCardDataKind(tags: string[], type?: string): BuiltinCardDataKind | null {
  if (type === 'task') return 'task'
  for (const binding of BUILTIN_CARD_DATA_BINDINGS) {
    if (tags.includes(binding.tag)) return binding.kind
  }
  return null
}
