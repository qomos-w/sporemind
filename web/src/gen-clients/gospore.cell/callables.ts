// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "gospore.cell",
    name: "stats",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1773,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "cellStatsReq",
      className: "cellStatsReq"
    },
    final: {
      kind: "scalar",
      name: "any",
      typeId: 15
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
