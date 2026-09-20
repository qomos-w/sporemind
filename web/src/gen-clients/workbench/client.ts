// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function attentionReport(client: GosporeClient, req: systemTypes.WorkbenchAttentionReportReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchAttentionReportResp> {
  return client.invoke<systemTypes.WorkbenchAttentionReportReq, systemTypes.WorkbenchAttentionReportResp>("workbench.attention_report", req, { reqSchemaId: 6497, resSchemaId: 6498, ...opts });
}

export const attentionReport_meta = {
  callable: "workbench.attention_report",
  name: "attention_report",
  reqSchemaId: 6497,
  resSchemaId: 6498,
} as const;

export async function promote(client: GosporeClient, req: systemTypes.WorkbenchCardRefReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchCardRefReq, systemTypes.WorkbenchSnapshot>("workbench.promote", req, { reqSchemaId: 6403, resSchemaId: 6401, ...opts });
}

export const promote_meta = {
  callable: "workbench.promote",
  name: "promote",
  reqSchemaId: 6403,
  resSchemaId: 6401,
} as const;

export async function setFrozen(client: GosporeClient, req: systemTypes.WorkbenchSetFrozenReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchSetFrozenReq, systemTypes.WorkbenchSnapshot>("workbench.set_frozen", req, { reqSchemaId: 6407, resSchemaId: 6401, ...opts });
}

export const setFrozen_meta = {
  callable: "workbench.set_frozen",
  name: "set_frozen",
  reqSchemaId: 6407,
  resSchemaId: 6401,
} as const;

export async function setHidden(client: GosporeClient, req: systemTypes.WorkbenchSetHiddenReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchSetHiddenReq, systemTypes.WorkbenchSnapshot>("workbench.set_hidden", req, { reqSchemaId: 6404, resSchemaId: 6401, ...opts });
}

export const setHidden_meta = {
  callable: "workbench.set_hidden",
  name: "set_hidden",
  reqSchemaId: 6404,
  resSchemaId: 6401,
} as const;

export async function setMaximized(client: GosporeClient, req: systemTypes.WorkbenchSetMaximizedReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchSetMaximizedReq, systemTypes.WorkbenchSnapshot>("workbench.set_maximized", req, { reqSchemaId: 6408, resSchemaId: 6401, ...opts });
}

export const setMaximized_meta = {
  callable: "workbench.set_maximized",
  name: "set_maximized",
  reqSchemaId: 6408,
  resSchemaId: 6401,
} as const;

export async function setPinned(client: GosporeClient, req: systemTypes.WorkbenchSetPinnedReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchSetPinnedReq, systemTypes.WorkbenchSnapshot>("workbench.set_pinned", req, { reqSchemaId: 6405, resSchemaId: 6401, ...opts });
}

export const setPinned_meta = {
  callable: "workbench.set_pinned",
  name: "set_pinned",
  reqSchemaId: 6405,
  resSchemaId: 6401,
} as const;

export async function snapshot(client: GosporeClient, req: systemTypes.WorkbenchSnapshotReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchSnapshotReq, systemTypes.WorkbenchSnapshot>("workbench.snapshot", req, { reqSchemaId: 6402, resSchemaId: 6401, ...opts });
}

export const snapshot_meta = {
  callable: "workbench.snapshot",
  name: "snapshot",
  reqSchemaId: 6402,
  resSchemaId: 6401,
} as const;

export async function upsertCard(client: GosporeClient, req: systemTypes.WorkbenchUpsertCardReq, opts?: InvokeOptions): Promise<systemTypes.WorkbenchSnapshot> {
  return client.invoke<systemTypes.WorkbenchUpsertCardReq, systemTypes.WorkbenchSnapshot>("workbench.upsert_card", req, { reqSchemaId: 6406, resSchemaId: 6401, ...opts });
}

export const upsertCard_meta = {
  callable: "workbench.upsert_card",
  name: "upsert_card",
  reqSchemaId: 6406,
  resSchemaId: 6401,
} as const;

