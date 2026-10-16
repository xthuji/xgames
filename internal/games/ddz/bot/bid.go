package bot

import (
	"context"
	"math"

	"xgames/internal/games/ddz/card"
)

// 叫分/加倍阈值：所有难度共用（难度差异只体现在记牌完整度，叫牌逻辑一致）
const (
	bidThreshold1   = 3.5 // 叫 1 分的手牌强度门槛
	bidThreshold2   = 5.5 // 叫 2 分的手牌强度门槛
	bidThreshold3   = 7.5 // 叫 3 分的手牌强度门槛
	doubleThreshold = 4.5 // 加倍的手牌强度门槛
)

// Phase 5 叫分/加倍模型（EnableBidModel 门控，基线阈值不变）：
// 在基线评分上叠加控制牌因子，炸弹计分非线性，连子按断点修复成本给半手潜力；
// 弃用“强度过X即加倍”的单一阈值，改按手数/牌权/炸弹的实战判据。
const (
	bidControlWeight  = 0.6 // 每组绝对控制牌的加分（能独立收回一次牌权）
	bidBombFirst      = 3.0 // 第一个炸弹（与基线一致）
	bidBombExtra      = 4.0 // 第二个及以后炸弹：两手并一手，兑现确定性非线性
	bidGapBonus       = 0.5 // 4 连点缺 1 张的断点修复（按 0.5 手计）
	bidGapBonusCap    = 1.0 // 断点修复总分上限
	bidDoubleMaxHands = 4   // 加倍手数上限（能一波走才敢加倍）
)

// 新模型叫分阈值：强度分布因控制牌/炸弹非线性整体上移（17 张随机手牌
// p50 从 6.1 抬到 7.4），阈值按 6 万手随机发牌的 V2 强度分位标定到人工口径
// （p45=7.1 / p75=8.8 / p90=11.8 → 不叫≈45% 叫 1≈30% 叫 2≈15% 叫 3≈10%，
// 基线口径为 0.7% / 32.2% / 45.1% / 22.1%，几乎从不让牌）
const (
	bidThreshold1V2 = 7.1  // 新模型叫 1 分门槛（≈p45）
	bidThreshold2V2 = 8.8  // 新模型叫 2 分门槛（≈p75）
	bidThreshold3V2 = 11.8 // 新模型叫 3 分门槛（≈p90）
)

// DecideBidScore 决定叫分：返回 0（不叫）或 1/2/3 分（按手牌强度选档，须大于 highBid）
func (e *Engine) DecideBidScore(_ context.Context, _ string, hand []card.Card, highBid int) int {
	if e.Config.EnableBidModel {
		return scoredBidScoreV2(hand, highBid)
	}
	return scoredBidScore(hand, highBid)
}

// DecideDouble 决定是否加倍：基线看手牌强度阈值；
// EnableBidModel 下改按“手数/预期牌权/炸弹数”的真人实战判据
func (e *Engine) DecideDouble(_ context.Context, _ string, hand []card.Card) bool {
	if e.Config.EnableBidModel {
		return shouldDoubleV2(hand)
	}
	return handStrength(hand) >= doubleThreshold
}

// handStrength 手牌强度评分：大牌、炸弹加权 + 结构因子（顺子/连对/飞机加分，2/王孤张减分）
func handStrength(hand []card.Card) float64 {
	score := 0.0
	rankCounts := make(map[card.Rank]int)
	for _, c := range hand {
		rankCounts[c.Rank]++
	}
	
	// 基础分：大牌和炸弹
	for rank, count := range rankCounts {
		if count == 4 {
			score += 3 // 炸弹
		}
		switch rank {
		case card.RankRedJoker:
			score += 2
		case card.RankBlackJoker:
			score += 1.5
		case card.Rank2:
			score += 1
		case card.RankA:
			score += 0.5
		}
	}
	
	// 结构加分：检测成型牌型
	score += structureBonus(hand, rankCounts)
	
	// 结构减分：2/王孤张惩罚
	score -= isolatedPenalty(rankCounts)
	
	return score
}

