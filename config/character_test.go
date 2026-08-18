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
	if c.Mode() != "normal" {
		t.Fatalf("初始状态应为 normal，得到 %s", c.Mode())
	}
}

func TestUpdateTriggersState(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}

	c.Update("源石计划进展如何")
	if c.Mode() != "architect" {
		t.Errorf("「源石计划」应触发 architect，得到 %s", c.Mode())
	}

	c.Update("我想你")
	if c.Mode() != "obsessive" {
		t.Errorf("「我想你」应触发 obsessive，得到 %s", c.Mode())
	}

	c.Update("今天天气不错")
	if c.Mode() != "normal" {
		t.Errorf("无关输入应回落 normal，得到 %s", c.Mode())
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	c, err := LoadCharacter("priestess.json")
	if err != nil {
		t.Fatalf("加载角色卡失败: %v", err)
	}
	c.Update("源石")

	p := c.BuildSystemPrompt()
	if !strings.Contains(p, "普瑞赛斯") {
		t.Errorf("prompt 应包含角色名，实际:\n%s", p)
	}
	if !strings.Contains(p, "architect") {
		t.Errorf("prompt 应包含当前状态 architect，实际:\n%s", p)
	}
}
