package bot

import (
	"context"
	"testing"
)

// bid_model_test.go Phase 5 单测：叫分新模型（绝对控制组数 / 炸弹非线性 / 断点修复）
// 与加倍判据（手数 + 牌权 + 炸弹数）。全部构造确定手牌，与手算值对照。

func TestAbsoluteControls(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hand string
		want float64
	}{
		{"R", 1},             // 大王：最大单张，稳收一次牌权
		{"B", 0.5},           // 单小王：可能被大王压，半组
		{"R B", 2},           // 大王 1 + 王炸再 1
		{"2", 1},             // 单 2 除王炸外无人能压
		{"2 2", 2},           // 单/对各收一次
		{"2 2 2", 3},         // 单/对/三三组
		{"2 2 2 2", 3},       // 四张封顶 3 组（炸弹分另计，不重复奖励）
		{"2 2 2 A", 4},       // 占满 3 张 2 → A 即场上最大同路
		{"2 A", 1},           // 2 未占满，A 仍可能被对手 2 压
		{"A K Q", 0},         // 无绝对控制
		{"R B 2 2 2 2 A", 6}, // 1 + 1 + 3 + A 升级
	}
	for _, tt := range tests {
		if got := absoluteControls(rankCountsOf(cards(tt.hand))); got != tt.want {
			t.Errorf("%s 控制组数 = %v, 期望 %v", tt.hand, got, tt.want)
		}
	}
}

func TestBombScoreV2_NonLinear(t *testing.T) {
	t.Parallel()
	if got := bombScoreV2(rankCountsOf(cards("3 4 5"))); got != 0 {
		t.Errorf("无炸弹 = %v, 期望 0", got)
	}
	if got := bombScoreV2(rankCountsOf(cards("5 5 5 5"))); got != 3 {
		t.Errorf("单炸 = %v, 期望 3", got)
	}
	// 非线性：第二炸 +4（两手并一手，兑现确定性递增），而非 3+3=6
	if got := bombScoreV2(rankCountsOf(cards("5 5 5 5 7 7 7 7"))); got != 7 {
		t.Errorf("双炸 = %v, 期望 7（3+4）", got)
	}
	if got := bombScoreV2(rankCountsOf(cards("R B"))); got != 3 {
		t.Errorf("王炸按一组计 = %v, 期望 3", got)
	}
}

func TestGapBonus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hand string
		want float64
	}{
		{"9 10 J Q", 0.5},         // 极大连子 4 张，两端缺 8/K → 半手
		{"9 10 J Q K", 0},         // 已成顺（5 连），不再给断点潜力
		{"9 10 J K", 0},           // 内部空洞拆成两段短连子，不算补一张成顺
		{"3 4 5 6 9 10 J Q", 1.0}, // 两段各 0.5，触上限
		{"3 4 5", 0},              // 3 连缺两张，无半手潜力
		{"10 J Q K", 0.5},         // 缺 A 的四连同样只按半手潜力计
	}
	for _, tt := range tests {
		if got := gapBonus(rankCountsOf(cards(tt.hand))); got != tt.want {
			t.Errorf("%s 断点修复 = %v, 期望 %v", tt.hand, got, tt.want)
		}
	}
}

// TestHandStrengthV2_TopCards 单张大牌：基线分 + 0.6×控制组数（无结构加减分）
func TestHandStrengthV2_TopCards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hand string
		want float64
	}{
		{"R", 2 + 0.6*1},
		{"B", 1.5 + 0.6*0.5},
		{"2", 1 + 0.6*1},
		{"A", 0.5},
	}
	for _, tt := range tests {
		if got := handStrengthV2(cards(tt.hand)); got != tt.want {
			t.Errorf("%s 强度 = %v, 期望 %v", tt.hand, got, tt.want)
		}
	}
}

