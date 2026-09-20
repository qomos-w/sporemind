// Hand-written skill types. Replaces the generated gen-types/skill.ts as part
// of the mono-card migration.

import { ModelUnit } from '../gen-types/aigen.part1'

export interface Skill {
  Id: string
  Source: string
  Version: number
  ForkOf: string
  BuiltinKey: string
  Name: string
  Description: string
  Tags: string[]
  Tools: string[]
  Params: string[]
  Context: string
  Permission: string
  SubAgentType: string
  Unit: ModelUnit
  Template: string
  Editable: boolean
  Deletable: boolean
  ProjectID?: string | undefined
  Meta?: Record<string, string> | undefined
  ModuleKind?: string | undefined
}

export interface SkillInstallSource {
  Url?: string | undefined
  GitUrl?: string | undefined
  GitRef?: string | undefined
  GitPath?: string | undefined
  LocalPath?: string | undefined
  RegistryName?: string | undefined
  SkillName?: string | undefined
  Version?: string | undefined
}

export interface SkillInstallReq {
  Source: SkillInstallSource
}

export interface SkillManagerGetReq {
  Id: string
}

export interface SkillManagerSaveReq {
  Id: string
  Name: string
  Description: string
  Tags: string[]
  Tools: string[]
  Params: string[]
  Context: string
  Permission: string
  SubAgentType: string
  Unit: ModelUnit
  Template: string
  ModuleKind?: string | undefined
}

export interface SkillListResp {
  Items: Skill[]
}

export interface SkillManagerListReq {
  ProjectID?: string | undefined
}

export interface SkillManifest {
  Id: string
  Name: string
  Description: string
  Tags: string[]
  Tools: string[]
  Params: string[]
  Context?: string | undefined
  ProjectID?: string | undefined
}

export interface SkillManagerManifestListReq {
  ProjectID?: string | undefined
}

export interface SkillManifestListResp {
  Items: SkillManifest[]
}

export interface SkillManagerGetBodyReq {
  Id: string
  Args?: string | undefined
}

export interface SkillManagerGetBodyResp {
  Id: string
  Version: number
  Body: string
  AllowedTools?: string[] | undefined
  Context?: string | undefined
  ProjectID?: string | undefined
  ModuleKind?: string | undefined
}

export interface AgentSkillMountReq {
  SkillId: string
  Args?: string | undefined
  Context?: string | undefined
  TurnId?: string | undefined
}

export interface AgentSkillMountResp {
  MountId: string
  Body?: string | undefined
  SkillId?: string | undefined
}

export interface SkillManagerDeleteReq {
  Id: string
}

export interface SkillImportSource {
  Kind: string
  Path: string
}

export interface SkillImportReq {
  Sources: SkillImportSource[]
  TargetProjectId?: string | undefined
}

export interface SkillImportResult {
  SkillId: string
  Name: string
  Source: string
  Status: string
  Error: string
}

export interface SkillImportResp {
  Items: SkillImportResult[]
}
