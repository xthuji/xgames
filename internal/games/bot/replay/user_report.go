package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 失误严重度分级
const (
	SeverityMinor    = "轻微" // 与最优选择等效，仅风格/细节差异
	SeverityMajor    = "明显" // 造成效率损失（手数/向听/子力劣势）
	SeverityCritical = "重大" // 直接影响胜负走向的节点（漏杀、该压不压等）
)

// PlayerDecision 真人玩家单次决策快照。
// 由各游戏 session 在玩家主动行动时记录（bot 与挂机托管代打不记录），
// 对局结束后交由各游戏 bot 包的分析器离线评估"当时最优着法"。
type PlayerDecision struct {
	Round      int    // 步数/轮次
	PlayerID   string // 玩家 ID（报告文件名与按用户过滤用）
	PlayerName string
	ChosenDesc string // 实际选择的人类可读描述
	IsPass     bool   // 是否为过牌/放弃类决策
	Timestamp  int64
	Snapshot   any // 游戏特定决策状态（各游戏 bot 包定义的快照结构）
}

// PlayerReplayLogger 真人玩家决策收集器（session 持有）
type PlayerReplayLogger struct {
	mu        sync.Mutex
	gameType  string
	gameID    string
	decisions []PlayerDecision
}

// NewPlayerReplayLogger 创建真人决策收集器
func NewPlayerReplayLogger(gameType, gameID string) *PlayerReplayLogger {
	return &PlayerReplayLogger{
		gameType:  gameType,
		gameID:    gameID,
		decisions: make([]PlayerDecision, 0),
	}
}

// Record 记录一次真人决策
func (l *PlayerReplayLogger) Record(d PlayerDecision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decisions = append(l.decisions, d)
}

// DecisionsByPlayer 获取指定玩家的全部决策
func (l *PlayerReplayLogger) DecisionsByPlayer(playerID string) []PlayerDecision {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []PlayerDecision
	for _, d := range l.decisions {
		if d.PlayerID == playerID {
			out = append(out, d)
		}
	}
	return out
}

// PlayerIDs 按首次出现顺序返回所有记录过的玩家 ID
func (l *PlayerReplayLogger) PlayerIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := make(map[string]bool)
	var ids []string
	for _, d := range l.decisions {
		if !seen[d.PlayerID] {
			seen[d.PlayerID] = true
			ids = append(ids, d.PlayerID)
		}
	}
	return ids
}

// Empty 是否没有任何记录（纯 bot 对局或未启用时为 true）
func (l *PlayerReplayLogger) Empty() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.decisions) == 0
}

// MoveAnalysis 单步评估结果：真人实际选择 vs 引擎最优选择
type MoveAnalysis struct {
	Round        int
	ActualDesc   string
	BestDesc     string
	ActualScore  float64 // 实际选择的量化评分（游戏特定标度，可缺省）
	BestScore    float64 // 最优选择的量化评分
	Reason       string  // 为什么更优（面向玩家的通俗解释）
	IsMistake    bool
	Highlight    bool   // 亮点着法（与最优一致且局面收益显著）
	Severity     string // 失误严重度（IsMistake 时有效）
}

// UserReportOptions 用户复盘报告构建参数
type UserReportOptions struct {
	GameType    string
	GameID      string // 房间码
	PlayerID    string
	PlayerName  string
	WinnerName  string
	PlayerWon   bool
	Draw        bool
	Analyses    []MoveAnalysis
	Duration    time.Duration
	GameSummary string // 游戏特定总结行（如斗地主的身份信息），可为空
}

// UserReplayReport 以真人玩家为中心的复盘报告
type UserReplayReport struct {
	GameType       string
	GameID         string
	PlayerID       string
	PlayerName     string
	WinnerName     string
	PlayerWon      bool
	Draw           bool
	TotalDecisions int
	Mistakes       []MoveAnalysis
	Highlights     []MoveAnalysis
	Suggestions    []string
	Accuracy       float64 // 决策准确率（0-100）
	Duration       time.Duration
	GameSummary    string
}

// BuildUserReport 汇总分析结果构建报告
func BuildUserReport(opts UserReportOptions) *UserReplayReport {
	r := &UserReplayReport{
		GameType:       opts.GameType,
		GameID:         opts.GameID,
		PlayerID:       opts.PlayerID,
		PlayerName:     opts.PlayerName,
		WinnerName:     opts.WinnerName,
		PlayerWon:      opts.PlayerWon,
		Draw:           opts.Draw,
		TotalDecisions: len(opts.Analyses),
		Duration:       opts.Duration,
		GameSummary:    opts.GameSummary,
	}
	for _, a := range opts.Analyses {
		if a.IsMistake {
			r.Mistakes = append(r.Mistakes, a)
		} else if a.Highlight {
			r.Highlights = append(r.Highlights, a)
		}
	}
	if r.TotalDecisions > 0 {
		r.Accuracy = float64(r.TotalDecisions-len(r.Mistakes)) / float64(r.TotalDecisions) * 100
	}
	r.Suggestions = buildUserSuggestions(r)
	return r
}

