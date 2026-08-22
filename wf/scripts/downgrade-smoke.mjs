#!/usr/bin/env node
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cpSync, existsSync, mkdirSync, readFileSync, rmSync, symlinkSync} from 'node:fs';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';

const defaultBase = '391dc2c3a788b7754b52d4234fbfc80c5d5a3dae';
const requestedBase = process.env.FISHYUME_DOWNGRADE_BASE?.trim() || defaultBase;
if (!/^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/.test(requestedBase) || requestedBase.includes('..')) {
  throw new Error('FISHYUME_DOWNGRADE_BASE must be a commit or tag name');
}

const npmCli = process.env.npm_execpath;
if (!npmCli) throw new Error('run this smoke through npm so npm_execpath is available');

const wfRoot = process.cwd();
const repoRoot = resolve(wfRoot, '..');
const currentNodeModules = join(wfRoot, 'node_modules');
if (!existsSync(currentNodeModules)) throw new Error('current wf/node_modules is required; run npm --prefix wf ci first');

const root = await mkdtemp(join(tmpdir(), 'fishyume-downgrade-smoke-'));
const archivePath = join(root, 'historical-source.tar');
const historicalRoot = join(root, 'historical-source');
const historicalWf = join(historicalRoot, 'wf');
const historicalPlatform = join(root, 'historical-platform');
const currentPlatform = join(root, 'current-platform');
const historicalPacks = join(root, 'historical-packs');
const currentPacks = join(root, 'current-packs');
const installRoot = join(root, 'install');
const stateRoot = join(root, 'state');
const rollbackSnapshot = join(root, 'rollback-snapshot');
const platformRelative = process.platform === 'win32' && process.arch === 'x64'
  ? join('packages', 'fishyume-engine-win32-x64')
  : process.platform === 'linux' && process.arch === 'x64'
    ? join('packages', 'fishyume-engine-linux-x64')
    : undefined;
if (!platformRelative) throw new Error(`unsupported downgrade-smoke platform ${process.platform}-${process.arch}`);
const engineName = process.platform === 'win32' ? 'fishyume-engine.exe' : 'fishyume-engine';

function run(command, args, cwd = repoRoot, environment = process.env) {
  const result = spawnSync(command, args, {cwd, env: environment, encoding: 'utf8', windowsHide: true});
  if (result.status !== 0) throw new Error(`${command} ${args[0] || ''} failed: ${result.stderr || result.stdout || result.error?.message}`);
  return result.stdout.trim();
}

function npm(args, cwd = wfRoot) {
  return run(process.execPath, [npmCli, ...args], cwd);
}

function resolveCommit(reference) {
  try {return run('git', ['rev-parse', '--verify', `${reference}^{commit}`])}
  catch {throw new Error(`downgrade base ${reference} is unavailable; fetch full history or set FISHYUME_DOWNGRADE_BASE to an archived baseline`)}
}

function pack(directory, destination) {
  mkdirSync(destination, {recursive: true});
  const report = JSON.parse(npm(['pack', '--json', '--pack-destination', destination], directory));
  if (!Array.isArray(report) || report.length !== 1 || !report[0].filename) throw new Error(`unexpected npm pack report for ${directory}`);
  return join(destination, report[0].filename);
}

function buildEngine(sourceRoot, platformRoot) {
  const sourcePlatform = join(sourceRoot, 'wf', platformRelative);
  cpSync(sourcePlatform, platformRoot, {recursive: true});
  const binary = join(platformRoot, 'bin', engineName);
  run('go', ['build', '-trimpath', '-ldflags=-s -w', '-o', binary, './cmd/wf-engine'], join(sourceRoot, 'wf-engine'), {...process.env, CGO_ENABLED: '0'});
  return binary;
}

function hash(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function installedPaths() {
  const packageRoot = join(installRoot, 'node_modules');
  const enginePackage = process.platform === 'win32' ? 'fishyume-engine-win32-x64' : 'fishyume-engine-linux-x64';
  return {
    cli: join(packageRoot, 'fishyume', 'dist', 'cli.js'),
    cliEvidence: join(packageRoot, 'fishyume', 'dist', 'bridge', 'application.js'),
    engine: join(packageRoot, enginePackage, 'bin', engineName),
  };
}

function installPair(cliTarball, engineTarball, expected) {
  npm(['install', '--force', '--prefix', installRoot, cliTarball, engineTarball, '--ignore-scripts', '--no-audit', '--no-fund']);
  const installed = installedPaths();
  if (!existsSync(installed.cli) || !existsSync(installed.cliEvidence) || !existsSync(installed.engine)) throw new Error('downgrade package installation is incomplete');
  if (hash(installed.cliEvidence) !== expected.cli || hash(installed.engine) !== expected.engine) {
    throw new Error(`npm did not install the expected ${expected.label} CLI/Engine pair`);
  }
  return installed.cli;
}

function invoke(cli, args) {
  const result = spawnSync(process.execPath, [cli, ...args], {
    encoding: 'utf8',
    windowsHide: true,
    env: {...process.env, FISHYUME_STATE_DIR: stateRoot, WF_STATE_DIR: stateRoot},
  });
  if (result.status !== 0) throw new Error(`fishyume ${args.join(' ')} failed (${result.status}): ${result.stderr || result.stdout}`);
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

function stopControlPlane() {
  const metadataPath = join(stateRoot, 'control-plane.json');
  if (!existsSync(metadataPath)) return;
  const owner = JSON.parse(readFileSync(metadataPath, 'utf8'));
  if (resolve(String(owner.stateDir)) !== resolve(stateRoot) || !Number.isInteger(owner.pid) || owner.pid <= 0) throw new Error('refusing to stop an unverified Control Plane owner');
  try {process.kill(owner.pid, 'SIGTERM')} catch (error) {
    if (error?.code !== 'ESRCH') throw error;
  }
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {process.kill(owner.pid, 0)} catch (error) {
      if (error?.code === 'ESRCH') return;
      throw error;
    }
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 25);
  }
  throw new Error(`temporary Fishyume Control Plane ${owner.pid} did not exit after termination`);
}

