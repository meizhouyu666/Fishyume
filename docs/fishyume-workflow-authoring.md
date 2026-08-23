# Fishyume Workflow 编排指南

Fishyume 编排的是完整 Agent 工作包，不是 Agent 内部的思考步骤、工具调用或
subagent 风格小任务。当前 `codex + local` Driver 会为每个 Agent Node Attempt
启动一个独立的、无头的 `codex exec --ephemeral` 进程。因此，Node 边界直接影响
启动成本、上下文质量、并行安全、恢复语义和最终结果质量。

## 核心模型

```text
Workflow
  -> Node
    -> Attempt
      -> one-shot Codex process
        -> read / reason / edit / shell / verify / report
```

一个 Agent Node 可以在自己的完整执行过程中读取仓库、分析问题、修改文件、运行
命令并验证结果。不要把这些内部动作机械地拆成多个 Node。

同一个 Node 在 `needs_input`、显式重试或合格 fallback 后可以产生新的 Attempt；
每个 Attempt 都是新的 Codex 进程，但仍属于同一个持久化 Node 生命周期。

## 什么时候应该拆成 Node

当一个工作单元至少满足下面一项时，通常值得成为独立 Node：

- 有可以独立验收的交付物，例如审计报告、批准后的实现或独立验证结论；
- 能与其他工作包安全并行，且不会同时修改相同文件或共享可变资源；
- 需要单独的人工审批、风险确认、补充输入或权限边界；
- 失败后需要单独重试，而不应重新运行此前已完成的阶段；
- 是值得持久化和恢复的长耗时阶段；
- 后续阶段需要显式消费它的结构化结果。

一个实用判断是：如果这个工作单元失败、暂停或重新执行时，用户会希望 Fishyume
单独显示并控制它，它就可能是一个合适的 Node。

## 什么应该留在一个 Node 内部

下面这些通常只是 Agent 完成一个工作包所需的内部动作：

- 阅读几个相关文件；
- 运行一条测试、构建或搜索命令；
- 先列计划再实施；
- 修改代码后执行聚焦测试；
- 对自己的修改做一次基本检查；
- 写几句摘要或转换输出格式。

例如，“修复一个限定模块的缺陷、补充回归测试、运行聚焦验证并汇报结果”适合成为
一个 Agent Node。把它拆成 `read`、`plan`、`edit`、`test`、`summarize` 五个 Node
通常只会产生五个相互割裂的 Codex 进程。

## 推荐结构

Fishyume 最适合阶段清楚、存在并行机会并且需要人工把关的长程任务：

```text
architecture-audit ─┐
test-build-audit ───┼─> synthesis -> approval -> implementation -> verification -> acceptance
product-docs-audit ─┘
```

这里每个审计 Node 都负责一个完整问题域和一份可验收报告。`synthesis` 将多个报告
收敛为有边界的实施方案；`implementation` 在一次 Codex 执行内完成批准范围的修改和
聚焦检查；`verification` 由新的 Codex 进程独立检查最终状态。

完整可运行模板见
[`examples/repository-hardening.yaml`](examples/repository-hardening.yaml)。

## 依赖与上下文不是一回事

- `dependsOn` 决定调度顺序：祖先完成后，Node 才可能开始。
- `context.dependencies` 决定结果注入：只把后续 Agent 真正需要的祖先结果加入其
  Context。

不要因为一个 Node 在拓扑上依赖另一个 Node，就默认把所有祖先结果都注入 Prompt。
显式选择依赖结果可以减少噪声并保留可审计的 Context 边界。

只在项目确实存在对应文件时声明 `projectInstructions`。例如目标仓库有
`AGENTS.md` 时可以显式加入；通用模板不应假定每个项目都有同名文件。

## 并行与写入安全

并行 Node 应默认只读，或者拥有互不重叠的写入范围。多个 Codex 进程共享同一个项目
工作区；Fishyume 调度并行 Attempt，但不会自动为每个 Node 创建 Git worktree，也
不会合并并发文件修改。

推荐模式是并行研究、串行写入：

1. 多个只读 Agent Node 并行调查不同问题域；
2. 一个 Agent Node 汇总并形成实施方案；
3. Approval 明确批准范围；
4. 一个实现 Node 集中修改；
5. 一个独立验证 Node 检查 diff 和测试结果。

只有在文件所有权和集成方式已经明确时，才并行运行写入型 Node。

## Approval 应放在哪里

Approval 用于真实决策边界，而不是装饰 DAG。适合放在：

- 从只读调查进入仓库修改之前；
- 执行高成本或有外部副作用的阶段之前；
- 验证完成、需要用户接受最终结果时。

Approval 的 prompt 应说明用户批准的对象和影响范围，而不是只写“是否继续”。

## 编排反例

过细：

```text
read-files -> make-plan -> edit-code -> run-one-test -> summarize
```

这通常应该是一个完整的 `implement-and-verify` Agent Node。

过粗：

```text
do-everything
```

如果任务同时包含大范围调查、关键决策、仓库修改和独立验收，单个 Node 会失去并行、
审批、恢复和独立重试的价值。

合适：

```text
parallel domain audits -> bounded proposal -> approval -> implementation -> independent verification
```

## 编排检查表

在调用 `workflow.validate` 之前检查：

- 每个 Agent Node 是否有独立、可验收的结果；
- 是否把 Agent 内部动作误拆成了 Node；
- 并行 Node 是否会争用相同文件或共享可变资源；
- 每个写入阶段之前是否有必要的 Approval；
- `context.dependencies` 是否只包含真正需要注入的祖先结果；
- 某个 Node 重试时，重复副作用是否可接受并被明确控制；
- Node task 是否写清目标、范围、约束、产物和验证要求；
- 最终是否有独立验证，而不是只依赖实现 Agent 自报成功。

## 示例分级

[`examples/`](examples/) 中的文件分为两类：

- 产品模板：展示真实任务的推荐 Node 粒度，可以复制后按项目调整；
- 协议与界面夹具：用于 smoke、MCP 调用顺序或 TUI 拓扑验证，不代表推荐的生产
  Workflow 粒度。
