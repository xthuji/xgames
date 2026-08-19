package bot

import (
	"context"
	"testing"

	"xgames/internal/games/gomoku/rule"
)

// deadBoard 构造双方均无连五可能的僵局棋盘（empties 指定保留空位的一维下标）。
// 底板按 (r+2c)%5 < 2 为黑、其余为白：4 个方向步长 (±1/±2) 均与 5 互质，
// 任意 5 连窗口覆盖全部模 5 余数，必含黑白两色；保留空位后，含空位的窗口
// 仍同时含双方棋子（各 ≥2 子），不含空位的窗口无空格 → 双方均无连五可能。
func deadBoard(empties ...int) []int {
	b := rule.NewBoard()
	emptySet := make(map[int]bool, len(empties))
	for _, e := range empties {
		emptySet[e] = true
	}
	for r := 0; r < rule.Size; r++ {
		for c := 0; c < rule.Size; c++ {
			idx := rule.Idx(r, c)
			if emptySet[idx] {
				continue
			}
			if (r+2*c)%5 < 2 {
				b[idx] = rule.Black
			} else {
				b[idx] = rule.White
			}
		}
	}
	return b
}

// TestNegamax_DeadBoardDrawScore 僵局/满盘终局：negamax 应返回 0（平局），
// 而非按残存棋型给出误导性分值（满盘时所有窗口无空位，IsDead 同样成立）。
func TestNegamax_DeadBoardDrawScore(t *testing.T) {
	for name, b := range map[string][]int{
		"满盘":   deadBoard(),
		"僵局留空": deadBoard(rule.Idx(0, 4)),
	} {
		if !rule.IsDead(b) {
			t.Fatalf("%s：构造的棋盘应判僵局", name)
		}
		tt := newTranspositionTable()
		h := hashBoard(b)
		for _, color := range []int{rule.Black, rule.White} {
			if got := negamax(b, 4, -1<<30, 1<<30, color, -1, -1, h, tt); got != 0 {
				t.Fatalf("%s：僵局 negamax 应返回 0，实际 %d（color=%d）", name, got, color)
			}
		}
	}
}

// TestDecideMoveDetailed_DeadBoardPlaysEmpty 僵局棋盘上决策链应短路：
// 直接落空位（Score=0），不进入开局库/杀棋/迭代加深搜索。
func TestDecideMoveDetailed_DeadBoardPlaysEmpty(t *testing.T) {
	eng := NewEngine(discardLogger())
	b := deadBoard(rule.Idx(0, 4), rule.Idx(7, 7))
	for _, color := range []int{rule.Black, rule.White} {
		detail := eng.DecideMoveDetailed(context.Background(), "sim", b, color, DifficultyFor("hard"))
		idx := rule.Idx(detail.Row, detail.Col)
		if b[idx] != rule.Empty {
			t.Fatalf("color=%d：应落在空位，实际 (%d,%d) 已有子 %d", color, detail.Row, detail.Col, b[idx])
		}
		if detail.Score != 0 {
			t.Fatalf("color=%d：僵局落子分数应为 0，实际 %d", color, detail.Score)
		}
		if len(detail.TopMoves) != 1 || detail.TopMoves[0].Row != detail.Row || detail.TopMoves[0].Col != detail.Col {
			t.Fatalf("color=%d：TopMoves 应仅含所选落点，实际 %+v", color, detail.TopMoves)
		}
	}
}
