# Fishyume M4：Agent-Native Control Plane & Headless Runtime v1

> 状态：架构已批准；M4.0 + M4.1 已实施，M4.2 + M4.3 待实施
>
> 日期：2026-08-06
>
> 基线：M3.3 Calm Operator Console 已完成；M2 的状态机、持久化、恢复、并发、取消与结果契约继续作为不可破坏的基础。

## 1. 决策摘要

Fishyume 是供 Codex、Claude、Kimi、OpenCode 等 Host Agent 操作的本地 AI Agent 编排控制面，不是通用工作流编辑器、聊天 Coding Agent 或 AutoGen 式进程内多 Agent Framework。

M4 完成以下迁移：

- Host Agent 通过 MCP 或 Machine CLI 创建、验证、解释、运行和控制 Workflow。
- 人类通过 TUI 观察 DAG，并执行审批、拒绝、重试、取消和恢复。
- Node Agent 使用成熟 Agent Harness 的无头、非交互、一次性外部进程运行。
- Fishyume Core 不实现模型 Tool loop，不直接与用户对话，也不负责厂商 Agent 内部推理。
- 当前公开 `Backend` 语义迁移为 `Agent Driver` 与 `Execution Target`。
- 引入本地、用户级常驻 Control Plane；Windows 使用 Named Pipe，Linux/macOS 使用 Unix Domain Socket，默认不开放 TCP。
- MCP、Machine CLI 与 TUI 连接同一 Control Plane，共享同一 Run 状态和动作入口。

CC-Panes 只是一种外部终端环境。Fishyume 不再依赖其 Profile、TaskBinding、Session、daemon 或 MCP approval。

## 2. 产品角色

### 用户

- 提出目标、约束和风险边界；
- 与 Host Agent 讨论方案；
- 确认重要决策；
- 通过 TUI 或 Host Agent 审批和干预 Run。

用户可以手写 Workflow，但这不是默认产品链路。

### Host Agent

- 获取 Fishyume 能力、Schema 和限制；
- 将用户目标编排成显式 DAG；
- 调用 `workflow.validate` 和 `workflow.explain`；
- 启动、查询和控制 Run；
- 获取最终结构化结果并向用户汇报。

Fishyume Core 不隐式调用 LLM 替 Host Agent 生成 Workflow。

### Fishyume Control Plane

Fishyume 拥有：

- Workflow、Run、Node、Attempt 正式身份；
- DAG 调度、并发、条件分支和 Approval；
- Context 编译、Result Contract 和后续结果传递；
- 持久化、事件、租约、崩溃恢复和对账；
- 重试、取消、等待输入与最终结论；
- Driver 选择和能力验证。

### Node Agent Runtime

Codex、Claude、OpenCode 等外部 Agent Harness 负责模型推理、Tool loop、文件修改和命令执行。一个 Fishyume Node Attempt 对应一个可观察的外部 Agent 执行。

Node Agent 可以使用普通开发工具，但不能用不可观察的 Agent 扇出替代 Fishyume 正式 DAG。若 Harness 内部继续派生 Agent，Fishyume 只能把整个 Harness 视为一个不透明原子 Attempt。

### Operator TUI

TUI 是 Human-facing Operator Client，只负责展示和提交动作。它不复制 Engine 业务状态，不拥有调度规则，也不把 React 本地状态作为动作真相。

## 3. 总体架构

```text
User
  |
  v
Host Agent: Codex / Claude / Kimi / OpenCode
  |
  | MCP / Machine CLI
  v
+-------------------------------------------------------+
| Fishyume Local Control Plane                          |
|                                                       |
| Workflow Service                                      |
|   parse / validate / normalize / explain              |
|                                                       |
| Orchestration Kernel                                  |
|   DAG / Run / Node / Attempt / approval / recovery    |
|                                                       |
| Context Plane                                         |
|   deterministic Context Compiler / Result Contract    |
|                                                       |
| Driver Layer                                          |
|   Codex Driver / future Claude and OpenCode Drivers   |
|                                                       |
| Persistence                                           |
|   snapshots / events / attempts / results / leases    |
+-------------------------------------------------------+
  |
  | Headless Agent Process Protocol v1
  v
External one-shot Agent processes

Machine CLI -----------+
MCP adapter -----------+-- local IPC -- Control Plane
Operator TUI ----------+
```

## 4. 不可破坏的核心基础

