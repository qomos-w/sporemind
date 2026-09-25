// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function control(client: GosporeClient, req: systemTypes.InterfaceManagerControlReq, opts?: InvokeOptions): Promise<systemTypes.InterfaceManagerControlResp> {
  return client.invoke<systemTypes.InterfaceManagerControlReq, systemTypes.InterfaceManagerControlResp>("interfacemanager.control", req, { reqSchemaId: 3779, resSchemaId: 3781, ...opts });
}

export const control_meta = {
  callable: "interfacemanager.control",
  name: "control",
  reqSchemaId: 3779,
  resSchemaId: 3781,
} as const;

export async function queryInteractions(client: GosporeClient, req: systemTypes.QueryInteractionsReq, opts?: InvokeOptions): Promise<systemTypes.QueryInteractionsResp> {
  return client.invoke<systemTypes.QueryInteractionsReq, systemTypes.QueryInteractionsResp>("interfacemanager.query_interactions", req, { reqSchemaId: 3785, resSchemaId: 3786, ...opts });
}

export const queryInteractions_meta = {
  callable: "interfacemanager.query_interactions",
  name: "query_interactions",
  reqSchemaId: 3785,
  resSchemaId: 3786,
} as const;

export async function reportInteraction(client: GosporeClient, req: systemTypes.ReportInteractionReq, opts?: InvokeOptions): Promise<systemTypes.ReportInteractionResp> {
  return client.invoke<systemTypes.ReportInteractionReq, systemTypes.ReportInteractionResp>("interfacemanager.report_interaction", req, { reqSchemaId: 3783, resSchemaId: 3784, ...opts });
}

export const reportInteraction_meta = {
  callable: "interfacemanager.report_interaction",
  name: "report_interaction",
  reqSchemaId: 3783,
  resSchemaId: 3784,
} as const;

export type InterfaceManagerEventHandler = (payload: systemTypes.InterfaceManagerEvent) => void;

export function OnInterfaceManagerEvent(client: GosporeClient, handler: InterfaceManagerEventHandler): () => void {
  return client.events.onService("interfacemanager", "interface_manager_event", (payload) => handler(payload as systemTypes.InterfaceManagerEvent));
}

export function OffInterfaceManagerEvent(cancel: () => void): void {
  cancel();
}

