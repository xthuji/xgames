package bot

import (
	"context"
	"math"
	"math/rand/v2"
	"testing"

	"xgames/internal/games/mahjong/rule"
)

// simStats 单局统计
type simStats struct {
	winner      int    // 赢家座位（-1=流局）
	winBySelf   bool   // 是否自摸
	totalTurns  int    // 总巡数（牌墙消耗）
	playerHands [4]int // 各玩家最终手牌数
	discards    [4]int // 各玩家弃牌数

	// 新增统计分析字段
	shantenHistory [4][]int   // 各玩家向听数演化历史（每5巡采样）
	tenpaiType     [4]string  // 各玩家最终听牌类型（未听牌="none", 两面="two-sided", 嵌张="middle", 边张="edge", 单骑="single"）
	dangerAnalysis [4]float64 // 各玩家平均出牌危险度
	pongCount      [4]int     // 各玩家碰牌次数
}

// simulateMahjongGameSimple 简化版麻将模拟（仅出牌和胡牌，无碰杠）
func simulateMahjongGameSimple(t *testing.T, eng *Engine, rng *rand.Rand) simStats {
	t.Helper()

	// 初始化牌墙
	wall := rule.NewWall()
	rng.Shuffle(len(wall), func(i, j int) {
		wall[i], wall[j] = wall[j], wall[i]
	})

	// 发牌：每人13张
	hands := make([][]int, 4)
	for i := 0; i < 4; i++ {
		drawn, rest := rule.DrawFromWall(wall, 13)
		hands[i] = drawn
		wall = rest
	}

	// 庄家多摸一张
	dealer := rng.IntN(4)
	drawn, rest := rule.DrawFromWall(wall, 1)
	hands[dealer] = append(hands[dealer], drawn[0])
	wall = rest

	// 游戏状态
	discardPools := make([][]int, 4)
	currentTurn := dealer
	totalTurns := 0

	// 统计分析
	var shantenHistory [4][]int
	var dangerSum [4]float64
	var dangerCount [4]int
	var pongCount [4]int

	// 初始化向听数历史
	for i := 0; i < 4; i++ {
		counts := rule.CountsFromHand(hands[i])
		sh := rule.ShantenNumber(counts)
		shantenHistory[i] = append(shantenHistory[i], sh)
	}

	// 模拟最多 80 巡
	for step := 0; step < 80 && len(wall) > 0; step++ {
		totalTurns++
		player := currentTurn

		// 检查自摸胡
		if rule.CanSelfDrawWin(hands[player]) {
			// 分析听牌类型
			var tenpaiType [4]string
			for i := 0; i < 4; i++ {
				meldCount := 0 // 简化：不计副露
				tenpaiType[i] = analyzeTenpaiType(hands[i], meldCount)
			}

			// 计算平均危险度
			var dangerAnalysis [4]float64
			for i := 0; i < 4; i++ {
				if dangerCount[i] > 0 {
					dangerAnalysis[i] = dangerSum[i] / float64(dangerCount[i])
				}
			}

			return simStats{
				winner:         player,
				winBySelf:      true,
				totalTurns:     totalTurns,
				playerHands:    [4]int{len(hands[0]), len(hands[1]), len(hands[2]), len(hands[3])},
				discards:       [4]int{len(discardPools[0]), len(discardPools[1]), len(discardPools[2]), len(discardPools[3])},
				shantenHistory: shantenHistory,
				tenpaiType:     tenpaiType,
				dangerAnalysis: dangerAnalysis,
				pongCount:      pongCount,
			}
		}

		// 机器人出牌决策（使用快速 ukeire 优化性能）
		meldInfos := make([]MeldInfo, 0)
		tile := eng.DecideDiscardForSim(context.Background(), "sim", hands[player], meldInfos)

		// 收集危险度分析数据（简化：基于手牌构成估算）
		counts := rule.CountsFromHand(hands[player])
		tileDanger := estimateTileDanger(tile, counts, allDiscardsSimple(discardPools))
		dangerSum[player] += tileDanger
		dangerCount[player]++

		// 执行出牌
		var removed bool
		hands[player], removed = removeTileFromHandSimple(hands[player], tile)
		if !removed {
			t.Fatalf("玩家 %d 试图打出不存在的牌 %d", player, tile)
		}
		discardPools[player] = append(discardPools[player], tile)

		// 每5巡记录向听数
		if totalTurns%5 == 0 {
			for i := 0; i < 4; i++ {
				c := rule.CountsFromHand(hands[i])
				sh := rule.ShantenNumber(c)
				shantenHistory[i] = append(shantenHistory[i], sh)
			}
		}

		// 检查其他玩家是否可以点炮胡或碰
		pongPlayer := -1
		for d := 1; d < 4; d++ {
			other := (player + d) % 4

			// 优先检查胡牌（胡牌优先级高于碰）
			if rule.CanWinFromDiscard(hands[other], tile) {
				hands[other] = append(hands[other], tile)

				// 分析听牌类型
				var tenpaiType [4]string
				for i := 0; i < 4; i++ {
					meldCount := 0
					tenpaiType[i] = analyzeTenpaiType(hands[i], meldCount)
				}

				// 计算平均危险度
				var dangerAnalysis [4]float64
				for i := 0; i < 4; i++ {
					if dangerCount[i] > 0 {
						dangerAnalysis[i] = dangerSum[i] / float64(dangerCount[i])
					}
				}

				return simStats{
					winner:         other,
					winBySelf:      false,
					totalTurns:     totalTurns,
					playerHands:    [4]int{len(hands[0]), len(hands[1]), len(hands[2]), len(hands[3])},
					discards:       [4]int{len(discardPools[0]), len(discardPools[1]), len(discardPools[2]), len(discardPools[3])},
					shantenHistory: shantenHistory,
					tenpaiType:     tenpaiType,
					dangerAnalysis: dangerAnalysis,
					pongCount:      pongCount,
				}
			}

			// 检查碰牌（简化策略：有对子就碰，因为碰可以加快听牌速度）
			if pongPlayer == -1 && rule.CanPong(hands[other], tile) {
				pongPlayer = other
			}
		}

		// 如果有人碰，执行碰操作
		if pongPlayer >= 0 {
			pongCount[pongPlayer]++ // 记录碰牌次数

			// 从碰牌者手牌中移除2张相同的牌
			removedCount := 0
			newHand := make([]int, 0, len(hands[pongPlayer])-2)
			for _, t := range hands[pongPlayer] {
				if t == tile && removedCount < 2 {
					removedCount++
					continue
				}
				newHand = append(newHand, t)
			}
			hands[pongPlayer] = newHand

			// 碰牌者直接出牌（碰后必须打出一张）
			meldInfos := make([]MeldInfo, 0)
			discardTile := eng.DecideDiscardForSim(context.Background(), "sim", hands[pongPlayer], meldInfos)

			var removed bool
			hands[pongPlayer], removed = removeTileFromHandSimple(hands[pongPlayer], discardTile)
			if !removed {
				t.Fatalf("碰牌玩家 %d 试图打出不存在的牌 %d", pongPlayer, discardTile)
			}
			discardPools[pongPlayer] = append(discardPools[pongPlayer], discardTile)

			// 碰牌者的下家摸牌
			next := (pongPlayer + 1) % 4
			if len(wall) > 0 {
				drawn, rest := rule.DrawFromWall(wall, 1)
				wall = rest
				hands[next] = append(hands[next], drawn[0])
				currentTurn = next
			}
			continue // 跳过正常的摸牌流程
		}

		// 下家摸牌
		next := (player + 1) % 4
		if len(wall) == 0 {
			break // 流局
		}
		drawn, rest := rule.DrawFromWall(wall, 1)
		wall = rest
		hands[next] = append(hands[next], drawn[0])
		currentTurn = next
	}

	// 流局
	// 分析听牌类型
	var tenpaiType [4]string
	for i := 0; i < 4; i++ {
		meldCount := 0
		tenpaiType[i] = analyzeTenpaiType(hands[i], meldCount)
	}

	// 计算平均危险度
	var dangerAnalysis [4]float64
	for i := 0; i < 4; i++ {
		if dangerCount[i] > 0 {
			dangerAnalysis[i] = dangerSum[i] / float64(dangerCount[i])
		}
	}

	return simStats{
		winner:         -1,
		winBySelf:      false,
		totalTurns:     totalTurns,
		playerHands:    [4]int{len(hands[0]), len(hands[1]), len(hands[2]), len(hands[3])},
		discards:       [4]int{len(discardPools[0]), len(discardPools[1]), len(discardPools[2]), len(discardPools[3])},
		shantenHistory: shantenHistory,
		tenpaiType:     tenpaiType,
		dangerAnalysis: dangerAnalysis,
		pongCount:      pongCount,
	}
}

