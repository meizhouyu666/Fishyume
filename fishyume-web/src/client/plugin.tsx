/** Native DSH client entry. Renders a React workspace; never creates an iframe. */
import { useEffect, useState, useSyncExternalStore, type ComponentType, type WheelEvent } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { createConnectionTransport, createHttpTransport, createRemoteTransport, type RpcTransport } from './transport.js'
import { FISHYUME_REMOTE, type FishyumeRemoteFace } from '../remote-contract.js'
import entryIcon from './fishyume-dsh.png'
import fishyumeLogo from './fishyume.png'
import ClaudeIcon from '@lobehub/icons/es/Claude/components/Color.js'
import DeepSeekIcon from '@lobehub/icons/es/DeepSeek/components/Color.js'
import GeminiIcon from '@lobehub/icons/es/Gemini/components/Color.js'
import OpenAIIcon from '@lobehub/icons/es/OpenAI/components/Mono.js'
import OpenCodeIcon from '@lobehub/icons/es/OpenCode/components/Mono.js'

export const inject = ['slots', 'connection', 'remote']

interface SlotComponentProps { wide?: boolean }
interface ClientSlots {
  inject(slot: string, register: () => void): void
  register(options: { name: string; id: string; order?: number; label?: string; inject?: () => unknown }, Component: (props: SlotComponentProps) => JSX.Element | null): (() => void) | void
}
export interface ClientContext {
  slots: ClientSlots
  get?: (name: string) => unknown
  remote?: { $mount(contribution: unknown): Promise<() => void> }
  reflect?: { get(name: string): unknown }
  effect?: (setup: () => void | (() => void) | Promise<void | (() => void)>, label?: string) => void
}

type View = 'teams' | 'runs' | 'routing' | 'settings'
type TeamParticipant = { participantId: string; label?: string; role?: string; modelId?: string; driver?: string; target?: string; state?: string; currentTurnId?: string }
type Team = { teamId: string; project?: string; topic?: string; status?: string; state?: string; stateVersion?: number; costGrant?: number; costUsed?: number; participants?: number | TeamParticipant[]; createdAt?: string | number; updatedAt?: string }
type TeamTurn = { turnId: string; participantId: string; number?: number; state?: string; driver?: string; target?: string; modelId?: string; diagnostic?: string; contributionMessageId?: string; createdAt?: string; updatedAt?: string; completedAt?: string }
type TeamMessage = { messageId: string; sequence: number; kind?: string; actor?: string; turnId?: string; content?: string; createdAt?: string }
type TeamDetail = Team & { participants?: TeamParticipant[]; turns?: TeamTurn[]; clientRequestId?: string; requestHash?: string; catalogHash?: string; instructions?: string; closeReason?: string }
type Handoff = { handoffId: string; teamId: string; goal: string; createdAt?: string; decisions?: string[]; acceptanceExpectations?: string[] }
type Run = { runId: string; workflowName?: string; project?: string; driver?: string; phase?: string; conclusion?: string; stateVersion?: number; cancelRequested?: boolean; updatedAt?: string }
type RunNode = { nodeId: string; type?: string; title?: string; phase?: string; conclusion?: string; reason?: string; currentAttempt?: number; message?: string; diagnostic?: string; dependsOn?: string[]; parallelLayer?: number; attempt?: { number?: number; phase?: string; driver?: string; target?: string; startedAt?: string; updatedAt?: string; completedAt?: string; diagnostic?: string; contextHash?: string; activity?: { summary?: string; items?: Array<{ kind?: string; status?: string; message?: string }> }; routingDecision?: unknown; executionProfile?: unknown; routingUsage?: unknown; sideEffectStatus?: string; failureClass?: string }; result?: { summary?: string; artifacts?: string[]; warnings?: string[]; checks?: string[]; questions?: Array<{ id: string; prompt: string; choices?: string[]; required?: boolean }>; decision?: string; reason?: string; usage?: Record<string, number> } }
type RunDetail = Run & { summary?: string; nodes?: RunNode[] }
type RunEvent = { sequence: number; type: string; phase?: string; nodeId?: string; nodePhase?: string; conclusion?: string; reason?: string; message?: string; summary?: string; timestamp?: string; createdAt?: string; turnId?: string; messageId?: string }
type Driver = { driver: string; available: boolean; teamEligible: boolean; workflowEligible: boolean; executable?: string; diagnostic?: string; modelCount?: number; version?: string; authenticated?: boolean }
type Route = { routeId: string; enabled: boolean; effective?: boolean; driver?: string; provider?: string; model?: string; driverAvailable?: boolean }
type TeamTemplateMember = { label: string; roleHint?: string; driver?: string; modelId?: string }
type TeamTemplate = { schemaVersion?: string; templateId: string; name: string; description?: string; color?: string; members: TeamTemplateMember[]; createdAt?: string; updatedAt?: string }
type TeamTemplateDraft = { templateId: string; name: string; description: string; color: string; members: TeamTemplateMember[] }
type TeamModelOption = { modelId: string; provider?: string; model?: string; label?: string }
type TeamHarnessOption = { driver: string; models: TeamModelOption[] }
type TeamCapabilities = { participantTemplates?: Array<{ label?: string; role?: string; modelId?: string; driver?: string; target?: string }>; harnesses?: TeamHarnessOption[] }
type TeamSection = 'tasks' | 'templates'
type TemplateMode = 'list' | 'create'
type PanelState = { open: boolean; view: View; teamSection: TeamSection; templateMode: TemplateMode; teams: Team[]; templates: TeamTemplate[]; capabilities?: TeamCapabilities; runs: Run[]; handoffs: Handoff[]; drivers: Driver[]; routes: Route[]; selectedTeam?: string; selectedTemplate?: string; selectedRun?: string; selectedHandoff?: string; selectedMember?: string; selectedNode?: string; selectedEvent?: number; teamDetail?: TeamDetail; teamMessages: TeamMessage[]; teamEvents: RunEvent[]; runDetail?: RunDetail; runEvents: RunEvent[]; token?: string; loading: boolean; error?: string; focusRevision: number }

