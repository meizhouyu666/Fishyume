# Fishyume

Fishyume 是一个面向 Codex、Claude、Kimi、OpenCode 等 Host Agent 的本地、可恢复 AI Agent 编排控制面。Host Agent 通过 MCP 或 Machine CLI 编排由 Agent 与人工审批组成的 DAG；人类通过 TUI 观察、审批、取消、重试和恢复。Fishyume 不是通用工作流编辑器，也不在 Core 中实现聊天 Agent 或模型 Tool loop。

当前 `0.2.1-alpha.1` 已完成 M4.0 + M4.1：TypeScript CLI 仍以前台 stdio 方式启动 Go Engine，但 Node Attempt 已迁移到 Agent Driver、Headless Protocol v1 与 Context Compiler v1。新 Run 只使用无头、非交互、one-shot 的 Codex Driver；CC-Panes 已退出新 Run，仅保留历史 snapshot 读取和不可恢复诊断。常驻 Control Plane/IPC（M4.2）与 Agent-native MCP（M4.3）尚未实现。

当前版本：`0.2.1-alpha.1`

## 核心能力

- Agent、Approval 节点及显式依赖关系
- 输入、条件分支和受限模板变量
- Run、Node、Attempt 持久化与跨进程恢复
- 崩溃接管、取消和执行进程身份校验
- 确定性、有界的并行 Agent 调度与多 Attempt 恢复
- 失败后停止新调度并排空活动兄弟；显式取消要求逐 Attempt 确认
- Agent Driver Registry、能力检查和 Doctor 诊断
- 确定性 Context Compiler v1、Attempt Envelope、Driver Event/Observation、结构化 Agent Result（含 `needs_input`）
- Calm Operator Console：以 Workflow 和单一焦点节点为主体，覆盖并行 Attempt、Approval、显式 retry/cancel、诊断与终态汇总
- `fishyume` 主命令及兼容别名 `wf`

`execution.maxConcurrency` 可设为 `1..32`；实际并发取 Workflow 请求、Driver 每 Run 限制和 Fishyume 安全上限的最小值。`maxConcurrency: 1` 保持原有确定性顺序。

## 当前 Agent Driver

正式新语义是 `agent.driver/target`，当前只提供 `codex + local`。Driver 将确定性的 Attempt Envelope 转换为 `codex exec --ephemeral --json` 调用，并保持进程指纹、PID 复用防护、结果 hash、有界日志、崩溃对账和明确取消确认：

- 已安装并认证 Codex CLI；本里程碑实机验证版本为 `codex-cli 0.144.6`
- `FISHYUME_CODEX_PATH` 可显式指定原生 Codex 可执行文件；通常可自动解析 npm shim 对应的原生二进制
- `FISHYUME_DIRECT_SANDBOX` 可设为 `read-only`、`workspace-write` 或 `danger-full-access`，默认 `workspace-write`

```powershell
fishyume doctor --driver codex --project "E:\project"
fishyume run --driver codex --target local --project "E:\project" "实现指定需求"
```

旧 `--backend/--tool/--runtime`、Workflow `defaults.backend/tool/runtime` 与 `FISHYUME_BACKEND` 仍在兼容窗口读取并输出 deprecation warning；`direct` 会归一化为 `codex`。新状态只写 `resolvedDriver`、`resolvedTarget`、Driver Handle、Context manifest/version/hash，不写 CC-Panes Profile、TaskBinding 或 Session 身份。

## 构建与验证

源码构建需要 Go 1.26+ 和 Node.js 24+。

```powershell
cd wf-engine
go test ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm ci
npm run verify
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
  agent:
    driver: codex
    target: local

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

CLI `--driver/--target` 可以覆盖示例中的 `defaults.agent`：

```powershell
fishyume run --workflow .\workflow.yaml --driver codex --target local --project "E:\project" --input goal="实现新功能"

