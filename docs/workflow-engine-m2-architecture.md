# Workflow Engine (`wf`) — M2 Architecture v0.1

> 历史基线：本文保留 M2 DAG 与状态机设计记录。Backend 产品语义和 CC-Panes 执行链已被 M4 正式决策取代，当前目标架构见 [`fishyume-m4-agent-native-control-plane.md`](./fishyume-m4-agent-native-control-plane.md)。

> 状态：已确认核心方向，进入实现
> 日期：2026-08-04
> 前置里程碑：M1（单 Agent 垂直链路）已完成

## 1. 目标

M2 在 M1 的真实完成协议和持久化能力上增加显式多节点 DAG，使用户可以用 YAML 或 JSON 描述一个可恢复的工作流。M2 的重点不是动态规划，而是让预先定义的节点、依赖、审批、上下文传递和恢复语义稳定可靠。

M2.1 采用单节点并发度：调度器理解完整 DAG，但同一 Run 每次最多启动一个 Agent。Schema 从第一天保留 `maxConcurrency`，M2.1 只接受值 `1`；无依赖节点并行执行后置到 M2.2。

## 2. 延续的架构边界

- Go 仍是唯一调度器、状态所有者、Workflow 解析与验证者。
- TypeScript 仍只负责 CLI、Ink 展示和 JSON-RPC 客户端。
- Go 与 TypeScript 使用 stdio 上的 NDJSON JSON-RPC。
- M2 继续使用 CC-Panes Backend 和 `cc-panes-ctl --json`。
- TaskBinding 终态仍是 Agent 结果的正式证据。
- 不引入常驻 daemon。每个 `wf run`、`wf resume`、`wf cancel` 命令启动一个临时 Engine 进程。
- M1 的 `wf doctor` 和单任务 `wf run "<task>"` 保持可用；单任务在 Engine 内部规范化为一个 Agent 节点的 Workflow。
- 不启用 `agent-team-workflow`。

## 3. M2.1 范围

### 包含

- Workflow YAML/JSON 模板和版本化 Schema。
- `agent`、`approval` 两种节点。
- `dependsOn` 显式依赖、DAG 校验和确定性拓扑调度。
- 结构化 `when` 条件。
- 节点级 `requiredSkills`。
- 显式输入和上游结构化结果引用。
- `wf status`、`wf resume`、`wf cancel`。
- 人工批准、拒绝和显式节点重试。
- Run/Node/Attempt 持久化、崩溃恢复和 Backend 对账。
- Workflow 级跨进程控制租约。
- 对 M1 历史快照的只读状态展示。

### 不包含

- Planner Agent 或自动 DAG 生成。
- 无依赖节点并行执行。
- 自动重试、模型 fallback、精确 Token 硬限制。
- 任意脚本表达式、JavaScript、Shell 条件。
- CommandNode、SummarizerNode、动态节点创建。
- SQLite、远程服务、Web/桌面 GUI。
- `agent-team-workflow` 集成。

## 4. Workflow Schema

YAML 是主要作者体验，JSON 使用相同数据结构。Go Engine 负责解析、规范化、校验，并在 Run 创建时保存不可变的规范化副本。

```yaml
apiVersion: wf/v1
name: implement-and-review

inputs:
  goal:
    required: true

defaults:
  tool: codex
  runtime: local

execution:
  maxConcurrency: 1

nodes:
  plan:
    type: agent
    task: |
      分析任务并制定实现方案：
      {{ inputs.goal }}
    requiredSkills: []

  approve:
    type: approval
    dependsOn: [plan]
    prompt: |
      是否批准以下方案？
      {{ nodes.plan.result.summary }}

  implement:
    type: agent
    dependsOn: [approve]
    when:
      node: approve
      field: result.decision
      equals: approved
    task: |
      按已批准方案实现：
      {{ nodes.plan.result.summary }}
    requiredSkills: []

  verify:
    type: agent
    dependsOn: [implement]
    task: |
      检查实现并运行测试。
      摘要：{{ nodes.implement.result.summary }}
      产物：{{ nodes.implement.result.artifacts }}
```

### 4.1 输入

