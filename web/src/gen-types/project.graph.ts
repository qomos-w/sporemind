// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface GraphSnapshotMeta {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  Revision: string;
  SchemaVersion: string;
  CreatedAt: string;
  UpdatedAt?: string | undefined;
  Source?: string | undefined;
}

export interface ProjectDefinition {
  Id: string;
  Purpose: string;
  Scope: string[];
  NonGoals: string[];
  Source: string;
}

export interface ValueConstraint {
  Id: string;
  Statement: string;
  Allows: string[];
  Forbids: string[];
  Source: string;
}

export interface EngineeringConstraint {
  Id: string;
  Statement: string;
  AppliesTo: string[];
  Must: string[];
  MustNot: string[];
  Source: string;
}

export interface ProjectCharter {
  Definition: ProjectDefinition;
  ValueConstraints: ValueConstraint[];
  EngineeringConstraints: EngineeringConstraint[];
}

export interface Capability {
  Id: string;
  Promise: string;
  Concepts: string[];
  Checks: string[];
}

export interface Concept {
  Id: string;
  Desc: string;
  Capabilities: string[];
  Constraints: string[];
  Checks: string[];
  State: string;
}

export interface CoreValue {
  Id: string;
  Statement: string;
  Constrains: string[];
  Source: string;
}

export interface Constraint {
  Id: string;
  Statement: string;
  AppliesTo: string[];
  Scope: string;
}

export interface Check {
  Id: string;
  Statement: string;
}

export interface Relation {
  From: string;
  To: string;
  Relation: string;
}

export interface FileAnchor {
  Path: string;
  Role: string;
  Supports: string[];
  EvidenceScope?: string | undefined;
}

export interface FileCoverage {
  Path: string;
  Status: string;
  Supports?: string[] | undefined;
  Reason?: string | undefined;
}

export interface ProjectGraphScope {
  Root: string;
  Include: string[];
  Ignore: string[];
  PhaseId?: string | undefined;
}

export interface TargetGraph {
  Charter: ProjectCharter;
  Capabilities: Capability[];
  Concepts: Concept[];
  CoreValues: CoreValue[];
  Constraints: Constraint[];
  Checks: Check[];
  Relations: Relation[];
  Sources: string[];
}

export interface PhaseGraph {
  PhaseId: string;
  Baseline: string;
  Capabilities: string[];
  Concepts: string[];
  CoreValues: string[];
  Constraints: string[];
  Checks: string[];
  Relations: Relation[];
  Future: string[];
}

export interface RealityGraph {
  Concepts: Concept[];
  Relations: Relation[];
  Anchors: FileAnchor[];
  Coverage: FileCoverage[];
}

export interface SemanticDiff {
  Id: string;
  Type: string;
  Target?: string | undefined;
  Reality?: string | undefined;
  Desc: string;
  Evidence: string[];
}

export interface AlignmentMove {
  Id: string;
  Closes: string[];
  TargetState: string;
  Acceptance: string[];
  CandidateAnchors: FileAnchor[];
  Splittable: boolean;
}

export interface GraphKanbanCard {
  Id: string;
  MoveId: string;
  Closes: string[];
  PhaseId: string;
  ConceptIds: string[];
  CandidateAnchors: string[];
  AcceptanceChecks: string[];
  ParallelGroup?: string | undefined;
  OwnerAgentId?: string | undefined;
}

export interface DiffPlan {
  Diffs: SemanticDiff[];
  Moves: AlignmentMove[];
  Cards: GraphKanbanCard[];
}

export interface ProjectionNode {
  ProjectionId: string;
  ConceptId?: string | undefined;
  Section: string;
  Children: ProjectionNode[];
}

export interface ProjectionGraph {
  ProjectionId: string;
  Root: ProjectionNode;
}

export interface ProjectGraphGetReq {
  ProjectId: string;
  GraphKind: string;
  Id?: string | undefined;
  Revision?: string | undefined;
}

export interface ProjectGraphSaveReq {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  ExpectedRevision?: string | undefined;
  Meta: GraphSnapshotMeta;
  EnvelopeText: string;
}

export interface ProjectGraphEnvelopeResp {
  Meta: GraphSnapshotMeta;
  EnvelopeText: string;
}

export interface ProjectGraphConceptGetReq {
  ProjectId: string;
  GraphKind: string;
  Id: string;
  ConceptId: string;
}

export interface ProjectGraphConceptGetResp {
  Concept: Concept;
  GraphKind: string;
  Id: string;
  Revision: string;
}

export interface ProjectGraphTraceReq {
  ProjectId: string;
  PhaseId?: string | undefined;
  Include?: string[] | undefined;
  Ignore?: string[] | undefined;
}

export interface ProjectGraphDiffReq {
  ProjectId: string;
  PhaseId: string;
  RealityRevision?: string | undefined;
}

export interface ProjectGraphPlanMovesReq {
  ProjectId: string;
  PhaseId: string;
  DiffRevision?: string | undefined;
}

export interface ProjectGraphProjectionReq {
  ProjectId: string;
  ProjectionId: string;
  GraphKind?: string | undefined;
  Id?: string | undefined;
}
