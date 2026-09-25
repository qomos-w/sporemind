// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function capabilityDiscover(client: GosporeClient, req: systemTypes.OracleCapabilityDiscoverReq, opts?: InvokeOptions): Promise<systemTypes.OracleCapabilityDiscoverResp> {
  return client.invoke<systemTypes.OracleCapabilityDiscoverReq, systemTypes.OracleCapabilityDiscoverResp>("oracle.capability_discover", req, { reqSchemaId: 960, resSchemaId: 962, ...opts });
}

export const capabilityDiscover_meta = {
  callable: "oracle.capability_discover",
  name: "capability_discover",
  reqSchemaId: 960,
  resSchemaId: 962,
} as const;

export async function capabilityExplain(client: GosporeClient, req: systemTypes.OracleCapabilityExplainReq, opts?: InvokeOptions): Promise<systemTypes.OracleCapabilityExplainResp> {
  return client.invoke<systemTypes.OracleCapabilityExplainReq, systemTypes.OracleCapabilityExplainResp>("oracle.capability_explain", req, { reqSchemaId: 963, resSchemaId: 964, ...opts });
}

export const capabilityExplain_meta = {
  callable: "oracle.capability_explain",
  name: "capability_explain",
  reqSchemaId: 963,
  resSchemaId: 964,
} as const;

export async function eventsStatsTick(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("oracle.events_stats_tick", undefined, opts);
}

export async function getDiagnostic(client: GosporeClient, req: systemTypes.OracleGetDiagnosticReq, opts?: InvokeOptions): Promise<systemTypes.Diagnostic> {
  return client.invoke<systemTypes.OracleGetDiagnosticReq, systemTypes.Diagnostic>("oracle.get_diagnostic", req, { reqSchemaId: 970, resSchemaId: 965, ...opts });
}

export const getDiagnostic_meta = {
  callable: "oracle.get_diagnostic",
  name: "get_diagnostic",
  reqSchemaId: 970,
  resSchemaId: 965,
} as const;

export async function listDiagnostics(client: GosporeClient, req: systemTypes.OracleListDiagnosticsReq, opts?: InvokeOptions): Promise<systemTypes.OracleListDiagnosticsResp> {
  return client.invoke<systemTypes.OracleListDiagnosticsReq, systemTypes.OracleListDiagnosticsResp>("oracle.list_diagnostics", req, { reqSchemaId: 968, resSchemaId: 969, ...opts });
}

export const listDiagnostics_meta = {
  callable: "oracle.list_diagnostics",
  name: "list_diagnostics",
  reqSchemaId: 968,
  resSchemaId: 969,
} as const;

export async function refreshCapabilityCache(client: GosporeClient, opts?: InvokeOptions): Promise<void> {
  return client.invoke<void, void>("oracle.refresh_capability_cache", undefined, opts);
}

export async function reportDiagnostic(client: GosporeClient, req: systemTypes.OracleReportDiagnosticReq, opts?: InvokeOptions): Promise<void> {
  return client.invoke<systemTypes.OracleReportDiagnosticReq, void>("oracle.report_diagnostic", req, { reqSchemaId: 966, ...opts });
}

export const reportDiagnostic_meta = {
  callable: "oracle.report_diagnostic",
  name: "report_diagnostic",
  reqSchemaId: 966,
} as const;

export async function searchServices(client: GosporeClient, req: systemTypes.OracleSearchServicesReq, opts?: InvokeOptions): Promise<systemTypes.OracleSearchServicesResp> {
  return client.invoke<systemTypes.OracleSearchServicesReq, systemTypes.OracleSearchServicesResp>("oracle.search_services", req, { reqSchemaId: 971, resSchemaId: 972, ...opts });
}

export const searchServices_meta = {
  callable: "oracle.search_services",
  name: "search_services",
  reqSchemaId: 971,
  resSchemaId: 972,
} as const;

export type DiagnosticHandler = (payload: systemTypes.Diagnostic) => void;

export function OnDiagnostic(client: GosporeClient, handler: DiagnosticHandler): () => void {
  return client.events.onService("oracle", "diagnostic", (payload) => handler(payload as systemTypes.Diagnostic));
}

export function OffDiagnostic(cancel: () => void): void {
  cancel();
}

