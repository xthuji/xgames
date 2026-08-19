package bot

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/gomoku/rule"
)

// UserMoveSnapshot 真人落子决策快照（session 记录，本包分析器离线重建决策上下文用）
type UserMoveSnapshot struct {
	Board []int // 决策时的棋盘
	Color int   // 己方棋色
	Row   int   // 实际落子
	Col   int
}

// AnalyzeUserDecisions 离线复盘分析：对每个落子决策点用引擎求"当时最优落点"，
// 与玩家实际落子对比判定失误（漏杀、漏堵等）。
func AnalyzeUserDecisions(moves []replay.PlayerDecision) []replay.MoveAnalysis {
	engine := NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	analyses := make([]replay.MoveAnalysis, 0, len(moves))
	for _, m := range moves {
		snap, ok := m.Snapshot.(UserMoveSnapshot)
		if !ok || len(snap.Board) == 0 {
			continue
		}
		analyses = append(analyses, analyzeUserMove(ctx, engine, m, snap))
	}
	replay.SortAnalysesByRound(analyses)
	return analyses
}

// analyzeUserMove 分析单次落子
func analyzeUserMove(ctx context.Context, engine *Engine, m replay.PlayerDecision, snap UserMoveSnapshot) replay.MoveAnalysis {
	board := append([]int(nil), snap.Board...)
	bestRow, bestCol := engine.DecideMove(ctx, "复盘分析", board, snap.Color)

	sameMove := snap.Row == bestRow && snap.Col == bestCol
	a := replay.MoveAnalysis{
		Round:      m.Round,
		ActualDesc: fmt.Sprintf("(%d,%d) 落子", snap.Row, snap.Col),
		BestDesc:   fmt.Sprintf("(%d,%d) 落子", bestRow, bestCol),
	}

	opp := rule.Opposite(snap.Color)
	if sameMove {
		// 与引擎一致；该手为取胜/防守关键点时标记亮点
		switch {
		case inMoves(findWinningMoves(board, snap.Color), snap.Row, snap.Col):
			a.Highlight = true
			a.Reason = "直接成五取胜，把握住了制胜点"
		case inMoves(findWinningMoves(board, opp), snap.Row, snap.Col):
			a.Highlight = true
			a.Reason = "及时堵住对手的成五点，化解了败势"
		}
		return a
	}

	// 失误判定
	a.IsMistake = true
	myWins := findWinningMoves(board, snap.Color)
	oppWins := findWinningMoves(board, opp)
	switch {
	case len(myWins) > 0 && !inMoves(myWins, snap.Row, snap.Col):
		a.Severity = replay.SeverityCritical
		a.Reason = "已存在直接成五的取胜点，却没有落子取胜，错失良机"
	case len(oppWins) > 0 && !inMoves(oppWins, snap.Row, snap.Col):
		a.Severity = replay.SeverityCritical
		a.Reason = "对手下一步即可连五，必须先堵住对手的成五点"
	case vcfMissed(board, snap):
		a.Severity = replay.SeverityMajor
		a.Reason = "存在连续冲四（VCF）取胜路线，应优先按杀棋路线行棋"
	default:
		a.Severity = replay.SeverityMinor
		a.Reason = "与引擎推荐落点不同，攻防价值稍逊（引擎会优先进攻威胁点与防守要点）"
	}
	return a
}

// vcfMissed 存在 VCF 杀棋且实际落子不是 VCF 推荐点（复盘分析用全强度杀棋深度）
func vcfMissed(board []int, snap UserMoveSnapshot) bool {
	vcf := tryVCF(board, snap.Color, vcfMaxDepth)
	return vcf != nil && !(vcf.row == snap.Row && vcf.col == snap.Col)
}

// inMoves 落子是否在候选列表中
func inMoves(moves []move, row, col int) bool {
	for _, mv := range moves {
		if mv.row == row && mv.col == col {
			return true
		}
	}
	return false
}
