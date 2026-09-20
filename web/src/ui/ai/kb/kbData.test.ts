import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({
  wikiListCards: vi.fn(),
}))

import { loadProjectLaneCards } from './kbData'
import { wikiListCards } from '../../../gen-clients/project/client'

const wikiListCardsMock = vi.mocked(wikiListCards)

function listItem(id: string, overrides: Record<string, unknown> = {}) {
  return {
    Id: id,
    Tags: [],
    List: [],
    Modified: '2026-09-14T00:00:00Z',
    ...overrides,
  }
}

describe('loadProjectLaneCards', () => {
  beforeEach(() => {
    wikiListCardsMock.mockReset()
  })

  it('keeps cards whose raw has no frontmatter (legacy notes the backend mounts under toc)', async () => {
    wikiListCardsMock.mockResolvedValue({
      Cards: [
        listItem('with-fm', { Raw: '---\nid: with-fm\ntype: wiki\ntags: []\n---\n\nBody A.' }),
        listItem('legacy', { Type: 'wiki', Raw: 'Just a plain markdown note, no frontmatter.' }),
        listItem('empty-raw', { Type: 'wiki' }),
      ],
    } as never)
    const cards = await loadProjectLaneCards('proj')
    expect(cards.map(c => c.id)).toEqual(['with-fm', 'legacy', 'empty-raw'])
    expect(cards[1]!.body).toBe('Just a plain markdown note, no frontmatter.')
    expect(cards[1]!.type).toBe('wiki')
    expect(cards[2]!.body).toBe('')
  })

  it('carries list-item metadata onto the fallback card', async () => {
    wikiListCardsMock.mockResolvedValue({
      Cards: [
        listItem('legacy', {
          Type: 'wiki',
          Raw: 'plain body',
          Parent: 'toc',
          Standalone: true,
          Status: 'todo',
          Tags: ['alpha'],
          List: ['child'],
        }),
      ],
    } as never)
    const cards = await loadProjectLaneCards('proj')
    expect(cards[0]).toMatchObject({
      id: 'legacy',
      parent: 'toc',
      standalone: true,
      status: 'todo',
      tags: ['alpha'],
      list: ['child'],
      body: 'plain body',
    })
  })

  it('keeps frontmatter cards on the parseMonoCard path', async () => {
    wikiListCardsMock.mockResolvedValue({
      Cards: [listItem('a', { Raw: '---\nid: a\ntype: task\ntags: [x]\n---\n\nBody.' })],
    } as never)
    const cards = await loadProjectLaneCards('proj')
    expect(cards[0]!.type).toBe('task')
    expect(cards[0]!.tags).toEqual(['x'])
  })
})