// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "glass_interact",
    name: "bootstrap",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4018,
    finalSchemaId: 4019,
    req: {
      kind: "struct",
      name: "GlassBootstrapReq",
      className: "GlassBootstrapReq"
    },
    final: {
      kind: "struct",
      name: "GlassBootstrapResp",
      className: "GlassBootstrapResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "debug_simulate",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4256,
    finalSchemaId: 4257,
    req: {
      kind: "struct",
      name: "GlassDebugSimulateReq",
      className: "GlassDebugSimulateReq"
    },
    final: {
      kind: "struct",
      name: "GlassDebugSimulateResp",
      className: "GlassDebugSimulateResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "debug_state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4048,
    finalSchemaId: 4049,
    req: {
      kind: "struct",
      name: "GlassDebugReq",
      className: "GlassDebugReq"
    },
    final: {
      kind: "struct",
      name: "GlassDebugResp",
      className: "GlassDebugResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "interaction_report",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4165,
    finalSchemaId: 4166,
    req: {
      kind: "struct",
      name: "GlassInteractionReportReq",
      className: "GlassInteractionReportReq"
    },
    final: {
      kind: "struct",
      name: "GlassInteractionReportResp",
      className: "GlassInteractionReportResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "session_claim",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4020,
    finalSchemaId: 4021,
    req: {
      kind: "struct",
      name: "GlassSessionClaimReq",
      className: "GlassSessionClaimReq"
    },
    final: {
      kind: "struct",
      name: "GlassSessionClaimResp",
      className: "GlassSessionClaimResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "session_get_state",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4024,
    finalSchemaId: 4025,
    req: {
      kind: "struct",
      name: "GlassGetStateReq",
      className: "GlassGetStateReq"
    },
    final: {
      kind: "struct",
      name: "GlassGetStateResp",
      className: "GlassGetStateResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_chunk",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4027,
    finalSchemaId: 4029,
    req: {
      kind: "struct",
      name: "GlassSpeechChunkReq",
      className: "GlassSpeechChunkReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechAck",
      className: "GlassSpeechAck"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_end",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4028,
    finalSchemaId: 4031,
    req: {
      kind: "struct",
      name: "GlassSpeechEndReq",
      className: "GlassSpeechEndReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechEndResp",
      className: "GlassSpeechEndResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "speech_start",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4026,
    finalSchemaId: 4030,
    req: {
      kind: "struct",
      name: "GlassSpeechStartReq",
      className: "GlassSpeechStartReq"
    },
    final: {
      kind: "struct",
      name: "GlassSpeechStartResp",
      className: "GlassSpeechStartResp"
    }
  },
  {
    namespace: "glass_interact",
    name: "telemetry_report",
    visibility: "public",
    mode: "unary",
    reqSchemaId: 4208,
    finalSchemaId: 4209,
    req: {
      kind: "struct",
      name: "GlassTelemetryReq",
      className: "GlassTelemetryReq"
    },
    final: {
      kind: "struct",
      name: "GlassTelemetryResp",
      className: "GlassTelemetryResp"
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
