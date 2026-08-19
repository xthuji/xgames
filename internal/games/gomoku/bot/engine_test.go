package bot

import (
	"io"
	"log/slog"
	"testing"

	"xgames/internal/games/gomoku/rule"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func set(board []int, color int, coords ...[2]int) {
	for _, c := range coords {
		board[rule.Idx(c[0], c[1])] = color
	}
}

func TestEngine_EmptyBoardPlaysCenter(t *testing.T) {
	e := NewEngine(discardLogger())
	r, c := e.DecideMove(t.Context(), "BOT", rule.NewBoard(), rule.Black)
	mid := rule.Size / 2
	if r != mid || c != mid {
		t.Fatalf("空棋盘应下天元 (%d,%d)，实际 (%d,%d)", mid, mid, r, c)
	}
}

func TestEngine_TakesWinningMove(t *testing.T) {
	// 自己四连（活四），应补第五子成五
	board := rule.NewBoard()
	set(board, rule.Black, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7}, [2]int{7, 8})
	set(board, rule.White, [2]int{6, 5}, [2]int{6, 6})

	e := NewEngine(discardLogger())
	r, c := e.DecideMove(t.Context(), "BOT", board, rule.Black)
	if !(r == 7 && (c == 4 || c == 9)) {
		t.Fatalf("应补五连 (7,4)/(7,9)，实际 (%d,%d)", r, c)
	}
}

func TestEngine_BlocksOpponentFour(t *testing.T) {
	// 对手四连将成五，自己无进攻棋型 → 必须堵
	board := rule.NewBoard()
	set(board, rule.White, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7}, [2]int{7, 8})
	set(board, rule.Black, [2]int{5, 2}, [2]int{3, 10})

	e := NewEngine(discardLogger())
	r, c := e.DecideMove(t.Context(), "BOT", board, rule.Black)
	if !(r == 7 && (c == 4 || c == 9)) {
		t.Fatalf("应堵住对手五连 (7,4)/(7,9)，实际 (%d,%d)", r, c)
	}
}

func TestEngine_PrefersOwnWinOverBlock(t *testing.T) {
	// 双方各四连：优先自己成五
	board := rule.NewBoard()
	set(board, rule.Black, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7}, [2]int{7, 8})
	set(board, rule.White, [2]int{3, 3}, [2]int{3, 4}, [2]int{3, 5}, [2]int{3, 6})

	e := NewEngine(discardLogger())
	r, c := e.DecideMove(t.Context(), "BOT", board, rule.Black)
	if !(r == 7 && (c == 4 || c == 9)) {
		t.Fatalf("应优先自己成五，实际 (%d,%d)", r, c)
	}
}

func TestEngine_MoveAlwaysLegal(t *testing.T) {
	// 随机散子局面下决策结果必须合法且为空位
	board := rule.NewBoard()
	set(board, rule.Black, [2]int{7, 7}, [2]int{8, 8}, [2]int{6, 9})
	set(board, rule.White, [2]int{7, 8}, [2]int{9, 6})

	e := NewEngine(discardLogger())
	r, c := e.DecideMove(t.Context(), "BOT", board, rule.Black)
	if !rule.InBounds(r, c) {
		t.Fatalf("决策越界 (%d,%d)", r, c)
	}
	if board[rule.Idx(r, c)] != rule.Empty {
		t.Fatalf("决策落在已有棋子 (%d,%d)", r, c)
	}
}
