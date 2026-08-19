// Package replay 提供统一的决策解释与复盘系统
// 适用于所有对战类游戏（斗地主、象棋、五子棋、麻将等）
package replay

import (
	"fmt"
	"strings"
	"time"
)

// DecisionLog 单次决策日志记录（通用结构）
type DecisionLog struct {
	Timestamp    int64               // 决策时间戳
	Round        int                 // 当前轮次/步数
	PlayerName   string              // 机器人名称
	Context      GameContextInfo     // 游戏上下文摘要
	Candidates   []CandidateInfo     // 所有候选着法
	Chosen       MoveInfo            // 最终选择
	Reasoning    string              // 自然语言解释
	StrengthDiff float64             // 选择 vs 次优的强度差
	EvalScore    float64             // 评估分数（如适用）
	WinRate      float64             // 胜率预估（如启用MCTS）
	Metadata     map[string]any      // 游戏特定的元数据
}

// GameContextInfo 游戏上下文摘要（通用字段）
type GameContextInfo struct {
	GameType     string              // 游戏类型：ddz/chess/gomoku/mahjong
	IsFirstMove  bool                // 是否先手/领出
	OpponentCount int                // 对手数量
	RoundNumber  int                 // 当前轮次
	Difficulty   string              // 难度等级
	CustomFields map[string]any      // 游戏特定字段
}

// CandidateInfo 候选着法信息
type CandidateInfo struct {
	Move        MoveInfo             // 着法描述
	Score       float64              // 评分
	Reason      string               // 推荐理由简述
	Priority    int                  // 优先级排序
}

// MoveInfo 着法信息（通用表示）
type MoveInfo struct {
	Description string               // 人类可读的描述（如"炮二平五"、"出对3"）
	Type        string               // 着法类型（lead/follow/capture/check等）
	Value       float64              // 数值化价值（用于比较）
	RawData     any                  // 原始数据结构（游戏特定）
}

// ReplayReport 对局复盘报告（通用结构）
type ReplayReport struct {
	GameID         string             // 对局 ID
	GameType       string             // 游戏类型
	Duration       time.Duration      // 对局时长
	Winner         string             // 获胜者
	Players        []string           // 参与玩家
	KeyMoments     []DecisionLog      // 关键决策点
	MissedOpportunities []string      // 错过的更优选择
	PlayerStyles   map[string]string  // 玩家风格总结
	Suggestions    []string           // 改进建议
	Statistics     GameStatistics     // 对局统计
}

// GameStatistics 对局统计数据
type GameStatistics struct {
	TotalMoves     int                // 总步数
	AvgDecisionTime float64           // 平均决策时间（秒）
	CriticalErrors int               // 关键失误数
	BestMove       DecisionLog       // 最佳决策
	WorstMove      DecisionLog       // 最差决策
	CustomStats    map[string]any    // 游戏特定统计
}

// DecisionLogger 决策日志收集器（通用）
type DecisionLogger struct {
	logs     []DecisionLog
	gameType string
	gameID   string
}

// NewDecisionLogger 创建新的日志收集器
func NewDecisionLogger(gameType, gameID string) *DecisionLogger {
	return &DecisionLogger{
		logs:     make([]DecisionLog, 0),
		gameType: gameType,
		gameID:   gameID,
	}
}

// Record 记录一次决策
func (dl *DecisionLogger) Record(log DecisionLog) {
	log.Context.GameType = dl.gameType
	dl.logs = append(dl.logs, log)
}

// GetLogs 获取所有日志
func (dl *DecisionLogger) GetLogs() []DecisionLog {
	return dl.logs
}

// GetKeyMoments 获取关键决策点
func (dl *DecisionLogger) GetKeyMoments() []DecisionLog {
	var moments []DecisionLog
	for _, log := range dl.logs {
		if isKeyDecision(log, dl.gameType) {
			moments = append(moments, log)
		}
	}
	return moments
}

// GenerateReplayReport 生成完整复盘报告
func (dl *DecisionLogger) GenerateReplayReport(winner string, players []string) *ReplayReport {
	report := &ReplayReport{
		GameID:              dl.gameID,
		GameType:            dl.gameType,
		Winner:              winner,
		Players:             players,
		KeyMoments:          dl.GetKeyMoments(),
		MissedOpportunities: detectMissedOpportunities(dl.logs),
		PlayerStyles:        analyzePlayerStyles(dl.logs, players),
		Suggestions:         generateSuggestions(dl.logs, winner, dl.gameType),
		Statistics:          calculateStatistics(dl.logs),
	}

	// 计算对局时长
	if len(dl.logs) >= 2 {
		firstTime := time.Unix(dl.logs[0].Timestamp, 0)
		lastTime := time.Unix(dl.logs[len(dl.logs)-1].Timestamp, 0)
		report.Duration = lastTime.Sub(firstTime)
	}

	return report
}

