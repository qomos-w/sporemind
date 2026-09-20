// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function clearCache(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.LspClearCacheReq> {
  return client.invoke<void, systemTypes.LspClearCacheReq>("lsp.clear_cache", undefined, { resSchemaId: 5002, ...opts });
}

export async function codeAction(client: GosporeClient, req: systemTypes.LspRangeParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspRangeParams, systemTypes.LspJsonResp>("lsp.code_action", req, { reqSchemaId: 4993, resSchemaId: 5003, ...opts });
}

export const codeAction_meta = {
  callable: "lsp.code_action",
  name: "code_action",
  reqSchemaId: 4993,
  resSchemaId: 5003,
} as const;

export async function completion(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.completion", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const completion_meta = {
  callable: "lsp.completion",
  name: "completion",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function definition(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.definition", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const definition_meta = {
  callable: "lsp.definition",
  name: "definition",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function didChange(client: GosporeClient, req: systemTypes.LspDidChangeReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspDidChangeReq, systemTypes.LspJsonResp>("lsp.did_change", req, { reqSchemaId: 4995, resSchemaId: 5003, ...opts });
}

export const didChange_meta = {
  callable: "lsp.did_change",
  name: "did_change",
  reqSchemaId: 4995,
  resSchemaId: 5003,
} as const;

export async function didClose(client: GosporeClient, req: systemTypes.LspDidCloseReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspDidCloseReq, systemTypes.LspJsonResp>("lsp.did_close", req, { reqSchemaId: 4996, resSchemaId: 5003, ...opts });
}

export const didClose_meta = {
  callable: "lsp.did_close",
  name: "did_close",
  reqSchemaId: 4996,
  resSchemaId: 5003,
} as const;

export async function didOpen(client: GosporeClient, req: systemTypes.LspDidOpenReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspDidOpenReq, systemTypes.LspJsonResp>("lsp.did_open", req, { reqSchemaId: 4994, resSchemaId: 5003, ...opts });
}

export const didOpen_meta = {
  callable: "lsp.did_open",
  name: "did_open",
  reqSchemaId: 4994,
  resSchemaId: 5003,
} as const;

export async function documentSymbol(client: GosporeClient, req: systemTypes.LspUriReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspUriReq, systemTypes.LspJsonResp>("lsp.document_symbol", req, { reqSchemaId: 4997, resSchemaId: 5003, ...opts });
}

export const documentSymbol_meta = {
  callable: "lsp.document_symbol",
  name: "document_symbol",
  reqSchemaId: 4997,
  resSchemaId: 5003,
} as const;

export async function formatting(client: GosporeClient, req: systemTypes.LspUriReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspUriReq, systemTypes.LspJsonResp>("lsp.formatting", req, { reqSchemaId: 4997, resSchemaId: 5003, ...opts });
}

export const formatting_meta = {
  callable: "lsp.formatting",
  name: "formatting",
  reqSchemaId: 4997,
  resSchemaId: 5003,
} as const;

export async function hover(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.hover", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const hover_meta = {
  callable: "lsp.hover",
  name: "hover",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function implementation(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.implementation", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const implementation_meta = {
  callable: "lsp.implementation",
  name: "implementation",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function initialize(client: GosporeClient, req: systemTypes.LspInitializeReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspInitializeReq, systemTypes.LspJsonResp>("lsp.initialize", req, { reqSchemaId: 4998, resSchemaId: 5003, ...opts });
}

export const initialize_meta = {
  callable: "lsp.initialize",
  name: "initialize",
  reqSchemaId: 4998,
  resSchemaId: 5003,
} as const;

export async function install(client: GosporeClient, req: systemTypes.LspInstallReq, opts?: InvokeOptions): Promise<systemTypes.LspInstallResp> {
  return client.invoke<systemTypes.LspInstallReq, systemTypes.LspInstallResp>("lsp.install", req, { reqSchemaId: 5011, resSchemaId: 5012, ...opts });
}

export const install_meta = {
  callable: "lsp.install",
  name: "install",
  reqSchemaId: 5011,
  resSchemaId: 5012,
} as const;

export async function prepareRename(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.prepare_rename", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const prepareRename_meta = {
  callable: "lsp.prepare_rename",
  name: "prepare_rename",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function references(client: GosporeClient, req: systemTypes.LspReferencesReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspReferencesReq, systemTypes.LspJsonResp>("lsp.references", req, { reqSchemaId: 5000, resSchemaId: 5003, ...opts });
}

export const references_meta = {
  callable: "lsp.references",
  name: "references",
  reqSchemaId: 5000,
  resSchemaId: 5003,
} as const;

export async function rename(client: GosporeClient, req: systemTypes.LspRenameReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspRenameReq, systemTypes.LspJsonResp>("lsp.rename", req, { reqSchemaId: 4999, resSchemaId: 5003, ...opts });
}

export const rename_meta = {
  callable: "lsp.rename",
  name: "rename",
  reqSchemaId: 4999,
  resSchemaId: 5003,
} as const;

export async function shutdown(client: GosporeClient, req: systemTypes.LspShutdownReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspShutdownReq, systemTypes.LspJsonResp>("lsp.shutdown", req, { reqSchemaId: 5001, resSchemaId: 5003, ...opts });
}

export const shutdown_meta = {
  callable: "lsp.shutdown",
  name: "shutdown",
  reqSchemaId: 5001,
  resSchemaId: 5003,
} as const;

export async function signatureHelp(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.signature_help", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const signatureHelp_meta = {
  callable: "lsp.signature_help",
  name: "signature_help",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function stateGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.LspStateResp> {
  return client.invoke<void, systemTypes.LspStateResp>("lsp.state_get", undefined, { resSchemaId: 5006, ...opts });
}

export async function stateSave(client: GosporeClient, req: systemTypes.LspStateSaveReq, opts?: InvokeOptions): Promise<systemTypes.LspStateResp> {
  return client.invoke<systemTypes.LspStateSaveReq, systemTypes.LspStateResp>("lsp.state_save", req, { reqSchemaId: 5007, resSchemaId: 5006, ...opts });
}

export const stateSave_meta = {
  callable: "lsp.state_save",
  name: "state_save",
  reqSchemaId: 5007,
  resSchemaId: 5006,
} as const;

export async function status(client: GosporeClient, req: systemTypes.LspStatusReq, opts?: InvokeOptions): Promise<systemTypes.LspStatusResp> {
  return client.invoke<systemTypes.LspStatusReq, systemTypes.LspStatusResp>("lsp.status", req, { reqSchemaId: 5008, resSchemaId: 5010, ...opts });
}

export const status_meta = {
  callable: "lsp.status",
  name: "status",
  reqSchemaId: 5008,
  resSchemaId: 5010,
} as const;

export async function typeDefinition(client: GosporeClient, req: systemTypes.LspPositionParams, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspPositionParams, systemTypes.LspJsonResp>("lsp.type_definition", req, { reqSchemaId: 4992, resSchemaId: 5003, ...opts });
}

export const typeDefinition_meta = {
  callable: "lsp.type_definition",
  name: "type_definition",
  reqSchemaId: 4992,
  resSchemaId: 5003,
} as const;

export async function warmUp(client: GosporeClient, req: systemTypes.LspUriReq, opts?: InvokeOptions): Promise<systemTypes.LspJsonResp> {
  return client.invoke<systemTypes.LspUriReq, systemTypes.LspJsonResp>("lsp.warm_up", req, { reqSchemaId: 4997, resSchemaId: 5003, ...opts });
}

export const warmUp_meta = {
  callable: "lsp.warm_up",
  name: "warm_up",
  reqSchemaId: 4997,
  resSchemaId: 5003,
} as const;