- M2.1 输入值只允许 JSON 标量：string、number、boolean。
- 输入可声明 `required` 和 `default`。
- CLI 使用重复的 `--input key=value` 提供值；JSON 标量可通过 `--inputs <file.json>` 提供。
- 未声明输入、缺少必需输入或类型不匹配都在创建 Run 前失败，不产生半初始化 Run。

### 4.2 节点 ID 与依赖

- Node ID 必须匹配 `[A-Za-z][A-Za-z0-9_-]{0,63}`。
- `dependsOn` 不从文件排列顺序推断。
- 所有依赖必须存在，图必须无环，至少包含一个节点。
- 规范化时使用稳定的节点 ID 排序作为拓扑并列项的 tie-breaker，保证同一模板的执行顺序可复现。

### 4.3 节点类型

`agent` 节点字段：

- `task`：必需，受限模板字符串。
- `dependsOn`：可选。
- `when`：可选。
- `tool`、`runtime`：可选，覆盖 Workflow 默认值。
- `requiredSkills`：可选字符串数组；Engine 将其作为明确要求注入 Agent prompt，但不负责安装 Skill。

`approval` 节点字段：

- `prompt`：必需，受限模板字符串。
- `dependsOn`：可选。
- `when`：可选。

审批节点完成后写入：

```json
{
  "decision": "approved",
  "reason": "optional human reason"
}
```

`decision` 只允许 `approved` 或 `rejected`。

### 4.4 条件

M2.1 不执行任意表达式。条件是可校验的数据结构，支持：

- 单个比较：`node`、`field`、`equals`。
- `all`：所有子条件成立。
- `any`：至少一个子条件成立。
- `not`：对子条件取反。

字段引用只允许当前节点的祖先节点，并只允许公开的结构化结果字段。条件为 false 时节点进入 `skipped`，原因是 `condition_false`。

### 4.5 模板引用

模板只识别简单路径占位符，不使用 Go `text/template`，也不支持函数、循环、条件代码或执行能力：

```text
{{ inputs.goal }}
{{ nodes.plan.result.summary }}
{{ nodes.plan.result.artifacts }}
```

- 节点只能引用自己的祖先节点。
- Artifact 只作为路径/标识符列表传递，不自动读取文件内容。
- 不允许引用完整终端输出、会话历史、Backend metadata 或凭据。
- 缺失引用在节点启动前成为可操作的验证错误，不渲染为空字符串。

## 5. 生命周期与结论

M1 的扁平状态把“等待”和“最终结果”混在一起。M2 将生命周期 `phase`、最终结论 `conclusion` 和机器可读原因 `reason` 分离。

### 5.1 Run Phase

```text
created
running
waiting
paused
cancelling
completed
```

### 5.2 Node Phase

```text
pending
ready
running
waiting
completed
skipped
```

### 5.3 Conclusion

只有完成或被跳过的对象拥有结论：

```text
succeeded
failed
cancelled
rejected
indeterminate
```

`skipped` 节点不伪造成功，使用 `reason` 解释 `condition_false`、`upstream_failed` 或 `workflow_cancelled`。

常见等待原因：

```text
approval_required
agent_waiting_input
completion_missing
invalid_result
cancel_failed
```

### 5.4 结果归类

- `failed`：Backend 或 TaskBinding 给出明确失败结果。
- `indeterminate`：有明确异常证据，但无法判断任务是否已产生副作用或是否完成。
- 没有检测信号时保持 `running` 或恢复中的状态，不能因静默超时推断 `indeterminate`。
- `idle` 且缺少终态 Binding 时先进入短暂对账；持续缺失则进入 `waiting: completion_missing`。
- `exited`、session lost/not found 且没有正式结果时可进入 `indeterminate`。
- Binding 声称完成但缺少必需结构化结果时进入 `waiting: invalid_result`，允许修复结果而不是自动重跑。

## 6. 调度语义

