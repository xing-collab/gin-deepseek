# ai-test

一个使用 Go 构建的 LLM Agent CLI 实验项目。当前框架把角色卡、短期会话记忆、模型工具调用和 Agent loop 组合成一个可运行的命令行助手。

项目的核心原则是：

- 模型只负责理解任务和提出下一步动作；
- 程序负责工具白名单、工具执行、上下文取消和最大步数；
- Chat Completions、Responses API 等协议细节留在各自适配器中；
- Agent loop 只处理协议无关的 transcript 和调度。

## 当前能力

- AgentLoop：有最大步数限制的 ReAct 调度循环。
- ChatAgentModel：将 Chat Completions 客户端适配为 AgentModel。
- 工具调用：外部代码可注入任意 Go 方法，模型按声明调用，程序执行后把结果交回模型。
- 多轮记忆：main.go 保存 AgentResult.Messages，下一轮通过 InitialMessages 续接会话。
- 角色卡：从 config/priestess.json 加载角色状态、触发器和 system prompt。
- 思考与调用日志：终端可显示模型思考和 Agent 调用，详细记录写入 log/agent.log。
- 双协议客户端：同时保留 Chat Completions 客户端和 Responses API 客户端。
- 流式接口：底层客户端提供回调、channel 和迭代器三种流式 API。
- 自动化测试：使用标准库 testing 和 httptest.Server，不访问真实 API。

## 快速开始

### 1. 配置 API Key

当前 CLI 使用 Chat Completions 客户端，默认读取 OPENAI_API_KEY。也可以在程序启动时通过 config.WithAPIKey 显式传入。

PowerShell：

```powershell
$env:OPENAI_API_KEY = "sk-..."
go run .
```

Bash：

```bash
export OPENAI_API_KEY="sk-..."
go run .
```

DEEPSEEK_API_KEY 供 Responses API 客户端使用。不要把真实 API Key 写入源码、测试、文档或日志。

### 2. 运行 CLI

```text
=== Agent Loop 对话（输入 exit 退出）===
你好
[思考] ...
[Agent 调用] get_current_time 参数={}
[Agent 结果] 12:34:56
现在是 12:34:56。
```

输入 exit 退出。每轮交互会更新角色卡状态，并通过 Agent loop 继续使用前面的 transcript。

### 3. 查看日志

运行时日志写入 log/agent.log。日志包含用户输入、模型思考、工具调用、工具结果、最终回答和错误。log/ 已被 .gitignore 排除，不应提交运行日志。

## 项目结构

```text
ai-test/
├── main.go                  # Agent CLI：角色卡、模型适配器、日志和输入循环
├── config/
│   ├── agent.go             # AgentLoop、AgentModel、工具执行器、Chat 适配器
│   ├── api.go               # Chat Completions 客户端、SSE 和工具请求
│   ├── openapi.go           # Responses API 客户端
│   ├── character.go         # 角色卡加载、状态机和 system prompt
│   ├── memory.go            # 客户端短期历史与裁剪
│   ├── mcp.go               # MCP 工具发现与调用适配
│   ├── skill.go             # Skill Markdown 加载和注册
│   ├── tool.go              # Tool 声明、时间/日期工具和处理器
│   └── priestess.json       # 示例角色卡
├── test/
│   ├── agent_test.go         # Agent loop 和 Chat 适配器测试
│   └── *_test.go             # 客户端、工具和角色卡测试
├── docs/
│   ├── agent-loop.md         # Agent loop 开发指导
│   ├── mcp-skill-use.md      # MCP 与 Skill 接入指导
│   ├── tool-use.md           # 外部工具定义与注入使用手册
│   ├── streaming-patterns.md # 流式接口说明
│   └── api-formats-and-tools.md
├── skills/
│   └── weather/SKILL.md      # Skill 示例
├── go.mod
└── AGENTS.md                # 仓库开发约定
```

## Agent loop 架构

```text
用户输入
   │
   ▼
角色状态更新 ──> system prompt
   │
   ▼
AgentLoop.Run
   │
   ▼
AgentModel.Decide ── ChatAgentModel ── Chat Completions
   │
   ├── Final：追加 assistant 答案并结束
   │
   └── Calls：追加 assistant tool_calls
                    │
                    ▼
             AgentToolExecutor
                    │
                    ▼
             追加 tool 结果
                    │
                    └──── 回到 Decide
```

核心接口位于 [config/agent.go](config/agent.go)：

```go
type AgentModel interface {
    Decide(context.Context, AgentState, []Tool) (AgentDecision, error)
}

type AgentToolExecutor interface {
    Execute(context.Context, AgentToolCall) (string, error)
}

loop := config.AgentLoop{
    Model:           modelAdapter,
    Executor:        executor,
    Tools:           config.TimeDateTools(),
    InitialMessages: previousMessages,
    MaxSteps:        8,
}
result, err := loop.Run(ctx, userPrompt)
```

### 决策规则

每一轮模型决策必须满足以下两种情况之一：

1. Final 非空、Calls 为空：返回最终答案。
2. Final 为空、Calls 至少一个：按顺序执行工具。

以下情况会返回显式错误：

- 模型没有返回答案也没有工具调用：ErrAgentNoAction；
- 模型同时返回答案和工具调用：ErrAgentInvalidDecision；
- 超过最大步数：ErrAgentMaxSteps；
- 模型或工具执行器为空：ErrAgentModelNil / ErrAgentExecutorNil。

