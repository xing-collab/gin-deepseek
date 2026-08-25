package test

import (
	"ai-test/config"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestStdioMCPClientDiscoversAndCallsTools(t *testing.T) {
	client, err := config.NewStdioMCPClient(context.Background(), config.StdioMCPConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStdioMCPHelperProcess"},
		Env:     []string{"GO_WANT_MCP_HELPER=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "get_weather_by_region" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.CallTool(context.Background(), "get_weather_by_region", map[string]any{"region": "北京"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "北京：晴，25°C" {
		t.Fatalf("result = %#v", result)
	}
}

func TestStdioMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.ID == nil {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test-weather", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name":        "get_weather_by_region",
				"description": "查询地区天气。",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"region": map[string]any{"type": "string"}},
					"required":   []string{"region"},
				},
			}}}
		case "tools/call":
			region := request.Params["arguments"].(map[string]any)["region"]
			result = map[string]any{"content": []any{map[string]any{
				"type": "text",
				"text": fmt.Sprintf("%s：晴，25°C", region),
			}}}
		default:
			os.Exit(3)
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}
