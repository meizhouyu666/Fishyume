#!/usr/bin/env node
import {spawn} from 'node:child_process';
import {mkdtemp, readFile, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const wfRoot = join(repoRoot, 'wf');
const engineRoot = join(repoRoot, 'wf-engine');

function run(command, args, options = {}) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command, args, {stdio: 'inherit', shell: false, ...options});
    child.once('error', rejectRun);
    child.once('exit', (code, signal) => code === 0 ? resolveRun() : rejectRun(new Error(`${command} failed (${signal ? `signal ${signal}` : `exit ${code}`})`)));
  });
}

async function stopControlPlane(stateDir) {
  let pid;
  try {pid = JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')).pid} catch {return}
  if (!Number.isInteger(pid) || pid < 1) return;
  try {process.kill(pid, 'SIGTERM')} catch (error) {
    if (error?.code === 'ESRCH') return;
    throw error;
  }
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {process.kill(pid, 0)} catch (error) {
      if (error?.code === 'ESRCH') return;
      throw error;
    }
    await new Promise(resolveWait => setTimeout(resolveWait, 25));
  }
  throw new Error(`temporary Control Plane ${pid} did not exit`);
}

async function cancelRun(bridge, runId) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const current = await bridge.call('run.get', {runId});
    if (current.run.phase === 'completed') return;
    try {
      await bridge.call('run.action', {
        actionId: `codex-live-cleanup-${current.run.stateVersion}`,
        runId,
        type: 'cancel',
        expectedStateVersion: current.run.stateVersion,
      });
      return;
    } catch (error) {
      if (error?.data?.code !== 'conflict') throw error;
    }
  }
  throw new Error(`live Run ${runId} cleanup cancellation did not converge`);
}

async function waitFor(bridge, runId, predicate, label) {
  const deadline = Date.now() + 180000;
  let latest;
  while (Date.now() < deadline) {
    const view = await bridge.call('run.get', {runId});
    latest = view.run;
    if (predicate(view.run)) return view.run;
    await new Promise(resolveWait => setTimeout(resolveWait, 1000));
  }
  throw new Error(`${label} timed out: ${JSON.stringify(latest)}`);
}

