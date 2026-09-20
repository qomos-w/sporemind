// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GlassSceneBox {
  X: number;
  Y: number;
  W: number;
  H: number;
}

export interface GlassSceneOption {
  Id: string;
  Text: string;
}

export interface GlassSceneElement {
  Id: string;
  Type: string;
  Box: GlassSceneBox;
  Text?: string | undefined;
  ImageData?: string | undefined;
  Border?: number | undefined;
  Radius?: number | undefined;
  Overflow?: string | undefined;
  BreakMode?: string | undefined;
  Visible?: boolean | undefined;
  Z?: number | undefined;
  Role?: string | undefined;
  Selected?: boolean | undefined;
  Options?: GlassSceneOption[] | undefined;
  Label?: string | undefined;
}

export interface GlassScene {
  Tick: number;
  Elements: GlassSceneElement[];
  FocusId?: string | undefined;
}

export interface GlassInteractionEvent {
  SessionId: string;
  Generation: number;
  Tick: number;
  ElementId: string;
  Action: string;
  Value?: string | undefined;
  Timestamp?: string | undefined;
}

export interface GlassInteractionReportReq {
  SessionId: string;
  Generation: number;
  Tick: number;
  ElementId: string;
  Action: string;
  Value?: string | undefined;
}

export interface GlassInteractionReportResp {
  SessionId: string;
  Generation: number;
  Acknowledged: boolean;
}
