import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import test from 'node:test';

function invoke(...args: string[]) {
  return spawnSync(process.execPath, ['--import', 'tsx', 'src/cli.tsx', ...args], {encoding: 'utf8'});
}

test('Fishyume exposes help and version', () => {
  const help = invoke('--help');
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /Fishyume/);
  assert.match(help.stdout, /fishyume <command>/);

  const version = invoke('--version');
  assert.equal(version.status, 0, version.stderr);
  assert.equal(version.stdout.trim(), '0.2.1-alpha.1');
});

test('command help remains available', () => {
  for (const command of ['run', 'status', 'resume', 'cancel', 'doctor']) {
    const result = invoke(command, '--help');
    assert.equal(result.status, 0, `${command}: ${result.stderr}`);
    assert.match(result.stdout, new RegExp(`fishyume ${command}`));
  }
});

test('run and doctor expose Backend selection', () => {
  for (const command of ['run', 'doctor']) {
    const result = invoke(command, '--help');
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /--backend/);
  }
});

test('status exposes watch and rejects incompatible or non-interactive use before starting the Engine', () => {
  const help = invoke('status', '--help'); assert.equal(help.status, 0, help.stderr); assert.match(help.stdout, /--watch/);
  const json = invoke('status', 'run-1', '--watch', '--json'); assert.equal(json.status, 6); assert.match(json.stderr, /cannot be combined with --json/);
  const nonTTY = invoke('status', 'run-1', '--watch'); assert.equal(nonTTY.status, 6); assert.match(nonTTY.stderr, /use plain fishyume status/);
});
