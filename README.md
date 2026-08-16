# Fishyume

Fishyume 是一个面向 Codex、Claude、Kimi、OpenCode 等 Host Agent 的本地、可恢复 AI Agent 编排控制面。Host Agent 通过 MCP 或 Machine CLI 调用同一套 Application API，创建和管理由 Agent 与人工审批组成的 DAG；人类可随时用 TUI attach 到同一个持久化 Run，观察或提交审批、取消和重试。执行侧由 headless Agent Driver 负责，当前正式组合是 `codex + local`。Fishyume 的新 Run 与 CC-Panes 无关，也不在 Core 中实现聊天 Agent 或模型 Tool loop。

当前 `0.2.1-alpha.1` 已完成 M4 Agent-Native Control Plane：Agent Driver/Context Compiler、用户级 Local Control Plane、统一 Application Service、持久化 start/action 幂等、MCP、Machine CLI、`fishyume attach` 和 TUI 的 `run.get/run.action` 迁移。Windows 使用 Named Pipe，Linux/macOS 使用 Unix Domain Socket；直接启动 `wf-engine` 时仍保留 stdio JSON-RPC，供测试和受控嵌入使用。M4.4 的产品/迁移表面、真实 Codex Driver 单节点与并行 Workflow、真实 Host MCP、Host + rendered PTY/TUI 冲突收敛，以及 Provider-independent Control Plane crash/restart 验收均已通过；独立审查 P0/P1/P2 为 `0/0/0`，公开 CI 六项全绿。M4 已技术收口，M4.5 Developer Preview 产品体验门禁已于 2026-08-14 验收通过；尚未执行版本发布或 GitHub Release。

当前版本：`0.2.1-alpha.1`

## 首次使用黄金链路

当前尚未发布 npm 正式包。Windows Developer Preview 可从仓库根目录一条命令安装 CLI 与当前源码构建的平台 Engine：

```powershell
.\install-fishyume.ps1 -Proxy "http://127.0.0.1:7897"  # 仅在当前网络需要代理时添加 -Proxy
```

安装后只需完成一次 Codex 接入：

```powershell
fishyume setup codex
fishyume doctor --project "E:\project"
```

`setup codex` 是用户对本地 Fishyume MCP 的显式授权点：它通过 Codex CLI 注册 canonical stdio transport，并只在 Fishyume 自己的配置 section 中为九个 Application 工具写入 unattended approval，避免每次调用出现人工 MCP allow；不会修改其他 MCP、Provider 或认证信息。

重启 Codex 后，用户与 Codex 讨论目标并声明使用 Fishyume；Codex 通过 MCP 获取 capabilities、编排并启动 Workflow。用户在另一个终端直接运行：

```powershell
fishyume
```

零参数命令打开 Run Dashboard：方向键或 `j`/`k` 选择 Run，`Enter` attach，随后可观察并行节点、审批、回答 `needs_input`、重试、取消或安全 detach。正常链路不要求用户手写 Workflow、复制 Run ID、提供 profile ID 或处理逐次 MCP allow。完整范围与验收见 [`docs/fishyume-m4.5-developer-preview.md`](./docs/fishyume-m4.5-developer-preview.md)。

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
- 用户级 Local Control Plane、跨客户端状态共享、崩溃重启对账与串行 mutation
- Agent-native capabilities、Workflow validate/explain、Run start/list/get/events/action/result
- 持久化 request/action journal、稳定分页/byte bounds 与 MCP/Machine JSON parity
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

旧 `--backend/--tool/--runtime`、Workflow `defaults.backend/tool/runtime` 与 `FISHYUME_BACKEND` 仍在兼容窗口读取并输出 deprecation warning；`direct` 会归一化为 `codex`。请将新配置写成 `--driver codex --target local` 或 `defaults.agent.driver/target`；完整映射与冲突规则见 [`docs/fishyume-m4-migration-guide.md`](./docs/fishyume-m4-migration-guide.md)。兼容读取不会删除或重写历史 snapshot；新状态只写 `resolvedDriver`、`resolvedTarget`、Driver Handle、Context manifest/version/hash，不写 CC-Panes Profile、TaskBinding 或 Session 身份。

