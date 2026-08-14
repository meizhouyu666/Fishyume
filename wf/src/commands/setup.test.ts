import assert from 'node:assert/strict';
import test from 'node:test';
import {checkCodexHost} from './doctor.js';
import {codexSetupCommand, setupCodex} from './setup.js';
import type {CodexRunner, CommandResult, McpInvocation} from './codex-cli.js';

const invocation: McpInvocation = {command: 'C:\\Program Files\\nodejs\\node.exe', args: ['C:\\npm\\node_modules\\fishyume\\dist\\cli.js', 'mcp']};
const configured = JSON.stringify({enabled: true, transport: {type: 'stdio', command: invocation.command, args: invocation.args}});
const missing: CommandResult = {status: 1, stdout: '', stderr: "No MCP server named 'fishyume' found."};
const ok = (stdout = ''): CommandResult => ({status: 0, stdout, stderr: ''});
const noPolicyWrite = async (): Promise<void> => undefined;

function scripted(results: CommandResult[]): {runner: CodexRunner; calls: string[][]} {
  const calls: string[][] = [];
  return {calls, runner(args) {calls.push(args); const result = results.shift(); if (!result) throw new Error(`unexpected call ${args.join(' ')}`); return result}};
}

test('setup codex print is mutation-free and copyable', async () => {
  let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {printOnly: true, runner() {throw new Error('must not run')}, invocation}), 0);
  assert.equal(output.trim(), codexSetupCommand(invocation));
});

test('setup codex adds and verifies a missing MCP server', async () => {
  const plan = scripted([ok('codex-cli 0.147.0'), missing, ok(), ok(configured)]); let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {runner: plan.runner, invocation, policyWriter: noPolicyWrite}), 0);
  assert.deepEqual(plan.calls, [
    ['--version'],
    ['mcp', 'get', 'fishyume', '--json'],
    ['mcp', 'add', 'fishyume', '--', invocation.command, ...invocation.args],
    ['mcp', 'get', 'fishyume', '--json'],
  ]);
  assert.match(output, /ok codex-mcp/);
});

test('setup codex is idempotent and protects a conflicting entry', async () => {
  const current = scripted([ok('codex-cli fixture'), ok(configured)]); let currentOutput = '';
  assert.equal(await setupCodex({write(text) {currentOutput += text}}, {runner: current.runner, invocation, policyWriter: noPolicyWrite}), 0);
  assert.equal(current.calls.length, 2);
  assert.match(currentOutput, /already configured/);

  const conflicting = JSON.stringify({transport: {type: 'stdio', command: 'other', args: []}});
  const conflict = scripted([ok('codex-cli fixture'), ok(conflicting)]); let conflictOutput = '';
  assert.equal(await setupCodex({write(text) {conflictOutput += text}}, {runner: conflict.runner, invocation, policyWriter: noPolicyWrite}), 1);
  assert.match(conflictOutput, /--force/);
  assert.equal(conflict.calls.length, 2);
});

test('setup codex force replaces then verifies a conflicting entry', async () => {
  const conflicting = JSON.stringify({transport: {type: 'stdio', command: 'other', args: []}});
  const plan = scripted([ok('codex-cli fixture'), ok(conflicting), ok(), ok(), ok(configured)]); let output = '';
  assert.equal(await setupCodex({write(text) {output += text}}, {runner: plan.runner, force: true, invocation, policyWriter: noPolicyWrite}), 0);
  assert.deepEqual(plan.calls[2], ['mcp', 'remove', 'fishyume']);
  assert.deepEqual(plan.calls[3], ['mcp', 'add', 'fishyume', '--', invocation.command, ...invocation.args]);
});

test('doctor reports Codex CLI, login, MCP, and executable recovery', () => {
  const ready = scripted([ok('codex-cli fixture'), ok('authenticated'), ok(configured)]); let readyOutput = '';
  assert.equal(checkCodexHost({write(text) {readyOutput += text}}, ready.runner, invocation, true), 0);
  assert.match(readyOutput, /ok codex-login authenticated/);
  assert.match(readyOutput, /ok codex-mcp/);

  const missingPolicy = scripted([ok('codex-cli fixture'), ok('authenticated'), ok(configured)]); let policyOutput = '';
  assert.equal(checkCodexHost({write(text) {policyOutput += text}}, missingPolicy.runner, invocation, false), 1);
  assert.match(policyOutput, /approval policy is incomplete/);
  assert.match(policyOutput, /Run: fishyume setup codex/);

  const broken = scripted([ok('codex-cli fixture'), {status: 1, stdout: '', stderr: 'not logged in'}, missing]); let brokenOutput = '';
  assert.equal(checkCodexHost({write(text) {brokenOutput += text}}, broken.runner, invocation, true), 1);
  assert.match(brokenOutput, /Run: codex login/);
  assert.match(brokenOutput, /Run: fishyume setup codex/);
});
