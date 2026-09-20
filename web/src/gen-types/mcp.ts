// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface McpStdioTransport {
  Command: string;
  Args: string[];
  Env: Record<string, string>;
}

export interface McpHttpTransport {
  Url: string;
  Headers: Record<string, string>;
  Proxy?: string | undefined;
}

export interface McpServerConfig {
  Id: string;
  Name: string;
  Transport: string;
  Stdio?: McpStdioTransport | undefined;
  Http?: McpHttpTransport | undefined;
  Enabled: boolean;
}

export interface McpServerStatus {
  Id: string;
  Connected: boolean;
  ToolCount: number;
  Error?: string | undefined;
}

export interface McpEnvVarEntry {
  Key: string;
  Configured: boolean;
}

export interface McpStdioTransportView {
  Command: string;
  Args: string[];
  Env: McpEnvVarEntry[];
}

export interface McpHttpTransportView {
  Url: string;
  Headers: McpEnvVarEntry[];
  Proxy?: string | undefined;
}

export interface McpServerView {
  Id: string;
  Name: string;
  Transport: string;
  Stdio?: McpStdioTransportView | undefined;
  Http?: McpHttpTransportView | undefined;
  Enabled: boolean;
  Status: McpServerStatus;
}

export interface McpListServersReq {

}

export interface McpListServersResp {
  Items: McpServerView[];
}

export interface McpAddServerReq {
  Config: McpServerConfig;
}

export interface McpAddServerResp {
  Server: McpServerView;
}

export interface McpUpdateServerReq {
  Id: string;
  Config: McpServerConfig;
}

export interface McpUpdateServerResp {
  Server: McpServerView;
}

export interface McpRemoveServerReq {
  Id: string;
}

export interface McpRemoveServerResp {

}

export interface McpConnectReq {
  Id: string;
}

export interface McpConnectResp {
  Status: McpServerStatus;
}

export interface McpDisconnectReq {
  Id: string;
}

export interface McpDisconnectResp {
  Status: McpServerStatus;
}

export interface McpToolContent {
  Type: string;
  Text: string;
  Data?: string | undefined;
  MimeType?: string | undefined;
}

export interface McpCallToolReq {
  Id: string;
  Tool: string;
  Arguments: Record<string, unknown>;
}

export interface McpCallToolResp {
  Content: McpToolContent[];
  IsError?: boolean | undefined;
  Error?: string | undefined;
}

export interface McpServerStatusEvent {
  Status: McpServerStatus;
}

export interface McpToolView {
  Name: string;
  Description: string;
  InputSchema: string;
}

export interface McpServerTools {
  Id: string;
  Name: string;
  Tools: McpToolView[];
}

export interface McpDiscoverToolsReq {

}

export interface McpDiscoverToolsResp {
  Servers: McpServerTools[];
}
