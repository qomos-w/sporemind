// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ProjectFileListReq {
  Path: string;
}

export interface ProjectFileReadReq {
  Path: string;
}

export interface ProjectInfoRoot {
  Name: string;
  Path: string;
}

export interface ProjectInfoResp {
  Roots: ProjectInfoRoot[];
}

export interface ProjectGitStatusReq {
  Repo?: string | undefined;
}

export interface ProjectGitStatusResp {
  Branch: string;
  MainBranch: string;
  Status: string;
  Log: string;
  Files: ProjectGitFileStatus[];
  UserName?: string | undefined;
  IsGit?: boolean | undefined;
}

export interface ProjectGitFileStatus {
  Path: string;
  Staging: string;
  Worktree: string;
}

export interface ProjectSyncRootsReq {
  Roots: ProjectInfoRoot[];
}

export interface ProjectGitCommitInfo {
  Hash: string;
  Short: string;
  Message: string;
  Author: string;
  Email: string;
  When: string;
  Parents?: string[] | undefined;
}

export interface ProjectGitBranchInfo {
  Name: string;
  Current: boolean;
  Hash: string;
  IsRemote: boolean;
  Remote: string;
}

export interface ProjectGitStashInfo {
  Index: number;
  Message: string;
  Hash: string;
  When: string;
}

export interface ProjectGitRemoteInfo {
  Name: string;
  Urls: string[];
}

export interface ProjectGitBlameLine {
  Hash: string;
  Author: string;
  Email: string;
  When: string;
  Line: number;
  Content: string;
}

export interface ProjectGitLogReq {
  Limit: number;
  Repo?: string | undefined;
}

export interface ProjectGitLogResp {
  Commits: ProjectGitCommitInfo[];
}

export interface ProjectGitDiffReq {
  FilePath: string;
  CommitHash: string;
  Repo?: string | undefined;
}

export interface ProjectGitDiffResp {
  Diff: string[];
}

export interface ProjectGitAddReq {
  Paths: string[];
}

export interface ProjectGitCommitReq {
  Message: string;
}

export interface ProjectGitCommitResp {
  Hash: string;
  Short: string;
}

export interface ProjectGitPushReq {
  Remote: string;
}

export interface ProjectGitPullReq {
  Remote: string;
}

export interface ProjectGitBranchReq {

}

export interface ProjectGitBranchResp {
  Branches: ProjectGitBranchInfo[];
}

export interface ProjectGitCheckoutReq {
  Branch: string;
  Create: boolean;
}

export interface ProjectGitResetReq {
  Paths?: string[] | undefined;
}

export interface ProjectGitStashSaveReq {
  Message?: string | undefined;
  Repo?: string | undefined;
}

export interface ProjectGitStashPopReq {
  Index?: number | undefined;
  Repo?: string | undefined;
}

export interface ProjectGitStashListReq {
  Repo?: string | undefined;
}

export interface ProjectGitStashListResp {
  Stashes: ProjectGitStashInfo[];
}

export interface ProjectGitStashDropReq {
  Index?: number | undefined;
}

export interface ProjectGitRemoteListReq {

}
