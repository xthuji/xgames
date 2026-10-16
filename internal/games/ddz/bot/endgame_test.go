package bot

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// endgame_test.go Phase 4 单测：发牌分布枚举（完备性/权重/可复现）、
// 完全信息最优博弈求解、精确残局决策接入与超时预算。
// 全部构造确定性场景（显式记牌视图 + 固定手牌），与手算值对照。

// seatCounts 用牌面串构造三席计数（seat0=自己，1=下家，2=上家）
func seatCounts(self, down, up string) [3][rankCount]int8 {
	var c [3][rankCount]int8
	for seat, notation := range [3]string{self, down, up} {
		for i, n := range handToCounts(cards(notation)) {
			c[seat][i] = int8(n)
		}
	}
	return c
}

func TestEnumerateDeals_CompleteSmallPool(t *testing.T) {
	t.Parallel()
	// 未知池：3 两张 + 5 一张，上家 2 张下家 1 张 → 全部合法分配
	var unknown [rankCount]int
	unknown[rankIdx(card.Rank3)] = 2
	unknown[rankIdx(card.Rank5)] = 1

	deals, exhaustive := enumerateDeals(unknown, 2, 1, exactDealsCap)
	if !exhaustive {
		t.Error("3 张池应完整枚举")
	}
	// 下家那一张只能是 3 或 5 → 2 个不同构成
	if len(deals) != 2 {
		t.Fatalf("分布数 = %d, 期望 2：%v", len(deals), deals)
	}
	// 权重和 = 从 3 张物理牌里给下家选 1 张的组合数 C(3,1)
	var sumW float64
	for _, d := range deals {
		sum := 0
		for _, n := range d.up {
			sum += int(n)
		}
		for _, n := range d.down {
			sum += int(n)
		}
		if sum != 3 {
			t.Errorf("分布 %v 张数和 = %d, 期望 3", d, sum)
		}
		if dealTotal(d.up) != 2 || dealTotal(d.down) != 1 {
			t.Errorf("分布 %v 座位张数不符：up=%d down=%d", d, dealTotal(d.up), dealTotal(d.down))
		}
		sumW += d.weight
	}
	if sumW != combSmall(3, 1) {
		t.Errorf("权重和 = %v, 期望 %v", sumW, combSmall(3, 1))
	}
}

// dealTotal 计数数组张数和
func dealTotal(c [rankCount]int) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

func TestEnumerateDeals_NotApplicable(t *testing.T) {
	t.Parallel()
	// 视图含未跟踪点数（-1）：easy/normal 不发精确分布
	var unknown [rankCount]int
	unknown[rankIdx(card.Rank3)] = -1
	unknown[rankIdx(card.Rank5)] = 2
	if _, ok := enumerateDeals(unknown, 1, 1, exactDealsCap); ok {
		t.Error("含未跟踪点数时不应适用精确分布")
	}

	// 池张数与座位余牌不符（视图缺漏）：不可信，直接弃用
	var u2 [rankCount]int
	u2[rankIdx(card.Rank3)] = 3
	if deals, _ := enumerateDeals(u2, 2, 2, exactDealsCap); deals != nil {
		t.Errorf("池 3 张 vs 座位 2+2 时不应枚举，实际得 %v", deals)
	}
	if _, ok := enumerateDeals(u2, 2, 2, exactDealsCap); ok {
		t.Error("张数不符应返回不可用")
	}
}

func TestEnumerateDeals_SampledOverCapIsDeterministic(t *testing.T) {
	t.Parallel()
	var unknown [rankCount]int
	for i := rankIdx(card.Rank3); i <= rankIdx(card.RankJ); i++ {
		unknown[i] = 2
	} // 18 张池，远超抽样上限
	up, down := 10, 8
	const cap = 40

	first, exhaustive := enumerateDeals(unknown, up, down, cap)
	if exhaustive {
		t.Error("方案数超上限时不应完整枚举")
	}
	if len(first) != cap {
		t.Fatalf("抽样分布数 = %d, 期望 %d", len(first), cap)
	}
	second, _ := enumerateDeals(unknown, up, down, cap)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Error("同参数两次抽样结果不一致（须可复现）")
	}
	for _, d := range first {
		if dealTotal(d.up) != up || dealTotal(d.down) != down {
			t.Fatalf("抽样分布张数不符：up=%d down=%d", dealTotal(d.up), dealTotal(d.down))
		}
		for i := range d.up {
			if d.up[i]+d.down[i] > unknown[i] {
				t.Fatalf("分布 %v 超出未知池 %v 的点数 %d", d, unknown, i)
			}
		}
	}
}

