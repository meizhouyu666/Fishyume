# Fishyume M3.3：Calm Operator Console

> 状态：已实现并通过自动化验收（2026-08-06）
>
> 定位：Fishyume 的正式产品表面仍是 CLI/TUI。M3.3 建立克制、稳定、高密度的编排控制台视觉语言，不扩展到 Web/Desktop，不实现模型路由、Prompt Library 或新的 Engine 能力。

## 1. 产品方向

Fishyume 不复制单 Agent 的聊天界面，也不做传统运维 Dashboard。它的独特价值是同时展示 Workflow、并行节点、Attempt、人工决策和可恢复状态，因此 M3.3 采用：

**Calm Operator Console（克制的编排控制台）**

核心体验：

- Workflow 是主体，不是聊天记录；
- 当前焦点节点承担主要细节，其他节点保持低噪声；
- 颜色只帮助扫描，符号和文字承载完整语义；
- 布局在 streaming 更新中保持稳定；
- 快捷键只显示当前真正可执行的动作；
- 默认保留终端 scrollback，不进入 alternate screen；
- 终态强调结果、耗时、风险和下一步，而不是只替换一个状态标签。

## 2. 设计原则

### 2.1 一个视觉焦点

同一时刻只允许一个主要焦点：选中的 Workflow 节点及其详情。Header、状态汇总和 Footer 是辅助层级，其他节点行不得与焦点争夺强调度。

### 2.2 减少盒子

- 不再为 Workflow、Attempts、Approvals、Diagnostics、Summary 分别绘制完整边框；
- 使用留白、单行 Divider、缩进和标题层级组织内容；
- 只允许焦点详情或危险确认区域使用强边界；
- 80 columns 下优先去掉装饰，不隐藏状态和动作。

### 2.3 稳定而非活泼

- 不使用高频 spinner、闪烁、渐变或随机动画；
- 计时仍按 1 秒刷新；
- 状态变化不应改变无关节点的相对位置；
- 固定列和有界文本不得随内容长度左右抖动；
- action pending 可以使用稳定的 `…`/`working` 文本，不做逐帧动画。

### 2.4 颜色不是唯一信息

- 所有状态都有独立符号和短标签；
- mono/NO_COLOR 与彩色模式使用相同层级、缩进、选中标记和文字；
- 同屏可见强调色原则上不超过三类：品牌/活动、等待/审批、危险/失败；
- muted 信息不能低对比到不可读。

### 2.5 Scrollback 友好

- 不默认启用 alternate screen；
- 退出后用户仍可回看最后一帧和必要命令；
- 不依赖鼠标、窗口尺寸 API 或终端专有协议；
- TTY 交互增强不得污染非 TTY、CI 或 JSON 输出。

## 3. 视觉语法

### 3.1 状态层级

Header 使用完整状态标签；Workflow 行使用紧凑符号和短标签，避免重复 15 列宽 Badge。

| 状态 | 默认符号 | ASCII fallback | 行标签 |
| --- | --- | --- | --- |
| running | `●` | `>` | `run` |
| waiting | `◌` | `.` | `wait` |
| approval | `◆` | `?` | `approve` |
| succeeded | `✓` | `+` | `done` |
| failed | `!` | `!` | `fail` |
| indeterminate | `?` | `?` | `unknown` |
| cancelled | `×` | `x` | `cancel` |
| rejected | `×` | `x` | `reject` |
| cancelling | `◍` | `~` | `stop` |
| paused | `Ⅱ` | `|` | `pause` |
| ready | `◇` | `>` | `ready` |
| pending | `○` | `o` | `pending` |
| skipped | `·` | `-` | `skip` |

符号模式：

- 默认使用 Unicode terminal-safe 字符；
- `TERM=dumb` 或 `FISHYUME_ASCII=1` 使用 ASCII fallback；
- CJK、emoji、combining marks 继续由统一 display-width 原语处理；
- 任何符号切换不得改变状态文字和业务语义。

### 3.2 配色

- `brand/active`：cyan/teal；
- `waiting/approval`：amber/magenta，但审批只使用一种主色；
- `success`：green；
- `danger`：red/rose；
- `neutral/muted`：终端安全灰度，保证浅色背景仍可读。

主题策略：

- 保留现有 TrueColor / ANSI256 / ANSI16 / mono 能力；
- 可增加 `FISHYUME_THEME=dark|light|auto|mono`，但不得要求用户配置才能获得可读输出；
- `auto` 只能使用可靠提示（例如 `COLORFGBG`），无法判断时采用背景无关的安全 palette；
- 不新增独立 CLI theme 命令或主题插件系统。

### 3.3 分隔与层级

- Header 下允许一条轻量 Divider；
- Workflow 节点不使用逐行边框；
- Focus Detail 上下使用 Divider 或单侧强调线；
- Footer 与正文之间使用一条稳定 Divider；
- Unicode `─` 默认，ASCII `-` fallback；
- 不同时混用多套边框风格。

## 4. 信息架构

