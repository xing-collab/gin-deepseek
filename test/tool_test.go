package test

import (
	. "ai-test/config"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
