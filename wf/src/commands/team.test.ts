import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello} from '../bridge/types.js';
import {parseParticipant, startTeam} from './team.js';

class TeamClient implements EngineClient {
  closed = false;
  calls: Array<{method: string; params?: unknown}> = [];
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'team.start') return {schemaVersion: 'fishyume.team/v1', replayed: false, team: team('running')} as T;
    if (method === 'team.get') return {schemaVersion: 'fishyume.team/v1', team: team('closed'), turns: [{schemaVersion: 'fishyume.team/v1', teamId: 'team-1', participantId: 'participant-1', turnId: 'turn-1', number: 1, state: 'responded', driver: 'codex', target: 'local', modelId: 'codex/local/gpt-5.6', usage: {target: 'local', catalogHash: 'a'.repeat(64), costUnits: 100, cumulativeCostUnits: 100}, createdAt: now, updatedAt: now}]} as T;
    if (method === 'team.messages') return {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', messages: [{schemaVersion: 'fishyume.team/v1', messageId: 'message-1', teamId: 'team-1', sequence: 1, kind: 'participant_contribution', actor: 'participant-1', turnId: 'turn-1', content: JSON.stringify({schemaVersion: 'fishyume.team/v1', status: 'completed', contentMarkdown: 'Use the smaller design.', warnings: [], openQuestions: []}), createdAt: now, contentHash: 'b'.repeat(64)}], nextAfterSequence: 1, more: false} as T;
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(_listener: EventListener): () => void {return () => undefined}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
}

const now = '2026-08-25T00:00:00Z';
function team(state: 'running' | 'closed') {return {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', clientRequestId: 'request-1', requestHash: 'c'.repeat(64), project: 'C:/project', mode: 'panel', topic: 'Compare designs', catalogHash: 'a'.repeat(64), participants: [{participantId: 'participant-1', label: 'architect', role: 'propose', modelId: 'codex/local/gpt-5.6', driver: 'codex', target: 'local', state: 'responded'}], state, stateVersion: state === 'closed' ? 4 : 2, costGrant: 1000, costUsed: 100, ...(state === 'closed' ? {closeReason: 'panel_settled'} : {}), createdAt: now, updatedAt: now}}

test('team start waits for the Panel and renders independently identified contributions', async () => {
  const client = new TeamClient(); let output = '';
  const code = await startTeam(client, {schemaVersion: 'fishyume.team/v1', clientRequestId: 'request-1', project: 'C:/project', mode: 'panel', topic: 'Compare designs'}, {pollMs: 1}, {write(text) {output += text}});
  assert.equal(code, 0); assert.equal(client.closed, true);
  assert.deepEqual(client.calls.map(call => call.method), ['team.start', 'team.get', 'team.messages']);
  assert.match(output, /architect  codex\/local\/gpt-5.6  completed/);
  assert.match(output, /Use the smaller design/);
});

test('team start detach returns the durable identity without observing', async () => {
  const client = new TeamClient(); let output = '';
  assert.equal(await startTeam(client, {schemaVersion: 'fishyume.team/v1', clientRequestId: 'request-1', project: 'C:/project', mode: 'panel', topic: 'Compare'}, {detach: true, json: true}, {write(text) {output += text}}), 0);
  assert.deepEqual(client.calls.map(call => call.method), ['team.start']);
  assert.equal(JSON.parse(output).team.teamId, 'team-1');
});

test('explicit participants use the frozen modelId:label spelling', () => {
  assert.deepEqual(parseParticipant('codex/local/gpt-5.6:architect'), {modelId: 'codex/local/gpt-5.6', label: 'architect', role: 'propose a coherent architecture and tradeoffs'});
  assert.throws(() => parseParticipant('missing-label'), /modelId:label/);
});
