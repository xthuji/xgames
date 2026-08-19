package bot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// --- 测试辅助函数 ---

// cards 从空格分隔的牌面字符串构建 []card.Card（花色统一用黑桃，不影响规则判断）
func cards(notation string) []card.Card {
	tokens := strings.Fields(strings.ToUpper(notation))
	result := make([]card.Card, 0, len(tokens))
	for _, token := range tokens {
		var rank card.Rank
		if token == "10" {
			rank = card.Rank10
		} else {
			r, err := card.RankFromChar(rune(token[0]))
			if err != nil {
				panic(fmt.Sprintf("无效牌面记号: %q", token))
			}
			rank = r
		}
		result = append(result, card.Card{Rank: rank, Suit: card.Spade})
	}
	return result
}

// play 从牌面字符串构建 PlayRecord
func play(notation string, isLandlord bool) PlayRecord {
	c := cards(notation)
	parsed, err := rule.ParseHand(c)
	if err != nil || parsed.Type == rule.Invalid {
		panic(fmt.Sprintf("无法解析出牌记录 %q: %v", notation, err))
	}
	return PlayRecord{Played: parsed, IsLandlord: isLandlord}
}

// newTestEngine 创建静默日志的测试引擎
func newTestEngine() *Engine {
	return NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// fullRemaining 全副牌记牌器（无人出过牌）
func fullRemaining() map[card.Rank]int {
	m := make(map[card.Rank]int)
	for r := card.Rank3; r <= card.Rank2; r++ {
		m[r] = 4
	}
	m[card.RankBlackJoker] = 1
	m[card.RankRedJoker] = 1
	return m
}

// keyRemaining 关键牌记牌器（normal 难度视图：仅 A/2/大小王，无人出过牌）
func keyRemaining() map[card.Rank]int {
	return map[card.Rank]int{
		card.RankA:          4,
		card.Rank2:          4,
		card.RankBlackJoker: 1,
		card.RankRedJoker:   1,
	}
}

// assertLegalPlay 断言出牌合法且（非 nil 时）可解析为合法牌型
func assertLegalPlay(t *testing.T, got []card.Card) rule.ParsedHand {
	t.Helper()
	if got == nil {
		t.Fatal("期望出牌，却返回 nil")
	}
	parsed, err := rule.ParseHand(got)
	if err != nil || parsed.Type == rule.Invalid {
		t.Fatalf("产出了非法牌型: %s", cardsToStr(got))
	}
	return parsed
}

// passInfo 构造有效的过牌推理信息（测试用）
func passInfo(notation string) PassInfo {
	parsed, err := rule.ParseHand(cards(notation))
	if err != nil || parsed.Type == rule.Invalid {
		panic(fmt.Sprintf("无法解析过牌参照 %q: %v", notation, err))
	}
	return PassInfo{Valid: true, Target: parsed}
}

// --- 通用保证 ---

// TestDecidePlay_PassWhenNoBeat 既非必须出牌、也无法压过上家 → pass
func TestDecidePlay_PassWhenNoBeat(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	gctx := GameContext{
		Hand:           cards("3 4 5"),
		RecentPlays:    [2]PlayRecord{play("2", true), {}},
		MustPlay:       false,
		CanBeat:        false,
		RemainingCards: fullRemaining(),
	}
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("无牌可压时应 pass，却出了 %s", cardsToStr(got))
	}
}

// TestDecidePlay_MustPlayNeverNil 领出回合必出合法牌（难度只影响记牌视图，出牌逻辑统一）
func TestDecidePlay_MustPlayNeverNil(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	hands := []string{"3 4 5 6 7 8 9", "2 2 2 2", "B R", "K", "3 3 9 9 9"}
	views := []map[card.Rank]int{fullRemaining(), keyRemaining()} // 全量视图 / 关键牌视图（normal 难度）
	for _, view := range views {
		for _, h := range hands {
			gctx := GameContext{
				Hand:           cards(h),
				MustPlay:       true,
				CanBeat:        true,
				RemainingCards: view,
				PlayerCounts:   [2]int{10, 10},
			}
			got := e.DecidePlay(context.Background(), "bot", gctx)
			if got == nil {
				t.Fatalf("手牌 %q 领出时不应 pass", h)
			}
			assertLegalPlay(t, got)
		}
	}
}

// --- 统一策略：记牌视图差异下的行为验证（难度只影响记牌完整度） ---

