// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface CardRef {
  Id: string;
  Version?: number | undefined;
  Source?: string | undefined;
  Scope?: string | undefined;
  Order?: number | undefined;
  Disabled?: boolean | undefined;
}

export interface CardPromptContribution {
  Id: string;
  Section: string;
  Text: string;
  Priority?: number | undefined;
}

export interface CardToolContribution {
  Id: string;
  CallableId: string;
  Priority?: number | undefined;
}

export interface MonoCard {
  Id: string;
  Type: string;
  Version?: number | undefined;
  ProjectId?: string | undefined;
  Source?: string | undefined;
  Protected?: boolean | undefined;
  OnDemand?: boolean | undefined;
  Body?: string | undefined;
  PromptContributions: CardPromptContribution[];
  ToolContributions: CardToolContribution[];
  Dependencies: CardRef[];
}

export interface BuiltinCardRegistration {
  Id: string;
  Type: string;
  Directory: string;
  MarkdownPath: string;
  EmbedKey: string;
  Protected?: boolean | undefined;
  Visibility?: string | undefined;
}