const listeners = new Set<() => void>()
let panel: PanelState = { open: false, view: 'teams', teamSection: 'tasks', templateMode: 'list', teams: [], templates: [], handoffs: [], drivers: [], routes: [], teamMessages: [], teamEvents: [], runEvents: [], loading: false, focusRevision: 0 }
let transport: RpcTransport = createHttpTransport({ rpcPath: '/plugins/dsh-fishyume/api/rpc', tokenPath: '/plugins/dsh-fishyume/token' })
const nativeStyles = `
.dsh-fishyume-panel{--fy-bg-canvas:#08090a;--fy-bg-surface:#0f1011;--fy-bg-elevated:#1c1c1f;--fy-bg-hover:#232326;--fy-border:#28282c;--fy-border-strong:#3e3e44;--fy-text-primary:#f7f8f8;--fy-text-secondary:#8a8f98;--fy-text-tertiary:#62666d;--fy-accent:#5e6ad2;--fy-accent-soft:#828fff;--fy-success:#27a644;--fy-warning:#f0bf00;--fy-danger:#eb5757;--fy-info:#4ea7fc;width:100%;height:100%;display:flex;overflow:hidden;color:var(--fy-text-primary);background:var(--fy-bg-canvas);font:14px/21px Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
.dsh-fishyume-workspace{position:relative;display:flex;flex:1;min-width:0;min-height:0;box-sizing:border-box;gap:10px;padding:14px 16px 16px;flex-direction:column;background:var(--fy-bg-canvas)}
.dsh-fishyume-header{box-sizing:border-box;display:flex;align-items:center;justify-content:space-between;min-height:32px;padding:0;border-bottom:0;background:transparent}
.dsh-fishyume-header h2{margin:0;font-size:16px;line-height:22px;font-weight:700;letter-spacing:0}
.dsh-fishyume-header button,.dsh-fishyume-tabs button,.dsh-fishyume-state button{color:var(--fy-text-secondary);background:transparent;border:1px solid var(--fy-border);border-radius:8px;padding:5px 12px;font:600 12px/18px inherit;cursor:pointer;transition:background .15s,border-color .15s,color .15s}
.dsh-fishyume-header button:hover,.dsh-fishyume-tabs button:hover,.dsh-fishyume-state button:hover{color:var(--fy-text-primary);background:var(--fy-bg-hover);border-color:var(--fy-border-strong)}
.dsh-fishyume-header button:focus-visible,.dsh-fishyume-tabs button:focus-visible,.dsh-fishyume-list button:focus-visible,.dsh-fishyume-state button:focus-visible,.dsh-fishyume-node:focus-visible,.dsh-fishyume-event:focus-visible,.dsh-fishyume-member:focus-visible{outline:2px solid var(--fy-accent-soft);outline-offset:2px}
.dsh-fishyume-header div:last-child{display:flex;gap:8px}
.dsh-fishyume-brand{display:flex;align-items:center;gap:10px;min-width:0}
.dsh-fishyume-logo{display:block;width:34px;height:34px;flex:none;object-fit:contain}
.dsh-fishyume-eyebrow{font-size:11px;line-height:16px;color:var(--fy-accent-soft);font-weight:600;letter-spacing:.08em}
.dsh-fishyume-tabs{display:flex;align-items:center;gap:2px;min-height:34px;box-sizing:border-box;padding:0;border-bottom:1px solid var(--fy-border);background:transparent}
.dsh-fishyume-tabs button{border-color:transparent;color:var(--fy-text-secondary);font-weight:500}
.dsh-fishyume-tabs button[aria-current=page]{color:var(--fy-text-primary);background:var(--fy-bg-elevated);border-color:var(--fy-border-strong)}
.dsh-fishyume-columns{display:grid;grid-template-columns:minmax(210px,.68fr) minmax(0,1.8fr);min-height:0;flex:1}
.dsh-fishyume-list{padding:12px;overflow:auto;border-right:1px solid var(--fy-border);background:var(--fy-bg-surface)}
.dsh-fishyume-list:has(button small:nth-of-type(2)){padding:0;background:var(--fy-bg-canvas)}
.dsh-fishyume-list:has(button small:nth-of-type(2))::before{content:'团队任务';display:flex;align-items:center;height:56px;padding:0 24px;border-bottom:1px solid var(--fy-border);color:var(--fy-text-primary);font-size:15px;font-weight:600}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button{position:relative;min-height:92px;padding:16px 24px;border:0;border-bottom:1px solid var(--fy-border);border-radius:0}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button::before{content:'';position:absolute;left:0;top:0;bottom:0;width:3px;background:transparent}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button.is-selected::before{background:var(--fy-accent-soft)}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button small{display:inline-block;margin-right:12px}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button small:first-of-type::before{content:'';display:inline-block;width:8px;height:8px;margin:0 7px 1px 0;border-radius:50%;background:var(--fy-success)}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button small:nth-of-type(1){color:var(--fy-text-secondary)}
.dsh-fishyume-list:has(button small:nth-of-type(2))>button small:nth-of-type(2){color:var(--fy-text-tertiary)}
.dsh-fishyume-list button,.dsh-fishyume-driver{display:grid;gap:4px;width:100%;box-sizing:border-box;min-height:56px;padding:10px;border:1px solid transparent;border-radius:8px;text-align:left;color:var(--fy-text-primary);background:transparent}
.dsh-fishyume-list button{cursor:pointer}
.dsh-fishyume-list button:hover,.dsh-fishyume-list button.is-selected{background:var(--fy-bg-elevated);border-color:var(--fy-border-strong)}
.dsh-fishyume-list strong{font-size:13px;line-height:18px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dsh-fishyume-list small,.dsh-fishyume-detail small,.dsh-fishyume-detail p,.dsh-fishyume-driver small{color:var(--fy-text-secondary)}
.dsh-fishyume-detail{min-width:0;padding:24px;overflow:auto;background:var(--fy-bg-canvas)}
.dsh-fishyume-detail:has(.dsh-fishyume-members){padding:28px 32px}
.dsh-fishyume-detail:has(.dsh-fishyume-members)>h3{max-width:900px;margin-top:8px;font-size:22px}
.dsh-fishyume-detail:has(.dsh-fishyume-members)>p{max-width:900px;font-size:15px}
.dsh-fishyume-detail:has(.dsh-fishyume-members)>h4:first-of-type{margin-top:28px;padding-top:18px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-detail h3{margin:4px 0 12px;font-size:20px;line-height:28px;font-weight:650}
.dsh-fishyume-detail h4{margin:24px 0 10px;font-size:15px;line-height:22px;font-weight:600;color:var(--fy-text-primary)}
.dsh-fishyume-detail p{margin:0 0 12px;line-height:21px}
.dsh-fishyume-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;margin:16px 0 22px}
.dsh-fishyume-metric{padding:10px;border:1px solid var(--fy-border);border-radius:6px;background:var(--fy-bg-surface)}
.dsh-fishyume-metric span{display:block;color:var(--fy-text-tertiary);font-size:11px;line-height:16px}.dsh-fishyume-metric strong{display:block;margin-top:2px;font-size:14px;line-height:20px}
.dsh-fishyume-members{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:8px}
.dsh-fishyume-member{display:grid;gap:8px;padding:14px 14px 12px;border:1px solid var(--fy-border);border-radius:8px;background:var(--fy-bg-elevated)}
.dsh-fishyume-member strong{font-size:14px;line-height:20px}.dsh-fishyume-member small{color:var(--fy-text-secondary)}
.dsh-fishyume-member-duty{display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:2;overflow:hidden;margin:0;color:var(--fy-text-secondary);font-size:13px;line-height:20px;min-height:40px;max-height:40px}
.dsh-fishyume-member-divider{height:1px;background:var(--fy-border)}
.dsh-fishyume-member-row{display:grid;grid-template-columns:58px minmax(0,1fr);align-items:center;gap:10px;min-height:28px;color:var(--fy-text-secondary);font-size:12px}
.dsh-fishyume-member-row>span:first-child{color:var(--fy-text-tertiary)}
.dsh-fishyume-harness,.dsh-fishyume-model{display:inline-flex;align-items:center;justify-content:flex-end;gap:6px;justify-self:end;max-width:100%;min-height:24px;box-sizing:border-box;padding:2px 8px;border:1px solid var(--fy-border-strong);border-radius:999px;font-size:12px;line-height:18px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.dsh-fishyume-harness svg,.dsh-fishyume-model svg,.dsh-fishyume-harness img,.dsh-fishyume-model img{display:block;width:16px;height:16px;flex:none;object-fit:contain;border-radius:3px}.dsh-fishyume-harness img{filter:none}.dsh-fishyume-model img{padding:1px;box-sizing:border-box;background:var(--fy-bg-elevated)}
.dsh-fishyume-harness[data-driver="claude"]{color:#ff9b5f;border-color:#75431f;background:#2d1d14}
.dsh-fishyume-harness[data-driver="opencode"]{color:#69b9ff;border-color:#214e70;background:#122232}
.dsh-fishyume-harness[data-driver="codex"]{color:#72d6a1;border-color:#235f46;background:#11281e}
.dsh-fishyume-harness[data-driver="deepseek"]{color:#8ca7ff;border-color:#354a8f;background:#151c38}
.dsh-fishyume-model{color:var(--fy-text-secondary);border-color:var(--fy-border-strong);background:var(--fy-bg-surface)}
.dsh-fishyume-resource{display:grid;gap:8px;margin-top:18px;padding-top:16px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-resource-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:8px 0;border-bottom:1px solid var(--fy-border);color:var(--fy-text-secondary)}
.dsh-fishyume-resource-row strong{color:var(--fy-text-primary);font-weight:600}
.dsh-fishyume-message{position:relative;display:grid;grid-template-columns:30px max-content minmax(0,1fr);align-items:start;gap:0 10px;padding:10px 0;border-bottom:1px solid var(--fy-border)}
.dsh-fishyume-message::before{content:'·';display:grid;place-items:center;grid-column:1;width:26px;height:26px;margin-top:2px;border:1px solid var(--fy-border-strong);border-radius:50%;color:var(--fy-accent-soft);background:var(--fy-bg-elevated);font-size:18px;line-height:1}
.dsh-fishyume-message small{display:block;grid-column:2;margin:6px 0 0;color:var(--fy-text-tertiary);font-size:11px;line-height:16px;white-space:nowrap}
.dsh-fishyume-message>div{grid-column:3;min-width:0;padding:8px 10px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-primary);background:var(--fy-bg-surface);white-space:pre-wrap;overflow-wrap:anywhere;line-height:20px}
.dsh-fishyume-agent-status{margin-top:24px;padding-top:18px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-agent-status h4{margin:0 0 8px}
.dsh-fishyume-agent-stream{display:grid;gap:0}
.dsh-fishyume-agent-row{display:grid;grid-template-columns:10px max-content minmax(0,1fr);align-items:start;gap:8px;padding:8px 0;border-bottom:1px solid var(--fy-border);font-size:12px}
.dsh-fishyume-agent-row::before{content:'';width:7px;height:7px;margin-top:5px;border-radius:50%;background:var(--fy-info)}
.dsh-fishyume-agent-row[data-kind="output"]::before{background:var(--fy-success)}
.dsh-fishyume-agent-row[data-kind="command"]::before{background:var(--fy-warning)}
.dsh-fishyume-agent-row[data-kind="tool"]::before{background:var(--fy-accent-soft)}
.dsh-fishyume-agent-row small{color:var(--fy-text-tertiary);white-space:nowrap}
.dsh-fishyume-agent-row span:last-child{min-width:0;color:var(--fy-text-secondary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dsh-fishyume-agent-output{padding:0;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-agent-output summary{display:grid;grid-template-columns:max-content minmax(0,1fr) max-content;align-items:center;gap:10px;padding:11px 0;cursor:pointer;list-style:none}.dsh-fishyume-agent-output summary::-webkit-details-marker{display:none}.dsh-fishyume-agent-output summary>span:first-child{display:grid;gap:2px;min-width:0}.dsh-fishyume-agent-output strong{font-size:13px;color:var(--fy-text-primary)}.dsh-fishyume-agent-output small{color:var(--fy-text-tertiary);font-size:11px}.dsh-fishyume-output-files{display:flex;flex-wrap:wrap;align-items:center;gap:5px;min-width:0}.dsh-fishyume-output-file{display:inline-flex;align-items:center;max-width:100%;padding:2px 7px;border:1px solid var(--fy-border-strong);border-radius:999px;color:var(--fy-text-secondary);background:var(--fy-bg-surface);font-size:11px;line-height:16px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dsh-fishyume-output-chevron{color:var(--fy-text-tertiary);font-size:17px;line-height:18px;transition:transform .2s ease}.dsh-fishyume-agent-output[open] .dsh-fishyume-output-chevron{transform:rotate(180deg);color:var(--fy-accent-soft)}.dsh-fishyume-agent-output-body{display:grid;grid-template-rows:0fr;opacity:0;overflow:hidden;transition:grid-template-rows .2s ease,opacity .16s ease}.dsh-fishyume-agent-output[open] .dsh-fishyume-agent-output-body{grid-template-rows:1fr;opacity:1}.dsh-fishyume-agent-output-inner{min-height:0;overflow:hidden;padding:0 0 12px}.dsh-fishyume-agent-output-body>div>p{margin:8px 0 0;padding:9px 10px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-primary);background:var(--fy-bg-surface);white-space:pre-wrap;overflow-wrap:anywhere;line-height:20px}
.dsh-fishyume-member-details{margin-top:12px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-member-details summary{padding:10px 0;color:var(--fy-accent-soft);font-size:12px;cursor:pointer}
.dsh-fishyume-member-detail{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:5px 14px;padding:0 0 12px;color:var(--fy-text-secondary);font-size:12px}
.dsh-fishyume-member-detail dt{color:var(--fy-text-tertiary)}.dsh-fishyume-member-detail dd{margin:0;overflow-wrap:anywhere}
.dsh-fishyume-member{cursor:pointer;transition:border-color .15s,background .15s}.dsh-fishyume-member:hover,.dsh-fishyume-member[data-selected]{border-color:var(--fy-accent-soft);background:var(--fy-bg-hover)}
.dsh-fishyume-member-drawer-backdrop{position:absolute;inset:0;background:rgba(0,0,0,.36);z-index:70}
.dsh-fishyume-member-drawer-backdrop{padding:0;border:0;cursor:pointer}
.dsh-fishyume-member-drawer{position:absolute;inset:0 0 0 auto;z-index:71;width:min(420px,46%);min-width:320px;box-sizing:border-box;padding:20px 22px;overflow:auto;border-left:1px solid var(--fy-border-strong);background:var(--fy-bg-surface);box-shadow:-12px 0 32px rgba(0,0,0,.28)}
.dsh-fishyume-member-drawer header{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;padding-bottom:18px;border-bottom:1px solid var(--fy-border)}
.dsh-fishyume-member-drawer header button{width:32px;height:32px;padding:0}.dsh-fishyume-member-drawer h3{margin:2px 0 6px;font-size:18px;line-height:25px}.dsh-fishyume-member-drawer p{margin:0;color:var(--fy-text-secondary)}
.dsh-fishyume-member-drawer h4{margin:22px 0 10px}.dsh-fishyume-member-drawer dl{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:8px 14px;margin:0}.dsh-fishyume-member-drawer dt{color:var(--fy-text-tertiary)}.dsh-fishyume-member-drawer dd{margin:0;color:var(--fy-text-secondary);overflow-wrap:anywhere}
.dsh-fishyume-member-activity{display:grid;gap:0;border-top:1px solid var(--fy-border)}.dsh-fishyume-member-activity>div{padding:10px 0;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-member-activity small{display:block;color:var(--fy-text-tertiary);font-size:11px}.dsh-fishyume-member-activity p{margin:4px 0 0;color:var(--fy-text-secondary);white-space:pre-wrap;overflow-wrap:anywhere}
.dsh-fishyume-live-status{margin-top:22px}.dsh-fishyume-live-status-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.dsh-fishyume-live-status-heading h4{margin:0}.dsh-fishyume-live-indicator{display:inline-flex;align-items:center;gap:6px;color:var(--fy-text-tertiary);font-size:12px}.dsh-fishyume-live-indicator::before{content:'';width:8px;height:8px;border-radius:50%;background:#68727d}.dsh-fishyume-live-indicator[data-live]{color:#52d4e8}.dsh-fishyume-live-indicator[data-live]::before{background:#33c4d7;box-shadow:0 0 0 3px rgba(51,196,215,.14)}
.dsh-fishyume-live-scroll{position:relative;height:336px;min-height:336px;max-height:336px;overflow-y:scroll;overscroll-behavior:contain;padding:8px 12px 8px 0;border:1px solid var(--fy-border);border-radius:8px;background:var(--fy-bg-canvas);scrollbar-width:thin;scrollbar-color:var(--fy-border-strong) transparent}.dsh-fishyume-live-scroll::-webkit-scrollbar{width:7px}.dsh-fishyume-live-scroll::-webkit-scrollbar-thumb{border-radius:999px;background:var(--fy-border-strong)}.dsh-fishyume-live-scroll::-webkit-scrollbar-track{background:transparent}
.dsh-fishyume-live-timeline{position:relative;display:grid;gap:0;padding:0 12px 0 42px}.dsh-fishyume-live-timeline::before{content:'';position:absolute;left:20px;top:15px;bottom:15px;width:1px;background:var(--fy-border-strong)}.dsh-fishyume-live-item{position:relative;display:grid;grid-template-columns:minmax(0,1fr) max-content;gap:8px;padding:9px 0;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-live-item::before{content:'';position:absolute;left:-28px;top:15px;width:9px;height:9px;border:2px solid var(--fy-bg-canvas);border-radius:50%;background:#68727d;box-shadow:0 0 0 1px #68727d}.dsh-fishyume-live-item[data-tone="green"]::before{background:#39b86a;box-shadow:0 0 0 1px #39b86a}.dsh-fishyume-live-item[data-tone="cyan"]::before{width:11px;height:11px;left:-29px;top:14px;background:#33c4d7;box-shadow:0 0 0 1px #33c4d7,0 0 0 5px rgba(51,196,215,.16)}.dsh-fishyume-live-label{min-width:0;color:var(--fy-text-primary);font-size:13px;line-height:19px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dsh-fishyume-live-time{color:var(--fy-text-tertiary);font-size:11px;line-height:19px;white-space:nowrap}.dsh-fishyume-live-detail{grid-column:1/-1;margin:0;color:var(--fy-text-secondary);font-size:12px;line-height:18px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dsh-fishyume-live-history{position:absolute;right:0;bottom:0;left:0;display:flex;justify-content:center;padding:12px 0 10px;border-top:1px solid rgba(40,40,44,.86);background:linear-gradient(transparent,var(--fy-bg-canvas) 35%);pointer-events:none}.dsh-fishyume-live-history button{pointer-events:auto;color:var(--fy-text-secondary);background:transparent;border:0;font:12px/18px inherit;cursor:pointer}.dsh-fishyume-live-history button:hover{color:var(--fy-text-primary)}
.dsh-fishyume-team-subnav{display:flex;align-items:center;gap:6px;min-height:48px;padding:0 20px;border-bottom:1px solid var(--fy-border);background:var(--fy-bg-surface)}
.dsh-fishyume-team-switcher{position:relative}.dsh-fishyume-team-switcher>button{display:inline-flex;align-items:center;gap:7px;height:30px;padding:0 9px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-primary);background:var(--fy-bg-elevated);font:600 13px/18px inherit;cursor:pointer}.dsh-fishyume-team-switcher>button span:last-child{color:var(--fy-text-tertiary);font-size:11px}
.dsh-fishyume-team-menu{position:absolute;z-index:80;top:36px;left:0;width:156px;padding:5px;border:1px solid var(--fy-border-strong);border-radius:6px;background:var(--fy-bg-elevated);box-shadow:0 8px 24px rgba(0,0,0,.3)}.dsh-fishyume-team-menu button{display:flex;align-items:center;justify-content:space-between;width:100%;height:30px;padding:0 8px;border:0;border-radius:4px;color:var(--fy-text-secondary);background:transparent;font:500 13px/18px inherit;text-align:left;cursor:pointer}.dsh-fishyume-team-menu button:hover,.dsh-fishyume-team-menu button[data-selected]{color:var(--fy-text-primary);background:var(--fy-bg-hover)}.dsh-fishyume-team-menu button[data-selected] span:last-child{color:#33c4d7}
.dsh-fishyume-template-page{display:grid;grid-template-columns:minmax(0,1fr) minmax(260px,32%);min-height:0;flex:1;overflow:auto}.dsh-fishyume-template-main{min-width:0;padding:24px 28px 36px}.dsh-fishyume-template-summary{min-width:0;padding:56px 24px 32px;border-left:1px solid var(--fy-border);background:var(--fy-bg-surface)}.dsh-fishyume-template-header{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;padding-bottom:20px;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-template-header h3{margin:3px 0 6px;font-size:20px;line-height:28px;font-weight:650}.dsh-fishyume-template-header p{margin:0;color:var(--fy-text-secondary)}.dsh-fishyume-template-actions{display:flex;gap:8px;flex:none}.dsh-fishyume-template-actions button,.dsh-fishyume-template-toolbar button{height:32px;padding:0 12px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-secondary);background:transparent;font:600 13px/18px inherit;cursor:pointer}.dsh-fishyume-template-actions button:hover,.dsh-fishyume-template-toolbar button:hover{color:var(--fy-text-primary);background:var(--fy-bg-hover)}.dsh-fishyume-template-actions button[data-primary],.dsh-fishyume-template-toolbar button[data-primary]{border-color:#2f9caf;color:#071114;background:#33c4d7}.dsh-fishyume-template-section{padding-top:22px}.dsh-fishyume-template-section>h4,.dsh-fishyume-template-summary h4{margin:0 0 12px;font-size:15px;line-height:22px}.dsh-fishyume-template-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px 16px}.dsh-fishyume-template-field{display:grid;gap:6px;min-width:0}.dsh-fishyume-template-field[data-wide]{grid-column:1/-1}.dsh-fishyume-template-field>span{color:var(--fy-text-secondary);font-size:12px;line-height:17px}.dsh-fishyume-template-field input,.dsh-fishyume-template-field textarea,.dsh-fishyume-template-member input,.dsh-fishyume-template-member select{box-sizing:border-box;width:100%;min-width:0;height:34px;padding:0 10px;border:1px solid var(--fy-border);border-radius:6px;outline:0;color:var(--fy-text-primary);background:var(--fy-bg-canvas);font:14px/21px inherit}.dsh-fishyume-template-field textarea{height:72px;padding-top:7px;resize:vertical}.dsh-fishyume-template-field input:focus,.dsh-fishyume-template-field textarea:focus,.dsh-fishyume-template-member input:focus,.dsh-fishyume-template-member select:focus{border-color:#33c4d7;box-shadow:0 0 0 2px rgba(51,196,215,.12)}.dsh-fishyume-template-color-row{display:flex;align-items:center;gap:8px;height:34px}.dsh-fishyume-template-color{width:20px;height:20px;padding:0;border:2px solid transparent;border-radius:50%;cursor:pointer}.dsh-fishyume-template-color[data-color="cyan"]{background:#33c4d7}.dsh-fishyume-template-color[data-color="violet"]{background:#9d7bea}.dsh-fishyume-template-color[data-color="blue"]{background:#4f8cff}.dsh-fishyume-template-color[data-color="green"]{background:#47c878}.dsh-fishyume-template-color[data-color="orange"]{background:#f2ae4a}.dsh-fishyume-template-color[data-color="red"]{background:#ed6b73}.dsh-fishyume-template-color[data-color="gray"]{background:#8d96a3}.dsh-fishyume-template-color[data-selected]{border-color:#f7f8f8;box-shadow:0 0 0 1px #33c4d7}
.dsh-fishyume-template-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.dsh-fishyume-template-toolbar h4{margin:0}.dsh-fishyume-template-members{display:grid;gap:12px}.dsh-fishyume-template-member{display:grid;gap:12px;padding:14px;border:1px solid var(--fy-border);border-radius:8px;background:var(--fy-bg-surface)}.dsh-fishyume-template-member-top{display:grid;grid-template-columns:30px minmax(120px,180px) minmax(0,1fr) max-content;align-items:center;gap:10px}.dsh-fishyume-template-avatar,.dsh-fishyume-template-summary-avatar{display:grid;place-items:center;width:28px;height:28px;border-radius:50%;color:#d9fbff;background:#075968;font-weight:650}.dsh-fishyume-template-member-grid{display:grid;grid-template-columns:minmax(150px,1fr) minmax(170px,1fr);gap:12px}.dsh-fishyume-template-member-select{display:grid;gap:6px}.dsh-fishyume-template-member-select>span{color:var(--fy-text-tertiary);font-size:11px;line-height:16px}.dsh-fishyume-template-member-select select{height:32px}.dsh-fishyume-template-member-tools{display:flex;gap:4px}.dsh-fishyume-template-member-tools button{width:28px;height:28px;padding:0;border:0;border-radius:5px;color:var(--fy-text-tertiary);background:transparent;font-size:16px;cursor:pointer}.dsh-fishyume-template-member-tools button:hover{color:var(--fy-text-primary);background:var(--fy-bg-hover)}.dsh-fishyume-template-permission-note{padding:8px 10px;border-top:1px solid var(--fy-border);color:var(--fy-text-tertiary);font-size:12px;line-height:17px}.dsh-fishyume-template-add{display:flex;align-items:center;justify-content:center;width:100%;height:36px;margin-top:2px;border:1px dashed var(--fy-border-strong);border-radius:6px;color:var(--fy-text-tertiary);background:transparent;font:500 13px/18px inherit;cursor:pointer}.dsh-fishyume-template-add:hover{color:var(--fy-text-primary);border-color:#33c4d7}.dsh-fishyume-template-list{display:grid;gap:0;border-top:1px solid var(--fy-border)}.dsh-fishyume-template-list-row{display:grid;grid-template-columns:minmax(0,1fr) max-content max-content;align-items:center;gap:18px;padding:14px 0;border-bottom:1px solid var(--fy-border);color:var(--fy-text-primary);background:transparent;text-align:left;cursor:pointer}.dsh-fishyume-template-list-row:hover{background:var(--fy-bg-hover)}.dsh-fishyume-template-list-row strong{font-size:14px}.dsh-fishyume-template-list-row small{color:var(--fy-text-tertiary);font-size:12px}.dsh-fishyume-template-empty{display:grid;place-items:center;min-height:280px;color:var(--fy-text-tertiary);text-align:center}.dsh-fishyume-template-summary h3{margin:0 0 20px;font-size:18px;line-height:25px}.dsh-fishyume-template-summary-list{display:grid;margin:0 0 26px}.dsh-fishyume-template-summary-row{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:16px;padding:9px 0;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-template-summary-row dt{color:var(--fy-text-tertiary)}.dsh-fishyume-template-summary-row dd{margin:0;color:var(--fy-text-primary);overflow-wrap:anywhere}.dsh-fishyume-template-summary-members{display:grid;gap:10px;margin-bottom:26px}.dsh-fishyume-template-summary-member{display:grid;grid-template-columns:28px minmax(0,1fr) max-content;align-items:center;gap:8px}.dsh-fishyume-template-summary-member small{color:var(--fy-text-secondary);white-space:nowrap}.dsh-fishyume-template-command{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:12px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-primary);background:var(--fy-bg-canvas);font:12px/18px ui-monospace,SFMono-Regular,Menlo,monospace}.dsh-fishyume-template-command button{width:28px;height:28px;padding:0;border:0;border-radius:5px;color:var(--fy-text-secondary);background:transparent;cursor:pointer}.dsh-fishyume-template-command button:hover{color:var(--fy-text-primary);background:var(--fy-bg-hover)}.dsh-fishyume-template-summary-note{margin-top:28px;color:var(--fy-text-tertiary);font-size:12px;line-height:18px}
 .dsh-fishyume-template-field[data-aligned]{grid-template-rows:17px 34px 16px}.dsh-fishyume-template-harness-control{display:grid;grid-template-columns:minmax(0,1fr) max-content;align-items:center;gap:8px;min-width:0}.dsh-fishyume-template-harness-control .dsh-fishyume-harness{justify-self:start;max-width:100%;justify-content:flex-start}.dsh-fishyume-template-harness-control select{min-width:0}.dsh-fishyume-template-summary-call{margin:0;color:var(--fy-text-secondary);font-size:12px;line-height:19px}.dsh-fishyume-template-summary-call code{display:inline-block;padding:1px 5px;border:1px solid var(--fy-border);border-radius:4px;color:var(--fy-text-primary);background:var(--fy-bg-canvas);font:11px/16px ui-monospace,SFMono-Regular,Menlo,monospace}
 .dsh-fishyume-template-harness-control{grid-template-columns:max-content minmax(0,1fr);gap:4px}.dsh-fishyume-member{box-sizing:border-box;height:175px;min-height:175px;grid-template-rows:20px 40px 1px 28px 28px;overflow:hidden}.dsh-fishyume-member>strong{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.dsh-fishyume-member-row{min-width:0}.dsh-fishyume-member-row>span:last-child{min-width:0;overflow:hidden}
 .dsh-fishyume-detail:has(.dsh-fishyume-message)>h4:last-of-type{margin-top:28px;padding-top:18px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-detail:has(.dsh-fishyume-agent-status):not(:has(.dsh-fishyume-resource))>h4:last-of-type,.dsh-fishyume-detail:has(.dsh-fishyume-agent-status):not(:has(.dsh-fishyume-resource))>.dsh-fishyume-message{display:none}
.dsh-fishyume-handoff{margin-top:20px;padding-top:16px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-state{display:grid;place-content:center;gap:12px;flex:1;padding:32px;color:var(--fy-text-tertiary);text-align:center}
.dsh-fishyume-entry{position:relative;display:grid;place-items:center;width:32px;height:32px;margin:2px;border:1px solid var(--fy-border,#28282c);border-radius:6px;color:#fff;background:var(--fy-accent,#5e6ad2);font-weight:700;cursor:pointer}
.dsh-fishyume-entry b{position:absolute;right:1px;top:1px;width:6px;height:6px;border-radius:50%;background:var(--fy-danger,#eb5757)}
@media(prefers-reduced-motion:reduce){.dsh-fishyume-agent-output-body,.dsh-fishyume-output-chevron{transition:none!important}}
@media(max-width:700px){.dsh-fishyume-columns{grid-template-columns:1fr}.dsh-fishyume-list{max-height:36vh;border-right:0;border-bottom:1px solid var(--fy-border)}.dsh-fishyume-detail{padding:16px}.dsh-fishyume-message{grid-template-columns:30px minmax(0,1fr)}.dsh-fishyume-message small,.dsh-fishyume-message>div{grid-column:2}.dsh-fishyume-message small{margin-top:2px}.dsh-fishyume-message>div{margin-top:4px}.dsh-fishyume-member-drawer{inset:0;width:100%;min-width:0}}
`
const centerStyles = `
.dsh-fishyume-view{position:absolute;inset:0;display:none;z-index:60;background:var(--fy-bg-canvas,#08090a);min-width:0;min-height:0}
.dsh-fishyume-view[data-active]{display:flex}
.dsh-fishyume-view .dsh-fishyume-panel{width:100%;height:100%;border:0;margin:0}
.dsh-fishyume-back{display:inline-flex;align-items:center;gap:4px;margin-right:10px;padding:5px 12px!important;border-radius:8px!important;color:var(--fy-text-primary)!important;font-size:12px!important;line-height:18px!important}
.dsh-fishyume-back span:first-child{font-size:18px;line-height:1}
.dsh-fishyume-workflow-summary{display:flex;flex-wrap:wrap;gap:8px;margin-bottom:20px}
.dsh-fishyume-workflow-chip{display:inline-flex;align-items:center;gap:5px;padding:5px 9px;border:1px solid var(--fy-border);border-radius:6px;color:var(--fy-text-secondary);font-size:12px;line-height:17px}
.dsh-fishyume-workflow-chip strong{color:var(--fy-text-primary);font-weight:600}
.dsh-fishyume-workflow{display:flex;align-items:stretch;gap:8px;overflow-x:auto;padding:4px 2px 16px}
.dsh-fishyume-workflow-step{display:flex;align-items:center;gap:8px;min-width:220px}
.dsh-fishyume-workflow-arrow{color:var(--fy-text-tertiary);font-size:18px}
.dsh-fishyume-node{display:grid;gap:5px;width:168px;min-height:92px;padding:10px;border:1px solid var(--fy-border);border-left:3px solid var(--fy-text-tertiary);border-radius:6px;background:var(--fy-bg-elevated);cursor:pointer;text-align:left}
.dsh-fishyume-node:hover,.dsh-fishyume-node[data-selected]{border-color:var(--fy-border-strong);background:var(--fy-bg-hover)}
.dsh-fishyume-node[data-phase="running"]{border-left-color:var(--fy-info)}
.dsh-fishyume-node[data-phase="waiting"]{border-left-color:var(--fy-warning)}
.dsh-fishyume-node[data-phase="completed"]{border-left-color:var(--fy-success)}
.dsh-fishyume-node[data-conclusion="failed"]{border-left-color:var(--fy-danger)}
.dsh-fishyume-node-title{font-weight:600;color:var(--fy-text-primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dsh-fishyume-node-meta{color:var(--fy-text-secondary);font-size:12px;line-height:17px}
.dsh-fishyume-node-action{display:flex;gap:6px;margin-top:4px}
.dsh-fishyume-node-action button{padding:4px 8px!important;font-size:12px!important}
.dsh-fishyume-events{display:grid;gap:0;margin-top:20px;padding-top:16px;border-top:1px solid var(--fy-border)}
.dsh-fishyume-event{display:grid;grid-template-columns:64px minmax(0,1fr);gap:10px;width:100%;padding:10px 8px;border:0;border-bottom:1px solid var(--fy-border);border-radius:4px;color:var(--fy-text-primary);background:transparent;text-align:left;cursor:pointer}
.dsh-fishyume-event:hover,.dsh-fishyume-event[data-selected]{background:var(--fy-bg-hover)}
.dsh-fishyume-event-sequence{color:var(--fy-text-tertiary);font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
.dsh-fishyume-node-detail,.dsh-fishyume-event-detail{margin-top:16px;padding:14px 16px;border:1px solid var(--fy-border);border-radius:6px;background:var(--fy-bg-surface)}
.dsh-fishyume-node-detail h4,.dsh-fishyume-event-detail h4{margin:0 0 8px}.dsh-fishyume-node-detail dl,.dsh-fishyume-event-detail dl{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:5px 14px;margin:0}.dsh-fishyume-node-detail dt,.dsh-fishyume-event-detail dt{color:var(--fy-text-tertiary)}.dsh-fishyume-node-detail dd,.dsh-fishyume-event-detail dd{margin:0;color:var(--fy-text-secondary);overflow-wrap:anywhere}
.dsh-fishyume-node-detail ul{margin:6px 0 0;padding-left:18px;color:var(--fy-text-secondary)}
@media(max-width:700px){.dsh-fishyume-workflow-step{min-width:184px}.dsh-fishyume-node{width:152px}.dsh-fishyume-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}}
`
function updatePanel(patch: Partial<PanelState>): void {
  // Keep the current detail visible while the newly selected team is fetched.
  // Replacing the pane with an empty loading state makes task switching flash.
  const nextPatch = patch.selectedTeam && patch.teamDetail === undefined && panel.teamDetail ? { ...patch, teamDetail: panel.teamDetail } : patch
  panel = { ...panel, ...nextPatch }
  listeners.forEach((listener) => listener())
}
function subscribe(listener: () => void): () => void { listeners.add(listener); return () => listeners.delete(listener) }
function snapshot(): PanelState { return panel }
function togglePanel(): void { updatePanel({ open: !panel.open }) }

