// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SshDownloadReq {
  SessionId: string;
  Path: string;
}

export interface SshDownloadResp {
  Name: string;
  Content: string;
  Size: number;
  IsDirectory: boolean;
  NumEntries: number;
}

export interface SshUploadReq {
  SessionId: string;
  Path: string;
  Content: string;
  IsArchive: boolean;
}

export interface SshUploadResp {
  NumEntries: number;
  BytesWritten: number;
}

export interface SshTunnelOpenReq {
  HostId: string;
  TargetAddr: string;
}

export interface SshTunnelOpenResp {
  LocalAddr: string;
  RefCount: number;
}

export interface SshTunnelCloseReq {
  HostId: string;
  TargetAddr: string;
}

export interface SshTunnelCloseResp {
  Closed: boolean;
  RefCount: number;
}

export interface SshTunnelListReq {

}

export interface SshTunnelInfo {
  HostId: string;
  TargetAddr: string;
  LocalAddr: string;
  RefCount: number;
  ActiveConns: number;
}

export interface SshTunnelListResp {
  Items: SshTunnelInfo[];
}
