// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "unified_graph",
    name: "history",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 909,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "TopologyHistoryResp",
      className: "TopologyHistoryResp"
    }
  },
  {
    namespace: "unified_graph",
    name: "sync",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 906,
    finalSchemaId: 907,
    req: {
      kind: "struct",
      name: "TopologySyncReq",
      className: "TopologySyncReq"
    },
    final: {
      kind: "struct",
      name: "TopologySyncResp",
      className: "TopologySyncResp"
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
