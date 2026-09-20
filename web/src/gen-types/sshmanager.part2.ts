// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SshFileMkdirReq {
  SessionId: string;
  Path: string;
}

export interface SshFileMkdirResp {

}

export interface SshFileDeleteReq {
  SessionId: string;
  Path: string;
}

export interface SshFileDeleteResp {

}

export interface SshFileRenameReq {
  SessionId: string;
  From: string;
  To: string;
}

export interface SshFileRenameResp {

}

export interface SshFileTransferReq {
  SessionId: string;
  RemotePath: string;
  LocalDir: string;
  IsUpload: boolean;
}

export interface SshProcInfo {
  Pid: number;
  User: string;
  Cpu: number;
  Mem: number;
  Command: string;
}

export interface SshDiskInfo {
  MountPoint: string;
  Available: number;
  Total: number;
}

export interface SshNetInfo {
  Name: string;
  RxBps: number;
  TxBps: number;
}

export interface SshStatus {
  HostId: string;
  HostName: string;
  Connected: boolean;
  CpuPercent: number;
  MemUsed: number;
  MemTotal: number;
  SwapUsed: number;
  SwapTotal: number;
  Disks: SshDiskInfo[];
  Net: SshNetInfo[];
  Procs: SshProcInfo[];
  LoadAvg: number[];
  Uptime: number;
  Timestamp: string;
}

export interface SshStatusReq {
  HostId: string;
}

export interface SshStatusResp {
  Status: SshStatus;
}

export interface SshStatusListReq {

}

export interface SshStatusListResp {
  Items: SshStatus[];
}

export interface SshFileDownloadReq {
  SessionId: string;
  Path: string;
}

export interface SshFileDownloadResp {
  Name: string;
  Content: string;
  Size: number;
}

export interface SshShellStreamReq {
  SessionId: string;
}

export interface SshShellStreamChunk {
  Data: Uint8Array;
  Exited?: boolean | undefined;
  Replay?: boolean | undefined;
}

export interface SshCommandSnippet {
  Id: string;
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandListReq {

}

export interface SshCommandListResp {
  Items: SshCommandSnippet[];
}

export interface SshCommandCreateReq {
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandCreateResp {
  Item: SshCommandSnippet;
}

export interface SshCommandUpdateReq {
  Id: string;
  Name: string;
  Content: string;
  Category: string;
}

export interface SshCommandUpdateResp {

}

export interface SshCommandRemoveReq {
  Id: string;
}

export interface SshCommandRemoveResp {

}

export interface SshHistoryListReq {
  Limit: number;
}

export interface SshHistoryListResp {
  Items: string[];
}

export interface SshArchiveExportReq {
  SessionId: string;
  Path: string;
}

export interface SshArchiveImportReq {
  SessionId: string;
  Path: string;
  Content: string;
}
