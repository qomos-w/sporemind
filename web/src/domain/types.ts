// UI view-model types that have no spore schema equivalent.
// Schema-owned types live in web/src/gen-types/.

export type { ModelUnit } from "../gen-types/aigen";

/** AI shell content modes — controls which panel is shown as the main area. */
export type ContentMode = 'conversation' | 'topology' | 'multiconsole' | 'notes';

export type DiagnosticSeverity = "error" | "warning" | "info";

export interface MountSnapshot {
  Name: string;
  Path: string;
  Permission: string;
}

export interface ProjectSnapshot {
  ProjectID: string;
  Name: string;
  RootPath: string;
  Mounts?: MountSnapshot[];
  IsOpen: boolean;
  GitBranch?: string;
  GitDirty?: boolean;
  LastOpenedAt?: string;
  PermissionMode?: string;
  System?: boolean;
  AppKind?: string;
}

export interface HistoryConversationSnapshot {
  SessionID: string;
  Title: string;
  ProjectID?: string;
  ProjectName?: string;
  DirectoryName?: string;
  Status: "active" | "closed";
  CanResurrect?: boolean;
  LastMessagePreview?: string;
  LastActivityAt?: string;
  AgentName?: string;
}

export interface FileEntry {
  Name: string;
  Path: string;
  Kind: "file" | "directory";
  Size?: number;
  Children?: FileEntry[];
}

export interface FileTreeSnapshot {
  RootPath: string;
  Entries: FileEntry[];
}

export interface PathEntry {
  Name: string;
  Path: string;
  Kind: "file" | "directory";
}

export interface BrowsePathSnapshot {
  Path: string;
  Parent?: string;
  Entries: PathEntry[];
}

export interface FileContentSnapshot {
  Path: string;
  Content: string;
  Size: number;
  IsBinary: boolean;
}

export interface SkillSnapshot {
  ID: string;
  Name: string;
  Description: string;
  Tags: string[];
  Permission: string;
  ToolFilter: string[];
  SubAgentType: string;
  Model: string;
  Source: string;
  Origin: string;
  Version?: string;
  IsOverridden: boolean;
  IsEnabled: boolean;
}

export type ProviderType = 'auto' | 'anthropic' | 'openai' | 'gemini'

export interface ProviderModel {
  id: string
  name: string
  maxTokens: number
  maxContextLength: number
  inputRate: number
  outputRate: number
  protocol?: 'anthropic' | 'openai' | 'gemini'
}

export interface ProviderConfig {
  id: string
  type: ProviderType
  name: string
  baseURL: string
  apiKey: string
  model: string
  models: ProviderModel[]
  maxConcurrency?: number
  hasAuthToken?: boolean
  status: 'connected' | 'disconnected' | 'error'
}

export interface AddProviderCommand {
  type: ProviderType
  name: string
  baseURL: string
  apiKey: string
  model: string
  models: ProviderModel[]
  maxConcurrency?: number
}

export interface EditProviderCommand {
  id: string
  type: ProviderType
  name: string
  baseURL: string
  apiKey: string
  model: string
  models: ProviderModel[]
}

import type { ManualCallableUnit } from '../gen-clients/system/types'

export interface CallableUnit {
  model: string
  endpoint: string
  providerName: string
  intents?: string[]
  roles?: string[]
}

export interface AggregatorConfig {
  id: string
  actorId: string
  name: string
  version: number
  units: CallableUnit[]
  status: 'active' | 'idle' | 'error'
  manualUnits?: ManualCallableUnit[]
}

export interface CallableStream {
  start?: string
  delta?: string
  end?: string
}
