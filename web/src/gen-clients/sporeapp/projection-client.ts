// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getAssets(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.sporeapp.Assets);
}

export async function *watchAssets(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.sporeapp.Assets);
}

export async function getEntrymodule(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.sporeapp.EntryModule);
}

export async function *watchEntrymodule(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.sporeapp.EntryModule);
}

export async function getManifest(client: GosporeClient): Promise<systemTypes.AppManifest> {
  return getProjection<systemTypes.AppManifest>(client, Projections.sporeapp.Manifest);
}

export async function *watchManifest(client: GosporeClient): AsyncIterable<systemTypes.AppManifest> {
  yield* watchProjection<systemTypes.AppManifest>(client, Projections.sporeapp.Manifest);
}

export async function getMigrationversion(client: GosporeClient): Promise<number> {
  return getProjection<number>(client, Projections.sporeapp.MigrationVersion);
}

export async function *watchMigrationversion(client: GosporeClient): AsyncIterable<number> {
  yield* watchProjection<number>(client, Projections.sporeapp.MigrationVersion);
}

export async function getModules(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.sporeapp.Modules);
}

export async function *watchModules(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.sporeapp.Modules);
}

export async function getPackagehash(client: GosporeClient): Promise<string> {
  return getProjection<string>(client, Projections.sporeapp.PackageHash);
}

export async function *watchPackagehash(client: GosporeClient): AsyncIterable<string> {
  yield* watchProjection<string>(client, Projections.sporeapp.PackageHash);
}

export async function getSchemadescriptors(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.sporeapp.SchemaDescriptors);
}

export async function *watchSchemadescriptors(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.sporeapp.SchemaDescriptors);
}

export async function getState(client: GosporeClient): Promise<Record<string, any>> {
  return getProjection<Record<string, any>>(client, Projections.sporeapp.State);
}

export async function *watchState(client: GosporeClient): AsyncIterable<Record<string, any>> {
  yield* watchProjection<Record<string, any>>(client, Projections.sporeapp.State);
}

export async function getStateversion(client: GosporeClient): Promise<number> {
  return getProjection<number>(client, Projections.sporeapp.StateVersion);
}

export async function *watchStateversion(client: GosporeClient): AsyncIterable<number> {
  yield* watchProjection<number>(client, Projections.sporeapp.StateVersion);
}

