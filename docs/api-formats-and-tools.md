# API 两种格式对比与 Tool 集成：chat/completions vs Responses

> 项目：DeepSeek 大模型客户端（Go 1.25）
> 代码位置：`config/api.go`（chat 格式）、`config/openapi.go`（Responses 格式）
> 阅读前提：Java 基础，正在学 Go

## 一句话总结

同一个「让模型对话」的能力，OpenAI 出了**两套 HTTP 协议**：老的是 `/v1/chat/completions`（用 `messages` 数组），新的是 `/v1/responses`（用 `input` + `instructions`）。字段名、响应结构、tool 写法全都不一样，**不能混用**。本项目网关 `cow.g201.com` 的 `gpt-5.6-sol` 只实现了前者，所以用 `NewClient()`（chat）能跑，用 `NewOpenAPIClient()`（Responses）报 `502 upstream failed`。

## 为什么会有两套格式

- **Chat Completions**（2023）：最早开放，核心就是一个 `messages` 数组，简单直白。绝大多数第三方网关、DeepSeek、国内中转都兼容这一套。
- **Responses**（2025）：OpenAI 推出的统一接口，把 Chat 和 Assistant 两套 API 合并，原生支持 tool、多模态、结构化输出。字段更「面向对象」（`input` 里放各种 `item`），但更复杂，很多网关还没跟进实现。

## 两套格式对比总表

| 维度 | Chat Completions（`api.go`） | Responses（`openapi.go`） |
|---|---|---|
| 端点 | `/v1/chat/completions` | `/v1/responses` |
| 请求体 | `messages` 数组 | `input` + `instructions` |
| 系统提示词 | `messages[0]`，`role:"system"` | 单独的 `instructions` 字段 |
| 用户输入 | `messages[]`，`role:"user"` | `input`（可以是字符串，或消息数组） |
| 非流式响应 | `choices[0].message.content` | `output[]` 数组（一个个 item） |
| 流式事件 | `choices[0].delta.content` | `type` + `delta`（如 `response.output_text.delta`） |
| 推理过程 | `delta.reasoning_content` | `reasoning` 相关事件（`type` 含 `reasoning`） |
| Tool 调用 | 藏在 `message.tool_calls` 里 | 独立的 `function_call` output item |
| Tool 结果回传 | `role:"tool"` 消息 | `function_call_output` input item |

## 一、Chat Completions 格式（对应 `config/api.go`）

### 请求体

```json
{
  "model": "gpt-5.6-sol",
  "messages": [
    {"role": "system", "content": "你是一个助手"},
    {"role": "user", "content": "你好"}
  ],
  "stream": true
}
```

对应 Go 结构（`config/api.go:25`）：

```go
type ApiRequest struct {
	Model           string              `json:"model"`
	Messages        []map[string]string `json:"messages"`
	Thinking        map[string]string   `json:"thinking"`
	ReasoningEffort string              `json:"reasoning_effort"`
	Stream          bool                `json:"stream"`
}
```

### 非流式响应

```json
{
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "你好！"},
      "finish_reason": "stop"
    }
  ]
}
```

### 流式 SSE

每行一个 `data:`，增量在 `delta` 字段，`content` / `reasoning_content` 可能为 `null`：

```
data: {"choices":[{"delta":{"role":"assistant"}}]}
data: {"choices":[{"delta":{"content":"你好"}}]}
data: [DONE]
```

对应解析结构（`config/api.go:68`）：

```go
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
}
```

## 二、Responses 格式（对应 `config/openapi.go`）

### 请求体

```json
{
  "model": "gpt-5.6-sol",
  "instructions": "你是一个助手",
  "input": [{"role": "user", "content": "你好"}],
  "reasoning": {"effort": "none"},
  "text": {"format": {"type": "text"}},
  "stream": true
}
```

对应 Go 结构（`config/openapi.go:54`）：

