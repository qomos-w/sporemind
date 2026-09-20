// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "dbclient",
    name: "close",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6168,
    finalSchemaId: 6169,
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
    reqSchemaId: 6172,
    finalSchemaId: 6173,
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
    reqSchemaId: 6162,
    finalSchemaId: 6163,
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
    reqSchemaId: 6181,
    finalSchemaId: 6182,
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
    reqSchemaId: 6175,
    finalSchemaId: 6176,
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
    reqSchemaId: 6183,
    finalSchemaId: 6184,
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
    reqSchemaId: 6177,
    finalSchemaId: 6178,
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
    reqSchemaId: 6185,
    finalSchemaId: 6186,
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
    reqSchemaId: 6179,
    finalSchemaId: 6180,
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
    reqSchemaId: 6167,
    finalSchemaId: 6161,
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
    reqSchemaId: 6166,
    finalSchemaId: 6161,
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
    reqSchemaId: 6164,
    finalSchemaId: 6165,
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
