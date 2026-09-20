// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface BrowserPageState {
  ScrollX: number;
  ScrollY: number;
  Zoom: number;
  Data: string;
}

export interface BrowserHistoryEntry {
  Url: string;
  Title: string;
  VisitedAt: string;
}

export interface BrowserWindowState {
  X: number;
  Y: number;
  Width: number;
  Height: number;
  Maximised: boolean;
  Url: string;
  Title: string;
  PageState: BrowserPageState;
  History: BrowserHistoryEntry[];
}

export interface BrowserInstanceConfig {
  Id: string;
  Name: string;
  Url: string;
  Open: boolean;
  Proxy: string;
  ProxyMode: string;
  Mode: string;
  Hidden: boolean;
  State: BrowserWindowState;
}

export interface BrowserInstanceStatus {
  Id: string;
  Open: boolean;
  Url: string;
  Title: string;
  Error?: string | undefined;
}

export interface BrowserInstance {
  Config: BrowserInstanceConfig;
  Status: BrowserInstanceStatus;
}

export interface BrowserManagerListResp {
  Items: BrowserInstance[];
}

export interface BrowserManagerCreateReq {
  Name: string;
  Url: string;
  Proxy: string;
  ProxyMode: string;
  Hidden: boolean;
}

export interface BrowserManagerRemoveReq {
  Id: string;
}

export interface BrowserManagerGetReq {
  Id: string;
}

export interface BrowserManagerUpdateReq {
  Id: string;
  Config: BrowserInstanceConfig;
}

export interface BrowserManagerAsyncResult {
  Accepted: boolean;
  Instance: BrowserInstance;
}

export interface BrowserManagerEvent {
  Kind: string;
  Id: string;
  Instance: BrowserInstance;
  Error?: string | undefined;
}

export interface BrowserManagerNavigateReq {
  Id: string;
  Url: string;
}

export interface BrowserManagerOpenReq {
  Id: string;
}

export interface BrowserManagerOpenGlobalReq {
  Url: string;
}

export interface BrowserManagerOpenGlobalResp {
  Opened: boolean;
  Url: string;
  Error: string;
}
