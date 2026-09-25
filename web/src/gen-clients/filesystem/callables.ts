// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "filesystem",
    name: "edit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 746,
    finalSchemaId: 748,
    req: {
      kind: "struct",
      name: "FileSystemEditReq",
      className: "FileSystemEditReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemEditResp",
      className: "FileSystemEditResp"
    }
  },
  {
    namespace: "filesystem",
    name: "glob",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 749,
    finalSchemaId: 750,
    req: {
      kind: "struct",
      name: "FileSystemGlobReq",
      className: "FileSystemGlobReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemGlobResp",
      className: "FileSystemGlobResp"
    }
  },
  {
    namespace: "filesystem",
    name: "grep",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 751,
    finalSchemaId: 754,
    req: {
      kind: "struct",
      name: "FileSystemGrepReq",
      className: "FileSystemGrepReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemGrepResp",
      className: "FileSystemGrepResp"
    }
  },
  {
    namespace: "filesystem",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 738,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "FileSystemListReq",
      className: "FileSystemListReq"
    },
    final: {
      kind: "scalar",
      name: "string",
      typeId: 12
    }
  },
  {
    namespace: "filesystem",
    name: "list_json",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 738,
    finalSchemaId: 737,
    req: {
      kind: "struct",
      name: "FileSystemListReq",
      className: "FileSystemListReq"
    },
    final: {
      kind: "struct",
      name: "FileEntryListResp",
      className: "FileEntryListResp"
    }
  },
  {
    namespace: "filesystem",
    name: "read",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 739,
    finalSchemaId: 740,
    req: {
      kind: "struct",
      name: "FileSystemReadReq",
      className: "FileSystemReadReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadResp",
      className: "FileSystemReadResp"
    }
  },
  {
    namespace: "filesystem",
    name: "read_base64",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 741,
    finalSchemaId: 762,
    req: {
      kind: "struct",
      name: "FileSystemReadBase64Req",
      className: "FileSystemReadBase64Req"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadBase64Resp",
      className: "FileSystemReadBase64Resp"
    }
  },
  {
    namespace: "filesystem",
    name: "read_chunk",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 742,
    finalSchemaId: 743,
    req: {
      kind: "struct",
      name: "FileSystemReadChunkReq",
      className: "FileSystemReadChunkReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemReadChunkResp",
      className: "FileSystemReadChunkResp"
    }
  },
  {
    namespace: "filesystem",
    name: "rm",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 755,
    finalSchemaId: 756,
    req: {
      kind: "struct",
      name: "FileSystemRmReq",
      className: "FileSystemRmReq"
    },
    final: {
      kind: "struct",
      name: "FileSystemRmResp",
      className: "FileSystemRmResp"
    }
  },
  {
    namespace: "filesystem",
    name: "roots",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 764,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "FileSystemRootsResp",
      className: "FileSystemRootsResp"
    }
  },
  {
    namespace: "filesystem",
    name: "write",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 744,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "FileSystemWriteReq",
      className: "FileSystemWriteReq"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "filesystem",
    name: "write_base64",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 763,
    finalSchemaId: 745,
    req: {
      kind: "struct",
      name: "FileSystemWriteBase64Req",
      className: "FileSystemWriteBase64Req"
    },
    final: {
      kind: "struct",
      name: "FileSystemWriteResp",
      className: "FileSystemWriteResp"
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
