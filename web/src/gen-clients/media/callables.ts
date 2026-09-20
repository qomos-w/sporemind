// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "media",
    name: "activate_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4506,
    finalSchemaId: 4507,
    req: {
      kind: "struct",
      name: "MediaAccountActivateReq",
      className: "MediaAccountActivateReq"
    },
    final: {
      kind: "struct",
      name: "MediaAccountActivateResp",
      className: "MediaAccountActivateResp"
    }
  },
  {
    namespace: "media",
    name: "create_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4500,
    finalSchemaId: 4501,
    req: {
      kind: "struct",
      name: "MediaAccountCreateReq",
      className: "MediaAccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "MediaAccountCreateResp",
      className: "MediaAccountCreateResp"
    }
  },
  {
    namespace: "media",
    name: "delete_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4504,
    finalSchemaId: 4505,
    req: {
      kind: "struct",
      name: "MediaAccountDeleteReq",
      className: "MediaAccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "MediaAccountDeleteResp",
      className: "MediaAccountDeleteResp"
    }
  },
  {
    namespace: "media",
    name: "list_accounts",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4498,
    finalSchemaId: 4499,
    req: {
      kind: "struct",
      name: "MediaAccountListReq",
      className: "MediaAccountListReq"
    },
    final: {
      kind: "struct",
      name: "MediaAccountListResp",
      className: "MediaAccountListResp"
    }
  },
  {
    namespace: "media",
    name: "provider_model_set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5680,
    finalSchemaId: 5681,
    req: {
      kind: "struct",
      name: "MediaProviderModelSetReq",
      className: "MediaProviderModelSetReq"
    },
    final: {
      kind: "struct",
      name: "MediaProviderModelSetResp",
      className: "MediaProviderModelSetResp"
    }
  },
  {
    namespace: "media",
    name: "update_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4502,
    finalSchemaId: 4503,
    req: {
      kind: "struct",
      name: "MediaAccountUpdateReq",
      className: "MediaAccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "MediaAccountUpdateResp",
      className: "MediaAccountUpdateResp"
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
