package rule

import (
	"testing"
)

// TestShantenNumberPrecision 测试向听数计算能区分不同阶段
func TestShantenNumberPrecision(t *testing.T) {
	tests := []struct {
		name        string
		hand        []int
		minShanten  int
		maxShanten  int
		description string
	}{
		{
			name:        "好牌（0-1向听）",
			hand:        []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 27}, // 4顺子+孤张
			minShanten:  0,
			maxShanten:  1,
			description: "4个完成面子应该接近听牌",
		},
		{
			name:        "中等牌（1-3向听）",
			hand:        []int{0, 1, 2, 9, 10, 11, 18, 19, 20, 27, 27, 28, 30}, // 3顺子+1对+2孤张
			minShanten:  1,
			maxShanten:  3,
			description: "3个面子+对子应该在1-3向听范围内",
		},
		{
			name:        "差牌（4-6向听）",
			hand:        []int{0, 9, 18, 27, 28, 29, 30, 31, 32, 33, 1, 10, 19}, // 大量孤张
			minShanten:  4,
			maxShanten:  8,
			description: "大量孤张应该在4-8向听范围内",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counts := CountsFromHand(tt.hand)
			result := ShantenNumber(counts)
			if result < tt.minShanten || result > tt.maxShanten {
				t.Errorf("%s: ShantenNumber=%d, expected between %d and %d. Hand: %v",
					tt.description, result, tt.minShanten, tt.maxShanten, tt.hand)
			}
		})
	}
}

// TestShantenRange 测试向听数范围（0-8）
func TestShantenRange(t *testing.T) {
	// 构造不同向听数的手牌
	testCases := []struct {
		hand        []int
		minShanten  int
		maxShanten  int
		description string
	}{
		{
			hand:        []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			minShanten:  0,
			maxShanten:  2,
			description: "较好的手牌应该在0-2向听范围内",
		},
		{
			hand:        []int{0, 9, 18, 27, 28, 29, 30, 31, 32, 33, 1, 10, 19},
			minShanten:  5,
			maxShanten:  8,
			description: "极差的手牌应该在5-8向听范围内",
		},
	}

	for _, tc := range testCases {
		counts := CountsFromHand(tc.hand)
		result := ShantenNumber(counts)
		if result < tc.minShanten || result > tc.maxShanten {
			t.Errorf("%s: ShantenNumber = %d, expected between %d and %d",
				tc.description, result, tc.minShanten, tc.maxShanten)
		}
	}
}

// TestShantenCacheThreadSafety 测试缓存线程安全
func TestShantenCacheThreadSafety(t *testing.T) {
	hand := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	counts := CountsFromHand(hand)

	// 并发访问缓存
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = ShantenNumberCached(counts)
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestShantenWithMelds 带已鸣面子的向听数：
// 0 面子与 ShantenNumber 等价；面子按公式 8-2×(已完成面子+鸣面子) 折减
func TestShantenWithMelds(t *testing.T) {
	// meldCount=0 时与 ShantenNumber 等价
	for _, h := range [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 27},
		{0, 9, 18, 27, 28, 29, 30, 31, 32, 33, 1, 10, 19},
	} {
		counts := CountsFromHand(h)
		if got := ShantenWithMelds(counts, 0); got != ShantenNumber(counts) {
			t.Errorf("meldCount=0 应与 ShantenNumber 等价: got %d, want %d", got, ShantenNumber(counts))
		}
	}

	// 10 张手牌 + 1 鸣面子，等价 13 张口径（面子 + 手牌）：
	// 面子 + 123万 + 456万 + 78筒 + 东东 → 听牌（0 向听）
	tenpai := CountsFromHand([]int{0, 1, 2, 3, 4, 5, 14, 15, 27, 27})
	if got := ShantenWithMelds(tenpai, 1); got != 0 {
		t.Errorf("面子+两顺子+两面搭子+雀头应为听牌(0), got %d", got)
	}

	// 面子 + 123万 + 456万 + 7筒 + 东东 + 南 → 1 向听（差一个面子）
	oneAway := CountsFromHand([]int{0, 1, 2, 3, 4, 5, 14, 27, 27, 28})
	if got := ShantenWithMelds(oneAway, 1); got != 1 {
		t.Errorf("面子+两顺子+孤张+雀头应为 1 向听, got %d", got)
	}

	// 面子 + 123万 + 456万 + 孤张×3 → 2 向听
	twoAway := CountsFromHand([]int{0, 1, 2, 3, 4, 5, 27, 28, 29, 30})
	if got := ShantenWithMelds(twoAway, 1); got != 2 {
		t.Errorf("面子+两顺子+三张孤张应为 2 向听, got %d", got)
	}

	// 4 鸣面子 + 单张 → 听牌（0 向听，单骑）
	if got := ShantenWithMelds(CountsFromHand([]int{27}), 4); got != 0 {
		t.Errorf("4 面子+单张应为听牌(0), got %d", got)
	}
}
