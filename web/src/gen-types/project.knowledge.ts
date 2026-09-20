// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface WikiGetConceptTreeReq {
  Id: string;
  Level?: number | undefined;
}

export interface WikiConceptNode {
  Id: string;
  ParentId: string;
  Children: WikiConceptNode[];
}

export interface WikiGetConceptTreeResp {
  Root: WikiConceptNode;
  Source: string;
}
