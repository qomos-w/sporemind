import { describe, expect, it, vi } from 'vitest'
import type { WikiCreateCardResp, WikiDeleteCardResp, WikiGetCardResp, WikiListCardsResp, WikiEditCardResp } from '../gen-clients/system/types'
import { ProjectWikiCardStore } from './project-wiki-card-client'

function api() {
  return {
    wikiCreateCard: vi.fn(async (): Promise<WikiCreateCardResp> => ({ Card: { Id: 'card-1', Type: 'wiki', Source: 'project', Storage: 'cardstore', Visibility: 'wiki', Tags: [], List: [], Created: '', Modified: '', Protected: false, Editable: true, Deletable: true, Raw: '' } })),
    wikiGetCard: vi.fn(async (): Promise<WikiGetCardResp> => ({ Id: 'card-1', Raw: 'raw' })),
    wikiEditCard: vi.fn(async (): Promise<WikiEditCardResp> => ({ Card: { Id: 'card-1', Type: 'wiki', Source: 'project', Storage: 'cardstore', Visibility: 'wiki', Tags: [], List: [], Created: '', Modified: '', Protected: false, Editable: true, Deletable: true, Raw: '' } })),
    wikiDeleteCard: vi.fn(async (): Promise<WikiDeleteCardResp> => ({ Id: 'card-1' })),
    wikiListCards: vi.fn(async (): Promise<WikiListCardsResp> => ({ Tree: 'card-1', Nodes: [], Cards: [], Total: 0 })),
  }
}

describe('ProjectWikiCardStore', () => {
  it('passes the selected project target through every CRUD operation', async () => {
    const mock = api()
    const store = new ProjectWikiCardStore(mock)
    store.setProjectId('project-1')

    await store.create('card-1', 'raw')
    await store.get('card-1')
    await store.update('card-1', 'updated')
    await store.list()
    await store.delete('card-1')

    expect(mock.wikiCreateCard).toHaveBeenCalledWith(expect.anything(), { Id: 'card-1', Raw: 'raw' }, { target: 'project-1' })
    expect(mock.wikiGetCard).toHaveBeenCalledWith(expect.anything(), { Id: 'card-1' }, { target: 'project-1' })
    expect(mock.wikiEditCard).toHaveBeenCalledWith(expect.anything(), { Id: 'card-1', Raw: 'updated' }, { target: 'project-1' })
    expect(mock.wikiListCards).toHaveBeenCalledWith(expect.anything(), {}, { target: 'project-1' })
    expect(mock.wikiDeleteCard).toHaveBeenCalledWith(expect.anything(), { Id: 'card-1' }, { target: 'project-1' })
  })
})
