// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "toast",
    name: "action",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6216,
    finalSchemaId: 6217,
    req: {
      kind: "struct",
      name: "ToastActionReq",
      className: "ToastActionReq"
    },
    final: {
      kind: "struct",
      name: "ToastActionResp",
      className: "ToastActionResp"
    }
  },
  {
    namespace: "toast",
    name: "dismiss",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6211,
    finalSchemaId: 6212,
    req: {
      kind: "struct",
      name: "ToastDismissReq",
      className: "ToastDismissReq"
    },
    final: {
      kind: "struct",
      name: "ToastDismissResp",
      className: "ToastDismissResp"
    }
  },
  {
    namespace: "toast",
    name: "show",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6209,
    finalSchemaId: 6210,
    req: {
      kind: "struct",
      name: "ToastShowReq",
      className: "ToastShowReq"
    },
    final: {
      kind: "struct",
      name: "ToastShowResp",
      className: "ToastShowResp"
    }
  },
  {
    namespace: "toast",
    name: "state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6214,
    finalSchemaId: 6215,
    req: {
      kind: "struct",
      name: "ToastStateReq",
      className: "ToastStateReq"
    },
    final: {
      kind: "struct",
      name: "ToastStateResp",
      className: "ToastStateResp"
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
