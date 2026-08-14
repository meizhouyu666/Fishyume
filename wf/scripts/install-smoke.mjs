import {spawnSync} from 'node:child_process';
import {cpSync, existsSync, mkdirSync, readFileSync, rmSync} from 'node:fs';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';

const npmCli = process.env.npm_execpath;
if (!npmCli) throw new Error('run this smoke through npm so npm_execpath is available');

const root = await mkdtemp(join(tmpdir(), 'fishyume-install-smoke-'));
const packs = join(root, 'packs');
const installRoot = join(root, 'install');
const stateRoot = join(root, 'state');
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
  const result = spawnSync(process.execPath, [cli, ...args], {
    encoding: 'utf8',
    windowsHide: true,
    env: {...process.env, FISHYUME_STATE_DIR: stateRoot, WF_STATE_DIR: stateRoot},
  });
  if (!allowed.includes(result.status)) throw new Error(`fishyume ${args.join(' ')} failed (${result.status}): ${result.stderr || result.stdout}`);
  return `${result.stdout ?? ''}${result.stderr ?? ''}`;
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
  if (!/Fishyume/.test(help) || !/dashboard/.test(help)) throw new Error('installed top-level help is incomplete');
  if (!/Operator Dashboard/.test(invoke(cli, ['dashboard', '--help']))) throw new Error('installed Dashboard help is incomplete');
  const setup = invoke(cli, ['setup', 'codex', '--print']);
  if (setup.trim() !== 'codex mcp add fishyume -- fishyume mcp') throw new Error('installed Codex setup command is not copyable');
  const dashboard = invoke(cli, []);
  if (!/No durable Runs yet\./.test(dashboard) || !/Check readiness: fishyume doctor/.test(dashboard)) throw new Error(`installed zero-argument Dashboard empty state is incomplete: ${dashboard}`);
  const doctor = invoke(cli, ['doctor'], [0, 1]);
  if (!/ok engine 0\.2\.1-alpha\.1 started/.test(doctor) || !/ok protocol 2 compatible/.test(doctor) || !/(?:ok|fail) codex-mcp/.test(doctor)) throw new Error(`installed Doctor output is incomplete: ${doctor}`);
  process.stdout.write('Verified packed Fishyume install, Dashboard, Codex setup, and Doctor recovery surface\n');
} finally {
  stopControlPlane();
  rmSync(root, {recursive: true, force: true});
}
