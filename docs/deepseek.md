# LLM 与 Agent 学习路线

> 目标：以本项目（Go 编写的 LLM 聊天客户端）为载体，系统掌握 LLM 与 Agent 的基本机制和技术原理。
> 阅读前提：Java 基础，正在学 Go。
> 配套文档：`api-formats-and-tools.md`（两种 API 格式 + Tool）、`streaming-patterns.md`（流式三范式）、`llm-class-structure.md`（Go/Java 对照）。

## 一句话总结

LLM 只是"根据输入预测下一个 token"的模型；**Agent 是在 LLM 外面套一层控制循环**——让模型反复"观察 → 思考 → 行动（调工具）→ 再观察"，直到完成任务。学习路线就是：先吃透"模型本身怎么调用"（LLM API / KV Cache），再吃透"怎么让它动起来"（Agent Loop / Tool Use / Reasoning / Planning），最后吃透"怎么让它可靠、可扩展、可协作"（Memory / Skills / MCP / Subagent / Multi-Agent），全程用 Prompt / Context / Harness 三项工程能力兜底。

---

## 一、学习地图总览

把要学的名词按层级分类，先建立全景图，再逐层深入：

| 层级 | 名词 | 一句话定位 | 本项目落点 |
|---|---|---|---|
| **基础层** | LLM API | 怎么用 HTTP 调模型 | `config/api.go` / `config/openapi.go` |
| | KV Cache | 推理为什么快/为什么吃内存 | 多轮对话的上下文增长 |
| **Agent 核心** | Agent Loop | Agent 的运行骨架 | `StreamChanWithTools` 的 `for` 循环 |
| | Tool Use | 模型"点菜"、客户端"做菜" | `TimeDateTools` / `TimeDateHandler` |
| | Reasoning | 模型先思考再回答 | `ReasoningContent` / `thinking` 字段 |
| | Planning | 把大任务拆成步骤 | 工具调用循环的扩展方向 |
| | Memory | 记住上下文 | `history` / `AddHistory` / 窗口裁剪 |
| **接入标准化** | Skills | 可复用的能力包 | （扩展方向） |
| | MCP | 工具/资源的标准接入协议 | （扩展方向） |
| **多智能体** | Subagent | 委托子任务的子代理 | （扩展方向） |
| | Multi-Agent | 多代理协作 | （扩展方向） |
| **工程三件套** | Prompt Engineering | 说什么 | `prompt` / `instructions` |
| | Context Engineering | 给什么 | 历史窗口 / 裁剪 / 检索 |
| | Harness Engineering | 怎么验证 | `*_test.go` 单测 |

**建议阅读顺序**：按表格从上到下推进——先基础层，再 Agent 核心，再接入与多智能体，工程三件套贯穿始终（不是单独一步，而是每个阶段都要用）。

---

## 二、专有名词解释

### 2.1 基础层

#### LLM API（大模型接口）

**是什么**：通过 HTTP 请求让大模型生成文本的接口。OpenAI 家族有两条主线：

- **Chat Completions**（`/v1/chat/completions`，2023 推出）：请求体是 `messages` 数组，响应在 `choices[0].message.content`。本项目 `config/api.go` 实现的就是这条。
- **Responses**（`/v1/responses`，2025 推出）：统一 Chat 与 Assistant 接口，请求体是 `input` + `instructions`，响应是 `output[]` 数组。本项目 `config/openapi.go` 实现的是这条。

**为什么重要**：一切 Agent 的地基。不了解请求/响应字段，就无法理解工具调用、思考过程、记忆这些上层概念到底在协议里长什么样。

**本项目落点**：两份客户端源码并存，正好对比两种格式。详细对照见 `docs/api-formats-and-tools.md`。

**深入方向**：请求参数（`temperature`、`max_output_tokens`、`stream`）、鉴权（`Authorization: Bearer`）、错误码处理。

#### KV Cache（键值缓存）

**是什么**：Transformer 模型逐 token 生成时，每一步都要计算所有历史 token 的注意力（Attention）。KV Cache 把已经算过的 Key/Value 矩阵缓存下来，生成第 N 个 token 时只需计算新 token 的注意力，不用重算前 N-1 个——**这是推理能实时吐字的关键优化**。

**为什么重要**：
- 生成速度从 O(n²) 降到 O(n)（每步），但**显存占用随上下文长度线性增长**——这就是为什么超长上下文又贵又慢。
- 多轮对话把历史全塞进上下文，等于让模型"从头算一遍历史"，所以 Agent 的 Memory 一定要做**裁剪/压缩**，否则成本失控。

