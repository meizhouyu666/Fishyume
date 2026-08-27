import {createIcons, MessagesSquare, Workflow, RefreshCw, Send, XCircle, Square, RotateCcw, Check, X, Link, GitBranch, Inbox, Route, Maximize2, Scan, Terminal, FileCode2, FileText, FileJson, FileCog, Wrench} from 'lucide';
import type {ApplicationEvent, ApplicationNodeView, RunEventsResponse, RunGetResponse, RunListResponse, RunSummary} from '../../../wf/src/bridge/application.js';
import type {HandoffArtifact, Participant, ParticipantTurn, TeamGetResponse, TeamListResponse, TeamMessage, TeamMessagesResponse, TeamSummary} from '../../../wf/src/bridge/team.js';
import type {DriverListResponse, EffectiveCatalogResponse, RoutingConfig, TeamRoutesResponse} from '../../../wf/src/bridge/routing.js';
import {findWorkflowNode, relatedWorkflowNodeIds, workflowEdges, workflowGraphLayout} from './workflow-view.js';
import {messageContent} from './team-view.js';

const teamVersion = 'fishyume.team/v1';
type View = 'teams' | 'runs' | 'routing';
type Filter = 'all' | 'active' | 'closed';
type DetailTab = 'discussion' | 'handoffs' | 'run';
interface ApiErrorShape {code: string; message: string}
class ApiError extends Error {constructor(readonly detail: ApiErrorShape) {super(detail.message)}}
const launch = readLaunchContext();
type FocusTarget = {kind: 'team'; teamId: string} | {kind: 'handoff'; teamId: string; handoffId: string} | {kind: 'run'; runId: string};

const state: {
  token: string; view: View; filter: Filter; tab: DetailTab; teams: TeamSummary[]; runs: RunSummary[];
  selectedTeam?: string; selectedRun?: string; selectedNode?: string; teamView?: TeamGetResponse; messages?: TeamMessagesResponse;
  handoffs: HandoffArtifact[]; runView?: RunGetResponse; busy: boolean; refreshing: boolean; focusRevision: number; pendingFocus?: FocusTarget;
  eventRunId?: string; eventCursor: number; lastRunEvent?: ApplicationEvent; eventsBusy: boolean; graphScale: 'actual' | 'fit';
  routingView?: EffectiveCatalogResponse; routingConfig?: RoutingConfig; teamRoutes?: TeamRoutesResponse; drivers?: DriverListResponse; routingErrors: string[];
} = {token: launch.token, view: launch.view, filter: 'all', tab: launch.target?.kind === 'handoff' ? 'handoffs' : 'discussion', teams: [], runs: [], handoffs: [], busy: false, refreshing: false, focusRevision: 0, pendingFocus: launch.target, eventCursor: 0, eventsBusy: false, graphScale: 'actual', routingErrors: []};

const collection = element('collection-list');
const detail = element('detail-pane');
const connection = document.querySelector('.connection-state') as HTMLElement;
const connectionLabel = element('connection-label');

function readLaunchContext(): {token: string; view: View; target?: FocusTarget} {
  const parameters = new URLSearchParams(location.hash.slice(1));
  const token = parameters.get('token');
  if (!token || token.length < 32) throw new Error('Fishyume Web launch token is missing');
  const kind = parameters.get('targetKind');
  const target: FocusTarget | undefined = kind === 'team' && parameters.get('teamId') ? {kind: 'team', teamId: parameters.get('teamId')!} : kind === 'handoff' && parameters.get('teamId') && parameters.get('handoffId') ? {kind: 'handoff', teamId: parameters.get('teamId')!, handoffId: parameters.get('handoffId')!} : kind === 'run' && parameters.get('runId') ? {kind: 'run', runId: parameters.get('runId')!} : undefined;
  const requested = parameters.get('view');
  const view: View = target?.kind === 'run' ? 'runs' : target ? 'teams' : requested === 'runs' || requested === 'routing' ? requested : 'teams';
  return {token, view, target};
}

async function rpc<T>(method: string, params: Record<string, unknown>): Promise<T> {
  const response = await fetch('/api/rpc', {
    method: 'POST', headers: {'Content-Type': 'application/json', Authorization: `Bearer ${state.token}`},
    body: JSON.stringify({method, params}),
  });
  const payload = await response.json() as {result?: T; error?: ApiErrorShape};
  if (!response.ok || payload.error) throw new ApiError(payload.error ?? {code: 'http_error', message: `Request failed (${response.status})`});
  connection.className = 'connection-state is-online';
  connectionLabel.textContent = '本地控制面';
  return payload.result as T;
}

async function refresh(): Promise<void> {
  if (state.busy || state.refreshing) return;
  state.refreshing = true;
  try {
    if (state.view === 'teams') await refreshTeams(); else if (state.view === 'runs') await refreshRuns(); else await refreshRouting();
  } catch (error) {showError(error)} finally {state.refreshing = false}
}

async function pollFocus(): Promise<void> {
  try {
    const response = await fetch('/api/focus', {method: 'POST', headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + state.token}, body: '{}'});
    if (!response.ok) return;
    const payload = await response.json() as {revision?: number; target?: FocusTarget};
    if (typeof payload.revision !== 'number' || payload.revision <= state.focusRevision || !payload.target) return;
    state.focusRevision = payload.revision;
    applyFocus(payload.target);
  } catch { /* the regular refresh reports connection state */ }
}

function applyFocus(target: FocusTarget): void {
  state.pendingFocus = target;
  if (target.kind === 'run') {state.view = 'runs'; state.selectedRun = target.runId; state.tab = 'run'; updateViewControls(); void refresh(); return}
  state.view = 'teams'; state.selectedTeam = target.teamId; state.tab = target.kind === 'handoff' ? 'handoffs' : 'discussion'; updateViewControls(); void refresh();
}

async function refreshTeams(): Promise<void> {
  const listed = await rpc<TeamListResponse>('team.list', {schemaVersion: teamVersion, limit: 100});
  state.teams = listed.items;
  if (!state.selectedTeam || !state.teams.some(team => team.teamId === state.selectedTeam)) state.selectedTeam = state.teams[0]?.teamId;
  renderCollection();
  if (state.selectedTeam) await loadTeam(state.selectedTeam); else renderEmptyDetail('messages-square', '暂无团队');
}

async function loadTeam(teamId: string): Promise<void> {
  const [view, messages, handoffs] = await Promise.all([
    rpc<TeamGetResponse>('team.get', {schemaVersion: teamVersion, teamId}),
    rpc<TeamMessagesResponse>('team.messages', {schemaVersion: teamVersion, teamId, limit: 100}),
    rpc<{items: HandoffArtifact[]}>('team.handoff.list', {schemaVersion: teamVersion, teamId, limit: 100}),
  ]);
  if (state.selectedTeam !== teamId) return;
  state.teamView = view; state.messages = messages; state.handoffs = handoffs.items;
  renderTeamDetail();
  const pending = state.pendingFocus;
  if (pending?.kind === 'handoff' && pending.teamId === teamId) {state.tab = 'handoffs'; renderTeamDetail(); state.pendingFocus = undefined}
  if (pending?.kind === 'team' && pending.teamId === teamId) state.pendingFocus = undefined;
}

async function refreshRuns(): Promise<void> {
  const listed = await rpc<RunListResponse>('run.list', {limit: 100});
  state.runs = listed.items;
  if (!state.selectedRun || !state.runs.some(run => run.runId === state.selectedRun)) state.selectedRun = state.runs[0]?.runId;
  renderCollection();
  if (state.selectedRun) await loadRun(state.selectedRun); else renderEmptyDetail('workflow', '暂无工作流运行');
}

