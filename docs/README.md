# 文档索引

## Agent 与工具

- [agent-loop.md](agent-loop.md)：AgentLoop、transcript、最大步数和执行边界。
- [tool-use.md](tool-use.md)：本地 Go Tool 的注册、Schema、参数转换和测试。
- [mcp-skill-use.md](mcp-skill-use.md)：MCP、本地 Tool、Skill 的统一接入模型和当前支持矩阵。
- [mcp-stdio.md](mcp-stdio.md)：Open-Meteo stdio MCP 配置，以及 stdio/HTTP MCP 对比。

## API 与协议

- [api-formats-and-tools.md](api-formats-and-tools.md)：Chat Completions、Responses 和 function calling wire format。
- [streaming-patterns.md](streaming-patterns.md)：回调、channel、迭代器三种流式调用。
- [deepseek.md](deepseek.md)：LLM/Agent 学习路线和项目代码映射。

## 应用结构

- [character-card-application-agent.md](character-card-application-agent.md)：角色卡和应用 Agent 的职责边界。
- [llm-class-structure.md](llm-class-structure.md)：Go struct、构造器和 Java 类的对照说明。

## 推荐阅读顺序

1. 先看 [agent-loop.md](agent-loop.md) 了解调度链；
2. 添加进程内函数时看 [tool-use.md](tool-use.md)；
3. 接外部工具时看 [mcp-skill-use.md](mcp-skill-use.md) 和 [mcp-stdio.md](mcp-stdio.md)；
4. 修改模型协议或流式行为时看 API 和 streaming 文档。
