// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "websearch",
    name: "account_activate",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4592,
    finalSchemaId: 4593,
    req: {
      kind: "struct",
      name: "WebSearchAccountActivateReq",
      className: "WebSearchAccountActivateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountActivateResp",
      className: "WebSearchAccountActivateResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4586,
    finalSchemaId: 4587,
    req: {
      kind: "struct",
      name: "WebSearchAccountCreateReq",
      className: "WebSearchAccountCreateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountCreateResp",
      className: "WebSearchAccountCreateResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4590,
    finalSchemaId: 4591,
    req: {
      kind: "struct",
      name: "WebSearchAccountDeleteReq",
      className: "WebSearchAccountDeleteReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountDeleteResp",
      className: "WebSearchAccountDeleteResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4584,
    finalSchemaId: 4585,
    req: {
      kind: "struct",
      name: "WebSearchAccountListReq",
      className: "WebSearchAccountListReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountListResp",
      className: "WebSearchAccountListResp"
    }
  },
  {
    namespace: "websearch",
    name: "account_update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 4588,
    finalSchemaId: 4589,
    req: {
      kind: "struct",
      name: "WebSearchAccountUpdateReq",
      className: "WebSearchAccountUpdateReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchAccountUpdateResp",
      className: "WebSearchAccountUpdateResp"
    }
  },
  {
    namespace: "websearch",
    name: "download",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4597,
    finalSchemaId: 4598,
    req: {
      kind: "struct",
      name: "WebDownloadReq",
      className: "WebDownloadReq"
    },
    final: {
      kind: "struct",
      name: "WebDownloadResp",
      className: "WebDownloadResp"
    }
  },
  {
    namespace: "websearch",
    name: "fetch",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4594,
    finalSchemaId: 4596,
    req: {
      kind: "struct",
      name: "WebFetchReq",
      className: "WebFetchReq"
    },
    final: {
      kind: "struct",
      name: "WebFetchResp",
      className: "WebFetchResp"
    }
  },
  {
    namespace: "websearch",
    name: "provider_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 4581,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "WebSearchProviderListResp",
      className: "WebSearchProviderListResp"
    }
  },
  {
    namespace: "websearch",
    name: "search",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4576,
    finalSchemaId: 4577,
    req: {
      kind: "struct",
      name: "WebSearchReq",
      className: "WebSearchReq"
    },
    final: {
      kind: "struct",
      name: "WebSearchResp",
      className: "WebSearchResp"
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
