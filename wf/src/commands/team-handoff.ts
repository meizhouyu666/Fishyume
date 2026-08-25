import {randomUUID} from 'node:crypto';
import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callTeam, type HandoffArtifact, type TeamMessagesResponse, teamApiVersion} from '../bridge/team.js';

type Writer = {write(text: string): unknown};

export interface HandoffCreateOptions {
  handoffId?: string;
  goal: string;
  messages?: string[];
  decisions?: string[];
  constraints?: string[];
  openQuestions?: string[];
  acceptanceExpectations?: string[];
  json?: boolean;
}

export async function readAllTeamMessages(client: EngineClient, teamId: string): Promise<TeamMessagesResponse['messages']> {
  const messages: TeamMessagesResponse['messages'] = [];
  let afterSequence = 0;
  for (;;) {
    const page = await callTeam(client, 'team.messages', {schemaVersion: teamApiVersion, teamId, afterSequence, limit: 100});
    messages.push(...page.messages);
    if (!page.more) return messages;
    if (page.nextAfterSequence <= afterSequence) throw new Error('team.messages cursor did not advance');
    afterSequence = page.nextAfterSequence;
  }
}

export async function createTeamHandoff(client: EngineClient, teamId: string, options: HandoffCreateOptions, output: Writer): Promise<void> {
  const [view, messages] = await Promise.all([
    callTeam(client, 'team.get', {schemaVersion: teamApiVersion, teamId}),
    readAllTeamMessages(client, teamId),
  ]);
  const selectedMessageIds = options.messages?.length
    ? options.messages
    : messages.filter(message => message.kind === 'participant_contribution').map(message => message.messageId);
  if (selectedMessageIds.length === 0) throw new Error('no participant contribution messages are available for this Handoff');
  const response = await callTeam(client, 'team.handoff.create', {
    schemaVersion: teamApiVersion,
    handoffId: options.handoffId ?? `handoff-${randomUUID()}`,
    teamId,
    expectedStateVersion: view.team.stateVersion,
    goal: options.goal,
    decisions: options.decisions,
    constraints: options.constraints,
    openQuestions: options.openQuestions,
    acceptanceExpectations: options.acceptanceExpectations,
    selectedMessageIds,
  });
  output.write(options.json
    ? `${JSON.stringify(response)}\n`
    : `Handoff ${response.handoff.handoffId} ${response.replayed ? 'replayed' : 'created'} from ${response.handoff.selectedMessageIds.length} messages.\nContent hash: ${response.handoff.contentHash}\n`);
}

export function renderHandoff(handoff: HandoffArtifact, binding?: {runId: string; project: string; boundAt: string}): string {
  const lines = [`Handoff ${handoff.handoffId}`, `Goal: ${handoff.goal}`, `Source Team: ${handoff.teamId} @ state ${handoff.sourceTeamVersion}`];
  appendItems(lines, 'Decisions', handoff.decisions);
  appendItems(lines, 'Constraints', handoff.constraints);
  appendItems(lines, 'Open questions', handoff.openQuestions);
  appendItems(lines, 'Acceptance', handoff.acceptanceExpectations);
  lines.push(`Selected messages: ${handoff.selectedMessageIds.join(', ')}`, `Content hash: ${handoff.contentHash}`);
  lines.push(binding ? `Bound Run: ${binding.runId} (${binding.project})` : 'Bound Run: none');
  lines.push('', 'Host promotion sequence:',
    `1. team.handoff.get (${handoff.teamId}, ${handoff.handoffId})`,
    '2. Host authors a fishyume/v2 Workflow from the Handoff.',
    '3. workflow.validate',
    '4. workflow.explain',
    '5. User confirms, then run.start.',
    '6. team.handoff.bindRun');
  return `${lines.join('\n')}\n`;
}

export async function listTeamHandoffs(client: EngineClient, teamId: string, options: {cursor?: string; limit?: number; json?: boolean}, output: Writer): Promise<void> {
  const response = await callTeam(client, 'team.handoff.list', {schemaVersion: teamApiVersion, teamId, cursor: options.cursor, limit: options.limit});
  if (options.json) output.write(`${JSON.stringify(response)}\n`);
  else for (const handoff of response.items) output.write(`${handoff.handoffId}  ${handoff.contentHash}  ${handoff.goal}\n`);
}

