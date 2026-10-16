package bot

import (
	"math"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// inference.go 对手牌型推断（计划 §Phase 2）：
// 把"未现点数全有/全无"的二元判断升级为按余牌数的超几何概率，并为对手
// 余牌 3~6 张场景枚举"恰好走完的出牌路径"权重分布，供死封领出与炸弹预警使用。
// 全部由 EngineConfig.EnableInference 门控；记牌视图缺失点数（-1）时按 4 张
// 先验参与计算（自然降级），叫分档 LandlordBid 调节地主侧先验密度。

// bombProbThreshold 炸弹预警概率阈值：对手座位持炸粗概率低于此值时不再降级候选
// （竞技场调参初始值）
const bombProbThreshold = 0.25

// shapeBlockMaxCards 收尾形状推断启用的对手余牌上限（死封窗口从 ≤2 扩到 ≤6）
const shapeBlockMaxCards = 6

// 炸弹 EV 与让牌阈值（计划 §Phase 3，竞技场调参）
const (
	bombBlockShare    = 0.35 // 封堵触发：对手"一步走完"路径权重占比阈值
	bombTempoHandsCap = 2    // 炸后计划：手数 - 预期掌权组 ≤ 此值才可兑现胜利
	bombCashOutHandsCap = 4  // 炸后手数上限：手数再多就不是"炸完即走"，不值当投弹
)

// tankPlanCostFactor 让牌阈值系数：压这手的计划代价（剩余计划评分下跌
// + 手数未能减少的额外手成本）超过 tankPlanCostFactor × 牌权加分（相当于
// 烧掉 2 个牌权）时放过这手
const tankPlanCostFactor = 2

// isRocketCards 是否王炸（双王）
func isRocketCards(cards []card.Card) bool {
	if len(cards) != 2 {
		return false
	}
	return (cards[0].Rank == card.RankBlackJoker && cards[1].Rank == card.RankRedJoker) ||
		(cards[0].Rank == card.RankRedJoker && cards[1].Rank == card.RankBlackJoker)
}

// oneStepFinishShare 对手 n 张余牌"能一手走完"的概率（按计数构成的超几何权重
// 归一）：对每个构成只看其路径集中是否存在单步路径（对手会选最短路线），
// 不按路径条数稀释——持 4 张同点数即必为炸弹，占比 1。
func oneStepFinishShare(unknown [rankCount]int, n int) float64 {
	total, one := 0.0, 0.0
	forEachFinishComp(unknown, n, func(seen map[shapeKey]bool, w float64) {
		total += w
		for k := range seen {
			if k.steps == 1 {
				one += w
				break
			}
		}
	})
	if total <= 0 {
		return 0
	}
	return one / total
}

// planControlEst 从计划评分反推预期掌权组数上界（保守取下整，
// 小牌暴露惩罚使估计值偏低 → 判据宁缺毋滥）：
// v = Σ(-手数成本 + 控制加分·c_i - pen_i) → c_est = floor((v + 手数成本·n) / 控制加分)
func planControlEst(v, hands int) int {
	c := (v + planCostPerHand*hands) / planControlBonus
	if c < 0 {
		c = 0
	}
	if c > hands {
		c = hands
	}
	return c
}

// combSmall 组合数 C(n,k)（n≤54、k≤6，float64 足够精度）
func combSmall(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := 1.0
	for i := 1; i <= k; i++ {
		r *= float64(n - k + i) / float64(i)
	}
	return r
}

// bombHoldProb 未现 4 张的点数全部集中于对手座位（成炸）的超几何概率：
// oppCount 敌对座位余牌数、allyCount 其余座位（农民视角=队友）余牌数。
// bidPrior 叫分先验：地主叫 3 → 掌权/炸弹密度上调；叫 1 → 下调（仅对手是地主时生效）。
func bombHoldProb(oppCount, allyCount int, isOppLandlord bool, landlordBid int) float64 {
	total := oppCount + allyCount
	denom := combSmall(total, 4)
	if denom <= 0 {
		return 0
	}
	p := combSmall(oppCount, 4) / denom
	if isOppLandlord {
		switch {
		case landlordBid >= 3:
			p *= 1.5 // 叫满 3 分：强牌先验，炸弹密度上调
		case landlordBid == 1:
			p *= 0.6 // 偷鸡 1 分：下调
			// bid 2 或 0（未注入）：中性不修正
		}
	}
	if p > 1 {
		p = 1
	}
	return p
}

// bombThreatProb 未知池中存在"对手可成炸"点数的概率。
// 替代 potentialBombRanks 的全有/全无二元判断：地主视角取两农民座位并集，
// 农民视角仅地主座位持炸才是威胁（队友持炸无害，叫分先验自动生效）。
func bombThreatProb(gctx GameContext, unknown [rankCount]int, oppCount, allyCount int) float64 {
	if len(potentialBombRanks(unknown)) == 0 {
		return 0
	}
	if gctx.IsLandlord {
		up, down := gctx.PlayerCounts[0], gctx.PlayerCounts[1]
		pu := bombHoldProb(up, down, gctx.UpIsLandlord, gctx.LandlordBid)
		pd := bombHoldProb(down, up, gctx.DownIsLandlord, gctx.LandlordBid)
		return 1 - (1-pu)*(1-pd)
	}
	return bombHoldProb(oppCount, allyCount, true, gctx.LandlordBid)
}

// shapeKey 收尾路径签名：步数 + 各牌型组数多重集（顺序无关，走完路径只看组件构成）
type shapeKey struct {
	steps uint8
	kinds [int(rule.Rocket) + 1]int8
}

// addType 路径签名追加一个牌型组
func (s *shapeKey) addType(t rule.HandType) {
	s.steps++
	s.kinds[t]++
}

// maxShapePartitionNodes 单一构成拆分明举的节点上限（张数 ≤6 时远不会触及）
const maxShapePartitionNodes = 4000

// enumeratePartitions 枚举 counts 的全部完整拆分路径签名（牌型多重集）。
// 复用 enumerateMoves 锚定枚举；张数 ≤6 时组合空间极小。
func enumeratePartitions(c [rankCount]int, seen map[shapeKey]bool, cur shapeKey, nodes *int) {
	if countsTotal(c) == 0 {
		seen[cur] = true
		return
	}
	*nodes++
	if *nodes > maxShapePartitionNodes {
		return
	}
	for _, m := range enumerateMoves(c) {
		next := cur
		next.addType(m.ph.Type)
		enumeratePartitions(m.rest, seen, next, nodes)
	}
}

// shapeWeight 计数构成 c 从未知池抽取的相对权重 ∏C(U_i, c_i)；
// 未跟踪点数（-1）按 4 张先验。
func shapeWeight(c, unknown [rankCount]int) float64 {
	w := 1.0
	for i, n := range c {
		if n == 0 {
			continue
		}
		u := unknown[i]
		if u < 0 {
			u = 4
		}
		w *= combSmall(u, n)
	}
	return w
}

// forEachFinishComp 枚举未知池中 n 张余牌的全部计数构成：对每个构成给出
// 其"恰好走完"的路径多重集集合与超几何抽取权重（构成遍历的单一真源，
// finishShapeWeights 与 oneStepFinishShare 共用）。
func forEachFinishComp(unknown [rankCount]int, n int, fn func(seen map[shapeKey]bool, w float64)) {
	var comp [rankCount]int
	var dfs func(i, left int)
	dfs = func(i, left int) {
		if left == 0 {
			seen := make(map[shapeKey]bool, 16)
			nodes := 0
			enumeratePartitions(comp, seen, shapeKey{}, &nodes)
			fn(seen, shapeWeight(comp, unknown))
			return
		}
		if i >= rankCount {
			return
		}
		u := unknown[i]
		if u < 0 {
			u = 4 // 未跟踪点数按最大可能参与构成（保守）
		}
		maxC := u
		if maxC > left {
			maxC = left
		}
		for c := 0; c <= maxC; c++ {
			comp[i] = c
			dfs(i+1, left-c)
		}
		comp[i] = 0
	}
	dfs(0, n)
}

// finishShapeWeights 对手余牌 n 张（3~6）时，所有"恰好走完"的出牌路径权重分布。
// 枚举未知池可行的计数构成（Σ=n），对每个构成明举完整拆分的牌型多重集，
// 按超几何抽取概率加权聚合；返回 map[路径签名]权重（未归一化，比较用足够）。
func finishShapeWeights(unknown [rankCount]int, n int) map[shapeKey]float64 {
	out := make(map[shapeKey]float64, 32)
	forEachFinishComp(unknown, n, func(seen map[shapeKey]bool, w float64) {
		for k := range seen {
			out[k] += w
		}
	})
	return out
}

// leadShapeRisk 领出候选 ph 落入对手收尾路径的风险：
// 对手 n 张余牌的任意走完路径中，ph 的牌型正是其中一步（且长度匹配）的权重和。
// 顺子类还需 Length 相同才互压；这里按"对手该牌型组存在即可能被这一手衔接走完"计。
func leadShapeRisk(weights map[shapeKey]float64, ph rule.ParsedHand) float64 {
	risk := 0.0
	for k, w := range weights {
		if k.kinds[ph.Type] > 0 {
			risk += w
			continue
		}
		// 带牌组：主体牌型在三带/四带/飞机带翅路径中出现同样暴露收尾机会
		switch ph.Type {
		case rule.Trio:
			if k.kinds[rule.TrioWithSingle] > 0 || k.kinds[rule.TrioWithPair] > 0 ||
				k.kinds[rule.PlaneWithSingles] > 0 || k.kinds[rule.PlaneWithPairs] > 0 {
				risk += w
			}
		case rule.Pair:
			if k.kinds[rule.TrioWithPair] > 0 || k.kinds[rule.PlaneWithPairs] > 0 {
				risk += w
			}
		case rule.Single:
			if k.kinds[rule.TrioWithSingle] > 0 || k.kinds[rule.PlaneWithSingles] > 0 ||
				k.kinds[rule.FourWithTwo] > 0 {
				risk += w
			}
		}
	}
	return risk
}

// shapeBlockLead 形状死封领出（对手余牌 3~6 张，农民上家）：
// 领出候选中避开对手最可能收尾路径的牌型——按 leadShapeRisk 最小择优，
// 同风险出点数更大者（保留压制力）；候选须不拆炸弹/王炸、不增手数。
func shapeBlockLead(gctx GameContext) []card.Card {
	opp := opponentSeatCount(gctx) // 敌对座位余牌（上家视角=地主=下家）
	if opp < 3 || opp > shapeBlockMaxCards {
		return nil
	}
	hand := gctx.Hand
	unknown := unknownCounts(gctx.RemainingCards, hand)
	weights := finishShapeWeights(unknown, opp)
	if len(weights) == 0 {
		return nil
	}
	current := minHandCountWithCache(&gctx, handToCounts(hand))

	var best []card.Card
	bestRisk := math.Inf(1)
	bestKey := card.Rank(0)
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		if after > current-1 {
			continue // 死封不让自己的结构变差（一手必须省出手）
		}
		ph, err := rule.ParseHand(h.Cards)
		if err != nil {
			continue
		}
		risk := leadShapeRisk(weights, ph)
		if risk < bestRisk-1e-9 || (risk < bestRisk+1e-9 && ph.KeyRank > bestKey) {
			best, bestRisk, bestKey = h.Cards, risk, ph.KeyRank
		}
	}
	return best
}

// opponentSeatCount 敌对座位余牌数（农民=地主座位；地主取两农民较大者——
// 死封场景只针对最紧迫的临门对手）
func opponentSeatCount(gctx GameContext) int {
	if gctx.IsLandlord {
		if gctx.PlayerCounts[0] > gctx.PlayerCounts[1] {
			return gctx.PlayerCounts[0]
		}
		return gctx.PlayerCounts[1]
	}
	if gctx.UpIsLandlord {
		return gctx.PlayerCounts[0]
	}
	return gctx.PlayerCounts[1]
}

// allySeatCount 队友座位余牌数（农民视角：非地主座位；地主视角无队友，
// 返回余牌较少的农民座位作炸弹分布的"非敌对摊薄"项）
func allySeatCount(gctx GameContext) int {
	if gctx.IsLandlord {
		if gctx.PlayerCounts[0] < gctx.PlayerCounts[1] {
			return gctx.PlayerCounts[0]
		}
		return gctx.PlayerCounts[1]
	}
	if gctx.UpIsLandlord {
		return gctx.PlayerCounts[1] // 上家地主 → 队友是下家
	}
	return gctx.PlayerCounts[0] // 下家地主 → 队友是上家
}
