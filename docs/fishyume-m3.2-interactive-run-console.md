# Fishyume M3.2：交互式 Run Console

> 状态：已实现并通过自动化验收（2026-08-06）
>
> 产品边界：CLI/TUI 是正式产品表面；本批继续保持 Engine 为唯一业务状态所有者，不建设 Web/Desktop，不实现模型路由、Prompt Library、自动重试、超时或 fallback。

## 1. 目标

把 M3 第一批的只读 Run TUI 提升为可日常操作的键盘优先 Run Console。用户应能在同一终端表面观察运行、处理人工审批、显式重试异常节点、取消 Run，并在退出后可靠恢复观察。

本批的核心产品路径：

1. `fishyume run ...` 启动后进入交互式 Run Console；
2. `fishyume status <run-id> --watch` 可以重新进入同一个 Run 的交互式观察与操作界面；
3. 等待审批或可重试节点出现时，用户通过键盘选择目标并执行动作；
4. 所有动作提交给现有 Engine RPC，随后重新读取完整 `RunStatusView`；
5. TUI 退出不伪造、不缓存业务结论，重新进入后以持久化状态恢复。

## 2. 范围

### 2.1 Watch / Reattach

- 为 `fishyume status <run-id>` 增加显式 `--watch`。
- `--watch` 仅在交互式 TTY 中启用 Ink Run Console；普通 `status` 行为不变。
- `--watch --json` 必须被拒绝并给出明确参数错误；JSON 永远只输出单个对象。
- 非 TTY 或 CI 使用 `--watch` 时不得悄悄进入无限输出；返回可操作的诊断并建议使用普通 `status`。
- Watch 以有界频率读取 `run.status`，活动状态默认 1 秒刷新；终态停止轮询但保留最终视图，直到用户退出。
- Run 启动路径继续消费 `run.event`，并按已有串行刷新机制补齐完整状态。

### 2.2 键盘导航

- `j` / `k` 与上下方向键：在当前可操作节点之间移动选择。
- 可操作节点包括：等待审批节点，以及 Engine 已允许人工 retry 的 waiting/failed/indeterminate 节点。
- 选中项必须在无颜色模式下仍清晰可辨，不能只依赖背景色或颜色。
- 当状态刷新导致节点消失时，选择自动落到最近的合法项；不得保留指向旧业务对象的本地副本。
- `?`：展开/收起快捷键帮助；`Esc`：退出当前输入或确认模式。

### 2.3 动作

- `a`：批准当前选中的 waiting approval；批准属于明确动作，但不需要二次确认。
- `r`：进入拒绝原因输入模式；Enter 提交 reject，Esc 放弃。空原因允许提交，但界面应清楚显示将执行 reject。
- `R`：重试当前可重试节点。
  - failed/waiting retry 需要一次确认；
  - indeterminate retry 必须额外展示 duplicate-risk 警告，并仅在明确确认后携带 `acknowledgeDuplicateRisk: true`。
- `c`：进入取消 Run 确认模式；明确确认后调用 `run.cancel`。
- `d` / `q` / `Ctrl+C`：安全离开 Console。
  - 从 `fishyume run` 离开时沿用 `run.detach`，Workflow 继续由 Engine 管理；
  - 从 `status --watch` 离开时只退出观察，不把 Run 标记为取消。
- 动作执行中锁定重复提交；成功或失败都要重新读取 `run.status`。
- RPC 错误显示在 TUI 的临时 action message 区域，但不得写回或覆盖 Engine diagnostic。

### 2.4 Console 架构

- 提取可复用的 live console/session controller，供 `run` 与 `status --watch` 共用状态刷新和动作提交逻辑。
- `RunApp` 通过 props/callback 接收动作能力；React 本地状态只允许保存：选择索引、帮助展开、确认态、拒绝原因输入、动作 pending/message。
- Run、Node、Attempt、Approval、diagnostic、结论、重试资格的最终判断均来自 Engine 状态或与 Engine 规则一致的纯派生函数。
- presentation 层继续无 React/Ink 依赖；交互状态机优先做成纯函数，以便确定性测试。
- 不新增 JSON-RPC 方法；复用 `run.status`、`run.resume`、`run.cancel`、`run.detach`。

## 3. 非目标

- Workflow 编辑器、节点拖拽、Run 历史浏览器；
- 多 Run Dashboard；
- Shell/HTTP/容器节点；
- 自动 retry、timeout、fallback、预算和模型路由；
- Prompt Library 或 Prompt 优化；
- Web/Desktop；
- 修改 Engine 持久化 schema、Backend 契约或 RPC protocol。