```go
type OpenAPIRequest struct {
	Model           string           `json:"model"`
	Input           any              `json:"input"`
	Instructions    string           `json:"instructions"`
	Reasoning       OpenAPIReasoning `json:"reasoning"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	Stream          bool             `json:"stream"`
	// ... 还有 Temperature / TopP / Text / Tools / ToolChoice 等
}
```

注意：系统提示词从 `messages[0]` 挪到了独立的 `instructions` 字段，用户输入在 `input` 里。

### 非流式响应

```json
{
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [{"type": "output_text", "text": "你好！"}]
    }
  ]
}
```

### 流式 SSE

事件用 `type` 区分，增量在顶层 `delta` 字段：

```
data: {"type":"response.output_text.delta","delta":"你好"}
data: {"type":"response.completed"}
```

对应解析结构（`config/openapi.go`）：

```go
type openAPIStreamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}
```

### 短期记忆（会话内对话历史）

`OpenAPIClient` 会自动维护会话内的短期记忆（上限 20 条，超出后丢弃最旧的）：

- 每轮调用自动把用户输入写入历史（`role:"user"`），请求发出时把历史作为 `input` 数组一起发送，让模型记得此前问答；
- 流式/非流式**成功后**，客户端自动把模型回复正文写入历史（`role:"assistant"`，只记正文、不记推理过程）；请求失败则保留本轮 user 消息、不写 assistant；
- 历史由互斥锁保护、并发安全，可用 `History()` / `AddHistory()` / `ClearHistory()` 查看或管理；
- 参与记忆的方法：`Invoke(instructions, content string)`（签名已由 `input any` 改为 `content string`）与流式方法 `Stream` / `StreamChan` / `StreamIter`；
- `CreateResponse(ctx, request)` 与 `NewOpenAPIRequest` 是**无记忆的底层方法**：请求体完全由调用方构造，不读写历史。

## 三、当前项目映射 + 网关约束

本项目两个客户端各对应一套协议，**互不兼容**：

| 文件 | 客户端 | 协议 |
|---|---|---|
| `config/api.go` | `NewClient()` → `*LLM` | chat/completions |
| `config/openapi.go` | `NewOpenAPIClient()` → `*OpenAPIClient` | Responses |

网关 `cow.g201.com` 的 `gpt-5.6-sol` **只实现 chat 协议**。实测三组对比：

| 请求 | 结果 |
|---|---|
| chat 端点 + `messages` body | ✅ 正常流式返回 |
| chat 端点 + `input`/`instructions` body | ❌ `502 upstream failed` |
| responses 端点 + `input` body | ❌ `upstream failed` |

所以报错不是代码 bug，是**协议不匹配**：`NewOpenAPIClient()` 发的 Responses 格式请求，网关/上游只认 chat 的 `messages`，不认 `input`/`instructions`，直接 502。

**结论**：对接这个网关，必须用 `NewClient()`（chat 格式）。

## 四、Tool（function calling）集成

### 核心概念

模型**不会真正执行你的函数**。它只会返回「我想调用哪个函数 + 参数是什么」（一段 JSON）。真正的执行发生在你的 Go 代码里，执行完把结果回传给模型，模型再据此生成最终回答。

### 完整流程（五步，chat 格式）

```
① 请求带上 tools 数组（声明有哪些函数可用）
② 模型返回 tool_calls（函数名 + arguments JSON 字符串）
③ 你的代码解析 tool_calls，switch 分发执行对应函数
④ 把执行结果作为 role:"tool" 消息追加回 messages
⑤ 再次请求，模型基于结果生成最终回复
```

### Chat 格式的 Tool（本网关可用，重点）

**① 请求里的 tools 声明**（注意有 `function` 嵌套层）：

```json
{
  "model": "gpt-5.6-sol",
  "messages": [{"role": "user", "content": "北京天气怎么样？"}],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "获取指定城市的天气",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {"type": "string", "description": "城市名"}
          },
          "required": ["location"]
        }
      }
    }
  ],
  "tool_choice": "auto"
}
```

**② 模型返回的 tool_calls**（藏在 `message` 里，`arguments` 是 JSON 字符串）：

```json
{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": {
              "name": "get_weather",
              "arguments": "{\"location\":\"北京\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ]
}
```

**④ 结果回传**（`role:"tool"` + `tool_call_id` 必须和上面 `id` 对上）：

```json
{
  "role": "tool",
  "tool_call_id": "call_abc123",
  "content": "北京今天晴，25°C"
}
```

### Responses 格式的 Tool（对比，本网关不支持）

和 chat 格式的差异：

- **tools 声明是扁平的**，没有 `function` 嵌套层（`name`/`description`/`parameters` 直接放 tool 对象上）：

```json
{
  "type": "function",
  "name": "get_weather",
  "description": "获取指定城市的天气",
  "parameters": {
    "type": "object",
    "properties": {"location": {"type": "string"}},
    "required": ["location"]
  }
}
```

- **模型返回独立的 `function_call` item**（不是塞在 message 里），ID 用 `fc_` 前缀：

```json
{
  "type": "function_call",
  "id": "fc_abc123",
  "call_id": "fc_abc123",
  "name": "get_weather",
  "arguments": "{\"location\":\"北京\"}"
}
```

- **结果回传用 `function_call_output` item**（`call_id` 必须和上面一致）：

```json
{
  "type": "function_call_output",
  "call_id": "fc_abc123",
  "output": "北京今天晴，25°C"
}
```

### 落地到本项目 `api.go` 的步骤（教学示例，未改实际文件）

要给 `api.go` 集成 tool，需要四步改动：

**1. `ApiRequest` 加字段**：

```go
type ApiRequest struct {
	Model           string              `json:"model"`
	Messages        []map[string]string `json:"messages"`
	Tools           []Tool              `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
	Stream          bool                `json:"stream"`
}
```

**2. 定义 Tool / ToolCall 结构**：

```go
// Tool 对应请求里的 tools 数组元素
type Tool struct {
	Type     string   `json:"type"` // 固定 "function"
	Function Function `json:"function"`
}

type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall 对应响应里的 tool_calls 元素
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
```

**3. 响应结构加 tool_calls 解析**（`Message` 里加字段）：

```go
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}
```

**4. 主循环：执行函数 → 回传 → 再请求**：

```go
// 你的真实业务函数，由模型通过 name 触发
func getWeather(location string) string {
	return location + "今天晴，25°C"
}

// 一个带 tool 的调用循环（示意）
func (llm *LLM) InvokeWithTools(prompt string, content string, tools []Tool) (*ApiResponse, error) {
	resp, err := llm.Invoke(prompt, content) // 第一次请求，带 tools
	if err != nil {
		return nil, err
	}
	// 只要模型还在请求调用工具，就循环
	for len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
		call := resp.Choices[0].Message.ToolCalls[0]
		// ③ 解析参数并执行对应函数
		var args map[string]string
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		result := ""
		switch call.Function.Name {
		case "get_weather":
			result = getWeather(args["location"])
		}
		// ④ 把结果作为 tool 消息追加回 history，再请求
		llm.AddHistory(map[string]string{"role": "tool", "content": result})
		resp, err = llm.Invoke(prompt, content) // 再次请求（省略 tool_call_id 简化为示意）
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}
```

> 上面的 `Arguments` 字段需要 `Function` 结构额外加一个 `Arguments string json:"arguments"`（请求和响应复用同一个 `Function` 结构时，请求里 `arguments` 留空即可）。真实落地时建议把「执行函数」抽成一个 `map[string]func(args map[string]any) (string, error)` 的注册表，用 `call.Function.Name` 查表分发，比 switch 更通用。

## 五、对接 MCP（Model Context Protocol）

### MCP 是什么

MCP（Model Context Protocol）是 Anthropic 提出的**开放协议**，标准化「LLM 应用」和「外部工具/数据源」之间的通信。它把工具定义和调用统一成一套 JSON-RPC 2.0 消息，让任意 LLM 客户端都能接入任意 MCP server 暴露的工具——不用再为每个工具硬编码。

架构上分两方：

- **MCP server**：暴露工具（tools）、资源（resources）、提示词（prompts）的服务，可以是一个本地子进程（stdio）或一个 HTTP 服务。
- **MCP client**：连接 server、发现工具、调用工具的一方（你的 Go 客户端，或 Claude Code）。

核心方法：

| 方法 | 作用 |
|---|---|
| `initialize` | 握手，协商协议版本和能力 |
| `tools/list` | 列出 server 暴露的所有工具 |
| `tools/call` | 调用某个工具，传参数，拿结果 |

三种传输方式：

| 传输 | 场景 |
|---|---|
| stdio | server 是本地子进程，通过 stdin/stdout 通信（最常见） |
| Streamable HTTP | server 是远程 HTTP 服务（2025 新规范） |
| SSE（旧） | 老式 HTTP + SSE 流 |

### 方式一：代码层面（Go 客户端连 MCP server）

用社区主流的 `mark3labs/mcp-go`（官方还有 `modelcontextprotocol/go-sdk`）。核心四步：

```go
import (
    "github.com/mark3labs/mcp-go/client"
    "github.com/mark3labs/mcp-go/mcp"
)

// ① 创建 stdio client：spawn 一个本地 MCP server 子进程
c, err := client.NewStdioMCPClient("go", []string{}, "run", "/path/to/server/main.go")
if err != nil {
    log.Fatal(err)
}
defer c.Close()

ctx := context.Background()

// ② 握手
_, err = c.Initialize(ctx, mcp.InitializeRequest{
    Params: mcp.InitializeRequestParams{
        ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
        ClientInfo: mcp.Implementation{Name: "my-client", Version: "1.0.0"},
    },
})
if err != nil {
    log.Fatal(err)
}

// ③ 发现工具（tools/list）
tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
for _, tool := range tools.Tools {
    fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
}

// ④ 调用工具（tools/call）
result, err := c.CallTool(ctx, mcp.CallToolRequest{
    Params: mcp.CallToolRequestParams{
        Name:      "list_files",
        Arguments: map[string]any{"path": ".", "recursive": false},
    },
})
```

**和 function calling 怎么衔接**：MCP 只负责「发现工具 + 调用工具」，模型要不要调、调哪个，仍由 function calling 决定。落地时把 `ListTools()` 返回的每个 tool（有 `Name` + `Description` + `InputSchema`）转成上一章讲的 `Tool`（function calling 的 `tools` 数组），模型发起 tool_call 时再用 `CallTool()` 转发执行，结果回传。相当于 MCP 替代了你手写 `getWeather` 那部分，工具来源变成了「随时可插拔的 server」。

### 方式二：配置层面（Claude Code 连 MCP server）

如果只是想让 **Claude Code** 用上某个 MCP server（而不是自己的 Go 程序），不用写代码，配置即可。

命令行添加：

```bash
claude mcp add filesystem -- npx -y @modelcontextprotocol/server-filesystem /path/to/dir
```

或直接写进 `~/.claude/settings.json` 的 `mcpServers` 字段（`claude mcp add` 本质就是改这里）：

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]
    }
  }
}
```

HTTP 类型的 server 用 `--transport http` 或 `url` 字段配置。配置完重启 Claude Code，对话里就能让它调用这些 MCP 工具。

## 六、Skill（项目自定义能力扩展）

### 概念

function calling 里的工具是「散」的——每个函数独立定义。**Skill** 是把「一段 system 指令 + 一组 tools + 对应的处理函数」打包成一个**可复用的能力模块**，客户端加载多个 skill，自动聚合工具、按需分发执行。

类比 Java：Skill 像一个「可插拔的 Service 模块」，每个模块自带配置（prompt 片段）、能力声明（tools）和实现（handler）。

### 设计

```go
// Skill 代表一个可复用的能力模块
type Skill struct {
    Name        string                 // 能力名，如 "weather"
    Description string                 // 能力说明，注入 system prompt 让模型知道何时用
    Tools       []Tool                 // 该能力提供的 function calling 工具
    Handler     map[string]ToolHandler // 工具名 -> 执行函数
}

