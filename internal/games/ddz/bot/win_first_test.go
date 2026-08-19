package bot

import (
	"context"
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// TestLeadPairFinishesHand P0 硬规则：领出手牌恰为一对 → 整对打出清空手牌
//（P0 检查必须前置于残局 MCTS，见 docs/bots/ddz-bot.md §5.1）。
func TestLeadPairFinishesHand(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// MCTS 接管路径：手牌 ≤5 张必触发残局增强，P0 应先于 MCTS 拦截
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("9 9"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		RemainingCards: fullRemaining(),
	}))
	if parsed.Type != rule.Pair {
		t.Errorf("领出手牌恰为一对应整对打出，实际 %s", cardsToStr(parsed.Cards))
	}

	// 队友豁免路径（MCTS 不接管农民跟牌）：队友出对 4，手牌恰为整对 6 → 直接打出获胜
	parsed = assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("6 6"),
		RecentPlays:    [2]PlayRecord{play("4 4", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}))
	if parsed.Type != rule.Pair || parsed.KeyRank != card.Rank6 {
		t.Errorf("手牌恰为更大的整对应一次走完，实际 %s", cardsToStr(parsed.Cards))
	}
}

// TestFollow_StraightWithSplitFollows 用户场景回归（F1/F2）：上家出顺子 34567，
// 手牌含可管的 45678（需拆对）→ 应跟出而非自动过
func TestFollow_StraightWithSplitFollows(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 3 4 4 5 5 6 6 7 7 8"),
		RecentPlays:    [2]PlayRecord{play("3 4 5 6 7", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}))
	if parsed.Type != rule.Straight || len(parsed.Cards) != 5 {
		t.Errorf("应跟出更大的顺子 45678，实际 %s", cardsToStr(parsed.Cards))
	}
}

// TestFollow_MultiTypeTargetSplitAllowed 目标为连对等多牌型时，手数变差的
// 跟牌候选不再直接让牌（P2 节奏价值 > P3 结构代价）；单牌目标维持手数保护
func TestFollow_MultiTypeTargetSplitAllowed(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 地主出连对 334455：跟出 445566 后剩 7788QK（手数 3→4 变差），
	// 但对手一手可走 6 张，放行的节奏价值更高 → 应跟出
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("4 4 5 5 6 6 7 7 8 8 Q K"),
		RecentPlays:    [2]PlayRecord{play("3 3 4 4 5 5", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}))
	if parsed.Type != rule.PairStraight || parsed.KeyRank != card.Rank6 {
		t.Errorf("应跟出连对 445566，实际 %s", cardsToStr(parsed.Cards))
	}

	// 对照：单牌目标维持手数保护 —— 整手恰为六连对（手数 1），
	// 跟单 5 的任何候选都会拆坏连对结构 → 让牌
	if got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("4 4 5 5 6 6 7 7 8 8 9 9"),
		RecentPlays:    [2]PlayRecord{play("5", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}); got != nil {
		t.Errorf("单牌目标手数变差应让牌，却出了 %s", cardsToStr(got))
	}
}

// TestIntentionalImperfection_NeverReplaceWinningPlay 清空手牌的必胜出牌
// 永不被故意犯错机制替换（胜利优先级阶梯最高层）
func TestIntentionalImperfection_NeverReplaceWinningPlay(t *testing.T) {
	t.Parallel()
	hand := cards("3 3 4 4 5 5 6 6")
	chosen := cards("3 3 4 4 5 5 6 6")
	got := intentionalImperfection(DefaultPersonalities["easy"], hand, chosen, nil, GameContext{})
	if cardsToStr(got) != cardsToStr(chosen) {
		t.Errorf("清空手牌的出牌不应被替换，实际 %s", cardsToStr(got))
	}
}
