// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ComponentRef {
  CardId: string;
  Kind: string;
  Version?: number | undefined;
  Source?: string | undefined;
}

export interface ComponentDependency {
  CardId: string;
  Required?: boolean | undefined;
  Reason?: string | undefined;
}

export interface ComponentPromptContribution {
  Id: string;
  CardId: string;
  Title?: string | undefined;
  Text: string;
  Placement?: string | undefined;
  Priority?: number | undefined;
}

export interface ComponentToolContribution {
  Id: string;
  CardId: string;
  CallableId: string;
  Name?: string | undefined;
  Description?: string | undefined;
  Usage?: string | undefined;
  Constraints?: string | undefined;
  Priority?: number | undefined;
}

export interface ComponentDescriptor {
  Ref: ComponentRef;
  Title: string;
  Icon?: string | undefined;
  Visual?: ComponentVisual | undefined;
  Protected?: boolean | undefined;
  Dependencies: ComponentDependency[];
  Prompts: ComponentPromptContribution[];
  Tools: ComponentToolContribution[];
  ShadowedById?: string | undefined;
}

export interface ComponentVisual {
  Icon?: string | undefined;
  Accent?: string | undefined;
  Emphasis?: string | undefined;
  Color?: string | undefined;
  Background?: string | undefined;
  Border?: string | undefined;
  Size?: number | undefined;
}

export interface AgentComponentMount {
  MountId: string;
  CardId: string;
  Kind?: string | undefined;
  Version?: number | undefined;
  Enabled?: boolean | undefined;
  Order?: number | undefined;
  Scope?: string | undefined;
  Title?: string | undefined;
  Icon?: string | undefined;
  Visual?: ComponentVisual | undefined;
  MountedAt?: string | undefined;
  UpdatedAt?: string | undefined;
}

export interface ComponentDiagnostic {
  CardId: string;
  Level: string;
  Message: string;
  Reason?: string | undefined;
}

export interface AgentComponentSnapshot {
  Revision: number;
  TurnId?: string | undefined;
  Mounts: AgentComponentMount[];
  Prompts: ComponentPromptContribution[];
  Tools: ComponentToolContribution[];
  Diagnostics?: ComponentDiagnostic[] | undefined;
}
