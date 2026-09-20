// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function aggregatorConfigure(client: GosporeClient, req: systemTypes.AIManagerAggregatorConfigureReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerAggregatorConfigureResp> {
  return client.invoke<systemTypes.AIManagerAggregatorConfigureReq, systemTypes.AIManagerAggregatorConfigureResp>("aimanager.aggregator_configure", req, { reqSchemaId: 1425, resSchemaId: 1426, ...opts });
}

export const aggregatorConfigure_meta = {
  callable: "aimanager.aggregator_configure",
  name: "aggregator_configure",
  reqSchemaId: 1425,
  resSchemaId: 1426,
} as const;

export async function aggregatorGet(client: GosporeClient, req: systemTypes.AIManagerAggregatorGetReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerAggregatorGetResp> {
  return client.invoke<systemTypes.AIManagerAggregatorGetReq, systemTypes.AIManagerAggregatorGetResp>("aimanager.aggregator_get", req, { reqSchemaId: 1427, resSchemaId: 1428, ...opts });
}

export const aggregatorGet_meta = {
  callable: "aimanager.aggregator_get",
  name: "aggregator_get",
  reqSchemaId: 1427,
  resSchemaId: 1428,
} as const;

export async function aggregatorList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AggregatorDescriptorListResp> {
  return client.invoke<void, systemTypes.AggregatorDescriptorListResp>("aimanager.aggregator_list", undefined, { resSchemaId: 351, ...opts });
}

export async function aggregatorSetDisabled(client: GosporeClient, req: systemTypes.AIManagerAggregatorSetDisabledReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerAggregatorSetDisabledResp> {
  return client.invoke<systemTypes.AIManagerAggregatorSetDisabledReq, systemTypes.AIManagerAggregatorSetDisabledResp>("aimanager.aggregator_set_disabled", req, { reqSchemaId: 1454, resSchemaId: 1455, ...opts });
}

export const aggregatorSetDisabled_meta = {
  callable: "aimanager.aggregator_set_disabled",
  name: "aggregator_set_disabled",
  reqSchemaId: 1454,
  resSchemaId: 1455,
} as const;

export async function configExport(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AIManagerConfigExportResp> {
  return client.invoke<void, systemTypes.AIManagerConfigExportResp>("aimanager.config_export", undefined, { resSchemaId: 1432, ...opts });
}

export async function configImport(client: GosporeClient, req: systemTypes.AIManagerConfigImportReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerConfigImportResp> {
  return client.invoke<systemTypes.AIManagerConfigImportReq, systemTypes.AIManagerConfigImportResp>("aimanager.config_import", req, { reqSchemaId: 1433, resSchemaId: 1434, ...opts });
}

export const configImport_meta = {
  callable: "aimanager.config_import",
  name: "config_import",
  reqSchemaId: 1433,
  resSchemaId: 1434,
} as const;

export async function fetchOpenrouterModels(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AIManagerFetchOpenRouterModelsResp> {
  return client.invoke<void, systemTypes.AIManagerFetchOpenRouterModelsResp>("aimanager.fetch_openrouter_models", undefined, { resSchemaId: 4950, ...opts });
}

export async function listUnits(client: GosporeClient, req: systemTypes.AIManagerListUnitsReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerListUnitsResp> {
  return client.invoke<systemTypes.AIManagerListUnitsReq, systemTypes.AIManagerListUnitsResp>("aimanager.list_units", req, { reqSchemaId: 1456, resSchemaId: 1457, ...opts });
}

export const listUnits_meta = {
  callable: "aimanager.list_units",
  name: "list_units",
  reqSchemaId: 1456,
  resSchemaId: 1457,
} as const;

export async function modelDefaultsGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AIManagerModelDefaultsGetResp> {
  return client.invoke<void, systemTypes.AIManagerModelDefaultsGetResp>("aimanager.model_defaults_get", undefined, { resSchemaId: 4946, ...opts });
}

export async function modelDefaultsSet(client: GosporeClient, req: systemTypes.AIManagerModelDefaultsSetReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerModelDefaultsSetResp> {
  return client.invoke<systemTypes.AIManagerModelDefaultsSetReq, systemTypes.AIManagerModelDefaultsSetResp>("aimanager.model_defaults_set", req, { reqSchemaId: 4947, resSchemaId: 4948, ...opts });
}

export const modelDefaultsSet_meta = {
  callable: "aimanager.model_defaults_set",
  name: "model_defaults_set",
  reqSchemaId: 4947,
  resSchemaId: 4948,
} as const;

export async function modelList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ModelListResp> {
  return client.invoke<void, systemTypes.ModelListResp>("aimanager.model_list", undefined, { resSchemaId: 1417, ...opts });
}

export async function providerConfigure(client: GosporeClient, req: systemTypes.AIManagerProviderConfigureReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderConfigureResp> {
  return client.invoke<systemTypes.AIManagerProviderConfigureReq, systemTypes.AIManagerProviderConfigureResp>("aimanager.provider_configure", req, { reqSchemaId: 1413, resSchemaId: 1414, ...opts });
}

export const providerConfigure_meta = {
  callable: "aimanager.provider_configure",
  name: "provider_configure",
  reqSchemaId: 1413,
  resSchemaId: 1414,
} as const;

export async function providerFetchModels(client: GosporeClient, req: systemTypes.AIManagerProviderFetchModelsReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderFetchModelsResp> {
  return client.invoke<systemTypes.AIManagerProviderFetchModelsReq, systemTypes.AIManagerProviderFetchModelsResp>("aimanager.provider_fetch_models", req, { reqSchemaId: 1418, resSchemaId: 1419, ...opts });
}

export const providerFetchModels_meta = {
  callable: "aimanager.provider_fetch_models",
  name: "provider_fetch_models",
  reqSchemaId: 1418,
  resSchemaId: 1419,
} as const;

export async function providerList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.ProviderListResp> {
  return client.invoke<void, systemTypes.ProviderListResp>("aimanager.provider_list", undefined, { resSchemaId: 1416, ...opts });
}

export async function providerRecordProbe(client: GosporeClient, req: systemTypes.AIManagerProviderRecordProbeReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderRecordProbeResp> {
  return client.invoke<systemTypes.AIManagerProviderRecordProbeReq, systemTypes.AIManagerProviderRecordProbeResp>("aimanager.provider_record_probe", req, { reqSchemaId: 1447, resSchemaId: 1448, ...opts });
}

export const providerRecordProbe_meta = {
  callable: "aimanager.provider_record_probe",
  name: "provider_record_probe",
  reqSchemaId: 1447,
  resSchemaId: 1448,
} as const;

export async function providerResetHealth(client: GosporeClient, req: systemTypes.AIManagerProviderResetHealthReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderResetHealthResp> {
  return client.invoke<systemTypes.AIManagerProviderResetHealthReq, systemTypes.AIManagerProviderResetHealthResp>("aimanager.provider_reset_health", req, { reqSchemaId: 1445, resSchemaId: 1446, ...opts });
}

export const providerResetHealth_meta = {
  callable: "aimanager.provider_reset_health",
  name: "provider_reset_health",
  reqSchemaId: 1445,
  resSchemaId: 1446,
} as const;

export async function providerSetDisabled(client: GosporeClient, req: systemTypes.AIManagerProviderSetDisabledReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderSetDisabledResp> {
  return client.invoke<systemTypes.AIManagerProviderSetDisabledReq, systemTypes.AIManagerProviderSetDisabledResp>("aimanager.provider_set_disabled", req, { reqSchemaId: 1452, resSchemaId: 1453, ...opts });
}

export const providerSetDisabled_meta = {
  callable: "aimanager.provider_set_disabled",
  name: "provider_set_disabled",
  reqSchemaId: 1452,
  resSchemaId: 1453,
} as const;

export async function providerSetTokenPlan(client: GosporeClient, req: systemTypes.AIManagerProviderSetTokenPlanReq, opts?: InvokeOptions): Promise<systemTypes.AIManagerProviderSetTokenPlanResp> {
  return client.invoke<systemTypes.AIManagerProviderSetTokenPlanReq, systemTypes.AIManagerProviderSetTokenPlanResp>("aimanager.provider_set_token_plan", req, { reqSchemaId: 1441, resSchemaId: 1442, ...opts });
}

export const providerSetTokenPlan_meta = {
  callable: "aimanager.provider_set_token_plan",
  name: "provider_set_token_plan",
  reqSchemaId: 1441,
  resSchemaId: 1442,
} as const;

export async function unitHealthList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AIManagerUnitHealthListResp> {
  return client.invoke<void, systemTypes.AIManagerUnitHealthListResp>("aimanager.unit_health_list", undefined, { resSchemaId: 1451, ...opts });
}