**本项目落点**：`api.go` 的 `AddHistory` 把历史裁剪到 20 条；`openapi.go` 的 `maxHistoryMessages = 20` 是同一个思路——**不是模型记不住，而是省 KV Cache / 省 token / 省延迟**。

**深入方向**：Prompt Caching（API 层对重复前缀的缓存复用）、上下文压缩（把长历史摘要成短历史）。

---

### 2.2 Agent 核心

#### Agent Loop（智能体循环）

**是什么**：Agent 的运行骨架，一个循环：

```
观察（Observe） → 思考/决策（Reason/Decide） → 行动（Act，调用工具） → 观察结果 → ... → 完成
```

**为什么重要**：这是 Agent 和"单次问答 LLM"的本质区别。单次问答：一问一答就结束；Agent：模型反复决定下一步，直到任务完成。

**本项目落点**：`config/api.go` 的 `InvokeWithTools` 和 `StreamChanWithTools` 里那个 `for { ... }` 就是最朴素的 Agent Loop——每轮请求 → 看有没有 `tool_calls` → 有就执行、回传 → 再请求，直到模型不再要工具、直接给答案。

**深入方向**：ReAct（Reasoning + Acting 交替）、带最大轮数上限（防止死循环）、带终止条件。

#### Tool Use / Function Calling（工具调用）

**是什么**：**模型不执行任何函数**。它只输出一个结构化请求："我想调用 `get_current_time`，参数是 `{}`"。真正执行函数的是客户端（harness），执行完把结果作为 `role: "tool"` 消息回传给模型，模型再基于结果生成最终回答。

**为什么重要**：这是 Agent 的"手"。没有工具，模型只能空谈；有工具，模型能查时间、查数据库、发请求、写文件。

**本项目落点**（完整五步循环已实现）：
1. 请求携带 `tools` 数组声明可调函数 → `TimeDateTools()`
2. 模型返回 `tool_calls`（函数名 + JSON 参数）→ `ToolCall` 结构
3. 客户端解析参数 → `json.Unmarshal(..., &args)`
4. 执行对应函数 → `TimeDateHandler(name, args)` 的 `switch`
5. 把结果作为 `role:"tool"` 消息回传 → 再请求

**深入方向**：并行调用多个工具、工具参数校验、流式 `tool_calls` 的增量拼接（本项目 `doStreamToolRound` 已按 `index` 拼接）。

#### Reasoning（推理/思考）

**是什么**：推理模型在给出正式回答前，先生成一段"思考过程"（Chain-of-Thought），再生成正文。思考过程在协议里是独立字段（如 `reasoning_content` / `thinking`），可以和正文分开显示、甚至不记录进历史。

**为什么重要**：复杂问题（数学、代码、多步规划）靠"先想再答"显著提升准确率。理解它才能理解"为什么模型有时要先显示一段灰字思考"。

**本项目落点**：
- `StreamDelta.ReasoningContent` 与 `Content` 分开——`main.go` 的 `printDelta` 用蓝色打印思考、默认色打印正文。
- `api.go` 请求里的 `Thinking: map[string]string{"type": "enabled"}` 和 `ReasoningEffort: "medium"` 控制思考开关与强度。
- `openapi.go` 的 `StreamChanContext` 里 `strings.Contains(event.Type, "reasoning")` 区分思考与正文。

**深入方向**：`reasoning_effort` 的档位对效果/成本的影响、思考过程是否回填进上下文。

#### Planning（规划）

**是什么**：面对复杂任务，Agent 先拆解出若干子步骤，再逐步执行。常见范式：Plan-and-Execute（先列计划再执行）、ReAct（边想边做）。可以靠提示词引导，也可以靠工具返回一个显式的 plan。

**为什么重要**：单步 Tool Use 只能解决"一次调用一个工具"的简单任务；复杂任务需要"先规划、再分步执行、必要时修订计划"。

**本项目落点**：目前只有单层工具调用循环，是 Planning 的**地基**。扩展方向：给模型加一个"先输出计划"的引导，或在 handler 里支持"工具 A 的结果决定下一步调用工具 B"的多步编排。

**深入方向**：任务分解、依赖关系、计划失败后的重规划。

#### Memory（记忆）

