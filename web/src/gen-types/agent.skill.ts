// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AgentSkillUseReq {
  SkillId: string;
  Args?: string | undefined;
  Context?: string | undefined;
  TurnId?: string | undefined;
}

export interface AgentSkillUseResp {
  MountId: string;
  Body?: string | undefined;
  SkillId?: string | undefined;
  Warning?: string | undefined;
}
