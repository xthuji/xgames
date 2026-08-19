package bot

import (
	"context"
	"testing"

	"xgames/internal/games/chess/rule"
)

// drawMaterialBoard 双方仅剩帅仕相（无车马炮兵卒）的最简和棋局面
func drawMaterialBoard() []int {
	b := make([]int, rule.Rows*rule.Cols)
	b[rule.Idx(9, 4)] = rule.RedGeneral
	b[rule.Idx(9, 3)] = rule.RedAdvisor
	b[rule.Idx(9, 2)] = rule.RedElephant
	b[rule.Idx(0, 4)] = rule.BlackGeneral
	b[rule.Idx(0, 3)] = rule.BlackAdvisor
	b[rule.Idx(0, 2)] = rule.BlackElephant
	return b
}

// TestEvaluateInsufficientMaterial 简单和棋局面评估为 0（任何视角都无法取胜）
func TestEvaluateInsufficientMaterial(t *testing.T) {
	b := drawMaterialBoard()
	if s := evaluate(b, rule.CampRed); s != 0 {
		t.Fatalf("evaluate(红视角) = %d, want 0", s)
	}
	if s := evaluate(b, rule.CampBlack); s != 0 {
		t.Fatalf("evaluate(黑视角) = %d, want 0", s)
	}
	// 对照：有攻子且非对称时不为 0（评估未被误伤）
	b2 := drawMaterialBoard()
	b2[rule.Idx(5, 0)] = rule.RedChariot
	if s := evaluate(b2, rule.CampRed); s == 0 {
		t.Fatal("红方多车时 evaluate 不应为 0")
	}
}

// stalemateBoard 困毙局面：黑将被红车封锁（(0,3)/(0,5) 受车控制，(1,4) 仕被拴住），
// 黑方无合法走法且未被将军 → 中国象棋困毙判负。
func stalemateBoard() []int {
	b := make([]int, rule.Rows*rule.Cols)
	b[rule.Idx(9, 4)] = rule.RedGeneral
	b[rule.Idx(1, 3)] = rule.RedChariot // 控制 (0,3)
	b[rule.Idx(2, 4)] = rule.RedChariot // 同列被黑仕遮挡，不构成将军
	b[rule.Idx(1, 5)] = rule.RedChariot // 控制 (0,5)
	b[rule.Idx(0, 4)] = rule.BlackGeneral
	b[rule.Idx(1, 4)] = rule.BlackAdvisor // 被 (2,4) 车拴住（挪开即露将）
	return b
}

// TestNegamaxStalemateLoses 困毙评分修复：无合法走法且未被将军 → 深负分（不再当和棋 0 分）
func TestNegamaxStalemateLoses(t *testing.T) {
	b := stalemateBoard()
	if !rule.IsStalemate(b, rule.CampBlack) {
		t.Fatal("测试局面应为黑方困毙")
	}
	if rule.IsInCheck(b, rule.CampBlack) {
		t.Fatal("困毙局面不应被将军")
	}
	tt := newChessTranspositionTable()
	score := negamax(b, 1, -1<<30, 1<<30, rule.CampBlack,
		zobristHash(b, rule.CampBlack), tt, newHistoryTable(), newKillerTable(), nil)
	if score > -80000 {
		t.Fatalf("困毙应返回深负分, got %d", score)
	}
}

// repetitionSetup 红方仅有帅仕 vs 黑方车（黑大优）。
// 红帅唯一有意义的逃步 (9,3)→(8,3) 通向的局面 Q 已在实战中出现两次：
// 走它即第三次重复 → 搜索内计 0 分，显著优于其他必败着法（负分）。
func repetitionSetup() (pos []int, qAfterRedMove []int, qHash uint64) {
	pos = make([]int, rule.Rows*rule.Cols)
	pos[rule.Idx(9, 3)] = rule.RedGeneral
	pos[rule.Idx(9, 4)] = rule.RedAdvisor
	pos[rule.Idx(9, 5)] = rule.RedAdvisor
	pos[rule.Idx(0, 4)] = rule.BlackGeneral
	pos[rule.Idx(9, 8)] = rule.BlackChariot // 第 9 行受红仕 (9,5) 阻挡，不构成将军

	qAfterRedMove = rule.CopyBoard(pos)
	qAfterRedMove[rule.Idx(9, 3)] = rule.Empty
	qAfterRedMove[rule.Idx(8, 3)] = rule.RedGeneral
	qHash = zobristHash(qAfterRedMove, rule.CampBlack)
	return pos, qAfterRedMove, qHash
}

// TestLosingBotSeeksRepetitionDraw 劣势求和：实战已两次重复的局面在搜索内计 0 分，
// 劣势方引擎应选择通往第三次重复的着法（0 分）而非其他必败着法。
func TestLosingBotSeeksRepetitionDraw(t *testing.T) {
	eng := NewEngine(nil)
	pos, _, qHash := repetitionSetup()

	detail := eng.DecideMoveWithContext(context.Background(), "t", pos, rule.CampRed,
		DifficultyConfig{}, GameContext{History: []uint64{qHash, qHash}})

	if detail.FromRow != 9 || detail.FromCol != 3 || detail.ToRow != 8 || detail.ToCol != 3 {
		t.Fatalf("劣势方应走向三次重复 (9,3)→(8,3), got (%d,%d)→(%d,%d) score=%d",
			detail.FromRow, detail.FromCol, detail.ToRow, detail.ToCol, detail.Score)
	}
	if detail.Score != 0 {
		t.Fatalf("重复局面着法评分应为 0, got %d", detail.Score)
	}
}

// TestContemptPrefersCapture 求和倾向：限着计数逼近判和线时，
// 优势方根选择应切换到 margin 内分值最高的吃子候选。
func TestContemptPrefersCapture(t *testing.T) {
	board := rule.NewBoard()
	board[rule.Idx(5, 4)] = rule.RedChariot
	board[rule.Idx(0, 0)] = rule.BlackChariot // 黑车供吃（与帅同列隔空）

	results := []ScoredMove{
		{9, 0, 8, 0, 120}, // 非吃子，最优
		{5, 4, 0, 4, 110}, // 吃黑车，margin 内
		{5, 4, 0, 8, -100}, // 吃子但差距 220 > margin 200
	}
	m, s, ok := bestCaptureMove(results, board, 120)
	if !ok {
		t.Fatal("应找到 margin 内的吃子候选")
	}
	if m.FromRow != 5 || m.FromCol != 4 || m.ToRow != 0 || m.ToCol != 4 {
		t.Fatalf("应选吃黑车的候选, got (%d,%d)→(%d,%d)", m.FromRow, m.FromCol, m.ToRow, m.ToCol)
	}
	if s != 110 {
		t.Fatalf("吃子候选分值 = %d, want 110", s)
	}

	// 全部非吃子 → 不切换
	if _, _, ok := bestCaptureMove(results[:1], board, 120); ok {
		t.Fatal("无非吃子候选时应返回 false")
	}
	// 吃子差距超 margin → 不切换
	if _, _, ok := bestCaptureMove(results[2:], board, 120); ok {
		t.Fatal("差距超 margin 的吃子候选不应被接受")
	}
}