**是什么**：Agent 记住信息的能力，分两种：
- **短期记忆**：当前对话窗口里的历史消息（本项目 `history`）。
- **长期记忆**：跨会话持久化的信息（向量数据库、RAG、语义记忆），不在本项目范围内。

**为什么重要**：没有记忆的 Agent 每句话都像失忆；记忆窗口又是成本/延迟大户，所以要**管理**（裁剪、摘要、检索）。

**本项目落点**：
- `api.go` 的 `history []map[string]string` + `AddHistory`（超过 20 条删最旧对话，保留 system）。
- `openapi.go` 的 `mu sync.Mutex` + `History/AddHistory/ClearHistory`（并发安全的短期记忆），并有 `openapi_test.go` 覆盖裁剪、深拷贝、并发。

**深入方向**：滑动窗口、摘要压缩、RAG（检索增强）、向量库、`role` 语义（system/user/assistant/tool）。

---

### 2.3 接入标准化

#### Skills（技能）

**是什么**：把"一组指令 + 配套代码/资源"打包成可复用、可动态加载的能力单元（如 Anthropic 的 Agent Skills，一个 `SKILL.md` + 脚本目录）。Agent 按需加载技能，而不是把所有能力都塞进 system prompt。

**为什么重要**：解决"能力越多、提示词越臃肿"的问题——按需注入，既省上下文又易维护。

**本项目落点**：`TimeDateTools` + `TimeDateHandler` 已经是"技能"的雏形（能力被打包成一个函数，按需声明）。真正的 Skills 更进一步：包含说明文档、触发条件、可执行脚本。

**深入方向**：技能发现、按需加载、与 MCP 的区别（Skills 是"能力包"，MCP 是"接入协议"）。

#### MCP（Model Context Protocol，模型上下文协议）

**是什么**：一个**标准协议**（Anthropic 发起、已开放），定义模型如何标准化地发现并调用外部工具、数据源、提示词——类似"AI 世界的 USB-C"。通过 MCP Server 暴露能力，MCP Client（Agent）统一接入。

**为什么重要**：没有标准协议时，每接一个新工具都要写一套胶水代码；MCP 让"工具接入"变成可插拔的。

**本项目落点**：目前工具是**硬编码 Go 函数**（`TimeDateHandler`）。MCP 的扩展方向：把工具改造成"从 MCP Server 发现并调用"，客户端不再硬编码工具清单。

**深入方向**：MCP 的三种原语（tools / resources / prompts）、stdio 与 HTTP 传输、用现成 SDK 搭一个 MCP Server。

---

### 2.4 多智能体

#### Subagent（子代理）

**是什么**：主 Agent 把某个子任务**委托**给一个独立的子 Agent 去完成，子 Agent 有自己的上下文窗口和执行循环，完成后把结果返回主 Agent。

**为什么重要**：
- **隔离上下文**：子任务不污染主 Agent 的上下文窗口（主 Agent 只看结果）。
- **并行**：多个子任务可同时派给多个 subagent。

**本项目落点**：无（单客户端）。扩展方向：在工具处理器里，某个"工具"其实是"启动一个子 LLM 客户端跑一个子任务"，主客户端只接收它的返回。

**深入方向**：任务委派边界、上下文隔离、子代理的预算与超时控制。

#### Multi-Agent（多智能体协作）

**是什么**：多个 Agent 相互协作完成一个共同目标。两种主流组织方式：
- **编排式（Orchestrator）**：一个主控 Agent 分配任务、汇总结果。
- **涌现式（Swarm / 自治）**：多个对等 Agent 各自决策、相互通信。

**为什么重要**：单一 Agent 受上下文、专精度、可靠性限制；多 Agent 可以把"专家分工"引入 AI 系统（例如：一个负责写代码、一个负责 review、一个负责测试）。

**本项目落点**：无（单客户端）。扩展方向：用多个 `LLM` 实例分别扮演不同角色，通过共享的状态/消息队列协作。

**深入方向**：通信协议（消息传递 vs 共享黑板）、角色设计、冲突解决、成本控制。

---

### 2.5 工程三件套

三个词都带 Engineering，但对象不同，一句话区分：

| | 管什么 | 一句话 |
|---|---|---|
| **Prompt Engineering** | 说什么 | 怎么设计指令让模型表现好 |
| **Context Engineering** | 给什么 | 怎么选择、组织、压缩喂进窗口的信息 |
| **Harness Engineering** | 怎么验证 | 怎么搭测试/评估/观测框架，保证改动能回归 |

