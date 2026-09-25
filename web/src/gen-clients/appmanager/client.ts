// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function agentAction(client: GosporeClient, req: systemTypes.AppManagerAgentActionReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerAgentActionResp> {
  return client.invoke<systemTypes.AppManagerAgentActionReq, systemTypes.AppManagerAgentActionResp>("appmanager.agent_action", req, { reqSchemaId: 2912, resSchemaId: 2913, ...opts });
}

export const agentAction_meta = {
  callable: "appmanager.agent_action",
  name: "agent_action",
  reqSchemaId: 2912,
  resSchemaId: 2913,
} as const;

export async function appExport(client: GosporeClient, req: systemTypes.AppManagerAppExportReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerAppExportResp> {
  return client.invoke<systemTypes.AppManagerAppExportReq, systemTypes.AppManagerAppExportResp>("appmanager.app_export", req, { reqSchemaId: 6208, resSchemaId: 6209, ...opts });
}

export const appExport_meta = {
  callable: "appmanager.app_export",
  name: "app_export",
  reqSchemaId: 6208,
  resSchemaId: 6209,
} as const;

export async function audit(client: GosporeClient, req: systemTypes.AppManagerAuditReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerAuditResp> {
  return client.invoke<systemTypes.AppManagerAuditReq, systemTypes.AppManagerAuditResp>("appmanager.audit", req, { reqSchemaId: 2189, resSchemaId: 2190, ...opts });
}

export const audit_meta = {
  callable: "appmanager.audit",
  name: "audit",
  reqSchemaId: 2189,
  resSchemaId: 2190,
} as const;

export async function callableInfo(client: GosporeClient, req: systemTypes.AppManagerCallableInfoReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerCallableInfoResp> {
  return client.invoke<systemTypes.AppManagerCallableInfoReq, systemTypes.AppManagerCallableInfoResp>("appmanager.callable_info", req, { reqSchemaId: 4784, resSchemaId: 4787, ...opts });
}

export const callableInfo_meta = {
  callable: "appmanager.callable_info",
  name: "callable_info",
  reqSchemaId: 4784,
  resSchemaId: 4787,
} as const;

export async function cast(client: GosporeClient, req: systemTypes.AppManagerCastReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerCastResp> {
  return client.invoke<systemTypes.AppManagerCastReq, systemTypes.AppManagerCastResp>("appmanager.cast", req, { reqSchemaId: 2184, resSchemaId: 2185, ...opts });
}

export const cast_meta = {
  callable: "appmanager.cast",
  name: "cast",
  reqSchemaId: 2184,
  resSchemaId: 2185,
} as const;

export async function componentGet(client: GosporeClient, req: systemTypes.AppManagerComponentGetReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerComponentGetResp> {
  return client.invoke<systemTypes.AppManagerComponentGetReq, systemTypes.AppManagerComponentGetResp>("appmanager.component_get", req, { reqSchemaId: 5730, resSchemaId: 5731, ...opts });
}

export const componentGet_meta = {
  callable: "appmanager.component_get",
  name: "component_get",
  reqSchemaId: 5730,
  resSchemaId: 5731,
} as const;

export async function componentList(client: GosporeClient, req: systemTypes.AppManagerComponentListReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerComponentListResp> {
  return client.invoke<systemTypes.AppManagerComponentListReq, systemTypes.AppManagerComponentListResp>("appmanager.component_list", req, { reqSchemaId: 5728, resSchemaId: 5729, ...opts });
}

export const componentList_meta = {
  callable: "appmanager.component_list",
  name: "component_list",
  reqSchemaId: 5728,
  resSchemaId: 5729,
} as const;

export async function devGate(client: GosporeClient, req: systemTypes.AppManagerDevGateReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerDevGateResp> {
  return client.invoke<systemTypes.AppManagerDevGateReq, systemTypes.AppManagerDevGateResp>("appmanager.dev_gate", req, { reqSchemaId: 4944, resSchemaId: 4947, ...opts });
}

export const devGate_meta = {
  callable: "appmanager.dev_gate",
  name: "dev_gate",
  reqSchemaId: 4944,
  resSchemaId: 4947,
} as const;

export async function devGenerate(client: GosporeClient, req: systemTypes.AppManagerDevGenerateReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerDevGenerateResp> {
  return client.invoke<systemTypes.AppManagerDevGenerateReq, systemTypes.AppManagerDevGenerateResp>("appmanager.dev_generate", req, { reqSchemaId: 4896, resSchemaId: 4898, ...opts });
}

export const devGenerate_meta = {
  callable: "appmanager.dev_generate",
  name: "dev_generate",
  reqSchemaId: 4896,
  resSchemaId: 4898,
} as const;

export async function devGuide(client: GosporeClient, req: systemTypes.AppManagerDevGuideReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerDevGuideResp> {
  return client.invoke<systemTypes.AppManagerDevGuideReq, systemTypes.AppManagerDevGuideResp>("appmanager.dev_guide", req, { reqSchemaId: 4788, resSchemaId: 4791, ...opts });
}

export const devGuide_meta = {
  callable: "appmanager.dev_guide",
  name: "dev_guide",
  reqSchemaId: 4788,
  resSchemaId: 4791,
} as const;

export async function emit(client: GosporeClient, req: systemTypes.AppManagerEmitReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerEmitResp> {
  return client.invoke<systemTypes.AppManagerEmitReq, systemTypes.AppManagerEmitResp>("appmanager.emit", req, { reqSchemaId: 2186, resSchemaId: 2187, ...opts });
}

export const emit_meta = {
  callable: "appmanager.emit",
  name: "emit",
  reqSchemaId: 2186,
  resSchemaId: 2187,
} as const;

export async function get(client: GosporeClient, req: systemTypes.AppManagerGetReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerGetResp> {
  return client.invoke<systemTypes.AppManagerGetReq, systemTypes.AppManagerGetResp>("appmanager.get", req, { reqSchemaId: 2178, resSchemaId: 2179, ...opts });
}

export const get_meta = {
  callable: "appmanager.get",
  name: "get",
  reqSchemaId: 2178,
  resSchemaId: 2179,
} as const;

export async function hostProtocol(client: GosporeClient, req: systemTypes.AppManagerHostProtocolReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerHostProtocolResp> {
  return client.invoke<systemTypes.AppManagerHostProtocolReq, systemTypes.AppManagerHostProtocolResp>("appmanager.host_protocol", req, { reqSchemaId: 5872, resSchemaId: 5875, ...opts });
}

export const hostProtocol_meta = {
  callable: "appmanager.host_protocol",
  name: "host_protocol",
  reqSchemaId: 5872,
  resSchemaId: 5875,
} as const;

export async function iconNames(client: GosporeClient, req: systemTypes.AppManagerIconNamesReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerIconNamesResp> {
  return client.invoke<systemTypes.AppManagerIconNamesReq, systemTypes.AppManagerIconNamesResp>("appmanager.icon_names", req, { reqSchemaId: 5632, resSchemaId: 5634, ...opts });
}

export const iconNames_meta = {
  callable: "appmanager.icon_names",
  name: "icon_names",
  reqSchemaId: 5632,
  resSchemaId: 5634,
} as const;

export async function installLocal(client: GosporeClient, req: systemTypes.AppManagerInstallLocalReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerInstallLocalResp> {
  return client.invoke<systemTypes.AppManagerInstallLocalReq, systemTypes.AppManagerInstallLocalResp>("appmanager.install_local", req, { reqSchemaId: 5584, resSchemaId: 5585, ...opts });
}

export const installLocal_meta = {
  callable: "appmanager.install_local",
  name: "install_local",
  reqSchemaId: 5584,
  resSchemaId: 5585,
} as const;

export async function invoke(client: GosporeClient, req: systemTypes.AppManagerInvokeReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerInvokeResp> {
  return client.invoke<systemTypes.AppManagerInvokeReq, systemTypes.AppManagerInvokeResp>("appmanager.invoke", req, { reqSchemaId: 2182, resSchemaId: 2183, ...opts });
}

export const invoke_meta = {
  callable: "appmanager.invoke",
  name: "invoke",
  reqSchemaId: 2182,
  resSchemaId: 2183,
} as const;

export async function *invokeStream(client: GosporeClient, req: systemTypes.AppManagerInvokeReq, opts?: InvokeOptions): AsyncIterable<systemTypes.PluginInvokeChunk> {
  yield* client.subscribe<systemTypes.PluginInvokeChunk>("appmanager.invoke_stream", req, { reqSchemaId: 2182, chunkSchemaId: 2329, ...opts });
}

export async function list(client: GosporeClient, req: systemTypes.AppManagerListReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerListResp> {
  return client.invoke<systemTypes.AppManagerListReq, systemTypes.AppManagerListResp>("appmanager.list", req, { reqSchemaId: 2180, resSchemaId: 2181, ...opts });
}

export const list_meta = {
  callable: "appmanager.list",
  name: "list",
  reqSchemaId: 2180,
  resSchemaId: 2181,
} as const;

export async function openView(client: GosporeClient, req: systemTypes.AppManagerOpenViewReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerOpenViewResp> {
  return client.invoke<systemTypes.AppManagerOpenViewReq, systemTypes.AppManagerOpenViewResp>("appmanager.open_view", req, { reqSchemaId: 5680, resSchemaId: 5681, ...opts });
}

export const openView_meta = {
  callable: "appmanager.open_view",
  name: "open_view",
  reqSchemaId: 5680,
  resSchemaId: 5681,
} as const;

export async function panelTopology(client: GosporeClient, req: systemTypes.AppManagerPanelTopologyReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerPanelTopologyResp> {
  return client.invoke<systemTypes.AppManagerPanelTopologyReq, systemTypes.AppManagerPanelTopologyResp>("appmanager.panel_topology", req, { reqSchemaId: 6160, resSchemaId: 6161, ...opts });
}

export const panelTopology_meta = {
  callable: "appmanager.panel_topology",
  name: "panel_topology",
  reqSchemaId: 6160,
  resSchemaId: 6161,
} as const;

export async function pluginEmit(client: GosporeClient, req: systemTypes.AppManagerPluginEmitReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerPluginEmitResp> {
  return client.invoke<systemTypes.AppManagerPluginEmitReq, systemTypes.AppManagerPluginEmitResp>("appmanager.plugin_emit", req, { reqSchemaId: 5920, resSchemaId: 5921, ...opts });
}

export const pluginEmit_meta = {
  callable: "appmanager.plugin_emit",
  name: "plugin_emit",
  reqSchemaId: 5920,
  resSchemaId: 5921,
} as const;

export async function pluginLoad(client: GosporeClient, req: systemTypes.AppManagerPluginLoadReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerPluginLoadResp> {
  return client.invoke<systemTypes.AppManagerPluginLoadReq, systemTypes.AppManagerPluginLoadResp>("appmanager.plugin_load", req, { reqSchemaId: 5536, resSchemaId: 5537, ...opts });
}

export const pluginLoad_meta = {
  callable: "appmanager.plugin_load",
  name: "plugin_load",
  reqSchemaId: 5536,
  resSchemaId: 5537,
} as const;

export async function pluginUnload(client: GosporeClient, req: systemTypes.AppManagerPluginUnloadReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerPluginUnloadResp> {
  return client.invoke<systemTypes.AppManagerPluginUnloadReq, systemTypes.AppManagerPluginUnloadResp>("appmanager.plugin_unload", req, { reqSchemaId: 5538, resSchemaId: 5539, ...opts });
}

export const pluginUnload_meta = {
  callable: "appmanager.plugin_unload",
  name: "plugin_unload",
  reqSchemaId: 5538,
  resSchemaId: 5539,
} as const;

export async function projectPackage(client: GosporeClient, req: systemTypes.AppManagerProjectPackageReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerProjectPackageResp> {
  return client.invoke<systemTypes.AppManagerProjectPackageReq, systemTypes.AppManagerProjectPackageResp>("appmanager.project_package", req, { reqSchemaId: 3072, resSchemaId: 3073, ...opts });
}

export const projectPackage_meta = {
  callable: "appmanager.project_package",
  name: "project_package",
  reqSchemaId: 3072,
  resSchemaId: 3073,
} as const;

export async function register(client: GosporeClient, req: systemTypes.AppManagerRegisterReq, opts?: InvokeOptions): Promise<systemTypes.AppStatus> {
  return client.invoke<systemTypes.AppManagerRegisterReq, systemTypes.AppStatus>("appmanager.register", req, { reqSchemaId: 2176, resSchemaId: 2132, ...opts });
}

export const register_meta = {
  callable: "appmanager.register",
  name: "register",
  reqSchemaId: 2176,
  resSchemaId: 2132,
} as const;

export async function registerProject(client: GosporeClient, req: systemTypes.AppManagerRegisterProjectReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerRegisterProjectResp> {
  return client.invoke<systemTypes.AppManagerRegisterProjectReq, systemTypes.AppManagerRegisterProjectResp>("appmanager.register_project", req, { reqSchemaId: 3120, resSchemaId: 3121, ...opts });
}

export const registerProject_meta = {
  callable: "appmanager.register_project",
  name: "register_project",
  reqSchemaId: 3120,
  resSchemaId: 3121,
} as const;

export async function registrationPreview(client: GosporeClient, req: systemTypes.AppManagerRegistrationPreviewReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerRegistrationPreviewResp> {
  return client.invoke<systemTypes.AppManagerRegistrationPreviewReq, systemTypes.AppManagerRegistrationPreviewResp>("appmanager.registration_preview", req, { reqSchemaId: 6112, resSchemaId: 6113, ...opts });
}

export const registrationPreview_meta = {
  callable: "appmanager.registration_preview",
  name: "registration_preview",
  reqSchemaId: 6112,
  resSchemaId: 6113,
} as const;

export async function reload(client: GosporeClient, req: systemTypes.AppManagerReloadReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerReloadResp> {
  return client.invoke<systemTypes.AppManagerReloadReq, systemTypes.AppManagerReloadResp>("appmanager.reload", req, { reqSchemaId: 2914, resSchemaId: 2915, ...opts });
}

export const reload_meta = {
  callable: "appmanager.reload",
  name: "reload",
  reqSchemaId: 2914,
  resSchemaId: 2915,
} as const;

export async function reloadProject(client: GosporeClient, req: systemTypes.AppManagerReloadProjectReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerReloadProjectResp> {
  return client.invoke<systemTypes.AppManagerReloadProjectReq, systemTypes.AppManagerReloadProjectResp>("appmanager.reload_project", req, { reqSchemaId: 3122, resSchemaId: 3123, ...opts });
}

export const reloadProject_meta = {
  callable: "appmanager.reload_project",
  name: "reload_project",
  reqSchemaId: 3122,
  resSchemaId: 3123,
} as const;

export async function retryCleanup(client: GosporeClient, req: systemTypes.AppManagerRetryCleanupReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerRetryCleanupResp> {
  return client.invoke<systemTypes.AppManagerRetryCleanupReq, systemTypes.AppManagerRetryCleanupResp>("appmanager.retry_cleanup", req, { reqSchemaId: 4464, resSchemaId: 4465, ...opts });
}

export const retryCleanup_meta = {
  callable: "appmanager.retry_cleanup",
  name: "retry_cleanup",
  reqSchemaId: 4464,
  resSchemaId: 4465,
} as const;

export async function routeToken(client: GosporeClient, req: systemTypes.AppRouteTokenReq, opts?: InvokeOptions): Promise<systemTypes.AppRouteTokenResp> {
  return client.invoke<systemTypes.AppRouteTokenReq, systemTypes.AppRouteTokenResp>("appmanager.route_token", req, { reqSchemaId: 3302, resSchemaId: 3303, ...opts });
}

export const routeToken_meta = {
  callable: "appmanager.route_token",
  name: "route_token",
  reqSchemaId: 3302,
  resSchemaId: 3303,
} as const;

export async function sdkVendor(client: GosporeClient, req: systemTypes.AppManagerSdkVendorReq, opts?: InvokeOptions): Promise<systemTypes.AppManagerSdkVendorResp> {
  return client.invoke<systemTypes.AppManagerSdkVendorReq, systemTypes.AppManagerSdkVendorResp>("appmanager.sdk_vendor", req, { reqSchemaId: 5776, resSchemaId: 5777, ...opts });
}

export const sdkVendor_meta = {
  callable: "appmanager.sdk_vendor",
  name: "sdk_vendor",
  reqSchemaId: 5776,
  resSchemaId: 5777,
} as const;

export async function sessionCreate(client: GosporeClient, req: systemTypes.AppSessionCreateReq, opts?: InvokeOptions): Promise<systemTypes.AppSessionCreateResp> {
  return client.invoke<systemTypes.AppSessionCreateReq, systemTypes.AppSessionCreateResp>("appmanager.session_create", req, { reqSchemaId: 3296, resSchemaId: 3297, ...opts });
}

export const sessionCreate_meta = {
  callable: "appmanager.session_create",
  name: "session_create",
  reqSchemaId: 3296,
  resSchemaId: 3297,
} as const;

export async function sessionResolve(client: GosporeClient, req: systemTypes.AppSessionResolveReq, opts?: InvokeOptions): Promise<systemTypes.AppSessionResolveResp> {
  return client.invoke<systemTypes.AppSessionResolveReq, systemTypes.AppSessionResolveResp>("appmanager.session_resolve", req, { reqSchemaId: 3298, resSchemaId: 3299, ...opts });
}

export const sessionResolve_meta = {
  callable: "appmanager.session_resolve",
  name: "session_resolve",
  reqSchemaId: 3298,
  resSchemaId: 3299,
} as const;

export async function sessionRevoke(client: GosporeClient, req: systemTypes.AppSessionRevokeReq, opts?: InvokeOptions): Promise<systemTypes.AppSessionRevokeResp> {
  return client.invoke<systemTypes.AppSessionRevokeReq, systemTypes.AppSessionRevokeResp>("appmanager.session_revoke", req, { reqSchemaId: 3300, resSchemaId: 3301, ...opts });
}

export const sessionRevoke_meta = {
  callable: "appmanager.session_revoke",
  name: "session_revoke",
  reqSchemaId: 3300,
  resSchemaId: 3301,
} as const;

export async function unregister(client: GosporeClient, req: systemTypes.AppManagerUnregisterReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.AppManagerUnregisterReq, void>("appmanager.unregister", req, { reqSchemaId: 2177, ...opts });
}

export const unregister_meta = {
  callable: "appmanager.unregister",
  name: "unregister",
  reqSchemaId: 2177,
} as const;

export type AppEventHandler = (payload: systemTypes.AppEventMessage) => void;

export function OnAppEvent(client: GosporeClient, handler: AppEventHandler): () => void {
  return client.events.onService("appmanager", "app_event", (payload) => handler(payload as systemTypes.AppEventMessage));
}

export function OffAppEvent(cancel: () => void): void {
  cancel();
}

export type AppLifecycleHandler = (payload: systemTypes.AppLifecycleEvent) => void;

export function OnAppLifecycle(client: GosporeClient, handler: AppLifecycleHandler): () => void {
  return client.events.onService("appmanager", "app_lifecycle", (payload) => handler(payload as systemTypes.AppLifecycleEvent));
}

export function OffAppLifecycle(cancel: () => void): void {
  cancel();
}

