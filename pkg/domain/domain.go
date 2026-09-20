package domain

import (
	"fmt"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ModelUnit is the canonical model + provider pair that identifies one callable
// unit. Defined here because the same struct is code-generated independently in
// several schema packages; the agent/aigen wire path is the heaviest user.
type ModelUnit = gen.ModelUnit

// ModelRef is one candidate in a ModelSlot fallback chain. Kind selects the
// resolution target: "unit" (concrete model+provider served by the system
// aggregator), "aggregator" (delegate to a named aggregator pool), or "auto"
// (system aggregator fast-pick).
type ModelRef = gen.ModelRef

// ModelSlot is an ordered fallback chain of ModelRef candidates for one agent
// role slot (primary/fast/execution/review/summary). An empty slot is
// equivalent to [auto].
type ModelSlot = gen.ModelSlot

// ProjectRef is a mounted project handle. Generated from
// schemas/workspace.spore; aliased here for callers that already import
// pkg/domain.
type ProjectRef = gen.ProjectRef

// ProjectMount is an additional content root within a project.
type ProjectMount = gen.ProjectMount

// FileEntry is a single file or directory listing.
type FileEntry = gen.FileEntry

// Provider is an LLM provider configuration.
type Provider = gen.Provider

// ProviderDisableWindow is a daily [Start, End) time window during which a
// provider is disabled. See gen.ProviderDisableWindow for field semantics.
type ProviderDisableWindow = gen.ProviderDisableWindow

// ModelDefault is one editable model-name-prefix → default-parameter entry.
// The built-in baseline (MODEL_CONTEXT_DEFAULTS) seeds context sizes; user
// entries with the same prefix override the baseline and new prefixes augment
// it. See gen.ModelDefault for field semantics.
type ModelDefault = gen.ModelDefault

// AIManagerModelDefaultsGetReq is the request for aimanager.model_defaults_get.
type AIManagerModelDefaultsGetReq = gen.AIManagerModelDefaultsGetReq

// AIManagerModelDefaultsGetResp is the response for
// aimanager.model_defaults_get, carrying the effective defaults table
// (baseline + user overrides, sorted by prefix).
type AIManagerModelDefaultsGetResp = gen.AIManagerModelDefaultsGetResp

// AIManagerModelDefaultsSetReq is the request for
// aimanager.model_defaults_set, carrying the full replacement of the
// user-edited entries.
type AIManagerModelDefaultsSetReq = gen.AIManagerModelDefaultsSetReq

// AIManagerModelDefaultsSetResp is the response for
// aimanager.model_defaults_set.
type AIManagerModelDefaultsSetResp = gen.AIManagerModelDefaultsSetResp

// AIManagerFetchOpenRouterModelsReq is the request for
// aimanager.fetch_openrouter_models (empty).
type AIManagerFetchOpenRouterModelsReq = gen.AIManagerFetchOpenRouterModelsReq

// AIManagerFetchOpenRouterModelsResp is the response for
// aimanager.fetch_openrouter_models, carrying a deduplicated, sorted list of
// model defaults (prefix + context length) fetched from openrouter.ai.
type AIManagerFetchOpenRouterModelsResp = gen.AIManagerFetchOpenRouterModelsResp

// ProviderModel is one model entry on a provider, carrying context window and
// output token limits so callers (aiaggregator, agent) can size context
// budgets without a separate provider round-trip per dispatch.
type ProviderModel = gen.ProviderModel

// Model is LLM model metadata.
type Model = gen.Model

// Prompt is a prompt asset.
type Prompt = gen.Prompt

// AgentConfig is a snapshot of an agent instance configuration.
type AgentConfig = gen.AgentConfig

// SkillsChangedEvent is emitted when the skill collection changes.
type SkillsChangedEvent = gen.SkillsChangedEvent

// CompactionPolicy configures context compaction behavior for an agent.
type CompactionPolicy = gen.CompactionPolicy

// StoragePolicy configures session storage/discard behavior for an agent.
type StoragePolicy = gen.StoragePolicy

// AgentStorageConfigureReq is the request for agent.storage.configure.
type AgentStorageConfigureReq = gen.AgentStorageConfigureReq

// AgentStorageConfigureResp is the response for agent.storage.configure.
type AgentStorageConfigureResp = gen.AgentStorageConfigureResp

// SummarySegment records one contiguous compaction result.
type SummarySegment = gen.SummarySegment

// SystemContentBlock is one block in a structured system prompt with optional cache control.
type SystemContentBlock = gen.SystemContentBlock

// MiddlewareEntry is one middleware binding in a turn's compiled context.
type MiddlewareEntry = gen.MiddlewareEntry

// AIStatsRecord is one immutable LLM request telemetry record.
type AIStatsRecord = gen.AIStatsRecord

// TurnRequestStat is a trimmed request stat embedded inside a Turn.
type TurnRequestStat = gen.TurnRequestStat

// AIStatsRecordReq is the request for aistats.record.
type AIStatsRecordReq = gen.AIStatsRecordReq

// AIStatsQueryReq/Resp are the aistats query surfaces.
type AIStatsQueryReq = gen.AIStatsQueryReq
type AIStatsQueryResp = gen.AIStatsQueryResp

// AIStatsCostConfigureReq/Resp are the aistats cost rate surfaces.
type AIStatsCostConfigureReq = gen.AIStatsCostConfigureReq
type AIStatsCostConfigureResp = gen.AIStatsCostConfigureResp

// AIStatsCostListReq/Resp list configured cost rates.
type AIStatsCostListReq = gen.AIStatsCostListReq
type AIStatsCostListResp = gen.AIStatsCostListResp

// AgentSessionStatsReq/Resp are the agent.session.stats surfaces.
type AgentSessionStatsReq = gen.AgentSessionStatsReq
type AgentSessionStatsResp = gen.AgentSessionStatsResp

// CompiledInstructions is the provider-agnostic instruction bundle built by agent.
type CompiledInstructions = gen.CompiledInstructions

// CompiledCapabilities is the provider-agnostic capability snapshot built by agent.
type CompiledCapabilities = gen.CompiledCapabilities

// CompiledContext is the provider-agnostic turn context built by agent.
type CompiledContext = gen.CompiledContext

// TurnInput is the raw business input for one turn after submit validation.
type TurnInput = gen.TurnInput

// TurnStartReq is the frozen input + compiled context handed from agent to turn.
type TurnStartReq = gen.TurnStartReq

// TurnAnswerReq carries the user's answer back to a paused turn.
type TurnAnswerReq = gen.TurnAnswerReq

// TurnAction is one durable execution trace item recorded during a turn.
type TurnAction = gen.TurnAction

// Step is one UI message unit (user or assistant).
type Step = gen.Step

// StepEvent is a streaming delta for an open Step.
type StepEvent = gen.StepEvent

// TurnActionKind discriminates structured execution trace items.
type TurnActionKind = string

type EffectKind = string

const (
	TurnActionLLMCall    TurnActionKind = "llm_call"
	TurnActionToolCall   TurnActionKind = "tool_call"
	TurnActionToolResult TurnActionKind = "tool_result"
	TurnActionReasoning  TurnActionKind = "reasoning"
	TurnActionPlan       TurnActionKind = "plan"
	TurnActionError      TurnActionKind = "error"
	TurnActionTurnStart  TurnActionKind = "turn_start"
)

const (
	EffectNone         EffectKind = "none"
	EffectReversible   EffectKind = "reversible"
	EffectIrreversible EffectKind = "irreversible"
)

// TurnEntry is one durable semantic unit inside a turn body.
type TurnEntry = gen.TurnEntry

// TurnBlock is one durable payload/render unit inside a turn entry.
type TurnBlock = gen.TurnBlock

// AgentKind values identify the role/strategy of an agent instance.
type AgentKind = string

const (
	AgentKindCoder       AgentKind = "coder"
	AgentKindReviewer    AgentKind = "reviewer"
	AgentKindAppBuilder  AgentKind = "app-builder"
	AgentKindDreamer     AgentKind = "dreamer"
	AgentKindCoordinator AgentKind = "coordinator"
	AgentKindWorker      AgentKind = "worker"
	AgentKindScout       AgentKind = "scout"
	AgentKindArchitect   AgentKind = "architect" // retired: workflow capability is now a mountable bundle (workflow-tools)
	AgentKindPlugin      AgentKind = "plugin"    // dedicated per-app agent provisioned at registration (plugin_agent block); never user-creatable
)

// AgentKindInfo describes one agent kind for the list_agent_kinds API.
type AgentKindInfo = gen.AgentKindInfo

// ValidAgentKinds returns the currently enabled agent kinds. Builtin is true on
// every entry: this table is exactly the set the AgentKindInfo.Builtin marker
// is defined against (a custom user-defined kind is never in this table).
func ValidAgentKinds() []AgentKindInfo {
	return []AgentKindInfo{
		{Kind: AgentKindCoder, DisplayName: "Coder", UserCreatable: true, SystemManaged: false, Builtin: true},
		{Kind: AgentKindReviewer, DisplayName: "Reviewer", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: AgentKindAppBuilder, DisplayName: "App Builder", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: "explorer", DisplayName: "Explorer", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: "general", DisplayName: "General", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: AgentKindDreamer, DisplayName: "Dreamer", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: AgentKindCoordinator, DisplayName: "Coordinator", UserCreatable: true, SystemManaged: true, Builtin: true},
		{Kind: AgentKindWorker, DisplayName: "Worker", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: AgentKindScout, DisplayName: "Scout", UserCreatable: false, SystemManaged: true, Builtin: true},
		{Kind: AgentKindPlugin, DisplayName: "Plugin", UserCreatable: false, SystemManaged: true, Builtin: true},
	}
}

// ValidateAgentKind rejects unknown or not-yet-enabled agent kinds.
func ValidateAgentKind(kind string) error {
	for _, k := range ValidAgentKinds() {
		if k.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("unknown agent kind: %q", kind)
}

// AgentKindDisplayName returns the human-readable role name for an agent kind
// (e.g. "Coder" for "coder"). Falls back to the raw kind from the registry
// (including retired kinds like "architect").
func AgentKindDisplayName(kind string) string {
	for _, k := range ValidAgentKinds() {
		if k.Kind == kind {
			return k.DisplayName
		}
	}
	return kind
}

// TurnEventKind discriminates streaming events emitted on the "turn" channel.
// Defined as a string alias so the TurnEventKind constants below assign
// directly to the generated TurnEvent.Kind field (which is plain string).
type TurnEventKind = string

const (
	TurnPlanApprovalRequested TurnEventKind = "turn.plan_approval_requested"
	TurnContextBudget         TurnEventKind = "turn.context_budget"
	TurnDispatchRetry         TurnEventKind = "turn.dispatch_retry"
	TurnStarted               TurnEventKind = "turn.started"
	TurnPaused                TurnEventKind = "turn.paused"
	TurnResumed               TurnEventKind = "turn.resumed"
	TurnUnitChanged           TurnEventKind = "turn.unit_changed"
	TurnCompleted             TurnEventKind = "turn.completed"
	TurnFailed                TurnEventKind = "turn.failed"
	TurnCancelled             TurnEventKind = "turn.cancelled"
	// TurnHistoryImported is emitted when a chunk of session history is
	// imported off the live turn loop (agent clone/fork). The importing
	// handler runs without emitting step/turn lifecycle events, so without
	// this signal subscribers (e.g. the frontend timeline) never learn the
	// imported turns exist and keep showing an empty session. It carries no
	// TurnId; the Kind alone is the trigger.
	TurnHistoryImported TurnEventKind = "turn.history_imported"
)

// TurnContextBudgetPayload is the payload for TurnContextBudget.
type TurnContextBudgetPayload = gen.TurnContextBudgetPayload

// TurnPlanApprovalRequestedPayload is the payload for TurnPlanApprovalRequested.
type TurnPlanApprovalRequestedPayload = gen.TurnPlanApprovalRequestedPayload

// PermissionPolicy configures permission checking behavior for a turn.
type PermissionPolicy = gen.PermissionPolicy

// ToolCallSummary carries minimal tool call info for permission requests.
type ToolCallSummary = gen.ToolCallSummary

// PermissionReviewReq is sent to an agent approver.
type PermissionReviewReq = gen.PermissionReviewReq

// PermissionReviewResp is returned by an agent approver.
type PermissionReviewResp = gen.PermissionReviewResp

// TurnEvent is a streaming output event from an agent. The payload pointer
// fields are an or-pattern discriminated by Kind.
type TurnEvent = gen.TurnEvent

// Session is the minimal persisted turn history owned by an agent.
type Session = gen.Session

// Turn is one completed or submitted turn in agent session history.
type Turn = gen.Turn

// AttachmentEntry and ImageEntry are shared attachment/image metadata types.
type AttachmentEntry = gen.AttachmentEntry
type ImageEntry = gen.ImageEntry

// ExploreResult persists the output of a fork_child exploration so the
// frontend can render the summary after reconnect.
type ExploreResult = gen.ExploreResult

// TurnFileChange records a single file modification within a turn.
type TurnFileChange = gen.TurnFileChange

// PendingSubmit is a user message queued while a turn is in progress.
type PendingSubmit = gen.PendingSubmit

// RawSession is the full uncompiled truth layer for an agent.
type RawSession = gen.RawSession

// RawSessionArtifact is one artifact in the raw truth layer.
type RawSessionArtifact = gen.RawSessionArtifact

// AggregatorChunkKind discriminates streaming chunks emitted by aiaggregator
// to its caller. This is the provider-facing protocol — distinct from
// TurnEventKind, which is the turn-orchestration protocol owned by the
// turn assembler one layer up. Defined as a string alias so the constants
// below assign directly to the generated AggregatorChunk.Kind field.
type AggregatorChunkKind = string

const (
	AggregatorChunkText              AggregatorChunkKind = "text_delta"
	AggregatorChunkReasoning         AggregatorChunkKind = "reasoning_delta"
	AggregatorChunkUsage             AggregatorChunkKind = "usage"
	AggregatorChunkToolUseStart      AggregatorChunkKind = "tool_use_start"
	AggregatorChunkToolUseInputDelta AggregatorChunkKind = "tool_use_input_delta"
	AggregatorChunkToolUseComplete   AggregatorChunkKind = "tool_use_complete"
	AggregatorChunkStop              AggregatorChunkKind = "stop"
	// AggregatorChunkResolvedUnit reports the concrete unit the aggregator
	// selected for this dispatch, emitted once before content. Lets the caller
	// learn the real executing model under aggregator/auto routing.
	AggregatorChunkResolvedUnit AggregatorChunkKind = "resolved_unit"
)

// UsageData carries token counters reported by a provider.
type UsageData = gen.UsageData

// AggregatorChunk is one streamed chunk from aiaggregator.dispatch.
// Lifecycle (started/completed/failed) is implicit via the gospore stream
// frames (End / Error), so this protocol carries only payload chunks.
type AggregatorChunk = gen.AggregatorChunk

// ToolSpec describes one callable exposed to the LLM as a tool. InputSchema
// is JSON Schema (object form). Carried opaquely from the orchestrator
// down through the aggregator into the provider request.
type ToolSpec = gen.ToolSpec

// ChatMessageRole values used inside ChatMessage.
const (
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
	ChatRoleTool      = "tool"
)

// ContentBlockType values used inside ContentBlock.
const (
	ContentBlockText       = "text"
	ContentBlockToolUse    = "tool_use"
	ContentBlockToolResult = "tool_result"
	ContentBlockImage      = "image"
)

// ContentBlock is one piece of a ChatMessage. Type determines which
// fields are populated.
type ContentBlock = gen.ContentBlock

// ChatMessage is one multi-content message in a turn's conversation
// history (for tool-use iterations).
type ChatMessage = gen.ChatMessage

// WorkspaceMountReq is the request for workspace.mount.
type WorkspaceMountReq = gen.WorkspaceMountReq

// WorkspaceUnmountReq is the request for workspace.unmount.
type WorkspaceUnmountReq = gen.WorkspaceUnmountReq

// WorkspaceCreateReq is the request for workspace.create.
type WorkspaceCreateReq = gen.WorkspaceCreateReq

// WorkspaceUpdateProjectReq is the request for workspace.update_project.
type WorkspaceUpdateProjectReq = gen.WorkspaceUpdateProjectReq

// WorkspaceUpdateProjectResp is the response for workspace.update_project.
type WorkspaceUpdateProjectResp = gen.WorkspaceUpdateProjectResp

// WorkspaceMountsEvent is emitted whenever the workspace mounts list changes
// (mount/unmount/create or sub-mount add/remove). Carries the full snapshot
// so subscribers reconcile without an extra round-trip.
type WorkspaceMountsEvent = gen.WorkspaceMountsEvent

// WorkspaceAgentsChangedEvent is emitted whenever the workspace agent list changes
// (create/delete). Frontends should re-fetch the full agent list.
type WorkspaceAgentsChangedEvent = gen.WorkspaceAgentsChangedEvent

// FrontendErrorReport is an error report from the web UI.
type FrontendErrorReport = gen.FrontendErrorReport

// RootDirEntry is a root directory (module) inside a project.
type RootDirEntry = gen.RootDirEntry

// DesktopWindowState is the persisted desktop window geometry.
type DesktopWindowState = gen.DesktopWindowState

// ── browser ──

// BrowserPageState is the persisted page-level browsing state of a browser window.
type BrowserPageState = gen.BrowserPageState

// BrowserHistoryEntry is one visit in a browser window's persisted address history.
type BrowserHistoryEntry = gen.BrowserHistoryEntry

// BrowserWindowState is the persisted geometry + navigation state of a browser window.
type BrowserWindowState = gen.BrowserWindowState

// BrowserInstanceConfig is the persisted record for one browser window.
type BrowserInstanceConfig = gen.BrowserInstanceConfig

// BrowserInstanceStatus is the runtime state of one browser window.
type BrowserInstanceStatus = gen.BrowserInstanceStatus

// BrowserInstance composes config + status for list/get responses.
type BrowserInstance = gen.BrowserInstance

// BrowserManagerListResp is the response for browsermanager.list.
type BrowserManagerListResp = gen.BrowserManagerListResp

// BrowserManagerCreateReq creates a new browser instance (manager assigns the id).
type BrowserManagerCreateReq = gen.BrowserManagerCreateReq

// BrowserManagerRemoveReq removes an instance by id.
type BrowserManagerRemoveReq = gen.BrowserManagerRemoveReq

// BrowserManagerGetReq fetches one instance by id.
type BrowserManagerGetReq = gen.BrowserManagerGetReq

// BrowserManagerUpdateReq replaces a stored instance config.
type BrowserManagerUpdateReq = gen.BrowserManagerUpdateReq

// BrowserManagerNavigateReq navigates one instance to a new URL.
type BrowserManagerNavigateReq = gen.BrowserManagerNavigateReq

// BrowserManagerOpenReq reopens one closed instance at its last saved state.
type BrowserManagerOpenReq = gen.BrowserManagerOpenReq

// BrowserManagerOpenGlobalReq opens a URL in the shared global browser tab.
type BrowserManagerOpenGlobalReq = gen.BrowserManagerOpenGlobalReq

// BrowserManagerOpenGlobalResp is the response for browsermanager.open_global.
type BrowserManagerOpenGlobalResp = gen.BrowserManagerOpenGlobalResp

// BrowserManagerAsyncResult is the immediate response for async-ack callables.
type BrowserManagerAsyncResult = gen.BrowserManagerAsyncResult

// BrowserManagerEvent is emitted when an async operation completes or fails.
type BrowserManagerEvent = gen.BrowserManagerEvent

// BrowserCookieEntry represents one cookie, aligned with WebView2 cookie fields.
// Generated from schemas/browser.cookie._897.spore.
type BrowserCookieEntry = gen.BrowserCookieEntry

// BrowserManagerExportCookiesReq is the request for browsermanager.export_cookies.
// Generated from schemas/browser.cookie._897.spore.
type BrowserManagerExportCookiesReq = gen.BrowserManagerExportCookiesReq

// BrowserManagerExportCookiesResp carries cookies grouped by domain.
// Generated from schemas/browser.cookie._897.spore.
type BrowserManagerExportCookiesResp = gen.BrowserManagerExportCookiesResp

// BrowserManagerImportCookiesReq carries cookies to import into an instance.
// Generated from schemas/browser.cookie._897.spore.
type BrowserManagerImportCookiesReq = gen.BrowserManagerImportCookiesReq

// BrowserManagerImportCookiesResp reports the number of cookies written.
// Generated from schemas/browser.cookie._897.spore.
type BrowserManagerImportCookiesResp = gen.BrowserManagerImportCookiesResp

// CookieBridgePairingInfoReq is empty; pairing info is actor state.
// Generated from schemas/cookiebridge._6256.spore.
type CookieBridgePairingInfoReq = gen.CookieBridgePairingInfoReq

// CookieBridgePairingInfoResp exposes loopback endpoints and the pairing token.
// Generated from schemas/cookiebridge._6256.spore.
type CookieBridgePairingInfoResp = gen.CookieBridgePairingInfoResp

// CookieBridgeRegenTokenReq is empty; regeneration needs no parameters.
// Generated from schemas/cookiebridge._6256.spore.
type CookieBridgeRegenTokenReq = gen.CookieBridgeRegenTokenReq

// CookieBridgeRegenTokenResp carries the freshly minted pairing token.
// Generated from schemas/cookiebridge._6256.spore.
type CookieBridgeRegenTokenResp = gen.CookieBridgeRegenTokenResp

// BrowserPageObservation is a structured page snapshot produced by the desktop observe script.
// Generated from schemas/browseruse._892.spore.
type BrowserPageObservation = gen.BrowserPageObservation

// BrowserUseReq is the request payload for the browser.use callable.
// Generated from schemas/browseruse._892.spore.
type BrowserUseReq = gen.BrowserUseReq

// BrowserUseResp is the response from the browser.use callable.
// Generated from schemas/browseruse._892.spore.
type BrowserUseResp = gen.BrowserUseResp

// BrowserElement is an interactive element detected on a web page.
// Generated from schemas/browseruse._892.spore.
type BrowserElement = gen.BrowserElement

// BrowserElementRect is a bounding box in viewport coordinates.
// Generated from schemas/browseruse._892.spore.
type BrowserElementRect = gen.BrowserElementRect

// ── interfacemanager ──

// InterfaceManagerControlReq drives the omnibox surfaces via interfacemanager.control.
type InterfaceManagerControlReq = gen.InterfaceManagerControlReq

// InterfaceManagerControlResp is the ack for interfacemanager.control.
type InterfaceManagerControlResp = gen.InterfaceManagerControlResp

// InterfaceManagerEvent is emitted on every successful control invocation.
type InterfaceManagerEvent = gen.InterfaceManagerEvent

// GuideStep describes one step in an interactive guide overlay.
type GuideStep = gen.GuideStep

// TutorialSpec describes a dynamic tutorial with literal Title/Body steps
// (dynamic tutorials never go through i18n keys).
type TutorialSpec = gen.TutorialSpec

// GuideAnchor describes one addressable UI anchor the agent can target when
// composing guide steps.
type GuideAnchor = gen.GuideAnchor

// UiInteractionRecord captures one user interaction with the UI.
type UiInteractionRecord = gen.UiInteractionRecord

// ReportInteractionReq records a user interaction with the UI.
type ReportInteractionReq = gen.ReportInteractionReq

// ReportInteractionResp acknowledges a reported interaction.
type ReportInteractionResp = gen.ReportInteractionResp

// QueryInteractionsReq filters the interaction history.
type QueryInteractionsReq = gen.QueryInteractionsReq

// QueryInteractionsResp returns matching interaction records.
type QueryInteractionsResp = gen.QueryInteractionsResp

// ProjectInfoRoot is one root entry inside a project.info response.
type ProjectInfoRoot = gen.ProjectInfoRoot

// ProjectFileListReq is the request for project.list.
type ProjectFileListReq = gen.ProjectFileListReq

// ProjectFileReadReq is the request for project.read.
type ProjectFileReadReq = gen.ProjectFileReadReq

// ProjectGitStatusReq is the request for project.git_status.
type ProjectGitStatusReq = gen.ProjectGitStatusReq

// ProjectGitStatusResp is the response for project.git_status.
type ProjectGitStatusResp = gen.ProjectGitStatusResp

// ProjectGitFileStatus is one file entry in a project.git_status response.
type ProjectGitFileStatus = gen.ProjectGitFileStatus

// Shared git domain types (project-local copies).
type ProjectGitCommitInfo = gen.ProjectGitCommitInfo
type ProjectGitBranchInfo = gen.ProjectGitBranchInfo
type ProjectGitStashInfo = gen.ProjectGitStashInfo
type ProjectGitRemoteInfo = gen.ProjectGitRemoteInfo
type ProjectGitBlameLine = gen.ProjectGitBlameLine

// project.git_* request / response wrappers.
type ProjectGitLogReq = gen.ProjectGitLogReq
type ProjectGitLogResp = gen.ProjectGitLogResp
type ProjectGitDiffReq = gen.ProjectGitDiffReq
type ProjectGitDiffResp = gen.ProjectGitDiffResp
type ProjectGitAddReq = gen.ProjectGitAddReq
type ProjectGitCommitReq = gen.ProjectGitCommitReq
type ProjectGitCommitResp = gen.ProjectGitCommitResp
type ProjectGitPushReq = gen.ProjectGitPushReq
type ProjectGitPullReq = gen.ProjectGitPullReq
type ProjectGitBranchReq = gen.ProjectGitBranchReq
type ProjectGitBranchResp = gen.ProjectGitBranchResp
type ProjectGitCheckoutReq = gen.ProjectGitCheckoutReq
type ProjectGitResetReq = gen.ProjectGitResetReq
type ProjectGitStashSaveReq = gen.ProjectGitStashSaveReq
type ProjectGitStashPopReq = gen.ProjectGitStashPopReq
type ProjectGitStashListReq = gen.ProjectGitStashListReq
type ProjectGitStashListResp = gen.ProjectGitStashListResp
type ProjectGitStashDropReq = gen.ProjectGitStashDropReq
type ProjectGitRemoteListReq = gen.ProjectGitRemoteListReq
type ProjectGitRemoteListResp = gen.ProjectGitRemoteListResp
type ProjectGitRemoteAddReq = gen.ProjectGitRemoteAddReq
type ProjectGitRemoteRemoveReq = gen.ProjectGitRemoteRemoveReq
type ProjectGitBlameReq = gen.ProjectGitBlameReq
type ProjectGitBlameResp = gen.ProjectGitBlameResp
type ProjectGitConfigGetReq = gen.ProjectGitConfigGetReq
type ProjectGitConfigGetResp = gen.ProjectGitConfigGetResp
type ProjectGitConfigSetReq = gen.ProjectGitConfigSetReq

// ProjectInfoResp is the response for project.info.
type ProjectInfoResp = gen.ProjectInfoResp

// ProjectSyncRootsReq is the request for project.sync_roots.
type ProjectSyncRootsReq = gen.ProjectSyncRootsReq

// FileSystemListReq is the request for filesystem.list.
type FileSystemListReq = gen.FileSystemListReq

// FileSystemReadReq is the request for filesystem.read.
type FileSystemReadReq = gen.FileSystemReadReq

// FileSystemReadResp is the response for filesystem.read.
type FileSystemReadResp = gen.FileSystemReadResp

// FileSystemReadBase64Req is the request for filesystem.read_base64.
type FileSystemReadBase64Req = gen.FileSystemReadBase64Req

// FileSystemReadBase64Resp is the response for filesystem.read_base64.
type FileSystemReadBase64Resp = gen.FileSystemReadBase64Resp

// FileSystemReadChunkReq is the request for filesystem.read_chunk.
type FileSystemReadChunkReq = gen.FileSystemReadChunkReq

// FileSystemReadChunkResp is the response for filesystem.read_chunk.
type FileSystemReadChunkResp = gen.FileSystemReadChunkResp

// FileSystemWriteReq is the request for filesystem.write.
type FileSystemWriteReq = gen.FileSystemWriteReq

// FileSystemWriteBase64Req is the request for filesystem.write_base64.
type FileSystemWriteBase64Req = gen.FileSystemWriteBase64Req

// FileSystemWriteResp is the response for filesystem.write.
type FileSystemWriteResp = gen.FileSystemWriteResp

// FileSystemEditReq is the request for filesystem.edit.
type FileSystemEditReq = gen.FileSystemEditReq

// FileSystemEditHunk is a single diff hunk in filesystem.edit response.
type FileSystemEditHunk = gen.FileSystemEditHunk

// FileSystemEditResp is the response for filesystem.edit.
type FileSystemEditResp = gen.FileSystemEditResp

// FileSystemGlobReq is the request for filesystem.glob.
type FileSystemGlobReq = gen.FileSystemGlobReq

// FileSystemGlobResp is the response for filesystem.glob.
type FileSystemGlobResp = gen.FileSystemGlobResp

// FileSystemGrepReq is the request for filesystem.grep.
type FileSystemGrepReq = gen.FileSystemGrepReq

// FileSystemGrepMatch is a single match result for filesystem.grep.
type FileSystemGrepMatch = gen.FileSystemGrepMatch

// FileSystemGrepCount is a per-file count result for filesystem.grep.
type FileSystemGrepCount = gen.FileSystemGrepCount

// FileSystemGrepResp is the unified response for filesystem.grep.
type FileSystemGrepResp = gen.FileSystemGrepResp

// FileSystemRootsResp is the response for filesystem.roots.
type FileSystemRootsResp = gen.FileSystemRootsResp

// FileSystemRmReq is the request for filesystem.rm.
type FileSystemRmReq = gen.FileSystemRmReq

// FileSystemRmResp is the response for filesystem.rm.
type FileSystemRmResp = gen.FileSystemRmResp

// ArchiveExportReq is the request for archive_export.
type ArchiveExportReq = gen.ArchiveExportReq

// ArchiveExportResp is the response for archive_export.
type ArchiveExportResp = gen.ArchiveExportResp

// ArchiveImportReq is the request for archive_import.
type ArchiveImportReq = gen.ArchiveImportReq

// ArchiveImportResp is the response for archive_import.
type ArchiveImportResp = gen.ArchiveImportResp

// FileSystemSyncRootsReq is the internal request for filesystem.sync_roots.
type FileSystemSyncRootsReq = gen.FileSystemSyncRootsReq

// AIManagerProviderConfigureReq is the request for aimanager.provider.configure.
type AIManagerProviderConfigureReq = gen.AIManagerProviderConfigureReq

// AIManagerProviderConfigureResp is the response for aimanager.provider.configure.
type AIManagerProviderConfigureResp = gen.AIManagerProviderConfigureResp

// AIManagerProviderSetTokenPlanReq is the request for aimanager.provider.set_token_plan.
type AIManagerProviderSetTokenPlanReq = gen.AIManagerProviderSetTokenPlanReq

// AIManagerProviderSetTokenPlanResp is the response for aimanager.provider.set_token_plan.
type AIManagerProviderSetTokenPlanResp = gen.AIManagerProviderSetTokenPlanResp

// AIManagerProviderSetDisabledReq is the request for aimanager.provider.set_disabled.
type AIManagerProviderSetDisabledReq = gen.AIManagerProviderSetDisabledReq

// AIManagerProviderSetDisabledResp is the response for aimanager.provider.set_disabled.
type AIManagerProviderSetDisabledResp = gen.AIManagerProviderSetDisabledResp

// AIManagerProviderResetHealthReq is the request for aimanager.provider.reset_health.
type AIManagerProviderResetHealthReq = gen.AIManagerProviderResetHealthReq

// AIManagerProviderResetHealthResp is the response for aimanager.provider.reset_health.
type AIManagerProviderResetHealthResp = gen.AIManagerProviderResetHealthResp

// AIManagerProviderRecordProbeReq is the request for aimanager.provider.record_probe.
type AIManagerProviderRecordProbeReq = gen.AIManagerProviderRecordProbeReq

// AIManagerProviderRecordProbeResp is the response for aimanager.provider.record_probe.
type AIManagerProviderRecordProbeResp = gen.AIManagerProviderRecordProbeResp

// AIManagerUnitHealthListReq is the request for aimanager.unit_health_list.
type AIManagerUnitHealthListReq = gen.AIManagerUnitHealthListReq

// AIManagerUnitHealthListResp is the response for aimanager.unit_health_list.
type AIManagerUnitHealthListResp = gen.AIManagerUnitHealthListResp

// AIManagerListUnitsReq is the request for aimanager.list_units.
type AIManagerListUnitsReq = gen.AIManagerListUnitsReq

// AIManagerListUnitsResp is the response for aimanager.list_units.
type AIManagerListUnitsResp = gen.AIManagerListUnitsResp

// AIManagerProviderFetchModelsReq is the request for aimanager.provider.fetch_models.
type AIManagerProviderFetchModelsReq = gen.AIManagerProviderFetchModelsReq

// AIManagerProviderFetchModelsResp is the response for aimanager.provider.fetch_models.
type AIManagerProviderFetchModelsResp = gen.AIManagerProviderFetchModelsResp

// AIManagerProviderResolveTokenReq is the request for aimanager.provider.resolve_token.
type AIManagerProviderResolveTokenReq = gen.AIManagerProviderResolveTokenReq

// AIManagerProviderResolveTokenResp is the response for aimanager.provider.resolve_token.
type AIManagerProviderResolveTokenResp = gen.AIManagerProviderResolveTokenResp

// AIManagerProviderResolveModelReq is the request for aimanager.provider.resolve_model.
type AIManagerProviderResolveModelReq = gen.AIManagerProviderResolveModelReq

// AIManagerProviderResolveModelResp is the response for aimanager.provider.resolve_model.
type AIManagerProviderResolveModelResp = gen.AIManagerProviderResolveModelResp

// AIManagerAggregatorConfigureReq is the request for aimanager.aggregator.configure.
type AIManagerAggregatorConfigureReq = gen.AIManagerAggregatorConfigureReq

// AIManagerAggregatorConfigureResp is the response for aimanager.aggregator.configure.
type AIManagerAggregatorConfigureResp = gen.AIManagerAggregatorConfigureResp

// AIManagerAggregatorSetDisabledReq is the request for aimanager.aggregator.set_disabled.
type AIManagerAggregatorSetDisabledReq = gen.AIManagerAggregatorSetDisabledReq

// AIManagerAggregatorSetDisabledResp is the response for aimanager.aggregator.set_disabled.
type AIManagerAggregatorSetDisabledResp = gen.AIManagerAggregatorSetDisabledResp

// AIManagerAggregatorGetReq is the request for aimanager.aggregator.get.
type AIManagerAggregatorGetReq = gen.AIManagerAggregatorGetReq

// AIManagerAggregatorGetResp is the response for aimanager.aggregator.get.
type AIManagerAggregatorGetResp = gen.AIManagerAggregatorGetResp

// AIManagerAggregatorResolveReq is the request for aimanager.aggregator.resolve.
type AIManagerAggregatorResolveReq = gen.AIManagerAggregatorResolveReq

// AIManagerAggregatorResolveResp is the response for aimanager.aggregator.resolve.
type AIManagerAggregatorResolveResp = gen.AIManagerAggregatorResolveResp

// ManualCallableUnit is a manually added callable unit for an aggregator.
type ManualCallableUnit = gen.ManualCallableUnit

// FetchedModel is one model entry returned by provider model fetching, with an
// inferred modality so the UI can classify image vs chat models.
type FetchedModel = gen.FetchedModel

// ConfigVersionPush is sent by aimanager to aiaggregator when provider config changes.
type ConfigVersionPush = gen.ConfigVersionPush

// AIAggregatorImageResolveReq is the request for aiaggregator.image.resolve.
type AIAggregatorImageResolveReq = gen.AIAggregatorImageResolveReq

// AIAggregatorImageResolveResp is the response for aiaggregator.image.resolve.
type AIAggregatorImageResolveResp = gen.AIAggregatorImageResolveResp

// AIAggregatorVideoResolveReq is the request for aiaggregator.video.resolve.
type AIAggregatorVideoResolveReq = gen.AIAggregatorVideoResolveReq

// AIAggregatorVideoResolveResp is the response for aiaggregator.video.resolve.
type AIAggregatorVideoResolveResp = gen.AIAggregatorVideoResolveResp

// AIAggregatorImageRecognizeReq is the request for aiaggregator.image_recognize.
type AIAggregatorImageRecognizeReq = gen.AIAggregatorImageRecognizeReq

// AIAggregatorImageRecognizeResp is the response for aiaggregator.image_recognize.
type AIAggregatorImageRecognizeResp = gen.AIAggregatorImageRecognizeResp

// AIManagerConfigExportReq is the request for aimanager.config.export.
type AIManagerConfigExportReq = gen.AIManagerConfigExportReq

// AIManagerConfigExportResp is the response for aimanager.config.export.
type AIManagerConfigExportResp = gen.AIManagerConfigExportResp

// AIManagerConfigImportReq is the request for aimanager.config.import.
type AIManagerConfigImportReq = gen.AIManagerConfigImportReq

// AIManagerConfigImportResp is the response for aimanager.config.import.
type AIManagerConfigImportResp = gen.AIManagerConfigImportResp

// PromptManagerGetReq is the request for promptmanager.get.
type PromptManagerGetReq = gen.PromptManagerGetReq

// PromptManagerSaveReq is the request for promptmanager.save.
type PromptManagerSaveReq = gen.PromptManagerSaveReq

// PromptRef is a stable reference to a prompt asset.
type PromptRef = gen.PromptRef

// WorkspacePromptRef is the PromptRef type from the workspace schema.
type WorkspacePromptRef = gen.PromptRef

// PromptManagerResolveRefReq is the request for promptmanager.resolve_prompt_ref.
type PromptManagerResolveRefReq = gen.PromptManagerResolveRefReq

// PromptResolvedRef is the resolved text for a PromptRef + slot.
type PromptResolvedRef = gen.PromptResolvedRef

// PromptManagerResolveFragmentsReq is the request for promptmanager.resolve_fragments.
type PromptManagerResolveFragmentsReq = gen.PromptManagerResolveFragmentsReq

// PromptResolvedFragmentsResp is the response for promptmanager.resolve_fragments.
type PromptResolvedFragmentsResp = gen.PromptResolvedFragmentsResp

// PromptFragment is a single prompt template fragment.
type PromptFragment = gen.PromptFragment

// PromptContextSegment describes a region of the assembled prompt.
type PromptContextSegment = gen.PromptContextSegment

// PromptArtifact is an assembled prompt with fragments and layout.
type PromptArtifact = gen.PromptArtifact

// PromptProfile stores system/role prompts for a scope+role pair.
type PromptProfile = gen.PromptProfile

// PromptManagerListFragmentsReq is the request for promptmanager.list_fragments.
type PromptManagerListFragmentsReq = gen.PromptManagerListFragmentsReq

// PromptManagerGetArtifactReq is the request for promptmanager.get_artifact.
type PromptManagerGetArtifactReq = gen.PromptManagerGetArtifactReq

// PromptManagerGetProfileReq is the request for promptmanager.get_profile.
type PromptManagerGetProfileReq = gen.PromptManagerGetProfileReq

// PromptManagerSaveProfileReq is the request for promptmanager.save_profile.
type PromptManagerSaveProfileReq = gen.PromptManagerSaveProfileReq

// PromptManagerDeleteProfileReq is the request for promptmanager.delete_profile.
type PromptManagerDeleteProfileReq = gen.PromptManagerDeleteProfileReq

// PromptManagerSaveFragmentReq is the request for promptmanager.save_fragment.
type PromptManagerSaveFragmentReq = gen.PromptManagerSaveFragmentReq

// PromptManagerDeleteFragmentReq is the request for promptmanager.delete_fragment.
type PromptManagerDeleteFragmentReq = gen.PromptManagerDeleteFragmentReq

// AgentRef is a lightweight agent instance reference.
//
// ActorID is the gospore canonical ActorID (32-char lowercase hex) of the
// agent actor itself — used by the frontend as the `target` wire field for
// chat.submit calls.
//
// AggregatorActorID is the canonical ActorID of the aimanager-owned
// aggregator the agent is bound to. The agent forwards chat dispatch to
// that aggregator via ctx.Plan.
type AgentRef = gen.AgentRef
type AgentChildRef = gen.AgentChildRef

type AgentConfigureReq = gen.AgentConfigureReq

// ── workflow orchestration ──

type GoalSummary = gen.GoalSummary
type AgentWorkflowStartReq = gen.AgentWorkflowStartReq
type AgentWorkflowStartResp = gen.AgentWorkflowStartResp
type AgentWorkflowStopReq = gen.AgentWorkflowStopReq
type AgentWorkflowStopResp = gen.AgentWorkflowStopResp
type AgentWorkflowPauseAllReq = gen.AgentWorkflowPauseAllReq
type AgentWorkflowPauseAllResp = gen.AgentWorkflowPauseAllResp
type WorkflowPlanSubmitReq = gen.WorkflowPlanSubmitReq
type WorkflowPlanSubmitResp = gen.WorkflowPlanSubmitResp
type AgentInternalAssignGoalReq = gen.AgentInternalAssignGoalReq
type AgentInternalAssignGoalResp = gen.AgentInternalAssignGoalResp
type AgentInternalResumeFromReviewReq = gen.AgentInternalResumeFromReviewReq
type WorkspaceAgentSpawnAssignReq = gen.WorkspaceAgentSpawnAssignReq
type WorkspaceAgentSpawnAssignResp = gen.WorkspaceAgentSpawnAssignResp
type WorkspaceAgentReviewReq = gen.WorkspaceAgentReviewReq
type WorkspaceAgentReviewResp = gen.WorkspaceAgentReviewResp
type WorkspaceAgentTerminateReq = gen.WorkspaceAgentTerminateReq
type WorkspaceAgentTerminateResp = gen.WorkspaceAgentTerminateResp
type WorkspaceAgentSpawnByTypeReq = gen.WorkspaceAgentSpawnByTypeReq
type WorkspaceAgentSpawnByTypeResp = gen.WorkspaceAgentSpawnByTypeResp

// WorkspaceAddMountReq is the request for workspace.add_mount.
type WorkspaceAddMountReq = gen.WorkspaceAddMountReq

// WorkspaceRemoveMountReq is the request for workspace.remove_mount.
type WorkspaceRemoveMountReq = gen.WorkspaceRemoveMountReq

// WorkspaceListAgentsReq is the request for workspace.list_agents.
type WorkspaceListAgentsReq = gen.WorkspaceListAgentsReq

// WorkspaceListAgentKindsResp is the response for workspace.list_agent_kinds.
type WorkspaceListAgentKindsResp = gen.WorkspaceListAgentKindsResp

// AgentKindConfig describes prompt-binding and lifecycle policy for one agent kind.
type AgentKindConfig = gen.AgentKindConfig

// RandomNameConfig controls random display-name generation for an agent kind.
type RandomNameConfig = gen.RandomNameConfig

// WorkspaceCompactionPolicy is the compaction policy type from workspace schema.
type WorkspaceCompactionPolicy = gen.CompactionPolicy

// WorkspaceGetAgentKindConfigReq is the request for workspace.get_agent_kind_config.
type WorkspaceGetAgentKindConfigReq = gen.WorkspaceGetAgentKindConfigReq

// WorkspaceSaveAgentKindConfigReq is the request for workspace.save_agent_kind_config.
type WorkspaceSaveAgentKindConfigReq = gen.WorkspaceSaveAgentKindConfigReq

// WorkspaceListAgentKindConfigsResp is the response for workspace.list_agent_kind_configs.
type WorkspaceListAgentKindConfigsResp = gen.WorkspaceListAgentKindConfigsResp

// WorkspaceCreateAgentKindReq is the request for workspace.create_agent_kind.
type WorkspaceCreateAgentKindReq = gen.WorkspaceCreateAgentKindReq

// WorkspaceCreateAgentKindResp is the response for workspace.create_agent_kind.
type WorkspaceCreateAgentKindResp = gen.WorkspaceCreateAgentKindResp

// WorkspaceDeleteAgentKindReq is the request for workspace.delete_agent_kind.
type WorkspaceDeleteAgentKindReq = gen.WorkspaceDeleteAgentKindReq

// WorkspaceDeleteAgentKindResp is the response for workspace.delete_agent_kind.
type WorkspaceDeleteAgentKindResp = gen.WorkspaceDeleteAgentKindResp

// UnifiedGraphNode is a node in the unified actor graph.
type UnifiedGraphNode = gen.UnifiedGraphNode

// CallableInterface describes a single callable on an actor.
type CallableInterface = gen.CallableInterface

// CallableParam describes a parameter of a callable.
type CallableParam = gen.CallableParam

// TopologyNodeDescriptor provides display metadata for an actor node.
type TopologyNodeDescriptor = gen.TopologyNodeDescriptor

// TopologyCard provides structured detail sections for DAG node expansion.
type TopologyCard = gen.TopologyCard

// TopologyCardSection is a group of rows in an expanded card.
type TopologyCardSection = gen.TopologyCardSection

// TopologyCardRow is a single key-value row, optionally expandable.
type TopologyCardRow = gen.TopologyCardRow

// UnifiedGraphEdge is an edge in the unified actor graph.
type UnifiedGraphEdge = gen.UnifiedGraphEdge

// UnifiedGraph is the full actor topology snapshot.
type UnifiedGraph = gen.UnifiedGraph

type GraphPatch = gen.GraphPatch
type TopologySyncReq = gen.TopologySyncReq
type TopologySyncResp = gen.TopologySyncResp
type TopologyHistoryEntry = gen.TopologyHistoryEntry
type TopologyHistoryResp = gen.TopologyHistoryResp
type TopologyEpochEvent = gen.TopologyEpochEvent

// ServiceInfo describes an exposed App-level service.
type ServiceInfo = gen.ServiceInfo

// RuntimeListServicesReq is the request for runtime.list_services.
type RuntimeListServicesReq = gen.RuntimeListServicesReq

// RuntimeListServicesResp is the response for runtime.list_services.
type RuntimeListServicesResp = gen.RuntimeListServicesResp

// RuntimeBuildInfoReq is the request for runtime.build_info.
type RuntimeBuildInfoReq = gen.RuntimeBuildInfoReq

// RuntimeBuildInfoResp is the response for runtime.build_info: the running
// process's build identity (version, build type, commit, build time).
type RuntimeBuildInfoResp = gen.RuntimeBuildInfoResp

// SendSessionMessageReq is the payload an agent forwards to
// aiaggregator.dispatch. Agents derive SessionID from their own turn ID.
//
// For a first-turn user message, Text carries the user input. For
// subsequent iterations within a multi-step tool-use loop, the agent
// builds Messages with the full conversation history (user input,
// assistant tool_use, user tool_result, ...). When Messages is non-empty
// Text is ignored.
type SendSessionMessageReq = gen.SendSessionMessageReq

// SummarizeResp is the response from aiaggregator.summarize.
type SummarizeResp = gen.SummarizeResp

// ProbeTokensResp is the response from aiaggregator.probe_tokens.
type ProbeTokensResp = gen.ProbeTokensResp

// TurnStatus is the response for turn.status.
type TurnStatus = gen.TurnStatus

// AgentTurnCompleteReq is sent by the inline turnEngine when a single-turn
// execution reaches a terminal state.
type AgentTurnCompleteReq = gen.AgentTurnCompleteReq

// AgentSessionUndoReq is the request for agent.session.undo.
type AgentSessionUndoReq = gen.AgentSessionUndoReq

// AgentSessionForkReq is the request for agent.session.fork.
type AgentSessionForkReq = gen.AgentSessionForkReq

// AgentSessionForkResp is the response for agent.session.fork.
type AgentSessionForkResp = gen.AgentSessionForkResp

// AgentSessionImportReq is the request for agent.session.import.
type AgentSessionImportReq = gen.AgentSessionImportReq

// AgentSessionImportResp is the response for agent.session.import.
type AgentSessionImportResp = gen.AgentSessionImportResp

// AgentSessionSummaryReq is the request for agent.session.summary.
type AgentSessionSummaryReq = gen.AgentSessionSummaryReq

// AgentSessionSummaryResp is the response for agent.session.summary.
type AgentSessionSummaryResp = gen.AgentSessionSummaryResp

// AgentTurnsListReq is the request for agent.turns.list.
type AgentTurnsListReq = gen.AgentTurnsListReq

// AgentTurnsListResp is the response for agent.turns.list.
type AgentTurnsListResp = gen.AgentTurnsListResp

// AgentCompactionConfigureReq is the request for agent.compaction.configure.
type AgentCompactionConfigureReq = gen.AgentCompactionConfigureReq

// AgentCompactionConfigureResp is the response for agent.compaction.configure.
type AgentCompactionConfigureResp = gen.AgentCompactionConfigureResp

// CompactionEvent records one auto-compaction round within a turn.
type CompactionEvent = gen.CompactionEvent

// SessionRequestRecord is one entry in the session-level request ledger
// (ordinary LLM request or compaction request).
type SessionRequestRecord = gen.SessionRequestRecord

// ContextSegment describes one segment in a token layout.
type ContextSegment = gen.ContextSegment

// StructuredTraceEntry captures one tool invocation for long-term history.
type StructuredTraceEntry = gen.StructuredTraceEntry

// StructuredTraceBlock wraps all tool trace entries from one turn.
type StructuredTraceBlock = gen.StructuredTraceBlock

// WorkspaceCreateAgentReq is the request for workspace.create_agent.
//
// AggregatorActorID binds the new agent to a specific aimanager-owned
// aggregator (canonical ActorID, 32-char lowercase hex). Required.
type WorkspaceCreateAgentReq = gen.WorkspaceCreateAgentReq

type WorkspaceUpdateAgentReq = gen.WorkspaceUpdateAgentReq

type WorkspaceDeleteAgentReq = gen.WorkspaceDeleteAgentReq

// WorkspaceCreateAppAgentReq is the request for workspace.create_app_agent —
// the internal idempotent provisioning callable used by appmanager once per
// plugin_agent binding slot (Slot; "default" for the unnamed block) of apps
// that declared plugin_agent blocks. The response shape keeps its historic
// WorkspaceEnsureAppAgentResp name.
type WorkspaceCreateAppAgentReq = gen.WorkspaceCreateAppAgentReq

type WorkspaceEnsureAppAgentResp = gen.WorkspaceEnsureAppAgentResp

// WorkspaceRemoveAppAgentReq is the request for workspace.remove_app_agent.
type WorkspaceRemoveAppAgentReq = gen.WorkspaceRemoveAppAgentReq

// WikiStarredEntry is one starred card in one mounted project, as returned by
// workspace.wiki_list_starred.
type WikiStarredEntry = gen.WikiStarredEntry

// WikiListStarredReq is the request for workspace.wiki_list_starred.
type WikiListStarredReq = gen.WikiListStarredReq

// WikiListStarredResp is the response for workspace.wiki_list_starred.
type WikiListStarredResp = gen.WikiListStarredResp

// WorkspaceLoadAgentReq is the request for workspace.load_agent.
type WorkspaceLoadAgentReq = gen.WorkspaceLoadAgentReq

// WorkspaceCloneAgentReq is the request for workspace.clone_agent.
type WorkspaceCloneAgentReq = gen.WorkspaceCloneAgentReq

// WorkspaceLogsQueryReq is the request for workspace.logs.query.
type WorkspaceLogsQueryReq = gen.WorkspaceLogsQueryReq

// WorkspaceLogsQueryResp is the response for workspace.logs.query.
type WorkspaceLogsQueryResp = gen.WorkspaceLogsQueryResp

// WorkspaceLogEntry is one structured log record returned by workspace.logs.query.
type WorkspaceLogEntry = gen.WorkspaceLogEntry

// WorkspaceAistatsActorIdReq is the request for workspace.aistats_actor_id.
type WorkspaceAistatsActorIdReq = gen.WorkspaceAistatsActorIdReq

// WorkspaceAistatsActorIdResp is the response for workspace.aistats_actor_id.
type WorkspaceAistatsActorIdResp = gen.WorkspaceAistatsActorIdResp

// SlashCommand is a single registered slash command (name + short help).
type SlashCommand = gen.SlashCommand

// WorkspaceSlashCommandsListReq is the request for workspace.slash_commands.list.
type WorkspaceSlashCommandsListReq = gen.WorkspaceSlashCommandsListReq

// WorkspaceSlashCommandsListResp is the response for workspace.slash_commands.list.
type WorkspaceSlashCommandsListResp = gen.WorkspaceSlashCommandsListResp

// DebugCommandsListResp is the response for workspace.debug_commands_list.
type DebugCommandsListResp = gen.DebugCommandsListResp

// DebugCommandExecReq is the request for workspace.debug_command_exec.
type DebugCommandExecReq = gen.DebugCommandExecReq

// DebugCommandExecResp is the response for workspace.debug_command_exec.
type DebugCommandExecResp = gen.DebugCommandExecResp

// BuiltinMode is a builtin mode card projected for the composer slash-mode
// interception filter (CardId, Name, Title, Icon).
type BuiltinMode = gen.BuiltinMode

// WorkspaceBuiltinModesListReq is the request for workspace.builtin.modes.list.
type WorkspaceBuiltinModesListReq = gen.WorkspaceBuiltinModesListReq

// WorkspaceBuiltinModesListResp is the response for workspace.builtin.modes.list.
type WorkspaceBuiltinModesListResp = gen.WorkspaceBuiltinModesListResp

type ProjectSpawnAgentReq = gen.ProjectSpawnAgentReq
type ProjectSpawnAgentResp = gen.ProjectSpawnAgentResp
type ChildSpawnConfig = gen.ChildSpawnConfig

// AggregatorDescriptor names one aimanager aggregator child, with both its
// kind (anthropic / openai / ...) and the gospore canonical ActorID the
// frontend uses as a `target` for chat.submit binding.
type AggregatorDescriptor = gen.AggregatorDescriptor

// AgentChatSubmitReq is the request for agent.chat.submit.
// The target ActorID identifying which agent runs the turn is supplied
// out-of-band via the gateway `target` wire field, so there is no
// agentID/sessionID field on the payload.
//
// AgentChatSubmitReq points to the agent.chat namespace request type so the
// public callable's wire shape matches the generated manifest entry.
type AgentChatSubmitReq = gen.AgentChatSubmitReq

// AgentChatSubmitResp is returned by agent.chat.submit, carrying the
// canonical ActorID of the spawned turn so the frontend can subscribe
// to its events immediately.
type AgentChatSubmitResp = gen.AgentChatSubmitResp

// WorkspaceCoordinatorLookupResp is the response for workspace.coordinator.lookup.
type WorkspaceCoordinatorLookupResp = gen.WorkspaceCoordinatorLookupResp

// CompleteMessageReq is the request for agent.complete_message.
// It carries a plain text prompt that is sent directly to the LLM
// without entering the agent's turn loop or polluting conversation history.
type CompleteMessageReq = gen.CompleteMessageReq

// AgentMessagesListReq is the request for agent.messages.list.
type AgentMessagesListReq = gen.AgentMessagesListReq

// AgentMessagesListResp is the response for agent.messages.list.
type AgentMessagesListResp = gen.AgentMessagesListResp

// AgentListCallablesReq is the request for agent.list_callables.
type AgentListCallablesReq = gen.AgentListCallablesReq

// AgentListCallablesResp is the response for agent.list_callables.
type AgentListCallablesResp = gen.AgentListCallablesResp

// AgentInvokeCallableReq is the request for agent.invoke_callable.
type AgentInvokeCallableReq = gen.AgentInvokeCallableReq

// AgentInvokeCallableResp is the response for agent.invoke_callable.
type AgentInvokeCallableResp = gen.AgentInvokeCallableResp

// AgentFrontendDebugReq is the request for agent.frontend_debug.
type AgentFrontendDebugReq = gen.AgentFrontendDebugReq

// AgentFrontendDebugResp is the response for agent.frontend_debug.
type AgentFrontendDebugResp = gen.AgentFrontendDebugResp

// AgentInspectActorReq is the request for agent.inspect_actor.
type AgentInspectActorReq = gen.AgentInspectActorReq

// AgentInspectActorResp is the response for agent.inspect_actor.
type AgentInspectActorResp = gen.AgentInspectActorResp

// AgentCaptureProfileReq is the request for agent.capture_profile.
type AgentCaptureProfileReq = gen.AgentCaptureProfileReq

// AgentCaptureProfileResp is the response for agent.capture_profile.
type AgentCaptureProfileResp = gen.AgentCaptureProfileResp

// AccountSnapshot is the response for workspace.account.
type AccountSnapshot = gen.AccountSnapshot

// ActorContextSnapshot describes the default actor context for an account.
type ActorContextSnapshot = gen.ActorContextSnapshot

// SessionSnapshot is the response for workspace.session.
type SessionSnapshot = gen.SessionSnapshot

// AccountPrefKeyAiShellTheme is the account-preferences key holding the
// ai-shell theme JSON ({"mode":"light"|"dark","fontSize":...}). It mirrors
// themeKey('ai-shell') in web/src/application/theme-persist.ts.
const AccountPrefKeyAiShellTheme = "theme.ai-shell.v1"

// AccountPreferencesSnapshot is the response for workspace.preferences.get.
type AccountPreferencesSnapshot = gen.AccountPreferencesSnapshot

// SaveAccountPreferencesCommand is the request for workspace.preferences.save.
type SaveAccountPreferencesCommand = gen.SaveAccountPreferencesCommand

// WorkspaceDockState stores durable dock sizes and split ratios.
type WorkspaceDockState = gen.WorkspaceDockState

// WorkspacePanelsState stores durable shell panel state.
type WorkspacePanelsState = gen.WorkspacePanelsState

// WorkspacePanelState is one persisted shell panel entry.
type WorkspacePanelState = gen.WorkspacePanelState

// XY is a persisted 2D point.
type XY = gen.XY

// WH is a persisted size.
type WH = gen.WH

// WorkspaceShellLayout stores durable shell tab/split layout JSON.
type WorkspaceShellLayout = gen.WorkspaceShellLayout

// WorkspaceAIShellState stores durable AI shell sidebar state.
type WorkspaceAIShellState = gen.WorkspaceAIShellState

// WorkspaceProjectBrowserState stores durable project browser preferences.
type WorkspaceProjectBrowserState = gen.WorkspaceProjectBrowserState

// WorkspaceExplorerState stores durable explorer panel state.
type WorkspaceExplorerState = gen.WorkspaceExplorerState

// WorkspaceUIModel is the typed actor-owned UI model for workbench state.
type WorkspaceUIModel = gen.WorkspaceUIModel

// SaveWorkspaceLayoutCommand updates the durable shell layout document.
type SaveWorkspaceLayoutCommand = gen.SaveWorkspaceLayoutCommand

// SaveWorkspacePanelsCommand updates the durable shell panel document.
type SaveWorkspacePanelsCommand = gen.SaveWorkspacePanelsCommand

// SaveWorkspaceDockCommand updates the durable shell dock document.
type SaveWorkspaceDockCommand = gen.SaveWorkspaceDockCommand

// SaveWorkspaceAIShellCommand updates the durable AI shell UI document.
type SaveWorkspaceAIShellCommand = gen.SaveWorkspaceAIShellCommand

// SaveWorkspaceProjectCardBrowserCommand updates the durable project browser UI document.
type SaveWorkspaceProjectCardBrowserCommand = gen.SaveWorkspaceProjectCardBrowserCommand

// SaveWorkspaceExplorerCommand updates the durable explorer panel UI document.
type SaveWorkspaceExplorerCommand = gen.SaveWorkspaceExplorerCommand

// Git types
type GitFileStatus = gen.GitFileStatus
type GitCommitInfo = gen.GitCommitInfo
type GitBranchInfo = gen.GitBranchInfo
type WorkspaceGitStatusReq = gen.WorkspaceGitStatusReq
type WorkspaceGitStatusResp = gen.WorkspaceGitStatusResp
type WorkspaceGitLogReq = gen.WorkspaceGitLogReq
type WorkspaceGitLogResp = gen.WorkspaceGitLogResp
type WorkspaceGitDiffReq = gen.WorkspaceGitDiffReq
type WorkspaceGitDiffResp = gen.WorkspaceGitDiffResp
type WorkspaceGitAddReq = gen.WorkspaceGitAddReq
type WorkspaceGitCommitReq = gen.WorkspaceGitCommitReq
type WorkspaceGitCommitResp = gen.WorkspaceGitCommitResp
type WorkspaceGitPushReq = gen.WorkspaceGitPushReq
type WorkspaceGitPullReq = gen.WorkspaceGitPullReq
type WorkspaceGitBranchReq = gen.WorkspaceGitBranchReq
type WorkspaceGitBranchResp = gen.WorkspaceGitBranchResp
type WorkspaceGitCheckoutReq = gen.WorkspaceGitCheckoutReq
type WorkspaceGitResetReq = gen.WorkspaceGitResetReq
type GitStashInfo = gen.GitStashInfo
type WorkspaceGitStashSaveReq = gen.WorkspaceGitStashSaveReq
type WorkspaceGitStashPopReq = gen.WorkspaceGitStashPopReq
type WorkspaceGitStashListReq = gen.WorkspaceGitStashListReq
type WorkspaceGitStashListResp = gen.WorkspaceGitStashListResp
type WorkspaceGitStashDropReq = gen.WorkspaceGitStashDropReq
type GitRemoteInfo = gen.GitRemoteInfo
type WorkspaceGitRemoteListReq = gen.WorkspaceGitRemoteListReq
type WorkspaceGitRemoteListResp = gen.WorkspaceGitRemoteListResp
type WorkspaceGitRemoteAddReq = gen.WorkspaceGitRemoteAddReq
type WorkspaceGitRemoteRemoveReq = gen.WorkspaceGitRemoteRemoveReq
type GitBlameLine = gen.GitBlameLine
type WorkspaceGitBlameReq = gen.WorkspaceGitBlameReq
type WorkspaceGitBlameResp = gen.WorkspaceGitBlameResp
type WorkspaceGitConfigGetReq = gen.WorkspaceGitConfigGetReq
type WorkspaceGitConfigGetResp = gen.WorkspaceGitConfigGetResp
type WorkspaceGitConfigSetReq = gen.WorkspaceGitConfigSetReq
type GitShowFile = gen.GitShowFile
type WorkspaceGitShowReq = gen.WorkspaceGitShowReq
type WorkspaceGitShowResp = gen.WorkspaceGitShowResp
type WorkspaceGitFetchReq = gen.WorkspaceGitFetchReq
type WorkspaceGitDiscardReq = gen.WorkspaceGitDiscardReq
type WorkspaceGitAmendReq = gen.WorkspaceGitAmendReq
type WorkspaceGitTagListReq = gen.WorkspaceGitTagListReq
type WorkspaceGitTagListResp = gen.WorkspaceGitTagListResp
type WorkspaceGitTagCreateReq = gen.WorkspaceGitTagCreateReq
type WorkspaceGitTagDeleteReq = gen.WorkspaceGitTagDeleteReq
type WorkspaceGitMergeReq = gen.WorkspaceGitMergeReq
type WorkspaceGitMergeResp = gen.WorkspaceGitMergeResp
type GitTagInfo = gen.GitTagInfo

// WorkspaceAgentSpawnSchedulerReq/Resp are the protocol for the internal
// scheduler ephemeral-agent spawn callable (workspace.agent_spawn_scheduler).
type WorkspaceAgentSpawnSchedulerReq = gen.WorkspaceAgentSpawnSchedulerReq
type WorkspaceAgentSpawnSchedulerResp = gen.WorkspaceAgentSpawnSchedulerResp

// AgentSchedulerBindReq/Resp + AgentSchedulerUnbindReq/Resp are the protocol
// for the agent-side scheduler binding lifecycle callables
// (agent.scheduler_bind / agent.scheduler_unbind, registered flat as
// scheduler_bind / scheduler_unbind).
type AgentSchedulerBindReq = gen.AgentSchedulerBindReq
type AgentSchedulerBindResp = gen.AgentSchedulerBindResp
type AgentSchedulerUnbindReq = gen.AgentSchedulerUnbindReq
type AgentSchedulerUnbindResp = gen.AgentSchedulerUnbindResp

// ShellExecReq is the request for shell.exec.
type ShellExecReq = gen.ShellExecReq

// ShellExecResp is the response for shell.exec.
type ShellExecResp = gen.ShellExecResp

// ShellBashReq is the request for shell.bash.
type ShellBashReq = gen.ShellBashReq

// ShellBashResp is the response for shell.bash.
type ShellBashResp = gen.ShellBashResp

// ShellChunk is the streaming chunk for shell.bash / shell.exec / project.shell_exec.
type ShellChunk = gen.ShellChunk

// ShellCandidate is a single probed shell family with an availability flag.
type ShellCandidate = gen.ShellCandidate

// ShellEnvProbeResp is the response for workspace.shell_env_probe.
type ShellEnvProbeResp = gen.ShellEnvProbeResp

// ShellPrefSaveReq is the request for workspace.shell_pref_save.
type ShellPrefSaveReq = gen.ShellPrefSaveReq

// ShellPrefSaveResp is the response for workspace.shell_pref_save.
type ShellPrefSaveResp = gen.ShellPrefSaveResp

// ShellSessionOpenReq is the request for shell.session_open.
type ShellSessionOpenReq = gen.ShellSessionOpenReq

// ShellSessionOpenResp is the response for shell.session_open.
type ShellSessionOpenResp = gen.ShellSessionOpenResp

// ShellSessionWriteReq is the request for shell.session_write.
type ShellSessionWriteReq = gen.ShellSessionWriteReq

// ShellSessionWriteResp is the response for shell.session_write.
type ShellSessionWriteResp = gen.ShellSessionWriteResp

// ShellSessionResizeReq is the request for shell.session_resize.
type ShellSessionResizeReq = gen.ShellSessionResizeReq

// ShellSessionResizeResp is the response for shell.session_resize.
type ShellSessionResizeResp = gen.ShellSessionResizeResp

// ShellSessionCloseReq is the request for shell.session_close.
type ShellSessionCloseReq = gen.ShellSessionCloseReq

// ShellSessionCloseResp is the response for shell.session_close.
type ShellSessionCloseResp = gen.ShellSessionCloseResp

// ShellSessionOutputEvent is one output chunk (stdout/stderr/exit) of an
// interactive shell session, published as the shell.session_output event.
type ShellSessionOutputEvent = gen.ShellSessionOutputEvent

// ShellSessionFetchReq is the request for shell.session_fetch.
type ShellSessionFetchReq = gen.ShellSessionFetchReq

// ShellSessionFetchResp is the response for shell.session_fetch.
type ShellSessionFetchResp = gen.ShellSessionFetchResp

// ComputerUseRegion is a screen sub-rectangle.
type ComputerUseRegion = gen.ComputerUseRegion

// ComputerUseTargetSpec selects a screen location for an interaction.
type ComputerUseTargetSpec = gen.ComputerUseTargetSpec

// ComputerUseScrollDelta is a 2D scroll amount.
type ComputerUseScrollDelta = gen.ComputerUseScrollDelta

// ComputerUseScreenshotReq is the request for computeruse.screenshot.
type ComputerUseScreenshotReq = gen.ComputerUseScreenshotReq

// ComputerUseScreenshotResp is the response for computeruse.screenshot.
type ComputerUseScreenshotResp = gen.ComputerUseScreenshotResp

// ComputerUseInteractReq is the request for computeruse.interact.
type ComputerUseInteractReq = gen.ComputerUseInteractReq

// ComputerUseInteractResp is the response for computeruse.interact.
type ComputerUseInteractResp = gen.ComputerUseInteractResp

// ComputerUseWindowInfo describes one visible window.
type ComputerUseWindowInfo = gen.ComputerUseWindowInfo

// ComputerUseWindowsResp is the response for computeruse.list_windows.
type ComputerUseWindowsResp = gen.ComputerUseWindowsResp

// ComputerUseDisplayInfo describes one connected display.
type ComputerUseDisplayInfo = gen.ComputerUseDisplayInfo

// ComputerUseDisplaysResp is the response for computeruse.list_displays.
type ComputerUseDisplaysResp = gen.ComputerUseDisplaysResp

// ComputerUseListDisplaysReq is the request for computeruse.list_displays.
type ComputerUseListDisplaysReq = gen.ComputerUseListDisplaysReq

// ComputerUseCursorPosResp is the response for computeruse.cursor_position.
type ComputerUseCursorPosResp = gen.ComputerUseCursorPosResp

// ComputerUseCursorPositionReq is the request for computeruse.cursor_position.
type ComputerUseCursorPositionReq = gen.ComputerUseCursorPositionReq

// ComputerUseWindowTarget selects what to capture for screenshot/stream.
type ComputerUseWindowTarget = gen.ComputerUseWindowTarget

// ComputerUseStreamReq is the request for computeruse.stream.
type ComputerUseStreamReq = gen.ComputerUseStreamReq

// ComputerUseStreamChunk is one chunk emitted on a stream.
type ComputerUseStreamChunk = gen.ComputerUseStreamChunk

// ComputerUseStreamTile is one changed rectangular region on a stream.
type ComputerUseStreamTile = gen.ComputerUseStreamTile

// ComputerUseClipboardGetReq is the request for computeruse.clipboard_get.
type ComputerUseClipboardGetReq = gen.ComputerUseClipboardGetReq

// ComputerUseClipboardGetResp is the response for computeruse.clipboard_get.
type ComputerUseClipboardGetResp = gen.ComputerUseClipboardGetResp

// ComputerUseClipboardSetReq is the request for computeruse.clipboard_set.
type ComputerUseClipboardSetReq = gen.ComputerUseClipboardSetReq

// ComputerUseClipboardSetResp is the response for computeruse.clipboard_set.
type ComputerUseClipboardSetResp = gen.ComputerUseClipboardSetResp

// ComputerUseProcessInfo describes one OS process.
type ComputerUseProcessInfo = gen.ComputerUseProcessInfo

// ComputerUseListProcessesReq filters the process list.
type ComputerUseListProcessesReq = gen.ComputerUseListProcessesReq

// ComputerUseProcessesResp is the response for computeruse.list_processes.
type ComputerUseProcessesResp = gen.ComputerUseProcessesResp

// ComputerUseListWindowsReq filters the window list.
type ComputerUseListWindowsReq = gen.ComputerUseListWindowsReq

// ComputerUseGetWindowInfoReq is the request for computeruse.get_window_info.
type ComputerUseGetWindowInfoReq = gen.ComputerUseGetWindowInfoReq

// ComputerUseWindowInfoDetailed is the response for computeruse.get_window_info.
type ComputerUseWindowInfoDetailed = gen.ComputerUseWindowInfoDetailed

// ComputerUseElementNode is one UI Automation element snapshot.
type ComputerUseElementNode = gen.ComputerUseElementNode

// ComputerUseListElementsReq is the request for computeruse.list_elements.
type ComputerUseListElementsReq = gen.ComputerUseListElementsReq

// ComputerUseListElementsResp is the response for computeruse.list_elements.
type ComputerUseListElementsResp = gen.ComputerUseListElementsResp

// ComputerUseOcrReq is the request for computeruse.ocr.
type ComputerUseOcrReq = gen.ComputerUseOcrReq

// ComputerUseOcrLine is one OCR-recognised line.
type ComputerUseOcrLine = gen.ComputerUseOcrLine

// ComputerUseOcrResp is the response for computeruse.ocr.
type ComputerUseOcrResp = gen.ComputerUseOcrResp

// ComputerUseSetupOcrReq is the request for computeruse.setup_ocr.
type ComputerUseSetupOcrReq = gen.ComputerUseSetupOcrReq

// ComputerUseSetupOcrResp is the response for computeruse.setup_ocr.
type ComputerUseSetupOcrResp = gen.ComputerUseSetupOcrResp

// ComputerUseCapability is one entry of the computeruse capability matrix.
type ComputerUseCapability = gen.ComputerUseCapability

// ComputerUseCapabilitiesReq is the request for computeruse.capabilities.
type ComputerUseCapabilitiesReq = gen.ComputerUseCapabilitiesReq

// ComputerUseCapabilitiesResp is the response for computeruse.capabilities.
type ComputerUseCapabilitiesResp = gen.ComputerUseCapabilitiesResp

// ComputerUseClipboardRichGetReq is the request for computeruse.clipboard_get_rich.
type ComputerUseClipboardRichGetReq = gen.ComputerUseClipboardRichGetReq

// ComputerUseClipboardRichGetResp is the response for computeruse.clipboard_get_rich.
type ComputerUseClipboardRichGetResp = gen.ComputerUseClipboardRichGetResp

// ComputerUseClipboardRichSetReq is the request for computeruse.clipboard_set_rich.
type ComputerUseClipboardRichSetReq = gen.ComputerUseClipboardRichSetReq

// ComputerUseClipboardRichSetResp is the response for computeruse.clipboard_set_rich.
type ComputerUseClipboardRichSetResp = gen.ComputerUseClipboardRichSetResp

// FileEntryListResp wraps a file entry list response.
type FileEntryListResp = gen.FileEntryListResp

// ProviderListResp wraps a provider list response.
type ProviderListResp = gen.ProviderListResp

// ModelListResp wraps a model list response.
type ModelListResp = gen.ModelListResp

// AggregatorDescriptorListResp wraps an aggregator descriptor list response.
type AggregatorDescriptorListResp = gen.AggregatorDescriptorListResp

// PromptListResp wraps a prompt list response.
type PromptListResp = gen.PromptListResp

// PromptFragmentListResp wraps a prompt fragment list response.
type PromptFragmentListResp = gen.PromptFragmentListResp

// PromptProfileListResp wraps a prompt profile list response.
type PromptProfileListResp = gen.PromptProfileListResp

// ProjectRefListResp wraps a project ref list response.
type ProjectRefListResp = gen.ProjectRefListResp

// AgentRefListResp wraps an agent ref list response.
type AgentRefListResp = gen.AgentRefListResp

// AICallableUnitView is the wire-safe view of an aggregator callable unit.
// AuthToken is intentionally omitted: aiaggregator.status is Public() and must
// never leak provider credentials.
type AICallableUnitView = gen.AICallableUnitView

// AIAggregatorStatusResp is the response for aiaggregator.status.
type AIAggregatorStatusResp = gen.AIAggregatorStatusResp

// DispatchActivity captures the in-flight dispatch state of a single callable
// unit (or a nested-aggregator ref entry) during an aggregator dispatch. It
// is transient and aggregator-local; it rides on AICallableUnitView.
type DispatchActivity = gen.DispatchActivity

// InspectRef identifies an inspectable entity by kind/id with optional scope.
type InspectRef = gen.InspectRef

// OpenTarget describes how to open another view from an inspector action/item.
type OpenTarget = gen.OpenTarget

// InspectRow is a single key-value row in an inspector kv section.
type InspectRow = gen.InspectRow

// InspectListItem is a single entry in an inspector list section.
type InspectListItem = gen.InspectListItem

// InspectCapability carries the (performance, cost, speed) tuple rendered by
// the capability-triangle inspector section.
type InspectCapability = gen.InspectCapability

// InspectContextSegment is a single span rendered by the context-bar inspector section.
type InspectContextSegment = gen.InspectContextSegment

// InspectSection is a polymorphic section discriminated by Type:
//   - "kv"                  : Rows
//   - "list"                : Items
//   - "markdown"            : Body
//   - "capability-triangle" : Capability
//   - "context-bar"         : Segments
type InspectSection = gen.InspectSection

// InspectStatus is the badge state rendered at the top of an inspector document.
type InspectStatus = gen.InspectStatus

// InspectAction is a clickable action button in an inspector document.
type InspectAction = gen.InspectAction

// InspectRelation is a related entity link rendered in an inspector document.
type InspectRelation = gen.InspectRelation

// InspectDocument is the full payload returned by inspect.document.
type InspectDocument = gen.InspectDocument

// InspectPage describes one custom inspector page declared by an actor.
type InspectPage = gen.InspectPage

// InspectPagesResp wraps the page list returned by agent.inspect.pages.
type InspectPagesResp = gen.InspectPagesResp

// InspectDocumentReq is the request payload for inspect.document.
type InspectDocumentReq = gen.InspectDocumentReq

// OracleCapabilityDiscoverReq is the request for oracle.capability.discover.
type OracleCapabilityDiscoverReq = gen.OracleCapabilityDiscoverReq

// CapabilityCandidate is a single capability candidate returned by oracle.
type CapabilityCandidate = gen.CapabilityCandidate

// OracleCapabilityDiscoverResp is the response for oracle.capability.discover.
type OracleCapabilityDiscoverResp = gen.OracleCapabilityDiscoverResp

// Diagnostic is a standardized error/warning collected by the oracle actor.
type Diagnostic = gen.Diagnostic

// OracleReportDiagnosticReq is the request for oracle.report_diagnostic.
type OracleReportDiagnosticReq = gen.OracleReportDiagnosticReq

// OracleListDiagnosticsReq is the request for oracle.list_diagnostics.
type OracleListDiagnosticsReq = gen.OracleListDiagnosticsReq

// OracleListDiagnosticsResp is the response for oracle.list_diagnostics.
type OracleListDiagnosticsResp = gen.OracleListDiagnosticsResp

// DiagnosticSummary is a lightweight summary of a diagnostic for list views.
type DiagnosticSummary = gen.DiagnosticSummary

// OracleGetDiagnosticReq is the request for oracle.get_diagnostic.
type OracleGetDiagnosticReq = gen.OracleGetDiagnosticReq

// OracleSearchServicesReq is the request for oracle.search_services.
type OracleSearchServicesReq = gen.OracleSearchServicesReq

// OracleSearchServicesResp is the response for oracle.search_services.
type OracleSearchServicesResp = gen.OracleSearchServicesResp

// OracleCapabilityExplainReq is the request for oracle.capability.explain.
type OracleCapabilityExplainReq = gen.OracleCapabilityExplainReq

// OracleCapabilityExplainResp is the response for oracle.capability.explain.
type OracleCapabilityExplainResp = gen.OracleCapabilityExplainResp

// FrpProxy is one frpc-managed local→remote port mapping.
type FrpProxy = gen.FrpProxy

// FrpWebProxy exposes the sporemind web UI through the tunnel.
type FrpWebProxy = gen.FrpWebProxy

// FrpInstanceConfig is the persisted record for one frpc connection.
type FrpInstanceConfig = gen.FrpInstanceConfig

// FrpProxyStatus is the live per-proxy state reported by the embedded frpc.
type FrpProxyStatus = gen.FrpProxyStatus

// FrpInstanceStatus is the runtime state of one frpc client.
type FrpInstanceStatus = gen.FrpInstanceStatus

// FrpInstance composes sanitized config + status + actor path for list/get responses.
type FrpInstance = gen.FrpInstance

// FrpManagerCreateReq creates a new frp instance (manager assigns the id).
type FrpManagerCreateReq = gen.FrpManagerCreateReq

// FrpManagerRemoveReq removes an instance by id.
type FrpManagerRemoveReq = gen.FrpManagerRemoveReq

// FrpManagerGetReq fetches one instance by id.
type FrpManagerGetReq = gen.FrpManagerGetReq

// FrpManagerListResp is the response for frpmanager.list.
type FrpManagerListResp = gen.FrpManagerListResp

// FrpInstanceConfigureReq pushes a new config to a child instance (internal).
type FrpInstanceConfigureReq = gen.FrpInstanceConfigureReq

// FrpManagerUpdateReq replaces a stored instance config.
type FrpManagerUpdateReq = gen.FrpManagerUpdateReq

// FrpManagerStartReq flips the persistent disabled flag off for one instance.
type FrpManagerStartReq = gen.FrpManagerStartReq

// FrpManagerStopReq flips the persistent disabled flag on for one instance.
type FrpManagerStopReq = gen.FrpManagerStopReq

// FrpManagerDetectReq probes a remote frps for version compatibility.
type FrpManagerDetectReq = gen.FrpManagerDetectReq

// FrpManagerDetectResp reports whether legacy mode is required.
type FrpManagerDetectResp = gen.FrpManagerDetectResp

// ── mcp ──
//
// MCP (Model Context Protocol) server manager types. Full McpServerConfig
// (with stdio env / http header VALUES) is write-only: it appears in
// add/update requests and persisted state, never in callable responses.
// All responses use the safe views that expose only key names.

// McpServerConfig is the persisted record for one MCP server connection.
type McpServerConfig = gen.McpServerConfig

// McpStdioTransport configures a stdio subprocess server.
type McpStdioTransport = gen.McpStdioTransport

// McpHttpTransport configures a Streamable HTTP server.
type McpHttpTransport = gen.McpHttpTransport

// McpServerStatus is the runtime state of one MCP client connection.
type McpServerStatus = gen.McpServerStatus

// McpEnvVarEntry is one env/header key with a configured flag — never a value.
type McpEnvVarEntry = gen.McpEnvVarEntry

// McpStdioTransportView is the read-safe stdio view (key names only).
type McpStdioTransportView = gen.McpStdioTransportView

// McpHttpTransportView is the read-safe http view (key names only).
type McpHttpTransportView = gen.McpHttpTransportView

// McpServerView composes sanitized config + status for list/add/update responses.
type McpServerView = gen.McpServerView

// McpListServersReq/Resp back mcp.list_servers.
type McpListServersReq = gen.McpListServersReq

// McpListServersResp is the response for mcp.list_servers.
type McpListServersResp = gen.McpListServersResp

// McpAddServerReq carries the full write-side config for mcp.add_server.
type McpAddServerReq = gen.McpAddServerReq

// McpAddServerResp is the response for mcp.add_server.
type McpAddServerResp = gen.McpAddServerResp

// McpUpdateServerReq carries the full write-side config for mcp.update_server.
type McpUpdateServerReq = gen.McpUpdateServerReq

// McpUpdateServerResp is the response for mcp.update_server.
type McpUpdateServerResp = gen.McpUpdateServerResp

// McpRemoveServerReq targets mcp.remove_server.
type McpRemoveServerReq = gen.McpRemoveServerReq

// McpRemoveServerResp is the response for mcp.remove_server.
type McpRemoveServerResp = gen.McpRemoveServerResp

// McpConnectReq targets mcp.connect.
type McpConnectReq = gen.McpConnectReq

// McpConnectResp is the response for mcp.connect.
type McpConnectResp = gen.McpConnectResp

// McpDisconnectReq targets mcp.disconnect.
type McpDisconnectReq = gen.McpDisconnectReq

// McpDisconnectResp is the response for mcp.disconnect.
type McpDisconnectResp = gen.McpDisconnectResp

// McpToolContent is one content block of a tools/call result.
type McpToolContent = gen.McpToolContent

// McpCallToolReq targets mcp.call_tool.
type McpCallToolReq = gen.McpCallToolReq

// McpCallToolResp is the response for mcp.call_tool.
type McpCallToolResp = gen.McpCallToolResp

// McpServerStatusEvent is the payload of the mcp.server_status event.
type McpServerStatusEvent = gen.McpServerStatusEvent

// McpToolView is one cached MCP tool (name/description/inputSchema JSON string).
type McpToolView = gen.McpToolView

// McpServerTools is one server's cached tool list (id/name/tools).
type McpServerTools = gen.McpServerTools

// McpDiscoverToolsReq targets mcp.discover_tools (agent tool injection).
type McpDiscoverToolsReq = gen.McpDiscoverToolsReq

// McpDiscoverToolsResp is the response for mcp.discover_tools.
type McpDiscoverToolsResp = gen.McpDiscoverToolsResp

// ── agent.message ──

type AgentMessage = gen.AgentMessage
type AgentMessageSendReq = gen.AgentMessageSendReq
type AgentMessageSendResp = gen.AgentMessageSendResp
type AgentMessageReceiveReq = gen.AgentMessageReceiveReq
type AgentMessageListResp = gen.AgentMessageListResp
type AgentMessageReadReq = gen.AgentMessageReadReq
type AgentMessageReadResp = gen.AgentMessageReadResp
type AgentMessageReadItem = gen.AgentMessageReadItem

// ── project.wiki ──

type WikiOpenCardsReq = gen.WikiOpenCardsReq
type WikiOpenCardsResp = gen.WikiOpenCardsResp
type WikiSaveOpenCardsReq = gen.WikiSaveOpenCardsReq
type WikiOpenCardReq = gen.WikiOpenCardReq
type WikiCloseCardReq = gen.WikiCloseCardReq
type WikiSetStarredReq = gen.WikiSetStarredReq
type WikiGetStarredReq = gen.WikiGetStarredReq
type WikiStarredResp = gen.WikiStarredResp

type MonoCardListItem = gen.MonoCardListItem
type WikiListCardsReq = gen.WikiListCardsReq
type WikiListCardsResp = gen.WikiListCardsResp
type WikiCardTreeNode = gen.WikiCardTreeNode
type WikiWorkflowFilter = gen.WikiWorkflowFilter
type WikiSearchCardContentReq = gen.WikiSearchCardContentReq
type WikiSearchCardContentResp = gen.WikiSearchCardContentResp
type WikiCardContentMatch = gen.WikiCardContentMatch
type WikiGetCardHierarchyReq = gen.WikiGetCardHierarchyReq
type WikiGetCardHierarchyResp = gen.WikiGetCardHierarchyResp
type WikiGetCardReq = gen.WikiGetCardReq
type WikiGetCardResp = gen.WikiGetCardResp
type WikiGetConceptTreeReq = gen.WikiGetConceptTreeReq
type WikiConceptNode = gen.WikiConceptNode
type WikiGetConceptTreeResp = gen.WikiGetConceptTreeResp
type WikiCreateCardReq = gen.WikiCreateCardReq
type WikiCreateCardResp = gen.WikiCreateCardResp
type WikiEditCardReq = gen.WikiEditCardReq
type WikiEditCardResp = gen.WikiEditCardResp
type WikiDeleteCardReq = gen.WikiDeleteCardReq
type WikiDeleteCardResp = gen.WikiDeleteCardResp
type CardValidationError = gen.CardValidationError
type WikiValidateCardReq = gen.WikiValidateCardReq
type WikiValidateCardResp = gen.WikiValidateCardResp
type WikiSetStatusReq = gen.WikiSetStatusReq
type WikiSetStatusResp = gen.WikiSetStatusResp
type WikiFrontierReq = gen.WikiFrontierReq
type WikiFrontierResp = gen.WikiFrontierResp
type FrontierTaskCard = gen.FrontierTaskCard
type WikiSetMapOwnerReq = gen.WikiSetMapOwnerReq
type WikiSetMapOwnerResp = gen.WikiSetMapOwnerResp
type WikiClaimTaskCardReq = gen.WikiClaimTaskCardReq
type WikiClaimTaskCardResp = gen.WikiClaimTaskCardResp
type WikiCreateMapReq = gen.WikiCreateMapReq
type WikiCreateMapResp = gen.WikiCreateMapResp
type WikiCreateTaskCardReq = gen.WikiCreateTaskCardReq
type WikiCreateTaskCardResp = gen.WikiCreateTaskCardResp
type WikiSetTaskDependenciesReq = gen.WikiSetTaskDependenciesReq
type WikiSetTaskDependenciesResp = gen.WikiSetTaskDependenciesResp
type WikiListDependenciesReq = gen.WikiListDependenciesReq
type WikiListDependenciesResp = gen.WikiListDependenciesResp
type TaskDepEdge = gen.TaskDepEdge
type TaskDataBinding = gen.TaskDataBinding
type WikiSetTaskOutputsReq = gen.WikiSetTaskOutputsReq
type WikiSetTaskOutputsResp = gen.WikiSetTaskOutputsResp
type WikiTemplateSaveReq = gen.WikiTemplateSaveReq
type WikiTemplateSaveResp = gen.WikiTemplateSaveResp
type WikiTemplateInstantiateReq = gen.WikiTemplateInstantiateReq
type WikiTemplateInstantiateResp = gen.WikiTemplateInstantiateResp
type WikiSetMapInputsReq = gen.WikiSetMapInputsReq
type WikiSetMapInputsResp = gen.WikiSetMapInputsResp
type WikiPromoteNodeOutputsReq = gen.WikiPromoteNodeOutputsReq
type WikiPromoteNodeOutputsResp = gen.WikiPromoteNodeOutputsResp
type WikiAutomationBindReq = gen.WikiAutomationBindReq
type WikiAutomationBindResp = gen.WikiAutomationBindResp
type WikiListTemplatesReq = gen.WikiListTemplatesReq
type WikiListTemplatesResp = gen.WikiListTemplatesResp
type WikiListTemplateRunsReq = gen.WikiListTemplateRunsReq
type WikiListTemplateRunsResp = gen.WikiListTemplateRunsResp
type TemplateRunRecord = gen.TemplateRunRecord
type TemplateNodeMapping = gen.TemplateNodeMapping
type WikiGetCardsBatchReq = gen.WikiGetCardsBatchReq
type WikiGetCardsBatchResp = gen.WikiGetCardsBatchResp
type WikiCardRaw = gen.WikiCardRaw

type CardRef = gen.CardRef
type MonoCard = gen.MonoCard
type CardPromptContribution = gen.CardPromptContribution
type CardToolContribution = gen.CardToolContribution
type ConceptCardRelation = gen.ConceptCardRelation
type SourceAnchor = gen.SourceAnchor
type ConceptEvidence = gen.ConceptEvidence
type ConceptCard = gen.ConceptCard

// ── component cards ──

type ComponentRef = gen.ComponentRef
type ComponentDependency = gen.ComponentDependency
type ComponentPromptContribution = gen.ComponentPromptContribution
type ComponentToolContribution = gen.ComponentToolContribution
type ComponentDescriptor = gen.ComponentDescriptor
type ComponentVisual = gen.ComponentVisual
type ComponentDiagnostic = gen.ComponentDiagnostic
type AgentComponentMount = gen.AgentComponentMount
type AgentComponentSnapshot = gen.AgentComponentSnapshot
type AgentComponentMountReq = gen.AgentComponentMountReq
type AgentComponentMountResp = gen.AgentComponentMountResp
type AgentComponentUnmountReq = gen.AgentComponentUnmountReq
type AgentComponentUnmountResp = gen.AgentComponentUnmountResp
type AgentComponentSetEnabledReq = gen.AgentComponentSetEnabledReq
type AgentComponentSetEnabledResp = gen.AgentComponentSetEnabledResp
type AgentComponentListReq = gen.AgentComponentListReq
type AgentComponentListResp = gen.AgentComponentListResp
type AgentComponentSnapshotReq = gen.AgentComponentSnapshotReq
type AgentComponentSnapshotResp = gen.AgentComponentSnapshotResp
type AgentModesUnloadAllReq = gen.AgentModesUnloadAllReq
type AgentModesUnloadAllResp = gen.AgentModesUnloadAllResp
type ProjectComponentListReq = gen.ProjectComponentListReq
type ProjectComponentListResp = gen.ProjectComponentListResp
type ProjectComponentGetReq = gen.ProjectComponentGetReq
type ProjectComponentGetResp = gen.ProjectComponentGetResp
type AgentSkillUseReq = gen.AgentSkillUseReq
type AgentSkillUseResp = gen.AgentSkillUseResp

// ── fork child ──

type ForkResult = gen.ForkResult
type ForkChildProgress = gen.ForkChildProgress

// ── task list ──

type Task = gen.Task
type AgentTaskCreateReq = gen.AgentTaskCreateReq
type AgentTaskCreateResp = gen.AgentTaskCreateResp
type AgentTaskUpdateReq = gen.AgentTaskUpdateReq
type AgentTaskUpdateResp = gen.AgentTaskUpdateResp
type AgentTaskListReq = gen.AgentTaskListReq
type AgentTaskListResp = gen.AgentTaskListResp
type AgentTaskDeleteReq = gen.AgentTaskDeleteReq
type AgentTaskDeleteResp = gen.AgentTaskDeleteResp
type AgentTaskCancelReq = gen.AgentTaskCancelReq
type AgentTaskCancelResp = gen.AgentTaskCancelResp

// ── memory ──

type MemoryNode = gen.MemoryNode
type MemoryEdge = gen.MemoryEdge
type MemorySaveReq = gen.MemorySaveReq
type MemorySaveResp = gen.MemorySaveResp
type MemoryRecallReq = gen.MemoryRecallReq
type MemoryRecallResp = gen.MemoryRecallResp
type MemoryWeaveApplyReq = gen.MemoryWeaveApplyReq
type MemorySnapshotReq = gen.MemorySnapshotReq
type MemorySnapshotResp = gen.MemorySnapshotResp

// ── dbmanager ──

type DbProfile = gen.DbProfile
type DbProfileView = gen.DbProfileView
type DbProfileSaveReq = gen.DbProfileSaveReq
type DbProfileSaveResp = gen.DbProfileSaveResp
type DbProfileListReq = gen.DbProfileListReq
type DbProfileListResp = gen.DbProfileListResp
type DbProfileGetReq = gen.DbProfileGetReq
type DbProfileGetResp = gen.DbProfileGetResp
type DbProfileRemoveReq = gen.DbProfileRemoveReq
type DbProfileRemoveResp = gen.DbProfileRemoveResp
type DbProfileResolveReq = gen.DbProfileResolveReq
type DbProfileResolveResp = gen.DbProfileResolveResp
type DbProfileLookupReq = gen.DbProfileLookupReq
type DbProfileLookupResp = gen.DbProfileLookupResp

// ── dbclient ──

type DbTreeNode = gen.DbTreeNode
type DbRows = gen.DbRows
type DbDialTestReq = gen.DbDialTestReq
type DbDialTestResp = gen.DbDialTestResp
type DbTreeReq = gen.DbTreeReq
type DbTreeResp = gen.DbTreeResp
type DbReadReq = gen.DbReadReq
type DbQueryReq = gen.DbQueryReq
type DbCloseReq = gen.DbCloseReq
type DbCloseResp = gen.DbCloseResp
type DbColumnDef = gen.DbColumnDef
type DbIndexDef = gen.DbIndexDef
type DbDescribeReq = gen.DbDescribeReq
type DbDescribeResp = gen.DbDescribeResp
type DbObjectEntry = gen.DbObjectEntry
type DbObjectListReq = gen.DbObjectListReq
type DbObjectListResp = gen.DbObjectListResp
type DbObjectReadReq = gen.DbObjectReadReq
type DbObjectReadResp = gen.DbObjectReadResp
type DbObjectWriteReq = gen.DbObjectWriteReq
type DbObjectWriteResp = gen.DbObjectWriteResp
type DbObjectDeleteReq = gen.DbObjectDeleteReq
type DbObjectDeleteResp = gen.DbObjectDeleteResp
type DbObjectMkdirReq = gen.DbObjectMkdirReq
type DbObjectMkdirResp = gen.DbObjectMkdirResp
type DbObjectStatReq = gen.DbObjectStatReq
type DbObjectStatResp = gen.DbObjectStatResp

// ── sshmanager ──

type SshHost = gen.SshHost
type SshHostView = gen.SshHostView
type SshHostListReq = gen.SshHostListReq
type SshHostListResp = gen.SshHostListResp
type SshHostCreateReq = gen.SshHostCreateReq
type SshHostCreateResp = gen.SshHostCreateResp
type SshHostUpdateReq = gen.SshHostUpdateReq
type SshHostUpdateResp = gen.SshHostUpdateResp
type SshHostRemoveReq = gen.SshHostRemoveReq
type SshHostRemoveResp = gen.SshHostRemoveResp
type SshSessionInfo = gen.SshSessionInfo
type SshSessionListReq = gen.SshSessionListReq
type SshSessionListResp = gen.SshSessionListResp
type SshShellOpenReq = gen.SshShellOpenReq
type SshShellOpenResp = gen.SshShellOpenResp
type SshShellCloseReq = gen.SshShellCloseReq
type SshShellCloseResp = gen.SshShellCloseResp
type SshShellInputReq = gen.SshShellInputReq
type SshShellInputResp = gen.SshShellInputResp
type SshShellResizeReq = gen.SshShellResizeReq
type SshShellResizeResp = gen.SshShellResizeResp
type SshShellStreamReq = gen.SshShellStreamReq
type SshShellStreamChunk = gen.SshShellStreamChunk
type SshFileEntry = gen.SshFileEntry
type SshFileListReq = gen.SshFileListReq
type SshFileListResp = gen.SshFileListResp
type SshFileReadReq = gen.SshFileReadReq
type SshFileReadResp = gen.SshFileReadResp
type SshFileWriteReq = gen.SshFileWriteReq
type SshFileWriteResp = gen.SshFileWriteResp
type SshFileMkdirReq = gen.SshFileMkdirReq
type SshFileMkdirResp = gen.SshFileMkdirResp
type SshFileDeleteReq = gen.SshFileDeleteReq
type SshFileDeleteResp = gen.SshFileDeleteResp
type SshFileRenameReq = gen.SshFileRenameReq
type SshFileRenameResp = gen.SshFileRenameResp
type SshFileTransferReq = gen.SshFileTransferReq
type SshFileDownloadReq = gen.SshFileDownloadReq
type SshFileDownloadResp = gen.SshFileDownloadResp
type SshProcInfo = gen.SshProcInfo
type SshDiskInfo = gen.SshDiskInfo
type SshNetInfo = gen.SshNetInfo
type SshStatus = gen.SshStatus
type SshStatusReq = gen.SshStatusReq
type SshStatusResp = gen.SshStatusResp
type SshStatusListReq = gen.SshStatusListReq
type SshStatusListResp = gen.SshStatusListResp
type SshCommandSnippet = gen.SshCommandSnippet
type SshCommandListReq = gen.SshCommandListReq
type SshCommandListResp = gen.SshCommandListResp
type SshCommandCreateReq = gen.SshCommandCreateReq
type SshCommandCreateResp = gen.SshCommandCreateResp
type SshCommandUpdateReq = gen.SshCommandUpdateReq
type SshCommandUpdateResp = gen.SshCommandUpdateResp
type SshCommandRemoveReq = gen.SshCommandRemoveReq
type SshCommandRemoveResp = gen.SshCommandRemoveResp
type SshHistoryListReq = gen.SshHistoryListReq
type SshHistoryListResp = gen.SshHistoryListResp
type SshArchiveExportReq = gen.SshArchiveExportReq
type SshArchiveImportReq = gen.SshArchiveImportReq

// One-shot remote command execution (sshmanager.part3, IDs 2920/2921).
type SshExecReq = gen.SshExecReq
type SshExecResp = gen.SshExecResp

// Host folders (sshmanager.part3, IDs 2922–2927).
type SshFolderCreateReq = gen.SshFolderCreateReq
type SshFolderCreateResp = gen.SshFolderCreateResp
type SshFolderRenameReq = gen.SshFolderRenameReq
type SshFolderRenameResp = gen.SshFolderRenameResp
type SshFolderReorderReq = gen.SshFolderReorderReq
type SshFolderReorderResp = gen.SshFolderReorderResp
type SshFolderRemoveReq = gen.SshFolderRemoveReq
type SshFolderRemoveResp = gen.SshFolderRemoveResp

// Binary remote file write (sshmanager.part4, IDs 3021/3022).
type SshFileWriteBase64Req = gen.SshFileWriteBase64Req
type SshFileWriteBase64Resp = gen.SshFileWriteBase64Resp

// Remote chmod (sshmanager.part4, IDs 3023/3024).
type SshFileChmodReq = gen.SshFileChmodReq
type SshFileChmodResp = gen.SshFileChmodResp
type SshManagerEvent = gen.SshManagerEvent

// Synchronous PTY command execution (sshmanager.part4, IDs 3026/3027).
type SshShellRunReq = gen.SshShellRunReq
type SshShellRunResp = gen.SshShellRunResp

// Composite file transfer (sshmanager.part5, IDs 3070–3073).
type SshDownloadReq = gen.SshDownloadReq
type SshDownloadResp = gen.SshDownloadResp
type SshUploadReq = gen.SshUploadReq
type SshUploadResp = gen.SshUploadResp

// Local port forwarding / ssh -L tunnels (sshmanager.part5, IDs 1584–1590).
type SshTunnelOpenReq = gen.SshTunnelOpenReq
type SshTunnelOpenResp = gen.SshTunnelOpenResp
type SshTunnelCloseReq = gen.SshTunnelCloseReq
type SshTunnelCloseResp = gen.SshTunnelCloseResp
type SshTunnelListReq = gen.SshTunnelListReq
type SshTunnelInfo = gen.SshTunnelInfo
type SshTunnelListResp = gen.SshTunnelListResp

// coordinator.guidance namespace — per-account user capability profile and
// guidance records (IDs 2800+). Payloads for coordinator.guidance.profile.
// {query,update,clear}, registered on the Coordinator agent.
type GuidanceCapabilityEntry = gen.GuidanceCapabilityEntry
type GuidanceCapabilityProfile = gen.GuidanceCapabilityProfile
type GuidanceRecord = gen.GuidanceRecord
type GuidanceProfileQueryReq = gen.GuidanceProfileQueryReq
type GuidanceProfileQueryResp = gen.GuidanceProfileQueryResp
type GuidanceProfileUpdateReq = gen.GuidanceProfileUpdateReq
type GuidanceProfileUpdateResp = gen.GuidanceProfileUpdateResp
type GuidanceProfileClearReq = gen.GuidanceProfileClearReq
type GuidanceProfileClearResp = gen.GuidanceProfileClearResp
type GuidanceProfileIncrementReq = gen.GuidanceProfileIncrementReq
type GuidanceProfileIncrementResp = gen.GuidanceProfileIncrementResp
