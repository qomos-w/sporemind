// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "project",
    name: "archive_export",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 758,
    finalSchemaId: 759,
    req: {
      kind: "struct",
      name: "ArchiveExportReq",
      className: "ArchiveExportReq"
    },
    final: {
      kind: "struct",
      name: "ArchiveExportResp",
      className: "ArchiveExportResp"
    }
  },
  {
    namespace: "project",
    name: "archive_import",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 760,
    finalSchemaId: 761,
    req: {
      kind: "struct",
      name: "ArchiveImportReq",
      className: "ArchiveImportReq"
    },
    final: {
      kind: "struct",
      name: "ArchiveImportResp",
      className: "ArchiveImportResp"
    }
  },
  {
    namespace: "project",
    name: "card_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3444,
    finalSchemaId: 3445,
    req: {
      kind: "struct",
      name: "ProjectCardListReq",
      className: "ProjectCardListReq"
    },
    final: {
      kind: "struct",
      name: "ProjectCardListResp",
      className: "ProjectCardListResp"
    }
  },
  {
    namespace: "project",
    name: "card_mount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3440,
    finalSchemaId: 3441,
    req: {
      kind: "struct",
      name: "ProjectCardMountReq",
      className: "ProjectCardMountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectCardMountResp",
      className: "ProjectCardMountResp"
    }
  },
  {
    namespace: "project",
    name: "card_unmount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3442,
    finalSchemaId: 3443,
    req: {
      kind: "struct",
      name: "ProjectCardUnmountReq",
      className: "ProjectCardUnmountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectCardUnmountResp",
      className: "ProjectCardUnmountResp"
    }
  },
  {
    namespace: "project",
    name: "component_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2626,
    finalSchemaId: 2627,
    req: {
      kind: "struct",
      name: "ProjectComponentGetReq",
      className: "ProjectComponentGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectComponentGetResp",
      className: "ProjectComponentGetResp"
    }
  },
  {
    namespace: "project",
    name: "component_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2624,
    finalSchemaId: 2625,
    req: {
      kind: "struct",
      name: "ProjectComponentListReq",
      className: "ProjectComponentListReq"
    },
    final: {
      kind: "struct",
      name: "ProjectComponentListResp",
      className: "ProjectComponentListResp"
    }
  },
  {
    namespace: "project",
    name: "default_bundles_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1089,
    finalSchemaId: 1090,
    req: {
      kind: "struct",
      name: "ProjectDefaultBundlesGetReq",
      className: "ProjectDefaultBundlesGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectDefaultBundlesGetResp",
      className: "ProjectDefaultBundlesGetResp"
    }
  },
  {
    namespace: "project",
    name: "default_bundles_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1091,
    finalSchemaId: 1092,
    req: {
      kind: "struct",
      name: "ProjectDefaultBundlesSetReq",
      className: "ProjectDefaultBundlesSetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectDefaultBundlesSetResp",
      className: "ProjectDefaultBundlesSetResp"
    }
  },
  {
    namespace: "project",
    name: "edit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 746,
    finalSchemaId: 748,
    req: {
      kind: "struct",
      name: "FileSystemEditReq",
      className: "FileSystemEditReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemEditResp",
      className: "FileSystemEditResp"
    }
  },
  {
    namespace: "project",
    name: "git_add",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1025,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitAddReq",
      className: "ProjectGitAddReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_blame",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1044,
    finalSchemaId: 1045,
    req: {
      kind: "struct",
      name: "ProjectGitBlameReq",
      className: "ProjectGitBlameReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitBlameResp",
      className: "ProjectGitBlameResp"
    }
  },
  {
    namespace: "project",
    name: "git_branch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1030,
    finalSchemaId: 1031,
    req: {
      kind: "struct",
      name: "ProjectGitBranchReq",
      className: "ProjectGitBranchReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitBranchResp",
      className: "ProjectGitBranchResp"
    }
  },
  {
    namespace: "project",
    name: "git_checkout",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1032,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitCheckoutReq",
      className: "ProjectGitCheckoutReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_commit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1026,
    finalSchemaId: 1027,
    req: {
      kind: "struct",
      name: "ProjectGitCommitReq",
      className: "ProjectGitCommitReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitCommitResp",
      className: "ProjectGitCommitResp"
    }
  },
  {
    namespace: "project",
    name: "git_config_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1046,
    finalSchemaId: 1047,
    req: {
      kind: "struct",
      name: "ProjectGitConfigGetReq",
      className: "ProjectGitConfigGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitConfigGetResp",
      className: "ProjectGitConfigGetResp"
    }
  },
  {
    namespace: "project",
    name: "git_config_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1048,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitConfigSetReq",
      className: "ProjectGitConfigSetReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_diff",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1023,
    finalSchemaId: 1024,
    req: {
      kind: "struct",
      name: "ProjectGitDiffReq",
      className: "ProjectGitDiffReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitDiffResp",
      className: "ProjectGitDiffResp"
    }
  },
  {
    namespace: "project",
    name: "git_log",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1021,
    finalSchemaId: 1022,
    req: {
      kind: "struct",
      name: "ProjectGitLogReq",
      className: "ProjectGitLogReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitLogResp",
      className: "ProjectGitLogResp"
    }
  },
  {
    namespace: "project",
    name: "git_pull",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1029,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitPullReq",
      className: "ProjectGitPullReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_push",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1028,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitPushReq",
      className: "ProjectGitPushReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_remote_add",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1042,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitRemoteAddReq",
      className: "ProjectGitRemoteAddReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_remote_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1039,
    finalSchemaId: 1041,
    req: {
      kind: "struct",
      name: "ProjectGitRemoteListReq",
      className: "ProjectGitRemoteListReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitRemoteListResp",
      className: "ProjectGitRemoteListResp"
    }
  },
  {
    namespace: "project",
    name: "git_remote_remove",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1043,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitRemoteRemoveReq",
      className: "ProjectGitRemoteRemoveReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_reset",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1033,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitResetReq",
      className: "ProjectGitResetReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_stash_drop",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1038,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitStashDropReq",
      className: "ProjectGitStashDropReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_stash_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1036,
    finalSchemaId: 1037,
    req: {
      kind: "struct",
      name: "ProjectGitStashListReq",
      className: "ProjectGitStashListReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitStashListResp",
      className: "ProjectGitStashListResp"
    }
  },
  {
    namespace: "project",
    name: "git_stash_pop",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1035,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitStashPopReq",
      className: "ProjectGitStashPopReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_stash_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1034,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectGitStashSaveReq",
      className: "ProjectGitStashSaveReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "git_status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1012,
    finalSchemaId: 1013,
    req: {
      kind: "struct",
      name: "ProjectGitStatusReq",
      className: "ProjectGitStatusReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGitStatusResp",
      className: "ProjectGitStatusResp"
    }
  },
  {
    namespace: "project",
    name: "glob",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 749,
    finalSchemaId: 750,
    req: {
      kind: "struct",
      name: "FileSystemGlobReq",
      className: "FileSystemGlobReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemGlobResp",
      className: "FileSystemGlobResp"
    }
  },
  {
    namespace: "project",
    name: "graph_concept_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1226,
    finalSchemaId: 1227,
    req: {
      kind: "struct",
      name: "ProjectGraphConceptGetReq",
      className: "ProjectGraphConceptGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGraphConceptGetResp",
      className: "ProjectGraphConceptGetResp"
    }
  },
  {
    namespace: "project",
    name: "graph_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1223,
    finalSchemaId: 1225,
    req: {
      kind: "struct",
      name: "ProjectGraphGetReq",
      className: "ProjectGraphGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGraphEnvelopeResp",
      className: "ProjectGraphEnvelopeResp"
    }
  },
  {
    namespace: "project",
    name: "graph_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1224,
    finalSchemaId: 1225,
    req: {
      kind: "struct",
      name: "ProjectGraphSaveReq",
      className: "ProjectGraphSaveReq"
    },
    final: {
      kind: "struct",
      name: "ProjectGraphEnvelopeResp",
      className: "ProjectGraphEnvelopeResp"
    }
  },
  {
    namespace: "project",
    name: "grep",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 751,
    finalSchemaId: 754,
    req: {
      kind: "struct",
      name: "FileSystemGrepReq",
      className: "FileSystemGrepReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemGrepResp",
      className: "FileSystemGrepResp"
    }
  },
  {
    namespace: "project",
    name: "info",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1011,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ProjectInfoResp",
      className: "ProjectInfoResp"
    }
  },
  {
    namespace: "project",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 738,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "FileSystemListReq",
      className: "FileSystemListReq"
    },
    final: {
      kind: "scalar",
      name: "string",
      typeId: 12
    }
  },
  {
    namespace: "project",
    name: "no_git_mode_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1085,
    finalSchemaId: 1086,
    req: {
      kind: "struct",
      name: "ProjectNoGitModeGetReq",
      className: "ProjectNoGitModeGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectNoGitModeGetResp",
      className: "ProjectNoGitModeGetResp"
    }
  },
  {
    namespace: "project",
    name: "no_git_mode_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1087,
    finalSchemaId: 1088,
    req: {
      kind: "struct",
      name: "ProjectNoGitModeSetReq",
      className: "ProjectNoGitModeSetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectNoGitModeSetResp",
      className: "ProjectNoGitModeSetResp"
    }
  },
  {
    namespace: "project",
    name: "read",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 739,
    finalSchemaId: 740,
    req: {
      kind: "struct",
      name: "FileSystemReadReq",
      className: "FileSystemReadReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadResp",
      className: "FileSystemReadResp"
    }
  },
  {
    namespace: "project",
    name: "read_base64",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 741,
    finalSchemaId: 762,
    req: {
      kind: "struct",
      name: "FileSystemReadBase64Req",
      className: "FileSystemReadBase64Req"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadBase64Resp",
      className: "FileSystemReadBase64Resp"
    }
  },
  {
    namespace: "project",
    name: "read_chunk",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 742,
    finalSchemaId: 743,
    req: {
      kind: "struct",
      name: "FileSystemReadChunkReq",
      className: "FileSystemReadChunkReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadChunkResp",
      className: "FileSystemReadChunkResp"
    }
  },
  {
    namespace: "project",
    name: "review_changeset",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1152,
    finalSchemaId: 1153,
    req: {
      kind: "struct",
      name: "ProjectReviewChangesetReq",
      className: "ProjectReviewChangesetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectReviewChangesetSummaryResp",
      className: "ProjectReviewChangesetSummaryResp"
    }
  },
  {
    namespace: "project",
    name: "review_file_content",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1159,
    finalSchemaId: 1160,
    req: {
      kind: "struct",
      name: "ProjectReviewFileContentReq",
      className: "ProjectReviewFileContentReq"
    },
    final: {
      kind: "struct",
      name: "ProjectReviewFileContentResp",
      className: "ProjectReviewFileContentResp"
    }
  },
  {
    namespace: "project",
    name: "rm",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 755,
    finalSchemaId: 756,
    req: {
      kind: "struct",
      name: "FileSystemRmReq",
      className: "FileSystemRmReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemRmResp",
      className: "FileSystemRmResp"
    }
  },
  {
    namespace: "project",
    name: "set_protected_files",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4992,
    finalSchemaId: 4993,
    req: {
      kind: "struct",
      name: "SetProtectedFilesReq",
      className: "SetProtectedFilesReq"
    },
    final: {
      kind: "struct",
      name: "SetProtectedFilesResp",
      className: "SetProtectedFilesResp"
    }
  },
  {
    namespace: "project",
    name: "shell_exec",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 1552,
    chunkSchemaId: 1556,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ShellExecReq",
      className: "ShellExecReq"
    },
    chunk: {
      kind: "struct",
      name: "ShellChunk",
      className: "ShellChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "spawn_agent",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1892,
    finalSchemaId: 1893,
    req: {
      kind: "struct",
      name: "ProjectSpawnAgentReq",
      className: "ProjectSpawnAgentReq"
    },
    final: {
      kind: "struct",
      name: "ProjectSpawnAgentResp",
      className: "ProjectSpawnAgentResp"
    }
  },
  {
    namespace: "project",
    name: "sync_roots",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1015,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectSyncRootsReq",
      className: "ProjectSyncRootsReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "task_validate_outputs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1162,
    finalSchemaId: 1163,
    req: {
      kind: "struct",
      name: "ProjectTaskValidateOutputsReq",
      className: "ProjectTaskValidateOutputsReq"
    },
    final: {
      kind: "struct",
      name: "ProjectTaskValidateOutputsResp",
      className: "ProjectTaskValidateOutputsResp"
    }
  },
  {
    namespace: "project",
    name: "watch_file",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1076,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectWatchFileReq",
      className: "ProjectWatchFileReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "wiki_automation_bind",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1315,
    finalSchemaId: 1316,
    req: {
      kind: "struct",
      name: "WikiAutomationBindReq",
      className: "WikiAutomationBindReq"
    },
    final: {
      kind: "struct",
      name: "WikiAutomationBindResp",
      className: "WikiAutomationBindResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_claim_task_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1290,
    finalSchemaId: 1291,
    req: {
      kind: "struct",
      name: "WikiClaimTaskCardReq",
      className: "WikiClaimTaskCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiClaimTaskCardResp",
      className: "WikiClaimTaskCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_close_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2231,
    finalSchemaId: 1265,
    req: {
      kind: "struct",
      name: "WikiCloseCardReq",
      className: "WikiCloseCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiOpenCardsResp",
      className: "WikiOpenCardsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_create_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1272,
    finalSchemaId: 1273,
    req: {
      kind: "struct",
      name: "WikiCreateCardReq",
      className: "WikiCreateCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiCreateCardResp",
      className: "WikiCreateCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_create_map",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1292,
    finalSchemaId: 1293,
    req: {
      kind: "struct",
      name: "WikiCreateMapReq",
      className: "WikiCreateMapReq"
    },
    final: {
      kind: "struct",
      name: "WikiCreateMapResp",
      className: "WikiCreateMapResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_create_task_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1294,
    finalSchemaId: 1295,
    req: {
      kind: "struct",
      name: "WikiCreateTaskCardReq",
      className: "WikiCreateTaskCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiCreateTaskCardResp",
      className: "WikiCreateTaskCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_delete_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1276,
    finalSchemaId: 1277,
    req: {
      kind: "struct",
      name: "WikiDeleteCardReq",
      className: "WikiDeleteCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiDeleteCardResp",
      className: "WikiDeleteCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_dispatch_plan",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2864,
    finalSchemaId: 2865,
    req: {
      kind: "struct",
      name: "WikiDispatchPlanReq",
      className: "WikiDispatchPlanReq"
    },
    final: {
      kind: "struct",
      name: "WikiDispatchPlanResp",
      className: "WikiDispatchPlanResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_edit_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1274,
    finalSchemaId: 1275,
    req: {
      kind: "struct",
      name: "WikiEditCardReq",
      className: "WikiEditCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiEditCardResp",
      className: "WikiEditCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_frontier",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1286,
    finalSchemaId: 1287,
    req: {
      kind: "struct",
      name: "WikiFrontierReq",
      className: "WikiFrontierReq"
    },
    final: {
      kind: "struct",
      name: "WikiFrontierResp",
      className: "WikiFrontierResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_get_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1270,
    finalSchemaId: 1271,
    req: {
      kind: "struct",
      name: "WikiGetCardReq",
      className: "WikiGetCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiGetCardResp",
      className: "WikiGetCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_get_card_hierarchy",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2228,
    finalSchemaId: 2229,
    req: {
      kind: "struct",
      name: "WikiGetCardHierarchyReq",
      className: "WikiGetCardHierarchyReq"
    },
    final: {
      kind: "struct",
      name: "WikiGetCardHierarchyResp",
      className: "WikiGetCardHierarchyResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_get_cards_batch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1323,
    finalSchemaId: 1325,
    req: {
      kind: "struct",
      name: "WikiGetCardsBatchReq",
      className: "WikiGetCardsBatchReq"
    },
    final: {
      kind: "struct",
      name: "WikiGetCardsBatchResp",
      className: "WikiGetCardsBatchResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_get_open_cards",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1264,
    finalSchemaId: 1265,
    req: {
      kind: "struct",
      name: "WikiOpenCardsReq",
      className: "WikiOpenCardsReq"
    },
    final: {
      kind: "struct",
      name: "WikiOpenCardsResp",
      className: "WikiOpenCardsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_get_starred",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1327,
    finalSchemaId: 1328,
    req: {
      kind: "struct",
      name: "WikiGetStarredReq",
      className: "WikiGetStarredReq"
    },
    final: {
      kind: "struct",
      name: "WikiStarredResp",
      className: "WikiStarredResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_list_cards",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1268,
    finalSchemaId: 1269,
    req: {
      kind: "struct",
      name: "WikiListCardsReq",
      className: "WikiListCardsReq"
    },
    final: {
      kind: "struct",
      name: "WikiListCardsResp",
      className: "WikiListCardsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_list_dependencies",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1300,
    finalSchemaId: 1301,
    req: {
      kind: "struct",
      name: "WikiListDependenciesReq",
      className: "WikiListDependenciesReq"
    },
    final: {
      kind: "struct",
      name: "WikiListDependenciesResp",
      className: "WikiListDependenciesResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_list_template_runs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1320,
    finalSchemaId: 1321,
    req: {
      kind: "struct",
      name: "WikiListTemplateRunsReq",
      className: "WikiListTemplateRunsReq"
    },
    final: {
      kind: "struct",
      name: "WikiListTemplateRunsResp",
      className: "WikiListTemplateRunsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_list_templates",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1317,
    finalSchemaId: 1318,
    req: {
      kind: "struct",
      name: "WikiListTemplatesReq",
      className: "WikiListTemplatesReq"
    },
    final: {
      kind: "struct",
      name: "WikiListTemplatesResp",
      className: "WikiListTemplatesResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_list_timers",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1279,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WikiListTimersResp",
      className: "WikiListTimersResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_open_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2230,
    finalSchemaId: 1265,
    req: {
      kind: "struct",
      name: "WikiOpenCardReq",
      className: "WikiOpenCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiOpenCardsResp",
      className: "WikiOpenCardsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_promote_node_outputs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1313,
    finalSchemaId: 1314,
    req: {
      kind: "struct",
      name: "WikiPromoteNodeOutputsReq",
      className: "WikiPromoteNodeOutputsReq"
    },
    final: {
      kind: "struct",
      name: "WikiPromoteNodeOutputsResp",
      className: "WikiPromoteNodeOutputsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_save_open_cards",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1266,
    finalSchemaId: 1265,
    req: {
      kind: "struct",
      name: "WikiSaveOpenCardsReq",
      className: "WikiSaveOpenCardsReq"
    },
    final: {
      kind: "struct",
      name: "WikiOpenCardsResp",
      className: "WikiOpenCardsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_search_card_content",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2232,
    finalSchemaId: 2234,
    req: {
      kind: "struct",
      name: "WikiSearchCardContentReq",
      className: "WikiSearchCardContentReq"
    },
    final: {
      kind: "struct",
      name: "WikiSearchCardContentResp",
      className: "WikiSearchCardContentResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_map_inputs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1311,
    finalSchemaId: 1312,
    req: {
      kind: "struct",
      name: "WikiSetMapInputsReq",
      className: "WikiSetMapInputsReq"
    },
    final: {
      kind: "struct",
      name: "WikiSetMapInputsResp",
      className: "WikiSetMapInputsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_map_owner",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1288,
    finalSchemaId: 1289,
    req: {
      kind: "struct",
      name: "WikiSetMapOwnerReq",
      className: "WikiSetMapOwnerReq"
    },
    final: {
      kind: "struct",
      name: "WikiSetMapOwnerResp",
      className: "WikiSetMapOwnerResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_starred",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1326,
    finalSchemaId: 1328,
    req: {
      kind: "struct",
      name: "WikiSetStarredReq",
      className: "WikiSetStarredReq"
    },
    final: {
      kind: "struct",
      name: "WikiStarredResp",
      className: "WikiStarredResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1283,
    finalSchemaId: 1284,
    req: {
      kind: "struct",
      name: "WikiSetStatusReq",
      className: "WikiSetStatusReq"
    },
    final: {
      kind: "struct",
      name: "WikiSetStatusResp",
      className: "WikiSetStatusResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_task_dependencies",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1296,
    finalSchemaId: 1297,
    req: {
      kind: "struct",
      name: "WikiSetTaskDependenciesReq",
      className: "WikiSetTaskDependenciesReq"
    },
    final: {
      kind: "struct",
      name: "WikiSetTaskDependenciesResp",
      className: "WikiSetTaskDependenciesResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_set_task_outputs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1304,
    finalSchemaId: 1305,
    req: {
      kind: "struct",
      name: "WikiSetTaskOutputsReq",
      className: "WikiSetTaskOutputsReq"
    },
    final: {
      kind: "struct",
      name: "WikiSetTaskOutputsResp",
      className: "WikiSetTaskOutputsResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_template_instantiate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1308,
    finalSchemaId: 1309,
    req: {
      kind: "struct",
      name: "WikiTemplateInstantiateReq",
      className: "WikiTemplateInstantiateReq"
    },
    final: {
      kind: "struct",
      name: "WikiTemplateInstantiateResp",
      className: "WikiTemplateInstantiateResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_template_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1306,
    finalSchemaId: 1307,
    req: {
      kind: "struct",
      name: "WikiTemplateSaveReq",
      className: "WikiTemplateSaveReq"
    },
    final: {
      kind: "struct",
      name: "WikiTemplateSaveResp",
      className: "WikiTemplateSaveResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_toggle_timer",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2226,
    finalSchemaId: 2227,
    req: {
      kind: "struct",
      name: "WikiToggleTimerReq",
      className: "WikiToggleTimerReq"
    },
    final: {
      kind: "struct",
      name: "WikiToggleTimerResp",
      className: "WikiToggleTimerResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_trigger_timer_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2224,
    finalSchemaId: 2225,
    req: {
      kind: "struct",
      name: "WikiTriggerTimerCardReq",
      className: "WikiTriggerTimerCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiTriggerTimerCardResp",
      className: "WikiTriggerTimerCardResp"
    }
  },
  {
    namespace: "project",
    name: "wiki_validate_card",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1281,
    finalSchemaId: 1282,
    req: {
      kind: "struct",
      name: "WikiValidateCardReq",
      className: "WikiValidateCardReq"
    },
    final: {
      kind: "struct",
      name: "WikiValidateCardResp",
      className: "WikiValidateCardResp"
    }
  },
  {
    namespace: "project",
    name: "worktree_agent_bindings",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1057,
    finalSchemaId: 1058,
    req: {
      kind: "struct",
      name: "ProjectWorktreeAgentBindingsReq",
      className: "ProjectWorktreeAgentBindingsReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktreeAgentBindingsResp",
      className: "ProjectWorktreeAgentBindingsResp"
    }
  },
  {
    namespace: "project",
    name: "worktree_copy",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1077,
    finalSchemaId: 1080,
    req: {
      kind: "struct",
      name: "ProjectWorktreeCopyReq",
      className: "ProjectWorktreeCopyReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktreeCopyResp",
      className: "ProjectWorktreeCopyResp"
    }
  },
  {
    namespace: "project",
    name: "worktree_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1051,
    finalSchemaId: 1050,
    req: {
      kind: "struct",
      name: "ProjectWorktreeCreateReq",
      className: "ProjectWorktreeCreateReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktree",
      className: "ProjectWorktree"
    }
  },
  {
    namespace: "project",
    name: "worktree_discard",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1055,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ProjectWorktreeDiscardReq",
      className: "ProjectWorktreeDiscardReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "project",
    name: "worktree_enter",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1060,
    finalSchemaId: 1061,
    req: {
      kind: "struct",
      name: "ProjectWorktreeEnterReq",
      className: "ProjectWorktreeEnterReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktreeEnterResp",
      className: "ProjectWorktreeEnterResp"
    }
  },
  {
    namespace: "project",
    name: "worktree_exit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1062,
    finalSchemaId: 1063,
    req: {
      kind: "struct",
      name: "ProjectWorktreeExitReq",
      className: "ProjectWorktreeExitReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktreeExitResp",
      className: "ProjectWorktreeExitResp"
    }
  },
  {
    namespace: "project",
    name: "worktree_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1054,
    finalSchemaId: 1050,
    req: {
      kind: "struct",
      name: "ProjectWorktreeGetReq",
      className: "ProjectWorktreeGetReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktree",
      className: "ProjectWorktree"
    }
  },
  {
    namespace: "project",
    name: "worktree_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1052,
    finalSchemaId: 1053,
    req: {
      kind: "struct",
      name: "ProjectWorktreeListReq",
      className: "ProjectWorktreeListReq"
    },
    final: {
      kind: "struct",
      name: "ProjectWorktreeListResp",
      className: "ProjectWorktreeListResp"
    }
  },
  {
    namespace: "project",
    name: "write",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 744,
    finalSchemaId: 745,
    req: {
      kind: "struct",
      name: "FileSystemWriteReq",
      className: "FileSystemWriteReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemWriteResp",
      className: "FileSystemWriteResp"
    }
  },
];

export function buildCallableRegistry(): CallableRegistry {
  const registry = new CallableRegistry();
  for (const entry of callableEntries) {
    registry.register(entry);
  }
  return registry;
}
