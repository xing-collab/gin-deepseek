package config

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrToolRegistryNil = errors.New("tool registry is nil")
	ErrToolNameEmpty   = errors.New("tool name is empty")
	ErrToolHandlerNil  = errors.New("tool handler is nil")
	ErrToolDuplicate   = errors.New("tool is already registered")
)

// Tool 描述可供客户端和 Agent 使用的函数工具。
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function 保存工具名称、说明和 JSON Schema 参数。
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Arguments   string         `json:"arguments,omitempty"`
}

// ToolCall 是 Chat Completions 协议中的函数调用结构。
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// OpenAPITool 是 Responses API 使用的扁平工具结构。
type OpenAPITool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// RegisteredToolHandler 是外部工具实现的统一签名。
// args 来自模型返回的 JSON 参数，返回值会作为 tool 消息交回模型。
type RegisteredToolHandler func(ctx context.Context, args map[string]any) (string, error)

type registeredTool struct {
	declaration Tool
	handler     RegisteredToolHandler
}

// ToolRegistry 保存提供给模型的工具声明，并按名称分发真实 Go 方法。
// 它实现 AgentToolExecutor，可直接注入 AgentLoop.Executor。
type ToolRegistry struct {
	mu      sync.RWMutex
	order   []string
	entries map[string]registeredTool
}

// NewToolRegistry 创建空工具注册表。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{entries: make(map[string]registeredTool)}
}

// Register 注册完整 Tool 声明及其处理函数。
func (r *ToolRegistry) Register(tool Tool, handler RegisteredToolHandler) error {
	if r == nil {
		return ErrToolRegistryNil
	}
	name := tool.Function.Name
	if name == "" {
		return ErrToolNameEmpty
	}
	if handler == nil {
		return ErrToolHandlerNil
	}
	if tool.Type == "" {
		tool.Type = "function"
	}
	if tool.Function.Parameters == nil {
		tool.Function.Parameters = EmptyObjectSchema()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]registeredTool)
	}
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("%w: %s", ErrToolDuplicate, name)
	}
	r.entries[name] = registeredTool{
		declaration: cloneTool(tool),
		handler:     handler,
	}
	r.order = append(r.order, name)
	return nil
}

// RegisterFunction 使用函数工具常用字段完成注册。
func (r *ToolRegistry) RegisterFunction(
	name string,
	description string,
	parameters map[string]any,
	handler RegisteredToolHandler,
) error {
	return r.Register(Tool{
		Type: "function",
		Function: Function{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}, handler)
}

// Tools 返回按注册顺序排列的工具声明副本。
func (r *ToolRegistry) Tools() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		tools = append(tools, cloneTool(r.entries[name].declaration))
	}
	return tools
}

// Execute 根据模型返回的工具名称调用已注册的外部方法。
func (r *ToolRegistry) Execute(ctx context.Context, call AgentToolCall) (string, error) {
	if r == nil {
		return "", ErrToolRegistryNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	r.mu.RLock()
	entry, exists := r.entries[call.Name]
	r.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("unknown tool: %s", call.Name)
	}
	return entry.handler(ctx, cloneToolArguments(call.Arguments))
}

// EmptyObjectSchema 返回无参数函数可使用的 JSON Schema。
func EmptyObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func cloneTool(tool Tool) Tool {
	tool.Function.Parameters = cloneToolArguments(tool.Function.Parameters)
	return tool
}

func cloneToolArguments(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneToolValue(value)
	}
	return out
}

func cloneToolValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneToolArguments(value)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneToolValue(item)
		}
		return out
	default:
		return value
	}
}

type streamToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

type openAPIStreamToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

func openAPITools(tools []Tool) []OpenAPITool {
	out := make([]OpenAPITool, 0, len(tools))
	for _, tool := range tools {
		kind := tool.Type
		if kind == "" {
			kind = "function"
		}
		out = append(out, OpenAPITool{
			Type:        kind,
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out
}

func outputItemsAsInput(items []OpenAPIOutputItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		m := map[string]any{"type": item.Type}
		if item.ID != "" {
			m["id"] = item.ID
		}
		if item.Status != "" {
			m["status"] = item.Status
		}
		if item.Role != "" {
			m["role"] = item.Role
		}
		if item.CallID != "" {
			m["call_id"] = item.CallID
		}
		if item.Name != "" {
			m["name"] = item.Name
		}
		if item.Arguments != "" {
			m["arguments"] = item.Arguments
		}
		if item.Content != nil {
			m["content"] = item.Content
		}
		out = append(out, m)
	}
	return out
}

func responseFunctionCalls(items []OpenAPIOutputItem) []openAPIStreamToolCall {
	calls := make([]openAPIStreamToolCall, 0)
	for _, item := range items {
		if item.Type == "function_call" {
			calls = append(calls, openAPIStreamToolCall{
				CallID:    item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
	}
	return calls
}

func openAPIStreamCallsAsInput(calls []openAPIStreamToolCall) []any {
	out := make([]any, 0, len(calls))
	for _, call := range calls {
		out = append(out, map[string]any{
			"type":      "function_call",
			"call_id":   call.CallID,
			"name":      call.Name,
			"arguments": call.Arguments,
		})
	}
	return out
}

// TimeDateTools 返回两个客户端共用的时间和日期工具声明。
func TimeDateTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_time",
				Description: "Get the current local time in HH:mm:ss format.",
				Parameters:  EmptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_date",
				Description: "Get the current local date in YYYY-MM-DD format.",
				Parameters:  EmptyObjectSchema(),
			},
		},
	}
}

// TimeDateHandler 执行内置的时间和日期工具。
func TimeDateHandler(name string, _ map[string]any) (string, error) {
	switch name {
	case "get_current_time":
		return GetCurrentTime(), nil
	case "get_current_date":
		return GetCurrentDate(), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func GetCurrentTime() string {
	return time.Now().Format("15:04:05")
}

func GetCurrentDate() string {
	return time.Now().Format("2006-01-02")
}
