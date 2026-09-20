// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function buildInfo(client: GosporeClient, req: systemTypes.RuntimeBuildInfoReq, opts?: InvokeOptions): Promise<systemTypes.RuntimeBuildInfoResp> {
  return client.invoke<systemTypes.RuntimeBuildInfoReq, systemTypes.RuntimeBuildInfoResp>("runtime.build_info", req, { reqSchemaId: 898, resSchemaId: 899, ...opts });
}

export const buildInfo_meta = {
  callable: "runtime.build_info",
  name: "build_info",
  reqSchemaId: 898,
  resSchemaId: 899,
} as const;

export async function listServices(client: GosporeClient, req: systemTypes.RuntimeListServicesReq, opts?: InvokeOptions): Promise<systemTypes.RuntimeListServicesResp> {
  return client.invoke<systemTypes.RuntimeListServicesReq, systemTypes.RuntimeListServicesResp>("runtime.list_services", req, { reqSchemaId: 896, resSchemaId: 897, ...opts });
}

export const listServices_meta = {
  callable: "runtime.list_services",
  name: "list_services",
  reqSchemaId: 896,
  resSchemaId: 897,
} as const;

export type TopologyEpochHandler = (payload: systemTypes.TopologyEpochEvent) => void;

export function OnTopologyEpoch(client: GosporeClient, handler: TopologyEpochHandler): () => void {
  return client.events.onService("unified_graph", "topology-epoch", (payload) => handler(payload as systemTypes.TopologyEpochEvent));
}

export function OffTopologyEpoch(cancel: () => void): void {
  cancel();
}

