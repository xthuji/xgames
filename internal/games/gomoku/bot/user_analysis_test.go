package bot

import (
	"fmt"
	"testing"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/gomoku/rule"
)

// setStones 在棋盘上落子
func setStones(board []int, color int, cells ...[2]int) {
	for _, c := range cells {
		board[rule.Idx(c[0], c[1])] = color
	}
}

func decisionsOf(snap UserMoveSnapshot) []replay.PlayerDecision {
	return []replay.PlayerDecision{{
		Round:      1,
		PlayerID:   "p1",
		PlayerName: "玩家一",
		ChosenDesc: "落子",
		Snapshot:   snap,
	}}
}

// 已有成五点却漏杀 → 重大失误
func TestAnalyzeUserMove_MissedWin(t *testing.T) {
	board := make([]int, rule.Size*rule.Size)
	setStones(board, rule.Black, [2]int{7, 3}, [2]int{7, 4}, [2]int{7, 5}, [2]int{7, 6})

	wins := findWinningMoves(board, rule.Black)
	if len(wins) == 0 {
		t.Fatal("测试局面应存在成五点")
	}
	// 实际落子远离取胜点
	snap := UserMoveSnapshot{Board: board, Color: rule.Black, Row: 0, Col: 0}

	analyses := AnalyzeUserDecisions(decisionsOf(snap))
	if len(analyses) != 1 {
		t.Fatalf("analyses len = %d, want 1", len(analyses))
	}
	a := analyses[0]
	if !a.IsMistake {
		t.Fatalf("漏杀应判定为失误, got %+v", a)
	}
	if a.Severity != replay.SeverityCritical {
		t.Errorf("漏杀严重度 = %s, want %s", a.Severity, replay.SeverityCritical)
	}
	wantBest := fmt.Sprintf("(%d,%d) 落子", wins[0].row, wins[0].col)
	if a.BestDesc != wantBest {
		t.Errorf("BestDesc = %q, want 成五点 %q", a.BestDesc, wantBest)
	}
}

// 抓住成五点取胜 → 与引擎一致且为亮点
func TestAnalyzeUserMove_TakeWin(t *testing.T) {
	board := make([]int, rule.Size*rule.Size)
	setStones(board, rule.Black, [2]int{7, 3}, [2]int{7, 4}, [2]int{7, 5}, [2]int{7, 6})

	wins := findWinningMoves(board, rule.Black)
	snap := UserMoveSnapshot{Board: board, Color: rule.Black, Row: wins[0].row, Col: wins[0].col}

	a := AnalyzeUserDecisions(decisionsOf(snap))[0]
	if a.IsMistake {
		t.Errorf("抓住成五点不应判失误, got %+v", a)
	}
	if !a.Highlight {
		t.Errorf("成五取胜应标记亮点, got %+v", a)
	}
}

// 对手有成五点而不堵 → 重大失误
func TestAnalyzeUserMove_MissedBlock(t *testing.T) {
	board := make([]int, rule.Size*rule.Size)
	setStones(board, rule.White, [2]int{3, 3}, [2]int{3, 4}, [2]int{3, 5}, [2]int{3, 6})
	setStones(board, rule.Black, [2]int{10, 10})

	if wins := findWinningMoves(board, rule.White); len(wins) == 0 {
		t.Fatal("测试局面下白方应存在成五点")
	}
	snap := UserMoveSnapshot{Board: board, Color: rule.Black, Row: 0, Col: 0}

	a := AnalyzeUserDecisions(decisionsOf(snap))[0]
	if !a.IsMistake || a.Severity != replay.SeverityCritical {
		t.Errorf("该堵不堵应为重大失误, got %+v", a)
	}
}

// 非法快照应跳过
func TestAnalyzeUserDecisions_InvalidSnapshot(t *testing.T) {
	analyses := AnalyzeUserDecisions([]replay.PlayerDecision{{Round: 1, Snapshot: "bad"}})
	if len(analyses) != 0 {
		t.Errorf("非法快照应被跳过, got %d 条", len(analyses))
	}
}
