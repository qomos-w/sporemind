// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

export interface AIAggregatorStatusResp {
  Id: string;
  Name: string;
  Version: number;
  UnitCount: number;
  Disabled?: boolean | undefined;
  Units: AICallableUnitView[];
}

export interface AICallableUnitView {
  Id: string;
  Model: string;
  Endpoint: string;
  ProviderName: string;
  Protocol: string;
  aggregatorID?: string | undefined;
  UserAgent?: string | undefined;
  MaxContextLength?: number | undefined;
  Modality?: string | undefined;
  CooldownUntil?: number | undefined;
  DisableUntil?: number | undefined;
  Disabled?: boolean | undefined;
  CooldownReason?: string | undefined;
  ConsecutiveFailures?: number | undefined;
  OnDemand?: boolean | undefined;
  HealthState?: string | undefined;
  HealthReason?: string | undefined;
  LastFailureAt?: number | undefined;
  RecoveryMode?: string | undefined;
  DispatchActivity?: DispatchActivity | undefined;
}

export interface AIManagerAggregatorConfigureReq {
  Id: string;
  Name: string;
  Units?: ManualCallableUnit[] | undefined;
  Strategy?: string | undefined;
}

export interface AIManagerAggregatorConfigureResp {
}

export interface AIManagerAggregatorGetReq {
  Id: string;
}

export interface AIManagerAggregatorGetResp {
  Id: string;
  Name: string;
  Strategy?: string | undefined;
  Disabled?: boolean | undefined;
  Units: ManualCallableUnit[];
}

export interface AIManagerAggregatorSetDisabledReq {
  Id: string;
  Disabled: boolean;
}

export interface AIManagerAggregatorSetDisabledResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerConfigExportResp {
  Data: string;
}

export interface AIManagerConfigImportReq {
  Data: string;
}

export interface AIManagerConfigImportResp {
}

export interface AIManagerFetchOpenRouterModelsResp {
  Items: ModelDefault[];
}

export interface AIManagerListUnitsReq {
  Modality?: string | undefined;
}

export interface AIManagerListUnitsResp {
  Items: ManualCallableUnit[];
}

export interface AIManagerModelDefaultsGetResp {
  Items: ModelDefault[];
}

export interface AIManagerModelDefaultsSetReq {
  Items: ModelDefault[];
}

export interface AIManagerModelDefaultsSetResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerProviderConfigureReq {
  Name: string;
  PreviousName?: string | undefined;
  Kind: string;
  Endpoint: string;
  Models: ProviderModel[];
  AuthToken?: string | undefined;
  MaxConcurrency?: number | undefined;
  UserAgent?: string | undefined;
  Proxy?: string | undefined;
  DisableWindows?: ProviderDisableWindow[] | undefined;
  IsTokenPlan?: boolean | undefined;
  TokenPlanExpiresAt?: string | undefined;
  TokenPlanRemainingPct?: number | undefined;
  TokenPlanWindowMs?: number | undefined;
}

export interface AIManagerProviderConfigureResp {
}

export interface AIManagerProviderFetchModelsReq {
  Name: string;
  Endpoint: string;
  Kind: string;
  AuthToken?: string | undefined;
  Proxy?: string | undefined;
}

export interface AIManagerProviderFetchModelsResp {
  Models: FetchedModel[];
}

export interface AIManagerProviderRecordProbeReq {
  ProviderName: string;
  Model: string;
  Ok: boolean;
  LatencyMs?: number | undefined;
  Error?: string | undefined;
}

export interface AIManagerProviderRecordProbeResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerProviderResetHealthReq {
  ProviderName: string;
}

export interface AIManagerProviderResetHealthResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerProviderSetDisabledReq {
  ProviderName: string;
  Model?: string | undefined;
  Disabled: boolean;
}

export interface AIManagerProviderSetDisabledResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerProviderSetTokenPlanReq {
  ProviderName: string;
  ExpiresAt?: string | undefined;
  RemainingPct?: number | undefined;
  WindowMs?: number | undefined;
  IsTokenPlan?: boolean | undefined;
}

export interface AIManagerProviderSetTokenPlanResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerUnitHealthListResp {
  Items: AICallableUnitView[];
}

export interface AIStatsAggregatesReq {
  WorkspaceID?: string | undefined;
}

export interface AIStatsAggregatesResp {
  Workspace: AIStatsCounters;
  Models: AIStatsModelAggregate[];
  Providers: AIStatsProviderAggregate[];
}

export interface AIStatsBackfillReq {
  WorkspaceId: string;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  SessionId?: string | undefined;
}

export interface AIStatsBackfillResp {
  RecordCount: number;
}

export interface AIStatsBucket {
  Start: number;
  End: number;
  Requests: number;
  Errors: number;
  InputTokens: number;
  OutputTokens: number;
  CacheRead: number;
  CacheWrite: number;
  ReasoningTokens: number;
  Cost: number;
  LatencyP50: number;
  LatencyP95: number;
  LatencySumMs: number;
  TtftP50: number;
  ErrorCodes: Record<string, number>;
  Rollup?: boolean | undefined;
  Grain?: string | undefined;
}

export interface AIStatsCleanupReq {
  dryRun: boolean;
}

export interface AIStatsCleanupResp {
  found: number;
  deleted: number;
  remaining: number;
}

export interface AIStatsCostConfigureReq {
  Rate: AIStatsCostRate;
}

export interface AIStatsCostConfigureResp {
  Version: number;
}

export interface AIStatsCostListReq {
  Provider?: string | undefined;
  Model?: string | undefined;
}

export interface AIStatsCostListResp {
  Rates: AIStatsCostRate[];
}

export interface AIStatsCostRate {
  Provider: string;
  Model: string;
  Version: number;
  UpdatedAt: string;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite?: number | undefined;
  Tiers?: AIStatsCostTier[] | undefined;
}

export interface AIStatsCostTier {
  InputTokensAbove: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite?: number | undefined;
}

export interface AIStatsCounters {
  RequestCount: number;
  ErrorCount: number;
  LatencySumMs: number;
  InputTokens: number;
  OutputTokens: number;
  TotalTokens: number;
  CacheCreationInputTokens: number;
  CacheReadInputTokens: number;
  ReasoningTokens: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite: number;
  CostTotal: number;
}

export interface AIStatsExportReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  Format?: string | undefined;
}

export interface AIStatsExportResp {
  Data: string;
}

export interface AIStatsModelAggregate {
  Provider: string;
  Model: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsModelStat {
  Provider: string;
  Model: string;
  Requests: number;
  Errors: number;
  InputTokens: number;
  OutputTokens: number;
  CacheRead: number;
  CacheWrite: number;
  CostTotal: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite: number;
  LatencyP95: number;
  TtftP50: number;
  LatencySumMs: number;
}

export interface AIStatsProviderAggregate {
  Provider: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsQueryReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  Limit?: number | undefined;
  Offset?: number | undefined;
  Order?: string | undefined;
}

export interface AIStatsQueryResp {
  Records: AIStatsRecord[];
  Counters: AIStatsCounters;
  Total: number;
}

export interface AIStatsRecord {
  Id: string;
  WorkspaceId: string;
  ProjectId: string;
  AgentId: string;
  SessionId: string;
  TurnId: string;
  RequestId: string;
  Provider: string;
  Model: string;
  ResponseModel?: string | undefined;
  ResponseId?: string | undefined;
  ClientRequestId?: string | undefined;
  Usage?: UsageData | undefined;
  StopReason?: string | undefined;
  ErrorCode?: string | undefined;
  ErrorMessage?: string | undefined;
  LatencyMs?: number | undefined;
  FirstTokenMs?: number | undefined;
  StartedAt: string;
  CompletedAt: string;
  Tags?: Record<string, string> | undefined;
}

export interface AIStatsRollupReq {
  dryRun: boolean;
  onlyDay?: string | undefined;
}

export interface AIStatsRollupResp {
  dryRun: boolean;
  daysExamined: number;
  daysComputed: number;
  daysExisting: number;
  recordsDeleted: number;
  monthMarkersCleared: number;
  monthsExamined: number;
  monthsComputed: number;
  monthsExisting: number;
  monthsIncomplete: number;
  monthsNotEligible: number;
}

export interface AIStatsSeriesReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  ModelFilter?: string | undefined;
  BucketMs: number;
}

export interface AIStatsSeriesResp {
  Buckets: AIStatsBucket[];
  ModelStats: AIStatsModelStat[];
  OverallLatencyP50: number;
  OverallLatencyP95: number;
  OverallTtftP50: number;
  OverallLatencySumMs: number;
  OverallOutputTokens: number;
}

export interface Account {
  Id: string;
  Username: string;
  DisplayName: string;
  PasswordHash: string;
  Roles: string[];
  Groups: string[];
  Status: string;
  CreatedAt: string;
}

export interface AccountCreateReq {
  Username: string;
  Password: string;
  DisplayName?: string | undefined;
  Roles?: string[] | undefined;
  Groups?: string[] | undefined;
}

export interface AccountDeleteReq {
  Id: string;
}

export interface AccountListResp {
  Items: AccountView[];
}

export interface AccountPreferencesSnapshot {
  AccountId: string;
  Version: number;
  Preferences: Record<string, string>;
}

export interface AccountResetPasswordReq {
  Id: string;
  Password: string;
}

export interface AccountSnapshot {
  AccountId: string;
  DisplayName: string;
  Roles: string[];
  DefaultActor: ActorContextSnapshot;
}

export interface AccountUpdateReq {
  Id: string;
  DisplayName?: string | undefined;
  Roles?: string[] | undefined;
  Groups?: string[] | undefined;
  Status?: string | undefined;
}

export interface AccountView {
  Id: string;
  Username: string;
  DisplayName: string;
  Roles: string[];
  Groups: string[];
  Status: string;
  CreatedAt: string;
}

export interface ActiveSchedulerEntry {
  SchedulerCardID: string;
  SchedulerName: string;
}

export interface ActiveWorkflow {
  MapCardId: string;
  WorktreeID?: string | undefined;
}

export interface ActorContextSnapshot {
  ActorId: string;
  ActorType: string;
  SessionId: string;
  TrustLevel: string;
  AuthenticatedAs: string;
}

export interface AgentCapabilityBinding {
  Callables: string[];
  ProjectScope?: string | undefined;
}

export interface AgentCaptureProfileReq {
  Profile: string;
  Seconds?: number | undefined;
  Debug?: number | undefined;
  Diff?: boolean | undefined;
  TopN?: number | undefined;
}

export interface AgentCaptureProfileResp {
  Profile: string;
  Text?: string | undefined;
  Truncated?: boolean | undefined;
  Error?: string | undefined;
}

export interface AgentChatCancelPendingReq {
  MessageId: string;
}

export interface AgentChatCancelPendingResp {
  Cancelled: boolean;
}

export interface AgentChatSubmitReq {
  Text: string;
  Unit?: ModelUnit | undefined;
  Title?: string | undefined;
  ThinkingBudget?: number | undefined;
  ReasoningEffort?: string | undefined;
  Attachments?: AttachmentEntry[] | undefined;
  Images?: ImageEntry[] | undefined;
  Meta?: string | undefined;
}

export interface AgentChatSubmitResp {
  TurnActorId: string;
  MessageId?: string | undefined;
  Idx?: number | undefined;
  Timestamp?: string | undefined;
}

export interface AgentChildRef {
  Id: string;
  ActorId: string;
  ParentAgentId?: string | undefined;
  DisplayName?: string | undefined;
  AgentKind?: string | undefined;
  LifecycleScope?: string | undefined;
  Status?: string | undefined;
  ConversationTarget?: string | undefined;
  ToolUseId?: string | undefined;
  LastActivity?: string | undefined;
}

export interface AgentCompactionConfigureReq {
  CompactionPolicy?: CompactionPolicy | undefined;
  ResetSummary?: boolean | undefined;
}

export interface AgentCompactionConfigureResp {
  CompactionPolicy: CompactionPolicy;
  SummarySegments: SummarySegment[];
  EstimatedTokens?: number | undefined;
  Source?: string | undefined;
}

export interface AgentComponentListReq {
}

export interface AgentComponentListResp {
  Items: AgentComponentMount[];
}

export interface AgentComponentMount {
  MountId: string;
  CardId: string;
  Kind?: string | undefined;
  Version?: number | undefined;
  Enabled?: boolean | undefined;
  Order?: number | undefined;
  Scope?: string | undefined;
  Title?: string | undefined;
  Icon?: string | undefined;
  Visual?: ComponentVisual | undefined;
  MountedAt?: string | undefined;
  UpdatedAt?: string | undefined;
}

export interface AgentComponentMountReq {
  CardId: string;
  Enabled?: boolean | undefined;
  Order?: number | undefined;
  Scope?: string | undefined;
}

export interface AgentComponentMountResp {
  Mount: AgentComponentMount;
}

export interface AgentComponentSetEnabledReq {
  CardId: string;
  Enabled: boolean;
}

export interface AgentComponentSetEnabledResp {
  Mount: AgentComponentMount;
}

export interface AgentComponentSnapshot {
  Revision: number;
  TurnId?: string | undefined;
  Mounts: AgentComponentMount[];
  Prompts: ComponentPromptContribution[];
  Tools: ComponentToolContribution[];
  Diagnostics?: ComponentDiagnostic[] | undefined;
}

export interface AgentComponentSnapshotReq {
}

export interface AgentComponentSnapshotResp {
  Snapshot: AgentComponentSnapshot;
}

export interface AgentComponentUnmountReq {
  CardId: string;
  Confirm?: boolean | undefined;
}

export interface AgentComponentUnmountResp {
  CardId: string;
}

export interface AgentConfigureReq {
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  Title?: string | undefined;
}

export interface AgentFrontendDebugReq {
  Operation?: string | undefined;
  Script?: string | undefined;
}

export interface AgentFrontendDebugResp {
  Result: unknown;
  Error?: string | undefined;
}

export interface AgentGetSessionResp {
  Turns: Turn[];
  ActiveTurn?: TurnStatus | undefined;
  ActiveTurnEvents?: TurnEvent[] | undefined;
}

export interface AgentInspectActorReq {
  ActorPath: string;
}

export interface AgentInspectActorResp {
  ActorID: string;
  ActorType?: string | undefined;
  CellState?: string | undefined;
  BusinessState?: string | undefined;
  OwnerQueueDepth?: number | undefined;
  OwnerQueueCapacity?: number | undefined;
  SystemQueueDepth?: number | undefined;
  SystemQueueCapacity?: number | undefined;
  ReplyQueueDepth?: number | undefined;
  ReplyQueueCapacity?: number | undefined;
  PendingInvokes?: number | undefined;
  Invoke?: InvokeDiagnostics | undefined;
  Health?: string | undefined;
  Error?: string | undefined;
}

export interface AgentInvokeCallableReq {
  Service: string;
  CallID: string;
  Payload: Record<string, unknown>;
  AppID?: string | undefined;
  AgentID?: string | undefined;
  Role?: string | undefined;
  ProjectID?: string | undefined;
  RequestID?: string | undefined;
}

