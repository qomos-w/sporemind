// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { SummarySegment } from './aigen.part2';

export interface PromptRef {
  Kind: string;
  Key: string;
  Placement?: string | undefined;
}

export interface Prompt {
  Id: string;
  Content: string;
  Version: number;
  ForkOf: string;
  Meta: Record<string, string>;
}

export interface PromptFragment {
  Id?: string | undefined;
  Key?: string | undefined;
  BuiltinKey?: string | undefined;
  Name: string;
  Kind: string;
  Priority: number;
  Content?: string | undefined;
  Scope?: string | undefined;
  Role?: string | undefined;
  Source?: string | undefined;
  Path?: string | undefined;
  SourceRange?: string | undefined;
  Editable: boolean;
  Deletable: boolean;
}

export interface PromptContextSegment {
  Id: string;
  FragmentId?: string | undefined;
  Chars: number;
}

export interface PromptArtifact {
  Sections: string[];
  Fragments: PromptFragment[];
  ContextSegments?: PromptContextSegment[] | undefined;
  SummarySegments?: SummarySegment[] | undefined;
  ContextWindowSize?: number | undefined;
  TokenBudget?: number | undefined;
}

export interface PromptProfile {
  Key?: string | undefined;
  BuiltinKey?: string | undefined;
  Scope?: string | undefined;
  Role?: string | undefined;
  RolePrompt: string;
  FragmentRefs?: string[] | undefined;
  Source?: string | undefined;
  Editable: boolean;
  Deletable: boolean;
  UpdatedAt?: string | undefined;
}

export interface PromptManagerGetReq {
  Id: string;
}

export interface PromptManagerSaveReq {
  Id: string;
  Content: string;
  Meta: Record<string, string>;
}

export interface PromptManagerListFragmentsReq {
  Scope: string;
  Role: string;
}

export interface PromptManagerGetArtifactReq {
  Intent: string;
  Scope: string;
  Role: string;
}

export interface PromptManagerGetProfileReq {
  Scope: string;
  Role: string;
}

export interface PromptManagerSaveProfileReq {
  Key?: string | undefined;
  Scope?: string | undefined;
  Role?: string | undefined;
  BuiltinKey?: string | undefined;
  RolePrompt: string;
}

export interface PromptManagerDeleteProfileReq {
  Key: string;
}

export interface PromptManagerSaveFragmentReq {
  Key?: string | undefined;
  Name: string;
  Kind: string;
  Priority: number;
  Content?: string | undefined;
  Scope?: string | undefined;
  Role?: string | undefined;
}

export interface PromptManagerDeleteFragmentReq {
  Key: string;
}

export interface PromptManagerResolveRefReq {
  Ref: PromptRef;
  Slot: string;
}

export interface PromptResolvedRef {
  Content: string;
  Source: string;
}

export interface PromptManagerResolveFragmentsReq {
  Refs: PromptRef[];
}

export interface PromptResolvedFragmentsResp {
  Fragments: PromptFragment[];
}

export interface PromptListResp {
  Items: Prompt[];
}

export interface PromptFragmentListResp {
  Items: PromptFragment[];
}

export interface PromptProfileListResp {
  Items: PromptProfile[];
}
