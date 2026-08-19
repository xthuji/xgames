package replay

import (
	"strings"
	"testing"
	"time"
)

// TestDecisionLogger_Record 测试决策日志记录功能
func TestDecisionLogger_Record(t *testing.T) {
	logger := NewDecisionLogger("test_game", "test_001")
	
	log := DecisionLog{
		Timestamp:  1693824000,
		Round:      5,
		PlayerName: "Player1",
		Context: GameContextInfo{
			GameType:      "test_game",
			IsFirstMove:   false,
			OpponentCount: 2,
			RoundNumber:   5,
			Difficulty:    "normal",
		},
		Candidates: []CandidateInfo{
			{
				Move: MoveInfo{
					Description: "出牌: 3, 4, 5",
					Type:        "straight",
					Value:       85.5,
				},
				Score:    85.5,
				Reason:   "最佳出牌选择",
				Priority: 1,
			},
			{
				Move: MoveInfo{
					Description: "出牌: 单张3",
					Type:        "single",
					Value:       60.0,
				},
				Score:    60.0,
				Reason:   "保守选择",
				Priority: 2,
			},
		},
		Chosen: MoveInfo{
			Description: "出牌: 3, 4, 5",
			Type:        "straight",
			Value:       85.5,
		},
		Reasoning:    "选择顺子可以更快清空手牌",
		StrengthDiff: 0.0,
		EvalScore:    85.5,
	}
	
	logger.Record(log)
	
	if len(logger.logs) != 1 {
		t.Errorf("Record() logs count = %v, want 1", len(logger.logs))
	}
	
	if logger.logs[0].Round != 5 {
		t.Errorf("Record() round = %v, want 5", logger.logs[0].Round)
	}
}

// TestDecisionLogger_GenerateReplayReport 测试复盘报告生成
func TestDecisionLogger_GenerateReplayReport(t *testing.T) {
	logger := NewDecisionLogger("ddz", "room_001")
	
	// 添加多个决策日志，包含关键决策（StrengthDiff > 2.0）
	for i := 1; i <= 10; i++ {
		strengthDiff := 0.0
		if i == 5 || i == 8 {
			strengthDiff = 3.0 // 制造关键决策点
		}
		
		logger.Record(DecisionLog{
			Timestamp:  1693824000 + int64(i*60),
			Round:      i,
			PlayerName: "Player1",
			Context: GameContextInfo{
				GameType:      "ddz",
				IsFirstMove:   i == 1,
				OpponentCount: 2,
				RoundNumber:   i,
			},
			Chosen: MoveInfo{
				Description: "出牌测试",
				Type:        "test",
				Value:       float64(i * 10),
			},
			Reasoning:    "测试推理",
			EvalScore:    float64(i * 10),
			StrengthDiff: strengthDiff,
		})
	}
	
	report := logger.GenerateReplayReport("Player1", []string{"Player1", "Player2", "Player3"})
	
	if report == nil {
		t.Fatal("GenerateReplayReport() returned nil")
	}
	
	if report.GameID != "room_001" {
		t.Errorf("GenerateReplayReport() gameID = %v, want room_001", report.GameID)
	}
	
	if report.Winner != "Player1" {
		t.Errorf("GenerateReplayReport() winner = %v, want Player1", report.Winner)
	}
	
	if len(report.KeyMoments) == 0 {
		t.Error("GenerateReplayReport() keyMoments is empty")
	}
}

// TestFormatDecisionLog 测试决策日志格式化
func TestFormatDecisionLog(t *testing.T) {
	log := &DecisionLog{
		Timestamp:  1693824000,
		Round:      5,
		PlayerName: "Player1",
		Context: GameContextInfo{
			GameType:      "ddz",
			IsFirstMove:   false,
			OpponentCount: 2,
			RoundNumber:   5,
		},
		Chosen: MoveInfo{
			Description: "出牌: 3, 4, 5, 6, 7",
			Type:        "straight",
			Value:       85.5,
		},
		Reasoning: "选择顺子可以更快清空手牌",
		EvalScore: 85.5,
	}
	
	formatted := FormatDecisionLog(log)
	
	if !strings.Contains(formatted, "第5步") {
		t.Error("FormatDecisionLog() should contain round number")
	}
	
	if !strings.Contains(formatted, "Player1") {
		t.Error("FormatDecisionLog() should contain player name")
	}
	
	if !strings.Contains(formatted, "出牌: 3, 4, 5, 6, 7") {
		t.Error("FormatDecisionLog() should contain chosen move")
	}
}

