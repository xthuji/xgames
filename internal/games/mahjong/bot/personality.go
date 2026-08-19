package bot

import (
	"math/rand/v2"
	"time"
	"xgames/internal/games/mahjong/rule"
)

// BotPersonality 机器人性格特征
type BotPersonality struct {
	// 基础风格参数 (0.0-1.0)
	Aggression   float64 // 激进程度：影响碰杠倾向
	Conservatism float64 // 保守程度：影响防守阈值
	Patience     float64 // 耐心程度：影响听牌保护轮数

	// 情绪状态（动态变化）
	Mood              MoodState
	ConsecutiveLosses int // 连续输局数
	ConsecutiveDeals  int // 连续被胡数

	// 玩家适应
	PlayerProfiles map[string]*PlayerProfile // 对各玩家的策略档案
}

// MoodState 情绪状态
type MoodState struct {
	Excitement    float64 // 兴奋度（手牌好时升高）
	Caution       float64 // 谨慎度（连续被胡时升高）
	Determination float64 // 决心度（连败时升高）
}

// PlayerProfile 玩家档案（用于适应不同玩家）
type PlayerProfile struct {
	PlayerID        string
	PlayStyle       PlayStyleType // 玩家风格
	AggressionLevel float64       // 该玩家的激进程度
	DiscardPattern  []int         // 舍牌模式
	GameCount       int           // 对局次数
	WinRate         float64       // 对该玩家的胜率
}

// PlayStyleType 玩家风格类型
type PlayStyleType int

const (
	StyleUnknown      PlayStyleType = iota
	StyleAggressive                 // 激进型
	StyleConservative               // 保守型
	StyleBalanced                   // 均衡型
)

// NewBotPersonality 创建机器人性格
func NewBotPersonality(aggression, conservatism, patience float64) *BotPersonality {
	return &BotPersonality{
		Aggression:     clampFloat(aggression, 0, 1),
		Conservatism:   clampFloat(conservatism, 0, 1),
		Patience:       clampFloat(patience, 0, 1),
		Mood:           MoodState{},
		PlayerProfiles: make(map[string]*PlayerProfile),
	}
}

// CalculateThinkDelay 根据决策复杂度计算思考延迟。
// 各档上限必须低于会话侧机器人回合超时（session.afkActDelay = 3s），
// 否则延迟回调晚于回合超时，会被服务端“自动代打”抢先、提交被拒。
// 各档下限不得低于会话侧语音节奏预算（session.minActionGap = 1.5s）：
// 机器人出牌距上一动作播报至少一个预算，保证松弛画音同步（见 docs/architecture.md §10.3.5）。
// 简单决策（孤张字牌）：1.5-1.9s
// 中等决策（部分搭子）：1.6-2s
// 复杂决策（晚巡防守）：2.2-2.8s
func (p *BotPersonality) CalculateThinkDelay(hand []int, turnNumber int, isTenpai bool) time.Duration {
	complexity := p.evaluateDecisionComplexity(hand, turnNumber, isTenpai)

	var minDelay, maxDelay int
	switch complexity {
	case "simple":
		minDelay, maxDelay = 1500, 1900
	case "medium":
		minDelay, maxDelay = 1600, 2000
	case "complex":
		minDelay, maxDelay = 2200, 2800
	default:
		minDelay, maxDelay = 1600, 2000
	}

	delay := minDelay + rand.IntN(maxDelay-minDelay)
	return time.Duration(delay) * time.Millisecond
}

