// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function activateAccount(client: GosporeClient, req: systemTypes.VoiceAccountActivateReq, opts?: InvokeOptions): Promise<systemTypes.VoiceAccountActivateResp> {
  return client.invoke<systemTypes.VoiceAccountActivateReq, systemTypes.VoiceAccountActivateResp>("voice.activate_account", req, { reqSchemaId: 1818, resSchemaId: 1819, ...opts });
}

export const activateAccount_meta = {
  callable: "voice.activate_account",
  name: "activate_account",
  reqSchemaId: 1818,
  resSchemaId: 1819,
} as const;

export async function clone(client: GosporeClient, req: systemTypes.VoiceCloneReq, opts?: InvokeOptions): Promise<systemTypes.VoiceCloneResp> {
  return client.invoke<systemTypes.VoiceCloneReq, systemTypes.VoiceCloneResp>("voice.clone", req, { reqSchemaId: 1831, resSchemaId: 1832, ...opts });
}

export const clone_meta = {
  callable: "voice.clone",
  name: "clone",
  reqSchemaId: 1831,
  resSchemaId: 1832,
} as const;

export async function configExport(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.VoiceConfigExportResp> {
  return client.invoke<void, systemTypes.VoiceConfigExportResp>("voice.config_export", undefined, { resSchemaId: 1826, ...opts });
}

export async function configImport(client: GosporeClient, req: systemTypes.VoiceConfigImportReq, opts?: InvokeOptions): Promise<systemTypes.VoiceConfigImportResp> {
  return client.invoke<systemTypes.VoiceConfigImportReq, systemTypes.VoiceConfigImportResp>("voice.config_import", req, { reqSchemaId: 1827, resSchemaId: 1828, ...opts });
}

export const configImport_meta = {
  callable: "voice.config_import",
  name: "config_import",
  reqSchemaId: 1827,
  resSchemaId: 1828,
} as const;

export async function createAccount(client: GosporeClient, req: systemTypes.VoiceAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.VoiceAccountCreateResp> {
  return client.invoke<systemTypes.VoiceAccountCreateReq, systemTypes.VoiceAccountCreateResp>("voice.create_account", req, { reqSchemaId: 1812, resSchemaId: 1813, ...opts });
}

export const createAccount_meta = {
  callable: "voice.create_account",
  name: "create_account",
  reqSchemaId: 1812,
  resSchemaId: 1813,
} as const;

export async function deleteAccount(client: GosporeClient, req: systemTypes.VoiceAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.VoiceAccountDeleteResp> {
  return client.invoke<systemTypes.VoiceAccountDeleteReq, systemTypes.VoiceAccountDeleteResp>("voice.delete_account", req, { reqSchemaId: 1816, resSchemaId: 1817, ...opts });
}

export const deleteAccount_meta = {
  callable: "voice.delete_account",
  name: "delete_account",
  reqSchemaId: 1816,
  resSchemaId: 1817,
} as const;

export async function deleteHotwords(client: GosporeClient, req: systemTypes.VoiceHotwordsDeleteReq, opts?: InvokeOptions): Promise<systemTypes.VoiceHotwordsDeleteResp> {
  return client.invoke<systemTypes.VoiceHotwordsDeleteReq, systemTypes.VoiceHotwordsDeleteResp>("voice.delete_hotwords", req, { reqSchemaId: 3829, resSchemaId: 3830, ...opts });
}

export const deleteHotwords_meta = {
  callable: "voice.delete_hotwords",
  name: "delete_hotwords",
  reqSchemaId: 3829,
  resSchemaId: 3830,
} as const;

export async function design(client: GosporeClient, req: systemTypes.VoiceDesignReq, opts?: InvokeOptions): Promise<systemTypes.VoiceDesignResp> {
  return client.invoke<systemTypes.VoiceDesignReq, systemTypes.VoiceDesignResp>("voice.design", req, { reqSchemaId: 1833, resSchemaId: 1834, ...opts });
}

export const design_meta = {
  callable: "voice.design",
  name: "design",
  reqSchemaId: 1833,
  resSchemaId: 1834,
} as const;

export async function getHotwords(client: GosporeClient, req: systemTypes.VoiceHotwordsGetReq, opts?: InvokeOptions): Promise<systemTypes.VoiceHotwordsGetResp> {
  return client.invoke<systemTypes.VoiceHotwordsGetReq, systemTypes.VoiceHotwordsGetResp>("voice.get_hotwords", req, { reqSchemaId: 3827, resSchemaId: 3828, ...opts });
}

export const getHotwords_meta = {
  callable: "voice.get_hotwords",
  name: "get_hotwords",
  reqSchemaId: 3827,
  resSchemaId: 3828,
} as const;

export async function getNotifyConfig(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.VoiceNotifyConfigResp> {
  return client.invoke<void, systemTypes.VoiceNotifyConfigResp>("voice.get_notify_config", undefined, { resSchemaId: 1830, ...opts });
}

export async function listAccounts(client: GosporeClient, req: systemTypes.VoiceAccountListReq, opts?: InvokeOptions): Promise<systemTypes.VoiceAccountListResp> {
  return client.invoke<systemTypes.VoiceAccountListReq, systemTypes.VoiceAccountListResp>("voice.list_accounts", req, { reqSchemaId: 1810, resSchemaId: 1811, ...opts });
}

export const listAccounts_meta = {
  callable: "voice.list_accounts",
  name: "list_accounts",
  reqSchemaId: 1810,
  resSchemaId: 1811,
} as const;

export async function recognize(client: GosporeClient, req: systemTypes.VoiceRecognizeReq, opts?: InvokeOptions): Promise<systemTypes.VoiceRecognizeResp> {
  return client.invoke<systemTypes.VoiceRecognizeReq, systemTypes.VoiceRecognizeResp>("voice.recognize", req, { reqSchemaId: 1821, resSchemaId: 1822, ...opts });
}

export const recognize_meta = {
  callable: "voice.recognize",
  name: "recognize",
  reqSchemaId: 1821,
  resSchemaId: 1822,
} as const;

export async function setHotwords(client: GosporeClient, req: systemTypes.VoiceHotwordsSetReq, opts?: InvokeOptions): Promise<systemTypes.VoiceHotwordsSetResp> {
  return client.invoke<systemTypes.VoiceHotwordsSetReq, systemTypes.VoiceHotwordsSetResp>("voice.set_hotwords", req, { reqSchemaId: 3825, resSchemaId: 3826, ...opts });
}

export const setHotwords_meta = {
  callable: "voice.set_hotwords",
  name: "set_hotwords",
  reqSchemaId: 3825,
  resSchemaId: 3826,
} as const;

export async function setNotifyConfig(client: GosporeClient, req: systemTypes.VoiceNotifyConfig, opts?: InvokeOptions): Promise<systemTypes.VoiceNotifyConfigResp> {
  return client.invoke<systemTypes.VoiceNotifyConfig, systemTypes.VoiceNotifyConfigResp>("voice.set_notify_config", req, { reqSchemaId: 1829, resSchemaId: 1830, ...opts });
}

export const setNotifyConfig_meta = {
  callable: "voice.set_notify_config",
  name: "set_notify_config",
  reqSchemaId: 1829,
  resSchemaId: 1830,
} as const;

export async function synthesize(client: GosporeClient, req: systemTypes.VoiceSynthesizeReq, opts?: InvokeOptions): Promise<systemTypes.VoiceSynthesizeResp> {
  return client.invoke<systemTypes.VoiceSynthesizeReq, systemTypes.VoiceSynthesizeResp>("voice.synthesize", req, { reqSchemaId: 1823, resSchemaId: 1824, ...opts });
}

export const synthesize_meta = {
  callable: "voice.synthesize",
  name: "synthesize",
  reqSchemaId: 1823,
  resSchemaId: 1824,
} as const;

export async function updateAccount(client: GosporeClient, req: systemTypes.VoiceAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.VoiceAccountUpdateResp> {
  return client.invoke<systemTypes.VoiceAccountUpdateReq, systemTypes.VoiceAccountUpdateResp>("voice.update_account", req, { reqSchemaId: 1814, resSchemaId: 1815, ...opts });
}

export const updateAccount_meta = {
  callable: "voice.update_account",
  name: "update_account",
  reqSchemaId: 1814,
  resSchemaId: 1815,
} as const;

