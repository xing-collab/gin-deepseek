package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ContainsTimeIntent 判断文本是否涉及时间、日期、时段或时间问候。
// input 来源于用户消息；返回 true 时，调用方可以注入时间 Skill。
func ContainsTimeIntent(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	keywords := []string{
		"时间", "几点", "几号", "日期", "今天", "现在", "当前",
		"早上", "上午", "中午", "下午", "傍晚", "晚上", "晚安", "凌晨",
		"morning", "afternoon", "evening", "night", "time", "date",
		"good morning", "good afternoon", "good evening", "good night",
	}
	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return true
		}
	}
	return false
}

// JoinSkillPrompts 合并多个 Skill 正文，空文本会被忽略。
// prompts 来源于已加载的 Skill；返回值可直接追加到 system prompt。
func JoinSkillPrompts(prompts ...string) string {
	parts := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		if prompt = strings.TrimSpace(prompt); prompt != "" {
			parts = append(parts, prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Skill 保存一份可注入 Agent system prompt 的工作流说明。
// Name 和 Description 用于选择 Skill；Instructions 是实际注入模型的正文。
type Skill struct {
	// Name 来源：调用方设置、文件名或 Markdown 一级标题；意义：SkillRegistry 的唯一键。
	Name string
	// Description 来源：调用方配置；意义：供未来的 Skill 路由器判断适用场景。
	Description string
	// Instructions 来源：Skill Markdown 正文；意义：追加到模型 system prompt 的工作流规则。
	Instructions string
	// SourcePath 来源：LoadSkill 的 path 参数；意义：日志、审计和问题定位时标识原文件。
	SourcePath string
}

// LoadSkill 从 Markdown 文件加载 Skill。path 由应用配置或环境变量提供。
// 文件第一行若是“# 标题”，标题会作为默认 Name；正文保留为 Instructions。
func LoadSkill(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("读取 Skill %q: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	instructions := string(data)
	lines := strings.Split(instructions, "\n")
	if len(lines) > 0 {
		if title := strings.TrimSpace(lines[0]); strings.HasPrefix(title, "# ") {
			name = strings.TrimSpace(strings.TrimPrefix(title, "# "))
		}
	}
	return Skill{Name: name, Instructions: instructions, SourcePath: path}, nil
}

// SkillRegistry 保存可供 Agent 选择的 Skill。
// Skill 不负责执行工具，只负责向 system prompt 提供规则和工作流。
type SkillRegistry struct {
	mu     sync.RWMutex
	order  []string
	skills map[string]Skill
}

// NewSkillRegistry 创建空 Skill 注册表。
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{skills: make(map[string]Skill)}
}

// Register 将 Skill 加入注册表；Name 为空或重复时返回错误。
func (r *SkillRegistry) Register(skill Skill) error {
	if r == nil {
		return fmt.Errorf("skill registry is nil")
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return fmt.Errorf("skill name is empty")
	}
	if strings.TrimSpace(skill.Instructions) == "" {
		return fmt.Errorf("skill %q instructions are empty", name)
	}
	skill.Name = name
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skills == nil {
		r.skills = make(map[string]Skill)
	}
	if _, exists := r.skills[name]; exists {
		return fmt.Errorf("skill is already registered: %s", name)
	}
	r.skills[name] = skill
	r.order = append(r.order, name)
	return nil
}

// Load 从 Markdown 文件读取并注册 Skill。
func (r *SkillRegistry) Load(path string) error {
	skill, err := LoadSkill(path)
	if err != nil {
		return err
	}
	return r.Register(skill)
}

// Get 按名称获取 Skill；返回值是副本。
func (r *SkillRegistry) Get(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[name]
	return skill, ok
}

// Prompt 返回指定 Skill 的注入文本。name 来源于应用的 Skill 选择逻辑。
// 调用方应把结果拼接到 ChatAgentModel.SystemPrompt，而不是放入用户消息。
func (r *SkillRegistry) Prompt(name string) (string, error) {
	skill, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return skill.Instructions, nil
}

// Names 返回按注册顺序排列的 Skill 名称。
func (r *SkillRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}
