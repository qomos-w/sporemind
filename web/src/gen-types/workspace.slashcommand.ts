// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface SlashCommand {
  Name: string;
  ShortHelp: string;
}

export interface WorkspaceSlashCommandsListReq {

}

export interface WorkspaceSlashCommandsListResp {
  Commands: SlashCommand[];
}
