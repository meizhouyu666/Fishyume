import {Command, Option} from 'clipanion';
import {EngineBridge} from '../bridge/engine.js';
import {runProductDoctor} from './doctor.js';
import {setupCodex} from './setup.js';
import {callRouting, configApiVersion} from '../bridge/routing.js';

export class SetupProductCommand extends Command {
  static paths = [['setup']];
  static usage = Command.Usage({description: 'Set up Fishyume for the local Codex Host and verify readiness. Connect Fishyume to Codex as a local stdio MCP server.'});
  printOnly = Option.Boolean('--print', false, {description: 'Print the copyable Codex setup command without changing configuration'});
  force = Option.Boolean('--force', false, {description: 'Replace a conflicting Codex MCP entry named fishyume'});

  async execute(): Promise<number> {
    if (this.printOnly) return setupCodex(this.context.stdout, {printOnly: true});
    this.context.stdout.write('Fishyume setup: configuring the local Codex Host\n');
    const setupStatus = await setupCodex(this.context.stdout, {force: this.force});
    if (setupStatus !== 0) return setupStatus;
    this.context.stdout.write('Fishyume setup: discovering local Team Agents\n');
    const client = new EngineBridge();
    try {await callRouting(client, 'team.routes.refresh', {schemaVersion: configApiVersion})}
    finally {await client.close()}
    this.context.stdout.write('Fishyume setup: checking readiness\n');
    return runProductDoctor(new EngineBridge(), undefined, 'codex', this.context.stdout);
  }
}
