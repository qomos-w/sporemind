// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "gospore.projection",
    name: "get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 68,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "projectionGetReq",
      className: "projectionGetReq"
    },
    final: {
      kind: "scalar",
      name: "any",
      typeId: 15
    }
  },
  {
    namespace: "gospore.projection",
    name: "watch",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 69,
    chunkSchemaId: 1,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "projectionWatchReq",
      className: "projectionWatchReq"
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