async function refreshRouting(): Promise<void> {
  const methods = ['routing.catalog.effective', 'routing.config.get', 'team.routes.get', 'driver.list'] as const;
  const [effective, config, teamRoutes, drivers] = await Promise.allSettled([
    rpc<EffectiveCatalogResponse>('routing.catalog.effective', {schemaVersion: 'fishyume.config/v1'}),
    rpc<{config: RoutingConfig}>('routing.config.get', {schemaVersion: 'fishyume.config/v1'}),
    rpc<TeamRoutesResponse>('team.routes.get', {schemaVersion: 'fishyume.config/v1'}),
    rpc<DriverListResponse>('driver.list', {schemaVersion: 'fishyume.config/v1'}),
  ]);
  const results = [effective, config, teamRoutes, drivers];
  state.routingErrors = results.flatMap((result, index) => result.status === 'rejected' ? [`${methods[index]}: ${errorMessage(result.reason)}`] : []);
  state.routingView = effective.status === 'fulfilled' ? effective.value : undefined;
  state.routingConfig = config.status === 'fulfilled' ? config.value.config : undefined;
  state.teamRoutes = teamRoutes.status === 'fulfilled' ? teamRoutes.value : undefined;
  state.drivers = drivers.status === 'fulfilled' ? drivers.value : undefined;
  renderRoutingCollection(); renderRoutingDetail();
}

function renderRoutingCollection(): void {
  element('collection-eyebrow').textContent = '配置'; element('collection-title').textContent = 'Agent 路由';
  element('filter-row').style.display = 'none'; collection.replaceChildren();
  const drivers = state.drivers; if (!drivers) {collection.append(empty(state.routingErrors.length ? '当前 Engine 未提供路由信息' : '暂无本机 Agent')); return}
  if (!drivers.drivers.length) {collection.append(empty('未检测到本机 Agent')); return}
  for (const driver of drivers.drivers) {
    const item = div('collection-item route-summary');
    const top = div('item-topline'); top.append(text('span', 'item-title', driver.driver), status(driver.available ? 'available' : 'unavailable'));
    const meta = div('item-meta'); meta.append(text('span', '', driver.teamEligible ? 'Team 可用' : 'Team 不可用'), text('span', '', driver.workflowEligible ? 'Workflow + Team' : 'Team'));
    item.append(top, meta); collection.append(item);
  }
}

function renderRoutingDetail(): void {
  const view = state.routingView; const teamRoutes = state.teamRoutes; const drivers = state.drivers;
  detail.replaceChildren(); const header = div('detail-header'); const row = div('detail-title-row');
  const title = document.createElement('div'); title.append(text('span', 'eyebrow', `ROUTING · ${teamRoutes?.catalogHash?.slice(0, 12) || view?.catalogHash?.slice(0, 12) || 'LOCAL'}`), text('h2', 'detail-title', '本机 Agent 与模型路由'), text('div', 'detail-subtitle', teamRoutes ? `Team 配置修订 ${teamRoutes.config.revision} · Agent 认证由各自 CLI 管理` : '检查本机 Driver、Team 与 Workflow 的可用路由'));
  const actions = div('header-actions'); if (teamRoutes) actions.append(actionButton('refresh-cw', '刷新 Agent', 'refresh-team-routes')); if (view) actions.append(actionButton('refresh-cw', '刷新模型', 'discover-models'), actionButton('route', '主动探针', 'probe-models', 'primary')); row.append(title, actions); header.append(row); detail.append(header);
  detail.append(metrics([['本机 Agent', String(drivers?.drivers.filter(driver => driver.available).length ?? 0)], ['Team 路由', String(teamRoutes?.routes.filter(route => route.effective).length ?? 0)], ['Codex 已发现', String(view?.routes.filter(route => route.discovered).length ?? 0)], ['Workflow 可路由', String(view?.routes.filter(route => route.routable).length ?? 0)]]));
  const content = div('detail-content');
  if (state.routingErrors.length) content.append(routingNotice(state.routingErrors));
  const driverSection = div('section routing-table'); const driverHeading = div('section-title-row'); driverHeading.append(text('h3', 'section-title', '本机 Agent'), text('span', 'section-note', `${drivers?.drivers.length ?? 0} 个 Driver`)); driverSection.append(driverHeading);
  if (!drivers?.drivers.length) driverSection.append(empty('暂无可显示的本机 Agent'));
  for (const driver of drivers?.drivers ?? []) {
    const item = div('route-row driver-route-row'); const identity = div('route-identity'); identity.append(text('strong', '', driver.driver), text('span', 'section-note', `${driver.provider} · ${driver.modelCount} 个模型`));
    const states = div('route-states'); states.append(stateMark('Team', driver.teamEligible), stateMark('Workflow', driver.workflowEligible), status(driver.available ? 'available' : 'unavailable')); item.append(identity, states); driverSection.append(item);
  }
  content.append(driverSection);
  const teamSection = div('section routing-table'); const teamHeading = div('section-title-row'); teamHeading.append(text('h3', 'section-title', 'Team Agent 路由'), text('span', 'section-note', `${teamRoutes?.routes.length ?? 0} 条`)); teamSection.append(teamHeading);
  if (!teamRoutes?.routes.length) teamSection.append(empty('暂无 Team 路由'));
  for (const route of teamRoutes?.routes ?? []) {
    const item = div('route-row'); const identity = div('route-identity'); identity.append(text('strong', '', route.routeId), text('span', 'section-note', `${route.driver} / ${route.provider} / ${route.model}`));
    const states = div('route-states'); states.append(stateMark('已安装', route.driverAvailable), stateMark('已启用', route.enabled), status(route.effective ? 'available' : 'unavailable'));
    const toggle = actionButton(route.enabled ? 'x' : 'check', route.enabled ? '停用' : '启用', 'toggle-team-route', '', route.routeId); item.append(identity, states, toggle); teamSection.append(item);
  }
  content.append(teamSection);
  const section = div('section routing-table'); const workflowHeading = div('section-title-row'); workflowHeading.append(text('h3', 'section-title', 'Codex Workflow 路由'), text('span', 'section-note', `${view?.routes.length ?? 0} 条`)); section.append(workflowHeading);
  if (!view?.routes.length) section.append(empty('暂无 Workflow 路由'));
  for (const route of view?.routes ?? []) {
    const item = div('route-row'); const identity = div('route-identity'); identity.append(text('strong', '', route.model), text('span', 'section-note', route.recommendedUseCases.join(' · ')));
    const states = div('route-states'); states.append(stateMark('产品画像', route.qualified), stateMark('Codex 发现', route.discovered), stateMark('配置启用', route.enabled), status(route.availability));
    const toggle = actionButton(route.enabled ? 'x' : 'check', route.enabled ? '停用' : '启用', 'toggle-route', '', route.routeId); item.append(identity, states, toggle); section.append(item);
  }
  content.append(section); detail.append(content); wireRoutingActions(); refreshIcons();
}

function routingNotice(errors: string[]): HTMLElement {const notice = div('routing-notice'); notice.append(text('strong', '', '部分路由能力不可用'), text('p', '', errors.join('\n'))); return notice}

function stateMark(label: string, active: boolean): HTMLElement {return text('span', `status ${active ? 'available' : 'unknown'}`, `${label} ${active ? '是' : '否'}`)}