1. 创建 Run 前完整校验并规范化 Workflow。
2. 找出依赖已满足且条件为 true 的 `ready` 节点。
3. M2.1 按确定性拓扑顺序选择一个节点。
4. `approval` 节点使 Run 进入 `waiting: approval_required`。
5. `agent` 节点创建新的 Attempt 和 CC-Panes TaskBinding，然后启动会话。
6. Agent 结果持久化后再计算下游节点。
7. 任一 Agent 明确失败时默认 fail-fast：Run 完成为 `failed`，未启动下游节点跳过为 `upstream_failed`。
8. M2.1 不支持 `continueOnFailure`；字段保留到后续里程碑再设计。
9. 所有可执行节点完成或跳过后，Engine 计算 Run 结论。

审批拒绝规则：

- 如果存在条件匹配 `decision=rejected` 的后续分支，继续执行该分支。
- 如果没有任何拒绝分支可执行，Run 完成为 `rejected`。
- 审批拒绝不是用户取消。

## 7. 用户控制面

```powershell
wf run --project E:\project "单 Agent 临时任务"
wf run --workflow workflow.yaml --project E:\project --input goal="实现功能"
wf status <run-id>
wf resume <run-id>
wf resume <run-id> --approve <node-id>
wf resume <run-id> --reject <node-id> --reason "需要调整方案"
wf resume <run-id> --retry <node-id>
wf cancel <run-id>
```

- `wf status` 默认只读取持久化状态，不接管调度器。
- `wf resume` 获取 Run 控制租约、执行恢复对账并继续调度。
- `--approve/--reject` 解决当前审批节点后继续调度。
- `--retry` 只能用于允许人工恢复的节点状态，创建新 Attempt；旧 Attempt 永不覆盖。
- 对 `indeterminate` 执行重试前必须提示可能重复产生副作用。
- `wf cancel` 是跨 Shell 可用的 CLI 子命令，不是 Agent `/` 命令。

Ctrl+C 仍表示 detach：CLI 和临时 Engine 退出，不要求杀死 Agent。Run 记录为 `paused: controller_detached`，当前 Agent 会话可继续运行；后续节点直到 `wf resume` 重新接管后才会调度。

## 8. Workflow 级取消

取消是 Workflow 层意图，用户不需要知道当前 Node Agent 的 session ID。

1. Engine 获取 Run 控制租约。
2. 原子持久化 `cancelRequested=true`，Run 进入 `cancelling`。
3. 如果有活动 Agent Attempt，调用 Backend Cancel。
4. 只有 Backend 确认停止后，活动节点才完成为 `cancelled`。
5. 尚未启动的节点进入 `skipped: workflow_cancelled`。
6. Run 完成为 `cancelled: user_requested`。

如果 Backend Cancel 失败，不得记录虚假 `cancelled`；取消意图保持持久化，状态为 `waiting/cancelling` 并允许再次执行 `wf cancel`。

## 9. Context Summary Contract

每个成功 Agent Attempt 必须产生规范化结果：

```json
{
  "summary": "可直接交给下游节点的简洁摘要",
  "artifacts": ["path/to/file"],
  "warnings": [],
  "checks": ["go test ./..."],
  "usage": {
    "inputTokensEstimated": 0,
    "outputTokensEstimated": 0
  }
}
```

- `summary` 非空，是下游上下文的正式入口。
- Engine 不把完整 Agent 输出自动拼入下游 prompt。
- Engine 验证结果类型、UTF-8、字段数量和总体大小。
- M2.1 使用明确常量限制单个 summary 和最终渲染 prompt；超限进入 `waiting: invalid_result` 或在节点启动前报错，不静默截断。
- `requiredSkills`、用户 task、已显式引用的输入与上游结果共同组成 Agent prompt。
- Engine 在每个 Attempt 保存最终渲染 prompt 的 SHA-256，不默认持久化包含潜在敏感输入的完整 prompt；Workflow 规范化副本和非敏感输入仍按 Run 保存。

## 10. 持久化布局

```text
<state-dir>/runs/<run-id>/
├── run.json
├── workflow.json
├── events.jsonl
├── control.lock
└── nodes/
    └── <node-id>/
        ├── node.json
        └── attempts/
            └── 1/
                ├── attempt.json
                ├── result.json
                └── output.log
```