// evaluateDecisionComplexity 评估决策复杂度
func (p *BotPersonality) evaluateDecisionComplexity(hand []int, turnNumber int, isTenpai bool) string {
	counts := rule.CountsFromHand(hand)

	// 统计孤张数量
	orphanCount := 0
	for t := 0; t < rule.NumTypes; t++ {
		if counts[t] == 1 && rule.IsHonor(t) {
			orphanCount++
		} else if counts[t] == 1 && rule.IsNumber(t) {
			// 检查是否有邻居
			hasNeighbor := false
			suit, val := rule.SuitOf(t), rule.ValueOf(t)
			base := suit * rule.NumValues
			for dv := -2; dv <= 2; dv++ {
				if dv == 0 {
					continue
				}
				nv := val + dv
				if nv >= 0 && nv < rule.NumValues && counts[base+nv] > 0 {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				orphanCount++
			}
		}
	}

	// 简单决策：多孤张，无需深思
	if orphanCount >= 3 {
		return "simple"
	}

	// 复杂决策：晚巡且有对手听牌，或已听牌需要选择打点
	if turnNumber > 12 || isTenpai {
		return "complex"
	}

	// 中等决策：其他情况
	return "medium"
}

// UpdateMood 更新情绪状态
func (p *BotPersonality) UpdateMood(hand []int, wasDealtAgainst bool, lostLastGame bool) {
	counts := rule.CountsFromHand(hand)

	// 评估手牌质量（向听数越低越好）
	shanten := rule.ShantenNumber(counts)
	handQuality := 1.0 - float64(shanten)/8.0

	// 兴奋度：手牌好时升高
	p.Mood.Excitement = handQuality*0.7 + p.Mood.Excitement*0.3

	// 谨慎度：连续被胡时升高
	if wasDealtAgainst {
		p.ConsecutiveDeals++
		p.Mood.Caution = minFloat(p.Mood.Caution+0.2, 1.0)
	} else {
		p.ConsecutiveDeals = 0
		p.Mood.Caution *= 0.8
	}

	// 决心度：连败时升高
	if lostLastGame {
		p.ConsecutiveLosses++
		p.Mood.Determination = minFloat(p.Mood.Determination+0.15, 1.0)
	} else {
		p.ConsecutiveLosses = 0
		p.Mood.Determination *= 0.7
	}
}

// GetAdjustedAggression 获取调整后的激进程度（考虑情绪和对手）
func (p *BotPersonality) GetAdjustedAggression(opponentID string) float64 {
	baseAggression := p.Aggression

	// 情绪调整
	baseAggression += p.Mood.Determination * 0.2 // 决心提高激进度
	baseAggression -= p.Mood.Caution * 0.15      // 谨慎降低激进度

	// 对手适应调整
	if profile, ok := p.PlayerProfiles[opponentID]; ok {
		switch profile.PlayStyle {
		case StyleAggressive:
			baseAggression -= 0.1 // 对激进玩家更保守
		case StyleConservative:
			baseAggression += 0.1 // 对保守玩家更激进
		}
	}

	return clampFloat(baseAggression, 0, 1)
}

// GetAdjustedConservatism 获取调整后的保守程度
func (p *BotPersonality) GetAdjustedConservatism() float64 {
	baseConservatism := p.Conservatism

	// 情绪调整
	baseConservatism += p.Mood.Caution * 0.2       // 谨慎提高保守度
	baseConservatism -= p.Mood.Determination * 0.1 // 决心降低保守度

	return clampFloat(baseConservatism, 0, 1)
}

// GetAdjustedPatience 获取调整后的耐心程度
func (p *BotPersonality) GetAdjustedPatience() float64 {
	basePatience := p.Patience

	// 手牌好时降低耐心（想快速胡牌）
	basePatience -= p.Mood.Excitement * 0.15

	return clampFloat(basePatience, 0, 1)
}

// RecordPlayerBehavior 记录玩家行为
func (p *BotPersonality) RecordPlayerBehavior(playerID string, discardSequence []int, won bool) {
	profile, exists := p.PlayerProfiles[playerID]
	if !exists {
		profile = &PlayerProfile{
			PlayerID:  playerID,
			GameCount: 0,
		}
		p.PlayerProfiles[playerID] = profile
	}

	profile.GameCount++
	profile.DiscardPattern = append(profile.DiscardPattern, discardSequence...)

	// 更新胜率
	if profile.GameCount > 0 {
		wins := profile.WinRate * float64(profile.GameCount-1)
		if won {
			wins++
		}
		profile.WinRate = wins / float64(profile.GameCount)
	}

	// 分析玩家风格（基于舍牌模式）
	if len(profile.DiscardPattern) >= 10 {
		profile.PlayStyle = p.analyzePlayStyle(profile.DiscardPattern)
		profile.AggressionLevel = p.calculateAggressionLevel(profile.DiscardPattern)
	}
}

// analyzePlayStyle 分析玩家风格
func (p *BotPersonality) analyzePlayStyle(discards []int) PlayStyleType {
	if len(discards) < 10 {
		return StyleUnknown
	}

	// 统计早期打出的字牌数量（前6张）
	earlyHonors := 0
	for i := 0; i < 6 && i < len(discards); i++ {
		if rule.IsHonor(discards[i]) {
			earlyHonors++
		}
	}

	// 激进型：早期大量打字牌，后期做染手
	if earlyHonors >= 3 {
		return StyleAggressive
	}

	// 保守型：保留字牌作为安全牌
	if earlyHonors <= 1 {
		return StyleConservative
	}

	return StyleBalanced
}

// calculateAggressionLevel 计算玩家激进程度
func (p *BotPersonality) calculateAggressionLevel(discards []int) float64 {
	if len(discards) == 0 {
		return 0.5
	}

	// 基于早期字牌打出比例
	earlyHonors := 0
	earlyTotal := 0
	for i := 0; i < 6 && i < len(discards); i++ {
		earlyTotal++
		if rule.IsHonor(discards[i]) {
			earlyHonors++
		}
	}

	if earlyTotal == 0 {
		return 0.5
	}

	return float64(earlyHonors) / float64(earlyTotal)
}

// 辅助函数
func clampFloat(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
