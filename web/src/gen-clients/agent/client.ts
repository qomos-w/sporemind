// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export type AgentMessageReceivedHandler = (payload: systemTypes.AgentMessage) => void;

export function OnAgentMessageReceived(client: GosporeClient, actorId: string, handler: AgentMessageReceivedHandler): () => void {
  return client.events.onInstance(actorId, "agent_message_received", (payload) => handler(payload as systemTypes.AgentMessage));
}

export function OffAgentMessageReceived(cancel: () => void): void {
  cancel();
}

export type StepHandler = (payload: systemTypes.StepEvent) => void;

export function OnStep(client: GosporeClient, actorId: string, handler: StepHandler): () => void {
  return client.events.onInstance(actorId, "step", (payload) => handler(payload as systemTypes.StepEvent));
}

export function OffStep(cancel: () => void): void {
  cancel();
}

export type TurnHandler = (payload: systemTypes.TurnEvent) => void;

export function OnTurn(client: GosporeClient, actorId: string, handler: TurnHandler): () => void {
  return client.events.onInstance(actorId, "turn", (payload) => handler(payload as systemTypes.TurnEvent));
}

export function OffTurn(cancel: () => void): void {
  cancel();
}

