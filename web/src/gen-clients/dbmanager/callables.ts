// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "dbmanager",
    name: "profile_get",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5926,
    finalSchemaId: 5927,
    req: {
      kind: "struct",
      name: "DbProfileGetReq",
      className: "DbProfileGetReq"
    },
    final: {
      kind: "struct",
      name: "DbProfileGetResp",
      className: "DbProfileGetResp"
    }
  },
  {
    namespace: "dbmanager",
    name: "profile_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5924,
    finalSchemaId: 5925,
    req: {
      kind: "struct",
      name: "DbProfileListReq",
      className: "DbProfileListReq"
    },
    final: {
      kind: "struct",
      name: "DbProfileListResp",
      className: "DbProfileListResp"
    }
  },
  {
    namespace: "dbmanager",
    name: "profile_remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5928,
    finalSchemaId: 5929,
    req: {
      kind: "struct",
      name: "DbProfileRemoveReq",
      className: "DbProfileRemoveReq"
    },
    final: {
      kind: "struct",
      name: "DbProfileRemoveResp",
      className: "DbProfileRemoveResp"
    }
  },
  {
    namespace: "dbmanager",
    name: "profile_save",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5922,
    finalSchemaId: 5923,
    req: {
      kind: "struct",
      name: "DbProfileSaveReq",
      className: "DbProfileSaveReq"
    },
    final: {
      kind: "struct",
      name: "DbProfileSaveResp",
      className: "DbProfileSaveResp"
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
