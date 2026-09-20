import { describe, it, expect, vi, beforeEach } from 'vitest'

// The production data source pulls in the generated clients (which resolve
// through the host's `@qomos/*` file dependencies). This suite exercises the
// mapping/fan-out logic only, so stub those leaves out — mirroring the pattern
// used by the other client-touching component tests in this directory.
vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({
  wikiGetOpenCards: vi.fn(),
}))
vi.mock('../kb/kbData', () => ({
  listKbProjects: vi.fn(),
  listKbStarred: vi.fn(),
  searchKbAcrossProjects: vi.fn(),
  setCardStarred: vi.fn(),
}))

import * as project from '../../../gen-clients/project/client'
import * as kbData from '../kb/kbData'
import type * as systemTypes from '../../../gen-clients/system/types'
import { defaultKbQuickViewsDataSource } from './kbQuickViews.data'

const mockedProject = vi.mocked(project)
const mockedKbData = vi.mocked(kbData)

const projectRef = (name: string, actorId?: string, lastOpenedAt?: string): systemTypes.ProjectRef => ({
  Name: name,
  Path: `/projects/${name}`,
  Root: false,
  ...(actorId ? { ActorId: actorId } : {}),
  ...(lastOpenedAt ? { LastOpenedAt: lastOpenedAt } : {}),
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('defaultKbQuickViewsDataSource.listProjects', () => {
  it('maps projects to refs and drops entries with no actor id', async () => {
    mockedKbData.listKbProjects.mockResolvedValueOnce([
      projectRef('Alpha', 'p1', '2026-01-01T00:00:00Z'),
      projectRef('NoActor'),
    ])
    const out = await defaultKbQuickViewsDataSource.listProjects()
    expect(out).toEqual([{ projectId: 'p1', projectName: 'Alpha', lastOpenedAt: '2026-01-01T00:00:00Z' }])
  })
})

describe('defaultKbQuickViewsDataSource.listStarred', () => {
  it('maps aggregated entries to cross-project card refs', async () => {
    mockedKbData.listKbStarred.mockResolvedValueOnce([
      { ProjectID: 'p1', ProjectName: 'Alpha', CardID: 'design' },
      { ProjectID: 'p2', ProjectName: 'Beta', CardID: 'notes' },
    ])
    expect(await defaultKbQuickViewsDataSource.listStarred()).toEqual([
      { projectId: 'p1', projectName: 'Alpha', cardId: 'design' },
      { projectId: 'p2', projectName: 'Beta', cardId: 'notes' },
    ])
  })
})

describe('defaultKbQuickViewsDataSource.listRecent', () => {
  it('fans out open cards per project and interleaves them', async () => {
    mockedProject.wikiGetOpenCards.mockImplementation(async (_client, _req, opts) => {
      const target = (opts as { target?: string } | undefined)?.target
      if (target === 'p1') return { OpenCards: ['a1', 'a2'] }
      return { OpenCards: ['b1'] }
    })
    const out = await defaultKbQuickViewsDataSource.listRecent(
      [
        { projectId: 'p1', projectName: 'Alpha' },
        { projectId: 'p2', projectName: 'Beta' },
      ],
      3,
    )
    expect(out.map(r => `${r.projectId}/${r.cardId}`)).toEqual(['p1/a1', 'p2/b1', 'p1/a2'])
    expect(mockedProject.wikiGetOpenCards).toHaveBeenCalledTimes(2)
  })

  it('skips projects whose open-card read rejects', async () => {
    mockedProject.wikiGetOpenCards.mockImplementation(async (_client, _req, opts) => {
      const target = (opts as { target?: string } | undefined)?.target
      if (target === 'p1') throw new Error('unreachable')
      return { OpenCards: ['b1'] }
    })
    const out = await defaultKbQuickViewsDataSource.listRecent(
      [
        { projectId: 'p1', projectName: 'Alpha' },
        { projectId: 'p2', projectName: 'Beta' },
      ],
      5,
    )
    expect(out.map(r => `${r.projectId}/${r.cardId}`)).toEqual(['p2/b1'])
  })
})

describe('defaultKbQuickViewsDataSource.search', () => {
  it('reuses the shared aggregation and maps hits', async () => {
    mockedKbData.searchKbAcrossProjects.mockResolvedValueOnce([
      {
        projectId: 'p1',
        projectName: 'Alpha',
        card: { Id: 'design' } as systemTypes.MonoCardListItem,
        line: 3,
        snippet: 'hello',
      },
    ])
    const out = await defaultKbQuickViewsDataSource.search('des', [
      { projectId: 'p1', projectName: 'Alpha' },
    ])
    expect(out).toEqual([
      { projectId: 'p1', projectName: 'Alpha', cardId: 'design', line: 3, snippet: 'hello' },
    ])
    const [query, refs] = mockedKbData.searchKbAcrossProjects.mock.calls[0]!
    expect(query).toBe('des')
    expect(refs[0]!.ActorId).toBe('p1')
    expect(refs[0]!.Name).toBe('Alpha')
  })
})

describe('defaultKbQuickViewsDataSource.setStarred', () => {
  it('routes to the shared star callable', async () => {
    mockedKbData.setCardStarred.mockResolvedValueOnce([])
    await defaultKbQuickViewsDataSource.setStarred('p1', 'design', false)
    expect(mockedKbData.setCardStarred).toHaveBeenCalledWith('p1', 'design', false)
  })
})
