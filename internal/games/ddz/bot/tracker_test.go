package bot

import (
	"testing"

	"xgames/internal/games/ddz/card"
)

// TestHandCountsRoundTrip 手牌 ↔ 计数数组互转
func TestHandCountsRoundTrip(t *testing.T) {
	t.Parallel()
	hand := cards("3 3 K 2 B R")
	c := handToCounts(hand)
	if countsTotal(c) != len(hand) {
		t.Fatalf("计数总数 = %d, want %d", countsTotal(c), len(hand))
	}
	if c[rankIdx(card.Rank3)] != 2 || c[rankIdx(card.RankK)] != 1 ||
		c[rankIdx(card.Rank2)] != 1 || c[idxBlackJoker] != 1 || c[idxRedJoker] != 1 {
		t.Fatalf("计数数组错误: %v", c)
	}
	// 计数数组转回抽象手牌后总张数一致
	if got := countsToCards(c); len(got) != len(hand) {
		t.Fatalf("countsToCards 张数 = %d, want %d", len(got), len(hand))
	}
}

// TestUnknownCounts 未知牌池 = 全场剩余 − 自己手牌
func TestUnknownCounts(t *testing.T) {
	t.Parallel()
	remaining := fullRemaining()
	hand := cards("3 4 4 B")
	u := unknownCounts(remaining, hand)
	if u[rankIdx(card.Rank3)] != 3 || u[rankIdx(card.Rank4)] != 2 {
		t.Fatalf("扣除己手牌失败: %v", u)
	}
	if u[rankIdx(card.Rank5)] != 4 || u[idxBlackJoker] != 0 {
		t.Fatalf("未持有的点数不应被扣: %v", u)
	}
}

// TestPotentialBombRanks 未知池中某点数剩 4 张 → 潜在炸弹预警
func TestPotentialBombRanks(t *testing.T) {
	t.Parallel()
	var u [rankCount]int
	u[rankIdx(card.Rank8)] = 4 // 对手可能握有全部 4 张 8
	u[rankIdx(card.Rank5)] = 3 // 3 张不预警
	ranks := potentialBombRanks(u)
	if len(ranks) != 1 || ranks[0] != card.Rank8 {
		t.Fatalf("潜在炸弹 = %v, want [8]", ranks)
	}
}

// TestIsAbsoluteControl 绝对控制牌：打出后未知池中无更大的同路牌
func TestIsAbsoluteControl(t *testing.T) {
	t.Parallel()
	var u [rankCount]int
	u[rankIdx(card.Rank2)] = 1
	// 无王剩余 → 单张 2 绝对控制
	if !isAbsoluteControl(u, card.Rank2, 1) {
		t.Fatal("无王剩余时 2 应绝对控制")
	}
	// 未知池还有大王 → 2 不再绝对控制
	u[idxRedJoker] = 1
	if isAbsoluteControl(u, card.Rank2, 1) {
		t.Fatal("大王未出时 2 不应绝对控制")
	}
	// 手中没有足够张数（未知池计数不足）→ 不成立
	var empty [rankCount]int
	if isAbsoluteControl(empty, card.Rank2, 1) {
		t.Fatal("未知池中无该点数时不应成立")
	}
}

// TestIsBigCardSafe 大牌绝对安全（王恒被跟踪：已出完显式为 0，缺失=未跟踪按不安全处理）
func TestIsBigCardSafe(t *testing.T) {
	t.Parallel()
	// 王已全部打出（视图内显式为 0）→ 手中的 2 绝对安全
	remaining := map[card.Rank]int{card.Rank2: 1, card.RankBlackJoker: 0, card.RankRedJoker: 0}
	if !isBigCardSafe(remaining, cards("2"), card.Rank2) {
		t.Fatal("双王已出时 2 应绝对安全")
	}
	// 王未被跟踪（视图中缺失）→ 保守认为不安全
	if isBigCardSafe(map[card.Rank]int{card.Rank2: 1}, cards("2"), card.Rank2) {
		t.Fatal("王未跟踪时 2 不应绝对安全")
	}
	// 场上仍有大王未出 → 2 不安全
	remaining = map[card.Rank]int{card.Rank2: 1, card.RankBlackJoker: 0, card.RankRedJoker: 1}
	if isBigCardSafe(remaining, cards("2"), card.Rank2) {
		t.Fatal("大王未出时 2 不应绝对安全")
	}
	// 大王在自己手中 → 打 2 时自己的大王不算威胁
	remaining = map[card.Rank]int{card.Rank2: 1, card.RankBlackJoker: 0, card.RankRedJoker: 1}
	if !isBigCardSafe(remaining, cards("2 R"), card.Rank2) {
		t.Fatal("大王在己手时 2 应绝对安全")
	}
}
