// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "aistats",
    name: "aggregates",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2952,
    finalSchemaId: 2953,
    req: {
      kind: "struct",
      name: "AIStatsAggregatesReq",
      className: "AIStatsAggregatesReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsAggregatesResp",
      className: "AIStatsAggregatesResp"
    }
  },
  {
    namespace: "aistats",
    name: "backfill",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2950,
    finalSchemaId: 2951,
    req: {
      kind: "struct",
      name: "AIStatsBackfillReq",
      className: "AIStatsBackfillReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsBackfillResp",
      className: "AIStatsBackfillResp"
    }
  },
  {
    namespace: "aistats",
    name: "cleanup",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1797,
    finalSchemaId: 1798,
    req: {
      kind: "struct",
      name: "AIStatsCleanupReq",
      className: "AIStatsCleanupReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsCleanupResp",
      className: "AIStatsCleanupResp"
    }
  },
  {
    namespace: "aistats",
    name: "cost_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2942,
    finalSchemaId: 2943,
    req: {
      kind: "struct",
      name: "AIStatsCostConfigureReq",
      className: "AIStatsCostConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsCostConfigureResp",
      className: "AIStatsCostConfigureResp"
    }
  },
  {
    namespace: "aistats",
    name: "cost_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2944,
    finalSchemaId: 2945,
    req: {
      kind: "struct",
      name: "AIStatsCostListReq",
      className: "AIStatsCostListReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsCostListResp",
      className: "AIStatsCostListResp"
    }
  },
  {
    namespace: "aistats",
    name: "export",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2946,
    finalSchemaId: 2947,
    req: {
      kind: "struct",
      name: "AIStatsExportReq",
      className: "AIStatsExportReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsExportResp",
      className: "AIStatsExportResp"
    }
  },
  {
    namespace: "aistats",
    name: "query",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2940,
    finalSchemaId: 2941,
    req: {
      kind: "struct",
      name: "AIStatsQueryReq",
      className: "AIStatsQueryReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsQueryResp",
      className: "AIStatsQueryResp"
    }
  },
  {
    namespace: "aistats",
    name: "rollup",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1717,
    finalSchemaId: 1718,
    req: {
      kind: "struct",
      name: "AIStatsRollupReq",
      className: "AIStatsRollupReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsRollupResp",
      className: "AIStatsRollupResp"
    }
  },
  {
    namespace: "aistats",
    name: "series",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2956,
    finalSchemaId: 2957,
    req: {
      kind: "struct",
      name: "AIStatsSeriesReq",
      className: "AIStatsSeriesReq"
    },
    final: {
      kind: "struct",
      name: "AIStatsSeriesResp",
      className: "AIStatsSeriesResp"
    }
  },
];

export function buildCallableRegistry(): CallableRegistry {
  const registry = new CallableRegistry();
  for (const entry of callableEntries) {
    registry.register(entry);
  }
  return registry;
}
