// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { SshHostView } from './sshmanager.view';

export interface SshHost {
  Id: string;
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostListReq {

}

export interface SshHostListResp {
  Items: SshHostView[];
  Groups: string[];
}

export interface SshHostCreateReq {
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostCreateResp {
  Host: SshHostView;
}

export interface SshHostUpdateReq {
  Id: string;
  Name: string;
  Host: string;
  Port: number;
  User: string;
  AuthMethod: string;
  AgentInvisible: boolean;
  Group?: string | undefined;
  Password?: string | undefined;
  KeyPath?: string | undefined;
  KeyData?: string | undefined;
}

export interface SshHostUpdateResp {

}

export interface SshHostRemoveReq {
  Id: string;
}

export interface SshHostRemoveResp {

}

export interface SshSessionInfo {
  SessionId: string;
  HostId: string;
  HostName: string;
  HostAddr: string;
  User: string;
  Connected: boolean;
  Cwd?: string | undefined;
  Error?: string | undefined;
}

export interface SshSessionListReq {

}

export interface SshSessionListResp {
  Items: SshSessionInfo[];
}

export interface SshShellOpenReq {
  HostId: string;
  InitialCols?: number | undefined;
  InitialRows?: number | undefined;
}

export interface SshShellOpenResp {
  SessionId: string;
  Connected: boolean;
  Error?: string | undefined;
}

export interface SshShellCloseReq {
  SessionId: string;
}

export interface SshShellCloseResp {

}

export interface SshShellInputReq {
  SessionId: string;
  Data: string;
}

export interface SshShellInputResp {

}

export interface SshShellResizeReq {
  SessionId: string;
  Cols: number;
  Rows: number;
}

export interface SshShellResizeResp {

}

export interface SshFileEntry {
  Name: string;
  FullPath: string;
  IsDir: boolean;
  Size: number;
  Mode?: string | undefined;
  Modified?: string | undefined;
}

export interface SshFileListReq {
  SessionId: string;
  Path: string;
}

export interface SshFileListResp {
  Path: string;
  Entries: SshFileEntry[];
}

export interface SshFileReadReq {
  SessionId: string;
  Path: string;
}

export interface SshFileReadResp {
  Path: string;
  Content: string;
  IsBinary: boolean;
}

export interface SshFileWriteReq {
  SessionId: string;
  Path: string;
  Content: string;
}

export interface SshFileWriteResp {

}
