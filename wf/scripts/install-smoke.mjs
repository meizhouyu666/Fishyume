import {spawnSync} from 'node:child_process';
import {cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';

const npmCli = process.env.npm_execpath;
if (!npmCli) throw new Error('run this smoke through npm so npm_execpath is available');

const root = await mkdtemp(join(tmpdir(), 'fishyume-install-smoke-'));
const packs = join(root, 'packs');
const installRoot = join(root, 'install');
const stateRoot = join(root, 'state');
const rollbackSnapshot = join(root, 'rollback-snapshot');
const stagedPlatform = join(root, 'platform-package');
const platformDirectory = process.platform === 'win32' && process.arch === 'x64'
  ? join(process.cwd(), 'packages', 'fishyume-engine-win32-x64')
  : process.platform === 'linux' && process.arch === 'x64'
    ? join(process.cwd(), 'packages', 'fishyume-engine-linux-x64')
    : undefined;
if (!platformDirectory) throw new Error(`unsupported install-smoke platform ${process.platform}-${process.arch}`);
mkdirSync(packs, {recursive: true});

function npm(args, cwd = process.cwd()) {
  const result = spawnSync(process.execPath, [npmCli, ...args], {cwd, encoding: 'utf8', windowsHide: true});
  if (result.status !== 0) throw new Error(`npm ${args[0]} failed: ${result.stderr || result.stdout}`);
  return result.stdout;
}

function pack(directory) {
  const report = JSON.parse(npm(['pack', '--json', '--pack-destination', packs], directory));
  if (!Array.isArray(report) || report.length !== 1 || !report[0].filename) throw new Error(`unexpected npm pack report for ${directory}`);
  return join(packs, report[0].filename);
}

function stageCurrentEngine() {
  cpSync(platformDirectory, stagedPlatform, {recursive: true});
  const binary = join(stagedPlatform, 'bin', process.platform === 'win32' ? 'fishyume-engine.exe' : 'fishyume-engine');
  const build = spawnSync('go', ['build', '-trimpath', '-ldflags=-s -w', '-o', binary, './cmd/wf-engine'], {
    cwd: join(process.cwd(), '..', 'wf-engine'),
    encoding: 'utf8',
    windowsHide: true,
    env: {...process.env, CGO_ENABLED: '0'},
  });
  if (build.status !== 0) throw new Error(`current Engine build failed: ${build.stderr || build.stdout || build.error?.message}`);
}

function invoke(cli, args, allowed = [0]) {
  const smokeEnv = {...process.env, FISHYUME_STATE_DIR: stateRoot, WF_STATE_DIR: stateRoot};
  // The packed install gate must not inherit a developer's legacy route file;
  // that file is intentionally an opt-in compatibility import.
  delete smokeEnv.FISHYUME_AGENT_ROUTES_FILE;
  const result = spawnSync(process.execPath, [cli, ...args], {
    encoding: 'utf8',
    windowsHide: true,
    env: smokeEnv,
  });
  if (!allowed.includes(result.status)) throw new Error(`fishyume ${args.join(' ')} failed (${result.status}): ${result.stderr || result.stdout}`);
  return `${result.stdout ?? ''}${result.stderr ?? ''}`;
}

function applicationCall(cli, method, params) {
  const output = invoke(cli, ['machine', method, '--params', JSON.stringify(params)]);
  let response;
  try {response = JSON.parse(output)} catch {throw new Error(`fishyume ${method} returned invalid JSON: ${output}`)}
  if (response?.error) throw new Error(`fishyume ${method} failed: ${response.error.code}: ${response.error.message}`);
  return response;
}

function waitForRun(cli, runId, predicate, label) {
  const deadline = Date.now() + 15_000;
  let latest;
  while (Date.now() < deadline) {
    latest = applicationCall(cli, 'run.get', {runId}).run;
    if (predicate(latest)) return latest;
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100);
  }
  throw new Error(`${label} did not converge: ${JSON.stringify(latest)}`);
}

function runEvidence(runId) {
  const runDirectory = join(stateRoot, 'runs', runId);
  return {
    run: readFileSync(join(runDirectory, 'run.json')),
    events: readFileSync(join(runDirectory, 'events.jsonl')),
  };
}

function assertSameEvidence(actual, expected, label) {
  if (!actual.run.equals(expected.run) || !actual.events.equals(expected.events)) {
    throw new Error(`${label} changed the durable Run snapshot or event log`);
  }
}

function assertWaitingApproval(run, expected) {
  const approval = run.nodes?.find(node => node.nodeId === 'approve');
  if (run.runId !== expected.runId || run.phase !== 'waiting' || approval?.phase !== 'waiting' || approval?.type !== 'approval') {
    throw new Error(`rollback drill did not restore the waiting Approval: ${JSON.stringify(run)}`);
  }
  if (run.stateVersion !== expected.stateVersion) throw new Error(`rollback drill stateVersion changed from ${expected.stateVersion} to ${run.stateVersion}`);
}

