// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SessionSnapshot {
  SessionId: string;
  ConnectionId: string;
  AccountId: string;
  ClientKind: string;
  DisplayName: string;
  TrustLevel: string;
  ActorType: string;
  Online: boolean;
}

export interface AccountPreferencesSnapshot {
  AccountId: string;
  Version: number;
  Preferences: Record<string, string>;
}

export interface SaveAccountPreferencesCommand {
  RequestId: string;
  AccountId: string;
  Version: number;
  Preferences: Record<string, string>;
}

export interface XY {
  X: number;
  Y: number;
}

export interface WH {
  W: number;
  H: number;
}

export interface WorkspacePanelState {
  Mode: string;
  Visible: boolean;
  Pos: XY;
  Size: WH;
  ZIndex: number;
  DockZone: string;
}

export interface WorkspacePanelsState {
  Version: number;
  Panels: Record<string, WorkspacePanelState>;
  ZoneTabOrder?: Record<string, string[]> | undefined;
}

export interface WorkspaceDockState {
  LeftWidth: number;
  RightWidth: number;
  BottomHeight: number;
  LeftTopPct: number;
  RightTopPct: number;
  BottomLeftPct: number;
  WindowW: number;
  WindowH: number;
}

export interface WorkspaceShellLayout {
  Version: number;
  LayoutJson: string;
  ActiveViewId?: string | undefined;
}

export interface WorkspaceAIShellState {
  SidebarOrder?: string[] | undefined;
  SidebarPinned?: string[] | undefined;
  AgentOrder?: string[] | undefined;
  LayoutJson?: string | undefined;
  SelectedAgentId?: string | undefined;
  PreviousAgentId?: string | undefined;
}

export interface WorkspaceProjectBrowserState {
  SortMode?: string | undefined;
}

export interface WorkspaceExplorerState {
  ActiveProjectId?: string | undefined;
  SelectedPath?: string | undefined;
  ExpandedPaths?: string[] | undefined;
}

export interface WorkspaceUIModel {
  WorkspaceId: string;
  Version: number;
  SchemaVersion: number;
  Layout: WorkspaceShellLayout;
  Panels: WorkspacePanelsState;
  Dock: WorkspaceDockState;
  AiShell: WorkspaceAIShellState;
  ProjectCardBrowser: WorkspaceProjectBrowserState;
  Explorer: WorkspaceExplorerState;
}

export interface SaveWorkspaceLayoutCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  Layout: WorkspaceShellLayout;
}

export interface SaveWorkspacePanelsCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  Panels: WorkspacePanelsState;
}

export interface SaveWorkspaceDockCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  Dock: WorkspaceDockState;
}

export interface SaveWorkspaceAIShellCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  AiShell: WorkspaceAIShellState;
}

export interface SaveWorkspaceProjectCardBrowserCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  ProjectCardBrowser: WorkspaceProjectBrowserState;
}

export interface SaveWorkspaceExplorerCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  Explorer: WorkspaceExplorerState;
}

export interface GitFileStatus {
  Path: string;
  Staging: string;
  Worktree: string;
}

export interface GitCommitInfo {
  Hash: string;
  Short: string;
  Message: string;
  Author: string;
  Email: string;
  When: string;
  Parents?: string[] | undefined;
}

export interface GitBranchInfo {
  Name: string;
  Current: boolean;
  Hash: string;
  IsRemote: boolean;
  Remote: string;
}

export interface WorkspaceGitStatusReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitStatusResp {
  Branch: string;
  Files: GitFileStatus[];
  IsClean: boolean;
  IsGit: boolean;
}

export interface WorkspaceGitLogReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Limit: number;
  Branch?: string | undefined;
  All?: boolean | undefined;
}

export interface WorkspaceGitLogResp {
  Commits: GitCommitInfo[];
}

export interface WorkspaceGitDiffReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  FilePath: string;
  CommitHash: string;
  BaseCommitHash?: string | undefined;
}

export interface WorkspaceGitDiffResp {
  Diff: string[];
}

export interface WorkspaceGitAddReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Paths: string[];
}

export interface WorkspaceGitCommitReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Message: string;
}

export interface WorkspaceGitCommitResp {
  Hash: string;
  Short: string;
}

export interface WorkspaceGitPushReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Remote: string;
}
