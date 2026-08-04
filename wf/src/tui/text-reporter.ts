import type {RunEvent, RunSnapshot, RunStatus} from '../bridge/types.js';

export interface TextWriter { write(text: string): unknown }

export class TextReporter {
  constructor(private readonly output: TextWriter) {}

  started(snapshot: RunSnapshot): void {
    this.output.write(`run ${snapshot.id} status=${snapshot.status} node=${snapshot.nodeStatus} state=${snapshot.stateDir}\n`);
  }

  event(event: RunEvent): void {
    const summary = event.message ? ` summary=${event.message}` : '';
    this.output.write(`event ${event.sequence} status=${event.status} node=${event.nodeStatus}${summary}\n`);
  }

  finished(snapshot: RunSnapshot, elapsedMs: number): void {
    const summary = snapshot.summary ? ` summary=${snapshot.summary}` : '';
    this.output.write(`final run=${snapshot.id} status=${snapshot.status} node=${snapshot.nodeStatus} elapsed=${formatElapsed(elapsedMs)} state=${snapshot.stateDir}${summary}\n`);
  }
}

export function formatElapsed(elapsedMs: number): string {
  const seconds = Math.max(0, Math.floor(elapsedMs / 1000));
  const minutes = Math.floor(seconds / 60);
  return minutes > 0 ? `${minutes}m${seconds % 60}s` : `${seconds}s`;
}

export function exitCodeForStatus(status: RunStatus): number {
  return status === 'succeeded' || status === 'paused' ? 0 : 1;
}
