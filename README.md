# Fishyume

Fishyume 是一个面向 AI Agent 的本地协作控制平面。

它不替代 Codex、Claude Code 或 OpenCode，也不直接调用模型 API。Fishyume
负责把一个复杂任务组织成可观察、可恢复、可审计的协作过程；具体的模型
调用、登录状态和 Provider 配置仍由 Agent 自己负责。

## 适合什么任务

Fishyume 适合需要多个步骤、并行分析、人工确认或中途恢复的长程任务，例如：

- 让多个 Agent 独立比较实现方案，再由 Host Agent 汇总决策
- 将已确认的计划拆成有依赖关系的实现、审查和验证阶段
- 在 Agent 等待输入、需要批准或进程重启后继续同一个任务
- 保留 Team 贡献、失败证据、Handoff 和最终 Workflow Run

简单的一次性问题可以直接交给 Host Agent；只有在任务需要协作或持久化时，
才需要启动 Fishyume。

## 产品结构

```text
Host Agent
    |
    +-- Team Panel / TeamSession   先比较方案、收集证据
    |
    +-- Workflow                   再执行已确认的长程计划
            |
            +-- Codex Workflow Nodes
```

- **Team**：面向前期探索。多个 Agent 独立贡献方案，支持 Panel、可恢复的
  Session、follow-up、取消和 Handoff。
- **Workflow**：面向正式执行。使用有依赖关系的 Codex Node，支持审批、重试、
  并行调度、Context/Memory、事件和崩溃恢复。
- **Host Agent**：负责和用户沟通、选择 Team Route、确认方案，并决定何时把
  Handoff 提升为 Workflow。
- **Web**：Control Plane 的浏览器投影，用于查看 Team、Handoff 和 Run；它不是
  独立的执行引擎。

## 快速开始

当前版本是 `0.2.1-alpha.1`，仍属于 Developer Preview，尚未发布为稳定 npm
产品。源码安装需要 Node.js 24+、Go 1.26+，以及你准备使用的 Agent CLI。

在仓库根目录运行：

```powershell
.\install-fishyume.ps1
```

安装完成后，先让 Fishyume 配置自己的 Codex MCP 入口并检查本机环境：

```powershell
fishyume setup
fishyume doctor
```

`fishyume setup` 会自动发现本机的 Codex、Claude Code 和 OpenCode，并创建默认
Team Route。安装新的 Agent 后再次运行：

```powershell
fishyume team routes refresh
fishyume team routes
```

不需要手写 JSON，也不需要设置 `FISHYUME_AGENT_ROUTES_FILE`。旧环境变量仅保留
为一次性兼容导入方式。

## 使用 Team 探索方案

让两个默认 Codex 角色比较方案：

```powershell
fishyume team start --project "E:\project" `
  "Compare two approaches for this change"
```

也可以使用已发现的 Claude Code 或 OpenCode Route：

```powershell
fishyume team start --project "E:\project" `
  --participant claude/default/default:architect `
  --participant opencode/default/default:reviewer `
  "Compare two approaches for this change"
```

查看后台 Team：

```powershell
fishyume team list
fishyume team show <team-id>
fishyume team cancel <team-id>
```

探索结论确认后，创建不可变 Handoff：

```powershell
fishyume team handoff create <team-id> `
  --goal "Implement the accepted design" `
  --decision "Use the smaller design" `
  --acceptance "All repository gates pass"
```

Handoff 只保存探索证据，不会自动创建或启动 Workflow。Host Agent 应先生成并
验证 `fishyume/v2` Workflow，经用户确认后再执行。

## 使用 Workflow 执行计划

Workflow 适合已经确定目标、边界和验收标准的长程任务。一个 Node 应代表一个
完整的 Agent 工作包，例如“完成一个领域的实现并提交验证结果”，而不是把读文件、
写计划、编辑代码和测试机械地拆成多个 Node。

常用命令：

```powershell
fishyume run --project "E:\project" "Implement the accepted design"
fishyume status <run-id>
fishyume resume <run-id>
fishyume cancel <run-id>
```

正式接入通常由 Host Agent 通过 Fishyume MCP 完成：

```powershell
fishyume mcp
```

Host Agent 可以使用 `system.capabilities`、`team.capabilities`、
`workflow.validate`、`workflow.explain`、`run.start`、`run.events` 和
`run.action` 组成完整链路。

## Web 控制台

需要浏览器查看状态时，安装可选 Web 客户端：

```powershell
npm install -g fishyume-web
fishyume-web
```

Web 客户端连接同一个本地 Control Plane，显示 Team、Handoff、Workflow Run 和
路由状态。它不保存 Agent 凭据，也不会绕过 Host Agent 自动执行任务。

## 路由和模型边界

Fishyume Team 当前支持三个 Agent Driver：

- Codex
- Claude Code
- OpenCode

Fishyume 只保存 Driver、可信 Profile 名称、Route 和可选模型参数。使用
`model=default` 时，Agent 继承自己的默认模型；Fishyume 不管理 Agent 安装、
登录、API Key、Base URL，也不审计 Agent 最终使用的上游模型。

Workflow 当前保持 Codex-only。Codex 的模型发现、可用性探针、产品推荐和安全
回退属于 Workflow 路由能力，与 Team Agent 的实际上游模型选择相互独立。

## 当前状态

Fishyume 已完成 M6 核心合同冻结、M7 Team/Web 能力和 M7.9 Team 零配置路由。
公共合同、恢复语义和安全边界已固定；真实 Provider smoke 仍是显式的本地验收，
不是 CI 的前置条件。

验收记录：[M7.9 Team 零配置路由](./docs/fishyume-m7.9-acceptance.md)

## 文档

- [文档索引](./docs/README.md)
- [Team Agent Routes](./docs/fishyume-team-agent-routes.md)
- [Workflow 编排与 Node 粒度](./docs/fishyume-workflow-authoring.md)
- [Workflow 示例](./docs/examples/README.md)
- [安装与首次使用](./docs/fishyume-distribution-first-run.md)
- [M6 核心合同冻结](./docs/fishyume-m6-core-contract-freeze.md)
- [M7.9 开发方案](./docs/fishyume-m7.9-team-zero-config-routing-plan.md)
- [M7.9 验收记录](./docs/fishyume-m7.9-acceptance.md)

开发者验证命令和包发布说明见 [wf/README.md](./wf/README.md) 与
[开发与验证](./docs/fishyume-development.md)。

## License

Apache-2.0，见 [LICENSE](./LICENSE)。
