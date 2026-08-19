package rule

import (
	"slices"

	"xgames/internal/games/ddz/card"
)

// hasWinningBombOrRocket 检查是否有炸弹或王炸能压过对手的牌
func hasWinningBombOrRocket(analysis HandAnalysis, opponentHand ParsedHand) bool {
	// 检查王炸
	if analysis.counts[card.RankBlackJoker] >= 1 && analysis.counts[card.RankRedJoker] >= 1 {
		// 王炸压一切
		return true
	}

	// 检查炸弹
	for _, r := range analysis.fours {
		myBomb, _ := ParseHand([]card.Card{{Rank: r}, {Rank: r}, {Rank: r}, {Rank: r}})
		if CanBeat(myBomb, opponentHand) {
			return true
		}
	}
	return false
}

// findWinningSingle 检查是否有更大的单牌能压过对手
func findWinningSingle(analysis HandAnalysis, opponentHand ParsedHand) bool {
	for r := range analysis.counts {
		if r > opponentHand.KeyRank {
			return true
		}
	}
	return false
}

// findWinningPair 检查是否有更大的对子能压过对手
func findWinningPair(analysis HandAnalysis, opponentHand ParsedHand) bool {
	for r, count := range analysis.counts {
		if count >= 2 && r > opponentHand.KeyRank {
			return true
		}
	}
	return false
}

// findWinningTrio 检查是否有更大的三条（含带牌）能压过对手
// kickerType: 0=不带, 1=带单, 2=带对
func findWinningTrio(analysis HandAnalysis, opponentHand ParsedHand, kickerType int) bool {
	for r, count := range analysis.counts {
		if count >= 3 && r > opponentHand.KeyRank {
			// 找到更大的三条，检查是否有足够的带牌
			remainingCards := len(analysis.ones) + len(analysis.pairs)*2 + len(analysis.trios)*3 + len(analysis.fours)*4 - 3
			switch kickerType {
			case 0: // 不带牌
				return true
			case 1: // 带一张单牌
				if remainingCards >= 1 {
					return true
				}
			case 2: // 带一对
				if remainingCards < 2 {
					continue
				}
				// 检查剩余牌中是否有对子（其他对/三条/四条，或当前三条来自四条）
				if len(analysis.pairs) > 0 || len(analysis.trios) > 1 || len(analysis.fours) > 1 || (count == 4) {
					return true
				}
			}
		}
	}
	return false
}

// findWinningStraight 检查是否有更大的顺子能压过对手
func findWinningStraight(analysis HandAnalysis, opponentHand ParsedHand) bool {
	length := opponentHand.Length

	var availableRanks []card.Rank
	for r := range analysis.counts {
		if r < card.Rank2 { // 顺子不能包含2和王
			availableRanks = append(availableRanks, r)
		}
	}
	slices.Sort(availableRanks)

	if len(availableRanks) < length {
		return false
	}

	for i := 0; i <= len(availableRanks)-length; i++ {
		// 顺子比大小看窗口终点（最大点数）：窗口起点高于对方终点并不必要，
		// 如 45678 可压 34567；此前误用起点比较导致系统性漏判可管的顺子
		if isContinuousSequence(availableRanks, i, length) && availableRanks[i+length-1] > opponentHand.KeyRank {
			return true
		}
	}
	return false
}

// findWinningPairStraight 检查是否有更大的连对能压过对手
func findWinningPairStraight(analysis HandAnalysis, opponentHand ParsedHand) bool {
	length := opponentHand.Length

	var pairRanks []card.Rank
	for r, count := range analysis.counts {
		if count >= 2 && r < card.Rank2 {
			pairRanks = append(pairRanks, r)
		}
	}
	slices.Sort(pairRanks)

	if len(pairRanks) < length {
		return false
	}

	// 使用与 findWinningStraight 相同的滑动窗口逻辑（同样比较窗口终点）
	for i := 0; i <= len(pairRanks)-length; i++ {
		if isContinuousSequence(pairRanks, i, length) && pairRanks[i+length-1] > opponentHand.KeyRank {
			return true
		}
	}
	return false
}

// findWinningPlane 检查是否有更大的飞机（含带牌）能压过对手
// kickerType: 0=不带, 1=带单, 2=带对
func findWinningPlane(analysis HandAnalysis, opponentHand ParsedHand, kickerType int) bool {
	length := opponentHand.Length

	var trioRanks []card.Rank
	for r, count := range analysis.counts {
		if count >= 3 && r < card.Rank2 {
			trioRanks = append(trioRanks, r)
		}
	}
	slices.Sort(trioRanks)

	if len(trioRanks) < length {
		return false
	}

	for i := 0; i <= len(trioRanks)-length; i++ {
		// 检查连续序列（飞机主体）
		if !isContinuousSequence(trioRanks, i, length) {
			continue
		}

		// 检查点数是否更大（飞机主体比大小同样看窗口终点）
		if trioRanks[i+length-1] <= opponentHand.KeyRank {
			continue
		}

		// 检查带牌
		if checkKickers(analysis, trioRanks, i, length, kickerType) {
			return true
		}
	}
	return false
}

// isContinuousSequence 检查给定点数切片是否构成连续序列
func isContinuousSequence(ranks []card.Rank, startIndex, length int) bool {
	for j := 1; j < length; j++ {
		if ranks[startIndex+j-1]+1 != ranks[startIndex+j] {
			return false
		}
	}
	return true
}

// findWinningFourWithKickers 检查是否有更大的四带二（单/对）能压过对手
// kickerType: 1=带两张单牌, 2=带两对（两对须来自两个不同点数，与牌型解析口径一致）
func findWinningFourWithKickers(analysis HandAnalysis, opponentHand ParsedHand, kickerType int) bool {
	for r, count := range analysis.counts {
		if count < 4 || r <= opponentHand.KeyRank {
			continue
		}
		if kickerType == 1 {
			// 带两张任意单牌（含一对，如 AAAABB）：除主体外还剩 ≥2 张即可
			total := 0
			for _, cc := range analysis.counts {
				total += cc
			}
			if total-4 >= 2 {
				return true
			}
		} else {
			// 带两对：需要两个其他点数各能出一张对子
			pairRanks := 0
			for rr, cc := range analysis.counts {
				if rr != r && cc >= 2 {
					pairRanks++
				}
			}
			if pairRanks >= 2 {
				return true
			}
		}
	}
	return false
}

// checkKickers 检查飞机是否有足够的带牌
func checkKickers(analysis HandAnalysis, trioRanks []card.Rank, startIndex, length, kickerType int) bool {
	if kickerType == 0 {
		return true
	}

	totalCardsInHand := 0
	for _, c := range analysis.counts {
		totalCardsInHand += c
	}
	remainingCardCount := totalCardsInHand - (length * 3)

	switch kickerType {
	case 1: // 需要 N 张单牌
		return remainingCardCount >= length
	case 2: // 需要 N 对
		if remainingCardCount < length*2 {
			return false
		}

		startRank := trioRanks[startIndex]
		endRank := trioRanks[startIndex+length-1]

		kickerPairs := 0
		for r, count := range analysis.counts {
			// 跳过飞机主体的点数
			if r >= startRank && r <= endRank {
				continue
			}
			kickerPairs += count / 2
		}
		return kickerPairs >= length
	}
	return false
}
