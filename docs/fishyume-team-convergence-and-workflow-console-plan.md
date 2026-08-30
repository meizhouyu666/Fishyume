# Fishyume Team 收敛 + Workflow Console 方案

> 状态：讨论稿
> 前置：M7 team（panel/session/handoff）已实现；M7.8 动态 Codex 路由；M7.9 零配置 team 路由
> 性质：收敛 team（砍 session、去 mode 声明）、确立 workflow 为唯一主方向

## 0. 决策摘要

1. **砍掉 `session` 模式，只留 `panel`**；随之删除 `mode` 声明（后端 contract + UI）。
2. **用 DriverInventory 统一探测本机 harness**，砍掉默认模板，team 参与者按「支持 ∩ 可用」动态生成，配一个设置页显示 ✗/版本号/认证。
3. **workflow 是唯一主方向**：给现有 durable execution 引擎套一层 Linear 式观测/规划 UX，不再给 team 加码。

---

## 1. Team 收敛：砍 session，留 panel

### 1.1 理由

`panel`（2-4 个模型各跑一次性只读进程 → 产出贡献）已经覆盖 team 唯一真正有用的场景——「拿 N 个独立模型的一次性判断」。`session`（多轮可续聊）要解决的「实时多轮讨论」价值很薄（一个强模型多轮通常更好），却背了 M7.3/M7.4 整套机器（AgentSession driver + app-server v2 + park/resume + 会话身份 + 256-turn 上限）。性价比不成立，砍。

### 1.2 删除范围

**确定删除（`mode: session` 的多轮用户面）：**

| 位置 | 内容 |
|---|---|
| `internal/team/session_mode.go` + `session_mode_test.go` | 多轮 TeamSession 生命周期（park/resume/follow-up/close） |
| `internal/team/session_actions.go` | `follow_up` / `cancel_turn` / `close` 动作 |
| `internal/team/service.go` | `SetSessionDriver` / `ReplaceSessionDriver` / `RemoveSessionDriver` 及 ModeSession 分支 |
| `internal/team/public.go` | capabilities 里的 session features、session 生命周期、session actions |
| `internal/teamcontract/contract.go` | `ModeSession` 常量、session 相关 action 类型 |
| web client | session 相关 UI（session 视图、follow-up/cancel-turn 控件） |

**保留（panel 的执行原语，不是「模式」）：**

| 位置 | 原因 |
|---|---|
| `internal/sessiondriver/`（契约 + contracttest） | panel 的 claude/opencode 一次性执行靠它（`harnesssession.ExplorationAdapter` = StartSession → 单 turn） |
| `internal/driver/harnesssession/` | claude/opencode 的 exploration adapter（面板用） |
| `internal/driver/codexprocess/session*.go` | codex 的会话适配（harnesssession 同层原语） |

> 注意：`sessiondriver` 这个包名容易让人以为是「session 模式」，其实它是「外部 harness 单轮会话原语」。砍的是它的多轮上层，不是它本身。建议顺手把它重命名为更贴切的名字（如 `harnessconversation` 或 `turnruntime`），避免以后再混淆。

### 1.3 contract 变更（去 mode 声明）

- `TeamStartRequestV1` 删除 `Mode` 字段；`mode` 不再出现在任何 request/response/UI。
- `team.capabilities` 删除 `supportedModes`、`session` feature 位；只保留 `panel`。
- session 专属 action 类型（follow_up / cancel_turn / close）从 action 枚举中删除。
- 兼容：历史 `mode: session` 快照仍可读（只读兼容），但新请求一律 panel；不可恢复的历史 session 返回明确诊断，不重放。

### 1.4 验收门禁

- `go test ./...` / `go vet ./...` / `go build ./cmd/wf-engine` 通过；
- 代码库无 `ModeSession`、`follow_up`、`cancel_turn`、`close` 残留；
- `team.start` 不带 mode 仍能创建 panel 并产出贡献；
- 历史 session 快照可读、不可重放；
- MCP / Machine / TUI 的 team 面不再出现 `mode` / `session` 字样。

---

## 2. DriverInventory + 设置页 + 动态 participants

### 2.1 三层结构

```
[探测层] DriverInventory probe（引擎）
         对 codex/claude/opencode 探测 { installed, version, authenticated, models[] }
         —— 单一真相源

[能力广告层]（per-surface，从探测层投影）
         system.capabilities → workflow：只广告 codex（∩ 可用）
         team.capabilities    → team：广告 claude/codex/opencode（∩ 可用）

[设置页]（client）
         渲染 DriverInventory：每个 harness ✗ / 版本号 / 认证状态
```

### 2.2 DriverInventory 类型

```json
{
  "drivers": [
    { "driver": "codex",    "installed": true,  "version": "0.148.0", "authenticated": true, "models": ["gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"] },
    { "driver": "claude",   "installed": true,  "version": "x.y.z",   "authenticated": true, "models": ["sonnet", "opus"] },
    { "driver": "opencode", "installed": false, "version": null,      "authenticated": false, "models": [] }
  ]
}
```

- 探测层已部分存在：M7.8 的 `codexprocess/models.go`（codex 模型探测）+ `routingconfig/team_routes.go`（team driver 可用性）。缺的是把 claude/opencode 也接进同一层，并暴露成统一 `DriverInventory`。
- 复用已有的 `driver.models.probe` RPC，扩展成对三个 harness 的探测。

