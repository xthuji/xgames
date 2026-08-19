package bot

import (
	"context"
	"fmt"
	"testing"

	"xgames/internal/games/chess/rule"
)

// --- 开局库 ---

func TestOpeningBookInitialRed(t *testing.T) {
	board := rule.NewBoard()
	m, ok := globalChessOpeningBook.Lookup(board, rule.CampRed)
	if !ok {
		t.Fatal("初始局面红方应命中开局库")
	}
	for _, lm := range rule.AllLegalMoves(board, rule.CampRed) {
		if lm == m {
			return
		}
	}
	t.Fatalf("开局库返回非法着法: %+v", m)
}

func TestOpeningBookReplyToCannonCenter(t *testing.T) {
	board := rule.NewBoard()
	rule.ApplyMove(board, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	m, ok := globalChessOpeningBook.Lookup(board, rule.CampBlack)
	if !ok {
		t.Fatal("中炮后黑方应命中开局库")
	}
	// P3 加权随机：黑方有多种应招（屏风马/顺炮/列炮/飞象/右马），验证返回合法着法
	for _, lm := range rule.AllLegalMoves(board, rule.CampBlack) {
		if lm == m {
			return
		}
	}
	t.Fatalf("开局库返回非法着法: %+v", m)
}

func TestOpeningBookNoFalseHit(t *testing.T) {
	// P3 更新：开局库扩充后，中炮+屏风马后红方有 book move（马二进三等）
	board := rule.NewBoard()
	rule.ApplyMove(board, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	rule.ApplyMove(board, rule.Move{FromRow: 0, FromCol: 7, ToRow: 2, ToCol: 6})
	m, ok := globalChessOpeningBook.Lookup(board, rule.CampRed)
	if !ok {
		t.Fatal("中炮+屏风马后红方应命中开局库（马二进三等）")
	}
	for _, lm := range rule.AllLegalMoves(board, rule.CampRed) {
		if lm == m {
			goto valid
		}
	}
	t.Fatalf("开局库返回非法着法: %+v", m)
valid:

	// 黑方在初始局面不应命中开局库（开局库只为红方首着服务）
	if _, ok := globalChessOpeningBook.Lookup(rule.NewBoard(), rule.CampBlack); ok {
		t.Fatal("黑方在初始局面不应命中开局库")
	}
}

// --- P3 开局库扩充测试 ---

func TestOpeningBookCoverage(t *testing.T) {
	// 验证开局库条目总数 ≥ 100
	total := 0
	for _, entries := range globalChessOpeningBook.entries {
		total += len(entries)
	}
	if total < 100 {
		t.Fatalf("开局库条目数 = %d，want >= 100", total)
	}

	// 验证红方首着覆盖多种开局
	board := rule.NewBoard()
	firstMoves := make(map[string]int)
	for i := 0; i < 200; i++ {
		m, ok := globalChessOpeningBook.Lookup(board, rule.CampRed)
		if !ok {
			t.Fatal("初始局面红方应命中开局库")
		}
		key := fmt.Sprintf("%d,%d→%d,%d", m.FromRow, m.FromCol, m.ToRow, m.ToCol)
		firstMoves[key]++
	}
	if len(firstMoves) < 5 {
		t.Fatalf("红方首着种类 = %d，want >= 5（中炮/飞相/仙人指路/过宫炮/起马）", len(firstMoves))
	}
}

func TestOpeningBookWeightedSelection(t *testing.T) {
	// 验证加权随机：中炮后黑方屏风马应招概率最高
	board := rule.NewBoard()
	rule.ApplyMove(board, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	counts := make(map[string]int)
	trials := 500
	for i := 0; i < trials; i++ {
		m, ok := globalChessOpeningBook.Lookup(board, rule.CampBlack)
		if !ok {
			t.Fatal("中炮后黑方应命中开局库")
		}
		key := fmt.Sprintf("%d,%d→%d,%d", m.FromRow, m.FromCol, m.ToRow, m.ToCol)
		counts[key]++
	}
	// 屏风马 (0,7)→(2,6) 应是最常见应招（权重 20+12+10+...）
	pingFengMa := counts["0,7→2,6"]
	if pingFengMa < trials/4 {
		t.Fatalf("屏风马应招比例 = %d/%d，过低（加权随机可能有误）", pingFengMa, trials)
	}
}

// --- 静态搜索 ---

func TestQuiescenceSeesRecapture(t *testing.T) {
	// 红车可吃黑卒，但黑车沿 4 列回收吃：弃车吃卒净亏 570，
	// 静态搜索应识别回收吃而选择 stand-pat（不吃）
	board := make([]int, rule.Rows*rule.Cols)
	board[rule.Idx(9, 4)] = rule.RedGeneral
	board[rule.Idx(7, 4)] = rule.RedChariot
	board[rule.Idx(0, 3)] = rule.BlackGeneral
	board[rule.Idx(0, 4)] = rule.BlackChariot
	board[rule.Idx(5, 4)] = rule.BlackSoldier

	standPat := evaluate(board, rule.CampRed)
	q := quiescence(board, rule.AllLegalMoves(board, rule.CampRed), -1<<30, 1<<30, rule.CampRed, quiescenceDepth, zobristHash(board, rule.CampRed), nil)
	if q != standPat {
		t.Fatalf("静态搜索未识别回收吃: q=%d, standPat=%d", q, standPat)
	}
}

func TestQuiescenceQuietPositionEqualsEvaluate(t *testing.T) {
	board := rule.NewBoard()
	q := quiescence(board, rule.AllLegalMoves(board, rule.CampRed), -1<<30, 1<<30, rule.CampRed, quiescenceDepth, zobristHash(board, rule.CampRed), nil)
	if want := evaluate(board, rule.CampRed); q != want {
		t.Fatalf("初始局面无吃子，静态搜索应等于静态评估: q=%d, want=%d", q, want)
	}
}

// --- 自对弈回归 ---

func TestEngineSelfPlayMovesAlwaysLegal(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过自对弈回归")
	}
	engine := NewEngine(nil)
	board := rule.NewBoard()
	camp := rule.CampRed
	for ply := 0; ply < 16; ply++ {
		moves := rule.AllLegalMoves(board, camp)
		if len(moves) == 0 {
			break
		}
		boardBefore := rule.CopyBoard(board)
		fr, fc, tr, tc := engine.DecideMove(context.Background(), "bot", board, camp)
		for i := range board {
			if board[i] != boardBefore[i] {
				t.Fatalf("board modified by DecideMove at ply %d (idx %d: %d→%d)", ply+1, i, boardBefore[i], board[i])
			}
		}
		m := rule.Move{FromRow: fr, FromCol: fc, ToRow: tr, ToCol: tc}
		legal := false
		for _, lm := range moves {
			if lm.FromRow == m.FromRow && lm.FromCol == m.FromCol && lm.ToRow == m.ToRow && lm.ToCol == m.ToCol {
				legal = true
				break
			}
		}
		if !legal {
			// Print ALL legal moves for debugging
			t.Logf("第 %d 手引擎走子非法: %+v", ply+1, m)
			t.Logf("AllLegalMoves 共 %d 个:", len(moves))
			for i, lm := range moves {
				t.Logf("  [%d] (%d,%d)→(%d,%d) cap=%d", i, lm.FromRow, lm.FromCol, lm.ToRow, lm.ToCol, lm.Captured)
			}
			// Also check pseudo moves from the piece at the from-square
			piece := board[rule.Idx(fr, fc)]
			t.Logf("Piece at (%d,%d) = %d (type=%d, camp=%d)", fr, fc, piece, rule.TypeOf(piece), rule.CampOf(piece))
			pseudo := rule.PseudoMoves(board, fr, fc)
			t.Logf("PseudoMoves from (%d,%d): %d moves", fr, fc, len(pseudo))
			for i, pm := range pseudo {
				t.Logf("  pseudo[%d] (%d,%d)→(%d,%d) cap=%d", i, pm.FromRow, pm.FromCol, pm.ToRow, pm.ToCol, pm.Captured)
			}
			t.Fatalf("非法走子详情见上")
		}
		rule.ApplyMove(board, m)
		camp = rule.CampRed + rule.CampBlack - camp
	}
}