function sidebarRoot(): HTMLElement | undefined {
  const column = document.querySelector<HTMLElement>('[data-pane="sidebar"], [class*="sidebarCol"]')
  if (!column) return undefined
  return column.querySelector<HTMLElement>('[class*="logoRow"]')?.parentElement
    ?? (column.firstElementChild as HTMLElement | undefined)
}

function placeSidebarEntry(root: HTMLElement, entry: HTMLButtonElement): boolean {
  const newSession = root.querySelector<HTMLButtonElement>('button[class*="newSession"]')
    ?? Array.from(root.children).find((child): child is HTMLButtonElement => child instanceof HTMLButtonElement)
  if (!newSession) return false
  const logoRow = newSession.closest('[class*="logoRow"]')
  const anchor = logoRow?.parentElement === root ? logoRow.nextElementSibling : newSession.nextElementSibling
  if (entry.parentElement !== root) root.insertBefore(entry, anchor)
  return true
}

function mountSidebarEntry(): () => void {
  if (typeof document === 'undefined') return () => {}
  const selector = '[data-dsh-fishyume-entry]'
  const ownerKey = '__dshFishyumeSidebarEntry'
  const page = window as Window & { [ownerKey]?: HTMLButtonElement }
  const existing = document.querySelector<HTMLButtonElement>(selector)
  if (existing || page[ownerKey]) return () => {}
  const entry = document.createElement('button')
  entry.type = 'button'
  entry.dataset.dshFishyumeEntry = ''
  entry.dataset.dshPlugin = 'dsh-fishyume'
  entry.dataset.dshPart = 'sidebar-entry'
  entry.className = 'dsh-fishyume-sidebar-entry'
  entry.setAttribute('aria-label', '团队与 workflow')
  entry.title = '团队与 workflow'
  entry.innerHTML = `<span class="dsh-fishyume-sidebar-icon"><img src="${entryIcon}" alt="" aria-hidden="true"></span><span class="dsh-fishyume-sidebar-label">团队与 workflow</span>`
  entry.addEventListener('click', togglePanel)
  page[ownerKey] = entry
  const style = document.createElement('style')
  style.dataset.dshFishyumeStyle = ''
  style.textContent = `.dsh-fishyume-sidebar-entry{box-sizing:border-box;display:flex;align-items:center;gap:8px;width:100%;height:36px;margin:0 0 4px;padding:0 10px;border:0;border-radius:8px;background:transparent;color:var(--dsw-alias-label-secondary);cursor:pointer;font:500 13px var(--dsw-font-family,system-ui,sans-serif);text-align:left;white-space:nowrap}.dsh-fishyume-sidebar-entry:hover,.dsh-fishyume-sidebar-entry[data-active]{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary)}.dsh-fishyume-sidebar-entry[data-active]{background:var(--dsw-alias-interactive-bg-active);font-weight:600}.dsh-fishyume-sidebar-icon{display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;flex:none}.dsh-fishyume-sidebar-icon img{display:block;width:20px;height:20px;border-radius:4px;object-fit:cover}.dsh-fishyume-sidebar-label{min-width:0;overflow:hidden;text-overflow:ellipsis}.dsh-fishyume-sidebar-entry[data-active] .dsh-fishyume-sidebar-icon img{outline:1px solid var(--dsw-alias-brand-primary,#58a6ff)}[data-dsh-frame][data-sidebar-collapsed] .dsh-fishyume-sidebar-entry{justify-content:center;width:36px;height:36px;margin:0 auto 12px;padding:0;border-radius:50%}[data-dsh-frame][data-sidebar-collapsed] .dsh-fishyume-sidebar-label{display:none}`
  document.head.append(style)
  let root: HTMLElement | undefined
  const place = (): void => {
    root ??= sidebarRoot()
    if (root && !placeSidebarEntry(root, entry)) root = undefined
  }
  const observer = new MutationObserver(place)
  observer.observe(document.body, { childList: true, subtree: true })
  place()
  const sync = (): void => { if (panel.open) entry.dataset.active = 'true'; else delete entry.dataset.active }
  const activeListener = subscribe(sync)
  sync()
  return () => {
    observer.disconnect()
    activeListener()
    entry.remove()
    style.remove()
    if (page[ownerKey] === entry) delete page[ownerKey]
  }
}

async function rpc<T>(token: string, method: string, params: Record<string, unknown>): Promise<T> {
  void token
  return transport.call<T>(method, params)
}

function formatTeamMessage(message: TeamMessage): string {
  const content = message.content || ''
  if (!content || message.kind === 'host_message') return content
  try {
    const value = JSON.parse(content) as { contentMarkdown?: unknown; resultType?: unknown; output?: unknown }
    if (typeof value.contentMarkdown === 'string') return value.contentMarkdown
    if (typeof value.resultType === 'string' && value.output !== undefined) {
      const output = typeof value.output === 'string' ? value.output : JSON.stringify(value.output, null, 2)
      return `[${value.resultType}]\n${output}`
    }
  } catch {
    // Legacy messages are already displayable text.
  }
  return content
}

async function loadTeams(): Promise<void> {
  updatePanel({ loading: true, error: undefined })
  try {
    const listed = await rpc<{ items?: Team[] }>(await ensureToken(), 'team.list', { schemaVersion: 'fishyume.team/v1', limit: 100 })
    const teams = listed.items ?? []
    const selectedTeam = panel.selectedTeam ?? teams[0]?.teamId
    updatePanel({ teams, selectedTeam, loading: false })
    if (selectedTeam) void loadTeamDetail(selectedTeam)
  } catch (error) { updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) }) }
}

