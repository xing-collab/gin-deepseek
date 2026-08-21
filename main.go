package main

import (
	"ai-test/config"
	"ai-test/test"
	"bufio"
	"fmt"
	"os"
)

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Purple = "\033[35m"
	Cyan   = "\033[36m"
	Gray   = "\033[37m"
	White  = "\033[97m"
)

func main() {
	c := config.NewOpenAPIClient(
		config.WithAPIKey(os.Getenv("DEEPSEEK_API_KEY")),
	)

	char, err := config.LoadCharacter("config/priestess.json")
	if err != nil {
		fmt.Println("加载角色卡失败:", err)
		return
	}

	// 时间/日期工具：询问时间给时间、询问日期给日期
	tools := config.TimeDateTools()
	handler := config.TimeDateHandler
	fmt.Println("=== 通道式 StreamChan ===")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		if input == "exit" {
			break
		}
		char.Update(input)
		prompt := char.BuildSystemPrompt()
		ch, errCh := c.StreamChanWithTools(prompt, input, tools, handler)
		firstContent := true
		for d := range ch {
			if d.Content != "" && firstContent {
				fmt.Println()
				firstContent = false
			}
			printDelta(d)
		}
		if err := <-errCh; err != nil {
			fmt.Println("\n请求失败:", err)
			return
		}
		fmt.Println()
	}

}

// ShowReasoning 控制是否在终端打印模型的思考过程（reasoning_content）。
// 角色扮演时建议关闭，避免思考内容泄漏到对话；调试时置为 true 观察推理。
const ShowReasoning = true

// 把方法作为参数传递进去
func printDelta(d config.StreamDelta) (think string, content string) {
	if ShowReasoning && d.ReasoningContent != "" {
		fmt.Printf("%s%s%s", Blue, d.ReasoningContent, Reset)
		think = d.ReasoningContent
	}
	if d.Content != "" {
		fmt.Print(d.Content)
		content = d.Content
	}
	return think, content
}

func log(u test.User) string {
	return u.Username + "*******"
}

func log2(u test.User) string {
	return "新方法打印"
}
