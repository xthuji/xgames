package bot

import (
	"testing"

	"xgames/internal/games/ddz/card"
)

// trackedRemaining 构造全量记牌视图：全副牌 − 指定手牌
func trackedRemaining(hand []card.Card) map[card.Rank]int {
	rc := fullRemaining()
	for _, c := range hand {
		rc[c.Rank]--
	}
	return rc
}

// highRanksCleared 在记牌视图中清空指定点数及以上的大牌（模拟已全部打出）
func highRanksCleared(rc map[card.Rank]int, from card.Rank) {
	for r := from; r <= card.Rank2; r++ {
		rc[r] = 0
	}
	rc[card.RankBlackJoker] = 0
	rc[card.RankRedJoker] = 0
}

// TestSprintMode_Thresholds 冲刺模式触发条件：手数 ≤3 且敌方/队友余牌均未临门
func TestSprintMode_Thresholds(t *testing.T) {
	hand := cards("3 4 5") // 三个单张 = 3 手

	// 农民下家、敌方余牌充足 → 冲刺
	gctx := GameContext{
		Hand:           hand,
		PlayerCounts:   [2]int{10, 10},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
	}
	if !sprintMode(gctx) {
		t.Error("手数 3 且双方无临门危机应触发冲刺")
	}

	// 地主无队友概念，同样触发
	gctx.IsLandlord = true
	gctx.UpIsLandlord = false
	if !sprintMode(gctx) {
		t.Error("地主手数 3 且敌方无临门危机应触发冲刺")
	}

	// 敌方临门（≤2 张）→ 否决冲刺（死封优先）
	gctx.IsLandlord = false
	gctx.UpIsLandlord = true
	gctx.PlayerCounts = [2]int{2, 10}
	if sprintMode(gctx) {
		t.Error("敌方 ≤2 张应否决冲刺（死封优先）")
	}

	// 队友临门（≤2 张）→ 否决冲刺（送权优先）：地主下家视角，队友在下家
	gctx = GameContext{
		Hand:           hand,
		PlayerCounts:   [2]int{9, 2},
		UpIsLandlord:   true,
		RemainingCards: fullRemaining(),
	}
	if sprintMode(gctx) {
		t.Error("队友 ≤2 张应否决冲刺（送权优先）")
	}

	// 手数 >3 → 不冲刺（互不相连的散牌：5 个对子 + 单张）
	gctx.PlayerCounts = [2]int{9, 9}
	gctx.Hand = cards("3 3 5 5 7 7 9 9 J")
	if sprintMode(gctx) {
		t.Error("手数 >3 不应触发冲刺")
	}
}

// TestSprintLead_TrioTakesKicker 冲刺领出：三条可带翅时必带翅（三带一优于裸三条），
// 掌权主体（999 已无更大三条）回收牌权并顺带清走小牌
func TestSprintLead_TrioTakesKicker(t *testing.T) {
	hand := cards("9 9 9 6 7")
	rc := trackedRemaining(hand)
	highRanksCleared(rc, card.Rank10) // 10 以上全部出完 → 999 为绝对掌权三条

	gctx := GameContext{
		Hand:           hand,
		MustPlay:       true,
		IsLandlord:     true,
		PlayerCounts:   [2]int{10, 10},
		RemainingCards: rc,
	}
	got := sprintLead(gctx)
	if got == nil {
		t.Fatal("冲刺领出应返回候选")
	}
	if cardsToStr(got) != cardsToStr(cards("9 9 9 6")) {
		t.Errorf("三条应带最小翅 999+6，实际 %s", cardsToStr(got))
	}
}

// TestSprintLead_SmallFirstBigLast 冲刺领出：倒数第二手非绝对掌权 → 先出小牌，
// 大牌留最后一手便于跟随取胜；当前手绝对掌权时优先打出回收牌权
func TestSprintLead_SmallFirstBigLast(t *testing.T) {
	// 单 5 + 单 K，A/2 仍在场上 → K 非掌权，先出 5 留 K 收尾
	hand := cards("5 K")
	gctx := GameContext{
		Hand:           hand,
		MustPlay:       true,
		IsLandlord:     true,
		PlayerCounts:   [2]int{10, 10},
		RemainingCards: trackedRemaining(hand),
	}
	if got := sprintLead(gctx); cardsToStr(got) != cardsToStr(cards("5")) {
		t.Errorf("倒数第二手非掌权应先出小牌 5 留 K 收尾，实际 %s", cardsToStr(got))
	}

	// 单 5 + 单 2，2 以上全部出完 → 2 为绝对掌权，优先打出回收牌权
	hand = cards("5 2")
	rc := trackedRemaining(hand)
	highRanksCleared(rc, card.RankA)
	gctx.RemainingCards = rc
	gctx.Hand = hand
	if got := sprintLead(gctx); cardsToStr(got) != cardsToStr(cards("2")) {
		t.Errorf("绝对掌权单张 2 应优先打出回收牌权，实际 %s", cardsToStr(got))
	}
}

