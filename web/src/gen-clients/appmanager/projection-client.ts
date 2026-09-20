// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getApps(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.appmanager.Apps);
}

export async function *watchApps(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.appmanager.Apps);
}

export async function getAuditrecords(client: GosporeClient): Promise<systemTypes.AuditRecord[]> {
  return getProjection<systemTypes.AuditRecord[]>(client, Projections.appmanager.AuditRecords);
}

export async function *watchAuditrecords(client: GosporeClient): AsyncIterable<systemTypes.AuditRecord[]> {
  yield* watchProjection<systemTypes.AuditRecord[]>(client, Projections.appmanager.AuditRecords);
}

