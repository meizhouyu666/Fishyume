# Fishyume M4 分阶段实施计划

> 状态：M4.0 + M4.1 + M4.2 已完成；M4.3 未开始
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

> 实施状态：已完成（用户级 owner lock/metadata、Named Pipe/Unix Socket、IPC 握手、TS 自动发现/启动、异步 controller、启动恢复、多客户端读与串行 mutation、只读 detach）。

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

### 实施记录

- `wf-engine serve` 持有状态目录级 owner lock；metadata 固化 Engine/RPC/IPC/state schema、规范化 stateDir、owner ID、用户 identity 与 state-dir hash。活 owner 阻止同版本或不兼容版本并发写入；只有取得 lock 后才替换 stale endpoint。
- Windows Named Pipe 使用当前用户 SID ACL；Unix Socket 目录/socket 权限分别为 `0700`/`0600`。握手为 64 KiB 有界 frame 并带 deadline，后续 JSON-RPC 保持 1 MiB 上限；不监听 TCP。
- TS bridge 默认连接 Control Plane；显式 Engine args 的受控测试路径继续使用 stdio。客户端 close、TUI detach 和终端退出只关闭连接。
- 启动扫描非终态 Run；持久化 Attempt 总是先 reconcile，再进入 schedule。恢复 lease 仅在旧 PID 被确认失效或 lease 到期时替换。
- 连接级通知为有界 best-effort 流；snapshot/event 文件仍是持久化真相。读请求可并发，mutation 由共享 gate 串行化。
- 新 snapshot 写入单调 `stateVersion`；现有 resume/cancel RPC 接受可选 `expectedStateVersion`，CLI/TUI action 会绑定所见版本。完整 `run.action`/`actionId` API 仍属于 M4.3。
- M4.2 不启用自动 idle 退出，因此活动或等待中的 Run 不会因 idle 策略失去服务 owner。

## 5. M4.3：Agent-Native MCP 与 Machine API

> 实施状态：未开始；2026-08-08 已补充正式 Application Service、`answer` 动作、跨重启幂等与有界查询合同。

### M4.3.0：Application Contract

- 在 transport 与 Core 之间建立正式 Application Service。MCP、Machine CLI、TUI 和兼容 RPC 只能调用该层，不得直接导入 Scheduler、Store 或具体 Driver。
- Application request/response、错误 envelope 和 JSON fixture 是唯一公开合同；MCP 与 Machine CLI 复用相同类型，不各自重新解释业务错误。
- 新公开合同只使用 `driver/target`。`backend/tool/runtime` 和 `run.startWorkflow` 仅保留在兼容入口，不进入 `system.capabilities`、MCP tool schema 或新状态。
- 定义稳定错误 code：`invalid_argument`、`invalid_workflow`、`not_found`、`conflict`、`capability_unavailable`、`not_ready`、`protocol_mismatch` 和 `internal`；错误同时携带稳定 `message` 与有界结构化 `data`。

### M4.3.1：Application API

- `system.capabilities`
- `workflow.validate`
- `workflow.explain`
- `run.start`
- `run.list`
- `run.get`
- `run.events`
- `run.action`
- `run.result`

正式 `run.start` 接受 project、Workflow source/structured document、inputs 和 `clientRequestId`。Ad-hoc human CLI 可以在客户端生成单节点 Workflow；不得为 MCP 再定义第二套 Run 创建语义。

`run.list` 使用稳定排序、filter、cursor 和 limit；`run.events` 使用 `afterSequence`、limit 和有界 `waitMs`；`run.result` 明确区分 terminal result 与 `not_ready`。所有列表、事件、Result 和 schema response 均有 item/byte 上限。

### Workflow 作者体验

- Capabilities 返回 API/Workflow schema version、JSON Schema、Node/action 类型、Driver/Target 能力、稳定限制、错误码和最小示例，不返回 credential 或完整环境变量。
- Validate 返回稳定 path/code/message；静态语法/结构错误与当前机器的 Driver capability 缺口分开表达。
- Explain 返回规范化 DAG、拓扑顺序、并行层、Approval、条件、Context 来源、resolved Driver/Target、capability gap 和 warning。
- 不调用 LLM，不隐式改写 Workflow。

### `run.action` 与 `needs_input`

