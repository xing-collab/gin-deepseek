package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	. "ai-test/config"
)

type scriptedAgentModel struct {
	decisions []AgentDecision
	seen      []AgentState
	tools     [][]Tool
}

func (m *scriptedAgentModel) Decide(_ context.Context, state AgentState, tools []Tool) (AgentDecision, error) {
	m.seen = append(m.seen, state)
	m.tools = append(m.tools, tools)
	decision := m.decisions[len(m.seen)-1]
	return decision, nil
}

func TestAgentLoopExecutesToolsUntilFinalAnswer(t *testing.T) {
	model := &scriptedAgentModel{decisions: []AgentDecision{
		{Calls: []AgentToolCall{{ID: "call-1", Name: "add", Arguments: map[string]any{"a": 2, "b": 3}}}},
		{Final: "5"},
	}}
	var executed []string
	loop := AgentLoop{
		Model: model,
		Executor: AgentToolFunc(func(_ context.Context, call AgentToolCall) (string, error) {
			executed = append(executed, call.Name)
			return "5", nil
		}),
		MaxSteps: 3,
	}

	result, err := loop.Run(context.Background(), "calculate 2+3")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "5" || result.Steps != 2 {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(executed, []string{"add"}) {
		t.Fatalf("executed = %#v", executed)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("messages = %#v, want user/assistant/tool/assistant", result.Messages)
	}
	if got := result.Messages[1].ToolCalls; len(got) != 1 || got[0].ID != "call-1" {
		t.Fatalf("assistant tool calls = %#v", got)
	}
	if got := result.Messages[2].ToolCallID; got != "call-1" {
		t.Fatalf("tool call id = %q, want call-1", got)
	}
	model.seen[0].Messages[0].Content = "mutated"
	if result.Messages[0].Content != "calculate 2+3" {
		t.Fatal("model received mutable loop state")
	}
}

func TestAgentLoopGroupsMultipleCallsInOneAssistantTurn(t *testing.T) {
	model := &scriptedAgentModel{decisions: []AgentDecision{
		{Calls: []AgentToolCall{
			{ID: "call-1", Name: "first"},
			{ID: "call-2", Name: "second"},
		}},
		{Final: "done"},
	}}
	loop := AgentLoop{
		Model: model,
		Executor: AgentToolFunc(func(_ context.Context, call AgentToolCall) (string, error) {
			return call.Name + " result", nil
		}),
	}

	result, err := loop.Run(context.Background(), "run both")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 5 {
		t.Fatalf("messages = %#v, want user/assistant/tool/tool/assistant", result.Messages)
	}
	if calls := result.Messages[1].ToolCalls; len(calls) != 2 {
		t.Fatalf("assistant calls = %#v, want two calls", calls)
	}
	if result.Messages[2].ToolCallID != "call-1" || result.Messages[3].ToolCallID != "call-2" {
		t.Fatalf("tool results lost call ids: %#v", result.Messages)
	}
}

func TestAgentLoopContinuesInitialMessages(t *testing.T) {
	model := &scriptedAgentModel{decisions: []AgentDecision{{Final: "second answer"}}}
	initial := []AgentMessage{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
	}
	loop := AgentLoop{
		Model:           model,
		Executor:        AgentToolFunc(func(context.Context, AgentToolCall) (string, error) { return "", nil }),
		InitialMessages: initial,
	}

	result, err := loop.Run(context.Background(), "second question")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 4 || result.Messages[2].Content != "second question" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	model.seen[0].Messages[0].Content = "mutated"
	if initial[0].Content != "first question" {
		t.Fatal("initial messages were mutated")
	}
}

func TestChatAgentModelMapsTranscriptAndDecision(t *testing.T) {
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		requestBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"需要查询时间","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]}}]}`)
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithModel("test-model"),
	)
	var reasoning string
	var observed AgentToolCall
	model := ChatAgentModel{
		Client:       client,
		SystemPrompt: "system prompt",
		OnReasoning:  func(value string) { reasoning = value },
		OnToolCall:   func(call AgentToolCall) { observed = call },
	}
	decision, err := model.Decide(context.Background(), AgentState{Messages: []AgentMessage{
		{Role: "user", Content: "现在几点"},
		{Role: "assistant", ToolCalls: []AgentToolCall{{
			ID: "old-call", Name: "old-tool", Arguments: map[string]any{"value": "x"},
		}}},
		{Role: "tool", Content: "old-result", ToolCallID: "old-call"},
	}}, TimeDateTools())
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "需要查询时间" || decision.Reasoning != reasoning {
		t.Fatalf("reasoning = %q decision = %#v", reasoning, decision)
	}
	if len(decision.Calls) != 1 || observed.ID != "call-1" || observed.Name != "get_current_time" {
		t.Fatalf("decision = %#v observed = %#v", decision, observed)
	}

	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(requestBody, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 4 || body.Messages[0]["role"] != "system" {
		t.Fatalf("messages = %#v", body.Messages)
	}
	assistant := body.Messages[2]
	if _, ok := assistant["tool_calls"]; !ok {
		t.Fatalf("assistant tool_calls missing: %#v", assistant)
	}
	toolResult := body.Messages[3]
	if toolResult["tool_call_id"] != "old-call" {
		t.Fatalf("tool result = %#v", toolResult)
	}
}

func TestAgentLoopStopsAtMaxSteps(t *testing.T) {
	loop := AgentLoop{
		Model: AgentModelFunc(func(AgentState) (AgentDecision, error) {
			return AgentDecision{Calls: []AgentToolCall{{Name: "noop"}}}, nil
		}),
		Executor: AgentToolFunc(func(context.Context, AgentToolCall) (string, error) { return "ok", nil }),
		MaxSteps: 2,
	}
	result, err := loop.Run(context.Background(), "loop")
	if !errors.Is(err, ErrAgentMaxSteps) || result.Steps != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAgentLoopRejectsNoAction(t *testing.T) {
	loop := AgentLoop{
		Model: AgentModelFunc(func(AgentState) (AgentDecision, error) {
			return AgentDecision{}, nil
		}),
		Executor: AgentToolFunc(func(context.Context, AgentToolCall) (string, error) {
			return "", nil
		}),
	}

	result, err := loop.Run(context.Background(), "empty")
	if !errors.Is(err, ErrAgentNoAction) || result.Steps != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAgentLoopRejectsFinalAndCalls(t *testing.T) {
	loop := AgentLoop{
		Model: AgentModelFunc(func(AgentState) (AgentDecision, error) {
			return AgentDecision{
				Final: "done",
				Calls: []AgentToolCall{{Name: "noop"}},
			}, nil
		}),
		Executor: AgentToolFunc(func(context.Context, AgentToolCall) (string, error) {
			return "ok", nil
		}),
	}

	result, err := loop.Run(context.Background(), "invalid")
	if !errors.Is(err, ErrAgentInvalidDecision) || result.Steps != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAgentLoopStopsBeforeNextToolWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	executed := 0
	loop := AgentLoop{
		Model: AgentModelFunc(func(AgentState) (AgentDecision, error) {
			return AgentDecision{Calls: []AgentToolCall{{Name: "first"}, {Name: "second"}}}, nil
		}),
		Executor: AgentToolFunc(func(context.Context, AgentToolCall) (string, error) {
			executed++
			cancel()
			return "ok", nil
		}),
	}

	result, err := loop.Run(ctx, "cancel")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1", executed)
	}
}

func TestAgentLoopPassesCopiesToModel(t *testing.T) {
	tools := []Tool{{Function: Function{
		Name: "inspect",
		Parameters: map[string]any{
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}}}
	model := AgentModelWithToolsFunc(func(state AgentState, received []Tool) (AgentDecision, error) {
		if state.Step == 0 {
			received[0].Function.Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"] = "number"
			return AgentDecision{Calls: []AgentToolCall{{
				ID: "call-1", Name: "inspect",
				Arguments: map[string]any{"nested": map[string]any{"value": "original"}},
			}}}, nil
		}
		state.Messages[1].ToolCalls[0].Arguments["nested"].(map[string]any)["value"] = "mutated"
		return AgentDecision{Final: "done"}, nil
	})
	loop := AgentLoop{
		Model: model,
		Executor: AgentToolFunc(func(context.Context, AgentToolCall) (string, error) {
			return "ok", nil
		}),
		Tools: tools,
	}

	result, err := loop.Run(context.Background(), "copy")
	if err != nil {
		t.Fatal(err)
	}
	nested := result.Messages[1].ToolCalls[0].Arguments["nested"].(map[string]any)
	if nested["value"] != "original" {
		t.Fatalf("loop transcript was mutated: %#v", nested)
	}
	propertyType := tools[0].Function.Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"]
	if propertyType != "string" {
		t.Fatalf("tool definition was mutated: %v", propertyType)
	}
}

func TestJSONToolExecutor(t *testing.T) {
	executor := NewJSONToolExecutor(map[string]func(map[string]any) (any, error){
		"echo": func(args map[string]any) (any, error) { return args["value"], nil },
	})
	got, err := executor.Execute(context.Background(), AgentToolCall{
		Name: "echo", Arguments: map[string]any{"value": "ok"},
	})
	if err != nil || got != `"ok"` {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestJSONToolExecutorRejectsUnknownTool(t *testing.T) {
	executor := NewJSONToolExecutor(nil)
	if _, err := executor.Execute(context.Background(), AgentToolCall{Name: "missing"}); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

// AgentModelFunc 将简单回调适配为 AgentModel，供测试和小型集成使用。
type AgentModelFunc func(AgentState) (AgentDecision, error)

func (f AgentModelFunc) Decide(_ context.Context, state AgentState, _ []Tool) (AgentDecision, error) {
	return f(state)
}

// AgentModelWithToolsFunc 在副本隔离测试中保留对工具声明的访问。
type AgentModelWithToolsFunc func(AgentState, []Tool) (AgentDecision, error)

func (f AgentModelWithToolsFunc) Decide(_ context.Context, state AgentState, tools []Tool) (AgentDecision, error) {
	return f(state, tools)
}
