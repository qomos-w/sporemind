// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GlassDebugSimulateReq {
  Command: string;
  Title?: string | undefined;
  Body?: string | undefined;
  Source?: string | undefined;
  TimeAgo?: string | undefined;
  Context?: string | undefined;
  Options?: string[] | undefined;
  SelectedIdx?: number | undefined;
  Priority?: string | undefined;
  BatteryLevel?: number | undefined;
}

export interface GlassDebugSimulateResp {
  Applied: boolean;
  Detail?: string | undefined;
}
