package bot

import (
	"context"
	"math/rand/v2"
	"testing"

	"xgames/internal/games/mahjong/rule"
)

// TestDebug_SingleGame 单局详细调试：输出每步的手牌、向听数、决策
func TestDebug_SingleGame(t *testing.T) {
	eng := NewEngine(nil)
	rng := rand.New(rand.NewPCG(42, 123))

	// 初始化牌墙
	wall := rule.NewWall()
	rng.Shuffle(len(wall), func(i, j int) {
		wall[i], wall[j] = wall[j], wall[i]
	})

	// 发牌
	hands := make([][]int, 4)
	for i := 0; i < 4; i++ {
		drawn, rest := rule.DrawFromWall(wall, 13)
		hands[i] = drawn
		wall = rest
	}

	// 庄家多摸一张
	dealer := 0
	drawn, rest := rule.DrawFromWall(wall, 1)
	hands[dealer] = append(hands[dealer], drawn[0])
	wall = rest

	t.Logf("=== 初始手牌 ===")
	for i, hand := range hands {
		counts := rule.CountsFromHand(hand)
		shanten := rule.ShantenNumber(counts)
		isTenpai := rule.IsTenpai(counts)
		t.Logf("玩家 %d: 手牌=%v 向听数=%d 听牌=%v", i, hand, shanten, isTenpai)
	}

	discardPools := make([][]int, 4)
	currentTurn := dealer
	totalTurns := 0

	// 模拟前 20 巡
	for step := 0; step < 20 && len(wall) > 0; step++ {
		totalTurns++
		player := currentTurn

		// 检查自摸胡
		if rule.CanSelfDrawWin(hands[player]) {
			t.Logf("=== 第 %d 巡: 玩家 %d 自摸胡! ===", totalTurns, player)
			return
		}

		// 获取当前手牌状态
		counts := rule.CountsFromHand(hands[player])
		shanten := rule.ShantenNumber(counts)
		isTenpai := rule.IsTenpai(counts)

		// 机器人决策
		meldInfos := make([]MeldInfo, 0)
		tile := eng.DecideDiscard(context.Background(), "sim", hands[player], meldInfos)

		t.Logf("第 %d 巡: 玩家 %d 手牌=%v 向听=%d 听牌=%v 打出=%s(%d)",
			totalTurns, player, hands[player], shanten, isTenpai,
			rule.DisplayName(tile), tile)

		// 执行出牌
		var removed bool
		hands[player], removed = removeTileFromHandSimple(hands[player], tile)
		if !removed {
			t.Fatalf("玩家 %d 试图打出不存在的牌", player)
		}
		discardPools[player] = append(discardPools[player], tile)

		// 检查点炮胡
		for d := 1; d < 4; d++ {
			other := (player + d) % 4
			if rule.CanWinFromDiscard(hands[other], tile) {
				t.Logf("=== 第 %d 巡: 玩家 %d 点炮胡玩家 %d 的 %s ===",
					totalTurns, other, player, rule.DisplayName(tile))
				return
			}
		}

		// 下家摸牌
		next := (player + 1) % 4
		if len(wall) == 0 {
			break
		}
		drawn, rest := rule.DrawFromWall(wall, 1)
		wall = rest
		hands[next] = append(hands[next], drawn[0])

		t.Logf("  → 玩家 %d 摸到 %s(%d)", next, rule.DisplayName(drawn[0]), drawn[0])

		currentTurn = next
	}

	t.Logf("=== 模拟结束（%d 巡）===", totalTurns)
	for i, hand := range hands {
		counts := rule.CountsFromHand(hand)
		shanten := rule.ShantenNumber(counts)
		isTenpai := rule.IsTenpai(counts)
		t.Logf("玩家 %d: 剩余手牌=%d张 向听数=%d 听牌=%v", i, len(hand), shanten, isTenpai)
	}
}

// TestDebug_TenpaiCheck 测试听牌判定逻辑
func TestDebug_TenpaiCheck(t *testing.T) {
	// 测试用例 1：明显听牌型
	hand1 := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} // 1-9万各1张 + 额外3张
	counts1 := rule.CountsFromHand(hand1)
	t.Logf("测试1: 手牌=%v", hand1)
	t.Logf("  计数=%v", counts1[:9])
	t.Logf("  向听数=%d", rule.ShantenNumber(counts1))
	t.Logf("  听牌=%v", rule.IsTenpai(counts1))

	// 测试用例 2：标准听牌型（123万 456筒 789条 东东 听3/6万）
	hand2 := []int{0, 1, 2, 9, 10, 11, 18, 19, 20, 27, 27} // 123万 456筒 789条 东东
	// 需要14张才能胡，这里是13张听牌
	counts2 := rule.CountsFromHand(hand2)
	t.Logf("\n测试2: 手牌=%v", hand2)
	t.Logf("  计数=%v", counts2)
	t.Logf("  向听数=%d", rule.ShantenNumber(counts2))
	t.Logf("  听牌=%v", rule.IsTenpai(counts2))

	// 测试用例 3：加上听的那张牌，应该能胡
	hand3 := append([]int{}, hand2...)
	hand3 = append(hand3, 0) // 加1万
	counts3 := rule.CountsFromHand(hand3)
	t.Logf("\n测试3（加1万后）: 手牌=%v", hand3)
	t.Logf("  胡牌=%v", rule.IsWinningHand(counts3))
}