async function loadTeamTemplates(): Promise<void> {
  updatePanel({ loading: true, error: undefined })
  try {
    const token = await ensureToken()
    const [listed, capabilities] = await Promise.all([
      rpc<{ items?: TeamTemplate[] }>(token, 'team.template.list', { schemaVersion: 'fishyume.team-template/v1', limit: 100 }),
      rpc<TeamCapabilities>(token, 'team.capabilities', { schemaVersion: 'fishyume.team/v1' }),
    ])
    updatePanel({ templates: listed.items ?? [], capabilities, loading: false })
  } catch (error) {
    updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) })
  }
}

function templateModels(capabilities: TeamCapabilities | undefined, driver: string): Array<{ modelId: string; label: string }> {
	const harness = capabilities?.harnesses?.find((item) => item.driver.toLowerCase() === driver.toLowerCase())
	if (harness) return harness.models.map((model) => ({ modelId: model.modelId, label: model.label || model.model || model.modelId }))
	return (capabilities?.participantTemplates ?? [])
    .filter((item) => (item.driver || '').toLowerCase() === driver.toLowerCase())
    .filter((item): item is { modelId: string; label?: string } => Boolean(item.modelId))
    .map((item) => ({ modelId: item.modelId, label: item.label || modelName(item.modelId) }))
}

function defaultTemplateDraft(capabilities?: TeamCapabilities): TeamTemplateDraft {
	return {
    templateId: '',
    name: '',
    description: '',
    color: 'cyan',
		members: [
			{ label: '', roleHint: '', driver: '', modelId: '' },
			{ label: '', roleHint: '', driver: '', modelId: '' },
		],
  }
}

async function saveTeamTemplate(draft: TeamTemplateDraft): Promise<void> {
  const template = await rpc<{ template?: TeamTemplate }>(await ensureToken(), 'team.template.upsert', {
    schemaVersion: 'fishyume.team-template/v1',
    templateId: draft.templateId.trim(),
    name: draft.name.trim(),
    description: draft.description.trim(),
    color: draft.color,
    members: draft.members.map((member) => ({ ...member, label: member.label.trim(), roleHint: member.roleHint?.trim() || undefined })),
  })
  updatePanel({ selectedTemplate: template.template?.templateId, templateMode: 'list' })
  await loadTeamTemplates()
}

async function loadTeamDetail(teamId: string): Promise<void> {
  try {
    const token = await ensureToken()
    const [detail, messages, events] = await Promise.all([
      rpc<{ team?: TeamDetail; turns?: TeamTurn[] }>(token, 'team.get', { schemaVersion: 'fishyume.team/v1', teamId }),
      rpc<{ messages?: TeamMessage[] }>(token, 'team.messages', { schemaVersion: 'fishyume.team/v1', teamId, limit: 100 }),
      rpc<{ events?: RunEvent[] }>(token, 'team.events', { schemaVersion: 'fishyume.team/v1', teamId, afterSequence: 0, limit: 100 }),
    ])
    const formattedMessages = (messages.messages ?? []).map((message) => ({ ...message, content: formatTeamMessage(message) }))
    if (panel.selectedTeam === teamId) {
      updatePanel({ teamDetail: detail.team ? { ...detail.team, turns: detail.turns ?? detail.team.turns ?? [] } : undefined, teamMessages: formattedMessages, teamEvents: events.events ?? [] })
      void loadHandoffs(teamId)
    }
  } catch (error) {
    if (panel.selectedTeam === teamId) updatePanel({ error: error instanceof Error ? error.message : String(error) })
  }
}

async function ensureToken(): Promise<string> {
  if (panel.token) return panel.token
  const response = await fetch('/plugins/dsh-fishyume/token', { cache: 'no-store' })
  const data = await response.json() as { token?: string }
  if (!data.token) throw new Error('Fishyume token unavailable')
  updatePanel({ token: data.token })
  return data.token
}

async function loadRuns(): Promise<void> {
  updatePanel({ loading: true, error: undefined })
  try {
    const listed = await rpc<{ items?: Run[] }>(await ensureToken(), 'run.list', { limit: 100 })
    const runs = listed.items ?? []
    const selectedRun = panel.selectedRun ?? runs[0]?.runId
    updatePanel({ runs, selectedRun, loading: false })
    if (selectedRun) void loadRunDetail(selectedRun)
  } catch (error) { updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) }) }
}

async function loadRunDetail(runId: string): Promise<void> {
  try {
    const token = await ensureToken()
    const [detail, events] = await Promise.all([
      rpc<{ run?: RunDetail }>(token, 'run.get', { runId }),
      rpc<{ events?: RunEvent[] }>(token, 'run.events', { runId, afterSequence: 0, limit: 50 }),
    ])
    if (panel.selectedRun === runId) {
      const nodes = detail.run?.nodes ?? []
      const selectedNode = panel.selectedNode && nodes.some((node) => node.nodeId === panel.selectedNode) ? panel.selectedNode : nodes[0]?.nodeId
      updatePanel({ runDetail: detail.run, runEvents: events.events ?? [], selectedNode, selectedEvent: undefined })
    }
  } catch (error) { if (panel.selectedRun === runId) updatePanel({ error: error instanceof Error ? error.message : String(error) }) }
}

async function cancelRun(run: RunDetail & { stateVersion?: number }): Promise<void> {
  if (run.phase === 'completed' || run.cancelRequested) return
  if (typeof window !== 'undefined' && !window.confirm('Cancel this Run?')) return
  updatePanel({ loading: true, error: undefined })
  try {
    await rpc(await ensureToken(), 'run.action', { actionId: `dsh-fishyume-cancel-${run.runId}-${Date.now()}`, runId: run.runId, type: 'cancel', expectedStateVersion: run.stateVersion ?? 0 })
    await loadRunDetail(run.runId)
    updatePanel({ loading: false })
  } catch (error) { updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) }) }
}

async function runNodeAction(run: RunDetail, node: RunNode, type: 'approve' | 'reject' | 'retry'): Promise<void> {
  if (run.stateVersion === undefined) {
    updatePanel({ error: 'Run state version unavailable; refresh before acting' })
    return
  }
  if ((type === 'reject' || type === 'retry') && typeof window !== 'undefined' && !window.confirm(type === 'retry' ? 'Retry this node?' : 'Reject this approval?')) return
  updatePanel({ loading: true, error: undefined })
  try {
    await rpc(await ensureToken(), 'run.action', {
      actionId: `dsh-fishyume-${type}-${run.runId}-${node.nodeId}-${Date.now()}`,
      runId: run.runId,
      type,
      nodeId: node.nodeId,
      expectedStateVersion: run.stateVersion,
      ...(node.currentAttempt === undefined ? {} : { expectedAttempt: node.currentAttempt }),
      ...(type === 'reject' ? { reason: 'Rejected from Fishyume' } : {}),
      ...(type === 'retry' ? { acknowledgeDuplicateRisk: true } : {}),
    })
    await loadRunDetail(run.runId)
    updatePanel({ loading: false })
  } catch (error) { updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) }) }
}

async function loadRouting(): Promise<void> {
  updatePanel({ loading: true, error: undefined })
  try {
    const token = await ensureToken()
    const [drivers, routes, inventory] = await Promise.all([
      rpc<{ drivers?: Driver[] }>(token, 'driver.list', { schemaVersion: 'fishyume.config/v1' }),
      rpc<{ routes?: Route[] }>(token, 'team.routes.get', { schemaVersion: 'fishyume.config/v1' }),
      rpc<{ drivers?: Array<{ driver: string; version?: string; authenticated?: boolean }> }>(token, 'driver.inventory', { schemaVersion: 'fishyume.config/v1' }),
    ])
    const byDriver = new Map((inventory.drivers ?? []).map(entry => [entry.driver, entry]))
    const merged = (drivers.drivers ?? []).map(driver => ({...driver, version: byDriver.get(driver.driver)?.version, authenticated: byDriver.get(driver.driver)?.authenticated}))
    updatePanel({ drivers: merged, routes: routes.routes ?? [], loading: false })
  } catch (error) { updatePanel({ loading: false, error: error instanceof Error ? error.message : String(error) }) }
}

async function loadHandoffs(teamId: string, focusHandoff?: string): Promise<void> {
  try {
    const result = await rpc<{ items?: Handoff[] }>(await ensureToken(), 'team.handoff.list', { schemaVersion: 'fishyume.team/v1', teamId, limit: 100 })
    if (panel.selectedTeam === teamId) updatePanel({ handoffs: result.items ?? [], selectedHandoff: focusHandoff ?? panel.selectedHandoff })
  } catch (error) { updatePanel({ error: error instanceof Error ? error.message : String(error) }) }
}

function applyFocus(target: { kind?: string; teamId?: string; handoffId?: string; runId?: string }): void {
  if (target.kind === 'run' && target.runId) { updatePanel({ open: true, view: 'runs', selectedRun: target.runId }); void loadRuns(); return }
  if (target.kind === 'handoff' && target.teamId) { updatePanel({ open: true, view: 'teams', selectedTeam: target.teamId, selectedHandoff: target.handoffId }); void loadTeams(); void loadHandoffs(target.teamId, target.handoffId); return }
  if (target.kind === 'team' && target.teamId) { updatePanel({ open: true, view: 'teams', selectedTeam: target.teamId }); void loadTeams(); void loadHandoffs(target.teamId) }
}

function FishyumeEntry(): JSX.Element {
  const state = useSyncExternalStore(subscribe, snapshot)
  return <button type="button" className="dsh-fishyume-entry" aria-label="Fishyume" title="Fishyume" aria-pressed={state.open} onClick={togglePanel}><span aria-hidden="true">F</span>{state.teams.some((team) => team.status === 'running' || team.status === 'active') ? <b aria-label="active" /> : null}</button>
}

function ViewTabs({ view, setView }: { view: View; setView: (view: View) => void }): JSX.Element {
  return <nav className="dsh-fishyume-tabs" aria-label="Fishyume views">{(['teams', 'runs', 'routing'] as const).map((item) => <button key={item} type="button" aria-current={view === item ? 'page' : undefined} onClick={() => { setView(item); if (item === 'teams') void loadTeams(); if (item === 'runs') void loadRuns(); if (item === 'routing') void loadRouting() }}>{item === 'teams' ? 'Teams' : item === 'runs' ? 'Runs' : 'Routing'}</button>)}</nav>
}

function RunActions({ run, node }: { run: RunDetail; node?: RunNode }): JSX.Element | null {
  if (node) {
    if (node.type === 'approval' && node.phase === 'waiting') return <div><button type="button" onClick={() => void runNodeAction(run, node, 'approve')}>Approve</button><button type="button" onClick={() => void runNodeAction(run, node, 'reject')}>Reject</button></div>
    if (node.phase === 'completed' && (node.conclusion === 'failed' || node.conclusion === 'indeterminate')) return <button type="button" onClick={() => void runNodeAction(run, node, 'retry')}>Retry</button>
    return null
  }
  if (run.phase === 'completed' || run.cancelRequested) return null
  return <button type="button" onClick={() => void cancelRun(run)} aria-label="Cancel run" title="Cancel run">Cancel run</button>
}