#### Prompt Engineering（提示词工程）

**是什么**：设计系统提示词与用户消息，引导模型输出期望行为。包括角色设定、指令清晰度、少样本示例（few-shot）、输出格式约束等。

**本项目落点**：`main.go` 的 `prompt`（阿米娅角色设定）、`api.go` 里 `AskCurrentTime` 的"当用户询问时间时调用 get_current_time……"引导语——这就是 prompt 在指挥工具调用。

**深入方向**：角色扮演、结构化输出（JSON Schema）、CoT 提示、few-shot、负面示例。

#### Context Engineering（上下文工程）

**是什么**：管理"进入上下文窗口的内容"——选什么、放什么顺序、怎么压缩、何时检索。它是 Memory 和 RAG 的上位概念：**上下文不是越多越好，而是越"精"越好**。

**本项目落点**：`AddHistory` 的窗口裁剪（`maxHistoryMessages = 20`）、`history[0]` 固定保留 system、深拷贝避免并发污染——都是最基础的 context engineering。

**深入方向**：上下文预算分配、检索式上下文（RAG）、摘要式压缩、工具结果的精炼。

#### Harness Engineering（测试框架工程）

**是什么**：围绕 Agent 搭"工程脚手架"——评估、测试、追踪、观测。核心目标：**改一行提示词或代码，能立刻知道是变好还是变坏**。

**本项目落点**：`config/openapi_test.go` 用 `httptest.Server` 模拟 API、覆盖历史裁剪/深拷贝/并发/流式错误等，就是 harness 的雏形——**测试不碰真实 API**（AGENTS.md 要求）。

**深入方向**：LLM 评估集（eval set）、断言式 vs 模型判分式评估、链路追踪（tracing）、成本/延迟观测。

---

## 三、分阶段学习路线

每阶段给出：**目标 → 关键动作 → 本项目落点 → 验收标准**。按顺序推进，每阶段都用"工程三件套"的视角自我检查。

### Phase 0：跑通与热身
- **目标**：环境就绪，理解项目全貌。
- **动作**：`go run .` 跑通交互（需设置 `OPENAI_API_KEY` 环境变量）；读 `main.go` 的 CLI 循环。
- **落点**：`main.go`、`AGENTS.md`。
- **验收**：能解释"输入一句话到打印回复"的完整调用链。

### Phase 1：吃透 LLM API
- **目标**：掌握两种 API 协议的字段与差异。
- **动作**：逐行读 `config/api.go`（chat 格式）和 `config/openapi.go`（responses 格式）；对比请求体、响应体、流式结构。
- **落点**：`docs/api-formats-and-tools.md`。
- **验收**：能画出两种协议的请求/响应 JSON 结构图，说出为什么网关只支持 chat 会导致 `NewOpenAPIClient()` 报 502。

### Phase 2：流式与并发（Go 重点）
- **目标**：理解 SSE 与三种流式范式，顺带吃透 Go 的 goroutine/channel。
- **动作**：读 `Stream` / `StreamChan` / `StreamIter` 和 `scanSSE`。
- **落点**：`docs/streaming-patterns.md`。
- **验收**：能说清回调式/通道式/迭代器式的取舍，以及 `errCh` 缓冲为何是 1。

### Phase 3：Tool Use 落地
- **目标**：完整走通 function calling 五步循环。
- **动作**：读 `Tool`/`ToolCall`/`TimeDateTools`/`TimeDateHandler`/`InvokeWithTools`/`StreamChanWithTools`；在 `main.go` 里试"现在几点""今天几号"。
- **落点**：`config/api.go` 后半段、`docs/api-formats-and-tools.md` 第 6 节。
- **验收**：能自己加一个新工具（比如"获取天气"的 mock），并让模型在合适时调用。

### Phase 4：Reasoning 与 Memory
- **目标**：理解思考过程字段与上下文窗口管理。
- **动作**：观察 `ReasoningContent` 与 `Content` 的分离；改 `AddHistory` 的裁剪阈值，观察对回答与成本的影响；读 `openapi.go` 的并发记忆与对应测试。
- **落点**：`config/openapi.go`、`config/openapi_test.go`。
- **验收**：能解释"为什么要裁剪历史""思考过程为什么不回填正文"。

