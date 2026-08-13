#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const npmCommand = process.platform === 'win32'
  ? { command: process.execPath, args: [resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js')] }
  : { command: 'npm', args: [] };

const STEP_DEFINITIONS = Object.freeze([
  { name: 'go-test', label: 'Go tests', cwd: 'wf-engine', command: 'go', args: ['test', './...', '-count=1', '-timeout', '10m'] },
  { name: 'go-vet', label: 'Go vet', cwd: 'wf-engine', command: 'go', args: ['vet', './...'] },
  { name: 'go-build', label: 'Go build', cwd: 'wf-engine', command: 'go', args: ['build', './cmd/wf-engine'] },
  { name: 'typescript', label: 'TypeScript verify', cwd: '.', command: npmCommand.command, args: [...npmCommand.args, '--prefix', 'wf', 'run', 'verify'] },
  { name: 'diff-check', label: 'Git diff check', cwd: '.', command: 'git', args: ['diff', '--check'] },
]);

export function parseArgs(args) {
  let step = 'all';
  let dryRun = false;
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg === '--dry-run') {
      dryRun = true;
    } else if (arg === '--step') {
      step = args[++i];
      if (!step) throw new Error('--step requires a value');
    } else if (arg.startsWith('--step=')) {
      step = arg.slice('--step='.length);
    } else if (arg === '--help' || arg === '-h') {
      return { help: true };
    } else {
      throw new Error(`unknown argument: ${arg}`);
    }
  }
  if (step !== 'all' && !STEP_DEFINITIONS.some((candidate) => candidate.name === step)) {
    throw new Error(`unknown step: ${step}`);
  }
  return { step, dryRun };
}

export function selectSteps(step) {
  return step === 'all' ? [...STEP_DEFINITIONS] : STEP_DEFINITIONS.filter((candidate) => candidate.name === step);
}

function formatCommand(definition) {
  return [definition.command, ...definition.args].join(' ');
}

function run(definition) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(definition.command, definition.args, {
      cwd: resolve(repoRoot, definition.cwd),
      stdio: 'inherit',
      shell: false,
    });
    child.once('error', rejectRun);
    child.once('exit', (code, signal) => {
      if (code === 0) resolveRun();
      else rejectRun(new Error(`${definition.label} failed (${signal ? `signal ${signal}` : `exit ${code}`})`));
    });
  });
}

export async function main(args = process.argv.slice(2)) {
  const options = parseArgs(args);
  if (options.help) {
    console.log('Usage: node wf/scripts/preflight.mjs [--step NAME] [--dry-run]');
    console.log(`Steps: ${STEP_DEFINITIONS.map((definition) => definition.name).join(', ')}`);
    return;
  }
  for (const definition of selectSteps(options.step)) {
    console.log(`\n=== ${definition.label} ===`);
    console.log(`$ ${formatCommand(definition)}`);
    if (!options.dryRun) await run(definition);
  }
  console.log('\nPreflight passed.');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`Preflight failed: ${error.message}`);
    process.exitCode = 1;
  });
}
