// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function account(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AccountSnapshot> {
  return client.invoke<void, systemTypes.AccountSnapshot>("workspace.account", undefined, { resSchemaId: 1839, ...opts });
}

export async function addMount(client: GosporeClient, req: systemTypes.WorkspaceAddMountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectRef> {
  return client.invoke<systemTypes.WorkspaceAddMountReq, systemTypes.ProjectRef>("workspace.add_mount", req, { reqSchemaId: 1821, resSchemaId: 1809, ...opts });
}

export const addMount_meta = {
  callable: "workspace.add_mount",
  name: "add_mount",
  reqSchemaId: 1821,
  resSchemaId: 1809,
} as const;

export async function agentAccess(client: GosporeClient, req: systemTypes.WorkspaceAgentAccessReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentListState> {
  return client.invoke<systemTypes.WorkspaceAgentAccessReq, systemTypes.WorkspaceAgentListState>("workspace.agent_access", req, { reqSchemaId: 1901, resSchemaId: 1818, ...opts });
}

export const agentAccess_meta = {
  callable: "workspace.agent_access",
  name: "agent_access",
  reqSchemaId: 1901,
  resSchemaId: 1818,
} as const;

export async function agentAssign(client: GosporeClient, req: systemTypes.WorkspaceAgentAssignReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentAssignResp> {
  return client.invoke<systemTypes.WorkspaceAgentAssignReq, systemTypes.WorkspaceAgentAssignResp>("workspace.agent_assign", req, { reqSchemaId: 3794, resSchemaId: 3795, ...opts });
}

export const agentAssign_meta = {
  callable: "workspace.agent_assign",
  name: "agent_assign",
  reqSchemaId: 3794,
  resSchemaId: 3795,
} as const;

export async function agentListState(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentListState> {
  return client.invoke<void, systemTypes.WorkspaceAgentListState>("workspace.agent_list_state", undefined, { resSchemaId: 1818, ...opts });
}

export async function agentLoaded(client: GosporeClient, req: systemTypes.WorkspaceAgentLoadedReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentLoadedResp> {
  return client.invoke<systemTypes.WorkspaceAgentLoadedReq, systemTypes.WorkspaceAgentLoadedResp>("workspace.agent_loaded", req, { reqSchemaId: 5344, resSchemaId: 5345, ...opts });
}

export const agentLoaded_meta = {
  callable: "workspace.agent_loaded",
  name: "agent_loaded",
  reqSchemaId: 5344,
  resSchemaId: 5345,
} as const;

export async function agentPause(client: GosporeClient, req: systemTypes.AgentPauseReq, opts?: InvokeOptions): Promise<systemTypes.AgentPauseResp> {
  return client.invoke<systemTypes.AgentPauseReq, systemTypes.AgentPauseResp>("workspace.agent_pause", req, { reqSchemaId: 2736, resSchemaId: 2737, ...opts });
}

export const agentPause_meta = {
  callable: "workspace.agent_pause",
  name: "agent_pause",
  reqSchemaId: 2736,
  resSchemaId: 2737,
} as const;

export async function agentReadMessage(client: GosporeClient, req: systemTypes.AgentMessageReadReq, opts?: InvokeOptions): Promise<systemTypes.AgentMessageReadResp> {
  return client.invoke<systemTypes.AgentMessageReadReq, systemTypes.AgentMessageReadResp>("workspace.agent_read_message", req, { reqSchemaId: 277, resSchemaId: 278, ...opts });
}

export const agentReadMessage_meta = {
  callable: "workspace.agent_read_message",
  name: "agent_read_message",
  reqSchemaId: 277,
  resSchemaId: 278,
} as const;

export async function agentResume(client: GosporeClient, req: systemTypes.AgentResumeReq, opts?: InvokeOptions): Promise<systemTypes.AgentResumeResp> {
  return client.invoke<systemTypes.AgentResumeReq, systemTypes.AgentResumeResp>("workspace.agent_resume", req, { reqSchemaId: 2738, resSchemaId: 2739, ...opts });
}

export const agentResume_meta = {
  callable: "workspace.agent_resume",
  name: "agent_resume",
  reqSchemaId: 2738,
  resSchemaId: 2739,
} as const;

export async function agentReview(client: GosporeClient, req: systemTypes.WorkspaceAgentReviewReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentReviewResp> {
  return client.invoke<systemTypes.WorkspaceAgentReviewReq, systemTypes.WorkspaceAgentReviewResp>("workspace.agent_review", req, { reqSchemaId: 3796, resSchemaId: 3797, ...opts });
}

export const agentReview_meta = {
  callable: "workspace.agent_review",
  name: "agent_review",
  reqSchemaId: 3796,
  resSchemaId: 3797,
} as const;

export async function agentSendMessage(client: GosporeClient, req: systemTypes.AgentMessageSendReq, opts?: InvokeOptions): Promise<systemTypes.AgentMessageSendResp> {
  return client.invoke<systemTypes.AgentMessageSendReq, systemTypes.AgentMessageSendResp>("workspace.agent_send_message", req, { reqSchemaId: 272, resSchemaId: 273, ...opts });
}

export const agentSendMessage_meta = {
  callable: "workspace.agent_send_message",
  name: "agent_send_message",
  reqSchemaId: 272,
  resSchemaId: 273,
} as const;

export async function agentSpawnAssign(client: GosporeClient, req: systemTypes.WorkspaceAgentSpawnAssignReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentSpawnAssignResp> {
  return client.invoke<systemTypes.WorkspaceAgentSpawnAssignReq, systemTypes.WorkspaceAgentSpawnAssignResp>("workspace.agent_spawn_assign", req, { reqSchemaId: 3792, resSchemaId: 3793, ...opts });
}

export const agentSpawnAssign_meta = {
  callable: "workspace.agent_spawn_assign",
  name: "agent_spawn_assign",
  reqSchemaId: 3792,
  resSchemaId: 3793,
} as const;

export async function agentSpawnByType(client: GosporeClient, req: systemTypes.WorkspaceAgentSpawnByTypeReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentSpawnByTypeResp> {
  return client.invoke<systemTypes.WorkspaceAgentSpawnByTypeReq, systemTypes.WorkspaceAgentSpawnByTypeResp>("workspace.agent_spawn_by_type", req, { reqSchemaId: 3800, resSchemaId: 3801, ...opts });
}

export const agentSpawnByType_meta = {
  callable: "workspace.agent_spawn_by_type",
  name: "agent_spawn_by_type",
  reqSchemaId: 3800,
  resSchemaId: 3801,
} as const;

export async function agentSpawnScheduler(client: GosporeClient, req: systemTypes.WorkspaceAgentSpawnSchedulerReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentSpawnSchedulerResp> {
  return client.invoke<systemTypes.WorkspaceAgentSpawnSchedulerReq, systemTypes.WorkspaceAgentSpawnSchedulerResp>("workspace.agent_spawn_scheduler", req, { reqSchemaId: 1915, resSchemaId: 1916, ...opts });
}

export const agentSpawnScheduler_meta = {
  callable: "workspace.agent_spawn_scheduler",
  name: "agent_spawn_scheduler",
  reqSchemaId: 1915,
  resSchemaId: 1916,
} as const;

export async function agentSpawnSwarm(client: GosporeClient, req: systemTypes.WorkspaceAgentSpawnSwarmReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentSpawnSwarmResp> {
  return client.invoke<systemTypes.WorkspaceAgentSpawnSwarmReq, systemTypes.WorkspaceAgentSpawnSwarmResp>("workspace.agent_spawn_swarm", req, { reqSchemaId: 1930, resSchemaId: 1931, ...opts });
}

export const agentSpawnSwarm_meta = {
  callable: "workspace.agent_spawn_swarm",
  name: "agent_spawn_swarm",
  reqSchemaId: 1930,
  resSchemaId: 1931,
} as const;

export async function agentStatusUpdate(client: GosporeClient, req: systemTypes.WorkspaceAgentStatusUpdateReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentListState> {
  return client.invoke<systemTypes.WorkspaceAgentStatusUpdateReq, systemTypes.WorkspaceAgentListState>("workspace.agent_status_update", req, { reqSchemaId: 1820, resSchemaId: 1818, ...opts });
}

export const agentStatusUpdate_meta = {
  callable: "workspace.agent_status_update",
  name: "agent_status_update",
  reqSchemaId: 1820,
  resSchemaId: 1818,
} as const;

export async function agentTerminate(client: GosporeClient, req: systemTypes.WorkspaceAgentTerminateReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAgentTerminateResp> {
  return client.invoke<systemTypes.WorkspaceAgentTerminateReq, systemTypes.WorkspaceAgentTerminateResp>("workspace.agent_terminate", req, { reqSchemaId: 3798, resSchemaId: 3799, ...opts });
}

export const agentTerminate_meta = {
  callable: "workspace.agent_terminate",
  name: "agent_terminate",
  reqSchemaId: 3798,
  resSchemaId: 3799,
} as const;

export async function agentUnload(client: GosporeClient, req: systemTypes.AgentUnloadReq, opts?: InvokeOptions): Promise<systemTypes.AgentUnloadResp> {
  return client.invoke<systemTypes.AgentUnloadReq, systemTypes.AgentUnloadResp>("workspace.agent_unload", req, { reqSchemaId: 2740, resSchemaId: 2741, ...opts });
}

export const agentUnload_meta = {
  callable: "workspace.agent_unload",
  name: "agent_unload",
  reqSchemaId: 2740,
  resSchemaId: 2741,
} as const;

export async function agents(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AgentRefListResp> {
  return client.invoke<void, systemTypes.AgentRefListResp>("workspace.agents", undefined, { resSchemaId: 356, ...opts });
}

export async function aistatsActorId(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceAistatsActorIdResp> {
  return client.invoke<void, systemTypes.WorkspaceAistatsActorIdResp>("workspace.aistats_actor_id", undefined, { resSchemaId: 3505, ...opts });
}

export async function builtinModesList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceBuiltinModesListResp> {
  return client.invoke<void, systemTypes.WorkspaceBuiltinModesListResp>("workspace.builtin_modes_list", undefined, { resSchemaId: 3602, ...opts });
}

export async function cloneAgent(client: GosporeClient, req: systemTypes.WorkspaceCloneAgentReq, opts?: InvokeOptions): Promise<systemTypes.AgentRef> {
  return client.invoke<systemTypes.WorkspaceCloneAgentReq, systemTypes.AgentRef>("workspace.clone_agent", req, { reqSchemaId: 1827, resSchemaId: 354, ...opts });
}

export const cloneAgent_meta = {
  callable: "workspace.clone_agent",
  name: "clone_agent",
  reqSchemaId: 1827,
  resSchemaId: 354,
} as const;

export async function componentGet(client: GosporeClient, req: systemTypes.ProjectComponentGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectComponentGetResp> {
  return client.invoke<systemTypes.ProjectComponentGetReq, systemTypes.ProjectComponentGetResp>("workspace.component_get", req, { reqSchemaId: 2546, resSchemaId: 2547, ...opts });
}

export const componentGet_meta = {
  callable: "workspace.component_get",
  name: "component_get",
  reqSchemaId: 2546,
  resSchemaId: 2547,
} as const;

export async function create(client: GosporeClient, req: systemTypes.WorkspaceCreateReq, opts?: InvokeOptions): Promise<systemTypes.ProjectRef> {
  return client.invoke<systemTypes.WorkspaceCreateReq, systemTypes.ProjectRef>("workspace.create", req, { reqSchemaId: 1812, resSchemaId: 1809, ...opts });
}

export const create_meta = {
  callable: "workspace.create",
  name: "create",
  reqSchemaId: 1812,
  resSchemaId: 1809,
} as const;

export async function createAgent(client: GosporeClient, req: systemTypes.WorkspaceCreateAgentReq, opts?: InvokeOptions): Promise<systemTypes.AgentRef> {
  return client.invoke<systemTypes.WorkspaceCreateAgentReq, systemTypes.AgentRef>("workspace.create_agent", req, { reqSchemaId: 1824, resSchemaId: 354, ...opts });
}

export const createAgent_meta = {
  callable: "workspace.create_agent",
  name: "create_agent",
  reqSchemaId: 1824,
  resSchemaId: 354,
} as const;

export async function createAgentKind(client: GosporeClient, req: systemTypes.WorkspaceCreateAgentKindReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceCreateAgentKindResp> {
  return client.invoke<systemTypes.WorkspaceCreateAgentKindReq, systemTypes.WorkspaceCreateAgentKindResp>("workspace.create_agent_kind", req, { reqSchemaId: 1922, resSchemaId: 1923, ...opts });
}

export const createAgentKind_meta = {
  callable: "workspace.create_agent_kind",
  name: "create_agent_kind",
  reqSchemaId: 1922,
  resSchemaId: 1923,
} as const;

export async function debugCommandExec(client: GosporeClient, req: systemTypes.DebugCommandExecReq, opts?: InvokeOptions): Promise<systemTypes.DebugCommandExecResp> {
  return client.invoke<systemTypes.DebugCommandExecReq, systemTypes.DebugCommandExecResp>("workspace.debug_command_exec", req, { reqSchemaId: 5201, resSchemaId: 5202, ...opts });
}

export const debugCommandExec_meta = {
  callable: "workspace.debug_command_exec",
  name: "debug_command_exec",
  reqSchemaId: 5201,
  resSchemaId: 5202,
} as const;

export async function debugCommandsList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.DebugCommandsListResp> {
  return client.invoke<void, systemTypes.DebugCommandsListResp>("workspace.debug_commands_list", undefined, { resSchemaId: 5200, ...opts });
}

export async function deleteAgent(client: GosporeClient, req: systemTypes.WorkspaceDeleteAgentReq, opts?: InvokeOptions): Promise<systemTypes.AgentRef> {
  return client.invoke<systemTypes.WorkspaceDeleteAgentReq, systemTypes.AgentRef>("workspace.delete_agent", req, { reqSchemaId: 1826, resSchemaId: 354, ...opts });
}

export const deleteAgent_meta = {
  callable: "workspace.delete_agent",
  name: "delete_agent",
  reqSchemaId: 1826,
  resSchemaId: 354,
} as const;

export async function deleteAgentKind(client: GosporeClient, req: systemTypes.WorkspaceDeleteAgentKindReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceDeleteAgentKindResp> {
  return client.invoke<systemTypes.WorkspaceDeleteAgentKindReq, systemTypes.WorkspaceDeleteAgentKindResp>("workspace.delete_agent_kind", req, { reqSchemaId: 1920, resSchemaId: 1921, ...opts });
}

export const deleteAgentKind_meta = {
  callable: "workspace.delete_agent_kind",
  name: "delete_agent_kind",
  reqSchemaId: 1920,
  resSchemaId: 1921,
} as const;

export async function gateApprove(client: GosporeClient, req: systemTypes.WorkspaceGateApproveReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGateApproveResp> {
  return client.invoke<systemTypes.WorkspaceGateApproveReq, systemTypes.WorkspaceGateApproveResp>("workspace.gate_approve", req, { reqSchemaId: 3804, resSchemaId: 3805, ...opts });
}

export const gateApprove_meta = {
  callable: "workspace.gate_approve",
  name: "gate_approve",
  reqSchemaId: 3804,
  resSchemaId: 3805,
} as const;

export async function gateReject(client: GosporeClient, req: systemTypes.WorkspaceGateRejectReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGateRejectResp> {
  return client.invoke<systemTypes.WorkspaceGateRejectReq, systemTypes.WorkspaceGateRejectResp>("workspace.gate_reject", req, { reqSchemaId: 3806, resSchemaId: 3807, ...opts });
}

export const gateReject_meta = {
  callable: "workspace.gate_reject",
  name: "gate_reject",
  reqSchemaId: 3806,
  resSchemaId: 3807,
} as const;

export async function getAgentKindConfig(client: GosporeClient, req: systemTypes.WorkspaceGetAgentKindConfigReq, opts?: InvokeOptions): Promise<systemTypes.AgentKindConfig> {
  return client.invoke<systemTypes.WorkspaceGetAgentKindConfigReq, systemTypes.AgentKindConfig>("workspace.get_agent_kind_config", req, { reqSchemaId: 1835, resSchemaId: 1832, ...opts });
}

export const getAgentKindConfig_meta = {
  callable: "workspace.get_agent_kind_config",
  name: "get_agent_kind_config",
  reqSchemaId: 1835,
  resSchemaId: 1832,
} as const;

export async function gitAdd(client: GosporeClient, req: systemTypes.WorkspaceGitAddReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitAddReq, void>("workspace.git_add", req, { reqSchemaId: 1868, ...opts });
}

export const gitAdd_meta = {
  callable: "workspace.git_add",
  name: "git_add",
  reqSchemaId: 1868,
} as const;

export async function gitAmend(client: GosporeClient, req: systemTypes.WorkspaceGitAmendReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitAmendReq, void>("workspace.git_amend", req, { reqSchemaId: 1908, ...opts });
}

export const gitAmend_meta = {
  callable: "workspace.git_amend",
  name: "git_amend",
  reqSchemaId: 1908,
} as const;

export async function gitBlame(client: GosporeClient, req: systemTypes.WorkspaceGitBlameReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitBlameResp> {
  return client.invoke<systemTypes.WorkspaceGitBlameReq, systemTypes.WorkspaceGitBlameResp>("workspace.git_blame", req, { reqSchemaId: 1889, resSchemaId: 1890, ...opts });
}

export const gitBlame_meta = {
  callable: "workspace.git_blame",
  name: "git_blame",
  reqSchemaId: 1889,
  resSchemaId: 1890,
} as const;

export async function gitBranch(client: GosporeClient, req: systemTypes.WorkspaceGitBranchReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitBranchResp> {
  return client.invoke<systemTypes.WorkspaceGitBranchReq, systemTypes.WorkspaceGitBranchResp>("workspace.git_branch", req, { reqSchemaId: 1873, resSchemaId: 1874, ...opts });
}

export const gitBranch_meta = {
  callable: "workspace.git_branch",
  name: "git_branch",
  reqSchemaId: 1873,
  resSchemaId: 1874,
} as const;

export async function gitCheckout(client: GosporeClient, req: systemTypes.WorkspaceGitCheckoutReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitCheckoutReq, void>("workspace.git_checkout", req, { reqSchemaId: 1875, ...opts });
}

export const gitCheckout_meta = {
  callable: "workspace.git_checkout",
  name: "git_checkout",
  reqSchemaId: 1875,
} as const;

export async function gitCommit(client: GosporeClient, req: systemTypes.WorkspaceGitCommitReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitCommitResp> {
  return client.invoke<systemTypes.WorkspaceGitCommitReq, systemTypes.WorkspaceGitCommitResp>("workspace.git_commit", req, { reqSchemaId: 1869, resSchemaId: 1870, ...opts });
}

export const gitCommit_meta = {
  callable: "workspace.git_commit",
  name: "git_commit",
  reqSchemaId: 1869,
  resSchemaId: 1870,
} as const;

export async function gitConfigGet(client: GosporeClient, req: systemTypes.WorkspaceGitConfigGetReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitConfigGetResp> {
  return client.invoke<systemTypes.WorkspaceGitConfigGetReq, systemTypes.WorkspaceGitConfigGetResp>("workspace.git_config_get", req, { reqSchemaId: 1891, resSchemaId: 1892, ...opts });
}

export const gitConfigGet_meta = {
  callable: "workspace.git_config_get",
  name: "git_config_get",
  reqSchemaId: 1891,
  resSchemaId: 1892,
} as const;

export async function gitConfigSet(client: GosporeClient, req: systemTypes.WorkspaceGitConfigSetReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitConfigSetReq, void>("workspace.git_config_set", req, { reqSchemaId: 1893, ...opts });
}

export const gitConfigSet_meta = {
  callable: "workspace.git_config_set",
  name: "git_config_set",
  reqSchemaId: 1893,
} as const;

export async function gitDiff(client: GosporeClient, req: systemTypes.WorkspaceGitDiffReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitDiffResp> {
  return client.invoke<systemTypes.WorkspaceGitDiffReq, systemTypes.WorkspaceGitDiffResp>("workspace.git_diff", req, { reqSchemaId: 1866, resSchemaId: 1867, ...opts });
}

export const gitDiff_meta = {
  callable: "workspace.git_diff",
  name: "git_diff",
  reqSchemaId: 1866,
  resSchemaId: 1867,
} as const;

export async function gitDiscard(client: GosporeClient, req: systemTypes.WorkspaceGitDiscardReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitDiscardReq, void>("workspace.git_discard", req, { reqSchemaId: 1907, ...opts });
}

export const gitDiscard_meta = {
  callable: "workspace.git_discard",
  name: "git_discard",
  reqSchemaId: 1907,
} as const;

export async function gitFetch(client: GosporeClient, req: systemTypes.WorkspaceGitFetchReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitFetchReq, void>("workspace.git_fetch", req, { reqSchemaId: 1906, ...opts });
}

export const gitFetch_meta = {
  callable: "workspace.git_fetch",
  name: "git_fetch",
  reqSchemaId: 1906,
} as const;

export async function gitLog(client: GosporeClient, req: systemTypes.WorkspaceGitLogReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitLogResp> {
  return client.invoke<systemTypes.WorkspaceGitLogReq, systemTypes.WorkspaceGitLogResp>("workspace.git_log", req, { reqSchemaId: 1864, resSchemaId: 1865, ...opts });
}

export const gitLog_meta = {
  callable: "workspace.git_log",
  name: "git_log",
  reqSchemaId: 1864,
  resSchemaId: 1865,
} as const;

export async function gitMerge(client: GosporeClient, req: systemTypes.WorkspaceGitMergeReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitMergeResp> {
  return client.invoke<systemTypes.WorkspaceGitMergeReq, systemTypes.WorkspaceGitMergeResp>("workspace.git_merge", req, { reqSchemaId: 1913, resSchemaId: 1914, ...opts });
}

export const gitMerge_meta = {
  callable: "workspace.git_merge",
  name: "git_merge",
  reqSchemaId: 1913,
  resSchemaId: 1914,
} as const;

export async function gitPull(client: GosporeClient, req: systemTypes.WorkspaceGitPullReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitPullReq, void>("workspace.git_pull", req, { reqSchemaId: 1872, ...opts });
}

export const gitPull_meta = {
  callable: "workspace.git_pull",
  name: "git_pull",
  reqSchemaId: 1872,
} as const;

export async function gitPush(client: GosporeClient, req: systemTypes.WorkspaceGitPushReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitPushReq, void>("workspace.git_push", req, { reqSchemaId: 1871, ...opts });
}

export const gitPush_meta = {
  callable: "workspace.git_push",
  name: "git_push",
  reqSchemaId: 1871,
} as const;

export async function gitRemoteAdd(client: GosporeClient, req: systemTypes.WorkspaceGitRemoteAddReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitRemoteAddReq, void>("workspace.git_remote_add", req, { reqSchemaId: 1886, ...opts });
}

export const gitRemoteAdd_meta = {
  callable: "workspace.git_remote_add",
  name: "git_remote_add",
  reqSchemaId: 1886,
} as const;

export async function gitRemoteList(client: GosporeClient, req: systemTypes.WorkspaceGitRemoteListReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitRemoteListResp> {
  return client.invoke<systemTypes.WorkspaceGitRemoteListReq, systemTypes.WorkspaceGitRemoteListResp>("workspace.git_remote_list", req, { reqSchemaId: 1884, resSchemaId: 1885, ...opts });
}

export const gitRemoteList_meta = {
  callable: "workspace.git_remote_list",
  name: "git_remote_list",
  reqSchemaId: 1884,
  resSchemaId: 1885,
} as const;

export async function gitRemoteRemove(client: GosporeClient, req: systemTypes.WorkspaceGitRemoteRemoveReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitRemoteRemoveReq, void>("workspace.git_remote_remove", req, { reqSchemaId: 1887, ...opts });
}

export const gitRemoteRemove_meta = {
  callable: "workspace.git_remote_remove",
  name: "git_remote_remove",
  reqSchemaId: 1887,
} as const;

export async function gitReset(client: GosporeClient, req: systemTypes.WorkspaceGitResetReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitResetReq, void>("workspace.git_reset", req, { reqSchemaId: 1876, ...opts });
}

export const gitReset_meta = {
  callable: "workspace.git_reset",
  name: "git_reset",
  reqSchemaId: 1876,
} as const;

export async function gitShow(client: GosporeClient, req: systemTypes.WorkspaceGitShowReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitShowResp> {
  return client.invoke<systemTypes.WorkspaceGitShowReq, systemTypes.WorkspaceGitShowResp>("workspace.git_show", req, { reqSchemaId: 1904, resSchemaId: 1905, ...opts });
}

export const gitShow_meta = {
  callable: "workspace.git_show",
  name: "git_show",
  reqSchemaId: 1904,
  resSchemaId: 1905,
} as const;

export async function gitStashDrop(client: GosporeClient, req: systemTypes.WorkspaceGitStashDropReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitStashDropReq, void>("workspace.git_stash_drop", req, { reqSchemaId: 1882, ...opts });
}

export const gitStashDrop_meta = {
  callable: "workspace.git_stash_drop",
  name: "git_stash_drop",
  reqSchemaId: 1882,
} as const;

export async function gitStashList(client: GosporeClient, req: systemTypes.WorkspaceGitStashListReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitStashListResp> {
  return client.invoke<systemTypes.WorkspaceGitStashListReq, systemTypes.WorkspaceGitStashListResp>("workspace.git_stash_list", req, { reqSchemaId: 1880, resSchemaId: 1881, ...opts });
}

export const gitStashList_meta = {
  callable: "workspace.git_stash_list",
  name: "git_stash_list",
  reqSchemaId: 1880,
  resSchemaId: 1881,
} as const;

export async function gitStashPop(client: GosporeClient, req: systemTypes.WorkspaceGitStashPopReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitStashPopReq, void>("workspace.git_stash_pop", req, { reqSchemaId: 1879, ...opts });
}

export const gitStashPop_meta = {
  callable: "workspace.git_stash_pop",
  name: "git_stash_pop",
  reqSchemaId: 1879,
} as const;

export async function gitStashSave(client: GosporeClient, req: systemTypes.WorkspaceGitStashSaveReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitStashSaveReq, void>("workspace.git_stash_save", req, { reqSchemaId: 1878, ...opts });
}

export const gitStashSave_meta = {
  callable: "workspace.git_stash_save",
  name: "git_stash_save",
  reqSchemaId: 1878,
} as const;

export async function gitStatus(client: GosporeClient, req: systemTypes.WorkspaceGitStatusReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitStatusResp> {
  return client.invoke<systemTypes.WorkspaceGitStatusReq, systemTypes.WorkspaceGitStatusResp>("workspace.git_status", req, { reqSchemaId: 1862, resSchemaId: 1863, ...opts });
}

export const gitStatus_meta = {
  callable: "workspace.git_status",
  name: "git_status",
  reqSchemaId: 1862,
  resSchemaId: 1863,
} as const;

export async function gitTagCreate(client: GosporeClient, req: systemTypes.WorkspaceGitTagCreateReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitTagCreateReq, void>("workspace.git_tag_create", req, { reqSchemaId: 1911, ...opts });
}

export const gitTagCreate_meta = {
  callable: "workspace.git_tag_create",
  name: "git_tag_create",
  reqSchemaId: 1911,
} as const;

export async function gitTagDelete(client: GosporeClient, req: systemTypes.WorkspaceGitTagDeleteReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.WorkspaceGitTagDeleteReq, void>("workspace.git_tag_delete", req, { reqSchemaId: 1912, ...opts });
}

export const gitTagDelete_meta = {
  callable: "workspace.git_tag_delete",
  name: "git_tag_delete",
  reqSchemaId: 1912,
} as const;

export async function gitTagList(client: GosporeClient, req: systemTypes.WorkspaceGitTagListReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceGitTagListResp> {
  return client.invoke<systemTypes.WorkspaceGitTagListReq, systemTypes.WorkspaceGitTagListResp>("workspace.git_tag_list", req, { reqSchemaId: 1909, resSchemaId: 1910, ...opts });
}

export const gitTagList_meta = {
  callable: "workspace.git_tag_list",
  name: "git_tag_list",
  reqSchemaId: 1909,
  resSchemaId: 1910,
} as const;

export async function listAgentKindConfigs(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceListAgentKindConfigsResp> {
  return client.invoke<void, systemTypes.WorkspaceListAgentKindConfigsResp>("workspace.list_agent_kind_configs", undefined, { resSchemaId: 1836, ...opts });
}

export async function listAgentKinds(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceListAgentKindsResp> {
  return client.invoke<void, systemTypes.WorkspaceListAgentKindsResp>("workspace.list_agent_kinds", undefined, { resSchemaId: 1834, ...opts });
}

export async function listAgents(client: GosporeClient, req: systemTypes.WorkspaceListAgentsReq, opts?: InvokeOptions): Promise<systemTypes.AgentRefListResp> {
  return client.invoke<systemTypes.WorkspaceListAgentsReq, systemTypes.AgentRefListResp>("workspace.list_agents", req, { reqSchemaId: 1823, resSchemaId: 356, ...opts });
}

export const listAgents_meta = {
  callable: "workspace.list_agents",
  name: "list_agents",
  reqSchemaId: 1823,
  resSchemaId: 356,
} as const;

export async function listProject(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ProjectRefListResp> {
  return client.invoke<void, systemTypes.ProjectRefListResp>("workspace.list_project", undefined, { resSchemaId: 1837, ...opts });
}

export async function loadAgent(client: GosporeClient, req: systemTypes.WorkspaceLoadAgentReq, opts?: InvokeOptions): Promise<systemTypes.AgentRef> {
  return client.invoke<systemTypes.WorkspaceLoadAgentReq, systemTypes.AgentRef>("workspace.load_agent", req, { reqSchemaId: 1896, resSchemaId: 354, ...opts });
}

export const loadAgent_meta = {
  callable: "workspace.load_agent",
  name: "load_agent",
  reqSchemaId: 1896,
  resSchemaId: 354,
} as const;

export async function logsQuery(client: GosporeClient, req: systemTypes.WorkspaceLogsQueryReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceLogsQueryResp> {
  return client.invoke<systemTypes.WorkspaceLogsQueryReq, systemTypes.WorkspaceLogsQueryResp>("workspace.logs_query", req, { reqSchemaId: 1898, resSchemaId: 1899, ...opts });
}

export const logsQuery_meta = {
  callable: "workspace.logs_query",
  name: "logs_query",
  reqSchemaId: 1898,
  resSchemaId: 1899,
} as const;

export async function mount(client: GosporeClient, req: systemTypes.WorkspaceMountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectRef> {
  return client.invoke<systemTypes.WorkspaceMountReq, systemTypes.ProjectRef>("workspace.mount", req, { reqSchemaId: 1810, resSchemaId: 1809, ...opts });
}

export const mount_meta = {
  callable: "workspace.mount",
  name: "mount",
  reqSchemaId: 1810,
  resSchemaId: 1809,
} as const;

export async function preferencesGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AccountPreferencesSnapshot> {
  return client.invoke<void, systemTypes.AccountPreferencesSnapshot>("workspace.preferences_get", undefined, { resSchemaId: 1841, ...opts });
}

export async function preferencesSave(client: GosporeClient, req: systemTypes.SaveAccountPreferencesCommand, opts?: InvokeOptions): Promise<systemTypes.AccountPreferencesSnapshot> {
  return client.invoke<systemTypes.SaveAccountPreferencesCommand, systemTypes.AccountPreferencesSnapshot>("workspace.preferences_save", req, { reqSchemaId: 1842, resSchemaId: 1841, ...opts });
}

export const preferencesSave_meta = {
  callable: "workspace.preferences_save",
  name: "preferences_save",
  reqSchemaId: 1842,
  resSchemaId: 1841,
} as const;

export async function removeMount(client: GosporeClient, req: systemTypes.WorkspaceRemoveMountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectRef> {
  return client.invoke<systemTypes.WorkspaceRemoveMountReq, systemTypes.ProjectRef>("workspace.remove_mount", req, { reqSchemaId: 1822, resSchemaId: 1809, ...opts });
}

export const removeMount_meta = {
  callable: "workspace.remove_mount",
  name: "remove_mount",
  reqSchemaId: 1822,
  resSchemaId: 1809,
} as const;

export async function reportError(client: GosporeClient, req: systemTypes.FrontendErrorReport, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.FrontendErrorReport, void>("workspace.report_error", req, { reqSchemaId: 2401, ...opts });
}

export const reportError_meta = {
  callable: "workspace.report_error",
  name: "report_error",
  reqSchemaId: 2401,
} as const;

export async function saveAgentKindConfig(client: GosporeClient, req: systemTypes.WorkspaceSaveAgentKindConfigReq, opts?: InvokeOptions): Promise<systemTypes.AgentKindConfig> {
  return client.invoke<systemTypes.WorkspaceSaveAgentKindConfigReq, systemTypes.AgentKindConfig>("workspace.save_agent_kind_config", req, { reqSchemaId: 1833, resSchemaId: 1832, ...opts });
}

export const saveAgentKindConfig_meta = {
  callable: "workspace.save_agent_kind_config",
  name: "save_agent_kind_config",
  reqSchemaId: 1833,
  resSchemaId: 1832,
} as const;

export async function session(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.SessionSnapshot> {
  return client.invoke<void, systemTypes.SessionSnapshot>("workspace.session", undefined, { resSchemaId: 1840, ...opts });
}

export async function shellEnvProbe(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ShellEnvProbeResp> {
  return client.invoke<void, systemTypes.ShellEnvProbeResp>("workspace.shell_env_probe", undefined, { resSchemaId: 4897, ...opts });
}

export async function shellPrefSave(client: GosporeClient, req: systemTypes.ShellPrefSaveReq, opts?: InvokeOptions): Promise<systemTypes.ShellPrefSaveResp> {
  return client.invoke<systemTypes.ShellPrefSaveReq, systemTypes.ShellPrefSaveResp>("workspace.shell_pref_save", req, { reqSchemaId: 4898, resSchemaId: 4899, ...opts });
}

export const shellPrefSave_meta = {
  callable: "workspace.shell_pref_save",
  name: "shell_pref_save",
  reqSchemaId: 4898,
  resSchemaId: 4899,
} as const;

export async function slashCommandsList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceSlashCommandsListResp> {
  return client.invoke<void, systemTypes.WorkspaceSlashCommandsListResp>("workspace.slash_commands_list", undefined, { resSchemaId: 3554, ...opts });
}

export async function systemTree(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.SystemTreeResp> {
  return client.invoke<void, systemTypes.SystemTreeResp>("workspace.system_tree", undefined, { resSchemaId: 1716, ...opts });
}

export async function uiGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<void, systemTypes.WorkspaceUIModel>("workspace.ui_get", undefined, { resSchemaId: 1852, ...opts });
}

export async function uiSaveAiShell(client: GosporeClient, req: systemTypes.SaveWorkspaceAIShellCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspaceAIShellCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_ai_shell", req, { reqSchemaId: 1856, resSchemaId: 1852, ...opts });
}

export const uiSaveAiShell_meta = {
  callable: "workspace.ui_save_ai_shell",
  name: "ui_save_ai_shell",
  reqSchemaId: 1856,
  resSchemaId: 1852,
} as const;

export async function uiSaveDock(client: GosporeClient, req: systemTypes.SaveWorkspaceDockCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspaceDockCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_dock", req, { reqSchemaId: 1855, resSchemaId: 1852, ...opts });
}

export const uiSaveDock_meta = {
  callable: "workspace.ui_save_dock",
  name: "ui_save_dock",
  reqSchemaId: 1855,
  resSchemaId: 1852,
} as const;

export async function uiSaveExplorer(client: GosporeClient, req: systemTypes.SaveWorkspaceExplorerCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspaceExplorerCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_explorer", req, { reqSchemaId: 1858, resSchemaId: 1852, ...opts });
}

export const uiSaveExplorer_meta = {
  callable: "workspace.ui_save_explorer",
  name: "ui_save_explorer",
  reqSchemaId: 1858,
  resSchemaId: 1852,
} as const;

export async function uiSaveLayout(client: GosporeClient, req: systemTypes.SaveWorkspaceLayoutCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspaceLayoutCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_layout", req, { reqSchemaId: 1853, resSchemaId: 1852, ...opts });
}

export const uiSaveLayout_meta = {
  callable: "workspace.ui_save_layout",
  name: "ui_save_layout",
  reqSchemaId: 1853,
  resSchemaId: 1852,
} as const;

export async function uiSavePanels(client: GosporeClient, req: systemTypes.SaveWorkspacePanelsCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspacePanelsCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_panels", req, { reqSchemaId: 1854, resSchemaId: 1852, ...opts });
}

export const uiSavePanels_meta = {
  callable: "workspace.ui_save_panels",
  name: "ui_save_panels",
  reqSchemaId: 1854,
  resSchemaId: 1852,
} as const;

export async function uiSaveProjectCardBrowser(client: GosporeClient, req: systemTypes.SaveWorkspaceProjectCardBrowserCommand, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUIModel> {
  return client.invoke<systemTypes.SaveWorkspaceProjectCardBrowserCommand, systemTypes.WorkspaceUIModel>("workspace.ui_save_project_card_browser", req, { reqSchemaId: 1857, resSchemaId: 1852, ...opts });
}

export const uiSaveProjectCardBrowser_meta = {
  callable: "workspace.ui_save_project_card_browser",
  name: "ui_save_project_card_browser",
  reqSchemaId: 1857,
  resSchemaId: 1852,
} as const;

export async function unmount(client: GosporeClient, req: systemTypes.WorkspaceUnmountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectRef> {
  return client.invoke<systemTypes.WorkspaceUnmountReq, systemTypes.ProjectRef>("workspace.unmount", req, { reqSchemaId: 1811, resSchemaId: 1809, ...opts });
}

export const unmount_meta = {
  callable: "workspace.unmount",
  name: "unmount",
  reqSchemaId: 1811,
  resSchemaId: 1809,
} as const;

export async function updateAgent(client: GosporeClient, req: systemTypes.WorkspaceUpdateAgentReq, opts?: InvokeOptions): Promise<systemTypes.AgentRef> {
  return client.invoke<systemTypes.WorkspaceUpdateAgentReq, systemTypes.AgentRef>("workspace.update_agent", req, { reqSchemaId: 1825, resSchemaId: 354, ...opts });
}

export const updateAgent_meta = {
  callable: "workspace.update_agent",
  name: "update_agent",
  reqSchemaId: 1825,
  resSchemaId: 354,
} as const;

export async function updateProject(client: GosporeClient, req: systemTypes.WorkspaceUpdateProjectReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceUpdateProjectResp> {
  return client.invoke<systemTypes.WorkspaceUpdateProjectReq, systemTypes.WorkspaceUpdateProjectResp>("workspace.update_project", req, { reqSchemaId: 1894, resSchemaId: 1895, ...opts });
}

export const updateProject_meta = {
  callable: "workspace.update_project",
  name: "update_project",
  reqSchemaId: 1894,
  resSchemaId: 1895,
} as const;

export async function wikiCreateCard(client: GosporeClient, req: systemTypes.WikiCreateCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiCreateCardResp> {
  return client.invoke<systemTypes.WikiCreateCardReq, systemTypes.WikiCreateCardResp>("workspace.wiki_create_card", req, { reqSchemaId: 1256, resSchemaId: 1257, ...opts });
}

export const wikiCreateCard_meta = {
  callable: "workspace.wiki_create_card",
  name: "wiki_create_card",
  reqSchemaId: 1256,
  resSchemaId: 1257,
} as const;

export async function wikiDeleteCard(client: GosporeClient, req: systemTypes.WikiDeleteCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiDeleteCardResp> {
  return client.invoke<systemTypes.WikiDeleteCardReq, systemTypes.WikiDeleteCardResp>("workspace.wiki_delete_card", req, { reqSchemaId: 1260, resSchemaId: 1261, ...opts });
}

export const wikiDeleteCard_meta = {
  callable: "workspace.wiki_delete_card",
  name: "wiki_delete_card",
  reqSchemaId: 1260,
  resSchemaId: 1261,
} as const;

export async function wikiEditCard(client: GosporeClient, req: systemTypes.WikiEditCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiEditCardResp> {
  return client.invoke<systemTypes.WikiEditCardReq, systemTypes.WikiEditCardResp>("workspace.wiki_edit_card", req, { reqSchemaId: 1258, resSchemaId: 1259, ...opts });
}

export const wikiEditCard_meta = {
  callable: "workspace.wiki_edit_card",
  name: "wiki_edit_card",
  reqSchemaId: 1258,
  resSchemaId: 1259,
} as const;

export async function wikiGetCard(client: GosporeClient, req: systemTypes.WikiGetCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiGetCardResp> {
  return client.invoke<systemTypes.WikiGetCardReq, systemTypes.WikiGetCardResp>("workspace.wiki_get_card", req, { reqSchemaId: 1254, resSchemaId: 1255, ...opts });
}

export const wikiGetCard_meta = {
  callable: "workspace.wiki_get_card",
  name: "wiki_get_card",
  reqSchemaId: 1254,
  resSchemaId: 1255,
} as const;

export async function wikiListCards(client: GosporeClient, req: systemTypes.WikiListCardsReq, opts?: InvokeOptions): Promise<systemTypes.WikiListCardsResp> {
  return client.invoke<systemTypes.WikiListCardsReq, systemTypes.WikiListCardsResp>("workspace.wiki_list_cards", req, { reqSchemaId: 1252, resSchemaId: 1253, ...opts });
}

export const wikiListCards_meta = {
  callable: "workspace.wiki_list_cards",
  name: "wiki_list_cards",
  reqSchemaId: 1252,
  resSchemaId: 1253,
} as const;

export async function wikiListStarred(client: GosporeClient, req: systemTypes.WikiListStarredReq, opts?: InvokeOptions): Promise<systemTypes.WikiListStarredResp> {
  return client.invoke<systemTypes.WikiListStarredReq, systemTypes.WikiListStarredResp>("workspace.wiki_list_starred", req, { reqSchemaId: 1925, resSchemaId: 1926, ...opts });
}

export const wikiListStarred_meta = {
  callable: "workspace.wiki_list_starred",
  name: "wiki_list_starred",
  reqSchemaId: 1925,
  resSchemaId: 1926,
} as const;

export async function wikiSearchCardContent(client: GosporeClient, req: systemTypes.WikiSearchCardContentReq, opts?: InvokeOptions): Promise<systemTypes.WikiSearchCardContentResp> {
  return client.invoke<systemTypes.WikiSearchCardContentReq, systemTypes.WikiSearchCardContentResp>("workspace.wiki_search_card_content", req, { reqSchemaId: 2152, resSchemaId: 2154, ...opts });
}

export const wikiSearchCardContent_meta = {
  callable: "workspace.wiki_search_card_content",
  name: "wiki_search_card_content",
  reqSchemaId: 2152,
  resSchemaId: 2154,
} as const;

export async function workflowStart(client: GosporeClient, req: systemTypes.WorkspaceWorkflowStartReq, opts?: InvokeOptions): Promise<systemTypes.WorkspaceWorkflowStartResp> {
  return client.invoke<systemTypes.WorkspaceWorkflowStartReq, systemTypes.WorkspaceWorkflowStartResp>("workspace.workflow_start", req, { reqSchemaId: 3802, resSchemaId: 3803, ...opts });
}

export const workflowStart_meta = {
  callable: "workspace.workflow_start",
  name: "workflow_start",
  reqSchemaId: 3802,
  resSchemaId: 3803,
} as const;

export type AgentListStateHandler = (payload: systemTypes.WorkspaceAgentListStateEvent) => void;

export function OnAgentListState(client: GosporeClient, handler: AgentListStateHandler): () => void {
  return client.events.onService("workspace", "agent_list_state", (payload) => handler(payload as systemTypes.WorkspaceAgentListStateEvent));
}

export function OffAgentListState(cancel: () => void): void {
  cancel();
}

export type AgentsChangedHandler = (payload: systemTypes.WorkspaceAgentsChangedEvent) => void;

export function OnAgentsChanged(client: GosporeClient, handler: AgentsChangedHandler): () => void {
  return client.events.onService("workspace", "agents_changed", (payload) => handler(payload as systemTypes.WorkspaceAgentsChangedEvent));
}

export function OffAgentsChanged(cancel: () => void): void {
  cancel();
}

export type MountsHandler = (payload: systemTypes.WorkspaceMountsEvent) => void;

export function OnMounts(client: GosporeClient, handler: MountsHandler): () => void {
  return client.events.onService("workspace", "mounts", (payload) => handler(payload as systemTypes.WorkspaceMountsEvent));
}

export function OffMounts(cancel: () => void): void {
  cancel();
}

export type WorkspaceLogHandler = (payload: systemTypes.WorkspaceLogStreamEvent) => void;

export function OnWorkspaceLog(client: GosporeClient, handler: WorkspaceLogHandler): () => void {
  return client.events.onService("workspace", "workspace.log", (payload) => handler(payload as systemTypes.WorkspaceLogStreamEvent));
}

export function OffWorkspaceLog(cancel: () => void): void {
  cancel();
}

