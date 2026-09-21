// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "pluginhost",
    name: "appdata_usage",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 3123,
    finalSchemaId: 3124,
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
    reqSchemaId: 3092,
    finalSchemaId: 3093,
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
    reqSchemaId: 3098,
    finalSchemaId: 3099,
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
    reqSchemaId: 3096,
    finalSchemaId: 3097,
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
    reqSchemaId: 3094,
    finalSchemaId: 3095,
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
    reqSchemaId: 3100,
    finalSchemaId: 3101,
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
    reqSchemaId: 3102,
    finalSchemaId: 3103,
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
    reqSchemaId: 3104,
    finalSchemaId: 3105,
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
    reqSchemaId: 2250,
    finalSchemaId: 2251,
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
    reqSchemaId: 2247,
    finalSchemaId: 2248,
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
    reqSchemaId: 2247,
    chunkSchemaId: 2249,
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
    reqSchemaId: 3090,
    finalSchemaId: 3091,
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
    name: "plugin_dom",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3840,
    finalSchemaId: 3841,
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
    reqSchemaId: 3842,
    finalSchemaId: 3843,
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
    reqSchemaId: 2253,
    finalSchemaId: 2256,
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
    reqSchemaId: 2255,
    finalSchemaId: 2256,
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
    reqSchemaId: 5872,
    finalSchemaId: 5873,
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
    reqSchemaId: 5874,
    finalSchemaId: 5875,
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
    reqSchemaId: 3114,
    finalSchemaId: 3115,
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
    reqSchemaId: 3110,
    finalSchemaId: 3111,
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
    reqSchemaId: 3106,
    finalSchemaId: 3107,
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
    reqSchemaId: 3116,
    finalSchemaId: 3117,
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
    reqSchemaId: 3112,
    finalSchemaId: 3113,
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
    reqSchemaId: 3120,
    finalSchemaId: 3121,
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
    reqSchemaId: 3108,
    finalSchemaId: 3109,
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
    reqSchemaId: 3118,
    finalSchemaId: 3119,
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