export async function showTeamHandoff(client: EngineClient, teamId: string, handoffId: string, json: boolean, output: Writer): Promise<void> {
  const response = await callTeam(client, 'team.handoff.get', {schemaVersion: teamApiVersion, teamId, handoffId});
  output.write(json ? `${JSON.stringify(response)}\n` : renderHandoff(response.handoff, response.binding));
}

export async function bindTeamHandoff(client: EngineClient, teamId: string, handoffId: string, runId: string, options: {actionId?: string; json?: boolean}, output: Writer): Promise<void> {
  const current = await callTeam(client, 'team.get', {schemaVersion: teamApiVersion, teamId});
  const response = await callTeam(client, 'team.handoff.bindRun', {
    schemaVersion: teamApiVersion, actionId: options.actionId ?? `handoff-bind-${randomUUID()}`,
    teamId, handoffId, runId, expectedStateVersion: current.team.stateVersion,
  });
  output.write(options.json
    ? `${JSON.stringify(response)}\n`
    : `Handoff ${response.binding.handoffId} ${response.replayed ? 'remains bound' : 'bound'} to Run ${response.binding.runId}.\n`);
}

function appendItems(lines: string[], label: string, items?: string[]): void {
  if (!items?.length) return;
  lines.push(`${label}:`);
  for (const item of items) lines.push(`- ${item}`);
}

abstract class HandoffCommand extends Command {
  protected async withClient(action: (client: EngineClient) => Promise<void>): Promise<number> {
    const client = new EngineBridge();
    try {
      await action(client);
      return 0;
    } catch (error) {
      this.context.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
      return 6;
    } finally {
      await client.close();
    }
  }
}

export class TeamHandoffCreateCommand extends HandoffCommand {
  static paths = [['team', 'handoff', 'create']];
  static usage = Command.Usage({description: 'Create an immutable Handoff from retained Team contributions.'});
  teamId = Option.String({required: true, name: 'team-id'});
  goal = Option.String('--goal', {required: true});
  handoffId = Option.String('--handoff-id');
  messages = Option.Array('--message');
  decisions = Option.Array('--decision');
  constraints = Option.Array('--constraint');
  openQuestions = Option.Array('--open-question');
  acceptanceExpectations = Option.Array('--acceptance');
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    return this.withClient(client => createTeamHandoff(client, this.teamId, {
      handoffId: this.handoffId, goal: this.goal, messages: this.messages, decisions: this.decisions,
      constraints: this.constraints, openQuestions: this.openQuestions,
      acceptanceExpectations: this.acceptanceExpectations, json: this.json,
    }, this.context.stdout));
  }
}

export class TeamHandoffListCommand extends HandoffCommand {
  static paths = [['team', 'handoff', 'list']];
  static usage = Command.Usage({description: 'List immutable Handoffs for one Team.'});
  teamId = Option.String({required: true, name: 'team-id'});
  cursor = Option.String('--cursor');
  limit = Option.String('--limit');
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    return this.withClient(async client => {
      const limit = this.limit === undefined ? undefined : Number(this.limit);
      if (limit !== undefined && (!Number.isSafeInteger(limit) || limit < 1 || limit > 100)) throw new Error('--limit must be an integer from 1 to 100');
      await listTeamHandoffs(client, this.teamId, {cursor: this.cursor, limit, json: this.json}, this.context.stdout);
    });
  }
}

export class TeamHandoffShowCommand extends HandoffCommand {
  static paths = [['team', 'handoff', 'show']];
  static usage = Command.Usage({description: 'Show one Handoff, its Run binding, and the explicit Host promotion sequence.'});
  teamId = Option.String({required: true, name: 'team-id'});
  handoffId = Option.String({required: true, name: 'handoff-id'});
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    return this.withClient(async client => {
      await showTeamHandoff(client, this.teamId, this.handoffId, this.json, this.context.stdout);
    });
  }
}

export class TeamHandoffBindCommand extends HandoffCommand {
  static paths = [['team', 'handoff', 'bind']];
  static usage = Command.Usage({description: 'Bind one Handoff to one existing same-project Workflow Run.'});
  teamId = Option.String({required: true, name: 'team-id'});
  handoffId = Option.String({required: true, name: 'handoff-id'});
  runId = Option.String({required: true, name: 'run-id'});
  actionId = Option.String('--action-id');
  json = Option.Boolean('--json', false);
  async execute(): Promise<number> {
    return this.withClient(async client => {
      await bindTeamHandoff(client, this.teamId, this.handoffId, this.runId, {actionId: this.actionId, json: this.json}, this.context.stdout);
    });
  }
}
