package bot

import (
	"math/rand/v2"
	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// intentionalImperfection P0: 故意犯错机制（仅简单/普通难度）
// 以小概率将最优选择替换为次优候选，模拟人类失误
// 限制条件：
//   - 不在残局触发（对手 ≤3 张时保持严谨）
//   - 不犯致命错误（如拆王炸、拆炸弹）
//   - 仅在领出时生效（跟牌回合必须正确应对）
func intentionalImperfection(personality BotPersonality, hand []card.Card, chosen []card.Card, candidates []rule.Hint, gctx GameContext) []card.Card {
	// P0 硬规则：清空手牌的必胜出牌永不替换（胜利优先级阶梯最高层）
	if len(chosen) == len(hand) {
		return chosen
	}

	// 残局不犯错（对手牌很少时必须严谨）
	if getMinOpponentCount(gctx) <= 3 {
		return chosen
	}
	
	// 根据人格参数决定是否犯错
	if rand.Float64() >= personality.MistakeProbability {
		return chosen // 不犯错，返回原选择
	}
	
	// 检查是否会犯致命错误（拆王炸、拆炸弹）
	if breaksRocket(hand, chosen) || breaksBomb(hand, chosen) {
		return chosen // 避免致命错误
	}
	
	// 从候选中选择次优解（跳过最优解）
	if len(candidates) < 2 {
		return chosen // 只有一个候选，无法替换
	}
	
	// 找到当前选择在候选中的位置
	chosenIdx := -1
	for i, c := range candidates {
		if cardsEqual(c.Cards, chosen) {
			chosenIdx = i
			break
		}
	}
	
	if chosenIdx < 0 || chosenIdx >= len(candidates)-1 {
		return chosen // 找不到或已是最后一个，无法替换
	}
	
	// 选择下一个次优候选
	alternative := candidates[chosenIdx+1]
	
	// 再次检查次优候选是否会导致致命错误
	if breaksRocket(hand, alternative.Cards) || breaksBomb(hand, alternative.Cards) {
		return chosen // 次优候选也不安全，保持原选择
	}
	
	return alternative.Cards
}

// cardsEqual 判断两组卡牌是否相同
func cardsEqual(a, b []card.Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// getMinOpponentCount 返回对手中最少剩余牌数（避免与 engine.go 中的函数冲突）
func getMinOpponentCount(gctx GameContext) int {
	minCount := 999
	for _, c := range gctx.PlayerCounts {
		if c > 0 && c < minCount {
			minCount = c
		}
	}
	return minCount
}
