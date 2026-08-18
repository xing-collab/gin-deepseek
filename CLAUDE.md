# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
go run .              # 启动交互式对话 CLI
go build ./...        # 编译所有包
go test ./...         # 运行全部测试
go test ./config/ -run TestName   # 运行单个测试（例如 -run TestLoadCharacter）
gofmt -l .            # 列出格式不规范的 Go 文件；gofmt -w . 自动格式化
go vet ./...          # 静态检查
```

## Architecture

Go module `ai-test`（Go 1.25），包 `config` 是核心，`main.go` 是交互式 CLI。

**两个并行的 LLM 客户端**（都在 `config` 包，协议不同、互不依赖）：

- `LLM`（`config/api.go`）— 走 `POST /chat/completions`。用函数式选项构造：`NewClient(WithAPIKey(...), WithBaseURL(...), WithModel(...))`。默认 base URL `https://api.deepseek.com/chat/completions`。
- `OpenAPIClient`（`config/openapi.go`）— 走 OpenAI Responses API（`POST /responses`），默认 `https://api.deepseek.com/responses`。历史记录用 mutex 保护并截断到 20 条。

**短期记忆**：`LLM` 用统一的 `history []map[string]any` 跨 `InvokeWithTools` / `StreamChanWithTools` 复用；每次请求前 `copy` 一份工作消息，工具调用结束才把最终 assistant 回复写回 history，避免污染跨轮上下文。

**三种流式接口**：回调式（`Stream`）、通道式（`StreamChan`）、迭代器式（`StreamIter`），`LLM` 和 `OpenAPIClient` 各自实现一套。DeepSeek 的 `reasoning_content`（思考内容）与 `content`（正文）分开返回。

**角色卡运行时**（`config/character.go` + `config/priestess.json`）：状态机 + 关键词触发器 + 每轮动态组装 system prompt。流程是 `Character.Update(input)` 匹配触发器切换状态 → `BuildSystemPrompt()` 生成 prompt → 传给 LLM。**状态由程序决定，不交给模型**。

**Function calling**：`config.TimeDateTools()` + `config.TimeDateHandler` 提供时间/日期工具，供 `StreamChanWithTools` 使用。

## 目录约定

- `test/` 是学习示例代码（`User`、`Chan` 等），**不是自动化测试**。真正的测试与被测代码同目录、以 `_test.go` 结尾，用 `httptest.Server` 模拟服务端。
- `docs/` 存放架构笔记（streaming-patterns.md、llm-class-structure.md、api-formats-and-tools.md、deepseek.md），不是生成的 API 文档。
- `普瑞赛斯.md` 是角色协议原始伪代码，已落地为 `config/priestess.json`。

## 安全

绝不提交 API key、token、含私密数据的 prompt 或模型回复。凭据和端点优先用环境变量（`OpenAPIClient` 已用 `DEEPSEEK_API_KEY`；`LLM` 通过 `WithAPIKey` 注入）。
