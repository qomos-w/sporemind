// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "puppet",
    name: "edit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4562,
    finalSchemaId: 4563,
    req: {
      kind: "struct",
      name: "PuppetEditReq",
      className: "PuppetEditReq"
    },
    final: {
      kind: "struct",
      name: "PuppetEditResp",
      className: "PuppetEditResp"
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