// ToolHandler 是工具的执行函数：吃 JSON 参数，吐结果文本
type ToolHandler func(args map[string]any) (string, error)
```

客户端加一个 skill 注册表：

```go
type LLM struct {
    // ... 原有字段
    skills []Skill
}

// RegisterSkill 注册一个能力模块
func (llm *LLM) RegisterSkill(s Skill) { llm.skills = append(llm.skills, s) }

// aggregateTools 聚合所有已注册 skill 的工具，作为一次请求的 tools
func (llm *LLM) aggregateTools() []Tool {
    var all []Tool
    for _, s := range llm.skills {
        all = append(all, s.Tools...)
    }
    return all
}

// dispatch 根据工具名找到对应 skill 的处理函数并执行
func (llm *LLM) dispatch(name string, args map[string]any) (string, error) {
    for _, s := range llm.skills {
        if h, ok := s.Handler[name]; ok {
            return h(args)
        }
    }
    return "", fmt.Errorf("unknown tool: %s", name)
}
```

使用示例：

```go
c := config.NewClient()

c.RegisterSkill(config.Skill{
    Name:        "weather",
    Description: "查询城市天气",
    Tools:       []config.Tool{ /* get_weather 的 tools 声明 */ },
    Handler: map[string]config.ToolHandler{
        "get_weather": func(args map[string]any) (string, error) {
            return args["location"].(string) + "今天晴，25°C", nil
        },
    },
})

