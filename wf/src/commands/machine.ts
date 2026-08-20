import {Command, Option} from 'clipanion';
import {ApplicationCallError, callApplication, type ApplicationMethod} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

const methods = new Set<ApplicationMethod>(['system.capabilities', 'routing.catalog', 'workflow.validate', 'workflow.explain', 'run.start', 'run.list', 'run.get', 'run.events', 'run.action', 'run.result', 'memory.create', 'memory.get', 'memory.list', 'memory.supersede', 'memory.delete']);

export async function runMachine(client: EngineClient, method: string, paramsText: string | undefined, output: {write(text: string): unknown}): Promise<number> {
  try {
    if (!methods.has(method as ApplicationMethod)) throw new Error(`unsupported Application method ${method}`);
    const params = paramsText ? JSON.parse(paramsText) as unknown : {};
    const response = await callApplication(client, method as ApplicationMethod, params);
    output.write(`${JSON.stringify(response)}\n`);
    return 0;
  } catch (error) {
    const applicationError = error instanceof ApplicationCallError ? error.applicationError : {code: 'invalid_argument', message: error instanceof Error ? error.message : String(error)};
    output.write(`${JSON.stringify({error: applicationError})}\n`);
    return 6;
  } finally {await client.close()}
}

export class MachineCommand extends Command {
  static paths = [['machine']];
  static usage = Command.Usage({description: 'Call one Agent-facing Application API method and print one JSON response.'});
  method = Option.String({required: true, name: 'Application-method'});
  params = Option.String('--params', {description: 'Application request as one JSON object; defaults to {}'});
  async execute(): Promise<number> {return runMachine(new EngineBridge(), this.method, this.params, this.context.stdout)}
}
