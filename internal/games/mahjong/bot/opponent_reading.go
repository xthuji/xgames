package bot

import "xgames/internal/games/mahjong/rule"

// OpponentReader 对手读牌器：基于舍牌序列推断听牌范围
type OpponentReader struct {
	// 全局已打出的牌（所有玩家）
	GlobalDiscards []int
	// 各玩家的舍牌序列
	PlayerDiscards map[string][]int
	// 各玩家状态
	PlayerStates map[string]*PlayerState
}

// PlayerState 玩家状态
type PlayerState struct {
	DiscardSequence []int // 舍牌顺序
	IsRiichi        bool  // 是否立直
	Pongs           []int // 碰的牌
	TurnNumber      int   // 当前巡数
}

// NewOpponentReader 创建读牌器
func NewOpponentReader() *OpponentReader {
	return &OpponentReader{
		GlobalDiscards: make([]int, 0),
		PlayerDiscards: make(map[string][]int),
		PlayerStates:   make(map[string]*PlayerState),
	}
}

// UpdateDiscard 更新舍牌记录
func (r *OpponentReader) UpdateDiscard(playerID string, tile int) {
	r.GlobalDiscards = append(r.GlobalDiscards, tile)
	if r.PlayerDiscards[playerID] == nil {
		r.PlayerDiscards[playerID] = make([]int, 0)
	}
	r.PlayerDiscards[playerID] = append(r.PlayerDiscards[playerID], tile)

	if state, ok := r.PlayerStates[playerID]; ok {
		state.DiscardSequence = append(state.DiscardSequence, tile)
		state.TurnNumber++
	}
}

// InferWaitRange 推断对手的听牌范围
func (r *OpponentReader) InferWaitRange(playerID string, handCounts []int) map[int]float64 {
	state, ok := r.PlayerStates[playerID]
	if !ok {
		return make(map[int]float64)
	}

	waits := make(map[int]float64)

	// 1. 基于舍牌模式推断
	discardPattern := analyzeDiscardPattern(state.DiscardSequence)

	// 2. 检查可能的两面/坎张/边张听牌
	for tile := 0; tile < rule.NumTypes; tile++ {
		if handCounts[tile] > 0 {
			continue // 手中已有，不可能听
		}

		// 模拟摸上这张牌是否能胡
		testCounts := make([]int, rule.NumTypes)
		copy(testCounts, handCounts)
		testCounts[tile]++

		if rule.IsWinningHand(testCounts) {
			prob := calculateWaitProbability(tile, state, discardPattern, r.GlobalDiscards)
			if prob > 0.2 {
				waits[tile] = prob
			}
		}
	}

	return waits
}

// DiscardPattern 舍牌模式分析结果
type DiscardPattern struct {
	EarlySuits   map[int]int // 早期打出的花色计数
	LateSuits    map[int]int // 后期打出的花色计数
	HonorDiscard bool        // 是否早期打出字牌
	DominantSuit int         // 后期主导花色（-1表示无）
}

// analyzeDiscardPattern 分析舍牌模式
func analyzeDiscardPattern(sequence []int) *DiscardPattern {
	pattern := &DiscardPattern{
		EarlySuits:   make(map[int]int),
		LateSuits:    make(map[int]int),
		HonorDiscard: false,
		DominantSuit: -1,
	}

	midPoint := len(sequence) / 2

	// 分析早期舍牌
	for i, tile := range sequence {
		if i < midPoint {
			suit := rule.SuitOf(tile)
			pattern.EarlySuits[suit]++
			if rule.IsHonor(tile) && i < 3 {
				pattern.HonorDiscard = true
			}
		} else {
			suit := rule.SuitOf(tile)
			pattern.LateSuits[suit]++
		}
	}

	// 检测染手迹象
	maxLateCount := 0
	for suit, count := range pattern.LateSuits {
		if count > maxLateCount && suit < 3 { // 只考虑数牌
			maxLateCount = count
			pattern.DominantSuit = suit
		}
	}

	return pattern
}

// calculateWaitProbability 计算某张牌是听牌的概率
func calculateWaitProbability(tile int, state *PlayerState, pattern *DiscardPattern, globalDiscards []int) float64 {
	prob := 0.5 // 基础概率

	// 1. 该牌在舍牌序列中的位置
	foundIdx := -1
	for i, t := range state.DiscardSequence {
		if t == tile {
			foundIdx = i
			break
		}
	}

	if foundIdx != -1 {
		if foundIdx < len(state.DiscardSequence)/2 {
			prob -= 0.3 // 早期打出，不太可能再听
		} else {
			prob += 0.2 // 晚期打出，可能在调整听口
		}
	}

	// 2. 筋牌关系
	sujiTiles := getSujiTiles(tile)
	for _, s := range sujiTiles {
		if containsInt(state.DiscardSequence, s) {
			prob += 0.15 // 筋牌被打出，该牌听牌概率上升
		}
	}

	// 3. 染手推断
	if pattern.DominantSuit != -1 && rule.SuitOf(tile) == pattern.DominantSuit {
		prob += 0.2 // 同花色牌听牌概率高
	}

	// 4. 字牌早打，可能在做大牌
	if pattern.HonorDiscard && rule.IsNumber(tile) {
		prob += 0.1
	}

	// 5. 剩余张数调整
	remaining := 4 - countInSlice(tile, globalDiscards)
	prob *= float64(remaining) / 4.0

	// 限制范围
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}

	return prob
}

// containsInt 检查切片是否包含某值（已移至 rule.ContainsInt，保留此函数用于向后兼容）
func containsInt(slice []int, val int) bool {
	return rule.ContainsInt(slice, val)
}

// countInSlice 统计某值在切片中出现的次数（已移至 rule.CountInSlice，保留此函数用于向后兼容）
func countInSlice(val int, slice []int) int {
	return rule.CountInSlice(val, slice)
}

// IsDangerousTile 判断某张牌对指定玩家是否危险
func (r *OpponentReader) IsDangerousTile(playerID string, tile int, handCounts []int) bool {
	state, ok := r.PlayerStates[playerID]
	if !ok {
		return false
	}

	// 立直后未现过的牌极度危险（推倒胡无立直，此项恒不触发）
	if state.IsRiichi && !containsInt(r.GlobalDiscards, tile) {
		return true
	}

	// 晚巡且疑似听牌
	if state.TurnNumber > 12 && len(state.DiscardSequence) > 8 {
		// 检查该牌是否在可能的听牌范围内
		waits := r.InferWaitRange(playerID, handCounts)
		if prob, ok := waits[tile]; ok && prob > 0.5 {
			return true
		}
	}

	return false
}