function TeamWorkspace({ state }: { state: PanelState }): JSX.Element {
  const selected = state.teams.find((team) => team.teamId === state.selectedTeam)
  return <div className="dsh-fishyume-workspace">
    <header className="dsh-fishyume-header"><div className="dsh-fishyume-brand"><img className="dsh-fishyume-logo" src={fishyumeLogo} alt="" aria-hidden="true" /><div><span className="dsh-fishyume-eyebrow">FISHYUME</span><h2>{state.view === 'teams' ? 'Team workspace' : state.view === 'runs' ? 'Run workspace' : 'Routing workspace'}</h2></div></div><div><button type="button" onClick={() => { if (state.view === 'teams') void loadTeams(); else if (state.view === 'runs') void loadRuns(); else void loadRouting() }} disabled={state.loading} aria-label="Refresh" title="Refresh">↻</button><button type="button" onClick={() => updatePanel({ open: false })} aria-label="Close Fishyume" title="Close">×</button></div></header>
    <ViewTabs view={state.view} setView={(view) => updatePanel({ view })} />
    {state.error ? <div className="dsh-fishyume-state" role="alert">{state.error}<button type="button" onClick={() => { void loadTeams() }}>Retry</button></div> : null}
    {state.loading ? <div className="dsh-fishyume-state">Loading teams...</div> : null}
    {!state.loading && !state.error && state.view === 'teams' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="Teams">{state.teams.length === 0 ? <div className="dsh-fishyume-state">No teams yet.</div> : state.teams.map((team) => <button key={team.teamId} type="button" className={team.teamId === state.selectedTeam ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedTeam: team.teamId, selectedHandoff: undefined }); void loadHandoffs(team.teamId) }}><strong>{team.topic || team.teamId}</strong><small>{team.status || 'unknown'}</small></button>)}</div><section className="dsh-fishyume-detail" aria-label="Selected team">{selected ? <><span className="dsh-fishyume-eyebrow">TEAM</span><h3>{selected.topic || selected.teamId}</h3><p>{selected.project || 'Project unavailable'}</p><small>{selected.teamId}</small><h4>Handoffs</h4>{state.handoffs.length === 0 ? <p>No handoffs.</p> : state.handoffs.map((handoff) => <button key={handoff.handoffId} type="button" className={handoff.handoffId === state.selectedHandoff ? 'is-selected' : ''} onClick={() => updatePanel({ selectedHandoff: handoff.handoffId })}><strong>{handoff.goal}</strong><small>{handoff.handoffId}</small></button>)}{state.selectedHandoff ? <div className="dsh-fishyume-handoff"><span className="dsh-fishyume-eyebrow">HANDOFF</span><p>{state.handoffs.find((handoff) => handoff.handoffId === state.selectedHandoff)?.goal}</p></div> : null}</> : <div className="dsh-fishyume-state">Select a team.</div>}</section></div> : null}
    {!state.loading && !state.error && state.view === 'runs' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="Runs">{state.runs.length === 0 ? <div className="dsh-fishyume-state">No runs yet.</div> : state.runs.map((run) => <button key={run.runId} type="button" className={run.runId === state.selectedRun ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedRun: run.runId }); void loadRunDetail(run.runId) }}><strong>{run.workflowName || run.runId}</strong><small>{run.phase || 'unknown'}{run.conclusion ? ` · ${run.conclusion}` : ''}</small></button>)}</div><section className="dsh-fishyume-detail" aria-label="Selected run">{state.selectedRun ? <><span className="dsh-fishyume-eyebrow">RUN</span><h3>{state.runDetail?.workflowName || state.selectedRun}</h3><p>{state.runDetail?.summary || state.runDetail?.project || 'Loading run details...'}</p><small>{state.selectedRun} · {state.runDetail?.phase || 'unknown'}</small>{state.runDetail ? <RunActions run={state.runDetail} /> : null}{state.runDetail?.nodes?.length ? <><h4>Nodes</h4>{state.runDetail.nodes.map((node) => <div key={node.nodeId}><p><strong>{node.title || node.nodeId}</strong><br /><small>{node.phase || 'unknown'}{node.conclusion ? ` · ${node.conclusion}` : ''}{node.reason ? ` · ${node.reason}` : ''}</small></p><RunActions run={state.runDetail!} node={node} /></div>)}</> : null}{state.runEvents.length ? <><h4>Recent events</h4>{state.runEvents.slice(-8).reverse().map((event) => <p key={`${event.sequence}-${event.type}`}><strong>#{event.sequence} {event.type}</strong><br /><small>{event.message || event.phase || ''}</small></p>)}</> : null}</> : <div className="dsh-fishyume-state">Select a run.</div>}</section></div> : null}
    {!state.loading && !state.error && state.view === 'routing' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="Drivers">{state.drivers.length === 0 ? <div className="dsh-fishyume-state">No drivers found.</div> : state.drivers.map((driver) => <div key={driver.driver} className="dsh-fishyume-driver"><strong>{driver.driver}</strong><small>{driver.available ? 'Available' : driver.diagnostic || 'Unavailable'}{driver.version ? ` · ${driver.version}` : ''}{driver.authenticated === true ? ' · authenticated' : driver.authenticated === false ? ' · not authenticated' : ''}</small></div>)}</div><section className="dsh-fishyume-detail" aria-label="Effective routes"><span className="dsh-fishyume-eyebrow">ROUTING</span><h3>Effective routes</h3>{state.routes.length === 0 ? <p>No Team routes configured.</p> : state.routes.map((route) => <p key={route.routeId}><strong>{route.routeId}</strong><br /><small>{route.driver || 'unknown'} / {route.provider || 'default'} / {route.model || 'default'} · {route.enabled ? 'enabled' : 'disabled'}</small></p>)}</section></div> : null}
  </div>
}

function localizedPhase(value?: string): string {
  return ({ queued: '排队中', pending: '待执行', running: '执行中', waiting: '等待确认', prepared: '已准备', dispatching: '派发中', active: '执行中', responded: '已响应', completed: '已完成', failed: '失败', indeterminate: '状态不确定', cancelling: '取消中', cancelled: '已取消' } as Record<string, string>)[value ?? ''] ?? value ?? '未知'
}

function localizedConclusion(value?: string): string {
  return ({ succeeded: '成功', success: '成功', failed: '失败', indeterminate: '结果不确定', cancelled: '已取消' } as Record<string, string>)[value ?? ''] ?? value ?? ''
}

function localizedNodeType(value?: string): string {
  return ({ agent: '智能体任务', task: '任务', approval: '人工审批', condition: '条件判断', transform: '数据转换', notification: '通知', shell: '命令执行' } as Record<string, string>)[(value || '').toLowerCase()] ?? value ?? '任务'
}

function localizedEventType(value?: string): string {
  return ({ node_started: '节点开始', node_completed: '节点完成', node_failed: '节点失败', node_waiting: '节点等待', run_started: '工作流开始', run_completed: '工作流完成', run_failed: '工作流失败', approval_requested: '等待审批', approval_resolved: '审批完成', retry_scheduled: '已安排重试', state_changed: '状态变更' } as Record<string, string>)[(value || '').toLowerCase()] ?? value ?? '状态更新'
}

function localizedDriver(value?: string): string {
  return ({ codex: 'Codex', claude: 'Claude Code', opencode: 'OpenCode', deepseek: 'DeepSeek' } as Record<string, string>)[(value || '').toLowerCase()] ?? value ?? '未知执行器'
}

function LocalizedRunActions({ run, node }: { run: RunDetail; node?: RunNode }): JSX.Element | null {
  if (node?.type === 'approval' && node.phase === 'waiting') return <div className="dsh-fishyume-node-action"><button type="button" onClick={() => void runNodeAction(run, node, 'approve')}>批准</button><button type="button" onClick={() => void runNodeAction(run, node, 'reject')}>拒绝</button></div>
  if (node?.phase === 'completed' && (node.conclusion === 'failed' || node.conclusion === 'indeterminate')) return <div className="dsh-fishyume-node-action"><button type="button" onClick={() => void runNodeAction(run, node, 'retry')}>重试</button></div>
  if (!node && run.phase !== 'completed' && !run.cancelRequested) return <button type="button" onClick={() => void cancelRun(run)} aria-label="取消工作流">取消工作流</button>
  return null
}

function LocalizedWorkflowDetail({ state, run }: { state: PanelState; run: RunDetail }): JSX.Element {
  const nodes = run.nodes ?? []
  return <section className="dsh-fishyume-detail" aria-label="工作流详情">
    <div className="dsh-fishyume-workflow-summary"><span className="dsh-fishyume-workflow-chip"><strong>状态</strong> {localizedPhase(run.phase)}{run.conclusion ? ` · ${localizedConclusion(run.conclusion)}` : ''}</span>{run.driver ? <span className="dsh-fishyume-workflow-chip"><strong>执行器</strong> {run.driver}</span> : null}{run.project ? <span className="dsh-fishyume-workflow-chip"><strong>项目</strong> {run.project}</span> : null}{run.stateVersion !== undefined ? <span className="dsh-fishyume-workflow-chip"><strong>版本</strong> {run.stateVersion}</span> : null}</div>
    <p>{run.summary || '暂无工作流摘要。'}</p>
    <LocalizedRunActions run={run} />
    {nodes.length > 0 ? <><h4>执行流程</h4><div className="dsh-fishyume-workflow" aria-label="工作流节点流程">{nodes.map((node, index) => <div className="dsh-fishyume-workflow-step" key={node.nodeId}>{index > 0 ? <span className="dsh-fishyume-workflow-arrow" aria-hidden="true">→</span> : null}<article className="dsh-fishyume-node" data-phase={node.phase ?? 'pending'} data-conclusion={node.conclusion}><span className="dsh-fishyume-node-meta">节点 {index + 1} · {node.type || '任务'}</span><strong className="dsh-fishyume-node-title" title={node.title || node.nodeId}>{node.title || node.nodeId}</strong><span className="dsh-fishyume-node-meta">{localizedPhase(node.phase)}{node.conclusion ? ` · ${localizedConclusion(node.conclusion)}` : ''}</span>{node.reason || node.message ? <span className="dsh-fishyume-node-meta">{node.reason || node.message}</span> : null}{node.currentAttempt !== undefined ? <span className="dsh-fishyume-node-meta">尝试次数：{node.currentAttempt}</span> : null}<LocalizedRunActions run={run} node={node} /></article></div>)}</div></> : <div className="dsh-fishyume-state">暂无节点数据。</div>}
    {state.runEvents.length > 0 ? <div className="dsh-fishyume-events"><h4>事件时间线</h4>{state.runEvents.slice(-12).reverse().map((event) => <div className="dsh-fishyume-event" key={`${event.sequence}-${event.type}`}><span className="dsh-fishyume-event-sequence">#{event.sequence}</span><span><strong>{event.type}</strong><br /><small>{event.message || localizedPhase(event.phase) || '状态更新'}</small></span></div>)}</div> : null}
  </section>
}

function LocalizedWorkspace({ state }: { state: PanelState }): JSX.Element {
  const selected = state.teams.find((team) => team.teamId === state.selectedTeam)
  return <div className="dsh-fishyume-workspace">
    <header className="dsh-fishyume-header"><div className="dsh-fishyume-brand"><button type="button" className="dsh-fishyume-back" data-dsh-center-view-back="" aria-label="返回会话" onClick={() => updatePanel({ open: false })}><span aria-hidden="true">←</span><span>返回会话</span></button><img className="dsh-fishyume-logo" src={fishyumeLogo} alt="" aria-hidden="true" /><div><span className="dsh-fishyume-eyebrow">FISHYUME</span><h2>{state.view === 'teams' ? '团队工作区' : state.view === 'runs' ? '工作流执行' : '路由配置'}</h2></div></div><div><button type="button" onClick={() => { if (state.view === 'teams') void loadTeams(); else if (state.view === 'runs') void loadRuns(); else void loadRouting() }} disabled={state.loading} aria-label="刷新" title="刷新">↻</button></div></header>
    <nav className="dsh-fishyume-tabs" aria-label="Fishyume 视图">{(['teams', 'runs', 'routing'] as const).map((item) => <button key={item} type="button" aria-current={state.view === item ? 'page' : undefined} onClick={() => { updatePanel({ view: item }); if (item === 'teams') void loadTeams(); if (item === 'runs') void loadRuns(); if (item === 'routing') void loadRouting() }}>{item === 'teams' ? '团队' : item === 'runs' ? '工作流' : '路由'}</button>)}</nav>
    {state.error ? <div className="dsh-fishyume-state" role="alert">{state.error}<button type="button" onClick={() => { void loadTeams() }}>重试</button></div> : null}
    {state.loading ? <div className="dsh-fishyume-state">正在加载...</div> : null}
    {!state.loading && !state.error && state.view === 'teams' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="团队列表">{state.teams.length === 0 ? <div className="dsh-fishyume-state">暂无团队。</div> : state.teams.map((team) => <button key={team.teamId} type="button" className={team.teamId === state.selectedTeam ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedTeam: team.teamId, selectedHandoff: undefined }); void loadHandoffs(team.teamId) }}><strong>{team.topic || team.teamId}</strong><small>{localizedPhase(team.status)}</small></button>)}</div><section className="dsh-fishyume-detail" aria-label="团队详情">{selected ? <><span className="dsh-fishyume-eyebrow">团队</span><h3>{selected.topic || selected.teamId}</h3><p>{selected.project || '项目路径不可用'}</p><small>{selected.teamId}</small><h4>交接</h4>{state.handoffs.length === 0 ? <p>暂无交接。</p> : state.handoffs.map((handoff) => <button key={handoff.handoffId} type="button" className={handoff.handoffId === state.selectedHandoff ? 'is-selected' : ''} onClick={() => updatePanel({ selectedHandoff: handoff.handoffId })}><strong>{handoff.goal}</strong><small>{handoff.handoffId}</small></button>)}{state.selectedHandoff ? <div className="dsh-fishyume-handoff"><span className="dsh-fishyume-eyebrow">交接详情</span><p>{state.handoffs.find((handoff) => handoff.handoffId === state.selectedHandoff)?.goal}</p></div> : null}</> : <div className="dsh-fishyume-state">选择一个团队。</div>}</section></div> : null}
    {!state.loading && !state.error && state.view === 'runs' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="工作流列表">{state.runs.length === 0 ? <div className="dsh-fishyume-state">暂无工作流。</div> : state.runs.map((run) => <button key={run.runId} type="button" className={run.runId === state.selectedRun ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedRun: run.runId }); void loadRunDetail(run.runId) }}><strong>{run.workflowName || run.runId}</strong><small>{localizedPhase(run.phase)}{run.conclusion ? ` · ${localizedConclusion(run.conclusion)}` : ''}</small></button>)}</div>{state.selectedRun ? (state.runDetail ? <LocalizedWorkflowDetail state={state} run={state.runDetail} /> : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">正在加载工作流详情...</div></section>) : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">选择一个工作流。</div></section>}</div> : null}
    {!state.loading && !state.error && state.view === 'routing' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="执行器列表">{state.drivers.length === 0 ? <div className="dsh-fishyume-state">暂无执行器。</div> : state.drivers.map((driver) => <div key={driver.driver} className="dsh-fishyume-driver"><strong>{driver.driver}</strong><small>{driver.available ? '可用' : driver.diagnostic || '不可用'}{driver.version ? ` · ${driver.version}` : ''}{driver.authenticated === true ? ' · 已认证' : driver.authenticated === false ? ' · 未认证' : ''}</small></div>)}</div><section className="dsh-fishyume-detail" aria-label="生效路由"><span className="dsh-fishyume-eyebrow">路由</span><h3>生效路由</h3>{state.routes.length === 0 ? <p>暂无团队路由配置。</p> : state.routes.map((route) => <p key={route.routeId}><strong>{route.routeId}</strong><br /><small>{route.driver || '未知'} / {route.provider || '默认'} / {route.model || '默认'} · {route.enabled ? '已启用' : '已停用'}</small></p>)}</section></div> : null}
  </div>
}

function nativeTime(value?: string | number): string {
  if (value === undefined) return '时间未知'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? String(value) : new Intl.DateTimeFormat('zh-CN', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(date)
}

function NativeMetric({ label, value }: { label: string; value: string | number }): JSX.Element {
  return <div className="dsh-fishyume-metric"><span>{label}</span><strong>{value}</strong></div>
}

function NodeInspector({ node: sourceNode }: { node: RunNode }): JSX.Element {
  const node: RunNode = { ...sourceNode, type: localizedNodeType(sourceNode.type), attempt: sourceNode.attempt ? { ...sourceNode.attempt, driver: localizedDriver(sourceNode.attempt.driver) } : sourceNode.attempt }
  const result = node.result
  return <div className="dsh-fishyume-node-detail" aria-label="节点详情"><h4>{node.title || node.nodeId}</h4><dl><dt>类型</dt><dd>{node.type || '任务'}</dd><dt>状态</dt><dd>{localizedPhase(node.phase)}{node.conclusion ? ` · ${localizedConclusion(node.conclusion)}` : ''}</dd><dt>节点 ID</dt><dd>{node.nodeId}</dd><dt>依赖</dt><dd>{node.dependsOn?.length ? node.dependsOn.join('、') : '无'}</dd>{node.attempt?.driver ? <><dt>执行器</dt><dd>{node.attempt.driver} / {node.attempt.target || '默认目标'}</dd></> : null}{node.currentAttempt !== undefined ? <><dt>当前尝试</dt><dd>{node.currentAttempt}</dd></> : null}{node.attempt?.startedAt ? <><dt>开始时间</dt><dd>{nativeTime(node.attempt.startedAt)}</dd></> : null}{node.attempt?.completedAt ? <><dt>完成时间</dt><dd>{nativeTime(node.attempt.completedAt)}</dd></> : null}</dl>{node.reason || node.diagnostic || node.message ? <p>{node.reason || node.diagnostic || node.message}</p> : null}{result?.summary ? <><h4>结果摘要</h4><p>{result.summary}</p></> : null}{result?.decision ? <p><strong>决策：</strong>{result.decision}</p> : null}{result?.artifacts?.length ? <><h4>产物</h4><ul>{result.artifacts.map((item) => <li key={item}>{item}</li>)}</ul></> : null}{result?.warnings?.length ? <><h4>警告</h4><ul>{result.warnings.map((item) => <li key={item}>{item}</li>)}</ul></> : null}</div>
}

function EventInspector({ event }: { event: RunEvent }): JSX.Element {
  event = { ...event, type: localizedEventType(event.type) }
  return <div className="dsh-fishyume-event-detail" aria-label="事件详情"><h4>事件 #{event.sequence}</h4><dl><dt>类型</dt><dd>{event.type}</dd><dt>时间</dt><dd>{nativeTime(event.timestamp || event.createdAt)}</dd><dt>阶段</dt><dd>{localizedPhase(event.phase || event.nodePhase)}</dd>{event.nodeId ? <><dt>关联节点</dt><dd>{event.nodeId}</dd></> : null}{event.reason ? <><dt>原因</dt><dd>{event.reason}</dd></> : null}</dl><p>{event.message || event.summary || '该事件没有附加说明。'}</p></div>
}

function EnhancedWorkflowDetail({ state, run }: { state: PanelState; run: RunDetail }): JSX.Element {
  const nodes = run.nodes ?? []
  const selectedNode = nodes.find((node) => node.nodeId === state.selectedNode)
  const selectedEvent = state.runEvents.find((event) => event.sequence === state.selectedEvent)
  const events = [...state.runEvents].sort((a, b) => a.sequence - b.sequence).slice(-20)
  return <section className="dsh-fishyume-detail" aria-label="工作流详情"><div className="dsh-fishyume-workflow-summary"><span className="dsh-fishyume-workflow-chip"><strong>状态</strong> {localizedPhase(run.phase)}{run.conclusion ? ` · ${localizedConclusion(run.conclusion)}` : ''}</span>{run.driver ? <span className="dsh-fishyume-workflow-chip"><strong>执行器</strong> {run.driver}</span> : null}{run.project ? <span className="dsh-fishyume-workflow-chip"><strong>项目</strong> {run.project}</span> : null}{run.stateVersion !== undefined ? <span className="dsh-fishyume-workflow-chip"><strong>版本</strong> {run.stateVersion}</span> : null}</div><p>{run.summary || '暂无工作流摘要。'}</p><LocalizedRunActions run={run} />{nodes.length ? <><h4>执行流程</h4><div className="dsh-fishyume-workflow" aria-label="工作流节点流程">{nodes.map((node, index) => <div className="dsh-fishyume-workflow-step" key={node.nodeId}>{index ? <span className="dsh-fishyume-workflow-arrow" aria-hidden="true">→</span> : null}<article className="dsh-fishyume-node" tabIndex={0} role="button" aria-pressed={state.selectedNode === node.nodeId} data-phase={node.phase ?? 'pending'} data-conclusion={node.conclusion} data-selected={state.selectedNode === node.nodeId ? '' : undefined} onClick={(event) => { if (!(event.target as HTMLElement).closest('button')) updatePanel({ selectedNode: node.nodeId }) }} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); updatePanel({ selectedNode: node.nodeId }) } }}><span className="dsh-fishyume-node-meta">节点 {index + 1} · {node.type || '任务'}</span><strong className="dsh-fishyume-node-title" title={node.title || node.nodeId}>{node.title || node.nodeId}</strong><span className="dsh-fishyume-node-meta">{localizedPhase(node.phase)}{node.conclusion ? ` · ${localizedConclusion(node.conclusion)}` : ''}</span>{node.currentAttempt !== undefined ? <span className="dsh-fishyume-node-meta">尝试 {node.currentAttempt}</span> : null}<LocalizedRunActions run={run} node={node} /></article></div>)}</div>{selectedNode ? <NodeInspector node={selectedNode} /> : null}</> : <div className="dsh-fishyume-state">暂无节点数据。</div>}{events.length ? <div className="dsh-fishyume-events"><h4>事件时间线</h4>{events.map((event) => <button className="dsh-fishyume-event" type="button" key={`${event.sequence}-${event.type}`} data-selected={state.selectedEvent === event.sequence ? '' : undefined} onClick={() => updatePanel({ selectedEvent: event.sequence, ...(event.nodeId ? { selectedNode: event.nodeId } : {}) })}><span className="dsh-fishyume-event-sequence">#{event.sequence}</span><span><strong>{event.type}</strong><br /><small>{nativeTime(event.timestamp || event.createdAt)} · {event.message || event.summary || localizedPhase(event.phase || event.nodePhase)}</small></span></button>)}</div> : null}{selectedEvent ? <EventInspector event={selectedEvent} /> : null}</section>
}

type BrandIcon = ComponentType<{ size?: string | number; style?: Record<string, string> }>

function harnessName(driver?: string): string {
  return ({ claude: 'Claude Code', opencode: 'OpenCode', codex: 'Codex', deepseek: 'DeepSeek' } as Record<string, string>)[driver || ''] || driver || '未知 Harness'
}

function harnessIcon(driver?: string): BrandIcon | undefined {
  const key = (driver || '').toLowerCase()
  if (key === 'codex') return OpenAIIcon
  if (key === 'opencode') return OpenCodeIcon
  if (key === 'claude') return ClaudeIcon
  return undefined
}

function modelIcon(model?: string): BrandIcon | undefined {
  const key = (model || '').toLowerCase()
  if (key.includes('deepseek')) return DeepSeekIcon
  if (key.includes('gemini')) return GeminiIcon
  return undefined
}

function modelName(model?: string): string {
  return model?.split('/').filter(Boolean).slice(-1)[0] || '未指定模型'
}

function MemberCardGrid({ team }: { team?: TeamDetail }): JSX.Element {
  const members = Array.isArray(team?.participants) ? team.participants : []
  return <><h4>团队成员</h4>{members.length ? <div className="dsh-fishyume-members" aria-label="团队成员列表">{members.map((member) => {
    const HarnessIcon = harnessIcon(member.driver)
    const ProviderIcon = modelIcon(member.modelId)
    const selected = panel.selectedMember === member.participantId
    const selectMember = (): void => updatePanel({ selectedMember: member.participantId })
    return <article className="dsh-fishyume-member" key={member.participantId} tabIndex={0} role="button" aria-pressed={selected} data-selected={selected ? '' : undefined} onClick={selectMember} onKeyDown={(event) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault()
        selectMember()
      }
    }}>
      <strong>{member.label || member.participantId}</strong>
      <p className="dsh-fishyume-member-duty">{member.role || '协作者'}{member.state ? ` · ${localizedPhase(member.state)}` : ''}</p>
      <div className="dsh-fishyume-member-divider" />
      <div className="dsh-fishyume-member-row"><span>Harness</span><span className="dsh-fishyume-harness" data-driver={(member.driver || '').toLowerCase()}>{HarnessIcon ? <HarnessIcon size={16} aria-hidden="true" /> : null}{harnessName(member.driver)}</span></div>
      <div className="dsh-fishyume-member-row"><span>Model</span><span className="dsh-fishyume-model">{ProviderIcon ? <ProviderIcon size={16} aria-hidden="true" /> : null}{modelName(member.modelId)}</span></div>
    </article>
  })}</div> : <p>暂无成员详情。请刷新团队数据。</p>}</>
}

