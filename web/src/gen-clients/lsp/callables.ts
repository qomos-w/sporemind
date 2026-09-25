// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "lsp",
    name: "clear_cache",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5146,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "LspClearCacheReq",
      className: "LspClearCacheReq"
    }
  },
  {
    namespace: "lsp",
    name: "code_action",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5137,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspRangeParams",
      className: "LspRangeParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "completion",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "definition",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "did_change",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5139,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspDidChangeReq",
      className: "LspDidChangeReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "did_close",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5140,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspDidCloseReq",
      className: "LspDidCloseReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "did_open",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5138,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspDidOpenReq",
      className: "LspDidOpenReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "document_symbol",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5141,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspUriReq",
      className: "LspUriReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "formatting",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5141,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspUriReq",
      className: "LspUriReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "hover",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "implementation",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "initialize",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5142,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspInitializeReq",
      className: "LspInitializeReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "install",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5155,
    finalSchemaId: 5156,
    req: {
      kind: "struct",
      name: "LspInstallReq",
      className: "LspInstallReq"
    },
    final: {
      kind: "struct",
      name: "LspInstallResp",
      className: "LspInstallResp"
    }
  },
  {
    namespace: "lsp",
    name: "prepare_rename",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "references",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5144,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspReferencesReq",
      className: "LspReferencesReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "rename",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5143,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspRenameReq",
      className: "LspRenameReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "shutdown",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5145,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspShutdownReq",
      className: "LspShutdownReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "signature_help",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "state_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5150,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "LspStateResp",
      className: "LspStateResp"
    }
  },
  {
    namespace: "lsp",
    name: "state_save",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5151,
    finalSchemaId: 5150,
    req: {
      kind: "struct",
      name: "LspStateSaveReq",
      className: "LspStateSaveReq"
    },
    final: {
      kind: "struct",
      name: "LspStateResp",
      className: "LspStateResp"
    }
  },
  {
    namespace: "lsp",
    name: "status",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5152,
    finalSchemaId: 5154,
    req: {
      kind: "struct",
      name: "LspStatusReq",
      className: "LspStatusReq"
    },
    final: {
      kind: "struct",
      name: "LspStatusResp",
      className: "LspStatusResp"
    }
  },
  {
    namespace: "lsp",
    name: "type_definition",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5136,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspPositionParams",
      className: "LspPositionParams"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
    }
  },
  {
    namespace: "lsp",
    name: "warm_up",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 5141,
    finalSchemaId: 5147,
    req: {
      kind: "struct",
      name: "LspUriReq",
      className: "LspUriReq"
    },
    final: {
      kind: "struct",
      name: "LspJsonResp",
      className: "LspJsonResp"
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
