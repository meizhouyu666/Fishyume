import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello} from '../bridge/types.js';
import type {HandoffArtifact, TeamMessage} from '../bridge/team.js';
import {bindTeamHandoff, createTeamHandoff, listTeamHandoffs, renderHandoff, showTeamHandoff} from './team-handoff.js';

const now = '2026-08-25T00:00:00Z';
const handoff: HandoffArtifact = {
  schemaVersion: 'fishyume.team/v1', handoffId: 'handoff-1', teamId: 'team-1', sourceTeamVersion: 4,
  goal: 'Implement the accepted design', decisions: ['Use the smaller design'], constraints: ['Keep Run contracts unchanged'],
  openQuestions: ['Which rollout window?'], acceptanceExpectations: ['All gates pass'], selectedMessageIds: ['message-1', 'message-2'],
  sourceMessageHashes: ['a'.repeat(64), 'b'.repeat(64)], contentHash: 'c'.repeat(64), createdAt: now,
};

class HandoffClient implements EngineClient {
  calls: Array<{method: string; params?: unknown}> = [];
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'team.get') return {schemaVersion: 'fishyume.team/v1', team: {stateVersion: 4}, turns: []} as T;
    if (method === 'team.messages') {
      const after = (params as {afterSequence?: number}).afterSequence ?? 0;
      const messages = after === 0 ? [message('message-host', 1, 'host_message')] : [message('message-1', 2, 'participant_contribution'), message('message-2', 3, 'participant_contribution')];
      return {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', messages, nextAfterSequence: after === 0 ? 1 : 3, more: after === 0} as T;
    }
    if (method === 'team.handoff.create') return {schemaVersion: 'fishyume.team/v1', handoff, replayed: false} as T;
    if (method === 'team.handoff.list') return {schemaVersion: 'fishyume.team/v1', items: [handoff]} as T;
    if (method === 'team.handoff.get') return {schemaVersion: 'fishyume.team/v1', handoff, binding: {teamId: 'team-1', handoffId: 'handoff-1', runId: 'run-1', project: 'C:/project', boundAt: now}} as T;
    if (method === 'team.handoff.bindRun') return {schemaVersion: 'fishyume.team/v1', binding: {teamId: 'team-1', handoffId: 'handoff-1', runId: 'run-1', project: 'C:/project', boundAt: now}, replayed: false} as T;
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(_listener: EventListener): () => void {return () => undefined}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {}
}

function message(messageId: string, sequence: number, kind: TeamMessage['kind']): TeamMessage {
  return {schemaVersion: 'fishyume.team/v1', messageId, teamId: 'team-1', sequence, kind, actor: kind === 'host_message' ? 'host' : 'participant-1', content: 'content', createdAt: now, contentHash: 'a'.repeat(64)};
}

test('Handoff create pages retained messages and defaults to participant contributions', async () => {
  const client = new HandoffClient(); let output = '';
  await createTeamHandoff(client, 'team-1', {handoffId: 'handoff-1', goal: handoff.goal}, {write(text) {output += text}});
  assert.deepEqual(client.calls.map(call => call.method), ['team.get', 'team.messages', 'team.messages', 'team.handoff.create']);
  const request = client.calls.at(-1)?.params as {expectedStateVersion: number; selectedMessageIds: string[]};
  assert.equal(request.expectedStateVersion, 4);
  assert.deepEqual(request.selectedMessageIds, ['message-1', 'message-2']);
  assert.match(output, /Handoff handoff-1 created from 2 messages/);
});

test('Handoff list, show, and bind use the public Team methods and explicit promotion sequence', async () => {
  const client = new HandoffClient(); let output = '';
  const writer = {write(text: string) {output += text}};
  await listTeamHandoffs(client, 'team-1', {limit: 10}, writer);
  await showTeamHandoff(client, 'team-1', 'handoff-1', false, writer);
  await bindTeamHandoff(client, 'team-1', 'handoff-1', 'run-1', {actionId: 'bind-1'}, writer);
  assert.deepEqual(client.calls.map(call => call.method), ['team.handoff.list', 'team.handoff.get', 'team.get', 'team.handoff.bindRun']);
  assert.match(output, /Host promotion sequence:/);
  assert.match(output, /workflow\.validate/);
  assert.match(output, /workflow\.explain/);
  assert.match(output, /User confirms, then run\.start/);
  const bind = client.calls.at(-1)?.params as {actionId: string; expectedStateVersion: number};
  assert.deepEqual(bind, {schemaVersion: 'fishyume.team/v1', actionId: 'bind-1', teamId: 'team-1', handoffId: 'handoff-1', runId: 'run-1', expectedStateVersion: 4});
});

test('Handoff rendering includes immutable source identity and binding', () => {
  const rendered = renderHandoff(handoff, {runId: 'run-1', project: 'C:/project', boundAt: now});
  assert.match(rendered, /Source Team: team-1 @ state 4/);
  assert.match(rendered, new RegExp(`Content hash: ${'c'.repeat(64)}`));
  assert.match(rendered, /Bound Run: run-1/);
});
