// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";

export const Projections = {
  agent: {
    ActiveTurnRef: {
      actorPath: "agent",
      component: "ActiveTurnRef",
      schemaId: 0,
      schemaName: "string",
      mode: "full",
    },
    ComponentMounts: {
      actorPath: "agent",
      component: "ComponentMounts",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    ComponentRevision: {
      actorPath: "agent",
      component: "ComponentRevision",
      schemaId: 0,
      schemaName: "int",
      mode: "full",
    },
    DisplayName: {
      actorPath: "agent",
      component: "DisplayName",
      schemaId: 0,
      schemaName: "string",
      mode: "full",
    },
    RawSession: {
      actorPath: "agent",
      component: "RawSession",
      schemaId: 379,
      schemaName: "RawSession",
      mode: "full",
    },
    Session: {
      actorPath: "agent",
      component: "Session",
      schemaId: 380,
      schemaName: "Session",
      mode: "full",
    },
    Title: {
      actorPath: "agent",
      component: "Title",
      schemaId: 0,
      schemaName: "string",
      mode: "full",
    },
    TurnPauseKind: {
      actorPath: "agent",
      component: "TurnPauseKind",
      schemaId: 0,
      schemaName: "string",
      mode: "full",
    },
    TurnState: {
      actorPath: "agent",
      component: "TurnState",
      schemaId: 0,
      schemaName: "string",
      mode: "full",
    },
  },
  aimanager: {
    AggregatorNames: {
      actorPath: "aimanager",
      component: "AggregatorNames",
      schemaId: 0,
      schemaName: "map",
      mode: "full",
    },
    Providers: {
      actorPath: "aimanager",
      component: "Providers",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  appmanager: {
    Apps: {
      actorPath: "appmanager",
      component: "Apps",
      schemaId: 0,
      schemaName: "map",
      mode: "full",
    },
    AuditRecords: {
      actorPath: "appmanager",
      component: "AuditRecords",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  browsermanager: {
    Instances: {
      actorPath: "browsermanager",
      component: "Instances",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  frpmanager: {
    Instances: {
      actorPath: "frpmanager",
      component: "Instances",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  pluginhost: {
    Plugins: {
      actorPath: "pluginhost",
      component: "Plugins",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  project: {
    Graphs: {
      actorPath: "project",
      component: "Graphs",
      schemaId: 0,
      schemaName: "map",
      mode: "full",
    },
  },
  sshmanager: {
    Hosts: {
      actorPath: "sshmanager",
      component: "Hosts",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  user: {
    Accounts: {
      actorPath: "user",
      component: "Accounts",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    Groups: {
      actorPath: "user",
      component: "Groups",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    Permissions: {
      actorPath: "user",
      component: "Permissions",
      schemaId: 1763,
      schemaName: "PermissionMatrix",
      mode: "full",
    },
    RefreshTokens: {
      actorPath: "user",
      component: "RefreshTokens",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
  },
  workbench: {
    Snapshot: {
      actorPath: "workbench",
      component: "Snapshot",
      schemaId: 6417,
      schemaName: "WorkbenchSnapshot",
      mode: "full",
    },
  },
  workspace: {
    AgentKindConfigs: {
      actorPath: "workspace",
      component: "AgentKindConfigs",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    Agents: {
      actorPath: "workspace",
      component: "Agents",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    Mounts: {
      actorPath: "workspace",
      component: "Mounts",
      schemaId: 0,
      schemaName: "array",
      mode: "full",
    },
    UI: {
      actorPath: "workspace",
      component: "UI",
      schemaId: 1916,
      schemaName: "WorkspaceUIModel",
      mode: "full",
    },
  },
} as const;

export async function getProjection<T>(client: GosporeClient, p: {
  actorPath: string
  component: string
  schemaId: number
}): Promise<T> {
  return client.invoke("gospore.projection.get", {
    actorPath: p.actorPath,
    component: p.component,
    schemaId: p.schemaId,
  }, { reqSchemaId: 68 }) as Promise<T>
}

export async function *watchProjection<T>(client: GosporeClient, p: {
  actorPath: string
  component: string
  schemaId: number
}): AsyncIterable<T> {
  yield* client.subscribe<T>("gospore.projection.watch", {
    actorPath: p.actorPath,
    component: p.component,
    schemaId: p.schemaId,
  }, { reqSchemaId: 69 })
}
