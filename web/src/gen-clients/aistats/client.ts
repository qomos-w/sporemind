// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function aggregates(client: GosporeClient, req: systemTypes.AIStatsAggregatesReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsAggregatesResp> {
  return client.invoke<systemTypes.AIStatsAggregatesReq, systemTypes.AIStatsAggregatesResp>("aistats.aggregates", req, { reqSchemaId: 2952, resSchemaId: 2953, ...opts });
}

export const aggregates_meta = {
  callable: "aistats.aggregates",
  name: "aggregates",
  reqSchemaId: 2952,
  resSchemaId: 2953,
} as const;

export async function backfill(client: GosporeClient, req: systemTypes.AIStatsBackfillReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsBackfillResp> {
  return client.invoke<systemTypes.AIStatsBackfillReq, systemTypes.AIStatsBackfillResp>("aistats.backfill", req, { reqSchemaId: 2950, resSchemaId: 2951, ...opts });
}

export const backfill_meta = {
  callable: "aistats.backfill",
  name: "backfill",
  reqSchemaId: 2950,
  resSchemaId: 2951,
} as const;

export async function cleanup(client: GosporeClient, req: systemTypes.AIStatsCleanupReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsCleanupResp> {
  return client.invoke<systemTypes.AIStatsCleanupReq, systemTypes.AIStatsCleanupResp>("aistats.cleanup", req, { reqSchemaId: 1797, resSchemaId: 1798, ...opts });
}

export const cleanup_meta = {
  callable: "aistats.cleanup",
  name: "cleanup",
  reqSchemaId: 1797,
  resSchemaId: 1798,
} as const;

export async function costConfigure(client: GosporeClient, req: systemTypes.AIStatsCostConfigureReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsCostConfigureResp> {
  return client.invoke<systemTypes.AIStatsCostConfigureReq, systemTypes.AIStatsCostConfigureResp>("aistats.cost_configure", req, { reqSchemaId: 2942, resSchemaId: 2943, ...opts });
}

export const costConfigure_meta = {
  callable: "aistats.cost_configure",
  name: "cost_configure",
  reqSchemaId: 2942,
  resSchemaId: 2943,
} as const;

export async function costList(client: GosporeClient, req: systemTypes.AIStatsCostListReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsCostListResp> {
  return client.invoke<systemTypes.AIStatsCostListReq, systemTypes.AIStatsCostListResp>("aistats.cost_list", req, { reqSchemaId: 2944, resSchemaId: 2945, ...opts });
}

export const costList_meta = {
  callable: "aistats.cost_list",
  name: "cost_list",
  reqSchemaId: 2944,
  resSchemaId: 2945,
} as const;

export async function _export(client: GosporeClient, req: systemTypes.AIStatsExportReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsExportResp> {
  return client.invoke<systemTypes.AIStatsExportReq, systemTypes.AIStatsExportResp>("aistats.export", req, { reqSchemaId: 2946, resSchemaId: 2947, ...opts });
}

export const _export_meta = {
  callable: "aistats.export",
  name: "export",
  reqSchemaId: 2946,
  resSchemaId: 2947,
} as const;

export async function query(client: GosporeClient, req: systemTypes.AIStatsQueryReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsQueryResp> {
  return client.invoke<systemTypes.AIStatsQueryReq, systemTypes.AIStatsQueryResp>("aistats.query", req, { reqSchemaId: 2940, resSchemaId: 2941, ...opts });
}

export const query_meta = {
  callable: "aistats.query",
  name: "query",
  reqSchemaId: 2940,
  resSchemaId: 2941,
} as const;

export async function rollup(client: GosporeClient, req: systemTypes.AIStatsRollupReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsRollupResp> {
  return client.invoke<systemTypes.AIStatsRollupReq, systemTypes.AIStatsRollupResp>("aistats.rollup", req, { reqSchemaId: 1717, resSchemaId: 1718, ...opts });
}

export const rollup_meta = {
  callable: "aistats.rollup",
  name: "rollup",
  reqSchemaId: 1717,
  resSchemaId: 1718,
} as const;

export async function series(client: GosporeClient, req: systemTypes.AIStatsSeriesReq, opts?: InvokeOptions): Promise<systemTypes.AIStatsSeriesResp> {
  return client.invoke<systemTypes.AIStatsSeriesReq, systemTypes.AIStatsSeriesResp>("aistats.series", req, { reqSchemaId: 2956, resSchemaId: 2957, ...opts });
}

export const series_meta = {
  callable: "aistats.series",
  name: "series",
  reqSchemaId: 2956,
  resSchemaId: 2957,
} as const;

