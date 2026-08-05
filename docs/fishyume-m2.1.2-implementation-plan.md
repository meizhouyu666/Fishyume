# Fishyume M2.1.2 后端独立性实施计划

> 状态：待确认
> 日期：2026-08-05
> 依据：`docs/fishyume-m2.1.2-backend-independence.md`
> 范围：只实施后端独立性，不进入 M2.2 并发

## 1. 交付目标

本计划把已经确认的 M2.1.2 架构拆成可独立验证、可逐步提交的实施批次。最终交付不是一个新的空接口，而是同一 Fishyume Workflow 可以选择两个真实 Backend：

- `ccpanes`：保留 M2.1.1 已验证能力和默认体验。
- `direct`：首版直接启动本地非交互式 Codex CLI。

两个 Backend 必须共享同一套 Run/Node/Attempt 状态机、恢复语义、取消规则和规范化 `AgentResult`。调度器不得按 Backend 名称编写平台分支。

## 2. 实施原则

- 先冻结现有行为，再移动边界。
- 每个阶段先增加会失败的测试或历史 fixture，再改实现。
- 每个阶段完成后独立 commit，保留可审计历史。
- CC-Panes 默认路径在整个重构期间保持可运行。
- Direct CLI 的退出码和自然语言输出不能冒充结构化成功结果。
- 无法确认恢复对象或取消结果时保留 waiting/indeterminate，不制造成功或 cancelled。
- 不自动迁移、删除或批量重写旧状态。
- 不发布 npm 包或 GitHub Release。

## 3. 预期代码布局

```text
wf-engine/internal/backend/
├── backend.go                 # 平台无关契约
├── registry.go                # Backend 注册、发现和选择
├── contracttest/              # 所有正式 Backend 共用测试
├── ccpanes/                   # CC-Panes Adapter
└── directcli/                 # Direct CLI Adapter

wf-engine/internal/run/        # 只消费通用 Backend 契约
wf-engine/internal/store/      # handle 与历史快照兼容
wf-engine/internal/rpc/        # 动态 Backend 能力与选择参数
wf-engine/cmd/wf-engine/       # 唯一组合根

wf/src/bridge/                 # RPC 类型与参数
wf/src/commands/               # --backend 与 Doctor 呈现
wf/src/integration/            # 跨进程、跨 Backend 集成测试
```

具体文件可在实现中拆分，但具体 Backend 包不得被 `workflow`、`run`、`store` 或 `rpc` 导入。

## 4. 阶段 0：基线与 Direct CLI 能力 Spike

### 4.1 目标

在改变核心接口前保存可复现基线，并确认本机 Codex CLI 提供哪些稳定的非交互式、机器可读能力。

### 4.2 工作项

1. 保存或新增 M2.1.1 历史 Run fixture，覆盖：
   - completed；
   - approval waiting；
   - active Attempt；
   - completion missing；
   - cancel failed；
   - 含旧 `taskBindingId` 与 session metadata 的 Attempt。
2. 为现有 CC-Panes 行为补齐回归测试：
   - Start 后持久化 session；
   - Engine 重启后 reconcile；
   - TaskBinding terminal 转换结果；
   - WaitingInput；
   - idle 有界对账；
   - cancel 只有确认后完成。
3. 只读检查当前 Codex CLI：
   - 可执行文件发现；
   - 非交互式执行入口；
   - JSON/JSONL 事件能力；
   - 最终输出 schema 能力；
   - workspace 与权限参数；
   - 中断和进程退出行为。
4. 记录 Spike 结论，选择 Direct CLI 完成通道：
   - 优先使用官方且稳定的结构化最终输出能力；
   - 若不足，则使用 Fishyume 控制的 Attempt 专属结果通道；
   - 禁止退化为解析自然语言末尾文本。

### 4.3 主要文件

- `wf-engine/internal/run/testdata/` 或等价历史 fixture 目录。
- `wf-engine/internal/backend/ccpanes/backend_test.go`。
- `wf/src/integration/fake-backend.test.ts`。
- 新增 `docs/fishyume-direct-cli-spike.md` 记录可复验事实和最终选择。

