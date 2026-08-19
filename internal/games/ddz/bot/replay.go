package bot

import (
	"fmt"
	"strings"
	"time"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// DecisionLog P1: 单次决策日志记录
type DecisionLog struct {
	Timestamp    int64          // 决策时间戳
	Round        int            // 当前轮次
	PlayerName   string         // 机器人名称
	IsLandlord   bool           // 是否地主
	Hand         []card.Card    // 手牌快照
	Context      GameContext    // 决策上下文
	Candidates   []rule.Hint    // 所有候选
	Chosen       []card.Card    // 最终选择
	Reasoning    string         // 自然语言解释
	StrengthDiff float64        // 选择 vs 次优的强度差
	WinRate      float64        // MCTS 胜率（如启用）
}

// ReplayReport P1: 对局复盘报告
type ReplayReport struct {
	GameID         string             // 对局 ID
	Duration       time.Duration      // 对局时长
	Winner         string             // 获胜者
	Landlord       string             // 地主
	KeyMoments     []DecisionLog      // 关键决策点
	MissedOpportunities []string      // 错过的更优选择
	OpponentStyles map[string]string  // 对手风格总结
	Suggestions    []string           // 改进建议
}

// FormatDecisionLog 格式化单条决策日志为可读文本
func FormatDecisionLog(log *DecisionLog) string {
	var sb strings.Builder
	
	sb.WriteString(fmt.Sprintf("[第%d轮] ", log.Round))
	if log.IsLandlord {
		sb.WriteString("【地主】")
	} else {
		sb.WriteString("【农民】")
	}
	sb.WriteString(log.PlayerName)
	sb.WriteString(" 出牌: ")
	sb.WriteString(cardsToString(log.Chosen))
	sb.WriteString("\n")
	
	if log.Reasoning != "" {
		sb.WriteString("原因:\n")
		sb.WriteString(log.Reasoning)
		sb.WriteString("\n")
	}
	
	if log.WinRate > 0 {
		sb.WriteString(fmt.Sprintf("MCTS 胜率: %.1f%%\n", log.WinRate*100))
	}
	
	return sb.String()
}

// FormatReplayReport 格式化完整复盘报告
func FormatReplayReport(report *ReplayReport) string {
	var sb strings.Builder
	
	sb.WriteString("=== 斗地主对局复盘报告 ===\n\n")
	sb.WriteString(fmt.Sprintf("对局 ID: %s\n", report.GameID))
	sb.WriteString(fmt.Sprintf("时长: %v\n", report.Duration))
	sb.WriteString(fmt.Sprintf("地主: %s\n", report.Landlord))
	sb.WriteString(fmt.Sprintf("获胜者: %s\n\n", report.Winner))
	
	if len(report.KeyMoments) > 0 {
		sb.WriteString("--- 关键决策点 ---\n")
		for _, moment := range report.KeyMoments {
			sb.WriteString(FormatDecisionLog(&moment))
			sb.WriteString("\n")
		}
	}
	
	if len(report.MissedOpportunities) > 0 {
		sb.WriteString("--- 错过的机会 ---\n")
		for i, opp := range report.MissedOpportunities {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, opp))
		}
		sb.WriteString("\n")
	}
	
	if len(report.OpponentStyles) > 0 {
		sb.WriteString("--- 对手风格分析 ---\n")
		for name, style := range report.OpponentStyles {
			sb.WriteString(fmt.Sprintf("%s: %s\n", name, style))
		}
		sb.WriteString("\n")
	}
	
	if len(report.Suggestions) > 0 {
		sb.WriteString("--- 改进建议 ---\n")
		for i, sug := range report.Suggestions {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, sug))
		}
	}
	
	return sb.String()
}

// cardsToString 将卡牌转换为可读字符串
func cardsToString(cards []card.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Rank.String()
	}
	return strings.Join(parts, " ")
}

// DecisionLogger P1: 决策日志收集器
type DecisionLogger struct {
	logs []DecisionLog
}

// NewDecisionLogger 创建新的日志收集器
func NewDecisionLogger() *DecisionLogger {
	return &DecisionLogger{
		logs: make([]DecisionLog, 0),
	}
}

// Record 记录一次决策
func (dl *DecisionLogger) Record(log DecisionLog) {
	dl.logs = append(dl.logs, log)
}

// GetKeyMoments 获取关键决策点（过滤掉普通决策）
func (dl *DecisionLogger) GetKeyMoments() []DecisionLog {
	var moments []DecisionLog
	for _, log := range dl.logs {
		// 关键决策：使用炸弹、王炸、或改变局势的决策
		if isKeyDecision(log) {
			moments = append(moments, log)
		}
	}
	return moments
}

// isKeyDecision 判断是否为关键决策
func isKeyDecision(log DecisionLog) bool {
	// 简单启发式：手数减少 >=2 或使用特殊牌型
	if len(log.Chosen) >= 4 { // 可能是炸弹
		return true
	}
	if log.StrengthDiff > 2.0 { // 强度差异大
		return true
	}
	return false
}

