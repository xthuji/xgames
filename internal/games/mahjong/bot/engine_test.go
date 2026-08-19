package bot

import (
	"testing"

	"xgames/internal/games/mahjong/rule"
)

// --- 测试辅助 ---

// 万子（花色 0）：tile = 点数-1（3万=2，4万=3，5万=4，7万=6）
const (
	man3 = 2
	man4 = 3
	man5 = 4
	man7 = 6
)

// honorTile 孤张字牌（无顺子潜力）
var honorTile = rule.HonorStart + rule.HonorCount - 1

// seenOf 构造记牌器视图：基础为手牌自身可见，附加额外可见张数（牌河/面子）
func seenOf(hand []int, extra map[int]int) []int {
	seen := make([]int, rule.NumTypes)
	for _, t := range hand {
		seen[t]++
	}
	for t, n := range extra {
		seen[t] += n
	}
	return seen
}

// TestDiscard_KeepFormedSets 已组好的牌型（顺子/对子/刻子）不被拆解，
// 优先打出孤张散牌
func TestDiscard_KeepFormedSets(t *testing.T) {
	e := NewEngine(nil)
	cases := []struct {
		name string
		hand []int
	}{
		{"完整顺子", []int{man3, man4, man5, honorTile}},
		{"对子", []int{man3, man3, honorTile}},
		{"刻子", []int{man3, man3, man3, honorTile}},
	}
	for _, tc := range cases {
		got := e.DecideDiscardEnhanced(DiscardContext{Hand: tc.hand, TurnNumber: 0})
		if got != honorTile {
			t.Errorf("%s：应打孤张散牌保留成型牌型，实际打出 %s", tc.name, rule.DisplayName(got))
		}
	}
}

// TestDiscard_HopelessAfterLongWait 缺中间牌的跳搭等待超过 5 巡仍未组成，
// 且进张余量极少 → 视为无望组织好的牌，改为打出（不再死守）
func TestDiscard_HopelessAfterLongWait(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{man3, man5, man7, honorTile} // 3万5万 + 5万7万 两个缺中间的跳搭
	// 4万已现 3 张（进张余量仅 1），6万已现 1 张
	seen := seenOf(hand, map[int]int{man4: 3, man5 + 1: 1})

	// 前期仍按牌效保留搭子，打孤张字牌
	got := e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 0, Seen: seen})
	if got != honorTile {
		t.Errorf("前期应保留搭子打孤张，实际打出 %s", rule.DisplayName(got))
	}

	// 等待 6 巡后 3万/5万 搭子进张余量仍极少 → 无望，优先打出
	got = e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 6, Seen: seen})
	if got != man3 {
		t.Errorf("长期未组成的无望搭子应优先打出，实际打出 %s", rule.DisplayName(got))
	}
}

// TestDiscard_DeadGapByCounter 记牌器分析：进张全部可见（成组概率为 0）的
// 缺中间搭子按散牌处理；进张充足时仍保留
func TestDiscard_DeadGapByCounter(t *testing.T) {
	e := NewEngine(nil)
	hand := []int{man3, man5, honorTile}

	// 4万 4 张全部可见 → 3万/5万 跳搭成组概率为 0，按散牌打出
	seen := seenOf(hand, map[int]int{man4: 4})
	got := e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 0, Seen: seen})
	if got != man3 && got != man5 {
		t.Errorf("进张已耗尽的跳搭应按散牌打出，实际打出 %s", rule.DisplayName(got))
	}

	// 对照：4万充足时搭子有望成型，仍打孤张字牌
	seen = seenOf(hand, nil)
	got = e.DecideDiscardEnhanced(DiscardContext{Hand: hand, TurnNumber: 0, Seen: seen})
	if got != honorTile {
		t.Errorf("进张充足的搭子应保留，实际打出 %s", rule.DisplayName(got))
	}
}

// TestDiscard_PartialRunOuts 部分搭子进张余量计算：邻搭缺首尾两向、跳搭缺中间
func TestDiscard_PartialRunOuts(t *testing.T) {
	counts := make([]int, rule.NumTypes)
	counts[man3] = 1 // 3万
	counts[man5] = 1 // 5万（跳搭：缺 4万）
	// seen=nil → 全部可用
	if outs := partialRunOuts(counts, nil, man3); outs != 4 {
		t.Errorf("跳搭 3万 的进张余量应为 4，实际 %d", outs)
	}
	// 4万 全部可见 → 余量为 0
	seen := make([]int, rule.NumTypes)
	seen[man4] = 4
	if outs := partialRunOuts(counts, seen, man3); outs != 0 {
		t.Errorf("4万全现时跳搭进张余量应为 0，实际 %d", outs)
	}

	// 邻搭 3万4万：缺首尾两向（2万/5万）
	counts2 := make([]int, rule.NumTypes)
	counts2[man3] = 1
	counts2[man4] = 1
	if outs := partialRunOuts(counts2, nil, man3); outs != 8 {
		t.Errorf("邻搭缺首尾的进张余量应为 8，实际 %d", outs)
	}
}
