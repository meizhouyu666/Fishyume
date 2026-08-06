# Fishyume M4 分阶段实施计划

> 状态：M4.0 + M4.1 已完成；M4.2 + M4.3 未开始
>
> 对应架构：[`fishyume-m4-agent-native-control-plane.md`](./fishyume-m4-agent-native-control-plane.md)

## 1. 实施原则

- 每个批次保持 `main` 可构建、可测试和可使用。
- 先冻结契约和兼容语义，再迁移实现。
- 不以一次性大重写替换 M2 已验证的状态机。
- Scheduler 不出现 Codex、Claude、CC-Panes 或操作系统名称分支。
- 新旧状态迁移必须有 fixture；兼容读取与新写入分开测试。
- MCP、CLI、TUI 只能调用统一 Application API。
- M4 不顺带实现 Memory、模型路由、Native Harness 或动态插件 SDK。

## 2. M4.0：规格冻结与测试基线

> 实施状态：已完成（state schema v3、合同 fixture、稳定限制与历史 snapshot 兼容读取）。

### 工作项

- 固化 M2.2/M3.3 测试和 canonical fixtures。
- 为 Agent Driver、Attempt Envelope、Driver Event、Agent Result、IPC handshake 和 Agent-native API error 建立合同 fixture。
- 明确 state schema 迁移版本和旧 Backend snapshot 兼容窗口。
- 定义 stable error code、Result/Event/IPC frame 大小限制。
- 标记旧 M1/M2 架构文档为历史基线。

### 门禁

- `go test ./...`
- `go vet ./...`
- `npm --prefix wf run verify`
- M3.3 canonical gallery 不发生未批准变化。
- 新合同 fixture 不依赖真实 Codex 或 CC-Panes。

## 3. M4.1：Agent Driver 与 Headless Protocol

> 实施状态：已完成（Codex Driver、Context Compiler v1、CC-Panes 新 Run 退役与兼容诊断）。

### 目标

把 Direct Codex Backend 迁移为正式 Codex Driver，使新 Node 执行链不再依赖 Backend 产品语义。

### 工作项

#### 核心合同

- 新增平台无关 Driver 包。
- 定义 `AgentDriver`、`DriverCapabilities`、`AttemptEnvelope`、`ExecutionHandle`、`ExecutionObservation`、`CancelResult` 和 `AgentResult`。
- 将新状态中的 Backend 身份迁移为 resolved Driver 和 Target。
- 保留旧 snapshot decoder，但新状态不得写 TaskBinding 或 CC-Panes 私有身份。

#### Context Compiler v1

- 从调度器移除直接 Prompt 字符串拼接。
- 确定性编译固定契约、task、祖先 Result、Required Skills、约束、Attempt 身份和 Result Schema。
- 持久化 compiler version、component manifest 和 hash。
- 不持久化完整敏感 Prompt。

#### Codex Driver

- 迁移当前 `codex exec --ephemeral --json` 监督器。
- 保持进程指纹、PID 复用防护、结果 hash、日志上限和进程树取消。
- 归一化 Codex JSONL 为 Driver Event。
- 不分配交互式 PTY，不启动 Codex TUI。
- 将 `needs_input` 纳入正式 Result/Observation 语义。

#### CC-Panes 退役

- 新 Run 不注册或选择 CC-Panes Driver。
- 删除默认 Profile、orchestrator、daemon 和项目注册诊断。
- 旧 CC-Panes Run 保持 status 可读；不可恢复时返回明确兼容诊断。

### 门禁

- Driver contract suite 全通过。
- fake-agent 的成功、失败、invalid result、needs input、恢复、取消和 PID 复用测试通过。
- 同一 Workflow 在迁移前后得到等价结论。
- 新状态文件不含 CC-Panes 字段。
- Windows/Linux Codex live smoke 通过。

## 4. M4.2：Local Control Plane 与 IPC

### 目标

让 Run 生命周期独立于单个 CLI、MCP 或 TUI 客户端，并允许多客户端安全连接。

### 工作项

#### Serve 模式与 IPC

- Go Engine 增加 local service transport，并保留 stdio transport。
- Windows 实现 Named Pipe；Linux/macOS 实现 Unix Domain Socket。
- 用户级访问权限、有界 frame、连接超时和协议握手。
- 启动时验证 stateDir、版本、协议和 owner identity。
- endpoint 陈旧时确认旧 owner 已失效后再安全替换。

#### 生命周期

- TS CLI 自动发现、启动和连接 Control Plane。
- `run.start` 创建异步 controller 后立即返回。
- 服务重启扫描非终态 Run，先 reconcile 后 schedule。
- 活动 Run 存在时不得因空闲策略退出。
- 版本不兼容时禁止两个版本同时写同一 stateDir。

