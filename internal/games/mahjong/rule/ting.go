package rule

// ting.go 听牌判定（含已鸣面子）：供托管/机器人"听牌不拆牌"保护策略使用。

// TenpaiWaits 返回手牌（连同 meldCount 个已鸣面子）的听牌集合：
// 枚举打出任一张后，再摸任意一张能胡的牌（去重后返回）。
// 非听牌返回空切片。复杂度 O(手牌张数 × 34) 次胡型检查，
// 仅在出牌决策时调用，开销可接受。
func TenpaiWaits(hand []int, meldCount int) []int {
	handLen := len(hand)
	if handLen == 0 {
		return nil
	}
	waitSet := make(map[int]bool, 8)
	seen := make(map[int]bool, handLen)
	for i, t := range hand {
		if seen[t] {
			continue
		}
		seen[t] = true
		rest := make([]int, 0, handLen-1)
		rest = append(rest, hand[:i]...)
		rest = append(rest, hand[i+1:]...)
		// 打出 t 后处于听牌态：存在某张 w 使 rest+w 成胡
		for w := 0; w < NumTypes; w++ {
			if CanWinFromDiscardWithMelds(rest, w, meldCount) {
				waitSet[w] = true
			}
		}
	}
	waits := make([]int, 0, len(waitSet))
	for w := range waitSet {
		waits = append(waits, w)
	}
	return waits
}

// IsTenpaiWithMelds 手牌（连同 meldCount 个面子）是否听牌
func IsTenpaiWithMelds(hand []int, meldCount int) bool {
	return len(TenpaiWaits(hand, meldCount)) > 0
}

// TenpaiOuts 听牌的和牌余量（记牌）：waits 中每张牌 4 张减去已可见张数之和。
// seen[t] 为牌 t 已出现在牌河/面子/自己手中的张数。余量越大，成胡可能性越高。
func TenpaiOuts(waits []int, seen []int) int {
	outs := 0
	for _, w := range waits {
		if w < 0 || w >= len(seen) {
			continue
		}
		r := 4 - seen[w]
		if r > 0 {
			outs += r
		}
	}
	return outs
}

// VisibleCounts 统计所有可见牌（各家牌河 + 各家面子 + 自己手牌）的各牌种张数
func VisibleCounts(discardPools [][]int, meldTiles [][]int, hand []int) []int {
	seen := make([]int, NumTypes)
	bump := func(tiles []int) {
		for _, t := range tiles {
			if t >= 0 && t < NumTypes {
				seen[t]++
			}
		}
	}
	for _, pool := range discardPools {
		bump(pool)
	}
	for _, tiles := range meldTiles {
		bump(tiles)
	}
	bump(hand)
	return seen
}
