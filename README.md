# Fishyume

Fishyume 是一个本地、可恢复的 AI Agent 工作流控制台。

它把多个 Agent 步骤和人工决策组织成一个可观察的工作流：Agent 负责执行，Fishyume 负责调度、持久化、恢复和安全动作，人类可以随时接管同一个 Run，审批、补充输入、重试或取消。

## 你可以用它做什么

- 把计划、研究、实现、验证等步骤组织成有依赖关系的 Workflow
- 并行运行互不依赖的 Agent 节点，再在审批节点汇合
- 在 Agent 等待审批或补充信息时从终端接管
- 在进程崩溃、Control Plane 重启或客户端断开后继续同一个 Run
- 通过 MCP 让 Codex 等 Host Agent 创建和管理 Workflow
- 通过 Machine CLI 在脚本中使用同一套 Application API
- 在运行前查看确定性的路由预览、预算和 fallback 边界

当前正式支持的执行组合是 `codex + local`。Fishyume 本身不是聊天客户端，也不内置模型 Tool loop；它是 Agent 工作流的本地控制面。

## 当前状态

当前版本：`0.2.1-alpha.1`。项目处于 Developer Preview 阶段，尚未发布 npm 正式包或 GitHub Release。Windows + Codex 是参考体验，Ubuntu 也有安装和 CI 验证。

## 五分钟开始

### Windows Developer Preview

环境要求：Node.js 24+、Go 1.26+、已安装的 Codex CLI。仓库根目录执行：

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

让 Codex 通过 Fishyume MCP 创建工作后，在另一个终端打开 Dashboard：

```powershell
fishyume
```

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

Fishyume MCP 和 Machine CLI 暴露同一套 Application API。Host Agent 的典型顺序是：

```text
system.capabilities
workflow.validate -> workflow.explain
run.start -> run.events/run.get
run.action -> run.result
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
- 持久化 Run、Node、Attempt、事件和动作回执
- 崩溃恢复、Control Plane 重启对账和跨客户端共享状态
- 有界输出、稳定事件分页、幂等 `clientRequestId` 和带版本前置条件的 `run.action`
- Context、Memory 绑定和受限上下文预算
- 确定性能力目录、路由预检、成本预算和保守 fallback
- 中文 topology-first Operator Console，以及非 TTY 的 JSON/纯文本输出
- Windows Named Pipe、Unix Domain Socket；默认不开放 TCP

## 当前边界

当前不包含通用 Shell/HTTP/容器节点、动态 Driver 发现、Web/Desktop 客户端、内置 Harness 或 Claude/第三方 Driver。真实 Provider smoke 是显式本地 gate，不是公共 CI 的前置条件。

## 文档

- [文档总览](./docs/README.md)
- [Workflow 编排指南](./docs/fishyume-workflow-authoring.md)
- [M6 核心合同冻结](./docs/fishyume-m6-core-contract-freeze.md)
- [核心稳定化与就绪状态](./docs/fishyume-core-stabilization.md)
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
```

跨提交状态恢复演练是独立本地 gate：

```powershell
npm --prefix wf run smoke:downgrade
```

它需要完整 Git 历史，不属于公共 CI。

## License

Fishyume 使用 [Apache-2.0](./LICENSE) 许可证。
