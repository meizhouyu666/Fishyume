import assert from 'node:assert/strict';
import test from 'node:test';
import {demoText} from './demo.js';
import {displayWidth} from '../tui/layout.js';

test('demo renders topology, parallel stage, dependencies, and approval guidance offline', () => {
  const text = demoText(120);
  assert.match(text, /阶段 2 · 并行 2/);
  assert.match(text, /依赖 plan/);
  assert.match(text, /需要人工审批/);
  assert.match(text, /A\/Y 批准/);
  assert.ok(text.split('\n').every(line => displayWidth(line) <= 120));
});

test('demo supports bounded ASCII preview', () => {
  const text = demoText(80, 'ascii');
  assert.match(text, /Stage|阶段/);
  assert.doesNotMatch(text, /[├└│]/);
  assert.ok(text.split('\n').every(line => displayWidth(line) <= 80));
});
