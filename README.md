# Fishyume

Fishyume 是一个本地、可恢复的 AI Agent 协作与工作流控制台。

它提供两种明确的投入级别：先用只读 Team Panel 让多个模型独立比较方案；任务确定后，再用可恢复 Workflow 组织 Agent 步骤和人工决策。Agent 负责执行，Fishyume 负责调度、持久化、恢复和安全动作。

## 你可以用它做什么

- 把计划、研究、实现、验证等步骤组织成有依赖关系的 Workflow
- 不编写 YAML 或 DAG，直接让两个不同模型给出独立方案
- 保留 Team 的贡献、部分失败和取消证据，同时不创建虚假的 Run
- 并行运行互不依赖的 Agent 节点，再在审批节点汇合
- 在 Agent 等待审批或补充信息时从终端接管
- 在进程崩溃、Control Plane 重启或客户端断开后继续同一个 Run
- 通过 MCP 让 Codex 等 Host Agent 创建和管理 Workflow
- 通过 Machine CLI 在脚本中使用同一套 Application API
- 在运行前查看确定性的路由预览、预算和 fallback 边界

Team Panel 与 TeamSession 正式支持 Codex、Claude Code 和 OpenCode；Workflow Node 当前仍使用 `codex + local`。Fishyume 不直接调用模型 API，也不内置模型 Tool loop：Host Agent 选择可信 Agent Route，外部 Agent Harness 负责模型调用和会话，Fishyume 负责只读编排、持久化、恢复和安全动作。

## 当前状态

当前版本：`0.2.1-alpha.1`。项目处于 Developer Preview 阶段，尚未发布 npm 正式包或 GitHub Release。Windows + Codex 是参考体验，Ubuntu 也有安装和 CI 验证。

## 五分钟开始

### Windows Developer Preview

环境要求：Node.js 24+、Go 1.26+、已安装的 Codex CLI。Claude Code 和 OpenCode 仅在 Team 路由实际使用它们时需要安装。仓库根目录执行：

```powershell
.\install-fishyume.ps1
```

如果 npm 需要代理，可以显式传入：

```powershell
.\install-fishyume.ps1 -Proxy "http://127.0.0.1:7897"
```

安装后连接 Codex 并检查环境：

```powershell
fishyume setup
fishyume doctor --project "E:\project"
```

`fishyume setup codex` 仍是兼容写法。setup 只修改 Fishyume 自己的 Codex MCP 配置，不会改动其他 MCP、Provider 或认证信息。

### 运行与接管

先运行一个默认双 Agent 视角、只读的比较 Panel：

```powershell
fishyume team start "Compare two approaches for this change"
```

Panel 默认等待两个贡献完成并打印结果。也可以后台启动、查看或确认取消：

```powershell
fishyume team start --detach "Compare two approaches"
fishyume team list
fishyume team show <team-id>
fishyume team cancel <team-id>
```

显式选择 Agent Route 和角色（以下路由来自 `docs/examples/agent-routes.json`）：

```powershell
fishyume team start `
  --project "E:\project" `
  --participant claude/default/sonnet:architect `
  --participant opencode/deepseek/deepseek-chat:reviewer `
  "Compare two approaches"
```

Agent Route 文件只保存 Driver、可信 Profile 名称和模型绑定，不保存 API Key、Base URL 或其他凭据。配置方法见 [Team Agent Routes](./docs/fishyume-team-agent-routes.md)。

Team 始终使用只读 workspace。按 `Ctrl+C` 只会与观察过程分离，不会取消参与者。

接受探索结论后，可以把选中的贡献冻结为 Handoff；未传 `--message` 时默认选择该 Team 的全部参与者贡献：

```powershell
fishyume team handoff create <team-id> --goal "Implement the accepted design" `
  --decision "Use the smaller design" `
  --constraint "Keep public Run contracts unchanged" `
  --acceptance "All repository gates pass"
