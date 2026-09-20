// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AgentMessageSendReq {
  ToAgentId: string;
  Text: string;
  MessageType?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentMessageSendResp {
  Sent: boolean;
}

export interface AgentMessageReceiveReq {
  FromAgentId: string;
  FromName: string;
  Text: string;
  MessageType: string;
  Timestamp: string;
}

export interface AgentMessage {
  Id: string;
  FromAgentId: string;
  FromName: string;
  ToAgentId: string;
  Text: string;
  MessageType: string;
  Timestamp: string;
}

export interface AgentMessageListResp {
  Items: AgentMessage[];
}

export interface AgentMessageReadReq {
  ToAgentId: string;
  Limit?: number | undefined;
  BeforeSeq?: number | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentMessageReadResp {
  Items: AgentMessageReadItem[];
  HasMore: boolean;
}

export interface AgentMessageReadItem {
  Seq: number;
  Role: string;
  Content: string;
}
