# Fishyume M3 TUI 第一批产品化

> 状态：已完成并通过验收（2026-08-06）
>
> 产品决策：CLI/TUI 是正式产品表面；本批不建设 Web/Desktop，也不包含模型路由、Prompt 优化、自动重试、超时或 fallback

## 1. 本批目标与边界

本批把已有 Ink `run` 界面从生命周期调试输出提升为可持续使用的 Coding Agent 终端表面。M3.3 进一步将其收敛为 Calm Operator Console：Workflow 是主体，单一焦点节点承担详情，状态与快捷键保持克制和低噪声。实现继续位于 TypeScript CLI 层，不改变 Go Engine 对 Run、Node、Attempt、Approval 和持久化状态的所有权。

包含：

- 一套统一的终端 Design Tokens：完整 Header 状态、紧凑 Workflow 状态符号/短标签、颜色角色、Divider、Unicode/ASCII fallback；
- Header、Workflow、Focus Detail、Status Strip、Context Footer 五级信息层级；
- 并行 Attempt、等待 Approval、诊断与 Node result 归并到 Workflow 行和当前焦点详情；
- 80/120/160 columns 三档布局、Unicode 显示宽度和有界截断；
- TrueColor、256 色、16 色和无颜色模式；
- 稳定 streaming：生命周期事件先即时更新，随后按事件顺序刷新完整 `RunStatusView`，计时器从 250ms 降为 1s；
- 纯文本 reporter、`status --json`、退出码和 bridge/RPC 合同的兼容保护。

不包含：

- 交互式 Workflow 编辑器、Prompt Library、模型选择/路由界面；
- 自动重试、超时、模型 fallback 或预算策略；
- Web、Desktop 或新的 GUI 技术栈；
- Go Engine 状态模型或 JSON-RPC 字段变更。

## 2. 架构

```text
run command
  ├─ non-TTY / CI ──> TextReporter（逐行稳定契约）
  └─ TTY ───────────> RunApp（Ink）
                         ├─ design-tokens.ts（状态与颜色语义）
                         ├─ layout.ts（宽度、截断、终端档位）
                         ├─ presentation.ts（纯展示模型，可文本测试）
                         └─ components.tsx（可复用 Ink 组件）
```

`RunApp` 消费完整 `RunStatusView`，而不是只消费 `WorkflowSnapshot`。事件到达时界面立即更新 Run/Node phase；随后串行请求 `run.status`，补齐 active Attempts、waiting Approvals 和 diagnostics。串行刷新避免响应乱序覆盖较新的状态，最终停止前还会等待刷新链并读取一次权威状态。

M3.2 在这条边界上增加了共享 `live-console.tsx` controller 与纯 `interaction.ts` 状态机。`run` 继续消费 `run.event`，`status --watch` 以 1 秒有界轮询重新接入；刷新 generation 会使较早响应失效，动作完成后无论成功或失败都重新读取完整 `RunStatusView`。React 只保存全部 Workflow 节点中的视觉选择、详情折叠、固定 action target、帮助、输入/确认、pending 与临时 action message，不保存 Run/Node/diagnostic 业务副本。视觉选择可遍历全部节点，动作资格仍来自最新 actionable 列表；确认提交继续按固定 `nodeId/kind/duplicateRisk` 重新解析。Watch 成功 resume 后在 bridge 关闭前条件 detach，纯观察与终态不会隐式 detach。

纯文本与 JSON 路径不经过 Ink：

- 非 TTY 或 `CI`：继续使用 `TextReporter`；
- `fishyume status --json`：仍然只输出单个 JSON 对象；
- `NO_COLOR`：TTY 仍使用布局完整的单色 TUI，状态依靠符号和文本表达；
- `TERM=dumb`、无颜色深度：颜色模式降为 mono，不影响结构。

## 3. Design Tokens 与组件边界

### 3.1 状态视觉语义

Header 保留完整大写状态；Workflow 行使用固定的符号与短标签列，既保持 streaming 稳定，也减少重复 Badge 宽度。颜色只用于加强扫描，不承载唯一信息。

| 产品状态 | 符号/标签 | 非颜色含义 | 颜色角色 |
| --- | --- | --- | --- |
| running | `● run` / `RUNNING` | 前进/活动 | running/cyan |
| waiting | `◌ wait` / `WAITING` | 等待中 | waiting/yellow |
| approval | `◆ approve` / `APPROVAL` | 明确需要人工决策 | approval/magenta |
| failed | `! fail` / `FAILED` | 明确失败 | danger/red |
| indeterminate | `? unknown` / `INDETERMINATE` | 结果不可信或不完整 | danger/red |
| cancelled | `× cancel` / `CANCELLED` | 已停止 | neutral |
| succeeded | `✓ done` / `SUCCEEDED` | 已成功 | success/green |
| skipped | `· skip` / `SKIPPED` | 未执行且有原因 | muted |
| rejected | `× reject` / `REJECTED` | 人工拒绝 | danger/red |
| cancelling | `◍ stop` / `CANCELLING` | 正在停止且尚未确认 | waiting |
| paused | `Ⅱ pause` / `PAUSED` | 控制器暂停 | waiting |
| ready | `◇ ready` / `READY` | 可被调度 | strong |
| pending | `○ pending` / `PENDING` | 尚未就绪 | muted |

### 3.2 组件职责

