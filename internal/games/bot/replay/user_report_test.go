package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlayerReplayLogger_RecordAndQuery(t *testing.T) {
	logger := NewPlayerReplayLogger("ddz", "123456")
	if !logger.Empty() {
		t.Error("新建收集器应为空")
	}

	logger.Record(PlayerDecision{Round: 1, PlayerID: "p1", PlayerName: "玩家一", ChosenDesc: "出 3"})
	logger.Record(PlayerDecision{Round: 2, PlayerID: "p1", PlayerName: "玩家一", ChosenDesc: "过牌", IsPass: true})
	logger.Record(PlayerDecision{Round: 1, PlayerID: "p2", PlayerName: "玩家二", ChosenDesc: "出 5"})

	if logger.Empty() {
		t.Error("记录后不应为空")
	}

	p1 := logger.DecisionsByPlayer("p1")
	if len(p1) != 2 || p1[0].Round != 1 || p1[1].Round != 2 {
		t.Errorf("DecisionsByPlayer(p1) = %+v, want 2 条按序记录", p1)
	}
	p2 := logger.DecisionsByPlayer("p2")
	if len(p2) != 1 {
		t.Errorf("DecisionsByPlayer(p2) = %+v, want 1 条", p2)
	}
	if none := logger.DecisionsByPlayer("p3"); len(none) != 0 {
		t.Errorf("DecisionsByPlayer(不存在) = %+v, want 空", none)
	}

	ids := logger.PlayerIDs()
	if len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
		t.Errorf("PlayerIDs() = %v, want [p1 p2]（按首次出现顺序）", ids)
	}
}

func TestBuildUserReport_Stats(t *testing.T) {
	analyses := []MoveAnalysis{
		{Round: 1, ActualDesc: "出 3", IsMistake: true, Severity: SeverityCritical},
		{Round: 2, ActualDesc: "出 5", IsMistake: true, Severity: SeverityMajor},
		{Round: 3, ActualDesc: "出 王", Highlight: true},
		{Round: 4, ActualDesc: "出 4"},
	}

	report := BuildUserReport(UserReportOptions{
		GameType:   "ddz",
		GameID:     "123456",
		PlayerID:   "p1",
		PlayerName: "玩家一",
		WinnerName: "玩家二",
		Analyses:   analyses,
		Duration:   5 * time.Minute,
	})

	if report.TotalDecisions != 4 {
		t.Errorf("TotalDecisions = %d, want 4", report.TotalDecisions)
	}
	if len(report.Mistakes) != 2 {
		t.Errorf("Mistakes = %d, want 2", len(report.Mistakes))
	}
	if len(report.Highlights) != 1 {
		t.Errorf("Highlights = %d, want 1", len(report.Highlights))
	}
	wantAccuracy := 50.0
	if report.Accuracy != wantAccuracy {
		t.Errorf("Accuracy = %.1f, want %.1f", report.Accuracy, wantAccuracy)
	}
	// 建议应包含重大失误提示与准确率
	joined := strings.Join(report.Suggestions, "\n")
	if !strings.Contains(joined, "重大失误") || !strings.Contains(joined, "50%") {
		t.Errorf("Suggestions 应包含重大失误与准确率提示, got %q", joined)
	}
	if report.PlayerWon {
		t.Error("未设置 PlayerWon 时应为失败")
	}
}

func TestBuildUserReport_NoMistake(t *testing.T) {
	report := BuildUserReport(UserReportOptions{
		GameType: "gomoku",
		GameID:   "123456",
		PlayerID: "p1",
		Analyses: []MoveAnalysis{
			{Round: 1, ActualDesc: "(7,7) 落子"},
			{Round: 2, ActualDesc: "(7,8) 落子"},
		},
	})
	if len(report.Mistakes) != 0 || report.Accuracy != 100 {
		t.Errorf("无失误时 Mistakes/Accuracy = %d/%.1f, want 0/100", len(report.Mistakes), report.Accuracy)
	}
	joined := strings.Join(report.Suggestions, "\n")
	if !strings.Contains(joined, "继续保持") {
		t.Errorf("无失误时应给正面反馈, got %q", joined)
	}
}

func TestFormatUserReplayReport(t *testing.T) {
	report := BuildUserReport(UserReportOptions{
		GameType:    "ddz",
		GameID:      "123456",
		PlayerID:    "p1",
		PlayerName:  "玩家一",
		WinnerName:  "玩家二",
		GameSummary: "你是地主",
		Analyses: []MoveAnalysis{
			{Round: 1, ActualDesc: "", BestDesc: "出 4", IsMistake: true,
				Severity: SeverityMajor, Reason: "该压不压"},
			{Round: 2, ActualDesc: "对 2", Highlight: true, Reason: "显著简化手牌"},
		},
		Duration: 3 * time.Minute,
	})

	text := FormatUserReplayReport(report)
	for _, want := range []string{
		"斗地主 复盘报告", "玩家一", "对局 ID: 123456", "你是地主",
		"失败（获胜者: 玩家二）", "失误: 1", "--- 失误节点 ---", "第1步",
		"（过牌）", "更优出法: 出 4", "原因: 该压不压", "严重度: 明显",
		"--- 亮点着法 ---", "对 2", "--- 改进建议 ---",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("报告应包含 %q\n报告内容:\n%s", want, text)
		}
	}
}

func TestSaveUserReport_Filename(t *testing.T) {
	// SaveUserReport 写入相对路径 data/replays/<game>/，切换到临时目录验证
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	text := "测试报告内容"
	path, err := SaveUserReport("ddz", "123456", "abcdef0123456789", text)
	if err != nil {
		t.Fatalf("SaveUserReport() error = %v", err)
	}

	wantDir := filepath.Join("data", "replays", "ddz")
	if filepath.Dir(path) != wantDir {
		t.Errorf("保存目录 = %s, want %s", filepath.Dir(path), wantDir)
	}

	// 文件名格式：{roomCode}_{playerID}_{ts}.txt
	base := filepath.Base(path)
	parts := strings.Split(strings.TrimSuffix(base, ".txt"), "_")
	if len(parts) != 3 || parts[0] != "123456" || parts[1] != "abcdef0123456789" {
		t.Errorf("文件名 = %s, want 123456_abcdef0123456789_<ts>.txt", base)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("报告文件应已落盘: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != text {
		t.Errorf("文件内容不符: %q err=%v", content, err)
	}
}

func TestSortAnalysesByRound(t *testing.T) {
	analyses := []MoveAnalysis{
		{Round: 3}, {Round: 1}, {Round: 2},
	}
	SortAnalysesByRound(analyses)
	for i, a := range analyses {
		if a.Round != i+1 {
			t.Errorf("排序后 analyses[%d].Round = %d, want %d", i, a.Round, i+1)
		}
	}
}
