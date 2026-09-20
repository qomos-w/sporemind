// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerCallableInfoReq {
  Query?: string | undefined;
}

export interface AppManagerCallableParam {
  Name: string;
  Type: string;
  Required: boolean;
  Description: string;
}

export interface AppManagerCallableInfoEntry {
  Id: string;
  Service: string;
  Description: string;
  Effect: string;
  Params: AppManagerCallableParam[];
}

export interface AppManagerCallableInfoResp {
  Items: AppManagerCallableInfoEntry[];
}

export interface AppManagerDevGuideReq {
  Topic?: string | undefined;
}

export interface AppManagerDevGuideStep {
  Order: number;
  Title: string;
  Detail: string;
  Tools: string[];
}

export interface AppManagerDevGuideError {
  Symptom: string;
  Cause: string;
  Remedy: string;
}

export interface AppManagerDevGuideResp {
  Prerequisites: string[];
  Workflow: AppManagerDevGuideStep[];
  HostAPI: string[];
  Security: string[];
  CommonErrors: AppManagerDevGuideError[];
}