// TestScoredBidScoreV2_Bands 选档口径：超强牌叫 3、纯结构无控制牌不叫、
// 已叫到 3 时不再加叫；新模型阈值上调后整体比基线保守
func TestScoredBidScoreV2_Bands(t *testing.T) {
	t.Parallel()
	monster := cards("R B 2 2 2 2 A A K K Q J 9 8 7 6")
	if got := scoredBidScoreV2(monster, 0); got != 3 {
		t.Errorf("双王+2 炸弹+连对 应叫 3，got=%d strength=%v", got, handStrengthV2(monster))
	}
	if got := scoredBidScoreV2(monster, 3); got != 0 {
		t.Errorf("highBid=3 应不叫，got=%d", got)
	}

	structured := cards("3 4 5 6 7 8 9 10") // 一手长顺但零控制牌
	if got := scoredBidScoreV2(structured, 0); got != 0 {
		t.Errorf("无大牌的纯顺子不应抢叫，got=%d strength=%v", got, handStrengthV2(structured))
	}

	// 基线口径下同一手牌会叫到更高分（新模型按控制牌收紧了抢分冲动）
	if base, v2 := scoredBidScore(structured, 0), scoredBidScoreV2(structured, 0); v2 > base {
		t.Errorf("新模型应不高于基线：base=%d v2=%d", base, v2)
	}
}

// TestShouldDoubleV2 加倍判据：手数少 + 牌权覆盖 / 双炸直接加 /
// 大牌多但走不完不加（基线会因强度阈值误加）
func TestShouldDoubleV2(t *testing.T) {
	t.Parallel()
	// 4 手 + 3 组控制（大王 + 两张 2）→ 每手都有牌权回收
	if !shouldDoubleV2(cards("R 2 2 A A K K")) {
		t.Error("手数 4 且牌权覆盖应加倍")
	}
	// 两个炸弹（5 炸 + 7 炸）：兑现手段充足
	if !shouldDoubleV2(cards("5 5 5 5 9 9 9 9 3 4 6 7")) {
		t.Error("双炸应加倍")
	}
	// 王炸 + 三连对到底，手数 6 > 4：大牌多但走不完，不该加倍
	if shouldDoubleV2(cards("R B A A K K Q Q J 9 8 7")) {
		t.Error("手数超上限不应加倍")
	}
	// 王炸 + 四连对 + 顺子共 3 手、控制 2 组（王炸可拆单/对收权）→ 加倍
	if !shouldDoubleV2(cards("R B A A K K Q Q J J 9 8 7 6 5")) {
		t.Error("3 手且牌权可回收应加倍")
	}
	// 基线：同一手牌强度已过 4.5 阈值 → 误判加倍（回归对照）
	many := cards("R B A A K K Q Q J 9 8 7")
	if handStrength(many) < doubleThreshold {
		t.Fatalf("测试前置失败：该手牌基线强度应达加倍阈值，实际 %v", handStrength(many))
	}
}

// TestDecideBidModelGating 门控：零配置引擎保持基线口径（叫分/加倍结果与
// handStrength 阈值一致），开启 EnableBidModel 后才走新判据
func TestDecideBidModelGating(t *testing.T) {
	t.Parallel()
	hand := cards("R B A A K K Q Q J 9 8 7")

	base := newTestEngine()
	if !base.DecideDouble(context.Background(), "bot", hand) {
		t.Error("基线引擎应按强度阈值加倍")
	}
	if got := base.DecideBidScore(context.Background(), "bot", hand, 0); got != scoredBidScore(hand, 0) {
		t.Errorf("基线叫分应等于 scoredBidScore，got=%d", got)
	}

	modelled := newTestEngine().WithConfig(EngineConfig{EnableBidModel: true})
	if modelled.DecideDouble(context.Background(), "bot", hand) {
		t.Error("新模型应因手数超上限而拒绝加倍")
	}
	if got := modelled.DecideBidScore(context.Background(), "bot", hand, 0); got != scoredBidScoreV2(hand, 0) {
		t.Errorf("新模型叫分应等于 scoredBidScoreV2，got=%d", got)
	}
}
