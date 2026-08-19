package bot

import (
	"context"

	"xgames/internal/games/ddz/card"
)

// 叫分/加倍阈值：所有难度共用（难度差异只体现在记牌完整度，叫牌逻辑一致）
const (
	bidThreshold1   = 3.5 // 叫 1 分的手牌强度门槛
	bidThreshold2   = 5.5 // 叫 2 分的手牌强度门槛
	bidThreshold3   = 7.5 // 叫 3 分的手牌强度门槛
	doubleThreshold = 4.5 // 加倍的手牌强度门槛
)

// DecideBidScore 决定叫分：返回 0（不叫）或 1/2/3 分（按手牌强度选档，须大于 highBid）
func (e *Engine) DecideBidScore(_ context.Context, _ string, hand []card.Card, highBid int) int {
	return scoredBidScore(hand, highBid)
}

// DecideDouble 决定是否加倍：手牌强度达到阈值则加倍
func (e *Engine) DecideDouble(_ context.Context, _ string, hand []card.Card) bool {
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
