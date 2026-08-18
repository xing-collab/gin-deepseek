package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ---- 配置 ----

// BaseConfig 基础配置
type BaseConfig struct {
	baseUrl   string
	apiKey    string
	modelName string
}

// ---- 请求 ----

// ApiRequest API 请求参数
type ApiRequest struct {
	Model           string            `json:"model"`
	Messages        []map[string]any  `json:"messages"`
	Thinking        map[string]string `json:"thinking"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Stream          bool              `json:"stream"`
}

// Tool 对应 function calling 请求里的 tools 数组元素
type Tool struct {
	Type     string   `json:"type"` // 固定 "function"
	Function Function `json:"function"`
}

// Function 描述一个可调用函数及其参数 JSON Schema
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Arguments   string         `json:"arguments,omitempty"` // 仅响应里出现：模型给的参数 JSON 字符串
}

// ToolCall 对应响应里的 tool_calls 元素
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// ---- 非流式响应 ----

// Message 消息内容
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// Choice 候选项
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Logprobs     any     `json:"logprobs"`
	FinishReason string  `json:"finish_reason"`
}

// ApiResponse 非流式 API 返回
type ApiResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// ---- 流式响应 ----

// StreamDelta 流式响应的增量片段（回调入参）
type StreamDelta struct {
	Content          string
	ReasoningContent string
}

// streamChunk 流式响应单个 chunk 的 wire 格式（内部用）
// Content / ReasoningContent 用 *string 而非 string：
// JSON 的 null 反序列化为 nil，和空字符串 "" 可以区分
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// streamToolCall 流式过程中按 index 拼接好的工具调用（内部用）
type streamToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// ---- 回话历史 -----

// ---- 客户端 ----

// LLM 大模型客户端
type LLM struct {
	HTTPClient   *http.Client
	config       *BaseConfig
	systemPrompt string           // 当前 system prompt，每轮可更新（角色状态切换后即时生效）
	history      []map[string]any // 仅 user/assistant 对话（不含 system）
}

// Option 客户端配置项，用于 NewClient 的函数式选项模式
type Option func(*BaseConfig)

// WithBaseURL 覆盖默认的 baseUrl
func WithBaseURL(url string) Option {
	return func(c *BaseConfig) { c.baseUrl = url }
}

// WithAPIKey 覆盖默认的 apiKey（默认从环境变量 OPENAI_API_KEY 读取）
func WithAPIKey(key string) Option {
	return func(c *BaseConfig) { c.apiKey = key }
}

// WithModel 覆盖默认的 modelName
func WithModel(name string) Option {
	return func(c *BaseConfig) { c.modelName = name }
}

// NewClient 创建客户端（类似 Java 构造器）。
// 无参调用使用默认配置；也可传入 WithBaseURL / WithAPIKey / WithModel 覆盖单个字段。
//
//	c := config.NewClient()
//	c := config.NewClient(config.WithModel("gpt-4o"), config.WithAPIKey("sk-..."))
func NewClient(opts ...Option) *LLM {
	cfg := &BaseConfig{
		baseUrl:   "https://api.deepseek.com/chat/completions",
		apiKey:    os.Getenv("OPENAI_API_KEY"),
		modelName: "deepseek-v4-flash",
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &LLM{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		config:     cfg,
		history:    []map[string]any{},
	}
}

// ---- 公开方法 ----

// ApiTest 便捷测试方法（硬编码 prompt）
func (llm *LLM) ApiTest(content string) (*ApiResponse, error) {
	return llm.Invoke("你是一个基于中文语句回答的学生", content)
}

// Invoke 非流式调用
func (llm *LLM) Invoke(prompt string, content string) (*ApiResponse, error) {
	return llm.doRequest(prompt, content)
}

// Stream 流式调用，每收到一个增量片段即回调 onDelta（回调式）
//
// SSE 协议格式：每行一个 "data: <JSON>"，流结束标志是 "data: [DONE]"
//
//	data: {"choices":[{"delta":{"content":null,"reasoning_content":"嗯"},...}]}
//	data: {"choices":[{"delta":{"content":"你好",...}]}
//	data: [DONE]
func (llm *LLM) Stream(prompt string, content string, onDelta func(StreamDelta)) error {
	response, err := llm.doStreamReq(prompt, content)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	var answer strings.Builder
	if err := scanSSE(response.Body, func(delta StreamDelta) bool {
		if delta.Content != "" {
			answer.WriteString(delta.Content)
		}
		onDelta(delta)
		return true
	}); err != nil {
		return err
	}
	if answer.Len() > 0 {
		llm.AddHistory(map[string]any{"role": "assistant", "content": answer.String()})
	}
	return nil
}

// StreamChan 流式调用，返回只读 channel 逐条消费增量（通道式，Go 惯用并发）
//
//	ch, errCh := c.StreamChan(prompt, "你好")
//	for d := range ch { ... }
//	if err := <-errCh; err != nil { ... }   // 正常结束返回 nil
func (llm *LLM) StreamChan(prompt string, content string) (<-chan StreamDelta, <-chan error) {
	ch := make(chan StreamDelta)
	errCh := make(chan error, 1)

	go func() {
		defer close(ch)
		defer close(errCh)

		response, err := llm.doStreamReq(prompt, content)
		if err != nil {
			errCh <- err
			return
		}
		defer response.Body.Close()

		var answer strings.Builder
		if err := scanSSE(response.Body, func(delta StreamDelta) bool {
			if delta.Content != "" {
				answer.WriteString(delta.Content)
			}
			ch <- delta
			return true
		}); err != nil {
			errCh <- err
			return
		}
		if answer.Len() > 0 {
			llm.AddHistory(map[string]any{"role": "assistant", "content": answer.String()})
		}
	}()

	return ch, errCh
}

// StreamIter 流式调用，返回可 for-range 的迭代器（迭代器式，Go 1.23+ range-over-func）
//
//	for d, err := range c.StreamIter(prompt, "你好") {
//	    if err != nil { ... }
//	}
func (llm *LLM) StreamIter(prompt string, content string) iter.Seq2[StreamDelta, error] {
	return func(yield func(StreamDelta, error) bool) {
		response, err := llm.doStreamReq(prompt, content)
		if err != nil {
			yield(StreamDelta{}, err)
			return
		}
		defer response.Body.Close()

		var answer strings.Builder
		if err := scanSSE(response.Body, func(delta StreamDelta) bool {
			if delta.Content != "" {
				answer.WriteString(delta.Content)
			}
			return yield(delta, nil)
		}); err != nil {
			yield(StreamDelta{}, err)
			return
		}
		if answer.Len() > 0 {
			llm.AddHistory(map[string]any{"role": "assistant", "content": answer.String()})
		}
	}
}

// ---- 内部方法 ----

// doRequest 非流式请求：构建 → 发送 → 解析 → 返回
func (llm *LLM) doRequest(prompt string, content string) (*ApiResponse, error) {
	httpReq, err := send(llm, prompt, content, false)
	if err != nil {
		return nil, err
	}

	response, err := llm.do(httpReq)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var apiResp ApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	return &apiResp, nil
}

// doStreamReq 发送流式请求，返回可读的响应体
func (llm *LLM) doStreamReq(prompt string, content string) (*http.Response, error) {
	httpReq, err := send(llm, prompt, content, true)
	if err != nil {
		return nil, err
	}
	return llm.do(httpReq)
}

// do 发送请求并检查 HTTP 状态码，非 2xx 时返回带响应体的错误
func (llm *LLM) do(req *http.Request) (*http.Response, error) {
	resp, err := llm.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// scanSSE 逐行解析 SSE 响应体，onDelta 返回 false 时提前停止（正常结束返回 nil）
func scanSSE(body io.Reader, onDelta func(StreamDelta) bool) error {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return nil
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return err
		}

		var delta StreamDelta
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Content != nil {
				delta.Content = *chunk.Choices[0].Delta.Content
			}
			if chunk.Choices[0].Delta.ReasoningContent != nil {
				delta.ReasoningContent = *chunk.Choices[0].Delta.ReasoningContent
			}
		}
		if !onDelta(delta) {
			return nil
		}
	}
	return scanner.Err()
}

// send 构造 JSON body 并创建 HTTP 请求，设置 Content-Type 和 Authorization
func send(llm *LLM, prompt string, content string, stream bool) (*http.Request, error) {
	messages := llm.snapshot(prompt, content)
	apiReq := ApiRequest{
		Model:           llm.config.modelName,
		Messages:        messages,
		Thinking:        map[string]string{"type": "enabled"},
		ReasoningEffort: "medium",
		Stream:          stream,
	}

	jsonBody, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", llm.config.baseUrl, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+llm.config.apiKey)
	return httpReq, err
}

// AddHistory 追加一条对话消息。历史仅存 user/assistant 对话（system 独立于 history），
// 超过 maxHistoryMessages 条时丢弃最旧的两条（一轮 user+assistant）。
func (llm *LLM) AddHistory(m map[string]any) []map[string]any {
	llm.history = append(llm.history, m)
	if len(llm.history) > maxHistoryMessages {
		llm.history = append([]map[string]any(nil), llm.history[2:]...)
	}
	return llm.history
}

// SetSystemPrompt 更新 system prompt（角色状态变化后调用，下轮请求即时生效）。
func (llm *LLM) SetSystemPrompt(prompt string) {
	llm.systemPrompt = prompt
}

// snapshot 登记本轮 user 消息并返回本次请求的完整 messages：
// [system(当前 prompt)] + 历史对话 + 本轮 user。
// 每次调用都会用传入的 prompt 更新 system，保证状态切换后立即生效。
func (llm *LLM) snapshot(prompt string, content string) []map[string]any {
	if prompt != "" {
		llm.systemPrompt = prompt
	}
	llm.AddHistory(map[string]any{"role": "user", "content": content})
	messages := make([]map[string]any, 0, len(llm.history)+1)
	messages = append(messages, map[string]any{"role": "system", "content": llm.systemPrompt})
	messages = append(messages, llm.history...)
	return messages
}

// GetCurrentTime 返回当前时间（时:分:秒）。
// 注意 Go 的格式化模板不是 Java 的 HH:mm:ss，而是固定参考时间 15:04:05。
func GetCurrentTime() string {
	return time.Now().Format("15:04:05")
}

// GetCurrentDate 返回当前日期（年-月-日）。
// 注意 Go 的格式化模板不是 Java 的 yyyy-MM-dd，而是固定参考时间 2006-01-02。
func GetCurrentDate() string {
	return time.Now().Format("2006-01-02")
}

// buildToolRequest 构建带 tool 的 HTTP 请求，stream 控制是否流式。
// messages 用 []map[string]any（而非 []map[string]string），因为 assistant 的 tool_calls 是嵌套结构。
func (llm *LLM) buildToolRequest(messages []map[string]any, tools []Tool, stream bool) (*http.Request, error) {
	reqBody := map[string]any{
		"model":            llm.config.modelName,
		"messages":         messages,
		"thinking":         map[string]string{"type": "enabled"},
		"reasoning_effort": "medium",
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	if stream {
		reqBody["stream"] = true
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", llm.config.baseUrl, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+llm.config.apiKey)
	return httpReq, nil
}

// doToolRequest 发送带 tool 的非流式请求并解析响应。
func (llm *LLM) doToolRequest(messages []map[string]any, tools []Tool) (*ApiResponse, error) {
	httpReq, err := llm.buildToolRequest(messages, tools, false)
	if err != nil {
		return nil, err
	}

	response, err := llm.do(httpReq)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var apiResp ApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	return &apiResp, nil
}

// InvokeWithTools 非流式调用并自动处理工具调用循环。
// 声明 tools 后，模型返回 tool_calls 时用 handler 执行对应函数，把结果回传，直到模型给出最终回答。
// handler 入参为工具名和解析后的参数 JSON 对象。
func (llm *LLM) InvokeWithTools(prompt string, content string, tools []Tool, handler func(name string, args map[string]any) (string, error)) (*ApiResponse, error) {
	messages := llm.snapshot(prompt, content)

	for {
		resp, err := llm.doToolRequest(messages, tools)
		if err != nil {
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return resp, nil
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			llm.AddHistory(map[string]any{"role": "assistant", "content": msg.Content})
			return resp, nil
		}

		// 追加 assistant 的 tool_calls 消息
		calls := make([]map[string]any, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   tc.ID,
				"type": tc.Type,
				"function": map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				},
			})
		}
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    nil,
			"tool_calls": calls,
		})

		// 逐个执行工具并回传结果
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("解析工具参数: %w", err)
			}
			result, err := handler(tc.Function.Name, args)
			if err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      result,
			})
		}
	}
}

// TimeDateTools 返回「获取当前时间/日期」的两个工具声明，供调用方（含 main）复用。
func TimeDateTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_time",
				Description: "获取当前时间，返回格式为 时:分:秒（如 15:04:05）的字符串",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_date",
				Description: "获取当前日期，返回格式为 年-月-日（如 2006-01-02）的字符串",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

// TimeDateHandler 根据工具名分发：get_current_time 返回时间，get_current_date 返回日期。
func TimeDateHandler(name string, _ map[string]any) (string, error) {
	switch name {
	case "get_current_time":
		return GetCurrentTime(), nil
	case "get_current_date":
		return GetCurrentDate(), nil
	}
	return "", fmt.Errorf("未知工具: %s", name)
}

// AskCurrentTime 询问模型，模型需要时间/日期时会自动调用对应工具（非流式）。
// 这是把 GetCurrentTime / GetCurrentDate 作为 function calling 工具的完整示例，调用方只需传入问题。
func (llm *LLM) AskCurrentTime(content string) (*ApiResponse, error) {
	return llm.InvokeWithTools(
		"你是一个助手。当用户询问当前时间时调用 get_current_time，询问日期时调用 get_current_date，拿到结果后回答。",
		content,
		TimeDateTools(),
		TimeDateHandler,
	)
}

// doStreamToolRound 执行一次流式 tool 请求：边把正文增量交给 onDelta，边按 index 拼接 tool_calls。
// 返回按 index 升序排列的工具调用列表。
func (llm *LLM) doStreamToolRound(messages []map[string]any, tools []Tool, onDelta func(StreamDelta)) ([]streamToolCall, error) {
	httpReq, err := llm.buildToolRequest(messages, tools, true)
	if err != nil {
		return nil, err
	}
	response, err := llm.do(httpReq)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	byIndex := map[int]*streamToolCall{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return nil, err
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		var d StreamDelta
		if delta.Content != nil {
			d.Content = *delta.Content
		}
		if delta.ReasoningContent != nil {
			d.ReasoningContent = *delta.ReasoningContent
		}
		if d.Content != "" || d.ReasoningContent != "" {
			onDelta(d)
		}

		for _, tc := range delta.ToolCalls {
			call := byIndex[tc.Index]
			if call == nil {
				call = &streamToolCall{}
				byIndex[tc.Index] = call
			}
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Type != "" {
				call.Type = tc.Type
			}
			if tc.Function.Name != "" {
				call.Name = tc.Function.Name
			}
			call.Arguments += tc.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(byIndex))
	for i := range byIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	calls := make([]streamToolCall, 0, len(indices))
	for _, i := range indices {
		calls = append(calls, *byIndex[i])
	}
	return calls, nil
}

// StreamChanWithTools 流式调用并自动处理工具调用循环（通道式）。
// 最终回答的正文增量会逐条发送到返回的 channel；中间的 tool 调用轮次对调用方透明。
func (llm *LLM) StreamChanWithTools(prompt string, content string, tools []Tool, handler func(name string, args map[string]any) (string, error)) (<-chan StreamDelta, <-chan error) {
	deltas := make(chan StreamDelta)
	errs := make(chan error, 1)

	go func() {
		defer close(deltas)
		defer close(errs)

		messages := llm.snapshot(prompt, content)

		var answer string
		for {
			calls, err := llm.doStreamToolRound(messages, tools, func(d StreamDelta) {
				if d.Content != "" {
					answer += d.Content
				}
				deltas <- d
			})
			if err != nil {
				errs <- err
				return
			}
			if len(calls) == 0 {
				if answer != "" {
					llm.AddHistory(map[string]any{"role": "assistant", "content": answer})
				}
				return
			}

			// 追加 assistant 的 tool_calls 消息
			tcMsgs := make([]map[string]any, 0, len(calls))
			for _, c := range calls {
				tcMsgs = append(tcMsgs, map[string]any{
					"id":   c.ID,
					"type": c.Type,
					"function": map[string]any{
						"name":      c.Name,
						"arguments": c.Arguments,
					},
				})
			}
			messages = append(messages, map[string]any{
				"role":       "assistant",
				"content":    nil,
				"tool_calls": tcMsgs,
			})

			// 逐个执行工具并回传结果
			for _, c := range calls {
				var args map[string]any
				if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
					errs <- fmt.Errorf("解析工具参数: %w", err)
					return
				}
				result, err := handler(c.Name, args)
				if err != nil {
					errs <- err
					return
				}
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": c.ID,
					"content":      result,
				})
			}
		}
	}()

	return deltas, errs
}

// AskCurrentTimeStream 询问模型，模型需要时间/日期时会自动调用对应工具（流式）。
// 用法同 StreamChan：for d := range ch { ... }，结束后读 errCh。
func (llm *LLM) AskCurrentTimeStream(content string) (<-chan StreamDelta, <-chan error) {
	return llm.StreamChanWithTools(
		"你是一个助手。当用户询问当前时间时调用 get_current_time，询问日期时调用 get_current_date，拿到结果后回答。",
		content,
		TimeDateTools(),
		TimeDateHandler,
	)
}
