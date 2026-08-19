package bot

import (
	"context"

	"xgames/internal/games"
)

// DecisionEngine 中国象棋决策引擎接口
type DecisionEngine interface {
	// DecideMove 走子决策：返回 from/to 坐标（必须为合法走法，
	// 非法时服务端回退兗底走子）。
	// 未携带难度配置时按引擎既有行为决策（零值 DifficultyConfig 语义）。
	DecideMove(ctx context.Context, botName string, board []int, camp int) (fromRow, fromCol, toRow, toCol int)
}

// DecisionDetailer 决策详情接口：在 DecideMove 语义之上返回决策过程数据
// （难度配置、真实评估分、搜索深度、Top-N 候选、开局库命中），供复盘记录
// 与后续分析使用。Engine 实现；未实现该接口的引擎桩自动回退 DecideMove。
type DecisionDetailer interface {
	DecideMoveDetailed(ctx context.Context, botName string, board []int, camp int, cfg DifficultyConfig) DecisionDetail
}

// GameContext 实战对局上下文（供引擎判和感知：重复局面求和 / 自然限着避和）
type GameContext struct {
	// History 实战局面哈希序列（含当前局面，zobristHash 口径，含执子方）；
	// 传入搜索后实战中已重复两次的局面计 0 分（三次重复不变作和）。
	History []uint64
	// HalfmoveClock 连续未吃子 ply 数；逼近自然限着线时启用求和倾向（contempt）。
	HalfmoveClock int
}

// GameContexter 可选接口：支持传入实战判和上下文的引擎实现它；
// 未实现时调用方回退 DecideMoveDetailed（零值上下文，行为与旧版一致）。
type GameContexter interface {
	DecideMoveWithContext(ctx context.Context, botName string, board []int, camp int, cfg DifficultyConfig, gctx GameContext) DecisionDetail
}

// ScoredMove 带评估分的候选着法
type ScoredMove struct {
	FromRow, FromCol int
	ToRow, ToCol     int
	Score            int
}

// DecisionDetail 一次落子决策的完整详情（复盘去占位的数据来源）
type DecisionDetail struct {
	FromRow, FromCol int          // 最终走子
	ToRow, ToCol     int          // 最终落子
	Score            int          // 所选着法的根评估分
	Depth            int          // 实际完成的迭代加深层数
	BookHit          bool         // 开局库命中
	TopMoves         []ScoredMove // 根候选 Top-5（按分数降序，含所选着法）
	Difficulty       string       // 生效难度名称（normalize 后）
}

// BotResponder 平台机器人动作回调端口别名
type BotResponder = games.BotResponder
