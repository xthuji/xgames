package bot

import (
	"fmt"
	"strings"
	"time"
	"xgames/internal/games/bot/replay"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// DDZReplayAdapter 斗地主复盘适配器
type DDZReplayAdapter struct {
	logger *replay.DecisionLogger
}

// NewDDZReplayAdapter 创建斗地主复盘适配器
func NewDDZReplayAdapter(gameID string) *DDZReplayAdapter {
	return &DDZReplayAdapter{
		logger: replay.NewDecisionLogger("ddz", gameID),
	}
}

// RecordDecision 记录一次出牌决策
func (a *DDZReplayAdapter) RecordDecision(
	round int,
	playerName string,
	isLandlord bool,
	hand []card.Card,
	ctx GameContext,
	candidates []rule.Hint,
	chosen []card.Card,
	reasoning string,
) {
	// 转换候选着法
	candidateInfos := make([]replay.CandidateInfo, len(candidates))
	for i, hint := range candidates {
		candidateInfos[i] = replay.CandidateInfo{
			Move: replay.MoveInfo{
				Description: cardsToDisplayString(hint.Cards),
				Type:        getPlayType(hint.Cards),
				Value:       float64(hint.Score),
				RawData:     hint,
			},
			Score:    float64(hint.Score),
			Reason:   fmt.Sprintf("评分: %d", hint.Score),
			Priority: len(candidates) - i,
		}
	}

	// 找到实际选择的候选分数
	chosenScore := findChosenScoreFromHints(candidates, chosen)

	// 构建上下文
	contextInfo := replay.GameContextInfo{
		GameType:      "ddz",
		IsFirstMove:   ctx.MustPlay,
		OpponentCount: 2,
		RoundNumber:   round,
		Difficulty:    ctx.Personality.Name,
		CustomFields: map[string]any{
			"isLandlord":     isLandlord,
			"playerCounts":   ctx.PlayerCounts,
			"unbeatenStreak": ctx.UnbeatenStreak,
		},
	}

	// 生成自然语言解释
	fullReasoning := generateDDZReasoning(ctx, chosen, reasoning, isLandlord)

	log := replay.DecisionLog{
		Timestamp:  time.Now().Unix(),
		Round:      round,
		PlayerName: playerName,
		Context:    contextInfo,
		Candidates: candidateInfos,
		Chosen: replay.MoveInfo{
			Description: cardsToDisplayString(chosen),
			Type:        getPlayType(chosen),
			Value:       float64(chosenScore),
			RawData:     chosen,
		},
		Reasoning:    fullReasoning,
		StrengthDiff: calculateStrengthDiffFromHints(candidates, chosenScore),
		EvalScore:    float64(chosenScore),
	}

	a.logger.Record(log)
}

// GenerateReport 生成复盘报告
func (a *DDZReplayAdapter) GenerateReport(winner string, players []string) *replay.ReplayReport {
	return a.logger.GenerateReplayReport(winner, players)
}

// GetLogger 获取底层日志收集器
func (a *DDZReplayAdapter) GetLogger() *replay.DecisionLogger {
	return a.logger
}

// Helper functions

func cardsToDisplayString(cards []card.Card) string {
	if len(cards) == 0 {
		return "过牌"
	}
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = cardToDisplayString(c)
	}
	return strings.Join(parts, "")
}

func cardToDisplayString(c card.Card) string {
	rankMap := map[card.Rank]string{
		card.Rank3:          "3",
		card.Rank4:          "4",
		card.Rank5:          "5",
		card.Rank6:          "6",
		card.Rank7:          "7",
		card.Rank8:          "8",
		card.Rank9:          "9",
		card.Rank10:         "10",
		card.RankJ:          "J",
		card.RankQ:          "Q",
		card.RankK:          "K",
		card.RankA:          "A",
		card.Rank2:          "2",
		card.RankBlackJoker: "小王",
		card.RankRedJoker:   "大王",
	}

	suitMap := map[card.Suit]string{
		card.Spade:   "♠",
		card.Heart:   "♥",
		card.Club:    "♣",
		card.Diamond: "♦",
		card.Joker:   "",
	}

	if c.Rank == card.RankBlackJoker || c.Rank == card.RankRedJoker {
		return rankMap[c.Rank]
	}

	suit := suitMap[c.Suit]
	rank := rankMap[c.Rank]
	return suit + rank
}

func getPlayType(cards []card.Card) string {
	if len(cards) == 0 {
		return "pass"
	}

	ph, err := rule.ParseHand(cards)
	if err != nil {
		return "unknown"
	}

	switch ph.Type {
	case rule.Single:
		return "single"
	case rule.Pair:
		return "pair"
	case rule.Trio:
		return "trio"
	case rule.Bomb:
		return "bomb"
	case rule.Rocket:
		return "rocket"
	case rule.Straight:
		return "straight"
	default:
		return "combo"
	}
}

func findChosenScoreFromHints(candidates []rule.Hint, chosen []card.Card) int {
	for _, hint := range candidates {
		if hintsCardsEqual(hint.Cards, chosen) {
			return hint.Score
		}
	}
	return 0
}

func hintsCardsEqual(a, b []card.Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func calculateStrengthDiffFromHints(candidates []rule.Hint, chosenScore int) float64 {
	if len(candidates) == 0 || chosenScore == 0 {
		return 0
	}

	bestScore := candidates[0].Score
	if bestScore == 0 {
		return 0
	}

	return float64(chosenScore - bestScore)
}

func generateDDZReasoning(ctx GameContext, chosen []card.Card, baseReasoning string, isLandlord bool) string {
	var sb strings.Builder

	// 基础推理
	if baseReasoning != "" {
		sb.WriteString(baseReasoning)
		sb.WriteString("\n")
	}

	// 添加局势分析
	if ctx.MustPlay {
		sb.WriteString("- 领出回合，需要主动出击\n")
	} else {
		sb.WriteString("- 跟牌回合，根据上家出牌调整策略\n")
	}

	// 角色分析
	if isLandlord {
		sb.WriteString("- 作为地主，需要快速跑牌\n")
	} else {
		if ctx.UpIsLandlord {
			sb.WriteString("- 作为地主下家（跑牌位），优先送走小牌\n")
		} else if ctx.DownIsLandlord {
			sb.WriteString("- 作为地主上家（顶牌位），需要压制地主\n")
		}
	}

	// 残局提示
	if ctx.PlayerCounts[0] <= 3 || ctx.PlayerCounts[1] <= 3 {
		sb.WriteString("- 进入残局阶段，需谨慎决策\n")
	}

	// 连续让牌提示
	if ctx.UnbeatenStreak >= 2 {
		sb.WriteString(fmt.Sprintf("- 已连续让牌 %d 次，考虑拆牌夺权\n", ctx.UnbeatenStreak))
	}

	return sb.String()
}
