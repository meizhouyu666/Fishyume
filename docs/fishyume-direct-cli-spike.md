# Fishyume Direct CLI 能力验证

> 状态：已完成
> 日期：2026-08-05
> 本机 Codex CLI：`0.144.6`
> 用途：M2.1.2 Direct Backend 实现依据

## 1. 结论

本机 Codex CLI 已具备 Fishyume Direct Backend 所需的基础能力：

- `codex exec` 非交互式执行；
- `--json` 输出 JSONL 生命周期事件；
- `--output-schema <file>` 约束最终响应结构；
- `--output-last-message <file>` 保存最终结构化消息；
- `--ephemeral` 禁止创建可恢复的 Codex 会话记录；
- `-C/--cd` 指定 workspace；
- `--sandbox`、profile 和 config override 提供显式运行策略。

M2.1.2 Direct Backend 采用 Codex 官方结构化输出能力，不设计自然语言末尾文本解析，也不把进程退出码单独视为成功证据。

## 2. 实际验证

### 2.1 JSONL 生命周期

只读最小执行：

```powershell
codex exec --ephemeral --sandbox read-only --json `
  -C "E:\meizhouyu\agentstudy\my-agent" `
  "Do not inspect or modify files. Reply with exactly: fishyume-direct-spike-ok"
```

退出码为 `0`，stdout 依次包含：

```json
{"type":"thread.started","thread_id":"<id>"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"agent_message","text":"fishyume-direct-spike-ok"}}
{"type":"turn.completed","usage":{"input_tokens":12789,"output_tokens":11}}
```

生命周期事件是一行一个 JSON 对象，可以持续追加到 Attempt 专属 JSONL 文件。Token usage 可从 `turn.completed` 提取，但它仍是估算与诊断数据，不影响成功结论。

### 2.2 JSON Schema 约束

使用只允许 `status` 和 `summary` 的 JSON Schema 执行后，`item.completed` 中的 Agent message 为：

```json
{"status":"succeeded","summary":"fishyume-schema-ok"}
```

这证明 Direct Backend 可以使用 Fishyume 生成的 Attempt 专属 Schema 约束最终结果，不需要依靠 prompt 自觉输出正确 JSON。

### 2.3 最终结果文件

使用完整的最小 AgentResult Schema 与：

```text
--output-last-message E:\tmp\fishyume-codex-last-message.json
```

Codex 在退出前生成：

```json
{"status":"succeeded","summary":"fishyume-output-file-ok","artifacts":[],"warnings":[],"checks":[]}
```

该文件内容与 JSONL 的最终 `agent_message` 一致，可作为 Engine 重启后的结果恢复入口。Fishyume 仍需自行执行 UTF-8、字段、大小和身份校验。

## 3. Direct Backend 完成协议

首版实现采用：

```text
codex exec
  --ephemeral
  --json
  --output-schema <attempt-dir>/result.schema.json
  --output-last-message <attempt-dir>/result.json
  -C <workspace>
  <instructions>
```

Backend 同时把 stdout 与 stderr 写入 Attempt 专属有界日志。Engine 只有在以下条件同时满足时才接受 terminal result：

1. 观察到目标进程已经结束，或 JSONL 存在合法 `turn.completed`；
2. `result.json` 存在且属于当前 Run/Node/Attempt；
3. 结果通过 Fishyume 的 AgentResult 校验；
4. 没有检测到冲突或重复结果。

退出码只用于诊断与失败归类：

- exit `0` 但结果缺失：`result_pending`，有界对账后 `waiting: completion_missing`；
- 非零退出且没有合法结果：明确失败或 indeterminate，取决于可确认的进程证据；
- 合法结果与退出状态冲突：`waiting: invalid_result`，禁止静默选择其中一个。

## 4. 恢复设计输入

Direct Backend handle 至少保存：

- Backend schema version；
- PID；
- 进程启动时间或等价启动指纹；
- Codex 可执行文件规范化路径与文件身份；
- workspace 规范化路径；
- JSONL、stderr、Schema 和 result 文件的 Attempt 相对路径。

Engine 重启后先验证 PID 与启动指纹，再观察日志和结果文件。仅 PID 相同不足以证明是原进程；无法确认时必须返回 `lost`，不能连接或终止该进程。

## 5. 安全边界

- Direct Backend 使用参数数组启动进程，不经过 Shell 拼接用户输入。
- 默认不使用 `--dangerously-bypass-approvals-and-sandbox`。
- sandbox、approval/profile 等执行策略必须由用户显式配置或受控默认值决定。
- 结果 Schema、JSONL 和 result 文件位于 Fishyume 状态目录，不写入目标项目。
- 不持久化认证信息、完整环境变量或完整终端历史。
- `--output-last-message` 是结果载体，不等同于信任；核心仍执行统一校验。

## 6. 已知注意事项

- 本次最小执行结束后 stderr 出现 `Reading additional input from stdin...`，但进程已正常返回 `0`，结构化事件与结果文件完整。Direct Backend 应将 stderr 视为诊断输出，不按单条文本推断状态。
- `--ephemeral` 只禁止 Codex 自身保存会话；Fishyume 仍必须保存自己的 Attempt handle、事件和结果。
- 真实工作任务的权限策略不能由 Spike 中的 `read-only` 示例推导，必须在 Backend 配置和 Doctor 中显式呈现。
- 真实 Codex 调用依赖用户已有认证，不进入公共 CI；自动测试使用可执行 fake Agent fixture。