func allDiscardsSimple(pools [][]int) []int {
	all := make([]int, 0, 64)
	for _, pool := range pools {
		all = append(all, pool...)
	}
	return all
}

func removeTileFromHandSimple(hand []int, tile int) ([]int, bool) {
	for i, t := range hand {
		if t == tile {
			return append(hand[:i], hand[i+1:]...), true
		}
	}
	return hand, false
}

// estimateTileDanger 简化的危险度估算（基于手牌构成和已出牌）
func estimateTileDanger(tile int, counts []int, allDiscards []int) float64 {
	danger := 50.0

	// 现物安全度
	discardCount := 0
	for _, t := range allDiscards {
		if t == tile {
			discardCount++
		}
	}
	if discardCount >= 2 {
		danger -= 30
	} else if discardCount == 1 {
		danger -= 20
	}

	// 字牌相对安全
	if rule.IsHonor(tile) {
		danger -= 10
	}

	// 孤张相对安全
	if !rule.IsHonor(tile) && counts[tile] == 1 {
		suit, val := rule.SuitOf(tile), rule.ValueOf(tile)
		base := suit * rule.NumValues
		hasNeighbor := false
		for dv := -2; dv <= 2; dv++ {
			if dv == 0 {
				continue
			}
			nv := val + dv
			if nv >= 0 && nv < rule.NumValues && counts[base+nv] > 0 {
				hasNeighbor = true
				break
			}
		}
		if !hasNeighbor {
			danger -= 15 // 真孤张更安全
		}
	}

	if danger < 0 {
		danger = 0
	}
	if danger > 100 {
		danger = 100
	}
	return danger
}

