// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface InspectRef {
  Kind: string;
  Id: string;
  Scope?: Record<string, string> | undefined;
}

export interface OpenTarget {
  View: string;
  Kind?: string | undefined;
  Id?: string | undefined;
  Scope?: Record<string, string> | undefined;
  Params?: Record<string, string> | undefined;
}

export interface InspectCapability {
  Performance: number;
  Cost: number;
  Speed: number;
}

export interface InspectContextSegment {
  Id: string;
  Label: string;
  Kind: string;
  Chars: number;
  Priority: number;
  Source?: string | undefined;
  Path?: string | undefined;
  Editable?: boolean | undefined;
  FragmentId?: string | undefined;
  Content?: string | undefined;
}

export interface InspectStatus {
  State: string;
  Label: string;
}

export interface InspectRelation {
  Label: string;
  Ref: InspectRef;
}

export interface InspectDocumentReq {
  Kind: string;
  Id: string;
  Scope?: Record<string, string> | undefined;
}

export interface InspectRow {
  Label: string;
  Value: string;
  Mono?: boolean | undefined;
  Badge?: string | undefined;
  Ref?: InspectRef | undefined;
  Tooltip?: string | undefined;
  ExpandedRows?: InspectRow[] | undefined;
}

export interface InspectListItem {
  Label: string;
  Description?: string | undefined;
  Ref?: InspectRef | undefined;
  OpenTarget?: OpenTarget | undefined;
}

export interface InspectSection {
  Type: string;
  Id: string;
  Title: string;
  Rows?: InspectRow[] | undefined;
  Items?: InspectListItem[] | undefined;
  Body?: string | undefined;
  Capability?: InspectCapability | undefined;
  Segments?: InspectContextSegment[] | undefined;
}

export interface InspectAction {
  Id: string;
  Label: string;
  Target?: OpenTarget | undefined;
}

export interface InspectPage {
  Id: string;
  Label: string;
  Icon?: string | undefined;
}

export interface InspectPagesResp {
  Items: InspectPage[];
}

export interface InspectDocument {
  Ref: InspectRef;
  Title: string;
  Subtitle?: string | undefined;
  Summary?: string | undefined;
  Status?: InspectStatus | undefined;
  Sections: InspectSection[];
  Actions?: InspectAction[] | undefined;
  Relations?: InspectRelation[] | undefined;
  Pages?: InspectPage[] | undefined;
}
