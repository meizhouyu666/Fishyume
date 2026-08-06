import type {Conclusion, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';

export interface TextWriter {write(text: string): unknown}

export class TextReporter {
  constructor(private readonly output: TextWriter) {}
  started(snapshot: WorkflowSnapshot): void {this.output.write(`run ${snapshot.id} phase=${snapshot.phase} workflow=${snapshot.workflowName} state=${snapshot.stateDir}\n`)}
  event(event: RunEvent): void {
    const node = event.nodeId ? ` node=${event.nodeId}:${event.nodePhase ?? '-'}` : '';
    const result = event.conclusion ? ` conclusion=${event.conclusion}` : '';
    const reason = event.reason ? ` reason=${event.reason}` : '';
    const summary = event.message ? ` summary=${event.message}` : '';
    this.output.write(`event ${event.sequence} phase=${event.phase}${result}${reason}${node}${summary}\n`);
  }
  finished(snapshot: WorkflowSnapshot, elapsedMs: number): void {
    const conclusion = snapshot.conclusion ? ` conclusion=${snapshot.conclusion}` : '';
    const reason = snapshot.reason ? ` reason=${snapshot.reason}` : '';
    const summary = snapshot.summary ? ` summary=${snapshot.summary}` : '';
    this.output.write(`final run=${snapshot.id} phase=${snapshot.phase}${conclusion}${reason} elapsed=${formatElapsed(elapsedMs)} state=${snapshot.stateDir}${summary}\n`);
  }
}

export function writeStatus(view: RunStatusView, output: TextWriter): void {
  if (view.legacy && view.legacyRun) {
    output.write(`legacy run=${view.legacyRun.id} status=${view.legacyRun.status} node=${view.legacyRun.nodeStatus} state=${view.legacyRun.stateDir}\n`);
    return;
  }
  const run = view.run;
  if (!run) throw new Error('status response did not contain a run');
  const conclusion = run.conclusion ? ` conclusion=${run.conclusion}` : '';
  const reason = run.reason ? ` reason=${run.reason}` : '';
  const capacity = run.effectiveConcurrency ? ` capacity=${run.effectiveConcurrency}` : '';
  output.write(`run=${run.id} workflow=${run.workflowName} driver=${run.resolvedDriver ?? run.backend ?? 'unknown'} target=${run.resolvedTarget ?? 'local'}${capacity} phase=${run.phase}${conclusion}${reason}\n`);
  for (const node of view.nodes ?? []) {
    output.write(`node=${node.id} type=${node.type} phase=${node.phase}${node.conclusion ? ` conclusion=${node.conclusion}` : ''}${node.reason ? ` reason=${node.reason}` : ''}${node.currentAttempt ? ` attempt=${node.currentAttempt}` : ''}${node.diagnostic ? ` diagnostic=${node.diagnostic}` : ''}\n`);
  }
  for (const attempt of view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : [])) output.write(`active node=${attempt.nodeId} attempt=${attempt.number} driver=${attempt.resolvedDriver ?? attempt.backend ?? 'unknown'}\n`);
  for (const approval of view.waitingApprovals ?? []) output.write(`approval node=${approval.id} phase=${approval.phase}${approval.diagnostic ? ` prompt=${approval.diagnostic}` : ''}\n`);
  for (const diagnostic of view.diagnostics ?? []) if (diagnostic.message) output.write(`diagnostic node=${diagnostic.nodeId}${diagnostic.reason ? ` reason=${diagnostic.reason}` : ''} message=${diagnostic.message}\n`);
}

export function formatElapsed(elapsedMs: number): string {const seconds = Math.max(0, Math.floor(elapsedMs / 1000)); const minutes = Math.floor(seconds / 60); return minutes > 0 ? `${minutes}m${seconds % 60}s` : `${seconds}s`}

export function exitCodeForSnapshot(snapshot: WorkflowSnapshot): number {
  if (snapshot.phase === 'waiting' || snapshot.phase === 'paused' || snapshot.phase === 'cancelling') return 4;
  return exitCodeForConclusion(snapshot.conclusion);
}

export function exitCodeForConclusion(conclusion?: Conclusion): number {
  switch (conclusion) {case 'succeeded': return 0; case 'failed': return 1; case 'rejected': return 2; case 'cancelled': return 3; case 'indeterminate': return 5; default: return 4;}
}
