package bot

import (
	"math/rand/v2"
	"time"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// endgame.go 精确残局求解（计划 §Phase 4）。
// 随机发牌 + rollout 胜率带噪声且不可复现，残局（未知牌很少）改用两步精确化：
//  1. enumerateDeals：把未知池按点数完整枚举/加权抽样成"上家/下家各有哪几张"的分布；
//  2. solveDeal：在每个完全信息分布上做三方最优博弈 DFS + 状态记忆化，
//     返回我方阵营必胜/必败/未收敛。
//
// 候选价值 = 分布加权胜率，替换 MCTS 的 rollout 胜率（分布数超限或搜索超时
// 降级回 UCT 框架）。由 EngineConfig.EnableExactEndgame 门控。

const (
	// exactMaxUnknownCards 精确求解启用的未知牌总数上限：超过则状态空间爆炸，
	// 交回 UCT rollout（典型残局 ≤12 张可在数百毫秒内收敛）
	exactMaxUnknownCards = 12
	// exactDealsCap 分布数上限：超出按权重抽样这么多局
	exactDealsCap = 300
	// exactNodesCap 单个候选在单个分布内的搜索节点上限（超限判为未收敛）
	exactNodesCap = 60000
	// endgameSearchBudget 残局搜索总耗时预算（文档 §10.2 超时保护项）
	endgameSearchBudget = 400 * time.Millisecond
)

// endgameDeal 一种对手发牌分布及其相对权重（∏ 超几何组合数）
type endgameDeal struct {
	up, down [rankCount]int
	weight   float64
}

// dealKey 分布去重键
type dealKey [rankCount]int16 // 高 8 位上家、低 8 位下家（各点数 ≤4）

// enumerateDeals 枚举/抽样未知池在两家的分配方案。存在未跟踪点数（-1）或
// 张数与座位余牌不符时返回 nil（不适用精确分布）。
func enumerateDeals(unknown [rankCount]int, upCount, downCount, cap int) ([]endgameDeal, bool) {
	total := 0
	for _, u := range unknown {
		if u < 0 {
			return nil, false // 记牌视图不完整（easy/normal），按原 rollout 处理
		}
		total += u
	}
	if total != upCount+downCount || upCount < 0 || downCount < 0 {
		return nil, false
	}

	// ways[i][u][d]：用第 i..14 个点数分配出上家 u 张、下家 d 张的方案数
	//（饱和到 cap+1，只为判断"能否完整枚举"，不追求精确大数）
	saturate := func(v int64) int64 {
		if v > int64(cap) {
			return int64(cap) + 1
		}
		return v
	}
	ways := make([][][]int64, rankCount+1)
	for i := range ways {
		ways[i] = make([][]int64, upCount+1)
		for u := range ways[i] {
			ways[i][u] = make([]int64, downCount+1)
		}
	}
	ways[rankCount][0][0] = 1
	for i := rankCount - 1; i >= 0; i-- {
		u := int64(unknown[i])
		for left := 0; left <= upCount; left++ {
			for right := 0; right <= downCount; right++ {
				var sum int64
				for a := int64(0); a <= u && int(a) <= left; a++ {
					for b := int64(0); a+b <= u && int(b) <= right; b++ {
						sum = saturate(sum + ways[i+1][left-int(a)][right-int(b)])
					}
				}
				ways[i][left][right] = sum
			}
		}
	}
	whole := ways[0][upCount][downCount]
	if whole == 0 {
		return nil, false
	}

	deals := make([]endgameDeal, 0, cap)
	seen := make(map[dealKey]bool, cap)
	weightOf := func(up, down [rankCount]int) float64 {
		w := 1.0
		for i, n := range up {
			w *= combSmall(unknown[i], n)
		}
		for i, n := range down {
			w *= combSmall(unknown[i]-up[i], n) // 下家从去掉上家份额后的余量里取
		}
		return w
	}
	push := func(up, down [rankCount]int) {
		k := dealKey{}
		for i := range up {
			k[i] = int16(up[i])<<8 | int16(down[i])
		}
		if seen[k] {
			return
		}
		seen[k] = true
		deals = append(deals, endgameDeal{up: up, down: down, weight: weightOf(up, down)})
	}

	if whole <= int64(cap) { // 完整枚举
		var upAcc, downAcc [rankCount]int
		var walk func(i, left, right int)
		walk = func(i, left, right int) {
			if i == rankCount {
				if left == 0 && right == 0 { // 两家张数必须精确填满（子树预剪已保证，此处兜底）
					push(upAcc, downAcc)
				}
				return
			}
			for a := 0; a <= unknown[i] && a <= left; a++ {
				for b := 0; a+b <= unknown[i] && b <= right; b++ {
					if ways[i+1][left-a][right-b] == 0 {
						continue // 剩余点数凑不满余量，整棵子树弃掉
					}
					upAcc[i], downAcc[i] = a, b
					walk(i+1, left-a, right-b)
				}
				upAcc[i], downAcc[i] = 0, 0
			}
		}
		walk(0, upCount, downCount)
		return deals, true
	}

	// 超上限：按方案数权重做确定性随机游走抽样（固定种子 → 可复现）
	rng := rand.New(rand.NewPCG(0xE4D, uint64(upCount*31+downCount*7+total)))
	for len(deals) < cap {
		var upAcc, downAcc [rankCount]int
		left, right := upCount, downCount
		for i := 0; i < rankCount && (left > 0 || right > 0); i++ {
			// 按子树方案数累计抽样一组 (a,b)
			cum := make([]int64, 0, 16)
			opts := make([][2]int, 0, 16)
			var acc int64
			for a := 0; a <= unknown[i] && a <= left; a++ {
				for b := 0; a+b <= unknown[i] && b <= right; b++ {
					w := ways[i+1][left-a][right-b]
					if w == 0 {
						continue
					}
					acc += w
					opts = append(opts, [2]int{a, b})
					cum = append(cum, acc)
				}
			}
			if acc == 0 {
				break
			}
			pick := rng.Int64N(acc)
			idx := 0
			for idx < len(cum) && cum[idx] <= pick {
				idx++
			}
			if idx == len(cum) {
				idx = len(cum) - 1
			}
			a, b := opts[idx][0], opts[idx][1]
			upAcc[i], downAcc[i] = a, b
			left -= a
			right -= b
		}
		if left == 0 && right == 0 {
			push(upAcc, downAcc)
		}
	}
	return deals, false
}

// exactKey 完全信息残局状态键（三家计数 + 当前待压牌 + 轮转席位）
type exactKey struct {
	counts   [3][rankCount]int8
	lastType rule.HandType
	lastKey  card.Rank
	lastLen  int
	toMove   int8
	leader   int8 // 本手牌的出牌席位（轮到其必须出牌）
}

// endgameSolver 单个分布内的最优博弈搜索器（记忆化 + 节点上限 + 超时中断）
type endgameSolver struct {
	memo     map[exactKey]int8
	nodes    int
	limit    int
	deadline time.Time
	aborted  bool // 超时或超节点：结果按"未收敛"处理
	camp     [3]bool
}

// 残局结果三态
const (
	exactLose int8 = 0  // 我方阵营必败
	exactWin  int8 = 1  // 我方阵营必胜
	exactOpen int8 = -1 // 未收敛（超时/节点超限/子树未定）
)

func newEndgameSolver(camp [3]bool, deadline time.Time) *endgameSolver {
	return &endgameSolver{memo: make(map[exactKey]int8, 512), limit: exactNodesCap, camp: camp, deadline: deadline}
}

func (s *endgameSolver) tick() bool {
	s.nodes++
	if s.nodes > s.limit {
		s.aborted = true
		return true
	}
	if s.nodes%512 == 0 && time.Now().After(s.deadline) {
		s.aborted = true
		return true
	}
	return false
}

// solve 从当前状态起三方最优博弈，返回我方阵营胜负（三态：必胜/必败/未收敛）。
// 席位轮转 0→1→2；last 为空或轮到领出者（toMove==leader）时该席位必须出全新的一手。
func (s *endgameSolver) solve(counts [3][rankCount]int8, last rule.ParsedHand, leader int8, toMove int8) int8 {
	// 终局判定：任一席位走完即按阵营定胜负
	for seat := 0; seat < 3; seat++ {
		if countsTotalOf(counts[seat]) == 0 {
			if s.camp[seat] {
				return exactWin
			}
			return exactLose
		}
	}
	key := exactKey{counts: counts, lastType: last.Type, lastKey: last.KeyRank, lastLen: last.Length, toMove: toMove, leader: leader}
	if v, ok := s.memo[key]; ok {
		return v
	}
	if s.tick() {
		return exactOpen
	}

	// 轮到领出者：本墩结束，开新一轮自由出牌（不能压自己的牌）
	target := last
	mustLead := last.IsEmpty() || toMove == leader
	if mustLead {
		target = rule.ParsedHand{}
	}
	hand := counts8ToCards(counts[toMove])
	me := s.camp[toMove]
	best, definite := exactOpen, false

	// 取该席位最优结果：我方收敛到必胜，敌方收敛到我方必败
	take := func(v int8) {
		if v == exactOpen {
			return
		}
		if !definite {
			best, definite = v, true
			return
		}
		if me && v == exactWin {
			best = exactWin
		} else if !me && v == exactLose {
			best = exactLose
		}
	}
	optimal := func() bool {
		return definite && ((me && best == exactWin) || (!me && best == exactLose))
	}

	for _, h := range rule.GenerateHints(hand, target) {
		if !target.IsEmpty() && !beatsTarget(h.Cards, target) {
			continue // 提示系统可能含类型不符候选，必须验证可压
		}
		next := counts
		for _, c := range h.Cards {
			next[toMove][rankIdx(c.Rank)]--
		}
		take(s.solve(next, parseCards(h.Cards), toMove, int8((int(toMove)+1)%3)))
		if optimal() {
			break
		}
	}
	if !mustLead && !optimal() {
		take(s.solve(counts, last, leader, int8((int(toMove)+1)%3))) // 过牌选项
	}
	if s.aborted {
		best = exactOpen
	}
	s.memo[key] = best
	return best
}

// countsTotalOf int8 计数数组的张数和
func countsTotalOf(c [rankCount]int8) int {
	n := 0
	for _, v := range c {
		n += int(v)
	}
	return n
}

// counts8ToCards int8 计数数组还原成按点数聚合的卡牌（花色无意义，取黑桃）
func counts8ToCards(c [rankCount]int8) []card.Card {
	var out []card.Card
	for i, n := range c {
		for k := 0; k < int(n); k++ {
			out = append(out, card.Card{Rank: idxRank(i), Suit: card.Spade})
		}
	}
	return out
}

// exactSeatCounts 由 GameContext 还原三席计数（seat0=自己，1=下家，2=上家）
func exactSeatCounts(gctx GameContext, deal endgameDeal) [3][rankCount]int8 {
	var c [3][rankCount]int8
	for i, n := range handToCounts(gctx.Hand) {
		c[0][i] = int8(n)
	}
	for i := range deal.up {
		c[2][i] = int8(deal.up[i])
		c[1][i] = int8(deal.down[i])
	}
	return c
}

// exactCampOf 各席位是否与我方同阵营（地主：只有自己；农民：自己 + 非地主座位）
func exactCampOf(gctx GameContext) [3]bool {
	landlordAt := [3]bool{gctx.IsLandlord, gctx.DownIsLandlord, gctx.UpIsLandlord}
	var camp [3]bool
	for seat := 0; seat < 3; seat++ {
		camp[seat] = seat == 0 || (!gctx.IsLandlord && !landlordAt[seat])
	}
	return camp
}

// exactEndgamePlay 精确残局决策：候选价值 = 发牌分布加权胜率。
// ok=false 表示不适用（视图不完整/未知牌过多/搜索全未收敛），调用方回落 UCT。
func exactEndgamePlay(gctx GameContext, deadline time.Time) (cards []card.Card, winRate float64, ok bool) {
	hand := gctx.Hand
	unknown := unknownCounts(gctx.RemainingCards, hand)
	deals, _ := enumerateDeals(unknown, gctx.PlayerCounts[0], gctx.PlayerCounts[1], exactDealsCap)
	if len(deals) == 0 {
		return nil, 0, false
	}

	var candidates [][]card.Card
	if gctx.MustPlay {
		for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
			candidates = append(candidates, h.Cards)
		}
	} else {
		target := gctx.RecentPlays[0].Played
		for _, h := range rule.GenerateHints(hand, target) {
			if beatsTarget(h.Cards, target) {
				candidates = append(candidates, h.Cards)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, 0, false
	}
	// P0：一步清空手牌直接打出（必胜，无需分布搜索）
	for _, cand := range candidates {
		if len(cand) == len(hand) {
			return cand, 1, true
		}
	}
	if time.Now().After(deadline) {
		return nil, 0, false
	}

	camp := exactCampOf(gctx)
	weights := make([]float64, len(candidates)) // 各候选已收敛分布的权重和
	wins := make([]float64, len(candidates))    // 其中判为我方必胜的权重
	okIdx := make([]bool, len(candidates))
	for _, d := range deals {
		if time.Now().After(deadline) {
			break // 预算耗尽：以已收敛的分布为准
		}
		base := exactSeatCounts(gctx, d)
		// 记忆表跳候选共享：候选只是首手不同，后续子问题高度重叠
		solver := newEndgameSolver(camp, deadline)
		for ci, cand := range candidates {
			counts := base
			for _, c := range cand {
				counts[0][rankIdx(c.Rank)]--
			}
			solver.nodes, solver.aborted = 0, false // 节点上限按候选计
			v := solver.solve(counts, parseCards(cand), 0, 1)
			if v == exactOpen || solver.aborted {
				continue // 未收敛的分布不计入（等价 0.5 会让判据失去分辨力）
			}
			weights[ci] += d.weight
			if v == exactWin {
				wins[ci] += d.weight
			}
			okIdx[ci] = true
		}
	}

	bestScore, bestIdx, convergedAny := -1.0, -1, false
	for ci := range candidates {
		if !okIdx[ci] || weights[ci] <= 0 {
			continue
		}
		convergedAny = true
		score := wins[ci] / weights[ci]
		if score > bestScore {
			bestScore, bestIdx = score, ci
		}
	}
	if bestIdx < 0 || !convergedAny {
		return nil, 0, false
	}
	return candidates[bestIdx], bestScore, true
}

// exactEndgameUsable 精确残局是否适用：开关开启 + 记牌视图完整 + 未知牌数在预算内
func exactEndgameUsable(gctx GameContext) bool {
	if !gctx.Cfg.EnableExactEndgame {
		return false
	}
	total := 0
	for _, u := range unknownCounts(gctx.RemainingCards, gctx.Hand) {
		if u < 0 {
			return false // 视图不完整（easy/normal）：不接管，维持原 rollout
		}
		total += u
	}
	return total <= exactMaxUnknownCards
}
