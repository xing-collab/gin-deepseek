# 流式调用三种范式学习笔记

> 项目：DeepSeek 大模型客户端（Go 1.25）
> 代码位置：`config/api.go` → `Stream` / `StreamChan` / `StreamIter`
> 阅读前提：Java 基础，正在学 Go

## SSE 协议回顾

流式调用的 HTTP 响应不是一整块 JSON，而是 **Server-Sent Events（SSE）** 格式——每行一个 `data: <JSON>`，流结束标志是 `data: [DONE]`：

```
data: {"choices":[{"delta":{"content":null,"reasoning_content":"嗯"},...}]}
data: {"choices":[{"delta":{"content":"你好呀！",...}]}
data: [DONE]
```

注意：流式 chunk 用 `delta` 字段（而非流式的 `message`），`content`/`reasoning_content` 可能为 `null`。

## 三范式对比总表

三种方式共用 `scanSSE()`（逐行解析）和 `doStreamReq()`（发送请求），只换消费范式：

| | 回调式 `Stream` | 通道式 `StreamChan` | 迭代器式 `StreamIter` |
|---|---|---|---|
| Go 版本 | 任意 | 任意 | **1.23+** |
| 消费写法 | 传一个函数进去 | `for d := range ch` | `for d, err := range` |
| 中途停止 | 需手动标志 | `break` 即停 | `break` 即停 |
| 并发消费 | 麻烦 | 容易（再开 goroutine） | 容易 |
| 心智模型 | **推**（push）：回调是被动的 | **拉**（pull）：循环是主动的 | **拉** + 语法糖 |
| Java 对应 | `Consumer<T>` 回调 | `BlockingQueue<T>` | `Stream<T>` / `Flux<T>` |

## 一、回调式 `Stream`

**签名**：
```go
func (llm *LLM) Stream(prompt, content string, onDelta func(StreamDelta)) error
```

**调用**：
```go
c.Stream(prompt, "你好", func(d config.StreamDelta) {
    if d.Content != "" {
        fmt.Print(d.Content)
    }
})
```

**Java 对应**：
```java
client.stream(prompt, "你好", (StreamDelta d) -> {
    if (!d.getContent().isEmpty()) System.out.print(d.getContent());
});
```

- 优点：简单直接，Go 的闭包天然支持，不需要额外类型
- 缺点：不想处理时只能改传 `nil` 或一个空函数；多消费者不方便

## 二、通道式 `StreamChan`

**签名**：
```go
func (llm *LLM) StreamChan(prompt, content string) (<-chan StreamDelta, <-chan error)
```

**调用**：
```go
ch, errCh := c.StreamChan(prompt, "你好")
for d := range ch {
    fmt.Print(d.Content)
}
if err := <-errCh; err != nil {
    // 处理错误
}
```

**Java 对应**：
```java
BlockingQueue<StreamDelta> queue = client.streamChan(prompt, "你好");
while (true) {
    StreamDelta d = queue.take();          // 阻塞等待下一个增量
    if (d == POISON) break;               // 结束哨兵
    System.out.print(d.getContent());
}
```

底层 goroutine 是生产者，`ch` 是消费队列，`errCh` 带缓冲 1 防止 goroutine 泄漏。

- 优点：生产消费解耦，消费端随时 `break`，可多 goroutine 并行处理
- 缺点：涉及 goroutine/chan 生命周期概念，对初学并发有一定门槛

## 三、迭代器式 `StreamIter`

**签名**（Go 1.23+）：
```go
func (llm *LLM) StreamIter(prompt, content string) iter.Seq2[StreamDelta, error]
```

**调用**：
```go
for d, err := range c.StreamIter(prompt, "你好") {
    if err != nil {
        fmt.Println("请求失败:", err)
        return
    }
    fmt.Print(d.Content)
}
```

**Java 对应**（Java 9+ `Flow` / Reactor `Flux`）：
```java
Flux<StreamDelta> flux = client.streamIter(prompt, "你好");
flux.subscribe(d -> System.out.print(d.getContent()));
```

`iter.Seq2` 是 Go 1.23 引入的"推式迭代器"——把数据流包装成 `for-range` 可遍历的标准语法。背后还是回调（`yield`），但对调用方来说跟遍历普通 slice 一样自然。

- 优点：语法最简洁，`for-range` 是最熟悉的 Go 控制流
- 缺点：Go 1.23+ 才支持，旧项目用不了

## 底层共享：`scanSSE` 为什么回调返回 `bool`

```go
func scanSSE(body io.Reader, onDelta func(StreamDelta) bool) error
```

回调返回 `bool` 是让**三种范式共用同一套解析**的关键：

```
你的 Stream 调用
  │  onDelta(delta)   ← 你的打印逻辑
  │  return true      ← 永远继续
  ▼
scanSSE(body, func(delta StreamDelta) bool {
    onDelta(delta)     // 调你的回调
    return true        // true=继续读下一行，false=提前停止（迭代器 break 时会走到这里）
})
```

- 回调式和通道式 → 永远返回 `true`（不主动停）
- 迭代器式 → 返回 `yield(d, nil)`，用户 `break` 时 `yield` 自动返回 `false` → `scanSSE` 立刻停止扫描 → `defer response.Body.Close()` 自动关连接

## Go 专属概念（Java 视角）

### 多返回值 + `err` 模式

```go
response, err := http.DefaultClient.Do(req)  // Java 的 try { Response r = ... }
if err != nil {                                // } catch (Exception e) {
    return err                                 //     throw e;
}                                              // }
```

Go 没有异常，失败通过返回 `(value, error)` 显式传递。`if err != nil` 就是 Java 的 `catch` 块，但它是显式的、不可跳过的。

### 函数是一等公民

```go
// Go：函数类型就是一个普通类型，直接当参数传
func stream(onDelta func(StreamDelta)) { ... }
```
```java
// Java：需要用函数式接口"装"lambda
void stream(Consumer<StreamDelta> onDelta) { ... }
```

Go 不需要 `Consumer`、`Function`、`Supplier` 这类接口——`func(T) R` 本身就是一个合法的类型。

### `defer` = `try-finally`

```go
defer response.Body.Close()  // 注册：函数 return 时执行
```
```java
try { ... } finally { response.getBody().close(); }
```

`defer` 的好处：资源申请和释放写在**相邻两行**，不会因为中间代码多了几十行而忘了关。可以堆叠多个 `defer`，按 LIFO 顺序执行。

## 附录：main.go 三连调用示例

```go
func main() {
    c := config.NewClient()
    prompt := "你是《崩坏：星穹铁道》的人物：雪衣，你需要依照你的角色设定回答。"

    // 方式一：回调式
    fmt.Println("=== 回调式 Stream ===")
    c.Stream(prompt, "你好", printDelta)

    // 方式二：通道式
    fmt.Println("=== 通道式 StreamChan ===")
    ch, errCh := c.StreamChan(prompt, "你好")
    for d := range ch { printDelta(d) }
    if err := <-errCh; err != nil { /* ... */ }

    // 方式三：迭代器式
    fmt.Println("=== 迭代器式 StreamIter ===")
    for d, err := range c.StreamIter(prompt, "你好") {
        if err != nil { /* ... */ }
        printDelta(d)
    }
}

func printDelta(d config.StreamDelta) {
    if d.Content != ""      { fmt.Print(d.Content) }
    if d.ReasoningContent != "" { fmt.Print(d.ReasoningContent) }
}
```
