# MCP 与 Skill 接入指导

本文说明当前 Agent 框架如何接入本地 Go 工具、MCP 工具和 Skill。三者的职责不同：

| 能力 | 作用 | 接入位置 |
| --- | --- | --- |
| 本地函数 | 执行当前进程中的 Go 方法 | `ToolRegistry.RegisterReflectFunction` |
| MCP | 调用独立进程或远程服务提供的工具 | `RegisterMCPTools` |
| Skill | 向模型提供工作流、规则和工具使用说明 | `ChatAgentModel.SystemPrompt` |

## 1. Agent 的统一调用链

```text
本地函数 ───────┐
                ├── ToolRegistry.Tools() ──> 模型工具上下文
MCP tools/list ─┘

模型返回工具名和参数
        ↓
ToolRegistry.Execute
        ↓
本地函数或 MCP tools/call
        ↓
工具结果写回 AgentLoop
        ↓
模型生成最终回答
```

模型不会直接执行 Go 函数，也不会直接连接 MCP 服务。模型只返回工具名称和参数，本地 Agent 根据注册表完成真实调用。

## 2. 接入 MCP

`config.MCPClient` 是项目定义的最小适配接口：

```go
type MCPClient interface {
    ListTools(ctx context.Context) ([]MCPTool, error)
    CallTool(ctx context.Context, name string, arguments map[string]any) (any, error)
}
```

参数来源与意义：

- `ctx`：由 `AgentLoop.Run` 向下传递，用于取消和超时；
- `name`：来自 MCP 服务端 `tools/list` 返回的原始工具名；
- `arguments`：来自模型生成的工具参数，字段结构由 MCP `inputSchema` 描述；
- `MCPTool.InputSchema`：来自 MCP 服务端，是提供给模型的 JSON Schema；
- `CallTool` 返回值：来自 MCP 服务端，字符串直接返回，其他类型会编码为 JSON。

具体 MCP SDK 只需要实现这个接口。例如：

```go
type SDKMCPClient struct {
    client *SomeMCPSDKClient
}

func (c *SDKMCPClient) ListTools(ctx context.Context) ([]config.MCPTool, error) {
    response, err := c.client.ListTools(ctx)
    if err != nil {
        return nil, err
    }

    tools := make([]config.MCPTool, 0, len(response.Tools))
    for _, tool := range response.Tools {
        tools = append(tools, config.MCPTool{
            Name:        tool.Name,
            Description: tool.Description,
            InputSchema: tool.InputSchema,
        })
    }
    return tools, nil
}

func (c *SDKMCPClient) CallTool(
    ctx context.Context,
    name string,
    arguments map[string]any,
) (any, error) {
    return c.client.CallTool(ctx, name, arguments)
}
```

连接 MCP 服务后，将发现的工具注册到现有注册表：

```go
registry := config.NewToolRegistry()
mcpClient := &SDKMCPClient{client: sdkClient}

err := config.RegisterMCPTools(
    context.Background(),
    registry,
    mcpClient,
    "weather",
)
if err != nil {
    return err
}
```

`namespace` 参数由调用方提供，用于避免不同 MCP 服务出现同名工具。例如 MCP 原始工具名是 `forecast`，命名空间是 `weather`，暴露给模型的名称为：

```text
mcp_weather_forecast
```

执行时注册表会自动把名称映射回 MCP 原始名称 `forecast`，再调用 `MCPClient.CallTool`。

## 3. 将 MCP 和本地函数放入同一注册表

```go
registry := config.NewToolRegistry()

// 本地普通函数。
if err := registry.RegisterReflectFunction(
    "get_current_time",
    "获取当前本地时间。",
    getCurrentTime,
); err != nil {
    return err
}

// MCP 服务发现的远程工具。
if err := config.RegisterMCPTools(
    ctx,
    registry,
    mcpClient,
    "weather",
); err != nil {
    return err
}

loop := config.AgentLoop{
    Model:    model,
    Tools:    registry.Tools(),
    Executor: registry,
    MaxSteps: 8,
}
```

`AgentLoop` 不需要区分工具来自本地、HTTP、数据库还是 MCP，它只调用统一的 `AgentToolExecutor`。

## 4. 接入 Skill

Skill 不是可执行函数。它是一份 Markdown 工作流说明，应该注入 system prompt，告诉模型何时以及如何使用工具。

示例 Skill：

```markdown
# 天气查询

当用户询问天气时：

1. 如果缺少城市，先询问用户；
2. 使用 `mcp_weather_forecast` 获取真实天气；
3. 默认使用摄氏度；
4. MCP 调用失败时明确告知用户，不要编造结果。
```

加载并注入：

```go
skill, err := config.LoadSkill("skills/weather/SKILL.md")
if err != nil {
    return err
}

model := config.ChatAgentModel{
    Client: client,
    SystemPrompt: character.BuildSystemPrompt() +
        "\n\n## 当前启用的 Skill\n" +
        skill.Instructions,
}
```

参数来源与意义：

- `path`：调用方提供的 Skill Markdown 文件路径；
- `Skill.Name`：默认来自文件名，首行是一级标题时使用标题；
- `Skill.Instructions`：文件完整正文，最终加入模型 system prompt；
- `Skill.SourcePath`：原始文件路径，用于日志和审计；
- `Skill.Description`：可由调用方补充，用于实现 Skill 路由或选择器。

当前 `main.go` 支持通过环境变量启用一个 Skill：

```powershell
$env:AGENT_SKILL_PATH = "skills/weather/SKILL.md"
go run .
```

环境变量为空时不加载额外 Skill。

## 5. 多 Skill 注册和选择

多个 Skill 可以放入 `SkillRegistry`：

```go
skills := config.NewSkillRegistry()
if err := skills.Load("skills/weather/SKILL.md"); err != nil {
    return err
}
if err := skills.Load("skills/database/SKILL.md"); err != nil {
    return err
}

prompt, err := skills.Prompt("天气查询")
if err != nil {
    return err
}
```

生产场景不建议每轮把全部 Skill 加入上下文。应根据用户任务、路由模型或程序规则选择少量相关 Skill，再拼接到 system prompt。

## 6. 推荐目录结构

```text
config/
├── tool.go   # 本地函数注册和统一 ToolRegistry
├── mcp.go    # MCP tools/list 与 tools/call 适配
└── skill.go  # Skill 文件加载和注册

skills/
├── weather/
│   └── SKILL.md
└── database/
    └── SKILL.md
```

## 7. 安全边界

- MCP Server 地址、认证令牌和启动参数来自应用配置，不应由模型生成；
- `tools/list` 返回的名称和 Schema 在注册前应按需审查；
- 对写文件、执行命令、发送消息等有副作用的 MCP 工具增加审批；
- Skill 内容会直接影响模型行为，只加载可信文件；
- 不把 API Key、MCP token 或用户隐私写入 Skill、工具描述和日志；
- MCP 调用必须使用 `context.Context` 控制取消和超时。
