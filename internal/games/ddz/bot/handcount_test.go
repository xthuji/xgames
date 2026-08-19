package bot

import "testing"

// TestMinHandCount 典型手牌的最少出牌次数
func TestMinHandCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hand string
		want int
	}{
		// 单一牌型：1 手
		{"3", 1},
		{"3 3", 1},
		{"3 3 3", 1},
		{"3 3 3 5", 1},         // 三带一
		{"3 4 5 6 7", 1},       // 顺子
		{"3 3 4 4 5 5", 1},     // 连对
		{"7 7 7 8 8 8 3 4", 1}, // 飞机带两单
		{"9 9 9 9", 1},         // 炸弹
		{"B R", 1},             // 火箭
		// 组合牌型
		{"3 4 5 6 7 9", 2},                   // 顺子 + 单张
		{"9 9 9 9 3", 2},                     // 炸弹 + 单张
		{"3 3 4 4 5 5 6 6", 1},               // 4 连对
		{"3 4 5 6 7 8 9 T J Q K A 2", 2},     // 12 张顺子 + 2
		{"3 3 4 5 6 7 8 9 T J Q K A 2 2", 3}, // 一对 3 + 顺子 + 一对 2 的拆分
	}
	for _, tc := range cases {
		got := MinHandCount(handToCounts(cards(tc.hand)))
		if got != tc.want {
			t.Errorf("MinHandCount(%q) = %d, want %d", tc.hand, got, tc.want)
		}
	}
}

// TestMinHandCount_Empty 空手牌手数 0
func TestMinHandCount_Empty(t *testing.T) {
	t.Parallel()
	if got := MinHandCount([rankCount]int{}); got != 0 {
		t.Errorf("空手牌手数 = %d, want 0", got)
	}
}