1. **Engine 是唯一状态真相。** Driver、MCP、CLI、TUI 和进程退出状态都不能直接决定最终结论。
2. **Attempt 身份持久化且可恢复。** 启动前持久化 Prepared Attempt；崩溃后先 Observe/Reconcile，再决定是否调度，禁止重复 Start。
3. **结构化 Result 才是完成证据。** 退出码、终端空闲、stdout 停止或进程消失都不等于成功。
4. **厂商与平台细节隔离。** Scheduler 不按 Codex、Claude、CC-Panes 或操作系统名称分支。
5. **Human-in-the-loop 保持显式。** Approval 是正式 Node 状态，不允许被隐藏副作用绕过。

## 5. 本地 Control Plane

### 为什么需要常驻服务

M3 之前每条 TypeScript CLI 命令启动自己的 Go Engine。这适合前台单客户端体验，但 Host Agent、MCP 与 TUI 无法可靠地同时控制一个异步 Run。继续只靠状态文件会逐渐形成隐式的动作队列、锁、唤醒和事件 IPC。

M4 因此引入明确的本地 Control Plane：

- 每个用户状态目录最多有一个兼容版本的 Control Plane owner；
- CLI 自动发现或启动本地服务；
- Windows 使用 Named Pipe，Linux/macOS 使用 Unix Domain Socket；
- endpoint 只允许当前用户访问，默认不监听 TCP；
- stdio JSON-RPC 保留给测试、受控嵌入和兼容路径；
- `run.start` 异步返回 `runId`，客户端退出不暂停 Run；
- 服务启动后扫描未完成 Run，获取租约并在调度前完成对账；
- 多客户端通过 Control Plane 提交动作，不直接争抢 Run lease；
- 状态文件继续是持久化真相，服务不是唯一的数据持有者。

M4 首版采用用户级按需启动，不要求安装操作系统服务或建设自动更新系统。

## 6. Headless Agent Process Protocol v1

### Attempt Envelope

Context Compiler 为每个 Attempt 构造版本化 Envelope：

```json
{
  "protocolVersion": 1,
  "identity": {
    "runId": "run-...",
    "nodeId": "implement",
    "attempt": 1
  },
  "workspace": "E:\\project",
  "task": "执行已批准的实现方案",
  "context": {
    "upstreamResults": [],
    "requiredSkills": []
  },
  "constraints": {},
  "budget": {},
  "resultContract": {
    "schema": {},
    "maxBytes": 65536
  }
}
```

M4 的 Context Compiler 只做确定性装配：

1. 固定执行与安全契约；
2. Node task；
3. 显式祖先结果；
4. Required Skills；
5. 工具、工作区和运行约束；
6. Run/Node/Attempt 身份；
7. Result Schema。

Compiler 记录版本、组件来源和最终 hash，但默认不持久化完整敏感 Prompt。Project Memory、Prompt Library、自动压缩和 Attention Budget 后置到 M5。

### 标准事件与结果

Driver 将厂商输出归一化为有界 Fishyume 事件：

- `attempt.started`
- `attempt.progress`
- `attempt.diagnostic`
- `attempt.needs_input`
- `attempt.result_pending`
- `attempt.completed`

stdout 只承载 JSONL 机器事件，stderr 作为有界诊断。标准终态至少包括 `succeeded`、`failed`、`needs_input` 和 `indeterminate`。

`needs_input` 必须携带结构化问题并结束当前进程。Fishyume 将 Node 置为 waiting；收到回答后创建新 Attempt，并将回答显式编入新 Envelope。Node Agent 不长期占用交互式 PTY。

### 取消

- Driver 只终止与持久化身份匹配的进程树；
- 只有明确确认停止后，Attempt 才能写 `cancelled`；
- 身份不匹配、PID 复用或结果未知必须返回 `not_confirmed`。

## 7. Agent Driver 与 Execution Target

当前公开语义：

```yaml
defaults:
  backend: direct
  tool: codex
  runtime: local
```

M4 目标语义：

```yaml
defaults:
  agent:
    driver: codex
    target: local
```

- `driver` 将 Attempt Envelope 转换为 Agent Harness 调用，并归一化事件和结果。
- `target` 描述执行环境，例如 `local`；未来可增加 `wsl` 或 `ssh`。
- `requirements` 为未来模型路由保留抽象能力位置，M4 不自动决策模型。

迁移规则：

- `directcli` 迁移为首个正式 Codex Driver；
- 新 Run 不再选择 CC-Panes；
- CC-Panes 代码只在兼容窗口读取历史 snapshot；
- 旧 `backend/tool/runtime` 在兼容窗口归一化到新语义并输出 deprecation warning；
- 新状态持久化 resolved Driver、Target、Driver Handle schema version 和 Context hash；
- 第三方动态 Driver SDK、热加载和插件市场不属于 M4。

