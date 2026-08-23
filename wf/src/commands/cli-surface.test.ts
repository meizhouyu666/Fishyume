import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtempSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
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
  for (const command of ['dashboard', 'demo', 'run', 'status', 'resume', 'cancel', 'doctor', 'attach', 'machine', 'mcp']) {
    const result = invoke(command, '--help');
    assert.equal(result.status, 0, `${command}: ${result.stderr}`);
    assert.match(result.stdout, new RegExp(`fishyume ${command}`));
  }
  for (const command of ['create', 'get', 'list', 'supersede', 'delete']) {
    const result = invoke('memory', command, '--help');
    assert.equal(result.status, 0, `memory ${command}: ${result.stderr}`);
    assert.match(result.stdout, new RegExp(`fishyume memory ${command}`));
  }
  const createMemory = invoke('memory', 'create', '--help');
  assert.match(createMemory.stdout, /--stdin/);
  assert.match(createMemory.stdout, /--file/);
  assert.doesNotMatch(createMemory.stdout, /--content/);
  const setup = invoke('setup', 'codex', '--help');
  assert.equal(setup.status, 0, setup.stderr);
  assert.match(setup.stdout, /fishyume setup/);
  assert.match(setup.stdout, /--print/);
  assert.match(setup.stdout, /--force/);
  const compatibleSetupPrint = invoke('setup', 'codex', '--print');
  assert.equal(compatibleSetupPrint.status, 0, compatibleSetupPrint.stderr);
  assert.match(compatibleSetupPrint.stdout, /^codex mcp add fishyume --/);
  const productSetup = invoke('setup', '--help');
  assert.equal(productSetup.status, 0, productSetup.stderr);
  assert.match(productSetup.stdout, /fishyume setup/);
  const demo = invoke('demo', '--help');
  assert.equal(demo.status, 0, demo.stderr);
  assert.match(demo.stdout, /--width/);
  const exampleShow = invoke('examples', 'show', '--help');
  assert.equal(exampleShow.status, 0, exampleShow.stderr);
  assert.match(exampleShow.stdout, /fishyume examples show/);
  const exampleList = invoke('examples', 'list', '--help');
  assert.equal(exampleList.status, 0, exampleList.stderr);
  assert.match(exampleList.stdout, /fishyume examples list/);
});

test('memory file content rejects oversized and invalid UTF-8 input before starting the Engine', () => {
  const directory = mkdtempSync(join(tmpdir(), 'fishyume-memory-cli-'));
  try {
    const common = ['memory', 'create', '--project', directory, '--mutation-id', 'bounded-file', '--type', 'fact', '--reason', 'bounded CLI test', '--file'];
    const oversized = join(directory, 'oversized.txt');
    writeFileSync(oversized, Buffer.alloc(16 * 1024 + 1, 0x61));
    const oversizedResult = invoke(...common, oversized);
    assert.equal(oversizedResult.status, 6, oversizedResult.stderr);
    assert.equal(JSON.parse(oversizedResult.stdout).error.code, 'invalid_argument');
    assert.match(JSON.parse(oversizedResult.stdout).error.message, /no larger than 16 KiB/);

    const invalid = join(directory, 'invalid.txt');
    writeFileSync(invalid, Buffer.from([0xff, 0xfe]));
    const invalidResult = invoke(...common, invalid);
    assert.equal(invalidResult.status, 6, invalidResult.stderr);
    assert.equal(JSON.parse(invalidResult.stdout).error.code, 'invalid_argument');
    assert.match(JSON.parse(invalidResult.stdout).error.message, /valid UTF-8/);
  } finally {
    rmSync(directory, {recursive: true, force: true});
  }
});

test('command help describes the Agent-facing control-plane surface', () => {
  const expected = new Map([
    ['run', /Start an ad-hoc task or Workflow Run through the local Control Plane/],
    ['dashboard', /Open the Fishyume Operator Dashboard/],
    ['doctor', /Check the Engine, Application protocol, Driver/],
    ['status', /Read a durable Run snapshot or watch it in the human TUI/],
    ['attach', /Attach the human TUI to an existing durable Run/],
    ['machine', /Call one Agent-facing Application API method/],
    ['mcp', /Serve the Agent-facing Application API as MCP tools over stdio/],
    ['setup codex', /Connect Fishyume to Codex as a local stdio MCP server/],
    ['setup', /Set up Fishyume for the local Codex Host/],
    ['demo', /Preview the Chinese topology console/],
    ['examples list', /List bundled product Workflow examples/],
    ['examples show', /Print one bundled Workflow YAML/],
  ]);
  for (const [command, description] of expected) {
    const result = invoke(...command.split(' '), '--help');
    assert.equal(result.status, 0, `${command}: ${result.stderr}`);
    assert.match(result.stdout, description);
  }
});

test('run and doctor expose Driver selection with legacy compatibility flags', () => {
  for (const command of ['run', 'doctor']) {
    const result = invoke(command, '--help');
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /--driver/);
    assert.match(result.stdout, /--backend/);
    assert.match(result.stdout, /Deprecated compatibility alias for --driver/);
  }
});

test('status exposes watch and rejects incompatible or non-interactive use before starting the Engine', () => {
  const help = invoke('status', '--help'); assert.equal(help.status, 0, help.stderr); assert.match(help.stdout, /--watch/);
  const json = invoke('status', 'run-1', '--watch', '--json'); assert.equal(json.status, 6); assert.match(json.stderr, /cannot be combined with --json/);
  const nonTTY = invoke('status', 'run-1', '--watch'); assert.equal(nonTTY.status, 6); assert.match(nonTTY.stderr, /use plain fishyume status/);
});
