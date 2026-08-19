package bot

import (
	"testing"

	"xgames/internal/games/mahjong/rule"
)

// 押引判断（听牌进攻/未听牌防守）专项测试：
// 修复前 genbutsuFallback 晚巡无条件改打现物，自己听牌也弃胡，
// 导致 hard 档自战胡牌率倒挂（7.5% < easy 17.2%，见 docs/bots/mahjong-bot.md §6）。
// 注意 DiscardContext.Hand 为摸牌后 14 张，向听数按"弃掉任一张后"计算。

// tenpaiHand14 三组万子 + 1-2-3筒 + 4筒对儿 + 现物 9筒（听 4/9 筒相关形，听牌）
func tenpaiHand14() []int {
	pin1, pin2, pin9 := 9, 10, 17
	return []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin1, pin2, pin3, pin4, pin9}
}

// farHand14 远听牌：三组万子 + 散乱筒/字（≥2 向听）
func farHand14() []int {
	pin1 := 9
	return []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, pin1, pin6, south, west, north}
}

func genbutsuCtx(hand []int, safeTile int) DiscardContext {
	return DiscardContext{
		Hand:           hand,
		TurnNumber:     13,
		MyDiscards:     []int{safeTile}, // 现物取手牌中存在的牌，findSafeTile 可命中
		OpponentModels: []OpponentModel{{PlayerID: "opp", IsTenpai: true}},
		Difficulty:     DifficultyFor("hard"),
	}
}

// TestGenbutsuFallback_TenpaiKeepsAttacking 听牌不弃胡：自己听牌时现物兜底不触发，
// 保留进攻；未听牌时兜底正常触发改打现物
func TestGenbutsuFallback_TenpaiKeepsAttacking(t *testing.T) {
	// 听牌手：现物 9筒 在手牌中，旧逻辑会改打现物弃胡，修复后不应触发
	if _, ok := genbutsuFallback(genbutsuCtx(tenpaiHand14(), 17), 80); ok {
		t.Errorf("听牌手不应触发现物兜底弃胡（danger=80 也不应放弃听牌）")
	}

	// 未听牌手：现物 北 在手牌中，兜底正常触发改打现物
	safe, ok := genbutsuFallback(genbutsuCtx(farHand14(), north), 80)
	if !ok {
		t.Fatalf("未听牌手应触发现物兜底")
	}
	if safe != north {
		t.Errorf("未听牌手兜底应改打现物北，实际 %s", rule.DisplayName(safe))
	}
}

// TestHandBestShanten 手牌最优向听数：14 张听牌=0、一向听=1、远听牌≥2
func TestHandBestShanten(t *testing.T) {
	cases := []struct {
		name string
		hand []int
		want int
	}{
		{"听牌", tenpaiHand14(), 0},
		{"一向听", []int{man1, man2, man3, man4, man5, man6, man7, man8, man9, 9, 10, 12, 14, north}, 1},
		{"远听牌", farHand14(), 2},
	}
	for _, tc := range cases {
		if got := handBestShanten(tc.hand); got != tc.want {
			t.Errorf("%s: handBestShanten=%d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestTenpaiDefenseFactor 押引缩放：听牌 0.3 / 一向听 0.6 / 两向听及以后 1.0
func TestTenpaiDefenseFactor(t *testing.T) {
	cases := []struct {
		shanten int
		want    float64
	}{
		{0, 0.3}, {1, 0.6}, {2, 1.0}, {8, 1.0},
	}
	for _, tc := range cases {
		if got := tenpaiDefenseFactor(tc.shanten); got != tc.want {
			t.Errorf("tenpaiDefenseFactor(%d)=%v, want %v", tc.shanten, got, tc.want)
		}
	}
}

// TestDefenseWeight_TenpaiPush 听牌手的危险听牌不被评分层防守压过：
// 听牌手在晚巡面对听牌对手时，仍应打出保持听牌的进张相关牌而非自断听牌
func TestDefenseWeight_TenpaiPush(t *testing.T) {
	e := NewEngine(nil)
	hand := tenpaiHand14()
	ctx := genbutsuCtx(hand, 17)
	ctx.Seen = seenOf(hand, map[int]int{17: 1}) // 9筒已见 1 张，仍有余量

	got := e.DecideDiscardEnhanced(ctx)
	// 听牌手打出后仍应保持向听 0（任一合理弃牌都不破坏听牌结构中的面子），
	// 关键断言：不会改打孤张字牌自断搭子（防守缩放生效的间接证据）
	s := rule.ShantenAfterDiscard(rule.CountsFromHand(hand), got)
	if s != 0 {
		t.Errorf("听牌手晚巡应保持听牌进攻，打出 %s 后向听 %d", rule.DisplayName(got), s)
	}
}