// TestDebug_DifficultySeedSweep 临时诊断：跨引擎种子扫描难度 EV，评估校准指标的方差
func TestDebug_DifficultySeedSweep(t *testing.T) {
	const gamesPerSeed = 200
	for _, target := range []string{"normal", "hard"} {
		for _, seed := range []uint64{11, 22, 33, 44, 55} {
			eng := NewEngineWithRng(nil, rand.New(rand.NewPCG(seed, 42)))
			var targetEV, easyEV float64
			decided := 0
			for g := 0; g < gamesPerSeed; g++ {
				var diffs [4]DifficultyConfig
				targetSeat := g % 4
				for i := range diffs {
					if i == targetSeat {
						diffs[i] = DifficultyFor(target)
					} else {
						diffs[i] = DifficultyFor("easy")
					}
				}
				rng := rand.New(rand.NewPCG(2026, uint64(g*31+7)))
				out := simulateGameWithDifficulty(t, eng, rng, diffs)
				if out.winner < 0 {
					continue
				}
				decided++
				var ev [4]float64
				if out.winBySelf {
					ev[out.winner] += 1.5
					for i := 0; i < 4; i++ {
						if i != out.winner {
							ev[i] -= 0.5
						}
					}
				} else {
					ev[out.winner]++
					ev[out.dealIn]--
				}
				targetEV += ev[targetSeat]
				for i := 0; i < 4; i++ {
					if i != targetSeat {
						easyEV += ev[i] / 3
					}
				}
			}
			t.Logf("%s seed=%d: 目标 EV %.3f vs easy 席均 %.3f（差 %.3f，%d 局）",
				target, seed, targetEV/float64(decided), easyEV/float64(decided),
				(targetEV-easyEV)/float64(decided), decided)
		}
	}
}

// TestDebug_DifficultyTableCompare 临时诊断：全同难度桌的成局率/流局率对比，
// 防守强度直接压低桌面成局率，该指标方差远低于 1v3 EV 对比
func TestDebug_DifficultyTableCompare(t *testing.T) {
	const games = 300
	for _, diffName := range []string{"easy", "normal", "hard"} {
		eng := NewEngineWithRng(nil, rand.New(rand.NewPCG(77, 42)))
		decided := 0
		for g := 0; g < games; g++ {
			var diffs [4]DifficultyConfig
			for i := range diffs {
				diffs[i] = DifficultyFor(diffName)
			}
			rng := rand.New(rand.NewPCG(2026, uint64(g*31+7)))
			out := simulateGameWithDifficulty(t, eng, rng, diffs)
			if out.winner >= 0 {
				decided++
			}
		}
		t.Logf("%s 桌: 成局率 %.1f%%（%d/%d）", diffName,
			float64(decided)/float64(games)*100, decided, games)
	}
}

// TestDebug_ShantenProgression 跟踪向听数变化
func TestDebug_ShantenProgression(t *testing.T) {
	eng := NewEngine(nil)
	rng := rand.New(rand.NewPCG(42, 456))

	// 创建一手牌
	wall := rule.NewWall()
	rng.Shuffle(len(wall), func(i, j int) {
		wall[i], wall[j] = wall[j], wall[i]
	})

	hand, _ := rule.DrawFromWall(wall, 14)
	t.Logf("初始手牌: %v", hand)

	for turn := 0; turn < 15; turn++ {
		counts := rule.CountsFromHand(hand)
		shanten := rule.ShantenNumber(counts)
		isTenpai := rule.IsTenpai(counts)

		t.Logf("第 %d 巡: 手牌%d张 向听=%d 听牌=%v",
			turn, len(hand), shanten, isTenpai)

		if len(hand) == 0 {
			break
		}

		// 打出一张
		meldInfos := make([]MeldInfo, 0)
		tile := eng.DecideDiscard(context.Background(), "sim", hand, meldInfos)
		var removed bool
		hand, removed = removeTileFromHandSimple(hand, tile)
		if !removed {
			t.Fatalf("无法打出不存在的牌")
		}

		t.Logf("  打出: %s(%d)", rule.DisplayName(tile), tile)

		// 如果还有牌墙，摸一张
		if len(wall) > 0 {
			drawn, rest := rule.DrawFromWall(wall, 1)
			wall = rest
			hand = append(hand, drawn[0])
			t.Logf("  摸到: %s(%d)", rule.DisplayName(drawn[0]), drawn[0])
		}
	}
}
