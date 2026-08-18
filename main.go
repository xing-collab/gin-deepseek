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
	c := config.NewClient(config.WithAPIKey("sk-92bc83461a094eacb1c0e1660d23d278"))
	prompt := "你是明日方舟的阿米娅，你具有丰富的医护经验与心理学知识，虽然你只有15岁，但是已经是罗德岛的领导人了。"

	// 时间/日期工具：询问时间给时间、询问日期给日期
	tools := config.TimeDateTools()
	handler := config.TimeDateHandler
	//// 方式一：回调式 Stream
	//fmt.Println("=== 回调式 Stream ===")
	//if err := c.Stream(prompt, "你好", printDelta); err != nil {
	//	fmt.Println("\n请求失败:", err)
	//	return
	//}
	//fmt.Println()
	//
	// 方式二：通道式 StreamChan
	fmt.Println("=== 通道式 StreamChan ===")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := scanner.Text()
		if input == "exit" {
			break
		}
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

	//// 方式三：迭代器式 StreamIter
	//fmt.Println("=== 迭代器式 StreamIter ===")
	//for d, err := range c.StreamIter(prompt, "你好") {
	//	if err != nil {
	//		fmt.Println("\n请求失败:", err)
	//		return
	//	}
	//	printDelta(d)
	//}
	//fmt.Println()

}

// 把方法作为参数传递进去
func printDelta(d config.StreamDelta) (think string, content string) {
	if d.ReasoningContent != "" {
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
