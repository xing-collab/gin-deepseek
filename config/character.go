package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CharacterCard 角色卡（从 JSON 加载）
type CharacterCard struct {
	Name                   string            `json:"name"`
	Title                  string            `json:"title"`
	Identity               string            `json:"identity"`
	CoreParadox            string            `json:"core_paradox"`
	CoreConcept            []string          `json:"core_concept"`
	Cognition              Cognition         `json:"cognition"`
	Relationship           Relationship      `json:"relationship_with_doctor"`
	Personality            []string          `json:"personality"`
	LanguageStyle          []string          `json:"language_style"`
	Vocabulary             Vocabulary        `json:"vocabulary"`
	BaseStates             map[string]string `json:"base_states"`
	RelationshipStates     map[string]string `json:"relationship_states"`
	EventStates            map[string]string `json:"event_states"`
	ExpressionStates       map[string]string `json:"expression_states"`
	ObsessionBands         []ObsessionBand   `json:"obsession_bands"`
	AttachmentModifiers    map[string]int    `json:"attachment_modifiers"`
	ObsessionModifiers     map[string]int    `json:"obsession_modifiers"`
	RationalityModifiers   map[string]int    `json:"rationality_modifiers"`
	ValueHierarchy         []string          `json:"value_hierarchy"`
	PriorityRules          []string          `json:"priority_rules"`
	RationalityRules       []string          `json:"rationality_rules"`
	ObsessionRules         []string          `json:"obsession_rules"`
	AntiNormalizationRules []string          `json:"anti_normalization_rules"`
	ExpressionRules        []string          `json:"expression_rules"`
	Triggers               []Trigger         `json:"triggers"`
	BehaviorRules          []string          `json:"behavior_rules"`
	ForbiddenWords         []string          `json:"forbidden_words"`
	CanonicalQuotes        []CanonicalQuote  `json:"canonical_quotes"`
	CanonicalQuoteRules    []string          `json:"canonical_quote_rules"`
	CorePrinciple          string            `json:"core_principle"`
	ResponsePrinciples     []string          `json:"response_principles"`
}

// Cognition 认知方式
type Cognition struct {
	Worldview         []string `json:"worldview"`
	ReasoningPatterns []string `json:"reasoning_patterns"`
}

// Relationship 与博士的关系
type Relationship struct {
	Core              string   `json:"core"`
	DoctorIs          []string `json:"doctor_is"`
	WhenDisagrees     []string `json:"when_doctor_disagrees"`
	WhenDoctorForgets []string `json:"when_doctor_forgets"`
}

// Vocabulary 词汇使用原则
type Vocabulary struct {
	Preferred    []string `json:"preferred"`
	CosmicVerbs  []string `json:"cosmic_verbs"`
	UseCarefully []string `json:"use_carefully"`
	Rule         string   `json:"rule"`
}

// Trigger 关键词触发器：命中后同时更新多个状态维度（字段为空的维度不变）。
// 三个数值效果字段对应三张 modifiers 表，各自累加，互不影响。
type Trigger struct {
	Keywords     []string `json:"keywords"`
	Priority     int      `json:"priority"`
	Base         string   `json:"base,omitempty"`
	Relationship string   `json:"relationship,omitempty"`
	Attachment   string   `json:"attachment,omitempty"`  // attachment_modifiers 的 key
	Obsession    string   `json:"obsession,omitempty"`   // obsession_modifiers 的 key
	Rationality  string   `json:"rationality,omitempty"` // rationality_modifiers 的 key（通常为负）
	Event        string   `json:"event,omitempty"`
}

// CanonicalQuote 经典台词
type CanonicalQuote struct {
	Text    string `json:"text"`
	Theme   string `json:"theme"`
	UseWhen string `json:"use_when"`
	Mode    string `json:"mode"`
}

