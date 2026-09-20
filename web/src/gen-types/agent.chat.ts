// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ModelUnit } from './aigen.part1';

export interface ImageEntry {
  Url: string;
  Alt?: string | undefined;
  MimeType?: string | undefined;
}

export interface AttachmentEntry {
  Name: string;
  MimeType: string;
  Url?: string | undefined;
  SizeBytes?: number | undefined;
}

export interface AgentChatSubmitReq {
  Text: string;
  Unit?: ModelUnit | undefined;
  Title?: string | undefined;
  ThinkingBudget?: number | undefined;
  ReasoningEffort?: string | undefined;
  Attachments?: AttachmentEntry[] | undefined;
  Images?: ImageEntry[] | undefined;
  Meta?: string | undefined;
}

export interface AgentChatSubmitResp {
  TurnActorId: string;
  MessageId?: string | undefined;
  Idx?: number | undefined;
  Timestamp?: string | undefined;
}

export interface AgentChatCancelPendingReq {
  MessageId: string;
}

export interface AgentChatCancelPendingResp {
  Cancelled: boolean;
}

export interface WorkspaceCoordinatorLookupResp {
  Found: boolean;
  Nickname?: string | undefined;
  ActorId?: string | undefined;
}
