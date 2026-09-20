// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "shell",
    name: "bash",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 1490,
    chunkSchemaId: 1492,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ShellBashReq",
      className: "ShellBashReq"
    },
    chunk: {
      kind: "struct",
      name: "ShellChunk",
      className: "ShellChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "shell",
    name: "exec",
    visibility: "public",
    mode: "streaming",
    reqSchemaId: 1488,
    chunkSchemaId: 1492,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "ShellExecReq",
      className: "ShellExecReq"
    },
    chunk: {
      kind: "struct",
      name: "ShellChunk",
      className: "ShellChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "shell",
    name: "session_close",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5062,
    finalSchemaId: 5063,
    req: {
      kind: "struct",
      name: "ShellSessionCloseReq",
      className: "ShellSessionCloseReq"
    },
    final: {
      kind: "struct",
      name: "ShellSessionCloseResp",
      className: "ShellSessionCloseResp"
    }
  },
  {
    namespace: "shell",
    name: "session_fetch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5152,
    finalSchemaId: 5153,
    req: {
      kind: "struct",
      name: "ShellSessionFetchReq",
      className: "ShellSessionFetchReq"
    },
    final: {
      kind: "struct",
      name: "ShellSessionFetchResp",
      className: "ShellSessionFetchResp"
    }
  },
  {
    namespace: "shell",
    name: "session_open",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5056,
    finalSchemaId: 5057,
    req: {
      kind: "struct",
      name: "ShellSessionOpenReq",
      className: "ShellSessionOpenReq"
    },
    final: {
      kind: "struct",
      name: "ShellSessionOpenResp",
      className: "ShellSessionOpenResp"
    }
  },
  {
    namespace: "shell",
    name: "session_resize",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5060,
    finalSchemaId: 5061,
    req: {
      kind: "struct",
      name: "ShellSessionResizeReq",
      className: "ShellSessionResizeReq"
    },
    final: {
      kind: "struct",
      name: "ShellSessionResizeResp",
      className: "ShellSessionResizeResp"
    }
  },
  {
    namespace: "shell",
    name: "session_write",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5058,
    finalSchemaId: 5059,
    req: {
      kind: "struct",
      name: "ShellSessionWriteReq",
      className: "ShellSessionWriteReq"
    },
    final: {
      kind: "struct",
      name: "ShellSessionWriteResp",
      className: "ShellSessionWriteResp"
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