function wireRoutingActions(): void {
  detail.querySelector<HTMLButtonElement>('[data-action="refresh-team-routes"]')?.addEventListener('click', () => void refreshTeamRoutes());
  detail.querySelector<HTMLButtonElement>('[data-action="discover-models"]')?.addEventListener('click', () => void routingRefresh(false));
  detail.querySelector<HTMLButtonElement>('[data-action="probe-models"]')?.addEventListener('click', () => {if (window.confirm('主动探针会实际调用已启用的 Codex 模型并产生少量模型费用。继续吗？')) void routingRefresh(true)});
  for (const button of detail.querySelectorAll<HTMLButtonElement>('[data-action="toggle-route"]')) button.addEventListener('click', () => void toggleRoute(button.dataset.value!));
  for (const button of detail.querySelectorAll<HTMLButtonElement>('[data-action="toggle-team-route"]')) button.addEventListener('click', () => void toggleTeamRoute(button.dataset.value!));
}

async function refreshTeamRoutes(): Promise<void> {
  if (state.busy) return; state.busy = true; setButtonsDisabled(true);
  try {await rpc('team.routes.refresh', {schemaVersion: 'fishyume.config/v1'})} catch (error) {showError(error)} finally {await refreshRouting().catch(() => undefined); state.busy = false; setButtonsDisabled(false)}
}

async function toggleTeamRoute(routeId: string): Promise<void> {
  const current = state.teamRoutes; const route = current?.routes.find(candidate => candidate.routeId === routeId); if (!current || !route) return;
  await mutate(`team-route:${routeId}:${!route.enabled}`, 'team.routes.upsert', {schemaVersion: 'fishyume.config/v1', expectedRevision: current.config.revision, routeId, driver: route.driver, provider: route.provider, model: route.model, enabled: !route.enabled}, 'mutationId');
}

async function routingRefresh(probe: boolean): Promise<void> {
  if (state.busy) return; state.busy = true; setButtonsDisabled(true);
  try {await rpc('driver.models.discover', {schemaVersion: 'fishyume.config/v1'}); if (probe) await rpc('driver.models.probe', {schemaVersion: 'fishyume.config/v1'})} catch (error) {showError(error)} finally {await refreshRouting().catch(() => undefined); state.busy = false; setButtonsDisabled(false)}
}

async function toggleRoute(routeId: string): Promise<void> {
  const config = state.routingConfig; if (!config) return; const enabled = !config.routes.find(route => route.routeId === routeId)?.enabled;
  await mutate(`routing:${routeId}:${enabled}`, 'routing.config.update', {schemaVersion: 'fishyume.config/v1', expectedRevision: config.revision, routeId, enabled}, 'mutationId');
}

async function loadRun(runId: string): Promise<void> {
  const view = await rpc<RunGetResponse>('run.get', {runId});
  if (state.selectedRun !== runId) return;
  if (state.eventRunId !== runId) {state.eventRunId = runId; state.eventCursor = 0; state.lastRunEvent = undefined; state.selectedNode = undefined}
  state.runView = view;
  if (!findWorkflowNode(view.run, state.selectedNode)) state.selectedNode = view.run.topologicalOrder.find(id => findWorkflowNode(view.run, id)) ?? view.run.nodes[0]?.nodeId;
  renderRunDetail(true);
  if (state.pendingFocus?.kind === 'run' && state.pendingFocus.runId === runId) state.pendingFocus = undefined;
}

async function pollRunEvents(runId: string): Promise<void> {
  if (state.eventsBusy || state.busy || state.view !== 'runs' || state.selectedRun !== runId) return;
  state.eventsBusy = true;
  try {
    const response = await rpc<RunEventsResponse>('run.events', {runId, afterSequence: state.eventCursor, limit: 100, waitMs: 1000});
    if (state.selectedRun !== runId) return;
    state.eventCursor = Math.max(state.eventCursor, response.nextAfterSequence);
    if (response.events.length) {state.lastRunEvent = response.events[response.events.length - 1]; await loadRun(runId)}
  } catch (error) {showError(error)} finally {state.eventsBusy = false}
}

function renderCollection(): void {
  if (state.view === 'routing') {renderRoutingCollection(); return}
  element('filter-row').style.display = '';
  element('collection-eyebrow').textContent = state.view === 'teams' ? '探索' : '执行';
  element('collection-title').textContent = state.view === 'teams' ? '团队' : '工作流运行';
  const items = state.view === 'teams' ? filteredTeams() : filteredRuns();
  collection.replaceChildren();
  if (!items.length) {collection.append(empty('没有匹配的项目')); return}
  for (const item of items) collection.append(state.view === 'teams' ? teamListItem(item as TeamSummary) : runListItem(item as RunSummary));
}

function filteredTeams(): TeamSummary[] {
  if (state.filter === 'all') return state.teams;
  return state.teams.filter(team => state.filter === 'closed' ? team.state === 'closed' : team.state !== 'closed');
}

function filteredRuns(): RunSummary[] {
  if (state.filter === 'all') return state.runs;
  return state.runs.filter(run => state.filter === 'closed' ? run.phase === 'completed' : run.phase !== 'completed');
}

function teamListItem(team: TeamSummary): HTMLButtonElement {
  const button = document.createElement('button'); button.className = `collection-item${team.teamId === state.selectedTeam ? ' is-selected' : ''}`;
  button.dataset.id = team.teamId;
  const top = div('item-topline'); top.append(text('span', 'item-title', team.topic), status(team.state));
  const meta = div('item-meta'); meta.append(text('span', '', `${modeLabel(team.mode)} · ${team.participants} 位参与者`), text('span', '', relativeTime(team.updatedAt)));
  const progress = div('item-progress'); const bar = document.createElement('span'); bar.style.width = `${Math.min(100, Math.round(team.costUsed / team.costGrant * 100))}%`; progress.append(bar);
  button.append(top, meta, progress); button.addEventListener('click', () => selectTeam(team.teamId)); return button;
}

function runListItem(run: RunSummary): HTMLButtonElement {
  const button = document.createElement('button'); button.className = `collection-item${run.runId === state.selectedRun ? ' is-selected' : ''}`;
  const top = div('item-topline'); top.append(text('span', 'item-title', run.workflowName), status(run.conclusion ?? run.phase));
  const meta = div('item-meta'); meta.append(text('span', '', `${run.driver}/${run.target}`), text('span', '', relativeTime(run.updatedAt)));
  button.append(top, meta); button.addEventListener('click', () => selectRun(run.runId)); return button;
}

function renderTeamDetail(): void {
  const view = state.teamView; const messages = state.messages;
  if (!view || !messages) return;
  const team = view.team;
  detail.replaceChildren();
  const header = div('detail-header');
  const titleRow = div('detail-title-row');
  const title = document.createElement('div'); title.append(text('span', 'eyebrow', `${team.mode.toUpperCase()} · ${team.teamId}`), text('h2', 'detail-title', team.topic), text('div', 'detail-subtitle', team.project));
  const actions = div('header-actions');
  if (team.mode === 'session' && (team.state === 'open' || team.state === 'running')) actions.append(actionButton('square', '关闭', 'close-team'));
  if (team.state !== 'closed') actions.append(actionButton('x-circle', '取消', 'cancel-team', 'danger'));
  titleRow.append(title, actions); header.append(titleRow); detail.append(header);
  detail.append(metrics([
    ['状态', statusLabel(team.state)], ['参与者', String(team.participants.length)], ['额度', `${team.costUsed} / ${team.costGrant}`], ['更新时间', relativeTime(team.updatedAt)],
  ]));
  const tabs = div('detail-tabs');
  for (const [id, label] of [['discussion', '讨论'], ['handoffs', `交接 ${state.handoffs.length}`], ['run', '关联运行']] as const) {
    const button = text('button', `detail-tab${state.tab === id ? ' is-active' : ''}`, label); button.dataset.tab = id; button.addEventListener('click', () => {state.tab = id; renderTeamDetail()}); tabs.append(button);
  }
  detail.append(tabs);
  const content = div('detail-content');
  if (state.tab === 'discussion') content.append(renderDiscussion(view, messages));
  if (state.tab === 'handoffs') content.append(renderHandoffs());
  if (state.tab === 'run') content.append(renderLinkedRuns());
  detail.append(content); wireTeamActions(); refreshIcons();
}

