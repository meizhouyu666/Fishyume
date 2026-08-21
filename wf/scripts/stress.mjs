#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
export const STRESS_PACKAGES = Object.freeze([
  './internal/run',
  './internal/store',
  './internal/controlplane',
  './internal/driver/codexprocess',
]);

export function parseArgs(args) {
  let count = 20;
  let timeout = '10m';
  let dryRun = false;
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg === '--dry-run') dryRun = true;
    else if (arg === '--count') count = args[++i];
    else if (arg.startsWith('--count=')) count = arg.slice('--count='.length);
    else if (arg === '--timeout') timeout = args[++i];
    else if (arg.startsWith('--timeout=')) timeout = arg.slice('--timeout='.length);
    else if (arg === '--help' || arg === '-h') return { help: true };
    else throw new Error(`unknown argument: ${arg}`);
  }
  if (!/^\d+$/.test(String(count)) || Number(count) < 1) throw new Error('--count must be a positive integer');
  if (!/^[0-9]+(?:ms|s|m|h)$/.test(String(timeout))) throw new Error('--timeout must be a Go duration (for example 10m)');
  return { count: Number(count), timeout: String(timeout), dryRun };
}

export function commandFor(options) {
  return { command: 'go', args: ['test', `-count=${options.count}`, '-timeout', options.timeout, ...STRESS_PACKAGES] };
}

export function commandsFor(options) {
  return STRESS_PACKAGES.map((packagePath) => ({
    command: 'go',
    args: ['test', `-count=${options.count}`, '-timeout', options.timeout, packagePath],
  }));
}

function run(command) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command.command, command.args, { cwd: resolve(repoRoot, 'wf-engine'), stdio: 'inherit', shell: false });
    child.once('error', rejectRun);
    child.once('exit', (code, signal) => code === 0 ? resolveRun() : rejectRun(new Error(`stress gate failed (${signal ? `signal ${signal}` : `exit ${code}`})`)));
  });
}

export async function main(args = process.argv.slice(2)) {
  const options = parseArgs(args);
  if (options.help) {
    console.log('Usage: node wf/scripts/stress.mjs [--count N] [--timeout DURATION] [--dry-run]');
    console.log(`Packages: ${STRESS_PACKAGES.join(' ')}`);
    return;
  }
  const commands = commandsFor(options);
  console.log(`=== Deterministic stress gate (${options.count} repetitions) ===`);
  for (const command of commands) {
    console.log(`$ ${command.command} ${command.args.join(' ')}`);
    if (!options.dryRun) await run(command);
  }
  console.log('Stress gate passed.');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`Stress gate failed: ${error.message}`);
    process.exitCode = 1;
  });
}
