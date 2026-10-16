package bot

import (
	"testing"

	"xgames/internal/games/ddz/card"
)

// production_config_test.go Phase 6：生产接线的开关解析
// （引擎级配置 + 难度档位覆写 + Controller/session 上下文注入）。

func TestProductionConfigShapes(t *testing.T) {
	t.Parallel()
	if got := (HardPlayConfig()); got != (EngineConfig{
		EnablePlanner: true, EnableInference: true, EnableExactEndgame: true, EnableBidModel: true,
	}) {
		t.Errorf("HardPlayConfig 应全开，got=%+v", got)
	}
	// 引擎级生产配置只开叫分/加倍模型：出牌增强必须按档位覆写，
	// 否则 easy/normal 会连同增强一起生效，档位差异失效
	if got := ProductionConfig(); got != (EngineConfig{EnableBidModel: true}) {
		t.Errorf("ProductionConfig 应只开 EnableBidModel，got=%+v", got)
	}
}

// TestPlayConfigResolution 档位覆写优先，未配置档位（含空串=竞技场/单测直接
// 调 DecidePlay 的路径）沿用引擎自身配置
func TestPlayConfigResolution(t *testing.T) {
	t.Parallel()
	e := newTestEngine().WithConfig(ProductionConfig()).WithDifficultyConfig("hard", HardPlayConfig())

	if got := e.playConfig("hard"); got != HardPlayConfig() {
		t.Errorf("hard 档应全开，got=%+v", got)
	}
	for _, diff := range []string{"easy", "normal", "unknown", ""} {
		if got := e.playConfig(diff); got != (EngineConfig{EnableBidModel: true}) {
			t.Errorf("%q 档应沿用引擎配置（出牌开关全关），got=%+v", diff, got)
		}
	}

	// 未设置档位表的引擎（竞技场/单测口径）行为不变
	plain := newTestEngine().WithConfig(HardPlayConfig())
	if got := plain.playConfig("normal"); got != HardPlayConfig() {
		t.Errorf("无档位表时应沿用引擎配置，got=%+v", got)
	}
}

// TestBuildContextCarriesDifficulty Controller 把本局难度注入上下文，引擎据此
// 选择出牌开关；记牌视图完整度仍按档位分级（档位差异保持）
func TestBuildContextCarriesDifficulty(t *testing.T) {
	ctrl, _ := setupController(t, newTestEngine().WithConfig(ProductionConfig()).WithDifficultyConfig("hard", HardPlayConfig()))

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	gt := ctrl.games["R1"]

	gctx := ctrl.buildContextLocked(gt, "b1", true, true)
	if gctx.Difficulty != "hard" {
		t.Fatalf("上下文难度 = %q, 期望 hard", gctx.Difficulty)
	}
	if _, ok := gctx.RemainingCards[card.Rank3]; !ok {
		t.Error("hard 档应跟踪全部点数")
	}

	gt.difficulty = "normal"
	gctx = ctrl.buildContextLocked(gt, "b1", true, true)
	if gctx.Difficulty != "normal" {
		t.Fatalf("上下文难度 = %q, 期望 normal", gctx.Difficulty)
	}
	// normal 档只跟踪关键牌：普通点数不在视图中（未跟踪 = 未知）
	if _, ok := gctx.RemainingCards[card.Rank3]; ok {
		t.Error("normal 档不应跟踪点数 3，记牌视图完整度差异丢失")
	}
	if _, ok := gctx.RemainingCards[card.Rank2]; !ok {
		t.Error("normal 档应跟踪关键牌 2")
	}
}
