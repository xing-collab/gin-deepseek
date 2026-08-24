package test

import (
	. "ai-test/config"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type mockMCPClient struct {
	tools []MCPTool
	name  string
	args  map[string]any
}

func (m *mockMCPClient) ListTools(context.Context) ([]MCPTool, error) {
	return m.tools, nil
}

func (m *mockMCPClient) CallTool(_ context.Context, name string, args map[string]any) (any, error) {
	m.name = name
	m.args = args
	return map[string]any{"ok": true}, nil
}

func TestChatCompletionsInvokeWithSharedTools(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]}}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer server.Close()

	client := NewClient(
		WithBaseURL(server.URL),
		WithAPIKey("test-key"),
		WithModel("test-model"),
	)
	toolCalls := 0
	response, err := client.InvokeWithTools(
		"Use tools for current time questions.",
		"What time is it?",
		TimeDateTools(),
		func(name string, args map[string]any) (string, error) {
			toolCalls++
			if name != "get_current_time" {
				t.Fatalf("tool name = %q, want get_current_time", name)
			}
			return "12:34:56", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Choices[0].Message.Content; got != "done" {
		t.Fatalf("content = %q, want done", got)
	}
	if toolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", toolCalls)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}

	var first struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(bodies[0], &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Tools) != 2 || first.Tools[0].Function.Name != "get_current_time" {
		t.Fatalf("tools = %#v", first.Tools)
	}

	var second struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatal(err)
	}
	last := second.Messages[len(second.Messages)-1]
	if last["role"] != "tool" || last["content"] != "12:34:56" {
		t.Fatalf("tool result message = %#v", last)
	}
}

