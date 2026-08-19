package bot

import (
	"testing"

	"xgames/internal/games/chess/rule"
)

// TestOpeningBookLinesLegal 开局库线路合法性回归：每条线路的每一步着法必须在对应局面下
// 合法。背景：buildFromLines 曾对非法着法照常 ApplyMove，非法着法被注册进开局库后，
// 精确局面命中时引擎直接返回非法走子（自战模拟第 9 手即复现）。数据层修复后以此测试
// 防止新增线路再次引入非法着法。
func TestOpeningBookLinesLegal(t *testing.T) {
	for li, line := range openingLines {
		board := rule.NewBoard()
		camp := rule.CampRed
		for mi, m := range line.moves {
			legal := false
			for _, lm := range rule.AllLegalMoves(board, camp) {
				// 仅比坐标：AllLegalMoves 生成吃子着法时填充 Captured，线路着法不带该字段
				if lm.FromRow == m.FromRow && lm.FromCol == m.FromCol && lm.ToRow == m.ToRow && lm.ToCol == m.ToCol {
					legal = true
					break
				}
			}
			if !legal {
				t.Errorf("开局线路 #%d 第 %d 手非法: (%d,%d)→(%d,%d)",
					li, mi, m.FromRow, m.FromCol, m.ToRow, m.ToCol)
				break
			}
			rule.ApplyMove(board, m)
			camp = rule.CampRed + rule.CampBlack - camp
		}
	}
}
