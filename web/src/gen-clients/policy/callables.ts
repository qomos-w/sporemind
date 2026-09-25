// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "policy",
    name: "configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6614,
    finalSchemaId: 6615,
    req: {
      kind: "struct",
      name: "PolicyConfigureReq",
      className: "PolicyConfigureReq"
    },
    final: {
      kind: "struct",
      name: "PolicyConfigureResp",
      className: "PolicyConfigureResp"
    }
  },
  {
    namespace: "policy",
    name: "decide",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6609,
    finalSchemaId: 6611,
    req: {
      kind: "struct",
      name: "PolicyDecideReq",
      className: "PolicyDecideReq"
    },
    final: {
      kind: "struct",
      name: "PolicyDecideResp",
      className: "PolicyDecideResp"
    }
  },
  {
    namespace: "policy",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6616,
    finalSchemaId: 6617,
    req: {
      kind: "struct",
      name: "PolicyStatusReq",
      className: "PolicyStatusReq"
    },
    final: {
      kind: "struct",
      name: "PolicyStatusResp",
      className: "PolicyStatusResp"
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