### transcript 结构

AgentMessage 是协议无关的消息：

| Role | 用途 |
| --- | --- |
| user | 用户任务 |
| assistant + Content | 模型最终答案 |
| assistant + ToolCalls | 同一轮的全部工具调用 |
| tool + ToolCallID | 对应调用 ID 的工具结果 |

同一轮的多个工具调用必须保存在一条 assistant 消息中。工具结果通过 ToolCallID 与调用关联，适配器才能正确转换为 Chat Completions 的 tool_calls 或 Responses 的 function call output。

AgentLoop 会复制传给模型的 state 和 tools，避免模型适配器修改内部 transcript。InitialMessages 用于续接会话，调用方应保存上一轮的 AgentResult.Messages。

## Chat Completions 适配器

ChatAgentModel 负责：

1. 将 AgentMessage 转成 messages；
2. 将 ToolCalls 转成 Chat Completions 的 tool_calls；
3. 发起带 tools 的非流式请求；
4. 解析最终文本、reasoning_content 和工具参数；
5. 通过 OnReasoning、OnToolCall 回调输出观察信息。

模型适配器不负责执行工具。工具执行必须交给 AgentToolExecutor，这样本地函数、HTTP 服务、数据库、MCP server 或子 Agent 都可以替换接入。

## 工具调用

外部代码通过 config.ToolRegistry 同时注册工具声明和 Go 方法。注册表既提供模型需要的工具列表，也实现 AgentToolExecutor：

完整的定义、参数校验、上下文取消和测试示例见 [docs/tool-use.md](docs/tool-use.md)。

```go
registry := config.NewToolRegistry()
err := registry.RegisterFunction(
    "get_weather",
    "获取指定城市天气。",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "city": map[string]any{"type": "string"},
        },
        "required": []any{"city"},
    },
    func(ctx context.Context, args map[string]any) (string, error) {
        return queryWeather(ctx, args["city"].(string))
    },
)

loop := config.AgentLoop{
    Model:    model,
    Executor: registry,
    Tools:    registry.Tools(),
}
```

工具调用流程：

1. 工具声明随模型请求发送；
2. 模型返回工具名称、调用 ID 和 JSON 参数；
3. 适配器解析参数为 map[string]any；
4. executor 执行真实函数；
5. loop 将结果追加为 role=tool；
6. 模型读取结果并生成答案或继续调用工具。

工具名称必须唯一；未知工具、重复注册和空处理函数会返回错误。生产环境接入工具前，应增加 JSON Schema、参数范围、权限、超时、重试和敏感信息脱敏。

## 两套客户端

项目保留两条协议独立的客户端实现：

| 客户端 | 文件 | 协议 | 默认环境变量 |
| --- | --- | --- | --- |
| LLM | config/api.go | Chat Completions | OPENAI_API_KEY |
| OpenAPIClient | config/openapi.go | Responses API | DEEPSEEK_API_KEY |

两套客户端的请求结构、工具格式、SSE 事件和响应类型不同，不要在业务层混用 wire JSON。需要接入 Agent loop 时，应实现一个 AgentModel 适配器，把协议差异封装在适配器内部。

底层 LLM 和 OpenAPIClient 都提供非流式、回调式、channel 式和 Go 迭代器式流式调用。详细说明见 [docs/streaming-patterns.md](docs/streaming-patterns.md) 和 [docs/api-formats-and-tools.md](docs/api-formats-and-tools.md)。

MCP 工具发现、调用适配以及 Skill 加载方式见 [docs/mcp-skill-use.md](docs/mcp-skill-use.md)。

## 角色卡运行时

角色卡由程序控制，不由模型自行修改：

```text
用户输入
   ↓
Character.Update(input)
   ↓
匹配关键词并更新状态
   ↓
Character.BuildSystemPrompt()
   ↓
AgentModel.Decide()
```

config/priestess.json 定义角色身份、人格、表达风格、触发器和状态。每轮输入先执行 Character.Update，再根据当前状态生成 system prompt。

## 开发与验证

```bash
gofmt -w .
gofmt -l .
go build ./...
go test ./...
go test ./test -run TestAgent
go vet ./...
```

测试不调用真实 API，使用 httptest.Server 模拟请求和 SSE 响应。新增行为时，优先在 test/ 增加测试，不要在 config/ 内新增 *_test.go。

## 安全约定

- 不提交 API Key、token、私密 prompt、模型响应或运行日志。
- 使用环境变量管理凭据和端点。
- 工具名称必须经过白名单或注册表匹配。
- 工具执行结果写入日志或长期记忆前先脱敏。
- 思考内容可能包含敏感信息；生产环境应谨慎开启终端展示。
- log/ 和二进制文件属于生成物，不应手动提交。

## 后续路线

- 流式 AgentEvent，统一输出思考、正文、工具开始/结束和最终答案；
- 每工具独立超时、重试、幂等键和审批策略；
- MCP 工具发现与调用适配器；
- transcript 压缩和长期记忆写回；
- 子 Agent executor；
- Chat/Responses 两种协议的统一 AgentModel 适配层。

扩展时应保持 MaxSteps、context 取消、工具白名单和 transcript 映射规则不变。