### 4.4 验证

```powershell
cd wf-engine
go test ./internal/run ./internal/backend/ccpanes

cd ..\wf
npm test
```

### 4.5 停止条件

如果 Codex CLI 不具备安全的非交互式执行入口，且无法通过本地结构化结果通道补足，则停止 Direct Backend 实现并报告，不用退出码制造伪完成。

### 4.6 建议提交

```text
test: 固化后端重构前行为基线
docs: 记录 Direct CLI 能力验证
```

## 5. 阶段 1：建立平台无关 Backend 契约

### 5.1 目标

用新契约表达 Agent 的启动、观察、结果和取消，不改变现有 CC-Panes 用户行为。

### 5.2 工作项

1. 在 `backend.go` 引入：
   - `AgentExecutionSpec`；
   - `ExecutionHandle`；
   - `ExecutionObservation`；
   - `AgentResult`；
   - `CancelResult`；
   - `Capabilities`；
   - 新 `AgentBackend` 接口。
2. 将 `Observe` 设为必需能力，替代当前 `Wait + optional Reconciler` 的分裂语义。
3. 将 `result_pending`、`waiting_input`、`terminal` 和 `lost` 定义为平台无关观察。
4. 将 invalid result、completion missing、有界重试和最终结论保留在 Engine。
5. 定义 handle 的 JSON、大小、版本和敏感信息限制。
6. 新建 contract test harness，接受 Backend fixture factory 并运行共享场景。

### 5.3 主要文件

- `wf-engine/internal/backend/backend.go`。
- `wf-engine/internal/backend/contracttest/contract.go`。
- `wf-engine/internal/backend/contracttest/contract_test.go`。
- `wf-engine/internal/run/service_test.go`。

### 5.4 测试先行场景

- Handle JSON round-trip。
- Observe 不触发 Start。
- terminal 必须携带合法结果。
- result pending 不直接结束 Run。
- cancel not confirmed 不写 cancelled。
- opaque data 超限或非法时拒绝持久化。

### 5.5 验证

```powershell
cd wf-engine
go test ./internal/backend/... ./internal/run
go vet ./...
```

### 5.6 建议提交

```text
refactor: 定义平台无关 Agent 执行契约
test: 增加 Backend 共享契约测试
```

## 6. 阶段 2：将 CC-Panes 收进 Adapter 边界

### 6.1 目标

让现有 CC-Panes 实现完整适配新契约，并从核心模型和消息中移除平台专属概念。

### 6.2 工作项

1. 将 CC-Panes session、launch、binding 和 profile 信息编码进 `ExecutionHandle.Data`。
2. 将 TaskBinding terminal、WaitingInput、idle、exited/lost 映射为统一 Observation。
3. 将 Backend cancel 错误拆成：
   - transport/protocol error；
   - `CancelNotConfirmed`；
   - `CancelConfirmed`。
4. 先引入旧 Attempt compatibility decoder，将旧 `taskBindingId` 和 session metadata 转换为 CC-Panes handle。
5. 兼容读取测试通过后，从 `AttemptSnapshot` 的新写入路径删除顶层 `TaskBindingID`。
6. 从 `run.Service` 移除包含 TaskBinding、orchestrator、daemon 的平台消息。
7. 将平台诊断改由 CC-Panes Doctor 返回结构化条目。
8. 让 CC-Panes fixture 通过完整 contract test harness。

### 6.3 主要文件

- `wf-engine/internal/backend/ccpanes/backend.go`。
- `wf-engine/internal/backend/ccpanes/types.go`。
- `wf-engine/internal/backend/ccpanes/backend_test.go`。
- `wf-engine/internal/run/lifecycle.go`。
- `wf-engine/internal/run/service.go`。
- `wf-engine/internal/run/service_test.go`。
- 新增 `wf-engine/internal/store/legacy_attempt.go` 或等价兼容文件。

### 6.4 边界检查

```powershell
rg -n "TaskBinding|CC-Panes|orchestrator|daemon|profile" `
  wf-engine/internal/run `
  wf-engine/internal/store `
  wf-engine/internal/rpc
