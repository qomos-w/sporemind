// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "pluginhost",
    name: "appdata_usage",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3203,
    finalSchemaId: 3204,
    req: {
      kind: "struct",
      name: "PluginAppDataUsageReq",
      className: "PluginAppDataUsageReq"
    },
    final: {
      kind: "struct",
      name: "PluginAppDataUsageResp",
      className: "PluginAppDataUsageResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "artifact_load",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3172,
    finalSchemaId: 3173,
    req: {
      kind: "struct",
      name: "PluginArtifactLoadReq",
      className: "PluginArtifactLoadReq"
    },
    final: {
      kind: "struct",
      name: "PluginArtifactLoadResp",
      className: "PluginArtifactLoadResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "artifact_reload_abort",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3178,
    finalSchemaId: 3179,
    req: {
      kind: "struct",
      name: "PluginArtifactReloadAbortReq",
      className: "PluginArtifactReloadAbortReq"
    },
    final: {
      kind: "struct",
      name: "PluginArtifactReloadAbortResp",
      className: "PluginArtifactReloadAbortResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "artifact_reload_commit",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3176,
    finalSchemaId: 3177,
    req: {
      kind: "struct",
      name: "PluginArtifactReloadCommitReq",
      className: "PluginArtifactReloadCommitReq"
    },
    final: {
      kind: "struct",
      name: "PluginArtifactReloadCommitResp",
      className: "PluginArtifactReloadCommitResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "artifact_reload_prepare",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3174,
    finalSchemaId: 3175,
    req: {
      kind: "struct",
      name: "PluginArtifactReloadPrepareReq",
      className: "PluginArtifactReloadPrepareReq"
    },
    final: {
      kind: "struct",
      name: "PluginArtifactReloadPrepareResp",
      className: "PluginArtifactReloadPrepareResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "artifact_unload",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3180,
    finalSchemaId: 3181,
    req: {
      kind: "struct",
      name: "PluginArtifactUnloadReq",
      className: "PluginArtifactUnloadReq"
    },
    final: {
      kind: "struct",
      name: "PluginArtifactUnloadResp",
      className: "PluginArtifactUnloadResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "assets_put",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3182,
    finalSchemaId: 3183,
    req: {
      kind: "struct",
      name: "PluginAssetsPutReq",
      className: "PluginAssetsPutReq"
    },
    final: {
      kind: "struct",
      name: "PluginAssetsPutResp",
      className: "PluginAssetsPutResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "assets_remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3184,
    finalSchemaId: 3185,
    req: {
      kind: "struct",
      name: "PluginAssetsRemoveReq",
      className: "PluginAssetsRemoveReq"
    },
    final: {
      kind: "struct",
      name: "PluginAssetsRemoveResp",
      className: "PluginAssetsRemoveResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "event_deliver",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2330,
    finalSchemaId: 2331,
    req: {
      kind: "struct",
      name: "PluginEventDeliverReq",
      className: "PluginEventDeliverReq"
    },
    final: {
      kind: "struct",
      name: "PluginEventDeliverResp",
      className: "PluginEventDeliverResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "invoke",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2327,
    finalSchemaId: 2328,
    req: {
      kind: "struct",
      name: "PluginInvokeReq",
      className: "PluginInvokeReq"
    },
    final: {
      kind: "struct",
      name: "PluginInvokeResp",
      className: "PluginInvokeResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "invoke_stream",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 2327,
    chunkSchemaId: 2329,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "PluginInvokeReq",
      className: "PluginInvokeReq"
    },
    chunk: {
      kind: "struct",
      name: "PluginInvokeChunk",
      className: "PluginInvokeChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "pluginhost",
    name: "list_plugins",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1802,
    finalSchemaId: 1781,
    req: {
      kind: "struct",
      name: "listPluginsReq",
      className: "listPluginsReq"
    },
    final: {
      kind: "struct",
      name: "ListPluginsResp",
      className: "ListPluginsResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "native_build",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3170,
    finalSchemaId: 3171,
    req: {
      kind: "struct",
      name: "NativeBuildReq",
      className: "NativeBuildReq"
    },
    final: {
      kind: "struct",
      name: "NativeBuildResp",
      className: "NativeBuildResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "panel_op",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3968,
    finalSchemaId: 3969,
    req: {
      kind: "struct",
      name: "PluginPanelOpReq",
      className: "PluginPanelOpReq"
    },
    final: {
      kind: "struct",
      name: "PluginPanelOpResp",
      className: "PluginPanelOpResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "panel_op_put",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3970,
    finalSchemaId: 3971,
    req: {
      kind: "struct",
      name: "PluginPanelOpPutReq",
      className: "PluginPanelOpPutReq"
    },
    final: {
      kind: "struct",
      name: "PluginPanelOpPutResp",
      className: "PluginPanelOpPutResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "plugin_dom",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3920,
    finalSchemaId: 3921,
    req: {
      kind: "struct",
      name: "PluginDomReq",
      className: "PluginDomReq"
    },
    final: {
      kind: "struct",
      name: "PluginDomResp",
      className: "PluginDomResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "plugin_dom_put",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3922,
    finalSchemaId: 3923,
    req: {
      kind: "struct",
      name: "PluginDomPutReq",
      className: "PluginDomPutReq"
    },
    final: {
      kind: "struct",
      name: "PluginDomPutResp",
      className: "PluginDomPutResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "plugin_log_put",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2333,
    finalSchemaId: 2336,
    req: {
      kind: "struct",
      name: "PluginLogPutReq",
      className: "PluginLogPutReq"
    },
    final: {
      kind: "struct",
      name: "PluginLogsResp",
      className: "PluginLogsResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "plugin_logs",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2335,
    finalSchemaId: 2336,
    req: {
      kind: "struct",
      name: "PluginLogsReq",
      className: "PluginLogsReq"
    },
    final: {
      kind: "struct",
      name: "PluginLogsResp",
      className: "PluginLogsResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "proxy_attach",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6016,
    finalSchemaId: 6017,
    req: {
      kind: "struct",
      name: "PluginProxyAttachReq",
      className: "PluginProxyAttachReq"
    },
    final: {
      kind: "struct",
      name: "PluginProxyAttachResp",
      className: "PluginProxyAttachResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "proxy_detach",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6018,
    finalSchemaId: 6019,
    req: {
      kind: "struct",
      name: "PluginProxyDetachReq",
      className: "PluginProxyDetachReq"
    },
    final: {
      kind: "struct",
      name: "PluginProxyDetachResp",
      className: "PluginProxyDetachResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "register_actor",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1127,
    finalSchemaId: 1128,
    req: {
      kind: "struct",
      name: "registerActorReq",
      className: "registerActorReq"
    },
    final: {
      kind: "struct",
      name: "registerActorRes",
      className: "registerActorRes"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_append",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3194,
    finalSchemaId: 3195,
    req: {
      kind: "struct",
      name: "PluginStateAppendReq",
      className: "PluginStateAppendReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateAppendResp",
      className: "PluginStateAppendResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3190,
    finalSchemaId: 3191,
    req: {
      kind: "struct",
      name: "PluginStateDeleteReq",
      className: "PluginStateDeleteReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateDeleteResp",
      className: "PluginStateDeleteResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_get",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3186,
    finalSchemaId: 3187,
    req: {
      kind: "struct",
      name: "PluginStateGetReq",
      className: "PluginStateGetReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateGetResp",
      className: "PluginStateGetResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_get_many",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3196,
    finalSchemaId: 3197,
    req: {
      kind: "struct",
      name: "PluginStateGetManyReq",
      className: "PluginStateGetManyReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateGetManyResp",
      className: "PluginStateGetManyResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3192,
    finalSchemaId: 3193,
    req: {
      kind: "struct",
      name: "PluginStateListReq",
      className: "PluginStateListReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateListResp",
      className: "PluginStateListResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_purge",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3200,
    finalSchemaId: 3201,
    req: {
      kind: "struct",
      name: "PluginStatePurgeReq",
      className: "PluginStatePurgeReq"
    },
    final: {
      kind: "struct",
      name: "PluginStatePurgeResp",
      className: "PluginStatePurgeResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3188,
    finalSchemaId: 3189,
    req: {
      kind: "struct",
      name: "PluginStateSetReq",
      className: "PluginStateSetReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateSetResp",
      className: "PluginStateSetResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "state_set_many",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3198,
    finalSchemaId: 3199,
    req: {
      kind: "struct",
      name: "PluginStateSetManyReq",
      className: "PluginStateSetManyReq"
    },
    final: {
      kind: "struct",
      name: "PluginStateSetManyResp",
      className: "PluginStateSetManyResp"
    }
  },
  {
    namespace: "pluginhost",
    name: "unregister_actor",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1129,
    finalSchemaId: 1130,
    req: {
      kind: "struct",
      name: "unregisterActorReq",
      className: "unregisterActorReq"
    },
    final: {
      kind: "struct",
      name: "unregisterActorRes",
      className: "unregisterActorRes"
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
