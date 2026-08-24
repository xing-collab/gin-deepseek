package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

// ToolParameter 描述反射注册函数的一个参数。
// 参数类型由函数签名推断，注册时只需要提供模型可见的名称和说明。
type ToolParameter struct {
	Name        string
	Description string
	Required    bool
}

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

// AdaptTypedHandler 将接收具体参数类型的业务方法适配为 Agent 工具处理器。
// 参数会先从 map[string]any 编码为 JSON，再解码到 T；业务方法不需要处理 map。
func AdaptTypedHandler[T any](handler func(context.Context, T) (string, error)) RegisteredToolHandler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		payload, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("编码工具参数: %w", err)
		}
		var input T
		if err := json.Unmarshal(payload, &input); err != nil {
			return "", fmt.Errorf("解析工具参数: %w", err)
		}
		return handler(ctx, input)
	}
}

// AdaptTypedHandlerWithoutContext 适配不需要 context 的具体参数方法。
func AdaptTypedHandlerWithoutContext[T any](handler func(T) (string, error)) RegisteredToolHandler {
	if handler == nil {
		return nil
	}
	return AdaptTypedHandler(func(_ context.Context, input T) (string, error) {
		return handler(input)
	})
}

// RegisterTypedFunction 注册接收具体参数类型的业务方法。
// 例如 handler 可以定义为 func(context.Context, WeatherArgs) (string, error)。
func RegisterTypedFunction[T any](
	r *ToolRegistry,
	name string,
	description string,
	parameters map[string]any,
	handler func(context.Context, T) (string, error),
) error {
	return r.RegisterFunction(name, description, parameters, AdaptTypedHandler(handler))
}

// RegisterTypedFunctionWithoutContext 注册不需要 context 的具体参数方法。
func RegisterTypedFunctionWithoutContext[T any](
	r *ToolRegistry,
	name string,
	description string,
	parameters map[string]any,
	handler func(T) (string, error),
) error {
	return r.RegisterFunction(name, description, parameters, AdaptTypedHandlerWithoutContext(handler))
}

// RegisterReflectFunction 注册普通 Go 函数，不要求业务方法接收 map 或自定义参数结构体。
// 支持 func(T...) string、func(T...) (string, error)，以及在首参数位置接收 context.Context。
// 参数名称和说明通过 parameters 提供，参数类型和 JSON Schema 由函数签名自动推断。
func (r *ToolRegistry) RegisterReflectFunction(
	name string,
	description string,
	handler any,
	parameters ...ToolParameter,
) error {
	fn, err := newReflectToolHandler(handler, parameters)
	if err != nil {
		return err
	}
	declaration, err := reflectToolDeclaration(name, description, handler, parameters)
	if err != nil {
		return err
	}
	return r.Register(declaration, fn)
}

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
)

func reflectFunctionType(handler any) (reflect.Type, error) {
	if handler == nil {
		return nil, ErrToolHandlerNil
	}
	typ := reflect.TypeOf(handler)
	if typ.Kind() != reflect.Func {
		return nil, fmt.Errorf("tool handler must be a function, got %s", typ.Kind())
	}
	if typ.IsVariadic() {
		return nil, errors.New("variadic tool handlers are not supported")
	}
	return typ, nil
}

func reflectArgumentTypes(handler any) ([]reflect.Type, error) {
	typ, err := reflectFunctionType(handler)
	if err != nil {
		return nil, err
	}
	start := 0
	if typ.NumIn() > 0 && typ.In(0).Implements(contextType) {
		start = 1
	}
	args := make([]reflect.Type, typ.NumIn()-start)
	for i := range args {
		args[i] = typ.In(start + i)
	}
	return args, nil
}

func newReflectToolHandler(handler any, parameters []ToolParameter) (RegisteredToolHandler, error) {
	typ, err := reflectFunctionType(handler)
	if err != nil {
		return nil, err
	}
	argTypes, err := reflectArgumentTypes(handler)
	if err != nil {
		return nil, err
	}
	if len(parameters) != len(argTypes) {
		return nil, fmt.Errorf("tool parameter count = %d, want %d", len(parameters), len(argTypes))
	}
	if typ.NumOut() != 1 && typ.NumOut() != 2 {
		return nil, errors.New("tool handler must return string or (string, error)")
	}
	if typ.Out(0).Kind() != reflect.String {
		return nil, errors.New("tool handler first return value must be string")
	}
	if typ.NumOut() == 2 && !typ.Out(1).Implements(errorType) {
		return nil, errors.New("tool handler second return value must be error")
	}

	fn := reflect.ValueOf(handler)
	return func(ctx context.Context, values map[string]any) (string, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		callArgs := make([]reflect.Value, 0, typ.NumIn())
		if typ.NumIn() > 0 && typ.In(0).Implements(contextType) {
			callArgs = append(callArgs, reflect.ValueOf(ctx))
		}
		for i, parameter := range parameters {
			value, exists := values[parameter.Name]
			if !exists || value == nil {
				if parameter.Required {
					return "", fmt.Errorf("missing required tool argument %q", parameter.Name)
				}
				callArgs = append(callArgs, reflect.Zero(argTypes[i]))
				continue
			}
			converted, err := decodeReflectArgument(value, argTypes[i])
			if err != nil {
				return "", fmt.Errorf("parse tool argument %q: %w", parameter.Name, err)
			}
			callArgs = append(callArgs, converted)
		}

		results := fn.Call(callArgs)
		if typ.NumOut() == 2 && !results[1].IsNil() {
			return "", results[1].Interface().(error)
		}
		return results[0].String(), nil
	}, nil
}

func decodeReflectArgument(value any, typ reflect.Type) (reflect.Value, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return reflect.Value{}, err
	}
	target := reflect.New(typ)
	if err := json.Unmarshal(payload, target.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return target.Elem(), nil
}

func reflectToolDeclaration(name, description string, handler any, parameters []ToolParameter) (Tool, error) {
	argTypes, err := reflectArgumentTypes(handler)
	if err != nil {
		return Tool{}, err
	}
	properties := make(map[string]any, len(parameters))
	required := make([]any, 0, len(parameters))
	for i, parameter := range parameters {
		if parameter.Name == "" {
			return Tool{}, ErrToolNameEmpty
		}
		property := schemaForReflectType(argTypes[i])
		if parameter.Description != "" {
			property["description"] = parameter.Description
		}
		properties[parameter.Name] = property
		if parameter.Required {
			required = append(required, parameter.Name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return Tool{Type: "function", Function: Function{
		Name: name, Description: description, Parameters: schema,
	}}, nil
}

func schemaForReflectType(typ reflect.Type) map[string]any {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	schema := map[string]any{}
	switch typ.Kind() {
	case reflect.String:
		schema["type"] = "string"
	case reflect.Bool:
		schema["type"] = "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema["type"] = "integer"
	case reflect.Float32, reflect.Float64:
		schema["type"] = "number"
	case reflect.Slice, reflect.Array:
		schema["type"] = "array"
		schema["items"] = schemaForReflectType(typ.Elem())
	case reflect.Map, reflect.Struct:
		schema["type"] = "object"
	default:
		schema["type"] = "string"
	}
	return schema
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