### 4.1 Wide / Medium（120–160 columns）

```text
FISHYUME / release-workflow                         RUNNING  02:18
run-a91f · direct · capacity 3                         4/8 settled
────────────────────────────────────────────────────────────────

  ✓ done     plan             agent      attempt 1
› ● run      implement        agent      attempt 2 · direct
  ● run      tests            agent      attempt 1 · direct
  ◆ approve  review           approval   approval_required
  ○ pending  publish          agent

──────────────── implement / attempt 2 ─────────────────────────
direct · handle persisted · execution pid:4821
Implementing interactive console safety fixes…
diagnostic or selected-node result detail
────────────────────────────────────────────────────────────────

2 active · 1 waiting · capacity 3
↑↓/j/k select  Enter details  a approve  r reject  R retry  c cancel  ? help
```

### 4.2 Narrow（80 columns）

```text
FISHYUME / release-workflow
RUNNING · 02:18 · 4/8 settled · capacity 3
────────────────────────────────────────────────────────────────
  ✓ done     plan
› ● run      implement · a2 · direct
  ● run      tests · a1 · direct
  ◆ approve  review
  ○ pending  publish
──────────────── implement ─────────────────────────────────────
attempt 2 · direct · handle persisted
Implementing interactive console safety fixes…
────────────────────────────────────────────────────────────────
↑↓ select  Enter detail  ? help
a approve  r reject  R retry  c cancel  q detach
```

80 columns 可以换行 Footer，但任何单行都不得溢出。40–79 columns 继续尽力渲染，不作为完整审美验收档位。

## 5. Workflow 与焦点详情

### 5.1 Workflow 行

每行优先级：

1. 选择标记；
2. 状态符号和短标签；
3. Node ID；
4. 类型；
5. Attempt number / Backend；
6. Reason；
7. Diagnostic 摘要。

中窄屏按尾部优先级依次裁剪，但状态、Node ID 和选择标记不可丢失。

活动 Attempt 信息合并进对应 Workflow 行；默认不再单独重复整个 `ACTIVE ATTEMPTS` Section。等待 Approval 和 Node diagnostic 同样优先归并到节点行及 Focus Detail。

### 5.2 视觉选择与动作资格

- `j/k` 与方向键在全部 Workflow 节点之间移动视觉焦点，而不只遍历 actionable 节点；
- action key 仍仅对 Engine 当前允许的节点生效；
- M3.2 已实现的 `nodeId/kind/duplicateRisk` 动作身份固定必须保留；
- 视觉选择变化不得重定向正在输入或确认的 action；
- 选中节点消失时安全选择邻近节点；action target 消失时仍取消 action mode。

### 5.3 Focus Detail

焦点详情从现有 `RunStatusView` 派生：

- Node type / phase / conclusion / reason；
- active Attempt number、Backend、launch state、execution identity；
- Approval prompt/diagnostic；
- Node result summary、warnings、checks、artifacts（存在时）；
- Engine diagnostics；
- 当前 action pending/message；
- 终态时的下一步命令。

不得为了视觉详情新增 RPC 字段。缺失数据应省略，不显示 `undefined`、空标题或伪造耗时。

展示规则：

- 120/160 columns 默认展示 Focus Detail；
- 80 columns 保留紧凑详情；
- `Enter` 可折叠/展开详情，折叠属于 React 本地展示状态；
- action 输入/确认时，Focus Detail 暂时切换为 Action Detail，并固定到 action target；
- action 结束后恢复选中节点详情。

## 6. Header、Status Strip 与 Footer

### 6.1 Header

Header 必须在两秒扫描内回答：

- 这是哪个 Workflow；
- Run 当前状态；
- 已运行多久；
- Run ID / Backend；
- 并发 capacity；
- settled 进度。

State directory 属于次级信息；120/160 可保留，80 默认移入 Focus/Help 或最终结果，不挤占首屏。

### 6.2 Status Strip

正文底部使用一行紧凑汇总：active、waiting、failed、skipped、capacity。值为零的分类不显示，避免噪声。

### 6.3 Context Footer

Footer 只显示当前可用动作：

- 普通节点：select、details、help、detach；
- waiting approval：approve、reject；
- retryable node：retry；
- running Run：cancel；
- action input/confirm：Enter confirm、Esc discard；
- terminal：status/state/exit，不显示 cancel/approve/retry；
- Narrow 可换为两行，Medium/Wide 优先单行。

不得展示按下后无效果的快捷键。

## 7. 六个标准场景

M3.3 必须为以下场景建立确定性 fixtures：

1. 并发运行：至少两个 active Attempts，不同 Backend/Attempt number；
2. 等待审批：Approval 被选中，展示 prompt 和 approve/reject；
3. 失败可重试：failed 节点、diagnostic、retry action；
4. Indeterminate：duplicate-risk 明确可见，确认态不被其他节点变化重定向；
5. Cancelling：显示未确认取消的 active executions，不能提前渲染 cancelled；
6. Terminal：succeeded/failed/cancelled/rejected 至少覆盖代表性终态，展示 summary 和下一步。

