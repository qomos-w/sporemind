// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ToastCard {
  Id: string;
  Title: string;
  Body?: string | undefined;
  Kind: string;
  Source?: string | undefined;
  ActionLabel?: string | undefined;
  ActionCallable?: string | undefined;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
  DurationMs?: number | undefined;
  CreatedAt: string;
}

export interface ToastShowReq {
  Title: string;
  Body?: string | undefined;
  Kind?: string | undefined;
  Source?: string | undefined;
  ActionLabel?: string | undefined;
  ActionCallable?: string | undefined;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
  DurationMs?: number | undefined;
}

export interface ToastShowResp {
  Id: string;
}

export interface ToastDismissReq {
  Id: string;
}

export interface ToastDismissResp {

}

export interface ToastCardRemovedEvent {
  Id: string;
}

export interface ToastStateReq {

}

export interface ToastStateResp {
  Cards: ToastCard[];
}

export interface ToastActionReq {
  Id: string;
}

export interface ToastActionResp {

}

export interface ToastActionTriggeredEvent {
  Id: string;
  ActionCallable: string;
  ActionLabel: string;
  ActionArgs?: Record<string, unknown> | undefined;
  ActionSchemaID?: number | undefined;
}
