// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SshFileWriteBase64Req {
  SessionId: string;
  Path: string;
  Content: string;
}

export interface SshFileWriteBase64Resp {

}

export interface SshFileChmodReq {
  SessionId: string;
  Path: string;
  Mode: string;
}

export interface SshFileChmodResp {

}

export interface SshManagerEvent {
  Kind: string;
  SessionId: string;
  HostId: string;
  HostName: string;
  Error?: string | undefined;
}

export interface SshShellRunReq {
  SessionId: string;
  Command: string;
  TimeoutMs?: number | undefined;
}

export interface SshShellRunResp {
  Output: string;
  ExitCode: number;
  Truncated: boolean;
  TimedOut: boolean;
}