// TestFormatReplayReport 测试复盘报告格式化
func TestFormatReplayReport(t *testing.T) {
	report := &ReplayReport{
		GameID:    "room_001",
		GameType:  "ddz",
		Duration:  15 * time.Minute,
		Winner:    "Player1",
		Players:   []string{"Player1", "Player2", "Player3"},
		KeyMoments: []DecisionLog{
			{
				Round:      5,
				PlayerName: "Player1",
				Chosen: MoveInfo{
					Description: "关键出牌",
				},
				Reasoning: "这是关键时刻的决策",
			},
		},
		MissedOpportunities: []string{
			"第3轮错过了出炸弹的机会",
		},
		PlayerStyles: map[string]string{
			"Player2": "激进型玩家，喜欢冒险",
		},
		Suggestions: []string{
			"建议在早期保留更多大牌",
		},
	}
	
	formatted := FormatReplayReport(report)
	
	if !strings.Contains(formatted, "=== 斗地主对局复盘报告 ===") {
		t.Error("FormatReplayReport() should contain header with game name")
	}
	
	if !strings.Contains(formatted, "对局 ID: room_001") {
		t.Error("FormatReplayReport() should contain game ID")
	}
	
	if !strings.Contains(formatted, "--- 关键决策点 ---") {
		t.Error("FormatReplayReport() should contain key moments section")
	}
	
	if !strings.Contains(formatted, "--- 错过的机会 ---") {
		t.Error("FormatReplayReport() should contain missed opportunities section")
	}
	
	if !strings.Contains(formatted, "--- 玩家风格分析 ---") {
		t.Error("FormatReplayReport() should contain player styles section")
	}
	
	if !strings.Contains(formatted, "--- 改进建议 ---") {
		t.Error("FormatReplayReport() should contain suggestions section")
	}
}

