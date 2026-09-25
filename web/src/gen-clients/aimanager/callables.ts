// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "aimanager",
    name: "aggregator_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1473,
    finalSchemaId: 1474,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorConfigureReq",
      className: "AIManagerAggregatorConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorConfigureResp",
      className: "AIManagerAggregatorConfigureResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1475,
    finalSchemaId: 1476,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorGetReq",
      className: "AIManagerAggregatorGetReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorGetResp",
      className: "AIManagerAggregatorGetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 351,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AggregatorDescriptorListResp",
      className: "AggregatorDescriptorListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "aggregator_set_disabled",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1502,
    finalSchemaId: 1503,
    req: {
      kind: "struct",
      name: "AIManagerAggregatorSetDisabledReq",
      className: "AIManagerAggregatorSetDisabledReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerAggregatorSetDisabledResp",
      className: "AIManagerAggregatorSetDisabledResp"
    }
  },
  {
    namespace: "aimanager",
    name: "config_export",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1480,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerConfigExportResp",
      className: "AIManagerConfigExportResp"
    }
  },
  {
    namespace: "aimanager",
    name: "config_import",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1481,
    finalSchemaId: 1482,
    req: {
      kind: "struct",
      name: "AIManagerConfigImportReq",
      className: "AIManagerConfigImportReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerConfigImportResp",
      className: "AIManagerConfigImportResp"
    }
  },
  {
    namespace: "aimanager",
    name: "fetch_openrouter_models",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5094,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerFetchOpenRouterModelsResp",
      className: "AIManagerFetchOpenRouterModelsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "list_units",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1504,
    finalSchemaId: 1505,
    req: {
      kind: "struct",
      name: "AIManagerListUnitsReq",
      className: "AIManagerListUnitsReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerListUnitsResp",
      className: "AIManagerListUnitsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_defaults_get",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 5090,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerModelDefaultsGetResp",
      className: "AIManagerModelDefaultsGetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_defaults_set",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 5091,
    finalSchemaId: 5092,
    req: {
      kind: "struct",
      name: "AIManagerModelDefaultsSetReq",
      className: "AIManagerModelDefaultsSetReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerModelDefaultsSetResp",
      className: "AIManagerModelDefaultsSetResp"
    }
  },
  {
    namespace: "aimanager",
    name: "model_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1465,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ModelListResp",
      className: "ModelListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_configure",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1461,
    finalSchemaId: 1462,
    req: {
      kind: "struct",
      name: "AIManagerProviderConfigureReq",
      className: "AIManagerProviderConfigureReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderConfigureResp",
      className: "AIManagerProviderConfigureResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_fetch_models",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 1466,
    finalSchemaId: 1467,
    req: {
      kind: "struct",
      name: "AIManagerProviderFetchModelsReq",
      className: "AIManagerProviderFetchModelsReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderFetchModelsResp",
      className: "AIManagerProviderFetchModelsResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1464,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "ProviderListResp",
      className: "ProviderListResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_record_probe",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1495,
    finalSchemaId: 1496,
    req: {
      kind: "struct",
      name: "AIManagerProviderRecordProbeReq",
      className: "AIManagerProviderRecordProbeReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderRecordProbeResp",
      className: "AIManagerProviderRecordProbeResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_reset_health",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1493,
    finalSchemaId: 1494,
    req: {
      kind: "struct",
      name: "AIManagerProviderResetHealthReq",
      className: "AIManagerProviderResetHealthReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderResetHealthResp",
      className: "AIManagerProviderResetHealthResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_set_disabled",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1500,
    finalSchemaId: 1501,
    req: {
      kind: "struct",
      name: "AIManagerProviderSetDisabledReq",
      className: "AIManagerProviderSetDisabledReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderSetDisabledResp",
      className: "AIManagerProviderSetDisabledResp"
    }
  },
  {
    namespace: "aimanager",
    name: "provider_set_token_plan",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1489,
    finalSchemaId: 1490,
    req: {
      kind: "struct",
      name: "AIManagerProviderSetTokenPlanReq",
      className: "AIManagerProviderSetTokenPlanReq"
    },
    final: {
      kind: "struct",
      name: "AIManagerProviderSetTokenPlanResp",
      className: "AIManagerProviderSetTokenPlanResp"
    }
  },
  {
    namespace: "aimanager",
    name: "unit_health_list",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 0,
    finalSchemaId: 1499,
    req: {
      kind: "void",
      name: "void"
    },
    final: {
      kind: "struct",
      name: "AIManagerUnitHealthListResp",
      className: "AIManagerUnitHealthListResp"
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