function activityKind(kind?: string, content?: string): { label: string; kind: 'tool' | 'command' | 'output' | 'status' } {
  const value = `${kind || ''} ${content || ''}`.toLowerCase()
  if (/(tool|function)[_. -]?(call|use|invok)/.test(value)) return { label: '调用工具', kind: 'tool' }
  if (/(command|shell|exec|terminal)/.test(value)) return { label: '执行命令', kind: 'command' }
  if (/(output|result|contribution|message)/.test(value)) return { label: '输出流', kind: 'output' }
  return { label: '状态更新', kind: 'status' }
}

type ContributionItem = { message: TeamMessage; member?: TeamParticipant; turn?: TeamTurn }

type ContributionEnvelope = { resultType?: string; output?: unknown; contentMarkdown?: string }

function contributionEnvelope(message: TeamMessage): ContributionEnvelope | undefined {
  try {
    const value = JSON.parse(message.content || '') as unknown
    if (!value || typeof value !== 'object') return undefined
    return value as ContributionEnvelope
  } catch {
    return undefined
  }
}

function contributionFileLabels(message: TeamMessage): string[] {
  const value = contributionEnvelope(message)
  const labels: string[] = []
  const collect = (input: unknown, key = ''): void => {
    if (typeof input === 'string' && /^(path|file|filename|artifact)s?$/i.test(key)) {
      const normalized = input.replaceAll('\\', '/')
      labels.push(normalized.slice(normalized.lastIndexOf('/') + 1) || input)
      return
    }
    if (Array.isArray(input)) { input.forEach((item) => collect(item, key)); return }
    if (input && typeof input === 'object') Object.entries(input).forEach(([childKey, childValue]) => collect(childValue, childKey))
  }
  collect(value?.output)
  const unique = [...new Set(labels.filter(Boolean))]
  if (unique.length) return unique.slice(0, 4)
  const fallback = ({ report: 'report.md', decision: 'decision.json', artifact: 'artifact', data: 'data.json', question: 'question.md' } as Record<string, string>)[value?.resultType || '']
  return fallback ? [fallback] : value?.contentMarkdown ? ['Markdown 产出'] : ['产出内容']
}

function contributionItems(team?: TeamDetail): ContributionItem[] {
  const members = Array.isArray(team?.participants) ? team.participants : []
  return panel.teamMessages
    .filter((message) => message.kind === 'participant_contribution' && Boolean(message.content?.trim()))
    .map((message) => {
      const member = members.find((item) => item.participantId === message.actor || item.label === message.actor)
      const turn = team?.turns?.find((item) => item.turnId === message.turnId || item.contributionMessageId === message.messageId)
      return { message, member, turn }
    })
    .sort((a, b) => a.message.sequence - b.message.sequence)
}

function liveActivityLabel(type?: string): string {
  const value = (type || '').toLowerCase()
  if (value.includes('tool') || value.includes('function')) return '调用工具'
  if (value.includes('command') || value.includes('shell') || value.includes('exec')) return '执行命令'
  if (value.includes('output') || value.includes('response')) return value.includes('start') ? '开始响应' : '输出流'
  if (value === 'session.connected' || value === 'connected') return '会话已连接'
  if (value.includes('disconnected') || value.includes('closed')) return '会话已断开'
  if (value.includes('presence.busy') || value === 'busy') return '正在工作'
  if (value.includes('presence.idle') || value === 'idle') return '已空闲'
  if (value.includes('message.received')) return '收到消息'
  if (value.includes('message.updated')) return '产出更新'
  if (value.includes('heartbeat')) return '心跳'
  return '状态更新'
}

function liveActivityTone(type?: string, latest = false): 'cyan' | 'green' | 'gray' {
  if (latest) return 'cyan'
  const value = (type || '').toLowerCase()
  if (value.includes('heartbeat') || value.includes('connected') || value.includes('disconnected')) return 'gray'
  return 'green'
}

function memberLiveActivities(team: TeamDetail | undefined, member: TeamParticipant): RunEvent[] {
  const turns = team?.turns ?? []
  return panel.teamEvents.filter((event) => {
    const turn = event.turnId ? turns.find((item) => item.turnId === event.turnId) : undefined
    const message = event.messageId ? panel.teamMessages.find((item) => item.messageId === event.messageId) : undefined
    if (turn) return turn.participantId === member.participantId
    if (message) return message.actor === member.participantId || message.actor === member.label
    return false
  }).sort((a, b) => b.sequence - a.sequence)
}

function memberStatusState(team: TeamDetail | undefined, member: TeamParticipant, turn?: TeamTurn): string | undefined {
  if (member.state) return member.state
  if (turn?.state) return turn.state
  const latestTurn = (team?.turns ?? [])
    .filter((item) => item.participantId === member.participantId)
    .sort((a, b) => Date.parse(b.updatedAt || b.createdAt || '') - Date.parse(a.updatedAt || a.createdAt || ''))[0]
  return latestTurn?.state
}

function isLiveParticipantState(state?: string): boolean {
  return state === 'pending' || state === 'running' || state === 'prepared' || state === 'dispatching' || state === 'active' || state === 'cancelling'
}

function participantStatusLabel(state?: string): string {
  if (isLiveParticipantState(state)) return '实时'
  if (state === 'responded' || state === 'completed') return '已完成'
  if (state === 'failed') return '失败'
  if (state === 'indeterminate') return '状态不确定'
  if (state === 'cancelled') return '已取消'
  return '暂无状态'
}

function MemberLiveStatus({ team, member }: { team?: TeamDetail; member: TeamParticipant }): JSX.Element {
  const turn = team?.turns?.find((item) => item.turnId === member.currentTurnId)
  const status = memberStatusState(team, member, turn)
  const live = isLiveParticipantState(status)
  const items = memberLiveActivities(team, member)
  const handleWheel = (event: WheelEvent<HTMLDivElement>): void => {
    const pane = event.currentTarget
    const canScroll = pane.scrollHeight > pane.clientHeight
    const atTop = pane.scrollTop <= 0 && event.deltaY < 0
    const atBottom = pane.scrollTop + pane.clientHeight >= pane.scrollHeight - 1 && event.deltaY > 0
    event.stopPropagation()
    if (!canScroll || atTop || atBottom) event.preventDefault()
  }
  return <section className="dsh-fishyume-live-status" aria-label="实时状态">
    <div className="dsh-fishyume-live-status-heading"><h4>Harness 活动</h4><span className="dsh-fishyume-live-indicator" data-live={live ? '' : undefined}>{participantStatusLabel(status)}</span></div>
    <div className="dsh-fishyume-live-scroll" role="log" aria-live="polite" onWheelCapture={handleWheel}>
      {items.length ? <div className="dsh-fishyume-live-timeline">{items.map((event, index) => <div className="dsh-fishyume-live-item" data-tone={liveActivityTone(event.type, index === 0)} key={String(event.sequence) + '-' + event.type}><span className="dsh-fishyume-live-label">{liveActivityLabel(event.type)}</span><time className="dsh-fishyume-live-time">{nativeTime(event.timestamp || event.createdAt)}</time>{event.summary || event.message ? <p className="dsh-fishyume-live-detail">{event.summary || event.message}</p> : null}</div>)}</div> : <p className="dsh-fishyume-state">暂无 Harness 活动。</p>}
    </div>
  </section>
}

function AgentStatusStream({ team }: { team?: TeamDetail }): JSX.Element {
  const items = contributionItems(team).slice(-8).reverse()
  return <section className="dsh-fishyume-agent-status" aria-label="成员产出"><h4>成员产出</h4>{items.length ? <div className="dsh-fishyume-agent-stream">{items.map(({ message, member, turn }) => <details className="dsh-fishyume-agent-output" key={message.messageId}><summary><span><strong>{member?.label || message.actor || '智能体'}</strong><small>{turn?.number !== undefined ? `第 ${turn.number} 轮 · ` : ''}{nativeTime(message.createdAt)}</small></span><span className="dsh-fishyume-output-files">{contributionFileLabels(message).map((label) => <span className="dsh-fishyume-output-file" key={label}>▧ {label}</span>)}</span><span className="dsh-fishyume-output-chevron" aria-hidden="true">⌄</span></summary><div className="dsh-fishyume-agent-output-body"><div className="dsh-fishyume-agent-output-inner"><p title={message.content}>{message.content}</p></div></div></details>)}</div> : <p>暂无成员产出。</p>}</section>
}