// 请求时自动带上 aggregateTools() 的结果，收到 tool_call 用 dispatch() 分发
```

### 和 function calling / MCP 的关系

| 层 | 作用 |
|---|---|
| function calling | 底层机制：模型决定调哪个工具、给什么参数 |
| MCP | 标准化工具来源：从任意 MCP server 发现/调用工具 |
| Skill | 工程化组织：把 prompt + tools + handler 打包成可复用模块 |

三者可组合：Skill 里的 `Tools` 可以来自硬编码，也可以来自 MCP server 的 `ListTools()`；`Handler` 可以直接执行，也可以转发给 MCP 的 `CallTool()`。

## 七、Java 对照

| 概念 | Go | Java |
|---|---|---|
| `tools` 声明 | `[]Tool` struct + JSON tag | `List<ToolDto>` + Jackson 序列化 |
| JSON Schema（parameters） | `map[string]any` | `Map<String, Object>` 或手写 DTO |
| tool_calls 解析 | 在 `Message` 加 `ToolCalls []ToolCall` | 在 `Choice` DTO 加 `List<ToolCall>` |
| 函数分发 | `switch name` 或 `map[string]func` | `switch(name)` 或策略模式 |
| 结果回传 | 追加 `map[string]string` 到 `history` | `messages.add(Map.of(...))` |
| MCP client | `mark3labs/mcp-go`（`NewStdioMCPClient` → `Initialize` → `ListTools` → `CallTool`） | Spring AI 的 `McpClient` / `McpSyncClient` |
| Skill 模块 | `Skill` struct + `RegisterSkill` 注册表 | `@Component` 实现统一接口，启动时扫描注册 |

本质上 tool calling 就是「模型当调度器、你的代码当执行器」，多轮往返直到模型说 `finish_reason:"stop"` 不再要求调工具。
