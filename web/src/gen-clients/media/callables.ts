// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "media",
    name: "activate_account",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4650,
    finalSchemaId: 4651,
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
    reqSchemaId: 4644,
    finalSchemaId: 4645,
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
    reqSchemaId: 4648,
    finalSchemaId: 4649,
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
    reqSchemaId: 4642,
    finalSchemaId: 4643,
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
    reqSchemaId: 5824,
    finalSchemaId: 5825,
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
    reqSchemaId: 4646,
    finalSchemaId: 4647,
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
