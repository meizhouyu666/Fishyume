import process from 'node:process';
import {randomUUID} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {basename} from 'node:path';
import {Command, Option} from 'clipanion';
import {applicationRunToStatus, callApplication, type JsonScalar, type RunGetResponse, type RunStartResponse, type WorkflowInput} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {WorkflowSnapshot} from '../bridge/types.js';
import {runLiveConsole} from '../tui/live-console.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter} from '../tui/text-reporter.js';

const controllerStops = new Set(['waiting', 'paused', 'completed']);

export interface RunOptions {project: string; driver?: string; target?: string; backend?: string; tool?: 'codex' | 'claude' | 'opencode'; runtime?: 'local' | 'wsl' | 'ssh'; task?: string; workflow?: string; inputs?: Record<string, JsonScalar>; useTUI: boolean}

export function resolveRunSelection(workflow: string | undefined, driver: string | undefined, target: string | undefined, legacy: boolean): Pick<RunOptions, 'driver' | 'target'> {
  const useAdHocDefaults = !workflow && !legacy;
  return {
    driver: (driver ?? (useAdHocDefaults ? 'codex' : undefined)) as RunOptions['driver'],
    target: (target ?? (useAdHocDefaults ? 'local' : undefined)) as RunOptions['target'],
  };
}

export function shouldUseTUI(isTTY: boolean | undefined, environment: NodeJS.ProcessEnv = process.env): boolean {
  return Boolean(isTTY && !environment.CI);
}

export async function runWorkflow(client: EngineClient, options: RunOptions, output: TextWriter): Promise<number> {
  const startedAt = Date.now();
  try {
    const workflowSelectsDriver = Boolean(options.workflow && !options.driver && !options.backend);
    const selectedDriver = options.driver ?? (options.backend === 'direct' ? 'codex' : options.backend);
    const hello = await client.hello(workflowSelectsDriver ? undefined : options.project, selectedDriver);
    if (!workflowSelectsDriver && (!hello.backendReady || (hello.projectChecked && !hello.projectReady))) {output.write(`fail driver ${hello.projectDiagnostic ?? hello.backendDiagnostic}\n`); return 1}
    const driver = options.driver ?? (options.backend === 'direct' ? 'codex' : options.backend) ?? options.tool;
    const target = options.target ?? options.runtime;
    const start = async (): Promise<RunStartResponse> => {
      const workflow: WorkflowInput = options.workflow
        ? {source: {filename: basename(options.workflow), content: await readFile(options.workflow, 'utf8')}}
        : {document: {apiVersion: 'fishyume/v2', name: 'ad-hoc', defaults: {agent: {driver: driver ?? 'codex', target: target ?? 'local'}}, execution: {maxConcurrency: 1}, nodes: {'agent-1': {type: 'agent', task: options.task}}}};
      return callApplication(client, 'run.start', {project: options.project, workflow, inputs: options.inputs, ...(driver ? {driver} : {}), ...(target ? {target} : {}), clientRequestId: `cli-start-${randomUUID()}`});
    };
    if (options.useTUI) {
      const started = await start();
      const view = await runLiveConsole(client, {runId: started.runId, mode: 'run', startedAt});
      return view.run ? exitCodeForSnapshot(view.run) : 6;
    }
    let current: WorkflowSnapshot | undefined; const reporter = new TextReporter(output);
    let settle!: () => void; const stopped = new Promise<void>(resolve => {settle = resolve});
    const unsubscribe = client.onRunEvent(event => {
      if (current && event.runId !== current.id) return;
      if (current) {
        current = {...current, phase: event.phase, conclusion: event.conclusion, reason: event.reason, summary: event.message, updatedAt: event.timestamp,
          ...(event.nodeId && current.nodes[event.nodeId] ? {nodes: {...current.nodes, [event.nodeId]: {...current.nodes[event.nodeId], phase: event.nodePhase ?? current.nodes[event.nodeId].phase}}} : {})};
        reporter.event(event);
      }
      if (controllerStops.has(event.phase)) settle();
    });
    const started = await start();
    const initial = applicationRunToStatus(await callApplication(client, 'run.get', {runId: started.runId}) as RunGetResponse);
    if (!initial.run) throw new Error('new run returned legacy or missing status');
    current = initial.run; reporter.started(current);
    if (controllerStops.has(current.phase)) settle();
    let detaching = false;
    const onInterrupt = (): void => {if (detaching) return; detaching = true; settle()};
    process.once('SIGINT', onInterrupt);
    try {
      await stopped;
      const finalView = applicationRunToStatus(await callApplication(client, 'run.get', {runId: started.runId}) as RunGetResponse);
      if (!finalView.run) throw new Error('run status disappeared'); current = finalView.run;
      reporter.finished(current, Date.now()-startedAt);
      return exitCodeForSnapshot(current);
    } finally {process.off('SIGINT', onInterrupt); unsubscribe()}
  } catch (error) {
    output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`);
    return 6;
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
  static usage = Command.Usage({description: 'Start an ad-hoc task or Workflow Run through the local Control Plane.'});
  project = Option.String('--project', {description: 'Project directory used for Agent execution'});
  driver = Option.String('--driver', {description: 'Agent Driver override (currently codex)'});
  target = Option.String('--target', {description: 'Driver target override (currently local)'});
  backend = Option.String('--backend', {description: 'Deprecated compatibility alias for --driver'});
  tool = Option.String('--tool', {description: 'Deprecated compatibility Agent tool selection'});
  runtime = Option.String('--runtime', {description: 'Deprecated compatibility target selection'});
  workflow = Option.String('--workflow', {description: 'Workflow YAML or JSON file'});
  input = Option.Array('--input', {description: 'Workflow input as key=value; repeatable'});
  inputsFile = Option.String('--inputs', {description: 'JSON object containing Workflow inputs'});
  task = Option.Rest({name: 'task'});
  async execute(): Promise<number> {
    if (this.driver && this.driver !== 'codex') {this.context.stderr.write(`unsupported driver ${this.driver}\n`); return 6}
    if (this.target && this.target !== 'local') {this.context.stderr.write(`unsupported target ${this.target}\n`); return 6}
    if (this.tool && !['codex', 'claude', 'opencode'].includes(this.tool)) {this.context.stderr.write(`unsupported tool ${this.tool}\n`); return 6}
    if (this.runtime && !['local', 'wsl', 'ssh'].includes(this.runtime)) {this.context.stderr.write(`unsupported runtime ${this.runtime}\n`); return 6}
    if (Boolean(this.workflow) === Boolean(this.task.length)) {this.context.stderr.write('provide exactly one of --workflow or an ad-hoc task\n'); return 6}
    try {
      const fileValues = this.inputsFile ? JSON.parse(await readFile(this.inputsFile, 'utf8')) as Record<string, unknown> : {};
      const legacy = Boolean(this.backend || this.tool || this.runtime);
      if (legacy) this.context.stderr.write('warning: --backend/--tool/--runtime are deprecated; use --driver/--target\n');
      const inputs = parseInputValues(this.input, fileValues); const useTUI = shouldUseTUI(process.stdout.isTTY);
      const {driver, target} = resolveRunSelection(this.workflow, this.driver, this.target, legacy);
      return runWorkflow(new EngineBridge(), {project: this.project ?? process.cwd(), driver, target, backend: this.backend, tool: this.tool as RunOptions['tool'], runtime: this.runtime as RunOptions['runtime'], task: this.task.join(' '), workflow: this.workflow, inputs, useTUI}, this.context.stdout);
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6}
  }
}
