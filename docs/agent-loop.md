# Agent loop 开发指导

本文说明项目中 `config.AgentLoop` 的职责、协议和扩展方式。目标是先建立一个稳定的、与具体模型 API 无关的 ReAct 内核，再由适配器连接 Chat Completions、Responses、MCP 或其他模型服务。

## 1. Agent loop 解决什么问题

一次模型调用只能产生一个“下一步决定”。Agent loop 把这个决定交给程序执行，再把结果交回模型：

```text
用户任务
  -> transcript
  -> Model.Decide
       -> Final：结束并返回答案
       -> Calls：Executor 逐个执行工具
                    -> 追加 tool 结果
                    -> 回到 Model.Decide
  -> 达到 MaxSteps：返回 ErrAgentMaxSteps
```

模型负责选择工具和参数，程序负责权限、执行、超时、重试和终止条件。不要让模型通过自然语言决定最大轮数、可访问资源或是否跳过安全校验。

## 2. 当前核心接口

实现位于 [`config/agent.go`](../config/agent.go)。

```go
type AgentModel interface {
    Decide(context.Context, AgentState, []Tool) (AgentDecision, error)
}

type AgentToolExecutor interface {
    Execute(context.Context, AgentToolCall) (string, error)
}

registry := config.NewToolRegistry()
err := registry.RegisterFunction(
    "get_weather",
    "获取指定城市天气。",
    weatherParameters,
    getWeather,
)
if err != nil {
    return err
}

loop := config.AgentLoop{
    Model:           modelAdapter,
    Executor:        registry,
    Tools:           registry.Tools(),
    InitialMessages: previousMessages,
    MaxSteps:        8,
}
result, err := loop.Run(ctx, userPrompt)
```

`AgentModel` 是协议适配边界。它负责把 `AgentState.Messages` 转成服务需要的 `messages` 或 `input`，调用模型，并把输出解析为 `AgentDecision`。loop 不应知道 `choices[0]`、SSE 事件名称等线协议细节。

`AgentToolExecutor` 是执行边界。它可以调用本地函数、HTTP 服务、数据库、MCP server 或另一个 Agent。成功结果必须是可记录的字符串；失败必须返回 error，不能伪装成模型答案。

`ToolRegistry` 用于从 `config` 包外部注入工具。调用方先定义普通 Go 方法，再通过 `RegisterFunction` 将工具声明和 handler 绑定。注册表同时实现 `AgentToolExecutor` 并提供 `Tools()`，因此声明和实际执行方法不会分散到两套映射中。handler 会收到当前 `context.Context` 和模型解析后的参数副本。

## 3. transcript 约定

`AgentMessage` 是协议中立的消息：

- `role=user`：初始任务。
- `role=assistant` 且 `Content` 非空：最终答案。
- `role=assistant` 且 `ToolCalls` 非空：本轮模型请求的全部工具调用。多个调用必须保留在同一条 assistant 消息中。
- `role=tool`：一次工具结果；`ToolCallID` 必须对应请求中的调用 ID，`ToolName` 便于日志和审计。

这种结构可以直接映射到 Chat Completions 的 assistant `tool_calls`，也可以映射到 Responses 的 function call / function call output。适配器可以增加协议字段，但不要依赖 reasoning 文本作为工具参数。

每次传给模型的 `AgentState` 和 `Tools` 都是副本。模型适配器可以在本地修改副本进行序列化，但不得依赖这些修改影响 loop 内部状态。

`InitialMessages` 用于续接多轮会话。调用完成后，将 `AgentResult.Messages` 保存并传给下一次 loop；初始消息同样会被复制，调用方后续修改不会污染正在运行的状态。

## 4. 一轮循环的规则

每轮 `Decide` 后，`AgentDecision` 必须满足以下条件之一：

1. `Final` 非空且没有 `Calls`：追加 assistant 最终答案并结束。
2. `Calls` 至少一个且 `Final` 为空：追加一条包含全部调用的 assistant 消息，按返回顺序执行调用，并逐个追加 tool 结果。

空决定返回 `ErrAgentNoAction`；同时包含最终答案和工具调用返回 `ErrAgentInvalidDecision`。这两个错误都是显式协议错误，调用方应记录并决定是否重试。

工具调用按顺序执行。loop 不隐式并发；若业务确实允许并发，应在 executor 内实现，并保证结果与调用 ID 一一对应。

## 5. 边界与安全

- `MaxSteps <= 0` 使用默认上限 8；生产场景建议按任务设置更小的值。
- 模型决策和每次工具执行都接收 `context.Context`。取消后 loop 不再开始下一次工具调用。
- 工具名称必须经过白名单或注册表匹配。`ToolRegistry.Execute` 对未知名称返回错误，并拒绝重复注册。
- 在进入真实系统前，对 `Arguments` 做 JSON Schema、类型、范围和权限校验。
- 工具结果可能包含隐私或凭据。写入日志、长期记忆或返回给模型前应脱敏。
- `AgentResult.Messages` 适合调试、审计和短期记忆；不要未经筛选地持久化完整 transcript。

## 6. 适配现有客户端

项目已有 `LLM.InvokeWithTools` / `StreamChanWithTools`，它们在各自 API 内部实现工具循环。新增 `AgentLoop` 的意义是抽出协议无关的调度层：

```text
AgentModel(Chat/Responses adapter)
              ↓ AgentDecision
AgentLoop     ↓ AgentToolCall
AgentToolExecutor(local/MCP/HTTP)
              ↓ result string
transcript -> AgentModel
```

不要在 `AgentLoop` 中再次解析 SSE 或拼接 Chat/Responses 的 wire JSON；这些工作属于适配器。未来接入 MCP 时，将 `tools/list` 转为 `[]Tool`，将 `tools/call` 封装为 `AgentToolExecutor` 即可。

## 7. 测试清单

测试放在 `test/`，不调用真实 API。至少覆盖：

- 一轮工具调用后返回最终答案；
- 同一轮多个工具调用及其 `ToolCallID`；
- 空决定和“Final + Calls”非法决定；
- 达到 `MaxSteps`；
- context 取消后不执行后续工具；
- 未知工具和工具错误；
- 模型修改传入 state/tools 不会污染 loop 内部 transcript 或工具声明。

验证命令：

```bash
go test ./...
go vet ./...
gofmt -w config/agent.go test/agent_test.go
```

## 8. 后续演进建议

下一阶段可以在不改变 loop 核心协议的前提下增加：

- 流式 `AgentEvent`，把模型文本、工具开始/结束和最终答案统一向上层输出；
- 每工具超时、重试和幂等键；
- 工具审批策略（只读、需确认、禁止）；
- transcript 压缩和长期记忆写回；
- 子 Agent executor，以及 MCP server 的动态工具发现。

这些能力都应由程序显式控制，保持 `MaxSteps`、context 和工具白名单等边界不变。
