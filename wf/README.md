# Fishyume CLI

这是 Fishyume 的 CLI/MCP npm 包，提供 `fishyume` 主命令和兼容的 `wf` 别名。匹配平台的 Engine 作为同版本可选依赖安装；安装不会从网络下载未知可执行代码。

Fishyume 通过本地 Control Plane 管理只读 Team 探索和正式 Agent Workflow。用户可以先启动双模型 Panel 比较方案；Codex 等 Host Agent 再通过 MCP 或 Machine CLI 启动确定的 Workflow。

## 安装

当前版本 `0.2.1-alpha.1` 尚未发布到 npm。请使用仓库根目录的 Developer Preview 安装器：

```powershell
.\install-fishyume.ps1
```

环境要求：Node.js 24+、Go 1.26+ 和 Codex CLI。安装后执行：

```powershell
fishyume setup
fishyume doctor
```

## 常用命令

```text
fishyume                 打开 Run Dashboard
fishyume demo            离线预览拓扑控制台
fishyume examples list   查看推荐 Workflow 模板
fishyume team start      启动默认双模型只读 Panel
fishyume team list       列出持久化 Team
fishyume team show <id>  查看贡献和部分失败
fishyume team cancel <id> 确认取消 Team
fishyume team handoff create <team-id> --goal <goal>
fishyume team handoff list <team-id>
fishyume team handoff show <team-id> <handoff-id>
fishyume team handoff bind <team-id> <handoff-id> <run-id>
fishyume run             创建单 Agent Run
fishyume attach <id>     接管已有 Run
fishyume status <id>     查看状态
fishyume resume <id>     审批、拒绝或重试
fishyume cancel <id>     取消 Run
fishyume doctor          检查 Engine、Driver 和 Codex 接入
```

Host Agent 接入：

```powershell
fishyume mcp
fishyume machine system.capabilities --params '{}'
fishyume machine team.capabilities --params '{"schemaVersion":"fishyume.team/v1"}'
```

默认 Panel 会等待并打印两个独立贡献；`--detach` 立即返回，`--json` 输出机器可读结果：

```powershell
fishyume team start "Compare two approaches"
fishyume team start --detach --json "Compare two approaches"
fishyume team show <team-id> --json
```

Handoff create 默认选择全部参与者贡献，也可重复传入 `--message`、`--decision`、`--constraint`、`--open-question` 和 `--acceptance`。创建 Handoff 不会启动 Run；`team handoff show` 会显示明确的 Host promotion 顺序，只有用户确认后的已有同项目 Run 才能通过 `team handoff bind` 绑定。

`fishyume setup codex` 是 `fishyume setup` 的兼容写法。一个 Agent Node Attempt 会启动一个独立的 one-shot Codex 进程，因此实际 Workflow 应按完整工作包划分 Node。详细原则和长程任务模板见 [Workflow 编排指南](../docs/fishyume-workflow-authoring.md) 与 [示例目录](../docs/examples/README.md)；Application API 和产品边界见仓库根目录 [README](../README.md)。

## 支持范围

当前正式执行组合是 `codex + local`。产品支持一轮只读 Team Panel、不可变 Handoff 和显式 Workflow promotion，以及 Agent/Approval Workflow、并行与依赖、持久化恢复、Context/Memory 绑定、确定性路由预检和中文 Operator Console。多轮 Session、follow-up、单 turn 取消、Web/Desktop 客户端、动态 Driver 发现和第三方 Driver 不在当前版本范围内。

## License

Apache-2.0，见 [LICENSE](./LICENSE)。
