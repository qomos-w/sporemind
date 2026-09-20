// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface BrowserElement {
  Id: string;
  TagName: string;
  Selector: string;
  XPath: string;
  Rect: BrowserElementRect;
  Text: string;
  Placeholder: string;
  Role: string;
  AriaLabel: string;
  IsVisible: boolean;
  IsEnabled: boolean;
  IsFocusable: boolean;
  IsChecked: boolean;
  InputType: string;
  Value: string;
  Src: string;
  Href: string;
  FontSize: number;
  FontWeight: number;
  Color: string;
  BackgroundColor: string;
  ActionHint: string;
  ActionConfidence: number;
  ChildCount: number;
  SiblingIndex: number;
  ParentId: string;
}

export interface BrowserElementRect {
  X: number;
  Y: number;
  Width: number;
  Height: number;
}

export interface BrowserPageObservation {
  ObservationId: string;
  Url: string;
  Title: string;
  ViewportWidth: number;
  ViewportHeight: number;
  ScrollX: number;
  ScrollY: number;
  LoadState: string;
  Elements: BrowserElement[];
  TotalElementCount: number;
  Screenshot: Uint8Array;
  ScreenshotWidth: number;
  ScreenshotHeight: number;
  IsAnnotated: boolean;
  Timestamp: string;
  HistoryLength: number;
  Error: string;
  ActiveElementHtml: string;
}

export interface BrowserUseReq {
  InstanceId: string;
  Action: string;
  ElementId?: string | undefined;
  DragTargetElementId?: string | undefined;
  ElementText?: string | undefined;
  ElementSelector?: string | undefined;
  ClickX?: number | undefined;
  ClickY?: number | undefined;
  Text?: string | undefined;
  Append?: boolean | undefined;
  Submit?: boolean | undefined;
  ScrollX?: number | undefined;
  ScrollY?: number | undefined;
  ScrollTarget?: string | undefined;
  Url?: string | undefined;
  NavigateMode?: string | undefined;
  Key?: string | undefined;
  Modifiers?: string[] | undefined;
  WaitMs?: number | undefined;
  WaitFor?: string | undefined;
  WaitElementId?: string | undefined;
  FullPage?: boolean | undefined;
  Annotate?: boolean | undefined;
  Format?: string | undefined;
  TimeoutMs?: number | undefined;
  Context?: string | undefined;
  FilePath?: string | undefined;
}

export interface BrowserUseResp {
  Success: boolean;
  Message: string;
  Observation?: BrowserPageObservation | undefined;
  ErrorCode?: string | undefined;
  Element?: BrowserElement | undefined;
  CursorX?: number | undefined;
  CursorY?: number | undefined;
  Navigated?: boolean | undefined;
  NewUrl?: string | undefined;
  PageChanged?: boolean | undefined;
}
