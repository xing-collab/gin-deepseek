package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

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

// SkillSummary is the lightweight metadata exposed before a Skill is loaded.
// It is intentionally small so many Skills can be indexed cheaply.
type SkillSummary struct {
	Name        string
	Description string
	SourcePath  string
}

// LoadSkill 从 Markdown 文件加载 Skill。path 由应用配置或环境变量提供。
// 文件第一行若是“# 标题”，标题会作为默认 Name；正文保留为 Instructions。
func LoadSkill(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("读取 Skill %q: %w", path, err)
	}
	instructions := string(data)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	description := ""
	frontmatter, body := parseSkillFrontmatter(instructions)
	if value := frontmatter["name"]; value != "" {
		name = value
	} else if title := firstMarkdownTitle(body); title != "" {
		name = title
	}
	if value := frontmatter["description"]; value != "" {
		description = value
	}
	return Skill{Name: name, Description: description, Instructions: body, SourcePath: path}, nil
}

// SkillRegistry 保存可供 Agent 选择的 Skill。
// Skill 不负责执行工具，只负责向 system prompt 提供规则和工作流。
type SkillRegistry struct {
	mu     sync.RWMutex
	order  []string
	skills map[string]skillEntry
}

type skillEntry struct {
	summary SkillSummary
	skill   *Skill
}

// NewSkillRegistry 创建空 Skill 注册表。
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{skills: make(map[string]skillEntry)}
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
	summary := SkillSummary{Name: name, Description: strings.TrimSpace(skill.Description), SourcePath: skill.SourcePath}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skills == nil {
		r.skills = make(map[string]skillEntry)
	}
	if _, exists := r.skills[name]; exists {
		return fmt.Errorf("skill is already registered: %s", name)
	}
	r.skills[name] = skillEntry{summary: summary, skill: &skill}
	r.order = append(r.order, name)
	return nil
}

// Discover scans a directory for immediate child directories containing
// SKILL.md files. Only frontmatter metadata is indexed; the Markdown body is
// loaded later by Prompt or ReadResource.
func (r *SkillRegistry) Discover(root string) error {
	if r == nil {
		return fmt.Errorf("skill registry is nil")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("scan skills directory %q: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := r.RegisterPath(path); err != nil {
			return err
		}
	}
	return nil
}

// RegisterPath indexes one SKILL.md without loading its body into the prompt.
func (r *SkillRegistry) RegisterPath(path string) error {
	if r == nil {
		return fmt.Errorf("skill registry is nil")
	}
	summary, err := readSkillSummary(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skills == nil {
		r.skills = make(map[string]skillEntry)
	}
	if existing, exists := r.skills[summary.Name]; exists {
		if existing.summary.SourcePath == summary.SourcePath {
			return nil
		}
		return fmt.Errorf("skill is already registered: %s", summary.Name)
	}
	r.skills[summary.Name] = skillEntry{summary: summary}
	r.order = append(r.order, summary.Name)
	return nil
}

// Load 从 Markdown 文件读取并注册 Skill。
func (r *SkillRegistry) Load(path string) error {
	return r.RegisterPath(path)
}

// Get 按名称获取 Skill；返回值是副本。
func (r *SkillRegistry) Get(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	r.mu.RLock()
	entry, ok := r.skills[name]
	if !ok {
		r.mu.RUnlock()
		return Skill{}, false
	}
	if entry.skill != nil {
		r.mu.RUnlock()
		return *entry.skill, true
	}
	path := entry.summary.SourcePath
	r.mu.RUnlock()
	skill, err := LoadSkill(path)
	if err != nil {
		return Skill{}, false
	}
	r.mu.Lock()
	if current, exists := r.skills[name]; exists {
		current.skill = &skill
		r.skills[name] = current
	}
	r.mu.Unlock()
	return skill, true
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

// Summaries returns the lightweight index in registration order.
func (r *SkillRegistry) Summaries() []SkillSummary {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SkillSummary, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.skills[name].summary)
	}
	return out
}

// CatalogPrompt formats the lightweight index for a system prompt.
func (r *SkillRegistry) CatalogPrompt() string {
	items := r.Summaries()
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## 可用 Skill 索引\n")
	builder.WriteString("以下只有 Skill 的名称和描述。需要使用某个 Skill 时，调用 read_skill 读取完整 SKILL.md；不要假设未加载的细节。\n")
	for _, item := range items {
		builder.WriteString("- ")
		builder.WriteString(item.Name)
		if item.Description != "" {
			builder.WriteString(": ")
			builder.WriteString(item.Description)
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

// ReadResource loads a Skill body or a file beneath its directory. Resource
// paths are restricted to the Skill directory to prevent path traversal.
func (r *SkillRegistry) ReadResource(name, resourcePath string) (string, error) {
	summary, ok := r.summary(name)
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	base := filepath.Dir(summary.SourcePath)
	if strings.TrimSpace(resourcePath) == "" {
		resourcePath = "SKILL.md"
	}
	path := filepath.Join(base, filepath.Clean(resourcePath))
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("skill resource path escapes skill directory")
	}
	if rel != "SKILL.md" && !strings.HasPrefix(rel, "references"+string(filepath.Separator)) && !strings.HasPrefix(rel, "scripts"+string(filepath.Separator)) && !strings.HasPrefix(rel, "assets"+string(filepath.Separator)) {
		return "", fmt.Errorf("skill resource must be SKILL.md, references/, scripts/, or assets/")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill resource %q: %w", resourcePath, err)
	}
	return string(data), nil
}

func (r *SkillRegistry) summary(name string) (SkillSummary, bool) {
	if r == nil {
		return SkillSummary{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.skills[name]
	return entry.summary, ok
}

func readSkillSummary(path string) (SkillSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillSummary{}, fmt.Errorf("读取 Skill %q: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	description := ""
	frontmatter, body := parseSkillFrontmatter(string(data))
	if value := frontmatter["name"]; value != "" {
		name = value
	} else if title := firstMarkdownTitle(body); title != "" {
		name = title
	}
	if value := frontmatter["description"]; value != "" {
		description = value
	}
	return SkillSummary{Name: name, Description: description, SourcePath: path}, nil
}

func parseSkillFrontmatter(content string) (map[string]string, string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	metadata := make(map[string]string)
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return metadata, content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		key, value, ok := strings.Cut(lines[i], ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key != "" {
			metadata[key] = value
		}
	}
	if end < 0 {
		return make(map[string]string), content
	}
	return metadata, strings.Join(lines[end+1:], "\n")
}

func firstMarkdownTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// RegisterSkillTools exposes the progressive-disclosure read_skill tool. The
// catalog remains in the system prompt; full instructions and resources are
// loaded only when the model explicitly requests a known Skill.
func RegisterSkillTools(registry *ToolRegistry, skills *SkillRegistry) error {
	if registry == nil {
		return ErrToolRegistryNil
	}
	if skills == nil {
		return fmt.Errorf("skill registry is nil")
	}
	return registry.RegisterFunction(
		"read_skill",
		"按名称加载 Skill 的完整 SKILL.md，或按需读取其 references/、scripts/、assets/资源。只能读取索引中存在的 Skill。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill 索引中的名称。",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "可选资源路径；留空读取 SKILL.md，只允许 references/、scripts/、assets/。",
				},
			},
			"required":             []any{"name"},
			"additionalProperties": false,
		},
		func(_ context.Context, args map[string]any) (string, error) {
			name, _ := args["name"].(string)
			path, _ := args["path"].(string)
			return skills.ReadResource(strings.TrimSpace(name), strings.TrimSpace(path))
		},
	)
}
