package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newTestOpenAPIClient 返回指向 srv 的测试客户端，避免命中真实 API。
func newTestOpenAPIClient(srv *httptest.Server) *OpenAPIClient {
	return NewOpenAPIClientWithConfig(OpenAPIConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
}

// simpleOpenAPIResponse 返回包含固定文本的一条非流式响应。
func simpleOpenAPIResponse(text string) string {
	return fmt.Sprintf(`{"id":"r1","object":"response","output":[{"id":"m1","type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, text)
}

// equalMessages 比较两条对话历史是否逐条一致。
func equalMessages(a, b []map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i]["role"] != b[i]["role"] || a[i]["content"] != b[i]["content"] {
			return false
		}
	}
	return true
}

func TestOpenAPIAddHistoryTrims(t *testing.T) {
	c := NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: "k"})
	for i := 0; i < 25; i++ {
		c.AddHistory(map[string]string{"role": "user", "content": fmt.Sprintf("msg-%d", i)})
	}
	got := c.History()
	if len(got) != maxHistoryMessages {
		t.Fatalf("history length = %d, want %d", len(got), maxHistoryMessages)
	}
	if got[0]["content"] != "msg-5" {
		t.Errorf("oldest kept = %q, want %q", got[0]["content"], "msg-5")
	}
	if got[len(got)-1]["content"] != "msg-24" {
		t.Errorf("newest kept = %q, want %q", got[len(got)-1]["content"], "msg-24")
	}
}

func TestOpenAPIAddHistoryReturnsCopy(t *testing.T) {
	c := NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: "k"})
	c.AddHistory(map[string]string{"role": "user", "content": "hi"})
	got := c.AddHistory(map[string]string{"role": "user", "content": "second"})
	got[0]["content"] = "mutated"
	if inner := c.History(); inner[0]["content"] == "mutated" {
		t.Error("returned slice is not a copy: mutating it changed internal history")
	}
}

func TestOpenAPIClearHistory(t *testing.T) {
	c := NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: "k"})
	c.AddHistory(map[string]string{"role": "user", "content": "hi"})
	c.ClearHistory()
	if got := c.History(); len(got) != 0 {
		t.Errorf("history after ClearHistory = %v, want empty", got)
	}
}

func TestOpenAPIInvokeKeepsMemory(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, simpleOpenAPIResponse("你好"))
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	if _, err := c.Invoke("你是助手", "第一句"); err != nil {
		t.Fatalf("first Invoke: %v", err)
	}
	if _, err := c.Invoke("你是助手", "第二句"); err != nil {
		t.Fatalf("second Invoke: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("received %d requests, want 2", len(bodies))
	}

	var first struct {
		Input []map[string]string `json:"input"`
	}
	if err := json.Unmarshal(bodies[0], &first); err != nil {
		t.Fatalf("decode first request: %v", err)
	}
	wantFirst := []map[string]string{{"role": "user", "content": "第一句"}}
	if !equalMessages(first.Input, wantFirst) {
		t.Errorf("first request input = %v, want %v", first.Input, wantFirst)
	}

	var second struct {
		Input []map[string]string `json:"input"`
	}
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	wantSecond := []map[string]string{
		{"role": "user", "content": "第一句"},
		{"role": "assistant", "content": "你好"},
		{"role": "user", "content": "第二句"},
	}
	if !equalMessages(second.Input, wantSecond) {
		t.Errorf("second request input = %v, want %v", second.Input, wantSecond)
	}
}

func TestOpenAPIInvokeAPIErrorSkipsAssistant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	if _, err := c.Invoke("sys", "问题"); err == nil {
		t.Fatal("expected error from Invoke")
	}
	want := []map[string]string{{"role": "user", "content": "问题"}}
	if !equalMessages(c.History(), want) {
		t.Errorf("history = %v, want %v (user kept, assistant skipped)", c.History(), want)
	}
}

func TestOpenAPIInvokeTrimsHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, simpleOpenAPIResponse("hi"))
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	for i := 1; i <= 25; i++ {
		if _, err := c.Invoke("sys", fmt.Sprintf("第%d句", i)); err != nil {
			t.Fatalf("Invoke %d: %v", i, err)
		}
	}

	got := c.History()
	if len(got) != maxHistoryMessages {
		t.Fatalf("history length = %d, want %d", len(got), maxHistoryMessages)
	}
	// 50 条消息（user+assistant 各 25）裁剪到最近 20 条：从第 16 句的 user 开始。
	wantFirst := map[string]string{"role": "user", "content": "第16句"}
	wantLast := map[string]string{"role": "assistant", "content": "hi"}
	if got[0]["role"] != wantFirst["role"] || got[0]["content"] != wantFirst["content"] {
		t.Errorf("oldest kept = %v, want %v", got[0], wantFirst)
	}
	if got[len(got)-1]["role"] != wantLast["role"] || got[len(got)-1]["content"] != wantLast["content"] {
		t.Errorf("newest kept = %v, want %v", got[len(got)-1], wantLast)
	}
}

func TestOpenAPIStreamStoresAssistantTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, strings.Join([]string{
			`data: {"type":"response.reasoning_summary_text.delta","delta":"思考中"}`,
			`data: {"type":"response.output_text.delta","delta":"你"}`,
			`data: {"type":"response.output_text.delta","delta":"好"}`,
			`data: {"type":"response.completed"}`,
			`data: [DONE]`,
		}, "\n")+"\n")
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	ch, errCh := c.StreamChan("你是助手", "你好")
	var gotText strings.Builder
	for d := range ch {
		gotText.WriteString(d.Content)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotText.String() != "你好" {
		t.Errorf("streamed content = %q, want %q", gotText.String(), "你好")
	}
	want := []map[string]string{
		{"role": "user", "content": "你好"},
		{"role": "assistant", "content": "你好"},
	}
	if !equalMessages(c.History(), want) {
		t.Errorf("history = %v, want %v (reasoning must not be recorded)", c.History(), want)
	}
}

func TestOpenAPIStreamIncludesPriorTurns(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n\ndata: [DONE]\n")
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	for _, input := range []string{"第一句", "第二句"} {
		ch, errCh := c.StreamChan("你是助手", input)
		for range ch {
		}
		if err := <-errCh; err != nil {
			t.Fatalf("stream %q: %v", input, err)
		}
	}
	if len(bodies) != 2 {
		t.Fatalf("received %d requests, want 2", len(bodies))
	}

	var second struct {
		Input []map[string]string `json:"input"`
	}
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	want := []map[string]string{
		{"role": "user", "content": "第一句"},
		{"role": "assistant", "content": "你好"},
		{"role": "user", "content": "第二句"},
	}
	if !equalMessages(second.Input, want) {
		t.Errorf("second request input = %v, want %v", second.Input, want)
	}
}

func TestOpenAPIStreamErrorSkipsAssistant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":{"message":"upstream failed"}}`)
	}))
	defer srv.Close()

	c := newTestOpenAPIClient(srv)
	ch, errCh := c.StreamChan("sys", "问题")
	for range ch {
	}
	if err := <-errCh; err == nil {
		t.Fatal("expected stream error")
	}
	want := []map[string]string{{"role": "user", "content": "问题"}}
	if !equalMessages(c.History(), want) {
		t.Errorf("history = %v, want %v (user kept, assistant skipped)", c.History(), want)
	}
}

func TestOpenAPIAddHistoryConcurrent(t *testing.T) {
	c := NewOpenAPIClientWithConfig(OpenAPIConfig{APIKey: "k"})
	const workers = 8
	const perWorker = 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				c.AddHistory(map[string]string{"role": "user", "content": fmt.Sprintf("w%d-%d", w, i)})
			}
		}(w)
	}
	wg.Wait()
	if got := len(c.History()); got != maxHistoryMessages {
		t.Errorf("history length = %d, want %d", got, maxHistoryMessages)
	}
}