function renderDiscussion(view: TeamGetResponse, messages: TeamMessagesResponse): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const participants = div('section');
  const heading = div('section-title-row'); heading.append(text('h3', 'section-title', '参与者'), text('span', 'section-note', `${view.turns.length} 轮`)); participants.append(heading);
  const grid = div('participant-grid');
  for (const participant of view.team.participants) grid.append(participantView(participant, view.turns, view.team.mode === 'session'));
  participants.append(grid); fragment.append(participants);
  const discussion = div('section discussion'); discussion.append(text('h3', 'section-title', '讨论'));
  if (!messages.messages.length) discussion.append(empty('暂无已提交消息')); else for (const message of messages.messages) discussion.append(messageView(message));
  if (view.team.mode === 'session' && view.team.state === 'open') discussion.append(followUpComposer(view.team.participants));
  fragment.append(discussion); return fragment;
}

function participantView(participant: Participant, turns: ParticipantTurn[], allowTurnCancel: boolean): HTMLElement {
  const item = div('participant'); const top = div('participant-topline'); top.append(text('span', 'participant-label', participant.label), status(participant.state)); item.append(top, text('div', 'participant-role', participant.role), text('div', 'participant-model', participant.modelId));
  const relevant = turns.filter(turn => turn.participantId === participant.participantId).sort((a, b) => b.number - a.number).slice(0, 2);
  for (const turn of relevant) {
    const row = div('turn-row'); row.append(text('span', '', `第 ${turn.number} 轮`), status(turn.state));
    if (allowTurnCancel && turn.state === 'active') row.append(actionButton('x-circle', '取消', 'cancel-turn', 'danger', turn.turnId)); else row.append(document.createElement('span'));
    item.append(row);
  }
  return item;
}

function messageView(message: TeamMessage): HTMLElement {
  const item = div(`message ${message.kind === 'host_message' ? 'host' : ''}`); const actor = message.kind === 'host_message' ? 'H' : message.actor.slice(0, 2).toUpperCase(); item.append(text('div', 'message-avatar', actor));
  const body = document.createElement('div'); const head = div('message-head'); head.append(text('span', 'message-actor', message.kind === 'host_message' ? 'Host' : message.actor), text('span', 'message-time', formatTime(message.createdAt)), text('span', 'message-id', `#${message.sequence}`));
  body.append(head, text('div', 'message-body', messageContent(message)));
  if (message.referencedMessageIds?.length) body.append(text('div', 'message-ref', `引用 ${message.referencedMessageIds.join(', ')}`));
  const toggle = document.createElement('label'); toggle.className = 'reference-toggle'; const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.name = 'reference-message'; checkbox.value = message.messageId; toggle.append(checkbox, document.createTextNode('在追问中引用')); body.append(toggle);
  item.append(body); return item;
}

function followUpComposer(participants: Participant[]): HTMLElement {
  const form = document.createElement('form'); form.className = 'composer'; form.id = 'follow-up-form';
  form.append(text('h3', 'section-title', '定向追问'));
  const textarea = document.createElement('textarea'); textarea.name = 'content'; textarea.required = true; textarea.maxLength = 16_384; textarea.placeholder = '让选中的参与者比较、质疑或完善一个决策'; form.append(textarea);
  const options = div('composer-options'); const recipients = div('recipient-list');
  for (const participant of participants) {const label = document.createElement('label'); label.className = 'check'; const input = document.createElement('input'); input.type = 'checkbox'; input.name = 'participant'; input.value = participant.participantId; label.append(input, document.createTextNode(participant.label)); recipients.append(label)}
  const send = actionButton('send', '发送追问', 'submit-follow-up', 'primary'); send.type = 'submit'; options.append(recipients, send); form.append(options); return form;
}

function renderHandoffs(): HTMLElement {
  const section = div('section'); const heading = div('section-title-row'); heading.append(text('h3', 'section-title', '不可变交接'), text('span', 'section-note', `${state.handoffs.length} 条`)); section.append(heading);
  const list = div('handoff-list');
  if (!state.handoffs.length) list.append(empty('该团队暂无交接'));
  for (const handoff of state.handoffs) {
    const item = div('handoff'); item.append(text('h3', '', handoff.goal), text('p', '', `${handoff.selectedMessageIds.length} 条消息 · ${formatTime(handoff.createdAt)}`));
    if (handoff.decisions?.length) {const values = document.createElement('ul'); values.className = 'evidence-list'; for (const decision of handoff.decisions) values.append(text('li', '', decision)); item.append(values)}
    item.addEventListener('click', () => {void inspectHandoff(handoff.handoffId, item)}); list.append(item);
  }
  section.append(list); return section;
}

async function inspectHandoff(handoffId: string, target: HTMLElement): Promise<void> {
  try {
    const response = await rpc<{handoff: HandoffArtifact; binding?: {runId: string}}>('team.handoff.get', {schemaVersion: teamVersion, teamId: state.selectedTeam, handoffId});
    target.querySelector('.handoff-detail')?.remove(); const detailNode = div('handoff-detail');
    if (response.handoff.constraints?.length) {const list = document.createElement('ul'); list.className = 'evidence-list'; for (const value of response.handoff.constraints) list.append(text('li', '', value)); detailNode.append(list)}
    if (response.binding) {const button = actionButton('link', '打开关联运行', 'open-linked-run', ''); button.dataset.runId = response.binding.runId; button.addEventListener('click', event => {event.stopPropagation(); openRun(response.binding!.runId)}); detailNode.append(button)}
    target.append(detailNode); refreshIcons();
  } catch (error) {showError(error)}
}

function renderLinkedRuns(): HTMLElement {
  const section = div('section'); section.append(text('h3', 'section-title', '关联的工作流运行'));
  if (!state.handoffs.length) {section.append(empty('暂无已绑定的交接')); return section}
  const list = div('handoff-list');
  for (const handoff of state.handoffs) {const item = div('handoff'); item.append(text('h3', '', handoff.goal), text('p', '', handoff.handoffId)); item.addEventListener('click', () => {void inspectHandoff(handoff.handoffId, item)}); list.append(item); void hydrateLinkedRun(item, handoff.handoffId)}
  section.append(list); return section;
}

async function hydrateLinkedRun(item: HTMLElement, handoffId: string): Promise<void> {
  try {
    const response = await rpc<{binding?: {runId: string}}>('team.handoff.get', {schemaVersion: teamVersion, teamId: state.selectedTeam, handoffId});
    if (!response.binding || !item.isConnected) return;
    const button = actionButton('link', `打开运行 ${response.binding.runId}`, 'open-linked-run'); button.addEventListener('click', event => {event.stopPropagation(); openRun(response.binding!.runId)}); item.append(button); refreshIcons();
  } catch (error) {if (item.isConnected) showError(error)}
}

