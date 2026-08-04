#!/usr/bin/env node
import {Cli} from 'clipanion';
import {DoctorCommand} from './commands/doctor.js';
import {RunCommand} from './commands/run.js';

const cli = new Cli({binaryLabel: 'wf', binaryName: 'wf', binaryVersion: '0.1.0'});
cli.register(DoctorCommand);
cli.register(RunCommand);
await cli.runExit(process.argv.slice(2), Cli.defaultContext);