// buildUserSuggestions 基于失误统计生成改进建议
func buildUserSuggestions(r *UserReplayReport) []string {
	var suggestions []string
	var critical, major []MoveAnalysis
	for _, m := range r.Mistakes {
		switch m.Severity {
		case SeverityCritical:
			critical = append(critical, m)
		case SeverityMajor:
			major = append(major, m)
		}
	}

	if len(r.Mistakes) == 0 {
		suggestions = append(suggestions,
			fmt.Sprintf("本局 %d 次决策均与引擎推荐一致，决策质量很高，继续保持！", r.TotalDecisions))
		return suggestions
	}

	if len(critical) > 0 {
		rounds := make([]string, 0, len(critical))
		for _, m := range critical {
			rounds = append(rounds, fmt.Sprintf("第%d步", m.Round))
		}
		suggestions = append(suggestions,
			fmt.Sprintf("本局有 %d 处重大失误（%s），此类节点往往直接决定胜负，建议优先复盘。",
				len(critical), strings.Join(rounds, "、")))
	}
	if len(major) > 0 {
		suggestions = append(suggestions,
			fmt.Sprintf("有 %d 处效率类失误，损失主要体现在行棋/出牌效率上，注意参考报告中的更优选择。",
				len(major)))
	}
	minor := len(r.Mistakes) - len(critical) - len(major)
	if minor > 0 {
		suggestions = append(suggestions,
			fmt.Sprintf("另有 %d 处轻微差异，与最优选择效果接近，可作为风格参考。", minor))
	}
	suggestions = append(suggestions,
		fmt.Sprintf("本局决策准确率 %.0f%%（%d/%d），多对照失误节点的更优出法练习，胜率会稳步提升。",
			r.Accuracy, r.TotalDecisions-len(r.Mistakes), r.TotalDecisions))
	return suggestions
}

// FormatUserReplayReport 格式化用户复盘报告为中文文本
func FormatUserReplayReport(r *UserReplayReport) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "=== %s 复盘报告（%s） ===\n\n", getGameName(r.GameType), r.PlayerName)
	sb.WriteString(fmt.Sprintf("对局 ID: %s\n", r.GameID))
	if r.GameSummary != "" {
		sb.WriteString(fmt.Sprintf("本局信息: %s\n", r.GameSummary))
	}
	if r.Draw {
		sb.WriteString("结果: 平局\n")
	} else if r.PlayerWon {
		sb.WriteString(fmt.Sprintf("结果: 胜利 🎉\n"))
	} else {
		sb.WriteString(fmt.Sprintf("结果: 失败（获胜者: %s）\n", r.WinnerName))
	}
	if r.Duration > 0 {
		sb.WriteString(fmt.Sprintf("时长: %v\n", r.Duration))
	}
	sb.WriteString(fmt.Sprintf("决策总数: %d，失误: %d，准确率: %.0f%%\n\n",
		r.TotalDecisions, len(r.Mistakes), r.Accuracy))

	if len(r.Mistakes) > 0 {
		sb.WriteString("--- 失误节点 ---\n")
		for i, m := range r.Mistakes {
			passTag := ""
			if m.ActualDesc == "" {
				passTag = "（过牌）"
			}
			sb.WriteString(fmt.Sprintf("%d. [第%d步] 你的选择: %s%s\n", i+1, m.Round, m.ActualDesc, passTag))
			sb.WriteString(fmt.Sprintf("   更优出法: %s\n", m.BestDesc))
			if m.Reason != "" {
				sb.WriteString(fmt.Sprintf("   原因: %s\n", m.Reason))
			}
			sb.WriteString(fmt.Sprintf("   严重度: %s\n\n", m.Severity))
		}
	}

	if len(r.Highlights) > 0 {
		sb.WriteString("--- 亮点着法 ---\n")
		for i, m := range r.Highlights {
			sb.WriteString(fmt.Sprintf("%d. [第%d步] %s\n", i+1, m.Round, m.ActualDesc))
			if m.Reason != "" {
				sb.WriteString(fmt.Sprintf("   %s\n", m.Reason))
			}
		}
		sb.WriteString("\n")
	}

	if len(r.Suggestions) > 0 {
		sb.WriteString("--- 改进建议 ---\n")
		for i, s := range r.Suggestions {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
		}
	}

	return sb.String()
}

// replayBaseDir 复盘报告根目录，SaveUserReport 写入 <root>/<game>/ 子目录。
// 默认相对路径按进程工作目录解析（开发模式即项目根 data/replays）；
// main 启动时根据运行环境（开发/打包）调用 SetBaseDir 覆盖为绝对路径。
var replayBaseDir = filepath.Join("data", "replays")

// SetBaseDir 设置复盘报告根目录。仅在应用启动、对外提供服务之前调用（非并发安全）。
func SetBaseDir(dir string) {
	replayBaseDir = dir
}

// BaseDir 返回当前复盘报告根目录（测试与诊断用）
func BaseDir() string {
	return replayBaseDir
}

// SaveUserReport 将用户复盘报告保存到复盘根目录的 <game>/ 子目录。
// 文件名含 roomCode 与 playerID：列表接口按文件名前缀匹配房间、包含匹配玩家。
// 返回保存的文件路径。
func SaveUserReport(game, roomCode, playerID, text string) (string, error) {
	dir := filepath.Join(replayBaseDir, game)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建复盘报告目录失败: %w", err)
	}
	filename := fmt.Sprintf("%s_%s_%d.txt", roomCode, playerID, time.Now().Unix())
	fullPath := filepath.Join(dir, filename)
	if err := os.WriteFile(fullPath, []byte(text), 0644); err != nil {
		return "", fmt.Errorf("写入复盘报告失败: %w", err)
	}
	return fullPath, nil
}

// SortAnalysesByRound 按步数升序排列分析结果（生成报告前调用）
func SortAnalysesByRound(analyses []MoveAnalysis) {
	sort.Slice(analyses, func(i, j int) bool { return analyses[i].Round < analyses[j].Round })
}
