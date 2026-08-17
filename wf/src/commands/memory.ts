import process from 'node:process';
import {open} from 'node:fs/promises';
import {Command, Option} from 'clipanion';
import {ApplicationCallError, callApplication, type ApplicationMethod, type MemoryCreateRequest, type MemoryDeleteRequest, type MemoryListRequest, type MemorySupersedeRequest} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

type Writer = {write(text: string): unknown};

export async function runMemoryCall(client: EngineClient, method: ApplicationMethod, params: unknown, output: Writer): Promise<number> {
  try {
    const response = await callApplication(client, method, params);
    output.write(`${JSON.stringify(response)}\n`);
    return 0;
  } catch (error) {
    const applicationError = error instanceof ApplicationCallError ? error.applicationError : {code: 'internal', message: error instanceof Error ? error.message : String(error)};
    output.write(`${JSON.stringify({error: applicationError})}\n`);
    return 6;
  } finally {await client.close()}
}

async function readStdin(): Promise<string> {
  const chunks: Buffer[] = [];
  let bytes = 0;
  for await (const chunk of process.stdin) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk));
    bytes += buffer.byteLength;
    if (bytes > 16 * 1024) throw new Error('Memory content exceeds 16 KiB');
    chunks.push(buffer);
  }
  return decodeContent(Buffer.concat(chunks));
}

async function memoryContent(file: string | undefined, stdin: boolean): Promise<string> {
  if (Boolean(file) === stdin) throw new Error('provide exactly one of --file or --stdin; inline content is intentionally unsupported');
  if (!file) return readStdin();
  const handle = await open(file, 'r');
  try {
    const metadata = await handle.stat();
    if (!metadata.isFile() || metadata.size > 16 * 1024) throw new Error('Memory content file must be a regular file no larger than 16 KiB');
    const bounded = Buffer.allocUnsafe(16 * 1024 + 1);
    const {bytesRead} = await handle.read(bounded, 0, bounded.byteLength, 0);
    if (bytesRead > 16 * 1024) throw new Error('Memory content file must be a regular file no larger than 16 KiB');
    return decodeContent(bounded.subarray(0, bytesRead));
  } finally {await handle.close()}
}

function decodeContent(content: Uint8Array): string {
  try {return new TextDecoder('utf-8', {fatal: true}).decode(content)}
  catch {throw new Error('Memory content must be valid UTF-8')}
}

