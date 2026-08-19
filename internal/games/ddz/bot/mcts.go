package bot

import (
	"math"
	"math/rand/v2"
	"sync"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// --- UCT 树节点（轻量级 ISMCTS）---
// 每个节点代表一个游戏状态（手牌 + 最近出牌），存储访问次数和胜利次数，
// 用 UCB1 公式平衡探索与利用。
type mctsNode struct {
	cards   []card.Card // 该节点对应的出牌候选
	visits  int         // 访问次数
	wins    int         // 胜利次数（rollout 中自己走完的次数）
	parent  *mctsNode   // 父节点（根节点为 nil）
	children []*mctsNode // 子节点
}

// ucb1 UCB1 选择公式：exploit + explore
// exploit = wins/visits, explore = C * sqrt(ln(parentVisits)/visits)
func (n *mctsNode) ucb1(totalVisits int, c float64) float64 {
	if n.visits == 0 {
		return math.MaxFloat64 // 未访问节点优先探索
	}
	exploit := float64(n.wins) / float64(n.visits)
	explore := c * math.Sqrt(math.Log(float64(totalVisits))/float64(n.visits))
	return exploit + explore
}

// --- 斗地主 MCTS 残局增强 ---
// 核心思路：残局（对手 ≤ 3 张 或 自己 ≤ 5 张）时，
// 从 unknownCounts 随机发牌给对手，模拟多轮出牌直到一方走完，
// 评估每个候选出牌的胜率，选最高者。
// 非残局保持规则引擎原有决策路径。

// mctsBaseIterations 基础模拟次数
const mctsBaseIterations = 200

// mctsMaxIterations 最大模拟次数（残局高精度）
const mctsMaxIterations = 1000

// mctsMyCardsThreshold 触发 MCTS 增强的条件：自己手牌 ≤ 此值
const mctsMyCardsThreshold = 5

// mctsOpponentThreshold 任一对手手牌 ≤ 此值也触发 MCTS
const mctsOpponentThreshold = 3

// shouldUseMCTS 判断是否应该启用 MCTS 增强
func shouldUseMCTS(gctx GameContext) bool {
	myCount := len(gctx.Hand)
	oppMin := minOpponentCount(gctx)

	// 自己接近走完或任一对手危急时启用
	if myCount <= mctsMyCardsThreshold {
		return true
	}
	if oppMin <= mctsOpponentThreshold {
		return true
	}

	return false
}

// getMctsIterations 动态调整模拟次数
func getMctsIterations(gctx GameContext) int {
	myCount := len(gctx.Hand)
	oppMin := minOpponentCount(gctx)

	switch {
	case myCount <= 2 || oppMin <= 2:
		return mctsMaxIterations // 残局高精度
	case myCount <= 5:
		return 500 // 中残局中等精度
	default:
		return mctsBaseIterations // 常规局快速决策
	}
}

// mctsDecidePlay 用 UCT 树搜索决定出牌（残局增强）。
// 返回最优候选与其真实模拟胜率（winRate ∈ [0,1]；候选唯一未模拟时为 -1）。
func mctsDecidePlay(gctx GameContext) ([]card.Card, float64) {
	hand := gctx.Hand
	var candidates [][]card.Card

	if gctx.MustPlay {
		// 领出：生成所有合法牌型
		for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
			if !isBombLike(h.Cards) && !breaksBomb(hand, h.Cards) {
				candidates = append(candidates, h.Cards)
			}
		}
	} else {
		// 跟牌：只考虑能压过目标的
		target := gctx.RecentPlays[0].Played
		for _, h := range rule.GenerateHints(hand, target) {
			if beatsTarget(h.Cards, target) {
				candidates = append(candidates, h.Cards)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, 0
	}

	// P0 预筛：存在一步清空手牌的候选 → 直接返回（打出即整手获胜，无需搜索）。
	// 双保险：Engine.DecidePlay 的 immediateWinPlay 已先行拦截，此处兜底 MCTS 独立调用场景。
	for _, cand := range candidates {
		if len(cand) == len(hand) {
			return cand, 1
		}
	}

	if len(candidates) == 1 {
		return candidates[0], -1
	}

	// 动态调整模拟次数
	iterations := getMctsIterations(gctx)

	// 构建 UCT 树：根节点的子节点对应各候选出牌
	root := &mctsNode{children: make([]*mctsNode, len(candidates))}
	for i, cand := range candidates {
		root.children[i] = &mctsNode{cards: cand, parent: root}
	}

	unknown := unknownCounts(gctx.RemainingCards, hand)

	// UCT 主循环：并行化模拟（Selection → Simulation → Backpropagation）
	// 使用 worker pool 批量执行 rollout，减少锁竞争
	const batchSize = 100 // 每批模拟次数
	var mu sync.Mutex     // 保护树节点更新的互斥锁

	for batch := 0; batch < iterations; batch += batchSize {
		currentBatch := batchSize
		if batch+currentBatch > iterations {
			currentBatch = iterations - batch
		}

		// 并行执行一批模拟
		var wg sync.WaitGroup
		results := make([]struct {
			node   *mctsNode
			won    bool
		}, currentBatch)

		for i := 0; i < currentBatch; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				// Selection
				selected := uctSelect(root, iterations)
				// Simulation
				won := mctsSimulateWin(hand, selected.cards, unknown, gctx)
				results[idx] = struct {
					node *mctsNode
					won  bool
				}{selected, won}
			}(i)
		}
		wg.Wait()

		// Backpropagation: 批量更新（加锁保证原子性）
		mu.Lock()
		for _, r := range results {
			node := r.node
			for node != nil {
				node.visits++
				if r.won {
					node.wins++
				}
				node = node.parent
			}
		}
		mu.Unlock()
	}

	// 选择胜率最高的子节点
	bestIdx := 0
	bestWinRate := -1.0
	for i, child := range root.children {
		if child.visits == 0 {
			continue
		}
		winRate := float64(child.wins) / float64(child.visits)
		if winRate > bestWinRate {
			bestWinRate = winRate
			bestIdx = i
		}
	}

	return candidates[bestIdx], bestWinRate
}

