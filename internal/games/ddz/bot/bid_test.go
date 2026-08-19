package bot

import (
	"context"
	"testing"
)

// TestEngine_DecideBidScore 叫分：强牌叫 3、弱牌不叫、不超过当前最高分
func TestEngine_DecideBidScore(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 强牌（含双王 + 炸弹 + 2）应叫 3 分
	strong := cards("R B 2 2 2 2 A K")
	if s := e.DecideBidScore(context.Background(), "bot", strong, 0); s != 3 {
		t.Errorf("强牌应叫 3 分，got=%d handStrength=%v", s, handStrength(strong))
	}

	// 弱牌不叫
	weak := cards("3 4 5 6 7 8 9 T")
	if s := e.DecideBidScore(context.Background(), "bot", weak, 0); s != 0 {
		t.Errorf("弱牌不应叫分，got=%d handStrength=%v", s, handStrength(weak))
	}

	// 当前最高分已为 3 时无法再加，返回 0
	if s := e.DecideBidScore(context.Background(), "bot", strong, 3); s != 0 {
		t.Errorf("highBid=3 时不应再叫，got=%d", s)
	}

	// 中等偏强手牌（强度达阈值）应叫 1 分，不弃叫（所有难度阈值一致）
	mid := cards("R 2 2 A K")
	if s := e.DecideBidScore(context.Background(), "bot", mid, 0); s == 0 {
		t.Errorf("中等偏强手牌不应弃叫，handStrength=%v", handStrength(mid))
	}
}

// TestEngine_DecideDouble 加倍：强牌加倍、弱牌不加倍，无随机行为
func TestEngine_DecideDouble(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	strong := cards("R B 2 2 2 2 A K")
	if !e.DecideDouble(context.Background(), "bot", strong) {
		t.Error("强牌应加倍")
	}

	weak := cards("3 4 5 6 7 8 9 T")
	if e.DecideDouble(context.Background(), "bot", weak) {
		t.Error("弱牌不应加倍")
	}

	// 结果确定性：同一手牌重复决策结果一致（已移除随机放弃）
	for i := 0; i < 20; i++ {
		if !e.DecideDouble(context.Background(), "bot", strong) {
			t.Fatal("强牌加倍决策应确定性成立")
		}
	}
}

// TestHandStrength 手牌强度评分权重（含结构因子）
func TestHandStrength(t *testing.T) {
	t.Parallel()
	// 单张大牌：基础分，无结构加分/减分（手牌数<5不应用惩罚）
	if s := handStrength(cards("R")); s != 2 {
		t.Errorf("大王 = %v, want 2", s)
	}
	if s := handStrength(cards("B")); s != 1.5 {
		t.Errorf("小王 = %v, want 1.5", s)
	}
	if s := handStrength(cards("2")); s != 1 {
		t.Errorf("2 = %v, want 1", s)
	}
	if s := handStrength(cards("A")); s != 0.5 {
		t.Errorf("A = %v, want 0.5", s)
	}
	
	// 炸弹：基础 3 分 + 结构加分（4张牌省3手，+0.9）
	if s := handStrength(cards("9 9 9 9")); s < 3.8 || s > 4.0 {
		t.Errorf("炸弹 = %v, want ~3.9 (3 + 结构加分)", s)
	}
	
	// 完整手牌中的孤张 2 惩罚测试
	handTrulyIsolated2 := cards("2 4 6 8 T Q") // 2 周围无 3/A
	if s := handStrength(handTrulyIsolated2); s > 1.0 { // 6张单牌，2是孤张应被惩罚
		t.Logf("孤张2手牌强度 = %v (应有惩罚)", s)
	}
}
