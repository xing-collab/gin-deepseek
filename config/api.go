package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"strings"
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
	Model           string              `json:"model"`
	Messages        []map[string]string `json:"messages"`
	Thinking        map[string]string   `json:"thinking"`
	ReasoningEffort string              `json:"reasoning_effort"`
	Stream          bool                `json:"stream"`
}

// ---- 非流式响应 ----

// Message 消息内容
type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
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
		} `json:"delta"`
	} `json:"choices"`
}

// ---- 回话历史 -----

// ---- 客户端 ----

// LLM 大模型客户端
type LLM struct {
	HTTPClient *http.Client
	config     *BaseConfig
	history    []map[string]string
}

// NewClient 创建客户端 类似与java构造器
func NewClient() *LLM {
	return &LLM{
		HTTPClient: &http.Client{},
		config: &BaseConfig{
			baseUrl:   "https://api.deepseek.com/chat/completions",
			apiKey:    "",
			modelName: "deepseek-v4-pro",
		},
		history: []map[string]string{},
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

	return scanSSE(response.Body, func(delta StreamDelta) bool {
		onDelta(delta)
		return true
	})
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

		if err := scanSSE(response.Body, func(delta StreamDelta) bool {
			ch <- delta
			return true
		}); err != nil {
			errCh <- err
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

		if err := scanSSE(response.Body, func(delta StreamDelta) bool {
			return yield(delta, nil)
		}); err != nil {
			yield(StreamDelta{}, err)
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

	response, err := llm.HTTPClient.Do(httpReq)
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
	return llm.HTTPClient.Do(httpReq)
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
	if len(llm.history) == 0 {
		promptInfo := map[string]string{"role": "system", "content": prompt}
		llm.AddHistory(promptInfo)
	}
	contentInfo := map[string]string{"role": "user", "content": content}
	apiReq := ApiRequest{
		Model:           llm.config.modelName,
		Messages:        llm.AddHistory(contentInfo),
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

// addHistory 追加一条对话消息。history[0] 固定为 system 不可删，
// 对话消息超过 20 条时，一次删除第二、三条（下标 1、2），再追加新消息。
func (llm *LLM) AddHistory(m map[string]string) []map[string]string {
	if len(llm.history) > 20 {
		// 保留下标 0（system），删除下标 1、2（最旧两条对话）
		llm.history = append(llm.history[:1], llm.history[3:]...)
	}
	llm.history = append(llm.history, m)
	return llm.history
}
