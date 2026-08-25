import {mkdirSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';

const temporary = mkdtempSync(join(tmpdir(), 'fishyume-web-install-'));
const packs = join(temporary, 'packs');
const install = join(temporary, 'install');
try {
  mkdirSync(packs, {recursive: true});
  const packed = spawnSync(process.execPath, [process.env.npm_execpath, 'pack', '--json', '--pack-destination', packs], {encoding: 'utf8'});
  if (packed.status !== 0) throw new Error(packed.stderr || packed.stdout);
  const filename = JSON.parse(packed.stdout)[0].filename;
  const installed = spawnSync(process.execPath, [process.env.npm_execpath, 'install', '--prefix', install, join(packs, filename), '--ignore-scripts', '--no-audit', '--no-fund'], {encoding: 'utf8'});
  if (installed.status !== 0) throw new Error(installed.stderr || installed.stdout);
  const command = join(install, 'node_modules', 'fishyume-web', 'dist', 'server.js');
  const help = spawnSync(process.execPath, [command, '--help'], {encoding: 'utf8'});
  if (help.status !== 0 || !help.stdout.includes('authenticated loopback')) throw new Error(help.stderr || 'installed help is incomplete');
  process.stdout.write('Verified installed Fishyume Web package\n');
} finally {
  rmSync(temporary, {recursive: true, force: true});
}
