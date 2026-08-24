# 工具定义与注入使用手册

本文说明如何在 `config` 包外定义业务方法，并以注册方式注入 `AgentLoop`。

推荐的分层方式是：

- 简单工具直接使用普通 Go 函数，注册器通过反射推断参数类型；
- 复杂参数或需要严格类型约束时使用参数结构体；
- `ToolRegistry` 负责工具名称、描述、JSON Schema 和 handler 的绑定；
- 注册表在 Agent 边界接收 `map[string]any`，自动转换为业务参数类型；
- `AgentLoop` 只负责模型决策、工具执行和 transcript 维护。

因此，业务方法不需要全部手写 `args map[string]any`。只有直接实现底层 `RegisteredToolHandler` 时才需要使用这个签名。

## 1. 调用流程

```text
定义普通 Go 函数或参数结构体
          ↓
RegisterReflectFunction / RegisterTypedFunction
          ↓
registry.Tools() ── 工具声明发送给模型
registry.Execute() ← 模型返回名称和 JSON 参数
          ↓
参数转换为业务函数需要的类型
          ↓
调用业务方法并返回字符串结果
          ↓
AgentLoop 将结果交回模型
```

模型只能请求已经注册的工具。实际执行、权限控制、参数校验、超时和错误处理都由 Go 程序负责。

## 2. 最简方式：直接注册普通 Go 函数

参数简单时，可以直接注册普通函数，不需要定义参数结构体，也不需要手写 `map[string]any`：

```go
func getBirthday(name string) string {
    return name + "的生日是 2003-08-10"
}

err := registry.RegisterReflectFunction(
    "get_birthday",
    "根据姓名获取用户生日。",
    getBirthday,
    config.ToolParameter{
        Name:        "name",
        Description: "用户姓名。",
        Required:    true,
    },
)
```

支持的函数形式包括：

```go
func(name string) string
func(name string) (string, error)
func(context.Context, name string) (string, error)
func() string
```

如果函数有多个参数，就按函数参数顺序传入多个 `ToolParameter`。Go 反射可以读取参数类型，但无法可靠读取源码中的参数名和注释，因此参数的模型名称、说明和必填属性仍需显式提供。

## 3. 复杂参数：参数结构体 + 类型安全 handler

先定义工具参数结构体。字段必须带有与 JSON Schema 对应的 `json` 标签：

```go
type BirthdayArgs struct {
    Name string `json:"name"`
}
```

再定义业务方法：

```go
func getBirthday(ctx context.Context, args BirthdayArgs) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    if strings.TrimSpace(args.Name) == "" {
        return "", fmt.Errorf("参数 name 必须是非空字符串")
    }
    return getSheng(args.Name), nil
}
```

使用 `RegisterTypedFunction` 注册时，框架会自动完成以下转换：

```text
map[string]any
    → json.Marshal
    → json.Unmarshal
    → BirthdayArgs
    → getBirthday(ctx, args)
```

注册代码如下：

```go
registry := config.NewToolRegistry()
err := config.RegisterTypedFunction(
    registry,
    "get_birthday",
    "传递名称，获取用户生日。",
    birthdayParameters,
    getBirthday,
)
if err != nil {
    return err
}
```

不需要上下文时，使用 `RegisterTypedFunctionWithoutContext`：

```go
func greet(args GreetArgs) (string, error) {
    return "你好，" + args.Name, nil
}

err := config.RegisterTypedFunctionWithoutContext(
    registry,
    "greet",
    "向用户发送问候。",
    greetParameters,
    greet,
)
```

## 4. 参数 JSON Schema

工具声明中的 Schema 用于告诉模型参数格式，不能替代业务方法中的服务端校验。

```go
birthdayParameters := map[string]any{
    "type": "object",
    "properties": map[string]any{
        "name": map[string]any{
            "type":        "string",
            "description": "用户姓名。",
        },
    },
    "required":             []any{"name"},
    "additionalProperties": false,
}
```

Schema 与结构体应保持一致：

