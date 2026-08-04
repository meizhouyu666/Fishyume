import process from 'node:process';
import {readFile} from 'node:fs/promises';
import {basename} from 'node:path';
import React from 'react';
import {Command, Option} from 'clipanion';
import {render, type Instance} from 'ink';
import {EngineBridge, EngineRpcError, type EngineClient} from '../bridge/engine.js';
import type {JsonScalar, RunEvent, RunStartResult, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {RunApp} from '../tui/run-app.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter} from '../tui/text-reporter.js';

const controllerStops = new Set(['waiting', 'paused', 'completed']);

export interface RunOptions {project: string; tool: string; runtime: string; task?: string; workflow?: string; inputs?: Record<string, JsonScalar>; useTUI: boolean}

export async function runWorkflow(client: EngineClient, options: RunOptions, output: TextWriter): Promise<number> {
  const startedAt = Date.now();
  try {
    const hello = await client.hello(options.project);
    if (!hello.backendReady || (hello.projectChecked && !hello.projectReady)) {output.write(`fail backend ${hello.projectDiagnostic ?? hello.backendDiagnostic}\n`); return 1}
    let current: WorkflowSnapshot | undefined; let ink: Instance | undefined; const reporter = new TextReporter(output);
    let settle!: () => void; const stopped = new Promise<void>(resolve => {settle = resolve});
    const unsubscribe = client.onRunEvent(event => {
      if (current && event.runId !== current.id) return;
      if (current) {
        current = {...current, phase: event.phase, conclusion: event.conclusion, reason: event.reason, summary: event.message, updatedAt: event.timestamp,
          ...(event.nodeId && current.nodes[event.nodeId] ? {nodes: {...current.nodes, [event.nodeId]: {...current.nodes[event.nodeId], phase: event.nodePhase ?? current.nodes[event.nodeId].phase}}} : {})};
        if (options.useTUI) ink?.rerender(<RunApp snapshot={current} startedAt={startedAt}/>); else reporter.event(event);
      }
      if (controllerStops.has(event.phase)) settle();
    });
    const started = options.workflow
      ? await client.call<RunStartResult>('run.startWorkflow', {project: options.project, filename: basename(options.workflow), content: await readFile(options.workflow, 'utf8'), inputs: options.inputs})
      : await client.call<RunStartResult>('run.start', {project: options.project, tool: options.tool, runtime: options.runtime, task: options.task});
    const initial = await client.call<RunStatusView>('run.status', {runId: started.runId});
    if (!initial.run) throw new Error('new run returned legacy or missing status');
    current = initial.run;
    if (options.useTUI) ink = render(<RunApp snapshot={current} startedAt={startedAt}/>); else reporter.started(current);
    if (controllerStops.has(current.phase)) settle();
    let detaching = false;
    const onInterrupt = (): void => {if (detaching) return; detaching = true; void client.call<WorkflowSnapshot>('run.detach', {runId: started.runId}).then(snapshot => {current = snapshot; settle()})};
    process.once('SIGINT', onInterrupt);
    try {
      await stopped;
      const finalView = await client.call<RunStatusView>('run.status', {runId: started.runId});
      if (!finalView.run) throw new Error('run status disappeared'); current = finalView.run;
      if (options.useTUI) ink?.rerender(<RunApp snapshot={current} startedAt={startedAt}/>); else reporter.finished(current, Date.now()-startedAt);
      return exitCodeForSnapshot(current);
    } finally {process.off('SIGINT', onInterrupt); unsubscribe(); ink?.unmount()}
  } catch (error) {
    output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`);
    return error instanceof EngineRpcError && error.code === -32009 ? 7 : 6;
  } finally {await client.close()}
}

export function parseInputValues(values: string[] | undefined, fileValues: Record<string, unknown> = {}): Record<string, JsonScalar> {
  const result: Record<string, JsonScalar> = {};
  for (const [key, value] of Object.entries(fileValues)) {if (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') throw new Error(`input ${key} must be a JSON scalar`); result[key] = value}
  for (const item of values ?? []) {
    const separator = item.indexOf('='); if (separator < 1) throw new Error(`invalid --input ${item}; expected key=value`);
    const key = item.slice(0, separator); const raw = item.slice(separator+1); let value: unknown = raw;
    try {value = JSON.parse(raw)} catch {value = raw}
    if (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') throw new Error(`input ${key} must be a JSON scalar`);
    result[key] = value;
  }
  return result;
}

export class RunCommand extends Command {
  static paths = [['run']];
  project = Option.String('--project'); tool = Option.String('--tool', 'codex'); runtime = Option.String('--runtime', 'local');
  workflow = Option.String('--workflow'); input = Option.Array('--input'); inputsFile = Option.String('--inputs'); task = Option.Rest();
  async execute(): Promise<number> {
    if (!['codex', 'claude', 'opencode'].includes(this.tool)) {this.context.stderr.write(`unsupported tool ${this.tool}\n`); return 6}
    if (!['local', 'wsl', 'ssh'].includes(this.runtime)) {this.context.stderr.write(`unsupported runtime ${this.runtime}\n`); return 6}
    if (Boolean(this.workflow) === Boolean(this.task.length)) {this.context.stderr.write('provide exactly one of --workflow or an ad-hoc task\n'); return 6}
    try {
      const fileValues = this.inputsFile ? JSON.parse(await readFile(this.inputsFile, 'utf8')) as Record<string, unknown> : {};
      const inputs = parseInputValues(this.input, fileValues); const useTUI = Boolean(process.stdout.isTTY && !process.env.NO_COLOR && !process.env.CI);
      return runWorkflow(new EngineBridge(), {project: this.project ?? process.cwd(), tool: this.tool, runtime: this.runtime, task: this.task.join(' '), workflow: this.workflow, inputs, useTUI}, this.context.stdout);
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6}
  }
}
