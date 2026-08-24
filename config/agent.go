package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrAgentModelNil        = errors.New("agent model is nil")
	ErrAgentExecutorNil     = errors.New("agent tool executor is nil")
	ErrAgentMaxSteps        = errors.New("agent reached maximum steps")
	ErrAgentNoAction        = errors.New("agent returned neither a final answer nor a tool call")
	ErrAgentInvalidDecision = errors.New("agent returned both a final answer and tool calls")
)

// AgentMessage 是 AgentLoop 使用的协议无关消息。
// 不同 API 的适配器可将其转换为 messages 或 Responses input items。
type AgentMessage struct {
	Role    string
	Content string
	// ToolCall 为兼容早期适配器而保留。新代码应使用 ToolCalls，
	// 以便在同一轮 assistant 消息中表示多个工具调用。
	ToolCall   *AgentToolCall
	ToolCalls  []AgentToolCall
	ToolCallID string
	ToolName   string
}

// AgentToolCall 表示模型请求执行的工具动作，其中 Arguments 已完成解析。
type AgentToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// AgentDecision 表示模型在一轮中的决定：返回最终答案，或请求执行一个或多个工具。
type AgentDecision struct {
	Final     string
	Reasoning string
	Calls     []AgentToolCall
}

// AgentState 是每轮传给模型适配器的 Agent 状态副本。
type AgentState struct {
	Step     int
	Messages []AgentMessage
}

// AgentModel 根据当前对话记录决定下一步动作。
type AgentModel interface {
	Decide(ctx context.Context, state AgentState, tools []Tool) (AgentDecision, error)
}

// AgentToolExecutor 执行一次指定名称的工具调用。
type AgentToolExecutor interface {
	Execute(ctx context.Context, call AgentToolCall) (string, error)
}

// AgentToolFunc 将普通函数适配为 AgentToolExecutor。
type AgentToolFunc func(context.Context, AgentToolCall) (string, error)

func (f AgentToolFunc) Execute(ctx context.Context, call AgentToolCall) (string, error) {
	if f == nil {
		return "", ErrAgentExecutorNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return f(ctx, call)
}

// AgentLoop 负责协调模型决策与工具执行。
type AgentLoop struct {
	Model           AgentModel
	Executor        AgentToolExecutor
	Tools           []Tool
	InitialMessages []AgentMessage
	MaxSteps        int
}

// AgentResult 包含最终答案以及生成答案时使用的完整对话记录。
type AgentResult struct {
	Answer   string
	Steps    int
	Messages []AgentMessage
}

// Run 执行有最大步数限制的 ReAct 循环，直到模型返回 Final。
func (a AgentLoop) Run(ctx context.Context, prompt string) (AgentResult, error) {
	if a.Model == nil {
		return AgentResult{}, ErrAgentModelNil
	}
	if a.Executor == nil {
		return AgentResult{}, ErrAgentExecutorNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}

	state := AgentState{Messages: cloneAgentMessages(a.InitialMessages)}
	state.Messages = append(state.Messages, AgentMessage{Role: "user", Content: prompt})
	for state.Step < maxSteps {
		if err := ctx.Err(); err != nil {
			return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, err
		}
		decision, err := a.Model.Decide(ctx, cloneAgentState(state), cloneAgentTools(a.Tools))
		if err != nil {
			return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, fmt.Errorf("agent decision at step %d: %w", state.Step+1, err)
		}
		state.Step++
		if decision.Final != "" && len(decision.Calls) > 0 {
			return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, ErrAgentInvalidDecision
		}
		if decision.Final != "" {
			state.Messages = append(state.Messages, AgentMessage{Role: "assistant", Content: decision.Final})
			return AgentResult{Answer: decision.Final, Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, nil
		}
		if len(decision.Calls) == 0 {
			return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, ErrAgentNoAction
		}

		calls := cloneAgentToolCalls(decision.Calls)
		state.Messages = append(state.Messages, AgentMessage{
			Role: "assistant", ToolCall: firstAgentToolCall(calls), ToolCalls: calls,
		})
		for _, call := range calls {
			if err := ctx.Err(); err != nil {
				return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, err
			}
			output, err := a.Executor.Execute(ctx, call)
			if err != nil {
				return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, fmt.Errorf("execute tool %q at step %d: %w", call.Name, state.Step, err)
			}
			state.Messages = append(state.Messages, AgentMessage{
				Role: "tool", Content: output, ToolCallID: call.ID, ToolName: call.Name,
			})
		}
	}
	return AgentResult{Steps: state.Step, Messages: cloneAgentMessages(state.Messages)}, ErrAgentMaxSteps
}

// ChatAgentModel 将 Chat Completions 客户端适配为 AgentModel。
// 回调可用于输出模型思考过程和模型请求的工具调用。
type ChatAgentModel struct {
	Client       *LLM
	SystemPrompt string
	OnReasoning  func(string)
	OnToolCall   func(AgentToolCall)
}

// Decide 使用当前 Agent transcript 发起一次 Chat Completions 工具请求。
func (m ChatAgentModel) Decide(ctx context.Context, state AgentState, tools []Tool) (AgentDecision, error) {
	if m.Client == nil {
		return AgentDecision{}, ErrAgentModelNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	messages, err := agentMessagesAsChat(m.SystemPrompt, state.Messages)
	if err != nil {
		return AgentDecision{}, err
	}
	response, err := m.Client.doToolRequestContext(ctx, messages, tools)
	if err != nil {
		return AgentDecision{}, err
	}
	if len(response.Choices) == 0 {
		return AgentDecision{}, ErrAgentNoAction
	}

	message := response.Choices[0].Message
	decision := AgentDecision{
		Final:     message.Content,
		Reasoning: message.ReasoningContent,
	}
	if decision.Reasoning != "" && m.OnReasoning != nil {
		m.OnReasoning(decision.Reasoning)
	}
	for _, toolCall := range message.ToolCalls {
		var arguments map[string]any
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments); err != nil {
			return AgentDecision{}, fmt.Errorf("解析工具 %q 参数: %w", toolCall.Function.Name, err)
		}
		call := AgentToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: arguments,
		}
		decision.Calls = append(decision.Calls, call)
		if m.OnToolCall != nil {
			m.OnToolCall(call)
		}
	}
	return decision, nil
}

func agentMessagesAsChat(systemPrompt string, messages []AgentMessage) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(messages)+1)
	if systemPrompt != "" {
		out = append(out, map[string]any{"role": "system", "content": systemPrompt})
	}
	for _, message := range messages {
		item := map[string]any{
			"role":    message.Role,
			"content": message.Content,
		}
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				arguments, err := json.Marshal(call.Arguments)
				if err != nil {
					return nil, fmt.Errorf("编码工具 %q 参数: %w", call.Name, err)
				}
				calls = append(calls, map[string]any{
					"id":   call.ID,
					"type": "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": string(arguments),
					},
				})
			}
			item["content"] = nil
			item["tool_calls"] = calls
		}
		if message.Role == "tool" && message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		out = append(out, item)
	}
	return out, nil
}