## 8. Agent-Native API

MCP、Machine CLI 和 TUI 必须复用同一 Application API。

### 系统与 Workflow

- `system.capabilities`
- `workflow.validate`
- `workflow.explain`

`workflow.explain` 返回规范化 DAG、拓扑顺序、并行层级、Approval、条件分支、Context 来源、resolved Driver/Target、能力缺口和风险警告。它是确定性解释器，不调用模型。

### Run

- `run.start`
- `run.list`
- `run.get`
- `run.events`
- `run.action`
- `run.result`

MCP 不提供无限阻塞的 watch tool。`run.events` 使用 `afterSequence` 和有界 `waitMs` 实现游标读取或短轮询。

`run.action` 统一承载 approve、reject、retry 和 cancel，并绑定 `nodeId`、期望 Attempt/StateVersion 和唯一 `actionId`。

### 幂等与冲突

- `run.start` 使用 `clientRequestId` 去重；
- `run.action` 使用 `actionId` 去重；
- 变更请求携带 `expectedStateVersion` 或等价条件；
- Event `sequence` 在单个 Run 内严格递增；
- 错误使用稳定 code、message 和结构化 data。

## 9. 持久化与安全

M4 继续使用文件型状态存储，不因常驻服务强制迁移 SQLite。持久化内容包括 normalized Workflow、Run/Node/Attempt snapshot、append-only events、Driver Handle、Result、动作幂等身份、Context manifest/hash 和 controller lease。

安全要求：

- 不持久化 Provider credential、完整环境变量或未经裁剪的终端历史；
- 默认不持久化完整 Prompt；
- Result、事件、stderr 和 Handle 都有大小上限；
- 本地 IPC 使用用户级权限；
- Driver sandbox 和权限策略可诊断并固化到 Attempt metadata。

## 10. 正式用户链路

1. 用户与 Host Agent 讨论目标和架构；
2. Host Agent 调用 `system.capabilities`；
3. Host Agent 生成 Workflow；
4. 调用 `workflow.validate` 和 `workflow.explain`；
5. 用户确认方案；
6. Host Agent 调用 `run.start`，立即获得 `runId` 和 attach 命令；
7. Control Plane 调度无头 Node Agent；
8. 用户可执行 `fishyume attach <run-id>`；
9. Approval 可由 TUI 或 Host Agent 提交；
10. Control Plane 重启时先对账后恢复；
11. Host Agent 调用 `run.result` 获取最终结构化结果。

整条链路不要求 CC-Panes 存在。

## 11. M4 非目标

- Fishyume 内置聊天 Agent；
- Core 调用模型生成 Workflow；
- Project Memory 和长期记忆工程；
- 智能模型选择、成本路由和 fallback；
- Prompt 自动优化和动态 Prompt Library；
- Native/Pi Harness；
- 第三方 Driver 插件 SDK、动态发现和热加载；
- Web/Desktop；
- 通用 Shell、HTTP、容器或业务 ETL 编排；
- 运行期间由模型任意改写 DAG。

## 12. M4 验收标准

1. 新 Run 不依赖 CC-Panes Profile、TaskBinding、Session、daemon 或 MCP approval。
2. Node Agent 以无 TUI、无交互 PTY 的外部进程运行。
3. Host Agent 能通过 MCP 完成 capabilities → validate → explain → start → events/action → result。
4. `run.start` 异步返回，客户端退出后 Run 继续执行。
5. TUI 可从另一终端 attach，并与 MCP 共享动作真相。
6. Control Plane 崩溃重启后不会重复启动已持久化 Attempt。
7. 退出码、进程消失和普通 stdout 不能伪造成功。
8. 重复 start/action 由幂等键安全去重。
9. Control Plane、Driver 和 Context Compiler 有平台无关合同测试。
10. Windows Named Pipe 与 Linux Unix Socket 自动化验证通过。
11. M2/M3 状态、取消、并发和 TUI 安全测试不回退。
12. Codex Driver 完成真实项目 live smoke；Claude Driver 可后置。

## 13. 后续路线

- M5：Context Engineering & Memory
- M6：Capability and Model Routing
- M7：Driver Ecosystem and optional Native Harness

M4 必须先让 Fishyume 成为可靠的 Agent-facing 编排控制面，再建设智能上下文、模型调优和第三方生态。
