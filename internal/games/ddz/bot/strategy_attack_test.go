package bot

import (
	"context"
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// 主攻模式判定与决策回归（docs/bots/ddz-bot.md §5.4）：
// 农民上家手牌易走完时应以自己为主打地主——领出跑小牌、跟队友小牌跑牌、
// 放宽 Q/K/A 夺权；否决条件（地主 ≤2 / 队友 ≤2）维持硬豁免优先。

// TestFarmerUpperAttackModeJudgment 判定函数表驱动：必要条件 + 否决 + 触发（A1/A2/A3/A4 场景）
func TestFarmerUpperAttackModeJudgment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		gctx GameContext
		want bool
	}{
		{
			name: "非上家：地主不适用",
			gctx: GameContext{IsLandlord: true, Hand: cards("3 4 5 7 9 J"), PlayerCounts: [2]int{10, 10}},
			want: false,
		},
		{
			name: "非上家：地主下家（跑牌位）不适用",
			gctx: GameContext{UpIsLandlord: true, Hand: cards("3 4 5 7 9 J"), PlayerCounts: [2]int{10, 10}},
			want: false,
		},
		{
			name: "否决：地主 ≤2 张（死封路径优先）",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("3 4 5 7 9 J"), PlayerCounts: [2]int{8, 2}},
			want: false,
		},
		{
			name: "否决：队友 ≤2 张（送权帮队友冲刺）",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("3 4 5 7 9 J"), PlayerCounts: [2]int{2, 9}},
			want: false,
		},
		{
			name: "触发 A1：手牌 ≤6",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("3 4 5 7 9 J"), PlayerCounts: [2]int{8, 9}},
			want: true,
		},
		{
			name: "触发 A2：手数 ≤3（连对整牌型）",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("3 3 4 4 5 5 6 6"), PlayerCounts: [2]int{8, 9}},
			want: true,
		},
		{
			name: "触发 A3：2/王 ≥2 张且手牌 ≤10",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("2 B 3 5 7 9 J K"), PlayerCounts: [2]int{8, 9}},
			want: true,
		},
		{
			name: "触发 A4 场景：两步走完+夺权牌（被 A2 覆盖，整体判定为真）",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("B R 3 4 5 6 7"), PlayerCounts: [2]int{8, 9}},
			want: true,
		},
		{
			name: "不触发：手牌多且散、无 2/王集中",
			gctx: GameContext{DownIsLandlord: true, Hand: cards("7 9 J Q K A 3"), PlayerCounts: [2]int{8, 9}},
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := farmerUpperAttackMode(tt.gctx); got != tt.want {
				t.Errorf("farmerUpperAttackMode = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLead_AttackModeRunsSmallCards 主攻领出：敌余牌 6~10 张时出最小整组小牌
// 而非 7~J 顶牌（辅攻对照维持 pickBlockCard 语义）
func TestLead_AttackModeRunsSmallCards(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 主攻：手牌 ≤6（A1 触发）→ 领出单 3 跑小牌，掌权牌留收尾
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 3 4 5 6 7"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{8, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
	}))
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank3 {
		t.Errorf("主攻领出应跑最小单张 3，实际 %s", cardsToStr(parsed.Cards))
	}

	// 辅攻基线：手牌多且散（不触发主攻）→ 维持顶牌语义（中段 7~J 单张）
	parsed = assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("7 9 J Q K A 3"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{8, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
	}))
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankJ {
		t.Errorf("辅攻领出应顶中段单张 J，实际 %s", cardsToStr(parsed.Cards))
	}
}

// TestFollow_TeammateSmallCardShed 主攻跟队友：借队友 ≤J 小牌跑掉自己的小牌；
// 队友领大牌时让牌（不消耗掌权牌压队友）；辅攻基线维持接应语义
func TestFollow_TeammateSmallCardShed(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 主攻 + 队友领单 3，手中 44+5/6/7/9 散牌 → 跟 4（跟随小牌）而非让牌；
	// 注：跟单 4 不破坏结构（4,5,6,7 非顺子，拆散对子手数不增）
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("4 4 5 6 7 9"),
		RecentPlays:    [2]PlayRecord{play("3", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}))
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank4 {
		t.Errorf("主攻应借队友小牌跟单 4，实际 %s", cardsToStr(parsed.Cards))
	}

	// 主攻 + 队友领对 A：无 ≤J 同路小牌可跟 → 让牌（对 2 保留作收尾底牌）
	if got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("2 2 3 5 7 9"),
		RecentPlays:    [2]PlayRecord{play("A A", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}); got != nil {
		t.Errorf("队友领对 A 时应让牌（2 留作收尾），却出了 %s", cardsToStr(got))
	}

	// 辅攻基线 + 队友领单 5 → 维持 pickReliefBeat 接应语义（同路最小压牌）
	parsed = assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("4 4 8 9 10 J K"),
		RecentPlays:    [2]PlayRecord{play("5", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}))
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank8 {
		t.Errorf("辅攻应接应队友小牌（单 8），实际 %s", cardsToStr(parsed.Cards))
	}
}

// TestMCTS_AttackModeNotExempted 主攻模式下队友跟牌不再豁免 MCTS；
// 非主攻（队友 ≤2 否决）维持豁免
func TestMCTS_AttackModeNotExempted(t *testing.T) {
	t.Parallel()

	attackCtx := GameContext{
		Hand:           cards("4 K 5 6 7"),
		RecentPlays:    [2]PlayRecord{play("3", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 9},
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}
	if !farmerUpperAttackMode(attackCtx) {
		t.Fatal("测试前置失败：该上下文应处于主攻模式")
	}
	if got, _ := mctsEnhancedPlay(attackCtx); got == nil {
		t.Error("主攻模式下队友跟牌不应豁免 MCTS，应接管出牌")
	}

	// 对照：队友 ≤2 张（主攻被否决）→ 维持豁免，交还规则引擎配合逻辑
	vetoCtx := attackCtx
	vetoCtx.PlayerCounts = [2]int{2, 9}
	if farmerUpperAttackMode(vetoCtx) {
		t.Fatal("测试前置失败：队友 ≤2 张应否决主攻模式")
	}
	if got, _ := mctsEnhancedPlay(vetoCtx); got != nil {
		t.Errorf("非主攻的队友跟牌应豁免 MCTS，却被接管出 %s", cardsToStr(got))
	}
}
