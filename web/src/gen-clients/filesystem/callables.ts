// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "filesystem",
    name: "edit",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 730,
    finalSchemaId: 732,
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
    reqSchemaId: 733,
    finalSchemaId: 734,
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
    reqSchemaId: 735,
    finalSchemaId: 738,
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
    reqSchemaId: 722,
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
    reqSchemaId: 722,
    finalSchemaId: 721,
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
    reqSchemaId: 723,
    finalSchemaId: 724,
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
    reqSchemaId: 725,
    finalSchemaId: 746,
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
    reqSchemaId: 726,
    finalSchemaId: 727,
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
    reqSchemaId: 739,
    finalSchemaId: 740,
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
    finalSchemaId: 748,
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
    reqSchemaId: 728,
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
    reqSchemaId: 747,
    finalSchemaId: 729,
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
