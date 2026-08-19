package bot

import (
	"context"

	"xgames/internal/games"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// DecisionEngine 决策引擎接口（纯代码规则启发式）。
// 所有难度共用同一套叫分/加倍/出牌逻辑，难度差异只体现在
// Controller 构造的记牌视图（GameContext.RemainingCards）完整度上。
type DecisionEngine interface {
	// DecideBidScore 叫分决策：返回 0（不叫）或 1/2/3（须大于 highBid，否则服务端拒绝）
	DecideBidScore(ctx context.Context, botName string, hand []card.Card, highBid int) int
	// DecideDouble 加倍阶段决策：是否加倍
	DecideDouble(ctx context.Context, botName string, hand []card.Card) bool
	// DecidePlay 出牌决策：返回要打的牌；返回 nil 表示 pass。
	// 注意：gctx.MustPlay=true（领出）时禁止返回 nil，否则服务端拒绝导致回合超时。
	DecidePlay(ctx context.Context, botName string, gctx GameContext) []card.Card
}

// BotResponder 平台机器人动作回调端口别名（避免 session↔bot 循环依赖；
// 动作以协议消息形式提交，GameSession 结构上满足）
type BotResponder = games.BotResponder

// PlayRecord 一次出牌记录
type PlayRecord struct {
	Played     rule.ParsedHand
	PlayerName string
	IsLandlord bool
}

// PassInfo 过牌推理素材（主流斗地主 AI 的“不要牌”记忆）：
// 对手在当前一轮中对某手牌选择过牌 → 推断其当时没有同路更大的牌。
// 该玩家再次出牌或新一轮开始时记录失效（由 Controller 清理）。
type PassInfo struct {
	Valid          bool
	Target         rule.ParsedHand                    // 其未能压过的牌（单轮信息，优先使用）
	CumulativePass map[rule.HandType]card.Rank        // 累积过牌声明：{牌型: 历史最高KeyRank}（跨轮次持久化）
}

// GameContext 决策引擎所需的游戏状态
type GameContext struct {
	IsLandlord     bool
	Hand           []card.Card
	BottomCards    []card.Card
	RecentPlays    [2]PlayRecord // [0]=上家(最近), [1]=上上家
	MustPlay       bool
	CanBeat        bool
	PlayerCounts   [2]int            // [0]=上家, [1]=下家 剩余牌数
	RemainingCards map[card.Rank]int // 记牌视图（全副牌 − 已出牌，含自己手牌）；存在=已跟踪，缺失=未跟踪（未知），完整度由难度决定
	PlayedBombs    int               // 全场已打出的炸弹数（含火箭）
	UpIsLandlord   bool              // 上家是地主（自己是地主下家：跑牌位）
	DownIsLandlord bool              // 下家是地主（自己是地主上家：顶牌位）
	HasPlayed      bool              // 本局已出过牌（未开张时放弃让牌配合，避免整局零出牌）
	PassInfos      [2]PassInfo       // [0]=上家 [1]=下家：当前一轮内最近一次过牌的参照牌（手牌预估依据）
	UnbeatenStreak int               // 本方连续跟牌让牌/无法管住对手的次数（≥2 触发拆牌重计划：拆牌压住对手夺取牌权）
	handCountCache *handCountCache   // 一回合内 MinHandCount 结果缓存（内部使用，不对外暴露）
	Personality    BotPersonality    // P0: 机器人人格化参数
}

// BotPersonality 机器人人格化参数（P0 优化）
type BotPersonality struct {
	Name               string  // 人格名称：easy/normal/hard
	AggressionLevel    float64 // 激进程度 (0.3-1.5)：影响领出时的冒险倾向
	RiskTolerance      float64 // 风险承受 (0.5-1.2)：影响炸弹使用和掌权牌消耗
	CooperationWeight  float64 // 队友配合权重 (0.8-1.5)：影响农民配合积极性
	MistakeProbability float64 // 故意犯错概率 (0-0.15)：简单/普通难度下替换为次优候选
}

// DefaultPersonalities 预设的三档难度人格配置
var DefaultPersonalities = map[string]BotPersonality{
	"easy": {
		Name:               "easy",
		AggressionLevel:    0.6,  // 保守，少冒险
		RiskTolerance:      0.7,  // 谨慎，怕被炸
		CooperationWeight:  0.9,  // 稍弱配合
		MistakeProbability: 0.12, // 12% 概率犯低级错误
	},
	"normal": {
		Name:               "normal",
		AggressionLevel:    1.0,  // 均衡
		RiskTolerance:      1.0,  // 标准
		CooperationWeight:  1.0,  // 标准配合
		MistakeProbability: 0.03, // 3% 小概率失误
	},
	"hard": {
		Name:               "hard",
		AggressionLevel:    1.3,  // 激进，敢拆牌
		RiskTolerance:      1.2,  // 大胆用炸
		CooperationWeight:  1.3,  // 强配合
		MistakeProbability: 0.0,  // 不故意犯错
	},
}
