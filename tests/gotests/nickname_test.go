// Package tests 昵称生成器核心功能测试
//
// 覆盖：随机昵称格式、非空、多样性
package tests

import (
	"strings"
	"testing"

	"xgames/internal/platform/nickname"
)

// ─── 基本功能 ────────────────────────────────────────────

func TestGenerate_NonEmpty(t *testing.T) {
	name := nickname.Generate()
	if name == "" {
		t.Fatal("生成的昵称不应为空")
	}
}

func TestGenerate_Format(t *testing.T) {
	// 昵称应为 "形容词 + 名词" 格式，包含一个 "的" 字分隔
	for i := 0; i < 20; i++ {
		name := nickname.Generate()
		if !strings.Contains(name, "的") {
			t.Errorf("昵称 %q 应包含 '的' 字（形容词 + 名词格式）", name)
		}
		// "的" 不应在首尾
		if strings.HasPrefix(name, "的") || strings.HasSuffix(name, "的") {
			t.Errorf("昵称 %q 不应以 '的' 开头或结尾", name)
		}
	}
}

func TestGenerate_AdjectiveFromPool(t *testing.T) {
	adjectives := []string{
		"勇敢的", "聪明的", "快乐的", "神秘的", "酷炫的",
		"优雅的", "可爱的", "威武的", "沉稳的", "活泼的",
		"机智的", "潇洒的", "温柔的", "霸气的", "淡定的",
		"闪亮的", "迷人的", "傲娇的", "呆萌的", "高冷的",
	}
	valid := make(map[string]bool)
	for _, a := range adjectives {
		valid[a] = true
	}
	for i := 0; i < 50; i++ {
		name := nickname.Generate()
		parts := strings.SplitN(name, "的", 2)
		if len(parts) != 2 {
			t.Errorf("昵称 %q 格式异常", name)
			continue
		}
		adj := parts[0] + "的"
		if !valid[adj] {
			t.Errorf("形容词 %q 不在词库中", adj)
		}
	}
}

func TestGenerate_NounFromPool(t *testing.T) {
	nouns := []string{
		"小鸡", "熊猫", "老虎", "狮子", "猴子",
		"兔子", "狐狸", "海豚", "企鹅", "考拉",
		"柯基", "柴犬", "布偶", "龙猫", "仓鼠",
		"刺猬", "松鼠", "浣熊", "水獭", "羊驼",
	}
	valid := make(map[string]bool)
	for _, n := range nouns {
		valid[n] = true
	}
	for i := 0; i < 50; i++ {
		name := nickname.Generate()
		parts := strings.SplitN(name, "的", 2)
		if len(parts) != 2 {
			t.Errorf("昵称 %q 格式异常", name)
			continue
		}
		noun := parts[1]
		if !valid[noun] {
			t.Errorf("名词 %q 不在词库中", noun)
		}
	}
}

// ─── 多样性 ──────────────────────────────────────────────

func TestGenerate_Diversity(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := nickname.Generate()
		seen[name] = true
	}
	// 100 次生成应至少有 10 种不同昵称（词库 20×20=400 种组合）
	if len(seen) < 10 {
		t.Errorf("100 次仅生成 %d 种不同昵称，多样性不足", len(seen))
	}
}

func TestGenerate_NotAllSame(t *testing.T) {
	first := nickname.Generate()
	allSame := true
	for i := 0; i < 20; i++ {
		if nickname.Generate() != first {
			allSame = false
			break
		}
	}
	if allSame {
		t.Fatal("连续 21 次生成完全相同，随机性可能有问题")
	}
}
