# Fishyume Workflow Examples

这些示例分为产品模板和验证夹具。一个 Agent Node Attempt 会启动一个独立的
one-shot Codex 进程；实际 Workflow 应按完整工作包划分 Node，而不是按简单思考或
工具动作拆分。

## Product template

- [`repository-hardening.yaml`](repository-hardening.yaml)：面向真实仓库长程任务的推荐
  模板。它并行执行三个只读领域审计，汇总实施方案，经人工审批后集中修改，再由独立
  Agent 验证并等待最终验收。

## Protocol and UI fixtures

- [`fishyume-v2-host.yaml`](fishyume-v2-host.yaml)：展示 `fishyume/v2`、Context 依赖和
  Approval 的最小 Host golden path；为便于阅读而刻意简化，不代表推荐粒度。
- [`fishyume-v2-host-requests.json`](fishyume-v2-host-requests.json)：上述 golden path
  对应的 MCP 请求顺序和幂等参数示例。
- [`fishyume-smoke.yaml`](fishyume-smoke.yaml)：真实 Provider smoke fixture，任务刻意
  极小，只用于验证生命周期。
- [`fishyume-topology-demo.yaml`](fishyume-topology-demo.yaml)：并行拓扑和 TUI 展示
  fixture，不是实际仓库任务模板。

编排原则、反例和检查表见
[`../fishyume-workflow-authoring.md`](../fishyume-workflow-authoring.md)。

