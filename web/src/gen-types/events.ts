// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SkillsChangedEvent {
  ProjectId: string;
  Count: number;
}

export interface WikiCardChangedEvent {
  Id: string;
  Modified: string;
}

export interface GraphChangedEvent {
  GraphKind: string;
  Id: string;
  Revision: string;
}
