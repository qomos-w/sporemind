// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ShellCandidate {
  Kind: string;
  Executable: string;
  ExtraBin?: string[] | undefined;
  Available: boolean;
}

export interface ShellEnvProbeResp {
  Current: ShellCandidate;
  Candidates: ShellCandidate[];
  Platform: string;
}

export interface ShellPrefSaveReq {
  RequestId: string;
  Kind: string;
}

export interface ShellPrefSaveResp {
  Kind: string;
  Current: ShellCandidate;
}
