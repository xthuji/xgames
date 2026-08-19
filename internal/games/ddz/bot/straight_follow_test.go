package bot

import (
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// TestStraightFollowWithSplit 测试顺子跟牌时需要拆牌的情况
func TestStraightFollowWithSplit(t *testing.T) {
	// 场景：前家出 3-4-5-6-7 顺子
	// 玩家手牌：3,3,4,4,5,5,6,6,7,7,8（有对子，需要拆开）
	hand := []card.Card{
		{Rank: card.Rank3, Suit: card.Spade},
		{Rank: card.Rank3, Suit: card.Heart},
		{Rank: card.Rank4, Suit: card.Spade},
		{Rank: card.Rank4, Suit: card.Heart},
		{Rank: card.Rank5, Suit: card.Spade},
		{Rank: card.Rank5, Suit: card.Heart},
		{Rank: card.Rank6, Suit: card.Spade},
		{Rank: card.Rank6, Suit: card.Heart},
		{Rank: card.Rank7, Suit: card.Spade},
		{Rank: card.Rank7, Suit: card.Heart},
		{Rank: card.Rank8, Suit: card.Spade},
	}

	target := rule.ParsedHand{
		Type:    rule.Straight,
		KeyRank: card.Rank7,
		Length:  5,
		Cards: []card.Card{
			{Rank: card.Rank3, Suit: card.Spade},
			{Rank: card.Rank4, Suit: card.Spade},
			{Rank: card.Rank5, Suit: card.Spade},
			{Rank: card.Rank6, Suit: card.Spade},
			{Rank: card.Rank7, Suit: card.Spade},
		},
	}

	// 生成跟牌候选
	hints := rule.GenerateHints(hand, target)

	t.Logf("手牌: %v", hand)
	t.Logf("目标: %v", target)
	t.Logf("生成的候选数量: %d", len(hints))

	for i, h := range hints {
		t.Logf("候选%d: %v (Score: %d)", i+1, h.Cards, h.Score)
	}

	// 检查是否有能管上的候选
	hasBeat := false
	for _, h := range hints {
		ph, err := rule.ParseHand(h.Cards)
		if err != nil {
			t.Logf("解析失败: %v", err)
			continue
		}
		t.Logf("解析候选: Type=%v, KeyRank=%v, Length=%d", ph.Type, ph.KeyRank, ph.Length)
		
		canBeat := rule.CanBeat(ph, target)
		t.Logf("CanBeat结果: %v (KeyRank: %d > %d)", canBeat, ph.KeyRank, target.KeyRank)
		
		if canBeat {
			hasBeat = true
			t.Logf("找到能管上的牌: %v", h.Cards)
			break
		}
	}

	if !hasBeat {
		t.Error("应该有能管上顺子的牌，但没有找到候选")
	}
}

// TestFinishInOneMove 测试一次走完的情况
func TestFinishInOneMove(t *testing.T) {
	// 场景：玩家手牌是一对，应该直接出完而不是拆成单张
	hand := []card.Card{
		{Rank: card.Rank3, Suit: card.Spade},
		{Rank: card.Rank3, Suit: card.Heart},
	}

	// 领出场景
	hints := rule.GenerateHints(hand, rule.ParsedHand{})

	t.Logf("手牌: %v", hand)
	t.Logf("生成的候选数量: %d", len(hints))

	for i, h := range hints {
		t.Logf("候选%d: %v (Score: %d, Len: %d)", i+1, h.Cards, h.Score, len(h.Cards))
	}

	// 检查是否有能一次出完的候选
	hasFinish := false
	for _, h := range hints {
		if len(h.Cards) == len(hand) {
			hasFinish = true
			t.Logf("找到能一次出完的牌: %v", h.Cards)
			break
		}
	}

	if !hasFinish {
		t.Error("应该有能一次出完的牌，但没有找到候选")
	}
}

// TestTwoStepFinishWithControl 测试两步走完且有夺权牌的情况
func TestTwoStepFinishWithControl(t *testing.T) {
	// 场景：玩家手牌可以分两步走完，且有绝对夺权的2
	hand := []card.Card{
		{Rank: card.Rank3, Suit: card.Spade},
		{Rank: card.Rank3, Suit: card.Heart},
		{Rank: card.Rank2, Suit: card.Spade}, // 绝对夺权牌
	}

	gctx := GameContext{
		Hand:          hand,
		MustPlay:      true,
		IsLandlord:    true,
		PlayerCounts:  [2]int{5, 5}, // 对手各有5张牌
		RemainingCards: make(map[card.Rank]int), // 简化：假设其他牌都已出完
	}

	// 初始化 RemainingCards：假设只有手中的牌和对手的10张牌未知
	for r := card.Rank3; r <= card.RankRedJoker; r++ {
		gctx.RemainingCards[r] = 4 // 默认每个点数都有4张
	}
	// 减去手中的牌
	for _, c := range hand {
		gctx.RemainingCards[c.Rank]--
	}

	t.Logf("手牌: %v", hand)

	// 检查是否能检测到两步走完的情况
	decision := unifiedLead(gctx)
	t.Logf("决策出牌: %v", decision)

	// 期望：先出2拿到牌权，再出对3
	// 或者：直接出对3（如果能确保收回牌权）
	if decision == nil {
		t.Error("应该有出牌决策")
	}
}

// TestTwoStepFinishWithStraight 测试两步走完且第一步是顺子的情况
func TestTwoStepFinishWithStraight(t *testing.T) {
	// 场景：手牌 = 顺子 3-4-5-6-7 + 单张 A
	// A 是绝对夺权牌（假设对手没有更大的牌）
	hand := []card.Card{
		{Rank: card.Rank3, Suit: card.Spade},
		{Rank: card.Rank4, Suit: card.Spade},
		{Rank: card.Rank5, Suit: card.Spade},
		{Rank: card.Rank6, Suit: card.Spade},
		{Rank: card.Rank7, Suit: card.Spade},
		{Rank: card.RankA, Suit: card.Spade}, // 绝对夺权单张
	}

	gctx := GameContext{
		Hand:         hand,
		MustPlay:     true,
		IsLandlord:   true,
		PlayerCounts: [2]int{5, 5},
		RemainingCards: make(map[card.Rank]int),
	}

	// 初始化：假设除了手中的牌，其他牌都在对手手中或已出
	for r := card.Rank3; r <= card.RankRedJoker; r++ {
		gctx.RemainingCards[r] = 4
	}
	for _, c := range hand {
		gctx.RemainingCards[c.Rank]--
	}
	// 模拟：2和王都已经出完，A是最大的
	gctx.RemainingCards[card.Rank2] = 0
	gctx.RemainingCards[card.RankBlackJoker] = 0
	gctx.RemainingCards[card.RankRedJoker] = 0

	t.Logf("手牌: %v", hand)
	t.Logf("RemainingCards[2]: %d", gctx.RemainingCards[card.Rank2])

	decision := unifiedLead(gctx)
	t.Logf("决策出牌: %v", decision)

	// 期望：优先出A拿到牌权，然后出顺子
	if decision == nil {
		t.Error("应该有出牌决策")
	}
	if len(decision) == 1 && decision[0].Rank == card.RankA {
		t.Logf("✓ 正确：优先出绝对夺权牌 A")
	} else if len(decision) == 5 {
		t.Logf("✓ 可接受：直接出顺子（可能A不够绝对控制）")
	} else {
		t.Errorf("意外决策: %v", decision)
	}
}

// TestTwoStepFinishWithPairStraight 测试两步走完且第一步是连对的情况
func TestTwoStepFinishWithPairStraight(t *testing.T) {
	// 场景：手牌 = 连对 33-44-55 + 对子 AA
	// AA 是绝对夺权对子
	hand := []card.Card{
		{Rank: card.Rank3, Suit: card.Spade},
		{Rank: card.Rank3, Suit: card.Heart},
		{Rank: card.Rank4, Suit: card.Spade},
		{Rank: card.Rank4, Suit: card.Heart},
		{Rank: card.Rank5, Suit: card.Spade},
		{Rank: card.Rank5, Suit: card.Heart},
		{Rank: card.RankA, Suit: card.Spade},
		{Rank: card.RankA, Suit: card.Heart},
	}

	gctx := GameContext{
		Hand:         hand,
		MustPlay:     true,
		IsLandlord:   true,
		PlayerCounts: [2]int{5, 5},
		RemainingCards: make(map[card.Rank]int),
	}

	for r := card.Rank3; r <= card.RankRedJoker; r++ {
		gctx.RemainingCards[r] = 4
	}
	for _, c := range hand {
		gctx.RemainingCards[c.Rank]--
	}
	// 模拟：2和K都已出完，AA是最大的对子
	gctx.RemainingCards[card.Rank2] = 0
	gctx.RemainingCards[card.RankK] = 0

	t.Logf("手牌: %v", hand)

	decision := unifiedLead(gctx)
	t.Logf("决策出牌: %v", decision)

	// 期望：优先出AA拿到牌权，然后出连对
	if decision == nil {
		t.Error("应该有出牌决策")
	}
	if len(decision) == 2 && decision[0].Rank == card.RankA {
		t.Logf("✓ 正确：优先出绝对夺权对子 AA")
	} else if len(decision) == 6 {
		t.Logf("✓ 可接受：直接出连对")
	} else {
		t.Errorf("意外决策: %v", decision)
	}
}
