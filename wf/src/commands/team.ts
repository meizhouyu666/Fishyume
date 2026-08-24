import {randomUUID} from 'node:crypto';
import process from 'node:process';
import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callTeam, teamApiVersion, TeamCallError, type Contribution, type ParticipantSpec, type TeamGetResponse, type TeamMessagesResponse, type TeamStartRequest} from '../bridge/team.js';

type Writer = {write(text: string): unknown};

export function parseParticipant(value: string): ParticipantSpec {
  const separator = value.lastIndexOf(':');
  if (separator < 1 || separator === value.length - 1) throw new Error('--participant must be modelId:label');
  const modelId = value.slice(0, separator).trim();
  const label = value.slice(separator + 1).trim();
  const knownRoles: Record<string, string> = {architect: 'propose a coherent architecture and tradeoffs', reviewer: 'challenge assumptions and identify failure modes'};
  return {modelId, label, role: knownRoles[label] ?? `provide an independent ${label} perspective on the topic`};
}

export async function startTeam(client: EngineClient, request: TeamStartRequest, options: {detach?: boolean; json?: boolean; pollMs?: number}, output: Writer): Promise<number> {
  let interrupted = false;
  const detach = (): void => {interrupted = true};
  try {
    const started = await callTeam(client, 'team.start', request);
    if (options.detach) {
      output.write(options.json ? `${JSON.stringify(started)}\n` : `Team ${started.team.teamId} started.\n`);
      return 0;
    }
    process.once('SIGINT', detach);
    let view: TeamGetResponse;
    for (;;) {
      view = await callTeam(client, 'team.get', {schemaVersion: teamApiVersion, teamId: started.team.teamId});
      if (view.team.state === 'closed' || interrupted) break;
      await new Promise(resolve => setTimeout(resolve, options.pollMs ?? 100));
    }
    if (interrupted) {
      output.write(options.json ? `${JSON.stringify({schemaVersion: teamApiVersion, teamId: started.team.teamId, detached: true})}\n` : `Detached from Team ${started.team.teamId}.\n`);
      return 0;
    }
    const messages = await callTeam(client, 'team.messages', {schemaVersion: teamApiVersion, teamId: started.team.teamId, limit: 100});
    if (options.json) output.write(`${JSON.stringify({schemaVersion: teamApiVersion, team: view, messages})}\n`);
    else output.write(renderTeam(view, messages));
    return 0;
  } catch (error) {
    const detail = error instanceof TeamCallError ? error.teamError : {code: 'internal', message: error instanceof Error ? error.message : String(error)};
    output.write(`${JSON.stringify({error: detail})}\n`);
    return 6;
  } finally {
    process.off('SIGINT', detach);
    await client.close();
  }
}

export function renderTeam(view: TeamGetResponse, messages: TeamMessagesResponse): string {
  const lines = [`Team ${view.team.teamId}  ${view.team.state}${view.team.closeReason ? ` (${view.team.closeReason})` : ''}`, view.team.topic, ''];
  const byID = new Map(view.team.participants.map(participant => [participant.participantId, participant]));
  for (const message of messages.messages) {
    if (message.kind !== 'participant_contribution') continue;
    const participant = byID.get(message.actor);
    let contribution: Contribution | undefined;
    try {contribution = JSON.parse(message.content) as Contribution} catch { /* Engine contract validation owns malformed data */ }
    lines.push(`${participant?.label ?? message.actor}  ${participant?.modelId ?? ''}  ${contribution?.status ?? 'invalid'}`.trimEnd());
    lines.push(contribution?.contentMarkdown ?? '[invalid contribution]');
    for (const warning of contribution?.warnings ?? []) lines.push(`Warning: ${warning}`);
    for (const question of contribution?.openQuestions ?? []) lines.push(`Open question: ${question}`);
    lines.push('');
  }
  for (const turn of view.turns) {
    if (turn.state === 'responded') continue;
    const participant = byID.get(turn.participantId);
    lines.push(`${participant?.label ?? turn.participantId}  ${turn.modelId}  ${turn.state}${turn.diagnostic ? `: ${turn.diagnostic}` : ''}`);
  }
  return `${lines.join('\n').trimEnd()}\n`;
}

