// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "gospore.events",
    name: "stats",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1743,
    finalSchemaId: 1771,
    req: {
      kind: "struct",
      name: "eventStatsReq",
      className: "eventStatsReq"
    },
    final: {
      kind: "struct",
      name: "eventStatsResp",
      className: "eventStatsResp"
    }
  },
  {
    namespace: "gospore.events",
    name: "subscribe_instance",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 67,
    chunkSchemaId: 1,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "eventSubscribeInstanceReq",
      className: "eventSubscribeInstanceReq"
    },
    chunk: {
      kind: "scalar",
      name: "any",
      typeId: 15
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "gospore.events",
    name: "subscribe_service",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 66,
    chunkSchemaId: 1,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "eventSubscribeServiceReq",
      className: "eventSubscribeServiceReq"
    },
    chunk: {
      kind: "scalar",
      name: "any",
      typeId: 15
    },
    final: {
      kind: "void",
      name: "void"
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
