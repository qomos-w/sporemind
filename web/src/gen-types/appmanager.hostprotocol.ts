// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerHostProtocolReq {
  Query?: string | undefined;
  Capability?: string | undefined;
}

export interface AppManagerHostCallInfo {
  CallId: string;
  TargetCallId: string;
  Service?: string | undefined;
  Streaming: boolean;
  RequestType?: string | undefined;
  RequestSchemaId: number;
  ResponseType?: string | undefined;
  ResponseSchemaId: number;
  Note?: string | undefined;
}

export interface AppManagerHostCapabilityInfo {
  Capability: string;
  Title?: string | undefined;
  Description?: string | undefined;
  RiskLevel?: string | undefined;
  Calls: AppManagerHostCallInfo[];
}

export interface AppManagerHostProtocolResp {
  Capabilities: AppManagerHostCapabilityInfo[];
}