```

允许人类可读兼容说明出现在专门的旧状态解码器中；运行核心不得依赖这些概念。

### 6.5 验证

```powershell
cd wf-engine
go test ./internal/backend/ccpanes ./internal/backend/contracttest ./internal/run ./internal/store
go test ./...
go vet ./...
go build ./cmd/wf-engine
```

### 6.6 建议提交

```text
refactor: 隔离 CC-Panes 后端实现
test: 验证 CC-Panes 符合通用契约
```

## 7. 阶段 3：Registry、Backend 选择与 Doctor

### 7.1 目标

Engine 可以注册多个 Backend，并为每个 Run 显式、稳定地选择一个 Backend。

### 7.2 工作项

1. 实现 Backend Registry 与 Factory：
   - 名称唯一；
   - 稳定排序；
   - 初始化失败相互隔离；
   - 未知 Backend 给出可操作错误。
2. `cmd/wf-engine/main.go` 只负责注册 `ccpanes` 和 `direct` factory。
3. `run.Service` 接收 Registry，不再持有唯一 Backend。
4. Backend 选择优先级：
   - CLI `--backend`；
   - Workflow `defaults.backend`；
   - `FISHYUME_BACKEND`；
   - `ccpanes`。
5. 在 normalized Workflow 和 Run snapshot 中持久化最终 Backend。
6. `resume/cancel` 只按 snapshot 中的 Backend 获取 Adapter。
7. RPC `supportedBackends` 动态生成。
8. Doctor 支持选择 Backend，并区分：
   - Engine 可用；
   - Backend 可用；
   - Tool/Runtime 组合可用；
   - workspace 可用。
9. TypeScript CLI 增加参数、类型和一致的文本/JSON 呈现。

### 7.3 主要文件

- `wf-engine/internal/backend/registry.go`。
- `wf-engine/cmd/wf-engine/main.go`。
- `wf-engine/internal/workflow/model.go`。
- `wf-engine/internal/workflow/parse.go`。
- `wf-engine/internal/workflow/validate.go`。
- `wf-engine/internal/run/service.go`。
- `wf-engine/internal/rpc/server.go`。
- `wf-engine/internal/rpc/types.go`。
- `wf/src/bridge/types.ts`。
- `wf/src/commands/doctor.tsx`。
- `wf/src/commands/` 下 run/status/resume/cancel 参数实现。

### 7.4 测试先行场景

- Registry 拒绝重复名称。
- 一个 Backend 初始化失败不影响另一个。
- 选择优先级正确。
- 未知 Backend 在创建 Run 前失败。
- 不支持的 Backend/Tool/Runtime 组合在创建 Run 前失败。
- 默认仍为 `ccpanes`。
- 改变环境默认值不影响已有 Run 的 resume/cancel。
- `engine.hello` 动态返回 Registry 内容。

### 7.5 验证

```powershell
cd wf-engine
go test ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm run typecheck
npm test
npm run build
```

### 7.6 建议提交

```text
feat: 支持注册和选择 Agent 后端
feat: 增加后端感知 Doctor
```

## 8. 阶段 4：旧状态兼容

### 8.1 目标

架构纠偏不能破坏 M2.1.1 已存在 Run 的查看、恢复和取消。

### 8.2 工作项

1. 扩充阶段 2 建立的旧 Attempt compatibility decoder fixture，只在 compatibility 层暴露 `taskBindingId`。
2. 验证旧 session metadata 与 binding ID 到 CC-Panes `ExecutionHandle` 的所有历史组合。
3. 断言新 Run 不再写顶层 `taskBindingId`。
4. 明确是否需要升级 `stateSchemaVersion`：
   - 优先保持兼容读取且不升级；
   - 如果必须升级，先补双版本 fixture 和读写矩阵；
   - 不批量改写旧状态目录。
5. 对旧 Run 的 status、resume、cancel 分别增加测试。

### 8.3 主要文件

- `wf-engine/internal/run/lifecycle.go`。
- `wf-engine/internal/store/json_store.go`。
- 新增 `wf-engine/internal/store/legacy_attempt.go` 或等价兼容文件。
- `wf-engine/internal/store/*_test.go`。
- `wf-engine/internal/run/testdata/`。

### 8.4 验证

```powershell
cd wf-engine
go test ./internal/store ./internal/run
go test ./...
```

### 8.5 建议提交

```text
fix: 保持 M2.1.1 运行状态兼容
```

## 9. 阶段 5：Direct CLI 进程与结果通道

### 9.1 目标

实现第二个真实 Backend，首版支持 `backend=direct`、`tool=codex`、`runtime=local`。

### 9.2 工作项

1. CLI 发现与 Doctor：
   - 明确路径覆盖环境变量；
   - PATH 发现；
   - 版本和能力诊断；
   - 不自动安装或下载 Codex CLI。
2. 启动：
   - 使用参数数组，不通过 Shell 拼接命令；
   - 指定 workspace；
   - 生成 Attempt 专属状态目录；
   - 持久化 PID、启动时间、可执行文件身份和进程指纹。
3. 观察：
   - 活跃进程返回 active；
   - 结构化结果尚未到达返回 result pending；
   - 合法结果返回 terminal；
   - 进程结束但结果缺失进入 result pending，并由 Engine 有界对账；
   - 身份无法确认返回 lost。
4. 结果通道按阶段 0 的 Spike 决策实现，并统一转换为 `AgentResult`。
5. 输出：只读取有界 stdout/stderr 诊断，不进入下游上下文。
6. 取消：
   - Windows 与 Unix 分别实现进程树终止；
   - 终止后重新验证进程身份与存活状态；
   - 无法确认时返回 `CancelNotConfirmed`。
7. 使用可执行 fake Agent fixture 覆盖进程行为，再接入本机 Codex CLI live smoke。

### 9.3 建议文件

- `wf-engine/internal/backend/directcli/backend.go`。
- `wf-engine/internal/backend/directcli/discovery.go`。
- `wf-engine/internal/backend/directcli/handle.go`。
- `wf-engine/internal/backend/directcli/result.go`。
- `wf-engine/internal/backend/directcli/process_windows.go`。
- `wf-engine/internal/backend/directcli/process_unix.go`。
- `wf-engine/internal/backend/directcli/backend_test.go`。
- `wf-engine/internal/backend/directcli/testdata/fake-agent/`。

### 9.4 测试先行场景

- 不经过 Shell 的安全参数传递。
- workspace 含空格和中文。
- Agent active、success、failure、missing result、malformed result。
- Engine 退出后用 handle 恢复观察。
- PID 复用保护。
- cancel confirmed 与 not confirmed。
- stdout/stderr 大小限制。
- 结果 token 或等价身份校验。
- Windows 和 Linux 进程树 fixture。

### 9.5 验证

```powershell
cd wf-engine
go test ./internal/backend/directcli ./internal/backend/contracttest
go test ./...
go vet ./...
go build ./cmd/wf-engine
```

Linux CI 额外执行：

```bash
go test -race ./...
```

### 9.6 建议提交

```text
feat: 增加 Direct CLI Agent 后端
test: 覆盖 Direct CLI 恢复与取消
```

## 10. 阶段 6：跨 Backend 集成与产品收口

### 10.1 目标

证明两个真实 Backend 使用同一外部工作流语义，并完成安装、文档和跨平台验证。

### 10.2 工作项

1. 使用同一 YAML fixture：
   - Agent `plan`；
   - Approval `approve`；
   - Agent `implement`。
2. 分别执行：
   - `--backend ccpanes`；
   - `--backend direct --tool codex --runtime local`。
3. 对两个 Run 检查：
   - phase/conclusion/reason 一致；
   - 下游模板得到相同形状的 `AgentResult`；
   - approval 后由新 Engine 进程 resume；
   - crash reconcile 不重复启动 Agent；
   - cancel 只在确认后完成。
4. 更新 README：
   - 产品定位改为平台无关 AI Agent 编排引擎；
   - CC-Panes 为默认 Backend；
   - Direct CLI 环境要求和最小示例；
   - 保持 README 精简。
5. 更新 release readiness 和 CI：
   - Direct CLI fixture 自动测试；
   - 真实 Codex live smoke 仅手工执行，不进入公共 CI；
   - 包中不包含凭据、完整 prompt 或终端历史。

### 10.3 主要文件

- `docs/examples/` 下共享 smoke Workflow。
- `wf/src/integration/`。
- `README.md`。
- `wf/README.md`。
- `docs/fishyume-release-readiness.md`。
- `.github/workflows/fishyume-ci.yml`。

### 10.4 全量验证

```powershell
cd wf-engine
go test ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm ci
npm run typecheck
npm test
npm run build
npm run pack:audit
npm run pack:audit:real

cd ..
git diff --check
```

CI 必须继续包含 Ubuntu `go test -race ./...`、Windows/Ubuntu verify、Linux/Windows platform install 和 artifacts。

### 10.5 Live smoke

CC-Panes 和 Direct CLI smoke 均只在已经确认的本地项目执行。每次 smoke 后检查：

- Engine 已退出；
- Agent session/process 已退出或处于预期 detach 状态；
- 无遗留 Node、Codex、测试 fixture 或 Engine 进程；
- 结构化结果不包含凭据和完整环境。

### 10.6 建议提交

```text
test: 验证双后端工作流一致性
docs: 更新 Fishyume 后端使用说明
```

## 11. 测试矩阵

| 场景 | fake Backend | CC-Panes fixture | Direct fixture | CC-Panes live | Codex live |
|---|---:|---:|---:|---:|---:|
| Start/terminal success | 是 | 是 | 是 | 是 | 是 |
| 明确失败 | 是 | 是 | 是 | 可选 | 可选 |
| waiting input | 是 | 是 | 能力允许时 | 是 | 能力允许时 |
| result pending | 是 | 是 | 是 | 是 | 是 |
| invalid result | 是 | 是 | 是 | 可选 | 可选 |
| Engine crash/reconcile | 是 | 是 | 是 | 是 | 是 |
| cancel confirmed | 是 | 是 | 是 | 是 | 是 |
| cancel not confirmed | 是 | 是 | 是 | 可控时 | 可控时 |
| lost/身份不确定 | 是 | 是 | 是 | 可选 | 可选 |
| Approval 跨进程 resume | 是 | 间接 | 间接 | 是 | 是 |

自动测试不依赖已经注册的 CC-Panes 项目或真实 Codex 账户。真实平台验证只作为受控手工 gate。

## 12. 代码审查硬门槛

每个阶段审查时检查：

1. `run`、`workflow`、`store`、`rpc` 是否导入具体 Backend。
2. 是否出现 `if backend.Name() == ...` 一类平台分支。
3. 新 snapshot 是否仍写 TaskBinding 顶层字段。
4. Backend transport error 是否被误判为业务失败。
5. 进程退出或 session idle 是否被误判为成功。
6. cancel 是否在未确认时写 cancelled。
7. resume 是否可能重新 Start 已存在 Attempt。
8. opaque metadata 是否可能包含凭据、完整环境或完整 prompt。
9. Direct CLI 是否通过 Shell 拼接用户输入。
10. README 是否因实现细节变得冗长。

任一硬门槛不满足，不进入下一阶段。

## 13. 里程碑完成定义

只有以下证据全部存在，M2.1.2 才能收口：

- 架构文档中的 13 项验收标准全部有对应测试或 live evidence。
- CC-Panes 与 Direct CLI 均通过 Backend contract suite。
- 同一 Workflow 在两个真实 Backend 上完成 Agent → Approval → Agent。
- 两个平台都证明跨进程恢复不重复启动 Agent。
- 两个平台都证明取消确认语义正确。
- M2.1.1 历史状态兼容测试通过。
- Windows 与 Ubuntu CI 全绿，Ubuntu race 通过。
- 工作区干净，无遗留进程和未归档验证记录。
- 所有代码和文档修改已 commit 并 push。

完成 M2.1.2 后，再单独讨论 M2.2 的并发模型、能力声明和资源上限；本计划不预先实现任何并发调度。