function renderRunDetail(preserveScroll = false): void {
  const response = state.runView; if (!response) return; const run = response.run;
  const previousInspector = detail.querySelector<HTMLElement>('.node-inspector');
  const previousGraph = detail.querySelector<HTMLElement>('.dag-viewport');
  const previousScroll = preserveScroll ? {
    detailTop: detail.scrollTop,
    inspectorTop: previousInspector?.scrollTop ?? 0,
    graphTop: previousGraph?.scrollTop ?? 0,
    graphLeft: previousGraph?.scrollLeft ?? 0,
  } : undefined;
  detail.replaceChildren();
  const header = div('detail-header'); const row = div('detail-title-row'); const title = document.createElement('div'); title.append(text('span', 'eyebrow', `WORKFLOW · ${run.runId}`), text('h2', 'detail-title', run.workflowName), text('div', 'detail-subtitle', run.project));
  const actions = div('header-actions'); if (run.phase !== 'completed') actions.append(actionButton('x-circle', '取消运行', 'cancel-run', 'danger')); row.append(title, actions); header.append(row); detail.append(header);
  detail.append(metrics([['阶段', statusLabel(run.phase)], ['结论', statusLabel(run.conclusion ?? 'pending')], ['并发数', String(run.effectiveConcurrency)], ['最近事件', state.lastRunEvent ? `${state.lastRunEvent.type} · ${relativeTime(state.lastRunEvent.timestamp)}` : '等待事件']]));
  const content = div('detail-content'); const console = div('workflow-console');
  const graphSection = div('section workflow-graph-section'); const heading = div('section-title-row'); const graphMeta = div('dag-heading-meta'); graphMeta.append(text('span', 'section-note', `${run.nodes.length} 个节点 · ${workflowEdges(run).length} 条依赖边`), graphScaleControl()); heading.append(text('h3', 'section-title', '运行 DAG'), graphMeta); graphSection.append(heading, renderWorkflowGraph(run));
  const inspectorSection = div('section node-inspector-section'); inspectorSection.append(renderNodeInspector(run));
  console.append(graphSection, inspectorSection); content.append(console); detail.append(content); wireRunActions(); refreshIcons();
  if (previousScroll) requestAnimationFrame(() => {
    detail.scrollTop = previousScroll.detailTop;
    const inspector = detail.querySelector<HTMLElement>('.node-inspector');
    if (inspector) inspector.scrollTop = previousScroll.inspectorTop;
    const graph = detail.querySelector<HTMLElement>('.dag-viewport');
    if (graph) {graph.scrollTop = previousScroll.graphTop; graph.scrollLeft = previousScroll.graphLeft;}
  });
}

function renderWorkflowGraph(run: RunGetResponse['run']): HTMLElement {
  const root = div('workflow-graph');
  const layout = workflowGraphLayout(run);
  const related = relatedWorkflowNodeIds(run, state.selectedNode);
  const hasFocus = related.size > 0;
  const viewport = div('dag-viewport');
  const shell = div('dag-canvas-shell');
  const canvas = div('dag-canvas');
  canvas.style.width = `${layout.width}px`;
  canvas.style.height = `${layout.height}px`;
  canvas.setAttribute('aria-label', `工作流依赖图，共 ${layout.nodes.length} 个节点`);

  for (const stage of layout.stages) {
    const label = text('span', 'dag-stage-label', `阶段 ${stage.index + 1}`);
    label.style.left = `${stage.x}px`;
    label.style.width = `${layout.nodeWidth}px`;
    canvas.append(label);
  }

  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.classList.add('dag-edge-layer');
  svg.setAttribute('width', String(layout.width));
  svg.setAttribute('height', String(layout.height));
  svg.setAttribute('viewBox', `0 0 ${layout.width} ${layout.height}`);
  svg.setAttribute('aria-hidden', 'true');
  const definitions = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
  const marker = document.createElementNS('http://www.w3.org/2000/svg', 'marker');
  marker.id = 'workflow-arrowhead';
  marker.setAttribute('viewBox', '0 0 8 8');
  marker.setAttribute('refX', '7');
  marker.setAttribute('refY', '4');
  marker.setAttribute('markerWidth', '7');
  marker.setAttribute('markerHeight', '7');
  marker.setAttribute('orient', 'auto-start-reverse');
  const arrow = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  arrow.setAttribute('d', 'M 0 0 L 8 4 L 0 8 z');
  marker.append(arrow); definitions.append(marker); svg.append(definitions);
  for (const edge of layout.edges) {
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    const active = hasFocus && related.has(edge.from) && related.has(edge.to);
    path.setAttribute('d', edge.path);
    path.setAttribute('marker-end', 'url(#workflow-arrowhead)');
    path.dataset.active = String(active);
    path.dataset.dimmed = String(hasFocus && !active);
    svg.append(path);
  }
  canvas.append(svg);
  for (const item of layout.nodes) {
    const node = runNode(item.node, hasFocus && related.has(item.node.nodeId), hasFocus && !related.has(item.node.nodeId));
    node.style.left = `${item.x}px`;
    node.style.top = `${item.y}px`;
    node.style.width = `${layout.nodeWidth}px`;
    node.style.height = `${layout.nodeHeight}px`;
    canvas.append(node);
  }
  shell.append(canvas); viewport.append(shell); root.append(viewport);
  requestAnimationFrame(() => {
    if (!viewport.isConnected) return;
    const scale = state.graphScale === 'fit' ? Math.min(1, Math.max(.2, (viewport.clientWidth - 24) / layout.width)) : 1;
    canvas.style.transform = `scale(${scale})`;
    shell.style.width = `${layout.width * scale}px`;
    shell.style.height = `${layout.height * scale}px`;
    viewport.dataset.scale = state.graphScale;
  });
  if (hasFocus) root.append(text('p', 'dag-focus-note', '已突出显示所选节点的完整上下游链路'));
  return root;
}

function graphScaleControl(): HTMLElement {
  const root = div('graph-scale-switch');
  for (const [mode, icon, label] of [['fit', 'maximize-2', '适配画布'], ['actual', 'scan', '实际大小']] as const) {
    const button = document.createElement('button'); button.type = 'button'; button.className = state.graphScale === mode ? 'is-active' : ''; button.title = label; button.setAttribute('aria-label', label); button.setAttribute('aria-pressed', String(state.graphScale === mode)); const glyph = document.createElement('i'); glyph.dataset.lucide = icon; button.append(glyph); button.addEventListener('click', () => {state.graphScale = mode; renderRunDetail()}); root.append(button);
  }
  return root;
}

function runNode(node: ApplicationNodeView, related = false, dimmed = false): HTMLElement {
  const item = div(`run-node${state.selectedNode === node.nodeId ? ' is-selected' : ''}`); item.tabIndex = 0; item.setAttribute('role', 'button'); item.setAttribute('aria-pressed', String(state.selectedNode === node.nodeId)); item.dataset.state = safeState(node.conclusion ?? node.phase); item.dataset.related = String(related); item.dataset.dimmed = String(dimmed); item.title = `查看节点 ${node.nodeId}`; item.addEventListener('click', event => {if (!(event.target as HTMLElement).closest('button')) selectNode(node.nodeId)}); item.addEventListener('keydown', event => {if (event.key === 'Enter' || event.key === ' ') {event.preventDefault(); selectNode(node.nodeId)}});
  const top = div('participant-topline'); top.append(text('h3', '', node.nodeId), status(node.conclusion ?? node.phase)); item.append(top);
  const meta = div('node-meta'); meta.append(text('span', '', nodeTypeLabel(node.type)), text('span', '', node.attempt ? `${node.attempt.driver}/${node.attempt.target}` : '尚未开始')); item.append(meta);
  if (node.result?.summary) item.append(text('div', 'node-summary', node.result.summary));
  if (node.diagnostic) item.append(text('div', 'node-summary', node.diagnostic));
  return item;
}

