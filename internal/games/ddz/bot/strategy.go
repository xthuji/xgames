package bot

import (
	"sort"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// unifiedPlay 统一出牌策略：所有难度共用同一套拆牌、预估、配合与出牌选择逻辑，
// 难度差异只体现在 Controller 构造的 RemainingCards 记牌视图完整度上。
// 视图缺失的点数按"未知"保守处理（见 tracker.go），信息越少决策越保守，
// 行为差异由此自然涌现。
func unifiedPlay(gctx GameContext) []card.Card {
	if gctx.MustPlay {
		return unifiedLead(gctx)
	}
	if !gctx.CanBeat {
		return nil
	}
	return unifiedFollow(gctx)
}

// immediateWinPlay P0 硬规则：存在一步清空手牌的出法 → 直接返回（胜利优先级阶梯最高层，
// 优先于 MCTS、故意犯错与一切策略过滤，见 docs/bots/ddz-bot.md §2）。
// 领出：整手牌恰为一个合法牌型；跟牌：存在能压过上家且恰好清空手牌的候选。
// 返回 nil 表示不存在一步清空的出法。
func immediateWinPlay(gctx GameContext) []card.Card {
	if gctx.MustPlay {
		if ph, err := rule.ParseHand(gctx.Hand); err == nil && ph.Type != rule.Invalid {
			return gctx.Hand
		}
		return nil
	}
	target := gctx.RecentPlays[0].Played
	if target.IsEmpty() {
		return nil
	}
	for _, h := range rule.GenerateHints(gctx.Hand, target) {
		if len(h.Cards) == len(gctx.Hand) && beatsTarget(h.Cards, target) {
			return h.Cards
		}
	}
	return nil
}

// unifiedLead 领出：整手走完 → 两步走完+夺权牌 → 残局死封 → 角色分工（跑牌/顶牌）→
// 手数最小化（叠加拆牌代价、控制牌优先与潜在炸弹预警）→ 最小整组兜底
func unifiedLead(gctx GameContext) []card.Card {
	hand := gctx.Hand

	// 整手能一次走完（一手合法牌型）→ 直接获胜（优先于一切策略）
	if ph, err := rule.ParseHand(hand); err == nil && ph.Type != rule.Invalid {
		return hand
	}
	// 一手带牌走完（三带一/三带二/四带二/飞机带翅等带牌型恰好清空手牌）→
	// 不拆牌、大量出牌直接获胜（规则 5）
	if fin := finishingLead(hand); fin != nil {
		return fin
	}

	// P2优化：两步走完 + 绝对夺权牌
	// 如果手牌可以分两步走完，且手中有绝对夺权的王牌（根据记牌和对手余牌计算，
	// 敌对方大概率没有更大的或没有该牌型），优先出夺权牌拿到出牌权，再出剩下的牌
	if twoStepFinishWithControl(gctx) != nil {
		return twoStepFinishWithControl(gctx)
	}

	// 残局冲刺（手数 ≤3 且队友/敌方余牌均未到临门阈值）：无论地主/农民、
	// 上家/下家都以攻为主——优先出小牌与带牌散牌、掌权牌作夺权资源、
	// 最后一手保留大牌便于跟随取胜（见 docs/bots/ddz-bot.md §5.6）
	if sprintMode(gctx) {
		if c := sprintLead(gctx); c != nil {
			return c
		}
	}

	// 残局模式：对手（尤其地主）手牌 ≤2 → 农民上家无条件打最大单张/对子死封
	if minOpponentCount(gctx) <= 2 && isFarmerUpperSeat(gctx) {
		if c := biggestSingleOrPair(hand); c != nil {
			return c
		}
	}

	// 队友接近走完且地主不危急：领小牌送出牌权，帮队友走完（农民配合）
	if !gctx.IsLandlord && teammateCount(gctx) <= 2 && minOpponentCount(gctx) > 2 {
		if c := pickRunCard(gctx); c != nil {
			return c
		}
	}

	// 局势化领出（规则 2/3/4）：
	// 对手 <6 张 → 谨慎控牌，保持压制力，避免送小牌给对手收权；
	// 正常局势（对手余牌较多）：地主领出，或农民且下家是队友 →
	// 优先大批量牌型（连对/顺子/三带一等）或最小单张/对子；
	// 农民且下家是地主且对手 >10 张 → 防其跟跑小牌的快速跑牌。
	enemyMin, enemyMax := minOpponentCount(gctx), maxOpponentCount(gctx)
	if enemyMin <= 5 {
		if c := pickCautiousLead(gctx); c != nil {
			return c
		}
	} else if gctx.IsLandlord || nextSeatIsTeammate(gctx) {
		if c := pickFastShed(gctx, true); c != nil {
			return c
		}
	} else if enemyMax > 10 {
		if c := pickFastShed(gctx, false); c != nil {
			return c
		}
	}

	// 地主下家（跑牌位）：出小牌保留实力
	if isFarmerLowerSeat(gctx) {
		if c := pickRunCard(gctx); c != nil {
			return c
		}
	}
	// 地主上家（顶牌位）：默认打大牌限制地主（辅攻）；主攻模式（手牌易走完）
	// 改为先跑掉自己的小牌——掌权王牌不用于跑牌（与 pickFastShed 同口径），
	// 无合适跑牌候选时回落通用手数领出
	if isFarmerUpperSeat(gctx) {
		if farmerUpperAttackMode(gctx) {
			if c := pickRunCardFiltered(gctx, func(cards []card.Card) bool {
				return powerCardCount(cards) > 0
			}); c != nil {
				return c
			}
		} else if c := pickBlockCard(gctx); c != nil {
			return c
		}
	}

	// 动态手数优化 + 控制牌优先 + 潜在炸弹预警
	current := minHandCountWithCache(&gctx, handToCounts(hand))
	unknown := unknownCounts(gctx.RemainingCards, hand)
	bombThreat := len(potentialBombRanks(unknown)) > 0

	type candidate struct {
		cards []card.Card
		score int
		wins  bool // 预估胜出：打出后牌权必然/大概率回到己方（含绝对控制与过牌推理）
		split int  // 拆牌代价：拆散的对子/三条数量（掌权王牌加倍），同分时优先不拆牌
		power int  // 掌权王牌消耗：非胜出候选消耗掌权牌加罚，谨慎使用
	}
	var cands, fallback []candidate
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue // 保留炸弹/王炸应对预案
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		if after != current-1 {
			continue // 过滤导致总手数增加的出牌动作
		}
		wins := false
		if ph, perr := rule.ParseHand(h.Cards); perr == nil {
			wins = likelyWin(gctx, unknown, ph)
		}
		c := candidate{cards: h.Cards, score: h.Score, wins: wins,
			split: splitPenaltyEx(hand, h.Cards), power: powerLeadCost(h.Cards, wins)}
		// 潜在炸弹预警：存在对手可炸的点数时，裸出大牌单/对有被炸风险，降级备选
		if bombThreat && isRiskyBigLead(h.Cards, unknown) {
			fallback = append(fallback, c)
			continue
		}
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		cands = fallback
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].wins != cands[j].wins {
				return cands[i].wins // 预估胜出优先（确保收回出牌权）
			}
			if cands[i].split != cands[j].split {
				return cands[i].split < cands[j].split // 同分优先不拆牌的出法
			}
			if cands[i].power != cands[j].power {
				return cands[i].power < cands[j].power // 少消耗掌权王牌
			}
			return cands[i].score < cands[j].score
		})
		
		// P0: 应用故意犯错机制
		chosen := cands[0].cards
		// 注意：此处无法访问 personality，需要在调用层处理
		return chosen
	}
	// 仅剩炸弹类组合或全部候选都增手数：兜底出最小整组
	return fallbackLead(hand)
}

