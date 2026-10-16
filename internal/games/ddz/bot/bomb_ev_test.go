package bot

import (
	"testing"

	"xgames/internal/games/ddz/card"
)

// bomb_ev_test.go Phase 3 单测：炸弹 EV 判据、让牌（tank）、故意犯错次优替换。
// 全部构造确定性场景（显式记牌视图 + 固定手牌 + hard 人格无随机），与手算值对照。

// viewOf 记牌视图：自己手牌 + 指定未现点数，其余点数视为已全部现形
//（未知池精确可控，避免视图缺失带来的保守偏差）
func viewOf(hand []card.Card, unseen map[card.Rank]int) map[card.Rank]int {
	m := make(map[card.Rank]int, rankCount)
	hc := handToCounts(hand)
	for i := 0; i < rankCount; i++ {
		r := idxRank(i)
		n := hc[i]
		if u, ok := unseen[r]; ok {
			n += u
		}
		m[r] = n
	}
	return m
}

func TestOneStepFinishShare(t *testing.T) {
	// 未知池仅 4 张同一点数：对手 4 张只可能是炸弹 → 一步走完占比 1
	var u [rankCount]int
	u[rankIdx(card.Rank5)] = 4
	if got := oneStepFinishShare(u, 4); got != 1 {
		t.Errorf("单一炸弹池一步占比 = %v, 期望 1", got)
	}
	// 两张不同点数单张：只能分两步出 → 一步占比 0
	var u2 [rankCount]int
	u2[rankIdx(card.Rank3)] = 1
	u2[rankIdx(card.Rank4)] = 1
	if got := oneStepFinishShare(u2, 2); got != 0 {
		t.Errorf("两散牌一步占比 = %v, 期望 0", got)
	}
	// 全点数可用：4 张存在炸弹/三带一/两对等多路径，占比应在 (0,1) 开区间
	var uAll [rankCount]int
	for i := range uAll {
		uAll[i] = 4
	}
	if got := oneStepFinishShare(uAll, 4); got <= 0 || got >= 1 {
		t.Errorf("全可用池一步占比 = %v, 期望落在 (0,1)", got)
	}
}

// bombCtx 构造炸弹判定的确定性上下文
func bombCtx(hand []card.Card, unseen map[card.Rank]int, cfg EngineConfig, isLL bool, upIsLL bool,
	counts [2]int, lastTarget []card.Card, lastByLL bool) GameContext {
	g := GameContext{
		IsLandlord:   isLL,
		UpIsLandlord: upIsLL,
		Hand:         hand, MustPlay: false, CanBeat: true,
		PlayerCounts:   counts,
		RemainingCards: viewOf(hand, unseen),
		HasPlayed:      true,
		Personality:    DefaultPersonalities["hard"],
		Cfg:            cfg,
	}
	if !isLL && !upIsLL {
		g.DownIsLandlord = true
	}
	g.RecentPlays[0] = play(cardsToStr(lastTarget), lastByLL)
	g.handCountCache = newHandCountCache()
	g.planCache = newPlanCache(unknownCounts(g.RemainingCards, hand))
	return g
}

func TestShouldUseBomb_EVBranches(t *testing.T) {
	ev := EngineConfig{EnablePlanner: true, EnableInference: true}
	cases := []struct {
		name     string
		gctx     GameContext
		bomb     []card.Card
		want     bool
		wantBase bool // 关开关（基线）时的期望：差异应只来自 EV 判据
	}{
		{
			// 王炸预算：对手领小单、自己手牌还多 → 不毁王炸
			name: "王炸压小单保留", gctx: bombCtx(cards("3 4 5 6 7 8 9 10 J Q K A 2 B R"), nil, ev,
				true, false, [2]int{10, 10}, cards("3"), false),
			bomb: cards("B R"), want: false, wantBase: false,
		},
		{
			// 王炸压炸：唯一正当用途
			name: "王炸压炸打出", gctx: bombCtx(cards("3 4 5 6 7 8 9 10 J Q K A 2 B R"), nil, ev,
				true, false, [2]int{10, 10}, cards("7 7 7 7"), false),
			bomb: cards("B R"), want: true, wantBase: false,
		},
		{
			// 队友冲刺：农民不压队友的牌（即便炸后计划可兑现）
			name: "队友临门不压炸", gctx: bombCtx(cards("9 9 9 9 A 2"), nil, ev,
				false, true, [2]int{10, 2}, cards("3"), false),
			bomb: cards("9 9 9 9"), want: false, wantBase: false,
		},
		{
			// 炸后可兑现：出完炸弹剩 A/2 两手且无更大同路 → 炸
			name: "炸后计划兑现", gctx: bombCtx(cards("9 9 9 9 A 2"), nil, ev,
				true, false, [2]int{10, 10}, cards("3"), false),
			bomb: cards("9 9 9 9"), want: true, wantBase: false,
		},
		{
			// 封堵：地主余 4 张、未知池只有 5 的 4 张 → 必然炸弹一波走 → 抢先炸
			name: "封堵一波流", gctx: bombCtx(cards("9 9 9 9 3 4 5 6 7 8"),
				map[card.Rank]int{card.Rank5: 4}, ev, false, true, [2]int{4, 10}, cards("3"), false),
			bomb: cards("9 9 9 9"), want: true, wantBase: false,
		},
	}
	for _, c := range cases {
		if got := shouldUseBomb(c.gctx, c.bomb); got != c.want {
			t.Errorf("%s：shouldUseBomb = %v, 期望 %v", c.name, got, c.want)
		}
		base := c.gctx
		base.Cfg = EngineConfig{}
		base.planCache = nil
		if got := shouldUseBomb(base, c.bomb); got != c.wantBase {
			t.Errorf("%s（基线）：shouldUseBomb = %v, 期望 %v", c.name, got, c.wantBase)
		}
	}
}

