# Fishyume M2.2 并发调度实施计划

> 状态：已批准架构后的执行计划
>
> 依据：`docs/fishyume-m2.2-parallel-scheduling.md`
>
> 范围：完成 M2 剩余的有界并发调度，不实现第三方 Backend 插件系统

## 1. 交付目标

M2.2 交付一个可恢复、可取消、可观测的多 Agent 并发调度器：

- Workflow 接受大于 1 的 `execution.maxConcurrency`；
- 无依赖或依赖已满足的 Agent Node 可有界并行；
- Engine 重启后可以对账多个活动 Attempt，且不重复启动；
- Approval、失败、等待输入和用户取消在多活动节点下有确定语义；
- Direct 与 CC-Panes 继续使用同一个 Backend Contract、状态模型和调度器；
- M2.1.1/M2.1.2 历史状态继续兼容。

本里程碑继续采用 Run 级单 Backend，不支持 Workflow 内混用 Backend。

## 2. 实施原则

1. **测试先行**：每个阶段先建立会失败的并发、恢复或兼容测试，再修改实现。
2. **单一状态所有者**：一个 Run 保持一个逻辑 controller 和串行化提交路径；只并行 Backend I/O。
3. **确定性提交**：并发完成顺序不能改变合法状态、事件序列或最终结论。
4. **持久化后行动**：Start/Cancel 等不可逆操作继续遵循 durable intent/handle 协议。
5. **不按 Backend 名称分支**：差异只能来自 Backend Contract、Capabilities 和返回值。
6. **兼容优先**：旧 Run 的读取、恢复和取消能力不得因多活动状态模型退化。
7. **保守结论**：无法确认执行或取消状态时进入 waiting/indeterminate，不伪造成功或 cancelled。
8. **Plugin-ready**：保持插件化边界，但不增加 SDK、发现、加载、签名或分发代码。

## 3. 预期代码布局

实现过程中允许根据职责拆分当前较大的 `run/service.go`，但不要求为了形式重构所有旧代码。建议边界：

```text
wf-engine/internal/workflow/
  model.go                 # maxConcurrency schema and limits
  validate.go              # concurrency validation

wf-engine/internal/backend/
  backend.go               # minimal scheduling capabilities
  contracttest/            # expanded concurrency/cancel contract tests

wf-engine/internal/run/
  lifecycle.go             # multi-active status and schema compatibility
  scheduler.go             # ready-set and capacity planning
  reconcile.go             # multi-Attempt observation/reduction
  cancellation.go          # multi-Attempt cancel aggregation
  service.go               # RPC-facing orchestration service

wf-engine/internal/integration/
  parallel_workflow_test.go
  parallel_recovery_test.go
  parallel_cancel_test.go

wf/src/
  bridge/types.ts          # active Nodes/Attempts and capability types
  bridge/engine.ts         # unchanged protocol boundary where possible
  tui/ and commands/       # parallel status rendering
```

文件拆分必须保持行为性提交可审查；不得先做大规模无行为重排再补测试。

## 4. 阶段 0：并发语义基线

### 4.1 目标

在改变状态结构前固定 M2.1.2 行为，并把批准的并发语义写成可执行测试。

### 4.2 工作项

1. 保存新的 M2.1.2 状态 fixture，覆盖：
   - 单活动 Agent；
   - Approval waiting；
   - Agent waiting input/result pending；
   - cancel pending/cancel failed；
   - 已完成和 indeterminate Attempt。
2. 新增失败测试，描述：
   - `maxConcurrency: 2` 当前被拒绝；
   - 两个独立 ready Agent 必须重叠执行；
   - `maxConcurrency: 1` 必须保持确定性顺序；
   - 一个 Agent 失败后停止新调度，但已运行兄弟节点继续被对账；
   - 多活动节点取消必须全部确认。
3. 为测试 Backend 增加可控 barrier、开始时间、完成顺序、取消结果和重复 Start 计数。
4. 固化当前双 Backend contract suite 作为回归基线。