// TestNormalFollow_TeammatePass 农民配合：队友大牌不压（除非自己能直接走完）；
// 队友小牌则接手跑牌（避免牌权被小牌锁死）。
// 以地主下家（跑牌位）身份构造，避免上家主攻模式改变跟牌行为（见 docs/bots/ddz-bot.md §5.4）
func TestNormalFollow_TeammatePass(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 队友出大牌（J）且无法一手走完 → 让牌（已开张后才谈配合）
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("Q K 3 4"),
		RecentPlays:    [2]PlayRecord{play("J", false), {}}, // 队友（非地主）出牌
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	if got != nil {
		t.Errorf("不应压队友的大牌，却出了 %s", cardsToStr(got))
	}

	// 能一手走完 → 直接出完获胜
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("5"),
		RecentPlays:    [2]PlayRecord{play("4", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	if got == nil || got[0].Rank != card.Rank5 {
		t.Errorf("能直接走完时应出牌，实际 %v", got)
	}

	// 队友领出小牌（4）→ 接手跑牌（最小压牌，不增手数）
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("7 8 9 9"),
		RecentPlays:    [2]PlayRecord{play("4", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed := assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank7 {
		t.Errorf("应接手队友小牌出单 7，实际 %s", cardsToStr(got))
	}
}

// TestNormalFollow_KeepBomb 已开张的机器人非危急时刻不用炸弹压牌；
// 未开张时则不计代价先出一手（含炸弹）避免整局零出牌
func TestNormalFollow_KeepBomb(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	gctx := GameContext{
		Hand:           cards("7 7 7 7 3 3 4"),
		RecentPlays:    [2]PlayRecord{play("8", true), {}}, // 地主出单 8，只有炸弹能压
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10}, // 对手不危急
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("非危急时刻应保留炸弹，却出了 %s", cardsToStr(got))
	}

	// 未开张：不计代价先出一手（此时只有炸弹能压）
	gctx.HasPlayed = false
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Bomb {
		t.Errorf("未开张时应先用炸弹开张，实际 %s", parsed.Type.String())
	}
}

// TestLead_BlockAsUpper 地主上家领出顶牌：对手剩牌极少且无更大牌 → 出 2 死封
func TestNormalLead_BlockAsUpper(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	// 场上剩余仅自己手牌（对手无王/2）→ 2 绝对安全
	remaining := map[card.Rank]int{card.Rank2: 1, card.Rank5: 1, card.Rank6: 1}
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("2 5 6"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{8, 2}, // 下家（地主）剩 2 张，需要顶牌
		DownIsLandlord: true,
		RemainingCards: remaining,
	})
	parsed := assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank2 {
		t.Errorf("地主上家应顶出绝对安全的 2，实际 %s", cardsToStr(got))
	}
}

// TestHardLead_HandCountReduce 领出只选令总手数减一的组合
func TestHardLead_HandCountReduce(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	// 手牌 = 顺子 34567 + 顺子 9TJQK（手数 2）；拆出任何单张手数都会变 3+，
	// 两条顺子候选按同手数取小面值 → 应出 34567
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 4 5 6 7 9 T J Q K"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		RemainingCards: fullRemaining(),
	})
	parsed := assertLegalPlay(t, got)
	// KeyRank 现在是顺子的最大点数（Rank7），不是起始点数
	if parsed.Type != rule.Straight || len(got) != 5 || got[0].Rank != card.Rank3 {
		t.Errorf("应领出顺子 34567，实际 %s", cardsToStr(got))
	}
}

// TestHardLead_EndgameSeal 残局模式：地主剩 ≤2 张时农民上家无条件打最大牌死封
func TestHardLead_EndgameSeal(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("5 K 2"),
		MustPlay:       true,
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 2}, // 下家是地主且只剩 2 张
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
	})
	parsed := assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank2 {
		t.Errorf("残局应打出最大单张 2 死封，实际 %s", cardsToStr(got))
	}
}

