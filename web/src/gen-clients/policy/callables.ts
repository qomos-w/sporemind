// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "policy",
    name: "configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6694,
    finalSchemaId: 6695,
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
    reqSchemaId: 6689,
    finalSchemaId: 6691,
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
    reqSchemaId: 6696,
    finalSchemaId: 6697,
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
