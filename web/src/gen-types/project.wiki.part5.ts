// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface WikiListTemplateRunsReq {
  TemplateMapId: string;
  Limit?: number | undefined;
}

export interface WikiListTemplateRunsResp {
  Runs: TemplateRunRecord[];
}

export interface TemplateRunRecord {
  InstanceMapId: string;
  StartedAt: string;
  SchedulerCardId?: string | undefined;
  Source?: string | undefined;
}

export interface WikiGetCardsBatchReq {
  Ids: string[];
}

export interface WikiCardRaw {
  Id: string;
  Raw: string;
  Found: boolean;
}

export interface WikiGetCardsBatchResp {
  Cards: WikiCardRaw[];
}

export interface WikiSetStarredReq {
  Id: string;
  Starred: boolean;
}

export interface WikiGetStarredReq {

}

export interface WikiStarredResp {
  Starred: string[];
}
