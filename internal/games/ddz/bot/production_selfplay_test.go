package bot

import (
	"math/rand/v2"
	"os"
	"testing"
)

// production_selfplay_test.go Phase 6 验收：按生产接线跑自战——hard 席位用增强
// 引擎（Phase 1~5 开关全开），easy/normal 席位保持基线引擎（出牌开关全关），
// 档位差异仍只由记牌视图完整度 + 人格化故意犯错承载。
// 校验两件事：① 出牌不变量（零出牌/非法出牌为零）；② 档位差异未被新逻辑抹平。

// productionEngines 各难度档位对应的生产接线引擎（同档位席位共用实例：
// Engine 除构造期写入的只读配置外无状态，跨席位复用安全）
func productionEngines() map[string]*Engine {
	out := make(map[string]*Engine, 3)
	for _, d := range []string{"easy", "normal", "hard"} {
		e := newTestEngine().WithConfig(ProductionConfig())
		if d == "hard" {
			e.WithDifficultyConfig("hard", HardPlayConfig())
		}
		out[d] = e
	}
	return out
}

func TestSelfPlay_ProductionWiring(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("自战测试需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时自战模拟")
	}

	const games = 300
	diffNames := []string{"easy", "normal", "hard"}
	byDiff := productionEngines()
	// XGAMES_TIERBASE=1：同种子的全基线对照（三档都不开增强），用于量化
	// hard 增强在混档对局中的席位胜率变化
	if os.Getenv("XGAMES_TIERBASE") == "1" {
		plain := newTestEngine()
		for _, d := range diffNames {
			byDiff[d] = plain
		}
		t.Log("对照模式：三档均为基线引擎（无 hard 增强）")
	}

	type seatStat struct {
		seats, wins, plays, silent int
	}
	stats := map[string]*seatStat{}
	for _, d := range diffNames {
		stats[d] = &seatStat{}
	}

	for game := 0; game < games; game++ {
		rng := rand.New(rand.NewPCG(2026, uint64(game*31+7)))
		var diffs [3]string
		var engines [3]*Engine
		for seat := range diffs {
			diffs[seat] = diffNames[(game+seat)%3]
			engines[seat] = byDiff[diffs[seat]]
			stats[diffs[seat]].seats++
		}
		landlord := game % 3
		winner, counts, hintTurns := simulateGame(t, engines, diffs, landlord, rng)

		for seat, n := range counts {
			stats[diffs[seat]].plays += n
			if n > 0 {
				continue
			}
			stats[diffs[seat]].silent++
			if hintTurns[seat] > 0 {
				t.Errorf("[%s] game=%d 座位 %d 存在 %d 个可压回合却整局沉默（出牌次数=%v）",
					diffs[seat], game, seat, hintTurns[seat], counts)
			}
		}
		if winner < 0 {
			continue
		}
		if winner == landlord {
			stats[diffs[landlord]].wins++
			continue
		}
		for seat := 0; seat < 3; seat++ {
			if seat != landlord {
				stats[diffs[seat]].wins++
			}
		}
	}

	rate := map[string]float64{}
	for _, d := range diffNames {
		s := stats[d]
		rate[d] = 100 * float64(s.wins) / float64(s.seats)
		t.Logf("生产接线 难度 %-6s 席位数=%d 胜场=%d 席位胜率=%.1f%% 累计出牌=%d 零出牌席位=%d",
			d, s.seats, s.wins, rate[d], s.plays, s.silent)
	}
	// 档位差异仍在：easy（12% 故意犯错 + 关键牌记牌）必须明显弱于 normal/hard。
	// hard 与 normal 的差距小于样本噪声（300 席位 σ≈2.9pp），故只记录不断言；
	// 增强收益的量化判据走竞技场（TestArena_ConfigVsBaseline，同副牌配对对战）
	if rate["easy"] >= rate["normal"] || rate["easy"] >= rate["hard"] {
		t.Errorf("档位差异丢失：easy 胜率 %.1f%% 未低于 normal %.1f%% / hard %.1f%%",
			rate["easy"], rate["normal"], rate["hard"])
	}
	t.Logf("hard 相对 normal 胜率差 %+.1fpp（增强席位 vs 基线席位）", rate["hard"]-rate["normal"])
}
