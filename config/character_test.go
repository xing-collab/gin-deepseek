package config

import (
	"strings"
	"testing"
)

func TestLoadCharacter(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}
	s := c.State()
	if s.Base != "normal" {
		t.Errorf("初始 Base 应为 normal，得到 %s", s.Base)
	}
	if s.Relationship != "neutral" {
		t.Errorf("初始 Relationship 应为 neutral，得到 %s", s.Relationship)
	}
	if s.Attachment != 50 {
		t.Errorf("初始 Attachment 应为 50，得到 %d", s.Attachment)
	}
	if s.Obsession != 0 {
		t.Errorf("初始 Obsession 应为 0，得到 %d", s.Obsession)
	}
	if s.RationalIntegrity != 100 {
		t.Errorf("初始 RationalIntegrity 应为 100，得到 %d", s.RationalIntegrity)
	}
	if s.Event != "none" {
		t.Errorf("初始 Event 应为 none，得到 %s", s.Event)
	}
	if s.Expression != "none" {
		t.Errorf("初始 Expression 应为 none，得到 %s", s.Expression)
	}
}

func TestUpdateBase(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("源石计划进展如何")
	if c.State().Base != "architect" {
		t.Errorf("「源石计划」应触发 Base=architect，得到 %s", c.State().Base)
	}

	c.Update("今天天气不错")
	if c.State().Base != "normal" {
		t.Errorf("无关输入应回落 Base=normal，得到 %s", c.State().Base)
	}
}

func TestUpdateRelationship(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("我想你")
	if c.State().Relationship != "affection" {
		t.Errorf("「我想你」应触发 Relationship=affection，得到 %s", c.State().Relationship)
	}

	c.Update("今天天气不错")
	if c.State().Relationship != "neutral" {
		t.Errorf("无关输入应回落 Relationship=neutral，得到 %s", c.State().Relationship)
	}
}

func TestUpdateAttachment(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("我想你") // affection attachment +10
	if c.State().Attachment != 60 {
		t.Errorf("「我想你」后 Attachment 应为 60，得到 %d", c.State().Attachment)
	}
	if c.State().Obsession != 0 {
		t.Errorf("「我想你」不应提升 Obsession，得到 %d", c.State().Obsession)
	}
}

func TestUpdateObsession(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("我要离开你") // distance obsession +8
	if c.State().Obsession != 8 {
		t.Errorf("「我要离开你」后 Obsession 应为 8，得到 %d", c.State().Obsession)
	}

	c.Update("我不记得你了") // memory_loss obsession +15，rationality -10
	if c.State().Obsession != 23 {
		t.Errorf("「我不记得你了」后 Obsession 应为 23，得到 %d", c.State().Obsession)
	}
	if c.State().RationalIntegrity != 90 {
		t.Errorf("「我不记得你了」后 RationalIntegrity 应为 90，得到 %d", c.State().RationalIntegrity)
	}

	c.Update("你正在死") // doctor_death obsession +50，rationality -15
	if c.State().Obsession != 73 {
		t.Errorf("「你正在死」后 Obsession 应为 73，得到 %d", c.State().Obsession)
	}
	if c.State().RationalIntegrity != 75 {
		t.Errorf("「你正在死」后 RationalIntegrity 应为 75，得到 %d", c.State().RationalIntegrity)
	}
}

func TestUpdateObsessionClamp(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		c.Update("你正在死") // 每次 +50，应 clamp 到 100
	}
	if c.State().Obsession != 100 {
		t.Errorf("Obsession 应 clamp 到 100，得到 %d", c.State().Obsession)
	}
}

func TestUpdateCrisis(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("这里有危险")
	if c.State().Event != "crisis" {
		t.Errorf("「危险」应触发 Event=crisis，得到 %s", c.State().Event)
	}
}

func TestUpdateGlitch(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	// glitch 由「执念极高 + 理性受损」组合判定，需多次累积
	// 第 1 次「你正在死」：obsession=50, rationality=85 → 不 glitch
	c.Update("你正在死")
	if c.State().Expression != "none" {
		t.Errorf("单次危机不应触发 glitch，得到 %s", c.State().Expression)
	}

	// 再 3 次：obsession=100, rationality=40 → glitch
	for i := 0; i < 3; i++ {
		c.Update("你正在死")
	}
	if c.State().Expression != "glitch" {
		t.Errorf("高执念 + 理性受损应触发 glitch，得到 %s（obsession=%d, rationality=%d）",
			c.State().Expression, c.State().Obsession, c.State().RationalIntegrity)
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}
	c.Update("源石")

	p := c.BuildSystemPrompt()
	for _, want := range []string{
		"普瑞赛斯",
		"architect",
		"核心悖论",
		"价值优先级",
		"决策原则",
		"理性规则",
		"执念规则",
		"核心原则",
		"情感连接",
		"执念强度",
		"理性完整性",
		"restrained",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt 应包含 %q，实际:\n%s", want, p)
		}
	}
}