// unifiedFollow 跟牌：农民配合 → 残局强封 → 手数过滤（控制牌优先）→ 保守用炸
func unifiedFollow(gctx GameContext) []card.Card {
	hand := gctx.Hand
	target := gctx.RecentPlays[0].Played
	hints := rule.GenerateHints(hand, target)
	if len(hints) == 0 {
		return nil
	}

	danger := minOpponentCount(gctx) <= 2
	cautious := minOpponentCount(gctx) <= 5 // 敌方 <6 张：谨慎压牌，防被一波带走
	// 主攻模式：上家以自己走完为第一路径（判定含 danger/队友 ≤2 否决，
	// 与下方硬豁免分支天然互斥）
	attack := farmerUpperAttackMode(gctx)
	// 残局冲刺：手数 ≤3 且双方无临门危机，以攻为主（不分角色与席位，
	// 见 docs/bots/ddz-bot.md §5.6）
	sprint := sprintMode(gctx)
	// 拆牌重计划：连续多次无法管住对手，或对手余牌很少时，原出牌计划已失效，
	// 重新拆分牌型：允许拆散对子/三条/顺子去压住对手夺取牌权（炸弹/王炸仍不拆）。
	replan := gctx.UnbeatenStreak >= 2 || cautious
	current := minHandCountWithCache(&gctx, handToCounts(hand))

	// 跟牌回合也能一次走完：优先检查是否有候选能清空手牌（任何场景都适用）
	for _, h := range hints {
		if len(h.Cards) == len(hand) && beatsTarget(h.Cards, target) {
			return h.Cards
		}
	}

	// 农民配合：队友的大牌不压，除非自己能直接走完（上方清空候选已优先返回）；
	// 但队友领出小牌时需要处理，否则牌权被队友小牌锁死，
	// 会出现某个农民整局拿不到领出权、一张牌都出不去的问题；
	// 自己本局尚未出过牌时不做让牌（先开张再谈配合）。
	if lastPlayByTeammate(gctx) && gctx.HasPlayed {
		if teammateCount(gctx) <= 2 {
			return nil // 队友接近走完：不压队友牌，把牌权留给队友冲刺
		}
		if !danger {
			if sprint {
				// 冲刺：先借队友 ≤J 小牌同路跑掉自己的小牌（严格同路、
				// 2/王保留）；无同路候选时以最小接应夺取牌权接管节奏
				//（炸弹不动、手数不增），自己手数 ≤3 时主动抢跑优先
				if c := pickShedFollow(hand, hints, target, current, &gctx); c != nil {
					return c
				}
				if c := pickReliefBeat(hand, hints, target, current, &gctx); c != nil {
					return c
				}
			} else if attack {
				// 主攻：借队友 ≤J 小牌跑掉自己的小牌（严格同路、2/王保留、
				// 手数不增）；找不到就让牌，不为压队友消耗掌权牌
				if c := pickShedFollow(hand, hints, target, current, &gctx); c != nil {
					return c
				}
			} else if teammateNeedsRelief(target) {
				// 辅攻：接手队友小牌的牌权再送回阵营（同路最小接应）
				if c := pickReliefBeat(hand, hints, target, current, &gctx); c != nil {
					return c
				}
			}
		}
		return nil
	}

	// 残局强力封堵：地主剩 ≤2 张时，农民上家用最强手段（含炸弹）死封
	if danger && isFarmerUpperSeat(gctx) {
		return strongestBeat(hints, target)
	}

	// 跑牌方积极跟牌：地主与地主下家肩负快速走牌职责，对手领出
	// 中段及以下的单张/对子时跟出最小同路可压牌（允许消耗 Q/K/A，
	// 2 与王仍留作收权底牌），避免掌权保护过度导致中段牌不跟、
	// 小牌长期滞留手中只能被动挨压。
	if gctx.IsLandlord || isFarmerLowerSeat(gctx) || sprint {
		if c := pickShedFollow(hand, hints, target, current, &gctx); c != nil {
			return c
		}
	}

	unknown := unknownCounts(gctx.RemainingCards, hand)

	var best []card.Card
	bestAfter := 1 << 30
	bestControl := false
	for _, h := range hints {
		if !beatsTarget(h.Cards, target) {
			continue // 提示系统可能含类型不匹配的候选，出牌前必须验证可压
		}
		bomb := isBombLike(h.Cards)
		if bomb && !shouldUseBomb(gctx, h.Cards) {
			continue
		}
		if !bomb && (breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards)) {
			continue // 非危急绝不拆炸弹/王炸
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		// 多牌型目标（顺子/连对/飞机族）：对手一手可走 5 张以上，放行的节奏
		// 价值（P2）高于我方拆 1 组的结构代价（P3），after > current 的候选
		// 不直接丢弃，允许拆散至多 1 组对子/三条进入择优
		// （护栏：不拆炸弹/王炸、掌权牌保护维持不变，见 docs/bots/ddz-bot.md §5.3）。
		if after > current && !danger && !canFinishSoon(hand, h.Cards) && !replan {
			if !isMultiTypeTarget(target) || splitPenalty(hand, h.Cards) > 1 {
				continue // 手数变差且非危急/非重计划 → 让牌
			}
		}
		control := false
		if ph, perr := rule.ParseHand(h.Cards); perr == nil {
			control = likelyWin(gctx, unknown, ph)
		}
		// 掌权王牌保护：非预估胜出时不用 Q/K/A/2 去压小牌（目标牌较小且
		// 自己不是危急/谨慎状态），掌权牌留作后手收权；但连续无法管住
		// 对手时放开 Q/K/A 夺回牌权（2 与王仍留作终极收权底牌）。
		// 主攻模式放宽 Q/K/A：主攻者需要牌权兑现"3 步内走完"，
		// Q/K/A 在主攻语境下是夺权资源而非纯后手；2/王维持保护（收尾底牌）。
		if !control && !danger && !cautious && !sprint &&
			powerCardCount(h.Cards) > 0 && target.KeyRank <= card.Rank10 {
			attackRelaxed := attack && !containsTopPower(h.Cards)
			if !attackRelaxed && !(gctx.UnbeatenStreak >= 2 && !containsTopPower(h.Cards)) {
				continue
			}
		}
		// 选择顺序：预估胜出牌优先 → 手数更优 → 评分更优
		if best == nil ||
			(control && !bestControl) ||
			(control == bestControl && after < bestAfter) ||
			(control == bestControl && after == bestAfter && h.Score < scoreCards(best)) {
			best, bestAfter, bestControl = h.Cards, after, control
		}
	}
	if best == nil && !gctx.HasPlayed {
		return anyBeat(hints, target) // 未开张兜底：不计代价先出一手，避免整局零出牌
	}
	return best
}

