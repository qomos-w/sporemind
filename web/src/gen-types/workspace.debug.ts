// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { SlashCommand } from './workspace.slashcommand';

export interface DebugCommandsListResp {
  Commands: SlashCommand[];
}

export interface DebugCommandExecReq {
  Name: string;
  Args: string[];
}

export interface DebugCommandExecResp {
  Output: string;
}
