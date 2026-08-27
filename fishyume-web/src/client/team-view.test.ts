import assert from 'node:assert/strict';
import test from 'node:test';
import type {TeamMessage} from '../../../wf/src/bridge/team.js';
import {messageContent} from './team-view.js';

const base: TeamMessage = {schemaVersion: 'fishyume.team/v1', messageId: 'message-1', teamId: 'team-1', sequence: 1, kind: 'participant_contribution', actor: 'participant-1', turnId: 'turn-1', content: '', createdAt: '2026-08-27T00:00:00Z', contentHash: 'a'.repeat(64)};

test('Web team message formatting preserves legacy Markdown', () => {
  assert.equal(messageContent({...base, content: JSON.stringify({schemaVersion: 'fishyume.team/v1', status: 'completed', contentMarkdown: 'legacy answer'})}), 'legacy answer');
});

test('Web team message formatting renders structured output', () => {
  assert.equal(messageContent({...base, content: JSON.stringify({schemaVersion: 'fishyume.team/v1', status: 'completed', resultType: 'decision', output: {decision: 'adopt-a', confidence: 0.9}})}), '[decision]\n{\n  "decision": "adopt-a",\n  "confidence": 0.9\n}');
});

test('Web team message formatting safely falls back for malformed payloads', () => {
  assert.equal(messageContent({...base, content: '{malformed'}), '{malformed');
});
