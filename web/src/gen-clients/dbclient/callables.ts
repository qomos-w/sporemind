// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "dbclient",
    name: "close",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6264,
    finalSchemaId: 6265,
    req: {
      kind: "struct",
      name: "DbCloseReq",
      className: "DbCloseReq"
    },
    final: {
      kind: "struct",
      name: "DbCloseResp",
      className: "DbCloseResp"
    }
  },
  {
    namespace: "dbclient",
    name: "describe",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6268,
    finalSchemaId: 6269,
    req: {
      kind: "struct",
      name: "DbDescribeReq",
      className: "DbDescribeReq"
    },
    final: {
      kind: "struct",
      name: "DbDescribeResp",
      className: "DbDescribeResp"
    }
  },
  {
    namespace: "dbclient",
    name: "dial_test",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6258,
    finalSchemaId: 6259,
    req: {
      kind: "struct",
      name: "DbDialTestReq",
      className: "DbDialTestReq"
    },
    final: {
      kind: "struct",
      name: "DbDialTestResp",
      className: "DbDialTestResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6277,
    finalSchemaId: 6278,
    req: {
      kind: "struct",
      name: "DbObjectDeleteReq",
      className: "DbObjectDeleteReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectDeleteResp",
      className: "DbObjectDeleteResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6271,
    finalSchemaId: 6272,
    req: {
      kind: "struct",
      name: "DbObjectListReq",
      className: "DbObjectListReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectListResp",
      className: "DbObjectListResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_mkdir",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6279,
    finalSchemaId: 6280,
    req: {
      kind: "struct",
      name: "DbObjectMkdirReq",
      className: "DbObjectMkdirReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectMkdirResp",
      className: "DbObjectMkdirResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_read",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6273,
    finalSchemaId: 6274,
    req: {
      kind: "struct",
      name: "DbObjectReadReq",
      className: "DbObjectReadReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectReadResp",
      className: "DbObjectReadResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_stat",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6281,
    finalSchemaId: 6282,
    req: {
      kind: "struct",
      name: "DbObjectStatReq",
      className: "DbObjectStatReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectStatResp",
      className: "DbObjectStatResp"
    }
  },
  {
    namespace: "dbclient",
    name: "object_write",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6275,
    finalSchemaId: 6276,
    req: {
      kind: "struct",
      name: "DbObjectWriteReq",
      className: "DbObjectWriteReq"
    },
    final: {
      kind: "struct",
      name: "DbObjectWriteResp",
      className: "DbObjectWriteResp"
    }
  },
  {
    namespace: "dbclient",
    name: "query",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6263,
    finalSchemaId: 6257,
    req: {
      kind: "struct",
      name: "DbQueryReq",
      className: "DbQueryReq"
    },
    final: {
      kind: "struct",
      name: "DbRows",
      className: "DbRows"
    }
  },
  {
    namespace: "dbclient",
    name: "read",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6262,
    finalSchemaId: 6257,
    req: {
      kind: "struct",
      name: "DbReadReq",
      className: "DbReadReq"
    },
    final: {
      kind: "struct",
      name: "DbRows",
      className: "DbRows"
    }
  },
  {
    namespace: "dbclient",
    name: "tree",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6260,
    finalSchemaId: 6261,
    req: {
      kind: "struct",
      name: "DbTreeReq",
      className: "DbTreeReq"
    },
    final: {
      kind: "struct",
      name: "DbTreeResp",
      className: "DbTreeResp"
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
