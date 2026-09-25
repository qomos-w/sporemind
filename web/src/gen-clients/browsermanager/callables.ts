// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "browsermanager",
    name: "create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 583,
    finalSchemaId: 587,
    req: {
      kind: "struct",
      name: "BrowserManagerCreateReq",
      className: "BrowserManagerCreateReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerAsyncResult",
      className: "BrowserManagerAsyncResult"
    }
  },
  {
    namespace: "browsermanager",
    name: "export_cookies",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 689,
    finalSchemaId: 690,
    req: {
      kind: "struct",
      name: "BrowserManagerExportCookiesReq",
      className: "BrowserManagerExportCookiesReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerExportCookiesResp",
      className: "BrowserManagerExportCookiesResp"
    }
  },
  {
    namespace: "browsermanager",
    name: "get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 585,
    finalSchemaId: 581,
    req: {
      kind: "struct",
      name: "BrowserManagerGetReq",
      className: "BrowserManagerGetReq"
    },
    final: {
      kind: "struct",
      name: "BrowserInstance",
      className: "BrowserInstance"
    }
  },
  {
    namespace: "browsermanager",
    name: "import_cookies",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 691,
    finalSchemaId: 692,
    req: {
      kind: "struct",
      name: "BrowserManagerImportCookiesReq",
      className: "BrowserManagerImportCookiesReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerImportCookiesResp",
      className: "BrowserManagerImportCookiesResp"
    }
  },
  {
    namespace: "browsermanager",
    name: "list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 582,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerListResp",
      className: "BrowserManagerListResp"
    }
  },
  {
    namespace: "browsermanager",
    name: "navigate",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 589,
    finalSchemaId: 587,
    req: {
      kind: "struct",
      name: "BrowserManagerNavigateReq",
      className: "BrowserManagerNavigateReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerAsyncResult",
      className: "BrowserManagerAsyncResult"
    }
  },
  {
    namespace: "browsermanager",
    name: "open",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 590,
    finalSchemaId: 587,
    req: {
      kind: "struct",
      name: "BrowserManagerOpenReq",
      className: "BrowserManagerOpenReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerAsyncResult",
      className: "BrowserManagerAsyncResult"
    }
  },
  {
    namespace: "browsermanager",
    name: "open_global",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 591,
    finalSchemaId: 592,
    req: {
      kind: "struct",
      name: "BrowserManagerOpenGlobalReq",
      className: "BrowserManagerOpenGlobalReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerOpenGlobalResp",
      className: "BrowserManagerOpenGlobalResp"
    }
  },
  {
    namespace: "browsermanager",
    name: "remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 584,
    finalSchemaId: 587,
    req: {
      kind: "struct",
      name: "BrowserManagerRemoveReq",
      className: "BrowserManagerRemoveReq"
    },
    final: {
      kind: "struct",
      name: "BrowserManagerAsyncResult",
      className: "BrowserManagerAsyncResult"
    }
  },
  {
    namespace: "browsermanager",
    name: "update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 586,
    finalSchemaId: 581,
    req: {
      kind: "struct",
      name: "BrowserManagerUpdateReq",
      className: "BrowserManagerUpdateReq"
    },
    final: {
      kind: "struct",
      name: "BrowserInstance",
      className: "BrowserInstance"
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
