import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const source = readFileSync(fileURLToPath(new URL('./client/plugin.tsx', import.meta.url)), 'utf8')

test('native client registers a top-level sidebar entry and center workspace', () => {
  assert.match(source, /mountSidebarEntry\(\)/)
  assert.match(source, /data-dsh-fishyume-entry/)
  assert.match(source, /fishyume-dsh\.png/)
  assert.match(source, /fishyume\.png/)
  assert.match(source, /团队与 workflow/)
  assert.match(source, /mountCenterWorkspace\(\)/)
  assert.match(source, /dshFishyumeView/)
  assert.match(source, /data-dsh-center-view-back/)
  assert.match(source, /返回会话/)
  assert.doesNotMatch(source, /ctx\.slots\.inject\('shell\.overlay'/)
})

test('native client mounts the DSH Remote when the runtime exposes it', () => {
  assert.match(source, /\$mount\(FISHYUME_REMOTE\)/)
  assert.match(source, /remote\.fishyume/)
  assert.match(source, /createRemoteTransport\(/)
})

test('native client is iframe-free and uses same-origin transport', () => {
  assert.doesNotMatch(source, /<iframe\b/i)
  assert.match(source, /\/plugins\/dsh-fishyume\/api\/rpc/)
  assert.match(source, /\/plugins\/dsh-fishyume\/token/)
})

test('native workspace covers durable Team, Handoff, Run, and Routing reads', () => {
  for (const method of ['team.list', 'team.get', 'team.events', 'team.messages', 'team.handoff.list', 'run.list', 'run.get', 'run.events', 'driver.list', 'team.routes.get']) {
    assert.match(source, new RegExp(`['"]${method}['"]`), `missing ${method} adapter call`)
  }
  assert.match(source, /applyFocus\(/)
  assert.match(source, /focusRevision/)
  assert.match(source, /selectedNode/)
  assert.match(source, /事件时间线/)
  assert.match(source, /团队设置/)
})

test('native Team workspace exposes reusable template list and editor', () => {
  for (const method of ['team.template.list', 'team.template.upsert']) {
    assert.match(source, new RegExp(`['"]${method}['"]`), `missing ${method} adapter call`)
  }
  assert.match(source, /团队任务/)
  assert.match(source, /团队模板/)
  assert.match(source, /创建团队模板/)
  assert.match(source, /TeamTemplateEditor/)
  assert.match(source, /Harness（可选）/)
  assert.match(source, /随 Harness 可选/)
})

test('native Run workspace exposes explicit cancel and node actions', () => {
  assert.match(source, /runNodeAction\(/)
  assert.match(source, /type: 'cancel'/)
  assert.match(source, /type,\n\s+nodeId: node\.nodeId/)
  assert.match(source, /acknowledgeDuplicateRisk: true/)
  assert.match(source, />Approve</)
  assert.match(source, />Reject</)
  assert.match(source, />Retry</)
})

test('team member Harness badges use bundled LobeHub icon components', () => {
  assert.match(source, /@lobehub\/icons\/es\/Claude/)
  assert.match(source, /@lobehub\/icons\/es\/OpenCode/)
  assert.match(source, /@lobehub\/icons\/es\/DeepSeek/)
  assert.match(source, /harnessIcon\(member\.driver\)/)
  assert.match(source, /@lobehub\/icons\/es\/OpenAI/)
  assert.match(source, /<HarnessIcon size=\{16\} aria-hidden="true" \/>/)
})

test('team member cards open a right-side detail drawer', () => {
  assert.match(source, /selectedMember\?: string/)
  assert.match(source, /function MemberCardGrid\(/)
  assert.match(source, /data-selected=\{selected \? '' : undefined\}/)
  assert.match(source, /onKeyDown=\{\(event\) => \{/)
  assert.match(source, /function MemberDrawer\(/)
  assert.match(source, /dsh-fishyume-member-drawer-backdrop/)
  assert.match(source, /aria-label="成员详情"/)
  assert.doesNotMatch(source, /<details><summary>查看成员详情/)
})

test('agent status stream renders participant contributions instead of raw events', () => {
  assert.match(source, /function contributionItems\(team\?: TeamDetail\)/)
  assert.match(source, /message\.kind === 'participant_contribution'/)
  assert.match(source, /function AgentStatusStream\(\{ team \}/)
  assert.match(source, /aria-label="成员产出"/)
  assert.match(source, /message\.content\}/)
})

test('member detail provides a fixed-height live harness activity timeline', () => {
  assert.match(source, /function MemberLiveStatus\(/)
  assert.match(source, /dsh-fishyume-live-scroll/)
  assert.match(source, /height:336px;min-height:336px;max-height:336px/)
  assert.match(source, /dsh-fishyume-live-timeline::before/)
  assert.match(source, /data-tone=\{liveActivityTone/)
  assert.match(source, /onWheelCapture=\{handleWheel\}/)
  assert.match(source, /event\.stopPropagation\(\)/)
  assert.match(source, /event\.preventDefault\(\)/)
  assert.match(source, /state\.selectedMember, state\.teamDetail\?\.participants/)
  assert.match(source, /setTimeout\(\(\) => \{ void pollTeam\(\) \}, 1500\)/)
})

test('member status badge follows backend participant state instead of hardcoded Live', () => {
  assert.match(source, /function memberStatusState\(team: TeamDetail \| undefined, member: TeamParticipant/)
  assert.match(source, /function participantStatusLabel\(state\?: string\)/)
  assert.match(source, /data-live=\{live \? '' : undefined\}/)
  assert.match(source, /participantStatusLabel\(status\)/)
  assert.doesNotMatch(source, />Live<\/span>/)
  assert.match(source, /responded: '已响应'/)
  assert.match(source, /indeterminate: '状态不确定'/)
})

test('team messages normalize structured payloads before rendering', () => {
  assert.match(source, /function formatTeamMessage\(message: TeamMessage\)/)
  assert.match(source, /content: formatTeamMessage\(message\)/)
  assert.match(source, /white-space:pre-wrap/)
  assert.match(source, /grid-template-columns:30px max-content minmax\(0,1fr\)/)
})

test('node detail localizes technical labels', () => {
  assert.match(source, /function localizedNodeType\(/)
  assert.match(source, /function localizedDriver\(/)
  assert.match(source, /type: localizedNodeType\(sourceNode\.type\)/)
  assert.match(source, /driver: localizedDriver\(sourceNode\.attempt\.driver\)/)
})