// analyzeTenpaiType 分析听牌类型
func analyzeTenpaiType(hand []int, meldCount int) string {
	waits := rule.TenpaiWaits(hand, meldCount)
	if len(waits) == 0 {
		return "none"
	}

	// 单骑听牌（只听一张）
	if len(waits) == 1 {
		return "single"
	}

	// 简化判断：根据待牌数量分类
	// 两面听通常有8张待牌，嵌张4张，边张4张
	totalOuts := 0
	counts := rule.CountsFromHand(hand)
	for _, w := range waits {
		totalOuts += (4 - counts[w])
	}

	if totalOuts >= 7 {
		return "two-sided" // 两面或多面听
	} else if totalOuts >= 4 {
		return "middle" // 嵌张或边张
	}
	return "single" // 少待牌
}

// TestSimulate_MahjongBotSimple 简化版麻将模拟测试
func TestSimulate_MahjongBotSimple(t *testing.T) {
	eng := NewEngine(nil)
	numGames := 20
	results := make([]simStats, 0, numGames)

	for game := 0; game < numGames; game++ {
		rng := rand.New(rand.NewPCG(42, uint64(game*7+13)))
		result := simulateMahjongGameSimple(t, eng, rng)
		results = append(results, result)

		t.Logf("Game %d: winner=%d selfDraw=%v turns=%d",
			game, result.winner, result.winBySelf, result.totalTurns)
	}

	// 统计
	wins := 0
	draws := 0
	selfDraws := 0
	totalTurns := 0

	// 新增统计：听牌类型分布、碰牌次数、危险度
	tenpaiTypeCount := map[string]int{
		"none": 0, "two-sided": 0, "middle": 0, "single": 0,
	}
	totalPongs := 0
	avgDangerSum := 0.0
	avgDangerCount := 0

	for _, r := range results {
		if r.winner >= 0 {
			wins++
			if r.winBySelf {
				selfDraws++
			}
		} else {
			draws++
		}
		totalTurns += r.totalTurns

		// 统计听牌类型（所有玩家）
		for i := 0; i < 4; i++ {
			tenpaiTypeCount[r.tenpaiType[i]]++
		}

		// 统计碰牌次数
		for i := 0; i < 4; i++ {
			totalPongs += r.pongCount[i]
		}

		// 统计平均危险度
		for i := 0; i < 4; i++ {
			if r.dangerAnalysis[i] > 0 {
				avgDangerSum += r.dangerAnalysis[i]
				avgDangerCount++
			}
		}
	}

	t.Logf("=== 简化模拟统计（%d 局）===", numGames)
	t.Logf("胡牌率: %d/%d (%.1f%%)", wins, numGames, float64(wins)/float64(numGames)*100)
	t.Logf("自摸率: %d/%d (%.1f%%)", selfDraws, wins, float64(selfDraws)/float64(wins)*100)
	t.Logf("平均巡数: %.1f", float64(totalTurns)/float64(numGames))

	// 增强统计分析
	t.Logf("\n=== 听牌类型分布 ===")
	for typeName, count := range tenpaiTypeCount {
		if count > 0 && typeName != "none" {
			t.Logf("%-12s: %d (%.1f%%)", typeName, count, float64(count)/float64(numGames*4)*100)
		}
	}

	t.Logf("\n=== 碰牌统计 ===")
	t.Logf("总碰牌次数: %d (平均每局 %.1f 次)", totalPongs, float64(totalPongs)/float64(numGames))

	t.Logf("\n=== 出牌危险度分析 ===")
	if avgDangerCount > 0 {
		t.Logf("平均出牌危险度: %.1f / 100", avgDangerSum/float64(avgDangerCount))
	}

	// 向听数演化示例（展示第一局的数据）
	if len(results) > 0 && len(results[0].shantenHistory[0]) > 0 {
		t.Logf("\n=== 向听数演化示例（第1局，玩家0）===")
		history := results[0].shantenHistory[0]
		for i, sh := range history {
			if i > 0 {
				t.Logf("  巡数 %d: 向听数=%d", i*5, sh)
			}
		}
	}

	// 合理性检查
	if wins == 0 {
		t.Errorf("%d 局无一胡牌，可能决策逻辑有严重问题", numGames)
	}
}

