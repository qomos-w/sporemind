// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "workspace",
    name: "account",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1903,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AccountSnapshot",
      className: "AccountSnapshot"
    }
  },
  {
    namespace: "workspace",
    name: "add_mount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1885,
    finalSchemaId: 1873,
    req: {
      kind: "struct",
      name: "WorkspaceAddMountReq",
      className: "WorkspaceAddMountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectRef",
      className: "ProjectRef"
    }
  },
  {
    namespace: "workspace",
    name: "agent_access",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1965,
    finalSchemaId: 1882,
    req: {
      kind: "struct",
      name: "WorkspaceAgentAccessReq",
      className: "WorkspaceAgentAccessReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentListState",
      className: "WorkspaceAgentListState"
    }
  },
  {
    namespace: "workspace",
    name: "agent_assign",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3874,
    finalSchemaId: 3875,
    req: {
      kind: "struct",
      name: "WorkspaceAgentAssignReq",
      className: "WorkspaceAgentAssignReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentAssignResp",
      className: "WorkspaceAgentAssignResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_list_state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1882,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentListState",
      className: "WorkspaceAgentListState"
    }
  },
  {
    namespace: "workspace",
    name: "agent_loaded",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5488,
    finalSchemaId: 5489,
    req: {
      kind: "struct",
      name: "WorkspaceAgentLoadedReq",
      className: "WorkspaceAgentLoadedReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentLoadedResp",
      className: "WorkspaceAgentLoadedResp"
    }
  },
  {
    namespace: "workspace",
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
    namespace: "workspace",
    name: "agent_read_message",
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
    namespace: "workspace",
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
    namespace: "workspace",
    name: "agent_review",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3876,
    finalSchemaId: 3877,
    req: {
      kind: "struct",
      name: "WorkspaceAgentReviewReq",
      className: "WorkspaceAgentReviewReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentReviewResp",
      className: "WorkspaceAgentReviewResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_send_message",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 272,
    finalSchemaId: 273,
    req: {
      kind: "struct",
      name: "AgentMessageSendReq",
      className: "AgentMessageSendReq"
    },
    final: {
      kind: "struct",
      name: "AgentMessageSendResp",
      className: "AgentMessageSendResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_spawn_assign",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3872,
    finalSchemaId: 3873,
    req: {
      kind: "struct",
      name: "WorkspaceAgentSpawnAssignReq",
      className: "WorkspaceAgentSpawnAssignReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentSpawnAssignResp",
      className: "WorkspaceAgentSpawnAssignResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_spawn_by_type",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3880,
    finalSchemaId: 3881,
    req: {
      kind: "struct",
      name: "WorkspaceAgentSpawnByTypeReq",
      className: "WorkspaceAgentSpawnByTypeReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentSpawnByTypeResp",
      className: "WorkspaceAgentSpawnByTypeResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_spawn_scheduler",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1979,
    finalSchemaId: 1980,
    req: {
      kind: "struct",
      name: "WorkspaceAgentSpawnSchedulerReq",
      className: "WorkspaceAgentSpawnSchedulerReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentSpawnSchedulerResp",
      className: "WorkspaceAgentSpawnSchedulerResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_spawn_swarm",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1835,
    finalSchemaId: 1836,
    req: {
      kind: "struct",
      name: "WorkspaceAgentSpawnSwarmReq",
      className: "WorkspaceAgentSpawnSwarmReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentSpawnSwarmResp",
      className: "WorkspaceAgentSpawnSwarmResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_status_update",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1884,
    finalSchemaId: 1882,
    req: {
      kind: "struct",
      name: "WorkspaceAgentStatusUpdateReq",
      className: "WorkspaceAgentStatusUpdateReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentListState",
      className: "WorkspaceAgentListState"
    }
  },
  {
    namespace: "workspace",
    name: "agent_terminate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3878,
    finalSchemaId: 3879,
    req: {
      kind: "struct",
      name: "WorkspaceAgentTerminateReq",
      className: "WorkspaceAgentTerminateReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAgentTerminateResp",
      className: "WorkspaceAgentTerminateResp"
    }
  },
  {
    namespace: "workspace",
    name: "agent_unload",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2820,
    finalSchemaId: 2821,
    req: {
      kind: "struct",
      name: "AgentUnloadReq",
      className: "AgentUnloadReq"
    },
    final: {
      kind: "struct",
      name: "AgentUnloadResp",
      className: "AgentUnloadResp"
    }
  },
  {
    namespace: "workspace",
    name: "agents",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 356,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AgentRefListResp",
      className: "AgentRefListResp"
    }
  },
  {
    namespace: "workspace",
    name: "aistats_actor_id",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 3585,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceAistatsActorIdResp",
      className: "WorkspaceAistatsActorIdResp"
    }
  },
  {
    namespace: "workspace",
    name: "builtin_modes_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 3682,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceBuiltinModesListResp",
      className: "WorkspaceBuiltinModesListResp"
    }
  },
  {
    namespace: "workspace",
    name: "clone_agent",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1891,
    finalSchemaId: 354,
    req: {
      kind: "struct",
      name: "WorkspaceCloneAgentReq",
      className: "WorkspaceCloneAgentReq"
    },
    final: {
      kind: "struct",
      name: "AgentRef",
      className: "AgentRef"
    }
  },
  {
    namespace: "workspace",
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
    namespace: "workspace",
    name: "create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1876,
    finalSchemaId: 1873,
    req: {
      kind: "struct",
      name: "WorkspaceCreateReq",
      className: "WorkspaceCreateReq"
    },
    final: {
      kind: "struct",
      name: "ProjectRef",
      className: "ProjectRef"
    }
  },
  {
    namespace: "workspace",
    name: "create_agent",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1888,
    finalSchemaId: 354,
    req: {
      kind: "struct",
      name: "WorkspaceCreateAgentReq",
      className: "WorkspaceCreateAgentReq"
    },
    final: {
      kind: "struct",
      name: "AgentRef",
      className: "AgentRef"
    }
  },
  {
    namespace: "workspace",
    name: "create_agent_kind",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1986,
    finalSchemaId: 1987,
    req: {
      kind: "struct",
      name: "WorkspaceCreateAgentKindReq",
      className: "WorkspaceCreateAgentKindReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceCreateAgentKindResp",
      className: "WorkspaceCreateAgentKindResp"
    }
  },
  {
    namespace: "workspace",
    name: "debug_command_exec",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5345,
    finalSchemaId: 5346,
    req: {
      kind: "struct",
      name: "DebugCommandExecReq",
      className: "DebugCommandExecReq"
    },
    final: {
      kind: "struct",
      name: "DebugCommandExecResp",
      className: "DebugCommandExecResp"
    }
  },
  {
    namespace: "workspace",
    name: "debug_commands_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5344,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "DebugCommandsListResp",
      className: "DebugCommandsListResp"
    }
  },
  {
    namespace: "workspace",
    name: "delete_agent",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1890,
    finalSchemaId: 354,
    req: {
      kind: "struct",
      name: "WorkspaceDeleteAgentReq",
      className: "WorkspaceDeleteAgentReq"
    },
    final: {
      kind: "struct",
      name: "AgentRef",
      className: "AgentRef"
    }
  },
  {
    namespace: "workspace",
    name: "delete_agent_kind",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1984,
    finalSchemaId: 1985,
    req: {
      kind: "struct",
      name: "WorkspaceDeleteAgentKindReq",
      className: "WorkspaceDeleteAgentKindReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceDeleteAgentKindResp",
      className: "WorkspaceDeleteAgentKindResp"
    }
  },
  {
    namespace: "workspace",
    name: "gate_approve",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3884,
    finalSchemaId: 3885,
    req: {
      kind: "struct",
      name: "WorkspaceGateApproveReq",
      className: "WorkspaceGateApproveReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGateApproveResp",
      className: "WorkspaceGateApproveResp"
    }
  },
  {
    namespace: "workspace",
    name: "gate_reject",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3886,
    finalSchemaId: 3887,
    req: {
      kind: "struct",
      name: "WorkspaceGateRejectReq",
      className: "WorkspaceGateRejectReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGateRejectResp",
      className: "WorkspaceGateRejectResp"
    }
  },
  {
    namespace: "workspace",
    name: "get_agent_kind_config",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1899,
    finalSchemaId: 1896,
    req: {
      kind: "struct",
      name: "WorkspaceGetAgentKindConfigReq",
      className: "WorkspaceGetAgentKindConfigReq"
    },
    final: {
      kind: "struct",
      name: "AgentKindConfig",
      className: "AgentKindConfig"
    }
  },
  {
    namespace: "workspace",
    name: "git_add",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1932,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitAddReq",
      className: "WorkspaceGitAddReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_amend",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1972,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitAmendReq",
      className: "WorkspaceGitAmendReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_blame",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1953,
    finalSchemaId: 1954,
    req: {
      kind: "struct",
      name: "WorkspaceGitBlameReq",
      className: "WorkspaceGitBlameReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitBlameResp",
      className: "WorkspaceGitBlameResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_branch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1937,
    finalSchemaId: 1938,
    req: {
      kind: "struct",
      name: "WorkspaceGitBranchReq",
      className: "WorkspaceGitBranchReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitBranchResp",
      className: "WorkspaceGitBranchResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_checkout",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1939,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitCheckoutReq",
      className: "WorkspaceGitCheckoutReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_commit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1933,
    finalSchemaId: 1934,
    req: {
      kind: "struct",
      name: "WorkspaceGitCommitReq",
      className: "WorkspaceGitCommitReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitCommitResp",
      className: "WorkspaceGitCommitResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_config_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1955,
    finalSchemaId: 1956,
    req: {
      kind: "struct",
      name: "WorkspaceGitConfigGetReq",
      className: "WorkspaceGitConfigGetReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitConfigGetResp",
      className: "WorkspaceGitConfigGetResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_config_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1957,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitConfigSetReq",
      className: "WorkspaceGitConfigSetReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_diff",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1930,
    finalSchemaId: 1931,
    req: {
      kind: "struct",
      name: "WorkspaceGitDiffReq",
      className: "WorkspaceGitDiffReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitDiffResp",
      className: "WorkspaceGitDiffResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_discard",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1971,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitDiscardReq",
      className: "WorkspaceGitDiscardReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_fetch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1970,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitFetchReq",
      className: "WorkspaceGitFetchReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_log",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1928,
    finalSchemaId: 1929,
    req: {
      kind: "struct",
      name: "WorkspaceGitLogReq",
      className: "WorkspaceGitLogReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitLogResp",
      className: "WorkspaceGitLogResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_merge",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1977,
    finalSchemaId: 1978,
    req: {
      kind: "struct",
      name: "WorkspaceGitMergeReq",
      className: "WorkspaceGitMergeReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitMergeResp",
      className: "WorkspaceGitMergeResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_pull",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1936,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitPullReq",
      className: "WorkspaceGitPullReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_push",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1935,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitPushReq",
      className: "WorkspaceGitPushReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_remote_add",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1950,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitRemoteAddReq",
      className: "WorkspaceGitRemoteAddReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_remote_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1948,
    finalSchemaId: 1949,
    req: {
      kind: "struct",
      name: "WorkspaceGitRemoteListReq",
      className: "WorkspaceGitRemoteListReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitRemoteListResp",
      className: "WorkspaceGitRemoteListResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_remote_remove",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1951,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitRemoteRemoveReq",
      className: "WorkspaceGitRemoteRemoveReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_reset",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1940,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitResetReq",
      className: "WorkspaceGitResetReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_show",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1968,
    finalSchemaId: 1969,
    req: {
      kind: "struct",
      name: "WorkspaceGitShowReq",
      className: "WorkspaceGitShowReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitShowResp",
      className: "WorkspaceGitShowResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_stash_drop",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1946,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitStashDropReq",
      className: "WorkspaceGitStashDropReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_stash_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1944,
    finalSchemaId: 1945,
    req: {
      kind: "struct",
      name: "WorkspaceGitStashListReq",
      className: "WorkspaceGitStashListReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitStashListResp",
      className: "WorkspaceGitStashListResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_stash_pop",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1943,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitStashPopReq",
      className: "WorkspaceGitStashPopReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_stash_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1942,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitStashSaveReq",
      className: "WorkspaceGitStashSaveReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1926,
    finalSchemaId: 1927,
    req: {
      kind: "struct",
      name: "WorkspaceGitStatusReq",
      className: "WorkspaceGitStatusReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitStatusResp",
      className: "WorkspaceGitStatusResp"
    }
  },
  {
    namespace: "workspace",
    name: "git_tag_create",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1975,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitTagCreateReq",
      className: "WorkspaceGitTagCreateReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_tag_delete",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1976,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "WorkspaceGitTagDeleteReq",
      className: "WorkspaceGitTagDeleteReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "git_tag_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1973,
    finalSchemaId: 1974,
    req: {
      kind: "struct",
      name: "WorkspaceGitTagListReq",
      className: "WorkspaceGitTagListReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceGitTagListResp",
      className: "WorkspaceGitTagListResp"
    }
  },
  {
    namespace: "workspace",
    name: "host_call",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1991,
    finalSchemaId: 1992,
    req: {
      kind: "struct",
      name: "WorkspaceHostCallReq",
      className: "WorkspaceHostCallReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceHostCallResp",
      className: "WorkspaceHostCallResp"
    }
  },
  {
    namespace: "workspace",
    name: "list_agent_kind_configs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1900,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceListAgentKindConfigsResp",
      className: "WorkspaceListAgentKindConfigsResp"
    }
  },
  {
    namespace: "workspace",
    name: "list_agent_kinds",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1898,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceListAgentKindsResp",
      className: "WorkspaceListAgentKindsResp"
    }
  },
  {
    namespace: "workspace",
    name: "list_agents",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1887,
    finalSchemaId: 356,
    req: {
      kind: "struct",
      name: "WorkspaceListAgentsReq",
      className: "WorkspaceListAgentsReq"
    },
    final: {
      kind: "struct",
      name: "AgentRefListResp",
      className: "AgentRefListResp"
    }
  },
  {
    namespace: "workspace",
    name: "list_project",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1901,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ProjectRefListResp",
      className: "ProjectRefListResp"
    }
  },
  {
    namespace: "workspace",
    name: "load_agent",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1960,
    finalSchemaId: 354,
    req: {
      kind: "struct",
      name: "WorkspaceLoadAgentReq",
      className: "WorkspaceLoadAgentReq"
    },
    final: {
      kind: "struct",
      name: "AgentRef",
      className: "AgentRef"
    }
  },
  {
    namespace: "workspace",
    name: "logs_query",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1962,
    finalSchemaId: 1963,
    req: {
      kind: "struct",
      name: "WorkspaceLogsQueryReq",
      className: "WorkspaceLogsQueryReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceLogsQueryResp",
      className: "WorkspaceLogsQueryResp"
    }
  },
  {
    namespace: "workspace",
    name: "mount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1874,
    finalSchemaId: 1873,
    req: {
      kind: "struct",
      name: "WorkspaceMountReq",
      className: "WorkspaceMountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectRef",
      className: "ProjectRef"
    }
  },
  {
    namespace: "workspace",
    name: "preferences_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1905,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AccountPreferencesSnapshot",
      className: "AccountPreferencesSnapshot"
    }
  },
  {
    namespace: "workspace",
    name: "preferences_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1906,
    finalSchemaId: 1905,
    req: {
      kind: "struct",
      name: "SaveAccountPreferencesCommand",
      className: "SaveAccountPreferencesCommand"
    },
    final: {
      kind: "struct",
      name: "AccountPreferencesSnapshot",
      className: "AccountPreferencesSnapshot"
    }
  },
  {
    namespace: "workspace",
    name: "remove_mount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1886,
    finalSchemaId: 1873,
    req: {
      kind: "struct",
      name: "WorkspaceRemoveMountReq",
      className: "WorkspaceRemoveMountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectRef",
      className: "ProjectRef"
    }
  },
  {
    namespace: "workspace",
    name: "report_error",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2481,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "FrontendErrorReport",
      className: "FrontendErrorReport"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "workspace",
    name: "save_agent_kind_config",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1897,
    finalSchemaId: 1896,
    req: {
      kind: "struct",
      name: "WorkspaceSaveAgentKindConfigReq",
      className: "WorkspaceSaveAgentKindConfigReq"
    },
    final: {
      kind: "struct",
      name: "AgentKindConfig",
      className: "AgentKindConfig"
    }
  },
  {
    namespace: "workspace",
    name: "session",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1904,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "SessionSnapshot",
      className: "SessionSnapshot"
    }
  },
  {
    namespace: "workspace",
    name: "shell_env_probe",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5041,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ShellEnvProbeResp",
      className: "ShellEnvProbeResp"
    }
  },
  {
    namespace: "workspace",
    name: "shell_pref_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5042,
    finalSchemaId: 5043,
    req: {
      kind: "struct",
      name: "ShellPrefSaveReq",
      className: "ShellPrefSaveReq"
    },
    final: {
      kind: "struct",
      name: "ShellPrefSaveResp",
      className: "ShellPrefSaveResp"
    }
  },
  {
    namespace: "workspace",
    name: "slash_commands_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 3634,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceSlashCommandsListResp",
      className: "WorkspaceSlashCommandsListResp"
    }
  },
  {
    namespace: "workspace",
    name: "system_tree",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1716,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "SystemTreeResp",
      className: "SystemTreeResp"
    }
  },
  {
    namespace: "workspace",
    name: "ui_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1916,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_ai_shell",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1920,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspaceAIShellCommand",
      className: "SaveWorkspaceAIShellCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_dock",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1919,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspaceDockCommand",
      className: "SaveWorkspaceDockCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_explorer",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1922,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspaceExplorerCommand",
      className: "SaveWorkspaceExplorerCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_layout",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1917,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspaceLayoutCommand",
      className: "SaveWorkspaceLayoutCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_panels",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1918,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspacePanelsCommand",
      className: "SaveWorkspacePanelsCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "ui_save_project_card_browser",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1921,
    finalSchemaId: 1916,
    req: {
      kind: "struct",
      name: "SaveWorkspaceProjectCardBrowserCommand",
      className: "SaveWorkspaceProjectCardBrowserCommand"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUIModel",
      className: "WorkspaceUIModel"
    }
  },
  {
    namespace: "workspace",
    name: "unmount",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1875,
    finalSchemaId: 1873,
    req: {
      kind: "struct",
      name: "WorkspaceUnmountReq",
      className: "WorkspaceUnmountReq"
    },
    final: {
      kind: "struct",
      name: "ProjectRef",
      className: "ProjectRef"
    }
  },
  {
    namespace: "workspace",
    name: "update_agent",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1889,
    finalSchemaId: 354,
    req: {
      kind: "struct",
      name: "WorkspaceUpdateAgentReq",
      className: "WorkspaceUpdateAgentReq"
    },
    final: {
      kind: "struct",
      name: "AgentRef",
      className: "AgentRef"
    }
  },
  {
    namespace: "workspace",
    name: "update_project",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1958,
    finalSchemaId: 1959,
    req: {
      kind: "struct",
      name: "WorkspaceUpdateProjectReq",
      className: "WorkspaceUpdateProjectReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceUpdateProjectResp",
      className: "WorkspaceUpdateProjectResp"
    }
  },
  {
    namespace: "workspace",
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
    namespace: "workspace",
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
    namespace: "workspace",
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
    namespace: "workspace",
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
    namespace: "workspace",
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
    namespace: "workspace",
    name: "wiki_list_starred",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1989,
    finalSchemaId: 1990,
    req: {
      kind: "struct",
      name: "WikiListStarredReq",
      className: "WikiListStarredReq"
    },
    final: {
      kind: "struct",
      name: "WikiListStarredResp",
      className: "WikiListStarredResp"
    }
  },
  {
    namespace: "workspace",
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
    namespace: "workspace",
    name: "workflow_start",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3882,
    finalSchemaId: 3883,
    req: {
      kind: "struct",
      name: "WorkspaceWorkflowStartReq",
      className: "WorkspaceWorkflowStartReq"
    },
    final: {
      kind: "struct",
      name: "WorkspaceWorkflowStartResp",
      className: "WorkspaceWorkflowStartResp"
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