func TestIntentionalImperfection_UsesEngineRanking(t *testing.T) {
	hand := cards("3 4 5 6 7 8 9 10 J Q K A 2")
	gctx := GameContext{IsLandlord: true, Hand: hand, MustPlay: true,
		PlayerCounts: [2]int{10, 10}, RemainingCards: fullRemaining(),
		Personality: BotPersonality{Name: "easy", MistakeProbability: 1.0}}
	ranked := [][]card.Card{cards("3"), cards("4"), cards("5")}

	if got := intentionalImperfection(gctx.Personality, hand, ranked[0], ranked, gctx); !cardsEqual(got, cards("4")) {
		t.Errorf("次优替换应取引擎排序第二项 4，得 %s", cardsToStr(got))
	}
	// 已是末位候选 → 无可替换
	if got := intentionalImperfection(gctx.Personality, hand, ranked[2], ranked, gctx); !cardsEqual(got, ranked[2]) {
		t.Errorf("末位候选不应替换，得 %s", cardsToStr(got))
	}
	// 无排序清单（短路分支领出且无备选）→ 不替换（旧实现按 GenerateHints 原始序误替换）
	if got := intentionalImperfection(gctx.Personality, hand, ranked[0], nil, gctx); !cardsEqual(got, ranked[0]) {
		t.Errorf("空清单不应替换，得 %s", cardsToStr(got))
	}
	// 一手清空手牌的必胜出牌永不替换
	full := cards("3 4 5 6 7 8 9 10 J Q K A 2")
	if got := intentionalImperfection(gctx.Personality, hand, full, ranked, gctx); len(got) != len(hand) {
		t.Errorf("清空手牌的出牌不应被替换")
	}
	// 残局（对手 ≤3 张）不犯错
	endgame := gctx
	endgame.PlayerCounts = [2]int{3, 10}
	if got := intentionalImperfection(gctx.Personality, hand, ranked[0], ranked, endgame); !cardsEqual(got, ranked[0]) {
		t.Errorf("残局不应犯错，得 %s", cardsToStr(got))
	}
}

func TestRankedLeadMoves_SameShapeAscending(t *testing.T) {
	// 领出最小单张 3，备选清单 = 不减手牌的单张按点数升序（拆顺子的候选被排除）
	hand := cards("3 4 5 6 7 9 K A 2")
	gctx := GameContext{IsLandlord: true, Hand: hand, MustPlay: true,
		PlayerCounts: [2]int{10, 10}, RemainingCards: fullRemaining()}
	gctx.handCountCache = newHandCountCache()
	ranked := rankedLeadMoves(&gctx, cards("9"))
	if len(ranked) < 2 {
		t.Fatalf("备选清单过短: %d", len(ranked))
	}
	if !cardsEqual(ranked[0], cards("9")) {
		t.Errorf("首项应为当前选择 9，得 %s", cardsToStr(ranked[0]))
	}
	// 后续项均为单张且点数严格递增（同型次优），且不包含拆散顺子 34567 的候选
	prev := card.Rank9
	for _, c := range ranked[1:] {
		if len(c) != 1 {
			t.Fatalf("备选应为同型单张，得 %s", cardsToStr(c))
		}
		if c[0].Rank <= prev {
			t.Errorf("备选点数应递增：%s 接在 %s 之后", cardsToStr(c), prev.String())
		}
		prev = c[0].Rank
	}
	if !cardsEqual(ranked[1], cards("K")) {
		t.Errorf("次项应为同型最小更大候选 K（3~7 属顺子拆牌不计），得 %s", cardsToStr(ranked[1]))
	}
}

func TestTank_PassesStructureBurn(t *testing.T) {
	// 地主手牌含对 2，对手领单 J/Q：基线用单 2 压（拆对、手数不降），
	// 计划+推断启用后按让牌判据放过这手
	hand := cards("7 9 K 4 Q 2 2 7 Q J 9 10 10 J K")
	ev := EngineConfig{EnablePlanner: true, EnableInference: true}
	for _, ts := range []string{"J", "Q"} {
		gctx := GameContext{
			IsLandlord: true, Hand: hand, MustPlay: false, CanBeat: true,
			RecentPlays: [2]PlayRecord{play(ts, false), {}}, PlayerCounts: [2]int{10, 10},
			RemainingCards: fullRemaining(), HasPlayed: true,
			Personality: DefaultPersonalities["hard"], Cfg: ev,
		}
		gctx.handCountCache = newHandCountCache()
		gctx.planCache = newPlanCache(unknownCounts(gctx.RemainingCards, hand))
		if got := unifiedFollow(gctx); got != nil {
			t.Errorf("target=%s：让牌应 pass，得 %s", ts, cardsToStr(got))
		}
		base := gctx
		base.Cfg = EngineConfig{}
		base.planCache = nil
		if got := unifiedFollow(base); got == nil {
			t.Errorf("target=%s：基线应有出牌（差异仅来自让牌判据）", ts)
		}
	}
}