// diffSimOutcome 带难度自博弈单局结果
type diffSimOutcome struct {
	winner    int // 赢家座位，-1=流局
	winBySelf bool
	dealIn    int // 点炮者座位（荣胡时有效；自摸/流局为 -1）
	fan       int // 胡牌番数（流局为 0），与 rule.CalcFan 口径一致
}

// toRuleMelds 副露转 rule.Meld（番数计算用）
func toRuleMelds(ms []MeldInfo) []rule.Meld {
	out := make([]rule.Meld, len(ms))
	for i, m := range ms {
		out[i] = rule.Meld{Type: rule.MeldType(m.Type), Tile: m.Tile, From: -1}
	}
	return out
}

// seededWall 构建确定性牌墙：rule.NewWall 默认走全局随机源洗牌，
// 校准测试必须全程可复现，故本地按序生成 136 张后用注入的 rng 洗牌。
func seededWall(rng *rand.Rand) []int {
	wall := make([]int, 0, rule.TotalTiles)
	for t := 0; t < rule.NumTypes; t++ {
		for c := 0; c < rule.Copies; c++ {
			wall = append(wall, t)
		}
	}
	rng.Shuffle(len(wall), func(i, j int) { wall[i], wall[j] = wall[j], wall[i] })
	return wall
}

