import process from 'node:process';
import React from 'react';
import {Command, Option} from 'clipanion';
import {render, type Instance} from 'ink';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {RunEvent, RunSnapshot, RunStartResult, RunStatus} from '../bridge/types.js';
import {RunApp} from '../tui/run-app.js';
import {exitCodeForStatus, TextReporter, type TextWriter} from '../tui/text-reporter.js';

const terminal = new Set<RunStatus>(['succeeded', 'failed', 'blocked', 'indeterminate', 'paused', 'cancelled']);

export async function runWorkflow(
  client: EngineClient,
  options: {project: string; tool: string; runtime: string; task: string; useTUI: boolean},
  output: TextWriter,
): Promise<number> {
  const startedAt = Date.now();
  const hello = await client.hello(options.project);
  if (!hello.backendReady || (hello.projectChecked && !hello.projectReady)) {
    output.write(`fail backend ${hello.projectDiagnostic ?? hello.backendDiagnostic}\n`);
    await client.close();
    return 1;
  }

  let current: RunSnapshot | undefined;
  let ink: Instance | undefined;
  const reporter = new TextReporter(output);
  let settle!: (event: RunEvent) => void;
  const terminalEvent = new Promise<RunEvent>(resolve => { settle = resolve; });
  const unsubscribe = client.onRunEvent(event => {
    if (current && event.runId !== current.id) return;
    if (options.useTUI && current) {
      current = {...current, status: event.status, nodeStatus: event.nodeStatus, summary: event.message, updatedAt: event.timestamp};
      ink?.rerender(<RunApp snapshot={current} startedAt={startedAt}/>);
    } else {
      reporter.event(event);
    }
    if (terminal.has(event.status)) settle(event);
  });

  const started = await client.call<RunStartResult>('run.start', options);
  current = await client.call<RunSnapshot>('run.get', {runId: started.runId});
  if (options.useTUI) ink = render(<RunApp snapshot={current} startedAt={startedAt}/>);
  else reporter.started(current);
  if (terminal.has(current.status)) settle({protocolVersion: 1, runId: current.id, sequence: 0, type: 'run.current', status: current.status, nodeStatus: current.nodeStatus, timestamp: current.updatedAt});

  let detaching = false;
  const onInterrupt = (): void => {
    if (detaching) return;
    detaching = true;
    void client.call<RunSnapshot>('run.detach', {runId: started.runId}).then(snapshot => {
      current = snapshot;
      settle({protocolVersion: 1, runId: snapshot.id, sequence: 0, type: 'run.paused', status: snapshot.status, nodeStatus: snapshot.nodeStatus, message: snapshot.summary, timestamp: snapshot.updatedAt});
    });
  };
  process.once('SIGINT', onInterrupt);

  try {
    await terminalEvent;
    current = await client.call<RunSnapshot>('run.get', {runId: started.runId});
    if (options.useTUI) ink?.rerender(<RunApp snapshot={current} startedAt={startedAt}/>);
    else reporter.finished(current, Date.now() - startedAt);
    return exitCodeForStatus(current.status);
  } finally {
    process.off('SIGINT', onInterrupt);
    unsubscribe();
    ink?.unmount();
    await client.close();
  }
}

export class RunCommand extends Command {
  static paths = [['run']];
  project = Option.String('--project');
  tool = Option.String('--tool', 'codex');
  runtime = Option.String('--runtime', 'local');
  task = Option.Rest({required: 1});

  async execute(): Promise<number> {
    if (!['codex', 'claude', 'opencode'].includes(this.tool)) {
      this.context.stderr.write(`unsupported tool ${this.tool}\n`);
      return 1;
    }
    if (!['local', 'wsl', 'ssh'].includes(this.runtime)) {
      this.context.stderr.write(`unsupported runtime ${this.runtime}\n`);
      return 1;
    }
    const useTUI = Boolean(process.stdout.isTTY && !process.env.NO_COLOR && !process.env.CI);
    return runWorkflow(new EngineBridge(), {
      project: this.project ?? process.cwd(), tool: this.tool, runtime: this.runtime,
      task: this.task.join(' '), useTUI,
    }, this.context.stdout);
  }
}