## Local Control Plane

CLI/TUI 默认读取状态目录中的 `control-plane.json`，校验 Engine/RPC/IPC/state schema、规范化 `stateDir`、owner ID 与 state-dir hash 后连接；不存在或已确认陈旧时会 detached 启动 `wf-engine serve`。Control Plane 用进程生命周期持有 `control-plane.lock`，因此不兼容版本不能并发写同一状态目录。只有取得 owner lock 后才会替换陈旧 endpoint。

Named Pipe 使用当前 Windows 用户 SID 的 ACL；Unix Socket 所在目录为 `0700`、socket 为 `0600`。握手和 JSON-RPC frame 均有大小上限与 deadline，默认不监听 TCP。Control Plane 不因客户端断线退出；`run.start` 返回后 controller 继续运行。服务重启扫描非终态 Run，对已持久化 Attempt 先 Observe/Reconcile，再允许 scheduler 决策。当前 M4.2 服务不启用自动 idle 退出。

多客户端可并发读取；mutation 在 Control Plane 中串行化。Run snapshot 的 `stateVersion` 可作为 `expectedStateVersion` 提交 approve/reject/retry/cancel，陈旧动作返回冲突。TUI 的 `d`、`q` 和 `Ctrl+C` 仅断开观察，不暂停或取消 Run。

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

跨平台可靠性预检、确定性 stress gate、测试生命周期约定和交付策略见 [`docs/fishyume-development.md`](./docs/fishyume-development.md)。

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
fishyume attach <run-id>

# Machine API：每次只输出一个 Application response JSON
fishyume machine system.capabilities --params '{}'
fishyume machine run.get --params '{"runId":"<run-id>"}'

