# Open-Meteo 天气 MCP：stdio 接入

本项目当前实际接入的是 stdio MCP。Go CLI 启动 Node 子进程，通过 stdin/stdout 传输 MCP JSON-RPC 消息；它不会监听 HTTP 端口。

## 1. 准备服务

```powershell
cd C:\work\code\tool\mcp
npm install
npm run build
Test-Path .\dist\index.js
```

推荐直接运行构建产物：

```powershell
node C:\work\code\tool\mcp\dist\index.js
```

命令启动后没有网址或普通输出是正常的，因为它正在等待 MCP 客户端通过 stdio 连接。

## 2. Go Agent 配置

CLI 默认使用：

```text
command = node
args    = C:\work\code\tool\mcp\dist\index.js
```

可用环境变量：

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `MCP_WEATHER_PATH` | `C:\work\code\tool\mcp\dist\index.js` | MCP 入口文件 |
| `MCP_NODE_COMMAND` | `node` | Node 可执行文件路径或命令名 |
| `MCP_WEATHER_ENABLED` | 启用 | 设置为 `false` 禁用天气 MCP |

```powershell
$env:OPENAI_API_KEY = "sk-..."
$env:MCP_WEATHER_PATH = "C:\work\code\tool\mcp\dist\index.js"
go run .
```

Go 侧启动流程：

```text
NewStdioMCPClient
  → initialize
  → notifications/initialized
  → tools/list
  → RegisterMCPTools
  → AgentLoop 执行 tools/call
```

## 3. Claude、Cursor、Codex 配置

通用 Windows JSON：

```json
{
  "mcpServers": {
    "open-meteo-weather": {
      "command": "node",
      "args": ["C:\\work\\code\\tool\\mcp\\dist\\index.js"]
    }
  }
}
```

Claude Desktop：`%APPDATA%\\Claude\\claude_desktop_config.json`。

Cursor：Settings → MCP 或项目 `.cursor/mcp.json`。

Codex CLI：

```powershell
codex mcp add open-meteo-weather -- node C:\work\code\tool\mcp\dist\index.js
codex mcp list
```

这些是“外部 MCP 客户端直接连接天气 server”的配置，不是 Go CLI 的配置。Go CLI 会自己启动一个独立的天气子进程。

## 4. stdio 与 HTTP MCP 的区别

| 项目 | stdio | Streamable HTTP |
| --- | --- | --- |
| server 位置 | 本机子进程 | 独立 HTTP 服务，可远程 |
| 启动责任 | client 启动和关闭 server | server 独立部署，client 只连接 URL |
| 通信介质 | stdin/stdout | HTTP 请求/响应，必要时流式返回 |
| 端口 | 不需要 | 需要监听端口或网关地址 |
| 适合场景 | 本地开发、桌面客户端、单机工具 | 多客户端共享、容器、远程服务 |
| 运维 | 依赖本机 Node/脚本/权限 | 需要服务发现、鉴权、TLS、监控 |
| 当前 Go 项目支持 | 支持 | 不支持 |

旧版 MCP 还存在基于 SSE 的 HTTP transport。它与当前推荐的 Streamable HTTP 不完全相同；本项目两种 HTTP MCP transport 都没有实现。

## 5. 当前项目是否支持 HTTP MCP

结论：不支持。

当前代码中的 HTTP 能力是：

- `config/api.go`：通过 HTTP 调用 LLM Chat Completions；
- `config/openapi.go`：通过 HTTP 调用 Responses API；
- 业务本地 Tool 可以自行使用 Go `net/http` 调外部业务 API。

这些都不是 MCP HTTP client。当前 MCP 接口只有：

```go
type MCPClient interface {
    ListTools(context.Context) ([]MCPTool, error)
    CallTool(context.Context, string, map[string]any) (any, error)
}
```

任何实现了这个接口的 SDK client 都可以注册到 `RegisterMCPTools`，但仓库目前只提供 `StdioMCPClient`，没有 `NewHTTPMCPClient` 或 URL 配置。

### 5.1 使用配置的差异

stdio 配置描述的是“如何启动进程”：

```json
{
  "command": "node",
  "args": ["C:\\tools\\weather\\dist\\index.js"],
  "cwd": "C:\\tools\\weather"
}
```

HTTP MCP 配置描述的是“连接哪个已部署服务”：

```json
{
  "url": "https://mcp.example.com/mcp",
  "headers": {
    "Authorization": "Bearer ${MCP_TOKEN}"
  }
}
```

第二段只是传输配置示意，当前 Go CLI 不能读取或连接它。

### 5.2 后续支持 HTTP MCP 的落点

如果要增加支持，推荐新增实现 `MCPClient` 的 `HTTPMCPClient`，而不是修改 `AgentLoop`：

```text
HTTPMCPClient
  ├─ 连接 Streamable HTTP endpoint
  ├─ initialize
  ├─ tools/list
  └─ tools/call
        ↓
RegisterMCPTools
        ↓
现有 ToolRegistry 和 AgentLoop
```

还需要补充 URL、请求头/令牌、TLS、超时、会话 ID、流式响应和重连策略。传输实现完成后，本地 Tool、Skill 和 AgentLoop 层不需要改变。

## 6. 故障排查

- 找不到 `dist/index.js`：执行 `npm run build`。
- Go CLI 启动失败：检查 `node --version`、`MCP_WEATHER_PATH` 和文件权限。
- 查询中文城市失败：当前 Go 侧对常见城市有英文别名回退，例如 `上海 → Shanghai`。
- 没有 URL 或端口：stdio 服务不提供 HTTP URL，这是正常的。
- 看到 `API server listening at: 127.0.0.1:xxxxx`：通常是 GoLand/Delve 调试器端口，不是天气 MCP HTTP 服务。