// TestIsKeyDecision 测试关键决策检测
func TestIsKeyDecision(t *testing.T) {
	tests := []struct {
		name     string
		log      DecisionLog
		gameType string
		expected bool
	}{
		{
			name: "高评分差异是关键决策",
			log: DecisionLog{
				Round:        5,
				StrengthDiff: 3.0, // 超过阈值 2.0
			},
			gameType: "ddz",
			expected: true,
		},
		{
			name: "斗地主使用炸弹是关键决策",
			log: DecisionLog{
				Round: 5,
				Chosen: MoveInfo{
					Description: "出牌: 炸弹",
				},
			},
			gameType: "ddz",
			expected: true,
		},
		{
			name: "象棋吃子是关键决策",
			log: DecisionLog{
				Round: 5,
				Chosen: MoveInfo{
					Type: "capture",
				},
			},
			gameType: "chess",
			expected: true,
		},
		{
			name: "普通决策不是关键决策",
			log: DecisionLog{
				Round:        5,
				StrengthDiff: 1.0, // 未超过阈值 2.0
			},
			gameType: "ddz",
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isKeyDecision(tt.log, tt.gameType)
			if result != tt.expected {
				t.Errorf("isKeyDecision() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestDetectMissedOpportunities 测试错过机会检测
func TestDetectMissedOpportunities(t *testing.T) {
	logs := []DecisionLog{
		{
			Round:      3,
			PlayerName: "Player1",
			Chosen: MoveInfo{
				Description: "出牌: 单张3",
				Value:       30.0,
			},
			Candidates: []CandidateInfo{
				{
					Score: 90.0, // 有更好的选择
					Move: MoveInfo{
						Description: "出牌: 炸弹",
					},
				},
				{
					Score: 30.0,
					Move: MoveInfo{
						Description: "出牌: 单张3",
					},
				},
			},
		},
	}
	
	opportunities := detectMissedOpportunities(logs)
	
	if len(opportunities) == 0 {
		t.Error("detectMissedOpportunities() should detect missed opportunity")
	}
	
	if !strings.Contains(opportunities[0], "第3步") {
		t.Error("Missed opportunity should mention round number")
	}
}

// TestAnalyzePlayerStyles 测试玩家风格分析
func TestAnalyzePlayerStyles(t *testing.T) {
	logs := []DecisionLog{
		{
			PlayerName: "Player1",
			Chosen: MoveInfo{
				Type:  "bomb",
				Value: 95.0,
			},
		},
		{
			PlayerName: "Player1",
			Chosen: MoveInfo{
				Type:  "bomb",
				Value: 90.0,
			},
		},
		{
			PlayerName: "Player2",
			Chosen: MoveInfo{
				Type:  "single",
				Value: 30.0,
			},
		},
	}
	
	players := []string{"Player1", "Player2"}
	styles := analyzePlayerStyles(logs, players)
	
	if styles["Player1"] == "" {
		t.Error("analyzePlayerStyles() should have style for Player1")
	}
	
	if styles["Player2"] == "" {
		t.Error("analyzePlayerStyles() should have style for Player2")
	}
}

// TestGenerateSuggestions 测试建议生成
func TestGenerateSuggestions(t *testing.T) {
	logs := []DecisionLog{
		{
			Round:      5,
			StrengthDiff: -25.0, // 重大失误
		},
	}
	
	suggestions := generateSuggestions(logs, "Player1", "ddz")
	
	if len(suggestions) == 0 {
		t.Error("generateSuggestions() should generate at least one suggestion")
	}
}

// TestNewDecisionLogger 测试日志器创建
func TestNewDecisionLogger(t *testing.T) {
	logger := NewDecisionLogger("ddz", "room_001")
	
	if logger == nil {
		t.Fatal("NewDecisionLogger() returned nil")
	}
	
	if logger.gameType != "ddz" {
		t.Errorf("NewDecisionLogger() gameType = %v, want ddz", logger.gameType)
	}
	
	if logger.gameID != "room_001" {
		t.Errorf("NewDecisionLogger() gameID = %v, want room_001", logger.gameID)
	}
	
	if len(logger.logs) != 0 {
		t.Errorf("NewDecisionLogger() initial logs count = %v, want 0", len(logger.logs))
	}
}

// BenchmarkDecisionLogger_Record 性能测试：记录决策日志
func BenchmarkDecisionLogger_Record(b *testing.B) {
	logger := NewDecisionLogger("test", "test_001")
	
	log := DecisionLog{
		Timestamp:  1693824000,
		Round:      1,
		PlayerName: "Player1",
		Context: GameContextInfo{
			GameType:      "test",
			IsFirstMove:   true,
			OpponentCount: 2,
			RoundNumber:   1,
		},
		Chosen: MoveInfo{
			Description: "Test move",
			Type:        "test",
			Value:       50.0,
		},
		Reasoning: "Test reasoning",
		EvalScore: 50.0,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Record(log)
	}
}

// BenchmarkFormatReplayReport 性能测试：格式化复盘报告
func BenchmarkFormatReplayReport(b *testing.B) {
	logger := NewDecisionLogger("test", "test_001")
	
	// 添加100个决策日志
	for i := 1; i <= 100; i++ {
		logger.Record(DecisionLog{
			Timestamp:  1693824000 + int64(i*60),
			Round:      i,
			PlayerName: "Player1",
			Context: GameContextInfo{
				GameType:      "test",
				IsFirstMove:   i == 1,
				OpponentCount: 2,
				RoundNumber:   i,
			},
			Chosen: MoveInfo{
				Description: "Test move",
				Type:        "test",
				Value:       float64(i),
			},
			Reasoning: "Test reasoning",
			EvalScore: float64(i),
		})
	}
	
	report := logger.GenerateReplayReport("Player1", []string{"Player1", "Player2", "Player3"})
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FormatReplayReport(report)
	}
}
