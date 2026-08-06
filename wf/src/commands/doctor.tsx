import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

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

export class DoctorCommand extends Command {
  static paths = [['doctor']];
  project = Option.String('--project');
  driver = Option.String('--driver');
  backend = Option.String('--backend');

  async execute(): Promise<number> {
    if (this.driver && this.backend && this.driver !== (this.backend === 'direct' ? 'codex' : this.backend)) {
      this.context.stderr.write('--driver conflicts with deprecated --backend\n');
      return 6;
    }
    if (this.backend) this.context.stderr.write('warning: --backend is deprecated; use --driver\n');
    return runDoctor(new EngineBridge(), this.project, this.driver ?? (this.backend === 'direct' ? 'codex' : this.backend), this.context.stdout);
  }
}
