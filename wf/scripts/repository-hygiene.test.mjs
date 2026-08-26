import assert from 'node:assert/strict';
import {access, readFile, readdir, stat} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

async function exists(path) {
  try {await access(path); return true} catch {return false}
}

async function markdownFiles(directory) {
  const result = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) result.push(...await markdownFiles(path));
    else if (entry.name.endsWith('.md')) result.push(path);
  }
  return result;
}

test('repository excludes retired execution paths and local agent configuration', async () => {
  for (const relative of [
    'wf-engine/internal/backend/directcli',
    'wf-engine/internal/backend/driveradapter',
    '.codex/config.toml',
  ]) assert.equal(await exists(resolve(repoRoot, relative)), false, `${relative} must not return`);
  const ignore = await readFile(resolve(repoRoot, '.gitignore'), 'utf8');
  assert.match(ignore, /^\.claude\/$/m);
  assert.match(ignore, /^\.codex\/$/m);
});

test('Windows preview upgrade checks active Runs through the structured API', async () => {
  const installer = await readFile(resolve(repoRoot, 'install-fishyume.ps1'), 'utf8');
  assert.match(installer, /machine run\.list --params \$params/);
  assert.match(installer, /ConvertFrom-Json/);
  assert.doesNotMatch(installer, /\\b\(\\d\+\) active\\b/);
});

test('repository markdown has no broken relative links', async () => {
  const files = [resolve(repoRoot, 'README.md'), ...await markdownFiles(resolve(repoRoot, 'docs'))];
  const broken = [];
  for (const file of files) {
    const source = await readFile(file, 'utf8');
    for (const match of source.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
      const target = match[1].trim().replace(/^<|>$/g, '').split('#', 1)[0];
      if (!target || /^(?:https?:|mailto:)/i.test(target)) continue;
      const resolved = resolve(dirname(file), decodeURIComponent(target));
      try {await stat(resolved)} catch {broken.push(`${file.slice(repoRoot.length + 1)} -> ${target}`)}
    }
  }
  assert.deepEqual(broken, []);
});
