// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ProjectWorktreeBoundCheckReq {
  AgentActorID: string;
}

export interface ProjectWorktreeBoundCheckResp {
  Bound: boolean;
  WorktreePath: string;
}

export interface ProjectWorkflowCreateWorktreeReq {
  WorkflowMapID: string;
  AgentActorID: string;
  BaseRef?: string | undefined;
}

export interface ProjectWorkflowCreateWorktreeResp {
  WorktreeID: string;
}

export interface ProjectWorktreeMergeToParentReq {
  ChildWorktreeID: string;
  ParentWorktreeID: string;
}

export interface ProjectWorktreeMergeToParentResp {
  Status: string;
  ConflictFiles: string[];
}

export interface ProjectWorktreeRebaseToParentReq {
  ChildWorktreeID: string;
  ParentWorktreeID: string;
}

export interface ProjectWorktreeRebaseToParentResp {
  Status: string;
  ConflictFiles: string[];
}

export interface ProjectWorkflowStopMergeWorktreeReq {
  WorktreeID: string;
  BaseBranch?: string | undefined;
  RequireSynced?: boolean | undefined;
}

export interface ProjectWorkflowStopMergeWorktreeResp {
  Status: string;
  ConflictFiles: string[];
  ResiduePath?: string | undefined;
}

export interface ProjectWorktreeDiscardByIDReq {
  WorktreeID: string;
  Force?: boolean | undefined;
}

export interface ProjectWorktreeDiscardByIDResp {
  Status: string;
}

export interface ProjectWatchFileReq {
  Path: string;
  Remove?: boolean | undefined;
}

export interface ProjectWorktreeCopyReq {
  WorktreeID: string;
  Sources: string[];
  DestDir?: string | undefined;
  Force?: boolean | undefined;
}

export interface ProjectWorktreeCopyResult {
  Source: string;
  Dest: string;
  Overwritten: boolean;
}

export interface ProjectWorktreeCopySkipped {
  Source: string;
  Reason: string;
}

export interface ProjectWorktreeCopyResp {
  Copied: ProjectWorktreeCopyResult[];
  Skipped: ProjectWorktreeCopySkipped[];
}

export interface ProjectWorktreeVerifyMergedReq {
  ChildWorktreeID: string;
  ParentWorktreeID: string;
}

export interface ProjectWorktreeVerifyMergedResp {
  Status: string;
  Branch: string;
  ChildHead: string;
  ParentHead: string;
}

export interface ProjectWorktreeCleanCheckReq {
  AgentActorID: string;
}

export interface ProjectWorktreeCleanCheckResp {
  Clean: boolean;
  DirtyFiles: string[];
}

export interface ProjectNoGitModeGetReq {

}

export interface ProjectNoGitModeGetResp {
  NoGitMode: boolean;
  HasGitRepo: boolean;
}

export interface ProjectNoGitModeSetReq {
  NoGitMode: boolean;
}

export interface ProjectNoGitModeSetResp {
  NoGitMode: boolean;
}

export interface ProjectDefaultBundlesGetReq {

}

export interface ProjectDefaultBundlesGetResp {
  BundleIDs: string[];
}

export interface ProjectDefaultBundlesSetReq {
  BundleIDs: string[];
}

export interface ProjectDefaultBundlesSetResp {
  BundleIDs: string[];
}