// isMultiTypeTarget 目标是否为顺子/连对/飞机族（一手可走多张的多牌型）。
// 跟这类牌时允许拆散至多 1 组面子牌进入择优（多牌型放宽，见 docs/bots/ddz-bot.md §5.3）。
func isMultiTypeTarget(target rule.ParsedHand) bool {
	switch target.Type {
	case rule.Straight, rule.PairStraight, rule.Plane, rule.PlaneWithSingles, rule.PlaneWithPairs:
		return true
	}
	return false
}

// --- 领出辅助 ---

// pickRunCard 跑牌：在手数不增的前提下出面值最小的非炸组合；
// 优先整组出牌（拆牌代价为 0），无整组可选时才放宽到拆牌出法
func pickRunCard(gctx GameContext) []card.Card {
	return pickRunCardFiltered(gctx, nil)
}

// pickRunCardFiltered 跑牌的可过滤变体：skip 返回 true 的候选被跳过
//（主攻模式用于过滤掌权王牌候选，与 pickFastShed 同口径）。
func pickRunCardFiltered(gctx GameContext, skip func(cards []card.Card) bool) []card.Card {
	hand := gctx.Hand
	current := minHandCountWithCache(&gctx, handToCounts(hand))
	var best, bestWhole []card.Card
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue
		}
		if skip != nil && skip(h.Cards) {
			continue
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		if after > current {
			continue
		}
		if best == nil || sumRank(h.Cards) < sumRank(best) {
			best = h.Cards
		}
		if splitPenalty(hand, h.Cards) == 0 &&
			(bestWhole == nil || sumRank(h.Cards) < sumRank(bestWhole)) {
			bestWhole = h.Cards
		}
	}
	if bestWhole != nil {
		return bestWhole
	}
	return best
}

// pickBlockCard 顶牌：用整组限制地主、逼其消耗 2/王等控制牌夺权，
// 控制牌（2/王）自身保留作后手收权——先手王牌过早出手等于白送信息还丢牌权。
// 下家是地主（规则 2）：优先中段 7~J 单张/对子，既压制地主跟跑小牌，
// 又不至于过早消耗掌权王牌；绝不断拆对子/三条/王炸/炸弹。
// 返回 nil 表示无合适的顶牌，交由通用候选逻辑（最小整组）处理。
func pickBlockCard(gctx GameContext) []card.Card {
	hand := gctx.Hand
	counts := handToCounts(hand)

	usable := func(i int) bool {
		return counts[i] > 0 && counts[i] < 4 &&
			!(isJokerIdx(i) && hasRocketCounts(counts)) // 王炸组件不可拆
	}
	// 中段单张（7~J）：防地主跟跑小牌且不浪费掌权牌
	for i := rankIdx(card.RankJ); i >= rankIdx(card.Rank7); i-- {
		if usable(i) && counts[i] == 1 {
			return pickCardsByRank(hand, idxRank(i), 1)
		}
	}
	// 最大的整组单张（≤A）：逼地主消耗 2/王来压制
	for i := rankIdx(card.RankA); i >= 0; i-- {
		if usable(i) && counts[i] == 1 {
			return pickCardsByRank(hand, idxRank(i), 1)
		}
	}
	// 中段对子（7~J）
	for i := rankIdx(card.RankJ); i >= rankIdx(card.Rank7); i-- {
		if usable(i) && counts[i] == 2 {
			return pickCardsByRank(hand, idxRank(i), 2)
		}
	}
	// 无合适单张 → 最大的整组对子（≤A）
	for i := rankIdx(card.RankA); i >= 0; i-- {
		if usable(i) && counts[i] == 2 {
			return pickCardsByRank(hand, idxRank(i), 2)
		}
	}
	return nil
}