function stopControlPlane() {
  const metadataPath = join(stateRoot, 'control-plane.json');
  if (!existsSync(metadataPath)) return;
  const owner = JSON.parse(readFileSync(metadataPath, 'utf8'));
  if (resolve(String(owner.stateDir)) !== resolve(stateRoot) || !Number.isInteger(owner.pid) || owner.pid <= 0) throw new Error('refusing to stop an unverified Control Plane owner');
  try {process.kill(owner.pid, 'SIGTERM')} catch (error) {
    if (error?.code !== 'ESRCH') throw error;
  }
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) {
    try {process.kill(owner.pid, 0)} catch (error) {
      if (error?.code === 'ESRCH') return;
      throw error;
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 25);
  }
  // Windows does not reliably deliver SIGTERM to a detached Go process.
  // The owner record was validated above, so force only this verified process
  // tree and wait briefly for filesystem handles to be released.
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(owner.pid), '/T', '/F'], {encoding: 'utf8', windowsHide: true});
    const forcedDeadline = Date.now() + 3000;
    while (Date.now() < forcedDeadline) {
      try {process.kill(owner.pid, 0)} catch (error) {
        if (error?.code === 'ESRCH') return;
        throw error;
      }
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 25);
    }
  }
  throw new Error(`temporary Fishyume Control Plane ${owner.pid} did not exit after termination`);
}

