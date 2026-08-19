package bot

import (
	"xgames/internal/games/ddz/card"
)

// 计数数组关键下标
const (
	idxA          = int(card.RankA) - int(card.Rank3)          // 11：顺子类牌型的上限
	idxBlackJoker = int(card.RankBlackJoker) - int(card.Rank3) // 13
	idxRedJoker   = int(card.RankRedJoker) - int(card.Rank3)   // 14
)

// maxHandCountNodes 手数搜索的节点上限（防御性剪枝）：
// 超过后直接返回全单张上界，保证决策耗时可控（实战手牌通常在数千节点内收敛）。
const maxHandCountNodes = 20000

// handCountCache 一回合内的 MinHandCount 结果缓存（按 [rankCount]int 计数数组键值）。
// 生命周期：每回合开始时由 Controller 创建，回合结束后丢弃。
type handCountCache struct {
	memo map[[rankCount]int]int
}

// newHandCountCache 创建空缓存
func newHandCountCache() *handCountCache {
	return &handCountCache{memo: make(map[[rankCount]int]int, 64)}
}

// get 从缓存获取或计算 MinHandCount
func (c *handCountCache) get(counts [rankCount]int) int {
	if v, ok := c.memo[counts]; ok {
		return v
	}
	nodes := 0
	localMemo := make(map[[rankCount]int]int, 128)
	result := minHandCountDFS(counts, localMemo, &nodes)
	c.memo[counts] = result
	return result
}

// minHandCountWithCache 带缓存的 MinHandCount：优先从 GameContext 缓存读取，无缓存时直接计算
func minHandCountWithCache(gctx *GameContext, counts [rankCount]int) int {
	if gctx != nil && gctx.handCountCache != nil {
		return gctx.handCountCache.get(counts)
	}
	return MinHandCount(counts)
}

// MinHandCount 手牌最少出牌次数（手数）。
// DFS + 记忆化：每步锚定剩余最小点数，枚举包含它的合法组合
// （单/对/三/三带/炸弹/四带/顺子/连对/飞机/火箭）。
// 用于专家难度的动态手数优化与正常难度的拆牌收益评估。
func MinHandCount(c [rankCount]int) int {
	nodes := 0
	memo := make(map[[rankCount]int]int, 128)
	return minHandCountDFS(c, memo, &nodes)
}

