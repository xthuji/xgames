package bot

import (
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
)

// arena_test.go A/B 竞技场：同一副牌种子成对对战，量化引擎特性开关（EngineConfig）
// 的净收益，为每个优化 Phase 提供回归判据（计划 §Phase 0）。
//
// 对战设计（全部座位 hard 难度：全量记牌 + 无故意犯错，剥离档位差异只比决策质量）：
//   - 地主侧场景：增强引擎坐地主位，基线引擎坐两农民位 → 比地主胜率差
//   - 农民侧场景：增强引擎坐一个农民位，另两席基线 → 比农民阵营胜率差
//   - 对照组：同种子全基线，直接按座位角色配对比较（同牌同局，方差主要来自引擎内随机）
//
// 净胜率差 +5pp 以上视为该 Phase 收益达标（计划验收口径）。

// fullArenaConfig 竞技场"增强方"配置：当前已落地的全部特性开关
func fullArenaConfig() EngineConfig {
	return EngineConfig{
		EnablePlanner:      true,
		EnableInference:    true,
		EnableExactEndgame: true,
		EnableBidModel:     true,
	}
}

// arenaConfig 按 ARENA_CFG 环境变量选择增强配置（逐 Phase/逐开关归因用）：
// planner=仅 Phase 1；inference=Phase 1+2 累计；inference-only=仅 Phase 2；
// endgame=仅 Phase 4；bid=仅 Phase 5；默认全开。
func arenaConfig() EngineConfig {
	switch os.Getenv("ARENA_CFG") {
	case "planner":
		return EngineConfig{EnablePlanner: true}
	case "inference":
		return EngineConfig{EnablePlanner: true, EnableInference: true}
	case "inference-only":
		return EngineConfig{EnableInference: true}
	case "endgame":
		return EngineConfig{EnableExactEndgame: true}
	case "bid":
		return EngineConfig{EnableBidModel: true}
	default:
		return fullArenaConfig()
	}
}

// arenaGames 对局数（默认每场景 200 副，环境变量 ARENA_GAMES 可放大）
func arenaGames() int {
	if v := os.Getenv("ARENA_GAMES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 200
}

// runArena 跑一个场景：增强引擎坐地主位（enhancedLandlord=true）或轮换坐一个农民位，
// 其余席位基线；返回（增强方胜场, 对照组同种子基线胜场, 对局数）。
func runArena(t *testing.T, enhancedLandlord bool, games int) (enhWin, baseWin, n int) {
	t.Helper()
	full := [3]string{"hard", "hard", "hard"}
	base := newTestEngine()
	enh := newTestEngine().WithConfig(arenaConfig())

	for deal := 0; deal < games; deal++ {
		landlord := deal % 3
		// 增强席位：地主侧场景固定地主；农民侧场景在两农民位间轮换
		enhancedAt := landlord
		if !enhancedLandlord {
			n := 0
			farmerSeats := [2]int{}
			for s := 0; s < 3; s++ {
				if s != landlord {
					farmerSeats[n] = s
					n++
				}
			}
			enhancedAt = farmerSeats[deal%2]
		}

		var engines [3]*Engine
		for s := range engines {
			engines[s] = base
		}
		engines[enhancedAt] = enh
		rng := rand.New(rand.NewPCG(7, uint64(deal*13+5)))
		winner, counts, hintTurns := simulateGame(t, engines, full, landlord, rng)
		if winner >= 0 && (enhancedLandlord == (winner == landlord)) {
			enhWin++ // 地主侧：增强方=地主；农民侧：增强方=农民阵营（任一非地主走完）
		}
		checkSilence(t, counts, hintTurns)

		// 对照组：同种子同地主、全基线（增强席位由基线引擎代打）
		var ctrl [3]*Engine
		for s := range ctrl {
			ctrl[s] = base
		}
		rng2 := rand.New(rand.NewPCG(7, uint64(deal*13+5)))
		cw, counts2, ht2 := simulateGame(t, ctrl, full, landlord, rng2)
		if cw >= 0 && (enhancedLandlord == (cw == landlord)) {
			baseWin++
		}
		checkSilence(t, counts2, ht2)
	}
	return enhWin, baseWin, games
}

// checkSilence 零出牌不变量（轻量版）：存在可压回合却整局沉默即 Fail
func checkSilence(t *testing.T, counts, hintTurns [3]int) {
	t.Helper()
	for seat, n := range counts {
		if n == 0 && hintTurns[seat] > 0 {
			t.Errorf("座位 %d 存在 %d 个可压回合却整局沉默", seat, hintTurns[seat])
		}
	}
}

// zRate 配对比例差异的粗显著性（正态近似，|z|≥1.96 视为显著）
func zRate(enhWin, baseWin, games int) float64 {
	if games <= 0 {
		return 0
	}
	p := (float64(enhWin) + float64(baseWin)) / float64(2*games)
	if p <= 0 || p >= 1 {
		return 0
	}
	se := math.Sqrt(2 * p * (1 - p) / float64(games))
	if se == 0 {
		return 0
	}
	return float64(enhWin-baseWin) / float64(games) / se
}

// TestArena_ConfigVsBaseline 竞技场主测试：全开关增强 vs 基线，分地主/农民两场景。
// 需 XGAMES_SELFPLAY=1；仅在修改机器人出牌/决策逻辑后运行（常规测试跳过）。
func TestArena_ConfigVsBaseline(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("竞技场需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时竞技场对战")
	}
	games := arenaGames()

	for _, sc := range []struct {
		name     string
		landlord bool
	}{{"地主侧", true}, {"农民侧", false}} {
		enhWin, baseWin, n := runArena(t, sc.landlord, games)
		delta := float64(enhWin-baseWin) / float64(n) * 100
		t.Logf("场景 %-4s（%d 副）：增强胜 %d 局 vs 基线胜 %d 局 → 净差 %+.1fpp（z=%.2f）",
			sc.name, n, enhWin, baseWin, delta, zRate(enhWin, baseWin, n))
		if delta < -5 {
			t.Errorf("场景 %s：增强引擎净胜率劣化 %.1fpp（> 5pp），对应开关需回退或修正", sc.name, -delta)
		}
	}
}

// TestArenaConfigSanity 特性开关不破坏基本合法性：同场景下零配置引擎与
// 全开关引擎领出均返回合法牌型（快速、离线，常规 go test 即跑）。
func TestArenaConfigSanity(t *testing.T) {
	hand := cards("3 4 5 6 7 8 9 10 J Q K A 2 2")
	for _, cfg := range []EngineConfig{{}, fullArenaConfig()} {
		e := newTestEngine().WithConfig(cfg)
		cardsOut := e.DecidePlay(nil, "x", GameContext{
			IsLandlord: true, Hand: hand, MustPlay: true, CanBeat: true,
			BottomCards: cards("3 4 5"), Personality: DefaultPersonalities["hard"],
		})
		if len(cardsOut) == 0 {
			t.Fatalf("领出返回空（cfg=%+v）", cfg)
		}
	}
}
