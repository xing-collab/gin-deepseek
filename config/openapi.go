package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultResponsesURL    = "https://api.deepseek.com/responses"
	defaultResponsesModel  = "deepseek-v4-flash"
	defaultResponsesAPIKey = ""
)

// OpenAPIConfig 保存 Responses API 客户端配置。
	BaseURL string
	APIKey  string
	Model   string
}

// OpenAPIClient 封装 Responses API 的 HTTP 调用。
	HTTPClient *http.Client
	config     OpenAPIConfig
}

// OpenAPIReasoning 配置模型的推理强度。
	Effort string `json:"effort"`
}

// OpenAPITextFormat 配置文本输出格式和结构化 Schema。
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// OpenAPIText 配置 Responses API 的文本输出选项。
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

// OpenAPIResponse 表示一次非流式 Responses API 响应。
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
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Status  string                 `json:"status"`
	Role    string                 `json:"role"`
	Content []OpenAPIOutputContent `json:"content"`
}

// OpenAPIOutputContent 表示输出项中的文本内容块。
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
	Logprobs    []any  `json:"logprobs"`
}

// OpenAPIUsage 记录本次请求的 Token 使用量。
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// OpenAPIError 表示服务端返回的非 2xx 错误。
	StatusCode int
	Type       string
	Code       any
	Param      any
	Message    string
}

// Error 返回便于查看的 API 错误信息。
	if e.Message == "" {
		return fmt.Sprintf("responses API request failed, HTTP status %d", e.StatusCode)
	}
	return fmt.Sprintf("responses API request failed, HTTP status %d: %s", e.StatusCode, e.Message)
}

// NewOpenAPIClient 使用代码中的默认地址、模型和 API Key 创建客户端。
	return NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: defaultResponsesAPIKey})
}

// NewOpenAPIClientWithConfig 使用指定配置创建客户端，空字段使用默认值。
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultResponsesURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultResponsesModel
	}
	return &OpenAPIClient{HTTPClient: &http.Client{}, config: cfg}
}

// NewOpenAPIRequest 按 curl 示例创建请求，调用方可以继续修改返回值。
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

// Invoke 发起一次非流式请求并返回完整响应。
	return c.CreateResponse(context.Background(), NewOpenAPIRequest(instructions, input))
}

// CreateResponse 使用给定上下文和请求体执行非流式调用。
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

// OpenAPIStreamDelta 表示流式响应中的一个事件增量。
	Type  string
	Delta string
}

type openAPIStreamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// StreamChan 发起 SSE 流式请求，并通过 channel 返回文本增量。
func (c *OpenAPIClient) StreamChan(instructions string, input any) (<-chan OpenAPIStreamDelta, <-chan error) {
	return c.StreamChanContext(context.Background(), instructions, input)
}

// StreamChanContext 发起支持取消的 SSE 流式请求。ctx 取消后会关闭请求并返回错误。
func (c *OpenAPIClient) StreamChanContext(ctx context.Context, instructions string, input any) (<-chan OpenAPIStreamDelta, <-chan error) {
	deltas := make(chan OpenAPIStreamDelta)
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
		request := NewOpenAPIRequest(instructions, input)
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
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
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
			select {
			case deltas <- OpenAPIStreamDelta{Type: event.Type, Delta: event.Delta}:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("read streaming responses event: %w", err)
		}
	}()
	return deltas, errs
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