function renderNodeInspector(run: RunGetResponse['run']): HTMLElement {
  const node = findWorkflowNode(run, state.selectedNode);
  if (!node) return empty('请选择一个节点查看详情');
  const root = div('node-inspector'); const heading = div('section-title-row'); heading.append(text('h3', 'section-title', '节点详情'), status(node.conclusion ?? node.phase)); root.append(heading);
  const identity = div('inspector-identity'); identity.append(text('h4', '', node.nodeId), text('span', 'section-note', nodeTypeLabel(node.type))); root.append(identity);
  root.append(keyValueList([
    ['阶段', statusLabel(node.phase)], ['结论', statusLabel(node.conclusion ?? 'pending')], ['当前 Attempt', node.currentAttempt ? String(node.currentAttempt) : '尚未启动'], ['并行阶段', node.parallelLayer === undefined ? '未知' : String(node.parallelLayer + 1)],
  ]));
  const dependencies = div('inspector-block'); dependencies.append(text('h4', 'inspector-label', '关系'));
  const upstream = div('inspector-relation'); upstream.append(text('span', 'inspector-sublabel', '上游'));
  if (node.dependsOn?.length) upstream.append(nodeLinks(node.dependsOn)); else upstream.append(text('span', 'section-note', '无前置依赖'));
  const dependentIds = run.nodes.filter(candidate => candidate.dependsOn?.includes(node.nodeId)).map(candidate => candidate.nodeId);
  const downstream = div('inspector-relation'); downstream.append(text('span', 'inspector-sublabel', '下游'));
  if (dependentIds.length) downstream.append(nodeLinks(dependentIds)); else downstream.append(text('span', 'section-note', '无后续节点'));
  dependencies.append(upstream, downstream); root.append(dependencies);
  if (node.reason || node.diagnostic) {const diagnostics = div('inspector-block'); diagnostics.append(text('h4', 'inspector-label', '诊断')); if (node.reason) diagnostics.append(text('div', 'detail-value', statusLabel(node.reason))); if (node.diagnostic) diagnostics.append(text('p', 'inspector-copy', node.diagnostic)); root.append(diagnostics)}
  if (node.attempt) root.append(renderAttemptDetails(node));
  if (node.result) root.append(renderResultDetails(node));
  const actions = renderNodeActions(node); if (actions.childElementCount) {const block = div('inspector-block'); block.append(text('h4', 'inspector-label', '可用操作'), actions); root.append(block)}
  return root;
}

function renderAttemptDetails(node: ApplicationNodeView): HTMLElement {
  const attempt = node.attempt!; const block = div('inspector-block'); block.append(text('h4', 'inspector-label', `Attempt ${attempt.number}`));
  block.append(keyValueList([['状态', statusLabel(attempt.phase)], ['驱动', attempt.driver], ['目标', attempt.target], ['开始时间', formatTime(attempt.startedAt)], ['更新时间', formatTime(attempt.updatedAt)], ['副作用', attempt.sideEffectStatus ?? '未知']]));
  if (attempt.routingDecision) block.append(keyValueList([['路由选择', routeLabel(attempt.routingDecision.selected)], ['路由原因', attempt.routingDecision.reasonCodes.join(', ') || '未提供'], ['回退策略', attempt.routingDecision.fallbackPolicy.mode]]));
  if (attempt.executionProfile) block.append(keyValueList([['推理强度', attempt.executionProfile.reasoningEffort], ['执行模型', routeLabel(attempt.executionProfile.target)]]));
  if (attempt.routingUsage) block.append(keyValueList([['本次成本', String(attempt.routingUsage.costUnits)], ['累计成本', String(attempt.routingUsage.cumulativeCostUnits)], ['路由序号', String(attempt.routingUsage.routeIndex)]]));
  if (attempt.failureClass) block.append(text('div', 'detail-value', `失败类别: ${attempt.failureClass}`));
  if (attempt.context) block.append(renderContextSummary(attempt.context));
  if (attempt.activity) block.append(renderActivity(attempt.activity));
  return block;
}

function renderActivity(activityView: NonNullable<NonNullable<ApplicationNodeView['attempt']>['activity']>): HTMLElement {
  const activity = div('activity-list');
  const heading = div('activity-heading');
  heading.append(text('h4', 'inspector-label', '执行活动'), text('span', 'activity-count', `${activityView.items.length}${activityView.truncated ? '+' : ''} 条`));
  activity.append(heading);
  if (activityView.summary) activity.append(text('p', 'activity-summary', activityView.summary));
  const timeline = div('activity-timeline');
  for (const [index, item] of activityView.items.entries()) {
    const row = div('activity-item');
    row.dataset.state = safeState(item.status);
    const marker = div('activity-marker'); marker.append(text('span', 'activity-index', String(index + 1))); marker.setAttribute('aria-hidden', 'true');
    const content = div('activity-content');
    const meta = div('activity-item-head');
    meta.append(text('span', 'activity-kind', activityKindLabel(item.kind)), status(item.status));
    if (item.command) meta.append(text('span', `activity-tag activity-tag-${safeState(item.command.category)}`, `${item.command.category} · ${item.command.program}`));
    if (item.resource) meta.append(text('span', 'activity-tag activity-tag-resource', item.resource.operation));
    content.append(meta, text('p', 'activity-message', item.message));
    if (item.command?.program) content.append(text('code', 'activity-command', item.command.program));
    if (item.resource?.path) content.append(text('code', 'activity-resource-path', item.resource.path));
    row.append(marker, content); timeline.append(row);
  }
  if (!activityView.items.length) timeline.append(text('div', 'section-note', '暂无可显示的执行活动'));
  activity.append(timeline);
  if (activityView.truncated) activity.append(text('div', 'activity-truncated', '活动记录已截断，仅显示最近事件'));
  return activity;
}

function activityKindLabel(kind: string): string {
  const labels: Record<string, string> = {session: '会话', turn: '处理轮次', command: '命令', reasoning: '分析', message: '消息', tool: '工具'};
  return labels[kind] ?? kind.replaceAll('_', ' ');
}

function renderContextSummary(context: NonNullable<NonNullable<ApplicationNodeView['attempt']>['context']>): HTMLElement {
  const block = div('context-summary'); block.append(text('h4', 'inspector-label', '上下文摘要')); block.append(keyValueList([['编译器', context.compilerVersion], ['上下文 Hash', context.hash ?? '未提供'], ['组件数', String(context.components.length)], ['省略项', String(context.omissions?.length ?? 0)], ['已截断', context.truncated ? '是' : '否']]));
  if (context.memoryUsage) block.append(keyValueList([['Memory 记录数', String(context.memoryUsage.recordIds.length)], ['Memory 已提交', context.memoryUsage.committed ? '是' : '否']]));
  return block;
}

function renderResultDetails(node: ApplicationNodeView): HTMLElement {
  const result = node.result!; const block = div('inspector-block'); block.append(text('h4', 'inspector-label', '节点结果'));
  if (result.summary) block.append(text('p', 'inspector-copy', result.summary));
  if (result.artifacts?.length) block.append(renderArtifactCards(result.artifacts));
  if (result.decision) block.append(text('div', 'detail-value', `决定: ${result.decision}`));
  for (const [label, values] of [['产物', result.artifacts], ['检查', result.checks], ['警告', result.warnings]] as const) if (values?.length) {const list = div('inspector-list'); list.append(text('h5', 'inspector-sublabel', label)); for (const value of values) list.append(text('div', 'list-item', value)); block.append(list)}
  if (result.reason) block.append(text('div', 'detail-value', `原因: ${result.reason}`));
  if (result.artifacts?.length) for (const list of block.querySelectorAll('.inspector-list')) {
    const values = [...list.querySelectorAll('.list-item')].map(item => item.textContent ?? '');
    if (values.some(value => result.artifacts.includes(value))) list.remove();
  }
  if (result.questions?.length && node.phase === 'waiting') block.append(answerForm(node));
  return block;
}