| 组件 | 职责 | 稳定性约束 |
| --- | --- | --- |
| `Header` | 品牌、Workflow、完整 Run 状态、elapsed、Run/Backend、capacity、settled、可选 state | 窄屏拆为三行；终态 elapsed 固定在最终更新时间 |
| `WorkflowRow` | 选择标记、紧凑状态、Node、Attempt/Backend、Reason/Diagnostic | 所有节点可遍历；Node ID 优先于尾部元数据 |
| `Focus Detail` | 当前 Node/Attempt/Approval/result/diagnostic 或固定 Action Detail | 单一主焦点；Enter 本地折叠，action mode 优先 |
| `Status Strip` | active/waiting/failed/skipped/capacity | 只展示非零分类 |
| `Context Footer` | 当前真正有效的选择、详情、action、detach 或终态命令 | 80 columns 可换行；不展示无效快捷键 |

## 4. 终端兼容目标

| 宽度 | 档位 | 布局策略 | 验收状态 |
| --- | --- | --- | --- |
| 80 columns | narrow | 三行 Header、紧凑 Workflow、完整 Focus Detail、Footer 自动换行 | 自动测试通过 |
| 120 columns | medium | 两行 Header、稳定 Node ID 列、单行 Context Footer | 自动测试通过 |
| 160 columns | wide | 补充 state path、完整 execution identity 与长诊断 | 自动测试通过 |

最低渲染宽度为 40 columns；80 columns 是正式支持的最窄验收目标。布局工具按 Unicode display width 处理 CJK、emoji 和 combining marks，截断统一使用单字符省略号，不输出超出目标宽度的文本行。

颜色兼容目标：

- TrueColor：使用品牌化 RGB palette；
- 256/16 色：降级到 ANSI 命名色；
- `NO_COLOR`、`NODE_DISABLE_COLORS`、`TERM=dumb` 或深度不足：mono；
- 任一颜色档位都使用相同符号、标签、层级和命令提示。
- `TERM=dumb` 或 `FISHYUME_ASCII=1`：使用 ASCII 状态、选择标记、Separator 和 Divider，业务文字不变。

六个 canonical 场景由 `wf/src/tui/fixtures.ts` 纯构造，并通过 `npm --prefix wf run gallery` 生成 [`fishyume-m3.3-canonical-gallery.txt`](./fishyume-m3.3-canonical-gallery.txt)。Gallery 不包含 ANSI 转义；颜色能力由独立语义测试覆盖。

## 5. 状态与产品表面矩阵

| 表面 | TTY | 非 TTY / CI | `NO_COLOR` | JSON 契约 |
| --- | --- | --- | --- | --- |
| `fishyume run` | Ink TUI | `TextReporter` | mono Ink TUI | 不适用 |
| `fishyume status` | 稳定文本状态报告 | 同左 | 无 ANSI | `--json` 单对象不变 |
| `fishyume status --watch` | 交互式 Run Console | 明确拒绝并建议普通 `status` | mono Run Console | 与 `--json` 互斥 |
| `resume` / `cancel` | 稳定事件与状态文本 | 同左 | 无 ANSI | 无变化 |
| `doctor` | 稳定 `ok/fail` 文本 | 同左 | 无 ANSI | 无变化 |

Bridge types 与 RPC 合同本批无变化，因此无需 Go 合同迁移或新增 Go 测试门禁。

## 6. 验收标准

- [x] Design Tokens 集中定义颜色角色、状态符号/标签、边框、间距和强调层级。
- [x] 建立 Header、Workflow、Focus Detail、Status Strip、Context Footer 层级，移除重复 Panel。
- [x] running/waiting/approval/failed/indeterminate/cancelled/succeeded/skipped 不依赖颜色即可区分。
- [x] 活动并行 Attempts、等待 Approvals、diagnostics、Node result、summary 和 capacity 在节点/焦点中可见。
- [x] 视觉选择遍历全部节点；action identity 与 ownership/detach 安全语义不回退。
- [x] 六个 canonical 场景覆盖 80/120/160、Unicode/ASCII、mono 与颜色能力档位。
- [x] 80/120/160 columns 文本渲染均不溢出。
- [x] 无颜色能力降级到 mono；`NO_COLOR` 不损失 TUI 结构。
- [x] 非 TTY/CI 保持逐行 text reporter；`status --json` 保持单 JSON 对象。
- [x] 关键状态与布局有文本渲染测试；现有 CLI、bridge、integration 测试不回退。
- [x] `npm --prefix wf run typecheck`、`npm --prefix wf test`（42/42）、`npm --prefix wf run build` 最终门禁全部通过。
- [x] 以清晰提交推送 `origin/main`。

## 7. 实现文件与维护规则

- `wf/src/tui/design-tokens.ts`：只放跨组件视觉语义和终端颜色能力映射；
- `wf/src/tui/layout.ts`：只放终端宽度与字符串布局原语；
- `wf/src/tui/presentation.ts`：保持无 React/Ink 依赖，以便快速、确定性文本测试；
- `wf/src/tui/components.tsx`：只把纯 presentation model 映射为 Ink 颜色与层级；
- `wf/src/tui/run-app.tsx`：只负责把 `RunStatusView` 编排成产品界面；
- `wf/src/tui/interaction.ts`：只放 actionable/retry 派生、全节点视觉选择与 action identity reducer；
- `wf/src/tui/fixtures.ts`：六个确定性 `RunStatusView` 视觉场景；
- `wf/src/tui/live-console.tsx`：共享 refresh/poll/action/generation/cleanup 与 Ink session 生命周期；
- `wf/src/commands/run.tsx`：只负责 TTY 选择、Run 创建和纯文本 reporter；
- `wf/src/commands/status.ts`：保护普通/JSON 合同，并只在合法 TTY 中接入 watch Console。

后续批次若增加交互，必须继续保证 reporter/JSON 路径与 TUI 分离，且不能把 Engine 业务状态转移到 React 本地状态中。