func TestEndgameSolverSolve(t *testing.T) {
	// 我方（seat0）单张 3 领出即走完（落子后终局判定先于对手应牌）
	me := newEndgameSolver([3]bool{true, false, false}, time.Now().Add(endgameSearchBudget))
	if v := me.solve(seatCounts("3", "5 5", "7 7"), rule.ParsedHand{}, 0, 0); v != exactWin {
		t.Errorf("单张领出走完 = %d, 期望必胜(1)", v)
	}

	// 地主（下家）领出单 2 手里还剩单 6：全桌压不动 2 → 其再领 6 走完 → 我方必败
	lose := newEndgameSolver([3]bool{true, false, false}, time.Now().Add(endgameSearchBudget))
	if v := lose.solve(seatCounts("3", "2 6", "7 7"), parseCards(cards("2")), 1, 2); v != exactLose {
		t.Errorf("敌方单 2 夺权后再走 = %d, 期望必败(0)", v)
	}

	// 我方 3、4 两散牌 vs 敌方 5、8、9、9：无论领哪张都进不了收 → 必败
	// （验证深度搜索而非只看一手）
	deep := newEndgameSolver([3]bool{true, false, false}, time.Now().Add(endgameSearchBudget))
	if v := deep.solve(seatCounts("3 4", "5 8", "9 9"), rule.ParsedHand{}, 0, 0); v != exactLose {
		t.Errorf("我方两散牌 vs 敌方控制牌 = %d, 期望必败(0)", v)
	}

	// 我方 4、5 vs 两家全单 3：领 4 被压不动 → 收回牌权再领 5 走完 → 必胜
	win := newEndgameSolver([3]bool{true, false, false}, time.Now().Add(endgameSearchBudget))
	if v := win.solve(seatCounts("4 5", "3 3", "3 3"), rule.ParsedHand{}, 0, 0); v != exactWin {
		t.Errorf("我方 4 5 vs 敌方散 3 = %d, 期望必胜(1)", v)
	}

	// 队友（同阵营）走完也算我方胜：其刚打出最后一张 5
	partner := newEndgameSolver([3]bool{true, false, true}, time.Now().Add(endgameSearchBudget))
	if v := partner.solve(seatCounts("3 4", "6", ""), parseCards(cards("5")), 2, 0); v != exactWin {
		t.Errorf("队友走完 = %d, 期望按阵营判胜(1)", v)
	}

	// 节点上限：预算耗尽返回未收敛（不当作必胜/必败使用）
	tired := newEndgameSolver([3]bool{true, false, true}, time.Now().Add(endgameSearchBudget))
	tired.nodes = tired.limit
	if v := tired.solve(seatCounts("3 4 5 6 7", "3 4 5", "6 7 8"), rule.ParsedHand{}, 0, 0); v != exactOpen {
		t.Errorf("超出节点上限 = %d, 期望未收敛(-1)", v)
	}
}

func TestExactEndgameUsable(t *testing.T) {
	hand := cards("3 4 5")
	base := GameContext{Hand: hand, MustPlay: true, Cfg: EngineConfig{EnableExactEndgame: true}}

	g := base
	g.RemainingCards = viewOf(hand, map[card.Rank]int{card.Rank6: 1})
	if !exactEndgameUsable(g) {
		t.Error("开关开启 + 视图完整 + 未知 1 张应可用")
	}

	g = base
	g.RemainingCards = keyRemaining() // 视图仅记 A/2/王：含未跟踪点数
	if exactEndgameUsable(g) {
		t.Error("记牌视图不完整（easy/normal）时不可用")
	}

	g = base
	g.RemainingCards = fullRemaining() // 未知牌 49 张 > 上限
	if exactEndgameUsable(g) {
		t.Error("未知牌超预算时不可用")
	}

	g = base
	g.Cfg.EnableExactEndgame = false
	g.RemainingCards = viewOf(hand, map[card.Rank]int{card.Rank6: 1})
	if exactEndgameUsable(g) {
		t.Error("门控关闭时不可用（基线可复现）")
	}
}

