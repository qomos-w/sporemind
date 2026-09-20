// AUTO-GENERATED from gospore schema/builtin.go — DO NOT EDIT.
// Regenerate: go test ./internal/wiregen -update
import type { TypeDesc, ObjectDesc } from "../schema";

export const SYS_AUTH_REQ = 64;
export const SYS_AUTH_OK = 65;
export const SYS_EVENT_SUBSCRIBE_SERVICE_REQ = 66;
export const SYS_EVENT_SUBSCRIBE_INSTANCE_REQ = 67;
export const SYS_PROJECTION_GET_REQ = 68;
export const SYS_PROJECTION_WATCH_REQ = 69;

const scalarCache: Record<string, TypeDesc> = {};
function scalar(name: string): TypeDesc {
  return (scalarCache[name] ??= { kind: "scalar", name });
}

export const AUTH_REQ_DESC: TypeDesc = { kind: "struct", name: "AuthReq", className: "AuthReq", classId: SYS_AUTH_REQ };
export const AUTH_REQ_FIELD_TOKEN: TypeDesc = scalar("string");
export const AUTH_REQ_OBJECT: ObjectDesc = { kind: "struct", name: "AuthReq", fields: [{ name: "Token", type: AUTH_REQ_FIELD_TOKEN }] };

export const AUTH_OK_DESC: TypeDesc = { kind: "struct", name: "AuthOk", className: "AuthOk", classId: SYS_AUTH_OK };
export const AUTH_OK_FIELD_MANIFEST: TypeDesc = scalar("string");
export const AUTH_OK_OBJECT: ObjectDesc = { kind: "struct", name: "AuthOk", fields: [{ name: "Manifest", type: AUTH_OK_FIELD_MANIFEST }] };

export const EVENT_SUBSCRIBE_SERVICE_REQ_DESC: TypeDesc = { kind: "struct", name: "eventSubscribeServiceReq", className: "eventSubscribeServiceReq", classId: SYS_EVENT_SUBSCRIBE_SERVICE_REQ };
export const EVENT_SUBSCRIBE_SERVICE_REQ_FIELD_SERVICE_NAME: TypeDesc = scalar("string");
export const EVENT_SUBSCRIBE_SERVICE_REQ_FIELD_KIND: TypeDesc = scalar("string");
export const EVENT_SUBSCRIBE_SERVICE_REQ_OBJECT: ObjectDesc = { kind: "struct", name: "eventSubscribeServiceReq", fields: [{ name: "serviceName", type: EVENT_SUBSCRIBE_SERVICE_REQ_FIELD_SERVICE_NAME }, { name: "kind", type: EVENT_SUBSCRIBE_SERVICE_REQ_FIELD_KIND }] };

export const EVENT_SUBSCRIBE_INSTANCE_REQ_DESC: TypeDesc = { kind: "struct", name: "eventSubscribeInstanceReq", className: "eventSubscribeInstanceReq", classId: SYS_EVENT_SUBSCRIBE_INSTANCE_REQ };
export const EVENT_SUBSCRIBE_INSTANCE_REQ_FIELD_ACTOR_ID: TypeDesc = scalar("string");
export const EVENT_SUBSCRIBE_INSTANCE_REQ_FIELD_KIND: TypeDesc = scalar("string");
export const EVENT_SUBSCRIBE_INSTANCE_REQ_OBJECT: ObjectDesc = { kind: "struct", name: "eventSubscribeInstanceReq", fields: [{ name: "actorId", type: EVENT_SUBSCRIBE_INSTANCE_REQ_FIELD_ACTOR_ID }, { name: "kind", type: EVENT_SUBSCRIBE_INSTANCE_REQ_FIELD_KIND }] };

export const PROJECTION_GET_REQ_DESC: TypeDesc = { kind: "struct", name: "projectionGetReq", className: "projectionGetReq", classId: SYS_PROJECTION_GET_REQ };
export const PROJECTION_GET_REQ_FIELD_ACTOR_PATH: TypeDesc = scalar("string");
export const PROJECTION_GET_REQ_FIELD_COMPONENT: TypeDesc = scalar("string");
export const PROJECTION_GET_REQ_FIELD_SCHEMA_ID: TypeDesc = scalar("uint64");
export const PROJECTION_GET_REQ_OBJECT: ObjectDesc = { kind: "struct", name: "projectionGetReq", fields: [{ name: "actorPath", type: PROJECTION_GET_REQ_FIELD_ACTOR_PATH }, { name: "component", type: PROJECTION_GET_REQ_FIELD_COMPONENT }, { name: "schemaId", type: PROJECTION_GET_REQ_FIELD_SCHEMA_ID }] };

export const PROJECTION_WATCH_REQ_DESC: TypeDesc = { kind: "struct", name: "projectionWatchReq", className: "projectionWatchReq", classId: SYS_PROJECTION_WATCH_REQ };
export const PROJECTION_WATCH_REQ_FIELD_ACTOR_PATH: TypeDesc = scalar("string");
export const PROJECTION_WATCH_REQ_FIELD_COMPONENT: TypeDesc = scalar("string");
export const PROJECTION_WATCH_REQ_FIELD_SCHEMA_ID: TypeDesc = scalar("uint64");
export const PROJECTION_WATCH_REQ_OBJECT: ObjectDesc = { kind: "struct", name: "projectionWatchReq", fields: [{ name: "actorPath", type: PROJECTION_WATCH_REQ_FIELD_ACTOR_PATH }, { name: "component", type: PROJECTION_WATCH_REQ_FIELD_COMPONENT }, { name: "schemaId", type: PROJECTION_WATCH_REQ_FIELD_SCHEMA_ID }] };

// The two scalars the system protocol uses directly.
export const STRING_TYPE: TypeDesc = scalar("string");
export const UINT64_TYPE: TypeDesc = scalar("uint64");
