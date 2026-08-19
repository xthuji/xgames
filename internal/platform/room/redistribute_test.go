package room

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"xgames/internal/infra/store"
)

// 3 人平摊 16 分：5/5/6（回归：旧实现按固定人数均摊且 total 逐轮递减，会分出 5/3/8）
func TestSplitEvenlyThree(t *testing.T) {
	humans := make([]store.MatchPlayerResult, 3)
	splitEvenly(humans, []int{0, 1, 2}, 16)
	assert.Equal(t, 5, humans[0].Score)
	assert.Equal(t, 5, humans[1].Score)
	assert.Equal(t, 6, humans[2].Score)
}

// 2 人平摊含余数：7 → 3/4，尾差落入最后一位
func TestSplitEvenlyRemainder(t *testing.T) {
	humans := make([]store.MatchPlayerResult, 2)
	splitEvenly(humans, []int{0, 1}, 7)
	assert.Equal(t, 3, humans[0].Score)
	assert.Equal(t, 4, humans[1].Score)
}

// 单一目标与指定下标：全部落到目标位，不影响其他玩家
func TestSplitEvenlySingleAndIdx(t *testing.T) {
	humans := make([]store.MatchPlayerResult, 3)
	splitEvenly(humans, []int{1}, 9)
	assert.Equal(t, 9, humans[1].Score)
	assert.Equal(t, 0, humans[0].Score)
	assert.Equal(t, 0, humans[2].Score)
}