// NewJSONToolExecutor 根据“工具名称到处理函数”的映射创建执行器。
// 处理函数的返回值会编码为 JSON，便于适配器跨进程传递工具结果。
func NewJSONToolExecutor(handlers map[string]func(map[string]any) (any, error)) AgentToolExecutor {
	return AgentToolFunc(func(ctx context.Context, call AgentToolCall) (string, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		handler := handlers[call.Name]
		if handler == nil {
			return "", fmt.Errorf("unknown tool: %s", call.Name)
		}
		value, err := handler(call.Arguments)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("encode tool result: %w", err)
		}
		return string(encoded), nil
	})
}

func cloneAgentState(state AgentState) AgentState {
	state.Messages = cloneAgentMessages(state.Messages)
	return state
}

func cloneAgentMessages(messages []AgentMessage) []AgentMessage {
	out := make([]AgentMessage, len(messages))
	for i, message := range messages {
		out[i] = message
		if message.ToolCall != nil {
			call := *message.ToolCall
			call.Arguments = cloneAgentMap(call.Arguments)
			out[i].ToolCall = &call
		}
		out[i].ToolCalls = cloneAgentToolCalls(message.ToolCalls)
	}
	return out
}

func firstAgentToolCall(calls []AgentToolCall) *AgentToolCall {
	if len(calls) == 0 {
		return nil
	}
	call := calls[0]
	call.Arguments = cloneAgentMap(call.Arguments)
	return &call
}

func cloneAgentToolCalls(calls []AgentToolCall) []AgentToolCall {
	if calls == nil {
		return nil
	}
	out := make([]AgentToolCall, len(calls))
	for i, call := range calls {
		out[i] = call
		out[i].Arguments = cloneAgentMap(call.Arguments)
	}
	return out
}

func cloneAgentTools(tools []Tool) []Tool {
	if tools == nil {
		return nil
	}
	out := make([]Tool, len(tools))
	for i, tool := range tools {
		out[i] = tool
		out[i].Function.Parameters = cloneAgentMap(tool.Function.Parameters)
	}
	return out
}

func cloneAgentMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneAgentValue(value)
	}
	return out
}

func cloneAgentValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneAgentMap(value)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneAgentValue(item)
		}
		return out
	default:
		return value
	}
}