- Schema 中的 `name` 对应 `BirthdayArgs.Name` 和 ``json:"name"``；
- `required` 描述模型必须提供的字段；
- `enum`、`minimum`、`maxLength` 等约束应在 handler 中再次检查；
- 无参数工具使用 `config.EmptyObjectSchema()`。

例如：

```go
type WeatherArgs struct {
    City string `json:"city"`
    Unit string `json:"unit"`
}

var weatherParameters = map[string]any{
    "type": "object",
    "properties": map[string]any{
        "city": map[string]any{"type": "string"},
        "unit": map[string]any{
            "type": "string",
            "enum": []any{"celsius", "fahrenheit"},
        },
    },
    "required": []any{"city"},
}
```

## 5. 需要手动处理 map 时

`RegisteredToolHandler` 是底层统一接口：

```go
type RegisteredToolHandler func(
    context.Context,
    map[string]any,
) (string, error)
```

参数结构动态、需要兼容多个版本，或者工具本身就是通用 JSON 处理器时，可以直接使用 `RegisterFunction`：

```go
func echo(ctx context.Context, args map[string]any) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    text, ok := args["text"].(string)
    if !ok || text == "" {
        return "", fmt.Errorf("参数 text 必须是非空字符串")
    }
    return text, nil
}

err := registry.RegisterFunction(
    "echo",
    "回显文本。",
    echoParameters,
    echo,
)
```

不要直接对未校验的值使用 `args["text"].(string)`；模型参数异常时应返回错误，而不是触发 panic。

已有类型安全方法时，也可以单独使用适配器：

```go
registry.RegisterFunction(
    "get_birthday",
    "传递名称，获取用户生日。",
    birthdayParameters,
    config.AdaptTypedHandler(getBirthday),
)
```

## 6. 注入 AgentLoop

同一个 registry 同时提供工具声明和执行器：

```go
loop := config.AgentLoop{
    Model:           modelAdapter,
    Tools:           registry.Tools(),
    Executor:        registry,
    InitialMessages: previousMessages,
    MaxSteps:        8,
}

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := loop.Run(ctx, userPrompt)
if err != nil {
    return err
}
fmt.Println(result.Answer)
```

`Tools()` 返回按注册顺序排列的声明副本；外部修改返回值不会改变注册表。`Execute` 根据模型返回的工具名查找 handler，并向 handler 传递参数副本。不要分别维护工具声明列表和 handler map。

## 7. context、错误与副作用

带 context 的业务方法应把它传递给 HTTP、数据库或文件操作：

```go
func queryWeather(ctx context.Context, args WeatherArgs) (string, error) {
    request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
    if err != nil {
        return "", err
    }
    response, err := http.DefaultClient.Do(request)
    if err != nil {
        return "", err
    }
    defer response.Body.Close()
    return readWeather(response.Body)
}
```

工具失败必须返回 `error`。`AgentLoop.Run` 会把错误包装为包含工具名和 step 的错误；是否重试由程序策略决定，不要让模型通过自然语言决定重试次数。

对有副作用的工具，应额外考虑权限、审批、幂等键和重复调用。`MaxSteps` 应按业务设置合理上限，避免异常循环消耗资源。

## 8. 返回结果

工具接口返回字符串。对象或数组应编码为 JSON：

```go
func listTasks(_ context.Context, _ ListTasksArgs) (string, error) {
    tasks := []map[string]any{
        {"id": "task-1", "title": "整理文档", "done": false},
    }
    result, err := json.Marshal(tasks)
    if err != nil {
        return "", fmt.Errorf("编码任务结果: %w", err)
    }
    return string(result), nil
}
```

不要返回 API Key、访问令牌、完整数据库记录或其他不必要的敏感数据。写入日志前也要脱敏。

## 9. 注册约束与常见错误

注册表会拒绝：

- 空工具名：`ErrToolNameEmpty`；
- `nil` handler：`ErrToolHandlerNil`；
- 重复工具名：`ErrToolDuplicate`；
- 未知工具执行：返回 `unknown tool` 错误。

初始化函数必须先检查错误，再继续使用注册表：

```go
registry, err := buildToolRegistry()
if err != nil {
    return err
}
```

