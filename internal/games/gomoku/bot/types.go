package bot

import (
	"context"

	"xgames/internal/games"
)

// DecisionEngine 五子棋决策引擎接口（纯代码启发式）
type DecisionEngine interface {
	// DecideMove 落子决策：返回要落子的行/列（必须为棋盘内空位，
	// 非法时服务端回退兜底落子）。
	// 未携带难度配置时按引擎既有行为决策（零值 DifficultyConfig 语义）。
	DecideMove(ctx context.Context, botName string, board []int, color int) (row, col int)
}

// DecisionDetailer 决策详情接口：在 DecideMove 语义之上返回决策过程数据
// （难度配置、真实评估分、搜索深度、Top-N 候选、杀棋命中），供复盘记录
// 与后续分析使用。Engine 实现；未实现该接口的引擎桩自动回退 DecideMove。
type DecisionDetailer interface {
	DecideMoveDetailed(ctx context.Context, botName string, board []int, color int, cfg DifficultyConfig) DecisionDetail
}

// ScoredMove 带评估分的候选着法
type ScoredMove struct {
	Row, Col int
	Score    int
}

// DecisionDetail 一次落子决策的完整详情（复盘去占位的数据来源）
type DecisionDetail struct {
	Row, Col   int          // 最终落子
	Score      int          // 所选着法的根评估分
	Depth      int          // 实际完成的迭代加深层数（短路返回时为 0）
	BookHit    bool         // 开局库命中
	VCFFound   bool         // VCF 连续冲四必胜路径命中
	VCTFound   bool         // VCT 连续活三必胜路径命中
	Threats    []string     // 落子后形成的威胁（成五/活四/冲四/活三/眠三）
	TopMoves   []ScoredMove // 根候选 Top-5（按分数降序，含所选着法）
	Difficulty string       // 生效难度名称（normalize 后）
}

// BotResponder 平台机器人动作回调端口别名（避免 session↔bot 循环依赖；
// 动作以协议消息形式提交，GameSession 结构上满足）
type BotResponder = games.BotResponder
