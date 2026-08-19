package rule

import (
	"sort"

	"xgames/internal/games/ddz/card"
)

// Hint 一条出牌建议（含评分，Score 越低越优先）
type Hint struct {
	Cards []card.Card
	Score int
}

// GenerateHints 生成所有合法出牌建议并按推荐度排序（Score 升序）。
// lastParsed 为空（IsEmpty）表示领出，否则为跟牌。
func GenerateHints(hand []card.Card, lastParsed ParsedHand) []Hint {
	var hints []Hint
	if lastParsed.IsEmpty() {
		hints = generateLeadHints(hand)
	} else {
		hints = generateFollowHints(hand, lastParsed)
	}
	sort.SliceStable(hints, func(i, j int) bool { return hints[i].Score < hints[j].Score })
	return hints
}

// --- 评分 ---

func scorePlay(cards []card.Card) int {
	sum := 0
	for _, c := range cards {
		sum += int(c.Rank)
	}
	ph, err := ParseHand(cards)
	if err != nil {
		return sum
	}
	switch ph.Type {
	case Bomb:
		sum += 30
	case Rocket:
		sum += 40
	}
	if len(cards) >= 2 {
		sum -= len(cards) * 2
	}
	return sum
}

// --- 领出（新一轮） ---

func generateLeadHints(hand []card.Card) []Hint {
	cardsByRank := groupByRank(hand)
	var hints []Hint

	// 单张
	for _, cs := range cardsByRank {
		hints = append(hints, Hint{Cards: cs[:1], Score: scorePlay(cs[:1])})
	}
	// 对子
	for _, cs := range cardsByRank {
		if len(cs) >= 2 {
			c := copyCards(cs[:2])
			hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
		}
	}
	// 三张 / 三带一 / 三带二
	for _, cs := range cardsByRank {
		if len(cs) >= 3 {
			trio := copyCards(cs[:3])
			hints = append(hints, Hint{Cards: trio, Score: scorePlay(trio)})
			if k := pickKickerByCount(hand, cs[:3], 1); k != nil {
				c := append(copyCards(trio), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
			if k := pickKickerByCount(hand, cs[:3], 2); k != nil {
				c := append(copyCards(trio), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	// 炸弹（含四带二、四带两对）
	for _, cs := range cardsByRank {
		if len(cs) >= 4 {
			four := copyCards(cs[:4])
			hints = append(hints, Hint{Cards: four, Score: scorePlay(four)})
			remaining := excludeCards(hand, four)
			if k := pickN(remaining, nil, 2); k != nil {
				c := append(copyCards(four), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
			if k := pickPairs(remaining, nil, 2); k != nil {
				c := append(copyCards(four), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	// 火箭
	if jokers := findJokers(hand); jokers != nil {
		hints = append(hints, Hint{Cards: jokers, Score: scorePlay(jokers)})
	}
	// 顺子
	hints = append(hints, genStraights(hand, cardsByRank)...)
	// 连对
	hints = append(hints, genPairStraights(hand, cardsByRank)...)
	// 飞机（不带 / 带单 / 带对）
	hints = append(hints, genPlanes(hand, cardsByRank)...)
	return hints
}

// --- 跟牌 ---

func generateFollowHints(hand []card.Card, target ParsedHand) []Hint {
	var hints []Hint
	cardsByRank := groupByRank(hand)

	switch target.Type {
	case Single:
		hints = append(hints, followSingle(hand, cardsByRank, target)...)
	case Pair:
		hints = append(hints, followPair(hand, cardsByRank, target)...)
	case Trio:
		hints = append(hints, followTrio(hand, cardsByRank, target, 0)...)
	case TrioWithSingle:
		hints = append(hints, followTrio(hand, cardsByRank, target, 1)...)
	case TrioWithPair:
		hints = append(hints, followTrio(hand, cardsByRank, target, 2)...)
	case Straight:
		hints = append(hints, followStraight(hand, cardsByRank, target)...)
	case PairStraight:
		hints = append(hints, followPairStraight(hand, cardsByRank, target)...)
	case Plane:
		hints = append(hints, followPlane(hand, cardsByRank, target, 0)...)
	case PlaneWithSingles:
		hints = append(hints, followPlane(hand, cardsByRank, target, 1)...)
	case PlaneWithPairs:
		hints = append(hints, followPlane(hand, cardsByRank, target, 2)...)
	case FourWithTwo, FourWithTwoPairs:
		// 四带X 只能被炸弹/火箭压
	}
	// 炸弹
	if target.Type != Bomb {
		for _, cs := range cardsByRank {
			if len(cs) >= 4 {
				c := copyCards(cs[:4])
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	} else {
		for r, cs := range cardsByRank {
			if len(cs) >= 4 && r > target.KeyRank {
				c := copyCards(cs[:4])
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	// 火箭
	if target.Type != Rocket {
		if jokers := findJokers(hand); jokers != nil {
			hints = append(hints, Hint{Cards: jokers, Score: scorePlay(jokers)})
		}
	}
	return hints
}

func followSingle(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand) []Hint {
	var h []Hint
	for r, cs := range cbr {
		if r > t.KeyRank {
			h = append(h, Hint{Cards: cs[:1], Score: scorePlay(cs[:1])})
			if len(cs) >= 2 {
				c := copyCards(cs[:2])
				h = append(h, Hint{Cards: c, Score: scorePlay(c)})
			}
			if len(cs) >= 3 {
				trio := copyCards(cs[:3])
				h = append(h, Hint{Cards: trio, Score: scorePlay(trio)})
				if k := pickKickerByCount(hand, cs[:3], 1); k != nil {
					h = append(h, Hint{Cards: append(trio, k...), Score: scorePlay(append(trio, k...))})
				}
				if k := pickKickerByCount(hand, cs[:3], 2); k != nil {
					h = append(h, Hint{Cards: append(copyCards(trio), k...), Score: scorePlay(append(copyCards(trio), k...))})
				}
			}
		}
	}
	return h
}

func followPair(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand) []Hint {
	var h []Hint
	for r, cs := range cbr {
		if r > t.KeyRank && len(cs) >= 2 {
			c := copyCards(cs[:2])
			h = append(h, Hint{Cards: c, Score: scorePlay(c)})
		}
	}
	return h
}

func followTrio(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand, kickerType int) []Hint {
	var h []Hint
	for r, cs := range cbr {
		if r > t.KeyRank && len(cs) >= 3 {
			trio := copyCards(cs[:3])
			if kickerType == 0 {
				h = append(h, Hint{Cards: trio, Score: scorePlay(trio)})
			} else {
				if k := pickKickerByCount(hand, cs[:3], kickerType); k != nil {
					c := append(copyCards(trio), k...)
					h = append(h, Hint{Cards: c, Score: scorePlay(c)})
				}
			}
		}
	}
	return h
}

func followStraight(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand) []Hint {
	var h []Hint
	// 收集所有在手牌中存在的点数，拆分为连续片段以支持"拆牌型跟顺子"
	allRanks := consecutiveRanksFrom(cbr, card.Rank3, card.RankA)
	runs := splitIntoConsecutiveRuns(allRanks)
	for _, run := range runs {
		for _, seq := range subsequences(run, t.Length) {
			if seq[len(seq)-1] > t.KeyRank {
				c := takeOnePerRank(cbr, seq)
				h = append(h, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	return h
}

func followPairStraight(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand) []Hint {
	var h []Hint
	consec := consecutiveRanksWithMin(cbr, card.Rank3, card.RankA, 2)
	pairLen := t.Length
	for _, seq := range subsequences(consec, pairLen) {
		if seq[len(seq)-1] > t.KeyRank {
			c := takeNPerRank(cbr, seq, 2)
			h = append(h, Hint{Cards: c, Score: scorePlay(c)})
		}
	}
	return h
}

func followPlane(hand []card.Card, cbr map[card.Rank][]card.Card, t ParsedHand, kickerType int) []Hint {
	var h []Hint
	trioRanks := consecutiveRanksWithMin(cbr, card.Rank3, card.RankA, 3)
	planeLen := t.Length
	for _, seq := range subsequences(trioRanks, planeLen) {
		if seq[len(seq)-1] > t.KeyRank {
			base := takeNPerRank(cbr, seq, 3)
			if kickerType == 0 {
				h = append(h, Hint{Cards: base, Score: scorePlay(base)})
			} else {
				used := rankSet(seq)
				if k := pickKickersExcluding(hand, used, kickerType*planeLen); k != nil {
					c := append(copyCards(base), k...)
					h = append(h, Hint{Cards: c, Score: scorePlay(c)})
				}
			}
		}
	}
	return h
}

// --- 领出复合牌型 ---

func genStraights(hand []card.Card, cbr map[card.Rank][]card.Card) []Hint {
	var hints []Hint
	// 收集所有在手牌中存在的点数，拆分为连续片段以生成合法顺子
	allRanks := consecutiveRanksFrom(cbr, card.Rank3, card.RankA)
	runs := splitIntoConsecutiveRuns(allRanks)
	for _, run := range runs {
		maxLen := len(run)
		for length := maxLen; length >= 5; length-- {
			for _, seq := range subsequences(run, length) {
				c := takeOnePerRank(cbr, seq)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	return hints
}

func genPairStraights(hand []card.Card, cbr map[card.Rank][]card.Card) []Hint {
	var hints []Hint
	consec := consecutiveRanksWithMin(cbr, card.Rank3, card.RankA, 2)
	maxLen := len(consec)
	for length := maxLen; length >= 3; length-- {
		for _, seq := range subsequences(consec, length) {
			c := takeNPerRank(cbr, seq, 2)
			hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
		}
	}
	return hints
}

func genPlanes(hand []card.Card, cbr map[card.Rank][]card.Card) []Hint {
	var hints []Hint
	trioRanks := consecutiveRanksWithMin(cbr, card.Rank3, card.RankA, 3)
	maxLen := len(trioRanks)
	for length := maxLen; length >= 2; length-- {
		for _, seq := range subsequences(trioRanks, length) {
			base := takeNPerRank(cbr, seq, 3)
			hints = append(hints, Hint{Cards: base, Score: scorePlay(base)})
			used := rankSet(seq)
			if k := pickKickersExcluding(hand, used, length); k != nil {
				c := append(copyCards(base), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
			if k := pickKickersExcludingPairs(hand, used, length); k != nil {
				c := append(copyCards(base), k...)
				hints = append(hints, Hint{Cards: c, Score: scorePlay(c)})
			}
		}
	}
	return hints
}

// --- 辅助函数 ---

func groupByRank(hand []card.Card) map[card.Rank][]card.Card {
	m := make(map[card.Rank][]card.Card)
	for _, c := range hand {
		m[c.Rank] = append(m[c.Rank], c)
	}
	return m
}

func copyCards(cs []card.Card) []card.Card {
	r := make([]card.Card, len(cs))
	copy(r, cs)
	return r
}

func findJokers(hand []card.Card) []card.Card {
	var jokers []card.Card
	for _, c := range hand {
		if c.Rank == card.RankBlackJoker || c.Rank == card.RankRedJoker {
			jokers = append(jokers, c)
		}
	}
	if len(jokers) == 2 {
		return jokers
	}
	return nil
}

// consecutiveRanksFrom 找到 [lo, hi] 范围内所有在手牌中至少出现 1 次的点数，
// 返回所有可能的连续子序列（用于支持"拆牌型跟顺子"的场景）。
// 例如手牌有 3,4,5,7,8,9,10,J,Q,K（缺 6），目标顺子长度 5，
// 此函数会返回 [[3,4,5],[7,8,9,10,J],[8,9,10,J,Q],[9,10,J,Q,K]] 等片段，
// followStraight 再从中筛选出能压过目标的候选。
func consecutiveRanksFrom(cbr map[card.Rank][]card.Card, lo, hi card.Rank) []card.Rank {
	var all []card.Rank
	for r := lo; r <= hi; r++ {
		if len(cbr[r]) > 0 {
			all = append(all, r)
		}
	}
	return all
}

// splitIntoConsecutiveRuns 将一个可能含断点的有序点数列表拆分为多个连续片段。
// 例如 [3,4,5,7,8,9,10,J,Q,K] → [[3,4,5],[7,8,9,10,J,Q,K]]
func splitIntoConsecutiveRuns(sorted []card.Rank) [][]card.Rank {
	if len(sorted) == 0 {
		return nil
	}
	var runs [][]card.Rank
	current := []card.Rank{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1]+1 {
			current = append(current, sorted[i])
		} else {
			if len(current) >= 2 { // 只保留长度 ≥2 的片段（单张不构成顺子基础）
				runs = append(runs, current)
			}
			current = []card.Rank{sorted[i]}
		}
	}
	if len(current) >= 2 {
		runs = append(runs, current)
	}
	return runs
}

// consecutiveRanksWithMin 找到连续点数序列，每个点数至少有 minCount 张
func consecutiveRanksWithMin(cbr map[card.Rank][]card.Card, lo, hi card.Rank, minCount int) []card.Rank {
	var seq []card.Rank
	for r := lo; r <= hi; r++ {
		if len(cbr[r]) >= minCount {
			seq = append(seq, r)
		} else if len(seq) > 0 {
			break
		}
	}
	return seq
}

// subsequences 从连续序列中提取所有长度为 length 的连续子序列
func subsequences(sorted []card.Rank, length int) [][]card.Rank {
	if len(sorted) < length {
		return nil
	}
	var result [][]card.Rank
	for i := 0; i <= len(sorted)-length; i++ {
		sub := make([]card.Rank, length)
		copy(sub, sorted[i:i+length])
		result = append(result, sub)
	}
	return result
}

func takeOnePerRank(cbr map[card.Rank][]card.Card, ranks []card.Rank) []card.Card {
	var cs []card.Card
	for _, r := range ranks {
		cs = append(cs, cbr[r][0])
	}
	return cs
}

func takeNPerRank(cbr map[card.Rank][]card.Card, ranks []card.Rank, n int) []card.Card {
	var cs []card.Card
	for _, r := range ranks {
		cs = append(cs, cbr[r][:n]...)
	}
	return cs
}

func rankSet(ranks []card.Rank) map[card.Rank]bool {
	m := make(map[card.Rank]bool, len(ranks))
	for _, r := range ranks {
		m[r] = true
	}
	return m
}

// pickKickerByCount 从手牌中选最小面值且不拆结构的带牌（排除 exclude 中的牌），
// count=1 取单张，count=2 取对子。带牌优先级：
//   - 单张带牌：现成单张 > 拆对子 > 拆三张 > 拆炸弹（兜底保证候选合法）
//   - 对子带牌：现成对子 > 拆三张 > 拆炸弹（兜底）
//
// 同级内按点数升序取最小；若首选结果含掌权王牌（Q/K/A/2/王），
// 改拆更小牌的结构带走小牌，把大牌留作后手夺权（炸弹仍不拆）。
func pickKickerByCount(hand []card.Card, exclude []card.Card, count int) []card.Card {
	excluded := make(map[int]bool)
	for _, ec := range exclude {
		for i, c := range hand {
			if c == ec {
				excluded[i] = true
				break
			}
		}
	}
	remaining := groupByRankFiltered(hand, excluded)
	// 带牌不能与主体同点数（exclude 已含主体全部牌，这里双保险）
	for _, ec := range exclude {
		delete(remaining, ec.Rank)
	}

	if count == 2 {
		return pickPairsFrom(remaining, 1)
	}

	// 取单张带牌（王也按面值参与比较）
	picked := pickSinglesFrom(remaining, 1)
	if picked == nil {
		return nil
	}
	return picked
}

// pickSinglesFrom 从分组手牌中选 n 张不同点数的最小带牌：
// 现成单张 > 拆对子 > 拆三张 > 拆炸弹（兜底保证候选合法），同级按点数升序。
// 若首选结果含掌权王牌（Q/K/A/2/王），改用点数升序拆非炸弹组的小牌：
// 便于带走小牌、用大牌再次夺取牌权，避免浪费掌权王牌。
func pickSinglesFrom(remaining map[card.Rank][]card.Card, n int) []card.Card {
	picked := pickSinglesByStructure(remaining, n)
	if len(picked) < n || !containsControlRank(picked) {
		return picked
	}
	if alt := pickSmallestSingles(remaining, n); alt != nil && sumCardRank(alt) < sumCardRank(picked) {
		return alt
	}
	return picked
}

// pickSinglesByStructure 带牌结构优先级：现成单张 > 拆对子 > 拆三张 > 拆炸弹，同级点数升序
func pickSinglesByStructure(remaining map[card.Rank][]card.Card, n int) []card.Card {
	var picked []card.Card
	for _, want := range []int{1, 2, 3, 4} {
		for r := card.Rank3; r <= card.RankRedJoker && len(picked) < n; r++ {
			if len(remaining[r]) == want {
				picked = append(picked, remaining[r][0])
			}
		}
	}
	if len(picked) < n {
		return nil
	}
	return picked
}

// pickSmallestSingles 按点数升序从非炸弹组（1~3 张）各拆 1 张，凑 n 张最小带牌
func pickSmallestSingles(remaining map[card.Rank][]card.Card, n int) []card.Card {
	var picked []card.Card
	for r := card.Rank3; r <= card.RankRedJoker && len(picked) < n; r++ {
		if c := len(remaining[r]); c >= 1 && c <= 3 {
			picked = append(picked, remaining[r][0])
		}
	}
	if len(picked) < n {
		return nil
	}
	return picked
}

// pickPairsFrom 从分组手牌中选 pairCount 对最小带牌对：
// 现成对子 > 拆三张 > 拆炸弹（兜底），同级按点数升序。
// 若首选结果含掌权王牌，改拆更小牌的结构带走小牌（炸弹仍不拆）。
func pickPairsFrom(remaining map[card.Rank][]card.Card, pairCount int) []card.Card {
	picked := pickPairsByStructure(remaining, pairCount)
	if len(picked) < pairCount*2 || !containsControlRank(picked) {
		return picked
	}
	if alt := pickSmallestPairs(remaining, pairCount); alt != nil && sumCardRank(alt) < sumCardRank(picked) {
		return alt
	}
	return picked
}

// pickPairsByStructure 带牌对结构优先级：现成对子 > 拆三张 > 拆炸弹，同级点数升序
func pickPairsByStructure(remaining map[card.Rank][]card.Card, pairCount int) []card.Card {
	var picked []card.Card
	for _, want := range []int{2, 3, 4} {
		for r := card.Rank3; r <= card.RankRedJoker && len(picked) < pairCount*2; r++ {
			if len(remaining[r]) == want {
				picked = append(picked, copyCards(remaining[r][:2])...)
			}
		}
	}
	if len(picked) < pairCount*2 {
		return nil
	}
	return picked
}

// pickSmallestPairs 按点数升序从非炸弹组（2~3 张）拆出最小带牌对
func pickSmallestPairs(remaining map[card.Rank][]card.Card, pairCount int) []card.Card {
	var picked []card.Card
	for r := card.Rank3; r <= card.RankRedJoker && len(picked) < pairCount*2; r++ {
		if c := len(remaining[r]); c >= 2 && c <= 3 {
			picked = append(picked, copyCards(remaining[r][:2])...)
		}
	}
	if len(picked) < pairCount*2 {
		return nil
	}
	return picked
}

// containsControlRank 是否含掌权王牌（Q/K/A/2，含双王）
func containsControlRank(cs []card.Card) bool {
	for _, c := range cs {
		if c.Rank >= card.RankQ {
			return true
		}
	}
	return false
}

// sumCardRank 带牌面值和（用于比较两种带牌方案的大小）
func sumCardRank(cs []card.Card) int {
	s := 0
	for _, c := range cs {
		s += int(c.Rank)
	}
	return s
}

// groupByRankFiltered 按点数分组，排除指定索引的牌
func groupByRankFiltered(hand []card.Card, excluded map[int]bool) map[card.Rank][]card.Card {
	m := make(map[card.Rank][]card.Card)
	for i, c := range hand {
		if !excluded[i] {
			m[c.Rank] = append(m[c.Rank], c)
		}
	}
	return m
}

// pickKickersExcluding 从手牌中选 n 张最小单张带牌（排除 usedRanks 中的点数）
func pickKickersExcluding(hand []card.Card, usedRanks map[card.Rank]bool, n int) []card.Card {
	remaining := groupByRank(hand)
	for r := range usedRanks {
		delete(remaining, r)
	}
	return pickSinglesFrom(remaining, n)
}

// pickKickersExcludingPairs 从手牌中选 pairCount 对最小带牌对（排除 usedRanks 中的点数）
func pickKickersExcludingPairs(hand []card.Card, usedRanks map[card.Rank]bool, pairCount int) []card.Card {
	remaining := groupByRank(hand)
	for r := range usedRanks {
		delete(remaining, r)
	}
	return pickPairsFrom(remaining, pairCount)
}

// pickN 从手牌中（排除 already 中的牌）按点数升序选最小的 n 张（四带二带牌用）
func pickN(hand []card.Card, already []card.Card, n int) []card.Card {
	excluded := make(map[int]bool)
	for _, ac := range already {
		for i, c := range hand {
			if c == ac {
				excluded[i] = true
				break
			}
		}
	}
	remaining := groupByRankFiltered(hand, excluded)
	var picked []card.Card
	for r := card.Rank3; r <= card.RankRedJoker && len(picked) < n; r++ {
		take := len(remaining[r])
		if take > n-len(picked) {
			take = n - len(picked)
		}
		picked = append(picked, remaining[r][:take]...)
	}
	if len(picked) < n {
		return nil
	}
	return picked
}

// pickPairs 从手牌中（排除 already 中的牌）选 pairCount 对最小带牌对（四带两对用）
func pickPairs(hand []card.Card, already []card.Card, pairCount int) []card.Card {
	excluded := make(map[int]bool)
	for _, ac := range already {
		for i, c := range hand {
			if c == ac {
				excluded[i] = true
				break
			}
		}
	}
	return pickPairsFrom(groupByRankFiltered(hand, excluded), pairCount)
}

// excludeCards 从 hand 中移除 cards 中的牌（按值匹配，每张只匹配一次）
func excludeCards(hand []card.Card, cards []card.Card) []card.Card {
	used := make(map[int]bool)
	for _, tc := range cards {
		for i, hc := range hand {
			if !used[i] && hc == tc {
				used[i] = true
				break
			}
		}
	}
	var result []card.Card
	for i, c := range hand {
		if !used[i] {
			result = append(result, c)
		}
	}
	return result
}