function MemberDrawer({ team, member }: { team?: TeamDetail; member?: TeamParticipant }): JSX.Element | null {
  if (!member) return null
  const turn = team?.turns?.find((item) => item.turnId === member.currentTurnId)
  const status = memberStatusState(team, member, turn)
  const close = (): void => updatePanel({ selectedMember: undefined })
  const relatedItems = contributionItems(team).filter((item) => item.member?.participantId === member.participantId || item.message.actor === member.label).slice(-5).reverse()
  return <>
    <button type="button" className="dsh-fishyume-member-drawer-backdrop" aria-label="关闭成员详情" onClick={close} />
    <aside className="dsh-fishyume-member-drawer" aria-label="成员详情">
      <header><div><span className="dsh-fishyume-eyebrow">成员详情</span><h3>{member.label || member.participantId}</h3><p>{member.role || '协作者'}</p></div><button type="button" aria-label="关闭成员详情" title="关闭" onClick={close}>×</button></header>
      <h4>当前状态</h4>
      <dl><dt>状态</dt><dd>{participantStatusLabel(status)}{status ? `（${localizedPhase(status)}）` : ''}</dd><dt>当前任务</dt><dd>{turn?.number !== undefined ? `第 ${turn.number} 轮` : member.currentTurnId || '暂无任务'}</dd><dt>目标</dt><dd>{member.target || turn?.target || '默认目标'}</dd><dt>执行阶段</dt><dd>{turn?.state ? localizedPhase(turn.state) : '暂无阶段信息'}</dd></dl>
      <h4>执行配置</h4>
      <dl><dt>Harness</dt><dd>{harnessName(member.driver)}</dd><dt>Model</dt><dd>{modelName(member.modelId)}</dd><dt>回合 ID</dt><dd>{member.currentTurnId || '暂无'}</dd></dl>
      <MemberLiveStatus team={team} member={member} />
      <h4>最近产出</h4>
      {relatedItems.length ? <div className="dsh-fishyume-member-activity">{relatedItems.map(({ message, turn }) => <div key={message.messageId}><small>#{message.sequence} · {turn?.number !== undefined ? `第 ${turn.number} 轮 · ` : ''}{nativeTime(message.createdAt)}</small><div className="dsh-fishyume-output-files">{contributionFileLabels(message).map((label) => <span className="dsh-fishyume-output-file" key={label}>▧ {label}</span>)}</div></div>)}</div> : <p>暂无相关产出。</p>}
    </aside>
  </>
}

function EnhancedTeamMembers({ team }: { team?: TeamDetail }): JSX.Element {
  const members = Array.isArray(team?.participants) ? team.participants : []
  const selectedMember = members.find((member) => member.participantId === panel.selectedMember)
  return <><MemberCardGrid team={team} /><AgentStatusStream team={team} /><MemberDrawer team={team} member={selectedMember} /></>
}

function EnhancedTeamSettings({ team }: { team?: TeamDetail }): JSX.Element {
  return <section className="dsh-fishyume-detail" aria-label="团队设置"><span className="dsh-fishyume-eyebrow">团队设置</span><h3>{team?.topic || team?.teamId || '未选择团队'}</h3>{team ? <><div className="dsh-fishyume-metrics"><NativeMetric label="状态" value={localizedPhase(team.state || team.status)} /><NativeMetric label="成员" value={Array.isArray(team.participants) ? team.participants.length : team.participants ?? 0} /><NativeMetric label="版本" value={team.stateVersion ?? 0} /></div><EnhancedTeamMembers team={team} /><div className="dsh-fishyume-resource"><div className="dsh-fishyume-resource-row"><strong>项目目录</strong><span>{team.project || '未设置'}</span></div><div className="dsh-fishyume-resource-row"><strong>目录哈希</strong><span>{team.catalogHash || '未提供'}</span></div><div className="dsh-fishyume-resource-row"><strong>预算</strong><span>{team.costUsed ?? 0} / {team.costGrant ?? 0}</span></div><div className="dsh-fishyume-resource-row"><strong>创建时间</strong><span>{nativeTime(team.createdAt)}</span></div><div className="dsh-fishyume-resource-row"><strong>更新时间</strong><span>{nativeTime(team.updatedAt)}</span></div></div></> : <div className="dsh-fishyume-state">选择一个团队后查看成员和设置。</div>}</section>
}

function EnhancedWorkspace({ state }: { state: PanelState }): JSX.Element {
  const selected = state.teams.find((team) => team.teamId === state.selectedTeam)
  const team = state.teamDetail || selected
  const title = state.view === 'teams' ? '团队工作区' : state.view === 'runs' ? '工作流执行' : state.view === 'routing' ? '路由配置' : '团队设置'
 return <div className="dsh-fishyume-workspace"><header className="dsh-fishyume-header"><div className="dsh-fishyume-brand"><button type="button" className="dsh-fishyume-back" data-dsh-center-view-back="" aria-label="返回会话" onClick={() => updatePanel({ open: false })}><span aria-hidden="true">←</span><span>返回会话</span></button><img className="dsh-fishyume-logo" src={fishyumeLogo} alt="" aria-hidden="true" /><div><span className="dsh-fishyume-eyebrow">FISHYUME</span><h2>{title}</h2></div></div><div><button type="button" onClick={() => { if (state.view === 'teams' || state.view === 'settings') void loadTeams(); else if (state.view === 'runs') void loadRuns(); else void loadRouting() }} disabled={state.loading} aria-label="刷新" title="刷新">↻</button></div></header><nav className="dsh-fishyume-tabs" aria-label="Fishyume 视图">{(['teams', 'runs', 'routing', 'settings'] as const).map((item) => <button key={item} type="button" aria-current={state.view === item ? 'page' : undefined} onClick={() => { updatePanel({ view: item }); if (item === 'teams' || item === 'settings') void loadTeams(); if (item === 'runs') void loadRuns(); if (item === 'routing') void loadRouting() }}>{item === 'teams' ? '团队' : item === 'runs' ? '工作流' : item === 'routing' ? '路由' : '设置'}</button>)}</nav>{state.error ? <div className="dsh-fishyume-state" role="alert">{state.error}<button type="button" onClick={() => void loadTeams()}>重试</button></div> : null}{state.loading ? <div className="dsh-fishyume-state">正在加载...</div> : null}{!state.loading && !state.error && state.view === 'teams' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="团队列表">{state.teams.length ? state.teams.map((item) => <button key={item.teamId} type="button" className={item.teamId === state.selectedTeam ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedTeam: item.teamId, selectedHandoff: undefined, selectedMember: undefined, teamDetail: undefined }); void loadTeamDetail(item.teamId) }}><strong>{item.topic || item.teamId}</strong><small>{localizedPhase(item.state || item.status)}</small><small>{Array.isArray(item.participants) ? item.participants.length : item.participants ?? 0} 位成员 · {nativeTime(item.updatedAt || item.createdAt)}</small></button>) : <div className="dsh-fishyume-state">暂无团队。</div>}</div><section className="dsh-fishyume-detail" aria-label="团队详情">{team ? <><span className="dsh-fishyume-eyebrow">团队</span><h3>{team.topic || team.teamId}</h3><p>{team.project || '项目路径不可用'}</p><small>{team.teamId}</small><div className="dsh-fishyume-metrics"><NativeMetric label="状态" value={localizedPhase(team.state || team.status)} /><NativeMetric label="成员" value={Array.isArray(team.participants) ? team.participants.length : team.participants ?? 0} /><NativeMetric label="额度" value={`${team.costUsed ?? 0} / ${team.costGrant ?? 0}`} /></div><EnhancedTeamMembers team={team} /><h4>交接</h4>{state.handoffs.length ? state.handoffs.map((handoff) => <button key={handoff.handoffId} type="button" className={handoff.handoffId === state.selectedHandoff ? 'is-selected' : ''} onClick={() => updatePanel({ selectedHandoff: handoff.handoffId })}><strong>{handoff.goal}</strong><small>{handoff.handoffId}</small></button>) : <p>暂无交接。</p>}{state.teamMessages.length ? <><h4>最近消息</h4>{state.teamMessages.slice(-6).reverse().map((message) => <div className="dsh-fishyume-message" key={message.messageId}><small>#{message.sequence} · {message.actor || message.kind || '消息'} · {nativeTime(message.createdAt)}</small><div>{message.content || '空消息'}</div></div>)}</> : null}</> : <div className="dsh-fishyume-state">选择一个团队。</div>}</section></div> : null}{!state.loading && !state.error && state.view === 'runs' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="工作流列表">{state.runs.length ? state.runs.map((run) => <button key={run.runId} type="button" className={run.runId === state.selectedRun ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedRun: run.runId, selectedEvent: undefined }); void loadRunDetail(run.runId) }}><strong>{run.workflowName || run.runId}</strong><small>{localizedPhase(run.phase)}{run.conclusion ? ` · ${localizedConclusion(run.conclusion)}` : ''}</small></button>) : <div className="dsh-fishyume-state">暂无工作流。</div>}</div>{state.selectedRun ? (state.runDetail ? <EnhancedWorkflowDetail state={state} run={state.runDetail} /> : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">正在加载工作流详情...</div></section>) : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">选择一个工作流。</div></section>}</div> : null}{!state.loading && !state.error && state.view === 'routing' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="执行器列表">{state.drivers.length ? state.drivers.map((driver) => <div key={driver.driver} className="dsh-fishyume-driver"><strong>{driver.driver}</strong><small>{driver.available ? '可用' : driver.diagnostic || '不可用'}{driver.version ? ` · ${driver.version}` : ''}{driver.authenticated === true ? ' · 已认证' : driver.authenticated === false ? ' · 未认证' : ''}</small></div>) : <div className="dsh-fishyume-state">暂无执行器。</div>}</div><section className="dsh-fishyume-detail" aria-label="生效路由"><span className="dsh-fishyume-eyebrow">路由</span><h3>生效路由</h3>{state.routes.length ? state.routes.map((route) => <p key={route.routeId}><strong>{route.routeId}</strong><br /><small>{route.driver || '未知'} / {route.provider || '默认'} / {route.model || '默认'} · {route.enabled ? '已启用' : '已停用'}</small></p>) : <p>暂无团队路由配置。</p>}</section></div> : null}{!state.loading && !state.error && state.view === 'settings' ? <EnhancedTeamSettings team={team} /> : null}</div>
}

function TeamSubnav({ state }: { state: PanelState }): JSX.Element {
  const [open, setOpen] = useState(false)
  const choose = (section: TeamSection): void => {
    setOpen(false)
    updatePanel({ view: 'teams', teamSection: section, templateMode: section === 'templates' ? 'list' : 'list', selectedTemplate: undefined, error: undefined })
    if (section === 'templates') void loadTeamTemplates()
    else void loadTeams()
  }
  return <nav className="dsh-fishyume-team-subnav" aria-label="团队子导航">
    <div className="dsh-fishyume-team-switcher">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)}><span>我的团队</span><span aria-hidden="true">⌄</span></button>
      {open ? <div className="dsh-fishyume-team-menu" role="menu">
        <button type="button" role="menuitem" data-selected={state.teamSection === 'tasks' ? '' : undefined} onClick={() => choose('tasks')}><span>团队任务</span><span>{state.teamSection === 'tasks' ? '✓' : ''}</span></button>
        <button type="button" role="menuitem" data-selected={state.teamSection === 'templates' ? '' : undefined} onClick={() => choose('templates')}><span>团队模板</span><span>{state.teamSection === 'templates' ? '✓' : ''}</span></button>
      </div> : null}
    </div>
  </nav>
}

function TeamTasksView({ state }: { state: PanelState }): JSX.Element {
  const selected = state.teams.find((team) => team.teamId === state.selectedTeam)
  const team = state.teamDetail || selected
  return <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="团队任务列表">{state.teams.length ? state.teams.map((item) => <button key={item.teamId} type="button" className={item.teamId === state.selectedTeam ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedTeam: item.teamId, selectedHandoff: undefined, selectedMember: undefined, teamDetail: undefined }); void loadTeamDetail(item.teamId) }}><strong>{item.topic || item.teamId}</strong><small>{localizedPhase(item.state || item.status)}</small><small>{Array.isArray(item.participants) ? item.participants.length : item.participants ?? 0} 位成员 · {nativeTime(item.updatedAt || item.createdAt)}</small></button>) : <div className="dsh-fishyume-state">暂无团队任务。</div>}</div><section className="dsh-fishyume-detail" aria-label="团队任务详情">{team ? <><span className="dsh-fishyume-eyebrow">团队任务</span><h3>{team.topic || team.teamId}</h3><p>{team.project || '项目路径不可用'}</p><small>{team.teamId}</small><div className="dsh-fishyume-metrics"><NativeMetric label="状态" value={localizedPhase(team.state || team.status)} /><NativeMetric label="成员" value={Array.isArray(team.participants) ? team.participants.length : team.participants ?? 0} /><NativeMetric label="额度" value={`${team.costUsed ?? 0} / ${team.costGrant ?? 0}`} /></div><EnhancedTeamMembers team={team} />{state.teamMessages.length ? <><h4>最近消息</h4>{state.teamMessages.slice(-6).reverse().map((message) => <div className="dsh-fishyume-message" key={message.messageId}><small>#{message.sequence} · {message.actor || message.kind || '消息'} · {nativeTime(message.createdAt)}</small><div>{message.content || '空消息'}</div></div>)}</> : null}</> : <div className="dsh-fishyume-state">选择一个团队任务。</div>}</section></div>
}

function TeamTemplateListView({ state }: { state: PanelState }): JSX.Element {
  return <section className="dsh-fishyume-template-main" aria-label="团队模板列表">
    <header className="dsh-fishyume-template-header"><div><span className="dsh-fishyume-eyebrow">团队模板</span><h3>可复用的团队配置</h3><p>保存成员与运行环境，之后由 Host Agent 按模板标识启动。</p></div><div className="dsh-fishyume-template-actions"><button type="button" data-primary onClick={() => updatePanel({ templateMode: 'create', selectedTemplate: undefined, error: undefined })}>＋ 创建团队模板</button></div></header>
    {state.templates.length ? <div className="dsh-fishyume-template-list">{state.templates.map((template) => <button key={template.templateId} type="button" className="dsh-fishyume-template-list-row" onClick={() => updatePanel({ templateMode: 'create', selectedTemplate: template.templateId, error: undefined })}><span><strong>{template.name}</strong><br /><small>{template.description || '暂无模板说明'}</small></span><small>{template.templateId}</small><small>{template.members.length} 位成员</small></button>)}</div> : <div className="dsh-fishyume-template-empty"><div><p>还没有团队模板。</p><button type="button" data-primary onClick={() => updatePanel({ templateMode: 'create', selectedTemplate: undefined, error: undefined })}>＋ 创建第一个模板</button></div></div>}
  </section>
}

function templateDraftFromValue(value: TeamTemplate | undefined, capabilities?: TeamCapabilities): TeamTemplateDraft {
  if (!value) return defaultTemplateDraft(capabilities)
  return { templateId: value.templateId, name: value.name, description: value.description || '', color: value.color || 'cyan', members: value.members.map((member) => ({ ...member })) }
}

function TemplateHarnessBadge({ driver }: { driver?: string }): JSX.Element {
  const HarnessIcon = harnessIcon(driver)
  return <span className="dsh-fishyume-harness" data-driver={(driver || '').toLowerCase()}>{HarnessIcon ? <HarnessIcon size={16} aria-hidden="true" /> : null}<span>{driver ? harnessName(driver) : '未指定 Harness'}</span></span>
}

function TeamTemplateEditor({ template, capabilities }: { template?: TeamTemplate; capabilities?: TeamCapabilities }): JSX.Element {
  const [draft, setDraft] = useState<TeamTemplateDraft>(() => templateDraftFromValue(template, capabilities))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const update = (patch: Partial<TeamTemplateDraft>): void => setDraft((current) => ({ ...current, ...patch }))
  const updateMember = (index: number, patch: Partial<TeamTemplateMember>): void => setDraft((current) => ({ ...current, members: current.members.map((member, memberIndex) => memberIndex === index ? { ...member, ...patch } : member) }))
  const changeDriver = (index: number, driver: string): void => {
    const models = templateModels(capabilities, driver)
    updateMember(index, { driver, modelId: driver && models.some((model) => model.modelId === draft.members[index]?.modelId) ? draft.members[index].modelId : '' })
  }
  const addMember = (): void => {
    if (draft.members.length >= 4) return
    setDraft((current) => ({ ...current, members: [...current.members, { label: '', roleHint: '', driver: '', modelId: '' }] }))
  }
  const removeMember = (index: number): void => { if (draft.members.length > 2) setDraft((current) => ({ ...current, members: current.members.filter((_, memberIndex) => memberIndex !== index) })) }
  const submit = async (): Promise<void> => {
    setSaving(true); setError(undefined)
    try { await saveTeamTemplate(draft) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setSaving(false) }
  }
  return <div className="dsh-fishyume-template-page">
    <main className="dsh-fishyume-template-main">
      <header className="dsh-fishyume-template-header"><div><span className="dsh-fishyume-eyebrow">团队 / 团队模板 / {template ? '编辑' : '创建'}</span><h3>{template ? '编辑团队模板' : '创建团队模板'}</h3><p>配置成员与运行环境，保存后可由 Host Agent 按模板标识调用。</p></div><div className="dsh-fishyume-template-actions"><button type="button" onClick={() => updatePanel({ templateMode: 'list', selectedTemplate: undefined, error: undefined })}>取消</button><button type="button" data-primary disabled={saving} onClick={() => void submit()}>{saving ? '保存中…' : '保存团队模板'}</button></div></header>
      {error ? <div className="dsh-fishyume-state" role="alert">{error}</div> : null}
      <section className="dsh-fishyume-template-section"><h4>基本信息</h4><div className="dsh-fishyume-template-fields"><label className="dsh-fishyume-template-field" data-aligned><span>团队模板名称</span><input value={draft.name} placeholder="例如：秋招研究团队" onChange={(event) => update({ name: event.target.value })} /></label><label className="dsh-fishyume-template-field" data-aligned><span>模板标识</span><input value={draft.templateId} placeholder="例如：campus-research" disabled={Boolean(template)} onChange={(event) => update({ templateId: event.target.value })} /><small>供 Host Agent 在调用时引用。</small></label><label className="dsh-fishyume-template-field" data-wide><span>模板说明</span><textarea value={draft.description} placeholder="说明这个模板适合解决哪类任务。" onChange={(event) => update({ description: event.target.value })} /></label><div className="dsh-fishyume-template-field"><span>图标颜色</span><div className="dsh-fishyume-template-color-row">{(['cyan', 'violet', 'blue', 'green', 'orange', 'red', 'gray'] as const).map((color) => <button key={color} type="button" className="dsh-fishyume-template-color" data-color={color} data-selected={draft.color === color ? '' : undefined} aria-label={color} onClick={() => update({ color })} />)}</div></div></div></section>
      <section className="dsh-fishyume-template-section"><div className="dsh-fishyume-template-toolbar"><h4>团队成员</h4><button type="button" onClick={addMember} disabled={draft.members.length >= 4}>＋ 添加成员</button></div><div className="dsh-fishyume-template-members">{draft.members.map((member, index) => { const models = templateModels(capabilities, member.driver || ''); return <article className="dsh-fishyume-template-member" key={`${index}-${member.label}`}><div className="dsh-fishyume-template-member-top"><span className="dsh-fishyume-template-avatar" aria-hidden="true">{(member.label || String.fromCharCode(65 + index)).slice(0, 1).toUpperCase()}</span><input aria-label={`成员 ${index + 1} 名称`} value={member.label} placeholder="成员名称" onChange={(event) => updateMember(index, { label: event.target.value })} /><input aria-label={`成员 ${index + 1} 角色提示`} value={member.roleHint || ''} placeholder="角色提示（可选）" onChange={(event) => updateMember(index, { roleHint: event.target.value })} /><div className="dsh-fishyume-template-member-tools"><button type="button" title="更多设置" aria-label="更多设置">…</button><button type="button" title="删除成员" aria-label="删除成员" disabled={draft.members.length <= 2} onClick={() => removeMember(index)}>⌫</button></div></div><div className="dsh-fishyume-template-member-grid"><label className="dsh-fishyume-template-member-select"><span>Harness（可选）</span><div className="dsh-fishyume-template-harness-control"><TemplateHarnessBadge driver={member.driver} /><select aria-label={`成员 ${index + 1} Harness`} value={member.driver || ''} onChange={(event) => changeDriver(index, event.target.value)}><option value="">不指定 Harness</option>{(capabilities?.harnesses ?? []).map((harness) => <option key={harness.driver} value={harness.driver}>{harness.driver}</option>)}{!(capabilities?.harnesses?.length) ? <><option value="opencode">OpenCode</option><option value="codex">Codex</option><option value="claude">Claude Code</option></> : null}</select></div></label><label className="dsh-fishyume-template-member-select"><span>Model（随 Harness 可选）</span><select aria-label={`成员 ${index + 1} Model`} value={member.modelId || ''} disabled={!member.driver} onChange={(event) => updateMember(index, { modelId: event.target.value })}><option value="">{member.driver ? '选择 Model' : '先选择 Harness'}</option>{member.modelId && !models.some((model) => model.modelId === member.modelId) ? <option value={member.modelId}>{modelName(member.modelId)}</option> : null}{models.map((model) => <option key={model.modelId} value={model.modelId}>{model.label}</option>)}</select></label></div><div className="dsh-fishyume-template-permission-note">权限由所选 Harness 的权限分级决定，模板不固定跨 Harness 权限。</div><details><summary>高级设置</summary></details></article> })}</div><button type="button" className="dsh-fishyume-template-add" onClick={addMember} disabled={draft.members.length >= 4}>＋ 添加成员</button></section>
    </main>
    <aside className="dsh-fishyume-template-summary"><h3>模板摘要</h3><dl className="dsh-fishyume-template-summary-list"><div className="dsh-fishyume-template-summary-row"><dt>模板名称</dt><dd>{draft.name || '未命名模板'}</dd></div><div className="dsh-fishyume-template-summary-row"><dt>成员</dt><dd>{draft.members.length}</dd></div><div className="dsh-fishyume-template-summary-row"><dt>默认状态</dt><dd>未启动</dd></div><div className="dsh-fishyume-template-summary-row"><dt>模板标识</dt><dd>{draft.templateId || '尚未填写'}</dd></div></dl><h4>成员</h4><div className="dsh-fishyume-template-summary-members">{draft.members.map((member, index) => <div className="dsh-fishyume-template-summary-member" key={`${index}-${member.label}`}><span className="dsh-fishyume-template-summary-avatar">{(member.label || String.fromCharCode(65 + index)).slice(0, 1).toUpperCase()}</span><span>{member.label || '未命名成员'}</span><small>{harnessName(member.driver)} · {modelName(member.modelId)}</small></div>)}</div><h4>Host Agent 声明</h4><p className="dsh-fishyume-template-summary-call">在 Host Agent 中声明使用团队模板，并提供模板标识 <code>{draft.templateId || '尚未填写'}</code>。</p><p className="dsh-fishyume-template-summary-note">保存后，Host Agent 才能在发起团队任务时调用该模板。<br /><br />团队模板仅保存配置，不会立即启动成员。</p></aside>
  </div>
}

function TeamTemplatesView({ state }: { state: PanelState }): JSX.Element {
  if (state.templateMode === 'create') return <TeamTemplateEditor capabilities={state.capabilities} template={state.templates.find((item) => item.templateId === state.selectedTemplate)} />
  return <TeamTemplateListView state={state} />
}

function EnhancedWorkspaceV2({ state }: { state: PanelState }): JSX.Element {
  const title = state.view === 'teams' ? '团队工作区' : state.view === 'runs' ? '工作流执行' : state.view === 'routing' ? '路由配置' : '团队设置'
  return <div className="dsh-fishyume-workspace"><header className="dsh-fishyume-header"><div className="dsh-fishyume-brand"><button type="button" className="dsh-fishyume-back" data-dsh-center-view-back="" aria-label="返回会话" onClick={() => updatePanel({ open: false })}><span aria-hidden="true">←</span><span>返回会话</span></button><img className="dsh-fishyume-logo" src={fishyumeLogo} alt="" aria-hidden="true" /><div><span className="dsh-fishyume-eyebrow">FISHYUME</span><h2>{title}</h2></div></div><button type="button" onClick={() => { if (state.view === 'teams') state.teamSection === 'templates' ? void loadTeamTemplates() : void loadTeams(); else if (state.view === 'runs') void loadRuns(); else if (state.view === 'routing') void loadRouting(); else void loadTeams() }} disabled={state.loading} aria-label="刷新" title="刷新">↻</button></header><nav className="dsh-fishyume-tabs" aria-label="Fishyume 视图">{(['teams', 'runs', 'routing', 'settings'] as const).map((item) => <button key={item} type="button" aria-current={state.view === item ? 'page' : undefined} onClick={() => { updatePanel({ view: item, ...(item === 'teams' ? { teamSection: 'tasks', templateMode: 'list' } : {}) }); if (item === 'teams') void loadTeams(); if (item === 'runs') void loadRuns(); if (item === 'routing') void loadRouting(); if (item === 'settings') void loadTeams() }}>{item === 'teams' ? '团队' : item === 'runs' ? '工作流' : item === 'routing' ? '路由' : '设置'}</button>)}</nav>{state.view === 'teams' ? <TeamSubnav state={state} /> : null}{state.error ? <div className="dsh-fishyume-state" role="alert">{state.error}<button type="button" onClick={() => state.view === 'teams' && state.teamSection === 'templates' ? void loadTeamTemplates() : void loadTeams()}>重试</button></div> : null}{state.loading ? <div className="dsh-fishyume-state">正在加载...</div> : null}{!state.loading && !state.error && state.view === 'teams' ? state.teamSection === 'templates' ? <TeamTemplatesView state={state} /> : <TeamTasksView state={state} /> : null}{!state.loading && !state.error && state.view === 'runs' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="工作流列表">{state.runs.length ? state.runs.map((run) => <button key={run.runId} type="button" className={run.runId === state.selectedRun ? 'is-selected' : ''} onClick={() => { updatePanel({ selectedRun: run.runId, selectedEvent: undefined }); void loadRunDetail(run.runId) }}><strong>{run.workflowName || run.runId}</strong><small>{localizedPhase(run.phase)}{run.conclusion ? ` · ${localizedConclusion(run.conclusion)}` : ''}</small></button>) : <div className="dsh-fishyume-state">暂无工作流。</div>}</div>{state.selectedRun ? (state.runDetail ? <EnhancedWorkflowDetail state={state} run={state.runDetail} /> : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">正在加载工作流详情...</div></section>) : <section className="dsh-fishyume-detail"><div className="dsh-fishyume-state">选择一个工作流。</div></section>}</div> : null}{!state.loading && !state.error && state.view === 'routing' ? <div className="dsh-fishyume-columns"><div className="dsh-fishyume-list" aria-label="执行器列表">{state.drivers.length ? state.drivers.map((driver) => <div key={driver.driver} className="dsh-fishyume-driver"><strong>{driver.driver}</strong><small>{driver.available ? '可用' : driver.diagnostic || '不可用'}{driver.version ? ` · ${driver.version}` : ''}{driver.authenticated === true ? ' · 已认证' : driver.authenticated === false ? ' · 未认证' : ''}</small></div>) : <div className="dsh-fishyume-state">暂无执行器。</div>}</div><section className="dsh-fishyume-detail" aria-label="生效路由"><span className="dsh-fishyume-eyebrow">路由</span><h3>生效路由</h3>{state.routes.length ? state.routes.map((route) => <p key={route.routeId}><strong>{route.routeId}</strong><br /><small>{route.driver || '未知'} / {route.provider || '默认'} / {route.model || '默认'} · {route.enabled ? '已启用' : '已停用'}</small></p>) : <p>暂无团队路由配置。</p>}</section></div> : null}{!state.loading && !state.error && state.view === 'settings' ? <EnhancedTeamSettings team={state.teamDetail || state.teams.find((team) => team.teamId === state.selectedTeam)} /> : null}</div>
}

function FishyumePanel(): JSX.Element {
  const state = useSyncExternalStore(subscribe, snapshot)
  const [loaded, setLoaded] = useState(false)
  useEffect(() => {
    if (document.querySelector('style[data-dsh-fishyume]')) return
    const style = document.createElement('style'); style.dataset.dshFishyume = ''; style.textContent = nativeStyles + centerStyles; document.head.append(style)
    return () => { style.remove() }
  }, [])
  useEffect(() => { if (state.open && !loaded) { setLoaded(true); void loadTeams() } }, [state.open, loaded])
  useEffect(() => {
    if (!state.open) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async (): Promise<void> => {
      try {
        const response = await fetch('/plugins/dsh-fishyume/api/focus', { cache: 'no-store' })
        const data = await response.json() as { revision?: number; target?: { kind?: string; teamId?: string; handoffId?: string; runId?: string } }
        if (!cancelled && typeof data.revision === 'number' && data.revision > panel.focusRevision && data.target) {
          updatePanel({ focusRevision: data.revision })
          applyFocus(data.target)
        }
      } catch { /* workspace keeps its last successful snapshot */ }
      if (!cancelled) timer = setTimeout(() => { void poll() }, 2000)
    }
    void poll()
    return () => { cancelled = true; if (timer) clearTimeout(timer) }
  }, [state.open, state.focusRevision])
  useEffect(() => {
    const teamId = state.selectedTeam
    const member = state.teamDetail?.participants?.find((item) => item.participantId === state.selectedMember)
    const memberState = member ? memberStatusState(state.teamDetail, member, state.teamDetail?.turns?.find((item) => item.turnId === member.currentTurnId)) : undefined
    if (!state.open || !teamId || !state.selectedMember || !isLiveParticipantState(memberState)) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const pollTeam = async (): Promise<void> => {
      try { await loadTeamDetail(teamId) } catch { /* retain the last successful snapshot */ }
      if (!cancelled) timer = setTimeout(() => { void pollTeam() }, 1500)
    }
    timer = setTimeout(() => { void pollTeam() }, 1500)
    return () => { cancelled = true; if (timer) clearTimeout(timer) }
  }, [state.open, state.selectedTeam, state.selectedMember, state.teamDetail?.participants, state.teamDetail?.turns])
  return <div className="dsh-fishyume-panel" data-dsh-plugin="dsh-fishyume" role="region" aria-label="Fishyume 工作区"><EnhancedWorkspaceV2 state={state} /></div>
}

const CENTER_COLUMN_SELECTOR = '[data-pane="conversation"], [class*="centerCol"]'
const FISHYUME_ACTIVE_ATTR = 'data-dsh-fishyume-active'
const PANEL_ACTIVATE_EVENT = 'dsh-panel-activate'

function mountCenterWorkspace(): () => void {
  if (typeof document === 'undefined') return () => {}
  let root: Root | undefined
  let container: HTMLDivElement | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  const ensure = (): void => {
    if (container?.isConnected) return
    root?.unmount()
    container?.remove()
    const column = document.querySelector<HTMLElement>(CENTER_COLUMN_SELECTOR)
    if (!column) {
      container = undefined
      root = undefined
      if (!retryTimer) {
        retryTimer = setTimeout(() => {
          retryTimer = undefined
          ensure()
        }, 100)
      }
      return
    }
    container = document.createElement('div')
    container.dataset.dshFishyumeView = ''
    container.dataset.dshPlugin = 'dsh-fishyume'
    container.className = 'dsh-fishyume-view'
    column.appendChild(container)
    root = createRoot(container)
    root.render(<FishyumePanel />)
    applyActive()
  }
  const applyActive = (): void => {
    if (panel.open) {
      document.documentElement.removeAttribute('data-dsh-taskboard-active')
      document.documentElement.removeAttribute('data-dsh-ssh-active')
      document.documentElement.setAttribute(FISHYUME_ACTIVE_ATTR, '')
      document.dispatchEvent(new CustomEvent(PANEL_ACTIVATE_EVENT, { detail: 'fishyume' }))
      container?.setAttribute('data-active', '')
    } else {
      document.documentElement.removeAttribute(FISHYUME_ACTIVE_ATTR)
      container?.removeAttribute('data-active')
    }
  }
  const onOtherPanel = (event: Event): void => {
    const detail = (event as CustomEvent).detail
    if (detail !== 'fishyume' && panel.open) updatePanel({ open: false })
  }
  // Hand the center column back to DSH before it processes a session switch.
  // This also handles clicking the already-open session, which may not emit a
  // navigation event from the host shell.
  const SIDEBAR_ROW_SELECTOR = '[class*="sessionRow"], [class*="projectRow"], [class*="searchResultRow"], [class*="searchResultWorkspace"], [class*="newSession"]'
  const onClickSidebarRow = (event: MouseEvent): void => {
    if (!panel.open) return
    const target = event.target as HTMLElement | null
    if (target?.closest(SIDEBAR_ROW_SELECTOR) !== null) updatePanel({ open: false })
  }
  const observer = new MutationObserver(ensure)
  observer.observe(document.body, { childList: true, subtree: true })
  document.addEventListener(PANEL_ACTIVATE_EVENT, onOtherPanel)
  document.addEventListener('click', onClickSidebarRow, true)
  const unsubscribe = subscribe(applyActive)
  ensure()
  applyActive()
  return () => {
    observer.disconnect()
    if (retryTimer) clearTimeout(retryTimer)
    document.removeEventListener(PANEL_ACTIVATE_EVENT, onOtherPanel)
    document.removeEventListener('click', onClickSidebarRow, true)
    unsubscribe()
    document.documentElement.removeAttribute(FISHYUME_ACTIVE_ATTR)
    root?.unmount()
    container?.remove()
    root = undefined
    container = undefined
  }
}

export function apply(ctx: ClientContext): void {
  const connection = ctx.get?.('connection') as { rpc?: { call(channel: string, endpoint: string, payload: unknown): Promise<unknown> } } | undefined
  if (connection?.rpc) transport = createConnectionTransport(connection, transport)
  const remoteService = ctx.remote ?? ctx.get?.('remote') as ClientContext['remote']
  if (remoteService?.$mount && ctx.reflect && ctx.effect) {
    ctx.effect(async () => {
      const dispose = await remoteService.$mount(FISHYUME_REMOTE)
      const remote = ctx.reflect?.get('remote.fishyume') as FishyumeRemoteFace | undefined
      if (remote) transport = createRemoteTransport(remote, transport)
      return () => {
        void dispose()
        transport = createHttpTransport({ rpcPath: '/plugins/dsh-fishyume/api/rpc', tokenPath: '/plugins/dsh-fishyume/token' })
      }
    }, 'dsh-fishyume: remote')
  }
  ctx.effect?.(() => mountSidebarEntry(), 'dsh-fishyume: sidebar entry')
  ctx.effect?.(() => mountCenterWorkspace(), 'dsh-fishyume: center workspace')
}
