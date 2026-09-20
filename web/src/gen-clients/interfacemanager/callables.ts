// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "interfacemanager",
    name: "control",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3699,
    finalSchemaId: 3700,
    req: {
      kind: "struct",
      name: "InterfaceManagerControlReq",
      className: "InterfaceManagerControlReq"
    },
    final: {
      kind: "struct",
      name: "InterfaceManagerControlResp",
      className: "InterfaceManagerControlResp"
    }
  },
  {
    namespace: "interfacemanager",
    name: "query_interactions",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3704,
    finalSchemaId: 3705,
    req: {
      kind: "struct",
      name: "QueryInteractionsReq",
      className: "QueryInteractionsReq"
    },
    final: {
      kind: "struct",
      name: "QueryInteractionsResp",
      className: "QueryInteractionsResp"
    }
  },
  {
    namespace: "interfacemanager",
    name: "report_interaction",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 3702,
    finalSchemaId: 3703,
    req: {
      kind: "struct",
      name: "ReportInteractionReq",
      className: "ReportInteractionReq"
    },
    final: {
      kind: "struct",
      name: "ReportInteractionResp",
      className: "ReportInteractionResp"
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
