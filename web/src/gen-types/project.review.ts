// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { CardValidationError } from './project.wiki.part2';

export interface ProjectReviewChangesetReq {
  AgentActorID: string;
  ForceRefresh?: boolean | undefined;
  CallerAgentId?: string | undefined;
  TestCommand?: string | undefined;
  TestTimeoutMs?: number | undefined;
  TaskCardID?: string | undefined;
}

export interface ProjectReviewChangesetSummaryResp {
  AgentActorID: string;
  WorktreeID: string;
  GeneratedAt: string;
  Status: string;
  ErrorMsg: string;
  Baseline: string;
  Head: string;
  Branch: string;
  IsDirty: boolean;
  Commits: ProjectReviewCommitInfo[];
  Files: ProjectReviewFileEntry[];
  UntrackedFiles: ProjectReviewUntrackedFile[];
  TestResult: ProjectReviewTestResult;
  Stats: ProjectReviewChangesetStats;
}

export interface ProjectReviewCommitInfo {
  Hash: string;
  Short: string;
  Message: string;
  Author: string;
  When: string;
}

export interface ProjectReviewFileEntry {
  Path: string;
  Status: string;
  OldPath: string;
  IsBinary: boolean;
  IsGenerated: boolean;
  DiffLines: number;
}

export interface ProjectReviewUntrackedFile {
  Path: string;
  Size: number;
  IsBinary: boolean;
  IsText: boolean;
  Preview: string;
}

export interface ProjectReviewTestResult {
  Command: string;
  ExitCode: number;
  Output: string;
  Duration: string;
  Success: boolean;
  Skipped: boolean;
  ErrorMsg: string;
}

export interface ProjectReviewChangesetStats {
  TrackedChanged: number;
  Untracked: number;
  AddedLines: number;
  DeletedLines: number;
  TotalDiffLines: number;
}

export interface ProjectReviewFileContentReq {
  AgentActorID: string;
  FilePath: string;
  Offset?: number | undefined;
  Limit?: number | undefined;
  CallerAgentId?: string | undefined;
}

export interface ProjectReviewFileContentResp {
  FilePath: string;
  Status: string;
  Content: string;
  IsBinary: boolean;
  IsGenerated: boolean;
  StartLine: number;
  NumLines: number;
  TotalLines: number;
  Truncated: boolean;
}

export interface ProjectReviewChangesetFinalizeReq {
  AgentActorID: string;
  TaskCardID?: string | undefined;
  WorktreeID?: string | undefined;
  Generation?: number | undefined;
  Summary?: ProjectReviewChangesetSummaryResp | undefined;
  FileContents?: Record<string, string> | undefined;
  ErrMsg?: string | undefined;
}

export interface ProjectTaskValidateOutputsReq {
  CardID: string;
  Outputs: Record<string, unknown>;
}

export interface ProjectTaskValidateOutputsResp {
  Valid: boolean;
  Errors: CardValidationError[];
}
