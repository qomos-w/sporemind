// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function revertTo(client: GosporeClient, req: systemTypes.PuppetDocumentRevertToReq, opts?: InvokeOptions): Promise<systemTypes.PuppetDocumentRevertToResp> {
  return client.invoke<systemTypes.PuppetDocumentRevertToReq, systemTypes.PuppetDocumentRevertToResp>("puppet.document.revert_to", req, { reqSchemaId: 4582, resSchemaId: 4583, ...opts });
}

export const revertTo_meta = {
  callable: "puppet.document.revert_to",
  name: "revert_to",
  reqSchemaId: 4582,
  resSchemaId: 4583,
} as const;

export async function snapshot(client: GosporeClient, req: systemTypes.PuppetDocumentSnapshotReq, opts?: InvokeOptions): Promise<systemTypes.PuppetDocumentSnapshotResp> {
  return client.invoke<systemTypes.PuppetDocumentSnapshotReq, systemTypes.PuppetDocumentSnapshotResp>("puppet.document.snapshot", req, { reqSchemaId: 4547, resSchemaId: 4548, ...opts });
}

export const snapshot_meta = {
  callable: "puppet.document.snapshot",
  name: "snapshot",
  reqSchemaId: 4547,
  resSchemaId: 4548,
} as const;