async function main() {
  if (process.env.FISHYUME_LIVE_CODEX !== '1') {
    throw new Error('real Codex smoke is opt-in; set FISHYUME_LIVE_CODEX=1 after authenticating codex-cli');
  }
  const temporary = await mkdtemp(join(tmpdir(), 'fishyume-codex-live-'));
  const stateDir = join(temporary, 'state');
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const environment = {
    ...process.env,
    FISHYUME_ENGINE_PATH: enginePath,
    FISHYUME_STATE_DIR: stateDir,
    WF_STATE_DIR: stateDir,
    FISHYUME_DIRECT_SANDBOX: 'read-only',
  };
  let bridge;
  let runId;
  let primaryError;
  try {
    await run('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: engineRoot, env: environment});
    const {EngineBridge} = await import(pathToFileURL(join(wfRoot, 'dist', 'bridge', 'engine.js')).href);
    const previous = new Map();
    for (const [key, value] of Object.entries(environment)) {
      previous.set(key, process.env[key]);
      if (value !== undefined) process.env[key] = value;
    }
    try {bridge = new EngineBridge()} finally {
      for (const [key, value] of previous) value === undefined ? delete process.env[key] : process.env[key] = value;
    }
    const project = resolve(process.env.FISHYUME_LIVE_PROJECT || repoRoot);
    const capabilities = await bridge.call('system.capabilities', {project});
    const codex = capabilities.drivers.find(driver => driver.driver === 'codex' && driver.targets.includes('local'));
    if (!codex?.ready) throw new Error(`Codex Driver is not ready: ${codex?.diagnostic || 'capability unavailable'}`);
    const parallel = process.argv.includes('--parallel') || process.env.FISHYUME_LIVE_PARALLEL === '1';
    const workflow = parallel ? {document: {
      apiVersion: 'fishyume/v1', name: 'codex-live-parallel-smoke', defaults: {agent: {driver: 'codex', target: 'local'}}, execution: {maxConcurrency: 2},
      nodes: {
        planA: {type: 'agent', task: 'Return exactly the short planning summary codex-live-plan-a. Do not inspect, modify, or create files.'},
        planB: {type: 'agent', task: 'Return exactly the short planning summary codex-live-plan-b. Do not inspect, modify, or create files.'},
        approve: {type: 'approval', dependsOn: ['planA', 'planB'], prompt: 'Approve the two real Codex planning results?'},
        finalize: {type: 'agent', dependsOn: ['approve'], task: 'Return exactly the final summary codex-live-parallel-final. Do not inspect, modify, or create files.'},
      },
    }} : {document: {
      apiVersion: 'fishyume/v1', name: 'codex-live-smoke', defaults: {agent: {driver: 'codex', target: 'local'}}, execution: {maxConcurrency: 1},
      nodes: {agent: {type: 'agent', task: 'Return a short completion summary. Do not inspect, modify, or create files. State succeeded with summary codex-engine-live-smoke.'}},
    }};
    const started = await bridge.call('run.start', {project, workflow, clientRequestId: `codex-live-${parallel ? 'parallel-' : ''}${Date.now()}`});
    runId = started.runId;
    if (parallel) {
      const waiting = await waitFor(bridge, runId, run => run.phase === 'waiting' && run.nodes.some(node => node.nodeId === 'approve' && node.phase === 'waiting'), 'parallel Approval');
      await bridge.call('run.action', {actionId: 'codex-live-parallel-approve-1', runId, type: 'approve', expectedStateVersion: waiting.stateVersion, nodeId: 'approve'});
    }
    const deadline = Date.now() + 180000;
    let result;
    while (Date.now() < deadline) {
      try {result = await bridge.call('run.result', {runId}); break}
      catch (error) {
        if (error?.data?.code !== 'not_ready') throw error;
      }
      await new Promise(resolveWait => setTimeout(resolveWait, 1000));
    }
    if (!result) throw new Error(`Codex live Run ${runId} timed out`);
    const expectedNodes = parallel ? ['planA', 'planB', 'approve', 'finalize'] : ['agent'];
    const nodeIds = result.results.map(item => item.nodeId).sort();
    const expectedNodeIds = [...expectedNodes].sort();
    const expectedSummary = parallel ? 'codex-live-parallel-final' : 'codex-engine-live-smoke';
    const finalNode = result.results.find(item => item.nodeId === (parallel ? 'finalize' : 'agent'));
    if (result.conclusion !== 'succeeded' || JSON.stringify(nodeIds) !== JSON.stringify(expectedNodeIds) || finalNode?.result?.summary !== expectedSummary) {
      throw new Error(`unexpected Codex live result: ${JSON.stringify(result)}`);
    }
    console.log(JSON.stringify({ok: true, runId, mode: parallel ? 'parallel-approval' : 'single', conclusion: result.conclusion, summary: finalNode.result.summary, nodes: nodeIds, sandbox: 'read-only'}));
  } catch (error) {
    primaryError = error;
  } finally {
    const cleanupErrors = [];
    if (runId && bridge) {
      try {await cancelRun(bridge, runId)} catch (error) {cleanupErrors.push(error)}
    }
    try {await bridge?.close()} catch (error) {cleanupErrors.push(error)}
    try {await stopControlPlane(stateDir)} catch (error) {cleanupErrors.push(error)}
    try {
      await rm(temporary, {recursive: true, force: true, maxRetries: 5, retryDelay: 100});
      await assertRemoved(temporary);
    } catch (error) {cleanupErrors.push(error)}
    if (primaryError && cleanupErrors.length) throw new AggregateError([primaryError, ...cleanupErrors], 'Codex live smoke and cleanup failed');
    if (primaryError) throw primaryError;
    if (cleanupErrors.length) throw new AggregateError(cleanupErrors, 'Codex live smoke cleanup failed');
  }
}

async function assertRemoved(path) {
  try {
    await stat(path);
    throw new Error(`temporary Codex live directory remains: ${path}`);
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
  }
}

main().catch(error => {
  console.error(`Codex live smoke failed: ${error.message}`);
  process.exitCode = 1;
});