// fallbackLead 兜底领出：打出手中面值最小的一组牌（整组不拆），
// 仅在手上只剩炸弹类组合或所有候选都增手数时使用
func fallbackLead(hand []card.Card) []card.Card {
	if len(hand) == 0 {
		return nil
	}
	// 按点数统计并取最小点数
	counts := make(map[card.Rank]int)
	for _, c := range hand {
		counts[c.Rank]++
	}
	var minRank card.Rank
	found := false
	for r := card.Rank3; r <= card.RankRedJoker; r++ {
		if counts[r] > 0 {
			minRank, found = r, true
			break
		}
	}
	if !found {
		return []card.Card{smallestCard(hand)}
	}

	switch n := counts[minRank]; {
	case n >= 4:
		// 四张：四带二（带牌只取现成单张，不拆牌），凑不出则整组炸弹
		if kickers := fallbackKickers(hand, minRank, 2); len(kickers) == 2 {
			return append(pickCardsByRank(hand, minRank, 4), kickers...)
		}
		return pickCardsByRank(hand, minRank, 4)
	case n == 3:
		// 三条：三带一（带牌只取现成单张），凑不出则整组三条
		if kickers := fallbackKickers(hand, minRank, 1); len(kickers) == 1 {
			return append(pickCardsByRank(hand, minRank, 3), kickers...)
		}
		return pickCardsByRank(hand, minRank, 3)
	case n == 2:
		return pickCardsByRank(hand, minRank, 2)
	default:
		return pickCardsByRank(hand, minRank, 1)
	}
}

// fallbackKickers 为带牌选取 n 张现成单张（仅取恰好单张的点数，绝不拆牌），取面值最小者；
// 若结果含掌权王牌（Q/K/A/2/王），改按点数升序从最小非炸弹组（对子/三条）拆出小牌，
// 便于带走小牌、把大牌留作后手夺权。
func fallbackKickers(hand []card.Card, exclude card.Rank, n int) []card.Card {
	counts := make(map[card.Rank]int)
	for _, c := range hand {
		counts[c.Rank]++
	}
	var kickers []card.Card
	for r := card.Rank3; r <= card.RankRedJoker && len(kickers) < n; r++ {
		if r == exclude || counts[r] != 1 {
			continue
		}
		kickers = append(kickers, pickCardsByRank(hand, r, 1)...)
	}
	if len(kickers) == n {
		for _, k := range kickers {
			if k.Rank >= card.RankQ {
				// 首选带牌含掌权王牌：改拆小牌结构（炸弹不拆）
				if alt := smallestSplitKickers(counts, hand, exclude, n); alt != nil {
					return alt
				}
				break
			}
		}
	}
	return kickers
}

// smallestSplitKickers 按点数升序从最小非炸弹组（1~3 张）各拆 1 张，凑 n 张带牌
func smallestSplitKickers(counts map[card.Rank]int, hand []card.Card, exclude card.Rank, n int) []card.Card {
	var kickers []card.Card
	for r := card.Rank3; r <= card.RankRedJoker && len(kickers) < n; r++ {
		if r == exclude || counts[r] < 1 || counts[r] > 3 {
			continue
		}
		kickers = append(kickers, pickCardsByRank(hand, r, 1)...)
	}
	if len(kickers) < n {
		return nil
	}
	return kickers
}

// pickCardsByRank 从手牌中取指定点数的 n 张实体卡牌
func pickCardsByRank(hand []card.Card, rank card.Rank, n int) []card.Card {
	out := make([]card.Card, 0, n)
	for _, c := range hand {
		if c.Rank == rank && len(out) < n {
			out = append(out, c)
		}
	}
	return out
}

// --- 跟牌辅助 ---

// pickShedFollow 跑牌方（地主/地主下家）的积极跟牌：目标为中段及以下
//（≤J）的单张/对子时，跟出最小的同路可压牌——允许消耗 Q/K/A（2 与王
// 留作后手收权），不拆炸弹/王炸、出牌后手数不增；找不到合适候选
// 返回 nil，交由通用跟牌逻辑处理。
func pickShedFollow(hand []card.Card, hints []rule.Hint, target rule.ParsedHand, current int, gctx *GameContext) []card.Card {
	if target.Type != rule.Single && target.Type != rule.Pair {
		return nil
	}
	if target.KeyRank > card.RankJ {
		return nil // 目标已是中段以上，维持掌权牌保护的保守跟牌
	}
	var best []card.Card
	for _, h := range hints {
		if !beatsTarget(h.Cards, target) {
			continue
		}
		ph, err := rule.ParseHand(h.Cards)
		if err != nil || ph.Type != target.Type {
			continue // 只同路跟牌（单接单、对接对），不升级牌型硬压
		}
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue
		}
		keep := false
		for _, c := range h.Cards {
			if c.Rank >= card.Rank2 {
				keep = true // 2/王留作后手收权，不用于跟牌
				break
			}
		}
		if keep {
			continue
		}
		if after := minHandCountWithCache(gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards))); after > current {
			continue // 破坏牌型结构（拆顺子等）的跟牌仍不做
		}
		if best == nil || sumRank(h.Cards) < sumRank(best) {
			best = h.Cards // 优先跟随出小牌：同手数取面值和最小的可压牌
		}
	}
	return best
}