### 4.3 验证

```powershell
go test ./internal/workflow ./internal/run ./internal/backend/contracttest ./internal/integration
go test -race ./internal/run ./internal/integration   # Ubuntu/WSL gate
```

新增并发测试在实现前应按预期失败；现有单并发测试必须继续通过。

### 4.4 建议提交

```text
test: 固化 M2.2 并发调度语义
```

## 5. 阶段 1：多活动状态与兼容读取

### 5.1 目标

移除“一个 Run 只能有一个活动节点”的持久化假设，同时保留旧状态读取路径。

### 5.2 工作项

1. 将活动 Node/Attempt 集合作为 Node/Attempt 状态的派生结果，避免新增第二个相互矛盾的权威状态源。
2. 为 Status/RPC 增加集合形式：
   - active Node summaries；
   - active Attempt summaries；
   - waiting Approval summaries；
   - per-Node diagnostic/reason。
3. 保留旧 `activeNodeId` 和单个 `activeAttempt` 的兼容读取；只有一个活动节点时可继续填充兼容字段。
4. 如果持久化 shape 或语义发生变化，将 `stateSchemaVersion` 升级为 2，并实现：
   - v1 → v2 内存规范化；
   - v1 文件不被破坏性原地迁移；
   - M2.1.1 legacy execution handle 继续解码。
5. 扩展 snapshot validation，拒绝重复活动 Attempt、Node/Attempt 状态冲突和 Backend 身份不一致。
6. 更新 TypeScript 类型，但暂不改变最终 UI 布局。

### 5.3 验证

- 所有历史 fixture 可读取；
- 单活动状态对旧 CLI 输出保持兼容；
- 多活动状态不会丢失任一 Attempt；
- malformed snapshot 被明确拒绝；
- 不自动删除或覆盖旧状态。

### 5.4 建议提交

```text
feat: 支持多活动节点状态模型
test: 保持 M2 历史运行兼容
```

## 6. 阶段 2：纯调度决策与资源容量

### 6.1 目标

先把“哪些节点可启动、还能启动几个”实现为无 Backend I/O 的确定性决策，再接入控制循环。

### 6.2 工作项

1. 将 `scheduleOne` 中的以下逻辑提取为可单测 reducer/planner：
   - 依赖稳定性；
   - upstream failure；
   - `when` 条件；
   - Approval readiness；
   - Agent ready set；
   - Workflow 是否已经完成。
2. ready set 必须沿规范化拓扑顺序稳定排序。
3. 计算有效容量：

   ```text
   effective = min(workflow maxConcurrency, Backend limit, Fishyume safety ceiling)
   available = effective - active Agent Attempt count
   ```

4. `maxConcurrency: 1` 必须生成与 M2.1 相同的启动顺序。
5. 失败后进入停止新调度状态：
   - 已运行兄弟节点继续保留；
   - failed Node 的 descendants 标记 `upstream_failed`；
   - 只有显式可达的失败处理分支仍可进入 ready set；
   - 不隐式 Cancel 活动兄弟节点。
6. Approval 不占 Agent 槽位，但可以阻塞其依赖分支。

### 6.3 验证

用表驱动测试覆盖 ready set、容量、条件分支、失败分支、Approval 和完成聚合。相同输入重复运行必须得到相同决策顺序。

### 6.4 建议提交

```text
refactor: 提取确定性并发调度决策
```

## 7. 阶段 3：多 Attempt 恢复与对账

### 7.1 目标

让 controller 可以发现并恢复多个已经持久化的活动 Attempt，暂时仍可限制新启动为单个，先证明恢复模型正确。

### 7.2 工作项

1. 用 `findActiveAttempts` 替代 `findActiveNode` 单值路径。
2. 对每个活动 Attempt：
   - 按其持久化 Backend 获取 Adapter；
   - 解码 opaque execution handle；
   - 执行 Observe/Output；
   - 保留 Attempt number、controller generation 和 resultConsumed 检查。