// TestShedFollow_MidCardRun 跑牌方积极跟牌：地主/地主下家对中段及以下
// 的单张/对子必须跟出最小同路可压牌（允许动用 Q/K/A），
// 避免中段牌不跟导致小牌滞留手中
func TestShedFollow_MidCardRun(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 地主：手中小牌一串 + 掌权牌，农民领单 7 → 跟 Q 拿牌权甩小牌（不应 PASS）
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 4 5 6 Q K A 2"),
		RecentPlays:    [2]PlayRecord{play("7", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		IsLandlord:     true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed := assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankQ {
		t.Errorf("地主跟单 7 应跟最小可压的 Q，实际 %s", cardsToStr(got))
	}

	// 地主下家：同型手牌，地主领单 7 → 同样跟 Q
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 4 5 6 Q K A 2"),
		RecentPlays:    [2]PlayRecord{play("7", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{14, 12},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed = assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankQ {
		t.Errorf("地主下家跟单 7 应跟最小可压的 Q，实际 %s", cardsToStr(got))
	}

	// 地主下家：中段牌充足 → 优先跟最小的 8（不出 J/Q）
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("8 9 10 J Q K A"),
		RecentPlays:    [2]PlayRecord{play("6", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{14, 12},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed = assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank8 {
		t.Errorf("地主下家跟单 6 应跟最小的 8，实际 %s", cardsToStr(got))
	}

	// 对子同路跟牌：地主跟对 7 → 出最小整对 8
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("5 5 8 8 9 9 K K"),
		RecentPlays:    [2]PlayRecord{play("7 7", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		IsLandlord:     true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed = assertLegalPlay(t, got)
	if parsed.Type != rule.Pair || parsed.KeyRank != card.Rank8 {
		t.Errorf("地主跟对 7 应跟最小整对 8，实际 %s", cardsToStr(got))
	}

	// 2/王留作后手收权：可压牌仅 A/2 时跟 A 不跟 2
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 4 5 A 2"),
		RecentPlays:    [2]PlayRecord{play("7", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		IsLandlord:     true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	parsed = assertLegalPlay(t, got)
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankA {
		t.Errorf("地主跟单 7 应跟 A 而 2 留作收权，实际 %s", cardsToStr(got))
	}

	// 不拆整手连对：唯一可跟出法会破坏连对结构（手数变差）→ 仍让牌
	got = e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("6 6 7 7 8 8 9 9"),
		RecentPlays:    [2]PlayRecord{play("5", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		IsLandlord:     true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	})
	if got != nil {
		t.Errorf("拆连对跟单张手数变差，应让牌，却出了 %s", cardsToStr(got))
	}
}

// TestFollow_LikelyWinByPassInfer 过牌推理预估胜出：地主曾对更大的同路牌过牌，
// 说明其压不过当前的单张，应果断用预估胜出牌收权（而非保守让牌）
func TestFollow_LikelyWinByPassInfer(t *testing.T) {
	t.Parallel()
	e := newTestEngine()

	// 记牌视图：所有普通点数剩余均 <4（无炸弹威胁），王牌已出完（0=已出完）
	noBombThreat := func() map[card.Rank]int {
		m := make(map[card.Rank]int)
		for r := card.Rank3; r <= card.Rank2; r++ {
			m[r] = 2
		}
		m[card.RankBlackJoker] = 0
		m[card.RankRedJoker] = 0
		return m
	}
	gctx := GameContext{
		Hand:           cards("A 4 5 6"),
		RecentPlays:    [2]PlayRecord{play("Q", true), {}}, // 地主出单 Q
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: noBombThreat(),
		HasPlayed:      true,
		PassInfos:      [2]PassInfo{{}, passInfo("K")}, // 地主曾对单 K 过牌
	}
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankA {
		t.Errorf("地主压不过单张时应出 A 收权，实际 %s", cardsToStr(nil))
	}

	// 无过牌推理依据且非绝对控制 → 保守让牌（A 出手后手数不变但可能被更大单张压回）
}

// TestFollow_TeammateNearFinish 队友接近走完（≤2 张）：不接队友的牌，把牌权留给队友冲刺，
// 即使平时会接手队友的小牌（队友牌多时仍需接应）
func TestFollow_TeammateNearFinish(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	base := GameContext{
		Hand:           cards("4 4 4 5 9"),
		RecentPlays:    [2]PlayRecord{play("4", false), {}}, // 队友领出小牌（需接应的牌）
		CanBeat:        true,
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
	}

	// 队友剩 2 张 → 让牌送队友走完（上家是队友：PlayerCounts[0]）
	gctx := base
	gctx.PlayerCounts = [2]int{2, 8}
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("队友近完牌时应让牌，却出了 %s", cardsToStr(got))
	}

	// 队友牌还多 → 照常接手跑牌（对比组，确保新规则不误伤常规配合）
	gctx = base
	gctx.PlayerCounts = [2]int{3, 8}
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank5 {
		t.Errorf("队友牌多时应接手出单 5，实际 %s", cardsToStr(nil))
	}
}

// TestLead_SendToTeammate 队友接近走完且地主不危急：领出送小牌交出牌权帮队友冲刺；
// 地主危急时顶牌位优先封堵，不送牌
func TestLead_SendToTeammate(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	base := GameContext{
		Hand:         cards("3 7 K"),
		MustPlay:     true,
		CanBeat:      true,
		UpIsLandlord: true, // 上家是地主 → 下家（PlayerCounts[1]）是队友（跑牌位）
	}

	// 队友剩 2 张、地主不危急 → 送最小牌（单 3）
	gctx := base
	gctx.PlayerCounts = [2]int{8, 2}
	gctx.RemainingCards = fullRemaining()
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Single || parsed.KeyRank != card.Rank3 {
		t.Errorf("应送最小单张 3 给队友跑牌，实际 %s", cardsToStr(nil))
	}

	// 顶牌位（下家是地主）且地主危急（≤2 张）→ 残局死封出最大单张，不送牌
	parsed = assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("3 7 K"),
		MustPlay:       true,
		CanBeat:        true,
		DownIsLandlord: true,
		PlayerCounts:   [2]int{8, 2},
		RemainingCards: fullRemaining(),
	}))
	if parsed.KeyRank != card.RankK {
		t.Errorf("地主危急时应顶大牌而非送牌，实际 %s", cardsToStr(nil))
	}
}

// TestHardFollow_ControlCard 跟牌优先使用绝对控制牌收回牌权
func TestHardFollow_ControlCard(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("R 5 6 8 8"),
		RecentPlays:    [2]PlayRecord{play("K", true), {}}, // 地主出单 K
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
	})
	parsed := assertLegalPlay(t, got)
	if parsed.KeyRank != card.RankRedJoker {
		t.Errorf("应用大王（绝对控制牌）收权，实际 %s", cardsToStr(got))
	}
}

// TestHardFollow_EndgameBomb 残局强封：地主剩 ≤2 张时农民上家可用炸弹死封
func TestHardFollow_EndgameBomb(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	got := e.DecidePlay(context.Background(), "bot", GameContext{
		Hand:           cards("5 5 5 5 8"),
		RecentPlays:    [2]PlayRecord{play("3", true), {}}, // 地主出单 3
		CanBeat:        true,
		PlayerCounts:   [2]int{10, 2}, // 下家是地主且危急
		DownIsLandlord: true,
		RemainingCards: fullRemaining(),
	})
	parsed := assertLegalPlay(t, got)
	if parsed.Type != rule.Bomb {
		t.Errorf("残局危急时应炸弹死封，实际 %s", cardsToStr(got))
	}
}

// TestFollow_StuckReplanBreakStraight 拆牌重计划：连续多次无法管住对手后，
// 允许拆散整手连对去压住对手夺取牌权（原本手数变差会让牌）
func TestFollow_StuckReplanBreakStraight(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	gctx := GameContext{
		Hand:           cards("6 6 7 7 8 8 9 9"),
		RecentPlays:    [2]PlayRecord{play("5", false), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		IsLandlord:     true,
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
		UnbeatenStreak: 2, // 已连续两手无法管住对手：改变出牌计划
	}
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Single {
		t.Errorf("拆牌重计划应跟单张夺取牌权，实际 %s", cardsToStr(parsed.Cards))
	}

	// 对照：无连续让牌时维持原出牌计划，仍应让牌保护连对
	gctx.UnbeatenStreak = 0
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("未触发重计划时应让牌，却拆连对出了 %s", cardsToStr(got))
	}
}

// TestFollow_StuckUsesPowerButKeeps2 拆牌重计划放开 Q/K/A 夺权，
// 但 2 与王仍留作终极收权底牌。
// 手牌 7 张 / 5 手 / 仅 1 张 2：避免触发 MCTS（>5）、主攻模式（>6 张、>3 手、
// 2/王 <2），纯测规则路径
func TestFollow_StuckUsesPowerButKeeps2(t *testing.T) {
	t.Parallel()
	e := newTestEngine()
	gctx := GameContext{
		Hand:           cards("3 3 4 5 5 K 2"),
		RecentPlays:    [2]PlayRecord{play("8", true), {}},
		CanBeat:        true,
		PlayerCounts:   [2]int{12, 14},
		DownIsLandlord: true, // 顶牌位（地主上家）：验证掌权牌保护与重计划的交互
		RemainingCards: fullRemaining(),
		HasPlayed:      true,
		UnbeatenStreak: 2,
	}
	parsed := assertLegalPlay(t, e.DecidePlay(context.Background(), "bot", gctx))
	if parsed.Type != rule.Single || parsed.KeyRank != card.RankK {
		t.Errorf("重计划应用 K 夺权而保留 2，实际 %s", cardsToStr(parsed.Cards))
	}

	// 对照：无连续让牌时掌权牌保护生效，应让牌
	gctx.UnbeatenStreak = 0
	if got := e.DecidePlay(context.Background(), "bot", gctx); got != nil {
		t.Errorf("未触发重计划时应让牌保留掌权牌，却出了 %s", cardsToStr(got))
	}
}
