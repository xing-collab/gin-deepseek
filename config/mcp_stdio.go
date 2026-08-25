package config

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// StdioMCPConfig describes an MCP server started as a child process.
// The server must speak newline-delimited JSON-RPC over stdin/stdout.
type StdioMCPConfig struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Stderr  io.Writer
}

// StdioMCPClient is a small MCP client for servers using the stdio transport.
// It performs the MCP initialize handshake and serializes requests because a
// single stdio stream cannot safely be read by concurrent callers.
type StdioMCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
	nextID int64
	closed bool
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Result  json.RawMessage  `json:"result"`
	Error   *mcpJSONRPCError `json:"error"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// NewStdioMCPClient starts a server and completes the MCP initialization
// handshake. Call Close when the owning agent exits.
func NewStdioMCPClient(ctx context.Context, cfg StdioMCPConfig) (*StdioMCPClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("MCP stdio command is empty")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	if cfg.Env != nil {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	cmd.Stderr = cfg.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create MCP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create MCP stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start MCP server: %w", err)
	}
	client := &StdioMCPClient{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout), nextID: 1}
	if _, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ai-test", "version": "1.0.0"},
	}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize MCP server: %w", err)
	}
	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("finish MCP initialization: %w", err)
	}
	return client, nil
}

// ListTools implements MCPClient.
func (c *StdioMCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	result, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("decode MCP tools/list: %w", err)
	}
	return payload.Tools, nil
}

// CallTool implements MCPClient.
func (c *StdioMCPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (any, error) {
	result, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	var payload struct {
		IsError    bool             `json:"isError"`
		Content    []map[string]any `json:"content"`
		Structured any              `json:"structuredContent"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("decode MCP tools/call: %w", err)
	}
	if payload.IsError {
		return nil, fmt.Errorf("MCP tool %q returned an error: %s", name, formatMCPContent(payload.Content))
	}
	if payload.Structured != nil {
		return payload.Structured, nil
	}
	if text := formatMCPContent(payload.Content); text != "" {
		return text, nil
	}
	return payload.Content, nil
}

// Close stops the child process and releases its pipes.
func (c *StdioMCPClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

func (c *StdioMCPClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("MCP client is closed")
	}
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	return c.writeMessage(message)
}

func (c *StdioMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("MCP client is closed")
	}
	id := c.nextID
	c.nextID++
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.writeMessage(message); err != nil {
		return nil, err
	}
	readDone := make(chan struct{})
	defer close(readDone)
	go func() {
		select {
		case <-ctx.Done():
			if c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
		case <-readDone:
		}
	}()
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read MCP response: %w", err)
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var response mcpJSONRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return nil, fmt.Errorf("decode MCP JSON-RPC response: %w", err)
		}
		if string(response.ID) != fmt.Sprintf("%d", id) {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (c *StdioMCPClient) writeMessage(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := c.stdin.Write(payload); err != nil {
		return fmt.Errorf("write MCP request: %w", err)
	}
	return nil
}

func formatMCPContent(content []map[string]any) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item["type"] == "text" {
			if text, ok := item["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