// GenerateReplayReport P1: 从日志收集器生成完整复盘报告
func (dl *DecisionLogger) GenerateReplayReport(gameID string, winner string, landlord string, players []string) *ReplayReport {
	report := &ReplayReport{
		GameID:         gameID,
		Winner:         winner,
		Landlord:       landlord,
		KeyMoments:     dl.GetKeyMoments(),
		MissedOpportunities: detectMissedOpportunities(dl.logs),
		OpponentStyles: analyzeOpponentStyles(dl.logs, players),
		Suggestions:    generateSuggestions(dl.logs, winner),
	}
	
	// 计算对局时长（从第一条到最后一条日志的时间差）
	if len(dl.logs) >= 2 {
		firstTime := time.Unix(dl.logs[0].Timestamp, 0)
		lastTime := time.Unix(dl.logs[len(dl.logs)-1].Timestamp, 0)
		report.Duration = lastTime.Sub(firstTime)
	}
	
	return report
}

// detectMissedOpportunities 检测错过的更优选择
func detectMissedOpportunities(logs []DecisionLog) []string {
	var opportunities []string
	
	for i, log := range logs {
		// 检查是否有明显更好的候选被跳过
		if len(log.Candidates) >= 2 {
			bestScore := log.Candidates[0].Score
			chosenScore := -1
			
			// 找到实际选择的分数
			for _, c := range log.Candidates {
				if cardsEqual(c.Cards, log.Chosen) {
					chosenScore = c.Score
					break
				}
			}
			
			// 如果选择的分数明显低于最优（差距 >20%）
			if chosenScore > 0 && bestScore > 0 && float64(bestScore-chosenScore) > float64(bestScore)*0.2 {
				opportunities = append(opportunities, 
					fmt.Sprintf("第%d轮: 有更优出牌选择（分数 %d vs %d）", 
						log.Round, bestScore, chosenScore))
				
				// 限制输出数量
				if len(opportunities) >= 5 {
					return opportunities
				}
			}
		}
		
		_ = i // 避免未使用警告
	}
	
	return opportunities
}

// analyzeOpponentStyles 分析对手风格
func analyzeOpponentStyles(logs []DecisionLog, players []string) map[string]string {
	styles := make(map[string]string)
	
	// 统计每个玩家的出牌特征
	playerStats := make(map[string]struct {
		totalPlays int
		bombPlays  int
		passCount  int
		avgCards   float64
	})
	
	for _, log := range logs {
		name := log.PlayerName
		stats := playerStats[name]
		stats.totalPlays++
		stats.avgCards += float64(len(log.Chosen))
		
		// 检测是否使用炸弹
		if len(log.Chosen) == 4 {
			ph, err := rule.ParseHand(log.Chosen)
			if err == nil && ph.Type == rule.Bomb {
				stats.bombPlays++
			}
		}
		
		playerStats[name] = stats
	}
	
	// 根据统计数据判断风格
	for _, name := range players {
		stats, ok := playerStats[name]
		if !ok || stats.totalPlays == 0 {
			continue
		}
		
		avgCards := stats.avgCards / float64(stats.totalPlays)
		bombRate := float64(stats.bombPlays) / float64(stats.totalPlays)
		
		// 判断风格
		var style string
		if bombRate > 0.3 {
			style = "激进型（频繁使用炸弹）"
		} else if avgCards < 2.0 {
			style = "保守型（偏好小牌逐步推进）"
		} else if bombRate > 0.15 && avgCards > 3.0 {
			style = "均衡型（攻守平衡）"
		} else {
			style = "常规型"
		}
		
		styles[name] = style
	}
	
	return styles
}

// generateSuggestions 生成改进建议
func generateSuggestions(logs []DecisionLog, winner string) []string {
	var suggestions []string
	
	// 分析失败方的决策
	for _, log := range logs {
		if log.PlayerName == winner {
			continue // 跳过获胜者的日志
		}
		
		// 检查是否有拆牌导致手数增加的情况
		if log.Context.UnbeatenStreak >= 2 {
			suggestions = append(suggestions, 
				fmt.Sprintf("%s 在第%d轮连续让牌，可考虑更早拆牌夺权", 
					log.PlayerName, log.Round))
		}
		
		// 检查农民配合问题
		if !log.IsLandlord && len(log.Chosen) > 0 {
			// 检查是否在队友接近走完时送了小牌
			if log.Context.PlayerCounts[0] <= 2 || log.Context.PlayerCounts[1] <= 2 {
				if len(log.Chosen) <= 2 {
					suggestions = append(suggestions,
						fmt.Sprintf("%s 在队友接近走完时应优先送大牌帮助收尾", 
							log.PlayerName))
				}
			}
		}
		
		// 限制建议数量
		if len(suggestions) >= 5 {
			break
		}
	}
	
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "整体表现良好，继续保持！")
	}
	
	return suggestions
}
