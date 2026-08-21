import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import {applicationApiVersion, applicationMethods} from './application.js';

interface CoreFreeze {
  schemaVersion: string;
  status: string;
  application: {apiVersion: string; methods: string[]};
  workflow: {writeVersion: string; readVersions: string[]};
  state: {runProtocolVersion: number; currentSchemaVersion: number; historicalReadMinimum: number};
}

test('core contract freeze matches the TypeScript product surface', async () => {
  const raw = await readFile(new URL('../../../contracts/fishyume-core-v1.json', import.meta.url), 'utf8');
  const freeze = JSON.parse(raw) as CoreFreeze;
  assert.equal(freeze.schemaVersion, 'fishyume.core-contract-freeze/v1');
  assert.equal(freeze.status, 'frozen');
  assert.equal(freeze.application.apiVersion, applicationApiVersion);
  assert.deepEqual(freeze.application.methods, [...applicationMethods]);
  assert.equal(freeze.workflow.writeVersion, 'fishyume/v2');
  assert.deepEqual(freeze.workflow.readVersions, ['fishyume/v1', 'fishyume/v2']);
  assert.deepEqual(freeze.state, {runProtocolVersion: 2, currentSchemaVersion: 3, historicalReadMinimum: 1});
});
