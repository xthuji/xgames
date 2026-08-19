package bot

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"xgames/internal/games/bot/replay"
	"xgames/internal/games/chess/rule"
)

// UserMoveSnapshot 真人走子决策快照（session 记录，本包分析器离线重建决策上下文用）
type UserMoveSnapshot struct {
	Board    []int // 决策时的棋盘
	Camp     int   // 己方阵营
	FromRow  int   // 实际走子
	FromCol  int
	ToRow    int
	ToCol    int
}

// AnalyzeUserDecisions 离线复盘分析：对每个走子决策点用引擎搜索求"当时最优走法"，
// 与玩家实际走子对比判定失误。
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

// analyzeUserMove 分析单次走子
func analyzeUserMove(ctx context.Context, engine *Engine, m replay.PlayerDecision, snap UserMoveSnapshot) replay.MoveAnalysis {
	board := append([]int(nil), snap.Board...)
	bestFromRow, bestFromCol, bestToRow, bestToCol := engine.DecideMove(ctx, "复盘分析", board, snap.Camp)

	a := replay.MoveAnalysis{Round: m.Round, ActualDesc: m.ChosenDesc}
	sameMove := snap.FromRow == bestFromRow && snap.FromCol == bestFromCol &&
		snap.ToRow == bestToRow && snap.ToCol == bestToCol

	// 量化对比：分别推演实际/最优走法后的局面评估（从己方视角）
	evalActual := evalAfterMove(snap.Board, snap.Camp, snap.FromRow, snap.FromCol, snap.ToRow, snap.ToCol)
	evalBest := evalAfterMove(snap.Board, snap.Camp, bestFromRow, bestFromCol, bestToRow, bestToCol)
	a.ActualScore = float64(evalActual)
	a.BestScore = float64(evalBest)

	a.BestDesc = fmt.Sprintf("(%d,%d)→(%d,%d)", bestFromRow, bestFromCol, bestToRow, bestToCol)

	if sameMove {
		// 与引擎一致；收益显著（吃子/绝杀/局面大幅提升）时标记亮点
		if evalBest > evalBefore(snap.Board, snap.Camp)+150 {
			a.Highlight = true
			a.Reason = "与引擎推荐一致，局面收益显著"
		}
		return a
	}

	diff := evalBest - evalActual
	bestMate := moveDeliversMate(snap.Board, snap.Camp, bestFromRow, bestFromCol, bestToRow, bestToCol)
	actualMate := moveDeliversMate(snap.Board, snap.Camp, snap.FromRow, snap.FromCol, snap.ToRow, snap.ToCol)

	a.IsMistake = true
	switch {
	case bestMate && !actualMate:
		a.Severity = replay.SeverityCritical
	case diff >= 150:
		a.Severity = replay.SeverityCritical
	case diff >= 60:
		a.Severity = replay.SeverityMajor
	default:
		a.Severity = replay.SeverityMinor
	}

	var parts []string
	if bestMate && !actualMate {
		parts = append(parts, "引擎推荐走法可以直接将死对方，实际走法错失绝杀机会")
	}
	if diff >= 60 {
		parts = append(parts, fmt.Sprintf("该走法造成约 %d 分的子力/局面损失（约等于丢 %s）",
			diff, valueToPieceName(diff)))
	} else if diff > 10 {
		parts = append(parts, "该走法损失一定的位置分（机动性/兵形/将安全）")
	} else {
		parts = append(parts, "与最优走法效果接近，细节处理可以更好")
	}
	a.Reason = joinChessReason(parts)
	return a
}

// evalBefore 当前局面评估
func evalBefore(board []int, camp int) int {
	return evaluate(board, camp)
}

// evalAfterMove 推演走子后的局面评估（走法非法时返回原局面评估）
func evalAfterMove(board []int, camp, fromRow, fromCol, toRow, toCol int) int {
	b := append([]int(nil), board...)
	applyMove(b, fromRow, fromCol, toRow, toCol)
	return evaluate(b, camp)
}

// moveDeliversMate 走子后是否将死对方
func moveDeliversMate(board []int, camp, fromRow, fromCol, toRow, toCol int) bool {
	b := append([]int(nil), board...)
	if !applyMove(b, fromRow, fromCol, toRow, toCol) {
		return false
	}
	enemy := rule.CampRed + rule.CampBlack - camp
	return rule.IsCheckmate(b, enemy)
}

// applyMove 在棋盘上执行走子；非法时返回 false
func applyMove(board []int, fromRow, fromCol, toRow, toCol int) bool {
	if !rule.InBounds(fromRow, fromCol) || !rule.InBounds(toRow, toCol) {
		return false
	}
	piece := board[rule.Idx(fromRow, fromCol)]
	if piece == rule.Empty {
		return false
	}
	board[rule.Idx(toRow, toCol)] = piece
	board[rule.Idx(fromRow, fromCol)] = rule.Empty
	return true
}

// valueToPieceName 将分值损失换算成近似丢子描述
func valueToPieceName(diff int) string {
	switch {
	case diff >= 600:
		return "车"
	case diff >= 270:
		return "马/炮"
	case diff >= 110:
		return "士/象"
	case diff >= 30:
		return "兵/卒"
	default:
		return "小分"
	}
}

// joinChessReason 拼接原因说明
func joinChessReason(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "；"
		}
		out += p
	}
	return out
}
