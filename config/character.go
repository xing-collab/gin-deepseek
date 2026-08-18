package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type CharacterCard struct {
	Name               string            `json:"name"`
	Title              string            `json:"title"`
	Identity           string            `json:"identity"`
	CoreConcept        []string          `json:"core_concept"`
	Cognition          Cognition         `json:"cognition"`
	Relationship       Relationship      `json:"relationship_with_doctor"`
	Personality        []string          `json:"personality"`
	LanguageStyle      []string          `json:"language_style"`
	Vocabulary         Vocabulary        `json:"vocabulary"`
	States             map[string]string `json:"states"`
	StatePriority      map[string]int    `json:"state_priority"`
	Triggers           []Trigger         `json:"triggers"`
	BehaviorRules      []string          `json:"behavior_rules"`
	ForbiddenWords     []string          `json:"forbidden_words"`
	CanonicalQuotes    []CanonicalQuote  `json:"canonical_quotes"`
	ResponsePrinciples []string          `json:"response_principles"`
}

type Cognition struct {
	Worldview         []string `json:"worldview"`
	ReasoningPatterns []string `json:"reasoning_patterns"`
}

type Relationship struct {
	Core              string   `json:"core"`
	DoctorIs          []string `json:"doctor_is"`
	WhenDisagrees     []string `json:"when_doctor_disagrees"`
	WhenDoctorForgets []string `json:"when_doctor_forgets"`
}

type Vocabulary struct {
	Preferred    []string `json:"preferred"`
	CosmicVerbs  []string `json:"cosmic_verbs"`
	UseCarefully []string `json:"use_carefully"`
	Rule         string   `json:"rule"`
}

type Trigger struct {
	Keywords []string `json:"keywords"`
	State    string   `json:"state"`
	Priority int      `json:"priority"`
}

type CanonicalQuote struct {
	Text    string `json:"text"`
	Theme   string `json:"theme"`
	UseWhen string `json:"use_when"`
	Mode    string `json:"mode"`
}

type Character struct {
	card *CharacterCard
	mode string
	turn int

	// 状态连续性
	stateAge int
}

func LoadCharacter(path string) (*Character, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取角色卡 %s: %w", path, err)
	}

	var card CharacterCard

	if err := json.Unmarshal(data, &card); err != nil {
		return nil, fmt.Errorf("解析角色卡 %s: %w", path, err)
	}

	if _, ok := card.States["normal"]; !ok {
		return nil, fmt.Errorf(
			"角色卡 %s 的 states 缺少 normal 状态",
			path,
		)
	}

	return &Character{
		card: &card,
		mode: "normal",
	}, nil
}

// Update 根据输入更新角色状态。
// 不再是“没有关键词 -> normal”，而是：
// 1. 找出所有命中的 trigger
// 2. 按 priority 选择最高优先级状态
// 3. 如果没有触发，则维持当前状态一段时间
func (c *Character) Update(userInput string) {
	c.turn++

	input := strings.ToLower(userInput)

	var matched *Trigger

	for i := range c.card.Triggers {
		trigger := &c.card.Triggers[i]

		for _, keyword := range trigger.Keywords {
			if strings.Contains(input, strings.ToLower(keyword)) {

				if matched == nil ||
					trigger.Priority > matched.Priority {

					matched = trigger
				}

				break
			}
		}
	}

	if matched != nil {
		c.mode = matched.State
		c.stateAge = 0
		return
	}

	// 没有新的触发器时，不立即恢复 normal
	c.stateAge++

	// 状态自然衰减
	switch c.mode {
	case "glitch":
		if c.stateAge >= 2 {
			c.mode = "normal"
		}

	case "crisis":
		if c.stateAge >= 3 {
			c.mode = "normal"
		}

	case "distance":
		if c.stateAge >= 3 {
			c.mode = "normal"
		}

	case "affection":
		if c.stateAge >= 4 {
			c.mode = "normal"
		}

	case "architect":
		if c.stateAge >= 4 {
			c.mode = "normal"
		}
	}
}

func (c *Character) BuildSystemPrompt() string {
	var sb strings.Builder

	sb.WriteString("你正在扮演")
	sb.WriteString(c.card.Name)
	sb.WriteString("（")
	sb.WriteString(c.card.Title)
	sb.WriteString("）。\n\n")

	sb.WriteString("【身份】\n")
	sb.WriteString(c.card.Identity)
	sb.WriteString("\n")

	sb.WriteString("\n【核心人格】\n")
	for _, item := range c.card.CoreConcept {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【认知方式】\n")

	for _, item := range c.card.Cognition.Worldview {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	for _, item := range c.card.Cognition.ReasoningPatterns {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【你与博士的关系】\n")
	sb.WriteString(c.card.Relationship.Core)
	sb.WriteString("\n")

	sb.WriteString("\n博士对你而言是：\n")
	for _, item := range c.card.Relationship.DoctorIs {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n当博士与你意见不同时：\n")
	for _, item := range c.card.Relationship.WhenDisagrees {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n当博士表现出记忆缺失时：\n")
	for _, item := range c.card.Relationship.WhenDoctorForgets {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【人格】\n")
	for _, item := range c.card.Personality {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【语言风格】\n")
	for _, item := range c.card.LanguageStyle {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【词汇使用原则】\n")
	sb.WriteString(c.card.Vocabulary.Rule)
	sb.WriteString("\n")

	sb.WriteString("\n推荐词汇：")
	sb.WriteString(strings.Join(c.card.Vocabulary.Preferred, "、"))
	sb.WriteString("\n")

	sb.WriteString("宏大动词：")
	sb.WriteString(strings.Join(c.card.Vocabulary.CosmicVerbs, "、"))
	sb.WriteString("\n")

	sb.WriteString("\n【当前状态】\n")
	sb.WriteString(c.mode)
	sb.WriteString("\n")

	if desc, ok := c.card.States[c.mode]; ok {
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【行为原则】\n")
	for _, item := range c.card.BehaviorRules {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【回复原则】\n")
	for _, item := range c.card.ResponsePrinciples {
		sb.WriteString("- ")
		sb.WriteString(item)
		sb.WriteString("\n")
	}

	if len(c.card.CanonicalQuotes) > 0 {
		sb.WriteString("\n【经典台词参考】\n")
		sb.WriteString("以下内容只能在语义相关时参考，不要机械复制，不要为了像角色而强行引用。\n")

		for _, quote := range c.card.CanonicalQuotes {
			sb.WriteString("- ")
			sb.WriteString(quote.Text)
			sb.WriteString("（")
			sb.WriteString(quote.Theme)
			sb.WriteString("）\n")
		}
	}

	sb.WriteString("\n【最终要求】\n")
	sb.WriteString(
		"请始终优先保持人物的认知方式与人格逻辑，其次才是语言风格。" +
			"不要机械重复关键词或经典台词。" +
			"像一个真实的人一样，根据当前对话自然回应。",
	)

	return sb.String()
}

func (c *Character) Mode() string {
	return c.mode
}

func (c *Character) Turn() int {
	return c.turn
}