// FormatDecisionLog 格式化单条决策日志为可读文本
func FormatDecisionLog(log *DecisionLog) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("[第%d步] ", log.Round))
	sb.WriteString(log.PlayerName)
	sb.WriteString(": ")
	sb.WriteString(log.Chosen.Description)
	sb.WriteString("\n")

	if log.Reasoning != "" {
		sb.WriteString("原因:\n")
		// 按行缩进
		lines := strings.Split(log.Reasoning, "\n")
		for _, line := range lines {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	if log.EvalScore != 0 {
		sb.WriteString(fmt.Sprintf("评估分: %.2f\n", log.EvalScore))
	}

	if log.WinRate > 0 {
		sb.WriteString(fmt.Sprintf("胜率: %.1f%%\n", log.WinRate*100))
	}

	return sb.String()
}

// FormatReplayReport 格式化完整复盘报告
func FormatReplayReport(report *ReplayReport) string {
	var sb strings.Builder

	gameName := getGameName(report.GameType)
	sb.WriteString(fmt.Sprintf("=== %s对局复盘报告 ===\n\n", gameName))
	sb.WriteString(fmt.Sprintf("对局 ID: %s\n", report.GameID))
	sb.WriteString(fmt.Sprintf("时长: %v\n", report.Duration))
	sb.WriteString(fmt.Sprintf("参与玩家: %s\n", strings.Join(report.Players, ", ")))
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

	if len(report.PlayerStyles) > 0 {
		sb.WriteString("--- 玩家风格分析 ---\n")
		for name, style := range report.PlayerStyles {
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

// Helper functions

func isKeyDecision(log DecisionLog, gameType string) bool {
	// 通用判断：强度差异大
	if log.StrengthDiff > 2.0 {
		return true
	}

	// 游戏特定判断
	switch gameType {
	case "ddz":
		// 斗地主：使用炸弹、王炸
		if strings.Contains(log.Chosen.Description, "炸弹") || 
		   strings.Contains(log.Chosen.Description, "王炸") {
			return true
		}
	case "chess":
		// 象棋：吃子、将军
		if log.Chosen.Type == "capture" || log.Chosen.Type == "check" {
			return true
		}
	case "gomoku":
		// 五子棋：形成活四、冲四
		if strings.Contains(log.Chosen.Description, "活四") || 
		   strings.Contains(log.Chosen.Description, "冲四") {
			return true
		}
	case "mahjong":
		// 麻将：碰、杠、听牌
		if log.Chosen.Type == "pong" || log.Chosen.Type == "kong" || 
		   log.Chosen.Type == "ting" {
			return true
		}
	}

	return false
}

func detectMissedOpportunities(logs []DecisionLog) []string {
	var opportunities []string

	for _, log := range logs {
		// 检查是否有明显更好的候选被跳过
		if len(log.Candidates) >= 2 {
			bestScore := log.Candidates[0].Score
			chosenScore := log.Chosen.Value

			// 如果选择的分数明显低于最优（差距 >20%）
			if bestScore > 0 && chosenScore > 0 && 
			   (bestScore-chosenScore)/bestScore > 0.2 {
				opportunities = append(opportunities,
					fmt.Sprintf("第%d步: 有更优选择（评分 %.2f vs %.2f）",
						log.Round, bestScore, chosenScore))

				// 限制输出数量
				if len(opportunities) >= 5 {
					return opportunities
				}
			}
		}
	}

	return opportunities
}

func analyzePlayerStyles(logs []DecisionLog, players []string) map[string]string {
	styles := make(map[string]string)

	// 统计每个玩家的决策特征
	playerStats := make(map[string]struct {
		totalMoves    int
		aggressive    int // 进攻性着法
		defensive     int // 防守性着法
		risky         int // 冒险着法
	})

	for _, log := range logs {
		name := log.PlayerName
		stats := playerStats[name]
		stats.totalMoves++

		// 根据着法类型和推理判断风格
		if strings.Contains(log.Reasoning, "进攻") || 
		   strings.Contains(log.Reasoning, "攻击") {
			stats.aggressive++
		}
		if strings.Contains(log.Reasoning, "防守") || 
		   strings.Contains(log.Reasoning, "保护") {
			stats.defensive++
		}
		if log.StrengthDiff < -1.0 { // 选择了较差的着法，可能是冒险
			stats.risky++
		}

		playerStats[name] = stats
	}

	// 根据统计数据判断风格
	for _, name := range players {
		stats, ok := playerStats[name]
		if !ok || stats.totalMoves == 0 {
			continue
		}

		aggRate := float64(stats.aggressive) / float64(stats.totalMoves)
		defRate := float64(stats.defensive) / float64(stats.totalMoves)
		riskRate := float64(stats.risky) / float64(stats.totalMoves)

		var style string
		if aggRate > 0.4 {
			style = "激进型（偏好进攻）"
		} else if defRate > 0.4 {
			style = "保守型（偏好防守）"
		} else if riskRate > 0.2 {
			style = "冒险型（愿意承担风险）"
		} else if aggRate > 0.2 && defRate > 0.2 {
			style = "均衡型（攻守平衡）"
		} else {
			style = "常规型"
		}

		styles[name] = style
	}

	return styles
}

func generateSuggestions(logs []DecisionLog, winner string, gameType string) []string {
	var suggestions []string

	// 分析失败方的决策
	for _, log := range logs {
		if log.PlayerName == winner {
			continue // 跳过获胜者的日志
		}

		// 检查关键失误
		if log.StrengthDiff < -2.0 {
			suggestions = append(suggestions,
				fmt.Sprintf("%s 在第%d步出现明显失误，可考虑更优选择",
					log.PlayerName, log.Round))
		}

		// 游戏特定建议
		switch gameType {
		case "ddz":
			// 斗地主：农民配合问题
			if log.Context.CustomFields["isLandlord"] == false {
				if teammateCloseToEnd(log) {
					suggestions = append(suggestions,
						fmt.Sprintf("%s 在队友接近走完时应优先送大牌", log.PlayerName))
				}
			}
		case "chess":
			// 象棋：开局/中局/残局建议
			if log.Round <= 10 && log.StrengthDiff < -1.0 {
				suggestions = append(suggestions,
					fmt.Sprintf("%s 开局阶段应更注重布局", log.PlayerName))
			}
		case "gomoku":
			// 五子棋：防守疏忽
			if strings.Contains(log.Reasoning, "忽略威胁") {
				suggestions = append(suggestions,
					fmt.Sprintf("%s 应注意对手的潜在威胁", log.PlayerName))
			}
		case "mahjong":
			// 麻将：防守时机
			if log.Round > 12 && log.Chosen.Type == "discard" {
				suggestions = append(suggestions,
					fmt.Sprintf("%s 晚巡应更注重防守", log.PlayerName))
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

func calculateStatistics(logs []DecisionLog) GameStatistics {
	stats := GameStatistics{
		TotalMoves:  len(logs),
		CustomStats: make(map[string]any),
	}

	if len(logs) == 0 {
		return stats
	}

	// 计算平均决策时间
	var totalTime float64
	for i := 1; i < len(logs); i++ {
		diff := float64(logs[i].Timestamp - logs[i-1].Timestamp)
		totalTime += diff
	}
	if len(logs) > 1 {
		stats.AvgDecisionTime = totalTime / float64(len(logs)-1) / 1000.0 // 转换为秒
	}

	// 统计关键失误
	for _, log := range logs {
		if log.StrengthDiff < -2.0 {
			stats.CriticalErrors++
		}
	}

	// 找出最佳和最差决策
	bestIdx, worstIdx := 0, 0
	for i, log := range logs {
		if log.StrengthDiff > logs[bestIdx].StrengthDiff {
			bestIdx = i
		}
		if log.StrengthDiff < logs[worstIdx].StrengthDiff {
			worstIdx = i
		}
	}
	stats.BestMove = logs[bestIdx]
	stats.WorstMove = logs[worstIdx]

	return stats
}

func getGameName(gameType string) string {
	names := map[string]string{
		"ddz":     "斗地主",
		"chess":   "中国象棋",
		"gomoku":  "五子棋",
		"mahjong": "麻将",
	}
	if name, ok := names[gameType]; ok {
		return name
	}
	return gameType
}

func teammateCloseToEnd(log DecisionLog) bool {
	// 检查队友是否接近走完（斗地主特定逻辑）
	if counts, ok := log.Context.CustomFields["playerCounts"].([2]int); ok {
		return counts[0] <= 2 || counts[1] <= 2
	}
	return false
}
