// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function action(client: GosporeClient, req: systemTypes.ToastActionReq, opts?: InvokeOptions): Promise<systemTypes.ToastActionResp> {
  return client.invoke<systemTypes.ToastActionReq, systemTypes.ToastActionResp>("toast.action", req, { reqSchemaId: 6216, resSchemaId: 6217, ...opts });
}

export const action_meta = {
  callable: "toast.action",
  name: "action",
  reqSchemaId: 6216,
  resSchemaId: 6217,
} as const;

export async function dismiss(client: GosporeClient, req: systemTypes.ToastDismissReq, opts?: InvokeOptions): Promise<systemTypes.ToastDismissResp> {
  return client.invoke<systemTypes.ToastDismissReq, systemTypes.ToastDismissResp>("toast.dismiss", req, { reqSchemaId: 6211, resSchemaId: 6212, ...opts });
}

export const dismiss_meta = {
  callable: "toast.dismiss",
  name: "dismiss",
  reqSchemaId: 6211,
  resSchemaId: 6212,
} as const;

export async function show(client: GosporeClient, req: systemTypes.ToastShowReq, opts?: InvokeOptions): Promise<systemTypes.ToastShowResp> {
  return client.invoke<systemTypes.ToastShowReq, systemTypes.ToastShowResp>("toast.show", req, { reqSchemaId: 6209, resSchemaId: 6210, ...opts });
}

export const show_meta = {
  callable: "toast.show",
  name: "show",
  reqSchemaId: 6209,
  resSchemaId: 6210,
} as const;

export async function state(client: GosporeClient, req: systemTypes.ToastStateReq, opts?: InvokeOptions): Promise<systemTypes.ToastStateResp> {
  return client.invoke<systemTypes.ToastStateReq, systemTypes.ToastStateResp>("toast.state", req, { reqSchemaId: 6214, resSchemaId: 6215, ...opts });
}

export const state_meta = {
  callable: "toast.state",
  name: "state",
  reqSchemaId: 6214,
  resSchemaId: 6215,
} as const;

export type ToastActionTriggeredHandler = (payload: systemTypes.ToastActionTriggeredEvent) => void;

export function OnToastActionTriggered(client: GosporeClient, handler: ToastActionTriggeredHandler): () => void {
  return client.events.onService("toast", "toast.action_triggered", (payload) => handler(payload as systemTypes.ToastActionTriggeredEvent));
}

export function OffToastActionTriggered(cancel: () => void): void {
  cancel();
}

export type ToastCardAddedHandler = (payload: systemTypes.ToastCard) => void;

export function OnToastCardAdded(client: GosporeClient, handler: ToastCardAddedHandler): () => void {
  return client.events.onService("toast", "toast.card_added", (payload) => handler(payload as systemTypes.ToastCard));
}

export function OffToastCardAdded(cancel: () => void): void {
  cancel();
}

export type ToastCardRemovedHandler = (payload: systemTypes.ToastCardRemovedEvent) => void;

export function OnToastCardRemoved(client: GosporeClient, handler: ToastCardRemovedHandler): () => void {
  return client.events.onService("toast", "toast.card_removed", (payload) => handler(payload as systemTypes.ToastCardRemovedEvent));
}

export function OffToastCardRemoved(cancel: () => void): void {
  cancel();
}

