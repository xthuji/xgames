package bot

import (
	"io"
	"log/slog"
	"testing"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/mahjong/rule"
)

// 14 张手牌：三个万子顺子 + 三张筒子顺子 + 东/南/中 三张孤张字牌
func testHand() []int {
	return []int{0, 1, 2, 3, 4, 5, 9, 10, 11, 27, 28, 29, 30, 31}
}

func mahjongDecision(snap UserDiscardSnapshot) []replay.PlayerDecision {
	return []replay.PlayerDecision{{
		Round:      1,
		PlayerID:   "p1",
		PlayerName: "玩家一",
		ChosenDesc: "打牌",
		Snapshot:   snap,
	}}
}

// 打出引擎最优牌 → 不判失误
func TestAnalyzeUserDiscard_SameAsEngine(t *testing.T) {
	hand := testHand()
	engine := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	best := engine.DecideDiscardEnhanced(DiscardContext{Hand: hand})

	snap := UserDiscardSnapshot{Hand: hand, ActualTile: best, TurnNumber: 5}
	a := AnalyzeUserDecisions(mahjongDecision(snap))[0]

	if a.IsMistake {
		t.Errorf("与引擎一致不应判失误, got %+v", a)
	}
	if a.BestDesc != rule.DisplayName(best) {
		t.Errorf("BestDesc = %q, want %q", a.BestDesc, rule.DisplayName(best))
	}
}

// 明显亏向听的打牌 → 判失误
func TestAnalyzeUserDiscard_ShantenLoss(t *testing.T) {
	hand := testHand()
	counts := rule.CountsFromHand(hand)
	engine := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	best := engine.DecideDiscardEnhanced(DiscardContext{Hand: hand})

	// 找一张与最优解向听数不同的牌作为实际打牌
	seen := map[int]bool{best: true}
	badTile := -1
	for _, tile := range hand {
		if seen[tile] {
			continue
		}
		seen[tile] = true
		if rule.ShantenAfterDiscard(counts, tile) != rule.ShantenAfterDiscard(counts, best) {
			badTile = tile
			break
		}
	}
	if badTile < 0 {
		t.Skip("该手牌下所有打法向听数相同，跳过")
	}

	snap := UserDiscardSnapshot{Hand: hand, ActualTile: badTile, TurnNumber: 12}
	a := AnalyzeUserDecisions(mahjongDecision(snap))[0]

	if !a.IsMistake {
		t.Errorf("亏向听应判失误, got %+v", a)
	}
	if a.ActualScore <= a.BestScore {
		t.Errorf("实际打牌向听 %v 应劣于最优 %v（向听越少越好）", a.ActualScore, a.BestScore)
	}
}

// 非法快照应跳过
func TestAnalyzeUserDecisions_InvalidSnapshot(t *testing.T) {
	analyses := AnalyzeUserDecisions([]replay.PlayerDecision{{Round: 1, Snapshot: "bad"}})
	if len(analyses) != 0 {
		t.Errorf("非法快照应被跳过, got %d 条", len(analyses))
	}
}