// simulateGameWithDifficulty 带难度配置的四人自博弈模拟：
// 出牌走 DecideDiscardWithErrors（难度失误/防守权重/向听精度），
// 碰与胡走 DecideAction（难度碰牌阈值），对手模型按 ReadOpponent 开关构建，
// 忠实镜像 controller.go 的生产决策路径。
func simulateGameWithDifficulty(t *testing.T, eng *Engine, rng *rand.Rand, diffs [4]DifficultyConfig) diffSimOutcome {
	t.Helper()

	wall := seededWall(rng)

	// 发牌：每人13张，庄家多摸一张
	hands := make([][]int, 4)
	for i := 0; i < 4; i++ {
		drawn, rest := rule.DrawFromWall(wall, 13)
		hands[i] = drawn
		wall = rest
	}
	dealer := rng.IntN(4)
	drawn, rest := rule.DrawFromWall(wall, 1)
	hands[dealer] = append(hands[dealer], drawn[0])
	wall = rest

	discardPools := make([][]int, 4)
	melds := make([][]MeldInfo, 4)
	turnCounts := make([]int, 4) // 各玩家自己的巡数（防守权重随巡数增长）
	currentTurn := dealer
	playerIDs := [4]string{"p0", "p1", "p2", "p3"}

	// buildOpponents 对手听牌推断采用生产口径（controller.buildOpponentModels）：
	// 舍牌数与巡数同时达到难度阈值即视为疑似听牌（困难档阈值更低、更早警惕）
	buildOpponents := func(self int) []OpponentModel {
		inferDiscards, inferTurn := diffs[self].TenpaiInferGates()
		models := make([]OpponentModel, 0, 3)
		for i := 0; i < 4; i++ {
			if i == self {
				continue
			}
			models = append(models, OpponentModel{
				PlayerID:        playerIDs[i],
				IsTenpai:        len(discardPools[i]) > inferDiscards && turnCounts[i] > inferTurn,
				DiscardSequence: append([]int(nil), discardPools[i]...),
			})
		}
		return models
	}

	// discardFor 按玩家难度出牌（生产路径：DecideDiscardWithErrors）
	discardFor := func(p int) int {
		turnCounts[p]++
		var opponents []OpponentModel
		if diffs[p].ReadOpponent {
			opponents = buildOpponents(p)
		}
		tile := eng.DecideDiscardWithErrors(DiscardContext{
			Hand:           hands[p],
			Melds:          melds[p],
			DiscardedTiles: allDiscardsSimple(discardPools),
			MyDiscards:     discardPools[p],
			TurnNumber:     turnCounts[p],
			OpponentModels: opponents,
			Difficulty:     diffs[p],
		})
		var removed bool
		hands[p], removed = removeTileFromHandSimple(hands[p], tile)
		if !removed {
			t.Fatalf("玩家 %d 试图打出不存在的牌 %d", p, tile)
		}
		discardPools[p] = append(discardPools[p], tile)
		return tile
	}

	// tryRon 检查打牌后是否有他家荣胡（按座位顺序，头跳优先）
	tryRon := func(discarder, tile int) int {
		for d := 1; d < 4; d++ {
			other := (discarder + d) % 4
			if rule.CanWinFromDiscardWithMelds(hands[other], tile, len(melds[other])) {
				return other
			}
		}
		return -1
	}

	// drawNext 给指定玩家摸一张牌并推进回合，返回是否成功（牌墙耗尽=false）
	drawNext := func(next int) bool {
		if len(wall) == 0 {
			return false
		}
		drawn, rest := rule.DrawFromWall(wall, 1)
		wall = rest
		hands[next] = append(hands[next], drawn[0])
		currentTurn = next
		return true
	}

	for step := 0; step < 80 && len(wall) > 0; step++ {
		player := currentTurn

		// 自摸胡（手牌已含摸牌，14 张口径）
		if rule.CanSelfDrawWinWithMelds(hands[player], len(melds[player])) {
			return diffSimOutcome{
				winner: player, winBySelf: true, dealIn: -1,
				fan: rule.CalcFan(hands[player], toRuleMelds(melds[player]), true),
			}
		}

		tile := discardFor(player)
		if w := tryRon(player, tile); w >= 0 {
			ronHand := append(append([]int(nil), hands[w]...), tile)
			return diffSimOutcome{
				winner: w, dealIn: player,
				fan: rule.CalcFan(ronHand, toRuleMelds(melds[w]), false),
			}
		}

		// 碰牌决策（生产路径：DecideAction，难度影响安全牌余量阈值），按座位顺序取第一名家
		pongPlayer := -1
		for d := 1; d < 4 && pongPlayer < 0; d++ {
			other := (player + d) % 4
			if !rule.CanPong(hands[other], tile) {
				continue
			}
			pong, _ := eng.DecideAction(context.Background(), playerIDs[other], hands[other], tile, len(melds[other]), diffs[other])
			if pong {
				pongPlayer = other
			}
		}

		if pongPlayer >= 0 {
			// 移除 2 张同牌并记副露
			removedCount := 0
			newHand := make([]int, 0, len(hands[pongPlayer])-2)
			for _, ht := range hands[pongPlayer] {
				if ht == tile && removedCount < 2 {
					removedCount++
					continue
				}
				newHand = append(newHand, ht)
			}
			hands[pongPlayer] = newHand
			melds[pongPlayer] = append(melds[pongPlayer], MeldInfo{Tile: tile, Type: int(rule.MeldPong)})

			// 碰后出牌，他家可荣胡
			pongTile := discardFor(pongPlayer)
			if w := tryRon(pongPlayer, pongTile); w >= 0 {
				ronHand := append(append([]int(nil), hands[w]...), pongTile)
				return diffSimOutcome{
					winner: w, dealIn: pongPlayer,
					fan: rule.CalcFan(ronHand, toRuleMelds(melds[w]), false),
				}
			}

			// 碰牌者下家摸牌
			if !drawNext((pongPlayer + 1) % 4) {
				break
			}
			continue
		}

		// 下家摸牌
		if !drawNext((player + 1) % 4) {
			break
		}
	}

	return diffSimOutcome{winner: -1, dealIn: -1} // 流局
}

