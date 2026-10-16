package bot

import (
	"math/rand/v2"
	"xgames/internal/games/ddz/card"
)

// intentionalImperfection P0: 故意犯错机制（仅简单/普通难度）
// 以小概率将最优选择替换为次优候选，模拟人类失误
// 限制条件：
//   - 不在残局触发（对手 ≤3 张时保持严谨）
//   - 不犯致命错误（如拆王炸、拆炸弹）
//   - 仅在领出时生效（跟牌回合必须正确应对）
func intentionalImperfection(personality BotPersonality, hand []card.Card, chosen []card.Card, ranked [][]card.Card, gctx GameContext) []card.Card {
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
	
	// 从引擎真实排序中选择次优解（跳过当前选择）；无排序清单（短路分支
	// 领出）时不替换——旧实现按 GenerateHints 原始评分序取次项，与引擎
	// 真实择优序不一致（计划 §Phase 3 修正）
	if len(ranked) < 2 {
		return chosen // 无可靠次优候选，无法替换
	}

	// 找到当前选择在引擎排序中的位置
	chosenIdx := -1
	for i, c := range ranked {
		if cardsEqual(c, chosen) {
			chosenIdx = i
			break
		}
	}

	if chosenIdx < 0 || chosenIdx >= len(ranked)-1 {
		return chosen // 找不到或已是最后一个，无法替换
	}

	// 选择下一个次优候选
	alternative := ranked[chosenIdx+1]

	// 再次检查次优候选是否会导致致命错误
	if breaksRocket(hand, alternative) || breaksBomb(hand, alternative) {
		return chosen // 次优候选也不安全，保持原选择
	}
	
	return alternative
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
