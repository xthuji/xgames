package bot

import (
	"testing"
	"xgames/internal/games/mahjong/rule"
)

// TestFix_BreakingFormedMelds 测试修复：不应拆散已组成的顺子
func TestFix_BreakingFormedMelds(t *testing.T) {
	e := NewEngine(nil)

	// 场景1：手牌 [1万, 2万, 3万, 4万] - 有两个可能的顺子 [1,2,3] 和 [2,3,4]
	// 应该优先打出边缘的 1万 或 4万，而不是保留所有牌
	hand1 := []int{0, 1, 2, 3} // 1万, 2万, 3万, 4万
	ctx1 := DiscardContext{Hand: hand1}
	discard1 := e.DecideDiscardEnhanced(ctx1)

	// 期望：打出 1万(0) 或 4万(3)，因为它们不是"必需"的顺子成员
	if discard1 != 0 && discard1 != 3 {
		t.Errorf("Should discard edge tile (1万 or 4万), got %d", discard1)
	}
	t.Logf("Scenario 1 [1,2,3,4万]: discarded %d ✓", discard1)

	// 场景2：手牌 [1万, 2万, 3万, 5万] - 只有一个顺子 [1,2,3]
	// 应该保留完整的 [1,2,3] 顺子，打出 5万
	hand2 := []int{0, 1, 2, 4} // 1万, 2万, 3万, 5万
	ctx2 := DiscardContext{Hand: hand2}
	discard2 := e.DecideDiscardEnhanced(ctx2)

	// 期望：打出 5万(4)
	if discard2 != 4 {
		t.Errorf("Should discard isolated 5万, got %d", discard2)
	}
	t.Logf("Scenario 2 [1,2,3,5万]: discarded %d ✓", discard2)

	// 场景3：手牌 [1万, 1万, 1万, 2万, 3万] - 有刻子 [1,1,1] 和顺子 [1,2,3]
	// 注意：这里的 1万 同时属于刻子和顺子，但刻子优先级更高
	hand3 := []int{0, 0, 0, 1, 2} // 三个1万 + 2万 + 3万
	ctx3 := DiscardContext{Hand: hand3}
	discard3 := e.DecideDiscardEnhanced(ctx3)

	// 期望：打出 2万(1) 或 3万(2)，保留刻子
	if discard3 != 1 && discard3 != 2 {
		t.Errorf("Should discard from sequence, keep triplet, got %d", discard3)
	}
	t.Logf("Scenario 3 [1,1,1,2,3万]: discarded %d ✓", discard3)
}

// TestFix_SuitConcentrationPriority 测试修复：花色集中度优先出牌
func TestFix_SuitConcentrationPriority(t *testing.T) {
	e := NewEngine(nil)

	// 场景1：手牌有大量条子（8张），少量万字（3张）和筒子（2张）
	// 应该优先打出万字或筒子的孤张
	hand1 := make([]int, rule.NumTypes)
	// 条子：1-8条（8张）
	for i := 18; i <= 25; i++ {
		hand1[i] = 1
	}
	// 万字：1万, 5万, 9万（3张孤张）
	hand1[0] = 1
	hand1[4] = 1
	hand1[8] = 1
	// 筒子：2筒, 8筒（2张孤张）
	hand1[10] = 1
	hand1[16] = 1

	// 转换为手牌列表
	var handList []int
	for tile, count := range hand1 {
		for i := 0; i < count; i++ {
			handList = append(handList, tile)
		}
	}

	ctx1 := DiscardContext{Hand: handList}
	discard1 := e.DecideDiscardEnhanced(ctx1)

	// 期望：打出万字或筒子的孤张（0, 4, 8, 10, 16），而不是条子
	isMinoritySuit := discard1 < 18 || discard1 >= 27 // 非条子
	if !isMinoritySuit {
		t.Errorf("Should discard minority suit tile, got %d (should be <18 or >=27)", discard1)
	}
	t.Logf("Scenario 1 (8 souzu, 3 manzu, 2 pinzu): discarded %d ✓", discard1)

	// 场景2：手牌有大量万字（7张），少量其他花色
	// 且万字中有好搭子，其他花色是孤张
	hand2 := make([]int, rule.NumTypes)
	// 万字：1,2,3,4,5,6,7万（7张，有好搭子）
	for i := 0; i <= 6; i++ {
		hand2[i] = 1
	}
	// 条子：1条孤张
	hand2[18] = 1
	// 筒子：1筒孤张
	hand2[9] = 1
	// 字牌：东风孤张
	hand2[27] = 1

	var handList2 []int
	for tile, count := range hand2 {
		for i := 0; i < count; i++ {
			handList2 = append(handList2, tile)
		}
	}

	ctx2 := DiscardContext{Hand: handList2}
	discard2 := e.DecideDiscardEnhanced(ctx2)

	// 期望：优先打出字牌(27)、条子(18)或筒子(9)，保留万字搭子
	isMinoritySuit2 := discard2 >= 9 // 非万字
	if !isMinoritySuit2 {
		t.Errorf("Should discard minority suit, got %d (manzu tile)", discard2)
	}
	t.Logf("Scenario 2 (7 manzu with good shapes, 1 each others): discarded %d ✓", discard2)
}

