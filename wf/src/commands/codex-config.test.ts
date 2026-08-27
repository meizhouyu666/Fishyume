import assert from 'node:assert/strict';
import {mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {applyCodexMcpApprovalPolicy, fishyumeMcpTools, hasFishyumeApprovalPolicy, withFishyumeApprovalPolicy} from './codex-config.js';
import {teamMethods} from '../bridge/team.js';
import {routingMethods} from '../bridge/routing.js';

const fixture = [
  'model = "fixture"',
  '',
  '[mcp_servers.fishyume]',
  'command = "node"',
  'args = ["cli.js", "mcp"]',
  '',
  '[mcp_servers.other]',
  'command = "other"',
  '',
].join('\n');

test('Fishyume approval policy is bounded, complete, and idempotent', () => {
  for (const method of teamMethods) assert.ok(fishyumeMcpTools.includes(method), method);
  for (const method of routingMethods) assert.ok(fishyumeMcpTools.includes(method), method);
  assert.ok(fishyumeMcpTools.includes('web.open'));
  const updated = withFishyumeApprovalPolicy(fixture);
  assert.match(updated, /\[mcp_servers\.fishyume\][\s\S]*required = true[\s\S]*default_tools_approval_mode = "approve"/);
  for (const tool of fishyumeMcpTools) assert.ok(updated.includes(`[mcp_servers.fishyume.tools.${JSON.stringify(tool)}]`), tool);
  assert.match(updated, /\[mcp_servers\.other\]\ncommand = "other"/);
  assert.equal(withFishyumeApprovalPolicy(updated), updated);
});

test('Fishyume approval policy replaces only its tool subsections on disk', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'fishyume-codex-config-'));
  const path = join(directory, 'config.toml');
  try {
    await writeFile(path, fixture);
    await applyCodexMcpApprovalPolicy(path);
    const updated = await readFile(path, 'utf8');
    assert.match(updated, /approval_mode = "approve"/);
    assert.match(updated, /\[mcp_servers\.other\]/);
    assert.equal(hasFishyumeApprovalPolicy(path), true);
  } finally {await rm(directory, {recursive: true, force: true})}
});

test('Fishyume approval policy refuses a missing owned section', () => {
  assert.throws(() => withFishyumeApprovalPolicy('[mcp_servers.other]\ncommand="x"\n'), /did not create/);
});