export interface AgentInvokeCallableResp {
  Result: unknown;
  Error?: string | undefined;
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

export interface AgentKindInfo {
  Kind: string;
  DisplayName: string;
  UserCreatable: boolean;
  SystemManaged: boolean;
  Builtin: boolean;
  NamePool?: string[] | undefined;
  RandomName?: RandomNameConfig | undefined;
}

export interface AgentListCallablesReq {
  Query?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentListCallablesResp {
  Items: CallableInterface[];
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

export interface AgentMessage {
  Id: string;
  FromAgentId: string;
  FromName: string;
  ToAgentId: string;
  Text: string;
  MessageType: string;
  Timestamp: string;
}

export interface AgentMessageListResp {
  Items: AgentMessage[];
}

export interface AgentMessageReadItem {
  Seq: number;
  Role: string;
  Content: string;
}

export interface AgentMessageReadReq {
  ToAgentId: string;
  Limit?: number | undefined;
  BeforeSeq?: number | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentMessageReadResp {
  Items: AgentMessageReadItem[];
  HasMore: boolean;
}

export interface AgentMessageSendReq {
  ToAgentId: string;
  Text: string;
  MessageType?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentMessageSendResp {
  Sent: boolean;
}

export interface AgentMessagesListReq {
  SinceIdx?: number | undefined;
  BeforeIdx?: number | undefined;
  Limit?: number | undefined;
}

export interface AgentMessagesListResp {
  Messages: ChatMessage[];
  HasMore: boolean;
}

export interface AgentModeState {
  BoundTaskCardId?: string | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  MemoryMounted?: boolean | undefined;
}

export interface AgentModesUnloadAllReq {
}

export interface AgentModesUnloadAllResp {
  Unloaded: string[];
}

export interface AgentOpenGlobalBrowserReq {
  Url: string;
}

export interface AgentOpenGlobalBrowserResp {
  Opened: boolean;
  Url: string;
  Error: string;
}

export interface AgentOpenSshSessionReq {
  HostId: string;
}

export interface AgentOpenSshSessionResp {
  SessionId: string;
  HostId: string;
  HostName: string;
  Connected: boolean;
  Error: string;
}

export interface AgentPauseReq {
  ToAgentId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentPauseResp {
  Sent: boolean;
}

export interface AgentRef {
  Id: string;
  ActorId: string;
  ProjectId: string;
  ProjectName?: string | undefined;
  DisplayName: string;
  AgentKind: string;
  LoadState?: string | undefined;
  Mode?: AgentModeState | undefined;
  Status?: string | undefined;
  LastActivity?: string | undefined;
  ActiveTurnRef?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  Title?: string | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  StoragePolicy?: StoragePolicy | undefined;
  Degraded?: boolean | undefined;
  DegradedReason?: string | undefined;
  ParentAgentId?: string | undefined;
  LifecycleScope?: string | undefined;
  DeletionStatus?: string | undefined;
  Children?: AgentChildRef[] | undefined;
  BoundAppId?: string | undefined;
  BoundAppSlot?: string | undefined;
}

export interface AgentRefListResp {
  Items: AgentRef[];
}

export interface AgentResumeReq {
  ToAgentId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentResumeResp {
  Sent: boolean;
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

export interface AgentSchedulerBindReq {
  SchedulerCardID: string;
  SchedulerName?: string | undefined;
}

export interface AgentSchedulerBindResp {
  ActiveScheduler: ActiveSchedulerEntry[];
}

export interface AgentSchedulerUnbindReq {
  SchedulerCardID: string;
}

export interface AgentSchedulerUnbindResp {
  ActiveScheduler: ActiveSchedulerEntry[];
}

export interface AgentSessionExportRangeReq {
  BeforeTurnId?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentSessionExportRangeResp {
  Turns?: Turn[] | undefined;
  Steps?: Step[] | undefined;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  HasMore: boolean;
  TotalTurns?: number | undefined;
  NextBeforeTurnId?: string | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
  NextIdx?: number | undefined;
}

export interface AgentSessionForkReq {
  AtTurnId?: string | undefined;
}

export interface AgentSessionForkResp {
  Session: Session;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  Goal?: SessionGoal | undefined;
  NextIdx?: number | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
}

export interface AgentSessionImportReq {
  Session: Session;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  Goal?: SessionGoal | undefined;
  NextIdx?: number | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
}

export interface AgentSessionImportResp {
  AcceptedTurns: number;
  ActiveHead: number;
}

export interface AgentSessionImportTurnsReq {
  Turns?: Turn[] | undefined;
  Steps?: Step[] | undefined;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  IsFinal?: boolean | undefined;
  SourceAgentId?: string | undefined;
  SourceTotalTurns?: number | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
  NextIdx?: number | undefined;
}

export interface AgentSessionImportTurnsResp {
  AcceptedTurns: number;
}

export interface AgentSessionStatsReq {
  SessionId?: string | undefined;
  Since?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentSessionStatsResp {
  Counters: AIStatsCounters;
  CacheHitRate: number;
  LatestProvider: string;
  LatestModel: string;
}

export interface AgentSessionSummaryReq {
  MaxTurns?: number | undefined;
  KnownStepIDs?: string[] | undefined;
  knownStepEventSeqs?: Record<string, number> | undefined;
  offset?: number | undefined;
  limit?: number | undefined;
}

export interface AgentSessionSummaryResp {
  Turns: Turn[];
  Steps: Step[];
  ActiveTurn: TurnStatus;
  ActiveTurnEvents: TurnEvent[];
  TotalTurns: number;
  HasMoreHistory: boolean;
  AgentState?: string | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  MissingSteps?: Step[] | undefined;
  NextSeq?: number | undefined;
  OpenStepEvents?: StepEvent[] | undefined;
  HasDiscardedSteps?: boolean | undefined;
  Goal?: SessionGoal | undefined;
}

export interface AgentShowPageThumbnailReq {
  Url: string;
  Title?: string | undefined;
}

export interface AgentShowPageThumbnailResp {
  Url: string;
  Title: string;
  FilePath?: string | undefined;
  Error: string;
}

export interface AgentSkillMountReq {
  SkillId: string;
  Args?: string | undefined;
  Context?: string | undefined;
  TurnId?: string | undefined;
}

export interface AgentSkillMountResp {
  MountId: string;
  Body?: string | undefined;
  SkillId?: string | undefined;
}

export interface AgentSkillUseReq {
  SkillId: string;
  Args?: string | undefined;
  Context?: string | undefined;
  TurnId?: string | undefined;
}

export interface AgentSkillUseResp {
  MountId: string;
  Body?: string | undefined;
  SkillId?: string | undefined;
  Warning?: string | undefined;
}

export interface AgentStatusResp {
  ActiveTurnRef?: string | undefined;
  State?: string | undefined;
  PauseKind?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CurrentUnit?: ModelUnit | undefined;
  DisplayName?: string | undefined;
  Title?: string | undefined;
  LastActivity?: string | undefined;
  LastTurnCompletedAt?: string | undefined;
  Goal?: GoalSummary | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  PendingWorkflowStart?: PendingWorkflowStart | undefined;
  ChildLastActivitySec?: number | undefined;
}

export interface AgentStorageConfigureReq {
  StoragePolicy?: StoragePolicy | undefined;
}

export interface AgentStorageConfigureResp {
  StoragePolicy: StoragePolicy;
  Source?: string | undefined;
}

export interface AgentSurfaceBinding {
  Entrypoint: string;
  AgentId?: string | undefined;
  Projections: string[];
  Events: string[];
}

export interface AgentTaskCancelReq {
  Id: string;
}

export interface AgentTaskCancelResp {
  Task: Task;
}

export interface AgentTaskCreateReq {
  Subject: string;
  ActiveForm?: string | undefined;
}

export interface AgentTaskCreateResp {
  Task: Task;
}

export interface AgentTaskDeleteReq {
  Id: string;
}

export interface AgentTaskListResp {
  Tasks: Task[];
}

export interface AgentTaskUpdateReq {
  Id: string;
  Status: string;
}

export interface AgentTaskUpdateResp {
  Task: Task;
}

export interface AgentToolsRefreshNotifyReq {
  AppId: string;
  Reason?: string | undefined;
}

export interface AgentToolsRefreshNotifyResp {
}

export interface AgentTurnsListReq {
  BeforeTurnId?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentTurnsListResp {
  Turns: Turn[];
  HasMore: boolean;
  Steps?: Step[] | undefined;
}

export interface AgentUnloadReq {
  AgentId: string;
}

export interface AgentUnloadResp {
  Unloaded: boolean;
}

export interface AgentWorkflowPauseAllReq {
}

export interface AgentWorkflowPauseAllResp {
  PausedCount: number;
  SkippedCount: number;
}

export interface AgentWorkflowStartReq {
  MapCardId: string;
}

export interface AgentWorkflowStartResp {
  MapCardId: string;
}

export interface AgentWorkflowStopReq {
}

export interface AgentWorkflowStopResp {
  MapCardId: string;
  ResiduePath?: string | undefined;
}

export interface AggregatorChunk {
  Kind: string;
  Text?: string | undefined;
  Usage?: UsageData | undefined;
  ToolUseId?: string | undefined;
  ToolName?: string | undefined;
  InputDelta?: string | undefined;
  Input?: string | undefined;
  StopReason?: string | undefined;
  ResolvedUnit?: ModelUnit | undefined;
}

export interface AggregatorDescriptor {
  Id: string;
  Name: string;
  ActorId: string;
  Strategy?: string | undefined;
  Disabled?: boolean | undefined;
}

export interface AggregatorDescriptorListResp {
  Items: AggregatorDescriptor[];
}

export interface AppAgentBinding {
  Capability?: AgentCapabilityBinding | undefined;
  Surface?: AgentSurfaceBinding | undefined;
  FreeAgent?: FreeAgentBinding | undefined;
  PluginAgents?: PluginAgentBinding[] | undefined;
}

export interface AppAuditRecord {
  Time: string;
  RequestId: string;
  AppId: string;
  Runtime: string;
  AgentId: string;
  Role: string;
  ProjectId: string;
  Callable: string;
  Allowed: boolean;
  Reason: string;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
}

export interface AppBundle {
  Title: string;
  Description?: string | undefined;
  Icon?: string | undefined;
  Color?: string | undefined;
  GuideCardId?: string | undefined;
  Tools: AppBundleTool[];
}

export interface AppBundleTool {
  CallableId: string;
  Effect?: string | undefined;
  Service?: string | undefined;
  ToolName?: string | undefined;
  Stream?: boolean | undefined;
}

export interface AppCallableDescriptor {
  Id: string;
  Description?: string | undefined;
  RequestSchema: string;
  ResponseSchema: string;
  Encoding?: string | undefined;
  Permission?: string | undefined;
  Scope?: string | undefined;
  TimeoutMs?: number | undefined;
  Streaming?: boolean | undefined;
  Effect?: string | undefined;
  Service?: string | undefined;
  ToolName?: string | undefined;
  Expose?: string | undefined;
  Watch?: string[] | undefined;
}

export interface AppDependency {
  Id: string;
  Version: string;
  Hash?: string | undefined;
}

export interface AppEntrypoint {
  Kind: string;
  Id: string;
  Title: string;
  Route?: string | undefined;
  Zone?: string | undefined;
}

export interface AppEventDescriptor {
  Id: string;
  PayloadSchema: string;
  Encoding?: string | undefined;
  Permission?: string | undefined;
}

export interface AppEventMessage {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  Sender?: string | undefined;
}

export interface AppFieldDescriptor {
  Name: string;
  Type: AppTypeDescriptor;
  Description?: string | undefined;
  Private?: boolean | undefined;
  Optional?: boolean | undefined;
}

export interface AppLifecycleEvent {
  Kind: string;
  Id: string;
  Runtime: string;
  State: string;
  Version?: string | undefined;
  Generation?: number | undefined;
  Error?: string | undefined;
}

export interface AppManagerAgentActionReq {
  Id: string;
  Action: string;
  Kind?: string | undefined;
  TargetAgentId?: string | undefined;
  Payload?: Record<string, unknown> | undefined;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  RequestId?: string | undefined;
}

export interface AppManagerAgentActionResp {
  Accepted: boolean;
}

export interface AppManagerAppExportReq {
  ProjectId: string;
  CallerAgentId?: string | undefined;
  AppId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerAppExportResp {
  PackageData: Uint8Array;
  PackageHash: string;
  PublicKey: string;
}

export interface AppManagerAuditReq {
  AppId?: string | undefined;
  Limit?: number | undefined;
}

export interface AppManagerAuditResp {
  Records: AppAuditRecord[];
}

export interface AppManagerCallableInfoEntry {
  Id: string;
  Service: string;
  Description: string;
  Effect: string;
  Params: AppManagerCallableParam[];
}

export interface AppManagerCallableInfoReq {
  Query?: string | undefined;
}

export interface AppManagerCallableInfoResp {
  Items: AppManagerCallableInfoEntry[];
}

export interface AppManagerCallableParam {
  Name: string;
  Type: string;
  Required: boolean;
  Description: string;
}

export interface AppManagerCastReq {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  SessionToken?: string | undefined;
}

export interface AppManagerCastResp {
  Delivered: number;
}

export interface AppManagerComponentGetReq {
  CardId: string;
}

export interface AppManagerComponentGetResp {
  Component: ComponentDescriptor;
}

export interface AppManagerComponentListReq {
}

export interface AppManagerComponentListResp {
  Items: ComponentDescriptor[];
}

export interface AppManagerDevGateReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerDevGateResp {
  Passed: boolean;
  Results: AppManagerGateResult[];
  Error?: string | undefined;
}

export interface AppManagerDevGenerateFileEntry {
  Path: string;
  ContentHash: string;
}

export interface AppManagerDevGenerateReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
  Template?: boolean | undefined;
}

export interface AppManagerDevGenerateResp {
  Files: AppManagerDevGenerateFileEntry[];
  AppDefHash: string;
  SDKPath: string;
  FreshSDK: boolean;
  Template: boolean;
  Warnings?: string[] | undefined;
  Error?: string | undefined;
}

export interface AppManagerDevGuideError {
  Symptom: string;
  Cause: string;
  Remedy: string;
}

export interface AppManagerDevGuideReq {
  Topic?: string | undefined;
}

export interface AppManagerDevGuideResp {
  Prerequisites: string[];
  Workflow: AppManagerDevGuideStep[];
  HostAPI: string[];
  Security: string[];
  CommonErrors: AppManagerDevGuideError[];
}

export interface AppManagerDevGuideStep {
  Order: number;
  Title: string;
  Detail: string;
  Tools: string[];
}

export interface AppManagerEmitReq {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  SessionToken?: string | undefined;
}

export interface AppManagerEmitResp {
  Accepted: boolean;
}

export interface AppManagerGateError {
  Gate: string;
  Detail: string;
  Fix: string;
}

export interface AppManagerGateResult {
  Gate: string;
  Passed: boolean;
  Error?: AppManagerGateError | undefined;
}

export interface AppManagerGetReq {
  Id: string;
}

export interface AppManagerGetResp {
  Status: AppStatus;
}

export interface AppManagerHostCallInfo {
  CallId: string;
  TargetCallId: string;
  Service?: string | undefined;
  Streaming: boolean;
  RequestType?: string | undefined;
  RequestSchemaId: number;
  ResponseType?: string | undefined;
  ResponseSchemaId: number;
  Note?: string | undefined;
}

export interface AppManagerHostCapabilityInfo {
  Capability: string;
  Title?: string | undefined;
  Description?: string | undefined;
  RiskLevel?: string | undefined;
  Calls: AppManagerHostCallInfo[];
}

export interface AppManagerHostProtocolReq {
  Query?: string | undefined;
  Capability?: string | undefined;
}

export interface AppManagerHostProtocolResp {
  Capabilities: AppManagerHostCapabilityInfo[];
}

export interface AppManagerIconEntry {
  Name: string;
  Label: string;
  Keywords: string[];
  Category: string;
}

export interface AppManagerIconNamesReq {
  Query?: string | undefined;
  Category?: string | undefined;
}

export interface AppManagerIconNamesResp {
  Items: AppManagerIconEntry[];
}

export interface AppManagerInstallLocalReq {
  Path?: string | undefined;
  PackageData?: Uint8Array | undefined;
}

export interface AppManagerInstallLocalResp {
  Status: AppStatus;
}

export interface AppManagerInvokeReq {
  Id: string;
  Callable: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  WorkspaceId?: string | undefined;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  SessionToken?: string | undefined;
  CallSeq?: number | undefined;
  ExpectedPackageHash?: string | undefined;
  RouteDepth?: number | undefined;
  RouteToken?: string | undefined;
}

export interface AppManagerInvokeResp {
  Payload: Uint8Array;
}

export interface AppManagerListReq {
}

export interface AppManagerListResp {
  Items: AppStatus[];
}

export interface AppManagerOpenViewReq {
  Id: string;
  ViewId?: string | undefined;
}

export interface AppManagerOpenViewResp {
  Opened: boolean;
  ViewId?: string | undefined;
  Error?: string | undefined;
}

export interface AppManagerPanelTopologyReq {
  Id: string;
}

export interface AppManagerPanelTopologyResp {
  AppId: string;
  State: string;
  Generation: number;
  GatewayBase: string;
  PanelUrl: string;
  PluginListener: string;
  AuthNotes: string[];
  ExpectedCodes: string[];
}

export interface AppManagerPluginEmitReq {
  PluginId: string;
  Event: string;
  Payload: Uint8Array;
}

export interface AppManagerPluginEmitResp {
  Accepted: boolean;
}

export interface AppManagerPluginLoadReq {
  Id: string;
}

export interface AppManagerPluginLoadResp {
  Status: AppStatus;
}

export interface AppManagerPluginUnloadReq {
  Id: string;
}

export interface AppManagerPluginUnloadResp {
  Status: AppStatus;
}

export interface AppManagerProjectPackageReq {
  ProjectId: string;
  AppId?: string | undefined;
  EntryModule?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AppManagerProjectPackageResp {
  Manifest: AppManifest;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash: string;
}

export interface AppManagerRegisterProjectReq {
  ProjectId?: string | undefined;
  AppId?: string | undefined;
  EntryModule?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerRegisterProjectResp {
  Status: AppStatus;
  Warnings?: string[] | undefined;
}

export interface AppManagerRegisterReq {
  Manifest: AppManifest;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
  PackagePath?: string | undefined;
  ArtifactPath?: string | undefined;
  ArtifactHash?: string | undefined;
  Abi?: PluginAbi | undefined;
  Origin?: string | undefined;
}

export interface AppManagerRegistrationPreviewReq {
  Manifest?: AppManifest | undefined;
  ProjectId?: string | undefined;
  AppId?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
  Path?: string | undefined;
  Slug?: string | undefined;
}

export interface AppManagerRegistrationPreviewResp {
  Manifest: AppManifest;
}

export interface AppManagerReloadProjectReq {
  ProjectId?: string | undefined;
  AppId: string;
  EntryModule?: string | undefined;
  ExpectedStateVersion?: number | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AppManagerReloadProjectResp {
  Status: AppStatus;
  StateVersion: number;
}

export interface AppManagerReloadReq {
  Id: string;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
  ExpectedStateVersion?: number | undefined;
  MigratedState?: Record<string, unknown> | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  CandidateManifest?: AppManifest | undefined;
  CandidateAbi?: PluginAbi | undefined;
  CandidateArtifactPath?: string | undefined;
  CandidateArtifactHash?: string | undefined;
}

export interface AppManagerReloadResp {
  Status: AppStatus;
  StateVersion: number;
}

export interface AppManagerRetryCleanupReq {
  Id: string;
}

export interface AppManagerRetryCleanupResp {
  Status: string;
  Error?: string | undefined;
}

export interface AppManagerSdkVendorReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerSdkVendorResp {
  Vendored: boolean;
  Path?: string | undefined;
  Error?: string | undefined;
}

export interface AppManagerUnregisterReq {
  Id: string;
}

export interface AppManifest {
  Id: string;
  Name: string;
  Version: string;
  Runtime: string;
  ProtocolVersion: number;
  SdkVersion?: string | undefined;
  Namespace: string;
  Permissions: string[];
  Listens?: string[] | undefined;
  Schemas: AppSchemaRef[];
  Callables: AppCallableDescriptor[];
  Events: AppEventDescriptor[];
  Projections: AppProjectionDescriptor[];
  Entrypoints: AppEntrypoint[];
  Dependencies: AppDependency[];
  Bundles?: AppBundle[] | undefined;
  AgentBinding?: AppAgentBinding | undefined;
  Security?: AppSecurityPolicy | undefined;
}

export interface AppObjectDescriptor {
  Kind: string;
  Name: string;
  Fields: AppFieldDescriptor[];
  SchemaId?: number | undefined;
}

export interface AppProjectionDescriptor {
  Id: string;
  PayloadSchema: string;
  Permission?: string | undefined;
}

export interface AppRouteTokenReq {
  SourceAppId: string;
  TargetAppId: string;
  Callable: string;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
}

export interface AppRouteTokenResp {
  Token: string;
  ExpiresAt: number;
}

export interface AppSchemaRef {
  Name: string;
  Hash: string;
  SchemaId?: number | undefined;
}

export interface AppSecurityPolicy {
  AllowNativeAccess?: boolean | undefined;
  AllowCapabilities?: string[] | undefined;
  MaxInstructions?: number | undefined;
  MaxDurationMs?: number | undefined;
  MaxHostCalls?: number | undefined;
  MaxOutputBytes?: number | undefined;
}

export interface AppSessionCreateReq {
  AppId: string;
  ViewId: string;
  Origin?: string | undefined;
}

export interface AppSessionCreateResp {
  SessionId: string;
  AppId: string;
  ViewId: string;
  AgentId?: string | undefined;
  ProjectId?: string | undefined;
  Origin: string;
  ExpiresAt: number;
  Nonce: string;
  Generation: number;
  Token: string;
  BackendUrl?: string | undefined;
  CookieToken?: string | undefined;
}

export interface AppSessionResolveReq {
  Token: string;
}

export interface AppSessionResolveResp {
  SessionId: string;
  AppId: string;
  ViewId: string;
  AgentId?: string | undefined;
  ProjectId?: string | undefined;
  Origin: string;
  ExpiresAt: number;
  Nonce: string;
  Generation: number;
}

export interface AppSessionRevokeReq {
  Token: string;
}

export interface AppSessionRevokeResp {
}

export interface AppStatus {
  Id: string;
  Name?: string | undefined;
  Runtime: string;
  State: string;
  Version?: string | undefined;
  Namespace?: string | undefined;
  PackageHash?: string | undefined;
  ArtifactHash?: string | undefined;
  Generation?: number | undefined;
  Error?: string | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  Callables?: AppCallableDescriptor[] | undefined;
  Events?: AppEventDescriptor[] | undefined;
  Bundles?: AppBundle[] | undefined;
  Entrypoints: AppEntrypoint[];
  Permissions?: string[] | undefined;
  GrantedCapabilities?: string[] | undefined;
  Dependencies?: AppDependency[] | undefined;
  Dependents?: string[] | undefined;
  BackendPort?: number | undefined;
  BackendUrl?: string | undefined;
}

export interface AppTypeDescriptor {
  Kind: string;
  Name?: string | undefined;
  TypeId?: number | undefined;
  Element?: AppTypeDescriptor | undefined;
  Key?: AppTypeDescriptor | undefined;
  Value?: AppTypeDescriptor | undefined;
  ClassName?: string | undefined;
  ClassId?: number | undefined;
}

export interface ArchiveExportReq {
  Path: string;
}

export interface ArchiveExportResp {
  Content: string;
  Size: number;
  NumEntries: number;
}

export interface ArchiveImportReq {
  Path: string;
  Content: string;
}

export interface ArchiveImportResp {
  NumEntries: number;
  BytesWritten: number;
}

export interface AttachmentEntry {
  Name: string;
  MimeType: string;
  Url?: string | undefined;
  SizeBytes?: number | undefined;
}

export interface AuditRecord {
  Time: string;
  RequestID: string;
  AppID: string;
  Runtime: string;
  AgentID: string;
  Role: string;
  ProjectID: string;
  Callable: string;
  Allowed: boolean;
  Reason: string;
  SessionID: string;
  CallSeq: number;
}

export interface AuthLoginReq {
  Username: string;
  Password: string;
}

export interface AuthLoginResp {
  Token: string;
  RefreshToken: string;
  ExpiresAt: string;
  Account: AccountView;
}

export interface AuthRefreshReq {
  RefreshToken: string;
}

export interface AuthRefreshResp {
  Token: string;
  RefreshToken: string;
  ExpiresAt: string;
}

export interface AuthRegisterReq {
  Username: string;
  Password: string;
  DisplayName?: string | undefined;
  Roles?: string[] | undefined;
}

export interface BrowserCookieEntry {
  Name: string;
  Value: string;
  Domain: string;
  Path: string;
  Expires: number;
  HttpOnly: boolean;
  Secure: boolean;
  SameSite: string;
}

export interface BrowserCrawlConfig {
  Seeds?: string[] | undefined;
  MaxDepth?: number | undefined;
  MaxPages?: number | undefined;
  SameDomain?: boolean | undefined;
  RateLimit?: string | undefined;
  Mode?: string | undefined;
  Profile?: string | undefined;
  ExtractSchema?: string | undefined;
  ExtractSelectors?: Record<string, unknown> | undefined;
  InstanceID?: string | undefined;
}

export interface BrowserCrawlHandoffReq {
  TaskId: string;
}

export interface BrowserCrawlHandoffResp {
  Accepted: boolean;
  InstanceId: string;
  Url: string;
}

export interface BrowserCrawlPageResult {
  Url: string;
  Title: string;
  Links: string[];
  Text: string;
}

export interface BrowserCrawlResultsReq {
  TaskId: string;
  Cursor?: number | undefined;
  Limit?: number | undefined;
}

export interface BrowserCrawlResultsResp {
  Results: BrowserCrawlPageResult[];
  NextCursor: number;
  Done: boolean;
}

export interface BrowserCrawlStartReq {
  Card?: string | undefined;
  Config?: BrowserCrawlConfig | undefined;
  Overrides?: Record<string, unknown> | undefined;
}

export interface BrowserCrawlStartResp {
  TaskId: string;
}

export interface BrowserCrawlStatusReq {
  TaskId: string;
}

export interface BrowserCrawlStatusResp {
  State: string;
  Crawled: number;
  Frontier: number;
  MaxPages: number;
  Error?: string | undefined;
}

export interface BrowserHistoryEntry {
  Url: string;
  Title: string;
  VisitedAt: string;
}

export interface BrowserInstance {
  Config: BrowserInstanceConfig;
  Status: BrowserInstanceStatus;
}

export interface BrowserInstanceConfig {
  Id: string;
  Name: string;
  Url: string;
  Open: boolean;
  Proxy: string;
  ProxyMode: string;
  Mode: string;
  Hidden: boolean;
  State: BrowserWindowState;
}

export interface BrowserInstanceStatus {
  Id: string;
  Open: boolean;
  Url: string;
  Title: string;
  Error?: string | undefined;
}

export interface BrowserManagerAsyncResult {
  Accepted: boolean;
  Instance: BrowserInstance;
}

export interface BrowserManagerCreateReq {
  Name: string;
  Url: string;
  Proxy: string;
  ProxyMode: string;
  Hidden: boolean;
}

export interface BrowserManagerEvent {
  Kind: string;
  Id: string;
  Instance: BrowserInstance;
  Error?: string | undefined;
}

export interface BrowserManagerExportCookiesReq {
  Id: string;
}

export interface BrowserManagerExportCookiesResp {
  Cookies: Record<string, BrowserCookieEntry[]>;
}

export interface BrowserManagerGetReq {
  Id: string;
}

export interface BrowserManagerImportCookiesReq {
  Id: string;
  Cookies: Record<string, BrowserCookieEntry[]>;
}

export interface BrowserManagerImportCookiesResp {
  Imported: number;
}

export interface BrowserManagerListResp {
  Items: BrowserInstance[];
}

export interface BrowserManagerNavigateReq {
  Id: string;
  Url: string;
}

export interface BrowserManagerOpenGlobalReq {
  Url: string;
}

export interface BrowserManagerOpenGlobalResp {
  Opened: boolean;
  Url: string;
  Error: string;
}

export interface BrowserManagerOpenReq {
  Id: string;
}

export interface BrowserManagerRemoveReq {
  Id: string;
}

export interface BrowserManagerUpdateReq {
  Id: string;
  Config: BrowserInstanceConfig;
}

export interface BrowserPageState {
  ScrollX: number;
  ScrollY: number;
  Zoom: number;
  Data: string;
}

export interface BrowserWindowState {
  X: number;
  Y: number;
  Width: number;
  Height: number;
  Maximised: boolean;
  Url: string;
  Title: string;
  PageState: BrowserPageState;
  History: BrowserHistoryEntry[];
}

export interface BuiltinMode {
  CardId: string;
  Name: string;
  Title: string;
  Icon: string;
}

export interface CallableInterface {
  Name: string;
  Kind: string;
  Params?: CallableParam[] | undefined;
  FinalType?: string | undefined;
  FinalFields?: CallableParam[] | undefined;
  NextType?: string | undefined;
  NextFields?: CallableParam[] | undefined;
  ReqType?: string | undefined;
  FinalDesc?: string | undefined;
  ChunkDesc?: string | undefined;
  HasError?: boolean | undefined;
  Stream?: boolean | undefined;
  Permission?: string | undefined;
  Description?: string | undefined;
  Category?: string | undefined;
  EffectKind?: string | undefined;
  ServiceName?: string | undefined;
  ToolName?: string | undefined;
  ReqSchemaID?: number | undefined;
  FinalSchemaID?: number | undefined;
}

export interface CallableParam {
  Name: string;
  Type: string;
  Description?: string | undefined;
  Required?: boolean | undefined;
}

export interface CancelReq {
  id: string;
  reason?: string | undefined;
}

export interface CapabilityCandidate {
  Kind: string;
  Id: string;
  Name: string;
  RelevanceScore?: number | undefined;
  Rationale?: string | undefined;
}

export interface CardRef {
  Id: string;
  Version?: number | undefined;
  Source?: string | undefined;
  Scope?: string | undefined;
  Order?: number | undefined;
  Disabled?: boolean | undefined;
}

export interface CardValidationError {
  Code: string;
  Field: string;
  Message: string;
}

export interface CdkeyRedeemReq {
  Code: string;
}

export interface CdkeyRedeemResp {
  ProductName: string;
  PeriodDays: number;
  StartsAt: string;
  EndsAt: string;
}

export interface ChatMessage {
  Id: string;
  Role: string;
  Content: ContentBlock[];
  ReasoningContent?: string | undefined;
  Idx?: number | undefined;
  Timestamp?: string | undefined;
  Usage?: UsageData | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
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

export interface CloudAccountGetEntitlementsResp {
  Entitlements: EntitlementsView;
  Degraded: boolean;
}

export interface CloudAccountLinkReq {
  AccountId: string;
  AccessToken: string;
  RefreshToken: string;
  ExpiresIn: number;
  DisplayName?: string | undefined;
  AvatarUrl?: string | undefined;
}

export interface CloudAccountSessionTokenResp {
  AccessToken: string;
  ExpiresAt: string;
}

export interface CloudAccountStatus {
  Linked: boolean;
  AccountId: string;
  DisplayName?: string | undefined;
  AvatarUrl?: string | undefined;
  RateLimitTier?: string | undefined;
  Tier: string;
  IsExperimentalSubscriber: boolean;
  EntitlementsDegraded: boolean;
  TokenExpiresAt?: string | undefined;
  EntitlementsFetchedAt?: string | undefined;
}

export interface CloudAccountUnlinkResp {
}

export interface CompactedRange {
  segmentIndex: number;
  tokens: number;
}

export interface CompactionEvent {
  TurnId: string;
  Trigger: string;
  BeforeTokens: number;
  AfterTokens: number;
  Unit: ModelUnit;
  Level: number;
  Rounds: CompactionRoundRecord[];
  CreatedAt: string;
  Seq?: number | undefined;
  ContextWindowSize?: number | undefined;
}

export interface CompactionLockState {
  TurnID: string;
  Trigger: string;
  StartedAt: string;
  Seq: number;
}

export interface CompactionPolicy {
  Enabled: boolean;
  Strategy: string;
  TriggerKind: string;
  BudgetMode?: string | undefined;
  TokenBudget?: number | undefined;
  RecentWindow?: number | undefined;
  SummaryUnit?: ModelUnit | undefined;
  MaxSummaryTokens?: number | undefined;
}

export interface CompactionRoundRecord {
  Round: number;
  BeforeLayout: ContextSegment[];
  AfterLayout: ContextSegment[];
  CompactedRanges: CompactedRange[];
  SourceStartIndex: number;
  SourceEndIndex: number;
  Level: number;
  CompactedMessageCount: number;
}

export interface CompleteMessageReq {
  UserText: string;
  System?: string | undefined;
  Unit?: ModelUnit | undefined;
}

export interface ComponentDependency {
  CardId: string;
  Required?: boolean | undefined;
  Reason?: string | undefined;
}

export interface ComponentDescriptor {
  Ref: ComponentRef;
  Title: string;
  Icon?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Protected?: boolean | undefined;
  Dependencies: ComponentDependency[];
  Prompts: ComponentPromptContribution[];
  Tools: ComponentToolContribution[];
  ShadowedById?: string | undefined;
}

export interface ComponentDiagnostic {
  CardId: string;
  Level: string;
  Message: string;
  Reason?: string | undefined;
}

export interface ComponentPromptContribution {
  Id: string;
  CardId: string;
  Title?: string | undefined;
  Text: string;
  Placement?: string | undefined;
  Priority?: number | undefined;
}

export interface ComponentRef {
  CardId: string;
  Kind: string;
  Version?: number | undefined;
  Source?: string | undefined;
}

export interface ComponentToolContribution {
  Id: string;
  CardId: string;
  CallableId: string;
  Name?: string | undefined;
  Description?: string | undefined;
  Usage?: string | undefined;
  Constraints?: string | undefined;
  Priority?: number | undefined;
}

export interface ComponentVisual {
  Icon?: string | undefined;
  Accent?: string | undefined;
  Emphasis?: string | undefined;
  Color?: string | undefined;
  Background?: string | undefined;
  Border?: string | undefined;
  Size?: number | undefined;
}

export interface ComposerHistoryItem {
  Id: string;
  Text: string;
  Timestamp: string;
  Starred: boolean;
}

export interface ComputerUseCapabilitiesReq {
}

export interface ComputerUseCapabilitiesResp {
  Platform: string;
  Backend: string;
  Items: ComputerUseCapability[];
}

export interface ComputerUseCapability {
  Key: string;
  Supported: boolean;
  Scope?: string | undefined;
  Detail?: string | undefined;
}

export interface ComputerUseCursorPosResp {
  X: number;
  Y: number;
}

export interface ComputerUseCursorPositionReq {
}

export interface ComputerUseDisplayInfo {
  Id: number;
  Name: string;
  Bounds: ComputerUseRegion;
  IsPrimary: boolean;
}

export interface ComputerUseDisplaysResp {
  Items: ComputerUseDisplayInfo[];
  Count: number;
}

export interface ComputerUseElementNode {
  Id: string;
  AutomationID?: string | undefined;
  Name?: string | undefined;
  ClassName?: string | undefined;
  ControlType?: string | undefined;
  Bounds: ComputerUseRegion;
  IsEnabled?: boolean | undefined;
  IsKeyboardFocusable?: boolean | undefined;
  HasKeyboardFocus?: boolean | undefined;
  Value?: string | undefined;
}

export interface ComputerUseGetWindowInfoReq {
  WindowID: number;
}

export interface ComputerUseListDisplaysReq {
}

export interface ComputerUseListElementsReq {
  WindowID?: number | undefined;
  NameContains?: string | undefined;
  ControlType?: string | undefined;
  MaxDepth?: number | undefined;
  MaxResults?: number | undefined;
}

export interface ComputerUseListElementsResp {
  Items: ComputerUseElementNode[];
  Count: number;
}

export interface ComputerUseListProcessesReq {
  NameContains?: string | undefined;
  Pid?: number | undefined;
}

export interface ComputerUseListWindowsReq {
  Pid?: number | undefined;
  ProcessName?: string | undefined;
  TitlePattern?: string | undefined;
  ForegroundOnly?: boolean | undefined;
  AtPoint?: ComputerUseTargetSpec | undefined;
  IncludeInvisible?: boolean | undefined;
}

export interface ComputerUseOcrLine {
  Text: string;
  Bounds: ComputerUseRegion;
  Confidence?: number | undefined;
}

export interface ComputerUseOcrReq {
  Target?: ComputerUseWindowTarget | undefined;
  Language?: string | undefined;
}

export interface ComputerUseOcrResp {
  Text: string;
  Lines: ComputerUseOcrLine[];
  Engine: string;
  CaptureBounds: ComputerUseRegion;
}

export interface ComputerUseProcessInfo {
  Pid: number;
  ParentPid: number;
  Name: string;
  ExePath?: string | undefined;
}

export interface ComputerUseProcessesResp {
  Items: ComputerUseProcessInfo[];
  Count: number;
}

export interface ComputerUseRegion {
  X: number;
  Y: number;
  Width: number;
  Height: number;
}

export interface ComputerUseScreenshotReq {
  Target?: ComputerUseWindowTarget | undefined;
  Mode?: string | undefined;
  Display?: number | undefined;
  Region?: ComputerUseRegion | undefined;
  WindowTitle?: string | undefined;
  Annotate?: boolean | undefined;
  Format?: string | undefined;
  Quality?: number | undefined;
  Scale?: number | undefined;
  IncludeCursor?: boolean | undefined;
  Grayscale?: boolean | undefined;
}

export interface ComputerUseScreenshotResp {
  ImageBytes: Uint8Array;
  Format: string;
  Width: number;
  Height: number;
  DisplayName?: string | undefined;
  CursorX?: number | undefined;
  CursorY?: number | undefined;
  Unchanged?: boolean | undefined;
  Message?: string | undefined;
}

export interface ComputerUseSetupOcrReq {
  Dir?: string | undefined;
  Force?: boolean | undefined;
}

export interface ComputerUseSetupOcrResp {
  Dir: string;
  Status: string;
  Files: string[];
  Error?: string | undefined;
}

export interface ComputerUseTargetSpec {
  X?: number | undefined;
  Y?: number | undefined;
  GridCoord?: string | undefined;
  ElementDescription?: string | undefined;
  WindowID?: number | undefined;
}

export interface ComputerUseWindowInfo {
  Id: number;
  Title: string;
  Bounds: ComputerUseRegion;
  ProcessName?: string | undefined;
  Pid?: number | undefined;
  IsVisible?: boolean | undefined;
}

export interface ComputerUseWindowInfoDetailed {
  Id: number;
  Title: string;
  Bounds: ComputerUseRegion;
  ProcessName: string;
  Pid: number;
  IsVisible: boolean;
  State: string;
  ClassName: string;
  IsForeground: boolean;
  IsTopmost: boolean;
}

export interface ComputerUseWindowTarget {
  Mode?: string | undefined;
  Display?: number | undefined;
  Region?: ComputerUseRegion | undefined;
  WindowID?: number | undefined;
  WindowTitle?: string | undefined;
}

export interface ComputerUseWindowsResp {
  Items: ComputerUseWindowInfo[];
  Count: number;
}

export interface Concept {
  Id: string;
  Desc: string;
  Capabilities: string[];
  Constraints: string[];
  Checks: string[];
  State: string;
}

export interface Config {
  seeds: string[];
  max_depth: number;
  max_pages: number;
  same_domain: boolean;
  rate_limit: number;
  extract_schema?: string | undefined;
  extract_selectors?: Record<string, unknown> | undefined;
  profile?: string | undefined;
  mode: string;
  instance_id?: string | undefined;
  extra?: Record<string, unknown> | undefined;
}

export interface ContentBlock {
  Type: string;
  Id?: string | undefined;
  Text?: string | undefined;
  ToolUseId?: string | undefined;
  ToolName?: string | undefined;
  Input?: string | undefined;
  IsError?: boolean | undefined;
  CacheControl?: string | undefined;
  ImageUrl?: string | undefined;
  MimeType?: string | undefined;
  Recognized?: boolean | undefined;
  RecognitionText?: string | undefined;
}

export interface ContentDetailReq {
  Slug: string;
}

export interface ContentDetailResp {
  Item: ContentListItem;
  Manifest: string;
}

export interface ContentInstallReq {
  Slug: string;
  Confirm: boolean;
}

export interface ContentInstallResp {
  Slug: string;
  ContentType: string;
  Status: string;
  Message: string;
  Risk: ContentRiskAssessment;
}

export interface ContentListItem {
  Id: string;
  ContentType: string;
  Name: string;
  Slug: string;
  Description?: string | undefined;
  Version: string;
  Author?: string | undefined;
  DownloadCount: number;
  Sha256: string;
  Signature?: string | undefined;
  Visibility: string;
  Pricing: string;
}

export interface ContentRiskAssessment {
  ContentType: string;
  SignaturePresent: boolean;
  Sha256Verified: boolean;
  EntryPoint: string;
  Warnings: string[];
}

export interface ContentSearchReq {
  ContentType?: string | undefined;
  Query?: string | undefined;
  Sort?: string | undefined;
  Page?: number | undefined;
  PageSize?: number | undefined;
}

export interface ContentSearchResp {
  Items: ContentListItem[];
  Page: number;
  PageSize: number;
  Total: number;
}

export interface ContextSegment {
  kind: string;
  tokens: number;
  stepIndex: number;
}

export interface CookieBridgePairingInfoReq {
}

export interface CookieBridgePairingInfoResp {
  McpUrl: string;
  PushUrl: string;
  PendingUrl: string;
  PairingToken: string;
  Port: number;
}

export interface CookieBridgeRegenTokenReq {
}

export interface CookieBridgeRegenTokenResp {
  PairingToken: string;
}

export interface DbCloseReq {
  ProfileId: string;
}

export interface DbCloseResp {
}

export interface DbColumnDef {
  Name: string;
  DataType: string;
  Nullable: boolean;
  Default?: string | undefined;
  Key?: string | undefined;
  Extra?: string | undefined;
}

export interface DbDescribeReq {
  ProfileId: string;
  Path: string;
}

export interface DbDescribeResp {
  Columns: DbColumnDef[];
  Indexes: DbIndexDef[];
}

export interface DbDialTestReq {
  ProfileId: string;
}

export interface DbDialTestResp {
  Ok: boolean;
  LatencyMs: number;
  ServerVersion?: string | undefined;
  Error?: string | undefined;
}

export interface DbIndexDef {
  Name: string;
  Columns: string;
  Unique: boolean;
}

export interface DbObjectDeleteReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectDeleteResp {
}

export interface DbObjectEntry {
  Name: string;
  Path: string;
  IsDir: boolean;
  Size: number;
  Modified?: string | undefined;
  ContentType?: string | undefined;
}

export interface DbObjectListReq {
  ProfileId: string;
  Path: string;
  Cursor?: string | undefined;
  Limit?: number | undefined;
}

export interface DbObjectListResp {
  Entries: DbObjectEntry[];
  Cursor?: string | undefined;
  HasMore: boolean;
}

export interface DbObjectMkdirReq {
  ProfileId: string;
  ParentPath: string;
  Name: string;
}

export interface DbObjectMkdirResp {
}

export interface DbObjectReadReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectReadResp {
  Content: string;
  Size: number;
  Truncated: boolean;
  ContentType?: string | undefined;
  Modified?: string | undefined;
}

export interface DbObjectStatReq {
  ProfileId: string;
  Path: string;
}

export interface DbObjectStatResp {
  Size: number;
  IsDir: boolean;
  Modified?: string | undefined;
  ContentType?: string | undefined;
}

export interface DbObjectWriteReq {
  ProfileId: string;
  Path: string;
  Content: string;
  ContentType?: string | undefined;
}

export interface DbObjectWriteResp {
  Size: number;
}

export interface DbProfileGetReq {
  Id: string;
}

export interface DbProfileGetResp {
  Profile: DbProfileView;
}

export interface DbProfileListReq {
}

export interface DbProfileListResp {
  Items: DbProfileView[];
}

export interface DbProfileRemoveReq {
  Id: string;
}

export interface DbProfileRemoveResp {
}

export interface DbProfileSaveReq {
  Id: string;
  Name: string;
  Backend: string;
  Endpoint?: string | undefined;
  Database?: string | undefined;
  TunnelRef?: string | undefined;
  Username?: string | undefined;
  AccessKey?: string | undefined;
  Password?: string | undefined;
  Secret?: string | undefined;
  Token?: string | undefined;
}

export interface DbProfileSaveResp {
  Profile: DbProfileView;
}

export interface DbProfileView {
  Id: string;
  Name: string;
  Backend: string;
  Endpoint?: string | undefined;
  Database?: string | undefined;
  TunnelRef?: string | undefined;
  Username?: string | undefined;
  AccessKey?: string | undefined;
  HasPassword: boolean;
  HasSecret: boolean;
  HasToken: boolean;
}

export interface DbQueryReq {
  ProfileId: string;
  Text: string;
  Mode?: string | undefined;
}

export interface DbReadReq {
  ProfileId: string;
  Path: string;
  Limit?: number | undefined;
  Offset?: number | undefined;
}

export interface DbRows {
  Columns: string[];
  Rows: string[][];
  Truncated: boolean;
  Message?: string | undefined;
}

export interface DbTreeNode {
  Path: string;
  Kind: string;
  Label: string;
  Meta?: Record<string, string> | undefined;
}

export interface DbTreeReq {
  ProfileId: string;
  Path: string;
  Cursor?: string | undefined;
}

export interface DbTreeResp {
  Nodes: DbTreeNode[];
  Cursor?: string | undefined;
  HasMore: boolean;
}

export interface DebugCommandExecReq {
  Name: string;
  Args: string[];
}

export interface DebugCommandExecResp {
  Output: string;
}

export interface DebugCommandsListResp {
  Commands: SlashCommand[];
}

export interface Diagnostic {
  Id: string;
  Severity: string;
  Source: string;
  Message: string;
  Timestamp: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface DiagnosticSummary {
  Id: string;
  Severity: string;
  Source: string;
  Message: string;
  Timestamp: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface DispatchActivity {
  State: string;
  SessionId?: string | undefined;
  AgentId?: string | undefined;
  SlotKind?: string | undefined;
  StartedAt?: number | undefined;
  Depth?: number | undefined;
  AggregatorId?: string | undefined;
}

export interface EntitlementsView {
  Features: Record<string, boolean>;
  RateLimitTier: string;
  Tier: string;
  Pass: PassView;
  IsExperimentalSubscriber: boolean;
  Credits: number;
}

export interface ExploreResult {
  TurnId: string;
  Summary: string;
  SearchCount: number;
  ReadCount: number;
  InputTokens: number;
  OutputTokens: number;
  Timestamp: string;
  StepId?: string | undefined;
  AgentId?: string | undefined;
}

export interface FetchedModel {
  Name: string;
  Modality: string;
  Protocol: string;
}

export interface FileEntry {
  Name: string;
  IsDir: boolean;
  Size: number;
  ModTime: string;
}

export interface FileEntryListResp {
  Items: FileEntry[];
  NumEntries: number;
  Truncated?: boolean | undefined;
}

export interface FileSystemEditHunk {
  Old_start: number;
  Old_lines: number;
  New_start: number;
  New_lines: number;
  Lines: string[];
}

export interface FileSystemEditReq {
  Path: string;
  Old_string: string;
  New_string: string;
  Replace_all?: boolean | undefined;
  Overwrite?: boolean | undefined;
  Confirm?: boolean | undefined;
}

export interface FileSystemEditResp {
  Replacements: number;
  Additions: number;
  Deletions: number;
  Hunks: FileSystemEditHunk[];
}

export interface FileSystemGlobReq {
  Pattern: string;
  Path?: string | undefined;
  Maxdepth?: number | undefined;
  No_ignore?: boolean | undefined;
  Exclude?: string | undefined;
  Order_by?: string | undefined;
}

export interface FileSystemGlobResp {
  Files: string[];
  NumFiles: number;
  Truncated: boolean;
  Note?: string | undefined;
}

export interface FileSystemGrepCount {
  File: string;
  Count: number;
}

export interface FileSystemGrepMatch {
  File: string;
  Line: number;
  Content: string;
}

export interface FileSystemGrepReq {
  Pattern: string;
  Path?: string | undefined;
  Depth?: number | undefined;
  Ignore_case?: boolean | undefined;
  Output_mode?: string | undefined;
  Glob?: string | undefined;
  Head_limit?: number | undefined;
  Context?: number | undefined;
  Before_context?: number | undefined;
  After_context?: number | undefined;
  Multiline?: boolean | undefined;
  Invert_match?: boolean | undefined;
  Word_regexp?: boolean | undefined;
  No_ignore?: boolean | undefined;
  Recursive?: boolean | undefined;
  Exclude?: string | undefined;
}

export interface FileSystemGrepResp {
  Files?: string[] | undefined;
  Counts?: FileSystemGrepCount[] | undefined;
  Matches?: FileSystemGrepMatch[] | undefined;
  NumMatches?: number | undefined;
  Truncated?: boolean | undefined;
  Output_mode?: string | undefined;
  Note?: string | undefined;
}

export interface FileSystemListReq {
  Path: string;
  Depth?: number | undefined;
  Exclude?: string | undefined;
  No_ignore?: boolean | undefined;
  All?: boolean | undefined;
  Detail?: boolean | undefined;
}

export interface FileSystemReadBase64Req {
  Path: string;
}

export interface FileSystemReadBase64Resp {
  Content: string;
  Note?: string | undefined;
}

export interface FileSystemReadChunkReq {
  Path: string;
  Offset: number;
  Length: number;
}

export interface FileSystemReadChunkResp {
  Offset: number;
  Length: number;
  Total: number;
  Data: string;
  Note?: string | undefined;
}

export interface FileSystemReadReq {
  Path: string;
  Offset?: number | undefined;
  Limit?: number | undefined;
  Tail?: number | undefined;
  Filter?: string | undefined;
}

export interface FileSystemReadResp {
  Content: string;
  TotalLines: number;
  StartLine: number;
  NumLines: number;
  Truncated: boolean;
  Note?: string | undefined;
}

export interface FileSystemRmReq {
  Path: string;
  Recursive?: boolean | undefined;
  Confirm?: boolean | undefined;
  Force?: boolean | undefined;
}

export interface FileSystemRmResp {
  Removed: string[];
  Count: number;
  Preview: boolean;
}

export interface FileSystemRootsResp {
  Home: string;
  Roots: string[];
}

export interface FileSystemWriteBase64Req {
  Path: string;
  Content: string;
  Confirm?: boolean | undefined;
}

export interface FileSystemWriteReq {
  Path: string;
  Content: string;
  Confirm?: boolean | undefined;
}

export interface FileSystemWriteResp {
  Warning?: string | undefined;
}

export interface FreeAgentBinding {
  AllowCreate: boolean;
  AllowSwitch: boolean;
  AllowMessage: boolean;
  AgentKinds?: string[] | undefined;
}

export interface FrontendErrorReport {
  RequestId: string;
  Severity: string;
  SourceComponent: string;
  Message: string;
  StackTrace: string;
  UserAgent: string;
  SessionId: string;
}

export interface FrontierTaskCard {
  Id: string;
  Title: string;
  Status: string;
  Tags: string[];
}

export interface FrpInstance {
  Config: FrpInstanceConfig;
  Status: FrpInstanceStatus;
}

export interface FrpInstanceConfig {
  Id: string;
  Name: string;
  ServerAddr: string;
  Token?: string | undefined;
  Tls?: boolean | undefined;
  LegacyMode?: boolean | undefined;
  Disabled?: boolean | undefined;
  Proxies: FrpProxy[];
  WebProxy?: FrpWebProxy | undefined;
}

export interface FrpInstanceStatus {
  Id: string;
  Running: boolean;
  Connected?: boolean | undefined;
  Proxies?: FrpProxyStatus[] | undefined;
  DetectedVersion?: string | undefined;
  Error?: string | undefined;
  StartedAt?: string | undefined;
}

export interface FrpManagerCreateReq {
  Name: string;
  ServerAddr: string;
  Token?: string | undefined;
  Tls?: boolean | undefined;
  LegacyMode?: boolean | undefined;
  Proxies: FrpProxy[];
  WebProxy?: FrpWebProxy | undefined;
}

export interface FrpManagerDetectReq {
  ServerAddr: string;
  Token?: string | undefined;
}

export interface FrpManagerDetectResp {
  LegacyMode: boolean;
  Error?: string | undefined;
}

export interface FrpManagerGetReq {
  Id: string;
}

export interface FrpManagerListResp {
  Items: FrpInstance[];
  GatewayPort: number;
}

export interface FrpManagerRemoveReq {
  Id: string;
}

export interface FrpManagerStartReq {
  Id: string;
}

export interface FrpManagerStopReq {
  Id: string;
}

export interface FrpManagerUpdateReq {
  Id: string;
  Config: FrpInstanceConfig;
}

export interface FrpProxy {
  Name: string;
  Kind: string;
  LocalIP: string;
  LocalPort: number;
  RemotePort: number;
}

export interface FrpProxyStatus {
  Name: string;
  Status: string;
  Err?: string | undefined;
  RemoteAddr?: string | undefined;
}

export interface FrpWebProxy {
  Enabled: boolean;
  Name: string;
  Mode?: string | undefined;
  RemotePort?: number | undefined;
  CustomDomains?: string[] | undefined;
  CertPem?: string | undefined;
  KeyPem?: string | undefined;
  HostHeaderRewrite?: string | undefined;
}

export interface GetReq {
  id: string;
}

export interface GetThinkingRegistryResp {
  Entries: ThinkingRegistryEntry[];
}

export interface GitBlameLine {
  Hash: string;
  Author: string;
  Email: string;
  When: string;
  Line: number;
  Content: string;
}

export interface GitBranchInfo {
  Name: string;
  Current: boolean;
  Hash: string;
  IsRemote: boolean;
  Remote: string;
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

export interface GitFileStatus {
  Path: string;
  Staging: string;
  Worktree: string;
}

export interface GitRemoteInfo {
  Name: string;
  Urls: string[];
}

export interface GitShowFile {
  Path: string;
  Status: string;
  OldPath?: string | undefined;
}

export interface GitStashInfo {
  Index: number;
  Message: string;
  Hash: string;
  When: string;
}

export interface GitTagInfo {
  Name: string;
  Hash: string;
  Message: string;
  When: string;
}

export interface GlassBootstrapReq {
  Key?: string | undefined;
  Username?: string | undefined;
  Password?: string | undefined;
  DeviceId?: string | undefined;
  Capabilities?: GlassCapability[] | undefined;
}

export interface GlassBootstrapResp {
  SessionId: string;
  DeviceId: string;
  Token: string;
  ExpiresAt: string;
  Kind: string;
  Scope: string;
}

export interface GlassCapability {
  Name: string;
  Version?: string | undefined;
  Features?: string[] | undefined;
}

export interface GlassDebugReq {
}

export interface GlassDebugResp {
  State: GlassDebugState;
}

export interface GlassDebugSimulateReq {
  Command: string;
  Title?: string | undefined;
  Body?: string | undefined;
  Source?: string | undefined;
  TimeAgo?: string | undefined;
  Context?: string | undefined;
  Options?: string[] | undefined;
  SelectedIdx?: number | undefined;
  Priority?: string | undefined;
  BatteryLevel?: number | undefined;
}

export interface GlassDebugSimulateResp {
  Applied: boolean;
  Detail?: string | undefined;
}

export interface GlassDebugState {
  Session?: GlassSessionState | undefined;
  Capabilities: GlassCapability[];
  CurrentFrame?: GlassRenderFrame | undefined;
  AudioAck?: GlassSpeechAck | undefined;
  Stats: GlassDebugStats;
  Timeline: GlassDebugTimelineEntry[];
  Inbox?: GlassInboxSnapshot | undefined;
  Hud?: GlassHudStatus | undefined;
  Deliveries: GlassDeliveryLogEntry[];
}

export interface GlassDebugStats {
  Utterances: number;
  Transcripts: number;
  Renders: number;
  Speaks: number;
  SpeakDeduped: number;
  Errors: number;
}

export interface GlassDebugTimelineEntry {
  Kind: string;
  Detail?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassDeliveryLogEntry {
  EventId: string;
  Success: boolean;
  Detail?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassEvent {
  EventId: string;
  Source: string;
  Kind: string;
  Priority: string;
  Payload: string;
  DedupeKey?: string | undefined;
  State: string;
  CreatedAt: string;
  TTLAt?: string | undefined;
  RetryAt?: string | undefined;
  Retries: number;
  InFlightAt?: string | undefined;
  CompletedAt?: string | undefined;
  Outcome?: string | undefined;
}

export interface GlassEventCompletedEvent {
  Event: GlassEvent;
}

export interface GlassEventDeliveredEvent {
  Event: GlassEvent;
}

export interface GlassEventEnqueuedEvent {
  Event: GlassEvent;
}

export interface GlassGetStateReq {
}

export interface GlassGetStateResp {
  Active: boolean;
  Session?: GlassSessionState | undefined;
}

export interface GlassHudStatus {
  Connection: string;
  BatteryLevel?: number | undefined;
  Charging?: boolean | undefined;
  AgentRunning: boolean;
  AgentName?: string | undefined;
  AgentState?: string | undefined;
  AgentOutcome?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassInboxSnapshot {
  Total: number;
  Pending: number;
  InFlight: number;
  Completed: number;
  Ignored: number;
  Expired: number;
  Events: GlassEvent[];
}

export interface GlassInteractionEvent {
  SessionId: string;
  Generation: number;
  Tick: number;
  ElementId: string;
  Action: string;
  Value?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassInteractionReportReq {
  SessionId: string;
  Generation: number;
  Tick: number;
  ElementId: string;
  Action: string;
  Value?: string | undefined;
}

export interface GlassInteractionReportResp {
  SessionId: string;
  Generation: number;
  Acknowledged: boolean;
}

export interface GlassLifecycleEvent {
  Kind: string;
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Reason?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassRenderEvent {
  SessionId: string;
  Generation: number;
  Frame: GlassRenderFrame;
  Timestamp?: string | undefined;
}

export interface GlassRenderFrame {
  Text?: string | undefined;
  ImageUrl?: string | undefined;
  Scene?: GlassScene | undefined;
  Layout?: string | undefined;
  Meta?: string | undefined;
}

export interface GlassScene {
  Tick: number;
  Elements: GlassSceneElement[];
  FocusId?: string | undefined;
}

export interface GlassSceneBox {
  X: number;
  Y: number;
  W: number;
  H: number;
}

export interface GlassSceneElement {
  Id: string;
  Type: string;
  Box: GlassSceneBox;
  Text?: string | undefined;
  ImageData?: string | undefined;
  Border?: number | undefined;
  Radius?: number | undefined;
  Overflow?: string | undefined;
  BreakMode?: string | undefined;
  Visible?: boolean | undefined;
  Z?: number | undefined;
  Role?: string | undefined;
  Selected?: boolean | undefined;
  Options?: GlassSceneOption[] | undefined;
  Label?: string | undefined;
}

export interface GlassSceneOption {
  Id: string;
  Text: string;
}

export interface GlassSessionClaimReq {
  SessionId: string;
  DeviceId: string;
  Capabilities?: GlassCapability[] | undefined;
}

export interface GlassSessionClaimResp {
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Online: boolean;
  Reconnected: boolean;
  Replaced: boolean;
  LastFrame?: GlassRenderFrame | undefined;
}

export interface GlassSessionState {
  SessionId: string;
  DeviceId: string;
  Generation: number;
  Online: boolean;
  LastSeen?: string | undefined;
  OfflineAt?: string | undefined;
  LastFrame?: GlassRenderFrame | undefined;
  Capabilities: GlassCapability[];
}

export interface GlassSpeakEvent {
  SessionId: string;
  Generation: number;
  MessageId: string;
  Text: string;
  Duplicate: boolean;
  Timestamp?: string | undefined;
}

export interface GlassSpeechAck {
  SessionId: string;
  UtteranceId: string;
  HighestContiguousSeq: number;
  EndReceived?: boolean | undefined;
  Accepted?: boolean | undefined;
  Reason?: string | undefined;
}

export interface GlassSpeechChunkReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
  Sequence: number;
  Pcm: Uint8Array;
}

export interface GlassSpeechEndReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
}

export interface GlassSpeechEndResp {
  SessionId: string;
  UtteranceId: string;
  Ack: GlassSpeechAck;
}

export interface GlassSpeechStartReq {
  SessionId: string;
  Generation: number;
  UtteranceId: string;
  SampleRate?: number | undefined;
  Channels?: number | undefined;
  BitsPerSample?: number | undefined;
}

export interface GlassSpeechStartResp {
  SessionId: string;
  UtteranceId: string;
  Ack: GlassSpeechAck;
}

export interface GlassTelemetryReq {
  SessionId: string;
  Generation: number;
  BatteryLevel?: number | undefined;
  Charging?: boolean | undefined;
}

export interface GlassTelemetryResp {
  Accepted: boolean;
}

export interface GlassTranscript {
  SessionId: string;
  UtteranceId: string;
  Generation: number;
  DeviceId: string;
  Text: string;
  Error?: string | undefined;
  Timestamp: string;
}

export interface GlassTranscriptEvent {
  Transcript: GlassTranscript;
}

export interface GoalSummary {
  Condition?: string | undefined;
  Status?: string | undefined;
  Confirmed?: boolean | undefined;
  BoundTaskCardId?: string | undefined;
  TurnCount?: number | undefined;
  MaxTurns?: number | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

export interface GraphChangedEvent {
  GraphKind: string;
  Id: string;
  Revision: string;
}

export interface GraphPatch {
  Epoch: number;
  Timestamp: string;
  AddedNodes: UnifiedGraphNode[];
  RemovedNodes: string[];
  UpdatedNodes: UnifiedGraphNode[];
  AddedEdges: UnifiedGraphEdge[];
  RemovedEdges: string[];
}

export interface GraphSnapshotMeta {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  Revision: string;
  SchemaVersion: string;
  CreatedAt: string;
  UpdatedAt?: string | undefined;
  Source?: string | undefined;
}

export interface Group {
  Id: string;
  Name: string;
  Description: string;
  Roles: string[];
  MemberCount: number;
}

export interface GroupCreateReq {
  Name: string;
  Description?: string | undefined;
  Roles?: string[] | undefined;
}

export interface GroupDeleteReq {
  Id: string;
}

export interface GroupListResp {
  Items: Group[];
}

export interface GroupUpdateReq {
  Id: string;
  Name?: string | undefined;
  Description?: string | undefined;
  Roles?: string[] | undefined;
}

export interface GuidanceCapabilityEntry {
  Capability: string;
  Familiarity: number;
  Confidence: number;
  Evidence?: string[] | undefined;
  HintState?: string | undefined;
  UpdatedAt: string;
}

export interface GuidanceCapabilityProfile {
  Account: string;
  Capabilities: GuidanceCapabilityEntry[];
  UpdatedAt?: string | undefined;
}

export interface GuidanceProfileClearReq {
  Account: string;
  Capability?: string | undefined;
}

export interface GuidanceProfileClearResp {
  Account: string;
  Cleared: boolean;
}

export interface GuidanceProfileIncrementReq {
  Account: string;
  Capability: string;
  Signal: string;
  Evidence?: string | undefined;
}

export interface GuidanceProfileIncrementResp {
  Entry: GuidanceCapabilityEntry;
}

export interface GuidanceProfileQueryReq {
  Account: string;
}

export interface GuidanceProfileQueryResp {
  Profile?: GuidanceCapabilityProfile | undefined;
  Records?: GuidanceRecord[] | undefined;
}

export interface GuidanceProfileUpdateReq {
  Account: string;
  Capabilities: GuidanceCapabilityEntry[];
  Records?: GuidanceRecord[] | undefined;
}

export interface GuidanceProfileUpdateResp {
  Profile: GuidanceCapabilityProfile;
}

export interface GuidanceRecord {
  Id: string;
  Topic: string;
  Account?: string | undefined;
  Detail?: string | undefined;
  CreatedAt?: string | undefined;
  UpdatedAt?: string | undefined;
}

export interface GuideAnchor {
  GuideId: string;
  Area: string;
  Summary: string;
  VisibleWhen?: string | undefined;
  SuggestedGates?: string | undefined;
}

export interface GuideStep {
  TargetGuideId: string;
  Title: string;
  Body: string;
  Placement?: string | undefined;
  ExpectedInteraction?: string | undefined;
  CapabilityKind?: string | undefined;
  I18nTitleKey?: string | undefined;
  I18nBodyKey?: string | undefined;
}

export interface ImAccountCreateReq {
  Name: string;
  Provider: string;
  Token: string;
  Enabled?: boolean | undefined;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
}

export interface ImAccountCreateResp {
  Account: ImAccountView;
}

export interface ImAccountDeleteReq {
  Id: string;
}

export interface ImAccountDeleteResp {
}

export interface ImAccountListReq {
}

export interface ImAccountListResp {
  Items: ImAccountView[];
}

export interface ImAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Token?: string | undefined;
  Enabled?: boolean | undefined;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
}

export interface ImAccountUpdateResp {
  Account: ImAccountView;
}

export interface ImAccountView {
  Id: string;
  Name: string;
  Provider: string;
  Enabled: boolean;
  HasToken: boolean;
  AllowUsers?: string[] | undefined;
  ApiBase?: string | undefined;
  BotUsername?: string | undefined;
  Status?: string | undefined;
  StatusDetail?: string | undefined;
}

export interface ImRoute {
  Id: string;
  AccountId: string;
  AgentActorId: string;
  MountedAt?: string | undefined;
}

export interface ImRouteDeleteReq {
  Id: string;
}

export interface ImRouteDeleteResp {
}

export interface ImRouteListReq {
}

export interface ImRouteListResp {
  Items: ImRoute[];
}

export interface ImRouteSetReq {
  AccountId: string;
  AgentActorId: string;
}

export interface ImRouteSetResp {
  Route: ImRoute;
}

export interface ImSendReq {
  AccountId: string;
  ChatId: string;
  Text: string;
}

export interface ImSendResp {
  Sent: boolean;
}

export interface ImStatusReq {
}

export interface ImStatusResp {
  Items: ImAccountView[];
}

export interface ImageEntry {
  Url: string;
  Alt?: string | undefined;
  MimeType?: string | undefined;
}

export interface InspectAction {
  Id: string;
  Label: string;
  Target?: OpenTarget | undefined;
}

export interface InspectCapability {
  Performance: number;
  Cost: number;
  Speed: number;
}

export interface InspectContextSegment {
  Id: string;
  Label: string;
  Kind: string;
  Chars: number;
  Priority: number;
  Source?: string | undefined;
  Path?: string | undefined;
  Editable?: boolean | undefined;
  FragmentId?: string | undefined;
  Content?: string | undefined;
}

export interface InspectDocument {
  Ref: InspectRef;
  Title: string;
  Subtitle?: string | undefined;
  Summary?: string | undefined;
  Status?: InspectStatus | undefined;
  Sections: InspectSection[];
  Actions?: InspectAction[] | undefined;
  Relations?: InspectRelation[] | undefined;
  Pages?: InspectPage[] | undefined;
}

export interface InspectDocumentReq {
  Kind: string;
  Id: string;
  Scope?: Record<string, string> | undefined;
}

export interface InspectListItem {
  Label: string;
  Description?: string | undefined;
  Ref?: InspectRef | undefined;
  OpenTarget?: OpenTarget | undefined;
}

export interface InspectPage {
  Id: string;
  Label: string;
  Icon?: string | undefined;
}

export interface InspectPagesResp {
  Items: InspectPage[];
}

export interface InspectRef {
  Kind: string;
  Id: string;
  Scope?: Record<string, string> | undefined;
}

export interface InspectRelation {
  Label: string;
  Ref: InspectRef;
}

export interface InspectRow {
  Label: string;
  Value: string;
  Mono?: boolean | undefined;
  Badge?: string | undefined;
  Ref?: InspectRef | undefined;
  Tooltip?: string | undefined;
  ExpandedRows?: InspectRow[] | undefined;
}

export interface InspectSection {
  Type: string;
  Id: string;
  Title: string;
  Rows?: InspectRow[] | undefined;
  Items?: InspectListItem[] | undefined;
  Body?: string | undefined;
  Capability?: InspectCapability | undefined;
  Segments?: InspectContextSegment[] | undefined;
}

export interface InspectStatus {
  State: string;
  Label: string;
}

export interface InterfaceManagerControlReq {
  Action: string;
  Mode?: string | undefined;
  Category?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  Steps?: GuideStep[] | undefined;
  Interaction?: string | undefined;
  Text?: string | undefined;
  GuideId?: string | undefined;
  AppId?: string | undefined;
  ViewId?: string | undefined;
  TutorialId?: string | undefined;
  Title?: string | undefined;
  Description?: string | undefined;
  AutoPlay?: boolean | undefined;
}

export interface InterfaceManagerControlResp {
  Accepted: boolean;
  Error?: string | undefined;
  Anchors?: GuideAnchor[] | undefined;
  Tutorials?: TutorialSpec[] | undefined;
  TutorialId?: string | undefined;
}

export interface InterfaceManagerEvent {
  Action: string;
  Mode?: string | undefined;
  Category?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  Steps?: GuideStep[] | undefined;
  Interaction?: string | undefined;
  Text?: string | undefined;
  GuideId?: string | undefined;
  AppId?: string | undefined;
  ViewId?: string | undefined;
  Tutorial?: TutorialSpec | undefined;
  AutoPlay?: boolean | undefined;
  TutorialId?: string | undefined;
}

export interface InvokeDiagnostics {
  InFlight: number;
  InFlightByMode: Record<string, number>;
  InFlightByCall: Record<string, number>;
  Registered: number;
  Completed: number;
  Errored: number;
  SendFailed: number;
  ClosedEarly: number;
  EvictedStalled: number;
  EvictedCapacity: number;
  DroppedFrames: number;
  RecentEvictions?: InvokeEvictionRecord[] | undefined;
}

export interface InvokeEvictionRecord {
  CallID?: string | undefined;
  Mode?: string | undefined;
  CorID?: string | undefined;
  Reason?: string | undefined;
  Drops?: number | undefined;
  At?: string | undefined;
}

export interface ListPluginsResp {
  Plugins: PluginDescriptor[];
}

export interface ListResp {
  tasks: Task[];
}

export interface LoginDoneReq {
  id: string;
}

export interface LspClearCacheReq {
  Evicted: number;
}

export interface LspDiagnosticsEvent {
  Uri: string;
  Version: number;
  Diagnostics: string;
}

export interface LspDidChangeReq {
  Uri: string;
  Version: number;
  Changes: string;
  RootUri: string;
  Language: string;
}

export interface LspDidCloseReq {
  Uri: string;
  RootUri: string;
  Language: string;
}

export interface LspDidOpenReq {
  Uri: string;
  LanguageId: string;
  Text: string;
  Version: number;
  RootUri: string;
  Language: string;
}

export interface LspInitializeReq {
  RootUri: string;
  Language: string;
}

export interface LspInstallProgressEvent {
  Language: string;
  State: string;
  Percent: number;
  Error?: string | undefined;
}

export interface LspInstallReq {
  Language: string;
}

export interface LspInstallResp {
  Started: boolean;
}

export interface LspJsonResp {
  Json: string;
}

export interface LspLanguageInstallState {
  Language: string;
  Installed: boolean;
  Version?: string | undefined;
  BinaryPath?: string | undefined;
  DownloadSource?: string | undefined;
}

export interface LspLanguageState {
  Language: string;
  Enabled: boolean;
}

export interface LspPositionParams {
  Uri: string;
  Line: number;
  Character: number;
  RootUri: string;
  Language: string;
}

export interface LspRangeParams {
  Uri: string;
  StartLine: number;
  StartChar: number;
  EndLine: number;
  EndChar: number;
  RootUri: string;
  Language: string;
}

export interface LspReferencesReq {
  Uri: string;
  Line: number;
  Character: number;
  IncludeDeclaration: boolean;
  RootUri: string;
  Language: string;
}

export interface LspRenameReq {
  Uri: string;
  Line: number;
  Character: number;
  NewName: string;
  RootUri: string;
  Language: string;
}

export interface LspShutdownReq {
  RootUri: string;
  Language: string;
}

export interface LspStateResp {
  Languages: LspLanguageState[];
}

export interface LspStateSaveReq {
  Language: string;
  Enabled: boolean;
}

export interface LspStatusReq {
  Language: string;
}

export interface LspStatusResp {
  Languages: LspLanguageInstallState[];
}

export interface LspUriReq {
  Uri: string;
  RootUri: string;
  Language: string;
}

export interface ManualCallableUnit {
  Model: string;
  Endpoint: string;
  ProviderName: string;
  Protocol: string;
  aggregatorID?: string | undefined;
  IsReasoning?: boolean | undefined;
  UserAgent?: string | undefined;
  Proxy?: string | undefined;
  MaxConcurrency?: number | undefined;
  MaxContextLength?: number | undefined;
  Modality?: string | undefined;
  CooldownUntil?: number | undefined;
  DisableUntil?: number | undefined;
  Disabled?: boolean | undefined;
  ReasoningEffort?: string | undefined;
  IsTokenPlan?: boolean | undefined;
  TokenPlanExpiresAt?: string | undefined;
  TokenPlanRemainingPct?: number | undefined;
  TokenPlanWindowMs?: number | undefined;
  HealthState?: string | undefined;
  HealthReason?: string | undefined;
  RecoveryMode?: string | undefined;
}

export interface McpAddServerReq {
  Config: McpServerConfig;
}

export interface McpAddServerResp {
  Server: McpServerView;
}

export interface McpCallToolReq {
  Id: string;
  Tool: string;
  Arguments: Record<string, unknown>;
}

export interface McpCallToolResp {
  Content: McpToolContent[];
  IsError?: boolean | undefined;
  Error?: string | undefined;
}

export interface McpConnectReq {
  Id: string;
}

export interface McpConnectResp {
  Status: McpServerStatus;
}

export interface McpDisconnectReq {
  Id: string;
}

export interface McpDisconnectResp {
  Status: McpServerStatus;
}

export interface McpDiscoverToolsResp {
  Servers: McpServerTools[];
}

export interface McpEnvVarEntry {
  Key: string;
  Configured: boolean;
}

export interface McpHttpTransport {
  Url: string;
  Headers: Record<string, string>;
  Proxy?: string | undefined;
}

export interface McpHttpTransportView {
  Url: string;
  Headers: McpEnvVarEntry[];
  Proxy?: string | undefined;
}

export interface McpListServersResp {
  Items: McpServerView[];
}

export interface McpReconnectReq {
  Id: string;
}

export interface McpReconnectResp {
  Status: McpServerStatus;
}

export interface McpRemoveServerReq {
  Id: string;
}

export interface McpRemoveServerResp {
}

export interface McpServerConfig {
  Id: string;
  Name: string;
  Transport: string;
  Stdio?: McpStdioTransport | undefined;
  Http?: McpHttpTransport | undefined;
  Enabled: boolean;
}

export interface McpServerStatus {
  Id: string;
  Connected: boolean;
  ToolCount: number;
  Error?: string | undefined;
}

export interface McpServerStatusEvent {
  Status: McpServerStatus;
}

export interface McpServerTools {
  Id: string;
  Name: string;
  Tools: McpToolView[];
}

export interface McpServerView {
  Id: string;
  Name: string;
  Transport: string;
  Stdio?: McpStdioTransportView | undefined;
  Http?: McpHttpTransportView | undefined;
  Enabled: boolean;
  Status: McpServerStatus;
}

export interface McpStdioTransport {
  Command: string;
  Args: string[];
  Env: Record<string, string>;
}

export interface McpStdioTransportView {
  Command: string;
  Args: string[];
  Env: McpEnvVarEntry[];
}

export interface McpToolContent {
  Type: string;
  Text: string;
  Data?: string | undefined;
  MimeType?: string | undefined;
}

export interface McpToolView {
  Name: string;
  Description: string;
  InputSchema: string;
}

export interface McpUpdateServerReq {
  Id: string;
  Config: McpServerConfig;
}

export interface McpUpdateServerResp {
  Server: McpServerView;
}

export interface MediaAccountActivateReq {
  Kind: string;
  Id: string;
}

export interface MediaAccountActivateResp {
  ActiveId: string;
}

export interface MediaAccountCreateReq {
  Kind: string;
  Name: string;
  Provider: string;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountCreateResp {
  Account: MediaAccountView;
}

export interface MediaAccountDeleteReq {
  Id: string;
}

export interface MediaAccountDeleteResp {
}

export interface MediaAccountListReq {
  Kind?: string | undefined;
}

export interface MediaAccountListResp {
  Items: MediaAccountView[];
  ActiveId?: string | undefined;
  BoundProvider?: string | undefined;
  BoundModel?: string | undefined;
  BoundAggregator?: string | undefined;
}

export interface MediaAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Provider?: string | undefined;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaAccountUpdateResp {
  Account: MediaAccountView;
}

export interface MediaAccountView {
  Id: string;
  Kind: string;
  Name: string;
  Provider: string;
  HasApiKey: boolean;
  Model?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface MediaProviderModelSetReq {
  Kind: string;
  Provider?: string | undefined;
  Model?: string | undefined;
  Aggregator?: string | undefined;
}

export interface MediaProviderModelSetResp {
  Kind: string;
  Provider?: string | undefined;
  Model?: string | undefined;
  Aggregator?: string | undefined;
}

export interface MemoryEdge {
  From: string;
  To: string;
  Type: string;
}

export interface MemoryNode {
  Id: string;
  Layer: string;
  Content: string;
  Head: string;
  Tokens: number;
  Energy: number;
  CreatedAt: string;
  AccessedAt: string;
  MarkedForDeath: boolean;
  Protected: boolean;
  LastAccessTick: number;
}

export interface MemoryRecallReq {
  Query?: string | undefined;
  Layer?: string | undefined;
  Limit?: number | undefined;
}

export interface MemoryRecallResp {
  Nodes: MemoryNode[];
}

export interface MemorySaveReq {
  Content: string;
  Layer?: string | undefined;
}

export interface MemorySaveResp {
  Node: MemoryNode;
}

export interface MemorySnapshotReq {
}

export interface MemorySnapshotResp {
  Mounted: boolean;
  SleepDue: boolean;
  Nodes: MemoryNode[];
  Edges: MemoryEdge[];
}

export interface MiddlewareEntry {
  Hook: string;
  Middleware: string;
  Params?: Record<string, unknown> | undefined;
}

export interface Model {
  Id: string;
  Provider: string;
  MaxTokens: number;
  Cost: string;
}

export interface ModelDefault {
  Prefix: string;
  MaxContextLength: number;
  MaxTokens?: number | undefined;
  Modality?: string | undefined;
  CostInput?: number | undefined;
  CostOutput?: number | undefined;
}

export interface ModelListResp {
  Items: Model[];
}

export interface ModelRef {
  kind: string;
  Unit?: ModelUnit | undefined;
  AggregatorID?: string | undefined;
}

export interface ModelSlot {
  Candidates: ModelRef[];
}

export interface ModelUnit {
  model: string;
  provider: string;
  thinkLevel?: string | undefined;
}

export interface MonoCardListItem {
  Id: string;
  Type: string;
  Source: string;
  Storage: string;
  Visibility: string;
  Tags: string[];
  List: string[];
  Created: string;
  Modified: string;
  Protected: boolean;
  Editable: boolean;
  Deletable: boolean;
  Due?: string | undefined;
  Priority?: string | undefined;
  Status?: string | undefined;
  Parent?: string | undefined;
  Data?: Record<string, unknown> | undefined;
  Standalone?: boolean | undefined;
  Raw: string;
}

export interface NativeBuildReq {
  ProjectId: string;
  AppId: string;
  EntryModule?: string | undefined;
  AppDir?: string | undefined;
  SourceRoot?: string | undefined;
  TargetOS?: string | undefined;
  TargetArch?: string | undefined;
  ArtifactHash?: string | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  Mode?: string | undefined;
}

export interface NativeBuildResp {
  Result: NativeBuildResult;
  ManifestPath: string;
  Abi: PluginAbi;
}

export interface NativeBuildResult {
  Success: boolean;
  ArtifactPath: string;
  ArtifactHash: string;
  Diagnostic: string;
}

export interface OpenTarget {
  View: string;
  Kind?: string | undefined;
  Id?: string | undefined;
  Scope?: Record<string, string> | undefined;
  Params?: Record<string, string> | undefined;
}

export interface OracleCapabilityDiscoverReq {
  AgentKind: string;
  Intent?: string | undefined;
  Task?: string | undefined;
  CompressedHistory?: string | undefined;
  InstalledSkillIDs?: string[] | undefined;
  AvailableToolNames?: string[] | undefined;
  Limit?: number | undefined;
}

export interface OracleCapabilityDiscoverResp {
  Items: CapabilityCandidate[];
}

export interface OracleCapabilityExplainReq {
  Id: string;
  AgentKind?: string | undefined;
  Intent?: string | undefined;
  Task?: string | undefined;
}

export interface OracleCapabilityExplainResp {
  Summary: string;
  ExpandMode?: string | undefined;
  WhenToUse?: string | undefined;
  Examples?: string | undefined;
  RelatedIds?: string[] | undefined;
}

export interface OracleGetDiagnosticReq {
  Id: string;
}

export interface OracleListDiagnosticsReq {
  Severity?: string | undefined;
  Source?: string | undefined;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  Since?: string | undefined;
  Limit?: number | undefined;
  Offset?: number | undefined;
}

export interface OracleListDiagnosticsResp {
  Items: DiagnosticSummary[];
}

export interface OracleReportDiagnosticReq {
  Severity: string;
  Source: string;
  Message: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface OracleSearchServicesReq {
  Query?: string | undefined;
  Limit?: number | undefined;
}

export interface OracleSearchServicesResp {
  Items: ServiceInfo[];
}

export interface PassView {
  Type: string;
  StartsAt?: string | undefined;
  EndsAt?: string | undefined;
}

export interface PendingWorkflowStart {
  MapCardId: string;
  Destination: string;
  InterpretedGoal: string;
  FrontierSummary: string;
  RequestId: string;
  NonCoding?: boolean | undefined;
}

export interface PermissionEntry {
  Role: string;
  Actions: Record<string, boolean>;
}

export interface PermissionMatrix {
  Entries: PermissionEntry[];
}

export interface PermissionUpdateReq {
  Role: string;
  Actions: Record<string, boolean>;
}

export interface PlanAllowedPrompt {
  Tool: string;
  Prompt: string;
}

export interface PluginAbi {
  Name: string;
  Version: number;
  Encoding: string;
  InvokeSymbol: string;
  ContractVersion?: string | undefined;
  Isolation?: string | undefined;
  TrustClass?: string | undefined;
  Signer?: string | undefined;
  Capabilities?: string[] | undefined;
}

export interface PluginAgentBinding {
  Name: string;
  DisplayName?: string | undefined;
  SystemPrompt?: string | undefined;
  Bundles?: string[] | undefined;
  Model?: string | undefined;
}

export interface PluginAppDataUsageItem {
  PluginId: string;
  DataDir: string;
  Bytes: number;
  Files: number;
  Loaded?: boolean | undefined;
}

export interface PluginAppDataUsageReq {
  PluginId?: string | undefined;
}

export interface PluginAppDataUsageResp {
  Items: PluginAppDataUsageItem[];
  TotalBytes: number;
  SoftLimitBytes: number;
}

export interface PluginArtifactLoadReq {
  Manifest: AppManifest;
  Abi: PluginAbi;
  ArtifactPath: string;
  ArtifactHash?: string | undefined;
  EntrySymbol?: string | undefined;
  OnLoadConfig?: Uint8Array | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
}

export interface PluginArtifactLoadResp {
  Status: AppStatus;
  PluginId: string;
  ArtifactHash: string;
  HttpAddr?: string | undefined;
}

export interface PluginArtifactReloadAbortReq {
  Token: string;
}

export interface PluginArtifactReloadAbortResp {
}

export interface PluginArtifactReloadCommitReq {
  Token: string;
}

export interface PluginArtifactReloadCommitResp {
  Status: AppStatus;
  PluginId: string;
  ArtifactHash: string;
  HttpAddr?: string | undefined;
}

export interface PluginArtifactReloadPrepareReq {
  Manifest: AppManifest;
  Abi: PluginAbi;
  ArtifactPath: string;
  ArtifactHash?: string | undefined;
  EntrySymbol?: string | undefined;
  Assets?: Record<string, Uint8Array> | undefined;
  OnLoadConfig?: Uint8Array | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
}

export interface PluginArtifactReloadPrepareResp {
  Token: string;
  PluginId: string;
  ArtifactHash: string;
  Status: AppStatus;
}

export interface PluginArtifactUnloadReq {
  PluginId: string;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
}

export interface PluginArtifactUnloadResp {
  Removed: number;
}

export interface PluginAssetsPutReq {
  PluginId: string;
  Assets: Record<string, Uint8Array>;
}

export interface PluginAssetsPutResp {
  Registered: number;
}

export interface PluginAssetsRemoveReq {
  PluginId: string;
}

export interface PluginAssetsRemoveResp {
  Removed: number;
}

export interface PluginDescriptor {
  ID: string;
  Name: string;
  Version: string;
  Namespace: string;
  Runtime: string;
  AbiName: string;
  AbiVersion: number;
  Encoding: string;
  SchemaHash: string;
  Capabilities: string[];
  Callables: string[];
  Isolation: string;
  TrustClass: string;
  Signer: string;
  Trusted: boolean;
  LoadedAt: string;
  Status: string;
  Error: string;
}

export interface PluginDomPutReq {
  PluginId: string;
  Snapshot: string;
  Ts: number;
}

export interface PluginDomPutResp {
}

export interface PluginDomReq {
  PluginId: string;
}

export interface PluginDomResp {
  PluginId: string;
  Found: boolean;
  Snapshot?: string | undefined;
  CapturedAt?: number | undefined;
  Reason?: string | undefined;
}

export interface PluginEventDeliverReq {
  Kind: string;
  Payload: string;
}

export interface PluginEventDeliverResp {
  Delivered: number;
  Failures?: string[] | undefined;
}

export interface PluginInvokeChunk {
  Payload: Uint8Array;
  Terminal?: boolean | undefined;
}

export interface PluginInvokeReq {
  Id: string;
  Callable: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  WorkspaceId?: string | undefined;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
  TimeoutMs?: number | undefined;
}

export interface PluginInvokeResp {
  Payload: Uint8Array;
}

export interface PluginLogEntry {
  Time: string;
  Level: string;
  Source: string;
  Message: string;
}

export interface PluginLogPutReq {
  PluginId: string;
  Level: string;
  Message: string;
  ViewId?: string | undefined;
}

export interface PluginLogsReq {
  PluginId: string;
  Limit?: number | undefined;
}

export interface PluginLogsResp {
  PluginId: string;
  Generation: number;
  Dropped?: number | undefined;
  Entries?: PluginLogEntry[] | undefined;
  ProcessState?: string | undefined;
  Crash?: string | undefined;
}

export interface PluginProxyAttachReq {
  PluginId: string;
  Addr: string;
  Token: string;
}

export interface PluginProxyAttachResp {
}

export interface PluginProxyDetachReq {
  PluginId: string;
}

export interface PluginProxyDetachResp {
}

export interface PluginStateAppendReq {
  Plugin: string;
  Key: string;
  Data: Uint8Array;
}

export interface PluginStateAppendResp {
}

export interface PluginStateDeleteReq {
  Plugin: string;
  Key: string;
}

export interface PluginStateDeleteResp {
  Removed: boolean;
}

export interface PluginStateGetManyReq {
  Plugin: string;
  Keys: string[];
}

export interface PluginStateGetManyResp {
  Entries: Record<string, Uint8Array>;
}

export interface PluginStateGetReq {
  Plugin: string;
  Key: string;
}

export interface PluginStateGetResp {
  Value: Uint8Array;
  Found?: boolean | undefined;
}

export interface PluginStateListReq {
  Plugin: string;
  Prefix?: string | undefined;
}

export interface PluginStateListResp {
  Keys: string[];
}

export interface PluginStatePurgeReq {
  PluginId: string;
}

export interface PluginStatePurgeResp {
  Removed: boolean;
}

export interface PluginStateSetManyReq {
  Plugin: string;
  Entries: Record<string, Uint8Array>;
}

export interface PluginStateSetManyResp {
}

export interface PluginStateSetReq {
  Plugin: string;
  Key: string;
  Value: Uint8Array;
}

export interface PluginStateSetResp {
}

export interface PolicyAnswer {
  Type: string;
  Choice?: string | undefined;
  Score?: number | undefined;
  Noul?: number | undefined;
  Probabilities?: Record<string, number> | undefined;
  Confidence?: number | undefined;
  Backend: string;
  Calibrated: boolean;
}

export interface PolicyConfigureReq {
  Backend?: string | undefined;
  Jev?: PolicyJevConfig | undefined;
  LLM?: PolicyLLMConfig | undefined;
}

export interface PolicyConfigureResp {
  Status: PolicyStatusResp;
}

export interface PolicyDecideReq {
  State: string;
  Questions: Record<string, PolicyQuestion>;
  Backend?: string | undefined;
}

export interface PolicyDecideResp {
  Answers: Record<string, PolicyAnswer>;
  Backend: string;
  Degraded: boolean;
  LatencyMs: number;
  FailoverReason?: string | undefined;
  Error?: string | undefined;
}

export interface PolicyJevConfig {
  ApiKey?: string | undefined;
  Model?: string | undefined;
  Endpoint?: string | undefined;
}

export interface PolicyLLMConfig {
  Unit?: ModelUnit | undefined;
}

export interface PolicyQuestion {
  Type: string;
  Instructions: string;
  Choices?: Record<string, string> | undefined;
  Levels?: string[] | undefined;
}

export interface PolicyStatusReq {
}

export interface PolicyStatusResp {
  Backend: string;
  JevConfigured: boolean;
  LLMConfigured: boolean;
  LLMUnit?: ModelUnit | undefined;
  JevModel?: string | undefined;
  JevEndpoint?: string | undefined;
}

export interface ProbeTokensResp {
  Usage: UsageData;
}

export interface ProjectCardListReq {
}

export interface ProjectCardListResp {
  Refs: CardRef[];
}

export interface ProjectCardMountReq {
  Ref: CardRef;
}

export interface ProjectCardMountResp {
  Ref: CardRef;
}

export interface ProjectCardUnmountReq {
  Id: string;
}

export interface ProjectCardUnmountResp {
  Id: string;
}

export interface ProjectComponentGetReq {
  CardId: string;
}

export interface ProjectComponentGetResp {
  Component: ComponentDescriptor;
}

export interface ProjectComponentListReq {
}

export interface ProjectComponentListResp {
  Items: ComponentDescriptor[];
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

export interface ProjectFileChangedEvent {
  Path: string;
  Kind: string;
}

export interface ProjectGitAddReq {
  Paths: string[];
}

export interface ProjectGitBlameLine {
  Hash: string;
  Author: string;
  Email: string;
  When: string;
  Line: number;
  Content: string;
}

export interface ProjectGitBlameReq {
  FilePath: string;
}

export interface ProjectGitBlameResp {
  Lines: ProjectGitBlameLine[];
}

export interface ProjectGitBranchInfo {
  Name: string;
  Current: boolean;
  Hash: string;
  IsRemote: boolean;
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

export interface ProjectGitCommitInfo {
  Hash: string;
  Short: string;
  Message: string;
  Author: string;
  Email: string;
  When: string;
  Parents?: string[] | undefined;
}

export interface ProjectGitCommitReq {
  Message: string;
}

export interface ProjectGitCommitResp {
  Hash: string;
  Short: string;
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

export interface ProjectGitDiffReq {
  FilePath: string;
  CommitHash: string;
  Repo?: string | undefined;
}

export interface ProjectGitDiffResp {
  Diff: string[];
}

export interface ProjectGitFileStatus {
  Path: string;
  Staging: string;
  Worktree: string;
}

export interface ProjectGitLogReq {
  Limit: number;
  Repo?: string | undefined;
}

export interface ProjectGitLogResp {
  Commits: ProjectGitCommitInfo[];
}

export interface ProjectGitPullReq {
  Remote: string;
}

export interface ProjectGitPushReq {
  Remote: string;
}

export interface ProjectGitRemoteAddReq {
  Name: string;
  Url: string;
}

export interface ProjectGitRemoteInfo {
  Name: string;
  Urls: string[];
}

export interface ProjectGitRemoteListReq {
}

export interface ProjectGitRemoteListResp {
  Remotes: ProjectGitRemoteInfo[];
}

export interface ProjectGitRemoteRemoveReq {
  Name: string;
}

export interface ProjectGitResetReq {
  Paths?: string[] | undefined;
}

export interface ProjectGitStashDropReq {
  Index?: number | undefined;
}

export interface ProjectGitStashInfo {
  Index: number;
  Message: string;
  Hash: string;
  When: string;
}

export interface ProjectGitStashListReq {
  Repo?: string | undefined;
}

export interface ProjectGitStashListResp {
  Stashes: ProjectGitStashInfo[];
}

export interface ProjectGitStashPopReq {
  Index?: number | undefined;
  Repo?: string | undefined;
}

export interface ProjectGitStashSaveReq {
  Message?: string | undefined;
  Repo?: string | undefined;
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

export interface ProjectGraphConceptGetReq {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  ConceptId: string;
}

export interface ProjectGraphConceptGetResp {
  Concept: Concept;
  GraphKind: string;
  Id: string;
  Revision: string;
}

export interface ProjectGraphEnvelopeResp {
  Meta: GraphSnapshotMeta;
  EnvelopeText: string;
}

export interface ProjectGraphGetReq {
  ProjectId: string;
  GraphKind: string;
  Id?: string | undefined;
  Revision?: string | undefined;
}

export interface ProjectGraphSaveReq {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  ExpectedRevision?: string | undefined;
  Meta: GraphSnapshotMeta;
  EnvelopeText: string;
}

export interface ProjectInfoResp {
  Roots: ProjectInfoRoot[];
}

export interface ProjectInfoRoot {
  Name: string;
  Path: string;
}

export interface ProjectMount {
  Name: string;
  Path: string;
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

export interface ProjectRefListResp {
  Items: ProjectRef[];
}

export interface ProjectReviewChangesetReq {
  AgentActorID: string;
  ForceRefresh?: boolean | undefined;
  CallerAgentId?: string | undefined;
  TestCommand?: string | undefined;
  TestTimeoutMs?: number | undefined;
  TaskCardID?: string | undefined;
}

export interface ProjectReviewChangesetStats {
  TrackedChanged: number;
  Untracked: number;
  AddedLines: number;
  DeletedLines: number;
  TotalDiffLines: number;
}

export interface ProjectReviewChangesetSummaryResp {
  AgentActorID: string;
  WorktreeID: string;
  GeneratedAt: string;
  Status: string;
  ErrorMsg: string;
  Baseline: string;
  Head: string;
  Branch: string;
  IsDirty: boolean;
  Commits: ProjectReviewCommitInfo[];
  Files: ProjectReviewFileEntry[];
  UntrackedFiles: ProjectReviewUntrackedFile[];
  TestResult: ProjectReviewTestResult;
  Stats: ProjectReviewChangesetStats;
}

export interface ProjectReviewCommitInfo {
  Hash: string;
  Short: string;
  Message: string;
  Author: string;
  When: string;
}

export interface ProjectReviewFileContentReq {
  AgentActorID: string;
  FilePath: string;
  Offset?: number | undefined;
  Limit?: number | undefined;
  CallerAgentId?: string | undefined;
}

export interface ProjectReviewFileContentResp {
  FilePath: string;
  Status: string;
  Content: string;
  IsBinary: boolean;
  IsGenerated: boolean;
  StartLine: number;
  NumLines: number;
  TotalLines: number;
  Truncated: boolean;
}

export interface ProjectReviewFileEntry {
  Path: string;
  Status: string;
  OldPath: string;
  IsBinary: boolean;
  IsGenerated: boolean;
  DiffLines: number;
}

export interface ProjectReviewTestResult {
  Command: string;
  ExitCode: number;
  Output: string;
  Duration: string;
  Success: boolean;
  Skipped: boolean;
  ErrorMsg: string;
}

export interface ProjectReviewUntrackedFile {
  Path: string;
  Size: number;
  IsBinary: boolean;
  IsText: boolean;
  Preview: string;
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

export interface ProjectSyncRootsReq {
  Roots: ProjectInfoRoot[];
}

export interface ProjectTaskValidateOutputsReq {
  CardID: string;
  Outputs: Record<string, unknown>;
}

export interface ProjectTaskValidateOutputsResp {
  Valid: boolean;
  Errors: CardValidationError[];
}

export interface ProjectWatchFileReq {
  Path: string;
  Remove?: boolean | undefined;
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

export interface ProjectWorktreeCopyReq {
  WorktreeID: string;
  Sources: string[];
  DestDir?: string | undefined;
  Force?: boolean | undefined;
}

export interface ProjectWorktreeCopyResp {
  Copied: ProjectWorktreeCopyResult[];
  Skipped: ProjectWorktreeCopySkipped[];
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

export interface ProjectWorktreeCreateReq {
  Name: string;
  BaseRef: string;
  ParentWorktreeID?: string | undefined;
  WorkflowMapID?: string | undefined;
}

export interface ProjectWorktreeDiscardReq {
  WorktreeID: string;
  Force?: boolean | undefined;
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

export interface ProjectWorktreeGetReq {
  WorktreeID: string;
}

export interface ProjectWorktreeListReq {
}

export interface ProjectWorktreeListResp {
  Worktrees: ProjectWorktree[];
}

export interface PromptArtifact {
  Sections: string[];
  Fragments: PromptFragment[];
  ContextSegments?: PromptContextSegment[] | undefined;
  SummarySegments?: SummarySegment[] | undefined;
  ContextWindowSize?: number | undefined;
  TokenBudget?: number | undefined;
}

export interface PromptContextSegment {
  Id: string;
  FragmentId?: string | undefined;
  Chars: number;
}

export interface PromptFragment {
  Id?: string | undefined;
  Key?: string | undefined;
  BuiltinKey?: string | undefined;
  Name: string;
  Kind: string;
  Priority: number;
  Content?: string | undefined;
  Scope?: string | undefined;
  Role?: string | undefined;
  Source?: string | undefined;
  Path?: string | undefined;
  SourceRange?: string | undefined;
  Editable: boolean;
  Deletable: boolean;
}

export interface PromptRef {
  Kind: string;
  Key: string;
  Placement?: string | undefined;
}

export interface Provider {
  Name: string;
  Kind: string;
  Endpoint: string;
  Models: ProviderModel[];
  AuthToken?: string | undefined;
  ReadOnly?: boolean | undefined;
  MaxConcurrency?: number | undefined;
  HasAuthToken?: boolean | undefined;
  CooldownUntil?: number | undefined;
  HealthState?: string | undefined;
  HealthReason?: string | undefined;
  LastFailureAt?: number | undefined;
  RecoveryMode?: string | undefined;
  UserAgent?: string | undefined;
  Proxy?: string | undefined;
  DisableWindows?: ProviderDisableWindow[] | undefined;
  DisableUntil?: number | undefined;
  Disabled?: boolean | undefined;
  IsTokenPlan?: boolean | undefined;
  TokenPlanExpiresAt?: string | undefined;
  TokenPlanRemainingPct?: number | undefined;
  TokenPlanWindowMs?: number | undefined;
}

export interface ProviderDisableWindow {
  Start: string;
  End: string;
  Days?: number[] | undefined;
}

export interface ProviderListResp {
  Items: Provider[];
}

export interface ProviderModel {
  Name: string;
  MaxContextLength?: number | undefined;
  MaxTokens?: number | undefined;
  Modality?: string | undefined;
  Protocol?: string | undefined;
  CostInput?: number | undefined;
  CostOutput?: number | undefined;
  CostCacheRead?: number | undefined;
  CostCacheWrite?: number | undefined;
  CostTiers?: ProviderModelCostTier[] | undefined;
  ProbeState?: string | undefined;
  ProbeLatencyMs?: number | undefined;
  ProbeError?: string | undefined;
  ProbeAt?: number | undefined;
  Disabled?: boolean | undefined;
}

export interface ProviderModelCostTier {
  InputTokensAbove: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite?: number | undefined;
}

export interface PuppetAssetCommitReq {
  AssetId: string;
  NodeGuid: string;
}

export interface PuppetAssetCommitResp {
  Asset: PuppetStagedAsset;
  Revision: PuppetRevision;
}

export interface PuppetAssetListReq {
  State?: string | undefined;
}

export interface PuppetAssetListResp {
  Assets: PuppetStagedAsset[];
}

export interface PuppetAssetReadReq {
  AssetId: string;
}

export interface PuppetAssetReadResp {
  AssetId: string;
  Uri: string;
  MimeType?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
}

export interface PuppetAssetRejectReq {
  AssetId: string;
  Reason?: string | undefined;
}

export interface PuppetAssetRejectResp {
  Asset: PuppetStagedAsset;
}

export interface PuppetAssetStageReq {
  Kind: string;
  DataRef: string;
  Name?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
  SourceRef?: string | undefined;
  GenParamsSummary?: string | undefined;
}

export interface PuppetAssetStageResp {
  Asset: PuppetStagedAsset;
}

export interface PuppetDocument {
  Id: string;
  Revision: number;
  Name: string;
  SourcePath?: string | undefined;
  Root: PuppetNode;
  Params?: PuppetParam[] | undefined;
  ParamValues?: Record<string, string> | undefined;
  Bindings?: PuppetParamBinding[] | undefined;
  CreatedAt: string;
  UpdatedAt: string;
}

export interface PuppetDocumentRevertToReq {
  RevisionId: string;
}

export interface PuppetDocumentRevertToResp {
  Applied: boolean;
  Document: PuppetDocument;
  Revision: PuppetRevision;
  Detail?: string | undefined;
}

export interface PuppetDocumentSnapshot {
  Document: PuppetDocument;
  StagedAssetCount: number;
  RevisionCount: number;
}

export interface PuppetDocumentSnapshotReq {
}

export interface PuppetDocumentSnapshotResp {
  Snapshot: PuppetDocumentSnapshot;
}

export interface PuppetEditCommand {
  Kind: string;
  TargetGuid: string;
  Author: string;
  Params?: Record<string, string> | undefined;
  RequestId?: string | undefined;
}

export interface PuppetEditReq {
  Command: PuppetEditCommand;
}

export interface PuppetEditResp {
  Applied: boolean;
  Revision: PuppetRevision;
  Detail?: string | undefined;
  AffectedGuids?: string[] | undefined;
}

export interface PuppetMesh {
  Vertices?: number[] | undefined;
  Indices?: number[] | undefined;
  UVs?: number[] | undefined;
}

export interface PuppetNode {
  Guid: string;
  Name: string;
  Kind: string;
  Enabled: boolean;
  Z?: number | undefined;
  TextureAssetId?: string | undefined;
  Mesh?: PuppetMesh | undefined;
  Params?: Record<string, string> | undefined;
  Children: PuppetNode[];
}

export interface PuppetParam {
  Id: string;
  Name: string;
  Kind: string;
  Default: string;
  Min?: string | undefined;
  Max?: string | undefined;
  Step?: string | undefined;
  Group?: string | undefined;
  NoInherit?: boolean | undefined;
}

export interface PuppetParamBinding {
  ParamId: string;
  NodeGuid: string;
  Property: string;
  Multiplier?: number | undefined;
  Offset?: number | undefined;
}

export interface PuppetParamListReq {
}

export interface PuppetParamListResp {
  Params: PuppetParam[];
  Bindings: PuppetParamBinding[];
}

export interface PuppetRevision {
  Id: string;
  DocumentId: string;
  Sequence: number;
  ParentRevisionId: string;
  Author: string;
  CommandKind: string;
  AuditSource?: string | undefined;
  Summary?: string | undefined;
  AffectedGuids?: string[] | undefined;
  Timestamp: string;
}

export interface PuppetRevisionLogReq {
  Limit?: number | undefined;
}

export interface PuppetRevisionLogResp {
  Revisions: PuppetRevision[];
}

export interface PuppetStagedAsset {
  Id: string;
  DocumentId: string;
  State: string;
  Kind: string;
  DataRef: string;
  Name?: string | undefined;
  Width?: number | undefined;
  Height?: number | undefined;
  SourceRef?: string | undefined;
  GenParamsSummary?: string | undefined;
  NodeGuid?: string | undefined;
  CreatedAt: string;
  ReviewedAt?: string | undefined;
  ReviewReason?: string | undefined;
}

export interface PuppetViewportCaptureReq {
  Width?: number | undefined;
  Height?: number | undefined;
  Format?: string | undefined;
  Quality?: number | undefined;
}

export interface PuppetViewportCaptureResp {
  Uri: string;
  Width: number;
  Height: number;
  MimeType?: string | undefined;
  Source?: string | undefined;
}

export interface QueryInteractionsReq {
  Since?: string | undefined;
  GuideId?: string | undefined;
  Kind?: string | undefined;
  Limit?: number | undefined;
}

export interface QueryInteractionsResp {
  Items: UiInteractionRecord[];
}

export interface RandomNameConfig {
  Enabled: boolean;
  Prefixes?: string[] | undefined;
  Suffixes?: string[] | undefined;
}

export interface RawSession {
  SummarySegments: SummarySegment[];
  Artifacts: RawSessionArtifact[];
  CompactionEvents: CompactionEvent[];
  CompactionLock?: CompactionLockState | undefined;
  Tasks?: TurnTask[] | undefined;
  TaskHistory?: Record<string, TurnTask> | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  NextIdx: number;
  NextSeq: number;
  NextTurnOrder: number;
  RetentionPolicy: string;
  Goal?: SessionGoal | undefined;
  ActiveWorkflow?: ActiveWorkflow | undefined;
  ActiveScheduler?: ActiveSchedulerEntry[] | undefined;
  PendingWorkflowStart?: PendingWorkflowStart | undefined;
}

export interface RawSessionArtifact {
  Kind: string;
  Visibility: string;
  Retention: string;
  Payload: string;
}

export interface RefreshTokenEntry {
  Token: string;
  UserID: string;
  ExpiresAt: string;
}

export interface ReportInteractionReq {
  Kind: string;
  GuideId: string;
  Label?: string | undefined;
  View?: string | undefined;
  Detail?: string | undefined;
}

export interface ReportInteractionResp {
  Accepted: boolean;
}

export interface RuntimeBuildInfoReq {
}

export interface RuntimeBuildInfoResp {
  Version: string;
  BuildType?: string | undefined;
  BuildFlavor?: string | undefined;
  Commit?: string | undefined;
  BuildTime?: string | undefined;
  Dirty?: boolean | undefined;
  PublicVersion?: string | undefined;
  Channel?: string | undefined;
}

export interface RuntimeListServicesReq {
}

export interface RuntimeListServicesResp {
  Items: ServiceInfo[];
}

export interface SaveAccountPreferencesCommand {
  RequestId: string;
  AccountId: string;
  Version: number;
  Preferences: Record<string, string>;
}

export interface SaveWorkspaceAIShellCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  AiShell: WorkspaceAIShellState;
}

export interface SaveWorkspaceDockCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  Dock: WorkspaceDockState;
}

export interface SaveWorkspaceExplorerCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
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

export interface SaveWorkspaceProjectCardBrowserCommand {
  RequestId: string;
  WorkspaceId: string;
  Version: number;
  ProjectCardBrowser: WorkspaceProjectBrowserState;
}

export interface SendSessionMessageReq {
  SessionId: string;
  WorkspaceId?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  Unit?: ModelUnit | undefined;
  Title?: string | undefined;
  System?: string | undefined;
  SystemBlocks?: SystemContentBlock[] | undefined;
  Messages?: ChatMessage[] | undefined;
  Tools?: ToolSpec[] | undefined;
  ThinkingBudget?: number | undefined;
  ReasoningEffort?: string | undefined;
  Temperature?: number | undefined;
  TopP?: number | undefined;
  FrequencyPenalty?: number | undefined;
  PresencePenalty?: number | undefined;
  Seed?: number | undefined;
  ToolChoice?: string | undefined;
  SlotKind?: string | undefined;
  UnitPinned?: boolean | undefined;
}

export interface ServiceInfo {
  Name: string;
  ActorId: string;
  ActorType: string;
}

export interface Session {
  Turns?: Turn[] | undefined;
  ActiveHead?: number | undefined;
}

export interface SessionGoal {
  Condition: string;
  MaxTurns: number;
  TurnCount: number;
  CardID?: string | undefined;
  InterpretedGoal?: string | undefined;
  Confirmed?: boolean | undefined;
  PlanCardIDs?: string[] | undefined;
  Status?: string | undefined;
  ReviewID?: string | undefined;
  ReviewTurnID?: string | undefined;
  BoundTaskCardId?: string | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

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

export interface SetProtectedFilesReq {
  Files: string[];
  CallerAgentId?: string | undefined;
}

export interface SetProtectedFilesResp {
  Count: number;
}

export interface SetThinkingLevelReq {
  Unit: ModelUnit;
  Level: ThinkingLevel;
}

export interface ShellBashReq {
  Command: string;
  Args?: string[] | undefined;
  Dir?: string | undefined;
  Timeout?: number | undefined;
}

export interface ShellCandidate {
  Kind: string;
  Executable: string;
  ExtraBin?: string[] | undefined;
  Available: boolean;
}

export interface ShellChunk {
  Kind: string;
  Text?: string | undefined;
  ExitCode?: number | undefined;
  Stdout?: string | undefined;
  Stderr?: string | undefined;
  Truncated?: boolean | undefined;
  Interrupted?: boolean | undefined;
  ReturnCodeInterpretation?: string | undefined;
  DirWarning?: string | undefined;
}

export interface ShellEnvProbeResp {
  Current: ShellCandidate;
  Candidates: ShellCandidate[];
  Platform: string;
}

export interface ShellExecReq {
  Command: string;
  Args?: string[] | undefined;
  Dir?: string | undefined;
  Timeout?: number | undefined;
  Confirm?: boolean | undefined;
}

export interface ShellPrefSaveReq {
  RequestId: string;
  Kind: string;
}

export interface ShellPrefSaveResp {
  Kind: string;
  Current: ShellCandidate;
}

export interface ShellSessionCloseReq {
  SessionId: string;
}

export interface ShellSessionCloseResp {
}

export interface ShellSessionFetchReq {
  SessionId: string;
  FromIdx: number;
}

export interface ShellSessionFetchResp {
  Chunks: ShellSessionOutputEvent[];
  NextIdx: number;
  Truncated?: boolean | undefined;
}

export interface ShellSessionOpenReq {
  Cols?: number | undefined;
  Rows?: number | undefined;
  WorkingDirectory?: string | undefined;
}

export interface ShellSessionOpenResp {
  SessionId: string;
  Mode: string;
}

export interface ShellSessionOutputEvent {
  SessionId: string;
  Idx: number;
  Kind: string;
  Data: string;
  ExitCode?: number | undefined;
  Mode?: string | undefined;
}

export interface ShellSessionResizeReq {
  SessionId: string;
  Cols: number;
  Rows: number;
}

export interface ShellSessionResizeResp {
}

export interface ShellSessionWriteReq {
  SessionId: string;
  Data: string;
}

export interface ShellSessionWriteResp {
}

export interface SlashCommand {
  Name: string;
  ShortHelp: string;
}

export interface SshArchiveExportReq {
  SessionId: string;
  Path: string;
}

export interface SshArchiveImportReq {
  SessionId: string;
  Path: string;
  Content: string;
}

export interface SshCommandCreateReq {
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandCreateResp {
  Item: SshCommandSnippet;
}

export interface SshCommandListReq {
}

export interface SshCommandListResp {
  Items: SshCommandSnippet[];
}

export interface SshCommandRemoveReq {
  Id: string;
}

export interface SshCommandRemoveResp {
}

export interface SshCommandSnippet {
  Id: string;
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandUpdateReq {
  Id: string;
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandUpdateResp {
}

export interface SshDiskInfo {
  MountPoint: string;
  Available: number;
  Total: number;
}

export interface SshDownloadReq {
  SessionId: string;
  Path: string;
}

export interface SshDownloadResp {
  Name: string;
  Content: string;
  Size: number;
  IsDirectory: boolean;
  NumEntries: number;
}

export interface SshExecReq {
  HostId: string;
  Command: string;
  Timeout?: number | undefined;
}

export interface SshExecResp {
  Stdout: string;
  Stderr: string;
  ExitCode: number;
  Truncated: boolean;
  DurationMs: number;
}

export interface SshFileChmodReq {
  SessionId: string;
  Path: string;
  Mode: string;
}

export interface SshFileChmodResp {
}

export interface SshFileDeleteReq {
  SessionId: string;
  Path: string;
}

export interface SshFileDeleteResp {
}

export interface SshFileDownloadReq {
  SessionId: string;
  Path: string;
}

export interface SshFileDownloadResp {
  Name: string;
  Content: string;
  Size: number;
}

export interface SshFileEntry {
  Name: string;
  FullPath: string;
  IsDir: boolean;
  Size: number;
  Mode?: string | undefined;
  Modified?: string | undefined;
}

export interface SshFileListReq {
  SessionId: string;
  Path: string;
}

export interface SshFileListResp {
  Path: string;
  Entries: SshFileEntry[];
}

export interface SshFileMkdirReq {
  SessionId: string;
  Path: string;
}

export interface SshFileMkdirResp {
}

export interface SshFileReadReq {
  SessionId: string;
  Path: string;
}

export interface SshFileReadResp {
  Path: string;
  Content: string;
  IsBinary: boolean;
}

export interface SshFileRenameReq {
  SessionId: string;
  From: string;
  To: string;
}

export interface SshFileRenameResp {
}

export interface SshFileWriteBase64Req {
  SessionId: string;
  Path: string;
  Content: string;
}

export interface SshFileWriteBase64Resp {
}

export interface SshFileWriteReq {
  SessionId: string;
  Path: string;
  Content: string;
}

export interface SshFileWriteResp {
}

export interface SshFolderCreateReq {
  Name: string;
}

export interface SshFolderCreateResp {
}

export interface SshFolderRemoveReq {
  Name: string;
}

export interface SshFolderRemoveResp {
}

export interface SshFolderRenameReq {
  From: string;
  To: string;
}

export interface SshFolderRenameResp {
}

export interface SshFolderReorderReq {
  Names: string[];
}

export interface SshFolderReorderResp {
}

export interface SshHistoryListReq {
  Limit: number;
}

export interface SshHistoryListResp {
  Items: string[];
}

export interface SshHost {
  Id: string;
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostCreateReq {
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostCreateResp {
  Host: SshHostView;
}

export interface SshHostListReq {
}

export interface SshHostListResp {
  Items: SshHostView[];
  Groups: string[];
}

export interface SshHostRemoveReq {
  Id: string;
}

export interface SshHostRemoveResp {
}

export interface SshHostUpdateReq {
  Id: string;
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostUpdateResp {
}

export interface SshHostView {
  Id: string;
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  Group: string;
  HasCredential: boolean;
  AgentInvisible: boolean;
}

export interface SshManagerEvent {
  Kind: string;
  SessionId: string;
  HostId: string;
  HostName: string;
  Error?: string | undefined;
}

export interface SshNetInfo {
  Name: string;
  RxBps: number;
  TxBps: number;
}

export interface SshProcInfo {
  Pid: number;
  User: string;
  Cpu: number;
  Mem: number;
  Command: string;
}

export interface SshSessionInfo {
  SessionId: string;
  HostId: string;
  HostName: string;
  HostAddr: string;
  User: string;
  Connected: boolean;
  Cwd?: string | undefined;
  Error?: string | undefined;
}

export interface SshSessionListReq {
}

export interface SshSessionListResp {
  Items: SshSessionInfo[];
}

export interface SshShellCloseReq {
  SessionId: string;
}

export interface SshShellCloseResp {
}

export interface SshShellInputReq {
  SessionId: string;
  Data: string;
}

export interface SshShellInputResp {
}

export interface SshShellOpenReq {
  HostId: string;
  InitialCols?: number | undefined;
  InitialRows?: number | undefined;
}

export interface SshShellOpenResp {
  SessionId: string;
  Connected: boolean;
  Error?: string | undefined;
}

export interface SshShellResizeReq {
  SessionId: string;
  Cols: number;
  Rows: number;
}

export interface SshShellResizeResp {
}

export interface SshShellRunReq {
  SessionId: string;
  Command: string;
  TimeoutMs?: number | undefined;
}

export interface SshShellRunResp {
  Output: string;
  ExitCode: number;
  Truncated: boolean;
  TimedOut: boolean;
}

export interface SshShellStreamChunk {
  Data: Uint8Array;
  Exited?: boolean | undefined;
  Replay?: boolean | undefined;
}

export interface SshShellStreamReq {
  SessionId: string;
}

export interface SshStatus {
  HostId: string;
  HostName: string;
  Connected: boolean;
  CpuPercent: number;
  MemUsed: number;
  MemTotal: number;
  SwapUsed: number;
  SwapTotal: number;
  Disks: SshDiskInfo[];
  Net: SshNetInfo[];
  Procs: SshProcInfo[];
  LoadAvg: number[];
  Uptime: number;
  Timestamp: string;
}

export interface SshStatusListReq {
}

export interface SshStatusListResp {
  Items: SshStatus[];
}

export interface SshStatusReq {
  HostId: string;
}

export interface SshStatusResp {
  Status: SshStatus;
}

export interface SshTunnelCloseReq {
  HostId: string;
  TargetAddr: string;
}

export interface SshTunnelCloseResp {
  Closed: boolean;
  RefCount: number;
}

export interface SshTunnelInfo {
  HostId: string;
  TargetAddr: string;
  LocalAddr: string;
  RefCount: number;
  ActiveConns: number;
}

export interface SshTunnelListReq {
}

export interface SshTunnelListResp {
  Items: SshTunnelInfo[];
}

export interface SshTunnelOpenReq {
  HostId: string;
  TargetAddr: string;
}

export interface SshTunnelOpenResp {
  LocalAddr: string;
  RefCount: number;
}

export interface SshUploadReq {
  SessionId: string;
  Path: string;
  Content: string;
  IsArchive: boolean;
}

export interface SshUploadResp {
  NumEntries: number;
  BytesWritten: number;
}

export interface Step {
  Id: string;
  Role: string;
  Type: string;
  Content: ContentBlock[];
  Closed: boolean;
  Error?: string | undefined;
  Timestamp?: string | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  TurnId?: string | undefined;
  ReasoningContent?: string | undefined;
  Usage?: UsageData | undefined;
  ContentStatus?: string | undefined;
  ExecutionStatus?: string | undefined;
  InteractionStatus?: string | undefined;
  Progress?: string | undefined;
  Seq?: number | undefined;
  RequestId?: string | undefined;
  Meta?: string | undefined;
  Discarded?: boolean | undefined;
  Model?: string | undefined;
  Provider?: string | undefined;
}

export interface StepEvent {
  Kind: string;
  StepId: string;
  TurnId: string;
  StepType?: string | undefined;
  Role?: string | undefined;
  Time?: string | undefined;
  Block?: ContentBlock | undefined;
  BlockIndex?: number | undefined;
  Delta?: string | undefined;
  Error?: string | undefined;
  Usage?: UsageData | undefined;
  InteractionType?: string | undefined;
  TaskId?: string | undefined;
  Task?: Record<string, unknown> | undefined;
  FileChanges?: TurnFileChange[] | undefined;
  Progress?: string | undefined;
  RequestId?: string | undefined;
  EventSeq?: number | undefined;
  Seq?: number | undefined;
  OriginMessageId?: string | undefined;
  Meta?: string | undefined;
  Discarded?: boolean | undefined;
  Model?: string | undefined;
  Provider?: string | undefined;
}

export interface StoragePolicy {
  Enabled: boolean;
  MaxSessionChars?: number | undefined;
  DiscardBatchSize?: number | undefined;
}

export interface StoreClientConfigGetResp {
  Config: StoreClientConfigView;
}

export interface StoreClientConfigSetReq {
  BaseURL?: string | undefined;
  Channel?: string | undefined;
  ProductionPubKey?: string | undefined;
  AuthToken?: string | undefined;
}

export interface StoreClientConfigSetResp {
  Config: StoreClientConfigView;
}

export interface StoreClientConfigView {
  BaseURL: string;
  Channel: string;
  HasAuthToken: boolean;
  HasProductionPubKey: boolean;
}

export interface StoreCommunityView {
  Id: string;
  Slug: string;
  DisplayName: string;
  Description: string;
  RepoUrl: string;
  CommitSha: string;
  Version: string;
  SdkVersion: string;
  ProtocolVersion: number;
  TarballSha256: string;
  MirrorUrl: string;
  Compatible: boolean;
  IncompatibleReason?: string | undefined;
  ManifestId?: string | undefined;
  ManifestName?: string | undefined;
  Permissions?: string[] | undefined;
}

export interface StoreIndexResp {
  GeneratedAt: string;
  FetchedAt: string;
  FromCache: boolean;
  Store: StorePluginView[];
  Community: StoreCommunityView[];
}

export interface StoreInstallReq {
  Kind: string;
  Slug: string;
  Version?: string | undefined;
}

export interface StoreInstallResp {
  AppId: string;
  Version: string;
  State: string;
  Origin: string;
}

export interface StorePluginView {
  Id: string;
  Slug: string;
  DisplayName: string;
  Description: string;
  Publisher: string;
  Channel: string;
  PublisherPubkey: string;
  CurrentVersion: string;
  Versions: StoreVersionView[];
}

export interface StoreVersionView {
  Version: string;
  SdkVersion: string;
  ProtocolVersion: number;
  MinHostVersion: string;
  PayloadSha256: string;
  Size: number;
  Changelog: string;
  CreatedAt: string;
  Compatible: boolean;
  IncompatibleReason?: string | undefined;
}

export interface StructuredTraceEntry {
  ToolUseId: string;
  CallableId: string;
  Service: string;
  EffectKind: string;
  Input: string;
  Output: string;
  IsError: boolean;
}

export interface SubmitReq {
  config: Config;
}

export interface SubmitResp {
  task: Task;
}

export interface SummarizeResp {
  Text: string;
}

export interface SummarySegment {
  SourceStartIndex: number;
  SourceEndIndex: number;
  Level: number;
  Text: string;
  InputTokens: number;
  OutputTokens: number;
  CreatedAt: string;
}

export interface SystemContentBlock {
  Type: string;
  Text: string;
  CacheControl?: string | undefined;
}

export interface SystemTreeNode {
  Id: string;
  Name: string;
  Kind: string;
  Path?: string | undefined;
  Status?: string | undefined;
  Children?: SystemTreeNode[] | undefined;
  Extra?: Record<string, string> | undefined;
}

export interface SystemTreeResp {
  Nodes: SystemTreeNode[];
}

export interface Task {
  Id: string;
  Subject: string;
  Status: string;
  ActiveForm?: string | undefined;
}

export interface TaskDataBinding {
  Input: string;
  FromNode: string;
  FromOutput: string;
}

export interface TaskDepEdge {
  MapId: string;
  From: string;
  To: string;
}

export interface TemplateNodeMapping {
  From: string;
  To: string;
}

export interface TemplateRunRecord {
  InstanceMapId: string;
  StartedAt: string;
  SchedulerCardId?: string | undefined;
  Source?: string | undefined;
}

export interface ThinkingLevel {
  Mode: string;
  Effort?: string | undefined;
  Budget?: number | undefined;
}

export interface ThinkingRegistryEntry {
  Unit: ModelUnit;
  Level: ThinkingLevel;
}

export interface ToastActionReq {
  Id: string;
}

export interface ToastActionResp {
}

export interface ToastActionTriggeredEvent {
  Id: string;
  ActionCallable: string;
  ActionLabel: string;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
}

export interface ToastCard {
  Id: string;
  Title: string;
  Body?: string | undefined;
  Kind: string;
  Source?: string | undefined;
  ActionLabel?: string | undefined;
  ActionCallable?: string | undefined;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
  DurationMs?: number | undefined;
  CreatedAt: string;
}

export interface ToastCardRemovedEvent {
  Id: string;
}

export interface ToastDismissReq {
  Id: string;
}

export interface ToastDismissResp {
}

export interface ToastShowReq {
  Title: string;
  Body?: string | undefined;
  Kind?: string | undefined;
  Source?: string | undefined;
  ActionLabel?: string | undefined;
  ActionCallable?: string | undefined;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
  DurationMs?: number | undefined;
}

export interface ToastShowResp {
  Id: string;
}

export interface ToastStateReq {
}

export interface ToastStateResp {
  Cards: ToastCard[];
}

export interface ToolSpec {
  Name: string;
  Description?: string | undefined;
  InputSchema: string;
  Type?: string | undefined;
  EffectKind?: string | undefined;
  CallableId?: string | undefined;
  ServiceName?: string | undefined;
  Stream?: boolean | undefined;
  NativeConfig?: Record<string, unknown> | undefined;
}

export interface TopologyCard {
  Label?: string | undefined;
  Badge?: string | undefined;
  Sublabel?: string | undefined;
  DetailSections?: TopologyCardSection[] | undefined;
}

export interface TopologyCardRow {
  Label: string;
  Value: string;
  Mono?: boolean | undefined;
  Truncate?: boolean | undefined;
  Badge?: string | undefined;
  ExpandedRows?: TopologyCardRow[] | undefined;
  Tooltip?: string | undefined;
}

export interface TopologyCardSection {
  Key: string;
  Title: string;
  Rows: TopologyCardRow[];
}

export interface TopologyEpochEvent {
  Epoch: number;
  Patch?: GraphPatch | undefined;
}

export interface TopologyHistoryEntry {
  Epoch: number;
  Timestamp: string;
  ChangeCount: number;
}

export interface TopologyHistoryResp {
  Entries: TopologyHistoryEntry[];
}

export interface TopologyNodeDescriptor {
  CapabilityId?: string | undefined;
  CapabilityName?: string | undefined;
  CapabilityKind?: string | undefined;
  DisplayName?: string | undefined;
  Badge?: string | undefined;
  Sublabel?: string | undefined;
}

export interface TopologySyncReq {
  ClientEpoch: number;
}

export interface TopologySyncResp {
  CurrentEpoch: number;
  Snapshot?: UnifiedGraph | undefined;
  Patches: GraphPatch[];
}

export interface Turn {
  Id: string;
  Role: string;
  UserInput?: string | undefined;
  State?: string | undefined;
  Assessment?: TurnAssessment | undefined;
  Usage?: UsageData | undefined;
  ContextBudget?: TurnContextBudgetPayload | undefined;
  Tasks?: TurnTask[] | undefined;
  Error?: string | undefined;
  Cancelled?: boolean | undefined;
  Timestamp?: string | undefined;
  FileChanges?: TurnFileChange[] | undefined;
  ExploreResult?: ExploreResult | undefined;
  Unit?: ModelUnit | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  Seq?: number | undefined;
  TurnOrder?: number | undefined;
  Revision?: number | undefined;
  PauseReason?: string | undefined;
  ResumeDescriptor?: string | undefined;
  UserAttachments?: AttachmentEntry[] | undefined;
  UserImages?: ImageEntry[] | undefined;
  RequestStats?: TurnRequestStat[] | undefined;
}

export interface TurnAction {
  Id: string;
  Kind: string;
  Title?: string | undefined;
  Text?: string | undefined;
  State?: string | undefined;
  Error?: string | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  ParentStepId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Usage?: UsageData | undefined;
  Meta?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Seq?: number | undefined;
}

export interface TurnAnswerReq {
  AnswersJson: string;
  RequestId?: string | undefined;
}

export interface TurnAssessment {
  Decision: string;
  Reason?: string | undefined;
  Evidence?: string[] | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

export interface TurnContextBudgetPayload {
  EstimatedTokens: number;
  ContextWindowSize: number;
  TokenBudget: number;
}

export interface TurnEvent {
  Kind: string;
  TurnId: string;
  Usage?: UsageData | undefined;
  ContextBudget?: TurnContextBudgetPayload | undefined;
  Payload?: Record<string, unknown> | undefined;
}

export interface TurnFileChange {
  Path: string;
  Additions: number;
  Deletions: number;
  DiffContent: string;
}

export interface TurnHistoryResp {
  Steps: TurnAction[];
  TraceEntries: StructuredTraceEntry[];
  Usage?: UsageData | undefined;
}

export interface TurnMiddlewareResp {
  Entries: MiddlewareEntry[];
}

export interface TurnPlanApprovalRequestedPayload {
  TurnId: string;
  RequestId: string;
  Plan: string;
  Tasks?: TurnTask[] | undefined;
  Policy?: TurnPlanPolicy | undefined;
  Editable?: boolean | undefined;
}

export interface TurnPlanPolicy {
  Mode: string;
  AllowedPrompts?: PlanAllowedPrompt[] | undefined;
}

export interface TurnRequestStat {
  Id: string;
  Provider: string;
  Model: string;
  ResponseModel?: string | undefined;
  ResponseId?: string | undefined;
  RequestId?: string | undefined;
  Usage?: UsageData | undefined;
  StopReason?: string | undefined;
  ErrorCode?: string | undefined;
  ErrorMessage?: string | undefined;
  LatencyMs?: number | undefined;
  FirstTokenMs?: number | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
}

export interface TurnStatus {
  Turn: Turn;
  Unit?: ModelUnit | undefined;
  StartedAt?: string | undefined;
  UpdatedAt?: string | undefined;
  EventSeq?: number | undefined;
  PlanMode?: boolean | undefined;
  PlanTask?: string | undefined;
  PlanApproval?: TurnPlanApprovalRequestedPayload | undefined;
}

export interface TurnTask {
  Id: string;
  Subject: string;
  Status: string;
  ActiveForm?: string | undefined;
}

export interface TutorialSpec {
  TutorialId: string;
  Title: string;
  Description?: string | undefined;
  Steps: GuideStep[];
  CreatedAt: string;
}

export interface UiInteractionRecord {
  Id: string;
  Ts: string;
  Kind: string;
  GuideId: string;
  Label?: string | undefined;
  View?: string | undefined;
  Detail?: string | undefined;
}

export interface UnifiedGraph {
  Version: string;
  Nodes: UnifiedGraphNode[];
  Edges: UnifiedGraphEdge[];
}

export interface UnifiedGraphEdge {
  Id: string;
  From: string;
  To: string;
  Kind: string;
  Label?: string | undefined;
  LifecycleScope?: string | undefined;
  Status?: string | undefined;
}

export interface UnifiedGraphNode {
  Id: string;
  Kind: string;
  Label: string;
  ActorType?: string | undefined;
  ActorId?: string | undefined;
  StepKind?: string | undefined;
  Strategy?: string | undefined;
  Status: string;
  ParentId?: string | undefined;
  ContainerId?: string | undefined;
  Collapsible?: boolean | undefined;
  Expanded?: boolean | undefined;
  Detail?: Record<string, unknown> | undefined;
  Interfaces?: CallableInterface[] | undefined;
  Descriptor?: TopologyNodeDescriptor | undefined;
  Card?: TopologyCard | undefined;
}

export interface UsageData {
  InputTokens?: number | undefined;
  OutputTokens?: number | undefined;
  TotalTokens?: number | undefined;
  CacheCreationInputTokens?: number | undefined;
  CacheReadInputTokens?: number | undefined;
  EstimatedPromptTokens?: number | undefined;
  MaxContextLength?: number | undefined;
  ReasoningTokens?: number | undefined;
  CostInput?: number | undefined;
  CostOutput?: number | undefined;
  CostCacheRead?: number | undefined;
  CostCacheWrite?: number | undefined;
  CostTotal?: number | undefined;
}

export interface VoiceAccountActivateReq {
  Kind: string;
  Id: string;
}

export interface VoiceAccountActivateResp {
  ActiveId: string;
}

export interface VoiceAccountCreateReq {
  Kind: string;
  Name: string;
  Provider: string;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  Voice?: string | undefined;
  Language?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface VoiceAccountCreateResp {
  Account: VoiceAccountView;
}

export interface VoiceAccountDeleteReq {
  Id: string;
}

export interface VoiceAccountDeleteResp {
}

export interface VoiceAccountListReq {
  Kind?: string | undefined;
}

export interface VoiceAccountListResp {
  Items: VoiceAccountView[];
  ActiveId?: string | undefined;
}

export interface VoiceAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Provider?: string | undefined;
  ApiKey?: string | undefined;
  Model?: string | undefined;
  Voice?: string | undefined;
  Language?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface VoiceAccountUpdateResp {
  Account: VoiceAccountView;
}

export interface VoiceAccountView {
  Id: string;
  Kind: string;
  Name: string;
  Provider: string;
  HasApiKey: boolean;
  Model?: string | undefined;
  Voice?: string | undefined;
  Language?: string | undefined;
  BaseUrl?: string | undefined;
  Proxy?: string | undefined;
}

export interface VoiceAudio {
  AudioType: number;
  Data: Uint8Array;
}

export interface VoiceCloneReq {
  AccountId: string;
  AudioData: Uint8Array;
  AudioName: string;
  VoiceID?: string | undefined;
  PromptAudioData?: Uint8Array | undefined;
  PromptAudioName?: string | undefined;
  PromptText?: string | undefined;
  PreviewText?: string | undefined;
  NeedNoiseReduction?: boolean | undefined;
  NeedVolumeNormalization?: boolean | undefined;
}

export interface VoiceCloneResp {
  VoiceId: string;
  DemoAudioUrl?: string | undefined;
}

export interface VoiceConfigExportResp {
  Data: string;
}

export interface VoiceConfigImportReq {
  Data: string;
}

export interface VoiceConfigImportResp {
}

export interface VoiceDesignReq {
  AccountId: string;
  Prompt: string;
  PreviewText: string;
  VoiceID?: string | undefined;
}

export interface VoiceDesignResp {
  VoiceId: string;
  TrialAudio: Uint8Array;
}

export interface VoiceHotwordsBinding {
  ProjectId: string;
  AgentId: string;
  Hotwords: string[];
}

export interface VoiceHotwordsDeleteReq {
  ProjectId: string;
  AgentId: string;
}

export interface VoiceHotwordsDeleteResp {
}

export interface VoiceHotwordsGetReq {
  ProjectId: string;
  AgentId: string;
}

export interface VoiceHotwordsGetResp {
  Binding: VoiceHotwordsBinding;
}

export interface VoiceHotwordsSetReq {
  ProjectId: string;
  AgentId: string;
  Hotwords: string[];
}

export interface VoiceHotwordsSetResp {
  Binding: VoiceHotwordsBinding;
}

export interface VoiceNotifyConfig {
  Enabled: boolean;
  Volume: number;
  Sound: string;
  OnComplete: boolean;
  OnError: boolean;
  OnInteraction: boolean;
  OnAllComplete: boolean;
  SoundError?: string | undefined;
  SoundInteraction?: string | undefined;
  SoundAllComplete?: string | undefined;
  VolumeComplete?: number | undefined;
  VolumeError?: number | undefined;
  VolumeInteraction?: number | undefined;
  VolumeAllComplete?: number | undefined;
  CustomSoundComplete?: string | undefined;
  CustomSoundError?: string | undefined;
  CustomSoundInteraction?: string | undefined;
  CustomSoundAllComplete?: string | undefined;
}

export interface VoiceNotifyConfigResp {
  Config: VoiceNotifyConfig;
}

export interface VoiceRecognizeReq {
  Audio: VoiceAudio;
  Hotwords?: string[] | undefined;
  AccountId?: string | undefined;
}

export interface VoiceRecognizeResp {
  Text: string;
}

export interface VoiceSynthesizeReq {
  Input: string;
  Model?: string | undefined;
  Voice?: string | undefined;
  Format?: string | undefined;
  Instruction?: string | undefined;
  AccountId?: string | undefined;
}

export interface VoiceSynthesizeResp {
  Audio: VoiceAudio;
}

export interface WH {
  W: number;
  H: number;
}

export interface WebDownloadReq {
  Url: string;
  SavePath: string;
  MaxBytes?: number | undefined;
}

export interface WebDownloadResp {
  SavedPath: string;
  BytesDownloaded: number;
  ContentType: string;
  Truncated: boolean;
}

export interface WebFetchMeta {
  Description: string;
  SiteName: string;
  Lang: string;
}

export interface WebFetchReq {
  Url: string;
  MaxChars?: number | undefined;
}

export interface WebFetchResp {
  Url: string;
  Title: string;
  Text: string;
  Meta: WebFetchMeta;
  Truncated: boolean;
}

export interface WebSearchAccountActivateReq {
  Id: string;
}

export interface WebSearchAccountActivateResp {
  ActiveId: string;
}

export interface WebSearchAccountCreateReq {
  Name: string;
  Provider: string;
  ApiKey?: string | undefined;
  Endpoint?: string | undefined;
}

export interface WebSearchAccountCreateResp {
  Account: WebSearchAccountView;
}

export interface WebSearchAccountDeleteReq {
  Id: string;
}

export interface WebSearchAccountDeleteResp {
}

export interface WebSearchAccountListReq {
}

export interface WebSearchAccountListResp {
  Items: WebSearchAccountView[];
  ActiveId?: string | undefined;
}

export interface WebSearchAccountUpdateReq {
  Id: string;
  Name?: string | undefined;
  Provider?: string | undefined;
  ApiKey?: string | undefined;
  Endpoint?: string | undefined;
}

export interface WebSearchAccountUpdateResp {
  Account: WebSearchAccountView;
}

export interface WebSearchAccountView {
  Id: string;
  Name: string;
  Provider: string;
  HasApiKey: boolean;
  Endpoint?: string | undefined;
}

export interface WebSearchProviderInfo {
  Id: string;
  Name: string;
  RequiresKey: boolean;
}

export interface WebSearchProviderListResp {
  Providers: WebSearchProviderInfo[];
}

export interface WebSearchReq {
  Query: string;
  MaxResults?: number | undefined;
  TimeRange?: string | undefined;
  Engine?: string | undefined;
}

export interface WebSearchResp {
  Results: WebSearchResult[];
  Provider: string;
}

export interface WebSearchResult {
  Title: string;
  Url: string;
  Snippet: string;
  Source?: string | undefined;
  Icon?: string | undefined;
  PublishedDate?: string | undefined;
}

export interface WikiAutomationBindReq {
  MapId: string;
  SchedulerCardId: string;
}

export interface WikiAutomationBindResp {
  TemplateId: string;
  TemplateMap: MonoCardListItem;
  SchedulerCard: MonoCardListItem;
}

export interface WikiCardChangedEvent {
  Id: string;
  Modified: string;
}

export interface WikiCardContentMatch {
  Card: MonoCardListItem;
  Line?: number | undefined;
  Snippet?: string | undefined;
}

export interface WikiCardRaw {
  Id: string;
  Raw: string;
  Found: boolean;
}

export interface WikiCardTreeNode {
  Id: string;
  Title: string;
  Type: string;
  Status: string;
  Modified: string;
  Children: WikiCardTreeNode[];
}

export interface WikiClaimTaskCardReq {
  Id: string;
  Status: string;
  ExpectedStatuses: string[];
}

export interface WikiClaimTaskCardResp {
  Card: MonoCardListItem;
  PreviousStatus: string;
  Raw: string;
  Inputs?: Record<string, unknown> | undefined;
}

export interface WikiCloseCardReq {
  Id: string;
}

export interface WikiCreateCardReq {
  Id: string;
  Raw: string;
}

export interface WikiCreateCardResp {
  Card: MonoCardListItem;
}

export interface WikiCreateMapReq {
  Id: string;
  Destination?: string | undefined;
  Notes?: string | undefined;
}

export interface WikiCreateMapResp {
  Card: MonoCardListItem;
}

export interface WikiCreateTaskCardReq {
  MapId: string;
  Title: string;
  Question: string;
  DependsOn?: string[] | undefined;
  Status?: string | undefined;
  Category?: string | undefined;
  Bindings?: TaskDataBinding[] | undefined;
  Exec?: Record<string, unknown> | undefined;
}

export interface WikiCreateTaskCardResp {
  Card: MonoCardListItem;
}

export interface WikiDeleteCardReq {
  Id: string;
}

export interface WikiDeleteCardResp {
  Id: string;
}

export interface WikiDispatchPlanReq {
  Id: string;
  AgentId: string;
  Feedback?: string | undefined;
}

export interface WikiDispatchPlanResp {
  Id: string;
  AgentId: string;
}

export interface WikiEditCardReq {
  Id: string;
  Raw?: string | undefined;
  OldString?: string | undefined;
  NewString?: string | undefined;
  ReplaceAll?: boolean | undefined;
}

export interface WikiEditCardResp {
  Card: MonoCardListItem;
}

export interface WikiFrontierReq {
  MapId: string;
}

export interface WikiFrontierResp {
  TaskCards: FrontierTaskCard[];
}

export interface WikiGetCardHierarchyReq {
  Id: string;
  Level?: number | undefined;
}

export interface WikiGetCardHierarchyResp {
  Tree: string;
}

export interface WikiGetCardReq {
  Id: string;
}

export interface WikiGetCardResp {
  Id: string;
  Raw: string;
}

export interface WikiGetCardsBatchReq {
  Ids: string[];
}

export interface WikiGetCardsBatchResp {
  Cards: WikiCardRaw[];
}

export interface WikiGetStarredReq {
}

export interface WikiListCardsReq {
  RootId?: string | undefined;
  Limit?: number | undefined;
  Depth?: number | undefined;
  OrderBy?: string | undefined;
  Flat?: boolean | undefined;
  IncludeRaw?: boolean | undefined;
  IncludeBuiltin?: boolean | undefined;
  WorkflowFilter?: WikiWorkflowFilter | undefined;
  Total?: boolean | undefined;
  Query?: string | undefined;
  Tags?: string[] | undefined;
  Status?: string | undefined;
  Type?: string | undefined;
  Source?: string | undefined;
  List?: string | undefined;
  Parent?: string | undefined;
  Priority?: string | undefined;
  Due?: string | undefined;
  ModifiedAfter?: string | undefined;
  ModifiedBefore?: string | undefined;
  CreatedAfter?: string | undefined;
  CreatedBefore?: string | undefined;
  DueAfter?: string | undefined;
  DueBefore?: string | undefined;
}

export interface WikiListCardsResp {
  Tree: string;
  Nodes: WikiCardTreeNode[];
  Cards: MonoCardListItem[];
  Total: number;
}

export interface WikiListDependenciesReq {
  MapId?: string | undefined;
}

export interface WikiListDependenciesResp {
  Edges: TaskDepEdge[];
}

export interface WikiListStarredReq {
}

export interface WikiListStarredResp {
  Items: WikiStarredEntry[];
}

export interface WikiListTemplateRunsReq {
  TemplateMapId: string;
  Limit?: number | undefined;
}

export interface WikiListTemplateRunsResp {
  Runs: TemplateRunRecord[];
}

export interface WikiListTemplatesReq {
}

export interface WikiListTemplatesResp {
  Templates: MonoCardListItem[];
}

export interface WikiListTimersResp {
  Timers: WikiTimerListItem[];
}

export interface WikiOpenCardReq {
  Id: string;
}

export interface WikiOpenCardsReq {
}

export interface WikiOpenCardsResp {
  OpenCards: string[];
}

export interface WikiPromoteNodeOutputsReq {
  MapId: string;
  NodeId: string;
}

export interface WikiPromoteNodeOutputsResp {
  Revision: string;
  Inputs: Record<string, unknown>;
}

export interface WikiSaveOpenCardsReq {
  OpenCards: string[];
}

export interface WikiSearchCardContentReq {
  Query: string;
  Tags?: string[] | undefined;
  Status?: string | undefined;
  Type?: string | undefined;
  Source?: string | undefined;
  List?: string | undefined;
  Parent?: string | undefined;
  Priority?: string | undefined;
  Due?: string | undefined;
  ModifiedAfter?: string | undefined;
  ModifiedBefore?: string | undefined;
  CreatedAfter?: string | undefined;
  CreatedBefore?: string | undefined;
  DueAfter?: string | undefined;
  DueBefore?: string | undefined;
  OrderBy?: string | undefined;
  Limit?: number | undefined;
}

export interface WikiSearchCardContentResp {
  Matches: WikiCardContentMatch[];
  Total: number;
}

export interface WikiSetMapInputsReq {
  MapId: string;
  Inputs: Record<string, unknown>;
}

export interface WikiSetMapInputsResp {
  Revision: string;
}

export interface WikiSetMapOwnerReq {
  MapId: string;
  OwnerActorId: string;
}

export interface WikiSetMapOwnerResp {
  Card: MonoCardListItem;
  PreviousOwnerActorId: string;
}

export interface WikiSetStarredReq {
  Id: string;
  Starred: boolean;
}

export interface WikiSetStatusReq {
  Id: string;
  Status: string;
  ExpectedStatus?: string | undefined;
  Evidence?: string[] | undefined;
}

export interface WikiSetStatusResp {
  Card: MonoCardListItem;
  PreviousStatus: string;
}

export interface WikiSetTaskDependenciesReq {
  MapId: string;
  TaskId: string;
  DependsOn: string[];
  Bindings?: TaskDataBinding[] | undefined;
}

export interface WikiSetTaskDependenciesResp {
  Revision: string;
}

export interface WikiSetTaskOutputsReq {
  CardId: string;
  Outputs: Record<string, unknown>;
}

export interface WikiSetTaskOutputsResp {
  Card: MonoCardListItem;
}

export interface WikiStarredEntry {
  ProjectID: string;
  ProjectName: string;
  CardID: string;
}

export interface WikiStarredResp {
  Starred: string[];
}

export interface WikiTemplateInstantiateReq {
  TemplateMapId: string;
  InstanceMapId: string;
  Inputs?: Record<string, unknown> | undefined;
  Source?: string | undefined;
  SchedulerCardId?: string | undefined;
}

export interface WikiTemplateInstantiateResp {
  InstanceMap: MonoCardListItem;
  NodeMap: TemplateNodeMapping[];
}

export interface WikiTemplateSaveReq {
  MapId: string;
  TemplateId: string;
}

export interface WikiTemplateSaveResp {
  TemplateMap: MonoCardListItem;
  NodeMap: TemplateNodeMapping[];
}

export interface WikiTimerListItem {
  Id: string;
  NextFireAt: string;
  LastRunAt: string;
  LastStatus: string;
  Enabled: boolean;
  CurrentInstance: string;
  ScheduleType?: string | undefined;
  TaskMode?: string | undefined;
  AgentKind?: string | undefined;
  BoundTemplate?: string | undefined;
}

export interface WikiToggleTimerReq {
  Id: string;
  Enabled: boolean;
}

export interface WikiToggleTimerResp {
  Id: string;
  Enabled: boolean;
}

export interface WikiTriggerTimerCardReq {
  Id: string;
}

export interface WikiTriggerTimerCardResp {
  Id: string;
}

export interface WikiValidateCardReq {
  Id: string;
  Raw: string;
}

export interface WikiValidateCardResp {
  Valid: boolean;
  Errors: CardValidationError[];
}

export interface WikiWorkflowFilter {
  FoldedWorkflows: string[];
  FoldedCategories: string[];
  ExpandedBuckets: string[];
  AgentIds: string[];
}

export interface WorkbenchAttentionEvent {
  ActorId: string;
  Kind: string;
  Summary: string;
  Time: number;
}

export interface WorkbenchAttentionReportReq {
  LimitEvents?: number | undefined;
}

export interface WorkbenchAttentionReportResp {
  Cards: WorkbenchCardState[];
  Recent: WorkbenchAttentionEvent[];
  Frozen?: boolean | undefined;
  Maximized?: string | undefined;
}

export interface WorkbenchCardRefReq {
  Id: string;
}

export interface WorkbenchCardState {
  Id: string;
  Kind: string;
  Title: string;
  Icon: string;
  Color?: string | undefined;
  Score: number;
  Slot: string;
  Pinned: boolean;
  Hidden?: boolean | undefined;
  StatusText?: string | undefined;
  Why?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Mode?: string | undefined;
}

export interface WorkbenchSetFrozenReq {
  Frozen: boolean;
}

export interface WorkbenchSetHiddenReq {
  Id: string;
  Hidden: boolean;
}

export interface WorkbenchSetMaximizedReq {
  Id: string;
  Reason?: string | undefined;
}

export interface WorkbenchSetPinnedReq {
  Id: string;
  Pinned: boolean;
}

export interface WorkbenchSnapshot {
  Cards: WorkbenchCardState[];
  Generation: number;
  Frozen?: boolean | undefined;
  Maximized?: string | undefined;
}

export interface WorkbenchSnapshotReq {
  IncludeHidden?: boolean | undefined;
}

export interface WorkbenchUpsertCardReq {
  Id: string;
  Kind: string;
  Title?: string | undefined;
  Icon?: string | undefined;
  Color?: string | undefined;
  StatusText?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Hidden?: boolean | undefined;
  Score?: number | undefined;
  Mode?: string | undefined;
}

export interface WorkspaceAIShellState {
  SidebarOrder?: string[] | undefined;
  SidebarPinned?: string[] | undefined;
  AgentOrder?: string[] | undefined;
  LayoutJson?: string | undefined;
  SelectedAgentId?: string | undefined;
  PreviousAgentId?: string | undefined;
}

export interface WorkspaceAddMountReq {
  ProjectId: string;
  MountName: string;
  MountPath: string;
}

export interface WorkspaceAgentAccessReq {
  AgentActorId: string;
}

export interface WorkspaceAgentAssignReq {
  AgentActorId: string;
  InterpretedGoal?: string | undefined;
  BoundTaskCardId: string;
  MaxTurns?: number | undefined;
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentAssignResp {
  Goal: GoalSummary;
}

export interface WorkspaceAgentListState {
  Version: number;
  Full: boolean;
  Items: AgentListItem[];
}

export interface WorkspaceAgentListStateEvent {
  State: WorkspaceAgentListState;
}

export interface WorkspaceAgentLoadedReq {
  AgentId: string;
  ActorId: string;
  ProjectId: string;
}

export interface WorkspaceAgentLoadedResp {
}

export interface WorkspaceAgentReviewReq {
  AgentActorId: string;
  Decision: string;
  Feedback?: string | undefined;
  TaskCardId?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentReviewResp {
  Approved: boolean;
  CardStatus: string;
  ReviewNote?: string | undefined;
}

export interface WorkspaceAgentSpawnAssignReq {
  To?: string | undefined;
  AgentKind: string;
  InterpretedGoal?: string | undefined;
  BoundTaskCardId?: string | undefined;
  MaxTurns?: number | undefined;
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  PermissionMode?: string | undefined;
  Unit?: ModelUnit | undefined;
  WorktreeBranch?: string | undefined;
}

export interface WorkspaceAgentSpawnAssignResp {
  AgentActorId: string;
  DisplayName: string;
  Goal: GoalSummary;
}

export interface WorkspaceAgentSpawnByTypeReq {
  AgentKind: string;
  Description: string;
  Prompt: string;
  Unit?: ModelUnit | undefined;
  MaxIterations?: number | undefined;
  ParentTurnId?: string | undefined;
  ParentStepId?: string | undefined;
  ToolUseId?: string | undefined;
  LifecycleScope?: string | undefined;
  CallerAgentId?: string | undefined;
  PermissionMode?: string | undefined;
  PlanEvidence?: string[] | undefined;
}

export interface WorkspaceAgentSpawnByTypeResp {
  ChildActorId: string;
  DisplayName: string;
}

export interface WorkspaceAgentSpawnSchedulerReq {
  ProjectID: string;
  AgentKind: string;
  DisplayName: string;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
}

export interface WorkspaceAgentSpawnSchedulerResp {
  AgentID: string;
  ActorID: string;
  DisplayName: string;
  AgentKind: string;
}

export interface WorkspaceAgentSpawnSwarmReq {
  Description: string;
  Prompt?: string | undefined;
  AgentKind?: string | undefined;
  MaxTurns?: number | undefined;
  Unit?: ModelUnit | undefined;
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentSpawnSwarmResp {
  ChildActorId: string;
  DisplayName: string;
  Depth: number;
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

export interface WorkspaceAgentTerminateReq {
  AgentActorId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentTerminateResp {
  Deleted: boolean;
}

export interface WorkspaceAgentsChangedEvent {
}

export interface WorkspaceAistatsActorIdResp {
  ActorId: string;
  Error?: string | undefined;
}

export interface WorkspaceBuiltinModesListResp {
  Modes: BuiltinMode[];
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

export interface WorkspaceCreateAgentKindReq {
  Kind: string;
  DisplayName: string;
  BaseKind?: string | undefined;
}

export interface WorkspaceCreateAgentKindResp {
  Kind: string;
  DisplayName: string;
  UserCreatable: boolean;
  SystemManaged: boolean;
  Builtin: boolean;
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

export interface WorkspaceCreateReq {
  Path: string;
  Name: string;
  InitGit: boolean;
  AppKind?: string | undefined;
}

export interface WorkspaceDeleteAgentKindReq {
  Kind: string;
}

export interface WorkspaceDeleteAgentKindResp {
  Kind: string;
  Removed: boolean;
}

export interface WorkspaceDeleteAgentReq {
  AgentId: string;
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

export interface WorkspaceExplorerState {
  ActiveProjectId?: string | undefined;
  SelectedPath?: string | undefined;
  ExpandedPaths?: string[] | undefined;
}

export interface WorkspaceGateApproveReq {
  TaskCardId: string;
  Approver: string;
  CallerAgentId: string;
  FormValues?: Record<string, unknown> | undefined;
  CallableId?: string | undefined;
  Note?: string | undefined;
}

export interface WorkspaceGateApproveResp {
  CardStatus: string;
  ApprovedAt: string;
}

export interface WorkspaceGateRejectReq {
  TaskCardId: string;
  Approver: string;
  CallerAgentId: string;
  Reason?: string | undefined;
  CallableId?: string | undefined;
}

export interface WorkspaceGateRejectResp {
  CardStatus: string;
  RejectedAt: string;
}

export interface WorkspaceGetAgentKindConfigReq {
  Kind: string;
}

export interface WorkspaceGitAddReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Paths: string[];
}

export interface WorkspaceGitAmendReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Message?: string | undefined;
  NoEdit?: boolean | undefined;
}

export interface WorkspaceGitBlameReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  FilePath: string;
}

export interface WorkspaceGitBlameResp {
  Lines: GitBlameLine[];
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

export interface WorkspaceGitCommitReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Message: string;
}

export interface WorkspaceGitCommitResp {
  Hash: string;
  Short: string;
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

export interface WorkspaceGitDiscardReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Paths?: string[] | undefined;
}

export interface WorkspaceGitFetchReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Remote?: string | undefined;
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

export interface WorkspaceGitMergeReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Branch: string;
  NoFastForward?: boolean | undefined;
  Squash?: boolean | undefined;
}

export interface WorkspaceGitMergeResp {
  Status: string;
  ConflictFiles: string[];
}

export interface WorkspaceGitPullReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Remote: string;
}

export interface WorkspaceGitPushReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Remote: string;
}

export interface WorkspaceGitRemoteAddReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
  Url: string;
}

export interface WorkspaceGitRemoteListReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitRemoteListResp {
  Remotes: GitRemoteInfo[];
}

export interface WorkspaceGitRemoteRemoveReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
}

export interface WorkspaceGitResetReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Paths?: string[] | undefined;
}

export interface WorkspaceGitShowReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Ref: string;
}

export interface WorkspaceGitShowResp {
  Commit: GitCommitInfo;
  Files: GitShowFile[];
  Output?: string | undefined;
}

export interface WorkspaceGitStashDropReq {
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

export interface WorkspaceGitStashPopReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Index?: number | undefined;
}

export interface WorkspaceGitStashSaveReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Message?: string | undefined;
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

export interface WorkspaceGitTagCreateReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
  CommitHash?: string | undefined;
  Message?: string | undefined;
  Force?: boolean | undefined;
}

export interface WorkspaceGitTagDeleteReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
  Name: string;
}

export interface WorkspaceGitTagListReq {
  ProjectId: string;
  WorktreeID?: string | undefined;
}

export interface WorkspaceGitTagListResp {
  Tags: GitTagInfo[];
}

export interface WorkspaceHostCallReq {
  CallId: string;
  Payload?: Record<string, unknown> | undefined;
}

export interface WorkspaceHostCallResp {
  Result?: Record<string, unknown> | undefined;
}

export interface WorkspaceListAgentKindConfigsResp {
  Items: AgentKindConfig[];
}

export interface WorkspaceListAgentKindsResp {
  Items: AgentKindInfo[];
}

export interface WorkspaceListAgentsReq {
  ProjectId?: string | undefined;
  Query?: string | undefined;
  CallerAgentId?: string | undefined;
  ParentAgentId?: string | undefined;
  ChildrenOnly?: boolean | undefined;
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

export interface WorkspaceLogStreamEntry {
  Timestamp: string;
  Level: string;
  CallerFile: string;
  CallerLine: number;
  Message: string;
  Fields?: Record<string, unknown> | undefined;
  Source: string;
  Seq: number;
}

export interface WorkspaceLogStreamEvent {
  Entries: WorkspaceLogStreamEntry[];
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

export interface WorkspaceMountReq {
  Path: string;
  Name: string;
  AppKind?: string | undefined;
}

export interface WorkspaceMountsEvent {
  Mounts: ProjectRef[];
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

export interface WorkspaceProjectBrowserState {
  SortMode?: string | undefined;
}

export interface WorkspaceRemoveMountReq {
  ProjectId: string;
  MountName: string;
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

export interface WorkspaceShellLayout {
  Version: number;
  LayoutJson: string;
  ActiveViewId?: string | undefined;
}

export interface WorkspaceSlashCommandsListResp {
  Commands: SlashCommand[];
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

export interface WorkspaceUnmountReq {
  ProjectId: string;
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

export interface WorkspaceWorkflowStartReq {
  AgentActorId: string;
  MapCardId: string;
  ProjectId?: string | undefined;
  TemplateMapId?: string | undefined;
}

export interface WorkspaceWorkflowStartResp {
  MapCardId: string;
}

export interface XY {
  X: number;
  Y: number;
}

export interface cellStatsReq {
  actorPath: string;
}

export interface eventStatsReq {
}

export interface eventStatsResp {
  subscriptions: eventStatsSub[];
  rings: eventStatsRing[];
  storeSubs: eventStatsStoreSub[];
}

export interface eventStatsRing {
  actorId: string;
  len: number;
  capacity: number;
  evicted: number;
}

export interface eventStatsStoreSub {
  actorId: string;
  kinds?: string[] | undefined;
  dropped: number;
  bufferCap: number;
}

export interface eventStatsSub {
  subId: number;
  flavor: string;
  match: string;
  kind: string;
  subject: string;
  role: string;
  backlog: number;
  drops: number;
  created: string;
}

export interface eventSubscribeInstanceReq {
  actorId: string;
  kind: string;
}

export interface eventSubscribeServiceReq {
  serviceName: string;
  kind: string;
}

export interface listPluginsReq {
}

export interface projectionGetReq {
  actorPath: string;
  component: string;
  schemaId: number;
}

export interface projectionWatchReq {
  actorPath: string;
  component: string;
  schemaId: number;
}

export interface readSnapshotReq {
  snapshotId: string;
}

export interface readSnapshotResp {
  content: string;
}

export interface registerActorReq {
  PluginID: string;
  Name: string;
  Version: string;
  Namespace: string;
  CallIDs: string[];
  Manifest: AppManifest;
  Abi: PluginAbi;
}

export interface registerActorRes {
  Registered: number;
}

export interface setPermissionModeReq {
  Mode: string;
}

export interface unregisterActorReq {
  PluginID: string;
}

export interface unregisterActorRes {
  Removed: number;
}

