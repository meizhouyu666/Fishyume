import process from 'node:process';
import React from 'react';
import {render, type Instance} from 'ink';
import type {EngineClient} from '../bridge/engine.js';
import type {ResumeAction, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {RunApp} from './run-app.js';

export type LiveConsoleMode = 'run' | 'watch';
export interface ActionResult {accepted: boolean; ok: boolean; message: string}

export interface LiveConsoleControllerOptions {
  mode: LiveConsoleMode;
  pollIntervalMs?: number;
  onView(view: RunStatusView): void;
}

export class LiveConsoleController {
  readonly #client: EngineClient;
  readonly #runId: string;
  readonly #mode: LiveConsoleMode;
  readonly #pollIntervalMs: number;
  readonly #onView: (view: RunStatusView) => void;
  #view?: RunStatusView;
  #closed = false;
  #pendingMutation = false;
  #mayOwnController: boolean;
  #revision = 0;
  #refreshChain: Promise<void> = Promise.resolve();
  #pollTimer?: NodeJS.Timeout;
  #unsubscribe?: () => void;
  #mutationTask?: Promise<ActionResult>;
  #detachTask?: Promise<void>;

  constructor(client: EngineClient, runId: string, options: LiveConsoleControllerOptions) {
    this.#client = client;
    this.#runId = runId;
    this.#mode = options.mode;
    this.#mayOwnController = options.mode === 'run';
    this.#pollIntervalMs = options.pollIntervalMs ?? 1000;
    this.#onView = options.onView;
  }

  get view(): RunStatusView | undefined {return this.#view}
  get pendingMutation(): boolean {return this.#pendingMutation}

  async start(): Promise<RunStatusView> {
    if (this.#mode === 'run') this.#unsubscribe = this.#client.onRunEvent(event => this.#handleEvent(event));
    await this.refresh();
    if (!this.#view?.run) throw new Error('interactive Run Console requires a current protocol v2 run');
    return this.#view;
  }

  refresh(): Promise<void> {
    const revision = ++this.#revision;
    const task = this.#refreshChain.then(async () => {
      if (this.#closed) return;
      const view = await this.#client.call<RunStatusView>('run.status', {runId: this.#runId});
      if (this.#closed || revision !== this.#revision) return;
      if (!view.run || view.run.id !== this.#runId) throw new Error(`run ${this.#runId} returned a missing or mismatched status`);
      this.#view = view;
      this.#onView(view);
      this.#schedulePoll();
    });
    this.#refreshChain = task.catch(() => undefined);
    return task;
  }

  async resume(action: ResumeAction): Promise<ActionResult> {
    return this.#mutate('run.resume', {runId: this.#runId, action}, `${action.type} ${action.nodeId}`);
  }

  async cancel(): Promise<ActionResult> {
    return this.#mutate('run.cancel', {runId: this.#runId}, 'cancel run');
  }

  detach(): Promise<void> {
    if (!this.#mayOwnController) return Promise.resolve();
    if (this.#detachTask) return this.#detachTask;
    const task = this.#performDetach();
    this.#detachTask = task;
    void task.then(
      () => {if (this.#detachTask === task) this.#detachTask = undefined},
      () => {if (this.#detachTask === task) this.#detachTask = undefined},
    );
    return task;
  }

  async #performDetach(): Promise<void> {
    await this.#mutationTask?.catch(() => undefined);
    if (this.#view?.run?.phase === 'completed') {this.#mayOwnController = false; return}
    ++this.#revision;
    try {await this.#client.call<WorkflowSnapshot>('run.detach', {runId: this.#runId})}
    finally {this.#mayOwnController = false}
  }

  async close(): Promise<void> {
    if (this.#closed) return;
    await this.#mutationTask?.catch(() => undefined);
    await this.detach().catch(() => undefined);
    this.#closed = true;
    ++this.#revision;
    if (this.#pollTimer) clearTimeout(this.#pollTimer);
    this.#pollTimer = undefined;
    this.#unsubscribe?.();
    this.#unsubscribe = undefined;
    await this.#refreshChain;
  }

  #mutate(method: 'run.resume' | 'run.cancel', params: unknown, label: string): Promise<ActionResult> {
    if (this.#pendingMutation) return Promise.resolve({accepted: false, ok: false, message: 'ignored · another action is already pending'});
    this.#pendingMutation = true;
    const task = this.#executeMutation(method, params, label);
    this.#mutationTask = task;
    void task.finally(() => {if (this.#mutationTask === task) this.#mutationTask = undefined});
    return task;
  }

  async #executeMutation(method: 'run.resume' | 'run.cancel', params: unknown, label: string): Promise<ActionResult> {
    ++this.#revision;
    try {
      await this.#client.call<WorkflowSnapshot>(method, params);
      if (method === 'run.resume') this.#mayOwnController = true;
      return {accepted: true, ok: true, message: `${label} submitted`};
    } catch (error) {
      return {accepted: true, ok: false, message: `${label} failed · ${error instanceof Error ? error.message : String(error)}`};
    } finally {
      try {await this.refresh()} catch (error) {
        this.#pendingMutation = false;
        return {accepted: true, ok: false, message: `${label} refresh failed · ${error instanceof Error ? error.message : String(error)}`};
      }
      this.#pendingMutation = false;
    }
  }

  #handleEvent(event: RunEvent): void {
    if (this.#closed || event.runId !== this.#runId) return;
    const current = this.#view?.run;
    if (current) {
      const run: WorkflowSnapshot = {
        ...current,
        phase: event.phase,
        conclusion: event.conclusion,
        reason: event.reason,
        summary: event.message,
        updatedAt: event.timestamp,
        ...(event.nodeId && current.nodes[event.nodeId]
          ? {nodes: {...current.nodes, [event.nodeId]: {...current.nodes[event.nodeId], phase: event.nodePhase ?? current.nodes[event.nodeId].phase}}}
          : {}),
      };
      this.#view = {...this.#view!, run};
      this.#onView(this.#view);
    }
    void this.refresh().catch(() => undefined);
  }

  #schedulePoll(): void {
    if (this.#mode !== 'watch' || this.#closed) return;
    if (this.#pollTimer) clearTimeout(this.#pollTimer);
    this.#pollTimer = undefined;
    if (this.#view?.run?.phase === 'completed') return;
    this.#pollTimer = setTimeout(() => {
      this.#pollTimer = undefined;
      void this.refresh().catch(() => this.#schedulePoll());
    }, this.#pollIntervalMs);
  }
}

export interface RunLiveConsoleOptions {runId: string; mode: LiveConsoleMode; startedAt?: number; pollIntervalMs?: number}

export async function runLiveConsole(client: EngineClient, options: RunLiveConsoleOptions): Promise<RunStatusView> {
  let latest: RunStatusView | undefined;
  let ink: Instance | undefined;
  let leaving = false;
  let settle!: () => void;
  const stopped = new Promise<void>(resolve => {settle = resolve});
  const controller = new LiveConsoleController(client, options.runId, {
    mode: options.mode,
    pollIntervalMs: options.pollIntervalMs,
    onView(view) {
      latest = view;
      if (ink) ink.rerender(<RunApp view={view} startedAt={options.startedAt ?? (Date.parse(view.run?.createdAt ?? '') || Date.now())} onResume={action => controller.resume(action)} onCancel={() => controller.cancel()} onExit={() => {void leave()}}/>);
    },
  });
  const leave = async (): Promise<void> => {
    if (leaving) return;
    leaving = true;
    try {await controller.detach()} catch { /* Closing the bridge still releases the local controller. */ }
    settle();
  };
  const onInterrupt = (): void => {void leave()};
  process.once('SIGINT', onInterrupt);
  try {
    const initial = await controller.start();
    latest = initial;
    const startedAt = options.startedAt ?? (Date.parse(initial.run?.createdAt ?? '') || Date.now());
    ink = render(<RunApp view={initial} startedAt={startedAt} onResume={action => controller.resume(action)} onCancel={() => controller.cancel()} onExit={() => {void leave()}}/>, {exitOnCtrlC: false});
    await stopped;
    return latest;
  } finally {
    process.off('SIGINT', onInterrupt);
    const mounted = ink;
    ink = undefined;
    mounted?.unmount();
    await controller.close();
  }
}
