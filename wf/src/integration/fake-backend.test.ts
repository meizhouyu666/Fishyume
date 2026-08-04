import assert from 'node:assert/strict';
import {mkdtemp, readFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import test from 'node:test';
import {EngineBridge} from '../bridge/engine.js';
import type {RunSnapshot} from '../bridge/types.js';

test('CLI bridge drives the Go engine through a completed fake TaskBinding and persists the run', {timeout: 30_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'wf-e2e-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const ctlPath = join(temporary, process.platform === 'win32' ? 'cc-panes-ctl.exe' : 'cc-panes-ctl');
  for (const [output, target] of [[enginePath, './cmd/wf-engine'], [ctlPath, './internal/backend/ccpanes/testdata/fake-cc-panes-ctl']] as const) {
    const built = spawnSync('go', ['build', '-o', output, target], {cwd: engineRoot, encoding: 'utf8'});
    assert.equal(built.status, 0, built.stderr);
  }

  const stateDir = join(temporary, 'state');
  const previous = {ctl: process.env.WF_CCPANES_CTL, state: process.env.WF_STATE_DIR, project: process.env.WF_FAKE_PROJECT};
  process.env.WF_CCPANES_CTL = ctlPath;
  process.env.WF_STATE_DIR = stateDir;
  process.env.WF_FAKE_PROJECT = projectRoot;
  const bridge = new EngineBridge(enginePath);
  if (previous.ctl === undefined) delete process.env.WF_CCPANES_CTL; else process.env.WF_CCPANES_CTL = previous.ctl;
  if (previous.state === undefined) delete process.env.WF_STATE_DIR; else process.env.WF_STATE_DIR = previous.state;
  if (previous.project === undefined) delete process.env.WF_FAKE_PROJECT; else process.env.WF_FAKE_PROJECT = previous.project;

  const statuses: string[] = [];
  let resolveTerminal!: () => void;
  const terminal = new Promise<void>(resolve => { resolveTerminal = resolve; });
  bridge.onRunEvent(event => {
    statuses.push(event.status);
    if (event.status === 'succeeded') resolveTerminal();
  });
  const hello = await bridge.hello(projectRoot);
  assert.equal(hello.backendReady, true);
  assert.equal(hello.projectReady, true);
  const started = await bridge.call<{runId: string}>('run.start', {project: projectRoot, tool: 'codex', runtime: 'local', task: 'fixture task'});
  assert.match(started.runId, /^run-/);
  await Promise.race([terminal, new Promise((_, reject) => setTimeout(() => reject(new Error('terminal event timeout')), 10_000))]);
  const snapshot = await bridge.call<RunSnapshot>('run.get', {runId: started.runId});
  assert.equal(snapshot.status, 'succeeded');
  assert.deepEqual(statuses.filter(status => ['dispatching', 'running', 'succeeded'].includes(status)), ['dispatching', 'running', 'succeeded']);
  const persistedSnapshot = JSON.parse(await readFile(join(snapshot.stateDir, 'run.json'), 'utf8')) as RunSnapshot;
  assert.equal(persistedSnapshot.status, 'succeeded');
  const events = (await readFile(join(snapshot.stateDir, 'events.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
  assert.ok(events.length >= 4);
  assert.match(await readFile(join(snapshot.stateDir, 'nodes', 'agent-1', 'output.log'), 'utf8'), /fixture agent output/);
  await bridge.close();
});
