import { expect, it, vi } from 'vitest'
import type { ProjectGraphConceptGetResp } from '../gen-clients/system/types'
import { ProjectConceptStore } from './project-wiki-card-client'

it('reads a concept on demand with the selected project target', async () => {
  const concept: ProjectGraphConceptGetResp = {
    Concept: { Id: 'card-ref', Desc: 'canonical reference', Capabilities: [], Constraints: [], Checks: [], State: 'real' },
    GraphKind: 'target',
    Id: 'target',
    Revision: 'rev-1',
  }
  const api = { graphConceptGet: vi.fn(async () => concept) }
  const store = new ProjectConceptStore(api)
  store.setProjectId('project-1')

  await expect(store.getConcept({ GraphKind: 'target', Id: 'target', ConceptId: 'card-ref' })).resolves.toEqual(concept)
  expect(api.graphConceptGet).toHaveBeenCalledWith(expect.anything(), {
    ProjectId: 'project-1', GraphKind: 'target', Id: 'target', ConceptId: 'card-ref',
  }, { target: 'project-1' })
})
