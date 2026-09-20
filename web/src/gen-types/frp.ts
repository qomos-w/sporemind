// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface FrpProxy {
  Name: string;
  Kind: string;
  LocalIP: string;
  LocalPort: number;
  RemotePort: number;
}

export interface FrpWebProxy {
  Enabled: boolean;
  Name: string;
  Mode?: string | undefined;
  RemotePort?: number | undefined;
  CustomDomains?: string[] | undefined;
  CertPem?: string | undefined;
  KeyPem?: string | undefined;
  HostHeaderRewrite?: string | undefined;
}

export interface FrpInstanceConfig {
  Id: string;
  Name: string;
  ServerAddr: string;
  Token?: string | undefined;
  Tls?: boolean | undefined;
  LegacyMode?: boolean | undefined;
  Disabled?: boolean | undefined;
  Proxies: FrpProxy[];
  WebProxy?: FrpWebProxy | undefined;
}

export interface FrpProxyStatus {
  Name: string;
  Status: string;
  Err?: string | undefined;
  RemoteAddr?: string | undefined;
}

export interface FrpInstanceStatus {
  Id: string;
  Running: boolean;
  Connected?: boolean | undefined;
  Proxies?: FrpProxyStatus[] | undefined;
  DetectedVersion?: string | undefined;
  Error?: string | undefined;
  StartedAt?: string | undefined;
}

export interface FrpInstance {
  Config: FrpInstanceConfig;
  Status: FrpInstanceStatus;
}

export interface FrpManagerCreateReq {
  Name: string;
  ServerAddr: string;
  Token?: string | undefined;
  Tls?: boolean | undefined;
  LegacyMode?: boolean | undefined;
  Proxies: FrpProxy[];
  WebProxy?: FrpWebProxy | undefined;
}

export interface FrpManagerRemoveReq {
  Id: string;
}

export interface FrpManagerGetReq {
  Id: string;
}

export interface FrpManagerListResp {
  Items: FrpInstance[];
  GatewayPort: number;
}

export interface FrpInstanceConfigureReq {
  Config: FrpInstanceConfig;
}

export interface FrpManagerUpdateReq {
  Id: string;
  Config: FrpInstanceConfig;
}

export interface FrpManagerStartReq {
  Id: string;
}

export interface FrpManagerStopReq {
  Id: string;
}

export interface FrpManagerDetectReq {
  ServerAddr: string;
  Token?: string | undefined;
}

export interface FrpManagerDetectResp {
  LegacyMode: boolean;
  Error?: string | undefined;
}
