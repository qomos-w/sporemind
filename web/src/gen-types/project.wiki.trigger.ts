// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { MonoCardListItem } from './project.wiki.part1';

export interface WikiTriggerTimerCardReq {
  Id: string;
}

export interface WikiTriggerTimerCardResp {
  Id: string;
}

export interface WikiToggleTimerReq {
  Id: string;
  Enabled: boolean;
}

export interface WikiToggleTimerResp {
  Id: string;
  Enabled: boolean;
}

export interface WikiGetCardHierarchyReq {
  Id: string;
  Level?: number | undefined;
}

export interface WikiGetCardHierarchyResp {
  Tree: string;
}

export interface WikiOpenCardReq {
  Id: string;
}

export interface WikiCloseCardReq {
  Id: string;
}

export interface WikiSearchCardContentReq {
  Query: string;
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
  OrderBy?: string | undefined;
  Limit?: number | undefined;
}

export interface WikiCardContentMatch {
  Card: MonoCardListItem;
  Line?: number | undefined;
  Snippet?: string | undefined;
}

export interface WikiSearchCardContentResp {
  Matches: WikiCardContentMatch[];
  Total: number;
}

export interface WikiWorkflowFilter {
  FoldedWorkflows: string[];
  FoldedCategories: string[];
  ExpandedBuckets: string[];
  AgentIds: string[];
}

export interface WikiCardTreeNode {
  Id: string;
  Title: string;
  Type: string;
  Status: string;
  Modified: string;
  Children: WikiCardTreeNode[];
}
