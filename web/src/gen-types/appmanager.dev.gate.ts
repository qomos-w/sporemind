// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerDevGateReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerGateError {
  Gate: string;
  Detail: string;
  Fix: string;
}

export interface AppManagerGateResult {
  Gate: string;
  Passed: boolean;
  Error?: AppManagerGateError | undefined;
}

export interface AppManagerDevGateResp {
  Passed: boolean;
  Results: AppManagerGateResult[];
  Error?: string | undefined;
}