func TestTimeDateHandlerRejectsUnknownTool(t *testing.T) {
	if _, err := TimeDateHandler("missing", nil); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestToolRegistryRegistersExternalFunction(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.RegisterFunction(
		"add",
		"Add two numbers.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			"required": []any{"a", "b"},
		},
		func(_ context.Context, args map[string]any) (string, error) {
			return fmt.Sprintf("%.0f", args["a"].(float64)+args["b"].(float64)), nil
		},
	); err != nil {
		t.Fatal(err)
	}

	tools := registry.Tools()
	if len(tools) != 1 || tools[0].Function.Name != "add" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := registry.Execute(context.Background(), AgentToolCall{
		Name:      "add",
		Arguments: map[string]any{"a": 2.0, "b": 3.0},
	})
	if err != nil || result != "5" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestToolRegistryRejectsDuplicateAndUnknownTools(t *testing.T) {
	registry := NewToolRegistry()
	handler := func(context.Context, map[string]any) (string, error) { return "ok", nil }
	if err := registry.RegisterFunction("echo", "Echo.", nil, handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterFunction("echo", "Echo again.", nil, handler); !errors.Is(err, ErrToolDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := registry.Execute(context.Background(), AgentToolCall{Name: "missing"}); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestToolRegistryReturnsDeclarationCopies(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.RegisterFunction(
		"inspect",
		"Inspect value.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
		func(context.Context, map[string]any) (string, error) { return "ok", nil },
	); err != nil {
		t.Fatal(err)
	}

	first := registry.Tools()
	first[0].Function.Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"] = "number"
	second := registry.Tools()
	got := second[0].Function.Parameters["properties"].(map[string]any)["value"].(map[string]any)["type"]
	if got != "string" {
		t.Fatalf("registry declaration was mutated: %v", got)
	}
}

func TestAdaptTypedHandler(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}

	handler := AdaptTypedHandler(func(_ context.Context, args input) (string, error) {
		return "你好，" + args.Name, nil
	})
	result, err := handler(context.Background(), map[string]any{"name": "小明"})
	if err != nil || result != "你好，小明" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRegisterTypedFunction(t *testing.T) {
	type input struct {
		Count int `json:"count"`
	}

	registry := NewToolRegistry()
	err := RegisterTypedFunction(
		registry,
		"repeat",
		"返回重复次数。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count": map[string]any{"type": "integer"},
			},
		},
		func(_ context.Context, args input) (string, error) {
			return fmt.Sprintf("%d", args.Count), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := registry.Execute(context.Background(), AgentToolCall{
		Name:      "repeat",
		Arguments: map[string]any{"count": 3.0},
	})
	if err != nil || result != "3" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRegisterReflectFunction(t *testing.T) {
	registry := NewToolRegistry()
	err := registry.RegisterReflectFunction(
		"greet",
		"向用户问候。",
		func(name string) string { return "你好，" + name },
		ToolParameter{Name: "name", Description: "姓名。", Required: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	tools := registry.Tools()
	if got := tools[0].Function.Parameters["properties"].(map[string]any)["name"].(map[string]any)["type"]; got != "string" {
		t.Fatalf("inferred schema type = %v", got)
	}
	result, err := registry.Execute(context.Background(), AgentToolCall{
		Name:      "greet",
		Arguments: map[string]any{"name": "小明"},
	})
	if err != nil || result != "你好，小明" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRegisterReflectFunctionWithContextAndError(t *testing.T) {
	registry := NewToolRegistry()
	err := registry.RegisterReflectFunction(
		"double",
		"计算两倍数值。",
		func(ctx context.Context, value int) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", value*2), nil
		},
		ToolParameter{Name: "value", Required: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(context.Background(), AgentToolCall{
		Name:      "double",
		Arguments: map[string]any{"value": 4.0},
	})
	if err != nil || result != "8" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}

func TestRegisterMCPTools(t *testing.T) {
	client := &mockMCPClient{tools: []MCPTool{{
		Name:        "forecast",
		Description: "查询天气。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
	}}}
	registry := NewToolRegistry()
	if err := RegisterMCPTools(context.Background(), registry, client, "weather"); err != nil {
		t.Fatal(err)
	}
	tools := registry.Tools()
	if len(tools) != 1 || tools[0].Function.Name != "mcp_weather_forecast" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := registry.Execute(context.Background(), AgentToolCall{
		Name: "mcp_weather_forecast", Arguments: map[string]any{"city": "上海"},
	})
	if err != nil || result != `{"ok":true}` {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if client.name != "forecast" || client.args["city"] != "上海" {
		t.Fatalf("MCP call = %q %#v", client.name, client.args)
	}
}

func TestSkillRegistry(t *testing.T) {
	registry := NewSkillRegistry()
	if err := registry.Register(Skill{Name: "weather", Instructions: "查询天气时使用工具。"}); err != nil {
		t.Fatal(err)
	}
	prompt, err := registry.Prompt("weather")
	if err != nil || prompt != "查询天气时使用工具。" {
		t.Fatalf("prompt=%q err=%v", prompt, err)
	}
	if _, err := registry.Prompt("missing"); err == nil {
		t.Fatal("expected missing skill error")
	}
}

func TestLoadSkillUsesMarkdownTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("# 天气查询\n\n使用天气工具。"), 0600); err != nil {
		t.Fatal(err)
	}
	skill, err := LoadSkill(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "天气查询" || skill.SourcePath != path {
		t.Fatalf("skill = %#v", skill)
	}
}

func TestMCPToolNameRejectsUnsupportedName(t *testing.T) {
	if got := MCPToolName("服务", "天气"); got != "" {
		t.Fatalf("tool name = %q, want empty", got)
	}
}

func TestContainsTimeIntent(t *testing.T) {
	for _, input := range []string{"早上好", "现在几点", "Good evening"} {
		if !ContainsTimeIntent(input) {
			t.Fatalf("containsTimeIntent(%q) = false", input)
		}
	}
	if ContainsTimeIntent("帮我写一首诗") {
		t.Fatal("unexpected time intent")
	}
}

func TestJoinSkillPrompts(t *testing.T) {
	if got := JoinSkillPrompts(" A ", "", "B "); got != "A\n\nB" {
		t.Fatalf("joined prompt = %q", got)
	}
}
