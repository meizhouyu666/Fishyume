#!/usr/bin/env node
import {existsSync, readFileSync, writeFileSync} from 'node:fs';
import {createInterface} from 'node:readline';

const statePath = process.argv[2];
if (!statePath) throw new Error('fake app-server requires a state path');

const state = existsSync(statePath)
  ? JSON.parse(readFileSync(statePath, 'utf8'))
  : {thread: undefined, turns: [], marker: undefined, nextTurn: 1};

function persist() {
  writeFileSync(statePath, `${JSON.stringify(state)}\n`, 'utf8');
}

function emit(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function response(id, result) {
  emit({id, result});
}

function error(id, message) {
  emit({id, error: {code: -32000, message}});
}

function threadResponse(params) {
  return {
    thread: {
      ...state.thread,
      turns: state.turns.map((turn) => ({...turn, items: turn.items || []})),
    },
    model: params.model,
    modelProvider: 'openai',
    serviceTier: null,
    cwd: params.cwd,
    instructionSources: [],
    approvalPolicy: params.approvalPolicy,
    approvalsReviewer: 'user',
    sandbox: {type: 'readOnly', networkAccess: false},
    reasoningEffort: null,
  };
}

function completeTurn(turn, item) {
  if (item) {
    turn.items.push(item);
    emit({method: 'item/completed', params: {threadId: state.thread.id, turnId: turn.id, item, completedAtMs: Date.now()}});
  }
  turn.status = 'completed';
  persist();
  emit({method: 'turn/completed', params: {threadId: state.thread.id, turn}});
}

function handle(message) {
  const {id, method, params = {}} = message;
  if (method === 'initialize') {
    response(id, {userAgent: 'fake-codex-app-server', platformFamily: 'windows', platformOs: 'windows', codexHome: 'C:/fake'});
    return;
  }
  if (method === 'initialized') return;
  if (method === 'thread/start') {
    state.thread = {
      id: '0198-fake-thread',
      sessionId: '0198-fake-session',
      cwd: params.cwd,
      ephemeral: Boolean(params.ephemeral),
      modelProvider: 'openai',
      turns: [],
    };
    persist();
    response(id, threadResponse(params));
    return;
  }
  if (method === 'thread/resume') {
    if (!state.thread || params.threadId !== state.thread.id) {
      error(id, `thread ${params.threadId} was not found`);
      return;
    }
    response(id, threadResponse(params));
    return;
  }
  if (method === 'turn/start') {
    if (!state.thread || params.threadId !== state.thread.id) {
      error(id, `thread ${params.threadId} was not found`);
      return;
    }
    const turn = {id: `0198-fake-turn-${state.nextTurn++}`, status: 'inProgress', items: []};
    state.turns.push(turn);
    const prompt = params.input?.find((input) => input.type === 'text')?.text || '';
    const markerMatch = prompt.match(/FISHYUME-CONTINUITY-[0-9a-f-]+/i);
    if (markerMatch) state.marker = markerMatch[0];
    persist();
    response(id, {turn});
    if (prompt.includes('waits for 60 seconds')) {
      emit({
        method: 'item/started',
        params: {
          threadId: state.thread.id,
          turnId: turn.id,
          item: {type: 'commandExecution', id: `command-${turn.id}`, command: 'Start-Sleep -Seconds 60'},
        },
      });
      return;
    }
    if (markerMatch) {
      const command = {
        type: 'commandExecution',
        id: `command-${turn.id}`,
        command: 'Set-Content fishyume-read-only-denied.txt denied',
        cwd: state.thread.cwd,
        status: 'failed',
      };
      turn.items.push(command);
      emit({method: 'item/completed', params: {threadId: state.thread.id, turnId: turn.id, item: command, completedAtMs: Date.now()}});
      completeTurn(turn, {type: 'agentMessage', id: `message-${turn.id}`, text: `${state.marker} write denied`});
      return;
    }
    completeTurn(turn, {type: 'agentMessage', id: `message-${turn.id}`, text: state.marker});
    return;
  }
  if (method === 'turn/interrupt') {
    const turn = state.turns.find((candidate) => candidate.id === params.turnId);
    if (!turn || turn.status !== 'inProgress') {
      error(id, `turn ${params.turnId} is not active`);
      return;
    }
    response(id, {});
    turn.status = 'interrupted';
    persist();
    emit({method: 'turn/completed', params: {threadId: state.thread.id, turn}});
    return;
  }
  error(id, `unsupported fake app-server method ${method}`);
}

createInterface({input: process.stdin, crlfDelay: Infinity}).on('line', (line) => {
  if (!line.trim()) return;
  handle(JSON.parse(line));
});
