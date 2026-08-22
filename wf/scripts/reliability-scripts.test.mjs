import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseArgs as parsePreflight, selectSteps } from './preflight.mjs';
import { commandFor, commandsFor, parseArgs as parseStress, STRESS_PACKAGES } from './stress.mjs';

test('preflight validates and selects one named primitive', () => {
  assert.deepEqual(parsePreflight(['--step', 'go-vet', '--dry-run']), { step: 'go-vet', dryRun: true });
  assert.deepEqual(selectSteps('go-vet').map((step) => step.name), ['go-vet']);
  assert.throws(() => parsePreflight(['--step', 'missing']), /unknown step/);
});

test('stress defaults to deterministic 20x gate and fixed high-risk packages', () => {
  const options = parseStress(['--dry-run']);
  assert.deepEqual(options, { count: 20, timeout: '10m', dryRun: true });
  assert.deepEqual(commandFor(options), { command: 'go', args: ['test', '-count=20', '-timeout', '10m', ...STRESS_PACKAGES] });
  assert.deepEqual(commandsFor(options), STRESS_PACKAGES.map((packagePath) => ({ command: 'go', args: ['test', '-count=20', '-timeout', '10m', packagePath] })));
});

test('stress rejects unsafe repetition and timeout values', () => {
  assert.throws(() => parseStress(['--count', '0']), /positive integer/);
  assert.throws(() => parseStress(['--count', '1.5']), /positive integer/);
  assert.throws(() => parseStress(['--timeout', 'forever']), /Go duration/);
});

test('packed install gate retains the durable rollback rehearsal', () => {
  const source = readFileSync(new URL('./install-smoke.mjs', import.meta.url), 'utf8');
  assert.match(source, /name: 'install-rollback-drill'/);
  assert.match(source, /cpSync\(stateRoot, rollbackSnapshot/);
  assert.match(source, /assertSameEvidence\(runEvidence\(started\.runId\), waitingEvidence, 'rollback restore'\)/);
  assert.match(source, /actionId: 'install-upgrade-approve-1'/);
  assert.match(source, /actionId: 'install-rollback-approve-1'/);
  assert.match(source, /restoredTerminal\.stateVersion !== upgradedTerminal\.stateVersion/);
});

test('historical downgrade gate pins package identity and restores matching state', () => {
  const source = readFileSync(new URL('./downgrade-smoke.mjs', import.meta.url), 'utf8');
  assert.match(source, /391dc2c3a788b7754b52d4234fbfc80c5d5a3dae/);
  assert.match(source, /git', \['archive'/);
  assert.match(source, /npm did not install the expected/);
  assert.match(source, /hash\(installed\.cliEvidence\) !== expected\.cli/);
  assert.match(source, /cpSync\(rollbackSnapshot, stateRoot/);
  assert.match(source, /actionId, runId: waiting\.runId/);
  assert.match(source, /historical-downgrade-approve-1/);
});