不要覆盖初始化错误后继续对可能为 `nil` 的 registry 调用注册方法。

## 10. 测试工具

测试放在 `test/`，不调用真实 API。重点覆盖声明、类型转换、handler 结果、未知工具、重复注册、参数错误和 context 取消：

```go
type EchoArgs struct {
    Text string `json:"text"`
}

func TestRegisterTypedFunction(t *testing.T) {
    registry := config.NewToolRegistry()
    err := config.RegisterTypedFunction(
        registry,
        "echo",
        "回显文本。",
        map[string]any{
            "type": "object",
            "properties": map[string]any{
                "text": map[string]any{"type": "string"},
            },
            "required": []any{"text"},
        },
        func(_ context.Context, args EchoArgs) (string, error) {
            return args.Text, nil
        },
    )
    if err != nil {
        t.Fatal(err)
    }

    result, err := registry.Execute(context.Background(), config.AgentToolCall{
        Name:      "echo",
        Arguments: map[string]any{"text": "hello"},
    })
    if err != nil || result != "hello" {
        t.Fatalf("result=%q err=%v", result, err)
    }
}
```

验证命令：

```bash
gofmt -w .
go test ./...
go vet ./...
```

## 11. 完整样例：注册生日查询工具

下面的样例可以直接放入 `main` 包。它演示了如何保留一个普通业务方法，并用类型安全的 handler 将它注册给 Agent。

```go
package main

import (
    "ai-test/config"
    "context"
    "fmt"
    "strings"
)

// BirthdayArgs 是模型调用 get_birthday 时需要提供的参数。
type BirthdayArgs struct {
    Name string `json:"name"`
}

// getSheng 是已有的普通业务方法，不依赖 Agent 框架。
func getSheng(name string) string {
    return name + "的生日是 2003-08-10"
}

// getBirthday 是类型安全的 Agent 工具方法。
func getBirthday(ctx context.Context, args BirthdayArgs) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }

    args.Name = strings.TrimSpace(args.Name)
    if args.Name == "" {
        return "", fmt.Errorf("参数 name 必须是非空字符串")
    }
    return getSheng(args.Name), nil
}

func registerBirthdayTool(registry *config.ToolRegistry) error {
    parameters := map[string]any{
        "type": "object",
        "properties": map[string]any{
            "name": map[string]any{
                "type":        "string",
                "description": "要查询生日的用户姓名。",
            },
        },
        "required":             []any{"name"},
        "additionalProperties": false,
    }

    return config.RegisterTypedFunction(
        registry,
        "get_birthday",
        "根据姓名查询用户生日。",
        parameters,
        getBirthday,
    )
}

func buildToolRegistry() (*config.ToolRegistry, error) {
    registry := config.NewToolRegistry()
    if err := registerBirthdayTool(registry); err != nil {
        return nil, fmt.Errorf("注册生日工具: %w", err)
    }
    return registry, nil
}
```

在启动 Agent 时注入注册表：

```go
registry, err := buildToolRegistry()
if err != nil {
    return err
}

loop := config.AgentLoop{
    Model:    modelAdapter,
    Tools:    registry.Tools(),
    Executor: registry,
    MaxSteps: 8,
}

result, err := loop.Run(context.Background(), "小明的生日是什么？")
if err != nil {
    return err
}
fmt.Println(result.Answer)
```

模型会看到名为 `get_birthday` 的工具，并生成类似参数：

```json
{
  "name": "小明"
}
```

注册表将参数转换成 `BirthdayArgs`，调用 `getBirthday`，再把 `getSheng` 返回的结果交给模型生成最终回答。

## 12. 安全清单

- 不在工具描述、参数默认值、日志或返回结果中写入 API Key 和其他秘密；
- 对字符串长度、数值范围、枚举值和资源 ID 做业务层校验；
- 对有副作用的工具增加权限检查、审批或幂等键；
- 为网络和数据库工具设置超时，并使用支持取消的 API；
- 日志记录工具名、调用 ID、耗时和错误即可，参数和结果按敏感级别脱敏；
- 思考内容可能包含敏感信息，生产环境谨慎开启终端展示。
