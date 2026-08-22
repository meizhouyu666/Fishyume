import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import test from 'node:test';
import {collectHostEvents, hasCompletedAgentMarker, terminateAndWait, validateHostEvidence} from './codex-host-mcp-smoke.mjs';

function completed(tool, args, payload, status = 'completed') {
  const result = payload?.error
    ? {content: [{type: 'text', text: JSON.stringify(payload)}], structured_content: null}
    : {content: [{type: 'text', text: JSON.stringify(payload)}], structured_content: payload};
  return {type: 'item.completed', item: {id: `item-${tool}`, type: 'mcp_tool_call', server: 'fishyume', tool, arguments: args, result, error: null, status}};
}

function validPtyStream() {
  const runId = 'run-acceptance';
  const events = [
    completed('system.capabilities', {}, {apiVersion: 'fishyume.application/v1'}),
    completed('workflow.validate', {}, {apiVersion: 'fishyume.application/v1', valid: true}),
    completed('workflow.explain', {}, {apiVersion: 'fishyume.application/v1', name: 'real-host-pty'}),
    completed('run.start', {}, {apiVersion: 'fishyume.application/v1', runId, stateVersion: 4}),
    completed('run.events', {runId}, {apiVersion: 'fishyume.application/v1', runId, events: [{runId, sequence: 2, type: 'node.waiting', phase: 'waiting', nodeId: 'approve', nodePhase: 'waiting'}]}),
    completed('run.get', {runId}, {apiVersion: 'fishyume.application/v1', run: {runId, phase: 'waiting', stateVersion: 5}}),
    completed('run.events', {runId, afterSequence: 2, waitMs: 30000}, {apiVersion: 'fishyume.application/v1', runId, events: [{runId, sequence: 5, type: 'run.completed', phase: 'completed', conclusion: 'succeeded'}]}),
    completed('run.action', {runId, actionId: 'real-host-mcp-pty-stale-1', expectedStateVersion: 5, nodeId: 'approve', type: 'approve'}, {error: {code: 'conflict', message: 'state version conflict', data: {expectedStateVersion: 5, currentStateVersion: 8}}}, 'failed'),
    completed('run.result', {runId}, {apiVersion: 'fishyume.application/v1', runId, conclusion: 'succeeded', completedAt: '2026-08-14T00:00:00Z', results: []}),
    {type: 'item.completed', item: {id: 'item-final', type: 'agent_message', text: `HOST_MCP_PTY succeeded run=${runId} conflict=host`}},
  ];
  return events.map(event => JSON.stringify(event)).join('\n');
}

test('PTY evidence requires exact conflict, retained waiting version, and matching terminal result', () => {
  const evidence = validateHostEvidence(collectHostEvents(validPtyStream()), true);
  assert.deepEqual(evidence, {
    runId: 'run-acceptance',
    retainedStateVersion: 5,
    currentStateVersion: 8,
    conflictCode: 'conflict',
    resultConclusion: 'succeeded',
  });
});

test('fabricated success cannot hide non-conflict or non-terminal MCP payloads', () => {
  const nonConflict = validPtyStream().replace('\\"code\\":\\"conflict\\"', '\\"code\\":\\"internal\\"');
  assert.throws(() => validateHostEvidence(collectHostEvents(nonConflict), true), /not exact conflict/);

  const notReady = validPtyStream().replace(
    '{"apiVersion":"fishyume.application/v1","runId":"run-acceptance","conclusion":"succeeded","completedAt":"2026-08-14T00:00:00Z","results":[]}',
    '{"error":{"code":"not_ready","message":"conflict-shaped but not terminal"}}',
  );
  assert.throws(() => validateHostEvidence(collectHostEvents(notReady), true), /run.result completed payload/);
});

test('PTY evidence rejects retargeted Runs and non-retained state versions', () => {
  const wrongRun = validPtyStream().replace('HOST_MCP_PTY succeeded run=run-acceptance', 'HOST_MCP_PTY succeeded run=run-other');
  assert.throws(() => validateHostEvidence(collectHostEvents(wrongRun), true), /final runId/);

  const wrongVersion = validPtyStream().replace('"expectedStateVersion":5,"nodeId"', '"expectedStateVersion":6,"nodeId"');
  assert.throws(() => validateHostEvidence(collectHostEvents(wrongVersion), true), /retain the waiting stateVersion/);
});

test('completion marker ignores prompt text and requires a completed agent message', () => {
  const marker = /^HOST_MCP_SMOKE succeeded run=[^\s]+ tools=system\.capabilities,workflow\.validate,workflow\.explain,run\.start,run\.events,run\.action,run\.result\.?$/;
  const promptEvent = JSON.stringify({type: 'thread.started', prompt: 'Reply HOST_MCP_SMOKE succeeded only after all tools complete.'});
  const startedMessage = JSON.stringify({type: 'item.started', item: {type: 'agent_message', text: 'HOST_MCP_SMOKE succeeded run=run-early'}});
  assert.equal(hasCompletedAgentMarker(`${promptEvent}\n${startedMessage}`, marker), false);

  const mentionedMarker = JSON.stringify({type: 'item.completed', item: {type: 'agent_message', text: 'I will later reply HOST_MCP_SMOKE succeeded run=run-early tools=system.capabilities,workflow.validate,workflow.explain,run.start,run.events,run.action,run.result.'}});
  assert.equal(hasCompletedAgentMarker(`${promptEvent}\n${mentionedMarker}`, marker), false);

  const completedMessage = JSON.stringify({type: 'item.completed', item: {type: 'agent_message', text: 'HOST_MCP_SMOKE succeeded run=run-complete tools=system.capabilities,workflow.validate,workflow.explain,run.start,run.events,run.action,run.result.'}});
  assert.equal(hasCompletedAgentMarker(`${promptEvent}\n${completedMessage}`, marker), true);
});

test('cleanup terminates and joins the spawned process tree', {timeout: 10_000}, async () => {
  const grandchildSource = 'setInterval(() => {}, 1000)';
  const childSource = `const {spawn}=require('node:child_process'); const child=spawn(process.execPath,['-e',${JSON.stringify(grandchildSource)}],{stdio:'ignore'}); console.log(child.pid); setInterval(() => {}, 1000);`;
  const child = spawn(process.execPath, ['-e', childSource], {stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true, detached: process.platform !== 'win32'});
  const grandchildPid = await new Promise((resolvePid, rejectPid) => {
    const timer = setTimeout(() => rejectPid(new Error('child did not report grandchild pid')), 3000);
    child.stdout.once('data', chunk => {clearTimeout(timer); resolvePid(Number(chunk.toString('utf8').trim()))});
    child.once('error', error => {clearTimeout(timer); rejectPid(error)});
  });
  assert.ok(Number.isInteger(grandchildPid) && grandchildPid > 0);
  await terminateAndWait(child, 'cleanup test');
  assert.ok(child.exitCode !== null || child.signalCode !== null, 'direct child must be joined');
  const deadline = Date.now() + 2000;
  let grandchildExited = false;
  while (Date.now() < deadline) {
    try {process.kill(grandchildPid, 0)} catch (error) {
      if (error?.code === 'ESRCH') {grandchildExited = true; break}
      throw error;
    }
    await new Promise(resolveCheck => setTimeout(resolveCheck, 25));
  }
  assert.equal(grandchildExited, true, 'grandchild must not survive tree cleanup');
});
