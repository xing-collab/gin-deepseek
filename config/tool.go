package config

import (
	"fmt"
	"time"
)

// Tool describes a function available to both client implementations.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function contains a tool name, description, and JSON Schema parameters.
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Arguments   string         `json:"arguments,omitempty"`
}

// ToolCall is the Chat Completions representation of a function call.
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// OpenAPITool is the flattened Responses API representation of Tool.
type OpenAPITool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
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

// TimeDateTools returns tool declarations shared by both clients.
func TimeDateTools() []Tool {
	return []Tool{
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_time",
				Description: "Get the current local time in HH:mm:ss format.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: Function{
				Name:        "get_current_date",
				Description: "Get the current local date in YYYY-MM-DD format.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
	}
}

// TimeDateHandler executes the shared time and date tools.
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