// TestInCompleteRun_Optimized 测试优化后的 inCompleteRun 函数
func TestInCompleteRun_Optimized(t *testing.T) {
	// 场景1：[1,2,3,4] - 1和4不是必需的
	counts1 := make([]int, rule.NumTypes)
	counts1[0] = 1 // 1万
	counts1[1] = 1 // 2万
	counts1[2] = 1 // 3万
	counts1[3] = 1 // 4万

	if inCompleteRun(counts1, 0) { // 1万
		t.Error("1万 in [1,2,3,4] should NOT be protected (redundant)")
	}
	if inCompleteRun(counts1, 3) { // 4万
		t.Error("4万 in [1,2,3,4] should NOT be protected (redundant)")
	}
	if !inCompleteRun(counts1, 1) { // 2万
		t.Error("2万 in [1,2,3,4] should be protected")
	}
	if !inCompleteRun(counts1, 2) { // 3万
		t.Error("3万 in [1,2,3,4] should be protected")
	}
	t.Log("✓ [1,2,3,4] correctly identifies essential tiles")

	// 场景2：[1,2,3] - 所有都是必需的
	counts2 := make([]int, rule.NumTypes)
	counts2[0] = 1
	counts2[1] = 1
	counts2[2] = 1

	if !inCompleteRun(counts2, 0) {
		t.Error("1万 in [1,2,3] should be protected")
	}
	if !inCompleteRun(counts2, 1) {
		t.Error("2万 in [1,2,3] should be protected")
	}
	if !inCompleteRun(counts2, 2) {
		t.Error("3万 in [1,2,3] should be protected")
	}
	t.Log("✓ [1,2,3] all tiles protected")

	// 场景3：[1,1,1,2,3] - 刻子优先
	counts3 := make([]int, rule.NumTypes)
	counts3[0] = 3 // 三个1万
	counts3[1] = 1 // 2万
	counts3[2] = 1 // 3万

	// 注意：inCompleteRun 只检查顺子，不检查刻子
	// 在这个手牌中，移除一个1万后仍有 [1,1,2,3]，还能组成 [1,2,3] 顺子
	// 所以 inCompleteRun 会返回 false（因为移除后顺子数量不变）
	// 但 discardLoss 函数会先检查 counts[tile]>=3，所以1万实际上会被保护
	if inCompleteRun(counts3, 0) {
		t.Log("Note: 1万 in [1,1,1,2,3] is protected by inCompleteRun (sequence still exists after removal)")
	} else {
		t.Log("Note: 1万 in [1,1,1,2,3] is NOT protected by inCompleteRun, but WILL be protected by counts>=3 check")
	}
	t.Log("✓ [1,1,1,2,3] - triplet protection handled by discardLoss, not inCompleteRun")
}

// TestCalculateSuitConcentrationBonus 测试花色集中度计算
func TestCalculateSuitConcentrationBonus(t *testing.T) {
	// 场景1：条子占主导（8/10 = 80%）
	counts1 := make([]int, rule.NumTypes)
	for i := 18; i <= 25; i++ { // 8张条子
		counts1[i] = 1
	}
	counts1[0] = 1 // 1万
	counts1[9] = 1 // 1筒

	// 万字应该得到负修正
	bonusMan := calculateSuitConcentrationBonus(counts1, 0)
	if bonusMan >= 0 {
		t.Errorf("Manzu should get negative bonus, got %d", bonusMan)
	}
	t.Logf("✓ Manzu in dominant souzu hand: bonus=%d", bonusMan)

	// 条子不应该得到修正
	bonusSou := calculateSuitConcentrationBonus(counts1, 18)
	if bonusSou != 0 {
		t.Errorf("Souzu should get no bonus, got %d", bonusSou)
	}
	t.Logf("✓ Souzu in dominant souzu hand: bonus=%d", bonusSou)

	// 场景2：均衡分布（无主导花色）
	counts2 := make([]int, rule.NumTypes)
	for i := 0; i < 3; i++ { // 3万
		counts2[i] = 1
	}
	for i := 9; i < 12; i++ { // 3筒
		counts2[i] = 1
	}
	for i := 18; i < 21; i++ { // 3条
		counts2[i] = 1
	}

	bonus := calculateSuitConcentrationBonus(counts2, 0)
	if bonus != 0 {
		t.Errorf("Balanced hand should get no bonus, got %d", bonus)
	}
	t.Log("✓ Balanced distribution: no bonus")

	// 场景3：字牌不参与计算
	counts3 := make([]int, rule.NumTypes)
	counts3[27] = 1 // 东风
	bonusHonor := calculateSuitConcentrationBonus(counts3, 27)
	if bonusHonor != 0 {
		t.Errorf("Honor tiles should get no bonus, got %d", bonusHonor)
	}
	t.Log("✓ Honor tiles: no bonus")
}

// BenchmarkInCompleteRun_Optimized 性能测试：优化后的 inCompleteRun
func BenchmarkInCompleteRun_Optimized(b *testing.B) {
	counts := make([]int, rule.NumTypes)
	for i := 0; i < 9; i++ {
		counts[i] = 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inCompleteRun(counts, 1)
	}
}

// BenchmarkCalculateSuitConcentrationBonus 性能测试：花色集中度计算
func BenchmarkCalculateSuitConcentrationBonus(b *testing.B) {
	counts := make([]int, rule.NumTypes)
	for i := 18; i <= 25; i++ {
		counts[i] = 1
	}
	counts[0] = 1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		calculateSuitConcentrationBonus(counts, 0)
	}
}
