# 天气 MCP 接入

本项目的 Agent 会在启动时连接一个 stdio MCP 服务，并把服务发现到的工具注册到同一个 `ToolRegistry`。天气服务使用 `C:\work\code\tool\mcp\dist\index.js`，提供：

- `mcp_weather_get_weather_by_region`
- `mcp_weather_get_weather_by_coordinates`

Agent 不直接执行 MCP 方法。模型先看到 `tools/list` 返回的 JSON Schema，再由 `ToolRegistry.Execute` 通过 `tools/call` 把结果交回 AgentLoop。

## 准备天气服务

```powershell
cd C:\work\code\tool\mcp
npm install
npm run build
Test-Path .\dist\index.js
```

推荐让客户端直接启动构建产物。stdio 服务启动后不会输出网址，也不应使用浏览器打开。

## 启动本项目 Agent

API Key 使用环境变量：

```powershell
$env:OPENAI_API_KEY = "sk-..."
go run .
```

默认天气 MCP 配置为：

```text
command: node
args: C:\work\code\tool\mcp\dist\index.js
```

可用环境变量：

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `MCP_WEATHER_PATH` | `C:\work\code\tool\mcp\dist\index.js` | MCP 服务入口路径 |
| `MCP_NODE_COMMAND` | `node` | Node.js 可执行文件 |
| `MCP_WEATHER_ENABLED` | 启用 | 设置为 `false` 可在没有 Node/MCP 服务时运行本地工具 |

例如使用项目内的另一个 MCP 服务：

```powershell
$env:MCP_WEATHER_PATH = "D:\tools\weather\dist\index.js"
go run .
```

## 客户端配置

下面的配置适用于支持 stdio MCP 的客户端。Windows JSON 路径中的反斜杠必须写成 `\\`。

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

Claude Desktop 配置文件：`%APPDATA%\\Claude\\claude_desktop_config.json`。保存后完全退出并重新打开客户端。

Cursor 可放在 Settings → MCP 或项目的 `.cursor/mcp.json`。

支持 MCP 注册命令的 Codex CLI：

```powershell
codex mcp add open-meteo-weather -- node C:\work\code\tool\mcp\dist\index.js
codex mcp list
```

## 故障排查

- `dist/index.js` 不存在：在 MCP 项目执行 `npm run build`。
- Agent 启动提示连接失败：检查 `node --version`、`MCP_WEATHER_PATH` 和网络访问 Open-Meteo 的权限。
- 不想启用天气服务：设置 `$env:MCP_WEATHER_ENABLED = "false"`。
- 服务没有 URL 或终端输出：这是 stdio MCP 的正常行为，客户端通过 stdin/stdout 通信。

MCP 服务进程由 `config.StdioMCPClient` 管理；Agent 退出时会关闭子进程，工具调用也会继承 `context.Context` 的取消信号。
