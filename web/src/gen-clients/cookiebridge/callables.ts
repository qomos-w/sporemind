// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "cookiebridge",
    name: "pairing_info",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6368,
    finalSchemaId: 6369,
    req: {
      kind: "struct",
      name: "CookieBridgePairingInfoReq",
      className: "CookieBridgePairingInfoReq"
    },
    final: {
      kind: "struct",
      name: "CookieBridgePairingInfoResp",
      className: "CookieBridgePairingInfoResp"
    }
  },
  {
    namespace: "cookiebridge",
    name: "regen_token",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 6370,
    finalSchemaId: 6371,
    req: {
      kind: "struct",
      name: "CookieBridgeRegenTokenReq",
      className: "CookieBridgeRegenTokenReq"
    },
    final: {
      kind: "struct",
      name: "CookieBridgeRegenTokenResp",
      className: "CookieBridgeRegenTokenResp"
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