### 2.3 砍默认模板 + 动态 participants

- 现在 `team.capabilities` 的 `participantTemplates`（architect=claude / reviewer=codex / researcher=opencode）写死三个。改成：
  - 按 DriverInventory 的「可用 harness」集合动态生成 participants（每个带默认模型 + 默认角色）；
  - 默认 participants = 可用集合（可能少于 3 个）；min 2 个 panel 参与者的约束按可用数调整（只有 1 个 harness 可用时给出明确诊断，而不是硬塞）。
  - Host 发起 team 前读 `team.capabilities`，按任务类别（设计→architect、挑刺→reviewer、取证→researcher）+ 可用集合选参与者。

### 2.4 设置页

- 读 DriverInventory，渲染：

```
Codex      ✓ codex-cli 0.148.0  (authenticated)   → workflow + team
Claude     ✓ claude x.y.z       (authenticated)   → team
OpenCode   ✗ 未安装                               → team
```

- `✗` / 版本号 / 认证状态全部来自 inventory，UI 不硬编码。

### 2.5 workflow vs team 的 surface 分离

- **「支持哪些」是产品能力，**「装没装」是环境事实 **，两者正交。**
- 探测层只回答「装没装/版本/认证/模型」，与 team/workflow 无关。
- `system.capabilities`（workflow）声明「支持 codex」；`team.capabilities` 声明「支持 claude/codex/opencode」。各自再按 inventory 过滤成「支持 ∩ 可用」。
- 将来 workflow 支持 claude 时，只改 workflow 的能力声明，探测层和设置页不动。

### 2.6 验收门禁

- `DriverInventory` 探测三 harness，本机结果如实（本机 opencode 应显示 `installed:false`）；
- `team.capabilities` 不再出现不可用 harness 的模板；
- 只装 codex 时，`team.start` 用 codex 系参与者能跑通；
- 设置页渲染 ✗/版本号/认证，无硬编码；
- M7.8/M7.9 的 codex 探测/零配置路由不回退。

---

## 3. Workflow Console（Linear 借鉴）—— 主方向

### 3.1 定位

fishyume 的 workflow 护城河 = **durable execution 引擎（已有）+ Linear 式观测/规划 UX（缺）**。不是再做一个 workflow 引擎，而是给现有引擎套一层「状态流 + cycle + 键盘优先 + 看板」的观测壳。

### 3.2 借什么（Linear → fishyume workflow）

| Linear 概念 | 落到 fishyume |
|---|---|
| triage → in-progress → done 状态流 | Run/Node phase 已天然是 `waiting → running → blocked → completed`，做成一等看板 |
| 建议状态机 | `Inbox/Triage → Ready → Running → Needs input/Approval → Succeeded | Failed → Archived`（triage 与执行状态分离） |
| Cycles | 加「program/cycle」分组层：一个目标 = 一个 cycle = 一批 Run（可选，不当执行前提） |
| 键盘优先 + ⌘K | web console 做命令面板 + 全程键盘（TUI 已是键盘流） |
| board / list / roadmap 三视图 | board = 按 phase 的 Run/Node kanban；list = run.list；roadmap = 已有 topology 视图 |
| sub-issue 层级 | Node → Attempt drill-down |
| opinionated 默认值 | 状态/重试/并发/审批/保留 全有出厂默认，per-project 可覆盖 |

### 3.3 别借什么

Jira 式状态爆炸、issue-only 建模、把看板/cycle 当强制、以图编辑器当主 UX、无界自动化、过早的协作功能、视觉花活当差异化。

### 3.4 建议架构

```
本地 daemon
  → append-only 事件日志 + 调度器（已有）
  → 可插拔 worker/runtime（已有 Driver 抽象）
  → 投影：run 列表 / board / cycle / roadmap（缺，要做）
  → 命令面板 + 审批层（缺，要做）
```

### 3.5 护城河（一句话）

不是「又一个 Linear 克隆」，而是：**可靠本地执行 + 统一上下文图（issue↔prompt↔run↔tool↔artifact↔approval）+ 工具边界安全 + 异构运行时调度 + Linear 式低摩擦 UX + 开放可移植数据**。

### 3.6 分阶段

- **Phase 1**：状态流 + triage 一等看板（board 投影 Run/Node phase）
- **Phase 2**：cycle/program 分组层 + 键盘命令面板（⌘K）
- **Phase 3**：sub-node/attempt drill-down + roadmap 时间线
- **Phase 4**：opinionated 默认值 + per-project policy 覆盖

---

## 4. 优先级

1. **Part A（team 收敛）** 先做：纯删减，低风险，立刻止血，让 team 回归「panel + handoff」的轻形态。
2. **Part B（DriverInventory + 设置页）** 次之：解决「不是所有人都有三个 harness」的适配问题，是 team 可用性的前提。
3. **Part C（workflow console）** 是长期主方向：投入最大、护城河最深，但不必一次做完，按 Phase 1→4 渐进。

> 总原则：**team 只保留「panel 拿判断 → handoff → workflow 确定性执行」这条链路；所有新增投入转向 workflow 的 Linear 式观测壳。**
