import { describe, expect, it } from 'vitest'
import { builtinCardDataKind } from './builtin-card-registry'

describe('builtin card data registry', () => {
  it('maps scheduler tags', () => expect(builtinCardDataKind(['automation', 'scheduler'])).toBe('scheduler'))
  it('does not treat ordinary tags as components', () => expect(builtinCardDataKind(['automation'])).toBeNull())
  it('maps task type to task kind', () => expect(builtinCardDataKind(['task'], 'task')).toBe('task'))
  it('prioritizes task type over scheduler tag', () => expect(builtinCardDataKind(['scheduler'], 'task')).toBe('task'))
  it('falls back to scheduler tag when type is not task', () => expect(builtinCardDataKind(['scheduler'], 'wiki')).toBe('scheduler'))
})
