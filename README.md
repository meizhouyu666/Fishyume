# Fishyume

Fishyume 是一个平台无关、可恢复的 AI Agent 编排引擎。它使用 YAML 或 JSON 描述由 Agent 与人工审批组成的 DAG 工作流，通过 TypeScript CLI 调用 Go Engine 执行，并把 Run、Node、Attempt 和 Backend 身份持久化下来。

CC-Panes 是默认 Agent Backend，不是 Fishyume 的架构边界。当前还内置 Direct Codex CLI Backend；两者实现同一套启动、观察、输出、取消和恢复契约。

当前版本：`0.2.1-alpha.1`

## 核心能力

- Agent、Approval 节点及显式依赖关系
- 输入、条件分支和受限模板变量
- Run、Node、Attempt 持久化与跨进程恢复
- 崩溃接管、取消和 Backend 进程身份校验
- 确定性、有界的并行 Agent 调度与多 Attempt 恢复
- 失败后停止新调度并排空活动兄弟；显式取消要求逐 Attempt 确认
- Backend Registry、能力检查和 Doctor 诊断
- `fishyume` 主命令及兼容别名 `wf`

`execution.maxConcurrency` 可设为 `1..32`；实际并发取 Workflow 请求、Backend 每 Run 限制和 Fishyume 安全上限的最小值。`maxConcurrency: 1` 保持原有确定性顺序。

## Agent Backend

Backend 选择优先级为：CLI `--backend`、Workflow `defaults.backend`、`FISHYUME_BACKEND`、默认 `ccpanes`。选定结果会固化到 Workflow、Run 和 Attempt，恢复或取消不会被后续环境变量改变。

### CC-Panes（默认）

需要：

- CC-Panes release orchestrator 与 daemon 已就绪
- 项目已注册到 CC-Panes
- 已创建专用于 Fishyume 的非交互式启动 Profile
- `FISHYUME_CCPANES_PROFILE_ID` 指向该 Profile

### Direct Codex CLI

Direct Backend 不经过 CC-Panes，当前支持 Windows/Linux 上的 `codex + local`：

- 已安装并认证 Codex CLI；本里程碑实机验证版本为 `codex-cli 0.144.6`
- `FISHYUME_CODEX_PATH` 可显式指定原生 Codex 可执行文件；通常可自动解析 npm shim 对应的原生二进制
- `FISHYUME_DIRECT_SANDBOX` 可设为 `read-only`、`workspace-write` 或 `danger-full-access`，默认 `workspace-write`

```powershell
fishyume doctor --backend direct --project "E:\project"
fishyume run --backend direct --project "E:\project" "实现指定需求"
```

## 构建与验证

源码构建需要 Go 1.26+ 和 Node.js 24+。

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
```

开发 checkout 可显式设置 Engine：

```powershell
$env:FISHYUME_ENGINE_PATH = "E:\path\to\Fishyume\wf-engine\wf-engine.exe"
```

## 工作流示例

```yaml
apiVersion: fishyume/v1
name: implement-with-approval

inputs:
  goal:
    required: true

defaults:
  backend: direct
  tool: codex
  runtime: local

execution:
  maxConcurrency: 2

nodes:
  plan:
    type: agent
    task: "为 {{ inputs.goal }} 制定实现方案"

  research:
    type: agent
    task: "并行分析 {{ inputs.goal }} 的风险与验证点"

  approve:
    type: approval
    dependsOn: [plan]
    prompt: "是否批准方案：{{ nodes.plan.result.summary }}"

  implement:
    type: agent
    dependsOn: [approve, research]
    when:
      node: approve
      field: result.decision
      equals: approved
    task: "执行已批准的方案：{{ nodes.plan.result.summary }}；验证：{{ nodes.research.result.summary }}"
```

CLI `--backend` 可以覆盖示例中的 `defaults.backend`：

```powershell
fishyume run --workflow .\workflow.yaml --backend direct --project "E:\project" --input goal="实现新功能"

fishyume status <run-id>
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

M2.2 仍不支持单个 Workflow 混用 Backend、通用 Shell/HTTP/容器节点、自动重试、模型回退或动态节点。Backend 契约和 Registry 已从核心编排逻辑中独立，但第三方插件 SDK、动态发现与运行时热加载尚未提供。

更完整的需求、架构和里程碑说明见 [`docs/`](./docs/)。
