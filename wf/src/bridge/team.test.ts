import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import {teamApiVersion, teamMethods} from './team.js';

test('Team contract freeze matches the TypeScript product surface', async () => {
  const raw = await readFile(new URL('../../../contracts/fishyume-team-v1.json', import.meta.url), 'utf8');
  const freeze = JSON.parse(raw) as {schemaVersion: string; status: string; apiVersion: string; methods: string[]};
  assert.equal(freeze.schemaVersion, 'fishyume.team-contract-freeze/v1');
  assert.equal(freeze.status, 'frozen');
  assert.equal(freeze.apiVersion, teamApiVersion);
  assert.deepEqual(freeze.methods, [...teamMethods]);
});
