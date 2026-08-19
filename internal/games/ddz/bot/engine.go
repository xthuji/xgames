package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// Engine 纯代码规则启发式决策引擎：无外部依赖、无需训练。
// 所有难度共用同一套出牌/叫分逻辑，难度差异只体现在记牌视图完整度：
// normal=仅跟踪关键牌（A/2/大小王） / hard=全量记牌。
// 任何难度下都保证：领出回合（MustPlay）必返回合法牌型，跟不动才 pass。
type Engine struct {
	logger *slog.Logger
	logger_collector *DecisionLogger // P1: 决策日志收集器（可选）
}

// NewEngine 创建规则启发式引擎
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{logger: logger}
}

// SetDecisionLogger P1: 设置决策日志收集器（用于复盘分析）
func (e *Engine) SetDecisionLogger(dl *DecisionLogger) {
	e.logger_collector = dl
}

// DecidePlay 决定出什么牌，返回 nil 表示 pass（仅跟牌回合允许）
// 决策为三级结构：P0 一手清空硬规则 > 残局 MCTS 接管（队友跟牌/死封豁免场景除外）> 规则引擎。
func (e *Engine) DecidePlay(_ context.Context, botName string, gctx GameContext) []card.Card {
	var winRate float64
	var chosen []card.Card

	// P0 硬规则：存在一步清空手牌的出法 → 直接打出。
	// 残局恰是该场景最高频的时机，必须先于 MCTS 检查，否则整手牌被拆散逐张出。
	if win := immediateWinPlay(gctx); win != nil {
		chosen = win
	} else if cards, wr := mctsEnhancedPlay(gctx); cards != nil {
		// 残局 MCTS 增强（覆盖规则引擎决策）
		e.logger.Info("bot MCTS 出牌", "bot", botName, "cards", cardsToStr(cards), "winRate", wr)
		chosen = cards
		winRate = wr
	} else {
		// 规则引擎决策
		chosen = unifiedPlay(gctx)
	}

	// P0: 应用故意犯错机制（仅领出回合，且非残局、非必须赢的场景）
	if chosen != nil && !gctx.MustPlay {
		// 跟牌回合不犯错（必须正确应对）
	} else if chosen != nil && gctx.Personality.MistakeProbability > 0 && winRate == 0 {
		// 生成候选列表用于次优替换（仅在未使用 MCTS 时）
		candidates := rule.GenerateHints(gctx.Hand, rule.ParsedHand{})
		chosen = intentionalImperfection(gctx.Personality, gctx.Hand, chosen, candidates, gctx)
	}

	// 领出回合兜底：禁止返回 nil（服务端会拒绝该动作导致回合超时）
	if chosen == nil && gctx.MustPlay {
		e.logger.Warn("领出决策为空，兜底出最小单张", "bot", botName)
		chosen = []card.Card{smallestCard(gctx.Hand)}
	}

	// P1: 记录决策日志（如果启用了收集器）
	if e.logger_collector != nil && chosen != nil {
		log := generateDecisionLog(botName, gctx, chosen, winRate)
		e.logger_collector.Record(log)
	}

	if chosen == nil {
		e.logger.Info("bot pass", "bot", botName)
	} else {
		e.logger.Info("bot 出牌", "bot", botName, "cards", cardsToStr(chosen))
	}
	return chosen
}

// --- 共用分析辅助 ---

// minOpponentCount 威胁最大的对手剩余牌数：农民只看地主，地主看两家农民的最小值
func minOpponentCount(gctx GameContext) int {
	up, down := gctx.PlayerCounts[0], gctx.PlayerCounts[1]
	if gctx.IsLandlord {
		if up < down {
			return up
		}
		return down
	}
	if gctx.UpIsLandlord {
		return up
	}
	if gctx.DownIsLandlord {
		return down
	}
	if up < down { // 地主未定的防御分支（正常流程不会发生）
		return up
	}
	return down
}

// lastPlayByTeammate 最近一手是否农民队友所出（自己与出牌者都不是地主）
func lastPlayByTeammate(gctx GameContext) bool {
	return !gctx.IsLandlord && !gctx.RecentPlays[0].IsLandlord
}

// isFarmerUpperSeat 自己是否地主上家（下家是地主）：职责是顶牌封堵
func isFarmerUpperSeat(gctx GameContext) bool {
	return !gctx.IsLandlord && gctx.DownIsLandlord
}

// isFarmerLowerSeat 自己是否地主下家（上家是地主）：职责是跑牌
func isFarmerLowerSeat(gctx GameContext) bool {
	return !gctx.IsLandlord && gctx.UpIsLandlord
}

// canFinishSoon 自己是否接近走完：出掉 cards 后剩余牌数 ≤1
func canFinishSoon(hand, cards []card.Card) bool {
	return len(hand)-len(cards) <= 1
}

// --- 通用小工具 ---

// smallestCard 手牌中最小的一张（按点数）
func smallestCard(hand []card.Card) card.Card {
	min := hand[0]
	for _, c := range hand[1:] {
		if c.Rank < min.Rank {
			min = c
		}
	}
	return min
}

// cardsToStr 将牌切片格式化为以空格分隔的牌面字符串（日志用）
func cardsToStr(cards []card.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Rank.String()
	}
	return strings.Join(parts, " ")
}

// generateDecisionLog P1: 生成决策日志
func generateDecisionLog(botName string, gctx GameContext, chosen []card.Card, winRate float64) DecisionLog {
	candidates := rule.GenerateHints(gctx.Hand, rule.ParsedHand{})
	
	log := DecisionLog{
		Timestamp:  time.Now().Unix(),
		PlayerName: botName,
		IsLandlord: gctx.IsLandlord,
		Hand:       append([]card.Card(nil), gctx.Hand...),
		Context:    gctx,
		Candidates: candidates,
		Chosen:     chosen,
		WinRate:    winRate,
	}
	
	// 生成解释文本
	log.Reasoning = explainDecision(gctx, chosen, candidates, winRate)
	
	// 计算强度差异
	if len(candidates) >= 2 {
		log.StrengthDiff = float64(candidates[0].Score - candidates[1].Score)
	}
	
	return log
}

// explainDecision 生成决策的自然语言解释
func explainDecision(gctx GameContext, chosen []card.Card, candidates []rule.Hint, winRate float64) string {
	var sb strings.Builder
	
	// 判断是否为残局 MCTS 决策
	if winRate > 0 {
		sb.WriteString(fmt.Sprintf("MCTS 残局分析，胜率 %.0f%%\n", winRate*100))
		return sb.String()
	}
	
	// 分析选择的牌型
	if ph, err := rule.ParseHand(chosen); err == nil {
		sb.WriteString(fmt.Sprintf("出 %s (%s)\n", ph.Type.String(), cardsToString(chosen)))
	}
	
	// 解释策略意图
	if gctx.MustPlay {
		// 领出回合
		if len(chosen) <= 2 {
			sb.WriteString("- 出小牌保留实力\n")
		} else if len(chosen) >= 5 {
			sb.WriteString("- 大批量出牌快速跑牌\n")
		}
	} else {
		// 跟牌回合
		sb.WriteString("- 跟上家牌型，不拆大牌\n")
	}
	
	// 农民配合提示
	if !gctx.IsLandlord && nextSeatIsTeammate(gctx) {
		sb.WriteString("- 队友接近走完，送牌权给队友\n")
	}
	
	return sb.String()
}
