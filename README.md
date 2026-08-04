# Fishyume

Fishyume 是一个面向 CC-Panes 的本地优先、可恢复工作流引擎。它使用 YAML 或 JSON 描述由 Agent 与人工审批组成的 DAG 工作流，并通过 TypeScript CLI 调用 Go 引擎执行。

当前版本：`0.2.1-alpha.1`

## 核心功能

- 支持 Agent、Approval 节点及显式依赖关系
- 支持输入、条件分支和受限模板变量
- 持久化 Run、Node、Attempt 状态
- 支持跨进程恢复、取消和崩溃接管
- 通过 CC-Panes 启动并跟踪 Agent 会话
- 保留 `wf` 作为兼容命令别名

当前版本最多同时运行一个 Agent，适合需要明确审批与可恢复执行的本地自动化流程。

## 环境要求

- Go 1.26 或更高版本
- Node.js 24 或更高版本
- 已启动 CC-Panes、orchestrator 和 daemon
- 项目已注册到 CC-Panes
- 已配置供 Fishyume 使用的非交互式启动 Profile

## 构建与验证

```powershell
cd wf-engine
go test ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm install
npm run typecheck
npm test
npm run build
```

本地使用时设置 Engine 路径和 CC-Panes Profile：

```powershell
$env:FISHYUME_ENGINE_PATH = "E:\path\to\Fishyume\wf-engine\wf-engine.exe"
$env:FISHYUME_CCPANES_PROFILE_ID = "<profile-id>"

fishyume doctor --project "E:\registered-project"
```

## 工作流示例

```yaml
apiVersion: fishyume/v1
name: implement-with-approval

inputs:
  goal:
    required: true

execution:
  maxConcurrency: 1

nodes:
  plan:
    type: agent
    task: "为 {{ inputs.goal }} 制定实现方案"

  approve:
    type: approval
    dependsOn: [plan]
    prompt: "是否批准方案：{{ nodes.plan.result.summary }}"

  implement:
    type: agent
    dependsOn: [approve]
    when:
      node: approve
      field: result.decision
      equals: approved
    task: "执行已批准的方案：{{ nodes.plan.result.summary }}"
```

## 常用命令

```powershell
# 执行单个 Agent 任务
fishyume run --project "E:\registered-project" "实现指定需求"

# 执行工作流
fishyume run --workflow .\workflow.yaml --project "E:\registered-project" --input goal="实现新功能"

# 查看、恢复或取消 Run
fishyume status <run-id>
fishyume resume <run-id>
fishyume resume <run-id> --approve approve
fishyume resume <run-id> --reject approve --reason "需要调整方案"
fishyume resume <run-id> --retry implement
fishyume cancel <run-id>
```

默认状态目录：

- Windows：`%LOCALAPPDATA%\fishyume`
- Linux：`$XDG_STATE_HOME/fishyume` 或 `~/.local/state/fishyume`
- macOS：`~/Library/Application Support/fishyume`

## 当前边界

M2.1.1 暂不支持并行 Agent、自动重试、模型回退、动态节点、守护进程或任意表达式执行。并行调度与聚合取消计划在后续版本实现。

更完整的需求、架构和里程碑说明见 [`docs/`](./docs/)。