- `workflow.json` 是创建 Run 时的规范化不可变副本。
- `run.json` 包含 Phase、Conclusion、Reason、调度游标、取消意图和节点摘要。
- `node.json` 包含 Node Phase、Conclusion、Reason、审批结果和当前 Attempt 序号。
- `attempt.json` 包含 Backend session metadata、TaskBinding ID、启动时间和 prompt hash。
- snapshot 使用临时文件加原子替换；event 继续只追加。
- M1 `nodes/agent-1/output.log` 快照可只读展示，但不能作为 M2 DAG 恢复数据。

## 11. 控制租约

因为没有 daemon，必须防止两个 CLI 进程同时推进同一个 Run。

- `wf run`、`wf resume`、`wf cancel` 对目标 Run 获取原子控制租约。
- `wf status` 不获取写租约。
- 租约记录随机 owner ID、PID、命令、创建时间、heartbeat 和到期时间。
- 持有者定期刷新 heartbeat。
- 未过期租约不能被抢占；CLI 显示当前控制命令和可操作提示。
- 进程崩溃后，过期租约可由后续 `resume/cancel` 安全接管。
- 接管租约不等于重启 Agent，必须先进行 Backend 对账。

## 12. 恢复与对账

`wf resume` 的顺序固定：

1. 读取并校验 Run、Workflow、Node、Attempt snapshot。
2. 获取控制租约。
3. 重放/检查事件序号，拒绝明显损坏的状态。
4. 对当前活动 Attempt 查询 Backend 和 TaskBinding。
5. TaskBinding 已终态：消费正式结果，不启动新 Agent。
6. 会话仍活动：恢复等待和输出采集。
7. `waitingInput`：Run 进入 `waiting: agent_waiting_input`。
8. `idle` 且 Binding 非终态：执行有限结果对账，之后进入 `waiting: completion_missing`。
9. `exited/lost` 且无正式结果：节点完成为 `indeterminate`。
10. 没有活动 Attempt：从持久化 DAG 重新计算下一个 ready 节点。

恢复过程必须幂等。重复 `wf resume` 不得重复消费同一个 Binding、重复启动已存在的 Attempt 或改变已完成结果。

## 13. JSON-RPC

M2 增加或扩展：

```text
run.startWorkflow
run.status
run.resume
run.cancel
```

`run.resume` 参数可携带单个显式动作：`approve`、`reject` 或 `retry`。一个请求不能同时执行多个动作。

M2 快照模型与 M1 不兼容，Engine/CLI 协议升级为 `protocolVersion: 2`。M2 CLI 可以识别 M1 持久化快照并以 legacy 只读形式展示；不承诺用 M2 Engine 恢复 M1 Run。

## 14. 验收证据

M2.1 必须证明：

1. YAML 和等价 JSON 规范化为相同 DAG。
2. 环、未知依赖、非法引用、缺失输入在执行前失败。
3. 三个 Agent/Approval 节点按确定顺序执行，且任何时刻最多一个 Agent 活动。
4. 审批后可以在新进程中 `resume`。
5. 电脑/Engine 异常退出后，`resume` 不重复启动已存在的 Agent。
6. 条件 false 节点正确跳过；拒绝分支或 Run `rejected` 语义正确。
7. 明确失败 fail-fast；等待与 indeterminate 不被误判为失败。
8. `wf cancel` 只在 Backend 确认后完成为 cancelled，失败可重试。
9. 两个并发控制命令不能同时拥有 Run。
10. 下游 prompt 只包含显式引用的结构化上下文，不含完整终端历史。
11. Go 测试、vet、build 和 TypeScript typecheck、test、build 全部通过。
12. 使用 fake Backend 完成恢复、审批、拒绝、取消和 crash-reconcile 集成测试。

## 15. 后续里程碑

### M2.2

- 支持 `maxConcurrency > 1`。
- 无依赖 ready 节点并行执行。
- 并发取消、聚合状态和资源上限。

### M3+

- 自动重试、超时、fallback 和预算策略。
- Summarizer/repair 节点。
- 动态 Planner Agent。
- 其他 Backend 和更完整 TUI。