function assertWaiting(run, expected) {
  const approval = run.nodes?.find(node => node.nodeId === 'approve');
  if (run.runId !== expected.runId || run.stateVersion !== expected.stateVersion || run.phase !== 'waiting' || approval?.phase !== 'waiting') {
    throw new Error(`historical rollback state changed: ${JSON.stringify(run)}`);
  }
}

function completeApproval(cli, waiting, actionId) {
  applicationCall(cli, 'run.action', {actionId, runId: waiting.runId, type: 'approve', expectedStateVersion: waiting.stateVersion, nodeId: 'approve'});
  const terminal = waitForRun(cli, waiting.runId, run => run.phase === 'completed', `${actionId} completion`);
  const result = applicationCall(cli, 'run.result', {runId: waiting.runId});
  if (terminal.conclusion !== 'succeeded' || result.conclusion !== 'succeeded') throw new Error(`${actionId} did not reach succeeded`);
  return terminal;
}

try {
  const baseCommit = resolveCommit(requestedBase);
  const currentCommit = run('git', ['rev-parse', 'HEAD']);
  mkdirSync(historicalRoot, {recursive: true});
  run('git', ['archive', '--format=tar', `--output=${archivePath}`, baseCommit]);
  run('tar', ['-xf', archivePath, '-C', historicalRoot]);
  symlinkSync(currentNodeModules, join(historicalWf, 'node_modules'), process.platform === 'win32' ? 'junction' : 'dir');
  npm(['run', 'build'], historicalWf);

  const historicalEngineBinary = buildEngine(historicalRoot, historicalPlatform);
  const currentEngineBinary = buildEngine(repoRoot, currentPlatform);
  const historicalCliTarball = pack(historicalWf, historicalPacks);
  const historicalEngineTarball = pack(historicalPlatform, historicalPacks);
  const currentCliTarball = pack(wfRoot, currentPacks);
  const currentEngineTarball = pack(currentPlatform, currentPacks);
  const historicalExpected = {label: 'historical', cli: hash(join(historicalWf, 'dist', 'bridge', 'application.js')), engine: hash(historicalEngineBinary)};
  const currentExpected = {label: 'current', cli: hash(join(wfRoot, 'dist', 'bridge', 'application.js')), engine: hash(currentEngineBinary)};
  if (historicalExpected.cli === currentExpected.cli && historicalExpected.engine === currentExpected.engine) throw new Error('historical and current package evidence is identical');

  const historicalCli = installPair(historicalCliTarball, historicalEngineTarball, historicalExpected);
  const workflow = {
    apiVersion: 'fishyume/v2', name: 'historical-downgrade-drill',
    defaults: {agent: {driver: 'codex', target: 'local'}}, execution: {maxConcurrency: 1},
    nodes: {approve: {type: 'approval', prompt: 'Approve historical downgrade drill?'}},
  };
  const startRequest = {project: repoRoot, workflow: {document: workflow}, clientRequestId: 'historical-downgrade-drill-1'};
  const started = applicationCall(historicalCli, 'run.start', startRequest);
  const historicalWaiting = waitForRun(historicalCli, started.runId, run => run.phase === 'waiting', 'historical Approval');
  stopControlPlane();
  cpSync(stateRoot, rollbackSnapshot, {recursive: true});

  const currentCli = installPair(currentCliTarball, currentEngineTarball, currentExpected);
  const upgradedReplay = applicationCall(currentCli, 'run.start', startRequest);
  if (upgradedReplay.runId !== started.runId) throw new Error('current package lost the historical run.start receipt');
  const currentWaiting = waitForRun(currentCli, started.runId, run => run.phase === 'waiting', 'current package historical Approval');
  assertWaiting(currentWaiting, historicalWaiting);
  const currentTerminal = completeApproval(currentCli, currentWaiting, 'historical-upgrade-approve-1');
  stopControlPlane();

  const downgradedCli = installPair(historicalCliTarball, historicalEngineTarball, historicalExpected);
  rmSync(stateRoot, {recursive: true, force: true});
  cpSync(rollbackSnapshot, stateRoot, {recursive: true});
  const downgradedReplay = applicationCall(downgradedCli, 'run.start', startRequest);
  if (downgradedReplay.runId !== started.runId) throw new Error('historical package lost its restored run.start receipt');
  const downgradedWaiting = waitForRun(downgradedCli, started.runId, run => run.phase === 'waiting', 'downgraded Approval');
  assertWaiting(downgradedWaiting, historicalWaiting);
  const downgradedTerminal = completeApproval(downgradedCli, downgradedWaiting, 'historical-downgrade-approve-1');
  if (downgradedTerminal.stateVersion !== currentTerminal.stateVersion) throw new Error('downgraded restored state did not reconverge to the current terminal stateVersion');

  process.stdout.write(`${JSON.stringify({ok: true, baseCommit, currentCommit, runId: started.runId, conclusion: 'succeeded', packageIdentityVerified: true, snapshotRestored: true})}\n`);
} finally {
  stopControlPlane();
  rmSync(root, {recursive: true, force: true});
}
