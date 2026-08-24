# 工具定义与注入使用手册

本文说明如何在 `config` 包外定义 Go 方法，并将它们注册给 `AgentLoop` 调用。推荐使用 `config.ToolRegistry`：它把提供给模型的工具声明和程序实际执行的方法绑定在一起，同时实现 `config.AgentToolExecutor`。

## 1. 工具调用的基本流程

```text
定义 Go handler
      ↓
RegisterFunction / Register
      ↓
registry.Tools() ── 工具声明发送给模型
registry.Execute() ← 模型返回工具名称和参数
      ↓
工具结果写回 AgentLoop
      ↓
模型生成最终答案
```

模型只能请求已经注册的工具。真正的函数执行、权限控制、参数校验和错误处理都由 Go 程序负责。

## 2. 定义工具方法

工具方法使用 `config.RegisteredToolHandler` 签名：

```go
type RegisteredToolHandler func(
    ctx context.Context,
    args map[string]any,
) (string, error)
```

- `ctx` 用于超时和取消传播。访问网络、数据库或其他外部资源时必须继续传递它。
- `args` 是模型返回的 JSON 参数，注册表会传入参数副本；handler 可以读取或修改它，不会修改 Agent 内部状态。
- 返回的字符串会作为工具结果交回模型。结构化结果应先用 `json.Marshal` 编码成 JSON 字符串。
- 业务失败必须返回 `error`，不要把错误文本伪装成成功结果。

示例工具方法：

```go
func getWeather(ctx context.Context, args map[string]any) (string, error) {
    city, ok := args["city"].(string)
    if !ok || strings.TrimSpace(city) == "" {
        return "", fmt.Errorf("参数 city 必须是非空字符串")
    }
    return queryWeather(ctx, city)
}
```

建议把参数读取和校验放在 handler 的最前面，并拒绝未知或类型不正确的参数。不要对未经检查的值直接做类型断言，否则异常参数可能导致 panic。

## 3. 声明参数 JSON Schema

模型需要通过工具声明知道工具名称、用途和参数格式。`RegisterFunction` 的第三个参数就是 JSON Schema：

```go
weatherParameters := map[string]any{
    "type": "object",
    "properties": map[string]any{
        "city": map[string]any{
            "type":        "string",
            "description": "城市名称，例如 Shanghai。",
        },
        "unit": map[string]any{
            "type": "string",
            "enum": []any{"celsius", "fahrenheit"},
        },
    },
    "required": []any{"city"},
    "additionalProperties": false,
}
```

Schema 负责向模型描述参数，不能替代服务端校验。handler 仍然必须检查必填字段、枚举值、长度、数值范围和访问权限。

无参数工具可以使用 `config.EmptyObjectSchema()`。

## 4. 注册外部方法

在业务代码中创建注册表，并调用 `RegisterFunction`：

```go
registry := config.NewToolRegistry()
err := registry.RegisterFunction(
    "get_weather",
    "获取指定城市的当前天气。",
    weatherParameters,
    getWeather,
)
if err != nil {
    return err
}
```

需要完整控制工具结构时，可以使用 `Register`：

```go
err := registry.Register(config.Tool{
    Type: "function",
    Function: config.Function{
        Name:        "get_weather",
        Description: "获取指定城市的当前天气。",
        Parameters:  weatherParameters,
    },
}, getWeather)
```

注册表约束：工具名称不能为空且必须唯一；handler 不能为 `nil`；未提供参数 Schema 时自动使用空对象 Schema；注册时会复制工具声明，外部后续修改原始 map 不会改变注册表内容。

## 5. 注入 AgentLoop

注册完成后，将同一个 registry 同时传给 `Tools` 和 `Executor`：

```go
loop := config.AgentLoop{
    Model:           modelAdapter,
    Tools:           registry.Tools(),
    Executor:        registry,
    InitialMessages: previousMessages,
    MaxSteps:        8,
}

result, err := loop.Run(ctx, userPrompt)
if err != nil {
    return err
}
fmt.Println(result.Answer)
```

`Tools()` 返回按注册顺序排列的声明副本。模型只会看到这些声明；`Execute` 根据模型返回的工具名称查找并调用对应 handler。不要分别维护工具声明列表和 handler map。

## 6. 上下文取消和超时

工具 handler 应尊重传入的 context，并将它继续传给网络或数据库 API：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
result, err := loop.Run(ctx, prompt)
```

取消发生后，`AgentLoop` 不会开始下一次工具调用；正在执行的 handler 能否立即停止，取决于它是否正确使用 `ctx`。

## 7. 返回结构化结果

工具结果接口是字符串。如果结果是对象或数组，请编码为 JSON：

```go
func listTasks(_ context.Context, _ map[string]any) (string, error) {
    tasks := []map[string]any{{"id": "task-1", "title": "整理文档", "done": false}}
    result, err := json.Marshal(tasks)
    if err != nil {
        return "", fmt.Errorf("编码任务结果: %w", err)
    }
    return string(result), nil
}
```

不要在结果中返回凭据、访问令牌、完整数据库记录或其他敏感信息。写入日志前也要单独做脱敏。

## 8. 错误和日志

注册表会对未知工具返回错误。`AgentLoop.Run` 会将工具错误包装为包含工具名和 step 的错误。上层可以记录错误、终止本轮，或根据业务策略重新发起请求；是否重试必须由程序决定。

## 9. 测试工具方法

工具测试放在 `test/`，不要访问真实 API。至少验证工具声明、参数传递、未知工具、重复注册、handler 错误和 context 取消：

```go
func TestRegistryExecutesExternalTool(t *testing.T) {
    registry := config.NewToolRegistry()
    if err := registry.RegisterFunction(
        "echo",
        "回显文本。",
        map[string]any{
            "type": "object",
            "properties": map[string]any{
                "text": map[string]any{"type": "string"},
            },
            "required": []any{"text"},
        },
        func(_ context.Context, args map[string]any) (string, error) {
            text, ok := args["text"].(string)
            if !ok {
                return "", fmt.Errorf("text 参数类型错误")
            }
            return text, nil
        },
    ); err != nil {
        t.Fatal(err)
    }

    result, err := registry.Execute(context.Background(), config.AgentToolCall{
        Name: "echo", Arguments: map[string]any{"text": "hello"},
    })
    if err != nil || result != "hello" {
        t.Fatalf("result=%q err=%v", result, err)
    }
}
```

## 10. 安全检查清单

- 不在工具描述、参数默认值或返回结果中写入 API Key 和其他秘密。
- 对字符串长度、数值范围、枚举值和资源 ID 做服务端校验。
- 对有副作用的工具增加权限检查、审批或幂等键。
- 为网络和数据库工具设置超时，并使用支持取消的 API。
- 日志记录工具名、调用 ID、耗时和错误即可；参数和结果按敏感级别脱敏。
- 将 `MaxSteps` 设置为符合业务的较小值，避免异常循环消耗资源。

