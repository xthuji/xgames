package bot

import (
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// situation.go 出牌规则的局势化判断：
//  1. 掌权王牌保护：大于 J 的牌（Q/K/A/2，前 3~4 位大牌含王）是控权核心，谨慎消耗、谨慎拆牌；
//  2. 领出配合：下家是地主 → 中段牌防止其跟跑小牌；下家是队友 → 走小牌/大批量出牌；自己是地主 → 出能继续掌权或大批量的牌型；
//  3. 快速跑牌：优先大批量牌型（连对/顺子/飞机/三带一/四带二）与最小小牌；下家是敌人时限制 6 以下小牌防跟跑；
//  4. 对手 <6 张 → 谨慎控牌，配合队友走牌或封堵对手。

// isPowerRank 掌权王牌：大于 J 的牌（Q/K/A/2），加上双王（王炸组件不可单出）
func isPowerRank(r card.Rank) bool {
	return r >= card.RankQ
}

// powerCardCount 出牌中消耗的掌权王牌张数
func powerCardCount(cards []card.Card) int {
	n := 0
	for _, c := range cards {
		if isPowerRank(c.Rank) || isJokerIdx(rankIdx(c.Rank)) {
			n++
		}
	}
	return n
}

// powerLeadCost 领出候选的掌权代价：非预估胜出的掌权牌消耗按 3 倍计罚
// （掌权王牌应留作后手收权，先手白送等于丢牌权）
func powerLeadCost(cards []card.Card, wins bool) int {
	p := powerCardCount(cards)
	if wins || p == 0 {
		return 0
	}
	return p * 3
}

// splitPenaltyEx 带掌权加权的拆牌代价：拆散掌权王牌的对子/三条代价加倍
// （对子或多个掌权王牌谨慎拆牌）
func splitPenaltyEx(hand, cards []card.Card) int {
	counts := handToCounts(hand)
	taken := make(map[card.Rank]int, len(cards))
	for _, c := range cards {
		taken[c.Rank]++
	}
	penalty := 0
	for r, n := range taken {
		if n < counts[rankIdx(r)] {
			penalty++
			if isPowerRank(r) {
				penalty++ // 掌权王牌拆牌额外加罚
			}
		}
	}
	return penalty
}

// maxOpponentCount 对手方最大的剩余牌数（判断是否处于快速跑牌阶段）
func maxOpponentCount(gctx GameContext) int {
	up, down := gctx.PlayerCounts[0], gctx.PlayerCounts[1]
	if gctx.IsLandlord {
		if up > down {
			return up
		}
		return down
	}
	if gctx.UpIsLandlord {
		return up
	}
	if gctx.DownIsLandlord {
		return down
	}
	if up > down {
		return up
	}
	return down
}

// nextSeatIsEnemy 下家是否敌人：领出后下家先行动，若为敌人须防其跟跑小牌
func nextSeatIsEnemy(gctx GameContext) bool {
	if gctx.IsLandlord {
		return true // 地主的下家必是农民
	}
	return gctx.DownIsLandlord
}

// nextSeatIsTeammate 下家是否农民队友（可以随意走小牌）
func nextSeatIsTeammate(gctx GameContext) bool {
	return !gctx.IsLandlord && !gctx.DownIsLandlord
}

// pickFastShed 快速跑牌（规则 3）：优先大批量牌型（连对/顺子/飞机/三带一/四带二），
// 其次最小的小单张/小对子；掌权王牌不用于跑牌。
// allowSmall=false 时（下家是敌人的防跟跑场景），避免送出 6 以下的小单/小对。
// 返回 nil 表示无合适候选，交由通用逻辑处理。
func pickFastShed(gctx GameContext, allowSmall bool) []card.Card {
	hand := gctx.Hand
	current := minHandCountWithCache(&gctx, handToCounts(hand))
	blockEnemy := nextSeatIsEnemy(gctx) && !allowSmall

	var best, bestBulk []card.Card
	bestScore := 1 << 30
	bestBulkScore := 1 << 30
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		if after > current {
			continue
		}
		ph, perr := rule.ParseHand(h.Cards)
		if perr != nil {
			continue
		}
		bulk := len(h.Cards) >= 5 // 大批量出牌
		if !bulk && ph.Type != rule.Single && ph.Type != rule.Pair {
			continue
		}
		// 防下家敌人走小牌：小单/小对仅在下家非敌人（队友）时优先
		if !bulk && blockEnemy && ph.KeyRank < card.Rank6 {
			continue
		}
		// 掌权王牌不用于快速跑牌
		if powerCardCount(h.Cards) > 0 {
			continue
		}
		s := sumRank(h.Cards)
		if bulk {
			if s < bestBulkScore {
				bestBulk, bestBulkScore = h.Cards, s
			}
		} else if s < bestScore {
			best, bestScore = h.Cards, s
		}
	}
	if bestBulk != nil {
		return bestBulk // 大批量出牌优先
	}
	return best
}

// pickCautiousLead 谨慎领出（规则 4，对手 <6 张）：
// 小牌会被对手用掌权牌压住后一波走完，领出应保持压制力——
// 优先大批量牌型与中大牌（A/2 的单张或对子，不拆炸弹/王炸），避免送小单/小对。
// 返回 nil 表示无合适候选。
func pickCautiousLead(gctx GameContext) []card.Card {
	hand := gctx.Hand
	current := minHandCountWithCache(&gctx, handToCounts(hand))

	var bestBulk, bestBig []card.Card
	bestBulkScore, bestBigScore := 1<<30, 1<<30
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if isBombLike(h.Cards) || breaksBomb(hand, h.Cards) || breaksRocket(hand, h.Cards) {
			continue
		}
		after := minHandCountWithCache(&gctx, subtractCounts(handToCounts(hand), handToCounts(h.Cards)))
		if after > current {
			continue
		}
		ph, perr := rule.ParseHand(h.Cards)
		if perr != nil {
			continue
		}
		s := sumRank(h.Cards)
		if len(h.Cards) >= 5 {
			if s < bestBulkScore {
				bestBulk, bestBulkScore = h.Cards, s
			}
			continue
		}
		switch ph.Type {
		case rule.Single, rule.Pair:
			// 中小单/对送给对手牌权，慎出；只保留 K/A/2 级别的压制性领出
			if ph.KeyRank < card.RankK || powerCardCount(h.Cards) != len(h.Cards) {
				continue
			}
			if s < bestBigScore {
				bestBig, bestBigScore = h.Cards, s
			}
		case rule.Trio, rule.TrioWithSingle, rule.TrioWithPair:
			if s < bestBigScore {
				bestBig, bestBigScore = h.Cards, s
			}
		}
	}
	if bestBulk != nil {
		return bestBulk
	}
	return bestBig
}

// finishingLead 一手带牌走完（规则 5）：三带一/三带二/四带二/飞机带翅等
// 带牌型恰好出完全手时直接打出，不拆牌、大量出牌。
// 整手能解析为单一牌型的情况已由调用方优先处理，这里覆盖
// "整体不是单一牌型、但某条带牌候选恰好清空手牌"的场景。
func finishingLead(hand []card.Card) []card.Card {
	for _, h := range rule.GenerateHints(hand, rule.ParsedHand{}) {
		if len(h.Cards) == len(hand) {
			// 排除王炸以外的炸弹（王炸已经在 GenerateHints 中作为最高优先级）
			// 但如果整手牌就是炸弹，应该允许直接出完
			if _, err := rule.ParseHand(h.Cards); err == nil {
				return h.Cards
			}
		}
	}
	return nil
}
