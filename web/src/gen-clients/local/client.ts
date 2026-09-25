// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function agentConfigure(client: GosporeClient, req: systemTypes.AgentConfigureReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.AgentConfigureReq, void>("agent_configure", req, { reqSchemaId: 357, ...opts });
}

export const agentConfigure_meta = {
  callable: "agent_configure",
  name: "agent_configure",
  reqSchemaId: 357,
} as const;

export async function agentPause(client: GosporeClient, req: systemTypes.AgentPauseReq, opts?: InvokeOptions): Promise<systemTypes.AgentPauseResp> {
  return client.invoke<systemTypes.AgentPauseReq, systemTypes.AgentPauseResp>("agent_pause", req, { reqSchemaId: 2816, resSchemaId: 2817, ...opts });
}

export const agentPause_meta = {
  callable: "agent_pause",
  name: "agent_pause",
  reqSchemaId: 2816,
  resSchemaId: 2817,
} as const;

export async function agentResume(client: GosporeClient, req: systemTypes.AgentResumeReq, opts?: InvokeOptions): Promise<systemTypes.AgentResumeResp> {
  return client.invoke<systemTypes.AgentResumeReq, systemTypes.AgentResumeResp>("agent_resume", req, { reqSchemaId: 2818, resSchemaId: 2819, ...opts });
}

export const agentResume_meta = {
  callable: "agent_resume",
  name: "agent_resume",
  reqSchemaId: 2818,
  resSchemaId: 2819,
} as const;

