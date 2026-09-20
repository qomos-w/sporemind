import { client } from './generated-client'
import * as graphClient from '../gen-clients/project/client'
import type { ProjectGraphConceptGetReq, ProjectGraphConceptGetResp } from '../gen-clients/system/types'

export interface ProjectConceptReader {
  getConcept(req: Omit<ProjectGraphConceptGetReq, 'ProjectId'>, opts?: InvokeOptions): Promise<ProjectGraphConceptGetResp>
}

interface ProjectConceptApi {
  graphConceptGet(client: GosporeClient, req: ProjectGraphConceptGetReq, opts?: InvokeOptions): Promise<ProjectGraphConceptGetResp>
}

export class ProjectConceptStore implements ProjectConceptReader {
  constructor(private readonly api: ProjectConceptApi = graphClient) {}

  setProjectId(projectId: string | null): void {
    this.projectId = projectId
  }

  getConcept(req: Omit<ProjectGraphConceptGetReq, 'ProjectId'>, opts?: InvokeOptions): Promise<ProjectGraphConceptGetResp> {
    const projectId = this.projectId
    return this.api.graphConceptGet(client, { ...req, ProjectId: projectId ?? '' }, { target: projectId ?? undefined, ...opts })
  }

  private projectId: string | null = null
}

export const projectConceptStore = new ProjectConceptStore()
import * as wikiClient from '../gen-clients/project/client'
import type { GosporeClient, InvokeOptions } from '@qomos/gospore-client'
import type {
  WikiCreateCardReq,
  WikiCreateCardResp,
  WikiDeleteCardReq,
  WikiDeleteCardResp,
  WikiGetCardReq,
  WikiGetCardResp,
  WikiListCardsReq,
  WikiListCardsResp,
  WikiEditCardReq,
  WikiEditCardResp,
} from '../gen-clients/system/types'

interface ProjectWikiCardApi {
  wikiCreateCard(client: GosporeClient, req: WikiCreateCardReq, opts?: InvokeOptions): Promise<WikiCreateCardResp>
  wikiGetCard(client: GosporeClient, req: WikiGetCardReq, opts?: InvokeOptions): Promise<WikiGetCardResp>
  wikiEditCard(client: GosporeClient, req: WikiEditCardReq, opts?: InvokeOptions): Promise<WikiEditCardResp>
  wikiDeleteCard(client: GosporeClient, req: WikiDeleteCardReq, opts?: InvokeOptions): Promise<WikiDeleteCardResp>
  wikiListCards(client: GosporeClient, req: WikiListCardsReq, opts?: InvokeOptions): Promise<WikiListCardsResp>
}

export class ProjectWikiCardStore {
  constructor(private readonly api: ProjectWikiCardApi = wikiClient) {}

  setProjectId(projectId: string | null): void {
    this.projectId = projectId
  }

  async create(id: string, raw: string): Promise<WikiCreateCardResp> {
    return this.api.wikiCreateCard(client, { Id: id, Raw: raw }, this.target())
  }

  async get(id: string): Promise<WikiGetCardResp> {
    return this.api.wikiGetCard(client, { Id: id }, this.target())
  }

  async update(id: string, raw: string): Promise<WikiEditCardResp> {
    return this.api.wikiEditCard(client, { Id: id, Raw: raw }, this.target())
  }

  async delete(id: string): Promise<WikiDeleteCardResp> {
    return this.api.wikiDeleteCard(client, { Id: id }, this.target())
  }

  async list(rootId?: string): Promise<WikiListCardsResp> {
    return this.api.wikiListCards(client, rootId ? { RootId: rootId } : {}, this.target())
  }

  private projectId: string | null = null

  private target(): InvokeOptions | undefined {
    return this.projectId ? { target: this.projectId } : undefined
  }
}

export const projectWikiCardStore = new ProjectWikiCardStore()