function parseBoundedInteger(value: string | undefined, name: string): number | undefined {
  if (value === undefined) return undefined;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${name} must be an integer`);
  return parsed;
}

abstract class MemoryWriteCommand extends Command {
  project = Option.String('--project', {description: 'Project directory whose canonical identity owns the Memory'});
  mutationId = Option.String('--mutation-id', {required: true, description: 'Durable idempotency identity for this exact mutation'});
  reason = Option.String('--reason', {required: true, description: 'Bounded audit reason'});
}

abstract class MemoryContentCommand extends MemoryWriteCommand {
  type = Option.String('--type', {required: true, description: 'decision, constraint, fact, procedure, or preference'});
  sensitivity = Option.String('--sensitivity', 'project', {description: 'public or project; sensitive Memory is rejected'});
  file = Option.String('--file', {description: 'Read Memory content from this UTF-8 file'});
  stdin = Option.Boolean('--stdin', false, {description: 'Read Memory content from stdin'});
  expiresAt = Option.String('--expires-at', {description: 'Optional RFC3339 expiry; no background mutation is performed'});
  maxUses = Option.String('--max-uses', {description: 'Optional bounded eligibility limit; M5.2 does not increment useCount'});
  async requestBase(): Promise<MemoryCreateRequest> {
    return {project: this.project ?? process.cwd(), mutationId: this.mutationId, type: this.type as MemoryCreateRequest['type'], content: await memoryContent(this.file, this.stdin), sensitivity: this.sensitivity as MemoryCreateRequest['sensitivity'], reason: this.reason, expiresAt: this.expiresAt, maxUses: parseBoundedInteger(this.maxUses, '--max-uses')};
  }
}

export class MemoryCreateCommand extends MemoryContentCommand {
  static paths = [['memory', 'create']];
  static usage = Command.Usage({description: 'Create active project Memory as the fixed user writer; content comes from stdin or a file.'});
  async execute(): Promise<number> {
    try {const request = await this.requestBase(); return runMemoryCall(new EngineBridge(), 'memory.create', request, this.context.stdout)}
    catch (error) {this.context.stdout.write(`${JSON.stringify({error: {code: 'invalid_argument', message: error instanceof Error ? error.message : String(error)}})}\n`); return 6}
  }
}

export class MemoryGetCommand extends Command {
  static paths = [['memory', 'get']];
  static usage = Command.Usage({description: 'Read one full project Memory record.'});
  recordId = Option.String({required: true, name: 'record-id'});
  project = Option.String('--project', {description: 'Project directory'});
  async execute(): Promise<number> {return runMemoryCall(new EngineBridge(), 'memory.get', {project: this.project ?? process.cwd(), recordId: this.recordId}, this.context.stdout)}
}

export class MemoryListCommand extends Command {
  static paths = [['memory', 'list']];
  static usage = Command.Usage({description: 'List bounded, cursor-paginated project Memory metadata without bulk content.'});
  project = Option.String('--project', {description: 'Project directory'});
  type = Option.String('--type');
  state = Option.String('--state');
  sensitivity = Option.String('--sensitivity');
  writer = Option.String('--writer');
  cursor = Option.String('--cursor');
  limit = Option.String('--limit');
  async execute(): Promise<number> {
    try {
      const filter = {type: this.type as MemoryListRequest['filter'] extends infer F ? F extends {type?: infer T} ? T : never : never, state: this.state as 'active' | 'superseded' | 'deleted' | undefined, sensitivity: this.sensitivity as 'public' | 'project' | undefined, writer: this.writer as 'user' | 'host_agent' | 'migration' | undefined};
      const request: MemoryListRequest = {project: this.project ?? process.cwd(), filter, cursor: this.cursor, limit: parseBoundedInteger(this.limit, '--limit')};
      return runMemoryCall(new EngineBridge(), 'memory.list', request, this.context.stdout);
    } catch (error) {this.context.stdout.write(`${JSON.stringify({error: {code: 'invalid_argument', message: error instanceof Error ? error.message : String(error)}})}\n`); return 6}
  }
}

export class MemorySupersedeCommand extends MemoryContentCommand {
  static paths = [['memory', 'supersede']];
  static usage = Command.Usage({description: 'Atomically create a replacement and supersede 1..16 active project Memories as the fixed user writer.'});
  supersedes = Option.Array('--supersedes', {description: 'Record ID to supersede; repeatable'});
  async execute(): Promise<number> {
    try {
      if (!this.supersedes?.length) throw new Error('provide at least one --supersedes record ID');
      const request: MemorySupersedeRequest = {...await this.requestBase(), supersedes: this.supersedes};
      return runMemoryCall(new EngineBridge(), 'memory.supersede', request, this.context.stdout);
    } catch (error) {this.context.stdout.write(`${JSON.stringify({error: {code: 'invalid_argument', message: error instanceof Error ? error.message : String(error)}})}\n`); return 6}
  }
}

export class MemoryDeleteCommand extends MemoryWriteCommand {
  static paths = [['memory', 'delete']];
  static usage = Command.Usage({description: 'Delete active or superseded project Memory by creating a plaintext-free tombstone as the fixed user writer.'});
  recordId = Option.String({required: true, name: 'record-id'});
  async execute(): Promise<number> {
    const request: MemoryDeleteRequest = {project: this.project ?? process.cwd(), mutationId: this.mutationId, recordId: this.recordId, reason: this.reason};
    return runMemoryCall(new EngineBridge(), 'memory.delete', request, this.context.stdout);
  }
}
