package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCPTool 是 MCP tools/list 返回的工具描述。
// Name 来自 MCP 服务端；Description 和 InputSchema 来自服务端的工具元数据。
type MCPTool struct {
	// Name 来源：MCP tools/list 响应；意义：调用 tools/call 时使用的原始工具名。
	Name string `json:"name"`
	// Description 来源：MCP tools/list 响应；意义：帮助模型判断何时调用该工具。
	Description string `json:"description"`
	// InputSchema 来源：MCP tools/list 响应；意义：约束模型生成的工具参数。
	InputSchema map[string]any `json:"inputSchema"`
}

// MCPClient 是具体 MCP SDK 的最小适配接口。
// ListTools 获取工具声明；CallTool 使用 MCP 原始名称执行工具。
type MCPClient interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (any, error)
}

// RegisterMCPTools 将 MCP 服务端工具注册到 ToolRegistry。
// ctx 来源于应用启动流程，用于控制 tools/list 的取消和超时。
// registry 是应用创建的统一工具注册表；client 是具体 MCP SDK 的适配实例。
// namespace 是调用方提供的服务名，用于避免不同 MCP 服务的工具重名。
// 例如 namespace=weather、name=forecast 时，暴露给模型的名称为 mcp_weather_forecast。
func RegisterMCPTools(ctx context.Context, registry *ToolRegistry, client MCPClient, namespace string) error {
	if registry == nil {
		return ErrToolRegistryNil
	}
	if client == nil {
		return fmt.Errorf("MCP client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("获取 MCP 工具列表失败: %w", err)
	}

	for _, mcpTool := range tools {
		if strings.TrimSpace(mcpTool.Name) == "" {
			return ErrToolNameEmpty
		}
		originalName := mcpTool.Name
		exposedName := MCPToolName(namespace, originalName)
		if exposedName == "" {
			return fmt.Errorf("MCP 工具名 %q 无法转换为有效的 Agent 工具名", originalName)
		}
		description := mcpTool.Description
		if description == "" {
			description = "由 MCP 服务提供的工具。"
		}
		inputSchema := mcpTool.InputSchema
		if inputSchema == nil {
			inputSchema = EmptyObjectSchema()
		}
		handler := func(callCtx context.Context, args map[string]any) (string, error) {
			result, callErr := client.CallTool(callCtx, originalName, args)
			if callErr != nil {
				return "", fmt.Errorf("调用 MCP 工具 %q: %w", originalName, callErr)
			}
			if text, ok := result.(string); ok {
				return text, nil
			}
			encoded, encodeErr := json.Marshal(result)
			if encodeErr != nil {
				return "", fmt.Errorf("编码 MCP 工具 %q 结果: %w", originalName, encodeErr)
			}
			return string(encoded), nil
		}
		if err := registry.Register(Tool{
			Type: "function",
			Function: Function{
				Name:        exposedName,
				Description: description,
				Parameters:  inputSchema,
			},
		}, handler); err != nil {
			return fmt.Errorf("注册 MCP 工具 %q: %w", exposedName, err)
		}
	}
	return nil
}

// MCPToolName 将 MCP 服务名和原始工具名组合为 Agent 可见名称。
// namespace 为空时，仅返回 mcp_ 加原始工具名。
func MCPToolName(namespace string, toolName string) string {
	namespace = normalizeToolNamePart(namespace)
	toolName = normalizeToolNamePart(toolName)
	if toolName == "" {
		return ""
	}
	if namespace == "" {
		return "mcp_" + toolName
	}
	return "mcp_" + namespace + "_" + toolName
}

func normalizeToolNamePart(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	previousUnderscore := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_'
		if valid {
			builder.WriteRune(char)
			previousUnderscore = false
			continue
		}
		if !previousUnderscore {
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_-")
}
