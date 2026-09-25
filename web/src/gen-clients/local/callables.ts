// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "local",
    name: "agent_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 357,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "AgentConfigureReq",
      className: "AgentConfigureReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "agent_pause",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2816,
    finalSchemaId: 2817,
    req: {
      kind: "struct",
      name: "AgentPauseReq",
      className: "AgentPauseReq"
    },
    final: {
      kind: "struct",
      name: "AgentPauseResp",
      className: "AgentPauseResp"
    }
  },
  {
    namespace: "local",
    name: "agent_resume",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2818,
    finalSchemaId: 2819,
    req: {
      kind: "struct",
      name: "AgentResumeReq",
      className: "AgentResumeReq"
    },
    final: {
      kind: "struct",
      name: "AgentResumeResp",
      className: "AgentResumeResp"
    }
  },
  {
    namespace: "local",
    name: "agent_status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 128,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AgentStatusResp",
      className: "AgentStatusResp"
    }
  },
  {
    namespace: "local",
    name: "capture_profile",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 169,
    finalSchemaId: 170,
    req: {
      kind: "struct",
      name: "AgentCaptureProfileReq",
      className: "AgentCaptureProfileReq"
    },
    final: {
      kind: "struct",
      name: "AgentCaptureProfileResp",
      className: "AgentCaptureProfileResp"
    }
  },
  {
    namespace: "local",
    name: "chat_cancel_pending",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 228,
    finalSchemaId: 229,
    req: {
      kind: "struct",
      name: "AgentChatCancelPendingReq",
      className: "AgentChatCancelPendingReq"
    },
    final: {
      kind: "struct",
      name: "AgentChatCancelPendingResp",
      className: "AgentChatCancelPendingResp"
    }
  },
  {
    namespace: "local",
    name: "chat_submit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 226,
    finalSchemaId: 227,
    req: {
      kind: "struct",
      name: "AgentChatSubmitReq",
      className: "AgentChatSubmitReq"
    },
    final: {
      kind: "struct",
      name: "AgentChatSubmitResp",
      className: "AgentChatSubmitResp"
    }
  },
  {
    namespace: "local",
    name: "compaction_configure",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 393,
    finalSchemaId: 394,
    req: {
      kind: "struct",
      name: "AgentCompactionConfigureReq",
      className: "AgentCompactionConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AgentCompactionConfigureResp",
      className: "AgentCompactionConfigureResp"
    }
  },
  {
    namespace: "local",
    name: "compiled_prompt",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1396,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "PromptArtifact",
      className: "PromptArtifact"
    }
  },
  {
    namespace: "local",
    name: "complete_message",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 129,
    finalSchemaId: 348,
    req: {
      kind: "struct",
      name: "CompleteMessageReq",
      className: "CompleteMessageReq"
    },
    final: {
      kind: "struct",
      name: "SummarizeResp",
      className: "SummarizeResp"
    }
  },
  {
    namespace: "local",
    name: "component_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2582,
    finalSchemaId: 2583,
    req: {
      kind: "struct",
      name: "AgentComponentListReq",
      className: "AgentComponentListReq"
    },
    final: {
      kind: "struct",
      name: "AgentComponentListResp",
      className: "AgentComponentListResp"
    }
  },
  {
    namespace: "local",
    name: "component_mount",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2576,
    finalSchemaId: 2577,
    req: {
      kind: "struct",
      name: "AgentComponentMountReq",
      className: "AgentComponentMountReq"
    },
    final: {
      kind: "struct",
      name: "AgentComponentMountResp",
      className: "AgentComponentMountResp"
    }
  },
  {
    namespace: "local",
    name: "component_set_enabled",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2580,
    finalSchemaId: 2581,
    req: {
      kind: "struct",
      name: "AgentComponentSetEnabledReq",
      className: "AgentComponentSetEnabledReq"
    },
    final: {
      kind: "struct",
      name: "AgentComponentSetEnabledResp",
      className: "AgentComponentSetEnabledResp"
    }
  },
  {
    namespace: "local",
    name: "component_snapshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2584,
    finalSchemaId: 2585,
    req: {
      kind: "struct",
      name: "AgentComponentSnapshotReq",
      className: "AgentComponentSnapshotReq"
    },
    final: {
      kind: "struct",
      name: "AgentComponentSnapshotResp",
      className: "AgentComponentSnapshotResp"
    }
  },
  {
    namespace: "local",
    name: "component_unmount",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2578,
    finalSchemaId: 2579,
    req: {
      kind: "struct",
      name: "AgentComponentUnmountReq",
      className: "AgentComponentUnmountReq"
    },
    final: {
      kind: "struct",
      name: "AgentComponentUnmountResp",
      className: "AgentComponentUnmountResp"
    }
  },
  {
    namespace: "local",
    name: "context_budget",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 343,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "TurnContextBudgetPayload",
      className: "TurnContextBudgetPayload"
    }
  },
  {
    namespace: "local",
    name: "coordinator_guidance_profile_clear",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4423,
    finalSchemaId: 4424,
    req: {
      kind: "struct",
      name: "GuidanceProfileClearReq",
      className: "GuidanceProfileClearReq"
    },
    final: {
      kind: "struct",
      name: "GuidanceProfileClearResp",
      className: "GuidanceProfileClearResp"
    }
  },
  {
    namespace: "local",
    name: "coordinator_guidance_profile_increment",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4425,
    finalSchemaId: 4426,
    req: {
      kind: "struct",
      name: "GuidanceProfileIncrementReq",
      className: "GuidanceProfileIncrementReq"
    },
    final: {
      kind: "struct",
      name: "GuidanceProfileIncrementResp",
      className: "GuidanceProfileIncrementResp"
    }
  },
  {
    namespace: "local",
    name: "coordinator_guidance_profile_query",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4419,
    finalSchemaId: 4420,
    req: {
      kind: "struct",
      name: "GuidanceProfileQueryReq",
      className: "GuidanceProfileQueryReq"
    },
    final: {
      kind: "struct",
      name: "GuidanceProfileQueryResp",
      className: "GuidanceProfileQueryResp"
    }
  },
  {
    namespace: "local",
    name: "coordinator_guidance_profile_update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4421,
    finalSchemaId: 4422,
    req: {
      kind: "struct",
      name: "GuidanceProfileUpdateReq",
      className: "GuidanceProfileUpdateReq"
    },
    final: {
      kind: "struct",
      name: "GuidanceProfileUpdateResp",
      className: "GuidanceProfileUpdateResp"
    }
  },
  {
    namespace: "local",
    name: "frontend_debug",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 163,
    finalSchemaId: 164,
    req: {
      kind: "struct",
      name: "AgentFrontendDebugReq",
      className: "AgentFrontendDebugReq"
    },
    final: {
      kind: "struct",
      name: "AgentFrontendDebugResp",
      className: "AgentFrontendDebugResp"
    }
  },
  {
    namespace: "local",
    name: "inspect_actor",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 165,
    finalSchemaId: 166,
    req: {
      kind: "struct",
      name: "AgentInspectActorReq",
      className: "AgentInspectActorReq"
    },
    final: {
      kind: "struct",
      name: "AgentInspectActorResp",
      className: "AgentInspectActorResp"
    }
  },
  {
    namespace: "local",
    name: "inspect_pages",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 860,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "InspectPagesResp",
      className: "InspectPagesResp"
    }
  },
  {
    namespace: "local",
    name: "invoke_callable",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 161,
    finalSchemaId: 162,
    req: {
      kind: "struct",
      name: "AgentInvokeCallableReq",
      className: "AgentInvokeCallableReq"
    },
    final: {
      kind: "struct",
      name: "AgentInvokeCallableResp",
      className: "AgentInvokeCallableResp"
    }
  },
  {
    namespace: "local",
    name: "list_callables",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 159,
    finalSchemaId: 160,
    req: {
      kind: "struct",
      name: "AgentListCallablesReq",
      className: "AgentListCallablesReq"
    },
    final: {
      kind: "struct",
      name: "AgentListCallablesResp",
      className: "AgentListCallablesResp"
    }
  },
  {
    namespace: "local",
    name: "memory_dream",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "map",
      name: "map",
      key: {
        kind: "scalar",
        name: "string",
        typeId: 12
      },
      value: {
        kind: "scalar",
        name: "any",
        typeId: 15
      }
    }
  },
  {
    namespace: "local",
    name: "memory_recall",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3730,
    finalSchemaId: 3731,
    req: {
      kind: "struct",
      name: "MemoryRecallReq",
      className: "MemoryRecallReq"
    },
    final: {
      kind: "struct",
      name: "MemoryRecallResp",
      className: "MemoryRecallResp"
    }
  },
  {
    namespace: "local",
    name: "memory_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3728,
    finalSchemaId: 3729,
    req: {
      kind: "struct",
      name: "MemorySaveReq",
      className: "MemorySaveReq"
    },
    final: {
      kind: "struct",
      name: "MemorySaveResp",
      className: "MemorySaveResp"
    }
  },
  {
    namespace: "local",
    name: "memory_snapshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3733,
    finalSchemaId: 3734,
    req: {
      kind: "struct",
      name: "MemorySnapshotReq",
      className: "MemorySnapshotReq"
    },
    final: {
      kind: "struct",
      name: "MemorySnapshotResp",
      className: "MemorySnapshotResp"
    }
  },
  {
    namespace: "local",
    name: "message_clear",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "message_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 276,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AgentMessageListResp",
      className: "AgentMessageListResp"
    }
  },
  {
    namespace: "local",
    name: "message_read",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 277,
    finalSchemaId: 278,
    req: {
      kind: "struct",
      name: "AgentMessageReadReq",
      className: "AgentMessageReadReq"
    },
    final: {
      kind: "struct",
      name: "AgentMessageReadResp",
      className: "AgentMessageReadResp"
    }
  },
  {
    namespace: "local",
    name: "messages_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 135,
    finalSchemaId: 391,
    req: {
      kind: "struct",
      name: "AgentMessagesListReq",
      className: "AgentMessagesListReq"
    },
    final: {
      kind: "struct",
      name: "AgentMessagesListResp",
      className: "AgentMessagesListResp"
    }
  },
  {
    namespace: "local",
    name: "modes_unload_all",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2672,
    finalSchemaId: 2673,
    req: {
      kind: "struct",
      name: "AgentModesUnloadAllReq",
      className: "AgentModesUnloadAllReq"
    },
    final: {
      kind: "struct",
      name: "AgentModesUnloadAllResp",
      className: "AgentModesUnloadAllResp"
    }
  },
  {
    namespace: "local",
    name: "open_global_browser",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 153,
    finalSchemaId: 154,
    req: {
      kind: "struct",
      name: "AgentOpenGlobalBrowserReq",
      className: "AgentOpenGlobalBrowserReq"
    },
    final: {
      kind: "struct",
      name: "AgentOpenGlobalBrowserResp",
      className: "AgentOpenGlobalBrowserResp"
    }
  },
  {
    namespace: "local",
    name: "open_ssh_session",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 157,
    finalSchemaId: 158,
    req: {
      kind: "struct",
      name: "AgentOpenSshSessionReq",
      className: "AgentOpenSshSessionReq"
    },
    final: {
      kind: "struct",
      name: "AgentOpenSshSessionResp",
      className: "AgentOpenSshSessionResp"
    }
  },
  {
    namespace: "local",
    name: "permission_mode_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1783,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "setPermissionModeReq",
      className: "setPermissionModeReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "prompt_artifact",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1396,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "PromptArtifact",
      className: "PromptArtifact"
    }
  },
  {
    namespace: "local",
    name: "read_snapshot",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1733,
    finalSchemaId: 1734,
    req: {
      kind: "struct",
      name: "readSnapshotReq",
      className: "readSnapshotReq"
    },
    final: {
      kind: "struct",
      name: "readSnapshotResp",
      className: "readSnapshotResp"
    }
  },
  {
    namespace: "local",
    name: "scheduler_bind",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2720,
    finalSchemaId: 2721,
    req: {
      kind: "struct",
      name: "AgentSchedulerBindReq",
      className: "AgentSchedulerBindReq"
    },
    final: {
      kind: "struct",
      name: "AgentSchedulerBindResp",
      className: "AgentSchedulerBindResp"
    }
  },
  {
    namespace: "local",
    name: "scheduler_unbind",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2722,
    finalSchemaId: 2723,
    req: {
      kind: "struct",
      name: "AgentSchedulerUnbindReq",
      className: "AgentSchedulerUnbindReq"
    },
    final: {
      kind: "struct",
      name: "AgentSchedulerUnbindResp",
      className: "AgentSchedulerUnbindResp"
    }
  },
  {
    namespace: "local",
    name: "session_export_range",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1784,
    finalSchemaId: 1785,
    req: {
      kind: "struct",
      name: "AgentSessionExportRangeReq",
      className: "AgentSessionExportRangeReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionExportRangeResp",
      className: "AgentSessionExportRangeResp"
    }
  },
  {
    namespace: "local",
    name: "session_fork",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 384,
    finalSchemaId: 385,
    req: {
      kind: "struct",
      name: "AgentSessionForkReq",
      className: "AgentSessionForkReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionForkResp",
      className: "AgentSessionForkResp"
    }
  },
  {
    namespace: "local",
    name: "session_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1730,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AgentGetSessionResp",
      className: "AgentGetSessionResp"
    }
  },
  {
    namespace: "local",
    name: "session_import",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 386,
    finalSchemaId: 387,
    req: {
      kind: "struct",
      name: "AgentSessionImportReq",
      className: "AgentSessionImportReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionImportResp",
      className: "AgentSessionImportResp"
    }
  },
  {
    namespace: "local",
    name: "session_import_turns",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1806,
    finalSchemaId: 1786,
    req: {
      kind: "struct",
      name: "AgentSessionImportTurnsReq",
      className: "AgentSessionImportTurnsReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionImportTurnsResp",
      className: "AgentSessionImportTurnsResp"
    }
  },
  {
    namespace: "local",
    name: "session_stats",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3028,
    finalSchemaId: 3029,
    req: {
      kind: "struct",
      name: "AgentSessionStatsReq",
      className: "AgentSessionStatsReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionStatsResp",
      className: "AgentSessionStatsResp"
    }
  },
  {
    namespace: "local",
    name: "session_summary",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 388,
    finalSchemaId: 389,
    req: {
      kind: "struct",
      name: "AgentSessionSummaryReq",
      className: "AgentSessionSummaryReq"
    },
    final: {
      kind: "struct",
      name: "AgentSessionSummaryResp",
      className: "AgentSessionSummaryResp"
    }
  },
  {
    namespace: "local",
    name: "session_undo",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 380,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "Session",
      className: "Session"
    }
  },
  {
    namespace: "local",
    name: "show_page_thumbnail",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 155,
    finalSchemaId: 156,
    req: {
      kind: "struct",
      name: "AgentShowPageThumbnailReq",
      className: "AgentShowPageThumbnailReq"
    },
    final: {
      kind: "struct",
      name: "AgentShowPageThumbnailResp",
      className: "AgentShowPageThumbnailResp"
    }
  },
  {
    namespace: "local",
    name: "skill_mount",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1150,
    finalSchemaId: 1151,
    req: {
      kind: "struct",
      name: "AgentSkillMountReq",
      className: "AgentSkillMountReq"
    },
    final: {
      kind: "struct",
      name: "AgentSkillMountResp",
      className: "AgentSkillMountResp"
    }
  },
  {
    namespace: "local",
    name: "skill_use",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2768,
    finalSchemaId: 2769,
    req: {
      kind: "struct",
      name: "AgentSkillUseReq",
      className: "AgentSkillUseReq"
    },
    final: {
      kind: "struct",
      name: "AgentSkillUseResp",
      className: "AgentSkillUseResp"
    }
  },
  {
    namespace: "local",
    name: "storage_configure",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 403,
    finalSchemaId: 404,
    req: {
      kind: "struct",
      name: "AgentStorageConfigureReq",
      className: "AgentStorageConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AgentStorageConfigureResp",
      className: "AgentStorageConfigureResp"
    }
  },
  {
    namespace: "local",
    name: "task_cancel",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 143,
    finalSchemaId: 144,
    req: {
      kind: "struct",
      name: "AgentTaskCancelReq",
      className: "AgentTaskCancelReq"
    },
    final: {
      kind: "struct",
      name: "AgentTaskCancelResp",
      className: "AgentTaskCancelResp"
    }
  },
  {
    namespace: "local",
    name: "task_create",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 140,
    finalSchemaId: 141,
    req: {
      kind: "struct",
      name: "AgentTaskCreateReq",
      className: "AgentTaskCreateReq"
    },
    final: {
      kind: "struct",
      name: "AgentTaskCreateResp",
      className: "AgentTaskCreateResp"
    }
  },
  {
    namespace: "local",
    name: "task_delete",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 148,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "AgentTaskDeleteReq",
      className: "AgentTaskDeleteReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "task_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 147,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AgentTaskListResp",
      className: "AgentTaskListResp"
    }
  },
  {
    namespace: "local",
    name: "task_update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 142,
    finalSchemaId: 145,
    req: {
      kind: "struct",
      name: "AgentTaskUpdateReq",
      className: "AgentTaskUpdateReq"
    },
    final: {
      kind: "struct",
      name: "AgentTaskUpdateResp",
      className: "AgentTaskUpdateResp"
    }
  },
  {
    namespace: "local",
    name: "thinking_get_registry",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 364,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "GetThinkingRegistryResp",
      className: "GetThinkingRegistryResp"
    }
  },
  {
    namespace: "local",
    name: "thinking_set_level",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 363,
    finalSchemaId: 361,
    req: {
      kind: "struct",
      name: "SetThinkingLevelReq",
      className: "SetThinkingLevelReq"
    },
    final: {
      kind: "struct",
      name: "ThinkingLevel",
      className: "ThinkingLevel"
    }
  },
  {
    namespace: "local",
    name: "tools_refresh_notify",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1731,
    finalSchemaId: 1732,
    req: {
      kind: "struct",
      name: "AgentToolsRefreshNotifyReq",
      className: "AgentToolsRefreshNotifyReq"
    },
    final: {
      kind: "struct",
      name: "AgentToolsRefreshNotifyResp",
      className: "AgentToolsRefreshNotifyResp"
    }
  },
  {
    namespace: "local",
    name: "turn_answer",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 337,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "TurnAnswerReq",
      className: "TurnAnswerReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "turn_cancel",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "turn_history",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 399,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "TurnHistoryResp",
      className: "TurnHistoryResp"
    }
  },
  {
    namespace: "local",
    name: "turn_middleware",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 400,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "TurnMiddlewareResp",
      className: "TurnMiddlewareResp"
    }
  },
  {
    namespace: "local",
    name: "turn_pause",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "turn_resume",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "local",
    name: "turn_status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 344,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "TurnStatus",
      className: "TurnStatus"
    }
  },
  {
    namespace: "local",
    name: "turns_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 390,
    finalSchemaId: 392,
    req: {
      kind: "struct",
      name: "AgentTurnsListReq",
      className: "AgentTurnsListReq"
    },
    final: {
      kind: "struct",
      name: "AgentTurnsListResp",
      className: "AgentTurnsListResp"
    }
  },
  {
    namespace: "local",
    name: "workflow_pause_all",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 180,
    finalSchemaId: 181,
    req: {
      kind: "struct",
      name: "AgentWorkflowPauseAllReq",
      className: "AgentWorkflowPauseAllReq"
    },
    final: {
      kind: "struct",
      name: "AgentWorkflowPauseAllResp",
      className: "AgentWorkflowPauseAllResp"
    }
  },
  {
    namespace: "local",
    name: "workflow_start",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 176,
    finalSchemaId: 177,
    req: {
      kind: "struct",
      name: "AgentWorkflowStartReq",
      className: "AgentWorkflowStartReq"
    },
    final: {
      kind: "struct",
      name: "AgentWorkflowStartResp",
      className: "AgentWorkflowStartResp"
    }
  },
  {
    namespace: "local",
    name: "workflow_stop",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 178,
    finalSchemaId: 179,
    req: {
      kind: "struct",
      name: "AgentWorkflowStopReq",
      className: "AgentWorkflowStopReq"
    },
    final: {
      kind: "struct",
      name: "AgentWorkflowStopResp",
      className: "AgentWorkflowStopResp"
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