每个场景至少验证：80/120/160 columns、mono，以及一种彩色能力档位。测试重点是层级、内容和宽度，不把 ANSI 转义序列作为脆弱的全量 snapshot。

## 8. 实施边界

允许修改：

- `wf/src/tui/design-tokens.ts`
- `wf/src/tui/layout.ts`
- `wf/src/tui/presentation.ts`
- `wf/src/tui/components.tsx`
- `wf/src/tui/interaction.ts`
- `wf/src/tui/run-app.tsx`
- 必要的 TUI fixtures/tests 和文档

谨慎修改：

- `wf/src/tui/live-console.tsx`：仅为展示状态或选择状态接线，不改变 M3.2 controller ownership/detach 语义；
- `wf/src/commands/*.ts(x)`：不得改变非 TTY、CI、JSON、退出码和现有 CLI 参数合同。

禁止修改：

- Go Engine 业务模型、RPC protocol、持久化 schema；
- Backend Registry/Adapter；
- 自动 retry、timeout、fallback、预算或模型路由；
- Web/Desktop；
- alternate-screen 强制模式、鼠标依赖或终端专有协议。

## 9. 验收标准

- [x] 视觉方向符合 Calm Operator Console，不再由多个完整 Panel 主导首屏。
- [x] Header、Workflow、Focus Detail、Status Strip、Context Footer 层级清晰。
- [x] Workflow 行使用紧凑状态语法；Header 保留完整状态语义。
- [x] 活动 Attempt、Approval、diagnostic 不再形成重复信息区块，而是归并到节点和 Focus Detail。
- [x] `j/k`/方向键可遍历全部节点；动作资格和 M3.2 action identity safety 不回归。
- [x] Enter 折叠/展开详情；action mode 固定目标并优先显示。
- [x] Footer 不展示当前无效动作。
- [x] streaming 中行位置、列宽和焦点稳定；无高频动画或视觉抖动。
- [x] 80/120/160 columns、CJK、长路径、mono、ANSI16/256/TrueColor 不溢出。
- [x] Unicode 与 ASCII fallback 均可读，状态不依赖颜色或单一符号。
- [x] 六个标准场景有确定性 fixture 和测试。
- [x] 普通 status、`status --json`、非 TTY/CI reporter、退出码、RPC 和 Backend 合同不回归。
- [x] `npm --prefix wf run typecheck` 通过。
- [x] `npm --prefix wf test` 全部通过。
- [x] `npm --prefix wf run build` 通过。
- [x] `git diff --check` 通过。
- [x] 若没有 Go/RPC 变化，明确记录无需 Go 测试。
- [x] 尽可能在真实 Windows Terminal/PowerShell PTY 中人工检查；环境不支持时必须记录限制，并提供可审阅的 canonical text gallery/fixture 输出。
- [x] 更新 README、`wf/README.md` 和 M3 文档，提交并 push `origin/main`，最终工作树干净。

## 10. 实施顺序

1. 建立六个 canonical visual fixtures 与 compact status/divider/theme tokens；
2. 重构纯 presentation model，产出 Workflow rows、Focus Detail、Status Strip、Context Footer；
3. 重构 Ink components，减少 Panel，建立单焦点布局；
4. 将视觉选择扩展到全部节点，同时复用 M3.2 action identity binding；
5. 完成 narrow/medium/wide、Unicode/ASCII、mono/color 适配；
6. 补齐 deterministic tests 和可审阅 gallery；
7. 更新文档，执行门禁，审查 diff，提交并推送。

## 11. 实现与验证记录

- 纯 presentation model 已输出 Header、紧凑 Workflow rows、单一 Focus/Action Detail、Status Strip 和 Context Footer；Ink 层只负责语义颜色与层级映射。
- 视觉选择覆盖全部 Workflow 节点；action 资格仍来自 Engine 当前状态，输入/确认继续按 `nodeId/kind/duplicateRisk` 固定。`live-console.tsx` 未修改，M3.2 controller ownership/detach 语义保持原样。
- 六个 canonical `RunStatusView` fixtures 覆盖并发运行、等待审批、失败可重试、indeterminate duplicate-risk、cancelling 和 terminal；80/120/160 文本 gallery 位于 `fishyume-m3.3-canonical-gallery.txt`。
- 自动化门禁：TypeScript typecheck 通过；42/42 tests 通过；build 通过；`git diff --check` 通过。没有新增 dependency，`package-lock.json` 无变化。
- 未修改 Go Engine、RPC protocol、Bridge types、持久化 schema 或 Backend 契约，因此无需扩大到 Go 测试。
- 当前执行环境的 PowerShell 输出被重定向，未安装 `winpty`/`node-pty`，WSL 有 `script` 但没有 Node.js；因此未伪造真实 Windows Terminal/PTY 通过。补充证据为 80-column Ink render smoke，以及确定性的 80/120/160 canonical gallery。
