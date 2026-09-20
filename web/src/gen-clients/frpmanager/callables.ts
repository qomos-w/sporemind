// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "frpmanager",
    name: "create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 790,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerCreateReq",
      className: "FrpManagerCreateReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
    }
  },
  {
    namespace: "frpmanager",
    name: "detect",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 798,
    finalSchemaId: 799,
    req: {
      kind: "struct",
      name: "FrpManagerDetectReq",
      className: "FrpManagerDetectReq"
    },
    final: {
      kind: "struct",
      name: "FrpManagerDetectResp",
      className: "FrpManagerDetectResp"
    }
  },
  {
    namespace: "frpmanager",
    name: "get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 792,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerGetReq",
      className: "FrpManagerGetReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
    }
  },
  {
    namespace: "frpmanager",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 793,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "FrpManagerListResp",
      className: "FrpManagerListResp"
    }
  },
  {
    namespace: "frpmanager",
    name: "remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 791,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerRemoveReq",
      className: "FrpManagerRemoveReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
    }
  },
  {
    namespace: "frpmanager",
    name: "start",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 796,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerStartReq",
      className: "FrpManagerStartReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
    }
  },
  {
    namespace: "frpmanager",
    name: "stop",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 797,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerStopReq",
      className: "FrpManagerStopReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
    }
  },
  {
    namespace: "frpmanager",
    name: "update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 795,
    finalSchemaId: 789,
    req: {
      kind: "struct",
      name: "FrpManagerUpdateReq",
      className: "FrpManagerUpdateReq"
    },
    final: {
      kind: "struct",
      name: "FrpInstance",
      className: "FrpInstance"
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
