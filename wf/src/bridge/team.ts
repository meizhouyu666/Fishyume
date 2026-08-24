import {EngineRpcError, type EngineClient} from './engine.js';

export const teamApiVersion = 'fishyume.team/v1' as const;
export const teamMethods = [
  'team.capabilities', 'team.start', 'team.list', 'team.get', 'team.events', 'team.messages', 'team.action',
  'team.handoff.create', 'team.handoff.get', 'team.handoff.list', 'team.handoff.bindRun',
] as const;
export type TeamMethod = typeof teamMethods[number];
export type TeamErrorCode = 'invalid_argument' | 'not_found' | 'conflict' | 'capability_unavailable' | 'quota_exceeded' | 'not_ready' | 'session_lost' | 'protocol_mismatch' | 'internal';
export interface TeamError {code: TeamErrorCode; message: string; retryable?: boolean}
export type TeamMode = 'panel' | 'session';
export type TeamLifecycle = 'created' | 'running' | 'open' | 'closing' | 'cancelling' | 'closed';
export type TeamCloseReason = 'panel_settled' | 'host_closed' | 'cancelled';
export type ParticipantState = 'pending' | 'running' | 'responded' | 'failed' | 'indeterminate' | 'cancelled';
export type TurnState = 'prepared' | 'dispatching' | 'active' | 'responded' | 'failed' | 'indeterminate' | 'cancelling' | 'cancelled';

export interface ParticipantSpec {label: string; role: string; modelId: string}
export interface Participant {participantId: string; label: string; role: string; modelId: string; driver: string; target: string; state: ParticipantState; currentTurnId?: string}
export interface UsageEstimate {inputTokens?: number; outputTokens?: number; totalTokens?: number}
export interface TeamTurnUsage {target: string; catalogHash: string; costUnits: number; cumulativeCostUnits: number; tokenEstimate?: UsageEstimate}
export interface ParticipantTurn {schemaVersion: typeof teamApiVersion; teamId: string; participantId: string; turnId: string; number: number; state: TurnState; driver: string; target: string; modelId: string; usage: TeamTurnUsage; contributionMessageId?: string; diagnostic?: string; createdAt: string; updatedAt: string; completedAt?: string}
export interface TeamSession {schemaVersion: typeof teamApiVersion; teamId: string; clientRequestId: string; requestHash: string; project: string; mode: TeamMode; topic: string; instructions?: string; catalogHash: string; participants: Participant[]; state: TeamLifecycle; stateVersion: number; costGrant: number; costUsed: number; closeReason?: TeamCloseReason; createdAt: string; updatedAt: string}
export interface Contribution {schemaVersion: typeof teamApiVersion; status: 'completed' | 'partial' | 'unable'; contentMarkdown: string; warnings?: string[]; openQuestions?: string[]; usageEstimates?: UsageEstimate}
export interface TeamMessage {schemaVersion: typeof teamApiVersion; messageId: string; teamId: string; sequence: number; kind: 'host_message' | 'participant_contribution'; actor: string; recipients?: string[]; turnId?: string; content: string; referencedMessageIds?: string[]; createdAt: string; contentHash: string}
export interface TeamEvent {schemaVersion: typeof teamApiVersion; teamId: string; sequence: number; type: string; stateVersion: number; messageId?: string; turnId?: string; summary?: string; createdAt: string}
export interface TeamCapabilities {schemaVersion: typeof teamApiVersion; supportedModes: TeamMode[]; features: {panel: boolean; handoff: boolean; session: boolean; followUp: boolean; cancelTurn: boolean; close: boolean; cancel: boolean}; limits: Record<string, number>; participantTemplates: Array<ParticipantSpec & {driver: string; target: string}>; catalogHash: string}
export interface TeamStartRequest {schemaVersion: typeof teamApiVersion; clientRequestId: string; project: string; mode: TeamMode; topic: string; instructions?: string; participants?: ParticipantSpec[]; costGrant?: number}
export interface TeamStartResponse {schemaVersion: typeof teamApiVersion; team: TeamSession; replayed: boolean}
export interface TeamSummary {teamId: string; project: string; mode: TeamMode; topic: string; state: TeamLifecycle; stateVersion: number; closeReason?: TeamCloseReason; participants: number; costGrant: number; costUsed: number; createdAt: string; updatedAt: string}
export interface TeamListResponse {schemaVersion: typeof teamApiVersion; items: TeamSummary[]; nextCursor?: string}
export interface TeamGetResponse {schemaVersion: typeof teamApiVersion; team: TeamSession; turns: ParticipantTurn[]}
export interface TeamEventsResponse {schemaVersion: typeof teamApiVersion; teamId: string; events: TeamEvent[]; nextAfterSequence: number; more: boolean}
export interface TeamMessagesResponse {schemaVersion: typeof teamApiVersion; teamId: string; messages: TeamMessage[]; nextAfterSequence: number; more: boolean}
export type TeamActionType = 'follow_up' | 'cancel_turn' | 'close' | 'cancel';
export interface TeamActionRequest {schemaVersion: typeof teamApiVersion; actionId: string; teamId: string; expectedStateVersion: number; type: TeamActionType; followUp?: {content: string; participantIds: string[]; referencedMessageIds?: string[]}; cancelTurn?: {turnId: string}; close?: {reason: 'host_closed' | 'cancelled'}}
export interface TeamActionResponse {schemaVersion: typeof teamApiVersion; actionId: string; teamId: string; type: TeamActionType; stateVersion: number; state: TeamLifecycle; replayed: boolean}
export interface HandoffArtifact {schemaVersion: typeof teamApiVersion; handoffId: string; teamId: string; sourceTeamVersion: number; goal: string; decisions?: string[]; constraints?: string[]; openQuestions?: string[]; acceptanceExpectations?: string[]; selectedMessageIds: string[]; sourceMessageHashes: string[]; contentHash: string; createdAt: string}
export interface HandoffBinding {teamId: string; handoffId: string; runId: string; project: string; boundAt: string}

export interface TeamResponses {
  'team.capabilities': TeamCapabilities;
  'team.start': TeamStartResponse;
  'team.list': TeamListResponse;
  'team.get': TeamGetResponse;
  'team.events': TeamEventsResponse;
  'team.messages': TeamMessagesResponse;
  'team.action': TeamActionResponse;
  'team.handoff.create': {schemaVersion: typeof teamApiVersion; handoff: HandoffArtifact; replayed: boolean};
  'team.handoff.get': {schemaVersion: typeof teamApiVersion; handoff: HandoffArtifact; binding?: HandoffBinding};
  'team.handoff.list': {schemaVersion: typeof teamApiVersion; items: HandoffArtifact[]; nextCursor?: string};
  'team.handoff.bindRun': {schemaVersion: typeof teamApiVersion; binding: HandoffBinding; replayed: boolean};
}

export class TeamCallError extends Error {
  constructor(readonly teamError: TeamError) {super(teamError.message); this.name = 'TeamCallError'}
}

export async function callTeam<M extends TeamMethod>(client: EngineClient, method: M, request: unknown): Promise<TeamResponses[M]> {
  try {return await client.call<TeamResponses[M]>(method, request)}
  catch (error) {
    if (error instanceof EngineRpcError && isTeamError(error.data)) throw new TeamCallError(error.data);
    throw error;
  }
}

function isTeamError(value: unknown): value is TeamError {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<TeamError>;
  return typeof candidate.code === 'string' && typeof candidate.message === 'string';
}
