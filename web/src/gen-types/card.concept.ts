// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ConceptCardRelation {
  From: string;
  To: string;
  Relation: string;
}

export interface SourceAnchor {
  Path: string;
  Symbol?: string | undefined;
  StartLine?: number | undefined;
  StartColumn?: number | undefined;
  EndLine?: number | undefined;
  EndColumn?: number | undefined;
  FileHash?: string | undefined;
  RegionHash?: string | undefined;
}

export interface ConceptEvidence {
  Id: string;
  Kind: string;
  Statement: string;
  Anchor: SourceAnchor;
  Confidence: number;
}

export interface ConceptCard {
  Title: string;
  Desc: string;
  State: string;
  Relations: ConceptCardRelation[];
  Anchors: SourceAnchor[];
  Evidence: ConceptEvidence[];
  Confidence: number;
  Status: string;
  Stale: boolean;
  Missing: boolean;
}
