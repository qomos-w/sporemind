// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "oracle",
    name: "capability_discover",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 944,
    finalSchemaId: 946,
    req: {
      kind: "struct",
      name: "OracleCapabilityDiscoverReq",
      className: "OracleCapabilityDiscoverReq"
    },
    final: {
      kind: "struct",
      name: "OracleCapabilityDiscoverResp",
      className: "OracleCapabilityDiscoverResp"
    }
  },
  {
    namespace: "oracle",
    name: "capability_explain",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 947,
    finalSchemaId: 948,
    req: {
      kind: "struct",
      name: "OracleCapabilityExplainReq",
      className: "OracleCapabilityExplainReq"
    },
    final: {
      kind: "struct",
      name: "OracleCapabilityExplainResp",
      className: "OracleCapabilityExplainResp"
    }
  },
  {
    namespace: "oracle",
    name: "events_stats_tick",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "oracle",
    name: "get_diagnostic",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 954,
    finalSchemaId: 949,
    req: {
      kind: "struct",
      name: "OracleGetDiagnosticReq",
      className: "OracleGetDiagnosticReq"
    },
    final: {
      kind: "struct",
      name: "Diagnostic",
      className: "Diagnostic"
    }
  },
  {
    namespace: "oracle",
    name: "list_diagnostics",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 952,
    finalSchemaId: 953,
    req: {
      kind: "struct",
      name: "OracleListDiagnosticsReq",
      className: "OracleListDiagnosticsReq"
    },
    final: {
      kind: "struct",
      name: "OracleListDiagnosticsResp",
      className: "OracleListDiagnosticsResp"
    }
  },
  {
    namespace: "oracle",
    name: "refresh_capability_cache",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 0,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "oracle",
    name: "report_diagnostic",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 950,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "OracleReportDiagnosticReq",
      className: "OracleReportDiagnosticReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "oracle",
    name: "search_services",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 955,
    finalSchemaId: 956,
    req: {
      kind: "struct",
      name: "OracleSearchServicesReq",
      className: "OracleSearchServicesReq"
    },
    final: {
      kind: "struct",
      name: "OracleSearchServicesResp",
      className: "OracleSearchServicesResp"
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
