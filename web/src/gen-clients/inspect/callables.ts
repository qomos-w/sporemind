// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "inspect",
    name: "document",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 838,
    finalSchemaId: 845,
    req: {
      kind: "struct",
      name: "InspectDocumentReq",
      className: "InspectDocumentReq"
    },
    final: {
      kind: "struct",
      name: "InspectDocument",
      className: "InspectDocument"
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
