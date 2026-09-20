// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { WikiCardTreeNode, WikiWorkflowFilter } from './project.wiki.trigger';

export interface WikiOpenCardsReq {

}

export interface WikiOpenCardsResp {
  OpenCards: string[];
}

export interface WikiSaveOpenCardsReq {
  OpenCards: string[];
}

export interface MonoCardListItem {
  Id: string;
  Type: string;
  Source: string;
  Storage: string;
  Visibility: string;
  Tags: string[];
  List: string[];
  Created: string;
  Modified: string;
  Protected: boolean;
  Editable: boolean;
  Deletable: boolean;
  Due?: string | undefined;
  Priority?: string | undefined;
  Status?: string | undefined;
  Parent?: string | undefined;
  Data?: Record<string, unknown> | undefined;
  Standalone?: boolean | undefined;
  Raw: string;
}

export interface WikiListCardsReq {
  RootId?: string | undefined;
  Limit?: number | undefined;
  Depth?: number | undefined;
  OrderBy?: string | undefined;
  Flat?: boolean | undefined;
  IncludeRaw?: boolean | undefined;
  IncludeBuiltin?: boolean | undefined;
  WorkflowFilter?: WikiWorkflowFilter | undefined;
  Total?: boolean | undefined;
  Query?: string | undefined;
  Tags?: string[] | undefined;
  Status?: string | undefined;
  Type?: string | undefined;
  Source?: string | undefined;
  List?: string | undefined;
  Parent?: string | undefined;
  Priority?: string | undefined;
  Due?: string | undefined;
  ModifiedAfter?: string | undefined;
  ModifiedBefore?: string | undefined;
  CreatedAfter?: string | undefined;
  CreatedBefore?: string | undefined;
  DueAfter?: string | undefined;
  DueBefore?: string | undefined;
}

export interface WikiListCardsResp {
  Tree: string;
  Nodes: WikiCardTreeNode[];
  Cards: MonoCardListItem[];
  Total: number;
}

export interface WikiGetCardReq {
  Id: string;
}

export interface WikiGetCardResp {
  Id: string;
  Raw: string;
}

export interface WikiCreateCardReq {
  Id: string;
  Raw: string;
}

export interface WikiCreateCardResp {
  Card: MonoCardListItem;
}

export interface WikiEditCardReq {
  Id: string;
  Raw?: string | undefined;
  OldString?: string | undefined;
  NewString?: string | undefined;
  ReplaceAll?: boolean | undefined;
}

export interface WikiEditCardResp {
  Card: MonoCardListItem;
}

export interface WikiDeleteCardReq {
  Id: string;
}

export interface WikiDeleteCardResp {
  Id: string;
}

export interface WikiTimerListItem {
  Id: string;
  NextFireAt: string;
  LastRunAt: string;
  LastStatus: string;
  Enabled: boolean;
  CurrentInstance: string;
  ScheduleType?: string | undefined;
  TaskMode?: string | undefined;
  AgentKind?: string | undefined;
  BoundTemplate?: string | undefined;
}

export interface WikiListTimersResp {
  Timers: WikiTimerListItem[];
}
