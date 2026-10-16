package bot

import (
	"testing"

	"xgames/internal/games/ddz/rule"
)

// handplan_test.go 整手拆牌计划器单测。
// 全部构造确定性场景（视图完备/未知池显式给出），避免策略随机性导致断言误判。

// emptyUnknown 全知且对手无牌的未知池（所有组均判为掌权）
func emptyUnknown() [rankCount]int { return [rankCount]int{} }

// allUnknown 全未知池（normal 以下记牌视图缺失点数场景：-1 = 任意张，掌权判定全否）
func allUnknown() [rankCount]int {
	var u [rankCount]int
	for i := range u {
		u[i] = -1
	}
	return u
}

func TestPlanValue_PairStraightControl(t *testing.T) {
	// 33445566：连对一组走完（1 手且掌权，-10+8=-2）优于拆 4 个小对（4×-5=-20）
	counts := handToCounts(cards("3 3 4 4 5 5 6 6"))
	if v := planDFS(counts, emptyUnknown(), map[[rankCount]int]int{}, new(int)); v != -2 {
		t.Errorf("连对控场计划评分 = %d, 期望 -2（单组连对掌权）", v)
	}
}

func TestPlanValue_KickerAbsorbsSmallSingle(t *testing.T) {
	// 888 2 4455：最优计划 = 888带44 + 对5 + 单2 = -2-5-2 = -9
	//（带牌吃掉一个暴露小对；裸 888 拆 4 手 = -2-5-5-2 = -14 必然更差，
	//计划器应选带牌出法而非裸三条）
	counts := handToCounts(cards("8 8 8 2 4 4 5 5"))
	got := planDFS(counts, emptyUnknown(), map[[rankCount]int]int{}, new(int))
	if got != -9 {
		t.Errorf("带牌计划评分 = %d, 期望 -9（888带44+对5+单2）", got)
	}
	bare := -2 - 5 - 5 - 2
	if bare >= got {
		t.Errorf("裸三条拆分不应优于带牌拆分")
	}
}

func TestPlanValue_KeepsRocketWhole(t *testing.T) {
	// 3 + 王炸：火箭一组（-10+8=-2）+ 小单 3（-4）= -6 优于拆单逐出（-8）
	counts := handToCounts(cards("3 B R"))
	v := planDFS(counts, emptyUnknown(), map[[rankCount]int]int{}, new(int))
	if v != -6 {
		t.Errorf("火箭计划评分 = %d, 期望 -6（火箭不拆）", v)
	}
}

func TestPlanValue_UnknownPoolNoControlBonus(t *testing.T) {
	// 全未知视图（点数未跟踪）：掌权判定全按保守 false 处理，计划退化为
	// 手数 + 暴露惩罚权衡——顺子一组 -10 仍优于 5 个小单 5×(-12)
	counts := handToCounts(cards("3 4 5 6 7"))
	if v := planDFS(counts, allUnknown(), map[[rankCount]int]int{}, new(int)); v != -10 {
		t.Errorf("全未知顺子评分 = %d, 期望 -10（无掌权加分仍按手数合并）", v)
	}
}

func TestPlanCache_FirstStepCovered(t *testing.T) {
	// 计划覆盖集：33445566 的主计划首步是整组连对；裸单 3（差 1 手+暴露）
	// 在容差外，不属于备选计划
	c := newPlanCache(emptyUnknown())
	counts := handToCounts(cards("3 3 4 4 5 5 6 6"))
	whole := cards("3 3 4 4 5 5 6 6")
	ph, err := rule.ParseHand(whole)
	if err != nil {
		t.Fatalf("解析连对失败: %v", err)
	}
	rest := subtractCounts(counts, handToCounts(whole))
	if !c.firstStepCovered(counts, ph, rest) {
		t.Errorf("连对整组应被主计划覆盖")
	}
	single := cards("3")
	singlePh, err := rule.ParseHand(single)
	if err != nil {
		t.Fatalf("解析单张失败: %v", err)
	}
	singleRest := subtractCounts(counts, handToCounts(single))
	if c.firstStepCovered(counts, singlePh, singleRest) {
		t.Errorf("裸出小单 3 不应在计划覆盖集内（多 3 手且暴露小牌）")
	}
}

func TestPlanCache_ValueMatchesDFS(t *testing.T) {
	// 缓存 value 与直算 planDFS 一致（缓存不改变语义）
	c := newPlanCache(emptyUnknown())
	counts := handToCounts(cards("3 3 4 4 5 5 6 6"))
	if got, want := c.value(counts), planDFS(counts, emptyUnknown(), map[[rankCount]int]int{}, new(int)); got != want {
		t.Errorf("planCache.value = %d, planDFS = %d, 应一致", got, want)
	}
	if c.value([rankCount]int{}) != 0 {
		t.Errorf("空手牌计划评分应为 0")
	}
}