// TestDifficulty_GradientSimulation 难度梯度自博弈校准（P0 收尾验证）：
// 每局 1 名目标难度玩家对阵 3 名 easy 玩家，座位逐局轮转以消除庄家/座位差；
// 8 个引擎种子取均值，抑制失误随机流波动（赢家通吃的混沌动力学使单流噪声显著，
// normal 与 hard 的防守差差距约 1~3pp，需足够种子保证单调性断言稳定）。
//
// 主校准指标为点炮率（防守）：难度越高越少点炮喂牌，这是对玩家体验最直接的难度体现，
// 且每局恰有一个点炮者（荣胡局），样本频率高、统计效力强。
// 番数加权 EV（与 session.executeWin 的零和计分一致：自摸 +3×fan/其余 -fan；荣胡 +fan/-fan）
// 与胡牌率受混沌噪声影响，仅作日志参考；进攻侧差异（失误率/向听精度）已由
// difficulty_test.go 的机制级用例覆盖。
func TestDifficulty_GradientSimulation(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过自博弈校准")
	}

	const gamesPerSeed = 400
	seeds := [8]uint64{11, 22, 33, 44, 55, 66, 77, 88}
	gaps := make(map[string]float64, 2) // 目标难度相对 easy 席均的点炮率差距（防守梯度）

	for _, tc := range []struct {
		name   string
		target string
	}{
		{"normal_vs_easy", "normal"},
		{"hard_vs_easy", "hard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wins, deals, decided, selfDraws int
			var targetEV, easySeatsEV float64
			for _, seed := range seeds {
				// 引擎注入固定种子随机源；配合每局固定种子 rng，误差随机流按种子取均值
				eng := NewEngineWithRng(nil, rand.New(rand.NewPCG(seed, 42)))
				for g := 0; g < gamesPerSeed; g++ {
					var diffs [4]DifficultyConfig
					targetSeat := g % 4
					for i := range diffs {
						if i == targetSeat {
							diffs[i] = DifficultyFor(tc.target)
						} else {
							diffs[i] = DifficultyFor("easy")
						}
					}
					rng := rand.New(rand.NewPCG(2026, uint64(g*31+7)))
					out := simulateGameWithDifficulty(t, eng, rng, diffs)
					if out.winner < 0 {
						continue // 流局：无 EV 结算与点炮归属
					}
					decided++

					if out.winBySelf {
						selfDraws++
					}
					if out.winner == targetSeat {
						wins++
					}
					if out.dealIn == targetSeat {
						deals++
					}

					// 零和 EV 结算（与 session.executeWin 的番数计分一致）：
					// 自摸 = 赢家 +3×fan、其余各家各 -fan；荣胡 = 赢家 +fan、点炮者 -fan
					for i := 0; i < 4; i++ {
						ev := 0.0
						switch {
						case out.winBySelf:
							ev = float64(-out.fan)
							if i == out.winner {
								ev = float64(3 * out.fan)
							}
						case i == out.winner:
							ev = float64(out.fan)
						case i == out.dealIn:
							ev = float64(-out.fan)
						}
						if i == targetSeat {
							targetEV += ev
						} else {
							easySeatsEV += ev
						}
					}
				}
			}

			// 零和自检：目标座位与其余三家的 EV 之和应为 0
			if math.Abs(targetEV+easySeatsEV) > 1e-9 {
				t.Fatalf("EV 结算破坏零和：target %.3f + easy %.3f != 0", targetEV, easySeatsEV)
			}

			n := float64(decided)
			ronCount := float64(decided - selfDraws)
			winRate := float64(wins) / n
			dealRate := float64(deals) / n
			// 每个荣胡局恰有一个点炮者：目标点炮数之外的都归 easy 三家
			easyDealRate := (ronCount - float64(deals)) / ronCount / 3
			gaps[tc.target] = easyDealRate - dealRate

			t.Logf("%s: 目标 胡牌率 %.1f%% 点炮率 %.1f%% vs easy 席均点炮率 %.1f%%（防守差 +%.1fpp）"+
				"｜ 番数加权 EV/局 %.2f vs easy 席均 %.2f（%d 局，流局 %d）",
				tc.name, winRate*100, dealRate*100, easyDealRate*100, gaps[tc.target]*100,
				targetEV/n, easySeatsEV/(3*n), decided, gamesPerSeed*len(seeds)-decided)

			// 校准断言（防守梯度）：目标难度点炮率应低于 easy 席位均值
			if dealRate >= easyDealRate {
				t.Errorf("%s 点炮率 %.1f%% 未低于 easy 席均 %.1f%%，防守难度梯度未体现",
					tc.name, dealRate*100, easyDealRate*100)
			}
		})
	}

	// 梯度单调：hard 的防守优势应大于 normal
	if gaps["hard"] <= gaps["normal"] {
		t.Errorf("hard 防守差 %.1fpp 未大于 normal %.1fpp，难度梯度不单调",
			gaps["hard"]*100, gaps["normal"]*100)
	}
}

// BenchmarkSimulateSimple 基准测试
func BenchmarkSimulateSimple(b *testing.B) {
	eng := NewEngine(nil)
	rng := rand.New(rand.NewPCG(42, 123))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		simulateMahjongGameSimple(&testing.T{}, eng, rng)
	}
}
