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
	skillPrompt, err := loadSkillPrompt(os.Getenv("AGENT_SKILL_PATH"))
	if err != nil {
		logger.Printf("加载 Skill 失败: %v", err)
		fmt.Println("加载 Skill 失败:", err)
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
			SystemPrompt: joinSystemPrompt(character.BuildSystemPrompt(), skillPrompt),
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
	tools := []struct {
		name        string
		description string
		handler     any
		parameters  []config.ToolParameter
	}{
		{
			name:        "get_current_time",
			description: "获取当前本地时间，格式为 HH:mm:ss。",
			handler:     getCurrentTime,
		},
		{
			name:        "get_current_date",
			description: "获取当前本地日期，格式为 YYYY-MM-DD。",
			handler:     getCurrentDate,
		},
		{
			name:        "get_birthday",
			description: "根据姓名获取用户生日。",
			handler:     getBirthday,
			parameters: []config.ToolParameter{
				{Name: "name", Description: "用户姓名。", Required: true},
			},
		},
	}

	for _, tool := range tools {
		if err := registry.RegisterReflectFunction(
			tool.name,
			tool.description,
			tool.handler,
			tool.parameters...,
		); err != nil {
			return nil, fmt.Errorf("注册工具 %q: %w", tool.name, err)
		}
	}
	return registry, nil
}

// 以下方法是普通业务函数，不需要依赖 Agent 的参数类型。
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

func getBirthday(name string) string {
	return fmt.Sprintf("%s的生日是 2003-08-10", strings.TrimSpace(name))
}

// loadSkillPrompt 从 AGENT_SKILL_PATH 指定的 Markdown 文件加载 Skill。
// path 的来源是环境变量；为空表示本次运行不注入额外 Skill。
func loadSkillPrompt(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	skill, err := config.LoadSkill(path)
	if err != nil {
		return "", err
	}
	return skill.Instructions, nil
}

// joinSystemPrompt 将角色卡提示词和可选 Skill 工作流合并为模型 system prompt。
func joinSystemPrompt(characterPrompt string, skillPrompt string) string {
	if strings.TrimSpace(skillPrompt) == "" {
		return characterPrompt
	}
	return characterPrompt + "\n\n## 当前启用的 Skill\n" + skillPrompt
}