// structureBonus 结构加分：顺子/连对/飞机等成型牌型
func structureBonus(hand []card.Card, rankCounts map[card.Rank]int) float64 {
	bonus := 0.0
	
	// 使用 MinHandCount 估算最少出牌手数，手数越少说明结构越好
	currentCount := MinHandCount(handToCounts(hand))
	maxPossible := len(hand) // 最差情况：全部单张
	
	// 手数优势：相比全单张节省的出牌次数
	saved := maxPossible - currentCount
	if saved > 0 {
		// 每节省一手加 0.3 分（反映牌型整合度）
		bonus += float64(saved) * 0.3
	}
	
	// 额外奖励：检测是否有长顺子/连对/飞机（批量出牌能力）
	// 简单启发式：如果某花色连续点数较多，额外加分
	bonus += consecutiveBonus(rankCounts)
	
	return bonus
}

// consecutiveBonus 连续点数加分：反映顺子/连对潜力
func consecutiveBonus(rankCounts map[card.Rank]int) float64 {
	bonus := 0.0
	
	// 检查数牌区域（3~A，排除 2 和王）
	var consecutiveStreak int
	maxStreak := 0
	
	for r := card.Rank3; r <= card.RankA; r++ {
		if rankCounts[r] > 0 {
			consecutiveStreak++
			if consecutiveStreak > maxStreak {
				maxStreak = consecutiveStreak
			}
		} else {
			consecutiveStreak = 0
		}
	}
	
	// 最长连续 ≥5：有顺子潜力
	if maxStreak >= 5 {
		bonus += 1.0 // 顺子潜力加分
	}
	// 最长连续 ≥10：有很好的连对/顺子组合
	if maxStreak >= 10 {
		bonus += 1.5 // 额外奖励
	}
	
	return bonus
}

// isolatedPenalty 2/王孤张惩罚：有大牌但无配套牌型时减分（仅在完整手牌中生效，避免单卡测试误伤）
func isolatedPenalty(rankCounts map[card.Rank]int) float64 {
	penalty := 0.0
	
	// 统计总牌数，手牌过少时不应用惩罚（避免单卡/少数牌误判）
	totalCards := 0
	for _, count := range rankCounts {
		totalCards += count
	}
	if totalCards < 5 {
		return 0 // 手牌太少，孤张概念不适用
	}
	
	// 检查 2 是否孤张（只有单张 2，无对子/刻子，且周围无 A/3 形成顺子）
	if count, ok := rankCounts[card.Rank2]; ok && count == 1 {
		// 2 是单张，检查是否有 A 或 3 形成潜在顺子
		hasNeighbor := rankCounts[card.RankA] > 0 || rankCounts[card.Rank3] > 0
		if !hasNeighbor {
			penalty += 0.5 // 孤张 2 减分
		}
	}
	
	// 检查王是否孤张（只有单王，无火箭）
	jokerCount := 0
	if _, ok := rankCounts[card.RankBlackJoker]; ok {
		jokerCount++
	}
	if _, ok := rankCounts[card.RankRedJoker]; ok {
		jokerCount++
	}
	if jokerCount == 1 {
		// 单王，检查是否有 2 形成压制链
		hasTwo := rankCounts[card.Rank2] > 0
		if !hasTwo {
			penalty += 0.3 // 单王无 2 配合，减分
		}
	}
	
	return penalty
}

// scoredBidScore 启发式叫分决策：按手牌强度选档位，返回大于 highBid 的目标分或 0
func scoredBidScore(hand []card.Card, highBid int) int {
	s := handStrength(hand)

	want := 0
	switch {
	case s >= bidThreshold3:
		want = 3
	case s >= bidThreshold2:
		want = 2
	case s >= bidThreshold1:
		want = 1
	}
	if want <= highBid {
		return 0
	}
	return want
}

// --- Phase 5：叫分/加倍模型（EnableBidModel 门控）---

// rankCountsOf 手牌按点数计数
func rankCountsOf(hand []card.Card) map[card.Rank]int {
	m := make(map[card.Rank]int, len(hand))
	for _, c := range hand {
		m[c.Rank]++
	}
	return m
}

// bombGroups 炸弹组数（同点 4 张 + 王炸）
func bombGroups(counts map[card.Rank]int) int {
	n := 0
	for r := card.Rank3; r <= card.Rank2; r++ {
		if counts[r] == 4 {
			n++
		}
	}
	if counts[card.RankRedJoker] > 0 && counts[card.RankBlackJoker] > 0 {
		n++
	}
	return n
}

