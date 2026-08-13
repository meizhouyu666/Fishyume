import test from 'node:test';
import assert from 'node:assert/strict';
import { parseArgs as parsePreflight, selectSteps } from './preflight.mjs';
import { commandFor, parseArgs as parseStress, STRESS_PACKAGES } from './stress.mjs';

test('preflight validates and selects one named primitive', () => {
  assert.deepEqual(parsePreflight(['--step', 'go-vet', '--dry-run']), { step: 'go-vet', dryRun: true });
  assert.deepEqual(selectSteps('go-vet').map((step) => step.name), ['go-vet']);
  assert.throws(() => parsePreflight(['--step', 'missing']), /unknown step/);
});

test('stress defaults to deterministic 20x gate and fixed high-risk packages', () => {
  const options = parseStress(['--dry-run']);
  assert.deepEqual(options, { count: 20, timeout: '10m', dryRun: true });
  assert.deepEqual(commandFor(options), { command: 'go', args: ['test', '-count=20', '-timeout', '10m', ...STRESS_PACKAGES] });
});

test('stress rejects unsafe repetition and timeout values', () => {
  assert.throws(() => parseStress(['--count', '0']), /positive integer/);
  assert.throws(() => parseStress(['--count', '1.5']), /positive integer/);
  assert.throws(() => parseStress(['--timeout', 'forever']), /Go duration/);
});
