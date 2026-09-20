// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ProjectGitBlameLine, ProjectGitRemoteInfo } from './project.part1';

export interface ProjectFileChangedEvent {
  Path: string;
  Kind: string;
}

export interface ProjectGitRemoteListResp {
  Remotes: ProjectGitRemoteInfo[];
}

export interface ProjectGitRemoteAddReq {
  Name: string;
  Url: string;
}

export interface ProjectGitRemoteRemoveReq {
  Name: string;
}

export interface ProjectGitBlameReq {
  FilePath: string;
}

export interface ProjectGitBlameResp {
  Lines: ProjectGitBlameLine[];
}

export interface ProjectGitConfigGetReq {
  Key: string;
}

export interface ProjectGitConfigGetResp {
  Value: string;
}

export interface ProjectGitConfigSetReq {
  Key: string;
  Value: string;
  Global: boolean;
}

export interface ProjectExecuteTimerCardReq {
  CardID: string;
  ProjectID: string;
}

export interface ProjectWorktree {
  ID: string;
  Name: string;
  Path: string;
  Branch: string;
  BaseRef: string;
  Status: string;
  CreatedAt: string;
  LastUsedAt: string;
  ParentWorktreeID?: string | undefined;
  WorkflowMapID?: string | undefined;
}

export interface ProjectWorktreeCreateReq {
  Name: string;
  BaseRef: string;
  ParentWorktreeID?: string | undefined;
  WorkflowMapID?: string | undefined;
}

export interface ProjectWorktreeListReq {

}

export interface ProjectWorktreeListResp {
  Worktrees: ProjectWorktree[];
}

export interface ProjectWorktreeGetReq {
  WorktreeID: string;
}

export interface ProjectWorktreeDiscardReq {
  WorktreeID: string;
  Force?: boolean | undefined;
}

export interface ProjectWorktreeAgentBinding {
  AgentActorID: string;
  WorktreeID: string;
  WorktreePath: string;
  Branch: string;
  Status?: string | undefined;
  Name?: string | undefined;
}

export interface ProjectWorktreeAgentBindingsReq {

}

export interface ProjectWorktreeAgentBindingsResp {
  Bindings: ProjectWorktreeAgentBinding[];
}

export interface ProjectWorktreeReleaseBindingReq {
  AgentActorID: string;
  ForceDelete: boolean;
}

export interface ProjectWorktreeEnterReq {
  Name?: string | undefined;
  BaseRef?: string | undefined;
}

export interface ProjectWorktreeEnterResp {
  Worktree: ProjectWorktree;
  Created: boolean;
}

export interface ProjectWorktreeExitReq {
  Mode: string;
  Force?: boolean | undefined;
  BaseBranch?: string | undefined;
  ParentWorktreeID?: string | undefined;
}

export interface ProjectWorktreeExitResp {
  WorktreeID: string;
  Mode: string;
  Status: string;
}