// shouldUseBomb 是否值得动用炸弹：对手危急 / 自己可冲刺走完 / 手牌已很少
func shouldUseBomb(gctx GameContext, bombCards []card.Card) bool {
	if minOpponentCount(gctx) <= 2 {
		return true // 对手即将走完，必须压制
	}
	if len(gctx.Hand)-len(bombCards) <= 1 {
		return true // 炸完即可走完
	}
	if len(gctx.Hand) <= 5 {
		return true // 手牌很少，争取一波带走
	}
	return false
}

// anyBeat 候选中第一个能真正压过目标的出牌（候选已按评分升序，不考量手数/拆牌代价）
func anyBeat(hints []rule.Hint, target rule.ParsedHand) []card.Card {
	for _, h := range hints {
		if beatsTarget(h.Cards, target) {
			return h.Cards
		}
	}
	return nil
}

// teammateNeedsRelief 队友领出的是否为需要接应的小牌（单张/对子且点数 ≤10）：
// 此类小牌若无人接手会被地主轻易压住，且会让另一农民永远拿不到出牌权。
func teammateNeedsRelief(target rule.ParsedHand) bool {
	return (target.Type == rule.Single || target.Type == rule.Pair) && target.KeyRank <= card.Rank10
}

// pickReliefBeat 接手队友小牌的最小压牌：不拆炸弹、不动炸弹、出牌后手数不增，
// 优先出与队友同路的牌型（单接单、对接对），无同路再放宽到任意可压候选。
func pickReliefBeat(hand []card.Card, hints []rule.Hint, target rule.ParsedHand, current int, gctx *GameContext) []card.Card {
	pick := func(cands []rule.Hint) []card.Card {
		var best []card.Card
		bestAfter := 1 << 30
		for _, h := range cands {
			if !beatsTarget(h.Cards, target) || isBombLike(h.Cards) ||
				breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
				continue
			}
			after := minHandCountWithCache(gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
			if after > current {
				continue // 接手跑牌不能破坏自己的牌型结构，否则不如让队友走
			}
			if after < bestAfter || (after == bestAfter && best != nil && h.Score < scoreCards(best)) {
				best, bestAfter = h.Cards, after
			}
		}
		return best
	}
	var sameType []rule.Hint
	for _, h := range hints {
		if ph, err := rule.ParseHand(h.Cards); err == nil && ph.Type == target.Type {
			sameType = append(sameType, h)
		}
	}
	if c := pick(sameType); c != nil {
		return c
	}
	return pick(hints)
}

// --- 通用辅助 ---

// handPowerCount 手牌中 2/王（不可再生的收尾掌权牌）的张数
func handPowerCount(hand []card.Card) int {
	n := 0
	for _, c := range hand {
		if c.Rank >= card.Rank2 {
			n++
		}
	}
	return n
}

// farmerUpperAttackMode 农民上家主攻模式判定（每回合无状态重算，
// 随出牌进程自然从辅攻切换到主攻）：上家默认辅攻（顶牌位），
// 满足触发条件时该回合动态切换为主攻——以自己走完为第一路径，
// 领出跑小牌、跟队友小牌跑牌、积极夺权；2/王仍留作收尾底牌。
// 否决条件：地主 ≤2 张（死封路径优先，两分支天然互斥）；
// 队友 ≤2 张（队友临门一脚，送权期望值必高于抢跑）。
// 触发条件（任一）：A1 手牌 ≤6；A2 手数 ≤3；A3 2/王 ≥2 张且手牌 ≤10；
// A4 两步走完+绝对夺权牌已被验证。判定与各决策点行为见 docs/bots/ddz-bot.md §5.4。
func farmerUpperAttackMode(gctx GameContext) bool {
	if !isFarmerUpperSeat(gctx) {
		return false // 仅上家触发，下家（跑牌位）与地主不适用
	}
	if minOpponentCount(gctx) <= 2 || teammateCount(gctx) <= 2 {
		return false // 否决：死封 / 队友临门一脚，硬豁免优先于主攻
	}
	hand := gctx.Hand
	if len(hand) <= 6 {
		return true // A1 手牌少：约 2~3 个回合内可清空
	}
	if minHandCountWithCache(&gctx, handToCounts(hand)) <= 3 {
		return true // A2 牌型整：手数比张数更贴近"容易出完"
	}
	if handPowerCount(hand) >= 2 && len(hand) <= 10 {
		return true // A3 掌权牌充足：2/王 ≥2 可稳定收回牌权两次
	}
	return twoStepFinishWithControl(gctx) != nil // A4 已验证的冲刺路线
}

// beatsTarget 候选出牌能否真正压过目标牌（提示系统候选集存在跨型候选，
// 如目标为单张时含对子/三条建议；出牌前必须验证，否则服务端拒收导致回合卡死）
func beatsTarget(cards []card.Card, target rule.ParsedHand) bool {
	ph, err := rule.ParseHand(cards)
	if err != nil {
		return false
	}
	return rule.CanBeat(ph, target)
}

// containsTopPower 出牌中是否含 2 或王（终极掌权牌，拆牌重计划时仍保留）
func containsTopPower(cards []card.Card) bool {
	for _, c := range cards {
		if c.Rank >= card.Rank2 {
			return true
		}
	}
	return false
}

