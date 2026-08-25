import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, mkdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {parseArgs, runCapabilityGate} from './codex-session-capability-gate.mjs';

const fixture = fileURLToPath(new URL('./fixtures/fake-codex-app-server.mjs', import.meta.url));

test('M7.3 capability gate proves restart continuity, identity rejection, and cancellation', async () => {
  const root = await mkdtemp(join(tmpdir(), 'fishyume-session-gate-test-'));
  const workspace = join(root, 'workspace');
  const statePath = join(root, 'fake-state.json');
  await mkdir(workspace);
  await writeFile(join(workspace, 'sentinel.txt'), 'Fishyume M7.3 session capability gate sentinel\n', 'utf8');
  try {
    const result = await runCapabilityGate({
      model: 'gpt-fake',
      timeoutMs: 5_000,
      workspace,
      invocation: {command: process.execPath, args: [fixture, statePath]},
    });
    assert.equal(result.ok, true);
    assert.deepEqual(result.capabilities, {
      supportsResume: true,
      supportsPark: true,
      supportsRecovery: true,
      supportsDirectedInput: true,
      supportsConfirmedCancel: true,
    });
    assert.equal(result.evidence.sameThreadIdentity, true);
    assert.equal(result.evidence.continuityMarkerMatched, true);
    assert.equal(result.evidence.wrongThreadRejected, true);
    assert.equal(result.evidence.staleTurnRejected, true);
    assert.equal(result.evidence.cancelledTurnStatus, 'interrupted');
  } finally {
    await rm(root, {recursive: true, force: true});
  }
});

test('M7.3 capability gate arguments remain explicit and bounded', () => {
  assert.deepEqual(parseArgs(['--model', 'gpt-test', '--timeout-ms', '5000']), {
    model: 'gpt-test', timeoutMs: 5_000,
  });
  assert.deepEqual(parseArgs(['--help']), {help: true});
  assert.throws(() => parseArgs(['--timeout-ms', '999']), /at least 1000/);
  assert.throws(() => parseArgs(['--unknown']), /unknown argument/);
});