### Phase 5：组装真正的 Agent Loop + Planning
- **目标**：把单层工具循环升级为带规划、带轮数上限的 Agent。
- **动作**：给 `InvokeWithTools` 加最大轮数；给 prompt 加"先列计划再执行"的引导；支持"工具 A 结果决定调用工具 B"的多步编排。
- **落点**：在 `api.go` 基础上扩展（教学性改动，可另开分支）。
- **验收**：能跑一个"分两步完成"的任务，Agent 自主决定调用顺序。

### Phase 6：标准化与多智能体
- **目标**：理解 Skills / MCP / Subagent / Multi-Agent 的边界与适用场景。
- **动作**：把工具改造成可插拔（先抽象 `ToolExecutor` 接口，再尝试接一个 MCP Server）；用两个 `LLM` 实例做一次"角色分工"实验。
- **落点**：本项目目前无对应代码，属扩展方向。
- **验收**：能说清"Skills vs MCP vs 硬编码工具"的取舍，能画一张 subagent 委托时序图。

### Phase 7：工程三件套贯穿复盘
- **目标**：用三个 Engineering 视角审视全局。
- **动作**：为新增工具补 `httptest` 测试（Harness）；整理上下文预算（Context）；打磨引导语（Prompt）。
- **落点**：`*_test.go`、`history`、`prompt`。
- **验收**：改任意一处后，`go build ./... && go vet ./... && go test ./...` 全绿。

---

## 四、概念 → 本项目代码映射速查

| 概念 | 代码位置 | 关键符号 |
|---|---|---|
| LLM API（chat） | `config/api.go` | `ApiRequest` / `ApiResponse` / `send` |
| LLM API（responses） | `config/openapi.go` | `OpenAPIRequest` / `OpenAPIResponse` |
| 流式三范式 | `config/api.go` | `Stream` / `StreamChan` / `StreamIter` |
| SSE 解析 | `config/api.go` | `scanSSE` / `streamChunk` |
| Tool Use（声明） | `config/api.go` | `Tool` / `Function` / `TimeDateTools` |
| Tool Use（响应） | `config/api.go` | `ToolCall` / `streamToolCall` |
| Agent Loop | `config/api.go` | `InvokeWithTools` / `StreamChanWithTools` 的 `for` |
| Reasoning | `config/api.go` | `ReasoningContent` / `Thinking` / `ReasoningEffort` |
| Memory（短期） | `config/api.go` | `history` / `AddHistory` |
| Memory（并发安全） | `config/openapi.go` | `mu sync.Mutex` / `History` / `ClearHistory` |
| Harness（测试） | `config/openapi_test.go` | `httptest.Server` 系列测试 |

---

## 五、推荐实践顺序（动手清单）

1. 跑通本项目，画出一次"问时间"的完整时序图（HTTP 请求 → 工具调用 → 回传 → 回答）。
2. 给 `TimeDateTools` 加一个"获取今天是星期几"的工具，让模型在合适时调用。
3. 把 `InvokeWithTools` 加上"最多 5 轮"的保护，防止死循环。
4. 给 `api.go` 的 `AddHistory` 写单元测试（参考 `openapi_test.go` 的 `httptest` 风格）。
5. 抽象一个 `ToolExecutor` 接口，把硬编码的 `switch` 改成可插拔（为 MCP 做铺垫）。
6. 读官方文档：OpenAI Responses API / Chat Completions、Anthropic Agent Skills、MCP 规范。

---

## 附录：术语速查卡

- **LLM API**：调模型的 HTTP 接口（chat/completions 与 responses 两套）。
- **KV Cache**：缓存注意力矩阵以加速逐 token 生成，代价是显存随上下文增长。
- **Agent Loop**：观察 → 决策 → 行动 → 观察 的循环骨架。
- **Tool Use**：模型输出"想调哪个函数 + 参数"，客户端执行并回传结果。
- **Reasoning**：模型先产出思考过程再给正式回答。
- **Planning**：把复杂任务拆成步骤逐步执行。
- **Memory**：短期（对话窗口）+ 长期（外部存储）记忆。
- **Skills**：指令 + 代码打包的可复用能力单元。
- **MCP**：工具/资源/提示词的标准化接入协议。
- **Subagent**：主代理委托子任务的独立子代理。
- **Multi-Agent**：多代理协作（编排式 / 涌现式）。
- **Prompt Engineering**：设计指令（说什么）。
- **Context Engineering**：管理上下文（给什么）。
- **Harness Engineering**：搭测试评估框架（怎么验证）。