3. Backend I/O 可有界并发，但状态提交必须通过单一 reducer/commit path。
4. 同一轮多个观察结果按稳定 Node 顺序提交，保持严格递增事件序列。
5. 一个 Attempt waiting 不得阻止其他活动 Attempt 被继续对账。
6. Engine crash/restart 后不得因为某个 Attempt 已完成而重新 Start 另一个仍活动的 Attempt。

### 7.3 验证

- 两个预置活动 Attempt 在新 Service 中均被观察；
- Start 计数保持为原值；
- 一个 terminal、一个 active 时状态正确；
- 观察返回顺序反转不会改变最终快照；
- stale controller/generation 结果不能写入状态。

### 7.4 建议提交

```text
feat: 支持多个活动 Attempt 恢复对账
```

## 8. 阶段 4：有界并行启动

### 8.1 目标

在已验证的状态和恢复模型上启用多个 ready Agent 的并发 Start。

### 8.2 工作项

1. Workflow 校验接受范围内的 `maxConcurrency >= 1`，并给出明确上限错误。
2. 根据阶段 2 的调度计划批量创建 prepared Attempt，先持久化所有启动意图。
3. 在容量范围内并行调用 Backend Start。
4. 每个 Start 独立执行现有 durable launch protocol：
   - prepared；
   - dispatching；
   - handle persisted；
   - finished without handle。
5. 启动结果只允许更新对应 Run/Node/Attempt/generation。
6. 部分启动失败不得丢失其他已启动 handle，也不得超发新 Attempt。
7. controller 每轮继续补满剩余容量，而不是等待所有同批 Attempt 同时完成。

### 8.3 验证

- 两个 barrier Agent 的运行区间真实重叠；
- 活动数从不超过有效容量；
- 一个 Start 失败时另一个 handle 仍可恢复；
- Engine 在 Start 返回前崩溃时不重复启动；
- `maxConcurrency: 1` 全量回归一致。

### 8.4 建议提交

```text
feat: 增加有界并行 Agent 调度
```

## 9. 阶段 5：Run 聚合、Approval 与失败排空

### 9.1 目标

用全部 Node 状态计算 Run phase/conclusion，消除单个活动节点直接控制 Run 状态的假设。

### 9.2 工作项

1. 建立 Run aggregation reducer，至少区分：
   - active Agent；
   - ready work；
   - waiting Approval；
   - Agent waiting input/result pending；
   - draining after failure；
   - terminal aggregate conclusion。
2. Run phase 规则：
   - 存在活动 Agent 或可启动节点时为 running；
   - 无可执行工作但存在人工决策时为 waiting/approval_required；
   - 只有等待中的 Agent 执行时，保留可操作的 waiting reason 和 per-Node 明细；
   - 所有活动状态明确后才形成 terminal conclusion。
3. 多个 Approval 可同时等待，resume action 必须指定 Node ID 且幂等。
4. 实现已批准失败策略：停止新调度、排空活动兄弟节点、不隐式取消。
5. 保持 rejection branch、condition false 和 upstream failure 的现有语义。

### 9.3 验证

- 一个 Approval waiting、一个 Agent running 时 Run 仍为 running；
- Agent 完成后 Run 转为 approval_required；
- 两个 Approval 可分别决定；
- 一个 Agent 失败、兄弟 Agent 继续完成；
- 未启动 descendants 正确 skipped；
- 完成顺序不同不改变聚合结论。

### 9.4 建议提交

```text
feat: 聚合并行节点生命周期状态
```

## 10. 阶段 6：并发取消与部分失败恢复

### 10.1 目标

让用户取消覆盖完整活动集合，并保持“没有全部确认就不能报告 cancelled”的安全原则。

### 10.2 工作项

1. cancel intent 持久化完整目标 Attempt 集合或可稳定重建的取消代次。
2. 对活动 execution handle 发起有界并发 Cancel。
3. 每个 Attempt 分别记录：
   - already terminal；
   - confirmed cancelled；
   - not confirmed；
   - transport/adapter error；
   - missing/indeterminate handle。