// uctSelect 用 UCB1 公式从根节点的子节点中选择下一个要模拟的节点
// 优先选择未访问节点，否则选 UCB1 值最大的
func uctSelect(root *mctsNode, totalVisits int) *mctsNode {
	var best *mctsNode
	bestUCB := -1.0
	for _, child := range root.children {
		ucb := child.ucb1(totalVisits, math.Sqrt2) // C = √2 是经典值
		if ucb > bestUCB {
			bestUCB = ucb
			best = child
		}
	}
	return best
}

// mctsSimulateWin 三席位 rollout：假设打出 playedCards 后，从 unknown 牌池按
// 上/下家真实余牌数随机发牌，按座位轮转（我 → 下家 → 上家）模拟至一方走完，
// 按阵营判定胜负（P1 对齐）：农民任一非地主（含队友）走完 = 我方胜；
// 地主自己走完 = 胜、任一农民走完 = 负。领出权被压后正确移交，跟不动则过牌。
func mctsSimulateWin(myHand []card.Card, playedCards []card.Card, unknown [rankCount]int, gctx GameContext) bool {
	upHand, downHand := dealOpponentHands(unknown, gctx.PlayerCounts[0], gctx.PlayerCounts[1])

	// 席位视角：seat0=我，seat1=下家，seat2=上家；轮转顺序 0→1→2
	hands := [3][]card.Card{
		subtractHand(myHand, playedCards),
		downHand,
		upHand,
	}
	landlordAt := [3]bool{gctx.IsLandlord, gctx.DownIsLandlord, gctx.UpIsLandlord}

	enemy := func(seat int) bool {
		if gctx.IsLandlord {
			return seat != 0
		}
		return landlordAt[seat]
	}

	// 候选已打出且未清空手牌（清空场景已被预筛拦截），现在轮到下家行动
	last := parseCards(playedCards)
	lastSeat := 0
	passes := 0
	cur := 1
	for step := 0; step < rolloutPlayCap(hands); step++ {
		// 阵营胜负判定：任一席位走完即分出胜负
		for seat := range hands {
			if len(hands[seat]) == 0 {
				if gctx.IsLandlord {
					return seat == 0
				}
				return !landlordAt[seat]
			}
		}

		mustPlay := last.IsEmpty() || cur == lastSeat
		play := mctsQuickPlay(hands[cur], mustPlay, last)
		if play == nil {
			passes++
			if passes >= 2 { // 连续两过开新轮：下一席领出
				last = rule.ParsedHand{}
				passes = 0
			}
			cur = (cur + 1) % 3
			continue
		}
		hands[cur] = subtractHand(hands[cur], play)
		last = parseCards(play)
		lastSeat = cur
		passes = 0
		cur = (cur + 1) % 3
	}

	// 步数上限未分胜负：剩余牌少的一方阵营判胜
	myLeft, enemyLeft := 0, 0
	for seat := range hands {
		if enemy(seat) {
			enemyLeft += len(hands[seat])
		} else {
			myLeft += len(hands[seat])
		}
	}
	return myLeft <= enemyLeft
}

