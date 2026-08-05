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
- 面向并行 Workflow 的交互式 Ink Run Console，覆盖活动 Attempt、Approval、显式 retry/cancel、诊断与汇总
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
fishyume status <run-id> --watch
fishyume resume <run-id> --approve approve
fishyume resume <run-id> --reject approve --reason "需要调整方案"
fishyume resume <run-id> --retry implement
fishyume cancel <run-id>
```

## 终端体验

交互式终端中的 `fishyume run` 使用正式 Ink Run Console：固定状态标记不依赖颜色，并在 80/120/160 columns 下分别采用窄、中、宽布局。并行 Attempt、审批、诊断、进度和最终摘要均在同一稳定视图中呈现。`fishyume status <run-id> --watch` 可重新进入同一 Console；终态停止轮询但保留最终视图，直到用户退出。

Console 以 `j`/`k` 或上下方向键选择可操作节点；`a` approve，`r` 输入 reject reason，`R` 确认 retry，`c` 确认 cancel，`?` 展开帮助，`Esc` 放弃当前输入或确认。indeterminate retry 会额外显示 duplicate-risk，并只在明确确认后提交风险确认。`d`、`q` 或 `Ctrl+C` 安全离开：从 `run` 离开会 detach，从 `status --watch` 离开只停止观察，二者都不会隐式 cancel。

非 TTY 或 CI 环境继续输出可流式处理的逐行纯文本；这些环境中的 `status --watch` 会返回诊断并建议使用普通 `status`，不会进入无限输出。`--watch --json` 会被拒绝，`fishyume status --json` 仍只输出一个 JSON 对象。`NO_COLOR` 会保留 TUI 结构并关闭颜色，TrueColor 不可用时自动降级到 256/16 色或单色。实现与验收矩阵见 [`docs/fishyume-m3-tui-productization.md`](./docs/fishyume-m3-tui-productization.md) 与 [`docs/fishyume-m3.2-interactive-run-console.md`](./docs/fishyume-m3.2-interactive-run-console.md)。

默认状态目录：

- Windows：`%LOCALAPPDATA%\fishyume`
- Linux：`$XDG_STATE_HOME/fishyume` 或 `~/.local/state/fishyume`
- macOS：`~/Library/Application Support/fishyume`

## 当前边界

当前仍不支持单个 Workflow 混用 Backend、通用 Shell/HTTP/容器节点、自动重试、模型回退或动态节点。Backend 契约和 Registry 已从核心编排逻辑中独立，但第三方插件 SDK、动态发现与运行时热加载尚未提供。M3 CLI/TUI 产品面不包含 Web/Desktop、模型路由或 Prompt Library。

更完整的需求、架构和里程碑说明见 [`docs/`](./docs/)。
