package bot

import (
	"math"
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// inference_test.go 对手推断单测：全部构造确定性输入（显式未知池/座位余牌），
// 与手算值精确对照，避免策略随机性导致断言误判。

func TestCombSmall(t *testing.T) {
	cases := []struct{ n, k int }{
		{8, 4}, {18, 4}, {5, 4}, {4, 4}, {10, 0},
	}
	vals := []float64{70, 3060, 5, 1, 1}
	for i, c := range cases {
		if got := combSmall(c.n, c.k); got != vals[i] {
			t.Errorf("C(%d,%d) = %v, 期望 %v", c.n, c.k, got, vals[i])
		}
	}
	if combSmall(3, 4) != 0 {
		t.Errorf("k>n 时应为 0")
	}
}

func TestBombHoldProb_Hypergeom(t *testing.T) {
	// 地主剩 8 张、队友剩 10 张：未现 4 张全在地主手的概率 = C(8,4)/C(18,4) = 70/3060
	got := bombHoldProb(8, 10, true, 0)
	if want := 70.0 / 3060.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("持炸概率 = %v, 期望 %v", got, want)
	}
	// 叫 3 分先验 ×1.5；叫 1 分 ×0.6
	if got := bombHoldProb(8, 10, true, 3); math.Abs(got-1.5*70.0/3060.0) > 1e-9 {
		t.Errorf("叫 3 先验 = %v, 期望 %v", got, 1.5*70.0/3060.0)
	}
	if got := bombHoldProb(8, 10, true, 1); math.Abs(got-0.6*70.0/3060.0) > 1e-9 {
		t.Errorf("叫 1 先验 = %v, 期望 %v", got, 0.6*70.0/3060.0)
	}
	// 非地主对手不吃叫分修正
	if got := bombHoldProb(8, 10, false, 3); math.Abs(got-70.0/3060.0) > 1e-9 {
		t.Errorf("农民对手不应吃叫分先验，得 %v", got)
	}
}

func TestBombThreatProb_Deterministic(t *testing.T) {
	// 农民视角：Q 未现 4 张，地主 8 队友 10 → 概率 ≈0.023 < 阈值 → 不构成威胁
	var u [rankCount]int
	u[rankIdx(card.RankQ)] = 4
	gctx := GameContext{PlayerCounts: [2]int{10, 8}, DownIsLandlord: true}
	if p := bombThreatProb(gctx, u, 8, 10); p > bombProbThreshold {
		t.Errorf("稀薄持炸概率不应超阈值：%v", p)
	}
	// 地主剩 4 张全部未现（队友 0）：必持炸 → 概率 1
	if p := bombThreatProb(gctx, u, 4, 0); p != 1 {
		t.Errorf("地主 4 张=未现 4 张应概率 1，得 %v", p)
	}
	// 无 4 未现点数 → 0
	if p := bombThreatProb(gctx, [rankCount]int{}, 8, 10); p != 0 {
		t.Errorf("无可炸点数应概率 0，得 %v", p)
	}
}

// hasPath 权重表中是否存在"单步 <type>"路径
func hasPath(weights map[shapeKey]float64, t rule.HandType) bool {
	for k, w := range weights {
		if k.steps == 1 && k.kinds[t] == 1 && w > 0 {
			return true
		}
	}
	return false
}

func TestFinishShapeWeights_Completeness(t *testing.T) {
	// 全 15 点数可用（未知池各 4 张）时 4 张余牌的收尾路径集完备性
	var u [rankCount]int
	for i := range u {
		u[i] = 4
	}
	w4 := finishShapeWeights(u, 4)
	for _, typ := range []rule.HandType{rule.Bomb, rule.TrioWithSingle} {
		if !hasPath(w4, typ) {
			t.Errorf("4 张路径集应含单步 %s", typ)
		}
	}
	// 两对一步不可走完（连对需 3 对），但 2 步"对+对"存在
	twoPair := false
	for k, w := range w4 {
		if k.steps == 2 && k.kinds[rule.Pair] == 2 && w > 0 {
			twoPair = true
		}
	}
	if !twoPair {
		t.Errorf("4 张路径集应含 对+对（2 步）")
	}
	if hasPath(w4, rule.Straight) {
		t.Errorf("4 张不应存在单步顺子路径")
	}

	// 5 张：单步 = 顺子(5) / 三带二；不含炸弹
	w5 := finishShapeWeights(u, 5)
	for _, typ := range []rule.HandType{rule.Straight, rule.TrioWithPair} {
		if !hasPath(w5, typ) {
			t.Errorf("5 张路径集应含单步 %s", typ)
		}
	}
	if hasPath(w5, rule.Bomb) {
		t.Errorf("5 张不应有单步炸弹路径（炸弹仅 4 张）")
	}

	// 6 张：单步 = 炸弹+单? 否；顺子(6)/连对(3节)/飞机(2节)/四带二 存在
	w6 := finishShapeWeights(u, 6)
	for _, typ := range []rule.HandType{rule.Straight, rule.PairStraight, rule.Plane, rule.FourWithTwo} {
		if !hasPath(w6, typ) {
			t.Errorf("6 张路径集应含单步 %s", typ)
		}
	}
}

func TestFinishShapeWeights_UnknownMinusOne(t *testing.T) {
	// 视图缺失（-1）按 4 张先验参与：3 张余牌应仍能枚举出三张单步路径（降级不失保守）
	var u [rankCount]int
	for i := range u {
		u[i] = -1
	}
	if !hasPath(finishShapeWeights(u, 3), rule.Trio) {
		t.Errorf("全未知时 3 张应含三张路径")
	}
}

func TestLeadShapeRisk(t *testing.T) {
	var u [rankCount]int
	for i := range u {
		u[i] = 4
	}
	w := finishShapeWeights(u, 5)
	// 单张领出：任何 5 张构成都能以单张序列走完 → 风险必然为正
	singleRisk := leadShapeRisk(w, rule.ParsedHand{Type: rule.Single, KeyRank: card.Rank5})
	// 顺子(5)：对手恰以 5 连顺收尾的路径占比存在但远小于全体 → 风险低于单张
	straightRisk := leadShapeRisk(w, rule.ParsedHand{Type: rule.Straight, KeyRank: card.Rank7, Length: 5})
	if !(straightRisk < singleRisk) {
		t.Errorf("顺子风险 %v 应低于单张风险 %v", straightRisk, singleRisk)
	}
	if singleRisk <= 0 {
		t.Errorf("单张风险应为正")
	}
}

func TestShapeBlockLead_Deterministic(t *testing.T) {
	// 农民上家、地主剩 4 张（Q 未现 4 张=地主可能成炸）、队友 10 张：
	// 领出应为合法非空牌型且不拆炸弹
	gctx := GameContext{
		Hand:           cards("K K 7 8 9 10 J"),
		MustPlay:       true,
		DownIsLandlord: true,
		PlayerCounts:   [2]int{10, 4},
		RemainingCards: shapeBlockTestRemaining(),
		Cfg:            EngineConfig{EnableInference: true},
	}
	cardsOut := shapeBlockLead(gctx)
	if len(cardsOut) == 0 {
		t.Fatalf("形状死封领出不应为空")
	}
	ph, err := rule.ParseHand(cardsOut)
	if err != nil || ph.Type == rule.Invalid {
		t.Fatalf("领出牌型非法: %s", cardsToStr(cardsOut))
	}
}

// shapeBlockTestRemaining 全 54 张记牌视图（hard 完整度）
func shapeBlockTestRemaining() map[card.Rank]int {
	return fullRemaining()
}