// isBombLike 是否炸弹或王炸（按张数与点数快速判断）
func isBombLike(cards []card.Card) bool {
	ph, err := rule.ParseHand(cards)
	if err != nil {
		return false
	}
	return ph.Type == rule.Bomb || ph.Type == rule.Rocket
}

// breaksBomb 候选出牌是否拆散了手牌中的炸弹（候选本身不是炸弹时）
func breaksBomb(hand, cards []card.Card) bool {
	counts := handToCounts(hand)
	for _, c := range cards {
		if counts[rankIdx(c.Rank)] == 4 {
			return true
		}
	}
	return false
}

// breaksRocket 候选出牌是否拆散了王炸（手持双王时单出一张王）
func breaksRocket(hand, cards []card.Card) bool {
	counts := handToCounts(hand)
	if !hasRocketCounts(counts) {
		return false
	}
	for _, c := range cards {
		if isJokerIdx(rankIdx(c.Rank)) {
			return true
		}
	}
	return false
}

// splitPenalty 拆牌代价：候选出牌拆散的同点数组数量
// （某点数只带走部分张数且仍有剩余 → 原有的对子/三条被拆出新的更小组）。
// 0 表示整组带走、不破坏手牌结构；用于同手数候选间优先选择不拆牌的出法。
func splitPenalty(hand, cards []card.Card) int {
	counts := handToCounts(hand)
	taken := make(map[card.Rank]int, len(cards))
	for _, c := range cards {
		taken[c.Rank]++
	}
	penalty := 0
	for r, n := range taken {
		if n < counts[rankIdx(r)] {
			penalty++
		}
	}
	return penalty
}

// sumRank 牌组点数总和（面值比较用）
func sumRank(cards []card.Card) int {
	s := 0
	for _, c := range cards {
		s += int(c.Rank)
	}
	return s
}

// scoreCards 重新计算候选评分（与 rule 包评分一致，用于同手数比较）
func scoreCards(cards []card.Card) int {
	sum := sumRank(cards)
	if isBombLike(cards) {
		if len(cards) == 2 {
			sum += 40
		} else {
			sum += 30
		}
	} else if len(cards) >= 2 {
		sum -= len(cards) * 2
	}
	return sum
}

// isJokerIdx 是否王牌下标
func isJokerIdx(i int) bool { return i == idxBlackJoker || i == idxRedJoker }

// hasRocketCounts 计数数组中是否同时有大小王（王炸组件不可拆）
func hasRocketCounts(c [rankCount]int) bool {
	return c[idxBlackJoker] > 0 && c[idxRedJoker] > 0
}

// biggestSingleOrPair 手中最大的单张或对子（残局死封用）：
// 不拆炸弹、不拆王炸；优先单张（覆盖面广）
func biggestSingleOrPair(hand []card.Card) []card.Card {
	counts := handToCounts(hand)
	hasRocket := hasRocketCounts(counts)
	usable := func(i int) bool {
		return counts[i] > 0 && counts[i] < 4 && !(hasRocket && isJokerIdx(i))
	}
	for i := rankCount - 1; i >= 0; i-- {
		if usable(i) {
			return pickCardsByRank(hand, idxRank(i), 1)
		}
	}
	for i := rankCount - 1; i >= 0; i-- {
		if counts[i] >= 2 && counts[i] < 4 {
			return pickCardsByRank(hand, idxRank(i), 2)
		}
	}
	return nil
}

// strongestBeat 候选压牌中最强的一手（王炸 > 炸弹 > 同型大点数），仅限能真正压过目标的候选
func strongestBeat(hints []rule.Hint, target rule.ParsedHand) []card.Card {
	var best []card.Card
	bestWeight := -1
	bestKey := card.Rank(0)
	for _, h := range hints {
		ph, err := rule.ParseHand(h.Cards)
		if err != nil || !rule.CanBeat(ph, target) {
			continue
		}
		w := 0
		switch ph.Type {
		case rule.Rocket:
			w = 2
		case rule.Bomb:
			w = 1
		}
		if w > bestWeight || (w == bestWeight && ph.KeyRank > bestKey) {
			best, bestWeight, bestKey = h.Cards, w, ph.KeyRank
		}
	}
	return best
}

// isControlPlay 该出牌是否为绝对控制牌（打出后必收回出牌权）：
// 单张/对子且未知牌池中无更大的同路牌
func isControlPlay(unknown [rankCount]int, cards []card.Card) bool {
	ph, err := rule.ParseHand(cards)
	if err != nil {
		return false
	}
	return isControlParsed(unknown, ph)
}

// isControlParsed 绝对控制牌判断（已解析形态）：单张/对子且未知牌池中无更大的同路牌
func isControlParsed(unknown [rankCount]int, ph rule.ParsedHand) bool {
	switch ph.Type {
	case rule.Single:
		return isAbsoluteControl(unknown, ph.KeyRank, 1)
	case rule.Pair:
		return isAbsoluteControl(unknown, ph.KeyRank, 2)
	}
	return false
}

// teammateCount 农民队友的剩余牌数（地主无队友，返回极大值）
func teammateCount(gctx GameContext) int {
	if gctx.UpIsLandlord {
		return gctx.PlayerCounts[1]
	}
	if gctx.DownIsLandlord {
		return gctx.PlayerCounts[0]
	}
	return 1 << 30
}

// landlordPassInfo 农民视角下地主座位的过牌推理信息
func landlordPassInfo(gctx GameContext) PassInfo {
	if gctx.UpIsLandlord {
		return gctx.PassInfos[0]
	}
	if gctx.DownIsLandlord {
		return gctx.PassInfos[1]
	}
	return PassInfo{}
}

