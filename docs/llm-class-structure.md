# Go struct 与 Java 类对照：LLM 客户端为例

> 代码位置：`config/api.go` → `type LLM struct` / `NewClient` / `func (llm *LLM)`
> 阅读前提：Java 基础，正在学 Go

## 一句话总结

Go **没有 `class` 关键字**。"类"由两部分拼成：**struct 存字段** + **外部函数通过接收者绑定方法**。和 Java 不同，方法不写在 struct 定义里面，而是独立声明，用 `func (llm *LLM) 方法名()` 关联到类型。

## 一、LLM 结构拆解

```go
// config/api.go:82-86
type LLM struct {
	HTTPClient *http.Client       // 公开字段（大写开头）
	config     *BaseConfig        // 私有字段（小写开头，包内可见）
	history    []map[string]string
}
```

### 字段对照表

| Go 字段 | Go 类型 | Java 对应 | 可见性 | 说明 |
|---|---|---|---|---|
| `HTTPClient` | `*http.Client` | `HttpClient` | **public**（大写） | HTTP 客户端实例 |
| `config` | `*BaseConfig` | `BaseConfig` | **private**（小写） | API 地址/密钥/模型名 |
| `history` | `[]map[string]string` | `List<Map<String,String>>` | **private**（小写） | 对话历史，`[]map[string]string` 中每个 map 一条消息 |

### Go 可见性规则

Go 没有 `public`/`private`/`protected` 修饰符，**靠首字母大小写决定**：

| 首字母 | 可见范围 | Java 对照 |
|---|---|---|
| 大写（如 `HTTPClient`） | **跨包可见** | `public` |
| 小写（如 `config`） | **仅当前包** | `private`（包级私有） |

## 二、NewClient 构造器

Go 没有 `new` 运算符的构造函数概念，习惯用**工厂函数**返回指针：

```go
// config/api.go:89-99
func NewClient() *LLM {
	return &LLM{
		HTTPClient: &http.Client{},
		config: &BaseConfig{
			baseUrl:   "https://api.deepseek.com/chat/completions",
			apiKey:    "sk-xxx",
			modelName: "deepseek-v4-pro",
		},
		history: []map[string]string{},
	}
}
```

调用方：

```go
c := config.NewClient()  // c 是 *LLM（指针）
```

**Java 对照**：相当于静态工厂方法：

```java
public class LLM {
    public static LLM newClient() {
        LLM llm = new LLM();
        llm.httpClient = HttpClient.newHttpClient();
        llm.config = new BaseConfig("https://api.deepseek.com/...", "sk-xxx", "deepseek-v4-pro");
        llm.history = new ArrayList<>();
        return llm;
    }
}
```

区别：Go 返回 `*LLM`（指针），Java 返回的其实就是引用（指针语义），但 Java 不显式写 `*`。

## 三、方法接收者 `(llm *LLM)` = Java 的 `this`

Go 把方法"挂"到类型上的语法：

```go
// func (接收者变量 类型) 方法名(参数) 返回值 {
func (llm *LLM) Invoke(prompt string, content string) (*ApiResponse, error) {
	// llm 就是 Java 的 this，指向调用它的那个实例
	return llm.doRequest(prompt, content)  // 通过 llm 调另一个方法
}

func (llm *LLM) AddHistory(m map[string]string) []map[string]string {
	if len(llm.history) > 20 {
		llm.history = append(llm.history[:1], llm.history[3:]...)
	}
	llm.history = append(llm.history, m)
	return llm.history
}
```

调用端和 Java 一样：

```go
c := config.NewClient()
c.Invoke("system prompt", "用户输入")       // Go：c.Invoke(...)
c.AddHistory(map[string]string{...})       // Go：c.AddHistory(...)
```

```java
LLM c = LLM.newClient();
c.invoke("system prompt", "用户输入");      // Java：c.invoke(...)
c.addHistory(Map.of(...));                  // Java：c.addHistory(...)
```

关键对比：

| 概念 | Go | Java |
|---|---|---|
| 实例引用 | `llm`（接收者变量名，习惯用类型名小写） | `this`（隐式关键字） |
| 调用 | `c.Invoke(prompt, content)` | `c.invoke(prompt, content)` |
| 方法内调另一个方法 | `llm.doRequest(...)` | `this.doRequest(...)` 或直接 `doRequest(...)` |

Go 的方法接收者变量名字是**自己取的**（习惯用类型名首字母小写如 `llm`），不是 `this` 或 `self` 那样的保留字。

## 四、指针接收者 `*LLM` vs 值接收者 `LLM`

Go 的特殊问题：接收者用 `*LLM` 还是 `LLM`？

