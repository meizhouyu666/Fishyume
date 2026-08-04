import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';

const expected = [
  'LICENSE',
  'README.md',
  'dist/bridge/engine.d.ts',
  'dist/bridge/engine.js',
  'dist/bridge/engine.js.map',
  'dist/bridge/types.d.ts',
  'dist/bridge/types.js',
  'dist/bridge/types.js.map',
  'dist/cli.d.ts',
  'dist/cli.js',
  'dist/cli.js.map',
  'dist/commands/cancel.d.ts',
  'dist/commands/cancel.js',
  'dist/commands/cancel.js.map',
  'dist/commands/doctor.d.ts',
  'dist/commands/doctor.js',
  'dist/commands/doctor.js.map',
  'dist/commands/resume.d.ts',
  'dist/commands/resume.js',
  'dist/commands/resume.js.map',
  'dist/commands/run.d.ts',
  'dist/commands/run.js',
  'dist/commands/run.js.map',
  'dist/commands/status.d.ts',
  'dist/commands/status.js',
  'dist/commands/status.js.map',
  'dist/tui/run-app.d.ts',
  'dist/tui/run-app.js',
  'dist/tui/run-app.js.map',
  'dist/tui/text-reporter.d.ts',
  'dist/tui/text-reporter.js',
  'dist/tui/text-reporter.js.map',
  'package.json',
].sort();

const real = process.argv.includes('--real');
const temporary = real ? mkdtempSync(join(tmpdir(), 'fishyume-pack-audit-')) : undefined;
const npmCli = process.env.npm_execpath;
if (!npmCli) throw new Error('npm_execpath is required for pack audit');

try {
  const args = [npmCli, 'pack', '--json'];
  if (real) args.push('--pack-destination', temporary);
  else args.push('--dry-run');
  const result = spawnSync(process.execPath, args, {encoding: 'utf8'});
  if (result.status !== 0) throw new Error(`npm pack failed: ${result.stderr || result.stdout}`);
  const report = JSON.parse(result.stdout);
  if (!Array.isArray(report) || report.length !== 1) throw new Error('npm pack returned an unexpected report');
  const actual = report[0].files.map(file => file.path.replaceAll('\\', '/')).sort();
  const banned = actual.filter(path => /\.test\.|(^|\/)(?:\.env|state|wf-state|\.wf|artifacts?|credentials?|secrets?)(\/|$)/i.test(path));
  if (banned.length) throw new Error(`forbidden package content: ${banned.join(', ')}`);
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    const missing = expected.filter(path => !actual.includes(path));
    const unexpected = actual.filter(path => !expected.includes(path));
    throw new Error(`package content mismatch; missing=[${missing.join(', ')}] unexpected=[${unexpected.join(', ')}]`);
  }
  if (real && (!report[0].filename || !report[0].integrity)) throw new Error('real pack did not produce a tarball report');
  process.stdout.write(`Verified ${real ? 'real' : 'dry-run'} Fishyume package content (${actual.length} files)\n`);
} finally {
  if (temporary) rmSync(temporary, {recursive: true, force: true});
}
