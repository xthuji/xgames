package bot

import (
	"xgames/internal/games/ddz/card"
)

// rankCount 点数种类数：3~大王共 15 种。
// 手牌内部表示为一维计数数组，索引 0~14 分别映射面值 3~大王，数值为该点数剩余张数。
const rankCount = 15

// rankIdx 点数 → 计数数组下标（3→0 ... 大王→14）
func rankIdx(r card.Rank) int { return int(r) - int(card.Rank3) }

// idxRank 计数数组下标 → 点数
func idxRank(i int) card.Rank { return card.Rank(i + int(card.Rank3)) }

// handToCounts 手牌 → 计数数组
func handToCounts(hand []card.Card) [rankCount]int {
	var c [rankCount]int
	for _, cc := range hand {
		c[rankIdx(cc.Rank)]++
	}
	return c
}

// countsTotal 计数数组的总牌数
func countsTotal(c [rankCount]int) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

// countsToCards 计数数组 → 抽象手牌（Suit/Color 为零值，仅用于牌型解析与手数计算）
func countsToCards(c [rankCount]int) []card.Card {
	out := make([]card.Card, 0, countsTotal(c))
	for i, n := range c {
		for k := 0; k < n; k++ {
			out = append(out, card.Card{Rank: idxRank(i)})
		}
	}
	return out
}

// subtractCounts 从 a 中扣除 b（要求 b ⊆ a，逐位相减）
func subtractCounts(a, b [rankCount]int) [rankCount]int {
	var out [rankCount]int
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out
}

// unknownCounts 未知牌池：记牌视图减去自己手牌（对手两家合计可能持有的各点数张数）。
// 记牌视图中存在的点数按跟踪值计算；未跟踪的点数（视图中缺失）统一记为 -1，
// 表示"未知"——分析函数须按最保守假设处理（可能存在任意张）。
func unknownCounts(remaining map[card.Rank]int, hand []card.Card) [rankCount]int {
	var u [rankCount]int
	for i := range u {
		u[i] = -1 // 未跟踪 = 未知
	}
	for r, n := range remaining {
		u[rankIdx(r)] = n
	}
	return subtractCounts(u, handToCounts(hand))
}

// potentialBombRanks 潜在炸弹预警：未知牌池中某点数剩余 4 张，
// 说明对手方持有该点数的全部 4 张，存在成炸可能，出牌计划须保留应对预案。
// 仅已跟踪的点数可能触发预警（未跟踪点数为 -1，不会误报）。
func potentialBombRanks(unknown [rankCount]int) []card.Rank {
	var ranks []card.Rank
	for i := 0; i < rankCount-2; i++ { // 2 和王各不足 4 张，无需检查
		if unknown[i] == 4 {
			ranks = append(ranks, idxRank(i))
		}
	}
	return ranks
}

// isAbsoluteControl 绝对控制牌判断：打出点数 r 的单张（need=1）或对子（need=2）后，
// 未知牌池中不存在更大的同路牌可压（忽略对手炸弹/火箭的极端情况）。
// 用于评估"打出后能强制收回出牌权"；未跟踪的更大点数按可能存在保守处理。
func isAbsoluteControl(unknown [rankCount]int, r card.Rank, need int) bool {
	if unknown[rankIdx(r)] < need {
		return false
	}
	for i := rankIdx(r) + 1; i < rankCount; i++ {
		if unknown[i] != 0 { // >0 有更大牌；-1 未跟踪同样视为不安全
			return false
		}
	}
	return true
}

// isKeyRank 是否关键牌（A/2/大小王）：普通难度的记牌范围仅覆盖这些点数，
// 专家难度跟踪全部点数。
func isKeyRank(r card.Rank) bool {
	return r == card.RankA || r == card.Rank2 ||
		r == card.RankBlackJoker || r == card.RankRedJoker
}

// isBigCardSafe 大牌绝对安全判断：基于记牌视图中的关键牌，
// 打出点数 r 后，不存在更大的关键牌仍在对手手中。
// remaining 为记牌视图（含自己手牌），hand 用于扣除自己持有的部分；
// 未跟踪的关键牌按可能存在保守处理。
func isBigCardSafe(remaining map[card.Rank]int, hand []card.Card, r card.Rank) bool {
	unknown := unknownCounts(remaining, hand)
	for kr := r + 1; kr <= card.RankRedJoker; kr++ {
		if isKeyRank(kr) && unknown[rankIdx(kr)] != 0 {
			return false
		}
	}
	return true
}
