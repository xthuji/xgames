package bot

import (
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// handplan.go 整手拆牌计划器（计划 §Phase 1）。
// 真人思维第一步是"这手牌拆成哪几组、能收几次牌权"：在计数数组上做完整拆分的
// 价值搜索（与 MinHandCount 共用 enumerateMoves 枚举，保证搜索空间一致），
// 目标函数同时衡量手数、预期牌权（控制组）与小牌暴露，替代纯手数贪心。
// 由 EngineConfig.EnablePlanner 门控，关闭时行为与基线完全一致。

// 计划评分权重（初始值，竞技场调参）
const (
	planCostPerHand   = 10 // 每手出牌机会的成本：手数越多越难走完
	planControlBonus  = 8  // 预期牌权组：该组在未知池中无更大同路（sprintControl）
	planSmallSinglePen = 2 // 暴露小单张：≤9 的单张组（难守、易被顶）
	planSmallPairPen   = 3 // 暴露小对子：≤9 的对子组
	maxPlanNodes      = 20000 // 计划搜索节点上限（与手数搜索同口径）
	planCoverMargin   = 4     // "计划在 covering"容差：与主计划差半手以内的拆分视为备选计划（竞技场调参）
)

// smallPlanRankIdx ≤9 的计数数组下标上界：单张/对子组点数 ≤9 视为小牌暴露
const smallPlanRankIdx = int(card.Rank9) - int(card.Rank3)

// planCache 一回合内的整手计划价值缓存（按 [rankCount]int 计数数组键值）。
// unknown 为该回合固定未知池（计划评估的唯一外部输入）。
// 生命周期：Controller.buildContextLocked 创建；直接调用 DecidePlay 的测试路径
// 由 Engine 入口惰性补建；回合结束随 GameContext 副本丢弃。
type planCache struct {
	memo    map[[rankCount]int]int
	unknown [rankCount]int
}

// newPlanCache 创建空缓存
func newPlanCache(unknown [rankCount]int) *planCache {
	return &planCache{memo: make(map[[rankCount]int]int, 64), unknown: unknown}
}

// value 当前计数数组的最优整手计划评分（越大越好；空手牌=0）。
// 未启用计划器或无缓存时返回 0（调用方须先经 planUsable 门控）。
func (c *planCache) value(counts [rankCount]int) int {
	if v, ok := c.memo[counts]; ok {
		return v
	}
	nodes := 0
	localMemo := make(map[[rankCount]int]int, 128)
	result := planDFS(counts, c.unknown, localMemo, &nodes)
	c.memo[counts] = result
	return result
}

// firstStepCovered 首步候选是否在最优/备选计划内：
// 该组价值 + 剩余最优计划 ≥ 全手最优计划 - planCoverMargin
// （容差内的所有首步即"评分 Top-N 备选计划"的首步集合，N 由价值差自然限定）。
func (c *planCache) firstStepCovered(counts [rankCount]int, ph rule.ParsedHand, rest [rankCount]int) bool {
	return planGroupValue(c.unknown, ph)+c.value(rest) >= c.value(counts)-planCoverMargin
}

// planGroupValue 单个拆分组对计划评分的贡献：
// 每手固定 -planCostPerHand；绝对掌权组（未知池无更大同路）+planControlBonus；
// ≤9 的单张/对子组按暴露风险减分。炸弹/王炸恒掌权（sprintControl 内置）。
func planGroupValue(unknown [rankCount]int, ph rule.ParsedHand) int {
	v := -planCostPerHand
	if sprintControl(ph, unknown) {
		v += planControlBonus
	}
	switch ph.Type {
	case rule.Single:
		if rankIdx(ph.KeyRank) <= smallPlanRankIdx {
			v -= planSmallSinglePen
		}
	case rule.Pair:
		if rankIdx(ph.KeyRank) <= smallPlanRankIdx {
			v -= planSmallPairPen
		}
	}
	return v
}

// planAllSinglesValue 全单张分解的保守估值（节点超限兜底与搜索下界）：
// 每个点数按 n 个独立单张组计。
func planAllSinglesValue(c [rankCount]int, unknown [rankCount]int) int {
	v := 0
	for i, n := range c {
		ph := rule.ParsedHand{Type: rule.Single, KeyRank: idxRank(i), Length: 1}
		gv := planGroupValue(unknown, ph)
		v += gv * n
	}
	return v
}

// planDFS 完整拆分的最大价值和：每步锚定剩余最小点数枚举合法组（enumerateMoves），
// 组价值 + 递归剩余。DFS + 记忆化 + 节点上限，结构与 minHandCountDFS 对称。
func planDFS(c [rankCount]int, unknown [rankCount]int, memo map[[rankCount]int]int, nodes *int) int {
	if countsTotal(c) == 0 {
		return 0
	}
	if v, ok := memo[c]; ok {
		return v
	}
	*nodes++
	if *nodes > maxPlanNodes {
		return planAllSinglesValue(c, unknown) // 超限兜底：全单张保守估值（不缓存）
	}

	best := planAllSinglesValue(c, unknown)
	for _, m := range enumerateMoves(c) {
		if v := planGroupValue(unknown, m.ph) + planDFS(m.rest, unknown, memo, nodes); v > best {
			best = v
		}
	}

	memo[c] = best
	return best
}

// planUsable 计划器本次决策是否可用：特性开关开启且缓存就绪
func planUsable(gctx *GameContext) bool {
	return gctx != nil && gctx.Cfg.EnablePlanner && gctx.planCache != nil
}

// ensurePlanCache 为按值传递的 GameContext 副本补建计划缓存：
// 正常链路（Controller.buildContextLocked / Engine.DecidePlay 入口）已建好，
// 此处兜底直接调用策略函数的测试路径，unknown 从记牌视图现场推导。
func ensurePlanCache(gctx *GameContext) {
	if gctx.Cfg.EnablePlanner && gctx.planCache == nil {
		gctx.planCache = newPlanCache(unknownCounts(gctx.RemainingCards, gctx.Hand))
	}
}
