// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import { getProjection, watchProjection, Projections } from "../projections";
import type * as systemTypes from "../system/types";

export async function getAccounts(client: GosporeClient): Promise<systemTypes.Account[]> {
  return getProjection<systemTypes.Account[]>(client, Projections.user.Accounts);
}

export async function *watchAccounts(client: GosporeClient): AsyncIterable<systemTypes.Account[]> {
  yield* watchProjection<systemTypes.Account[]>(client, Projections.user.Accounts);
}

export async function getGroups(client: GosporeClient): Promise<systemTypes.Group[]> {
  return getProjection<systemTypes.Group[]>(client, Projections.user.Groups);
}

export async function *watchGroups(client: GosporeClient): AsyncIterable<systemTypes.Group[]> {
  yield* watchProjection<systemTypes.Group[]>(client, Projections.user.Groups);
}

export async function getPermissions(client: GosporeClient): Promise<systemTypes.PermissionMatrix> {
  return getProjection<systemTypes.PermissionMatrix>(client, Projections.user.Permissions);
}

export async function *watchPermissions(client: GosporeClient): AsyncIterable<systemTypes.PermissionMatrix> {
  yield* watchProjection<systemTypes.PermissionMatrix>(client, Projections.user.Permissions);
}

export async function getRefreshtokens(client: GosporeClient): Promise<systemTypes.RefreshTokenEntry[]> {
  return getProjection<systemTypes.RefreshTokenEntry[]>(client, Projections.user.RefreshTokens);
}

export async function *watchRefreshtokens(client: GosporeClient): AsyncIterable<systemTypes.RefreshTokenEntry[]> {
  yield* watchProjection<systemTypes.RefreshTokenEntry[]>(client, Projections.user.RefreshTokens);
}