export async function agentStatus(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AgentStatusResp> {
  return client.invoke<void, systemTypes.AgentStatusResp>("agent_status", undefined, { resSchemaId: 128, ...opts });
}

export async function captureProfile(client: GosporeClient, req: systemTypes.AgentCaptureProfileReq, opts?: InvokeOptions): Promise<systemTypes.AgentCaptureProfileResp> {
  return client.invoke<systemTypes.AgentCaptureProfileReq, systemTypes.AgentCaptureProfileResp>("capture_profile", req, { reqSchemaId: 169, resSchemaId: 170, ...opts });
}

export const captureProfile_meta = {
  callable: "capture_profile",
  name: "capture_profile",
  reqSchemaId: 169,
  resSchemaId: 170,
} as const;

export async function chatCancelPending(client: GosporeClient, req: systemTypes.AgentChatCancelPendingReq, opts?: InvokeOptions): Promise<systemTypes.AgentChatCancelPendingResp> {
  return client.invoke<systemTypes.AgentChatCancelPendingReq, systemTypes.AgentChatCancelPendingResp>("chat_cancel_pending", req, { reqSchemaId: 228, resSchemaId: 229, ...opts });
}

export const chatCancelPending_meta = {
  callable: "chat_cancel_pending",
  name: "chat_cancel_pending",
  reqSchemaId: 228,
  resSchemaId: 229,
} as const;

export async function chatSubmit(client: GosporeClient, req: systemTypes.AgentChatSubmitReq, opts?: InvokeOptions): Promise<systemTypes.AgentChatSubmitResp> {
  return client.invoke<systemTypes.AgentChatSubmitReq, systemTypes.AgentChatSubmitResp>("chat_submit", req, { reqSchemaId: 226, resSchemaId: 227, ...opts });
}

export const chatSubmit_meta = {
  callable: "chat_submit",
  name: "chat_submit",
  reqSchemaId: 226,
  resSchemaId: 227,
} as const;

export async function compactionConfigure(client: GosporeClient, req: systemTypes.AgentCompactionConfigureReq, opts?: InvokeOptions): Promise<systemTypes.AgentCompactionConfigureResp> {
  return client.invoke<systemTypes.AgentCompactionConfigureReq, systemTypes.AgentCompactionConfigureResp>("compaction_configure", req, { reqSchemaId: 393, resSchemaId: 394, ...opts });
}

export const compactionConfigure_meta = {
  callable: "compaction_configure",
  name: "compaction_configure",
  reqSchemaId: 393,
  resSchemaId: 394,
} as const;

export async function compiledPrompt(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.PromptArtifact> {
  return client.invoke<void, systemTypes.PromptArtifact>("compiled_prompt", undefined, { resSchemaId: 1396, ...opts });
}

export async function completeMessage(client: GosporeClient, req: systemTypes.CompleteMessageReq, opts?: InvokeOptions): Promise<systemTypes.SummarizeResp> {
  return client.invoke<systemTypes.CompleteMessageReq, systemTypes.SummarizeResp>("complete_message", req, { reqSchemaId: 129, resSchemaId: 348, ...opts });
}

export const completeMessage_meta = {
  callable: "complete_message",
  name: "complete_message",
  reqSchemaId: 129,
  resSchemaId: 348,
} as const;

export async function componentList(client: GosporeClient, req: systemTypes.AgentComponentListReq, opts?: InvokeOptions): Promise<systemTypes.AgentComponentListResp> {
  return client.invoke<systemTypes.AgentComponentListReq, systemTypes.AgentComponentListResp>("component_list", req, { reqSchemaId: 2582, resSchemaId: 2583, ...opts });
}

export const componentList_meta = {
  callable: "component_list",
  name: "component_list",
  reqSchemaId: 2582,
  resSchemaId: 2583,
} as const;

export async function componentMount(client: GosporeClient, req: systemTypes.AgentComponentMountReq, opts?: InvokeOptions): Promise<systemTypes.AgentComponentMountResp> {
  return client.invoke<systemTypes.AgentComponentMountReq, systemTypes.AgentComponentMountResp>("component_mount", req, { reqSchemaId: 2576, resSchemaId: 2577, ...opts });
}

export const componentMount_meta = {
  callable: "component_mount",
  name: "component_mount",
  reqSchemaId: 2576,
  resSchemaId: 2577,
} as const;

export async function componentSetEnabled(client: GosporeClient, req: systemTypes.AgentComponentSetEnabledReq, opts?: InvokeOptions): Promise<systemTypes.AgentComponentSetEnabledResp> {
  return client.invoke<systemTypes.AgentComponentSetEnabledReq, systemTypes.AgentComponentSetEnabledResp>("component_set_enabled", req, { reqSchemaId: 2580, resSchemaId: 2581, ...opts });
}

export const componentSetEnabled_meta = {
  callable: "component_set_enabled",
  name: "component_set_enabled",
  reqSchemaId: 2580,
  resSchemaId: 2581,
} as const;

export async function componentSnapshot(client: GosporeClient, req: systemTypes.AgentComponentSnapshotReq, opts?: InvokeOptions): Promise<systemTypes.AgentComponentSnapshotResp> {
  return client.invoke<systemTypes.AgentComponentSnapshotReq, systemTypes.AgentComponentSnapshotResp>("component_snapshot", req, { reqSchemaId: 2584, resSchemaId: 2585, ...opts });
}

export const componentSnapshot_meta = {
  callable: "component_snapshot",
  name: "component_snapshot",
  reqSchemaId: 2584,
  resSchemaId: 2585,
} as const;

export async function componentUnmount(client: GosporeClient, req: systemTypes.AgentComponentUnmountReq, opts?: InvokeOptions): Promise<systemTypes.AgentComponentUnmountResp> {
  return client.invoke<systemTypes.AgentComponentUnmountReq, systemTypes.AgentComponentUnmountResp>("component_unmount", req, { reqSchemaId: 2578, resSchemaId: 2579, ...opts });
}

export const componentUnmount_meta = {
  callable: "component_unmount",
  name: "component_unmount",
  reqSchemaId: 2578,
  resSchemaId: 2579,
} as const;

export async function contextBudget(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.TurnContextBudgetPayload> {
  return client.invoke<void, systemTypes.TurnContextBudgetPayload>("context_budget", undefined, { resSchemaId: 343, ...opts });
}

export async function coordinatorGuidanceProfileClear(client: GosporeClient, req: systemTypes.GuidanceProfileClearReq, opts?: InvokeOptions): Promise<systemTypes.GuidanceProfileClearResp> {
  return client.invoke<systemTypes.GuidanceProfileClearReq, systemTypes.GuidanceProfileClearResp>("coordinator_guidance_profile_clear", req, { reqSchemaId: 4423, resSchemaId: 4424, ...opts });
}

export const coordinatorGuidanceProfileClear_meta = {
  callable: "coordinator_guidance_profile_clear",
  name: "coordinator_guidance_profile_clear",
  reqSchemaId: 4423,
  resSchemaId: 4424,
} as const;

export async function coordinatorGuidanceProfileIncrement(client: GosporeClient, req: systemTypes.GuidanceProfileIncrementReq, opts?: InvokeOptions): Promise<systemTypes.GuidanceProfileIncrementResp> {
  return client.invoke<systemTypes.GuidanceProfileIncrementReq, systemTypes.GuidanceProfileIncrementResp>("coordinator_guidance_profile_increment", req, { reqSchemaId: 4425, resSchemaId: 4426, ...opts });
}

export const coordinatorGuidanceProfileIncrement_meta = {
  callable: "coordinator_guidance_profile_increment",
  name: "coordinator_guidance_profile_increment",
  reqSchemaId: 4425,
  resSchemaId: 4426,
} as const;

export async function coordinatorGuidanceProfileQuery(client: GosporeClient, req: systemTypes.GuidanceProfileQueryReq, opts?: InvokeOptions): Promise<systemTypes.GuidanceProfileQueryResp> {
  return client.invoke<systemTypes.GuidanceProfileQueryReq, systemTypes.GuidanceProfileQueryResp>("coordinator_guidance_profile_query", req, { reqSchemaId: 4419, resSchemaId: 4420, ...opts });
}

export const coordinatorGuidanceProfileQuery_meta = {
  callable: "coordinator_guidance_profile_query",
  name: "coordinator_guidance_profile_query",
  reqSchemaId: 4419,
  resSchemaId: 4420,
} as const;

export async function coordinatorGuidanceProfileUpdate(client: GosporeClient, req: systemTypes.GuidanceProfileUpdateReq, opts?: InvokeOptions): Promise<systemTypes.GuidanceProfileUpdateResp> {
  return client.invoke<systemTypes.GuidanceProfileUpdateReq, systemTypes.GuidanceProfileUpdateResp>("coordinator_guidance_profile_update", req, { reqSchemaId: 4421, resSchemaId: 4422, ...opts });
}

export const coordinatorGuidanceProfileUpdate_meta = {
  callable: "coordinator_guidance_profile_update",
  name: "coordinator_guidance_profile_update",
  reqSchemaId: 4421,
  resSchemaId: 4422,
} as const;

export async function frontendDebug(client: GosporeClient, req: systemTypes.AgentFrontendDebugReq, opts?: InvokeOptions): Promise<systemTypes.AgentFrontendDebugResp> {
  return client.invoke<systemTypes.AgentFrontendDebugReq, systemTypes.AgentFrontendDebugResp>("frontend_debug", req, { reqSchemaId: 163, resSchemaId: 164, ...opts });
}

export const frontendDebug_meta = {
  callable: "frontend_debug",
  name: "frontend_debug",
  reqSchemaId: 163,
  resSchemaId: 164,
} as const;

export async function inspectActor(client: GosporeClient, req: systemTypes.AgentInspectActorReq, opts?: InvokeOptions): Promise<systemTypes.AgentInspectActorResp> {
  return client.invoke<systemTypes.AgentInspectActorReq, systemTypes.AgentInspectActorResp>("inspect_actor", req, { reqSchemaId: 165, resSchemaId: 166, ...opts });
}

export const inspectActor_meta = {
  callable: "inspect_actor",
  name: "inspect_actor",
  reqSchemaId: 165,
  resSchemaId: 166,
} as const;

export async function inspectPages(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.InspectPagesResp> {
  return client.invoke<void, systemTypes.InspectPagesResp>("inspect_pages", undefined, { resSchemaId: 860, ...opts });
}

export async function invokeCallable(client: GosporeClient, req: systemTypes.AgentInvokeCallableReq, opts?: InvokeOptions): Promise<systemTypes.AgentInvokeCallableResp> {
  return client.invoke<systemTypes.AgentInvokeCallableReq, systemTypes.AgentInvokeCallableResp>("invoke_callable", req, { reqSchemaId: 161, resSchemaId: 162, ...opts });
}

export const invokeCallable_meta = {
  callable: "invoke_callable",
  name: "invoke_callable",
  reqSchemaId: 161,
  resSchemaId: 162,
} as const;

export async function listCallables(client: GosporeClient, req: systemTypes.AgentListCallablesReq, opts?: InvokeOptions): Promise<systemTypes.AgentListCallablesResp> {
  return client.invoke<systemTypes.AgentListCallablesReq, systemTypes.AgentListCallablesResp>("list_callables", req, { reqSchemaId: 159, resSchemaId: 160, ...opts });
}

export const listCallables_meta = {
  callable: "list_callables",
  name: "list_callables",
  reqSchemaId: 159,
  resSchemaId: 160,
} as const;

export async function memoryDream(client: GosporeClient, opts?: InvokeOptions): Promise<Record<string, any>> {
  return client.invoke<void, Record<string, any>>("memory_dream", undefined, opts);
}

export async function memoryRecall(client: GosporeClient, req: systemTypes.MemoryRecallReq, opts?: InvokeOptions): Promise<systemTypes.MemoryRecallResp> {
  return client.invoke<systemTypes.MemoryRecallReq, systemTypes.MemoryRecallResp>("memory_recall", req, { reqSchemaId: 3730, resSchemaId: 3731, ...opts });
}

export const memoryRecall_meta = {
  callable: "memory_recall",
  name: "memory_recall",
  reqSchemaId: 3730,
  resSchemaId: 3731,
} as const;

export async function memorySave(client: GosporeClient, req: systemTypes.MemorySaveReq, opts?: InvokeOptions): Promise<systemTypes.MemorySaveResp> {
  return client.invoke<systemTypes.MemorySaveReq, systemTypes.MemorySaveResp>("memory_save", req, { reqSchemaId: 3728, resSchemaId: 3729, ...opts });
}

export const memorySave_meta = {
  callable: "memory_save",
  name: "memory_save",
  reqSchemaId: 3728,
  resSchemaId: 3729,
} as const;

export async function memorySnapshot(client: GosporeClient, req: systemTypes.MemorySnapshotReq, opts?: InvokeOptions): Promise<systemTypes.MemorySnapshotResp> {
  return client.invoke<systemTypes.MemorySnapshotReq, systemTypes.MemorySnapshotResp>("memory_snapshot", req, { reqSchemaId: 3733, resSchemaId: 3734, ...opts });
}

export const memorySnapshot_meta = {
  callable: "memory_snapshot",
  name: "memory_snapshot",
  reqSchemaId: 3733,
  resSchemaId: 3734,
} as const;

export async function messageClear(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("message_clear", undefined, opts);
}

export async function messageList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AgentMessageListResp> {
  return client.invoke<void, systemTypes.AgentMessageListResp>("message_list", undefined, { resSchemaId: 276, ...opts });
}

export async function messageRead(client: GosporeClient, req: systemTypes.AgentMessageReadReq, opts?: InvokeOptions): Promise<systemTypes.AgentMessageReadResp> {
  return client.invoke<systemTypes.AgentMessageReadReq, systemTypes.AgentMessageReadResp>("message_read", req, { reqSchemaId: 277, resSchemaId: 278, ...opts });
}

export const messageRead_meta = {
  callable: "message_read",
  name: "message_read",
  reqSchemaId: 277,
  resSchemaId: 278,
} as const;

export async function messagesList(client: GosporeClient, req: systemTypes.AgentMessagesListReq, opts?: InvokeOptions): Promise<systemTypes.AgentMessagesListResp> {
  return client.invoke<systemTypes.AgentMessagesListReq, systemTypes.AgentMessagesListResp>("messages_list", req, { reqSchemaId: 135, resSchemaId: 391, ...opts });
}

export const messagesList_meta = {
  callable: "messages_list",
  name: "messages_list",
  reqSchemaId: 135,
  resSchemaId: 391,
} as const;

export async function modesUnloadAll(client: GosporeClient, req: systemTypes.AgentModesUnloadAllReq, opts?: InvokeOptions): Promise<systemTypes.AgentModesUnloadAllResp> {
  return client.invoke<systemTypes.AgentModesUnloadAllReq, systemTypes.AgentModesUnloadAllResp>("modes_unload_all", req, { reqSchemaId: 2672, resSchemaId: 2673, ...opts });
}

export const modesUnloadAll_meta = {
  callable: "modes_unload_all",
  name: "modes_unload_all",
  reqSchemaId: 2672,
  resSchemaId: 2673,
} as const;

export async function openGlobalBrowser(client: GosporeClient, req: systemTypes.AgentOpenGlobalBrowserReq, opts?: InvokeOptions): Promise<systemTypes.AgentOpenGlobalBrowserResp> {
  return client.invoke<systemTypes.AgentOpenGlobalBrowserReq, systemTypes.AgentOpenGlobalBrowserResp>("open_global_browser", req, { reqSchemaId: 153, resSchemaId: 154, ...opts });
}

export const openGlobalBrowser_meta = {
  callable: "open_global_browser",
  name: "open_global_browser",
  reqSchemaId: 153,
  resSchemaId: 154,
} as const;

export async function openSshSession(client: GosporeClient, req: systemTypes.AgentOpenSshSessionReq, opts?: InvokeOptions): Promise<systemTypes.AgentOpenSshSessionResp> {
  return client.invoke<systemTypes.AgentOpenSshSessionReq, systemTypes.AgentOpenSshSessionResp>("open_ssh_session", req, { reqSchemaId: 157, resSchemaId: 158, ...opts });
}

export const openSshSession_meta = {
  callable: "open_ssh_session",
  name: "open_ssh_session",
  reqSchemaId: 157,
  resSchemaId: 158,
} as const;

export async function permissionModeSet(client: GosporeClient, req: systemTypes.setPermissionModeReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.setPermissionModeReq, void>("permission_mode_set", req, { reqSchemaId: 1783, ...opts });
}

export const permissionModeSet_meta = {
  callable: "permission_mode_set",
  name: "permission_mode_set",
  reqSchemaId: 1783,
} as const;

export async function promptArtifact(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.PromptArtifact> {
  return client.invoke<void, systemTypes.PromptArtifact>("prompt_artifact", undefined, { resSchemaId: 1396, ...opts });
}

export async function readSnapshot(client: GosporeClient, req: systemTypes.readSnapshotReq, opts?: InvokeOptions): Promise<systemTypes.readSnapshotResp> {
  return client.invoke<systemTypes.readSnapshotReq, systemTypes.readSnapshotResp>("read_snapshot", req, { reqSchemaId: 1733, resSchemaId: 1734, ...opts });
}

export const readSnapshot_meta = {
  callable: "read_snapshot",
  name: "read_snapshot",
  reqSchemaId: 1733,
  resSchemaId: 1734,
} as const;

export async function schedulerBind(client: GosporeClient, req: systemTypes.AgentSchedulerBindReq, opts?: InvokeOptions): Promise<systemTypes.AgentSchedulerBindResp> {
  return client.invoke<systemTypes.AgentSchedulerBindReq, systemTypes.AgentSchedulerBindResp>("scheduler_bind", req, { reqSchemaId: 2720, resSchemaId: 2721, ...opts });
}

export const schedulerBind_meta = {
  callable: "scheduler_bind",
  name: "scheduler_bind",
  reqSchemaId: 2720,
  resSchemaId: 2721,
} as const;

export async function schedulerUnbind(client: GosporeClient, req: systemTypes.AgentSchedulerUnbindReq, opts?: InvokeOptions): Promise<systemTypes.AgentSchedulerUnbindResp> {
  return client.invoke<systemTypes.AgentSchedulerUnbindReq, systemTypes.AgentSchedulerUnbindResp>("scheduler_unbind", req, { reqSchemaId: 2722, resSchemaId: 2723, ...opts });
}

export const schedulerUnbind_meta = {
  callable: "scheduler_unbind",
  name: "scheduler_unbind",
  reqSchemaId: 2722,
  resSchemaId: 2723,
} as const;

export async function sessionExportRange(client: GosporeClient, req: systemTypes.AgentSessionExportRangeReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionExportRangeResp> {
  return client.invoke<systemTypes.AgentSessionExportRangeReq, systemTypes.AgentSessionExportRangeResp>("session_export_range", req, { reqSchemaId: 1784, resSchemaId: 1785, ...opts });
}

export const sessionExportRange_meta = {
  callable: "session_export_range",
  name: "session_export_range",
  reqSchemaId: 1784,
  resSchemaId: 1785,
} as const;

export async function sessionFork(client: GosporeClient, req: systemTypes.AgentSessionForkReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionForkResp> {
  return client.invoke<systemTypes.AgentSessionForkReq, systemTypes.AgentSessionForkResp>("session_fork", req, { reqSchemaId: 384, resSchemaId: 385, ...opts });
}

export const sessionFork_meta = {
  callable: "session_fork",
  name: "session_fork",
  reqSchemaId: 384,
  resSchemaId: 385,
} as const;

export async function sessionGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AgentGetSessionResp> {
  return client.invoke<void, systemTypes.AgentGetSessionResp>("session_get", undefined, { resSchemaId: 1730, ...opts });
}

export async function sessionImport(client: GosporeClient, req: systemTypes.AgentSessionImportReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionImportResp> {
  return client.invoke<systemTypes.AgentSessionImportReq, systemTypes.AgentSessionImportResp>("session_import", req, { reqSchemaId: 386, resSchemaId: 387, ...opts });
}

export const sessionImport_meta = {
  callable: "session_import",
  name: "session_import",
  reqSchemaId: 386,
  resSchemaId: 387,
} as const;

export async function sessionImportTurns(client: GosporeClient, req: systemTypes.AgentSessionImportTurnsReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionImportTurnsResp> {
  return client.invoke<systemTypes.AgentSessionImportTurnsReq, systemTypes.AgentSessionImportTurnsResp>("session_import_turns", req, { reqSchemaId: 1806, resSchemaId: 1786, ...opts });
}

export const sessionImportTurns_meta = {
  callable: "session_import_turns",
  name: "session_import_turns",
  reqSchemaId: 1806,
  resSchemaId: 1786,
} as const;

export async function sessionStats(client: GosporeClient, req: systemTypes.AgentSessionStatsReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionStatsResp> {
  return client.invoke<systemTypes.AgentSessionStatsReq, systemTypes.AgentSessionStatsResp>("session_stats", req, { reqSchemaId: 3028, resSchemaId: 3029, ...opts });
}

export const sessionStats_meta = {
  callable: "session_stats",
  name: "session_stats",
  reqSchemaId: 3028,
  resSchemaId: 3029,
} as const;

export async function sessionSummary(client: GosporeClient, req: systemTypes.AgentSessionSummaryReq, opts?: InvokeOptions): Promise<systemTypes.AgentSessionSummaryResp> {
  return client.invoke<systemTypes.AgentSessionSummaryReq, systemTypes.AgentSessionSummaryResp>("session_summary", req, { reqSchemaId: 388, resSchemaId: 389, ...opts });
}

export const sessionSummary_meta = {
  callable: "session_summary",
  name: "session_summary",
  reqSchemaId: 388,
  resSchemaId: 389,
} as const;

export async function sessionUndo(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.Session> {
  return client.invoke<void, systemTypes.Session>("session_undo", undefined, { resSchemaId: 380, ...opts });
}

export async function showPageThumbnail(client: GosporeClient, req: systemTypes.AgentShowPageThumbnailReq, opts?: InvokeOptions): Promise<systemTypes.AgentShowPageThumbnailResp> {
  return client.invoke<systemTypes.AgentShowPageThumbnailReq, systemTypes.AgentShowPageThumbnailResp>("show_page_thumbnail", req, { reqSchemaId: 155, resSchemaId: 156, ...opts });
}

export const showPageThumbnail_meta = {
  callable: "show_page_thumbnail",
  name: "show_page_thumbnail",
  reqSchemaId: 155,
  resSchemaId: 156,
} as const;

export async function skillMount(client: GosporeClient, req: systemTypes.AgentSkillMountReq, opts?: InvokeOptions): Promise<systemTypes.AgentSkillMountResp> {
  return client.invoke<systemTypes.AgentSkillMountReq, systemTypes.AgentSkillMountResp>("skill_mount", req, { reqSchemaId: 1150, resSchemaId: 1151, ...opts });
}

export const skillMount_meta = {
  callable: "skill_mount",
  name: "skill_mount",
  reqSchemaId: 1150,
  resSchemaId: 1151,
} as const;

export async function skillUse(client: GosporeClient, req: systemTypes.AgentSkillUseReq, opts?: InvokeOptions): Promise<systemTypes.AgentSkillUseResp> {
  return client.invoke<systemTypes.AgentSkillUseReq, systemTypes.AgentSkillUseResp>("skill_use", req, { reqSchemaId: 2768, resSchemaId: 2769, ...opts });
}

export const skillUse_meta = {
  callable: "skill_use",
  name: "skill_use",
  reqSchemaId: 2768,
  resSchemaId: 2769,
} as const;

export async function storageConfigure(client: GosporeClient, req: systemTypes.AgentStorageConfigureReq, opts?: InvokeOptions): Promise<systemTypes.AgentStorageConfigureResp> {
  return client.invoke<systemTypes.AgentStorageConfigureReq, systemTypes.AgentStorageConfigureResp>("storage_configure", req, { reqSchemaId: 403, resSchemaId: 404, ...opts });
}

export const storageConfigure_meta = {
  callable: "storage_configure",
  name: "storage_configure",
  reqSchemaId: 403,
  resSchemaId: 404,
} as const;

export async function taskCancel(client: GosporeClient, req: systemTypes.AgentTaskCancelReq, opts?: InvokeOptions): Promise<systemTypes.AgentTaskCancelResp> {
  return client.invoke<systemTypes.AgentTaskCancelReq, systemTypes.AgentTaskCancelResp>("task_cancel", req, { reqSchemaId: 143, resSchemaId: 144, ...opts });
}

export const taskCancel_meta = {
  callable: "task_cancel",
  name: "task_cancel",
  reqSchemaId: 143,
  resSchemaId: 144,
} as const;

export async function taskCreate(client: GosporeClient, req: systemTypes.AgentTaskCreateReq, opts?: InvokeOptions): Promise<systemTypes.AgentTaskCreateResp> {
  return client.invoke<systemTypes.AgentTaskCreateReq, systemTypes.AgentTaskCreateResp>("task_create", req, { reqSchemaId: 140, resSchemaId: 141, ...opts });
}

export const taskCreate_meta = {
  callable: "task_create",
  name: "task_create",
  reqSchemaId: 140,
  resSchemaId: 141,
} as const;

export async function taskDelete(client: GosporeClient, req: systemTypes.AgentTaskDeleteReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.AgentTaskDeleteReq, void>("task_delete", req, { reqSchemaId: 148, ...opts });
}

export const taskDelete_meta = {
  callable: "task_delete",
  name: "task_delete",
  reqSchemaId: 148,
} as const;

export async function taskList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AgentTaskListResp> {
  return client.invoke<void, systemTypes.AgentTaskListResp>("task_list", undefined, { resSchemaId: 147, ...opts });
}

export async function taskUpdate(client: GosporeClient, req: systemTypes.AgentTaskUpdateReq, opts?: InvokeOptions): Promise<systemTypes.AgentTaskUpdateResp> {
  return client.invoke<systemTypes.AgentTaskUpdateReq, systemTypes.AgentTaskUpdateResp>("task_update", req, { reqSchemaId: 142, resSchemaId: 145, ...opts });
}

export const taskUpdate_meta = {
  callable: "task_update",
  name: "task_update",
  reqSchemaId: 142,
  resSchemaId: 145,
} as const;

export async function thinkingGetRegistry(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.GetThinkingRegistryResp> {
  return client.invoke<void, systemTypes.GetThinkingRegistryResp>("thinking_get_registry", undefined, { resSchemaId: 364, ...opts });
}

export async function thinkingSetLevel(client: GosporeClient, req: systemTypes.SetThinkingLevelReq, opts?: InvokeOptions): Promise<systemTypes.ThinkingLevel> {
  return client.invoke<systemTypes.SetThinkingLevelReq, systemTypes.ThinkingLevel>("thinking_set_level", req, { reqSchemaId: 363, resSchemaId: 361, ...opts });
}

export const thinkingSetLevel_meta = {
  callable: "thinking_set_level",
  name: "thinking_set_level",
  reqSchemaId: 363,
  resSchemaId: 361,
} as const;

export async function toolsRefreshNotify(client: GosporeClient, req: systemTypes.AgentToolsRefreshNotifyReq, opts?: InvokeOptions): Promise<systemTypes.AgentToolsRefreshNotifyResp> {
  return client.invoke<systemTypes.AgentToolsRefreshNotifyReq, systemTypes.AgentToolsRefreshNotifyResp>("tools_refresh_notify", req, { reqSchemaId: 1731, resSchemaId: 1732, ...opts });
}

export const toolsRefreshNotify_meta = {
  callable: "tools_refresh_notify",
  name: "tools_refresh_notify",
  reqSchemaId: 1731,
  resSchemaId: 1732,
} as const;

export async function turnAnswer(client: GosporeClient, req: systemTypes.TurnAnswerReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.TurnAnswerReq, void>("turn_answer", req, { reqSchemaId: 337, ...opts });
}

export const turnAnswer_meta = {
  callable: "turn_answer",
  name: "turn_answer",
  reqSchemaId: 337,
} as const;

export async function turnCancel(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("turn_cancel", undefined, opts);
}

export async function turnHistory(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.TurnHistoryResp> {
  return client.invoke<void, systemTypes.TurnHistoryResp>("turn_history", undefined, { resSchemaId: 399, ...opts });
}

export async function turnMiddleware(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.TurnMiddlewareResp> {
  return client.invoke<void, systemTypes.TurnMiddlewareResp>("turn_middleware", undefined, { resSchemaId: 400, ...opts });
}

export async function turnPause(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("turn_pause", undefined, opts);
}

export async function turnResume(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("turn_resume", undefined, opts);
}

export async function turnStatus(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.TurnStatus> {
  return client.invoke<void, systemTypes.TurnStatus>("turn_status", undefined, { resSchemaId: 344, ...opts });
}

export async function turnsList(client: GosporeClient, req: systemTypes.AgentTurnsListReq, opts?: InvokeOptions): Promise<systemTypes.AgentTurnsListResp> {
  return client.invoke<systemTypes.AgentTurnsListReq, systemTypes.AgentTurnsListResp>("turns_list", req, { reqSchemaId: 390, resSchemaId: 392, ...opts });
}

export const turnsList_meta = {
  callable: "turns_list",
  name: "turns_list",
  reqSchemaId: 390,
  resSchemaId: 392,
} as const;

export async function workflowPauseAll(client: GosporeClient, req: systemTypes.AgentWorkflowPauseAllReq, opts?: InvokeOptions): Promise<systemTypes.AgentWorkflowPauseAllResp> {
  return client.invoke<systemTypes.AgentWorkflowPauseAllReq, systemTypes.AgentWorkflowPauseAllResp>("workflow_pause_all", req, { reqSchemaId: 180, resSchemaId: 181, ...opts });
}

export const workflowPauseAll_meta = {
  callable: "workflow_pause_all",
  name: "workflow_pause_all",
  reqSchemaId: 180,
  resSchemaId: 181,
} as const;

export async function workflowStart(client: GosporeClient, req: systemTypes.AgentWorkflowStartReq, opts?: InvokeOptions): Promise<systemTypes.AgentWorkflowStartResp> {
  return client.invoke<systemTypes.AgentWorkflowStartReq, systemTypes.AgentWorkflowStartResp>("workflow_start", req, { reqSchemaId: 176, resSchemaId: 177, ...opts });
}

export const workflowStart_meta = {
  callable: "workflow_start",
  name: "workflow_start",
  reqSchemaId: 176,
  resSchemaId: 177,
} as const;

export async function workflowStop(client: GosporeClient, req: systemTypes.AgentWorkflowStopReq, opts?: InvokeOptions): Promise<systemTypes.AgentWorkflowStopResp> {
  return client.invoke<systemTypes.AgentWorkflowStopReq, systemTypes.AgentWorkflowStopResp>("workflow_stop", req, { reqSchemaId: 178, resSchemaId: 179, ...opts });
}

export const workflowStop_meta = {
  callable: "workflow_stop",
  name: "workflow_stop",
  reqSchemaId: 178,
  resSchemaId: 179,
} as const;

