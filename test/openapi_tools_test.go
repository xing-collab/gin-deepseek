package test

import (
	. "ai-test/config"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIInvokeWithToolsRoundTrip(t *testing.T) {
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
			fmt.Fprint(w, `{"output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"get_current_time","arguments":"{}"}]}`)
			return
		}
		fmt.Fprint(w, simpleOpenAPIResponse("done"))
	}))
	defer server.Close()

	client := newTestOpenAPIClient(server)
	response, err := client.InvokeWithTools(
		"Use tools for current time questions.",
		"What time is it?",
		TimeDateTools(),
		TimeDateHandler,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.OutputText(); got != "done" {
		t.Fatalf("output = %q, want done", got)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}

	var second map[string]any
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatal(err)
	}
	input, ok := second["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("second input = %#v, want history + call + result", second["input"])
	}
	result, ok := input[2].(map[string]any)
	if !ok || result["type"] != "function_call_output" || result["call_id"] != "call1" {
		t.Fatalf("tool result = %#v", input[2])
	}

	var first map[string]any
	if err := json.Unmarshal(bodies[0], &first); err != nil {
		t.Fatal(err)
	}
	tool := first["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "get_current_time" {
		t.Fatalf("tool shape = %#v", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Fatal("Responses tool unexpectedly used nested function shape")
	}
}

func TestOpenAPIStreamWithToolsMatchesItemIDToCallID(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			fmt.Fprintln(w, `data: {"type":"response.output_item.added","item":{"id":"fc1","type":"function_call","call_id":"call1","name":"get_current_date"}}`)
			fmt.Fprintln(w, `data: {"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"{}"}`)
			fmt.Fprint(w, "data: [DONE]\n")
			return
		}
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"answer"}`)
	}))
	defer server.Close()

	client := newTestOpenAPIClient(server)
	deltas, errs := client.StreamChanWithTools(
		"Use tools for current date questions.",
		"What date is it?",
		TimeDateTools(),
		TimeDateHandler,
	)
	var output strings.Builder
	for delta := range deltas {
		output.WriteString(delta.Content)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "answer" {
		t.Fatalf("stream output = %q, want answer", got)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
