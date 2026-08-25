import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';

const temporary = mkdtempSync(join(tmpdir(), 'fishyume-web-pack-'));
try {
  const result = spawnSync(process.execPath, [process.env.npm_execpath, 'pack', '--json', '--dry-run', '--pack-destination', temporary], {encoding: 'utf8'});
  if (result.status !== 0) throw new Error(result.stderr || result.stdout);
  const report = JSON.parse(result.stdout)[0];
  const files = report.files.map(file => file.path.replaceAll('\\', '/'));
  const required = ['dist/server.js', 'dist/public/index.html', 'dist/public/app.js', 'dist/public/styles.css', 'package.json', 'README.md'];
  const missing = required.filter(path => !files.includes(path));
  const forbidden = files.filter(path => /(^|\/)(?:src|scripts|node_modules|\.env|state|credentials|secrets?)(\/|$)/i.test(path) || /\.test\./.test(path));
  if (missing.length || forbidden.length) throw new Error(`package audit failed; missing=[${missing}] forbidden=[${forbidden}]`);
  process.stdout.write(`Verified Fishyume Web package content (${files.length} files)\n`);
} finally {
  rmSync(temporary, {recursive: true, force: true});
}
