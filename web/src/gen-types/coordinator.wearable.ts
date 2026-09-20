// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { GlassRenderFrame } from './glass';

export interface CoordinatorWearableNotifyReq {
  Text?: string | undefined;
  MessageId?: string | undefined;
  Frame?: GlassRenderFrame | undefined;
  SessionId?: string | undefined;
  Generation?: number | undefined;
}

export interface CoordinatorWearableNotifyResp {
  SessionId?: string | undefined;
  Generation?: number | undefined;
  MessageId?: string | undefined;
  Applied: boolean;
  Spoken: boolean;
  Displayed: boolean;
  Duplicate: boolean;
  Error?: string | undefined;
}

export interface CoordinatorWearableCallReq {
  Prompt: string;
  MessageId?: string | undefined;
  Frame?: GlassRenderFrame | undefined;
  SessionId?: string | undefined;
  Generation?: number | undefined;
}

export interface CoordinatorWearableCallResp {
  InteractionId: string;
  SessionId?: string | undefined;
  Generation?: number | undefined;
  MessageId?: string | undefined;
  Pending: boolean;
  Prompt?: string | undefined;
  Error?: string | undefined;
}
