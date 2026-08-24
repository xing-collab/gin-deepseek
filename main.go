package main

import (
	"ai-test/config"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Reset  = "\033[0m"
	Blue   = "\033[34m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
)

// ShowReasoning 控制是否在终端打印模型思考；思考始终会写入日志。
const ShowReasoning = true

func main() {
	logger, closeLog, err := newAgentLogger("log/agent.log")
	if err != nil {
		fmt.Println("创建 Agent 日志失败:", err)
		return
	}
	defer closeLog()

	client := config.NewClient(
		config.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
	)
	character, err := config.LoadCharacter("config/priestess.json")
	if err != nil {
		logger.Printf("加载角色卡失败: %v", err)
		fmt.Println("加载角色卡失败:", err)
		return
	}

	toolRegistry, err := buildToolRegistry()
	if err != nil {
		logger.Printf("注册 Agent 工具失败: %v", err)
		fmt.Println("注册 Agent 工具失败:", err)
		return
	}
	var conversation []config.AgentMessage
	fmt.Println("=== Agent Loop 对话（输入 exit 退出）===")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "exit" {
			break
		}
		if input == "" {
			continue
		}

		character.Update(input)
		model := config.ChatAgentModel{
			Client:       client,
			SystemPrompt: character.BuildSystemPrompt(),
			OnReasoning: func(reasoning string) {
				logger.Printf("[思考] %s", reasoning)
				if ShowReasoning {
					fmt.Printf("%s[思考] %s%s\n", Blue, reasoning, Reset)
				}
			},
			OnToolCall: func(call config.AgentToolCall) {
				logger.Printf("[Agent 调用] 工具=%s ID=%s 参数=%s", call.Name, call.ID, formatJSON(call.Arguments))
				fmt.Printf("%s[Agent 调用] %s 参数=%s%s\n", Yellow, call.Name, formatJSON(call.Arguments), Reset)
			},
		}
		executor := config.AgentToolFunc(func(ctx context.Context, call config.AgentToolCall) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			result, err := toolRegistry.Execute(ctx, call)
			if err != nil {
				logger.Printf("[Agent 失败] 工具=%s ID=%s 错误=%v", call.Name, call.ID, err)
				return "", err
			}
			logger.Printf("[Agent 结果] 工具=%s ID=%s 结果=%s", call.Name, call.ID, result)
			fmt.Printf("%s[Agent 结果] %s%s\n", Green, result, Reset)
			return result, nil
		})
		loop := config.AgentLoop{
			Model:           model,
			Executor:        executor,
			Tools:           toolRegistry.Tools(),
			InitialMessages: conversation,
			MaxSteps:        8,
		}

		logger.Printf("[用户] %s", input)
		result, err := loop.Run(context.Background(), input)
		if err != nil {
			logger.Printf("[Agent 错误] %v", err)
			fmt.Println("请求失败:", err)
			continue
		}
		conversation = result.Messages
		logger.Printf("[回答] %s", result.Answer)
		fmt.Println(result.Answer)
	}
	if err := scanner.Err(); err != nil {
		logger.Printf("读取输入失败: %v", err)
		fmt.Println("读取输入失败:", err)
	}
}

// buildToolRegistry 演示如何在 config 包外定义方法，并注册给 Agent 调用。
func buildToolRegistry() (*config.ToolRegistry, error) {
	registry := config.NewToolRegistry()
	if err := registry.RegisterFunction(
		"get_current_time",
		"获取当前本地时间，格式为 HH:mm:ss。",
		config.EmptyObjectSchema(),
		func(_ context.Context, _ map[string]any) (string, error) {
			return getCurrentTime(), nil
		},
	); err != nil {
		return nil, err
	}
	if err := registry.RegisterFunction(
		"get_current_date",
		"获取当前本地日期，格式为 YYYY-MM-DD。",
		config.EmptyObjectSchema(),
		func(_ context.Context, _ map[string]any) (string, error) {
			return getCurrentDate(), nil
		},
	); err != nil {
		return nil, err
	}
	if err := registry.RegisterFunction(
		"get_birthday",
		"传递名称，获取用户生日。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "用户姓名。",
				},
			},
			"required":             []any{"name"},
			"additionalProperties": false,
		},
		getBirthday,
	); err != nil {
		return nil, err
	}
	return registry, nil
}

func getCurrentTime() string {
	return time.Now().Format("15:04:05")
}

func getCurrentDate() string {
	return time.Now().Format("2006-01-02")
}

func newAgentLogger(path string) (*log.Logger, func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, err
	}
	logger := log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	return logger, func() { _ = file.Close() }, nil
}

func formatJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

func getSheng(name string) string {
	s := "2003-08-10"
	name += s
	return name
}

// getBirthday 将 Agent 参数适配为业务层的 getSheng 方法。
func getBirthday(_ context.Context, args map[string]any) (string, error) {
	name, ok := args["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("参数 name 必须是非空字符串")
	}
	return getSheng(name), nil
}
