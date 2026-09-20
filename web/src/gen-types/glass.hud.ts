// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GlassTelemetryReq {
  SessionId: string;
  Generation: number;
  BatteryLevel?: number | undefined;
  Charging?: boolean | undefined;
}

export interface GlassTelemetryResp {
  Accepted: boolean;
}