4. 只有全部目标终态或确认取消时，Run 才进入 `completed/cancelled`。
5. 任一未确认时进入 `waiting/cancel_failed`，保留未解决 Node 列表和诊断。
6. 重复 cancel 只重试未解决目标，不重复取消已确认目标。
7. cancellation monitor、外部 cancel 命令和 controller generation 必须保持幂等。

### 10.3 验证

- 两个活动 execution 都收到 Cancel；
- 一个确认、一个不确认时 Run 不得 cancelled；
- 第二次 cancel 只重试未确认 execution；
- Engine 在部分取消后重启可继续；
- CC-Panes/Direct/Fake 对取消结果使用相同聚合逻辑。

### 10.4 建议提交

```text
feat: 支持并行执行的确认式取消
```

## 11. 阶段 7：Backend 能力与资源约束

### 11.1 目标

只增加 M2.2 调度必须的最小能力声明，不设计插件 ABI。

### 11.2 工作项

1. 扩展 `backend.Capabilities`，表达每 Run 并发上限及必要的恢复/取消保证。
2. 明确 `0`、缺失值和安全上限的兼容语义。
3. StartWorkflow 前验证 Workflow 并发请求是否可由 Backend 支持，或计算并明确展示被限制后的有效容量。
4. CC-Panes 和 Direct 通过各自 Adapter 返回能力，不把 session/process 细节泄漏给 scheduler。
5. 扩展 contract suite，确保能力声明和实际 Start/Cancel 行为一致。
6. 能力字段需要持久化时保存版本化规范值，不保存动态内部配额对象。

### 11.3 验证

- Backend limit 小于 Workflow limit 时不超发；
- 非法或矛盾 capability 被拒绝；
- scheduler 包中无 `ccpanes`/`direct` 名称判断；
- 两个 Backend 通过同一 contract suite。

### 11.4 建议提交

```text
feat: 增加 Backend 并发能力约束
```

## 12. 阶段 8：CLI/TUI 与诊断

### 12.1 目标

让用户能看清多个活动、等待和取消失败节点，而不需要读取原始状态文件。

### 12.2 工作项

1. JSON status 输出 active Nodes/Attempts、waiting Approvals 和 per-Node diagnostics。
2. 文本/TUI 状态按稳定顺序展示：
   - running；
   - waiting；
   - ready/pending；
   - completed/skipped。
3. 单活动 Workflow 保持简洁输出和旧字段兼容。
4. cancel failed 输出明确列出未确认的 Node，而不是只显示一个全局字符串。
5. README 和示例增加最小并行 Workflow，不引入插件文档。

### 12.3 验证

```powershell
npm --prefix wf run typecheck
npm --prefix wf test
npm --prefix wf run build
```

增加 JSON snapshot 和终端渲染测试，避免完成顺序造成列表抖动。

### 12.4 建议提交

```text
feat: 展示并行 Workflow 执行状态
```

## 13. 阶段 9：双 Backend 集成与实机收口

### 13.1 自动化场景

同一个并行 Workflow 至少覆盖：

1. 两个独立 Agent 同时运行，后续汇合到 Approval/Agent；
2. Engine 在两个 Agent 活动时退出，新 Engine 无重复 Start 地恢复；
3. 一个 Agent 先完成、另一个后完成；
4. 一个失败、另一个排空完成；
5. 一个 waiting input、另一个继续推进；
6. 用户同时取消两个活动 Agent；
7. 部分取消未确认后重试；
8. `maxConcurrency: 1` 兼容运行。

### 13.2 全量验证

```powershell
go test ./...
go vet ./...
go build ./cmd/fishyume-engine
npm --prefix wf run typecheck
npm --prefix wf test
npm --prefix wf run build
```

Ubuntu CI additionally runs:

```bash
go test -race ./...
```

Windows 与 Ubuntu 均执行平台包安装验证，防止并发实现引入构建或进程生命周期回退。

