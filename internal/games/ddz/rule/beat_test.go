package rule

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"xgames/internal/games/ddz/card"
)

// mustParseTestHand 解析测试牌组（解析失败直接 Panic）
func mustParseTestHand(cards []card.Card) ParsedHand {
	ph, err := ParseHand(cards)
	if err != nil || ph.Type == Invalid {
		panic("测试牌组解析失败")
	}
	return ph
}

// TestCanBeatWithHand_WindowEnd 顺子/连对/飞机比大小按窗口终点判定（F1 回归锚点）：
// 此前误用窗口起点比较，导致 45678 无法压 34567 的系统性漏判
func TestCanBeatWithHand_WindowEnd(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		hand   []card.Card
		target []card.Card
		want   bool
	}{
		{"顺子：45678 压 34567（需拆对）", testRuleCards(4, 4, 5, 5, 6, 6, 7, 7, 8), testRuleCards(3, 4, 5, 6, 7), true},
		{"顺子：34567 不能压 34567（终点相同）", testRuleCards(3, 3, 4, 4, 5, 5, 6, 6, 7), testRuleCards(3, 4, 5, 6, 7), false},
		{"顺子：连续点数窗口长度不足", testRuleCards(3, 3, 4, 4, 5, 5, 6, 6), testRuleCards(3, 4, 5, 6, 7), false},
		{"连对：445566 压 334455", testRuleCards(4, 4, 5, 5, 6, 6, 9), testRuleCards(3, 3, 4, 4, 5, 5), true},
		{"连对：334455 不能压 334455", testRuleCards(3, 3, 4, 4, 5, 5, 9), testRuleCards(3, 3, 4, 4, 5, 5), false},
		{"飞机：555666 压 333444", testRuleCards(5, 5, 5, 6, 6, 6, 9), testRuleCards(3, 3, 3, 4, 4, 4), true},
		{"飞机：333444 不能压 333444", testRuleCards(3, 3, 3, 4, 4, 4, 9), testRuleCards(3, 3, 3, 4, 4, 4), false},
		{"无同型无炸弹：三条带单压不了顺子", testRuleCards(9, 9, 9, 3), testRuleCards(3, 4, 5, 6, 7), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CanBeatWithHand(tc.hand, mustParseTestHand(tc.target)))
		})
	}
}

// TestFindWinningFourWithKickers 四带二/四带两对的主体与带牌校验（F2 补齐牌型覆盖）
func TestFindWinningFourWithKickers(t *testing.T) {
	t.Parallel()

	twoSingles := ParsedHand{Type: FourWithTwo, KeyRank: card.Rank4}
	twoPairs := ParsedHand{Type: FourWithTwoPairs, KeyRank: card.Rank4}

	// 带两张单牌：主体更大且剩牌 ≥2 即可（允许带一对，如 5555+33）
	assert.True(t, findWinningFourWithKickers(analyzeCards(testRuleCards(5, 5, 5, 5, 3, 3)), twoSingles, 1))
	// 带牌不足：只有四张主体，无牌可带
	assert.False(t, findWinningFourWithKickers(analyzeCards(testRuleCards(5, 5, 5, 5)), twoSingles, 1))
	// 主体不够大
	assert.False(t, findWinningFourWithKickers(analyzeCards(testRuleCards(3, 3, 3, 3, 5, 6)), twoSingles, 1))

	// 带两对：需要两个其他点数各能出一张对子
	assert.True(t, findWinningFourWithKickers(analyzeCards(testRuleCards(5, 5, 5, 5, 3, 3, 8, 8)), twoPairs, 2))
	// 只有一个对子可带
	assert.False(t, findWinningFourWithKickers(analyzeCards(testRuleCards(5, 5, 5, 5, 3, 3, 9)), twoPairs, 2))
	// 主体不够大
	assert.False(t, findWinningFourWithKickers(analyzeCards(testRuleCards(3, 3, 3, 3, 5, 5, 8, 8)), twoPairs, 2))
}
