#!/usr/bin/env node
import {Builtins, Cli} from 'clipanion';
import {DoctorCommand} from './commands/doctor.js';
import {RunCommand} from './commands/run.js';
import {StatusCommand} from './commands/status.js';
import {ResumeCommand} from './commands/resume.js';
import {CancelCommand} from './commands/cancel.js';
import {AttachCommand} from './commands/attach.js';
import {MachineCommand} from './commands/machine.js';
import {MCPCommand} from './commands/mcp.js';
import {DashboardCommand} from './commands/dashboard.js';
import {SetupCodexCommand} from './commands/setup.js';

const cli = new Cli({binaryLabel: 'Fishyume', binaryName: 'fishyume', binaryVersion: '0.2.1-alpha.1'});
cli.register(Builtins.HelpCommand);
cli.register(Builtins.VersionCommand);
cli.register(DashboardCommand);
cli.register(SetupCodexCommand);
cli.register(DoctorCommand);
cli.register(RunCommand);
cli.register(StatusCommand);
cli.register(ResumeCommand);
cli.register(CancelCommand);
cli.register(AttachCommand);
cli.register(MachineCommand);
cli.register(MCPCommand);
const args = process.argv.slice(2);
await cli.runExit(args.length === 0 ? ['dashboard'] : args, Cli.defaultContext);
