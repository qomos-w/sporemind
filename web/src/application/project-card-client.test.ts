import { describe, expect, it, vi } from 'vitest'
import type { ProjectCardListResp, ProjectCardMountResp, ProjectCardUnmountResp } from '../gen-types/project.card'
import { ProjectCardStore } from './project-card-client'

const ref = { Id: 'builtin:mode:goal', Source: 'builtin', Scope: 'project' }

function api() {
  return {
    cardList: vi.fn(async (): Promise<ProjectCardListResp> => ({ Refs: [ref] })),
    cardMount: vi.fn(async (): Promise<ProjectCardMountResp> => ({ Ref: ref })),
    cardUnmount: vi.fn(async (): Promise<ProjectCardUnmountResp> => ({ Id: ref.Id })),
  }
}

describe('ProjectCardStore', () => {
  it('resets state when project changes and loads scoped refs', async () => {
    const mock = api()
    const store = new ProjectCardStore(mock)
    const states: string[] = []
    store.subscribe(state => states.push(`${state.projectId}:${state.loading}:${state.refs.length}`))

    store.setProjectId('project-1')
    await store.load()

    expect(store.getState()).toMatchObject({ projectId: 'project-1', refs: [ref], loading: false, error: null })
    expect(mock.cardList).toHaveBeenCalledWith(expect.anything(), {}, { target: 'project-1' })
    expect(states).toContain('project-1:true:0')
    expect(states.at(-1)).toBe('project-1:false:1')

    store.setProjectId('project-2')
    expect(store.getState()).toMatchObject({ projectId: 'project-2', refs: [], loading: false })
  })

  it('refreshes after mount and unmount', async () => {
    const mock = api()
    const store = new ProjectCardStore(mock)
    store.setProjectId('project-1')

    await store.mount(ref)
    await store.unmount(ref.Id)

    expect(mock.cardMount).toHaveBeenCalledWith(expect.anything(), { Ref: ref }, { target: 'project-1' })
    expect(mock.cardUnmount).toHaveBeenCalledWith(expect.anything(), { Id: ref.Id }, { target: 'project-1' })
    expect(mock.cardList).toHaveBeenCalledTimes(2)
  })

  it('publishes load errors', async () => {
    const mock = api()
    mock.cardList.mockRejectedValueOnce(new Error('load failed'))
    const store = new ProjectCardStore(mock)
    store.setProjectId('project-1')

    await expect(store.load()).rejects.toThrow('load failed')
    expect(store.getState()).toMatchObject({ loading: false, error: 'load failed' })
  })
})