// noBeatFromPass 过牌推理：该玩家曾对参照牌过牌，说明其当时没有更大的同路牌；
// 因此同牌型且点数不低于参照牌的牌他也压不过（其再次出牌或开新轮时记录已失效）。
// 优先使用单轮信息（info.Valid && info.Target），无单轮信息时使用累积过牌声明。
func noBeatFromPass(info PassInfo, ph rule.ParsedHand) bool {
	// 优先单轮信息
	if info.Valid && !info.Target.IsEmpty() {
		return info.Target.Type == ph.Type && ph.KeyRank >= info.Target.KeyRank
	}
	// 无单轮信息时，检查累积过牌声明
	if info.CumulativePass != nil {
		if cumRank, ok := info.CumulativePass[ph.Type]; ok {
			return ph.KeyRank >= cumRank
		}
	}
	return false
}

// bombThreatExists 未知池中是否存在炸弹/火箭威胁：某点数全部 4 张在对手方，
// 或王牌在对手方/未跟踪（预估胜出时必须排除被炸的可能）
func bombThreatExists(unknown [rankCount]int) bool {
	return len(potentialBombRanks(unknown)) > 0 ||
		unknown[idxBlackJoker] != 0 || unknown[idxRedJoker] != 0
}

// likelyWin 预估胜出：打出该手牌后牌权大概率回到己方——
// 绝对控制牌，或过牌推理确认威胁方全部压不过且无炸弹威胁。
// 农民威胁方仅地主（队友不会压自己），地主威胁方为两家农民。
func likelyWin(gctx GameContext, unknown [rankCount]int, ph rule.ParsedHand) bool {
	if isControlParsed(unknown, ph) {
		return true
	}
	if bombThreatExists(unknown) {
		return false
	}
	if gctx.IsLandlord {
		return noBeatFromPass(gctx.PassInfos[0], ph) && noBeatFromPass(gctx.PassInfos[1], ph)
	}
	return noBeatFromPass(landlordPassInfo(gctx), ph)
}

// isRiskyBigLead 潜在炸弹预警下的风险领出判断：
// 非控制的大点数裸单张/裸对子（Q 及以上），对手成炸时会被白炸并丢牌权
func isRiskyBigLead(cards []card.Card, unknown [rankCount]int) bool {
	if isControlPlay(unknown, cards) {
		return false
	}
	ph, err := rule.ParseHand(cards)
	if err != nil {
		return false
	}
	if ph.Type != rule.Single && ph.Type != rule.Pair {
		return false
	}
	return ph.KeyRank >= card.RankQ
}

// twoStepFinishWithControl 两步走完 + 绝对夺权牌优化
// 如果手牌可以分两步走完，且手中有绝对夺权的王牌（根据记牌和对手余牌计算，
// 敌对方大概率没有更大的或没有该牌型），优先出夺权牌拿到出牌权，再出剩下的牌
// 不限制手牌数量，支持大批量出牌（顺子、连对、飞机带翅等）
func twoStepFinishWithControl(gctx GameContext) []card.Card {
	hand := gctx.Hand
	unknown := unknownCounts(gctx.RemainingCards, hand)
	
	// 遍历所有候选，寻找能清空手牌的组合
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		remaining := subtractCounts(handToCounts(hand), handToCounts(h.Cards))
		remainingCards := countsToCards(remaining)
		
		// 检查剩余部分是否能一手出完
		if len(remainingCards) == 0 {
			continue // 这是 finishingLead 已处理的情况
		}
		
		if ph, err := rule.ParseHand(remainingCards); err == nil && ph.Type != rule.Invalid {
			// 剩余部分能一手出完，现在检查当前候选是否是绝对夺权牌
			currentPh, cerr := rule.ParseHand(h.Cards)
			if cerr != nil {
				continue
			}
			
			// 检查是否是绝对控制牌（单张/对子/炸弹/顺子等任何牌型）
			if isTwoStepControl(currentPh, unknown) {
				return h.Cards
			}
		}
	}
	
	return nil
}

// isTwoStepControl 检查某牌型是否是两步走完场景下的绝对夺权牌
// 支持所有牌型：单张、对子、顺子、连对、飞机、炸弹等
func isTwoStepControl(ph rule.ParsedHand, unknown [rankCount]int) bool {
	switch ph.Type {
	case rule.Single:
		// 单张：检查是否有更大的单张在未知池中
		for i := rankIdx(ph.KeyRank) + 1; i < rankCount; i++ {
			if unknown[i] > 0 || unknown[i] == -1 {
				return false // 有更大牌或未知
			}
		}
		return true
		
	case rule.Pair:
		// 对子：检查是否有更大的对子在未知池中
		for i := rankIdx(ph.KeyRank) + 1; i < rankCount; i++ {
			if unknown[i] >= 2 || unknown[i] == -1 {
				return false
			}
		}
		return true
		
	case rule.Trio:
		// 三条：检查是否有更大的三条在未知池中
		for i := rankIdx(ph.KeyRank) + 1; i < rankCount; i++ {
			if unknown[i] >= 3 || unknown[i] == -1 {
				return false
			}
		}
		return true
		
	case rule.Straight:
		// 顺子：检查是否有更大的顺子（相同长度）在未知池中
		// 顺子的 KeyRank 是最大点数
		length := ph.Length
		startIdx := rankIdx(ph.KeyRank) - length + 1 // 起始点数的索引
		if startIdx < 0 {
			return false
		}
		
		// 从当前顺子的下一个点数开始，检查是否存在更大的连续序列
		for start := startIdx + 1; start <= rankCount-length; start++ {
			// 检查从这个点数开始的 length 个连续点数是否都可用
			hasBigger := true
			for j := 0; j < length; j++ {
				idx := start + j
				if idx >= rankCount || (unknown[idx] <= 0 && unknown[idx] != -1) {
					hasBigger = false
					break
				}
			}
			if hasBigger {
				return false // 存在更大的顺子
			}
		}
		return true
		
	case rule.PairStraight:
		// 连对：检查是否有更大的连对（相同长度）在未知池中
		length := ph.Length
		startIdx := rankIdx(ph.KeyRank) - length + 1
		if startIdx < 0 {
			return false
		}
		
		for start := startIdx + 1; start <= rankCount-length; start++ {
			hasBigger := true
			for j := 0; j < length; j++ {
				idx := start + j
				if idx >= rankCount || (unknown[idx] < 2 && unknown[idx] != -1) {
					hasBigger = false
					break
				}
			}
			if hasBigger {
				return false
			}
		}
		return true
		
	case rule.Plane, rule.PlaneWithSingles, rule.PlaneWithPairs:
		// 飞机（带翅或不带）：检查是否有更大的飞机（相同长度）在未知池中
		length := ph.Length
		startIdx := rankIdx(ph.KeyRank) - length + 1
		if startIdx < 0 {
			return false
		}
		
		for start := startIdx + 1; start <= rankCount-length; start++ {
			hasBigger := true
			for j := 0; j < length; j++ {
				idx := start + j
				if idx >= rankCount || (unknown[idx] < 3 && unknown[idx] != -1) {
					hasBigger = false
					break
				}
			}
			if hasBigger {
				return false
			}
		}
		return true
		
	case rule.Bomb:
		// 炸弹：总是绝对控制（除非可能有火箭，但概率极低）
		return true
		
	case rule.Rocket:
		// 王炸：绝对最大
		return true
		
	default:
		return false
	}
}

