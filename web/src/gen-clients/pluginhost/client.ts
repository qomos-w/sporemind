// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function appdataUsage(client: GosporeClient, req: systemTypes.PluginAppDataUsageReq, opts?: InvokeOptions): Promise<systemTypes.PluginAppDataUsageResp> {
  return client.invoke<systemTypes.PluginAppDataUsageReq, systemTypes.PluginAppDataUsageResp>("pluginhost.appdata_usage", req, { reqSchemaId: 3123, resSchemaId: 3124, ...opts });
}

export const appdataUsage_meta = {
  callable: "pluginhost.appdata_usage",
  name: "appdata_usage",
  reqSchemaId: 3123,
  resSchemaId: 3124,
} as const;

export async function artifactLoad(client: GosporeClient, req: systemTypes.PluginArtifactLoadReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactLoadResp> {
  return client.invoke<systemTypes.PluginArtifactLoadReq, systemTypes.PluginArtifactLoadResp>("pluginhost.artifact_load", req, { reqSchemaId: 3092, resSchemaId: 3093, ...opts });
}

export const artifactLoad_meta = {
  callable: "pluginhost.artifact_load",
  name: "artifact_load",
  reqSchemaId: 3092,
  resSchemaId: 3093,
} as const;

export async function artifactReloadAbort(client: GosporeClient, req: systemTypes.PluginArtifactReloadAbortReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadAbortResp> {
  return client.invoke<systemTypes.PluginArtifactReloadAbortReq, systemTypes.PluginArtifactReloadAbortResp>("pluginhost.artifact_reload_abort", req, { reqSchemaId: 3098, resSchemaId: 3099, ...opts });
}

export const artifactReloadAbort_meta = {
  callable: "pluginhost.artifact_reload_abort",
  name: "artifact_reload_abort",
  reqSchemaId: 3098,
  resSchemaId: 3099,
} as const;

export async function artifactReloadCommit(client: GosporeClient, req: systemTypes.PluginArtifactReloadCommitReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadCommitResp> {
  return client.invoke<systemTypes.PluginArtifactReloadCommitReq, systemTypes.PluginArtifactReloadCommitResp>("pluginhost.artifact_reload_commit", req, { reqSchemaId: 3096, resSchemaId: 3097, ...opts });
}

export const artifactReloadCommit_meta = {
  callable: "pluginhost.artifact_reload_commit",
  name: "artifact_reload_commit",
  reqSchemaId: 3096,
  resSchemaId: 3097,
} as const;

export async function artifactReloadPrepare(client: GosporeClient, req: systemTypes.PluginArtifactReloadPrepareReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactReloadPrepareResp> {
  return client.invoke<systemTypes.PluginArtifactReloadPrepareReq, systemTypes.PluginArtifactReloadPrepareResp>("pluginhost.artifact_reload_prepare", req, { reqSchemaId: 3094, resSchemaId: 3095, ...opts });
}

export const artifactReloadPrepare_meta = {
  callable: "pluginhost.artifact_reload_prepare",
  name: "artifact_reload_prepare",
  reqSchemaId: 3094,
  resSchemaId: 3095,
} as const;

export async function artifactUnload(client: GosporeClient, req: systemTypes.PluginArtifactUnloadReq, opts?: InvokeOptions): Promise<systemTypes.PluginArtifactUnloadResp> {
  return client.invoke<systemTypes.PluginArtifactUnloadReq, systemTypes.PluginArtifactUnloadResp>("pluginhost.artifact_unload", req, { reqSchemaId: 3100, resSchemaId: 3101, ...opts });
}

export const artifactUnload_meta = {
  callable: "pluginhost.artifact_unload",
  name: "artifact_unload",
  reqSchemaId: 3100,
  resSchemaId: 3101,
} as const;

export async function assetsPut(client: GosporeClient, req: systemTypes.PluginAssetsPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginAssetsPutResp> {
  return client.invoke<systemTypes.PluginAssetsPutReq, systemTypes.PluginAssetsPutResp>("pluginhost.assets_put", req, { reqSchemaId: 3102, resSchemaId: 3103, ...opts });
}

export const assetsPut_meta = {
  callable: "pluginhost.assets_put",
  name: "assets_put",
  reqSchemaId: 3102,
  resSchemaId: 3103,
} as const;

export async function assetsRemove(client: GosporeClient, req: systemTypes.PluginAssetsRemoveReq, opts?: InvokeOptions): Promise<systemTypes.PluginAssetsRemoveResp> {
  return client.invoke<systemTypes.PluginAssetsRemoveReq, systemTypes.PluginAssetsRemoveResp>("pluginhost.assets_remove", req, { reqSchemaId: 3104, resSchemaId: 3105, ...opts });
}

export const assetsRemove_meta = {
  callable: "pluginhost.assets_remove",
  name: "assets_remove",
  reqSchemaId: 3104,
  resSchemaId: 3105,
} as const;

export async function eventDeliver(client: GosporeClient, req: systemTypes.PluginEventDeliverReq, opts?: InvokeOptions): Promise<systemTypes.PluginEventDeliverResp> {
  return client.invoke<systemTypes.PluginEventDeliverReq, systemTypes.PluginEventDeliverResp>("pluginhost.event_deliver", req, { reqSchemaId: 2250, resSchemaId: 2251, ...opts });
}

export const eventDeliver_meta = {
  callable: "pluginhost.event_deliver",
  name: "event_deliver",
  reqSchemaId: 2250,
  resSchemaId: 2251,
} as const;

export async function invoke(client: GosporeClient, req: systemTypes.PluginInvokeReq, opts?: InvokeOptions): Promise<systemTypes.PluginInvokeResp> {
  return client.invoke<systemTypes.PluginInvokeReq, systemTypes.PluginInvokeResp>("pluginhost.invoke", req, { reqSchemaId: 2247, resSchemaId: 2248, ...opts });
}

export const invoke_meta = {
  callable: "pluginhost.invoke",
  name: "invoke",
  reqSchemaId: 2247,
  resSchemaId: 2248,
} as const;

export async function *invokeStream(client: GosporeClient, req: systemTypes.PluginInvokeReq, opts?: InvokeOptions): AsyncIterable<systemTypes.PluginInvokeChunk> {
  yield* client.subscribe<systemTypes.PluginInvokeChunk>("pluginhost.invoke_stream", req, { reqSchemaId: 2247, chunkSchemaId: 2249, ...opts });
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
  return client.invoke<systemTypes.NativeBuildReq, systemTypes.NativeBuildResp>("pluginhost.native_build", req, { reqSchemaId: 3090, resSchemaId: 3091, ...opts });
}

export const nativeBuild_meta = {
  callable: "pluginhost.native_build",
  name: "native_build",
  reqSchemaId: 3090,
  resSchemaId: 3091,
} as const;

export async function pluginDom(client: GosporeClient, req: systemTypes.PluginDomReq, opts?: InvokeOptions): Promise<systemTypes.PluginDomResp> {
  return client.invoke<systemTypes.PluginDomReq, systemTypes.PluginDomResp>("pluginhost.plugin_dom", req, { reqSchemaId: 3840, resSchemaId: 3841, ...opts });
}

export const pluginDom_meta = {
  callable: "pluginhost.plugin_dom",
  name: "plugin_dom",
  reqSchemaId: 3840,
  resSchemaId: 3841,
} as const;

export async function pluginDomPut(client: GosporeClient, req: systemTypes.PluginDomPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginDomPutResp> {
  return client.invoke<systemTypes.PluginDomPutReq, systemTypes.PluginDomPutResp>("pluginhost.plugin_dom_put", req, { reqSchemaId: 3842, resSchemaId: 3843, ...opts });
}

export const pluginDomPut_meta = {
  callable: "pluginhost.plugin_dom_put",
  name: "plugin_dom_put",
  reqSchemaId: 3842,
  resSchemaId: 3843,
} as const;

export async function pluginLogPut(client: GosporeClient, req: systemTypes.PluginLogPutReq, opts?: InvokeOptions): Promise<systemTypes.PluginLogsResp> {
  return client.invoke<systemTypes.PluginLogPutReq, systemTypes.PluginLogsResp>("pluginhost.plugin_log_put", req, { reqSchemaId: 2253, resSchemaId: 2256, ...opts });
}

export const pluginLogPut_meta = {
  callable: "pluginhost.plugin_log_put",
  name: "plugin_log_put",
  reqSchemaId: 2253,
  resSchemaId: 2256,
} as const;

export async function pluginLogs(client: GosporeClient, req: systemTypes.PluginLogsReq, opts?: InvokeOptions): Promise<systemTypes.PluginLogsResp> {
  return client.invoke<systemTypes.PluginLogsReq, systemTypes.PluginLogsResp>("pluginhost.plugin_logs", req, { reqSchemaId: 2255, resSchemaId: 2256, ...opts });
}

export const pluginLogs_meta = {
  callable: "pluginhost.plugin_logs",
  name: "plugin_logs",
  reqSchemaId: 2255,
  resSchemaId: 2256,
} as const;

export async function proxyAttach(client: GosporeClient, req: systemTypes.PluginProxyAttachReq, opts?: InvokeOptions): Promise<systemTypes.PluginProxyAttachResp> {
  return client.invoke<systemTypes.PluginProxyAttachReq, systemTypes.PluginProxyAttachResp>("pluginhost.proxy_attach", req, { reqSchemaId: 5872, resSchemaId: 5873, ...opts });
}

export const proxyAttach_meta = {
  callable: "pluginhost.proxy_attach",
  name: "proxy_attach",
  reqSchemaId: 5872,
  resSchemaId: 5873,
} as const;

export async function proxyDetach(client: GosporeClient, req: systemTypes.PluginProxyDetachReq, opts?: InvokeOptions): Promise<systemTypes.PluginProxyDetachResp> {
  return client.invoke<systemTypes.PluginProxyDetachReq, systemTypes.PluginProxyDetachResp>("pluginhost.proxy_detach", req, { reqSchemaId: 5874, resSchemaId: 5875, ...opts });
}

export const proxyDetach_meta = {
  callable: "pluginhost.proxy_detach",
  name: "proxy_detach",
  reqSchemaId: 5874,
  resSchemaId: 5875,
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
  return client.invoke<systemTypes.PluginStateAppendReq, systemTypes.PluginStateAppendResp>("pluginhost.state_append", req, { reqSchemaId: 3114, resSchemaId: 3115, ...opts });
}

export const stateAppend_meta = {
  callable: "pluginhost.state_append",
  name: "state_append",
  reqSchemaId: 3114,
  resSchemaId: 3115,
} as const;

export async function stateDelete(client: GosporeClient, req: systemTypes.PluginStateDeleteReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateDeleteResp> {
  return client.invoke<systemTypes.PluginStateDeleteReq, systemTypes.PluginStateDeleteResp>("pluginhost.state_delete", req, { reqSchemaId: 3110, resSchemaId: 3111, ...opts });
}

export const stateDelete_meta = {
  callable: "pluginhost.state_delete",
  name: "state_delete",
  reqSchemaId: 3110,
  resSchemaId: 3111,
} as const;

export async function stateGet(client: GosporeClient, req: systemTypes.PluginStateGetReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateGetResp> {
  return client.invoke<systemTypes.PluginStateGetReq, systemTypes.PluginStateGetResp>("pluginhost.state_get", req, { reqSchemaId: 3106, resSchemaId: 3107, ...opts });
}

export const stateGet_meta = {
  callable: "pluginhost.state_get",
  name: "state_get",
  reqSchemaId: 3106,
  resSchemaId: 3107,
} as const;

export async function stateGetMany(client: GosporeClient, req: systemTypes.PluginStateGetManyReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateGetManyResp> {
  return client.invoke<systemTypes.PluginStateGetManyReq, systemTypes.PluginStateGetManyResp>("pluginhost.state_get_many", req, { reqSchemaId: 3116, resSchemaId: 3117, ...opts });
}

export const stateGetMany_meta = {
  callable: "pluginhost.state_get_many",
  name: "state_get_many",
  reqSchemaId: 3116,
  resSchemaId: 3117,
} as const;

export async function stateList(client: GosporeClient, req: systemTypes.PluginStateListReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateListResp> {
  return client.invoke<systemTypes.PluginStateListReq, systemTypes.PluginStateListResp>("pluginhost.state_list", req, { reqSchemaId: 3112, resSchemaId: 3113, ...opts });
}

export const stateList_meta = {
  callable: "pluginhost.state_list",
  name: "state_list",
  reqSchemaId: 3112,
  resSchemaId: 3113,
} as const;

export async function statePurge(client: GosporeClient, req: systemTypes.PluginStatePurgeReq, opts?: InvokeOptions): Promise<systemTypes.PluginStatePurgeResp> {
  return client.invoke<systemTypes.PluginStatePurgeReq, systemTypes.PluginStatePurgeResp>("pluginhost.state_purge", req, { reqSchemaId: 3120, resSchemaId: 3121, ...opts });
}

export const statePurge_meta = {
  callable: "pluginhost.state_purge",
  name: "state_purge",
  reqSchemaId: 3120,
  resSchemaId: 3121,
} as const;

export async function stateSet(client: GosporeClient, req: systemTypes.PluginStateSetReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateSetResp> {
  return client.invoke<systemTypes.PluginStateSetReq, systemTypes.PluginStateSetResp>("pluginhost.state_set", req, { reqSchemaId: 3108, resSchemaId: 3109, ...opts });
}

export const stateSet_meta = {
  callable: "pluginhost.state_set",
  name: "state_set",
  reqSchemaId: 3108,
  resSchemaId: 3109,
} as const;

export async function stateSetMany(client: GosporeClient, req: systemTypes.PluginStateSetManyReq, opts?: InvokeOptions): Promise<systemTypes.PluginStateSetManyResp> {
  return client.invoke<systemTypes.PluginStateSetManyReq, systemTypes.PluginStateSetManyResp>("pluginhost.state_set_many", req, { reqSchemaId: 3118, resSchemaId: 3119, ...opts });
}

export const stateSetMany_meta = {
  callable: "pluginhost.state_set_many",
  name: "state_set_many",
  reqSchemaId: 3118,
  resSchemaId: 3119,
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

