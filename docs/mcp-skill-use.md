# MCP、本地 Tool 与 Skill

本文是本项目三类能力接入方式的总览，以当前代码已经支持的行为为准。

## 1. 当前支持矩阵

| 能力 | 当前支持 | 实际实现 | 模型看到什么 | 程序如何执行 |
| --- | --- | --- | --- | --- |
| 本地 Tool | 是 | `config.ToolRegistry` | `function` 工具声明 | 直接调用 Go handler |
| MCP stdio | 是 | `config.StdioMCPClient` + `RegisterMCPTools` | MCP `tools/list` 转成 `function` 声明 | 通过 stdin/stdout 调 `tools/call` |
| MCP HTTP / Streamable HTTP | 否 | 当前没有 HTTP MCP client | 无 | 无 |
| Skill | 是 | `SkillRegistry.Discover` + `read_skill` | 启动时只有名称/描述索引；按需加载 Markdown 正文 | Skill 本身不执行代码，脚本仍需显式工具执行 |

MCP 和本地 Tool 最终都会进入同一个 `ToolRegistry`，所以 `AgentLoop` 不需要区分工具来源。

## 2. 统一调用链

```text
用户输入
  ↓
main.go 组装角色卡 + Skill 轻量索引
  ↓
ChatAgentModel.Decide
  ↓
模型返回最终答案，或返回一个/多个工具调用
  ↓
AgentLoop.Executor
  ↓
ToolRegistry.Execute
  ├─ 本地 Tool handler
  └─ MCP handler → MCPClient.CallTool → MCP server
  ↓
tool 结果写回 transcript
  ↓
模型继续决策或生成最终答案
```

模型只能提出工具调用；真正的工具白名单、参数转换、上下文取消和执行都由 Go 程序控制。

## 3. 本地 Tool

本地 Tool 是当前 Go 进程中的普通函数。注册时同时提供模型可见声明和实际 handler。

### 3.1 注册普通函数

```go
registry := config.NewToolRegistry()
err := registry.RegisterReflectFunction(
    "get_current_time",
    "获取当前本地时间。",
    getCurrentTime,
)
```

`RegisterReflectFunction` 支持普通函数、带 `context.Context` 的函数，以及返回 `(string, error)` 的函数。带参数时通过 `ToolParameter` 提供 JSON 名称、说明和必填属性。

### 3.2 注册类型安全函数

复杂参数推荐使用结构体和 `RegisterTypedFunction`：

```go
type WeatherArgs struct {
    City string `json:"city"`
}

err := config.RegisterTypedFunction(
    registry,
    "get_weather",
    "查询指定城市天气。",
    map[string]any{
        "type": "object",
        "properties": map[string]any{
            "city": map[string]any{"type": "string"},
        },
        "required": []any{"city"},
    },
    func(ctx context.Context, args WeatherArgs) (string, error) {
        if err := ctx.Err(); err != nil {
            return "", err
        }
        return queryWeather(ctx, args.City)
    },
)
```

### 3.3 注入 AgentLoop

```go
loop := config.AgentLoop{
    Model:    model,
    Executor: registry,
    Tools:    registry.Tools(),
    MaxSteps: 8,
}
```

`registry.Tools()` 给模型提供声明，`registry.Execute()` 根据模型返回的工具名调用 handler。详细用法见 [tool-use.md](tool-use.md)。

## 4. MCP

MCP 是外部工具的标准协议。MCP server 暴露工具，MCP client 负责连接、发现和调用。

| MCP 方法 | 当前用途 |
| --- | --- |
| `initialize` | 启动后协商协议版本和客户端信息 |
| `notifications/initialized` | 完成初始化通知 |
| `tools/list` | 发现工具名称、描述和 `inputSchema` |
| `tools/call` | 调用指定 MCP 工具 |

### 4.1 MCP 适配接口

项目只要求传输实现满足这个最小接口：

```go
type MCPClient interface {
    ListTools(context.Context) ([]MCPTool, error)
    CallTool(context.Context, string, map[string]any) (any, error)
}
```

`RegisterMCPTools` 会把 MCP 工具注册到本地 `ToolRegistry`：

```go
client, err := config.NewStdioMCPClient(ctx, config.StdioMCPConfig{
    Command: "node",
    Args:    []string{`C:\work\code\tool\mcp\dist\index.js`},
})
if err != nil {
    return err
}
defer client.Close()

if err := config.RegisterMCPTools(ctx, registry, client, "weather"); err != nil {
    return err
}
```

