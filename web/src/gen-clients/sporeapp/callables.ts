// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "sporeapp",
    name: "invoke",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2198,
    finalSchemaId: 2199,
    req: {
      kind: "struct",
      name: "SporeAppInvokeReq",
      className: "SporeAppInvokeReq"
    },
    final: {
      kind: "struct",
      name: "SporeAppInvokeResp",
      className: "SporeAppInvokeResp"
    }
  },
  {
    namespace: "sporeapp",
    name: "reload",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 2194,
    finalSchemaId: 2195,
    req: {
      kind: "struct",
      name: "SporeAppReloadReq",
      className: "SporeAppReloadReq"
    },
    final: {
      kind: "struct",
      name: "SporeAppReloadResp",
      className: "SporeAppReloadResp"
    }
  },
  {
    namespace: "sporeapp",
    name: "state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2196,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "SporeAppStateReq",
      className: "SporeAppStateReq"
    },
    final: {
      kind: "map",
      name: "map",
      key: {
        kind: "scalar",
        name: "string",
        typeId: 12
      },
      value: {
        kind: "scalar",
        name: "any",
        typeId: 15
      }
    }
  },
  {
    namespace: "sporeapp",
    name: "state_set",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 2197,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "SporeAppStateSetReq",
      className: "SporeAppStateSetReq"
    },
    final: {
      kind: "map",
      name: "map",
      key: {
        kind: "scalar",
        name: "string",
        typeId: 12
      },
      value: {
        kind: "scalar",
        name: "any",
        typeId: 15
      }
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
