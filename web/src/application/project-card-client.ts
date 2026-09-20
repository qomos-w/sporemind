import { client } from './generated-client'
import * as cardClient from '../gen-clients/project/client'
import type { GosporeClient, InvokeOptions } from '@qomos/gospore-client'
import type { CardRef } from '../gen-types/card'
import type { ProjectCardListResp, ProjectCardMountResp, ProjectCardUnmountResp } from '../gen-types/project.card'

export interface ProjectCardState {
  projectId: string | null
  refs: CardRef[]
  loading: boolean
  error: string | null
}

interface ProjectCardApi {
  cardList(client: GosporeClient, req: {}, opts?: InvokeOptions): Promise<ProjectCardListResp>
  cardMount(client: GosporeClient, req: { Ref: CardRef }, opts?: InvokeOptions): Promise<ProjectCardMountResp>
  cardUnmount(client: GosporeClient, req: { Id: string }, opts?: InvokeOptions): Promise<ProjectCardUnmountResp>
}

export class ProjectCardStore {
  private state: ProjectCardState = { projectId: null, refs: [], loading: false, error: null }
  private listeners = new Set<(state: ProjectCardState) => void>()

  constructor(private readonly api: ProjectCardApi = cardClient) {}

  subscribe(listener: (state: ProjectCardState) => void): () => void {
    this.listeners.add(listener)
    listener(this.state)
    return () => this.listeners.delete(listener)
  }

  getState(): ProjectCardState { return this.state }

  setProjectId(projectId: string | null): void {
    this.state = { projectId, refs: [], loading: false, error: null }
    this.emit()
  }

  async load(): Promise<CardRef[]> {
    if (!this.state.projectId) return []
    this.state = { ...this.state, loading: true, error: null }
    this.emit()
    try {
      const response = await this.api.cardList(client, {}, this.target())
      this.state = { ...this.state, refs: response.Refs, loading: false }
      this.emit()
      return response.Refs
    } catch (error) {
      this.state = { ...this.state, loading: false, error: error instanceof Error ? error.message : String(error) }
      this.emit()
      throw error
    }
  }

  async mount(ref: CardRef): Promise<CardRef> {
    const response = await this.api.cardMount(client, { Ref: ref }, this.target())
    await this.load()
    return response.Ref
  }

  async unmount(id: string): Promise<void> {
    await this.api.cardUnmount(client, { Id: id }, this.target())
    await this.load()
  }

  private target() {
    return this.state.projectId ? { target: this.state.projectId } : undefined
  }

  private emit(): void { for (const listener of this.listeners) listener(this.state) }
}

export const projectCardStore = new ProjectCardStore()

export function listProjectCards(): Promise<CardRef[]> { return projectCardStore.load() }
export function mountProjectCard(ref: CardRef): Promise<CardRef> { return projectCardStore.mount(ref) }
export function unmountProjectCard(id: string): Promise<void> { return projectCardStore.unmount(id) }