function renderArtifactCards(artifacts: string[]): HTMLElement {
  const block = div('artifact-block');
  block.append(text('h5', 'inspector-sublabel', '\u4ea7\u7269'));
  const grid = div('artifact-grid');
  for (const value of artifacts) {
    const info = artifactInfo(value);
    const card = div('artifact-card'); card.dataset.kind = info.kind;
    const icon = document.createElement('i'); icon.dataset.lucide = info.icon;
    const body = div('artifact-body'); body.append(text('strong', 'artifact-name', artifactName(value)), text('span', 'artifact-kind', info.label), text('span', 'artifact-path', value));
    card.append(icon, body); grid.append(card);
  }
  block.append(grid);
  return block;
}

function artifactName(value: string): string {
  const normalized = value.replaceAll('\\', '/');
  return normalized.slice(normalized.lastIndexOf('/') + 1) || value;
}

function artifactInfo(value: string): {kind: string; label: string; icon: string} {
  const extension = artifactName(value).split('.').pop()?.toLowerCase() ?? '';
  if (['md', 'txt', 'rst'].includes(extension)) return {kind: 'document', label: 'Document', icon: 'file-text'};
  if (['json', 'yaml', 'yml', 'toml', 'ini', 'env'].includes(extension)) return {kind: 'config', label: 'Config / data', icon: extension === 'json' ? 'file-json' : 'file-cog'};
  if (['py', 'ts', 'tsx', 'js', 'jsx', 'go', 'rs', 'java', 'rb', 'c', 'cpp', 'h'].includes(extension)) return {kind: 'source', label: 'Source code', icon: 'file-code-2'};
  return {kind: 'file', label: 'File', icon: 'file-text'};
}

function renderNodeActions(node: ApplicationNodeView): HTMLElement {
  const actions = div('node-actions');
  if (node.type === 'approval' && node.phase === 'waiting') actions.append(actionButton('check', '批准', 'approve-node', 'primary', node.nodeId), actionButton('x', '拒绝', 'reject-node', 'danger', node.nodeId));
  if (node.phase === 'completed' && (node.conclusion === 'failed' || node.conclusion === 'indeterminate')) actions.append(actionButton('rotate-ccw', '重试', 'retry-node', '', node.nodeId));
  return actions;
}

function keyValueList(values: Array<[string, string]>): HTMLElement {const list = div('key-value-list'); for (const [label, value] of values) {const row = div('key-value-row'); row.append(text('span', 'key-value-label', label), text('span', 'detail-value', value)); list.append(row)} return list}
function nodeLinks(nodeIds: string[]): HTMLElement {const list = div('inspector-links'); for (const nodeId of nodeIds) {const button = document.createElement('button'); button.className = 'link-button node-link'; button.textContent = nodeId; button.addEventListener('click', () => selectNode(nodeId)); list.append(button)} return list}
function routeLabel(route: {driver: string; provider: string; model: string}): string {return `${route.driver}/${route.provider}/${route.model}`}

function answerForm(node: ApplicationNodeView): HTMLElement {
  const form = document.createElement('form'); form.className = 'answer-form'; form.dataset.nodeId = node.nodeId; form.dataset.attempt = String(node.currentAttempt ?? node.attempt?.number ?? 0);
  for (const question of node.result?.questions ?? []) {const label = document.createElement('label'); label.textContent = question.prompt; const input = document.createElement('input'); input.name = question.id; input.required = question.required; if (question.choices.length) input.setAttribute('list', `choices-${node.nodeId}-${question.id}`); label.append(input); form.append(label); if (question.choices.length) {const data = document.createElement('datalist'); data.id = `choices-${node.nodeId}-${question.id}`; for (const choice of question.choices) {const option = document.createElement('option'); option.value = choice; data.append(option)} form.append(data)}}
  const submit = actionButton('send', '提交回答', 'answer-node', 'primary'); submit.type = 'submit'; form.append(submit); return form;
}

function wireTeamActions(): void {
  document.getElementById('follow-up-form')?.addEventListener('submit', event => {event.preventDefault(); void submitFollowUp(event.currentTarget as HTMLFormElement)});
  for (const button of detail.querySelectorAll<HTMLButtonElement>('[data-action="cancel-turn"]')) button.addEventListener('click', () => void teamAction('cancel_turn', {cancelTurn: {turnId: button.dataset.value}}));
  detail.querySelector<HTMLButtonElement>('[data-action="close-team"]')?.addEventListener('click', () => void teamAction('close', {close: {reason: 'host_closed'}}));
  detail.querySelector<HTMLButtonElement>('[data-action="cancel-team"]')?.addEventListener('click', () => void teamAction('cancel', {}));
}

async function submitFollowUp(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form); const participantIds = data.getAll('participant').map(String); const content = String(data.get('content') ?? '').trim();
  const referencedMessageIds = [...detail.querySelectorAll<HTMLInputElement>('input[name="reference-message"]:checked')].map(input => input.value);
  if (!content || !participantIds.length) {showError(new Error('Select at least one participant and enter a follow-up')); return}
  await teamAction('follow_up', {followUp: {content, participantIds, referencedMessageIds}});
}

async function teamAction(type: string, payload: Record<string, unknown>): Promise<void> {
  const view = state.teamView; if (!view || !state.selectedTeam) return;
  const request = {schemaVersion: teamVersion, teamId: state.selectedTeam, expectedStateVersion: view.team.stateVersion, type, ...payload};
  await mutate(`team:${state.selectedTeam}:${type}`, 'team.action', request, 'actionId');
}

function wireRunActions(): void {
  detail.querySelector<HTMLButtonElement>('[data-action="cancel-run"]')?.addEventListener('click', () => void runAction('cancel', {}));
  for (const [selector, type] of [['approve-node', 'approve'], ['reject-node', 'reject'], ['retry-node', 'retry']] as const) for (const button of detail.querySelectorAll<HTMLButtonElement>(`[data-action="${selector}"]`)) button.addEventListener('click', () => {
    const node = state.runView?.run.nodes.find(candidate => candidate.nodeId === button.dataset.value);
    if (!node) return;
    if (type === 'retry' && !window.confirm('要重试这个 Attempt 吗？这可能会重复外部副作用。')) return;
    void runAction(type, {nodeId: node.nodeId, ...(node.currentAttempt ? {expectedAttempt: node.currentAttempt} : {}), ...(type === 'reject' ? {reason: '从 Fishyume Web 拒绝'} : {}), ...(type === 'retry' ? {acknowledgeDuplicateRisk: true} : {})});
  });
  for (const form of detail.querySelectorAll<HTMLFormElement>('.answer-form')) form.addEventListener('submit', event => {event.preventDefault(); const answers = Object.fromEntries([...new FormData(form).entries()].map(([key, value]) => [key, String(value)])); void runAction('answer', {nodeId: form.dataset.nodeId, expectedAttempt: Number(form.dataset.attempt), answers})});
}

async function runAction(type: string, payload: Record<string, unknown>): Promise<void> {
  const view = state.runView; if (!view || !state.selectedRun) return;
  await mutate(`run:${state.selectedRun}:${type}`, 'run.action', {runId: state.selectedRun, expectedStateVersion: view.run.stateVersion, type, ...payload}, 'actionId');
}

