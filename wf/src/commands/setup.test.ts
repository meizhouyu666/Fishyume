import assert from 'node:assert/strict';
import test from 'node:test';
import {checkCodexHost} from './doctor.js';
import {codexSetupCommand, setupCodex} from './setup.js';
import type {CodexRunner, CommandResult} from './codex-cli.js';

const configured = JSON.stringify({enabled: true, transport: {type: 'stdio', command: 'fishyume', args: ['mcp']}});
const missing: CommandResult = {status: 1, stdout: '', stderr: "No MCP server named 'fishyume' found."};
const ok = (stdout = ''): CommandResult => ({status: 0, stdout, stderr: ''});

function scripted(results: CommandResult[]): {runner: CodexRunner; calls: string[][]} {
  const calls: string[][] = [];
  return {calls, runner(args) {calls.push(args); const result = results.shift(); if (!result) throw new Error(`unexpected call ${args.join(' ')}`); return result}};
}

test('setup codex print is mutation-free and copyable', async () => {
  let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {printOnly: true, runner() {throw new Error('must not run')}}), 0);
  assert.equal(output.trim(), codexSetupCommand);
});

test('setup codex adds and verifies a missing MCP server', async () => {
  const plan = scripted([ok('codex-cli 0.147.0'), missing, ok(), ok(configured)]); let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {runner: plan.runner}), 0);
  assert.deepEqual(plan.calls, [
    ['--version'],
    ['mcp', 'get', 'fishyume', '--json'],
    ['mcp', 'add', 'fishyume', '--', 'fishyume', 'mcp'],
    ['mcp', 'get', 'fishyume', '--json'],
  ]);
  assert.match(output, /ok codex-mcp/);
});

test('setup codex is idempotent and protects a conflicting entry', async () => {
  const current = scripted([ok('codex-cli fixture'), ok(configured)]); let currentOutput = '';
  assert.equal(await setupCodex({write(text) {currentOutput += text}}, {runner: current.runner}), 0);
  assert.equal(current.calls.length, 2);
  assert.match(currentOutput, /already configured/);

  const conflicting = JSON.stringify({transport: {type: 'stdio', command: 'other', args: []}});
  const conflict = scripted([ok('codex-cli fixture'), ok(conflicting)]); let conflictOutput = '';
  assert.equal(await setupCodex({write(text) {conflictOutput += text}}, {runner: conflict.runner}), 1);
  assert.match(conflictOutput, /--force/);
  assert.equal(conflict.calls.length, 2);
});

test('setup codex force replaces then verifies a conflicting entry', async () => {
  const conflicting = JSON.stringify({transport: {type: 'stdio', command: 'other', args: []}});
  const plan = scripted([ok('codex-cli fixture'), ok(conflicting), ok(), ok(), ok(configured)]); let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {runner: plan.runner, force: true}), 0);
  assert.deepEqual(plan.calls[2], ['mcp', 'remove', 'fishyume']);
  assert.deepEqual(plan.calls[3], ['mcp', 'add', 'fishyume', '--', 'fishyume', 'mcp']);
});

test('doctor reports Codex CLI, login, MCP, and executable recovery', () => {
  const ready = scripted([ok('codex-cli fixture'), ok('authenticated'), ok(configured)]); let readyOutput = '';
  assert.equal(checkCodexHost({write(text) {readyOutput += text}}, ready.runner), 0);
  assert.match(readyOutput, /ok codex-login authenticated/);
  assert.match(readyOutput, /ok codex-mcp/);

  const broken = scripted([ok('codex-cli fixture'), {status: 1, stdout: '', stderr: 'not logged in'}, missing]); let brokenOutput = '';
  assert.equal(checkCodexHost({write(text) {brokenOutput += text}}, broken.runner), 1);
  assert.match(brokenOutput, /Run: codex login/);
  assert.match(brokenOutput, /Run: fishyume setup codex/);
});