原始 MCP 工具名会加上命名空间，避免重名：

```text
get_weather_by_region
  → mcp_weather_get_weather_by_region
```

### 4.2 当前天气服务

CLI 默认启动：

```text
node C:\work\code\tool\mcp\dist\index.js
```

天气工具：

- `mcp_weather_get_weather_by_region`
- `mcp_weather_get_weather_by_coordinates`

详细构建和配置见 [mcp-stdio.md](mcp-stdio.md)。

## 5. Skill

当前项目的 Skill 是 Markdown 指令，不是可执行工具，也不是 MCP server 的封装对象。Skill 负责告诉模型：

- 什么任务需要使用哪些工具；
- 参数缺失时如何处理；
- 工具失败时如何回复；
- 哪些结果不能编造。

### 5.1 Skill 结构和渐进式加载

标准 Skill 目录至少包含一个 `SKILL.md`，文件头使用 YAML frontmatter：

```markdown
---
name: weather
description: 当用户询问天气时，确认地区并调用天气工具。
---

# 天气查询
这里是仅在需要时加载的完整指令。
```

`config.Skill` 保存 `Name`、`Description`、`Instructions` 和 `SourcePath`；`SkillRegistry.Discover` 启动时只读取 `name`/`description`，不会把正文全部放进 system prompt。

`main.go` 会扫描内置 `skills/` 目录，并可通过 `AGENT_SKILL_PATH` 额外注册一个 Skill：

```powershell
$env:AGENT_SKILL_PATH = "skills/weather/SKILL.md"
go run .
```

时间和天气 Skill 与其他 Skill 使用同一套索引及 `read_skill` 流程。Skill 不会替代工具：当前时间仍必须来自 `get_current_time`，天气仍必须来自 MCP 工具。

### 5.2 多 Skill

`SkillRegistry` 可以加载和按名称生成 prompt：

```go
skills := config.NewSkillRegistry()
if err := skills.Discover("skills"); err != nil {
    return err
}
catalog := skills.CatalogPrompt()
```

CLI 会把 `CatalogPrompt()` 追加到 system prompt，并注册一个本地 `read_skill` Tool：

```text
模型看到 Skill 名称和描述
  ↓ 判断任务是否相关
调用 read_skill({"name":"weather"})
  ↓
Go 从已索引路径读取 SKILL.md 正文
  ↓
模型依据完整指令继续调用天气 Tool 或回答
```

需要读取辅助资料时可以传 `path`，例如 `references/fields.md` 或 `scripts/check.py`。路径被限制在该 Skill 目录及 `references/`、`scripts/`、`assets/` 下，防止目录穿越。

`AGENT_SKILL_PATH` 仍可用于额外注册一个 Skill，但现在只建立索引；它不会绕过 `read_skill` 直接注入全文。旧的关键词匹配路由已经移除，所有 Skill 使用同一套发现和按需加载机制。

## 6. 三者的关系

```text
Skill = 行为规则和工作流提示
Tool  = 模型可请求的执行入口
MCP   = 外部 Tool 的标准发现和调用协议
```

组合后的顺序是：

1. Skill 的名称和描述索引进入 system prompt；
2. 模型按需调用 `read_skill`，完整 Skill 正文进入工具结果；
3. 本地 Tool 或 MCP Tool 进入 Chat Completions 的 `tools` 数组；
4. 模型返回 tool call；
5. Go executor 执行本地函数或转发到 MCP server。

## 7. 安全和工程边界

- MCP server 命令、路径、URL 和凭据来自配置，不由模型生成；
- MCP `tools/list` 返回的 Schema 应在注册前审查；
- 本地 Tool 和 MCP Tool 都应校验参数、设置超时并尊重 `context.Context`；
- 有写入、删除、发消息等副作用的工具应增加审批或权限控制；
- Skill 只加载可信文件，因为其内容会直接影响模型行为；
- 日志、Tool 描述和 Skill 中不能写 API Key、MCP token 或私密数据。

## 8. 当前未实现

- MCP Streamable HTTP client；
- MCP 旧 SSE transport client；
- 多 MCP server 配置文件和自动重连；
- MCP resources/prompts 接入；
- 独立的 Skill 路由器和按会话隔离的 Skill registry；当前由模型依据索引描述决定是否调用 `read_skill`。
