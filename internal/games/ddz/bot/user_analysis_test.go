package bot

import (
	"testing"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// 只剩对 2 的地主，领出。引擎最优与实际一致 → 不判失误
func TestAnalyzeUserMove_SameAsEngine(t *testing.T) {
	hand := []card.Card{
		{Rank: card.Rank2, Suit: card.Spade},
		{Rank: card.Rank2, Suit: card.Heart},
	}
	snap := UserMoveSnapshot{
		Hand:       hand,
		MustPlay:   true,
		CanBeat:    false,
		IsLandlord: true,
	}
	// 用分析器同款引擎求最优解，作为"实际出牌"
	gctx := buildUserGameContext(snap)
	best := unifiedPlay(gctx)
	if best == nil {
		t.Fatal("领出时引擎应给出出牌")
	}
	snap.Chosen = append([]card.Card(nil), best...)

	a := AnalyzeUserDecisions([]replay.PlayerDecision{{
		Round: 1, PlayerID: "p1", PlayerName: "玩家一",
		ChosenDesc: "出牌", Snapshot: snap,
	}})[0]

	if a.IsMistake {
		t.Errorf("与引擎一致不应判失误, got %+v", a)
	}
	if a.BestDesc == "" {
		t.Error("BestDesc 不应为空")
	}
	// 出完对 2 即取胜：出牌后最少手数应为 0
	if got := minHandCountAfter(&gctx, best); got != 0 {
		t.Errorf("出完 %v 后最少手数 = %d, want 0", best, got)
	}
}

// 该压不压（有单张可压却过牌）→ 判失误
func TestAnalyzeUserMove_PassWhenCanBeat(t *testing.T) {
	hand := []card.Card{
		{Rank: card.Rank2, Suit: card.Spade},
		{Rank: card.Rank2, Suit: card.Heart},
	}
	// 上家出单张 K，对 2 是唯一起作用的压制牌，实际选择过牌
	last, err := rule.ParseHand([]card.Card{{Rank: card.RankK, Suit: card.Spade}})
	if err != nil {
		t.Fatal(err)
	}
	snap := UserMoveSnapshot{
		Hand:         hand,
		LastPlayed:   last,
		RecentPlays:  [2]PlayRecord{{Played: last, PlayerName: "农民A"}},
		MustPlay:     false,
		CanBeat:      true,
		IsLandlord:   true,
		PlayerCounts: [2]int{10, 10},
		Chosen:       nil, // 过牌
	}

	a := AnalyzeUserDecisions([]replay.PlayerDecision{{
		Round: 1, PlayerID: "p1", PlayerName: "玩家一",
		ChosenDesc: "过牌", IsPass: true, Snapshot: snap,
	}})[0]

	if !a.IsMistake {
		t.Errorf("有牌可压却过牌应判失误, got %+v", a)
	}
	if a.Severity == "" {
		t.Error("失误严重度不应为空")
	}
	if a.Reason == "" {
		t.Error("失误原因不应为空")
	}
}

// 非法快照应跳过
func TestAnalyzeUserDecisions_InvalidSnapshot(t *testing.T) {
	analyses := AnalyzeUserDecisions([]replay.PlayerDecision{{Round: 1, Snapshot: "bad"}})
	if len(analyses) != 0 {
		t.Errorf("非法快照应被跳过, got %d 条", len(analyses))
	}
}
