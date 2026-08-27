import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import test from 'node:test';
import {createWebOpenManager, resolveLocalWebEntrypoint, type WebLauncher, type WebProcessLike, type WebTarget} from './web.js';

class FakeProcess extends EventEmitter implements WebProcessLike {
  exitCode: number | null = null;
  killed = false;
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  kill(): boolean {this.killed = true; this.exitCode = 0; this.emit('exit', 0); return true}
}

function launcher(process: FakeProcess): WebLauncher {
  return {command: 'fishyume-web', spawn: () => process};
}

test('web.open starts one sidecar and focuses later targets through the sidecar control channel', async () => {
  const process = new FakeProcess();
  const focused: WebTarget[] = [];
  const manager = createWebOpenManager({
    launcher: launcher(process),
    fetchFocus: async (_origin, _token, target) => {if (target) focused.push(target); return Boolean(target)},
    waitMs: 100,
  });
  const first = manager.open({kind: 'team', teamId: 'team-1'});
  process.stdout.emit('data', 'Fishyume Web: http://127.0.0.1:63995/#token=token-value\n');
  assert.deepEqual(await first, {status: 'opened', target: {kind: 'team', teamId: 'team-1'}});
  assert.deepEqual(await manager.open({kind: 'run', runId: 'run-1'}), {status: 'focused', target: {kind: 'run', runId: 'run-1'}});
  assert.deepEqual(focused, [{kind: 'run', runId: 'run-1'}]);
  await manager.close();
  assert.equal(process.killed, true);
});

test('web.open returns unavailable when the optional client cannot start', async () => {
  const manager = createWebOpenManager({
    launcher: {command: 'missing-fishyume-web', spawn: () => {throw new Error('not installed')}} as WebLauncher,
    waitMs: 10,
  });
  assert.deepEqual(await manager.open({kind: 'team', teamId: 'team-1'}), {status: 'unavailable', target: {kind: 'team', teamId: 'team-1'}, reason: 'not installed'});
  await manager.close();
});

test('web.open prepends launcher arguments for a local Node entrypoint', async () => {
  const fakeProcess = new FakeProcess();
  let command = '';
  let args: string[] = [];
  const manager = createWebOpenManager({
    launcher: {command: globalThis.process.execPath, argsPrefix: ['local-server.js'], spawn(resolvedCommand, resolvedArgs) {command = resolvedCommand; args = resolvedArgs; return fakeProcess}} as WebLauncher,
    waitMs: 100,
  });
  const opening = manager.open({kind: 'run', runId: 'run-1'});
  fakeProcess.stdout.emit('data', 'Fishyume Web: http://127.0.0.1:63995/#token=token-value\n');
  await opening;
  assert.equal(command, globalThis.process.execPath);
  assert.deepEqual(args, ['local-server.js', '--target-kind', 'run', '--run-id', 'run-1']);
  await manager.close();
});

test('local Web entrypoint resolves from the compiled wf package directory', () => {
  assert.equal(resolveLocalWebEntrypoint('E:/meizhouyu/agentstudy/my-agent/wf/dist/mcp')?.replaceAll('\\', '/'), 'E:/meizhouyu/agentstudy/my-agent/fishyume-web/dist/server.js');
});
