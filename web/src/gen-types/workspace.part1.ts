// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AgentModeState } from './aigen.part4';
import { ModelSlot, StoragePolicy } from './aigen.part3';
import { AgentChildRef, CompactionPolicy } from './aigen.part2';
import { ChildSpawnConfig } from './workspace.part3';
import { PromptRef } from './prompt';
import { CardRef } from './card';

export interface ProjectMount {
  Name: string;
  Path: string;
}

export interface ProjectRef {
  Name: string;
  Path: string;
  Root: boolean;
  ActorId?: string | undefined;
  Mounts?: ProjectMount[] | undefined;
  LastOpenedAt?: string | undefined;
  PermissionMode?: string | undefined;
  System?: boolean | undefined;
  AppKind?: string | undefined;
}

export interface WorkspaceMountReq {
  Path: string;
  Name: string;
  AppKind?: string | undefined;
}

export interface WorkspaceUnmountReq {
  ProjectId: string;
}

export interface WorkspaceCreateReq {
  Path: string;
  Name: string;
  InitGit: boolean;
  AppKind?: string | undefined;
}

export interface WorkspaceMountsEvent {
  Mounts: ProjectRef[];
}

export interface WorkspaceAgentsChangedEvent {

}

export interface AgentRuntimeState {
  State?: string | undefined;
  ActiveTurnRef?: string | undefined;
  Error?: string | undefined;
  ApprovalPending?: boolean | undefined;
  PlanApprovalPending?: boolean | undefined;
  AskUserPending?: boolean | undefined;
  GoalSubmitPending?: boolean | undefined;
  CurrentTaskSummary?: string | undefined;
  BoundTaskCardId?: string | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  LastActivity?: string | undefined;
  LastTurnCompletedAt?: string | undefined;
  LastAccessedAt?: string | undefined;
  ThinkLevel?: string | undefined;
  WorktreeID?: string | undefined;
  WorktreeStatus?: string | undefined;
  WorktreeName?: string | undefined;
  MemoryMounted?: boolean | undefined;
  PermissionMode?: string | undefined;
}

export interface ComposerHistoryItem {
  Id: string;
  Text: string;
  Timestamp: string;
  Starred: boolean;
}

export interface AgentListItem {
  Id: string;
  ActorId: string;
  DisplayName: string;
  AgentKind: string;
  ProjectId: string;
  LoadState?: string | undefined;
  Mode?: AgentModeState | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  ThinkLevel?: string | undefined;
  Title?: string | undefined;
  LastActivity?: string | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  Degraded?: boolean | undefined;
  DegradedReason?: string | undefined;
  Runtime?: AgentRuntimeState | undefined;
  ComposerHistory?: ComposerHistoryItem[] | undefined;
  ParentAgentId?: string | undefined;
  LifecycleScope?: string | undefined;
  BoundAppId?: string | undefined;
  BoundAppSlot?: string | undefined;
  ConversationTarget?: string | undefined;
  DeletionStatus?: string | undefined;
  Children?: AgentChildRef[] | undefined;
  PermissionMode?: string | undefined;
  CanDelete: boolean;
}

export interface WorkspaceAgentListState {
  Version: number;
  Full: boolean;
  Items: AgentListItem[];
}

export interface WorkspaceAgentListStateEvent {
  State: WorkspaceAgentListState;
}

export interface WorkspaceAgentStatusUpdateReq {
  AgentActorId: string;
  State?: string | undefined;
  ActiveTurnRef?: string | undefined;
  Error?: string | undefined;
  ApprovalPending?: boolean | undefined;
  PlanApprovalPending?: boolean | undefined;
  AskUserPending?: boolean | undefined;
  GoalSubmitPending?: boolean | undefined;
  CurrentTaskSummary?: string | undefined;
  BoundTaskCardId?: string | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  LastActivity?: string | undefined;
  LastTurnCompletedAt?: string | undefined;
  ThinkLevel?: string | undefined;
  Title?: string | undefined;
  WorktreeID?: string | undefined;
  WorktreeStatus?: string | undefined;
  WorktreeName?: string | undefined;
  MemoryMounted?: boolean | undefined;
  PermissionMode?: string | undefined;
  Primary?: ModelSlot | undefined;
}

export interface WorkspaceAddMountReq {
  ProjectId: string;
  MountName: string;
  MountPath: string;
}

export interface WorkspaceRemoveMountReq {
  ProjectId: string;
  MountName: string;
}