try {
  stageCurrentEngine();
  const cliTarball = pack(process.cwd());
  const engineTarball = pack(stagedPlatform);
  npm(['install', '--prefix', installRoot, cliTarball, engineTarball, '--ignore-scripts', '--no-audit', '--no-fund']);
  const cli = join(installRoot, 'node_modules', 'fishyume', 'dist', 'cli.js');
  if (!existsSync(cli)) throw new Error(`installed CLI is missing: ${cli}`);

  const help = invoke(cli, ['--help']);
  if (!/Fishyume/.test(help) || !/dashboard/.test(help) || !/demo/.test(help) || !/examples/.test(help) || !/setup/.test(help)) throw new Error('installed top-level help is incomplete');
  if (!/Operator Dashboard/.test(invoke(cli, ['dashboard', '--help']))) throw new Error('installed Dashboard help is incomplete');
  const setup = invoke(cli, ['setup', '--print']);
  if (!setup.includes('codex mcp add fishyume --') || !setup.includes(process.execPath) || !setup.includes(cli) || !setup.trim().endsWith('"mcp"')) throw new Error('installed Codex setup command is not canonical and copyable');
  const compatibleSetup = invoke(cli, ['setup', 'codex', '--print']);
  if (compatibleSetup !== setup) throw new Error('historical setup codex alias does not match product setup');
  const routing = JSON.parse(invoke(cli, ['machine', 'routing.catalog', '--params', '{}']));
  if (routing.apiVersion !== 'fishyume.application/v1' || routing.dynamicAvailability !== true || !routing.catalogHash || !Array.isArray(routing.catalog?.models) || routing.catalog.models.length < 1) throw new Error('installed routing catalog Application surface is incomplete');
  const demo = invoke(cli, ['demo', '--width', '80', '--ascii']);
  if (!/阶段 2 · 并行 2/.test(demo) || !/依赖 plan/.test(demo) || !/需要人工审批/.test(demo)) throw new Error(`installed offline topology demo is incomplete: ${demo}`);
  const exampleList = invoke(cli, ['examples', 'list']);
  if (!/repository-hardening/.test(exampleList) || !/fishyume examples show <name>/.test(exampleList)) throw new Error(`installed example catalog is incomplete: ${exampleList}`);
  const example = invoke(cli, ['examples', 'show', 'repository-hardening']);
  const documentedExample = readFileSync(join(process.cwd(), '..', 'docs', 'examples', 'repository-hardening.yaml'), 'utf8');
  if (example !== documentedExample) throw new Error('installed repository-hardening example differs from the documented Workflow');
  const dashboard = invoke(cli, []);
  if (!/目前没有可显示的任务。/.test(dashboard) || !/检查运行环境：fishyume doctor/.test(dashboard)) throw new Error(`installed zero-argument Dashboard empty state is incomplete: ${dashboard}`);
  const doctor = invoke(cli, ['doctor'], [0, 1]);
  if (!/ok engine 0\.2\.1-alpha\.1 started/.test(doctor) || !/ok protocol 2 compatible/.test(doctor) || !/(?:ok|fail) codex-mcp/.test(doctor)) throw new Error(`installed Doctor output is incomplete: ${doctor}`);

  const rollbackWorkflow = {
    apiVersion: 'fishyume/v2',
    name: 'install-rollback-drill',
    defaults: {agent: {driver: 'codex', target: 'local'}},
    execution: {maxConcurrency: 1},
    nodes: {approve: {type: 'approval', prompt: 'Approve the rollback drill?'}},
  };
  const startRequest = {project: process.cwd(), workflow: {document: rollbackWorkflow}, clientRequestId: 'install-rollback-drill-1'};
  const started = applicationCall(cli, 'run.start', startRequest);
  const waiting = waitForRun(cli, started.runId, run => run.phase === 'waiting', 'pre-upgrade Approval');
  const waitingEvents = applicationCall(cli, 'run.events', {runId: started.runId, afterSequence: 0, limit: 100});
  if (waitingEvents.events.length < 1 || waitingEvents.nextAfterSequence < 1) throw new Error('rollback drill has no durable waiting events');

  // Snapshot only after the Control Plane is idle. The external state must
  // survive package replacement and remain usable after an explicit restore.
  stopControlPlane();
  const upgradeMarker = join(stateRoot, 'upgrade-marker.txt');
  mkdirSync(stateRoot, {recursive: true});
  const marker = 'state-survives-package-upgrade';
  writeFileSync(upgradeMarker, marker, 'utf8');
  const waitingEvidence = runEvidence(started.runId);
  cpSync(stateRoot, rollbackSnapshot, {recursive: true});

  npm(['install', '--prefix', installRoot, cliTarball, engineTarball, '--ignore-scripts', '--no-audit', '--no-fund']);
  if (readFileSync(upgradeMarker, 'utf8') !== marker) throw new Error('in-place package upgrade touched external state');
  const upgradedCli = join(installRoot, 'node_modules', 'fishyume', 'dist', 'cli.js');
  if (!existsSync(upgradedCli)) throw new Error('upgraded CLI is missing');
  const upgradedHelp = invoke(upgradedCli, ['--help']);
  if (!/Fishyume/.test(upgradedHelp) || !/dashboard/.test(upgradedHelp)) throw new Error('upgraded CLI help is incomplete');

  const replayed = applicationCall(upgradedCli, 'run.start', startRequest);
  if (replayed.runId !== started.runId) throw new Error('in-place upgrade lost the idempotent run.start receipt');
  const upgradedWaiting = waitForRun(upgradedCli, started.runId, run => run.phase === 'waiting', 'post-upgrade Approval');
  assertWaitingApproval(upgradedWaiting, waiting);
  assertSameEvidence(runEvidence(started.runId), waitingEvidence, 'post-upgrade observation');
  const upgradedEvents = applicationCall(upgradedCli, 'run.events', {runId: started.runId, afterSequence: 0, limit: 100});
  if (upgradedEvents.nextAfterSequence !== waitingEvents.nextAfterSequence) throw new Error('in-place upgrade changed the waiting event sequence');

  applicationCall(upgradedCli, 'run.action', {actionId: 'install-upgrade-approve-1', runId: started.runId, type: 'approve', expectedStateVersion: upgradedWaiting.stateVersion, nodeId: 'approve'});
  const upgradedTerminal = waitForRun(upgradedCli, started.runId, run => run.phase === 'completed', 'post-upgrade completion');
  const upgradedResult = applicationCall(upgradedCli, 'run.result', {runId: started.runId});
  if (upgradedTerminal.conclusion !== 'succeeded' || upgradedResult.conclusion !== 'succeeded') throw new Error('upgraded package did not complete the restored Approval Run');

  stopControlPlane();
  rmSync(stateRoot, {recursive: true, force: true});
  cpSync(rollbackSnapshot, stateRoot, {recursive: true});
  if (readFileSync(upgradeMarker, 'utf8') !== marker) throw new Error('rollback restore lost external state');
  assertSameEvidence(runEvidence(started.runId), waitingEvidence, 'rollback restore');

  const restoredWaiting = waitForRun(upgradedCli, started.runId, run => run.phase === 'waiting', 'restored Approval');
  assertWaitingApproval(restoredWaiting, waiting);
  assertSameEvidence(runEvidence(started.runId), waitingEvidence, 'read-only restored observation');
  const restoredReplay = applicationCall(upgradedCli, 'run.start', startRequest);
  if (restoredReplay.runId !== started.runId) throw new Error('rollback restore lost the idempotent run.start receipt');
  applicationCall(upgradedCli, 'run.action', {actionId: 'install-rollback-approve-1', runId: started.runId, type: 'approve', expectedStateVersion: restoredWaiting.stateVersion, nodeId: 'approve'});
  const restoredTerminal = waitForRun(upgradedCli, started.runId, run => run.phase === 'completed', 'restored completion');
  const restoredResult = applicationCall(upgradedCli, 'run.result', {runId: started.runId});
  if (restoredTerminal.stateVersion !== upgradedTerminal.stateVersion || restoredTerminal.conclusion !== 'succeeded' || restoredResult.conclusion !== 'succeeded') {
    throw new Error('restored package state did not converge to the same terminal contract');
  }

  process.stdout.write('Verified packed install, in-place upgrade, durable Run snapshot restore, and terminal reconvergence\n');
} finally {
  stopControlPlane();
  rmSync(root, {recursive: true, force: true});
}
