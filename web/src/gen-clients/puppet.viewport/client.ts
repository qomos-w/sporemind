// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function capture(client: GosporeClient, req: systemTypes.PuppetViewportCaptureReq, opts?: InvokeOptions): Promise<systemTypes.PuppetViewportCaptureResp> {
  return client.invoke<systemTypes.PuppetViewportCaptureReq, systemTypes.PuppetViewportCaptureResp>("puppet.viewport.capture", req, { reqSchemaId: 4593, resSchemaId: 4594, ...opts });
}

export const capture_meta = {
  callable: "puppet.viewport.capture",
  name: "capture",
  reqSchemaId: 4593,
  resSchemaId: 4594,
} as const;

