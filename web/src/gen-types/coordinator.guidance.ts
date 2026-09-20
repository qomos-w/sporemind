// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GuidanceCapabilityEntry {
  Capability: string;
  Familiarity: number;
  Confidence: number;
  Evidence?: string[] | undefined;
  HintState?: string | undefined;
  UpdatedAt: string;
}

export interface GuidanceCapabilityProfile {
  Account: string;
  Capabilities: GuidanceCapabilityEntry[];
  UpdatedAt?: string | undefined;
}

export interface GuidanceRecord {
  Id: string;
  Topic: string;
  Account?: string | undefined;
  Detail?: string | undefined;
  CreatedAt?: string | undefined;
  UpdatedAt?: string | undefined;
}

export interface GuidanceProfileQueryReq {
  Account: string;
}

export interface GuidanceProfileQueryResp {
  Profile?: GuidanceCapabilityProfile | undefined;
  Records?: GuidanceRecord[] | undefined;
}

export interface GuidanceProfileUpdateReq {
  Account: string;
  Capabilities: GuidanceCapabilityEntry[];
  Records?: GuidanceRecord[] | undefined;
}

export interface GuidanceProfileUpdateResp {
  Profile: GuidanceCapabilityProfile;
}

export interface GuidanceProfileClearReq {
  Account: string;
  Capability?: string | undefined;
}

export interface GuidanceProfileClearResp {
  Account: string;
  Cleared: boolean;
}

export interface GuidanceProfileIncrementReq {
  Account: string;
  Capability: string;
  Signal: string;
  Evidence?: string | undefined;
}

export interface GuidanceProfileIncrementResp {
  Entry: GuidanceCapabilityEntry;
}
