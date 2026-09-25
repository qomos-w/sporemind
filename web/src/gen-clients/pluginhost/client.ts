// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function appdataUsage(client: GosporeClient, req: systemTypes.PluginAppDataUsageReq, opts?: InvokeOptions): Promise<systemTypes.PluginAppDataUsageResp> {
  return client.invoke<systemTypes.PluginAppDataUsageReq, systemTypes.PluginAppDataUsageResp>("pluginhost.appdata_usage", req, { reqSchemaId: 3203, resSchemaId: 3204, ...opts });
}

export const appdataUsage_meta = {
  callable: "pluginhost.appdata_usage",
  name: "appdata_usage",
  reqSchemaId: 3203,
  resSchemaId: 3204,
} as const;

export async function artifactLoad(client: GosporeClient, req: systemTypes.PluginArtifactLoadReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactLoadResp> {
  return client.invoke<systemTypes.PluginArtifactLoadReq, systemTypes.PluginArtifactLoadResp>("pluginhost.artifact_load", req, { reqSchemaId: 3172, resSchemaId: 3173, ...opts });
}

export const artifactLoad_meta = {
  callable: "pluginhost.artifact_load",
  name: "artifact_load",
  reqSchemaId: 3172,
  resSchemaId: 3173,
} as const;

export async function artifactReloadAbort(client: GosporeClient, req: systemTypes.PluginArtifactReloadAbortReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadAbortResp> {
  return client.invoke<systemTypes.PluginArtifactReloadAbortReq, systemTypes.PluginArtifactReloadAbortResp>("pluginhost.artifact_reload_abort", req, { reqSchemaId: 3178, resSchemaId: 3179, ...opts });
}

export const artifactReloadAbort_meta = {
  callable: "pluginhost.artifact_reload_abort",
  name: "artifact_reload_abort",
  reqSchemaId: 3178,
  resSchemaId: 3179,
} as const;

export async function artifactReloadCommit(client: GosporeClient, req: systemTypes.PluginArtifactReloadCommitReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadCommitResp> {
  return client.invoke<systemTypes.PluginArtifactReloadCommitReq, systemTypes.PluginArtifactReloadCommitResp>("pluginhost.artifact_reload_commit", req, { reqSchemaId: 3176, resSchemaId: 3177, ...opts });
}

export const artifactReloadCommit_meta = {
  callable: "pluginhost.artifact_reload_commit",
  name: "artifact_reload_commit",
  reqSchemaId: 3176,
  resSchemaId: 3177,
} as const;

export async function artifactReloadPrepare(client: GosporeClient, req: systemTypes.PluginArtifactReloadPrepareReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadPrepareResp> {
  return client.invoke<systemTypes.PluginArtifactReloadPrepareReq, systemTypes.PluginArtifactReloadPrepareResp>("pluginhost.artifact_reload_prepare", req, { reqSchemaId: 3174, resSchemaId: 3175, ...opts });
}

export const artifactReloadPrepare_meta = {
  callable: "pluginhost.artifact_reload_prepare",
  name: "artifact_reload_prepare",
  reqSchemaId: 3174,
  resSchemaId: 3175,
} as const;

export async function artifactUnload(client: GosporeClient, req: systemTypes.PluginArtifactUnloadReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactUnloadResp> {
  return client.invoke<systemTypes.PluginArtifactUnloadReq, systemTypes.PluginArtifactUnloadResp>("pluginhost.artifact_unload", req, { reqSchemaId: 3180, resSchemaId: 3181, ...opts });
}

export const artifactUnload_meta = {
  callable: "pluginhost.artifact_unload",
  name: "artifact_unload",
  reqSchemaId: 3180,
  resSchemaId: 3181,
} as const;

export async function assetsPut(client: GosporeClient, req: systemTypes.PluginAssetsPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginAssetsPutResp> {
  return client.invoke<systemTypes.PluginAssetsPutReq, systemTypes.PluginAssetsPutResp>("pluginhost.assets_put", req, { reqSchemaId: 3182, resSchemaId: 3183, ...opts });
}

export const assetsPut_meta = {
  callable: "pluginhost.assets_put",
  name: "assets_put",
  reqSchemaId: 3182,
  resSchemaId: 3183,
} as const;

export async function assetsRemove(client: GosporeClient, req: systemTypes.PluginAssetsRemoveReq, opts?: InvokeOptions): Promise<systemTypes.PluginAssetsRemoveResp> {
  return client.invoke<systemTypes.PluginAssetsRemoveReq, systemTypes.PluginAssetsRemoveResp>("pluginhost.assets_remove", req, { reqSchemaId: 3184, resSchemaId: 3185, ...opts });
}

export const assetsRemove_meta = {
  callable: "pluginhost.assets_remove",
  name: "assets_remove",
  reqSchemaId: 3184,
  resSchemaId: 3185,
} as const;

export async function eventDeliver(client: GosporeClient, req: systemTypes.PluginEventDeliverReq, opts?: InvokeOptions): Promise<systemTypes.PluginEventDeliverResp> {
  return client.invoke<systemTypes.PluginEventDeliverReq, systemTypes.PluginEventDeliverResp>("pluginhost.event_deliver", req, { reqSchemaId: 2330, resSchemaId: 2331, ...opts });
}

export const eventDeliver_meta = {
  callable: "pluginhost.event_deliver",
  name: "event_deliver",
  reqSchemaId: 2330,
  resSchemaId: 2331,
} as const;

export async function invoke(client: GosporeClient, req: systemTypes.PluginInvokeReq, opts?: InvokeOptions): Promise<systemTypes.PluginInvokeResp> {
  return client.invoke<systemTypes.PluginInvokeReq, systemTypes.PluginInvokeResp>("pluginhost.invoke", req, { reqSchemaId: 2327, resSchemaId: 2328, ...opts });
}

export const invoke_meta = {
  callable: "pluginhost.invoke",
  name: "invoke",
  reqSchemaId: 2327,
  resSchemaId: 2328,
} as const;

export async function *invokeStream(client: GosporeClient, req: systemTypes.PluginInvokeReq, opts?: InvokeOptions): AsyncIterable<systemTypes.PluginInvokeChunk> {
  yield* client.subscribe<systemTypes.PluginInvokeChunk>("pluginhost.invoke_stream", req, { reqSchemaId: 2327, chunkSchemaId: 2329, ...opts });
}

export async function listPlugins(client: GosporeClient, req: systemTypes.listPluginsReq, opts?: InvokeOptions): Promise<systemTypes.ListPluginsResp> {
  return client.invoke<systemTypes.listPluginsReq, systemTypes.ListPluginsResp>("pluginhost.list_plugins", req, { reqSchemaId: 1802, resSchemaId: 1781, ...opts });
}

export const listPlugins_meta = {
  callable: "pluginhost.list_plugins",
  name: "list_plugins",
  reqSchemaId: 1802,
  resSchemaId: 1781,
} as const;

export async function nativeBuild(client: GosporeClient, req: systemTypes.NativeBuildReq, opts?: InvokeOptions): Promise<systemTypes.NativeBuildResp> {
  return client.invoke<systemTypes.NativeBuildReq, systemTypes.NativeBuildResp>("pluginhost.native_build", req, { reqSchemaId: 3170, resSchemaId: 3171, ...opts });
}

export const nativeBuild_meta = {
  callable: "pluginhost.native_build",
  name: "native_build",
  reqSchemaId: 3170,
  resSchemaId: 3171,
} as const;

export async function panelOp(client: GosporeClient, req: systemTypes.PluginPanelOpReq, opts?: InvokeOptions): Promise<systemTypes.PluginPanelOpResp> {
  return client.invoke<systemTypes.PluginPanelOpReq, systemTypes.PluginPanelOpResp>("pluginhost.panel_op", req, { reqSchemaId: 3968, resSchemaId: 3969, ...opts });
}

export const panelOp_meta = {
  callable: "pluginhost.panel_op",
  name: "panel_op",
  reqSchemaId: 3968,
  resSchemaId: 3969,
} as const;

export async function panelOpPut(client: GosporeClient, req: systemTypes.PluginPanelOpPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginPanelOpPutResp> {
  return client.invoke<systemTypes.PluginPanelOpPutReq, systemTypes.PluginPanelOpPutResp>("pluginhost.panel_op_put", req, { reqSchemaId: 3970, resSchemaId: 3971, ...opts });
}

export const panelOpPut_meta = {
  callable: "pluginhost.panel_op_put",
  name: "panel_op_put",
  reqSchemaId: 3970,
  resSchemaId: 3971,
} as const;

export async function pluginDom(client: GosporeClient, req: systemTypes.PluginDomReq, opts?: InvokeOptions): Promise<systemTypes.PluginDomResp> {
  return client.invoke<systemTypes.PluginDomReq, systemTypes.PluginDomResp>("pluginhost.plugin_dom", req, { reqSchemaId: 3920, resSchemaId: 3921, ...opts });
}

export const pluginDom_meta = {
  callable: "pluginhost.plugin_dom",
  name: "plugin_dom",
  reqSchemaId: 3920,
  resSchemaId: 3921,
} as const;

export async function pluginDomPut(client: GosporeClient, req: systemTypes.PluginDomPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginDomPutResp> {
  return client.invoke<systemTypes.PluginDomPutReq, systemTypes.PluginDomPutResp>("pluginhost.plugin_dom_put", req, { reqSchemaId: 3922, resSchemaId: 3923, ...opts });
}

export const pluginDomPut_meta = {
  callable: "pluginhost.plugin_dom_put",
  name: "plugin_dom_put",
  reqSchemaId: 3922,
  resSchemaId: 3923,
} as const;

export async function pluginLogPut(client: GosporeClient, req: systemTypes.PluginLogPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginLogsResp> {
  return client.invoke<systemTypes.PluginLogPutReq, systemTypes.PluginLogsResp>("pluginhost.plugin_log_put", req, { reqSchemaId: 2333, resSchemaId: 2336, ...opts });
}

export const pluginLogPut_meta = {
  callable: "pluginhost.plugin_log_put",
  name: "plugin_log_put",
  reqSchemaId: 2333,
  resSchemaId: 2336,
} as const;

export async function pluginLogs(client: GosporeClient, req: systemTypes.PluginLogsReq, opts?: InvokeOptions): Promise<systemTypes.PluginLogsResp> {
  return client.invoke<systemTypes.PluginLogsReq, systemTypes.PluginLogsResp>("pluginhost.plugin_logs", req, { reqSchemaId: 2335, resSchemaId: 2336, ...opts });
}

export const pluginLogs_meta = {
  callable: "pluginhost.plugin_logs",
  name: "plugin_logs",
  reqSchemaId: 2335,
  resSchemaId: 2336,
} as const;

export async function proxyAttach(client: GosporeClient, req: systemTypes.PluginProxyAttachReq, opts?: InvokeOptions): Promise<systemTypes.PluginProxyAttachResp> {
  return client.invoke<systemTypes.PluginProxyAttachReq, systemTypes.PluginProxyAttachResp>("pluginhost.proxy_attach", req, { reqSchemaId: 6016, resSchemaId: 6017, ...opts });
}

export const proxyAttach_meta = {
  callable: "pluginhost.proxy_attach",
  name: "proxy_attach",
  reqSchemaId: 6016,
  resSchemaId: 6017,
} as const;

export async function proxyDetach(client: GosporeClient, req: systemTypes.PluginProxyDetachReq, opts?: InvokeOptions): Promise<systemTypes.PluginProxyDetachResp> {
  return client.invoke<systemTypes.PluginProxyDetachReq, systemTypes.PluginProxyDetachResp>("pluginhost.proxy_detach", req, { reqSchemaId: 6018, resSchemaId: 6019, ...opts });
}

export const proxyDetach_meta = {
  callable: "pluginhost.proxy_detach",
  name: "proxy_detach",
  reqSchemaId: 6018,
  resSchemaId: 6019,
} as const;

export async function registerActor(client: GosporeClient, req: systemTypes.registerActorReq, opts?: InvokeOptions): Promise<systemTypes.registerActorRes> {
  return client.invoke<systemTypes.registerActorReq, systemTypes.registerActorRes>("pluginhost.register_actor", req, { reqSchemaId: 1127, resSchemaId: 1128, ...opts });
}

export const registerActor_meta = {
  callable: "pluginhost.register_actor",
  name: "register_actor",
  reqSchemaId: 1127,
  resSchemaId: 1128,
} as const;

export async function stateAppend(client: GosporeClient, req: systemTypes.PluginStateAppendReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateAppendResp> {
  return client.invoke<systemTypes.PluginStateAppendReq, systemTypes.PluginStateAppendResp>("pluginhost.state_append", req, { reqSchemaId: 3194, resSchemaId: 3195, ...opts });
}

export const stateAppend_meta = {
  callable: "pluginhost.state_append",
  name: "state_append",
  reqSchemaId: 3194,
  resSchemaId: 3195,
} as const;

export async function stateDelete(client: GosporeClient, req: systemTypes.PluginStateDeleteReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateDeleteResp> {
  return client.invoke<systemTypes.PluginStateDeleteReq, systemTypes.PluginStateDeleteResp>("pluginhost.state_delete", req, { reqSchemaId: 3190, resSchemaId: 3191, ...opts });
}

export const stateDelete_meta = {
  callable: "pluginhost.state_delete",
  name: "state_delete",
  reqSchemaId: 3190,
  resSchemaId: 3191,
} as const;

export async function stateGet(client: GosporeClient, req: systemTypes.PluginStateGetReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateGetResp> {
  return client.invoke<systemTypes.PluginStateGetReq, systemTypes.PluginStateGetResp>("pluginhost.state_get", req, { reqSchemaId: 3186, resSchemaId: 3187, ...opts });
}

export const stateGet_meta = {
  callable: "pluginhost.state_get",
  name: "state_get",
  reqSchemaId: 3186,
  resSchemaId: 3187,
} as const;

export async function stateGetMany(client: GosporeClient, req: systemTypes.PluginStateGetManyReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateGetManyResp> {
  return client.invoke<systemTypes.PluginStateGetManyReq, systemTypes.PluginStateGetManyResp>("pluginhost.state_get_many", req, { reqSchemaId: 3196, resSchemaId: 3197, ...opts });
}

export const stateGetMany_meta = {
  callable: "pluginhost.state_get_many",
  name: "state_get_many",
  reqSchemaId: 3196,
  resSchemaId: 3197,
} as const;

export async function stateList(client: GosporeClient, req: systemTypes.PluginStateListReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateListResp> {
  return client.invoke<systemTypes.PluginStateListReq, systemTypes.PluginStateListResp>("pluginhost.state_list", req, { reqSchemaId: 3192, resSchemaId: 3193, ...opts });
}

export const stateList_meta = {
  callable: "pluginhost.state_list",
  name: "state_list",
  reqSchemaId: 3192,
  resSchemaId: 3193,
} as const;

export async function statePurge(client: GosporeClient, req: systemTypes.PluginStatePurgeReq, opts?: InvokeOptions): Promise<systemTypes.PluginStatePurgeResp> {
  return client.invoke<systemTypes.PluginStatePurgeReq, systemTypes.PluginStatePurgeResp>("pluginhost.state_purge", req, { reqSchemaId: 3200, resSchemaId: 3201, ...opts });
}

export const statePurge_meta = {
  callable: "pluginhost.state_purge",
  name: "state_purge",
  reqSchemaId: 3200,
  resSchemaId: 3201,
} as const;

export async function stateSet(client: GosporeClient, req: systemTypes.PluginStateSetReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateSetResp> {
  return client.invoke<systemTypes.PluginStateSetReq, systemTypes.PluginStateSetResp>("pluginhost.state_set", req, { reqSchemaId: 3188, resSchemaId: 3189, ...opts });
}

export const stateSet_meta = {
  callable: "pluginhost.state_set",
  name: "state_set",
  reqSchemaId: 3188,
  resSchemaId: 3189,
} as const;

export async function stateSetMany(client: GosporeClient, req: systemTypes.PluginStateSetManyReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateSetManyResp> {
  return client.invoke<systemTypes.PluginStateSetManyReq, systemTypes.PluginStateSetManyResp>("pluginhost.state_set_many", req, { reqSchemaId: 3198, resSchemaId: 3199, ...opts });
}

export const stateSetMany_meta = {
  callable: "pluginhost.state_set_many",
  name: "state_set_many",
  reqSchemaId: 3198,
  resSchemaId: 3199,
} as const;

export async function unregisterActor(client: GosporeClient, req: systemTypes.unregisterActorReq, opts?: InvokeOptions): Promise<systemTypes.unregisterActorRes> {
  return client.invoke<systemTypes.unregisterActorReq, systemTypes.unregisterActorRes>("pluginhost.unregister_actor", req, { reqSchemaId: 1129, resSchemaId: 1130, ...opts });
}

export const unregisterActor_meta = {
  callable: "pluginhost.unregister_actor",
  name: "unregister_actor",
  reqSchemaId: 1129,
  resSchemaId: 1130,
} as const;

