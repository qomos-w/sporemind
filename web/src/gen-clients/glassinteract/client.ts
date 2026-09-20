// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export type GlassEventCompletedHandler = (payload: systemTypes.GlassEventCompletedEvent) => void;

export function OnGlassEventCompleted(client: GosporeClient, handler: GlassEventCompletedHandler): () => void {
  return client.events.onService("glass_interact", "glass.event.completed", (payload) => handler(payload as systemTypes.GlassEventCompletedEvent));
}

export function OffGlassEventCompleted(cancel: () => void): void {
  cancel();
}

export type GlassEventDeliveredHandler = (payload: systemTypes.GlassEventDeliveredEvent) => void;

export function OnGlassEventDelivered(client: GosporeClient, handler: GlassEventDeliveredHandler): () => void {
  return client.events.onService("glass_interact", "glass.event.delivered", (payload) => handler(payload as systemTypes.GlassEventDeliveredEvent));
}

export function OffGlassEventDelivered(cancel: () => void): void {
  cancel();
}

export type GlassEventEnqueuedHandler = (payload: systemTypes.GlassEventEnqueuedEvent) => void;

export function OnGlassEventEnqueued(client: GosporeClient, handler: GlassEventEnqueuedHandler): () => void {
  return client.events.onService("glass_interact", "glass.event.enqueued", (payload) => handler(payload as systemTypes.GlassEventEnqueuedEvent));
}

export function OffGlassEventEnqueued(cancel: () => void): void {
  cancel();
}

export type GlassHudUpdateHandler = (payload: systemTypes.GlassHudStatus) => void;

export function OnGlassHudUpdate(client: GosporeClient, handler: GlassHudUpdateHandler): () => void {
  return client.events.onService("glass_interact", "glass.hud.update", (payload) => handler(payload as systemTypes.GlassHudStatus));
}

export function OffGlassHudUpdate(cancel: () => void): void {
  cancel();
}

export type GlassInteractionHandler = (payload: systemTypes.GlassInteractionEvent) => void;

export function OnGlassInteraction(client: GosporeClient, handler: GlassInteractionHandler): () => void {
  return client.events.onService("glass_interact", "glass.interaction", (payload) => handler(payload as systemTypes.GlassInteractionEvent));
}

export function OffGlassInteraction(cancel: () => void): void {
  cancel();
}

export type GlassOfflineHandler = (payload: systemTypes.GlassLifecycleEvent) => void;

export function OnGlassOffline(client: GosporeClient, handler: GlassOfflineHandler): () => void {
  return client.events.onService("glass_interact", "glass.offline", (payload) => handler(payload as systemTypes.GlassLifecycleEvent));
}

export function OffGlassOffline(cancel: () => void): void {
  cancel();
}

export type GlassOnlineHandler = (payload: systemTypes.GlassLifecycleEvent) => void;

export function OnGlassOnline(client: GosporeClient, handler: GlassOnlineHandler): () => void {
  return client.events.onService("glass_interact", "glass.online", (payload) => handler(payload as systemTypes.GlassLifecycleEvent));
}

export function OffGlassOnline(cancel: () => void): void {
  cancel();
}

export type GlassReconnectedHandler = (payload: systemTypes.GlassLifecycleEvent) => void;

export function OnGlassReconnected(client: GosporeClient, handler: GlassReconnectedHandler): () => void {
  return client.events.onService("glass_interact", "glass.reconnected", (payload) => handler(payload as systemTypes.GlassLifecycleEvent));
}

export function OffGlassReconnected(cancel: () => void): void {
  cancel();
}

export type GlassRenderHandler = (payload: systemTypes.GlassRenderEvent) => void;

export function OnGlassRender(client: GosporeClient, handler: GlassRenderHandler): () => void {
  return client.events.onService("glass_interact", "glass.render", (payload) => handler(payload as systemTypes.GlassRenderEvent));
}

export function OffGlassRender(cancel: () => void): void {
  cancel();
}

export type GlassReplacedHandler = (payload: systemTypes.GlassLifecycleEvent) => void;

export function OnGlassReplaced(client: GosporeClient, handler: GlassReplacedHandler): () => void {
  return client.events.onService("glass_interact", "glass.replaced", (payload) => handler(payload as systemTypes.GlassLifecycleEvent));
}

export function OffGlassReplaced(cancel: () => void): void {
  cancel();
}

export type GlassSpeakHandler = (payload: systemTypes.GlassSpeakEvent) => void;

export function OnGlassSpeak(client: GosporeClient, handler: GlassSpeakHandler): () => void {
  return client.events.onService("glass_interact", "glass.speak", (payload) => handler(payload as systemTypes.GlassSpeakEvent));
}

export function OffGlassSpeak(cancel: () => void): void {
  cancel();
}

export type GlassTranscriptHandler = (payload: systemTypes.GlassTranscriptEvent) => void;

export function OnGlassTranscript(client: GosporeClient, handler: GlassTranscriptHandler): () => void {
  return client.events.onService("glass_interact", "glass.transcript", (payload) => handler(payload as systemTypes.GlassTranscriptEvent));
}

export function OffGlassTranscript(cancel: () => void): void {
  cancel();
}

