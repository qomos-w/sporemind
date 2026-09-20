// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "storeclient",
    name: "config_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 6659,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "StoreClientConfigGetResp",
      className: "StoreClientConfigGetResp"
    }
  },
  {
    namespace: "storeclient",
    name: "config_set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6660,
    finalSchemaId: 6661,
    req: {
      kind: "struct",
      name: "StoreClientConfigSetReq",
      className: "StoreClientConfigSetReq"
    },
    final: {
      kind: "struct",
      name: "StoreClientConfigSetResp",
      className: "StoreClientConfigSetResp"
    }
  },
  {
    namespace: "storeclient",
    name: "index",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 6663,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "StoreIndexResp",
      className: "StoreIndexResp"
    }
  },
  {
    namespace: "storeclient",
    name: "install",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6667,
    finalSchemaId: 6668,
    req: {
      kind: "struct",
      name: "StoreInstallReq",
      className: "StoreInstallReq"
    },
    final: {
      kind: "struct",
      name: "StoreInstallResp",
      className: "StoreInstallResp"
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
