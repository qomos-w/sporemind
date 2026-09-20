// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { CardRef } from './card';

export interface ProjectCardMountReq {
  Ref: CardRef;
}

export interface ProjectCardMountResp {
  Ref: CardRef;
}

export interface ProjectCardUnmountReq {
  Id: string;
}

export interface ProjectCardUnmountResp {
  Id: string;
}

export interface ProjectCardListReq {

}

export interface ProjectCardListResp {
  Refs: CardRef[];
}