export interface WorkspaceListAgentsReq {
  ProjectId?: string | undefined;
  Query?: string | undefined;
  CallerAgentId?: string | undefined;
  ParentAgentId?: string | undefined;
  ChildrenOnly?: boolean | undefined;
}

export interface WorkspaceCreateAgentReq {
  ProjectId?: string | undefined;
  DisplayName?: string | undefined;
  AgentKind?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  WorktreeID?: string | undefined;
}

export interface WorkspaceUpdateAgentReq {
  AgentId: string;
  DisplayName?: string | undefined;
  Title?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  ComposerHistory?: ComposerHistoryItem[] | undefined;
}

export interface WorkspaceDeleteAgentReq {
  AgentId: string;
}

export interface WorkspaceCloneAgentReq {
  SourceAgentId: string;
  DisplayName: string;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  ForkAtTurnId?: string | undefined;
}

export interface ProjectSpawnAgentReq {
  SpawnName: string;
  ProjectId: string;
  AgentKind: string;
  WorkspaceId?: string | undefined;
  ActorId?: string | undefined;
  DisplayName?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  WorktreeID?: string | undefined;
  CloneSourceActorID?: string | undefined;
  ParentAgentID?: string | undefined;
  GoalCondition?: string | undefined;
  InterpretedGoal?: string | undefined;
  BoundTaskCardID?: string | undefined;
  GoalMaxTurns?: number | undefined;
  PromptPrelude?: string | undefined;
  PermissionMode?: string | undefined;
  ChildConfig?: ChildSpawnConfig | undefined;
  ExtraBundleIDs?: string[] | undefined;
}

export interface ProjectSpawnAgentResp {
  ActorId: string;
}

export interface AgentKindInfo {
  Kind: string;
  DisplayName: string;
  UserCreatable: boolean;
  SystemManaged: boolean;
  Builtin: boolean;
  NamePool?: string[] | undefined;
  RandomName?: RandomNameConfig | undefined;
}

export interface RandomNameConfig {
  Enabled: boolean;
  Prefixes?: string[] | undefined;
  Suffixes?: string[] | undefined;
}

export interface AgentKindConfig {
  Kind: string;
  DisplayName: string;
  UserCreatable: boolean;
  SystemManaged: boolean;
  RolePromptRef: PromptRef;
  SystemFragmentRefs?: PromptRef[] | undefined;
  DefaultBundleIDs?: string[] | undefined;
  RemovedBundleIDs?: string[] | undefined;
  AutoAllowTools?: string[] | undefined;
  AutoAllowCandidates?: string[] | undefined;
  EnvironmentContext?: Record<string, string> | undefined;
  NamePool?: string[] | undefined;
  RandomName?: RandomNameConfig | undefined;
  SkillIDs?: string[] | undefined;
  DefaultCardRefs?: CardRef[] | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  StoragePolicy?: StoragePolicy | undefined;
  MaxTurns?: number | undefined;
}

export interface WorkspaceSaveAgentKindConfigReq {
  Kind: string;
  DisplayName: string;
  UserCreatable: boolean;
  SystemManaged: boolean;
  RolePromptRef: PromptRef;
  SystemFragmentRefs?: PromptRef[] | undefined;
  DefaultBundleIDs?: string[] | undefined;
  AutoAllowTools?: string[] | undefined;
  AutoAllowCandidates?: string[] | undefined;
  EnvironmentContext?: Record<string, string> | undefined;
  NamePool?: string[] | undefined;
  RandomName?: RandomNameConfig | undefined;
  SkillIDs?: string[] | undefined;
  DefaultCardRefs?: CardRef[] | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  StoragePolicy?: StoragePolicy | undefined;
  MaxTurns?: number | undefined;
}

export interface WorkspaceListAgentKindsResp {
  Items: AgentKindInfo[];
}

export interface WorkspaceGetAgentKindConfigReq {
  Kind: string;
}

export interface WorkspaceListAgentKindConfigsResp {
  Items: AgentKindConfig[];
}

export interface ProjectRefListResp {
  Items: ProjectRef[];
}

export interface ActorContextSnapshot {
  ActorId: string;
  ActorType: string;
  SessionId: string;
  TrustLevel: string;
  AuthenticatedAs: string;
}

export interface AccountSnapshot {
  AccountId: string;
  DisplayName: string;
  Roles: string[];
  DefaultActor: ActorContextSnapshot;
}