- `run.action` 统一承载 `approve`、`reject`、`answer`、`retry` 和 `cancel`。
- 每个动作携带唯一 `actionId`、`runId`、`expectedStateVersion`；Node 动作还必须绑定 `nodeId` 和适用时的 `expectedAttempt`。
- `answer` 携带按 question ID 索引的结构化 scalar answers；Engine 校验 question identity、required/choice 约束和 Attempt identity。
- `answer` 不恢复旧交互进程。它结束 waiting 状态，创建新 Attempt，并由 Context Compiler 将原问题与回答显式编入新 Envelope。
- TUI 与 Host Agent 提交相同 action request；任何客户端都不能在本地推断动作是否仍然适用。

### M4.3.2：持久化幂等、冲突与事件

- `clientRequestId` 持久化去重 Run 创建；`actionId` 持久化去重全部动作，包括 `answer` 和 `retry`。
- 同一 ID 与相同 canonical request hash 返回原 `runId`/action response；同一 ID 与不同 payload 返回 `conflict`。
- 幂等记录必须跨客户端、Control Plane crash/restart 和 RPC/MCP 重发有效，不允许只保存在进程内 Map。
- 文件存储继续保留，但 request/action intent、业务状态 mutation 与 committed response 必须通过可恢复 journal 或等价协议消除崩溃窗口；恢复时不得重复 Start、retry、cancel 或 Approval/answer 副作用。
- `expectedStateVersion` 和 `expectedAttempt` 防止陈旧提交；幂等 replay 与新请求 conflict 必须可区分。
- Event sequence 在单个 Run 内严格递增。事件游标读取以持久化日志为真相；通知只负责唤醒，不得成为唯一事件来源。
- 有界 `run.events` 等待不能阻塞同一 MCP client 的独立读取或 action；RPC transport 必须支持连接内并发请求，或由适配器使用等价的独立连接策略。

### M4.3.3：MCP、Machine CLI 与 TUI 迁移

- MCP server 是连接 Control Plane 的薄适配器，不导入 Scheduler、Store 或具体 Driver。
- `run.events` 使用 `afterSequence` 和有界 `waitMs`，不提供无限阻塞调用。
- CLI `--json` 与 MCP 使用相同 response 类型。
- 人类文本、TUI 和 JSON 输出路径分离。
- 增加 `fishyume attach <run-id>`。
- TUI 迁移到 `run.get`/`run.action`，不再直接拥有 `resume/cancel` 的业务参数拼装；旧 human CLI 命令通过兼容 adapter 调用同一 Application Service。

### 门禁

- Application contract fixtures 覆盖全部 request/response/error，公开 JSON 不出现 `backend/tool/runtime`。
- Fake Host Agent 完成 capabilities → validate → explain → start → events/action（含 Approval 与 `needs_input` answer）→ result。
- MCP 在 Control Plane 重启前后重发 start 不创建第二个 Run；相同 ID 不同 payload 稳定返回 conflict。
- MCP 在 action intent、状态 mutation 和 response commit 故障点重发动作不产生重复副作用。
- `run.events` 覆盖 cursor、分页、byte/item limit、bounded wait、断线重连和等待期间并发 action。
- Machine CLI 与 MCP 的 JSON snapshot 完全一致；human text/TUI snapshot 不污染 machine output。
- TUI 只消费 Application API 返回的 Control Plane 状态，并与 MCP 共享 `actionId`/expected state 真相。
- 新 M4.3 测试不调用 LLM、不要求 Provider 登录、不依赖 CC-Panes 或人工 MCP allow。
- `go test ./...`、`go vet ./...`、`go build ./cmd/wf-engine`、`npm --prefix wf run verify` 与 `git diff --check` 全通过。

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
8. `feat: freeze agent-native application contracts`
9. `feat: add durable application api and idempotency`
10. `feat: expose fishyume mcp and machine cli`
11. `refactor: route tui and legacy cli through application api`
12. `docs: complete M4 product and migration guide`

状态 Schema、IPC、MCP 和 Driver 迁移不得混在一个不可分割的大提交中。

## 9. 后续入口

M4 收口后才进入：

- M5 Context Engineering & Memory；
- M6 Capability and Model Routing；
- M7 Driver Ecosystem and optional Native Harness。

进入 M5 的证据不是“Prompt 拼装代码已经存在”，而是同一套 Control Plane 已能稳定向不同 Node Attempt 提供可审计、版本化的 Context Envelope。