## 4. 兼容性约束

- 普通 `fishyume status <run-id>` 文本输出和退出码不变。
- `fishyume status <run-id> --json` 仍输出且只输出一个 JSON 对象。
- 非 TTY/CI 的 `run`、`resume`、`cancel`、`status` 继续使用稳定纯文本合同。
- `NO_COLOR` 保留完整交互结构，只关闭颜色。
- `fishyume` 与兼容别名 `wf` 的命令面一致。
- 不引入需要原生构建的新依赖；优先使用现有 Ink/React 能力和纯函数测试。

## 5. 关键风险与处理

### 5.1 重复动作

动作提交期间设置本地 pending 锁；每次确认只允许产生一个 RPC。Engine 的幂等/冲突校验仍是最终防线。

### 5.2 状态竞争

事件、轮询与动作完成后的刷新必须串行化或带 generation 判断，旧响应不得覆盖新状态。终态视图不得被较早的 running 响应回退。

### 5.3 危险操作误触

reject、retry、cancel 必须进入显式模式；indeterminate retry 必须展示 duplicate-risk 并要求再次确认。快捷键帮助需始终可发现。

### 5.4 终端恢复

退出、异常和 SIGINT 路径必须清理计时器、事件订阅与 Ink 实例，并关闭 Engine bridge；不得留下挂起 Promise 或阻止 Node 进程退出。

## 6. 验收标准

- [x] `status --watch` 可在 TTY 中打开已有 Run Console，并在终态停止刷新。
- [x] `--watch --json` 与非 TTY `--watch` 有明确、稳定的错误行为。
- [x] `j/k`、方向键、`?`、Esc 的交互状态转移有纯函数测试。
- [x] approve、reject reason、retry、indeterminate retry 风险确认、cancel 的 RPC 参数有测试。
- [x] 每次确认至多产生一个 mutation RPC；pending 时重复按键不会重复提交。
- [x] Run 启动与 Watch 复用同一动作/刷新边界，不复制两套业务状态机。
- [x] `d/q/Ctrl+C` 不会误取消 Run；启动路径 detach，Watch 路径仅退出观察。
- [x] action message 与 Engine diagnostics 分离，刷新后 Engine 信息保持权威。
- [x] 80/120/160 columns 与 mono 模式下选择、输入、确认和帮助区域均有界可读。
- [x] 普通 status、`status --json`、非 TTY/CI reporter、退出码和 bridge/RPC 合同不回归。
- [x] `npm --prefix wf run typecheck`、`npm --prefix wf test`、`npm --prefix wf run build` 全部通过。
- [x] 如未修改 Go/RPC 合同，记录无需 Go 测试；如实际修改则补跑相关 Go 测试。
- [x] 更新 README、`wf/README.md` 与 M3 文档，提交并推送 `origin/main`，工作树干净。

### 6.1 实现与验证记录

- 共享 controller 位于 `wf/src/tui/live-console.tsx`，统一负责串行 refresh、watch poll、mutation lock、generation 判定、event subscription、detach 与 cleanup。
- 纯 actionable/retry 派生与交互 reducer 位于 `wf/src/tui/interaction.ts`；React 只保存选择、帮助、输入/确认、pending 和临时 action message。
- 自动化验证覆盖 35 项测试，包括普通命令兼容、RPC 参数、重复提交、旧响应丢弃、终态停轮询、timer/subscription cleanup、80/120/160 columns 与 mono-safe 文本。
- 未修改 Go、RPC 方法、RPC 参数合同或持久化 schema，因此按门禁未扩大范围运行 Go 测试。
- 本机没有可用 Windows PTY harness（未安装 `winpty`/`node-pty`），WSL 有 `script` 但没有 Node.js；因此未伪造 TTY/PTY smoke 通过。已执行构建后 CLI 非 TTY smoke，确认 `--watch --json` 与非 TTY `--watch` 均以退出码 6 给出预期诊断。

## 7. 实施顺序

1. 提取 retry/actionable 节点的纯派生函数与交互 reducer；
2. 建立共享 live console controller，处理串行 refresh、poll、action 和 cleanup；
3. 扩展 `RunApp` 的选择、输入、确认、message 与快捷键区域；
4. 接入 `run`，保持现有事件驱动和 detach 语义；
5. 接入 `status --watch`，保护 JSON 与非 TTY 合同；
6. 补齐纯状态机、命令、渲染宽度、无颜色和生命周期测试；
7. 更新文档，完成 typecheck/test/build/diff check，提交并推送。