### 13.3 Live smoke

分别用 Direct 和 CC-Panes 执行同一份 `maxConcurrency: 2` Workflow，记录：

- Run ID、Backend、有效容量；
- 两个 Attempt 的开始/结束时间，证明真实重叠；
- Approval 跨 Engine resume；
- crash recovery 前后 Attempt 数量不变；
- 并发 cancel 的逐 Attempt 确认结果；
- 无遗留进程、session 或临时状态目录。

CC-Panes 使用专用非交互式 Profile；验证不得依赖人工 MCP allow。

### 13.4 建议提交

```text
test: 验证双后端并行工作流一致性
docs: 记录 M2.2 实机验证证据
```

## 14. 测试矩阵

| 场景 | Pure scheduler | Fake Backend | CC-Panes fixture | Direct fixture | Live |
|---|---:|---:|---:|---:|---:|
| maxConcurrency 限制 | 是 | 是 | 间接 | 间接 | 是 |
| 两 Agent 重叠 | 否 | 是 | 是 | 是 | 是 |
| 多活动 crash recovery | 否 | 是 | 是 | 是 | 是 |
| 完成顺序确定性 | 是 | 是 | 是 | 是 | 可选 |
| Approval 与 Agent 共存 | 是 | 是 | 是 | 是 | 是 |
| 失败停止调度并排空 | 是 | 是 | 是 | 是 | 可选 |
| 全量取消确认 | 否 | 是 | 是 | 是 | 是 |
| 部分取消失败重试 | 否 | 是 | 是 | 是 | 可控时 |
| M2.1 历史兼容 | 否 | 是 | 间接 | 间接 | 否 |
| 无 Backend 名称分支 | 静态检查 | 是 | 是 | 是 | 否 |

公共 CI 不依赖真实 Codex 认证或已注册 CC-Panes 项目。Live smoke 是受控发布 gate。

## 15. 代码审查硬门槛

每个阶段都检查：

1. 是否把 Backend I/O 放在全局状态锁内。
2. 是否存在 `if backend.Name() == ...` 或等价平台分支。
3. 是否可能在容量已满时创建新 Attempt。
4. 是否在持久化 prepared Attempt 前调用 Backend Start。
5. 是否可能由 stale controller/generation 写入结果。
6. 是否把并发完成顺序当作业务顺序。
7. 是否因为一个 waiting Node 停止观察其他活动 Node。
8. 是否在未全部确认时报告 Run cancelled。
9. 是否因节点失败隐式强杀独立兄弟节点。
10. 是否破坏 `maxConcurrency: 1` 行为。
11. 是否破坏 M2.1.1/M2.1.2 状态读取、恢复或取消。
12. 是否把 Backend 内部 session/process 数据提升为核心字段。
13. 是否混入插件 SDK、热加载或第三方分发设计。

任一硬门槛不满足，不进入下一阶段。

## 16. 里程碑完成定义

只有以下证据全部存在，M2.2 才能收口：

- 架构文档中的并发、Approval、失败和取消语义都有自动化测试；
- `maxConcurrency: 1` 全量兼容测试通过；
- 多活动 Attempt 崩溃恢复不产生重复 Start；
- Direct 与 CC-Panes 通过扩展后的 Backend contract suite；
- 同一并行 Workflow 在两个真实 Backend 上完成；
- 两个真实 Backend 均验证并发取消确认语义；
- M2.1.1/M2.1.2 历史状态兼容测试通过；
- Go test/vet/build、TypeScript typecheck/test/build 全部通过；
- Ubuntu race、Windows/Ubuntu CI 和平台安装验证通过；
- 调度器中没有 Backend 名称分支；
- 工作区干净，无遗留进程、session 和临时验证状态；
- 代码、测试、文档和 live evidence 已 commit 并 push。

完成 M2.2 后，再单独规划 M3 的自动重试、超时、fallback、预算策略和更完整可观测性。第三方 Backend 插件生态仍需等待核心契约进一步稳定。
