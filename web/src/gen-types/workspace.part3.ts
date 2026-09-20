// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { GitBranchInfo } from './workspace.part2';
import { ProjectRef } from './workspace.part1';
import { ContentBlock } from './aigen.part1';

export interface WorkspaceGitPullReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Remote: string;
}

export interface WorkspaceGitBranchReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitBranchResp {
  Branches: GitBranchInfo[];
}

export interface WorkspaceGitCheckoutReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Branch: string;
  Create: boolean;
}

export interface WorkspaceGitResetReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Paths?: string[] | undefined;
}

export interface GitStashInfo {
  Index: number;
  Message: string;
  Hash: string;
  When: string;
}

export interface WorkspaceGitStashSaveReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Message?: string | undefined;
}

export interface WorkspaceGitStashPopReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Index?: number | undefined;
}

export interface WorkspaceGitStashListReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitStashListResp {
  Stashes: GitStashInfo[];
}

export interface WorkspaceGitStashDropReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Index?: number | undefined;
}

export interface GitRemoteInfo {
  Name: string;
  Urls: string[];
}

export interface WorkspaceGitRemoteListReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitRemoteListResp {
  Remotes: GitRemoteInfo[];
}

export interface WorkspaceGitRemoteAddReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
  Url: string;
}

export interface WorkspaceGitRemoteRemoveReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
}

export interface GitBlameLine {
  Hash: string;
  Author: string;
  Email: string;
  When: string;
  Line: number;
  Content: string;
}

export interface WorkspaceGitBlameReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  FilePath: string;
}

export interface WorkspaceGitBlameResp {
  Lines: GitBlameLine[];
}

export interface WorkspaceGitConfigGetReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Key: string;
}

export interface WorkspaceGitConfigGetResp {
  Value: string;
}

export interface WorkspaceGitConfigSetReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Key: string;
  Value: string;
  Global: boolean;
}

export interface WorkspaceUpdateProjectReq {
  ProjectId: string;
  Name?: string | undefined;
  LastOpenedAt?: string | undefined;
  PermissionMode?: string | undefined;
  ClearPermissionMode?: boolean | undefined;
  AppKind?: string | undefined;
  ClearAppKind?: boolean | undefined;
}

export interface WorkspaceUpdateProjectResp {
  Project: ProjectRef;
}

export interface WorkspaceLoadAgentReq {
  AgentId: string;
}

export interface WorkspaceLogEntry {
  Timestamp: string;
  Level: string;
  Caller: string;
  Message: string;
  Fields?: Record<string, unknown> | undefined;
  CallerFile?: string | undefined;
  CallerLine?: number | undefined;
}

export interface WorkspaceLogsQueryReq {
  Source?: string | undefined;
  Level?: string | undefined;
  Caller?: string | undefined;
  Message?: string | undefined;
  Before?: string | undefined;
  Limit?: number | undefined;
  Tail?: number | undefined;
}

export interface WorkspaceLogsQueryResp {
  Items: WorkspaceLogEntry[];
  Truncated?: boolean | undefined;
  NextBefore?: string | undefined;
}

export interface ChildSpawnConfig {
  ParentActorID: string;
  ParentTurnID: string;
  ParentStepID: string;
  ParentToolUseID: string;
  Task: string;
  Prompt: string;
  MaxIterations: number;
  HotContext?: ContentBlock[] | undefined;
}

export interface WorkspaceAgentAccessReq {
  AgentActorId: string;
}
