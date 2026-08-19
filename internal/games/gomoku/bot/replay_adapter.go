package bot

import (
	"fmt"
	"time"
	"xgames/internal/games/bot/replay"
)

// GomokuReplayAdapter 五子棋复盘适配器
type GomokuReplayAdapter struct {
	logger *replay.DecisionLogger
}

// NewGomokuReplayAdapter 创建五子棋复盘适配器
func NewGomokuReplayAdapter(gameID string) *GomokuReplayAdapter {
	return &GomokuReplayAdapter{
		logger: replay.NewDecisionLogger("gomoku", gameID),
	}
}

// GomokuMoveInfo 五子棋着法信息
type GomokuMoveInfo struct {
	Row, Col      int
	Threats       []string // 形成的威胁（活三、冲四等）
	EvalScore     int      // 评估分数
	IsWinningMove bool     // 是否必胜着法
}

// RecordDecision 记录一次落子决策（difficulty/bookHit 来自引擎真实决策详情，
// 修复原先 Difficulty 硬编码 normal、深度/VCF/VCT 占位的缺陷）
func (a *GomokuReplayAdapter) RecordDecision(
	round int,
	playerName string,
	isBlack bool,
	board [15][15]int,
	move GomokuMoveInfo,
	candidates []GomokuCandidate,
	reasoning string,
	evalScore int,
	searchDepth int,
	vcfFound bool,
	vctFound bool,
	difficulty string,
	bookHit bool,
) {
	// 转换候选着法
	candidateInfos := make([]replay.CandidateInfo, len(candidates))
	for i, cand := range candidates {
		candidateInfos[i] = replay.CandidateInfo{
			Move: replay.MoveInfo{
				Description: positionToString(cand.Row, cand.Col),
				Type:        getGomokuMoveType(cand),
				Value:       float64(cand.Score),
				RawData:     cand,
			},
			Score:    float64(cand.Score),
			Reason:   fmt.Sprintf("评分: %d", cand.Score),
			Priority: len(candidates) - i,
		}
	}

	// 构建上下文
	contextInfo := replay.GameContextInfo{
		GameType:      "gomoku",
		IsFirstMove:   round == 1,
		OpponentCount: 1,
		RoundNumber:   round,
		Difficulty:    difficulty, // 真实难度档位（空值回退 normal）
		CustomFields: map[string]any{
			"isBlack":     isBlack,
			"searchDepth": searchDepth,
			"vcfFound":    vcfFound,
			"vctFound":    vctFound,
			"bookHit":     bookHit,
		},
	}

	// 生成自然语言解释
	fullReasoning := generateGomokuReasoning(move, isBlack, round, vcfFound, vctFound)

	log := replay.DecisionLog{
		Timestamp:  time.Now().Unix(),
		Round:      round,
		PlayerName: playerName,
		Context:    contextInfo,
		Candidates: candidateInfos,
		Chosen: replay.MoveInfo{
			Description: positionToString(move.Row, move.Col),
			Type:        getGomokuMoveTypeFromInfo(move),
			Value:       float64(move.EvalScore),
			RawData:     move,
		},
		Reasoning:    fullReasoning,
		StrengthDiff: calculateGomokuStrengthDiff(candidates, move),
		EvalScore:    float64(evalScore),
	}

	a.logger.Record(log)
}

// GenerateReport 生成复盘报告
func (a *GomokuReplayAdapter) GenerateReport(winner string, players []string) *replay.ReplayReport {
	return a.logger.GenerateReplayReport(winner, players)
}

// GetLogger 获取底层日志收集器
func (a *GomokuReplayAdapter) GetLogger() *replay.DecisionLogger {
	return a.logger
}

// GomokuCandidate 五子棋候选着法
type GomokuCandidate struct {
	Row, Col int
	Score    int
	Threats  []string
}

// Helper functions

func positionToString(row, col int) string {
	// 使用坐标表示，如 (7,7)
	return fmt.Sprintf("(%d,%d)", row, col)
}

func getGomokuMoveType(cand GomokuCandidate) string {
	if len(cand.Threats) > 0 {
		if contains(cand.Threats, "成五") {
			return "winning"
		}
		if contains(cand.Threats, "活四") {
			return "live_four"
		}
		if contains(cand.Threats, "冲四") {
			return "rush_four"
		}
		if contains(cand.Threats, "活三") {
			return "live_three"
		}
	}
	return "normal"
}

func getGomokuMoveTypeFromInfo(info GomokuMoveInfo) string {
	if info.IsWinningMove {
		return "winning"
	}
	if len(info.Threats) > 0 {
		if contains(info.Threats, "活四") {
			return "live_four"
		}
		if contains(info.Threats, "冲四") {
			return "rush_four"
		}
		if contains(info.Threats, "活三") {
			return "live_three"
		}
	}
	return "normal"
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func calculateGomokuStrengthDiff(candidates []GomokuCandidate, chosen GomokuMoveInfo) float64 {
	if len(candidates) == 0 {
		return 0
	}
	
	bestScore := candidates[0].Score
	chosenScore := chosen.EvalScore
	
	if bestScore == 0 {
		return 0
	}
	
	return float64(chosenScore - bestScore)
}
