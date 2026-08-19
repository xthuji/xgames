package bot

import (
	"context"

	"xgames/internal/games"
)

// DecisionEngine 麻将决策引擎接口
type DecisionEngine interface {
	// DecideDiscard 出牌决策：从手牌中选一张打出（无记牌器视图的简化版）
	DecideDiscard(ctx context.Context, botName string, hand []int, melds []MeldInfo) int
	// DecideDiscardEnhanced 完整上下文出牌决策：记牌器分析 + 成型牌型保护 + 防守安全度
	DecideDiscardEnhanced(ctx DiscardContext) int
	// DecideDiscardWithErrors 带可控失误的出牌决策（难度系统）：按 DiscardContext.Difficulty
	// 的失误率以小概率改打次优候选，现物兜底硬规则不豁免
	DecideDiscardWithErrors(ctx DiscardContext) int
	// DecideAction 动作决策：是否碰/胡（meldCount 为已鸣面子数；
	// 难度配置影响碰牌安全牌余量阈值）
	DecideAction(ctx context.Context, botName string, hand []int, tile int, meldCount int, cfg DifficultyConfig) (pong bool, win bool)
	// TingGuard 听牌保护：听牌后不随意拆牌，按记牌成胡可能性与已听轮数延迟拆听
	TingGuard(hand []int, meldCount int, seen []int, roundsInTenpai int, suggested int) int
}

// MeldInfo 面子信息（传给引擎）
type MeldInfo struct {
	Tile int
	Type int // 0=碰
}

// BotResponder 平台机器人动作回调端口
type BotResponder = games.BotResponder
