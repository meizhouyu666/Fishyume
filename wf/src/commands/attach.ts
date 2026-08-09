import process from 'node:process';
import {Command, Option} from 'clipanion';
import {EngineBridge} from '../bridge/engine.js';
import {watchStatus} from './status.js';
import {shouldUseTUI} from './run.js';

export class AttachCommand extends Command {
  static paths = [['attach']];
  runId = Option.String({required: true});
  async execute(): Promise<number> {
    if (!shouldUseTUI(process.stdout.isTTY, process.env)) {
      this.context.stderr.write('fishyume attach requires an interactive TTY outside CI\n');
      return 6;
    }
    return watchStatus(new EngineBridge(), this.runId, this.context.stdout);
  }
}