// rolloutPlayCap rollout 步数上限：随全场剩余牌数自适应——
// 残局（牌少）可完整收敛，大手牌局面（如对手仅剩 3 张触发 MCTS）开销仍可控
func rolloutPlayCap(hands [3][]card.Card) int {
	total := 0
	for _, h := range hands {
		total += len(h)
	}
	return 2*total + 4
}

// dealOpponentHands 从 unknown 牌池为上/下家随机发牌（数量取真实余牌数）。
// unknown 为负值（未跟踪）的点数按 2 张估计；池不足时按两家余牌数比例截断，
// 保证不发虚拟牌也不会在池耗尽时死循环。
func dealOpponentHands(unknown [rankCount]int, upCount, downCount int) (upHand, downHand []card.Card) {
	var pool []card.Card
	for i := 0; i < rankCount; i++ {
		n := unknown[i]
		if n < 0 {
			n = 2 // 未跟踪点数按平均 2 张估计
		}
		for j := 0; j < n; j++ {
			pool = append(pool, card.Card{Rank: idxRank(i)})
		}
	}
	if upCount+downCount > len(pool) && upCount+downCount > 0 {
		upCount = len(pool) * upCount / (upCount + downCount)
		downCount = len(pool) - upCount
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	upHand = append([]card.Card(nil), pool[:upCount]...)
	downHand = append([]card.Card(nil), pool[upCount:upCount+downCount]...)
	return upHand, downHand
}

// mctsQuickPlay rollout 阶段的简化出牌策略：
// MustPlay 时选最小合法牌型；跟牌时选最小能压过的
func mctsQuickPlay(hand []card.Card, mustPlay bool, target rule.ParsedHand) []card.Card {
	if mustPlay {
		// 领出：生成所有合法牌型，选最小点数的
		var minCards []card.Card
		minScore := 1 << 30
		for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
			if isBombLike(h.Cards) {
				continue
			}
			s := sumRank(h.Cards)
			if s < minScore {
				minScore = s
				minCards = h.Cards
			}
		}
		return minCards
	}

	// 跟牌：选最小能压过的
	var minBeat []card.Card
	minScore := 1 << 30
	for _, h := range rule.GenerateHints(hand, target) {
		if !beatsTarget(h.Cards, target) {
			continue
		}
		if isBombLike(h.Cards) {
			continue
		}
		s := sumRank(h.Cards)
		if s < minScore {
			minScore = s
			minBeat = h.Cards
		}
	}
	return minBeat
}

// parseCards 将 []card.Card 解析为 ParsedHand（用于跟牌判断）
func parseCards(cards []card.Card) rule.ParsedHand {
	ph, _ := rule.ParseHand(cards)
	return ph
}

// subtractHand 从 hand 中移除 played 中的牌
func subtractHand(hand, played []card.Card) []card.Card {
	counts := handToCounts(hand)
	for _, c := range played {
		counts[rankIdx(c.Rank)]--
	}
	return countsToCards(counts)
}

// --- 主决策集成 ---

// mctsEnhancedPlay 残局 MCTS 出牌决策入口。
// 优先级：残局 MCTS > 规则引擎；返回出牌候选与真实模拟胜率，不接管时返回 nil。
// 例外：MCTS 模拟不区分队友与对手（随机发牌均为敌人视角），
// 会无视规则引擎的农民配合约束，以下场景不接管：
//   - 队友出牌后的跟牌：须执行"不压队友大牌/让牌送队友/接应小牌"配合；
//     但农民上家主攻模式除外——主攻以自己走完为第一路径，
//     rollout 为三席位阵营判定，"压队友抢跑"只有在对阵营有利时才获高胜率，
//     可放心放开接管（见 docs/bots/ddz-bot.md §5.4/§5.5）
//   - 农民上家残局死封（对手 ≤2 张）：须用最强牌型一次性封堵，模拟胜率
//     难以体现死封的战略价值，常误出小牌放走对手
func mctsEnhancedPlay(gctx GameContext) ([]card.Card, float64) {
	if lastPlayByTeammate(gctx) && gctx.HasPlayed && !farmerUpperAttackMode(gctx) {
		return nil, 0
	}
	if minOpponentCount(gctx) <= 2 && isFarmerUpperSeat(gctx) {
		return nil, 0
	}
	// 残局触发 MCTS
	if shouldUseMCTS(gctx) {
		if mctsResult, winRate := mctsDecidePlay(gctx); mctsResult != nil {
			// 用 MCTS 结果覆盖规则引擎（仅残局）
			return mctsResult, winRate
		}
	}
	return nil, 0 // 返回 nil 表示不接管，由调用方走规则引擎路径
}