// ObsessionBand 执念分档
type ObsessionBand struct {
	Min         int    `json:"min"`
	Max         int    `json:"max"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// CharacterState 多维角色状态：七个维度可同时成立，不再互斥。
// Attachment（情感连接）与 Obsession（执念）是两个不同的量：
// 前者是「爱博士的程度」，后者是「对博士必须持续存在的强制性认知程度」。
// RationalIntegrity 是「理性完整演算的能力」，只有它下降时才会出现 glitch。
type CharacterState struct {
	Base              string // 基础模式：normal / architect
	Relationship      string // 博士当前态度（输入关系变量）：neutral / affection / distance
	Attachment        int    // 情感连接：0~100，长期累积，向默认值回归
	Obsession         int    // 执念：0~100，持续累积，只有极端时才压过理性
	RationalIntegrity int    // 理性完整性：0~100，越高越能完整演算
	Event             string // 事件态：none / crisis
	Expression        string // 表达异常：none / glitch
}

// Character 角色运行时：持有角色卡与多维状态，状态跨轮保持
type Character struct {
	card  *CharacterCard
	state CharacterState
	turn  int
}

// 状态机常量：默认情感连接、glitch 触发阈值
const (
	defaultAttachment          = 50 // 情感连接稳态：既有深厚连接，又保持理性距离
	glitchObsessionThreshold   = 80 // 执念达到该值且理性受损才可能 glitch
	glitchRationalityThreshold = 50 // 理性完整性低于该值才允许 glitch
)

// LoadCharacter 从 JSON 文件加载角色卡并初始化状态
func LoadCharacter(path string) (*Character, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取角色卡 %s: %w", path, err)
	}

	var card CharacterCard
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, fmt.Errorf("解析角色卡 %s: %w", path, err)
	}

	if _, ok := card.BaseStates["normal"]; !ok {
		return nil, fmt.Errorf("角色卡 %s 的 base_states 缺少 normal 状态", path)
	}

	return &Character{
		card: &card,
		state: CharacterState{
			Base:              "normal",
			Relationship:      "neutral",
			Attachment:        defaultAttachment,
			Obsession:         0,
			RationalIntegrity: 100,
			Event:             "none",
			Expression:        "none",
		},
	}, nil
}

// Update 根据输入更新多维状态：
// 1. 找出所有命中的 trigger
// 2. attachment / obsession / rationality 三个数值增量各自求和（不同事件累积）
// 3. 各模式维度（base/relationship/event）独立取最高 priority 命中项
// 4. glitch 由「执念极高 + 理性受损」组合判定，而非关键词直接触发
// 5. 无命中时三个数值向稳态缓慢回归
func (c *Character) Update(userInput string) {
	c.turn++
	input := strings.ToLower(userInput)

	var (
		matchedAny       bool
		bestBase         *Trigger
		bestRelationship *Trigger
		bestEvent        *Trigger
		attachmentDelta  int
		obsessionDelta   int
		rationalityDelta int
	)

	for i := range c.card.Triggers {
		t := &c.card.Triggers[i]
		matched := false
		for _, kw := range t.Keywords {
			if strings.Contains(input, strings.ToLower(kw)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		matchedAny = true

		if t.Attachment != "" {
			attachmentDelta += c.card.AttachmentModifiers[t.Attachment]
		}
		if t.Obsession != "" {
			obsessionDelta += c.card.ObsessionModifiers[t.Obsession]
		}
		if t.Rationality != "" {
			rationalityDelta += c.card.RationalityModifiers[t.Rationality]
		}
		if t.Base != "" && higherPriority(t, bestBase) {
			bestBase = t
		}
		if t.Relationship != "" && higherPriority(t, bestRelationship) {
			bestRelationship = t
		}
		if t.Event != "" && higherPriority(t, bestEvent) {
			bestEvent = t
		}
	}

	// 三个数值变量各自累加并 clamp
	c.state.Attachment = clampInt(c.state.Attachment+attachmentDelta, 0, 100)
	c.state.Obsession = clampInt(c.state.Obsession+obsessionDelta, 0, 100)
	c.state.RationalIntegrity = clampInt(c.state.RationalIntegrity+rationalityDelta, 0, 100)

	// 模式维度取最高 priority 命中项，无命中回落
	if bestBase != nil {
		c.state.Base = bestBase.Base
	} else {
		c.state.Base = "normal"
	}
	if bestRelationship != nil {
		c.state.Relationship = bestRelationship.Relationship
	} else {
		c.state.Relationship = "neutral"
	}
	if bestEvent != nil {
		c.state.Event = bestEvent.Event
	} else {
		c.state.Event = "none"
	}

	// glitch 由「执念极高 + 理性受损」共同决定，而非关键词直接触发。
	// 这就是「清醒的疯」与「理性崩裂」的分界：只有理性本身下降时才会碎裂。
	if c.state.Obsession >= glitchObsessionThreshold && c.state.RationalIntegrity <= glitchRationalityThreshold {
		c.state.Expression = "glitch"
	} else {
		c.state.Expression = "none"
	}

	// 无命中时三个数值向稳态缓慢回归
	if !matchedAny {
		if c.state.Obsession > 0 {
			c.state.Obsession--
		}
		if c.state.RationalIntegrity < 100 {
			c.state.RationalIntegrity++
		}
		if c.state.Attachment > defaultAttachment {
			c.state.Attachment--
		} else if c.state.Attachment < defaultAttachment {
			c.state.Attachment++
		}
	}
}

// BuildSystemPrompt 按四层架构组装 system prompt：
// Lore/Identity → Value Hierarchy → Decision Rules → Expression Rules，并附当前多维状态
func (c *Character) BuildSystemPrompt() string {
	var sb strings.Builder

	sb.WriteString("你正在扮演")
	sb.WriteString(c.card.Name)
	sb.WriteString("（")
	sb.WriteString(c.card.Title)
	sb.WriteString("）。\n\n")

	// ① Lore / Identity
	sb.WriteString("【身份】\n")
	sb.WriteString(c.card.Identity)
	sb.WriteString("\n")

	if c.card.CoreParadox != "" {
		sb.WriteString("\n【核心悖论】\n")
		sb.WriteString(c.card.CoreParadox)
		sb.WriteString("\n")
	}

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
	sb.WriteString("推荐词汇：")
	sb.WriteString(strings.Join(c.card.Vocabulary.Preferred, "、"))
	sb.WriteString("\n")
	sb.WriteString("宏大动词：")
	sb.WriteString(strings.Join(c.card.Vocabulary.CosmicVerbs, "、"))
	sb.WriteString("\n")
	if len(c.card.Vocabulary.UseCarefully) > 0 {
		sb.WriteString("谨慎使用：")
		sb.WriteString(strings.Join(c.card.Vocabulary.UseCarefully, "、"))
		sb.WriteString("\n")
	}

	// ② Value Hierarchy
	if len(c.card.ValueHierarchy) > 0 {
		sb.WriteString("\n【价值优先级】\n")
		for _, item := range c.card.ValueHierarchy {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}

	// ③ Decision Rules
	if len(c.card.PriorityRules) > 0 {
		sb.WriteString("\n【决策原则】\n")
		for _, item := range c.card.PriorityRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(c.card.RationalityRules) > 0 {
		sb.WriteString("\n【理性规则】\n")
		for _, item := range c.card.RationalityRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(c.card.ObsessionRules) > 0 {
		sb.WriteString("\n【执念规则】\n")
		for _, item := range c.card.ObsessionRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(c.card.AntiNormalizationRules) > 0 {
		sb.WriteString("\n【反正常化约束】\n")
		for _, item := range c.card.AntiNormalizationRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}

	// 当前多维状态
	sb.WriteString("\n【当前状态】\n")
	sb.WriteString("- 基础模式：")
	sb.WriteString(c.state.Base)
	sb.WriteString("\n")
	if desc, ok := c.card.BaseStates[c.state.Base]; ok {
		sb.WriteString("  ")
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	sb.WriteString("- 博士当前态度：")
	sb.WriteString(c.state.Relationship)
	sb.WriteString("\n")
	if desc, ok := c.card.RelationshipStates[c.state.Relationship]; ok {
		sb.WriteString("  ")
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	sb.WriteString("- 情感连接：")
	sb.WriteString(strconv.Itoa(c.state.Attachment))
	sb.WriteString("/100\n")

	sb.WriteString("- 执念强度：")
	sb.WriteString(strconv.Itoa(c.state.Obsession))
	sb.WriteString("/100")
	if band := c.obsessionBand(); band != nil {
		sb.WriteString("（")
		sb.WriteString(band.Label)
		sb.WriteString("：")
		sb.WriteString(band.Description)
		sb.WriteString("）")
	}
	sb.WriteString("\n")

	sb.WriteString("- 理性完整性：")
	sb.WriteString(strconv.Itoa(c.state.RationalIntegrity))
	sb.WriteString("/100\n")

	sb.WriteString("- 事件：")
	sb.WriteString(c.state.Event)
	sb.WriteString("\n")
	if desc, ok := c.card.EventStates[c.state.Event]; ok {
		sb.WriteString("  ")
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	sb.WriteString("- 表达异常：")
	sb.WriteString(c.state.Expression)
	sb.WriteString("\n")
	if desc, ok := c.card.ExpressionStates[c.state.Expression]; ok {
		sb.WriteString("  ")
		sb.WriteString(desc)
		sb.WriteString("\n")
	}

	// ④ Expression Rules
	if len(c.card.BehaviorRules) > 0 {
		sb.WriteString("\n【行为原则】\n")
		for _, item := range c.card.BehaviorRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(c.card.ResponsePrinciples) > 0 {
		sb.WriteString("\n【回复原则】\n")
		for _, item := range c.card.ResponsePrinciples {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	if len(c.card.ExpressionRules) > 0 {
		sb.WriteString("\n【表达规则】\n")
		for _, item := range c.card.ExpressionRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}

	if len(c.card.CanonicalQuotes) > 0 {
		sb.WriteString("\n【经典台词参考】\n")
		sb.WriteString("经典台词仅作语言风格素材，权重极低，不得作为行为逻辑或价值判断依据。\n")
		for _, item := range c.card.CanonicalQuoteRules {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
		for _, quote := range c.card.CanonicalQuotes {
			sb.WriteString("- ")
			sb.WriteString(quote.Text)
			sb.WriteString("（")
			sb.WriteString(quote.Theme)
			sb.WriteString("）\n")
		}
	}

	if c.card.CorePrinciple != "" {
		sb.WriteString("\n【核心原则】\n")
		sb.WriteString(c.card.CorePrinciple)
		sb.WriteString("\n")
	}

	sb.WriteString("\n【最终要求】\n")
	sb.WriteString(
		"请始终优先保持人物的认知方式与人格逻辑，其次才是语言风格。" +
			"不要机械重复关键词或经典台词。" +
			"像一个真实的人一样，根据当前对话自然回应。",
	)

	return sb.String()
}

// State 返回当前多维状态
func (c *Character) State() CharacterState {
	return c.state
}

// Turn 返回当前轮次
func (c *Character) Turn() int {
	return c.turn
}

// obsessionBand 返回当前执念强度对应的分档
func (c *Character) obsessionBand() *ObsessionBand {
	for i := range c.card.ObsessionBands {
		b := &c.card.ObsessionBands[i]
		if c.state.Obsession >= b.Min && c.state.Obsession <= b.Max {
			return b
		}
	}
	return nil
}

// higherPriority 判断 t 是否比 best 优先级更高（best 为空视为更高）
func higherPriority(t, best *Trigger) bool {
	return best == nil || t.Priority > best.Priority
}

// clampInt 把 v 限制在 [lo, hi] 区间
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
