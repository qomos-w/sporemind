// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { MonoCardListItem } from './project.wiki.part1';

export interface WikiSetStatusReq {
  Id: string;
  Status: string;
  ExpectedStatus?: string | undefined;
  Evidence?: string[] | undefined;
}

export interface WikiSetStatusResp {
  Card: MonoCardListItem;
  PreviousStatus: string;
}

export interface FrontierTaskCard {
  Id: string;
  Title: string;
  Status: string;
  Tags: string[];
}

export interface WikiFrontierReq {
  MapId: string;
}

export interface WikiFrontierResp {
  TaskCards: FrontierTaskCard[];
}

export interface WikiSetMapOwnerReq {
  MapId: string;
  OwnerActorId: string;
}

export interface WikiSetMapOwnerResp {
  Card: MonoCardListItem;
  PreviousOwnerActorId: string;
}

export interface WikiClaimTaskCardReq {
  Id: string;
  Status: string;
  ExpectedStatuses: string[];
}

export interface WikiClaimTaskCardResp {
  Card: MonoCardListItem;
  PreviousStatus: string;
  Raw: string;
  Inputs?: Record<string, unknown> | undefined;
}