// TestExactEndgamePlay_PicksForcedWin 地主剩 2/3 两张，两家各持一张小牌：
// 领 2 全桌压不动 → 收回牌权再走 3 必胜；领 3 会被压 → 必败。
// 精确求解必须选出 2（旧 rollout 只按点数最小出牌会选 3）。
func TestExactEndgamePlay_PicksForcedWin(t *testing.T) {
	hand := cards("2 3")
	gctx := GameContext{
		IsLandlord:     true,
		Hand:           hand,
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{1, 1}, // 上家 1 张、下家 1 张
		RemainingCards: viewOf(hand, map[card.Rank]int{card.Rank4: 1, card.Rank5: 1}),
		Cfg:            EngineConfig{EnableExactEndgame: true},
	}
	cardsOut, wr, ok := exactEndgamePlay(gctx, time.Now().Add(endgameSearchBudget))
	if !ok {
		t.Fatal("精确残局未接管")
	}
	if wr != 1 {
		t.Errorf("必胜局面胜率 = %v, 期望 1", wr)
	}
	if len(cardsOut) != 1 || cardsOut[0].Rank != card.Rank2 {
		t.Errorf("应领出单 2, 实际 %s", cardsToStr(cardsOut))
	}
}

// TestExactEndgamePlay_FollowTeamwork 农民跟牌：地主领出单 6、手里只剩单 10，
// 队友只剩单 K。两种发牌分布各权重 1：
//   - 地主持 10 / 队友持 K：出 J 地主压不动 → 队友 K 接风走完 → 胜；出 9 被 10 压掉 → 负
//   - 地主持 K / 队友持 10：出 J 或 9 都会被地主压掉走完 → 负
//
// 故出 J 加权胜率 = 1/2、出 9 = 0：验证搜索按阵营（含队友走完）而非“自己走完”评分。
func TestExactEndgamePlay_FollowTeamwork(t *testing.T) {
	hand := cards("9 J")
	gctx := GameContext{
		Hand:           hand,
		RecentPlays:    [2]PlayRecord{play("6", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{1, 1}, // 上家（队友）/下家（地主）各 1 张
		DownIsLandlord: true,
		RemainingCards: viewOf(hand, map[card.Rank]int{card.Rank10: 1, card.RankK: 1}),
		Cfg:            EngineConfig{EnableExactEndgame: true},
	}
	cardsOut, wr, ok := exactEndgamePlay(gctx, time.Now().Add(endgameSearchBudget))
	if !ok {
		t.Fatal("精确残局未接管跟牌决策")
	}
	if wr != 0.5 {
		t.Errorf("加权胜率 = %v, 期望 0.5（两种分布各一半）", wr)
	}
	if len(cardsOut) != 1 || cardsOut[0].Rank != card.RankJ {
		t.Errorf("应出 J 让队友 K 接风，实际 %s", cardsToStr(cardsOut))
	}
}

// TestExactEndgamePlay_TimeoutGivesUp 预算耗尽时不接管（回落 UCT/规则引擎），
// 但一手清空（P0 必胜）仍优先于超时检查短路返回。
func TestExactEndgamePlay_TimeoutGivesUp(t *testing.T) {
	past := time.Now().Add(-time.Millisecond)

	hand := cards("2 3")
	gctx := GameContext{
		IsLandlord:     true,
		Hand:           hand,
		MustPlay:       true,
		PlayerCounts:   [2]int{1, 1},
		RemainingCards: viewOf(hand, map[card.Rank]int{card.Rank4: 1, card.Rank5: 1}),
		Cfg:            EngineConfig{EnableExactEndgame: true},
	}
	if _, _, ok := exactEndgamePlay(gctx, past); ok {
		t.Error("预算耗尽时应放弃精确求解（回落 UCT/规则）")
	}

	winHand := cards("3")
	winCtx := GameContext{
		IsLandlord:     true,
		Hand:           winHand,
		MustPlay:       true,
		PlayerCounts:   [2]int{0, 0},
		RemainingCards: viewOf(winHand, nil),
		Cfg:            EngineConfig{EnableExactEndgame: true},
	}
	got, wr, ok := exactEndgamePlay(winCtx, past)
	if !ok || wr != 1 || len(got) != 1 {
		t.Errorf("一手清空应不等搜索直接打出，实际 ok=%v wr=%v cards=%s", ok, wr, cardsToStr(got))
	}
}

// TestMCTSEnhancedPlay_ContextCancelFallsBack 外部 ctx 已取消时视为超预算：
// 不接管并回报 timedOut，由调用方回退规则引擎（计划 §Phase 4 超时保护）。
func TestMCTSEnhancedPlay_ContextCancelFallsBack(t *testing.T) {
	hand := cards("3 4 5 6 7")
	gctx := GameContext{
		Hand:           hand,
		MustPlay:       true,
		PlayerCounts:   [2]int{1, 1},
		IsLandlord:     true,
		RemainingCards: viewOf(hand, map[card.Rank]int{card.Rank8: 1, card.Rank9: 1}),
		Cfg:            EngineConfig{EnableExactEndgame: true},
	}
	if !shouldUseMCTS(gctx) {
		t.Fatal("测试前置失败：该局面应触发残局搜索")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	got, wr, timedOut := mctsEnhancedPlay(cancelled, gctx)
	if got != nil || wr != 0 || !timedOut {
		t.Errorf("ctx 已取消应回报超时不接管：cards=%s wr=%v timedOut=%v", cardsToStr(got), wr, timedOut)
	}
}

// TestMCTSQuickPlay_LeadsWholeGroup rollout 快速策略领出取"降幅最大"的整组，
// 而非旧口径的点数最小单张（拆散顺子会系统性低估残局水平）。
func TestMCTSQuickPlay_LeadsWholeGroup(t *testing.T) {
	t.Parallel()
	hand := cards("3 4 5 6 7 9 9")
	got := mctsQuickPlay(hand, true, rule.ParsedHand{})
	if len(got) != 5 {
		t.Fatalf("应领出顺子 34567（降幅最大），实际 %s", cardsToStr(got))
	}
	if sumRank(got) != sumRank(cards("3 4 5 6 7")) {
		t.Errorf("顺子点数不符：%s", cardsToStr(got))
	}
	// 跟牌：目标单 8 → 取最小可压的 9（且不拆对子优先降幅）
	beat := mctsQuickPlay(hand, false, parseCards(cards("8")))
	if len(beat) == 0 || beat[0].Rank > card.RankJ {
		t.Errorf("跟牌应压最小同路，实际 %s", cardsToStr(beat))
	}
}

// TestMinHandCount_MemoConcurrent MinHandCount 改成跨调用共享记忆表后，
// 并发结果必须与单线程一致（-race 下运行）。
func TestMinHandCount_MemoConcurrent(t *testing.T) {
	notation := []string{
		"3 3 4 4 5 5 6 6 7 7", "3 4 5 6 7 8 9 10 J Q K A 2", "5 5 5 5 7 8 9",
		"2 2 2 2 3 4", "6 6 7 7 8 8 9", "3 3 3 4 4 4 5 5",
	}
	var want []int
	for _, s := range notation {
		want = append(want, MinHandCount(handToCounts(cards(s))))
	}

	var wg sync.WaitGroup
	got := make([][]int, len(notation))
	for i, s := range notation {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			c := handToCounts(cards(s))
			for k := 0; k < 50; k++ {
				got[i] = append(got[i], MinHandCount(c))
			}
		}(i, s)
	}
	wg.Wait()

	for i := range notation {
		for _, v := range got[i] {
			if v != want[i] {
				t.Fatalf("%s 并发手数 = %d, 期望 %d", notation[i], v, want[i])
			}
		}
	}
}
