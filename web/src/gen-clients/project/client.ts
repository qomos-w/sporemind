// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function archiveExport(client: GosporeClient, req: systemTypes.ArchiveExportReq, opts?: InvokeOptions): Promise<systemTypes.ArchiveExportResp> {
  return client.invoke<systemTypes.ArchiveExportReq, systemTypes.ArchiveExportResp>("project.archive_export", req, { reqSchemaId: 758, resSchemaId: 759, ...opts });
}

export const archiveExport_meta = {
  callable: "project.archive_export",
  name: "archive_export",
  reqSchemaId: 758,
  resSchemaId: 759,
} as const;

export async function archiveImport(client: GosporeClient, req: systemTypes.ArchiveImportReq, opts?: InvokeOptions): Promise<systemTypes.ArchiveImportResp> {
  return client.invoke<systemTypes.ArchiveImportReq, systemTypes.ArchiveImportResp>("project.archive_import", req, { reqSchemaId: 760, resSchemaId: 761, ...opts });
}

export const archiveImport_meta = {
  callable: "project.archive_import",
  name: "archive_import",
  reqSchemaId: 760,
  resSchemaId: 761,
} as const;

export async function cardList(client: GosporeClient, req: systemTypes.ProjectCardListReq, opts?: InvokeOptions): Promise<systemTypes.ProjectCardListResp> {
  return client.invoke<systemTypes.ProjectCardListReq, systemTypes.ProjectCardListResp>("project.card_list", req, { reqSchemaId: 3444, resSchemaId: 3445, ...opts });
}

export const cardList_meta = {
  callable: "project.card_list",
  name: "card_list",
  reqSchemaId: 3444,
  resSchemaId: 3445,
} as const;

