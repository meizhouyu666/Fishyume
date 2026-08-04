#!/usr/bin/env node
import {Builtins, Cli} from 'clipanion';
import {DoctorCommand} from './commands/doctor.js';
import {RunCommand} from './commands/run.js';
import {StatusCommand} from './commands/status.js';
import {ResumeCommand} from './commands/resume.js';
import {CancelCommand} from './commands/cancel.js';

const cli = new Cli({binaryLabel: 'Fishyume', binaryName: 'fishyume', binaryVersion: '0.2.1-alpha.1'});
cli.register(Builtins.HelpCommand);
cli.register(Builtins.VersionCommand);
cli.register(DoctorCommand);
cli.register(RunCommand);
cli.register(StatusCommand);
cli.register(ResumeCommand);
cli.register(CancelCommand);
await cli.runExit(process.argv.slice(2), Cli.defaultContext);
