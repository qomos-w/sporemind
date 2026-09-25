// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function create(client: GosporeClient, req: systemTypes.BrowserManagerCreateReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerAsyncResult> {
  return client.invoke<systemTypes.BrowserManagerCreateReq, systemTypes.BrowserManagerAsyncResult>("browsermanager.create", req, { reqSchemaId: 583, resSchemaId: 587, ...opts });
}

export const create_meta = {
  callable: "browsermanager.create",
  name: "create",
  reqSchemaId: 583,
  resSchemaId: 587,
} as const;

export async function exportCookies(client: GosporeClient, req: systemTypes.BrowserManagerExportCookiesReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerExportCookiesResp> {
  return client.invoke<systemTypes.BrowserManagerExportCookiesReq, systemTypes.BrowserManagerExportCookiesResp>("browsermanager.export_cookies", req, { reqSchemaId: 689, resSchemaId: 690, ...opts });
}

export const exportCookies_meta = {
  callable: "browsermanager.export_cookies",
  name: "export_cookies",
  reqSchemaId: 689,
  resSchemaId: 690,
} as const;

export async function get(client: GosporeClient, req: systemTypes.BrowserManagerGetReq, opts?: InvokeOptions): Promise<systemTypes.BrowserInstance> {
  return client.invoke<systemTypes.BrowserManagerGetReq, systemTypes.BrowserInstance>("browsermanager.get", req, { reqSchemaId: 585, resSchemaId: 581, ...opts });
}

export const get_meta = {
  callable: "browsermanager.get",
  name: "get",
  reqSchemaId: 585,
  resSchemaId: 581,
} as const;

export async function importCookies(client: GosporeClient, req: systemTypes.BrowserManagerImportCookiesReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerImportCookiesResp> {
  return client.invoke<systemTypes.BrowserManagerImportCookiesReq, systemTypes.BrowserManagerImportCookiesResp>("browsermanager.import_cookies", req, { reqSchemaId: 691, resSchemaId: 692, ...opts });
}

export const importCookies_meta = {
  callable: "browsermanager.import_cookies",
  name: "import_cookies",
  reqSchemaId: 691,
  resSchemaId: 692,
} as const;

export async function list(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerListResp> {
  return client.invoke<void, systemTypes.BrowserManagerListResp>("browsermanager.list", undefined, { resSchemaId: 582, ...opts });
}

export async function navigate(client: GosporeClient, req: systemTypes.BrowserManagerNavigateReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerAsyncResult> {
  return client.invoke<systemTypes.BrowserManagerNavigateReq, systemTypes.BrowserManagerAsyncResult>("browsermanager.navigate", req, { reqSchemaId: 589, resSchemaId: 587, ...opts });
}

export const navigate_meta = {
  callable: "browsermanager.navigate",
  name: "navigate",
  reqSchemaId: 589,
  resSchemaId: 587,
} as const;

export async function open(client: GosporeClient, req: systemTypes.BrowserManagerOpenReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerAsyncResult> {
  return client.invoke<systemTypes.BrowserManagerOpenReq, systemTypes.BrowserManagerAsyncResult>("browsermanager.open", req, { reqSchemaId: 590, resSchemaId: 587, ...opts });
}

export const open_meta = {
  callable: "browsermanager.open",
  name: "open",
  reqSchemaId: 590,
  resSchemaId: 587,
} as const;

export async function openGlobal(client: GosporeClient, req: systemTypes.BrowserManagerOpenGlobalReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerOpenGlobalResp> {
  return client.invoke<systemTypes.BrowserManagerOpenGlobalReq, systemTypes.BrowserManagerOpenGlobalResp>("browsermanager.open_global", req, { reqSchemaId: 591, resSchemaId: 592, ...opts });
}

export const openGlobal_meta = {
  callable: "browsermanager.open_global",
  name: "open_global",
  reqSchemaId: 591,
  resSchemaId: 592,
} as const;

export async function remove(client: GosporeClient, req: systemTypes.BrowserManagerRemoveReq, opts?: InvokeOptions): Promise<systemTypes.BrowserManagerAsyncResult> {
  return client.invoke<systemTypes.BrowserManagerRemoveReq, systemTypes.BrowserManagerAsyncResult>("browsermanager.remove", req, { reqSchemaId: 584, resSchemaId: 587, ...opts });
}

export const remove_meta = {
  callable: "browsermanager.remove",
  name: "remove",
  reqSchemaId: 584,
  resSchemaId: 587,
} as const;

export async function update(client: GosporeClient, req: systemTypes.BrowserManagerUpdateReq, opts?: InvokeOptions): Promise<systemTypes.BrowserInstance> {
  return client.invoke<systemTypes.BrowserManagerUpdateReq, systemTypes.BrowserInstance>("browsermanager.update", req, { reqSchemaId: 586, resSchemaId: 581, ...opts });
}

export const update_meta = {
  callable: "browsermanager.update",
  name: "update",
  reqSchemaId: 586,
  resSchemaId: 581,
} as const;

export type BrowserManagerEventHandler = (payload: systemTypes.BrowserManagerEvent) => void;

export function OnBrowserManagerEvent(client: GosporeClient, handler: BrowserManagerEventHandler): () => void {
  return client.events.onService("browsermanager", "browser_manager_event", (payload) => handler(payload as systemTypes.BrowserManagerEvent));
}

export function OffBrowserManagerEvent(cancel: () => void): void {
  cancel();
}

