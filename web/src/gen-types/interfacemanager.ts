// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GuideStep {
  TargetGuideId: string;
  Title: string;
  Body: string;
  Placement?: string | undefined;
  ExpectedInteraction?: string | undefined;
  CapabilityKind?: string | undefined;
  I18nTitleKey?: string | undefined;
  I18nBodyKey?: string | undefined;
}

export interface TutorialSpec {
  TutorialId: string;
  Title: string;
  Description?: string | undefined;
  Steps: GuideStep[];
  CreatedAt: string;
}

export interface UiInteractionRecord {
  Id: string;
  Ts: string;
  Kind: string;
  GuideId: string;
  Label?: string | undefined;
  View?: string | undefined;
  Detail?: string | undefined;
}

export interface InterfaceManagerControlReq {
  Action: string;
  Mode?: string | undefined;
  Category?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  Steps?: GuideStep[] | undefined;
  Interaction?: string | undefined;
  Text?: string | undefined;
  GuideId?: string | undefined;
  AppId?: string | undefined;
  ViewId?: string | undefined;
  TutorialId?: string | undefined;
  Title?: string | undefined;
  Description?: string | undefined;
  AutoPlay?: boolean | undefined;
  PanelOp?: PanelOpSpec | undefined;
}

export interface PanelOpSpec {
  RequestId: string;
  Op: string;
  Selector?: string | undefined;
  Text?: string | undefined;
  Expr?: string | undefined;
  TimeoutMs?: number | undefined;
  MaxChars?: number | undefined;
}

export interface InterfaceManagerControlResp {
  Accepted: boolean;
  Error?: string | undefined;
  Anchors?: GuideAnchor[] | undefined;
  Tutorials?: TutorialSpec[] | undefined;
  TutorialId?: string | undefined;
}

export interface InterfaceManagerEvent {
  Action: string;
  Mode?: string | undefined;
  Category?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  Steps?: GuideStep[] | undefined;
  Interaction?: string | undefined;
  Text?: string | undefined;
  GuideId?: string | undefined;
  AppId?: string | undefined;
  ViewId?: string | undefined;
  Tutorial?: TutorialSpec | undefined;
  AutoPlay?: boolean | undefined;
  TutorialId?: string | undefined;
  PanelOp?: PanelOpSpec | undefined;
}

export interface ReportInteractionReq {
  Kind: string;
  GuideId: string;
  Label?: string | undefined;
  View?: string | undefined;
  Detail?: string | undefined;
}

export interface ReportInteractionResp {
  Accepted: boolean;
}

export interface QueryInteractionsReq {
  Since?: string | undefined;
  GuideId?: string | undefined;
  Kind?: string | undefined;
  Limit?: number | undefined;
}

export interface QueryInteractionsResp {
  Items: UiInteractionRecord[];
}

export interface GuideAnchor {
  GuideId: string;
  Area: string;
  Summary: string;
  VisibleWhen?: string | undefined;
  SuggestedGates?: string | undefined;
}
