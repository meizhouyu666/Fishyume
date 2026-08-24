import {Command, Option} from 'clipanion';
import {ApplicationCallError, applicationMethods, callApplication, type ApplicationMethod} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callTeam, TeamCallError, teamMethods, type TeamMethod} from '../bridge/team.js';

const methods = new Set<string>([...applicationMethods, ...teamMethods]);

export async function runMachine(client: EngineClient, method: string, paramsText: string | undefined, output: {write(text: string): unknown}): Promise<number> {
  try {
    if (!methods.has(method)) throw new Error(`unsupported Fishyume method ${method}`);
    const params = paramsText ? JSON.parse(paramsText) as unknown : {};
    const response = method.startsWith('team.') ? await callTeam(client, method as TeamMethod, params) : await callApplication(client, method as ApplicationMethod, params);
    output.write(`${JSON.stringify(response)}\n`);
    return 0;
  } catch (error) {
    const applicationError = error instanceof ApplicationCallError ? error.applicationError : error instanceof TeamCallError ? error.teamError : {code: 'invalid_argument', message: error instanceof Error ? error.message : String(error)};
    output.write(`${JSON.stringify({error: applicationError})}\n`);
    return 6;
  } finally {await client.close()}
}

export class MachineCommand extends Command {
  static paths = [['machine']];
  static usage = Command.Usage({description: 'Call one Agent-facing Application or Team API method and print one JSON response.'});
  method = Option.String({required: true, name: 'method'});
  params = Option.String('--params', {description: 'Request as one JSON object; defaults to {}'});
  async execute(): Promise<number> {return runMachine(new EngineBridge(), this.method, this.params, this.context.stdout)}
}