func minHandCountDFS(c [rankCount]int, memo map[[rankCount]int]int, nodes *int) int {
	total := countsTotal(c)
	if total == 0 {
		return 0
	}
	if v, ok := memo[c]; ok {
		return v
	}
	*nodes++
	if *nodes > maxHandCountNodes {
		return total // 超限兜底：全单张上界（过滤条件退化为保守，不影响合法性）
	}

	// 锚定最小点数
	i := 0
	for i < rankCount && c[i] == 0 {
		i++
	}

	best := total // 上界：全部单张
	try := func(next [rankCount]int) {
		if n := 1 + minHandCountDFS(next, memo, nodes); n < best {
			best = n
		}
	}

	// 单张
	n1 := c
	n1[i]--
	try(n1)

	// 对子
	if c[i] >= 2 {
		n2 := c
		n2[i] -= 2
		try(n2)
	}

	// 三张 / 三带一 / 三带二
	if c[i] >= 3 {
		n3 := c
		n3[i] -= 3
		try(n3)
		for j := 0; j < rankCount; j++ { // 带一张单牌
			if j != i && c[j] > 0 {
				nk := n3
				nk[j]--
				try(nk)
			}
		}
		for j := 0; j < rankCount; j++ { // 带一对
			if j != i && c[j] >= 2 {
				nk := n3
				nk[j] -= 2
				try(nk)
			}
		}
	}

	// 炸弹 / 四带二 / 四带两对（带牌贪心选取）
	if c[i] == 4 {
		nb := c
		nb[i] = 0
		try(nb)
		if k, ok := greedySingles(c, onlyRank(i), 2); ok {
			try(k)
		}
		if k, ok := greedyPairs(c, onlyRank(i), 2); ok {
			try(k)
		}
	}

	// 锚点作带牌：三张/炸弹/飞机主体在更大点数处，锚点牌充当带牌（三带/四带/飞机带）
	for j := i + 1; j < rankCount; j++ {
		if c[j] >= 3 { // 三带一 / 三带二（锚点充当带牌）
			nw := c
			nw[j] -= 3
			nw[i]--
			try(nw)
			if c[i] >= 2 {
				nw2 := c
				nw2[j] -= 3
				nw2[i] -= 2
				try(nw2)
			}
		}
		if c[j] == 4 { // 四带二 / 四带两对（锚点充当其中一张带牌）
			nk := c
			nk[j] = 0
			nk[i]--
			if k, ok := greedySingles(nk, onlyRank(j), 1); ok {
				try(k)
			}
			if c[i] >= 2 {
				np := c
				np[j] = 0
				np[i] -= 2
				if k, ok := greedyPairs(np, onlyRank(j), 1); ok {
					try(k)
				}
			}
		}
	}

	// 顺子（i 为最低点，长度 >=5，不含 2 和王）
	maxLen := 0
	for j := i; j <= idxA && c[j] >= 1; j++ {
		maxLen++
	}
	for l := 5; l <= maxLen; l++ {
		ns := c
		for j := i; j < i+l; j++ {
			ns[j]--
		}
		try(ns)
	}

	// 连对（长度 >=3）
	maxPLen := 0
	for j := i; j <= idxA && c[j] >= 2; j++ {
		maxPLen++
	}
	for l := 3; l <= maxPLen; l++ {
		np := c
		for j := i; j < i+l; j++ {
			np[j] -= 2
		}
		try(np)
	}

	// 飞机（机身长度 >=2）：不带 / 带单（贪心）/ 带对（贪心）
	maxTL := 0
	for j := i; j <= idxA && c[j] >= 3; j++ {
		maxTL++
	}
	for l := 2; l <= maxTL; l++ {
		nt := c
		for j := i; j < i+l; j++ {
			nt[j] -= 3
		}
		try(nt)
		if k, ok := greedySingles(nt, rangeRanks(i, i+l-1), l); ok {
			try(k)
		}
		if k, ok := greedyPairs(nt, rangeRanks(i, i+l-1), l); ok {
			try(k)
		}
	}

	// 飞机带翅膀且锚点牌是翅膀：机身在更大点数处，锚点充当其中一张/一对带牌，其余贪心
	for j := i + 1; j <= idxA; j++ {
		if c[j] < 3 {
			continue
		}
		maxL := 0
		for k := j; k <= idxA && c[k] >= 3; k++ {
			maxL++
		}
		for l := 2; l <= maxL; l++ {
			nt := c
			for k := j; k < j+l; k++ {
				nt[k] -= 3
			}
			nt[i]--
			if k, ok := greedySingles(nt, rangeRanks(j, j+l-1), l-1); ok {
				try(k)
			}
			if c[i] >= 2 {
				np := c
				for k := j; k < j+l; k++ {
					np[k] -= 3
				}
				np[i] -= 2
				if k, ok := greedyPairs(np, rangeRanks(j, j+l-1), l-1); ok {
					try(k)
				}
			}
		}
	}

	// 火箭
	if i == idxBlackJoker && c[idxBlackJoker] >= 1 && c[idxRedJoker] >= 1 {
		nr := c
		nr[idxBlackJoker]--
		nr[idxRedJoker]--
		try(nr)
	}

	memo[c] = best
	return best
}

// onlyRank 排除函数：仅排除单个点数
func onlyRank(i int) func(int) bool {
	return func(j int) bool { return j == i }
}

// rangeRanks 排除函数：排除闭区间 [lo, hi] 内的点数
func rangeRanks(lo, hi int) func(int) bool {
	return func(j int) bool { return j >= lo && j <= hi }
}

// greedySingles 从 c 中贪心取 n 张单张带牌（排除 exclude 的点数）：
// 优先取张数少的点数（先单张、再对子、后更多），避免拆散大组合。
// 成功返回扣减后的新数组，不足返回原数组与 false。
func greedySingles(c [rankCount]int, exclude func(int) bool, n int) ([rankCount]int, bool) {
	out := c
	need := n
	for round := 1; round <= 3 && need > 0; round++ {
		for j := 0; j < rankCount && need > 0; j++ {
			if exclude(j) || out[j] == 0 {
				continue
			}
			if round == 1 && out[j] != 1 {
				continue
			}
			if round == 2 && out[j] != 2 {
				continue
			}
			out[j]--
			need--
		}
	}
	if need > 0 {
		return c, false
	}
	return out, true
}

// greedyPairs 从 c 中贪心取 n 对带牌（排除 exclude 的点数）：优先恰好成对的点数。
func greedyPairs(c [rankCount]int, exclude func(int) bool, n int) ([rankCount]int, bool) {
	out := c
	need := n
	for round := 1; round <= 2 && need > 0; round++ {
		for j := 0; j < rankCount && need > 0; j++ {
			if exclude(j) || out[j] < 2 {
				continue
			}
			if round == 1 && out[j] != 2 {
				continue
			}
			out[j] -= 2
			need--
		}
	}
	if need > 0 {
		return c, false
	}
	return out, true
}