// TestSprintLead_CautiousOverridden 冲刺优先于谨慎控牌：手数 ≤3 时
// 不再为防对手收权而只出 K 级压制牌，改为先出小牌散牌
func TestSprintLead_CautiousOverridden(t *testing.T) {
	hand := cards("3 5 5 K")
	gctx := GameContext{
		Hand:           hand,
		MustPlay:       true,
		IsLandlord:     true,
		PlayerCounts:   [2]int{8, 9},
		RemainingCards: trackedRemaining(hand),
	}
	got := sprintLead(gctx)
	if got == nil {
		t.Fatal("冲刺领出应返回候选")
	}
	if cardsToStr(got) != cardsToStr(cards("3")) {
		t.Errorf("冲刺应先出最小单张 3 散牌，实际 %s", cardsToStr(got))
	}
}

// TestSprintLead_NotTriggeredWhenEnemyClose 敌方临门时不进入冲刺：
// 农民上家维持死封路径（打最大单张/对子）
func TestSprintLead_NotTriggeredWhenEnemyClose(t *testing.T) {
	hand := cards("3 5 5 K")
	gctx := GameContext{
		Hand:           hand,
		MustPlay:       true,
		PlayerCounts:   [2]int{9, 2}, // 下家是地主且只剩 2 张
		DownIsLandlord: true,         // 农民上家
		RemainingCards: trackedRemaining(hand),
	}
	got := unifiedLead(gctx)
	if got == nil {
		t.Fatal("死封应返回候选")
	}
	if cardsToStr(got) != cardsToStr(cards("K")) {
		t.Errorf("敌方 ≤2 张应打最大单张死封，实际 %s", cardsToStr(got))
	}
}

// TestSprintFollow_UpperSeatShedBeat 冲刺跟牌不分席位：农民上家对中段及以下
// 的敌方单张也应同路跟压（此前仅地主/地主下家积极跟牌）
func TestSprintFollow_UpperSeatShedBeat(t *testing.T) {
	hand := cards("K 5 5 3") // 手数 3：K、55、3
	gctx := GameContext{
		Hand:           hand,
		RecentPlays:    [2]PlayRecord{play("9", true), {}},
		CanBeat:        true,
		HasPlayed:      true,
		PlayerCounts:   [2]int{8, 10},
		DownIsLandlord: true, // 农民上家，上家为地主
		RemainingCards: trackedRemaining(hand),
	}
	got := unifiedFollow(gctx)
	if got == nil {
		t.Fatal("冲刺跟牌应压住敌方中段单张")
	}
	if cardsToStr(got) != cardsToStr(cards("K")) {
		t.Errorf("应以最小可压牌 K 同路跟压，实际 %s", cardsToStr(got))
	}
}

// TestSprintFollow_PowerBeatRegainsLead 冲刺跟牌使用掌权牌回收牌权：
// 敌方领出 Q 时农民上家不再因掌权保护而让牌
func TestSprintFollow_PowerBeatRegainsLead(t *testing.T) {
	hand := cards("K 5 5 3")
	gctx := GameContext{
		Hand:           hand,
		RecentPlays:    [2]PlayRecord{play("Q", true), {}},
		CanBeat:        true,
		HasPlayed:      true,
		PlayerCounts:   [2]int{8, 10},
		DownIsLandlord: true,
		RemainingCards: trackedRemaining(hand),
	}
	got := unifiedFollow(gctx)
	if got == nil {
		t.Fatal("冲刺跟牌应用 K 压住 Q 回收牌权")
	}
	if cardsToStr(got) != cardsToStr(cards("K")) {
		t.Errorf("应出 K 压 Q 夺取牌权，实际 %s", cardsToStr(got))
	}
}

// TestSprintFollow_TeammateTakeover 冲刺跟队友牌：借不到同路小牌时以最小接应
// 夺取牌权接管节奏（队友余牌尚多、自己手数 ≤3 时抢跑优先于单纯让牌）
func TestSprintFollow_TeammateTakeover(t *testing.T) {
	hand := cards("K 3 3 5")
	gctx := GameContext{
		Hand:           hand,
		RecentPlays:    [2]PlayRecord{play("Q", false), {}},
		CanBeat:        true,
		HasPlayed:      true,
		PlayerCounts:   [2]int{10, 9},
		UpIsLandlord:   true, // 农民下家，队友在下家
		RemainingCards: trackedRemaining(hand),
	}
	got := unifiedFollow(gctx)
	if got == nil {
		t.Fatal("冲刺跟队友牌应以最小接应接管牌权")
	}
	if cardsToStr(got) != cardsToStr(cards("K")) {
		t.Errorf("应以 K 最小接应队友的 Q，实际 %s", cardsToStr(got))
	}
}
