import type {ApplicationNodeView, ApplicationRunView} from '../../../wf/src/bridge/application.js';

export interface WorkflowEdge {from: string; to: string}

export interface WorkflowGraphNode {
  node: ApplicationNodeView;
  x: number;
  y: number;
}

export interface WorkflowGraphEdge extends WorkflowEdge {path: string}

export interface WorkflowGraphStage {index: number; x: number}

export interface WorkflowGraphLayout {
  width: number;
  height: number;
  nodeWidth: number;
  nodeHeight: number;
  nodes: WorkflowGraphNode[];
  edges: WorkflowGraphEdge[];
  stages: WorkflowGraphStage[];
}

export const workflowNodeWidth = 220;
export const workflowNodeHeight = 96;
const workflowColumnGap = 104;
const workflowRowGap = 28;
const workflowCanvasPaddingX = 36;
const workflowCanvasPaddingTop = 58;
const workflowCanvasPaddingBottom = 34;
const workflowMinimumBodyHeight = 356;

/** Keep topology projection deterministic even when older runs omit layer metadata. */
export function workflowLayers(run: Pick<ApplicationRunView, 'nodes' | 'topologicalOrder' | 'parallelLayers'>): ApplicationNodeView[][] {
  const byId = new Map(run.nodes.map(node => [node.nodeId, node]));
  const ids = run.parallelLayers?.length ? run.parallelLayers.flat() : run.topologicalOrder;
  const fallback = ids.length ? ids : run.nodes.map(node => node.nodeId);
  if (run.parallelLayers?.length) return run.parallelLayers.map(layer => layer.map(id => byId.get(id)).filter((node): node is ApplicationNodeView => Boolean(node)));
  return fallback.map(id => byId.get(id)).filter((node): node is ApplicationNodeView => Boolean(node)).map(node => [node]);
}

export function workflowEdges(run: Pick<ApplicationRunView, 'nodes' | 'topologicalOrder'>): WorkflowEdge[] {
  const known = new Set(run.nodes.map(node => node.nodeId));
  const order = new Map(run.topologicalOrder.map((id, index) => [id, index]));
  return run.nodes.flatMap(node => (node.dependsOn ?? [])
    .filter(from => known.has(from))
    .map(from => ({from, to: node.nodeId})))
    .sort((left, right) => (order.get(left.from) ?? Number.MAX_SAFE_INTEGER) - (order.get(right.from) ?? Number.MAX_SAFE_INTEGER)
      || (order.get(left.to) ?? Number.MAX_SAFE_INTEGER) - (order.get(right.to) ?? Number.MAX_SAFE_INTEGER)
      || left.from.localeCompare(right.from) || left.to.localeCompare(right.to));
}

/** Project persisted parallel layers into a stable left-to-right DAG canvas. */
export function workflowGraphLayout(run: Pick<ApplicationRunView, 'nodes' | 'topologicalOrder' | 'parallelLayers'>): WorkflowGraphLayout {
  const layers = workflowLayers(run);
  const positions = new Map<string, {x: number; y: number}>();
  const maxRows = Math.max(1, ...layers.map(layer => layer.length));
  const bodyHeight = Math.max(workflowMinimumBodyHeight, maxRows * workflowNodeHeight + Math.max(0, maxRows - 1) * workflowRowGap);
  const nodes: WorkflowGraphNode[] = [];
  const stages: WorkflowGraphStage[] = [];

  for (const [column, layer] of layers.entries()) {
    const x = workflowCanvasPaddingX + column * (workflowNodeWidth + workflowColumnGap);
    const layerHeight = layer.length * workflowNodeHeight + Math.max(0, layer.length - 1) * workflowRowGap;
    const startY = workflowCanvasPaddingTop + (bodyHeight - layerHeight) / 2;
    stages.push({index: column, x});
    for (const [row, node] of layer.entries()) {
      const y = startY + row * (workflowNodeHeight + workflowRowGap);
      positions.set(node.nodeId, {x, y});
      nodes.push({node, x, y});
    }
  }

  const edges = workflowEdges(run).flatMap(edge => {
    const source = positions.get(edge.from);
    const target = positions.get(edge.to);
    if (!source || !target) return [];
    const x1 = source.x + workflowNodeWidth;
    const y1 = source.y + workflowNodeHeight / 2;
    const x2 = target.x;
    const y2 = target.y + workflowNodeHeight / 2;
    const control = Math.max(34, Math.abs(x2 - x1) * .44);
    return [{...edge, path: `M${x1} ${y1} C${x1 + control} ${y1}, ${x2 - control} ${y2}, ${x2} ${y2}`}];
  });

  return {
    width: layers.length === 0 ? 0 : workflowCanvasPaddingX * 2 + layers.length * workflowNodeWidth + Math.max(0, layers.length - 1) * workflowColumnGap,
    height: workflowCanvasPaddingTop + bodyHeight + workflowCanvasPaddingBottom,
    nodeWidth: workflowNodeWidth,
    nodeHeight: workflowNodeHeight,
    nodes,
    edges,
    stages,
  };
}

/** Return the complete upstream and downstream chain around a selected node. */
export function relatedWorkflowNodeIds(run: Pick<ApplicationRunView, 'nodes'>, nodeId: string | undefined): ReadonlySet<string> {
  if (!nodeId) return new Set();
  const byId = new Map(run.nodes.map(node => [node.nodeId, node]));
  if (!byId.has(nodeId)) return new Set();
  const dependents = new Map<string, string[]>();
  for (const node of run.nodes) {
    for (const dependency of node.dependsOn ?? []) {
      const values = dependents.get(dependency) ?? [];
      values.push(node.nodeId);
      dependents.set(dependency, values);
    }
  }
  const related = new Set<string>();
  const upstreamSeen = new Set<string>();
  const downstreamSeen = new Set<string>();
  const visitUpstream = (id: string): void => {
    if (upstreamSeen.has(id)) return;
    upstreamSeen.add(id);
    related.add(id);
    for (const dependency of byId.get(id)?.dependsOn ?? []) visitUpstream(dependency);
  };
  const visitDownstream = (id: string): void => {
    if (downstreamSeen.has(id)) return;
    downstreamSeen.add(id);
    related.add(id);
    for (const dependent of dependents.get(id) ?? []) visitDownstream(dependent);
  };
  visitUpstream(nodeId);
  visitDownstream(nodeId);
  return related;
}

export function findWorkflowNode(run: Pick<ApplicationRunView, 'nodes'>, nodeId: string | undefined): ApplicationNodeView | undefined {
  return nodeId ? run.nodes.find(node => node.nodeId === nodeId) : undefined;
}
