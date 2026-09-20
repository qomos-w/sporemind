// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppSchemaRef {
  Name: string;
  Hash: string;
  SchemaId?: number | undefined;
}

export interface AppTypeDescriptor {
  Kind: string;
  Name?: string | undefined;
  TypeId?: number | undefined;
  Element?: AppTypeDescriptor | undefined;
  Key?: AppTypeDescriptor | undefined;
  Value?: AppTypeDescriptor | undefined;
  ClassName?: string | undefined;
  ClassId?: number | undefined;
}

export interface AppFieldDescriptor {
  Name: string;
  Type: AppTypeDescriptor;
  Description?: string | undefined;
  Private?: boolean | undefined;
  Optional?: boolean | undefined;
}

export interface AppObjectDescriptor {
  Kind: string;
  Name: string;
  Fields: AppFieldDescriptor[];
  SchemaId?: number | undefined;
}

export interface AppCallableDescriptor {
  Id: string;
  Description?: string | undefined;
  RequestSchema: string;
  ResponseSchema: string;
  Encoding?: string | undefined;
  Permission?: string | undefined;
  Scope?: string | undefined;
  TimeoutMs?: number | undefined;
  Streaming?: boolean | undefined;
  Effect?: string | undefined;
  Service?: string | undefined;
  ToolName?: string | undefined;
  Expose?: string | undefined;
  Watch?: string[] | undefined;
}

export interface AppEventDescriptor {
  Id: string;
  PayloadSchema: string;
  Encoding?: string | undefined;
  Permission?: string | undefined;
}

export interface AppProjectionDescriptor {
  Id: string;
  PayloadSchema: string;
  Permission?: string | undefined;
}

export interface AppEntrypoint {
  Kind: string;
  Id: string;
  Title: string;
  Route?: string | undefined;
  Zone?: string | undefined;
}

export interface AppDependency {
  Id: string;
  Version: string;
  Hash?: string | undefined;
}

export interface AgentCapabilityBinding {
  Callables: string[];
  ProjectScope?: string | undefined;
}

export interface AgentSurfaceBinding {
  Entrypoint: string;
  AgentId?: string | undefined;
  Projections: string[];
  Events: string[];
}

export interface FreeAgentBinding {
  AllowCreate: boolean;
  AllowSwitch: boolean;
  AllowMessage: boolean;
  AgentKinds?: string[] | undefined;
}

export interface PluginAgentBinding {
  Name: string;
  DisplayName?: string | undefined;
  SystemPrompt?: string | undefined;
  Bundles?: string[] | undefined;
  Model?: string | undefined;
}

export interface AppAgentBinding {
  Capability?: AgentCapabilityBinding | undefined;
  Surface?: AgentSurfaceBinding | undefined;
  FreeAgent?: FreeAgentBinding | undefined;
  PluginAgents?: PluginAgentBinding[] | undefined;
}

export interface AppSecurityPolicy {
  AllowNativeAccess?: boolean | undefined;
  AllowCapabilities?: string[] | undefined;
  MaxInstructions?: number | undefined;
  MaxDurationMs?: number | undefined;
  MaxHostCalls?: number | undefined;
  MaxOutputBytes?: number | undefined;
}

export interface AppBundleTool {
  CallableId: string;
  Effect?: string | undefined;
  Service?: string | undefined;
  ToolName?: string | undefined;
  Stream?: boolean | undefined;
}

export interface AppBundle {
  Title: string;
  Description?: string | undefined;
  Icon?: string | undefined;
  Color?: string | undefined;
  GuideCardId?: string | undefined;
  Tools: AppBundleTool[];
}

export interface AppManifest {
  Id: string;
  Name: string;
  Version: string;
  Runtime: string;
  ProtocolVersion: number;
  SdkVersion?: string | undefined;
  Namespace: string;
  Permissions: string[];
  Listens?: string[] | undefined;
  Schemas: AppSchemaRef[];
  Callables: AppCallableDescriptor[];
  Events: AppEventDescriptor[];
  Projections: AppProjectionDescriptor[];
  Entrypoints: AppEntrypoint[];
  Dependencies: AppDependency[];
  Bundles?: AppBundle[] | undefined;
  AgentBinding?: AppAgentBinding | undefined;
  Security?: AppSecurityPolicy | undefined;
}

export interface AppAbiDescriptor {
  Name: string;
  Version: number;
  Encoding: string;
}

export interface AppProtocolDescriptor {
  Namespace: string;
  ProtocolVersion: number;
  SchemaHash: string;
  Abi: AppAbiDescriptor;
  Schemas: AppSchemaRef[];
  Callables: AppCallableDescriptor[];
  Events: AppEventDescriptor[];
  Projections: AppProjectionDescriptor[];
}

export interface AppStatus {
  Id: string;
  Name?: string | undefined;
  Runtime: string;
  State: string;
  Version?: string | undefined;
  Namespace?: string | undefined;
  PackageHash?: string | undefined;
  ArtifactHash?: string | undefined;
  Generation?: number | undefined;
  Error?: string | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  Callables?: AppCallableDescriptor[] | undefined;
  Events?: AppEventDescriptor[] | undefined;
  Bundles?: AppBundle[] | undefined;
  Entrypoints: AppEntrypoint[];
  Permissions?: string[] | undefined;
  GrantedCapabilities?: string[] | undefined;
  Dependencies?: AppDependency[] | undefined;
  Dependents?: string[] | undefined;
  BackendPort?: number | undefined;
  BackendUrl?: string | undefined;
}

export interface AppLifecycleEvent {
  Kind: string;
  Id: string;
  Runtime: string;
  State: string;
  Version?: string | undefined;
  Generation?: number | undefined;
  Error?: string | undefined;
}

export interface AppStatusProjection {
  Items: AppStatus[];
}
