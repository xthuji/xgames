package rule

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"xgames/internal/games/ddz/card"
)

// TestPickKickerByCount_Single 三带一：带牌应为最小面值且不拆结构
func TestPickKickerByCount_Single(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hand     []card.Rank // 前 3 张为三张主体
		expected card.Rank   // 期望带出的单张点数
	}{
		{"优先现成单张（最小面值）", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.RankK, card.RankK, card.Rank3}, card.Rank3},
		{"无单张时拆最小对子", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.RankK, card.RankK, card.Rank5, card.Rank5}, card.Rank5},
		{"单张比对子小也只拆对子兜底时取最小", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.Rank9, card.Rank9, card.Rank5}, card.Rank5},
		{"只剩炸弹时兜底拆炸弹", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.Rank4, card.Rank4, card.Rank4, card.Rank4}, card.Rank4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hand := testRuleCards(tc.hand...)
			k := pickKickerByCount(hand, hand[:3], 1)
			require.Len(t, k, 1)
			assert.Equal(t, tc.expected, k[0].Rank)
		})
	}
}

func TestPickKickerByCount_Pair(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hand     []card.Rank // 前 3 张为三张主体
		expected card.Rank   // 期望带出的对子点数
	}{
		{"优先最小现成对子", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.RankK, card.RankK, card.Rank4, card.Rank4}, card.Rank4},
		{"不拆炸弹（有现成对子时）", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.Rank9, card.Rank9, card.Rank9, card.Rank9, card.Rank5, card.Rank5}, card.Rank5},
		{"无现成对子时拆最小三张", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.Rank6, card.Rank6, card.Rank6, card.RankJ, card.RankJ, card.RankJ}, card.Rank6},
		{"只剩炸弹时兜底拆炸弹", []card.Rank{card.Rank8, card.Rank8, card.Rank8, card.Rank4, card.Rank4, card.Rank4, card.Rank4}, card.Rank4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hand := testRuleCards(tc.hand...)
			k := pickKickerByCount(hand, hand[:3], 2)
			require.Len(t, k, 2)
			assert.Equal(t, tc.expected, k[0].Rank)
			assert.Equal(t, tc.expected, k[1].Rank)
		})
	}
}

// findHintByType 从提示集中找第一个指定牌型的候选
func findHintByType(t *testing.T, hints []Hint, want HandType) Hint {
	t.Helper()
	for _, h := range hints {
		ph, err := ParseHand(h.Cards)
		require.NoError(t, err)
		if ph.Type == want {
			return h
		}
	}
	t.Fatalf("提示集中未找到牌型 %v", want)
	return Hint{}
}

// TestGenerateHints_TrioKickers 三带一/三带二候选的带牌应为最小且不拆结构
func TestGenerateHints_TrioKickers(t *testing.T) {
	t.Parallel()

	// 领出：888 + KK + 3 → 三带一 888+3，三带二 888+KK
	hand := testRuleCards(card.Rank8, card.Rank8, card.Rank8, card.RankK, card.RankK, card.Rank3)
	hints := GenerateHints(hand, ParsedHand{})

	h1 := findHintByType(t, hints, TrioWithSingle)
	assert.Equal(t, card.Rank3, h1.Cards[3].Rank, "三带一应带最小单张 3 而非 K")

	h2 := findHintByType(t, hints, TrioWithPair)
	assert.Equal(t, card.RankK, h2.Cards[3].Rank, "三带二应带对 KK")
	assert.Equal(t, card.RankK, h2.Cards[4].Rank)

	// 跟牌：压 333+4，手牌 555 + J + 6 → 应出 555+6（带最小单张 6，不带 J）
	target, err := ParseHand(testRuleCards(card.Rank3, card.Rank3, card.Rank3, card.Rank4))
	require.NoError(t, err)
	hand2 := testRuleCards(card.Rank5, card.Rank5, card.Rank5, card.RankJ, card.Rank6)
	follows := GenerateHints(hand2, target)

	hf := findHintByType(t, follows, TrioWithSingle)
	assert.Equal(t, card.Rank5, hf.Cards[0].Rank, "主体应为最小可压三条 555")
	assert.Equal(t, card.Rank6, hf.Cards[3].Rank, "带牌应为最小单张 6 而非 J")
}