#### 多客户端

- MCP、CLI、TUI 可并发读取状态和事件。
- 所有变更动作由 Control Plane 串行化。
- TUI detach 只断开观察，不暂停或取消 Run。
- 客户端断线不改变 Run 状态。

### 门禁

- CLI 启动 Run 后立即退出，Run 仍能完成。
- 第二个 CLI/TUI 能读取同一活动 Run。
- 强制终止并重启 Control Plane 后不产生重复 Attempt。
- 两个并发 action 只有一个满足 expected state。
- Named Pipe、Unix Socket 与保留的 stdio 集成测试通过。

## 5. M4.3：Agent-Native MCP 与 Machine API

### Application API

- `system.capabilities`
- `workflow.validate`
- `workflow.explain`
- `run.start`
- `run.list`
- `run.get`
- `run.events`
- `run.action`
- `run.result`

### Workflow 作者体验

- Capabilities 返回 JSON Schema、Node 类型、限制和示例。
- Validate 返回稳定 path/code/message。
- Explain 返回 DAG、并行层、Approval、条件、Context 来源、resolved Driver/Target 和 warning。
- 不调用 LLM，不隐式改写 Workflow。

### MCP 与 Machine CLI

- MCP server 是连接 Control Plane 的薄适配器，不导入 Scheduler、Store 或具体 Driver。
- `run.events` 使用 `afterSequence` 和有界 `waitMs`，不提供无限阻塞调用。
- CLI `--json` 与 MCP 使用相同 response 类型。
- 人类文本、TUI 和 JSON 输出路径分离。
- 增加 `fishyume attach <run-id>`。

### 幂等与冲突

- `clientRequestId` 去重 Run 创建。
- `actionId` 去重 approve/reject/retry/cancel。
- `expectedStateVersion` 和 expected Attempt 防止陈旧提交。
- 定义 conflict、not found、invalid workflow、capability unavailable 和 protocol mismatch 错误码。

### 门禁

- Fake Host Agent 完成 capabilities → validate → explain → start → events/action → result。
- MCP 重发 start 不创建第二个 Run。
- MCP 重发动作不产生重复副作用。
- Machine CLI JSON snapshot 稳定。
- TUI 仍只消费 Control Plane 状态。

## 6. M4.4：产品化、迁移与发布验证

### 工作项

- 用真实 Codex Host Agent 通过 MCP 创建和运行 Workflow。
- 用真实 Codex Driver 执行并行 Agent → Approval → Agent Workflow。
- Host Agent 与 TUI 同时连接并分别提交动作。
- 执行 Control Plane crash/restart live smoke。
- 更新 README、CLI help、MCP tool descriptions 和示例 Workflow。
- 提供 `backend/tool/runtime` 到 `agent.driver/target` 的迁移说明。
- 移除发布包中的 CC-Panes Profile 和控制面要求。
- 更新 CI，公开测试不依赖真实 Provider 登录。
- 生成 release readiness 和安全检查清单。

### M4 收口门禁

- Go test、vet、build 全通过。
- TypeScript typecheck、test、build、diff check 全通过。
- Windows 与 Linux IPC 集成测试通过。
- Codex live smoke 通过。
- P0/P1/P2 独立审查为零。
- `main == origin/main`，工作树干净。
- 架构文档、实施记录和 README 与实际行为一致。

## 7. M4.5：可选第二 Driver 证明

Claude Driver 可以在 M4 核心收口后作为追加验证，但不阻塞 M4：

- 复用同一 Attempt Envelope、Result 和 cancel contract；
- Scheduler 不出现 Claude 特例；
- CLI 能力不足时明确降级，不伪造 event 或 usage；
- 不在此阶段建设动态插件 SDK。

## 8. 建议提交边界

1. `docs: freeze M4 control-plane contracts`
2. `refactor: introduce agent driver contracts`
3. `refactor: migrate direct codex backend to driver`
4. `feat: add deterministic context compiler v1`
5. `chore: retire ccpanes from new runs`
6. `feat: add local control plane transport`
7. `feat: reconnect cli and tui through local ipc`
8. `feat: add agent-native application api`
9. `feat: expose fishyume mcp server`
10. `docs: complete M4 product and migration guide`

状态 Schema、IPC、MCP 和 Driver 迁移不得混在一个不可分割的大提交中。

## 9. 后续入口

M4 收口后才进入：

- M5 Context Engineering & Memory；
- M6 Capability and Model Routing；
- M7 Driver Ecosystem and optional Native Harness。

进入 M5 的证据不是“Prompt 拼装代码已经存在”，而是同一套 Control Plane 已能稳定向不同 Node Attempt 提供可审计、版本化的 Context Envelope。
