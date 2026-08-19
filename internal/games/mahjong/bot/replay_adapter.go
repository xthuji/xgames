package bot

import (
	"fmt"
	"time"
	"xgames/internal/games/bot/replay"
)

// MahjongReplayAdapter 麻将复盘适配器
type MahjongReplayAdapter struct {
	logger     *replay.DecisionLogger
	difficulty string // 机器人难度档位（easy/normal/hard）
}

// NewMahjongReplayAdapter 创建麻将复盘适配器
func NewMahjongReplayAdapter(gameID, difficulty string) *MahjongReplayAdapter {
	return &MahjongReplayAdapter{
		logger:     replay.NewDecisionLogger("mahjong", gameID),
		difficulty: difficulty,
	}
}

// MahjongMoveInfo 麻将着法信息
type MahjongMoveInfo struct {
	Tile         int    // 打出的牌（0-33）
	ActionType   string // discard/pong/kong/win
	ShantenAfter int    // 打出后的向听数
	Ukeire       int    // 进张数
	DangerLevel  int    // 危险度（0-100）
	IsTing       bool   // 是否听牌
	TingWaits    []int  // 听牌待牌
}

// RecordDecision 记录一次决策
func (a *MahjongReplayAdapter) RecordDecision(
	round int,
	playerName string,
	hand []int,
	melds []MeldInfo,
	move MahjongMoveInfo,
	candidates []MahjongCandidate,
	reasoning string,
	opponentModels []OpponentModel,
) {
	// 转换候选着法
	candidateInfos := make([]replay.CandidateInfo, len(candidates))
	for i, cand := range candidates {
		tileName := tileToString(cand.Tile)
		candidateInfos[i] = replay.CandidateInfo{
			Move: replay.MoveInfo{
				Description: tileName,
				Type:        cand.ActionType,
				Value:       cand.Score,
				RawData:     cand,
			},
			Score:    cand.Score,
			Reason:   fmt.Sprintf("向听数: %d, 进张: %d, 危险度: %d", cand.ShantenAfter, cand.Ukeire, cand.DangerLevel),
			Priority: len(candidates) - i,
		}
	}

	// 构建上下文
	contextInfo := replay.GameContextInfo{
		GameType:      "mahjong",
		IsFirstMove:   round == 1,
		OpponentCount: 3,
		RoundNumber:   round,
		Difficulty:    a.difficulty,
		CustomFields: map[string]any{
			"shantenAfter": move.ShantenAfter,
			"ukeire":       move.Ukeire,
			"isTing":       move.IsTing,
			"turnNumber":   round,
		},
	}

	// 生成自然语言解释
	fullReasoning := generateMahjongReasoning(move, reasoning, opponentModels, round)

	log := replay.DecisionLog{
		Timestamp:  time.Now().Unix(),
		Round:      round,
		PlayerName: playerName,
		Context:    contextInfo,
		Candidates: candidateInfos,
		Chosen: replay.MoveInfo{
			Description: tileToString(move.Tile),
			Type:        move.ActionType,
			Value:       calculateMahjongScore(move),
			RawData:     move,
		},
		Reasoning:    fullReasoning,
		StrengthDiff: calculateMahjongStrengthDiff(candidates, move),
		EvalScore:    calculateMahjongScore(move),
	}

	a.logger.Record(log)
}

// GenerateReport 生成复盘报告
func (a *MahjongReplayAdapter) GenerateReport(winner string, players []string) *replay.ReplayReport {
	return a.logger.GenerateReplayReport(winner, players)
}

// GetLogger 获取底层日志收集器
func (a *MahjongReplayAdapter) GetLogger() *replay.DecisionLogger {
	return a.logger
}

// MahjongCandidate 麻将候选着法
type MahjongCandidate struct {
	Tile         int
	ActionType   string
	ShantenAfter int
	Ukeire       int
	DangerLevel  int
	Score        float64
}

func calculateMahjongStrengthDiff(candidates []MahjongCandidate, chosen MahjongMoveInfo) float64 {
	if len(candidates) == 0 {
		return 0
	}

	bestScore := candidates[0].Score
	chosenScore := calculateMahjongScore(chosen)

	if bestScore == 0 {
		return 0
	}

	return chosenScore - bestScore
}