fishyume status <run-id>
fishyume status <run-id> --watch
fishyume resume <run-id> --approve approve
fishyume resume <run-id> --reject approve --reason "需要调整方案"
fishyume resume <run-id> --retry implement
fishyume cancel <run-id>
```

## 终端体验

交互式终端中的 `fishyume run` 使用 Calm Operator Console：Header 展示完整 Run 状态，紧凑 Workflow 行使用符号与短标签，当前焦点节点集中展示 Attempt、Approval、result 和 diagnostic 详情，底部只保留非零状态汇总与当前有效快捷键。界面不再为 Attempts、Approvals、Diagnostics 分别重复绘制 Panel，并在 80/120/160 columns 下保持有界、稳定和 scrollback 友好。`fishyume status <run-id> --watch` 可重新进入同一 Console；终态停止轮询并明确显示 summary、state 与下一步命令。

Console 以 `j`/`k` 或上下方向键遍历全部 Workflow 节点，`Enter` 折叠或展开焦点详情；action key 只对当前由 Engine 判定为 actionable 的选中节点显示并生效。`a` approve，`r` 输入 reject reason，`R` 确认 retry，`c` 确认 cancel，`?` 展开帮助，`Esc` 放弃当前输入或确认。action mode 继续按 `nodeId/kind/duplicateRisk` 固定目标；indeterminate retry 会明确显示 duplicate-risk，并只在确认后提交风险确认。`d`、`q` 或 `Ctrl+C` 安全离开：从 `run` 离开会 detach，从 `status --watch` 离开只停止观察，二者都不会隐式 cancel。

非 TTY 或 CI 环境继续输出可流式处理的逐行纯文本；这些环境中的 `status --watch` 会返回诊断并建议使用普通 `status`，不会进入无限输出。`--watch --json` 会被拒绝，`fishyume status --json` 仍只输出一个 JSON 对象。`NO_COLOR` 会保留 TUI 结构并关闭颜色，TrueColor 不可用时自动降级到 256/16 色或单色；`TERM=dumb` 或 `FISHYUME_ASCII=1` 使用 ASCII 状态与 Divider fallback。实现与验收矩阵见 [`docs/fishyume-m3-tui-productization.md`](./docs/fishyume-m3-tui-productization.md)、[`docs/fishyume-m3.2-interactive-run-console.md`](./docs/fishyume-m3.2-interactive-run-console.md) 与 [`docs/fishyume-m3.3-calm-operator-console.md`](./docs/fishyume-m3.3-calm-operator-console.md)。六个确定性场景的可审阅输出见 [`docs/fishyume-m3.3-canonical-gallery.txt`](./docs/fishyume-m3.3-canonical-gallery.txt)。

默认状态目录：

- Windows：`%LOCALAPPDATA%\fishyume`
- Linux：`$XDG_STATE_HOME/fishyume` 或 `~/.local/state/fishyume`
- macOS：`~/Library/Application Support/fishyume`

## 当前边界

当前仍不支持本地常驻 Control Plane、正式 MCP 产品面、通用 Shell/HTTP/容器节点、模型回退或动态节点。M4.1 不包含 Web/Desktop、Memory、模型路由、Prompt Library、Native Harness、Claude Driver 或第三方 Driver SDK；动态发现与运行时热加载后置到核心控制面稳定之后。

## M4：Agent-Native Control Plane

M4 已批准。M4.0 + M4.1 已完成合同冻结、Codex Driver、Context Compiler 和 CC-Panes 新 Run 退役；后续 M4.2 + M4.3 才会加入常驻服务、IPC 与 MCP。完整 M4 包括：

- 本地常驻 Control Plane 与 Named Pipe/Unix Domain Socket；
- Headless Agent Process Protocol v1；
- Codex Agent Driver 与 CC-Panes 新 Run 退役；
- 确定性 Context Compiler 骨架；
- `capabilities`、Workflow validate/explain、Run list/get/events/action/result；
- 幂等调用、跨进程动作和崩溃恢复。

正式架构见 [`docs/fishyume-m4-agent-native-control-plane.md`](./docs/fishyume-m4-agent-native-control-plane.md)，分批实施与门禁见 [`docs/fishyume-m4-implementation-plan.md`](./docs/fishyume-m4-implementation-plan.md)。

更完整的需求、架构和里程碑说明见 [`docs/`](./docs/)。