fishyume team handoff list <team-id>
fishyume team handoff show <team-id> <handoff-id>
```

Handoff 只保存不可变的探索证据，不会创建 Run。Host Agent 根据它编写并预检 `fishyume/v2` Workflow，用户确认并执行 `run.start` 后，再显式绑定已有 Run：

```powershell
fishyume team handoff bind <team-id> <handoff-id> <run-id>
```

让 Codex 通过 Fishyume MCP 创建工作后，在另一个终端打开 Dashboard：

```powershell
fishyume
```

需要浏览器操作 Team 或 Workflow 时，再单独安装并运行可选 Web 客户端：

```powershell
npm install -g fishyume-web
fishyume-web
```

它只连接本机 Fishyume Control Plane 的 Named Pipe/Unix Socket，并在
`127.0.0.1` 上临时启动带 token 的 sidecar；核心 `fishyume` 包不依赖它。

也可以直接运行一个单 Agent 任务：

```powershell
fishyume run --driver codex --target local --project "E:\project" "实现指定需求"
```

常用操作：

```powershell
fishyume status <run-id>
fishyume attach <run-id>
fishyume resume <run-id> --approve <node-id>
fishyume resume <run-id> --retry <node-id>
fishyume cancel <run-id>
```

`fishyume demo` 不需要 Engine、Provider 登录或网络，可直接预览终端工作流界面。

查看并导出推荐的长程 Workflow 模板：

```powershell
fishyume examples list
fishyume examples show repository-hardening
fishyume examples show repository-hardening > repository-hardening.yaml
```

这些命令只读取安装包内的静态示例，不启动 Engine，也不调用模型。

## Agent 集成

Fishyume MCP 和 Machine CLI 同时暴露独立的 `fishyume.team/v1` Team API 和冻结的 `fishyume.application/v1` Workflow API。Host Agent 的典型 Workflow 顺序是：

```text
system.capabilities
workflow.validate -> workflow.explain
run.start -> run.events/run.get
run.action -> run.result
```

从探索提升为正式执行时，顺序固定且不会自动规划或自动启动：

```text
team.handoff.get
-> Host authors fishyume/v2
-> workflow.validate
-> workflow.explain
-> user confirms
-> run.start
-> team.handoff.bindRun
```

启动 MCP 服务：

```powershell
fishyume mcp
```

脚本调用示例：

```powershell
fishyume machine system.capabilities --params '{}'
fishyume machine routing.catalog --params '{}'
fishyume machine run.get --params '{"runId":"<run-id>"}'
fishyume machine team.capabilities --params '{"schemaVersion":"fishyume.team/v1"}'
```

标准 `fishyume/v2` Workflow 示例和 Host 请求集合见：

- [Workflow 编排指南](./docs/fishyume-workflow-authoring.md)
- [真实仓库长程任务模板](./docs/examples/repository-hardening.yaml)
- [全部示例及用途分级](./docs/examples/README.md)

## 如何划分 Workflow Node

一个 Agent Node Attempt 会启动一个独立的 one-shot Codex 进程。Node 应表示有独立
交付物、审批边界、并行价值或恢复价值的完整工作包；读取文件、列计划、执行命令、
修改代码、运行聚焦测试和整理摘要通常应留在同一个 Node 内部。

推荐结构：

```text
并行领域审计 -> 汇总实施方案 -> 人工审批 -> 集中实施 -> 独立验证 -> 最终验收
```

不推荐把一次实现机械拆成：

```text
read -> plan -> edit -> test -> summarize
```

现有 `fishyume-smoke.yaml`、`fishyume-topology-demo.yaml` 和 Host golden path 是协议、
生命周期或界面验证夹具，不代表实际任务的推荐 Node 粒度。

## 核心特性

- Agent、Approval、依赖、条件分支和并行调度
- 默认双 Agent 视角、只读、可持久化的一轮 Team Panel
- Codex、Claude Code 与 OpenCode 的可配置 Team Panel/TeamSession Route
- Team 列表、事件、贡献、部分失败呈现和确认取消
- 不可变 Handoff、来源消息哈希校验，以及到同项目已有 Run 的显式一对一绑定
- 持久化 Run、Node、Attempt、事件和动作回执
- 崩溃恢复、Control Plane 重启对账和跨客户端共享状态
- 有界输出、稳定事件分页、幂等 `clientRequestId` 和带版本前置条件的 `run.action`
- Context、Memory 绑定和受限上下文预算
- 确定性能力目录、路由预检、成本预算和保守 fallback
- 中文 topology-first Operator Console，以及非 TTY 的 JSON/纯文本输出
- Windows Named Pipe、Unix Domain Socket；默认不开放 TCP

## 当前边界

M7.4 已开放公共多轮 TeamSession、follow-up、单 Turn 取消和主动 close；M7.5 提供独立的可选 Web Team 客户端。Team Driver 采用显式可信 Route 配置，不做动态 Driver 扫描；Workflow Backend 仍不扩展到 Claude Code/OpenCode，也不包含通用 Shell/HTTP/容器节点。真实 Provider smoke 是显式本地 gate，不是公共 CI 的前置条件。
M7.6 补齐 Host-Web Continuity：Host Agent 可在创建 Team、冻结 Handoff 或启动 Run 后打开/聚焦可选 Web 客户端；Web 仍只是同一 Control Plane 的投影，TUI 继续只支持 Workflow Run。

## 文档

- [文档总览](./docs/README.md)
- [Workflow 编排指南](./docs/fishyume-workflow-authoring.md)
- [Team Agent Routes](./docs/fishyume-team-agent-routes.md)
- [M6 核心合同冻结](./docs/fishyume-m6-core-contract-freeze.md)
- [核心稳定化与就绪状态](./docs/fishyume-core-stabilization.md)
- [M7 Team 与 Workflow Promotion 计划](./docs/fishyume-m7-session-native-web-team-console-plan.md)
- [M7.1 Panel 验收记录](./docs/fishyume-m7.1-acceptance.md)
- [M7.2 Handoff 验收记录](./docs/fishyume-m7.2-acceptance.md)
- [M7.3 AgentSession Driver 验收记录](./docs/fishyume-m7.3-acceptance.md)
- [M7.5 可选 Web 客户端验收记录](./docs/fishyume-m7.5-acceptance.md)
- [M7.6 Host-Web Continuity](./docs/fishyume-m7.6-host-web-continuity.md)
- [M7.7 三 Agent 真实测试与修复验收](./docs/fishyume-m7.7-live-team-repair-acceptance.md)
- [首次使用与安装说明](./docs/fishyume-distribution-first-run.md)
- [开发与验证](./docs/fishyume-development.md)
- [Live Provider smoke](./docs/fishyume-m4-live-smoke.md)

## 从源码验证

```powershell
go test ./wf-engine/...
go vet ./wf-engine/...
npm --prefix wf ci
npm --prefix wf run verify
npm --prefix wf run smoke:install
npm --prefix fishyume-web run verify
npm --prefix fishyume-web run smoke:install
```

跨提交状态恢复演练是独立本地 gate：

```powershell
npm --prefix wf run smoke:downgrade
```

它需要完整 Git 历史，不属于公共 CI。

## License

Fishyume 使用 [Apache-2.0](./LICENSE) 许可证。
