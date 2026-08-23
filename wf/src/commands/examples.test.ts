import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';
import {examplesListText, loadProductExample} from './examples.js';

test('product examples are discoverable and sourced from the documented Workflow', async () => {
  const listed = examplesListText();
  assert.match(listed, /repository-hardening/);
  assert.match(listed, /fishyume examples show <name>/);

  const documented = await readFile(new URL('../../../docs/examples/repository-hardening.yaml', import.meta.url), 'utf8');
  assert.equal(await loadProductExample('repository-hardening'), documented);
});

test('unknown product examples fail with a discovery command', async () => {
  await assert.rejects(loadProductExample('missing'), /unknown example "missing"; run fishyume examples list/);
});