async function mutate(key: string, method: string, request: Record<string, unknown>, idField: string): Promise<void> {
  if (state.busy) return; state.busy = true; setButtonsDisabled(true);
  const fingerprint = JSON.stringify({method, request}); const storageKey = 'fishyume-web.pending-mutation';
  let pending: {key: string; fingerprint: string; id: string} | undefined;
  try {pending = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as typeof pending} catch {pending = undefined}
  const id = pending?.key === key && pending.fingerprint === fingerprint ? pending.id : crypto.randomUUID();
  sessionStorage.setItem(storageKey, JSON.stringify({key, fingerprint, id}));
  try {
    await rpc(method, {...request, [idField]: id}); sessionStorage.removeItem(storageKey);
    if (state.view === 'teams') await refreshTeams(); else if (state.view === 'runs') await refreshRuns(); else await refreshRouting();
  } catch (error) {
    if (error instanceof ApiError) sessionStorage.removeItem(storageKey);
    showError(error);
    await (state.view === 'teams' ? refreshTeams() : state.view === 'runs' ? refreshRuns() : refreshRouting()).catch(() => undefined);
  } finally {state.busy = false; setButtonsDisabled(false)}
}

function selectTeam(id: string): void {state.selectedTeam = id; state.tab = 'discussion'; renderCollection(); detail.replaceChildren(loading()); void loadTeam(id).catch(showError)}
function selectRun(id: string): void {state.selectedRun = id; state.selectedNode = undefined; state.eventRunId = undefined; state.eventCursor = 0; state.lastRunEvent = undefined; state.graphScale = 'actual'; renderCollection(); detail.replaceChildren(loading()); void loadRun(id).catch(showError)}
function openRun(id: string): void {state.view = 'runs'; state.selectedRun = id; updateViewControls(); void refresh()}
function selectNode(nodeId: string): void {if (!state.runView || !findWorkflowNode(state.runView.run, nodeId)) return; state.selectedNode = nodeId; renderRunDetail()}

function updateViewControls(): void {
  const parameters = new URLSearchParams(location.hash.slice(1));
  parameters.set('view', state.view);
  history.replaceState(null, '', `#${parameters.toString()}`);
  syncViewControls();
  renderCollection(); detail.replaceChildren(loading());
}

function syncViewControls(): void {
  for (const button of document.querySelectorAll<HTMLButtonElement>('.view-tab')) button.classList.toggle('is-active', button.dataset.view === state.view);
}

function metrics(values: Array<[string, string]>): HTMLElement {const root = div('metrics'); for (const [label, value] of values) {const item = div('metric'); item.append(text('span', 'metric-label', label), text('span', 'metric-value', value)); root.append(item)} return root}
function status(value: string): HTMLElement {return text('span', `status ${safeState(value)}`, statusLabel(value))}
function safeState(value: string): string {return /^[a-z_]+$/.test(value) ? value.replaceAll('_', '-') : ''}
function statusLabel(value: string): string {
  const labels: Record<string, string> = {created: '已创建', running: '运行中', open: '开放', closing: '关闭中', cancelling: '取消中', closed: '已关闭', active: '进行中', responded: '已响应', completed: '已完成', succeeded: '成功', failed: '失败', cancelled: '已取消', waiting: '等待处理', paused: '已暂停', skipped: '已跳过', indeterminate: '未确定', pending: '待处理', approval: '等待批准', needs_input: '等待输入', not_started: '尚未开始'};
  return labels[value] ?? value.replaceAll('_', ' ');
}
function modeLabel(value: string): string {return value === 'session' ? '会话' : value === 'panel' ? '面板' : value}
function nodeTypeLabel(value: string): string {return value === 'agent' ? 'Agent 节点' : value === 'approval' ? '审批节点' : value}
function actionButton(icon: string, label: string, action: string, variant = '', value?: string): HTMLButtonElement {const button = document.createElement('button'); button.className = `command-button ${variant}`.trim(); button.dataset.action = action; if (value) button.dataset.value = value; const i = document.createElement('i'); i.dataset.lucide = icon; button.append(i, document.createTextNode(label)); return button}
function div(className: string): HTMLDivElement {const value = document.createElement('div'); value.className = className; return value}
function text<K extends keyof HTMLElementTagNameMap>(tag: K, className: string, value: string): HTMLElementTagNameMap[K] {const node = document.createElement(tag); node.className = className; node.textContent = value; return node}
function empty(value: string): HTMLElement {return text('div', 'empty-state', value)}
function loading(): HTMLElement {const root = document.createElement('div'); for (let i = 0; i < 4; i++) root.append(div('loading-line')); return root}
function element(id: string): HTMLElement {const value = document.getElementById(id); if (!value) throw new Error(`missing element ${id}`); return value}
function formatTime(value: string): string {return new Intl.DateTimeFormat(undefined, {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'}).format(new Date(value))}
function relativeTime(value: string): string {const seconds = Math.round((new Date(value).getTime() - Date.now()) / 1000); const formatter = new Intl.RelativeTimeFormat(undefined, {numeric: 'auto'}); if (Math.abs(seconds) < 60) return formatter.format(seconds, 'second'); const minutes = Math.round(seconds / 60); if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute'); const hours = Math.round(minutes / 60); if (Math.abs(hours) < 24) return formatter.format(hours, 'hour'); return formatter.format(Math.round(hours / 24), 'day')}
function renderEmptyDetail(icon: string, label: string): void {detail.replaceChildren(); const root = div('detail-empty'); const box = document.createElement('div'); const i = document.createElement('i'); i.dataset.lucide = icon; box.append(i, text('div', '', label)); root.append(box); detail.append(root); refreshIcons()}
function setButtonsDisabled(disabled: boolean): void {for (const button of detail.querySelectorAll<HTMLButtonElement>('button')) button.disabled = disabled}
function showError(error: unknown): void {if (error instanceof ApiError) {connection.className = 'connection-state is-online'; connectionLabel.textContent = '本地控制面'} else {connection.className = 'connection-state is-error'; connectionLabel.textContent = '连接异常'} const toast = text('div', 'toast', error instanceof Error ? error.message : String(error)); element('toast-region').append(toast); setTimeout(() => toast.remove(), 7000)}
function errorMessage(error: unknown): string {return error instanceof Error ? error.message : String(error)}
function refreshIcons(): void {createIcons({icons: {MessagesSquare, Workflow, RefreshCw, Send, XCircle, Square, RotateCcw, Check, X, Link, GitBranch, Inbox, Route, Maximize2, Scan, Terminal, FileCode2, FileText, FileJson, FileCog, Wrench}})}

for (const button of document.querySelectorAll<HTMLButtonElement>('.view-tab')) button.addEventListener('click', () => {state.view = button.dataset.view as View; state.filter = 'all'; updateViewControls(); void refresh()});
for (const button of document.querySelectorAll<HTMLButtonElement>('.filter')) button.addEventListener('click', () => {state.filter = button.dataset.filter as Filter; for (const candidate of document.querySelectorAll('.filter')) candidate.classList.toggle('is-active', candidate === button); renderCollection()});
element('refresh-button').addEventListener('click', () => void refresh());
document.addEventListener('visibilitychange', () => {if (!document.hidden) void refresh()});
setInterval(() => {if (!document.hidden) {void refresh(); void pollFocus(); if (state.view === 'runs' && state.selectedRun) void pollRunEvents(state.selectedRun)}}, 3000);
refreshIcons(); syncViewControls(); collection.append(loading()); detail.append(loading()); void refresh();
