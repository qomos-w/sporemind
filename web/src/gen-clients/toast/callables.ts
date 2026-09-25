// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "toast",
    name: "action",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 6328,
    finalSchemaId: 6329,
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
    reqSchemaId: 6323,
    finalSchemaId: 6324,
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
    reqSchemaId: 6321,
    finalSchemaId: 6322,
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
    reqSchemaId: 6326,
    finalSchemaId: 6327,
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
