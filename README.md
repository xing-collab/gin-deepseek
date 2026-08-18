# ai-test

一个基于 Go 的 DeepSeek 大模型对话项目，核心目标是把「角色卡 + 状态机 + 短期记忆 + Function calling + 流式输出」这套能力落成一个可运行的交互式 CLI。

它同时封装了 DeepSeek 的两种 API 协议，并示范了 Go 里几种典型的代码设计模式：函数式选项构造器、三种流式接口（回调 / 通道 / 迭代器）、用 channel 表达并发流、以及「状态由程序决定、不交给模型」的角色扮演架构。

## 功能特性

- **双协议客户端**：`chat/completions`（`LLM`）与 Responses API（`OpenAPIClient`）两套并行的客户端，协议不同、互不依赖。
- **三种流式接口**：回调式 `Stream`、通道式 `StreamChan`、迭代器式 `StreamIter`，每种都区分思考内容（`reasoning_content`）与正文（`content`）。
- **短期记忆**：多轮对话上下文自动复用，超窗自动裁剪，请求与写回分离避免污染。
- **Function calling**：模型按需调用工具（获取时间 / 日期），程序执行后回传结果。
- **角色卡运行时**：从 JSON 加载角色设定，关键词触发器切换状态，每轮动态组装 system prompt。
- **函数式选项构造器**：`NewClient(WithBaseURL(...), WithAPIKey(...), WithModel(...))`。

## 快速开始

```bash
# 设置 API Key（两种客户端分别读取不同环境变量）
export OPENAI_API_KEY="sk-..."        # LLM 客户端（chat/completions）
export DEEPSEEK_API_KEY="sk-..."      # OpenAPIClient（Responses API）

go run .     # 启动交互式对话，输入内容回车发送，输入 exit 退出
```

> 项目默认走 DeepSeek 的 `deepseek-v4-flash` 模型。`main.go` 里演示了用 `WithAPIKey` 显式注入 key，实际使用请改用环境变量，避免把 key 提交进仓库。

## 项目结构

```
ai-test/
├── main.go                  # 交互式 CLI 入口：装配角色卡 + 流式对话循环
├── config/
│   ├── api.go               # LLM 客户端：chat/completions 协议
│   ├── openapi.go           # OpenAPIClient：Responses API 协议
│   ├── character.go         # 角色卡运行时：状态机 + 触发器 + prompt 组装
│   ├── character_test.go    # 角色卡单元测试
│   ├── openapi_test.go      # OpenAPIClient 测试（httptest.Server）
│   └── priestess.json       # 普瑞赛斯角色卡数据
├── test/                    # 学习示例代码（非自动化测试）
├── docs/                    # 架构笔记
├── 普瑞赛斯.md              # 角色协议原始伪代码（已落地为 priestess.json）
├── go.mod
└── CLAUDE.md                # 面向 Claude Code 的开发指引
```

## 核心架构与代码逻辑

### 1. 两个并行的 LLM 客户端

`config` 包里有两条互不依赖的调用链路，分别对应 DeepSeek 的两种协议：

| 客户端 | 文件 | 协议 | 默认地址 | 记忆类型 |
| --- | --- | --- | --- | --- |
| `LLM` | `api.go` | `POST /chat/completions` | `https://api.deepseek.com/chat/completions` | `history []map[string]any` |
| `OpenAPIClient` | `openapi.go` | `POST /responses` | `https://api.deepseek.com/responses` | `history []map[string]string`（mutex 保护） |

两者都用 `http.Client` 发送请求、手动解析 SSE 流，都实现了「非流式 + 三种流式」四类调用，但数据模型与请求体结构按各自协议独立定义，因此不会互相牵扯。

### 2. 函数式选项构造器

Go 没有函数重载和默认参数，所以 `NewClient` 用函数式选项模式解决「可选参数」问题：

```go
type Option func(*BaseConfig)

func WithAPIKey(key string) Option   { return func(c *BaseConfig) { c.apiKey = key } }
func WithBaseURL(url string) Option  { return func(c *BaseConfig) { c.baseUrl = url } }
func WithModel(name string) Option   { return func(c *BaseConfig) { c.modelName = name } }

func NewClient(opts ...Option) *LLM   // 无参 = 全默认；传参 = 覆盖单个字段
```

### 3. 三种流式接口，同一套底层

`LLM` 与 `OpenAPIClient` 各自实现三种流式接口，但最终都汇聚到同一套 SSE 解析逻辑：