export async function cardMount(client: GosporeClient, req: systemTypes.ProjectCardMountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectCardMountResp> {
  return client.invoke<systemTypes.ProjectCardMountReq, systemTypes.ProjectCardMountResp>("project.card_mount", req, { reqSchemaId: 3440, resSchemaId: 3441, ...opts });
}

export const cardMount_meta = {
  callable: "project.card_mount",
  name: "card_mount",
  reqSchemaId: 3440,
  resSchemaId: 3441,
} as const;

export async function cardUnmount(client: GosporeClient, req: systemTypes.ProjectCardUnmountReq, opts?: InvokeOptions): Promise<systemTypes.ProjectCardUnmountResp> {
  return client.invoke<systemTypes.ProjectCardUnmountReq, systemTypes.ProjectCardUnmountResp>("project.card_unmount", req, { reqSchemaId: 3442, resSchemaId: 3443, ...opts });
}

export const cardUnmount_meta = {
  callable: "project.card_unmount",
  name: "card_unmount",
  reqSchemaId: 3442,
  resSchemaId: 3443,
} as const;

export async function componentGet(client: GosporeClient, req: systemTypes.ProjectComponentGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectComponentGetResp> {
  return client.invoke<systemTypes.ProjectComponentGetReq, systemTypes.ProjectComponentGetResp>("project.component_get", req, { reqSchemaId: 2626, resSchemaId: 2627, ...opts });
}

export const componentGet_meta = {
  callable: "project.component_get",
  name: "component_get",
  reqSchemaId: 2626,
  resSchemaId: 2627,
} as const;

export async function componentList(client: GosporeClient, req: systemTypes.ProjectComponentListReq, opts?: InvokeOptions): Promise<systemTypes.ProjectComponentListResp> {
  return client.invoke<systemTypes.ProjectComponentListReq, systemTypes.ProjectComponentListResp>("project.component_list", req, { reqSchemaId: 2624, resSchemaId: 2625, ...opts });
}

export const componentList_meta = {
  callable: "project.component_list",
  name: "component_list",
  reqSchemaId: 2624,
  resSchemaId: 2625,
} as const;

export async function defaultBundlesGet(client: GosporeClient, req: systemTypes.ProjectDefaultBundlesGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectDefaultBundlesGetResp> {
  return client.invoke<systemTypes.ProjectDefaultBundlesGetReq, systemTypes.ProjectDefaultBundlesGetResp>("project.default_bundles_get", req, { reqSchemaId: 1089, resSchemaId: 1090, ...opts });
}

export const defaultBundlesGet_meta = {
  callable: "project.default_bundles_get",
  name: "default_bundles_get",
  reqSchemaId: 1089,
  resSchemaId: 1090,
} as const;

export async function defaultBundlesSet(client: GosporeClient, req: systemTypes.ProjectDefaultBundlesSetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectDefaultBundlesSetResp> {
  return client.invoke<systemTypes.ProjectDefaultBundlesSetReq, systemTypes.ProjectDefaultBundlesSetResp>("project.default_bundles_set", req, { reqSchemaId: 1091, resSchemaId: 1092, ...opts });
}

export const defaultBundlesSet_meta = {
  callable: "project.default_bundles_set",
  name: "default_bundles_set",
  reqSchemaId: 1091,
  resSchemaId: 1092,
} as const;

export async function edit(client: GosporeClient, req: systemTypes.FileSystemEditReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemEditResp> {
  return client.invoke<systemTypes.FileSystemEditReq, systemTypes.FileSystemEditResp>("project.edit", req, { reqSchemaId: 746, resSchemaId: 748, ...opts });
}

export const edit_meta = {
  callable: "project.edit",
  name: "edit",
  reqSchemaId: 746,
  resSchemaId: 748,
} as const;

export async function gitAdd(client: GosporeClient, req: systemTypes.ProjectGitAddReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitAddReq, void>("project.git_add", req, { reqSchemaId: 1025, ...opts });
}

export const gitAdd_meta = {
  callable: "project.git_add",
  name: "git_add",
  reqSchemaId: 1025,
} as const;

export async function gitBlame(client: GosporeClient, req: systemTypes.ProjectGitBlameReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitBlameResp> {
  return client.invoke<systemTypes.ProjectGitBlameReq, systemTypes.ProjectGitBlameResp>("project.git_blame", req, { reqSchemaId: 1044, resSchemaId: 1045, ...opts });
}

export const gitBlame_meta = {
  callable: "project.git_blame",
  name: "git_blame",
  reqSchemaId: 1044,
  resSchemaId: 1045,
} as const;

export async function gitBranch(client: GosporeClient, req: systemTypes.ProjectGitBranchReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitBranchResp> {
  return client.invoke<systemTypes.ProjectGitBranchReq, systemTypes.ProjectGitBranchResp>("project.git_branch", req, { reqSchemaId: 1030, resSchemaId: 1031, ...opts });
}

export const gitBranch_meta = {
  callable: "project.git_branch",
  name: "git_branch",
  reqSchemaId: 1030,
  resSchemaId: 1031,
} as const;

export async function gitCheckout(client: GosporeClient, req: systemTypes.ProjectGitCheckoutReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitCheckoutReq, void>("project.git_checkout", req, { reqSchemaId: 1032, ...opts });
}

export const gitCheckout_meta = {
  callable: "project.git_checkout",
  name: "git_checkout",
  reqSchemaId: 1032,
} as const;

export async function gitCommit(client: GosporeClient, req: systemTypes.ProjectGitCommitReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitCommitResp> {
  return client.invoke<systemTypes.ProjectGitCommitReq, systemTypes.ProjectGitCommitResp>("project.git_commit", req, { reqSchemaId: 1026, resSchemaId: 1027, ...opts });
}

export const gitCommit_meta = {
  callable: "project.git_commit",
  name: "git_commit",
  reqSchemaId: 1026,
  resSchemaId: 1027,
} as const;

export async function gitConfigGet(client: GosporeClient, req: systemTypes.ProjectGitConfigGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitConfigGetResp> {
  return client.invoke<systemTypes.ProjectGitConfigGetReq, systemTypes.ProjectGitConfigGetResp>("project.git_config_get", req, { reqSchemaId: 1046, resSchemaId: 1047, ...opts });
}

export const gitConfigGet_meta = {
  callable: "project.git_config_get",
  name: "git_config_get",
  reqSchemaId: 1046,
  resSchemaId: 1047,
} as const;

export async function gitConfigSet(client: GosporeClient, req: systemTypes.ProjectGitConfigSetReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitConfigSetReq, void>("project.git_config_set", req, { reqSchemaId: 1048, ...opts });
}

export const gitConfigSet_meta = {
  callable: "project.git_config_set",
  name: "git_config_set",
  reqSchemaId: 1048,
} as const;

export async function gitDiff(client: GosporeClient, req: systemTypes.ProjectGitDiffReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitDiffResp> {
  return client.invoke<systemTypes.ProjectGitDiffReq, systemTypes.ProjectGitDiffResp>("project.git_diff", req, { reqSchemaId: 1023, resSchemaId: 1024, ...opts });
}

export const gitDiff_meta = {
  callable: "project.git_diff",
  name: "git_diff",
  reqSchemaId: 1023,
  resSchemaId: 1024,
} as const;

export async function gitLog(client: GosporeClient, req: systemTypes.ProjectGitLogReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitLogResp> {
  return client.invoke<systemTypes.ProjectGitLogReq, systemTypes.ProjectGitLogResp>("project.git_log", req, { reqSchemaId: 1021, resSchemaId: 1022, ...opts });
}

export const gitLog_meta = {
  callable: "project.git_log",
  name: "git_log",
  reqSchemaId: 1021,
  resSchemaId: 1022,
} as const;

export async function gitPull(client: GosporeClient, req: systemTypes.ProjectGitPullReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitPullReq, void>("project.git_pull", req, { reqSchemaId: 1029, ...opts });
}

export const gitPull_meta = {
  callable: "project.git_pull",
  name: "git_pull",
  reqSchemaId: 1029,
} as const;

export async function gitPush(client: GosporeClient, req: systemTypes.ProjectGitPushReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitPushReq, void>("project.git_push", req, { reqSchemaId: 1028, ...opts });
}

export const gitPush_meta = {
  callable: "project.git_push",
  name: "git_push",
  reqSchemaId: 1028,
} as const;

export async function gitRemoteAdd(client: GosporeClient, req: systemTypes.ProjectGitRemoteAddReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitRemoteAddReq, void>("project.git_remote_add", req, { reqSchemaId: 1042, ...opts });
}

export const gitRemoteAdd_meta = {
  callable: "project.git_remote_add",
  name: "git_remote_add",
  reqSchemaId: 1042,
} as const;

export async function gitRemoteList(client: GosporeClient, req: systemTypes.ProjectGitRemoteListReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitRemoteListResp> {
  return client.invoke<systemTypes.ProjectGitRemoteListReq, systemTypes.ProjectGitRemoteListResp>("project.git_remote_list", req, { reqSchemaId: 1039, resSchemaId: 1041, ...opts });
}

export const gitRemoteList_meta = {
  callable: "project.git_remote_list",
  name: "git_remote_list",
  reqSchemaId: 1039,
  resSchemaId: 1041,
} as const;

export async function gitRemoteRemove(client: GosporeClient, req: systemTypes.ProjectGitRemoteRemoveReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitRemoteRemoveReq, void>("project.git_remote_remove", req, { reqSchemaId: 1043, ...opts });
}

export const gitRemoteRemove_meta = {
  callable: "project.git_remote_remove",
  name: "git_remote_remove",
  reqSchemaId: 1043,
} as const;

export async function gitReset(client: GosporeClient, req: systemTypes.ProjectGitResetReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitResetReq, void>("project.git_reset", req, { reqSchemaId: 1033, ...opts });
}

export const gitReset_meta = {
  callable: "project.git_reset",
  name: "git_reset",
  reqSchemaId: 1033,
} as const;

export async function gitStashDrop(client: GosporeClient, req: systemTypes.ProjectGitStashDropReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitStashDropReq, void>("project.git_stash_drop", req, { reqSchemaId: 1038, ...opts });
}

export const gitStashDrop_meta = {
  callable: "project.git_stash_drop",
  name: "git_stash_drop",
  reqSchemaId: 1038,
} as const;

export async function gitStashList(client: GosporeClient, req: systemTypes.ProjectGitStashListReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitStashListResp> {
  return client.invoke<systemTypes.ProjectGitStashListReq, systemTypes.ProjectGitStashListResp>("project.git_stash_list", req, { reqSchemaId: 1036, resSchemaId: 1037, ...opts });
}

export const gitStashList_meta = {
  callable: "project.git_stash_list",
  name: "git_stash_list",
  reqSchemaId: 1036,
  resSchemaId: 1037,
} as const;

export async function gitStashPop(client: GosporeClient, req: systemTypes.ProjectGitStashPopReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitStashPopReq, void>("project.git_stash_pop", req, { reqSchemaId: 1035, ...opts });
}

export const gitStashPop_meta = {
  callable: "project.git_stash_pop",
  name: "git_stash_pop",
  reqSchemaId: 1035,
} as const;

export async function gitStashSave(client: GosporeClient, req: systemTypes.ProjectGitStashSaveReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectGitStashSaveReq, void>("project.git_stash_save", req, { reqSchemaId: 1034, ...opts });
}

export const gitStashSave_meta = {
  callable: "project.git_stash_save",
  name: "git_stash_save",
  reqSchemaId: 1034,
} as const;

export async function gitStatus(client: GosporeClient, req: systemTypes.ProjectGitStatusReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGitStatusResp> {
  return client.invoke<systemTypes.ProjectGitStatusReq, systemTypes.ProjectGitStatusResp>("project.git_status", req, { reqSchemaId: 1012, resSchemaId: 1013, ...opts });
}

export const gitStatus_meta = {
  callable: "project.git_status",
  name: "git_status",
  reqSchemaId: 1012,
  resSchemaId: 1013,
} as const;

export async function glob(client: GosporeClient, req: systemTypes.FileSystemGlobReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemGlobResp> {
  return client.invoke<systemTypes.FileSystemGlobReq, systemTypes.FileSystemGlobResp>("project.glob", req, { reqSchemaId: 749, resSchemaId: 750, ...opts });
}

export const glob_meta = {
  callable: "project.glob",
  name: "glob",
  reqSchemaId: 749,
  resSchemaId: 750,
} as const;

export async function graphConceptGet(client: GosporeClient, req: systemTypes.ProjectGraphConceptGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGraphConceptGetResp> {
  return client.invoke<systemTypes.ProjectGraphConceptGetReq, systemTypes.ProjectGraphConceptGetResp>("project.graph_concept_get", req, { reqSchemaId: 1226, resSchemaId: 1227, ...opts });
}

export const graphConceptGet_meta = {
  callable: "project.graph_concept_get",
  name: "graph_concept_get",
  reqSchemaId: 1226,
  resSchemaId: 1227,
} as const;

export async function graphGet(client: GosporeClient, req: systemTypes.ProjectGraphGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGraphEnvelopeResp> {
  return client.invoke<systemTypes.ProjectGraphGetReq, systemTypes.ProjectGraphEnvelopeResp>("project.graph_get", req, { reqSchemaId: 1223, resSchemaId: 1225, ...opts });
}

export const graphGet_meta = {
  callable: "project.graph_get",
  name: "graph_get",
  reqSchemaId: 1223,
  resSchemaId: 1225,
} as const;

export async function graphSave(client: GosporeClient, req: systemTypes.ProjectGraphSaveReq, opts?: InvokeOptions): Promise<systemTypes.ProjectGraphEnvelopeResp> {
  return client.invoke<systemTypes.ProjectGraphSaveReq, systemTypes.ProjectGraphEnvelopeResp>("project.graph_save", req, { reqSchemaId: 1224, resSchemaId: 1225, ...opts });
}

export const graphSave_meta = {
  callable: "project.graph_save",
  name: "graph_save",
  reqSchemaId: 1224,
  resSchemaId: 1225,
} as const;

export async function grep(client: GosporeClient, req: systemTypes.FileSystemGrepReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemGrepResp> {
  return client.invoke<systemTypes.FileSystemGrepReq, systemTypes.FileSystemGrepResp>("project.grep", req, { reqSchemaId: 751, resSchemaId: 754, ...opts });
}

export const grep_meta = {
  callable: "project.grep",
  name: "grep",
  reqSchemaId: 751,
  resSchemaId: 754,
} as const;

export async function info(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ProjectInfoResp> {
  return client.invoke<void, systemTypes.ProjectInfoResp>("project.info", undefined, { resSchemaId: 1011, ...opts });
}

export async function list(client: GosporeClient, req: systemTypes.FileSystemListReq, opts?: InvokeOptions): Promise<string> {
  return client.invoke<systemTypes.FileSystemListReq, string>("project.list", req, { reqSchemaId: 738, ...opts });
}

export const list_meta = {
  callable: "project.list",
  name: "list",
  reqSchemaId: 738,
} as const;

export async function noGitModeGet(client: GosporeClient, req: systemTypes.ProjectNoGitModeGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectNoGitModeGetResp> {
  return client.invoke<systemTypes.ProjectNoGitModeGetReq, systemTypes.ProjectNoGitModeGetResp>("project.no_git_mode_get", req, { reqSchemaId: 1085, resSchemaId: 1086, ...opts });
}

export const noGitModeGet_meta = {
  callable: "project.no_git_mode_get",
  name: "no_git_mode_get",
  reqSchemaId: 1085,
  resSchemaId: 1086,
} as const;

export async function noGitModeSet(client: GosporeClient, req: systemTypes.ProjectNoGitModeSetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectNoGitModeSetResp> {
  return client.invoke<systemTypes.ProjectNoGitModeSetReq, systemTypes.ProjectNoGitModeSetResp>("project.no_git_mode_set", req, { reqSchemaId: 1087, resSchemaId: 1088, ...opts });
}

export const noGitModeSet_meta = {
  callable: "project.no_git_mode_set",
  name: "no_git_mode_set",
  reqSchemaId: 1087,
  resSchemaId: 1088,
} as const;

export async function read(client: GosporeClient, req: systemTypes.FileSystemReadReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadResp> {
  return client.invoke<systemTypes.FileSystemReadReq, systemTypes.FileSystemReadResp>("project.read", req, { reqSchemaId: 739, resSchemaId: 740, ...opts });
}

export const read_meta = {
  callable: "project.read",
  name: "read",
  reqSchemaId: 739,
  resSchemaId: 740,
} as const;

export async function readBase64(client: GosporeClient, req: systemTypes.FileSystemReadBase64Req, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadBase64Resp> {
  return client.invoke<systemTypes.FileSystemReadBase64Req, systemTypes.FileSystemReadBase64Resp>("project.read_base64", req, { reqSchemaId: 741, resSchemaId: 762, ...opts });
}

export const readBase64_meta = {
  callable: "project.read_base64",
  name: "read_base64",
  reqSchemaId: 741,
  resSchemaId: 762,
} as const;

export async function readChunk(client: GosporeClient, req: systemTypes.FileSystemReadChunkReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemReadChunkResp> {
  return client.invoke<systemTypes.FileSystemReadChunkReq, systemTypes.FileSystemReadChunkResp>("project.read_chunk", req, { reqSchemaId: 742, resSchemaId: 743, ...opts });
}

export const readChunk_meta = {
  callable: "project.read_chunk",
  name: "read_chunk",
  reqSchemaId: 742,
  resSchemaId: 743,
} as const;

export async function reviewChangeset(client: GosporeClient, req: systemTypes.ProjectReviewChangesetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectReviewChangesetSummaryResp> {
  return client.invoke<systemTypes.ProjectReviewChangesetReq, systemTypes.ProjectReviewChangesetSummaryResp>("project.review_changeset", req, { reqSchemaId: 1152, resSchemaId: 1153, ...opts });
}

export const reviewChangeset_meta = {
  callable: "project.review_changeset",
  name: "review_changeset",
  reqSchemaId: 1152,
  resSchemaId: 1153,
} as const;

export async function reviewFileContent(client: GosporeClient, req: systemTypes.ProjectReviewFileContentReq, opts?: InvokeOptions): Promise<systemTypes.ProjectReviewFileContentResp> {
  return client.invoke<systemTypes.ProjectReviewFileContentReq, systemTypes.ProjectReviewFileContentResp>("project.review_file_content", req, { reqSchemaId: 1159, resSchemaId: 1160, ...opts });
}

export const reviewFileContent_meta = {
  callable: "project.review_file_content",
  name: "review_file_content",
  reqSchemaId: 1159,
  resSchemaId: 1160,
} as const;

export async function rm(client: GosporeClient, req: systemTypes.FileSystemRmReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemRmResp> {
  return client.invoke<systemTypes.FileSystemRmReq, systemTypes.FileSystemRmResp>("project.rm", req, { reqSchemaId: 755, resSchemaId: 756, ...opts });
}

export const rm_meta = {
  callable: "project.rm",
  name: "rm",
  reqSchemaId: 755,
  resSchemaId: 756,
} as const;

export async function setProtectedFiles(client: GosporeClient, req: systemTypes.SetProtectedFilesReq, opts?: InvokeOptions): Promise<systemTypes.SetProtectedFilesResp> {
  return client.invoke<systemTypes.SetProtectedFilesReq, systemTypes.SetProtectedFilesResp>("project.set_protected_files", req, { reqSchemaId: 4992, resSchemaId: 4993, ...opts });
}

export const setProtectedFiles_meta = {
  callable: "project.set_protected_files",
  name: "set_protected_files",
  reqSchemaId: 4992,
  resSchemaId: 4993,
} as const;

export async function *shellExec(client: GosporeClient, req: systemTypes.ShellExecReq, opts?: InvokeOptions): AsyncIterable<systemTypes.ShellChunk> {
  yield* client.subscribe<systemTypes.ShellChunk>("project.shell_exec", req, { reqSchemaId: 1552, chunkSchemaId: 1556, ...opts });
}

export async function spawnAgent(client: GosporeClient, req: systemTypes.ProjectSpawnAgentReq, opts?: InvokeOptions): Promise<systemTypes.ProjectSpawnAgentResp> {
  return client.invoke<systemTypes.ProjectSpawnAgentReq, systemTypes.ProjectSpawnAgentResp>("project.spawn_agent", req, { reqSchemaId: 1892, resSchemaId: 1893, ...opts });
}

export const spawnAgent_meta = {
  callable: "project.spawn_agent",
  name: "spawn_agent",
  reqSchemaId: 1892,
  resSchemaId: 1893,
} as const;

export async function syncRoots(client: GosporeClient, req: systemTypes.ProjectSyncRootsReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectSyncRootsReq, void>("project.sync_roots", req, { reqSchemaId: 1015, ...opts });
}

export const syncRoots_meta = {
  callable: "project.sync_roots",
  name: "sync_roots",
  reqSchemaId: 1015,
} as const;

export async function taskValidateOutputs(client: GosporeClient, req: systemTypes.ProjectTaskValidateOutputsReq, opts?: InvokeOptions): Promise<systemTypes.ProjectTaskValidateOutputsResp> {
  return client.invoke<systemTypes.ProjectTaskValidateOutputsReq, systemTypes.ProjectTaskValidateOutputsResp>("project.task_validate_outputs", req, { reqSchemaId: 1162, resSchemaId: 1163, ...opts });
}

export const taskValidateOutputs_meta = {
  callable: "project.task_validate_outputs",
  name: "task_validate_outputs",
  reqSchemaId: 1162,
  resSchemaId: 1163,
} as const;

export async function watchFile(client: GosporeClient, req: systemTypes.ProjectWatchFileReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectWatchFileReq, void>("project.watch_file", req, { reqSchemaId: 1076, ...opts });
}

export const watchFile_meta = {
  callable: "project.watch_file",
  name: "watch_file",
  reqSchemaId: 1076,
} as const;

export async function wikiAutomationBind(client: GosporeClient, req: systemTypes.WikiAutomationBindReq, opts?: InvokeOptions): Promise<systemTypes.WikiAutomationBindResp> {
  return client.invoke<systemTypes.WikiAutomationBindReq, systemTypes.WikiAutomationBindResp>("project.wiki_automation_bind", req, { reqSchemaId: 1315, resSchemaId: 1316, ...opts });
}

export const wikiAutomationBind_meta = {
  callable: "project.wiki_automation_bind",
  name: "wiki_automation_bind",
  reqSchemaId: 1315,
  resSchemaId: 1316,
} as const;

export async function wikiClaimTaskCard(client: GosporeClient, req: systemTypes.WikiClaimTaskCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiClaimTaskCardResp> {
  return client.invoke<systemTypes.WikiClaimTaskCardReq, systemTypes.WikiClaimTaskCardResp>("project.wiki_claim_task_card", req, { reqSchemaId: 1290, resSchemaId: 1291, ...opts });
}

export const wikiClaimTaskCard_meta = {
  callable: "project.wiki_claim_task_card",
  name: "wiki_claim_task_card",
  reqSchemaId: 1290,
  resSchemaId: 1291,
} as const;

export async function wikiCloseCard(client: GosporeClient, req: systemTypes.WikiCloseCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiOpenCardsResp> {
  return client.invoke<systemTypes.WikiCloseCardReq, systemTypes.WikiOpenCardsResp>("project.wiki_close_card", req, { reqSchemaId: 2231, resSchemaId: 1265, ...opts });
}

export const wikiCloseCard_meta = {
  callable: "project.wiki_close_card",
  name: "wiki_close_card",
  reqSchemaId: 2231,
  resSchemaId: 1265,
} as const;

export async function wikiCreateCard(client: GosporeClient, req: systemTypes.WikiCreateCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiCreateCardResp> {
  return client.invoke<systemTypes.WikiCreateCardReq, systemTypes.WikiCreateCardResp>("project.wiki_create_card", req, { reqSchemaId: 1272, resSchemaId: 1273, ...opts });
}

export const wikiCreateCard_meta = {
  callable: "project.wiki_create_card",
  name: "wiki_create_card",
  reqSchemaId: 1272,
  resSchemaId: 1273,
} as const;

export async function wikiCreateMap(client: GosporeClient, req: systemTypes.WikiCreateMapReq, opts?: InvokeOptions): Promise<systemTypes.WikiCreateMapResp> {
  return client.invoke<systemTypes.WikiCreateMapReq, systemTypes.WikiCreateMapResp>("project.wiki_create_map", req, { reqSchemaId: 1292, resSchemaId: 1293, ...opts });
}

export const wikiCreateMap_meta = {
  callable: "project.wiki_create_map",
  name: "wiki_create_map",
  reqSchemaId: 1292,
  resSchemaId: 1293,
} as const;

export async function wikiCreateTaskCard(client: GosporeClient, req: systemTypes.WikiCreateTaskCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiCreateTaskCardResp> {
  return client.invoke<systemTypes.WikiCreateTaskCardReq, systemTypes.WikiCreateTaskCardResp>("project.wiki_create_task_card", req, { reqSchemaId: 1294, resSchemaId: 1295, ...opts });
}

export const wikiCreateTaskCard_meta = {
  callable: "project.wiki_create_task_card",
  name: "wiki_create_task_card",
  reqSchemaId: 1294,
  resSchemaId: 1295,
} as const;

export async function wikiDeleteCard(client: GosporeClient, req: systemTypes.WikiDeleteCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiDeleteCardResp> {
  return client.invoke<systemTypes.WikiDeleteCardReq, systemTypes.WikiDeleteCardResp>("project.wiki_delete_card", req, { reqSchemaId: 1276, resSchemaId: 1277, ...opts });
}

export const wikiDeleteCard_meta = {
  callable: "project.wiki_delete_card",
  name: "wiki_delete_card",
  reqSchemaId: 1276,
  resSchemaId: 1277,
} as const;

export async function wikiDispatchPlan(client: GosporeClient, req: systemTypes.WikiDispatchPlanReq, opts?: InvokeOptions): Promise<systemTypes.WikiDispatchPlanResp> {
  return client.invoke<systemTypes.WikiDispatchPlanReq, systemTypes.WikiDispatchPlanResp>("project.wiki_dispatch_plan", req, { reqSchemaId: 2864, resSchemaId: 2865, ...opts });
}

export const wikiDispatchPlan_meta = {
  callable: "project.wiki_dispatch_plan",
  name: "wiki_dispatch_plan",
  reqSchemaId: 2864,
  resSchemaId: 2865,
} as const;

export async function wikiEditCard(client: GosporeClient, req: systemTypes.WikiEditCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiEditCardResp> {
  return client.invoke<systemTypes.WikiEditCardReq, systemTypes.WikiEditCardResp>("project.wiki_edit_card", req, { reqSchemaId: 1274, resSchemaId: 1275, ...opts });
}

export const wikiEditCard_meta = {
  callable: "project.wiki_edit_card",
  name: "wiki_edit_card",
  reqSchemaId: 1274,
  resSchemaId: 1275,
} as const;

export async function wikiFrontier(client: GosporeClient, req: systemTypes.WikiFrontierReq, opts?: InvokeOptions): Promise<systemTypes.WikiFrontierResp> {
  return client.invoke<systemTypes.WikiFrontierReq, systemTypes.WikiFrontierResp>("project.wiki_frontier", req, { reqSchemaId: 1286, resSchemaId: 1287, ...opts });
}

export const wikiFrontier_meta = {
  callable: "project.wiki_frontier",
  name: "wiki_frontier",
  reqSchemaId: 1286,
  resSchemaId: 1287,
} as const;

export async function wikiGetCard(client: GosporeClient, req: systemTypes.WikiGetCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiGetCardResp> {
  return client.invoke<systemTypes.WikiGetCardReq, systemTypes.WikiGetCardResp>("project.wiki_get_card", req, { reqSchemaId: 1270, resSchemaId: 1271, ...opts });
}

export const wikiGetCard_meta = {
  callable: "project.wiki_get_card",
  name: "wiki_get_card",
  reqSchemaId: 1270,
  resSchemaId: 1271,
} as const;

export async function wikiGetCardHierarchy(client: GosporeClient, req: systemTypes.WikiGetCardHierarchyReq, opts?: InvokeOptions): Promise<systemTypes.WikiGetCardHierarchyResp> {
  return client.invoke<systemTypes.WikiGetCardHierarchyReq, systemTypes.WikiGetCardHierarchyResp>("project.wiki_get_card_hierarchy", req, { reqSchemaId: 2228, resSchemaId: 2229, ...opts });
}

export const wikiGetCardHierarchy_meta = {
  callable: "project.wiki_get_card_hierarchy",
  name: "wiki_get_card_hierarchy",
  reqSchemaId: 2228,
  resSchemaId: 2229,
} as const;

export async function wikiGetCardsBatch(client: GosporeClient, req: systemTypes.WikiGetCardsBatchReq, opts?: InvokeOptions): Promise<systemTypes.WikiGetCardsBatchResp> {
  return client.invoke<systemTypes.WikiGetCardsBatchReq, systemTypes.WikiGetCardsBatchResp>("project.wiki_get_cards_batch", req, { reqSchemaId: 1323, resSchemaId: 1325, ...opts });
}

export const wikiGetCardsBatch_meta = {
  callable: "project.wiki_get_cards_batch",
  name: "wiki_get_cards_batch",
  reqSchemaId: 1323,
  resSchemaId: 1325,
} as const;

export async function wikiGetOpenCards(client: GosporeClient, req: systemTypes.WikiOpenCardsReq, opts?: InvokeOptions): Promise<systemTypes.WikiOpenCardsResp> {
  return client.invoke<systemTypes.WikiOpenCardsReq, systemTypes.WikiOpenCardsResp>("project.wiki_get_open_cards", req, { reqSchemaId: 1264, resSchemaId: 1265, ...opts });
}

export const wikiGetOpenCards_meta = {
  callable: "project.wiki_get_open_cards",
  name: "wiki_get_open_cards",
  reqSchemaId: 1264,
  resSchemaId: 1265,
} as const;

export async function wikiGetStarred(client: GosporeClient, req: systemTypes.WikiGetStarredReq, opts?: InvokeOptions): Promise<systemTypes.WikiStarredResp> {
  return client.invoke<systemTypes.WikiGetStarredReq, systemTypes.WikiStarredResp>("project.wiki_get_starred", req, { reqSchemaId: 1327, resSchemaId: 1328, ...opts });
}

export const wikiGetStarred_meta = {
  callable: "project.wiki_get_starred",
  name: "wiki_get_starred",
  reqSchemaId: 1327,
  resSchemaId: 1328,
} as const;

export async function wikiListCards(client: GosporeClient, req: systemTypes.WikiListCardsReq, opts?: InvokeOptions): Promise<systemTypes.WikiListCardsResp> {
  return client.invoke<systemTypes.WikiListCardsReq, systemTypes.WikiListCardsResp>("project.wiki_list_cards", req, { reqSchemaId: 1268, resSchemaId: 1269, ...opts });
}

export const wikiListCards_meta = {
  callable: "project.wiki_list_cards",
  name: "wiki_list_cards",
  reqSchemaId: 1268,
  resSchemaId: 1269,
} as const;

export async function wikiListDependencies(client: GosporeClient, req: systemTypes.WikiListDependenciesReq, opts?: InvokeOptions): Promise<systemTypes.WikiListDependenciesResp> {
  return client.invoke<systemTypes.WikiListDependenciesReq, systemTypes.WikiListDependenciesResp>("project.wiki_list_dependencies", req, { reqSchemaId: 1300, resSchemaId: 1301, ...opts });
}

export const wikiListDependencies_meta = {
  callable: "project.wiki_list_dependencies",
  name: "wiki_list_dependencies",
  reqSchemaId: 1300,
  resSchemaId: 1301,
} as const;

export async function wikiListTemplateRuns(client: GosporeClient, req: systemTypes.WikiListTemplateRunsReq, opts?: InvokeOptions): Promise<systemTypes.WikiListTemplateRunsResp> {
  return client.invoke<systemTypes.WikiListTemplateRunsReq, systemTypes.WikiListTemplateRunsResp>("project.wiki_list_template_runs", req, { reqSchemaId: 1320, resSchemaId: 1321, ...opts });
}

export const wikiListTemplateRuns_meta = {
  callable: "project.wiki_list_template_runs",
  name: "wiki_list_template_runs",
  reqSchemaId: 1320,
  resSchemaId: 1321,
} as const;

export async function wikiListTemplates(client: GosporeClient, req: systemTypes.WikiListTemplatesReq, opts?: InvokeOptions): Promise<systemTypes.WikiListTemplatesResp> {
  return client.invoke<systemTypes.WikiListTemplatesReq, systemTypes.WikiListTemplatesResp>("project.wiki_list_templates", req, { reqSchemaId: 1317, resSchemaId: 1318, ...opts });
}

export const wikiListTemplates_meta = {
  callable: "project.wiki_list_templates",
  name: "wiki_list_templates",
  reqSchemaId: 1317,
  resSchemaId: 1318,
} as const;

export async function wikiListTimers(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WikiListTimersResp> {
  return client.invoke<void, systemTypes.WikiListTimersResp>("project.wiki_list_timers", undefined, { resSchemaId: 1279, ...opts });
}

export async function wikiOpenCard(client: GosporeClient, req: systemTypes.WikiOpenCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiOpenCardsResp> {
  return client.invoke<systemTypes.WikiOpenCardReq, systemTypes.WikiOpenCardsResp>("project.wiki_open_card", req, { reqSchemaId: 2230, resSchemaId: 1265, ...opts });
}

export const wikiOpenCard_meta = {
  callable: "project.wiki_open_card",
  name: "wiki_open_card",
  reqSchemaId: 2230,
  resSchemaId: 1265,
} as const;

export async function wikiPromoteNodeOutputs(client: GosporeClient, req: systemTypes.WikiPromoteNodeOutputsReq, opts?: InvokeOptions): Promise<systemTypes.WikiPromoteNodeOutputsResp> {
  return client.invoke<systemTypes.WikiPromoteNodeOutputsReq, systemTypes.WikiPromoteNodeOutputsResp>("project.wiki_promote_node_outputs", req, { reqSchemaId: 1313, resSchemaId: 1314, ...opts });
}

export const wikiPromoteNodeOutputs_meta = {
  callable: "project.wiki_promote_node_outputs",
  name: "wiki_promote_node_outputs",
  reqSchemaId: 1313,
  resSchemaId: 1314,
} as const;

export async function wikiSaveOpenCards(client: GosporeClient, req: systemTypes.WikiSaveOpenCardsReq, opts?: InvokeOptions): Promise<systemTypes.WikiOpenCardsResp> {
  return client.invoke<systemTypes.WikiSaveOpenCardsReq, systemTypes.WikiOpenCardsResp>("project.wiki_save_open_cards", req, { reqSchemaId: 1266, resSchemaId: 1265, ...opts });
}

export const wikiSaveOpenCards_meta = {
  callable: "project.wiki_save_open_cards",
  name: "wiki_save_open_cards",
  reqSchemaId: 1266,
  resSchemaId: 1265,
} as const;

export async function wikiSearchCardContent(client: GosporeClient, req: systemTypes.WikiSearchCardContentReq, opts?: InvokeOptions): Promise<systemTypes.WikiSearchCardContentResp> {
  return client.invoke<systemTypes.WikiSearchCardContentReq, systemTypes.WikiSearchCardContentResp>("project.wiki_search_card_content", req, { reqSchemaId: 2232, resSchemaId: 2234, ...opts });
}

export const wikiSearchCardContent_meta = {
  callable: "project.wiki_search_card_content",
  name: "wiki_search_card_content",
  reqSchemaId: 2232,
  resSchemaId: 2234,
} as const;

export async function wikiSetMapInputs(client: GosporeClient, req: systemTypes.WikiSetMapInputsReq, opts?: InvokeOptions): Promise<systemTypes.WikiSetMapInputsResp> {
  return client.invoke<systemTypes.WikiSetMapInputsReq, systemTypes.WikiSetMapInputsResp>("project.wiki_set_map_inputs", req, { reqSchemaId: 1311, resSchemaId: 1312, ...opts });
}

export const wikiSetMapInputs_meta = {
  callable: "project.wiki_set_map_inputs",
  name: "wiki_set_map_inputs",
  reqSchemaId: 1311,
  resSchemaId: 1312,
} as const;

export async function wikiSetMapOwner(client: GosporeClient, req: systemTypes.WikiSetMapOwnerReq, opts?: InvokeOptions): Promise<systemTypes.WikiSetMapOwnerResp> {
  return client.invoke<systemTypes.WikiSetMapOwnerReq, systemTypes.WikiSetMapOwnerResp>("project.wiki_set_map_owner", req, { reqSchemaId: 1288, resSchemaId: 1289, ...opts });
}

export const wikiSetMapOwner_meta = {
  callable: "project.wiki_set_map_owner",
  name: "wiki_set_map_owner",
  reqSchemaId: 1288,
  resSchemaId: 1289,
} as const;

export async function wikiSetStarred(client: GosporeClient, req: systemTypes.WikiSetStarredReq, opts?: InvokeOptions): Promise<systemTypes.WikiStarredResp> {
  return client.invoke<systemTypes.WikiSetStarredReq, systemTypes.WikiStarredResp>("project.wiki_set_starred", req, { reqSchemaId: 1326, resSchemaId: 1328, ...opts });
}

export const wikiSetStarred_meta = {
  callable: "project.wiki_set_starred",
  name: "wiki_set_starred",
  reqSchemaId: 1326,
  resSchemaId: 1328,
} as const;

export async function wikiSetStatus(client: GosporeClient, req: systemTypes.WikiSetStatusReq, opts?: InvokeOptions): Promise<systemTypes.WikiSetStatusResp> {
  return client.invoke<systemTypes.WikiSetStatusReq, systemTypes.WikiSetStatusResp>("project.wiki_set_status", req, { reqSchemaId: 1283, resSchemaId: 1284, ...opts });
}

export const wikiSetStatus_meta = {
  callable: "project.wiki_set_status",
  name: "wiki_set_status",
  reqSchemaId: 1283,
  resSchemaId: 1284,
} as const;

export async function wikiSetTaskDependencies(client: GosporeClient, req: systemTypes.WikiSetTaskDependenciesReq, opts?: InvokeOptions): Promise<systemTypes.WikiSetTaskDependenciesResp> {
  return client.invoke<systemTypes.WikiSetTaskDependenciesReq, systemTypes.WikiSetTaskDependenciesResp>("project.wiki_set_task_dependencies", req, { reqSchemaId: 1296, resSchemaId: 1297, ...opts });
}

export const wikiSetTaskDependencies_meta = {
  callable: "project.wiki_set_task_dependencies",
  name: "wiki_set_task_dependencies",
  reqSchemaId: 1296,
  resSchemaId: 1297,
} as const;

export async function wikiSetTaskOutputs(client: GosporeClient, req: systemTypes.WikiSetTaskOutputsReq, opts?: InvokeOptions): Promise<systemTypes.WikiSetTaskOutputsResp> {
  return client.invoke<systemTypes.WikiSetTaskOutputsReq, systemTypes.WikiSetTaskOutputsResp>("project.wiki_set_task_outputs", req, { reqSchemaId: 1304, resSchemaId: 1305, ...opts });
}

export const wikiSetTaskOutputs_meta = {
  callable: "project.wiki_set_task_outputs",
  name: "wiki_set_task_outputs",
  reqSchemaId: 1304,
  resSchemaId: 1305,
} as const;

export async function wikiTemplateInstantiate(client: GosporeClient, req: systemTypes.WikiTemplateInstantiateReq, opts?: InvokeOptions): Promise<systemTypes.WikiTemplateInstantiateResp> {
  return client.invoke<systemTypes.WikiTemplateInstantiateReq, systemTypes.WikiTemplateInstantiateResp>("project.wiki_template_instantiate", req, { reqSchemaId: 1308, resSchemaId: 1309, ...opts });
}

export const wikiTemplateInstantiate_meta = {
  callable: "project.wiki_template_instantiate",
  name: "wiki_template_instantiate",
  reqSchemaId: 1308,
  resSchemaId: 1309,
} as const;

export async function wikiTemplateSave(client: GosporeClient, req: systemTypes.WikiTemplateSaveReq, opts?: InvokeOptions): Promise<systemTypes.WikiTemplateSaveResp> {
  return client.invoke<systemTypes.WikiTemplateSaveReq, systemTypes.WikiTemplateSaveResp>("project.wiki_template_save", req, { reqSchemaId: 1306, resSchemaId: 1307, ...opts });
}

export const wikiTemplateSave_meta = {
  callable: "project.wiki_template_save",
  name: "wiki_template_save",
  reqSchemaId: 1306,
  resSchemaId: 1307,
} as const;

export async function wikiToggleTimer(client: GosporeClient, req: systemTypes.WikiToggleTimerReq, opts?: InvokeOptions): Promise<systemTypes.WikiToggleTimerResp> {
  return client.invoke<systemTypes.WikiToggleTimerReq, systemTypes.WikiToggleTimerResp>("project.wiki_toggle_timer", req, { reqSchemaId: 2226, resSchemaId: 2227, ...opts });
}

export const wikiToggleTimer_meta = {
  callable: "project.wiki_toggle_timer",
  name: "wiki_toggle_timer",
  reqSchemaId: 2226,
  resSchemaId: 2227,
} as const;

export async function wikiTriggerTimerCard(client: GosporeClient, req: systemTypes.WikiTriggerTimerCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiTriggerTimerCardResp> {
  return client.invoke<systemTypes.WikiTriggerTimerCardReq, systemTypes.WikiTriggerTimerCardResp>("project.wiki_trigger_timer_card", req, { reqSchemaId: 2224, resSchemaId: 2225, ...opts });
}

export const wikiTriggerTimerCard_meta = {
  callable: "project.wiki_trigger_timer_card",
  name: "wiki_trigger_timer_card",
  reqSchemaId: 2224,
  resSchemaId: 2225,
} as const;

export async function wikiValidateCard(client: GosporeClient, req: systemTypes.WikiValidateCardReq, opts?: InvokeOptions): Promise<systemTypes.WikiValidateCardResp> {
  return client.invoke<systemTypes.WikiValidateCardReq, systemTypes.WikiValidateCardResp>("project.wiki_validate_card", req, { reqSchemaId: 1281, resSchemaId: 1282, ...opts });
}

export const wikiValidateCard_meta = {
  callable: "project.wiki_validate_card",
  name: "wiki_validate_card",
  reqSchemaId: 1281,
  resSchemaId: 1282,
} as const;

export async function worktreeAgentBindings(client: GosporeClient, req: systemTypes.ProjectWorktreeAgentBindingsReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktreeAgentBindingsResp> {
  return client.invoke<systemTypes.ProjectWorktreeAgentBindingsReq, systemTypes.ProjectWorktreeAgentBindingsResp>("project.worktree_agent_bindings", req, { reqSchemaId: 1057, resSchemaId: 1058, ...opts });
}

export const worktreeAgentBindings_meta = {
  callable: "project.worktree_agent_bindings",
  name: "worktree_agent_bindings",
  reqSchemaId: 1057,
  resSchemaId: 1058,
} as const;

export async function worktreeCopy(client: GosporeClient, req: systemTypes.ProjectWorktreeCopyReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktreeCopyResp> {
  return client.invoke<systemTypes.ProjectWorktreeCopyReq, systemTypes.ProjectWorktreeCopyResp>("project.worktree_copy", req, { reqSchemaId: 1077, resSchemaId: 1080, ...opts });
}

export const worktreeCopy_meta = {
  callable: "project.worktree_copy",
  name: "worktree_copy",
  reqSchemaId: 1077,
  resSchemaId: 1080,
} as const;

export async function worktreeCreate(client: GosporeClient, req: systemTypes.ProjectWorktreeCreateReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktree> {
  return client.invoke<systemTypes.ProjectWorktreeCreateReq, systemTypes.ProjectWorktree>("project.worktree_create", req, { reqSchemaId: 1051, resSchemaId: 1050, ...opts });
}

export const worktreeCreate_meta = {
  callable: "project.worktree_create",
  name: "worktree_create",
  reqSchemaId: 1051,
  resSchemaId: 1050,
} as const;

export async function worktreeDiscard(client: GosporeClient, req: systemTypes.ProjectWorktreeDiscardReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.ProjectWorktreeDiscardReq, void>("project.worktree_discard", req, { reqSchemaId: 1055, ...opts });
}

export const worktreeDiscard_meta = {
  callable: "project.worktree_discard",
  name: "worktree_discard",
  reqSchemaId: 1055,
} as const;

export async function worktreeEnter(client: GosporeClient, req: systemTypes.ProjectWorktreeEnterReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktreeEnterResp> {
  return client.invoke<systemTypes.ProjectWorktreeEnterReq, systemTypes.ProjectWorktreeEnterResp>("project.worktree_enter", req, { reqSchemaId: 1060, resSchemaId: 1061, ...opts });
}

export const worktreeEnter_meta = {
  callable: "project.worktree_enter",
  name: "worktree_enter",
  reqSchemaId: 1060,
  resSchemaId: 1061,
} as const;

export async function worktreeExit(client: GosporeClient, req: systemTypes.ProjectWorktreeExitReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktreeExitResp> {
  return client.invoke<systemTypes.ProjectWorktreeExitReq, systemTypes.ProjectWorktreeExitResp>("project.worktree_exit", req, { reqSchemaId: 1062, resSchemaId: 1063, ...opts });
}

export const worktreeExit_meta = {
  callable: "project.worktree_exit",
  name: "worktree_exit",
  reqSchemaId: 1062,
  resSchemaId: 1063,
} as const;

export async function worktreeGet(client: GosporeClient, req: systemTypes.ProjectWorktreeGetReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktree> {
  return client.invoke<systemTypes.ProjectWorktreeGetReq, systemTypes.ProjectWorktree>("project.worktree_get", req, { reqSchemaId: 1054, resSchemaId: 1050, ...opts });
}

export const worktreeGet_meta = {
  callable: "project.worktree_get",
  name: "worktree_get",
  reqSchemaId: 1054,
  resSchemaId: 1050,
} as const;

export async function worktreeList(client: GosporeClient, req: systemTypes.ProjectWorktreeListReq, opts?: InvokeOptions): Promise<systemTypes.ProjectWorktreeListResp> {
  return client.invoke<systemTypes.ProjectWorktreeListReq, systemTypes.ProjectWorktreeListResp>("project.worktree_list", req, { reqSchemaId: 1052, resSchemaId: 1053, ...opts });
}

export const worktreeList_meta = {
  callable: "project.worktree_list",
  name: "worktree_list",
  reqSchemaId: 1052,
  resSchemaId: 1053,
} as const;

export async function write(client: GosporeClient, req: systemTypes.FileSystemWriteReq, opts?: InvokeOptions): Promise<systemTypes.FileSystemWriteResp> {
  return client.invoke<systemTypes.FileSystemWriteReq, systemTypes.FileSystemWriteResp>("project.write", req, { reqSchemaId: 744, resSchemaId: 745, ...opts });
}

export const write_meta = {
  callable: "project.write",
  name: "write",
  reqSchemaId: 744,
  resSchemaId: 745,
} as const;

export type CardChangedHandler = (payload: systemTypes.WikiCardChangedEvent) => void;

export function OnCardChanged(client: GosporeClient, actorId: string, handler: CardChangedHandler): () => void {
  return client.events.onInstance(actorId, "card_changed", (payload) => handler(payload as systemTypes.WikiCardChangedEvent));
}

export function OffCardChanged(cancel: () => void): void {
  cancel();
}

export type FileChangedHandler = (payload: systemTypes.ProjectFileChangedEvent) => void;

export function OnFileChanged(client: GosporeClient, actorId: string, handler: FileChangedHandler): () => void {
  return client.events.onInstance(actorId, "file_changed", (payload) => handler(payload as systemTypes.ProjectFileChangedEvent));
}

export function OffFileChanged(cancel: () => void): void {
  cancel();
}

export type GraphChangedHandler = (payload: systemTypes.GraphChangedEvent) => void;

export function OnGraphChanged(client: GosporeClient, actorId: string, handler: GraphChangedHandler): () => void {
  return client.events.onInstance(actorId, "graph_changed", (payload) => handler(payload as systemTypes.GraphChangedEvent));
}

export function OffGraphChanged(cancel: () => void): void {
  cancel();
}

