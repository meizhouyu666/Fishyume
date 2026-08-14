import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {commandDiagnostic, isFishyumeMcpConfiguration, isMissingMcpServer, runCodex, type CodexRunner} from './codex-cli.js';

interface Writer { write(text: string): unknown }

export async function runDoctor(client: EngineClient, project: string | undefined, driver: string | undefined, output: Writer): Promise<number> {
  try {
    const hello = await client.hello(project, driver);
    output.write(`ok engine ${hello.engineVersion} started\n`);
    output.write(`ok protocol ${hello.protocolVersion} compatible\n`);
    output.write(`${hello.backendReady ? 'ok' : 'fail'} driver ${hello.backendDiagnostic}\n`);
    if (hello.projectChecked) {
      output.write(`${hello.projectReady ? 'ok' : 'fail'} project ${hello.projectDiagnostic ?? project}\n`);
    }
    return hello.backendReady && (!hello.projectChecked || hello.projectReady) ? 0 : 1;
  } catch (error) {
    output.write(`fail engine ${error instanceof Error ? error.message : String(error)}\n`);
    return 1;
  } finally {
    await client.close();
  }
}

export function checkCodexHost(output: Writer, runner: CodexRunner = runCodex): number {
  const version = runner(['--version']);
  if (version.status !== 0) {
    output.write(`fail codex CLI unavailable: ${commandDiagnostic(version)}\nRun: npm install -g @openai/codex\n`);
    return 1;
  }
  const label = version.stdout.trim().split(/\r?\n/, 1)[0] || 'available';
  output.write(`ok codex ${label}\n`);

  let failed = false;
  const login = runner(['login', 'status']);
  if (login.status === 0) output.write('ok codex-login authenticated\n');
  else {
    failed = true;
    output.write('fail codex-login authentication is not ready\nRun: codex login\n');
  }

  const mcp = runner(['mcp', 'get', 'fishyume', '--json']);
  if (mcp.status === 0 && isFishyumeMcpConfiguration(mcp.stdout)) output.write('ok codex-mcp Fishyume stdio server configured\n');
  else {
    failed = true;
    const reason = isMissingMcpServer(mcp) ? 'Fishyume is not configured' : mcp.status === 0 ? 'fishyume points to a different command' : `configuration check failed: ${commandDiagnostic(mcp)}`;
    output.write(`fail codex-mcp ${reason}\nRun: fishyume setup codex${mcp.status === 0 ? ' --force' : ''}\n`);
  }
  return failed ? 1 : 0;
}

export async function runProductDoctor(client: EngineClient, project: string | undefined, driver: string | undefined, output: Writer, runner: CodexRunner = runCodex): Promise<number> {
  const engine = await runDoctor(client, project, driver, output);
  const host = checkCodexHost(output, runner);
  return engine || host ? 1 : 0;
}

export class DoctorCommand extends Command {
  static paths = [['doctor']];
  static usage = Command.Usage({description: 'Check the Engine, Application protocol, Driver, and optional project readiness.'});
  project = Option.String('--project', {description: 'Project directory to check'});
  driver = Option.String('--driver', {description: 'Agent Driver to check (currently codex)'});
  backend = Option.String('--backend', {description: 'Deprecated compatibility alias for --driver'});

  async execute(): Promise<number> {
    if (this.driver && this.backend && this.driver !== (this.backend === 'direct' ? 'codex' : this.backend)) {
      this.context.stderr.write('--driver conflicts with deprecated --backend\n');
      return 6;
    }
    if (this.backend) this.context.stderr.write('warning: --backend is deprecated; use --driver\n');
    return runProductDoctor(new EngineBridge(), this.project, this.driver ?? (this.backend === 'direct' ? 'codex' : this.backend), this.context.stdout);
  }
}