export class TeamStartCommand extends Command {
  static paths = [['team', 'start']];
  static usage = Command.Usage({description: 'Run a durable read-only multi-model Panel and print independent contributions.'});
  topic = Option.String({required: true, name: 'topic'});
  project = Option.String('--project', {description: 'Repository directory; defaults to the current directory'});
  instructions = Option.String('--instructions', {description: 'Additional bounded exploration constraints'});
  participants = Option.Array('--participant', {description: 'Explicit modelId:label; repeatable, requires 2-4 distinct models'});
  costGrant = Option.String('--cost-grant', {description: 'Coarse trusted catalog cost grant'});
  detach = Option.Boolean('--detach', false, {description: 'Return after durable dispatch preparation'});
  json = Option.Boolean('--json', false, {description: 'Print one canonical JSON response'});
  async execute(): Promise<number> {
    try {
      const participants = this.participants?.map(parseParticipant);
      const costGrant = this.costGrant === undefined ? undefined : Number(this.costGrant);
      if (costGrant !== undefined && (!Number.isSafeInteger(costGrant) || costGrant < 1)) throw new Error('--cost-grant must be a positive integer');
      return startTeam(new EngineBridge(), {schemaVersion: teamApiVersion, clientRequestId: `team-start-${randomUUID()}`, project: this.project ?? process.cwd(), mode: 'panel', topic: this.topic, instructions: this.instructions, participants, costGrant}, {detach: this.detach, json: this.json}, this.context.stdout);
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6}
  }
}

export class TeamListCommand extends Command {
  static paths = [['team', 'list']];
  static usage = Command.Usage({description: 'List durable Teams in stable bounded pages.'});
  project = Option.String('--project');
  state = Option.String('--state');
  cursor = Option.String('--cursor');
  limit = Option.String('--limit');
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    const client = new EngineBridge();
    try {
      const limit = this.limit === undefined ? undefined : Number(this.limit);
      if (limit !== undefined && !Number.isSafeInteger(limit)) throw new Error('--limit must be an integer');
      const response = await callTeam(client, 'team.list', {schemaVersion: teamApiVersion, project: this.project, state: this.state, cursor: this.cursor, limit});
      if (this.json) this.context.stdout.write(`${JSON.stringify(response)}\n`);
      else for (const item of response.items) this.context.stdout.write(`${item.teamId}  ${item.state}  ${item.topic}\n`);
      return 0;
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6} finally {await client.close()}
  }
}

export class TeamShowCommand extends Command {
  static paths = [['team', 'show']];
  static usage = Command.Usage({description: 'Show one durable Team and its canonical public contributions.'});
  teamId = Option.String({required: true, name: 'team-id'});
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    const client = new EngineBridge();
    try {
      const view = await callTeam(client, 'team.get', {schemaVersion: teamApiVersion, teamId: this.teamId});
      const messages = await callTeam(client, 'team.messages', {schemaVersion: teamApiVersion, teamId: this.teamId, limit: 100});
      this.context.stdout.write(this.json ? `${JSON.stringify({schemaVersion: teamApiVersion, team: view, messages})}\n` : renderTeam(view, messages));
      return 0;
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6} finally {await client.close()}
  }
}

export class TeamCancelCommand extends Command {
  static paths = [['team', 'cancel']];
  static usage = Command.Usage({description: 'Confirm cancellation of active Team turns and close the Team.'});
  teamId = Option.String({required: true, name: 'team-id'});
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    const client = new EngineBridge();
    try {
      const current = await callTeam(client, 'team.get', {schemaVersion: teamApiVersion, teamId: this.teamId});
      const response = await callTeam(client, 'team.action', {schemaVersion: teamApiVersion, actionId: `team-cancel-${randomUUID()}`, teamId: this.teamId, expectedStateVersion: current.team.stateVersion, type: 'cancel'});
      this.context.stdout.write(this.json ? `${JSON.stringify(response)}\n` : `Team ${response.teamId} ${response.state}.\n`);
      return 0;
    } catch (error) {this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); return 6} finally {await client.close()}
  }
}
