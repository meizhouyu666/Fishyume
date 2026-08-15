import assert from 'node:assert/strict';
import {PassThrough} from 'node:stream';
import test from 'node:test';
import React from 'react';
import {render} from 'ink';
import {canonicalFixture} from './fixtures.js';
import {operatorCommand, RunApp} from './run-app.js';

const ansiPattern = /[\u001B\u009B][[\]()#;?]*(?:(?:(?:[a-zA-Z\d]*(?:;[-a-zA-Z\d/#&.:=?%@~_]+)*)?\u0007)|(?:(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]))/g;

test('中文操作提示对应大小写兼容的低负担按键', () => {
  for (const key of ['a', 'A', 'y', 'Y']) assert.equal(operatorCommand(key), 'approve');
  for (const key of ['r', 'x', 'X', 'n', 'N']) assert.equal(operatorCommand(key), 'reject');
  for (const key of ['R', 't', 'T']) assert.equal(operatorCommand(key), 'retry');
  for (const key of ['c', 'C']) assert.equal(operatorCommand(key), 'cancel');
  for (const key of ['d', 'D', 'q', 'Q']) assert.equal(operatorCommand(key), 'detach');
});

test('Ink renders the Calm Console hierarchy without repeated panels', async () => {
  const output = new PassThrough(); let captured = '';
  output.on('data', chunk => {captured += chunk.toString()});
  const stdout = Object.assign(output, {columns: 80, isTTY: true, getColorDepth: () => 1});
  const inputStream = new PassThrough();
  const input = Object.assign(inputStream, {isTTY: true, isRaw: false, setRawMode() {this.isRaw = true}, ref: () => inputStream, unref: () => inputStream});
  const fixture = canonicalFixture('approval');
  const instance = render(<RunApp view={fixture.view} startedAt={Date.parse(fixture.view.run!.createdAt)} now={Date.parse(fixture.view.run!.updatedAt)} width={80} colorMode="mono" symbolMode="unicode"
    onResume={async () => ({accepted: true, ok: true, message: 'fixture'})}
    onCancel={async () => ({accepted: true, ok: true, message: 'fixture'})}
    onExit={() => undefined}/>, {
    stdout: stdout as unknown as NodeJS.WriteStream,
    stderr: stdout as unknown as NodeJS.WriteStream,
    stdin: input as unknown as NodeJS.ReadStream,
    exitOnCtrlC: false,
    patchConsole: false,
    debug: true,
  });
  await new Promise<void>(resolve => setImmediate(resolve));
  instance.unmount(); input.end(); output.end();
  await new Promise<void>(resolve => setImmediate(resolve));
  const frame = captured.replace(ansiPattern, '').replace(/\r/g, '');
  assert.match(frame, /FISHYUME \/ production-release/); assert.match(frame, /◆ 待审批/); assert.match(frame, /⚠ 需要人工审批：security-review/);
  assert.match(frame, /A\/Y 批准/); assert.match(frame, /X\/N 拒绝/); assert.match(frame, /── 节点：security-review/);
  assert.doesNotMatch(frame, /ACTIVE ATTEMPTS|APPROVALS \(|DIAGNOSTICS \(|\+[-+]+\+/);
});
