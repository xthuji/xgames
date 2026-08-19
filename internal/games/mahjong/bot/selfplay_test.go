package bot

import (
	"math/rand/v2"
	"os"
	"testing"
)

// TestSelfPlay_100Games 100 局自战回归（难度轮转分配）：四座位难度按 (game+seat)%3 轮转，
// easy/normal/hard 各覆盖约 1/3 席位；复用 simulateGameWithDifficulty 的生产口径模拟
// （DecideDiscardWithErrors 出牌 + DecideAction 碰胡 + 对手听牌推断），
// 统计各难度胡牌率/点炮率/自摸占比/均番与流局率，作为难度梯度与稳定性的回归基线。
func TestSelfPlay_100Games(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("自战测试需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时自战模拟")
	}
	eng := NewEngineWithRng(nil, rand.New(rand.NewPCG(7, 2026)))
	diffNames := []string{"easy", "normal", "hard"}

	type seatStat struct {
		seats, wins, selfWins, dealIns, fanSum int
	}
	stats := map[string]*seatStat{}
	for _, d := range diffNames {
		stats[d] = &seatStat{}
	}

	draws := 0
	for game := 0; game < 100; game++ {
		rng := rand.New(rand.NewPCG(2026, uint64(game*31+7)))
		var diffs [4]DifficultyConfig
		for seat := range diffs {
			name := diffNames[(game+seat)%3]
			diffs[seat] = DifficultyFor(name)
			stats[name].seats++
		}
		out := simulateGameWithDifficulty(t, eng, rng, diffs)
		if out.winner < 0 {
			draws++
			continue
		}
		winnerName := diffNames[(game+out.winner)%3]
		stats[winnerName].wins++
		stats[winnerName].fanSum += out.fan
		if out.winBySelf {
			stats[winnerName].selfWins++
		}
		if out.dealIn >= 0 {
			stats[diffNames[(game+out.dealIn)%3]].dealIns++
		}
	}

	for _, d := range diffNames {
		s := stats[d]
		t.Logf("难度 %-6s 席位=%d 胡=%d（自摸 %d）点炮=%d 均番=%.2f 胡牌率=%.1f%% 点炮率=%.1f%%",
			d, s.seats, s.wins, s.selfWins, s.dealIns, float64(s.fanSum)/float64(max(1, s.wins)),
			100*float64(s.wins)/float64(s.seats), 100*float64(s.dealIns)/float64(s.seats))
	}
	t.Logf("流局 %d 局（共 100 局）", draws)

	if decided := stats["easy"].wins + stats["normal"].wins + stats["hard"].wins; decided < 40 {
		t.Fatalf("100 局仅 %d 局分出胜负（流局 %d 局），决策逻辑可能存在严重问题", decided, draws)
	}
}