// sprintMode 残局冲刺模式：自己手数 ≤3（至多 3 手可出完），且队友与敌方
// 余牌都未到临门阈值（敌方最少 >2、农民队友 >2）。此时无论地主/农民、
// 上家/下家，都以攻为主：优先出小牌与带牌散牌、掌权牌作夺权资源回收牌权、
// 最后一手保留大牌便于跟随取胜。敌方 ≤2 时死封路径优先，队友 ≤2 时
// 送权优先，两者均否决冲刺。
func sprintMode(gctx GameContext) bool {
	if minHandCountWithCache(&gctx, handToCounts(gctx.Hand)) > 3 {
		return false // 手数还多：维持常规攻防节奏
	}
	if minOpponentCount(gctx) <= 2 {
		return false // 敌方临门：死封优先于冲刺
	}
	if !gctx.IsLandlord && teammateCount(gctx) <= 2 {
		return false // 队友临门：送权优先于冲刺
	}
	return true
}

// sprintControl 冲刺候选的绝对掌权判断：在 isTwoStepControl 基础上
// 补充三带一/三带二（带牌不影响压制力，主体三条无更大即可掌权）
func sprintControl(ph rule.ParsedHand, unknown [rankCount]int) bool {
	if ph.Type == rule.TrioWithSingle || ph.Type == rule.TrioWithPair {
		return isTwoStepControl(rule.ParsedHand{Type: rule.Trio, KeyRank: ph.KeyRank}, unknown)
	}
	return isTwoStepControl(ph, unknown)
}

// sprintLead 残局冲刺领出（手数 ≤3 且双方无临门危机）：以攻为主，
//   - 优先减少手数：多走手数、逐手清空；
//   - 掌权牌作夺权资源：打出后大概率收回牌权（绝对掌权/过牌推理）的候选优先；
//   - 优先出小牌与带牌散牌：同手数先出小点数候选（小牌先出、大牌留后），
//     三条必带翅（三带一/三带二优于裸三条，顺带清走小牌）；
//   - 最后一手尽量大：倒数第二手非绝对掌权时，保留最强的一路牌收尾，
//     便于最后跟随对手出牌取胜。
func sprintLead(gctx GameContext) []card.Card {
	hand := gctx.Hand
	unknown := unknownCounts(gctx.RemainingCards, hand)
	current := minHandCountWithCache(&gctx, handToCounts(hand))

	type cand struct {
		cards    []card.Card
		after    int  // 打出后剩余手数
		wins     bool // 当前这手打出后大概率收回牌权
		finisher bool // 剩余最后一手是绝对掌权牌（便于跟随取胜）
		maxRank  card.Rank
		score    int
	}
	var cands []cand
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue // 炸弹/王炸留作收尾预案
		}
		afterCounts := subtractCounts(handToCounts(hand), handToCounts(h.Cards))
		after := minHandCountWithCache(&gctx, afterCounts)
		if after >= current {
			continue // 冲刺阶段绝不允许手数不降
		}
		wins, finisher := false, false
		if ph, err := rule.ParseHand(h.Cards); err == nil {
			wins = likelyWin(gctx, unknown, ph) || sprintControl(ph, unknown)
		}
		if after == 1 {
			if fph, err := rule.ParseHand(countsToCards(afterCounts)); err == nil {
				finisher = likelyWin(gctx, unknown, fph) || sprintControl(fph, unknown)
			}
		}
		maxRank := h.Cards[0].Rank
		for _, c := range h.Cards[1:] {
			if c.Rank > maxRank {
				maxRank = c.Rank
			}
		}
		cands = append(cands, cand{cards: h.Cards, after: after, wins: wins,
			finisher: finisher, maxRank: maxRank, score: sumRank(h.Cards)})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.after != b.after {
			return a.after < b.after // 手数最优先：逐手清空
		}
		if a.wins != b.wins {
			return a.wins // 掌权牌回收牌权优先
		}
		if a.finisher != b.finisher {
			return a.finisher // 留绝对掌权牌收尾
		}
		if a.maxRank != b.maxRank {
			return a.maxRank < b.maxRank // 小牌先出，大牌留最后一手
		}
		if len(a.cards) != len(b.cards) {
			return len(a.cards) > len(b.cards) // 同主体优先带牌散牌（三带一优于裸三条）
		}
		return a.score < b.score
	})
	return cands[0].cards
}
