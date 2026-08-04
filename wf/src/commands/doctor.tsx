import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

interface Writer { write(text: string): unknown }

export async function runDoctor(client: EngineClient, project: string | undefined, output: Writer): Promise<number> {
  try {
    const hello = await client.hello(project);
    output.write(`ok engine ${hello.engineVersion} started\n`);
    output.write(`ok protocol ${hello.protocolVersion} compatible\n`);
    output.write(`${hello.backendReady ? 'ok' : 'fail'} backend ${hello.backendDiagnostic}\n`);
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

  async execute(): Promise<number> {
    return runDoctor(new EngineBridge(), this.project, this.context.stdout);
  }
}