```go
// 指针接收者 —— 修改会作用于原对象（推荐）
func (llm *LLM) AddHistory(m map[string]string) []map[string]string {
	llm.history = append(llm.history, m)  // 改了 c 的 history
	return llm.history
}

// 值接收者 —— 修改的是"拷贝副本"（几乎从不用）
func (llm LLM) AddHistory(m map[string]string) []map[string]string {
	llm.history = append(llm.history, m)  // 只改了副本，c 的 history 没变！
	return llm.history
}
```

**Java 类比**：Java 方法默认拿到的 `this` 就是原对象的引用（= 指针接收者语义）。如果 Go 用值接收者，相当于 Java 先 `clone()` 了一份对象再调方法——方法改了 clone 的字段，原对象纹丝不动。

**结论**：只要方法需要修改 struct 的任何字段（`llm.history = ...`），就必须用 `*LLM`。项目中 `LLM` 的六个方法全用指针接收者。

## 五、对象状态独立

每次 `NewClient()` 创建一个**全新独立**的实例：

```go
c1 := config.NewClient()  // c1 有自己的 history、config
c2 := config.NewClient()  // c2 有自己的 history、config

c1.AddHistory(userMsg)    // 只影响 c1
// c2.history 仍然是空的
```

Java 同理——`new` 出来的每个对象字段独立。Go 只是没有 `new` 关键字，用 `NewClient()` 替代。

## 六、完整 Java 对照类

把 `config/api.go` 的核心结构翻译成 Java（简化版）：

```java
public class LLM {
    // 字段：struct 字段 1:1 对应
    public HttpClient httpClient;                          // HTTPClient *http.Client
    private BaseConfig config;                              // config *BaseConfig
    private List<Map<String, String>> history;              // history []map[string]string

    // 构造器/工厂方法：Go 的 NewClient()
    public static LLM newClient() {
        LLM llm = new LLM();
        llm.httpClient = HttpClient.newHttpClient();
        llm.config = new BaseConfig(
            "https://api.deepseek.com/chat/completions",
            "sk-xxx",
            "deepseek-v4-pro"
        );
        llm.history = new ArrayList<>();
        return llm;
    }

    // 实例方法：Go 的 func (llm *LLM) Invoke(...)
    public ApiResponse invoke(String prompt, String content) {
        return doRequest(prompt, content);
    }

    // 实例方法：Go 的 func (llm *LLM) AddHistory(...)
    public List<Map<String, String>> addHistory(Map<String, String> m) {
        if (history.size() > 20) {
            // history.remove(1); history.remove(1); // 删两条最旧对话
            history.subList(1, 3).clear();
        }
        history.add(m);
        return history;
    }
}
```

## 七、main.go 调用链梳理

```go
func main() {
	c := config.NewClient()       // ① 拿到 LLM 实例指针 *LLM
	prompt := "..."                // ② system prompt
	// ...
	for scanner.Scan() {
		input := scanner.Text()   // ③ 用户输入
		ch, errCh := c.StreamChan(prompt, input)  // ④ 调用实例方法
		for d := range ch {
			_, an := printDelta(d)
			answer += an
		}
		c.AddHistory(...)          // ⑤ 追加对话到实例的 history
	}
}
```

对应 Java 的思维模型：

```java
LLM c = LLM.newClient();
String prompt = "...";
while (scanner.hasNextLine()) {
    String input = scanner.nextLine();
    StreamChanResult result = c.streamChan(prompt, input);
    for (StreamDelta d : result.ch()) {
        String an = printDelta(d);
        answer += an;
    }
    c.addHistory(Map.of("role", "assistant", "content", answer));
}
```

## 八、差异小结表

| | Go | Java |
|---|---|---|
| **类定义** | `type LLM struct { 字段 }` + 外部方法 | `class LLM { 字段 + 方法 }` 合在一起 |
| **构造器** | `func NewClient() *LLM`（工厂函数名约定） | `new LLM()` 或构造函数 `LLM(...)` |
| **实例引用** | `llm`（自己命名的接收者变量） | `this`（隐式关键字，自动指向当前实例） |
| **方法声明** | `func (llm *LLM) Invoke(...)` 写在 struct 外 | `ApiResponse invoke(...)` 写在 class 内 |
| **可见性** | 首字母大小写（无修饰符） | `public` / `private` / `protected` |
| **字段修改** | 指针接收者 `*LLM` 才能改原对象 | 默认就是引用，直接改 `this.field` |
| **实例创建** | `c := config.NewClient()` 拿指针 | `LLM c = new LLM()` 或 `LLM.newClient()` |
| **空值** | `nil` | `null` |

核心差异就一点：**Go 把数据（struct）和行为（方法）分开了写**——struct 只管字段，方法用接收者挂上去。但这只是语法不同，运行时效果和 Java 的实例方法完全一致：接收者 `llm` 就是那个对象本身。
