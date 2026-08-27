import assert from 'node:assert/strict';
import test from 'node:test';
import type {ApplicationRunView} from '../../../wf/src/bridge/application.js';
import {findWorkflowNode, relatedWorkflowNodeIds, workflowEdges, workflowGraphLayout, workflowLayers} from './workflow-view.js';

const run = {
  nodes: [
    {nodeId: 'verify', type: 'agent', phase: 'pending', dependsOn: ['build'], parallelLayer: 2},
    {nodeId: 'build', type: 'agent', phase: 'running', dependsOn: [], parallelLayer: 1},
    {nodeId: 'review', type: 'approval', phase: 'waiting', dependsOn: ['build'], parallelLayer: 2},
  ],
  topologicalOrder: ['build', 'review', 'verify'],
  parallelLayers: [['build'], ['review', 'verify']],
} as ApplicationRunView;

test('workflow view preserves persisted DAG layers and filters unknown node ids', () => {
  assert.deepEqual(workflowLayers(run).map(layer => layer.map(node => node.nodeId)), [['build'], ['review', 'verify']]);
  assert.equal(findWorkflowNode(run, 'review')?.type, 'approval');
  assert.equal(findWorkflowNode(run, 'missing'), undefined);
});

test('workflow view derives deterministic dependency edges', () => {
  assert.deepEqual(workflowEdges(run), [{from: 'build', to: 'review'}, {from: 'build', to: 'verify'}]);
});

test('workflow view falls back to a vertical topology for historical runs', () => {
  const historical = {...run, parallelLayers: undefined, topologicalOrder: []};
  assert.deepEqual(workflowLayers(historical).map(layer => layer.map(node => node.nodeId)), [['verify'], ['build'], ['review']]);
});

test('workflow graph layout positions stages horizontally and centers fan-out', () => {
  const layout = workflowGraphLayout(run);
  const build = layout.nodes.find(item => item.node.nodeId === 'build')!;
  const review = layout.nodes.find(item => item.node.nodeId === 'review')!;
  const verify = layout.nodes.find(item => item.node.nodeId === 'verify')!;
  assert.ok(build.x < review.x);
  assert.equal(review.x, verify.x);
  assert.ok(build.y > review.y && build.y < verify.y);
  assert.equal(layout.edges.length, 2);
  assert.match(layout.edges[0]!.path, /^M[\d.]+ [\d.]+ C/);
});

test('workflow relationship focus includes complete upstream and downstream chain', () => {
  const chained = {
    ...run,
    nodes: [...run.nodes, {nodeId: 'publish', type: 'agent', phase: 'pending', dependsOn: ['review'], parallelLayer: 3}],
  } as ApplicationRunView;
  assert.deepEqual([...relatedWorkflowNodeIds(chained, 'review')].sort(), ['build', 'publish', 'review']);
  assert.deepEqual([...relatedWorkflowNodeIds(chained, 'missing')], []);
});