# MCP：通过 stdio 提供同一组 Application tools
fishyume mcp
```

Host Agent MCP smoke（不需要 Provider 登录）可重复验证 capabilities、Workflow 校验/解释、幂等 start、Approval、`needs_input`、events 和最终 result：

```powershell
npm --prefix wf run test:mcp-host
```

该测试使用仓库内 deterministic fake Agent；真实 Codex Provider smoke 仍是独立手动验收，不会退化为 TUI 或人工 MCP allow。流程说明见 [`docs/fishyume-m4-live-smoke.md`](./docs/fishyume-m4-live-smoke.md)。

MCP Host 与 TUI controller 的双客户端验收可重复验证共享状态、陈旧版本动作冲突、detach/close 不取消 Run，以及 Attempt 不重复：

```powershell
npm --prefix wf run test:mcp-tui
```

真实 Codex Driver 的本地单节点验收已使用 `codex-cli 0.147.0` 通过；重复执行要求本机已安装并认证 Codex CLI，并强制使用 headless `--ephemeral --json` 与显式 read-only sandbox。它不属于公共 CI。

## 终端体验

交互式终端中的 `fishyume` Dashboard 与 Run Console 默认使用中文。Header 直接展示任务状态，紧凑 Workflow 行使用符号与中文短标签，当前焦点节点集中展示执行次数、审批说明、结果与诊断详情，底部只保留当前真正可用的操作。等待人工审批、回答或重试时，首屏会出现醒目的下一步提示；打开 Run 时优先聚焦第一个可操作节点。界面在 80/120/160 columns 下保持有界、稳定和 scrollback 友好。`fishyume status <run-id> --watch` 可重新进入同一 Console；终态停止轮询并明确显示任务总结、状态目录与下一步命令。

Console 以 `J`/`K` 或上下方向键遍历全部 Workflow 节点，`Enter` 折叠或展开焦点详情；操作键只对当前由 Engine 判定为 actionable 的选中节点显示并生效。`A/Y` 批准或提交回答，`X/N` 拒绝并填写原因，`T` 确认重试，`C` 明确取消整个任务，`?` 展开中文帮助，`Esc` 放弃当前输入或确认；原有 `a/r/R/c/d/q` 按键继续兼容。answer 绑定 question ID 与 expected Attempt，scalar 或多问题 JSON answers 由 Engine 最终校验。操作模式继续按 `nodeId/kind/duplicateRisk` 固定目标；结果待确认的重试会明确提示重复副作用风险，并只在确认后提交风险确认。`D`、`Q` 或 `Ctrl+C` 只退出观察，不会暂停或取消 Run。

非 TTY 或 CI 环境继续输出可流式处理的逐行纯文本；这些环境中的 `status --watch` 会返回诊断并建议使用普通 `status`，不会进入无限输出。`--watch --json` 会被拒绝，`fishyume status --json` 仍只输出一个 JSON 对象。`NO_COLOR` 会保留 TUI 结构并关闭颜色，TrueColor 不可用时自动降级到 256/16 色或单色；`TERM=dumb` 或 `FISHYUME_ASCII=1` 使用 ASCII 状态与 Divider fallback。实现与验收矩阵见 [`docs/fishyume-m3-tui-productization.md`](./docs/fishyume-m3-tui-productization.md)、[`docs/fishyume-m3.2-interactive-run-console.md`](./docs/fishyume-m3.2-interactive-run-console.md) 与 [`docs/fishyume-m3.3-calm-operator-console.md`](./docs/fishyume-m3.3-calm-operator-console.md)。六个确定性场景的可审阅输出见 [`docs/fishyume-m3.3-canonical-gallery.txt`](./docs/fishyume-m3.3-canonical-gallery.txt)。

默认状态目录：

- Windows：`%LOCALAPPDATA%\fishyume`
- Linux：`$XDG_STATE_HOME/fishyume` 或 `~/.local/state/fishyume`
- macOS：`~/Library/Application Support/fishyume`

## 当前边界

当前仍不支持通用 Shell/HTTP/容器节点、模型回退或动态节点。M4.4 不包含 Web/Desktop、Memory、模型路由、Prompt Library、Native Harness、Claude Driver 或第三方 Driver SDK；动态发现和运行时热加载也不在本阶段范围。真实 Provider 调用只作为显式本地 gate，不进入公共 CI。

## M4：Agent-Native Control Plane

M4.0-M4.4 已完成合同冻结、Codex Driver、Context Compiler、CC-Panes 新 Run 退役、常驻服务/IPC、Agent-native Application API、产品迁移表面与发布验证。真实 Host/PTY、并行 Driver Workflow、Host/TUI stale-action conflict、crash/restart、独立审查和 Windows/Ubuntu CI 均已通过。M4 已作为技术基线收口；真实 Provider crash 记录仍可作为补充证据，但不再阻塞 M4。

- 本地常驻 Control Plane 与 Named Pipe/Unix Domain Socket；
- Headless Agent Process Protocol v1；
- Codex Agent Driver 的真实 headless 执行；
- 确定性 Context Compiler 骨架；
- `capabilities`、Workflow validate/explain、Run list/get/events/action（含 answer）/result；
- 有界事件读取、持久化幂等调用、跨进程动作和崩溃恢复；
- MCP、Machine CLI、`fishyume attach` 与 TUI 共享同一 Application Service。

正式架构见 [`docs/fishyume-m4-agent-native-control-plane.md`](./docs/fishyume-m4-agent-native-control-plane.md)，分批实施与门禁见 [`docs/fishyume-m4-implementation-plan.md`](./docs/fishyume-m4-implementation-plan.md)。
最终自动化、live、审查与 CI 证据见 [`docs/fishyume-release-readiness.md`](./docs/fishyume-release-readiness.md)。M5.0 已冻结 Context Envelope v2、Memory Record v1、稳定限制/错误、golden fixtures 与六类评测基线；M5.1 已实现固定六类 Context Source Registry、project-root 文件边界、显式 dependency isolation 和选定 Memory 生命周期解析，且生产 `context-compiler/v1` 仍保持不变。下一步是 M5.2 Memory Store。总体计划见 [`docs/fishyume-m5-context-engineering-plan.md`](./docs/fishyume-m5-context-engineering-plan.md)，合同细节见 [`docs/fishyume-m5.0-context-contracts.md`](./docs/fishyume-m5.0-context-contracts.md)，M5.1 边界见 [`docs/fishyume-m5.1-context-source-registry.md`](./docs/fishyume-m5.1-context-source-registry.md)。

更完整的需求、架构和里程碑说明见 [`docs/`](./docs/)。
