package bot

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/chess/rule"
)

// minimalBoard 最小对局局面：红帅 (9,4)、红车 (5,0)；黑将 (0,4)、黑车 (5,8)
func minimalBoard() []int {
	b := make([]int, rule.Cols*rule.Rows)
	b[rule.Idx(9, 4)] = rule.RedGeneral
	b[rule.Idx(5, 0)] = rule.RedChariot
	b[rule.Idx(0, 4)] = rule.BlackGeneral
	b[rule.Idx(5, 8)] = rule.BlackChariot
	return b
}

func chessDecision(snap UserMoveSnapshot) []replay.PlayerDecision {
	return []replay.PlayerDecision{{
		Round:      1,
		PlayerID:   "p1",
		PlayerName: "玩家一",
		ChosenDesc: "走子",
		Snapshot:   snap,
	}}
}

// 引擎可正常分析任意合法快照，输出结构完整
func TestAnalyzeUserMove_BasicOutput(t *testing.T) {
	board := rule.NewBoard()
	// 任选一个合法开局走子（红炮二平五式走法只要合法即可）
	moves := rule.AllLegalMoves(board, rule.CampRed)
	if len(moves) == 0 {
		t.Fatal("开局应有合法走法")
	}
	m := moves[0]
	snap := UserMoveSnapshot{Board: board, Camp: rule.CampRed,
		FromRow: m.FromRow, FromCol: m.FromCol, ToRow: m.ToRow, ToCol: m.ToCol}

	analyses := AnalyzeUserDecisions(chessDecision(snap))
	if len(analyses) != 1 {
		t.Fatalf("analyses len = %d, want 1", len(analyses))
	}
	a := analyses[0]
	if a.BestDesc == "" {
		t.Error("BestDesc 不应为空")
	}
}

// 吃车走法的局面评估应显著高于不吃（evaluate 确定性，量化评分方向正确）
func TestAnalyzeUserMove_CaptureGain(t *testing.T) {
	board := minimalBoard()
	// 吃车：红车 (5,0)→(5,8)；不吃：红车 (6,0)
	capture := UserMoveSnapshot{Board: board, Camp: rule.CampRed, FromRow: 5, FromCol: 0, ToRow: 5, ToCol: 8}
	quiet := UserMoveSnapshot{Board: board, Camp: rule.CampRed, FromRow: 5, FromCol: 0, ToRow: 6, ToCol: 0}

	ac := AnalyzeUserDecisions(chessDecision(capture))[0]
	aq := AnalyzeUserDecisions(chessDecision(quiet))[0]

	if ac.ActualScore-aq.ActualScore < 300 {
		t.Errorf("吃车走法评分 %v 应比不吃 %v 高至少 300", ac.ActualScore, aq.ActualScore)
	}
}

// 偏离引擎搜索的最优解（评分差距大）→ 判失误；引擎对等分走法的选择可能受并发调度影响，
// 因此不断言具体 BestDesc 与严重度，只验证失误判定路径。
func TestAnalyzeUserMove_OffEngine(t *testing.T) {
	board := minimalBoard()
	engine := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	bfr, bfc, btr, btc := engine.DecideMove(context.Background(), "测试", board, rule.CampRed)

	// 黑车在最右边，红车送吃（走进黑车攻击线且无保护）必劣于引擎最优
	snap := UserMoveSnapshot{Board: board, Camp: rule.CampRed, FromRow: 5, FromCol: 0, ToRow: 5, ToCol: 7}

	a := AnalyzeUserDecisions(chessDecision(snap))[0]
	if a.BestDesc == "" {
		t.Fatal("BestDesc 不应为空")
	}
	// 送吃走法与最优不一致时必判失误（最优只会是吃黑车或保守走子，评分均高于白丢车）
	if snap.FromRow == bfr && snap.FromCol == bfc && snap.ToRow == btr && snap.ToCol == btc {
		t.Skip("引擎恰好推荐了送吃走法，跳过")
	}
	if !a.IsMistake {
		t.Errorf("送吃黑车口应判失误, got %+v", a)
	}
	if a.Reason == "" {
		t.Error("失误原因不应为空")
	}
}

// 非法快照应跳过
func TestAnalyzeUserDecisions_InvalidSnapshot(t *testing.T) {
	analyses := AnalyzeUserDecisions([]replay.PlayerDecision{{Round: 1, Snapshot: "bad"}})
	if len(analyses) != 0 {
		t.Errorf("非法快照应被跳过, got %d 条", len(analyses))
	}
}
