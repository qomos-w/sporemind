import { describe, expect, it } from 'vitest'
import { cardTemplate } from './card-templates'

describe('card templates', () => {
  it('seeds scheduler Data', () => {
    const card = cardTemplate('scheduler')
    expect(card.tags).toContain('scheduler')
    expect(card.data?.schedule).toEqual({ cron: '' })
    expect(card.data?.run_status).toBe('idle')
  })
})
