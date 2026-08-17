package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ---- 配置 ----

const (
	defaultResponsesURL   = "https://api.deepseek.com/responses"
	defaultResponsesModel = "deepseek-v4-flash"
	// maxHistoryMessages 是短期记忆窗口：历史消息超过该数量时丢弃最旧的。
	maxHistoryMessages = 20
)

// OpenAPIConfig 保存 Responses API 客户端配置。
type OpenAPIConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// OpenAPIClient 封装 Responses API 的 HTTP 调用。
// history 保存会话内的短期记忆（user/assistant 轮次），由 mu 保护，并发安全。
type OpenAPIClient struct {
	HTTPClient *http.Client
	config     OpenAPIConfig
	mu         sync.Mutex
	history    []map[string]string
}

// ---- 请求 ----

// OpenAPIReasoning 配置模型的推理强度。
type OpenAPIReasoning struct {
	Effort string `json:"effort"`
}

// OpenAPITextFormat 配置文本输出格式和结构化 Schema。
type OpenAPITextFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// OpenAPIText 配置 Responses API 的文本输出选项。
type OpenAPIText struct {
	Format OpenAPITextFormat `json:"format"`
}

// OpenAPIRequest 是 POST /responses 的请求体。
type OpenAPIRequest struct {
	Model           string           `json:"model"`
	Input           any              `json:"input"`
	Instructions    string           `json:"instructions"`
	Reasoning       OpenAPIReasoning `json:"reasoning"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	Stream          bool             `json:"stream"`
	Temperature     float64          `json:"temperature"`
	TopP            float64          `json:"top_p"`
	Text            OpenAPIText      `json:"text"`
	Tools           any              `json:"tools"`
	ToolChoice      any              `json:"tool_choice"`
	TopLogprobs     any              `json:"top_logprobs"`
	User            string           `json:"user"`
}

// ---- 响应 ----

// OpenAPIResponse 表示一次非流式 Responses API 响应。
type OpenAPIResponse struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	CreatedAt         int64               `json:"created_at"`
	Status            string              `json:"status"`
	Error             any                 `json:"error"`
	IncompleteDetails any                 `json:"incomplete_details"`
	Instructions      any                 `json:"instructions"`
	Model             string              `json:"model"`
	Output            []OpenAPIOutputItem `json:"output"`
	Reasoning         OpenAPIReasoning    `json:"reasoning"`
	Usage             OpenAPIUsage        `json:"usage"`
}

// OpenAPIOutputItem 表示响应中的一个输出项。
type OpenAPIOutputItem struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Status  string                 `json:"status"`
	Role    string                 `json:"role"`
	Content []OpenAPIOutputContent `json:"content"`
}

// OpenAPIOutputContent 表示输出项中的文本内容块。
type OpenAPIOutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
	Logprobs    []any  `json:"logprobs"`
}

// OpenAPIUsage 记录本次请求的 Token 使用量。
type OpenAPIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ---- 错误 ----

// OpenAPIError 表示服务端返回的非 2xx 错误。
type OpenAPIError struct {
	StatusCode int
	Type       string
	Code       any
	Param      any
	Message    string
}

// Error 返回便于查看的 API 错误信息。
func (e *OpenAPIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("responses API request failed, HTTP status %d", e.StatusCode)
	}
	return fmt.Sprintf("responses API request failed, HTTP status %d: %s", e.StatusCode, e.Message)
}

// ---- 构造器 ----

// NewOpenAPIClient 使用代码中的默认地址、模型和环境变量中的 API Key 创建客户端。
func NewOpenAPIClient() *OpenAPIClient {
	return NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: os.Getenv("DEEPSEEK_API_KEY")})
}

// NewOpenAPIClientWithConfig 使用指定配置创建客户端，空字段使用默认值。
func NewOpenAPIClientWithConfig(cfg OpenAPIConfig) *OpenAPIClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultResponsesURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultResponsesModel
	}
	return &OpenAPIClient{HTTPClient: &http.Client{}, config: cfg}
}

// ---- 请求构造 ----

// NewOpenAPIRequest 按 curl 示例创建请求，调用方可以继续修改返回值。
// 注意：这是无记忆的底层构造器，参与短期记忆的调用请使用 Invoke 或流式方法。
func NewOpenAPIRequest(instructions string, input any) OpenAPIRequest {
	return OpenAPIRequest{
		Input: input, Instructions: instructions,
		Reasoning:       OpenAPIReasoning{Effort: "none"},
		MaxOutputTokens: 4096,
		Temperature:     1,
		TopP:            1,
		Text: OpenAPIText{Format: OpenAPITextFormat{
			Type: "text", Name: "string", Schema: map[string]any{},
		}},
		ToolChoice: "none",
		User:       "string",
	}
}

// ---- 短期记忆 ----

// History 返回短期记忆的对话历史副本（最近 maxHistoryMessages 条）。
// 深拷贝：修改返回值不会影响客户端内部状态。
func (c *OpenAPIClient) History() []map[string]string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyHistory(c.history)
}

// AddHistory 追加一条对话消息，超过 maxHistoryMessages 条时丢弃最旧的消息。
// 返回值为历史深拷贝（安全快照），修改返回值不会影响客户端内部状态。
func (c *OpenAPIClient) AddHistory(m map[string]string) []map[string]string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(m)
	return copyHistory(c.history)
}

// ClearHistory 清空短期记忆。
func (c *OpenAPIClient) ClearHistory() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = nil
}

// addHistoryLocked 在已持有锁的前提下追加消息并裁剪窗口。
func (c *OpenAPIClient) addHistoryLocked(m map[string]string) {
	c.history = append(c.history, m)
	if len(c.history) > maxHistoryMessages {
		c.history = append([]map[string]string(nil), c.history[len(c.history)-maxHistoryMessages:]...)
	}
}

// copyHistory 深拷贝历史，返回独立副本，调用方修改不影响客户端内部状态。
func copyHistory(h []map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(h))
	for _, m := range h {
		cp := make(map[string]string, len(m))
		for k, v := range m {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

// appendUser 追加一条 user 消息并返回历史深拷贝快照，供请求体使用。
// 快照只读，与并发 AddHistory 互不影响。
func (c *OpenAPIClient) appendUser(content string) []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(map[string]string{"role": "user", "content": content})
	return copyHistory(c.history)
}

// appendAssistant 追加一条 assistant 消息（模型回复正文，不含推理过程）。
func (c *OpenAPIClient) appendAssistant(content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(map[string]string{"role": "assistant", "content": content})
}

// ---- 非流式调用 ----

// Invoke 发起一次非流式请求并返回完整响应。
// 参与短期记忆：自动把 content 作为 user 消息写入历史并随请求发送，
// 成功后把响应的正文作为 assistant 消息写回历史；失败时保留 user 消息、不写 assistant。
// 需要完全自定义请求体时请使用 CreateResponse。
func (c *OpenAPIClient) Invoke(instructions string, content string) (*OpenAPIResponse, error) {
	if c == nil {
		return nil, errors.New("OpenAPIClient is nil")
	}
	history := c.appendUser(content)
	request := NewOpenAPIRequest(instructions, history)
	resp, err := c.CreateResponse(context.Background(), request)
	if err != nil {
		return nil, err
	}
	c.appendAssistant(resp.OutputText())
	return resp, nil
}

// CreateResponse 使用给定上下文和请求体执行非流式调用。
// 这是不带短期记忆的底层方法：请求体完全由调用方构造，历史不会被读写。
func (c *OpenAPIClient) CreateResponse(ctx context.Context, request OpenAPIRequest) (*OpenAPIResponse, error) {
	if c == nil {
		return nil, errors.New("OpenAPIClient is nil")
	}
	if c.config.APIKey == "" {
		return nil, errors.New("API key is missing: set OpenAPIConfig.APIKey in source code")
	}
	if request.Model == "" {
		request.Model = c.config.Model
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create responses request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send responses request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read responses response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, decodeOpenAPIError(httpResp.StatusCode, responseBody)
	}

	var response OpenAPIResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode responses response: %w", err)
	}
	return &response, nil
}

// OutputText 合并响应中所有输出内容块的文本。
func (r *OpenAPIResponse) OutputText() string {
	if r == nil {
		return ""
	}
	var result strings.Builder
	for _, item := range r.Output {
		for _, content := range item.Content {
			result.WriteString(content.Text)
		}
	}
	return result.String()
}

// ---- 流式调用 ----

// Stream 发起 SSE 流式请求，每收到一个增量即回调 onDelta（回调式）。
// 参与短期记忆：自动记录本轮 user 消息，流成功结束后自动记录 assistant 正文。
func (c *OpenAPIClient) Stream(prompt string, content string, onDelta func(StreamDelta)) error {
	ch, errCh := c.StreamChan(prompt, content)
	for d := range ch {
		onDelta(d)
	}
	return <-errCh
}

// StreamChan 发起 SSE 流式请求，返回 channel 逐条消费增量（通道式）。
// 参与短期记忆：自动记录本轮 user 消息，流成功结束后自动记录 assistant 正文。
func (c *OpenAPIClient) StreamChan(prompt string, content string) (<-chan StreamDelta, <-chan error) {
	return c.StreamChanContext(context.Background(), prompt, content)
}

// StreamIter 发起 SSE 流式请求，返回可 for-range 的迭代器（迭代器式）。
// 参与短期记忆：自动记录本轮 user 消息，流成功结束后自动记录 assistant 正文。
// 注意：消费方提前 break 时底层发送 goroutine 仍会继续等待写入（与 api.go 一致的既有限制）。
func (c *OpenAPIClient) StreamIter(prompt string, content string) iter.Seq2[StreamDelta, error] {
	return func(yield func(StreamDelta, error) bool) {
		ch, errCh := c.StreamChan(prompt, content)
		for d := range ch {
			if !yield(d, nil) {
				return
			}
		}
		if err := <-errCh; err != nil {
			yield(StreamDelta{}, err)
		}
	}
}

// StreamChanContext 发起支持取消的 SSE 流式请求。ctx 取消后会关闭请求并返回错误。
// 参与短期记忆：请求发出前把本轮用户消息写入历史并随历史一起发送（快照只读、并发安全），
// 流成功结束后把累积的正文增量作为 assistant 消息写入历史（不记录推理过程）。
func (c *OpenAPIClient) StreamChanContext(ctx context.Context, prompt string, content string) (<-chan StreamDelta, <-chan error) {
	deltas := make(chan StreamDelta)
	errs := make(chan error, 1)
	go func() {
		defer close(deltas)
		defer close(errs)
		if c == nil {
			errs <- errors.New("OpenAPIClient is nil")
			return
		}
		if c.config.APIKey == "" {
			errs <- errors.New("API key is missing: set OpenAPIConfig.APIKey in source code")
			return
		}
		// 短期记忆：登记本轮用户消息并快照历史，请求体使用快照。
		history := c.appendUser(content)
		request := NewOpenAPIRequest(prompt, history)
		request.Model = c.config.Model
		request.Stream = true
		body, err := json.Marshal(request)
		if err != nil {
			errs <- fmt.Errorf("marshal streaming responses request: %w", err)
			return
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL, bytes.NewReader(body))
		if err != nil {
			errs <- fmt.Errorf("create streaming responses request: %w", err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		httpClient := c.HTTPClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
		httpResp, err := httpClient.Do(httpReq)
		if err != nil {
			errs <- fmt.Errorf("send streaming responses request: %w", err)
			return
		}
		defer httpResp.Body.Close()
		if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
			responseBody, readErr := io.ReadAll(httpResp.Body)
			if readErr != nil {
				errs <- fmt.Errorf("read streaming responses error: %w", readErr)
				return
			}
			errs <- decodeOpenAPIError(httpResp.StatusCode, responseBody)
			return
		}
		// 累积正文增量，仅在流成功结束时写入 assistant 轮次。
		var assistant strings.Builder
		success := false
		defer func() {
			if success {
				c.appendAssistant(assistant.String())
			}
		}()
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				success = true
				return
			}
			var event openAPIStreamEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				errs <- fmt.Errorf("decode streaming responses event: %w", err)
				return
			}
			if event.Type == "response.failed" && event.Response != nil && event.Response.Error != nil {
				errs <- errors.New(event.Response.Error.Message)
				return
			}
			if event.Delta == "" {
				continue
			}
			// 根据事件类型区分思考过程与正文。
			delta := StreamDelta{}
			if strings.Contains(event.Type, "reasoning") {
				delta.ReasoningContent = event.Delta
			} else {
				delta.Content = event.Delta
				assistant.WriteString(event.Delta)
			}
			select {
			case deltas <- delta:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("read streaming responses event: %w", err)
			return
		}
		success = true
	}()
	return deltas, errs
}

// ---- 内部 ----

type openAPIStreamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func decodeOpenAPIError(statusCode int, body []byte) error {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Param   any    `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Error.Message == "" {
		payload.Error.Message = strings.TrimSpace(string(body))
	}
	return &OpenAPIError{
		StatusCode: statusCode,
		Type:       payload.Error.Type,
		Code:       payload.Error.Code,
		Param:      payload.Error.Param,
		Message:    payload.Error.Message,
	}
}