- **回调式 `Stream`**：每收到一个增量就回调 `onDelta`。
- **通道式 `StreamChan`**：返回 `<-chan StreamDelta` 和 `<-chan error`，用 goroutine 边读边往 channel 写，Go 惯用的并发消费方式。
- **迭代器式 `StreamIter`**：返回 Go 1.23+ 的 `iter.Seq2`，可直接 `for d, err := range`。

DeepSeek 流式返回里，`reasoning_content`（思考）和 `content`（正文）是分开的字段，`StreamDelta` 用两个字段分别承载，调用方（`main.go` 的 `printDelta`）可以决定是否展示思考过程。

### 4. 短期记忆

两个客户端的记忆机制都遵循「**请求时快照、成功后才写回**」的原则，避免中间态污染跨轮上下文：

- `LLM`：`history []map[string]any` 跨 `InvokeWithTools` / `StreamChanWithTools` 复用。每次请求前 `copy` 一份工作消息，工具调用循环结束后，才把最终的 assistant 回复写回 history。超过 20 条时保留下标 0 的 system 消息、删除最旧的对话。
- `OpenAPIClient`：`history []map[string]string` 用 `sync.Mutex` 保护（并发安全），裁剪到最近 20 条，并对外暴露 `History()` / `AddHistory()` / `ClearHistory()`。

### 5. Function calling

以时间/日期工具为例（`TimeDateTools()` + `TimeDateHandler`）：

1. 请求体带上 `tools` 声明（`get_current_time` / `get_current_date`）。
2. 模型返回 `tool_calls` 时，程序解析参数、调用 handler 执行函数、把 `role:"tool"` 的结果回传。
3. 循环直到模型给出最终回答，期间的工具调用轮次对调用方透明。

`InvokeWithTools` 是非流式版本，`StreamChanWithTools` 是流式版本（`main.go` 用的就是它，所以角色对话里问「现在几点」模型也能拿到真实时间）。

### 6. 角色卡运行时（状态机）

这是整个项目「角色扮演」的核心，落地了 `普瑞赛斯.md` 里的协议伪代码，关键主张是：**状态由程序决定，不交给模型**。

```
用户输入
   ↓
Character.Update(input)      // 关键词触发器匹配 → 切换/回落状态
   ↓
Character.BuildSystemPrompt() // 按当前状态组装 system prompt
   ↓
LLM 流式生成                // 模型只负责「在这个状态下角色该怎么说」
```

- `config/priestess.json` 定义角色数据：身份、人格、语言风格、禁词、经典台词、五种状态（`normal` / `architect` / `obsessive` / `crisis` / `glitch`）、四组触发器。
- `config/character.go` 定义 `CharacterCard` / `Trigger` / `Character` 类型，以及 `LoadCharacter`（读 JSON）、`Update`（关键词匹配、未命中回落 `normal`）、`BuildSystemPrompt`（身份 + 人格 + 语言风格 + 当前状态 + 禁词约束 + 台词风格参考）。
- 状态跨轮保持（存在 `Character.mode` 里），所以角色会「记住」自己当前处于哪种情绪状态。

### 7. 入口 `main.go`

`main.go` 把上面各模块串成一个交互式循环：

```go
c   := config.NewClient(config.WithAPIKey(...))          // 创建客户端
char, _ := config.LoadCharacter("config/priestess.json") // 加载角色卡
tools := config.TimeDateTools()                          // 时间/日期工具

for scanner.Scan() {
    input  := scanner.Text()
    char.Update(input)                                   // 1. 状态机更新
    prompt := char.BuildSystemPrompt()                   // 2. 组装 prompt
    ch, errCh := c.StreamChanWithTools(prompt, input, tools, handler)
    for d := range ch { printDelta(d) }                  // 3. 流式消费
    if err := <-errCh; err != nil { ... }
}
```

## 测试

```bash
go test ./...                         # 全部测试
go test ./config/ -run TestLoadCharacter   # 单个测试
```

真正的测试与被测代码同目录、以 `_test.go` 结尾，用 `httptest.Server` 模拟服务端，不真实调用外部 API。`test/` 目录是学习示例（`User`、`Chan` 等类型），**不是**自动化测试。

## 安全

- 绝不提交 API key、token 或含私密数据的 prompt / 模型回复。
- 凭据与端点优先用环境变量（`OPENAI_API_KEY` / `DEEPSEEK_API_KEY`）。
- 若 key 曾被硬编码进源码，请立即吊销并重新生成。
