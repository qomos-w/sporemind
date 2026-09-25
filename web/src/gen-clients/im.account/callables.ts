// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "im.account",
    name: "create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4836,
    finalSchemaId: 4837,
    req: {
      kind: "struct",
      name: "ImAccountCreateReq",
      className: "ImAccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "ImAccountCreateResp",
      className: "ImAccountCreateResp"
    }
  },
  {
    namespace: "im.account",
    name: "delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4840,
    finalSchemaId: 4841,
    req: {
      kind: "struct",
      name: "ImAccountDeleteReq",
      className: "ImAccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "ImAccountDeleteResp",
      className: "ImAccountDeleteResp"
    }
  },
  {
    namespace: "im.account",
    name: "list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4834,
    finalSchemaId: 4835,
    req: {
      kind: "struct",
      name: "ImAccountListReq",
      className: "ImAccountListReq"
    },
    final: {
      kind: "struct",
      name: "ImAccountListResp",
      className: "ImAccountListResp"
    }
  },
  {
    namespace: "im.account",
    name: "update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4838,
    finalSchemaId: 4839,
    req: {
      kind: "struct",
      name: "ImAccountUpdateReq",
      className: "ImAccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "ImAccountUpdateResp",
      className: "ImAccountUpdateResp"
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
