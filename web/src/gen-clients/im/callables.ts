// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "im",
    name: "send",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4851,
    finalSchemaId: 4852,
    req: {
      kind: "struct",
      name: "ImSendReq",
      className: "ImSendReq"
    },
    final: {
      kind: "struct",
      name: "ImSendResp",
      className: "ImSendResp"
    }
  },
  {
    namespace: "im",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4849,
    finalSchemaId: 4850,
    req: {
      kind: "struct",
      name: "ImStatusReq",
      className: "ImStatusReq"
    },
    final: {
      kind: "struct",
      name: "ImStatusResp",
      className: "ImStatusResp"
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