// bombScoreV2 炸弹计分非线性：第 1 个 +3，第 2 个起 +4
//（多炸把手数从“两手”压成“一手半”，兑现确定性的边际收益递增）
func bombScoreV2(counts map[card.Rank]int) float64 {
	n := bombGroups(counts)
	if n == 0 {
		return 0
	}
	return bidBombFirst + float64(n-1)*bidBombExtra
}

// absoluteControls 绝对控制组数：叫分阶段牌局未开，只能按点数阶判定
// “能独立收回一次牌权”的组数——大王 1 组；双王成炸再记 1 组；
// 单小王 0.5 组（可被大王压）；2 按张数计（最多 3 组：单/对/三各收一次）；
// 自占 3 张 2 时 A 升级为场上最大同路记 1 组。
func absoluteControls(counts map[card.Rank]int) float64 {
	hasBig := counts[card.RankRedJoker] > 0
	c := 0.0
	if hasBig {
		c++
	}
	switch {
	case counts[card.RankBlackJoker] > 0 && hasBig:
		c++ // 王炸：一手清空且压一切
	case counts[card.RankBlackJoker] > 0:
		c += 0.5
	}
	if n := counts[card.Rank2]; n > 0 {
		c += math.Min(float64(n), 3)
	}
	if counts[card.RankA] > 0 && counts[card.Rank2] >= 3 {
		c++
	}
	return c
}

// gapBonus 断点修复成本：一段极大连子恰好 4 张（如 9 10 J Q，两端缺 8/K）时，
// 摸到一张即可多抖一手，按 0.5 手计（总分上限 1.0）——基线只奖励已成型的
// 最长连子，不反映“差一张就能多走一手”的结构潜力。
// 只看极大段（内部空洞拆成两段短连子，不算“补一张成顺”，也不重复计数）。
func gapBonus(counts map[card.Rank]int) float64 {
	bonus := 0.0
	for r := card.Rank3; r <= card.RankA; r++ {
		if counts[r] == 0 {
			continue
		}
		if r > card.Rank3 && counts[r-1] > 0 {
			continue // 非极大连子起点
		}
		runLen := 0
		for k := r; k <= card.RankA && counts[k] > 0; k++ {
			runLen++
		}
		if runLen == 4 {
			bonus += bidGapBonus
		}
	}
	return math.Min(bonus, bidGapBonusCap)
}

// handStrengthV2 Phase 5 手牌强度：基线口径（大牌分/结构加分/孤张减分，
// 炸弹不再平计 +3）+ 炸弹非线性 + 绝对控制组数 + 断点修复半手潜力
func handStrengthV2(hand []card.Card) float64 {
	counts := rankCountsOf(hand)
	score := 0.0
	for rank := range counts {
		switch rank {
		case card.RankRedJoker:
			score += 2
		case card.RankBlackJoker:
			score += 1.5
		case card.Rank2:
			score += 1
		case card.RankA:
			score += 0.5
		}
	}
	score += bombScoreV2(counts) // 炸弹改走非线性（第 1 个 +3，后续 +4）
	score += bidControlWeight * absoluteControls(counts)
	score += structureBonus(hand, counts)
	score += gapBonus(counts)
	score -= isolatedPenalty(counts)
	return score
}

// scoredBidScoreV2 新模型叫分：选档口径与基线一致，强度与阈值换成 V2 口径
func scoredBidScoreV2(hand []card.Card, highBid int) int {
	s := handStrengthV2(hand)

	want := 0
	switch {
	case s >= bidThreshold3V2:
		want = 3
	case s >= bidThreshold2V2:
		want = 2
	case s >= bidThreshold1V2:
		want = 1
	}
	if want <= highBid {
		return 0
	}
	return want
}

// shouldDoubleV2 加倍判据（真人口径）：手里 ≥2 个炸弹（含王炸）直接加；
// 否则要求“能一波走”——手数 ≤ 4 且绝对控制牌组数能盖住手数-1
//（每一手都有牌权回收，才敢把分翻倍）
func shouldDoubleV2(hand []card.Card) bool {
	counts := rankCountsOf(hand)
	if bombGroups(counts) >= 2 {
		return true
	}
	hands := MinHandCount(handToCounts(hand))
	if hands > bidDoubleMaxHands {
		return false
	}
	return absoluteControls(counts) >= float64(hands-1)
}
