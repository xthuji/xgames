package bot

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"sync"

	"xgames/internal/games/mahjong/rule"
)

// Engine 麻将决策引擎：向听数优先 + 牌效率打分 + 防守安全度分析
type Engine struct {
	logger *slog.Logger
	// rng 可控失误随机源：nil 时使用全局随机源（生产默认）；
	// 测试注入固定种子以复现难度校准结果（见 TestDifficulty_GradientSimulation）
	rng *rand.Rand
}

// NewEngine 创建决策引擎
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{logger: logger}
}

// NewEngineWithRng 创建带固定随机源的决策引擎（测试注入用，保证决策可复现）
func NewEngineWithRng(logger *slog.Logger, rng *rand.Rand) *Engine {
	return &Engine{logger: logger, rng: rng}
}

// DiscardContext 出牌上下文（包含防守信息）
type DiscardContext struct {
	Hand           []int
	Melds          []MeldInfo
	DiscardedTiles []int            // 全局已打出的牌序列（所有人的弃牌）
	MyDiscards     []int            // 自己打出的牌序列（现物兜底用）
	OpponentModels []OpponentModel  // 对手模型
	TurnNumber     int              // 当前巡数（本方已弃牌数）
	Seen           []int            // 记牌器：各牌可见张数（牌河+面子+自己手牌）；nil 时按全部可用保守处理
	UseFastUkeire  bool             // 是否在模拟模式下使用快速 ukeire（只统计进张种类）
	Difficulty     DifficultyConfig // 难度配置；零值时按引擎既有行为处理（见 DifficultyConfig.normalize）
}

// OpponentModel 对手模型
type OpponentModel struct {
	PlayerID        string
	IsTenpai        bool  // 是否听牌
	IsRiichi        bool  // 是否立直
	DiscardSequence []int // 舍牌顺序
}

// DecideDiscard 出牌决策（简化版，向后兼容：无记牌器视图与防守信息）
func (e *Engine) DecideDiscard(_ context.Context, _ string, hand []int, melds []MeldInfo) int {
	return e.DecideDiscardEnhanced(DiscardContext{Hand: hand, Melds: melds})
}

// DecideDiscardForSim 为模拟优化的出牌决策（使用快速 ukeire）
func (e *Engine) DecideDiscardForSim(_ context.Context, _ string, hand []int, melds []MeldInfo) int {
	return e.DecideDiscardEnhanced(DiscardContext{Hand: hand, Melds: melds, UseFastUkeire: true})
}

// 听牌保护阈值：成胡可能性高时至少保持听牌 7 轮才考虑拆牌，较低时 3 轮
const (
	tingHoldRoundsHigh = 7
	tingHoldRoundsLow  = 3
	// 待牌余量（记牌）不低于该值时视为成胡可能性较高
	tingOutsHigh = 8
)

// TingGuard 听牌保护：托管/机器人听牌后不随意拆牌。
// suggested 为引擎常规决策；若打出它会破坏听牌型，则按已听牌轮数与
// 记牌得到的待牌余量判断成胡可能性：可能性高时至少坚持听牌 7 轮、
// 低时 3 轮，期间改出保听牌中牌效率损失最小的牌。非听牌或允许拆牌时原样返回。
func (e *Engine) TingGuard(hand []int, meldCount int, seen []int, roundsInTenpai int, suggested int) int {
	waits := rule.TenpaiWaits(hand, meldCount)
	if len(waits) == 0 {
		return suggested // 未听牌，无保护需求
	}

	// 常规决策仍保听：直接采纳
	if keepsTenpai(hand, suggested, meldCount) {
		return suggested
	}

	// 打出 suggested 会拆听：根据待牌余量评估成胡可能性，未到等待轮数前坚持听牌
	threshold := tingHoldRoundsLow
	if rule.TenpaiOuts(waits, seen) >= tingOutsHigh {
		threshold = tingHoldRoundsHigh
	}
	if roundsInTenpai >= threshold {
		return suggested // 已等足够轮数，允许按牌效调整听口
	}

	// 改出保听牌：优先待牌余量最大（更容易和出），同余量取牌效损失最小
	best := -1
	bestOuts := -1
	bestLoss := 1 << 30
	counts := rule.CountsFromHand(hand)
	for _, t := range hand {
		if !keepsTenpai(hand, t, meldCount) {
			continue
		}
		rest := make([]int, 0, len(hand)-1)
		removed := false
		for _, h := range hand {
			if !removed && h == t {
				removed = true
				continue
			}
			rest = append(rest, h)
		}
		outs := rule.TenpaiOuts(rule.TenpaiWaits(rest, meldCount), seen)
		loss := discardLoss(counts, seen, t, roundsInTenpai, false) // 听牌保护不使用快速模式
		if outs > bestOuts || (outs == bestOuts && loss < bestLoss) {
			best, bestOuts, bestLoss = t, outs, loss
		}
	}
	if best >= 0 {
		return best
	}
	return suggested
}

// keepsTenpai 打出 discard 后手牌是否仍听牌（仅移除第一张匹配牌）
func keepsTenpai(hand []int, discard int, meldCount int) bool {
	removed := false
	rest := make([]int, 0, len(hand)-1)
	for _, h := range hand {
		if !removed && h == discard {
			removed = true
			continue
		}
		rest = append(rest, h)
	}
	if !removed {
		return false
	}
	return rule.IsTenpaiWithMelds(rest, meldCount)
}

// scoredCandidate 单个候选牌的评分结果
type scoredCandidate struct {
	tile   int
	score  int
	danger int
}

// defenseProgress 攻守转换进度：前 15 巡线性 0→1，之后恒为 1（符合麻将实战经验）
func defenseProgress(turnNumber int) float64 {
	d := float64(turnNumber)
	if d > 15 {
		d = 15
	}
	return d / 15.0
}

// scoreCandidates 对手牌去重候选逐一评分（含难度配置），按手牌顺序返回。
// 评分公式（DifficultyConfig.normalize 后）：
//
//	finalScore = shanten×1000 + efficiencyLoss×(1−0.6×defense) + danger×10×defense
//	defense    = defenseProgress(turn) × DefenseWeight × tenpaiDefenseFactor(手牌向听)
func (e *Engine) scoreCandidates(ctx DiscardContext) []scoredCandidate {
	cfg := ctx.Difficulty.normalize()
	counts := rule.CountsFromHand(ctx.Hand)
	seenTile := make(map[int]bool)
	useFast := ctx.UseFastUkeire || cfg.UkeireMode == "fast"
	candidates := make([]scoredCandidate, 0, len(ctx.Hand))

	// 押引判断：防守权重按手牌进度缩放（听牌保留进攻，见 tenpaiDefenseFactor）
	defFactor := tenpaiDefenseFactor(handBestShanten(ctx.Hand))

	for _, t := range ctx.Hand {
		if seenTile[t] {
			continue
		}
		seenTile[t] = true

		// 基础评分：向听数（主导）+ 牌效率（成型牌型保护与无望牌处理）
		shanten := rule.ShantenAfterDiscard(counts, t)
		if shanten > cfg.ShantenPrecision {
			shanten = cfg.ShantenPrecision // 难度精度：低难度无法区分深向听差距
		}
		efficiencyLoss := discardLoss(counts, ctx.Seen, t, ctx.TurnNumber, useFast)

		// 防守修正：后期加入危险度惩罚，防守权重随难度与手牌进度缩放
		danger := tileDangerLevel(t, counts, ctx.DiscardedTiles, ctx.OpponentModels, ctx.TurnNumber)
		defense := defenseProgress(ctx.TurnNumber) * cfg.DefenseWeight * defFactor

		finalScore := float64(shanten)*1000 +
			float64(efficiencyLoss)*(1-0.6*defense) +
			float64(danger)*10*defense

		candidates = append(candidates, scoredCandidate{tile: t, score: int(finalScore), danger: danger})
	}
	return candidates
}

// pickBestCandidate 按手牌顺序取评分最优候选（严格更优才替换，与既有行为一致）
func pickBestCandidate(cands []scoredCandidate) scoredCandidate {
	best := cands[0]
	for _, c := range cands[1:] {
		if c.score < best.score {
			best = c
		}
	}
	return best
}

// genbutsuFallback 现物兜底：晚巡且有对手疑似听牌、所选牌危险度过高时
// 改打现物（自己打过的牌），避免放铳。触发下界随难度缩放
// （GenbutsuGates：normal 12 巡/危险度 70，hard 提前至 9 巡/60，见 §6）。
// 听牌不弃胡：自己已听牌时保留进攻（评分层防守已足够压住危险牌），
// 未听牌才转入现物防守 —— 避免“无条件弃胡”压低 hard 胜率（自战胡牌率倒挂根因）。
// 返回 (现物牌, 是否触发)。
func genbutsuFallback(ctx DiscardContext, danger int) (int, bool) {
	minTurn, minDanger := ctx.Difficulty.GenbutsuGates()
	if danger <= minDanger || ctx.TurnNumber <= minTurn {
		return 0, false
	}
	if handIsTenpai(ctx.Hand) {
		return 0, false
	}
	for _, opp := range ctx.OpponentModels {
		if opp.IsTenpai {
			if safe := findSafeTile(ctx.Hand, ctx.MyDiscards); safe >= 0 {
				return safe, true
			}
			break
		}
	}
	return 0, false
}

// handBestShanten 手牌最优向听数：弃掉任一张后可达的最小向听数
func handBestShanten(hand []int) int {
	counts := rule.CountsFromHand(hand)
	best := 99 // ShantenAfterDiscard 对非法牌返回 99，不会被选为最优
	seen := make(map[int]bool)
	for _, t := range hand {
		if seen[t] {
			continue
		}
		seen[t] = true
		if s := rule.ShantenAfterDiscard(counts, t); s < best {
			best = s
		}
	}
	return best
}

// handIsTenpai 手牌是否听牌：弃掉任一张后向听数可达 0 即为听牌
func handIsTenpai(hand []int) bool {
	return handBestShanten(hand) == 0
}

// tenpaiDefenseFactor 押引判断：按手牌进度缩放防守权重。
// 听牌时大幅保留进攻（0.3）、一向听适度收紧（0.6）、两向听及以后全力防守（1.0）——
// 避免“晚巡一律弃胡”压低胜率与 EV（hard 自战胡牌率倒挂根因之一）
func tenpaiDefenseFactor(bestShanten int) float64 {
	switch {
	case bestShanten <= 0:
		return 0.3
	case bestShanten == 1:
		return 0.6
	default:
		return 1.0
	}
}

// DecideDiscardEnhanced 增强版出牌决策（含记牌器分析与防守安全度）
func (e *Engine) DecideDiscardEnhanced(ctx DiscardContext) int {
	cands := e.scoreCandidates(ctx)
	if len(cands) == 0 {
		if len(ctx.Hand) > 0 {
			return ctx.Hand[0]
		}
		return 0
	}
	best := pickBestCandidate(cands)
	if safe, ok := genbutsuFallback(ctx, best.danger); ok {
		return safe
	}
	return best.tile
}

// DecideDiscardWithErrors 带可控失误的出牌决策（难度系统 §6.2）：
// 以难度配置的 ErrorRate 概率放弃最优解、改打评分次优的候选牌，
// 失误"像人类判断误差"而非乱打；现物兜底为安全硬规则，不因失误豁免。
func (e *Engine) DecideDiscardWithErrors(ctx DiscardContext) int {
	cfg := ctx.Difficulty.normalize()
	cands := e.scoreCandidates(ctx)
	if len(cands) == 0 {
		if len(ctx.Hand) > 0 {
			return ctx.Hand[0]
		}
		return 0
	}
	best := pickBestCandidate(cands)

	roll := rand.Float64()
	if e.rng != nil {
		roll = e.rng.Float64()
	}
	if cfg.ErrorRate > 0 && roll < cfg.ErrorRate {
		if alt, ok := chooseSuboptimalTile(e.rng, cands, best.tile); ok {
			if safe, fallback := genbutsuFallback(ctx, alt.danger); fallback {
				return safe
			}
			return alt.tile
		}
	}

	if safe, ok := genbutsuFallback(ctx, best.danger); ok {
		return safe
	}
	return best.tile
}

// findSafeTile 在自己打出的牌（现物）中找一张手牌中也有的牌，作为绝对安全牌
// 返回 -1 表示未找到
func findSafeTile(hand []int, myDiscards []int) int {
	// 构建自己打出的牌的集合
	discardSet := make(map[int]bool)
	for _, t := range myDiscards {
		discardSet[t] = true
	}

	// 在手牌中找已打过的牌（现物）
	for _, t := range hand {
		if discardSet[t] {
			return t // 找到现物
		}
	}

	return -1 // 无现物可打
}

// hopelessTurns 部分搭子等待超过该巡数后，若进张余量仍极少则视为无望组织好
const hopelessTurns = 5

// discardLoss 打出 tile 的牌效损失分（基于标准 ukeire 统计 + 启发式兜底）：
// Phase 4 重写：优先使用标准进张数（ukeire），手牌数不足时回退到邻居启发式。
// ukeire = 打出 tile 后，所有能改善向听数的摸牌的（进张种类 × 剩余枚数）之和。
// seen 为各牌可见张数（牌河+面子+自己手牌），nil 时按全部可用保守处理。
// useFast: 是否在模拟模式下使用快速近似版本（只统计进张种类，不乘以剩余枚数）
func discardLoss(counts, seen []int, tile, turn int, useFast bool) int {
	// 已组好的牌型保护：刻子 > 对子（保留原有逻辑，这些是绝对不拆的）
	if counts[tile] >= 3 {
		return 100 // 刻子损失极大，几乎不拆
	}
	if counts[tile] == 2 {
		return 80 // 对子损失大，尽量不拆
	}

	// 字牌无顺子潜力：孤张字牌直接返回 0，优先打出
	if rule.IsHonor(tile) {
		return 0
	}

	// 完整顺子成员，不拆（优化后的判断逻辑）
	if inCompleteRun(counts, tile) {
		return 90 // 完整顺子损失极大
	}

	// 真正的孤张检测：该牌既无对子/刻子，也无任何顺子搭子（邻牌或跳搭）
	if rule.IsTrueOrphan(counts, tile) {
		return 0 // 真孤张，优先打出
	}

	// 计算花色集中度修正
	suitBonus := calculateSuitConcentrationBonus(counts, tile)

	// 计算手牌总数，判断是否适合使用标准 ukeire 统计
	totalTiles := 0
	for _, c := range counts {
		totalTiles += c
	}

	// 手牌数 >= 13 时使用标准 ukeire 统计（天凤牌理系的标准做法）
	var baseLoss int
	if totalTiles >= 13 {
		var ukeire int
		if useFast {
			ukeire = rule.UkeireFast(counts, tile)
		} else {
			ukeire = rule.UkeireAfterDiscard(counts, tile, seen)
		}
		baseLoss = ukeire
	} else {
		// 手牌数 < 13（测试场景或极端情况）：回退到邻居启发式
		baseLoss = discardLossHeuristic(counts, seen, tile, turn)
	}

	// 应用花色集中度修正：减少非主导花色牌的损失分（更容易被丢弃）
	if suitBonus < 0 {
		baseLoss = maxInt(0, baseLoss+suitBonus)
	}

	return baseLoss
}

// calculateSuitConcentrationBonus 计算花色集中度修正值
// 如果某花色占比超过50%，则降低其他花色牌的"损失分"（使其更容易被丢弃）
// 返回值：负数表示应该优先丢弃，正数表示应该保留
func calculateSuitConcentrationBonus(counts []int, tile int) int {
	if rule.IsHonor(tile) {
		return 0 // 字牌不参与花色集中度计算
	}

	// 统计各花色的牌数
	suitCounts := [3]int{} // 0:万, 1:筒, 2:条
	totalNumberTiles := 0

	for t := 0; t < rule.HonorStart; t++ {
		suit := rule.SuitOf(t)
		suitCounts[suit] += counts[t]
		totalNumberTiles += counts[t]
	}

	if totalNumberTiles == 0 {
		return 0
	}

	// 找出主导花色（占比最高的花色）
	dominantSuit := -1
	maxCount := 0
	for i, count := range suitCounts {
		if count > maxCount {
			maxCount = count
			dominantSuit = i
		}
	}

	// 如果主导花色占比超过50%，则对其他花色施加惩罚
	tileSuit := rule.SuitOf(tile)
	concentrationRatio := float64(maxCount) / float64(totalNumberTiles)

	if concentrationRatio > 0.5 && tileSuit != dominantSuit {
		// 非主导花色的牌：根据集中度给予负修正（降低损失分，更容易丢弃）
		// 集中度越高，惩罚越重：-5 到 -20
		bonus := int(-10 * concentrationRatio)
		if bonus < -20 {
			bonus = -20
		}
		return bonus
	}

	return 0
}

// maxInt 返回两个整数中的较大值
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// discardLossHeuristic 基于邻居关系的启发式牌效评估（fallback）
func discardLossHeuristic(counts, seen []int, tile, turn int) int {
	score := 0

	// 未成型的部分搭子：按记牌器评估成组概率
	outs := partialRunOuts(counts, seen, tile)
	if outs == 0 || (turn > hopelessTurns && outs <= 1) {
		return score // 无望组织好：按散牌处理
	}

	// 有望成型的搭子：邻居越近、越多越值得保留
	base := rule.SuitOf(tile) * rule.NumValues
	val := rule.ValueOf(tile)
	for dv := -2; dv <= 2; dv++ {
		if dv == 0 {
			continue
		}
		nv := val + dv
		if nv < 0 || nv >= rule.NumValues {
			continue
		}
		if n := counts[base+nv]; n > 0 {
			score += (3 - abs(dv)) * n * 5
		}
	}
	if outs >= 2 {
		score += 8 // 两向进张（缺首尾）比单向更容易成型
	}
	return score
}

// inCompleteRun tile 是否为完整顺子成员（同花色存在 n,n+1,n+2 连续三张且含 tile）
// 优化：只有当移除该牌会减少完整顺子数量时才给予保护，避免过度保护导致无法拆散冗余牌型
func inCompleteRun(counts []int, tile int) bool {
	if rule.IsHonor(tile) {
		return false
	}

	// 计算当前手牌的完整顺子数量
	beforeCount := countCompleteRuns(counts)

	// 模拟移除该牌后的顺子数量
	testCounts := make([]int, len(counts))
	copy(testCounts, counts)
	testCounts[tile]--
	afterCount := countCompleteRuns(testCounts)

	// 只有当移除后会减少顺子数量时，才认为该牌是"必需"的顺子成员
	return afterCount < beforeCount
}

// countCompleteRuns 计算手牌中完整顺子的数量（每个顺子只计一次）
func countCompleteRuns(counts []int) int {
	totalRuns := 0

	// 遍历三种花色
	for suit := 0; suit < 3; suit++ {
		base := suit * rule.NumValues
		cp := make([]int, rule.NumValues)
		copy(cp, counts[base:base+rule.NumValues])

		// 贪心提取顺子
		for i := 0; i <= 6; i++ {
			if cp[i] > 0 && cp[i+1] > 0 && cp[i+2] > 0 {
				minCount := cp[i]
				if cp[i+1] < minCount {
					minCount = cp[i+1]
				}
				if cp[i+2] < minCount {
					minCount = cp[i+2]
				}
				totalRuns += minCount
				cp[i] -= minCount
				cp[i+1] -= minCount
				cp[i+2] -= minCount
			}
		}
	}

	return totalRuns
}

// partialRunOuts 未成型部分搭子的进张余量（记牌器）：
// 邻搭（n,n+1）缺首尾两向，跳搭（n,n+2）缺中间一张；
// seen 为各牌可见张数（含自己手牌），nil 时按全部可用保守处理。
func partialRunOuts(counts, seen []int, tile int) int {
	if rule.IsHonor(tile) {
		return 0
	}
	suit, val := rule.SuitOf(tile), rule.ValueOf(tile)
	base := suit * rule.NumValues
	at := func(v int) int {
		if v < 0 || v >= rule.NumValues {
			return 0
		}
		return counts[base+v]
	}
	rem := func(v int) int {
		if v < 0 || v >= rule.NumValues {
			return 0
		}
		if seen == nil || base+v >= len(seen) {
			return 4
		}
		r := 4 - seen[base+v]
		if r < 0 {
			r = 0
		}
		return r
	}
	outs := 0
	if at(val-1) > 0 { // 邻搭 n-1,n：缺首尾
		outs += rem(val-2) + rem(val+1)
	}
	if at(val+1) > 0 { // 邻搭 n,n+1：缺首尾
		outs += rem(val-1) + rem(val+2)
	}
	if at(val-2) > 0 { // 跳搭 n-2,n：缺中间
		outs += rem(val - 1)
	}
	if at(val+2) > 0 { // 跳搭 n,n+2：缺中间
		outs += rem(val + 1)
	}
	return outs
}

// DecideAction 动作决策：胡永远接受，碰牌需权衡（含防守视角）。
// meldCount 为本方已鸣面子数（碰/杠各计 1），hand 为不含面子牌的持牌；
// 难度配置的 Aggression 影响碰牌所需的安全牌余量阈值（见 pongSafeThreshold）。
func (e *Engine) DecideAction(ctx context.Context, botName string, hand []int, tile int, meldCount int, cfg DifficultyConfig) (pong bool, win bool) {
	win = rule.CanWinFromDiscardWithMelds(hand, tile, meldCount)
	if win {
		return
	}

	if !rule.CanPong(hand, tile) {
		return
	}

	// 碰后手牌：消耗 2 张同牌；碰来的牌视作摸牌，
	// 剩余张数恰为 14-3*(meldCount+1)（摸牌后口径，可直接做听牌判定）
	rest := make([]int, 0, len(hand)-2)
	removed := 0
	for _, t := range hand {
		if t == tile && removed < 2 {
			removed++
			continue
		}
		rest = append(rest, t)
	}

	// 碰后即进入听牌状态 → 碰
	if rule.IsTenpaiWithMelds(rest, meldCount+1) {
		pong = true
		return
	}

	// 碰后向听数恶化 → 不碰
	before := rule.ShantenWithMelds(rule.CountsFromHand(hand), meldCount)
	after := rule.ShantenWithMelds(rule.CountsFromHand(rest), meldCount+1)
	if after > before {
		return
	}

	// 碰的决策逻辑：
	// 1. 向听数改善（after < before）→ 必碰
	// 2. 向听数不变（after == before）且碰后安全牌余量达难度阈值 → 碰
	// 3. 向听数不变但安全牌不足 → 不碰（防守优先，阈值随难度激进度缩放）
	if after < before || countSafeTilesAfterPong(hand, tile, rule.CountsFromHand(hand)) >= pongSafeThreshold(cfg) {
		pong = true
	}
	return
}

// countSafeTilesAfterPong 评估碰后剩余的安全牌数量
// 安全牌定义：自己打过的牌（现物）+ 对手舍牌中已出现 ≥2 次的牌
func countSafeTilesAfterPong(hand []int, pongTile int, counts []int) int {
	// 简化实现：统计手牌中各牌的"安全度"
	// 现物（手牌中已有的单张）视为最安全
	safeCount := 0

	// 统计手牌中的孤张和弱搭子（这些是潜在的安全牌候选）
	for t := 0; t < rule.NumTypes; t++ {
		if counts[t] == 0 {
			continue
		}

		// 孤张字牌 = 绝对安全
		if rule.IsHonor(t) && counts[t] == 1 {
			safeCount++
			continue
		}

		// 数牌孤张（无邻居）= 相对安全
		if rule.IsNumber(t) && counts[t] == 1 {
			suit, val := rule.SuitOf(t), rule.ValueOf(t)
			base := suit * rule.NumValues
			hasNeighbor := false
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
				safeCount++ // 真孤张，相对安全
			}
		}
	}

	return safeCount
}

// abs 返回整数的绝对值（已移至 rule.Abs，保留此函数用于向后兼容）
func abs(x int) int {
	return rule.Abs(x)
}

// tileDangerLevel 牌的危险度评分（0-100，越高越危险）
func tileDangerLevel(tile int, counts []int, discardedTiles []int, opponents []OpponentModel, turnNumber int) int {
	danger := 50 // 基础危险度

	// 1. 现物安全度：该牌是否已被其他家打出
	discardCount := countDiscards(tile, discardedTiles)
	if discardCount >= 2 {
		danger -= 40 // 多家打过，相对安全
	} else if discardCount == 1 {
		danger -= 25
	}

	// 2. 筋牌理论：1-4-7, 2-5-8, 3-6-9的关联性
	if suit := rule.SuitOf(tile); suit < 3 { // 数牌
		sameSuji := getSujiTiles(tile)
		sujiSafe := true
		for _, s := range sameSuji {
			if countDiscards(s, discardedTiles) == 0 {
				sujiSafe = false
				break
			}
		}
		if sujiSafe {
			danger -= 15
		}
	}

	// 3. 壁牌理论：某牌已出现4张，其相邻牌变安全
	if rule.IsNumber(tile) {
		suit := rule.SuitOf(tile)
		val := rule.ValueOf(tile)
		base := suit * rule.NumValues

		for dv := -2; dv <= 2; dv++ {
			if dv == 0 {
				continue
			}
			nv := val + dv
			if nv >= 0 && nv < rule.NumValues {
				neighbor := base + nv
				if neighbor >= 0 && neighbor < len(counts) && counts[neighbor] == 0 {
					danger -= 10
				}
			}
		}
	}

	// 4. 读牌推断：根据对手舍牌模式判断
	for _, opp := range opponents {
		if opp.IsTenpai { // 对手听牌
			danger += 20
		}

		// 立直后：任何未现过的牌都极度危险（推倒胡无立直，此项恒不触发）
		if opp.IsRiichi && countDiscards(tile, discardedTiles) == 0 {
			danger += 30
		}

		// 基于对手舍牌序列的模式分析：后期突然不打的花色可能在做大牌或染手
		if len(opp.DiscardSequence) > 6 {
			suit := rule.SuitOf(tile)
			if suit < 3 { // 数牌才分析花色
				// 统计对手早期（前 6 张）打出的该花色数量
				earlySuitCount := 0
				for i, t := range opp.DiscardSequence {
					if i >= 6 {
						break
					}
					if rule.SuitOf(t) == suit {
						earlySuitCount++
					}
				}
				// 如果早期很少打该花色，后期也没打过这张牌，可能在做该花色的大牌
				if earlySuitCount <= 1 && !containsInSlice(tile, opp.DiscardSequence) {
					danger += 15
				}
			}
		}
	}

	// 5. 场况判断：晚巡危险度上升
	if turnNumber > 12 {
		danger += 10
	}

	// 限制范围
	if danger < 0 {
		danger = 0
	}
	if danger > 100 {
		danger = 100
	}

	return danger
}

// countDiscards 统计某张牌已被打出的次数
func countDiscards(tile int, discardedTiles []int) int {
	count := 0
	for _, t := range discardedTiles {
		if t == tile {
			count++
		}
	}
	return count
}

// getSujiTiles 获取同筋牌（1-4-7, 2-5-8, 3-6-9）
func getSujiTiles(tile int) []int {
	if rule.IsHonor(tile) {
		return nil // 字牌无筋
	}

	suit := rule.SuitOf(tile)
	val := rule.ValueOf(tile)
	base := suit * rule.NumValues

	var suji []int
	// 同一筋线上的其他两张牌
	mod := val % 3
	for v := mod; v < rule.NumValues; v += 3 {
		if v != val {
			suji = append(suji, base+v)
		}
	}

	return suji
}

// containsInSlice 检查切片是否包含某值（已移至 rule.ContainsInt，保留此函数用于向后兼容）
func containsInSlice(val int, slice []int) bool {
	return rule.ContainsInt(slice, val)
}

// DecideDiscardParallel 并行化出牌决策（适用于困难模式或高性能场景）
// 使用 goroutine 并行评估多张候选牌，充分利用多核 CPU
// 预期收益：决策时间减少 40-50%（取决于 CPU 核心数）
func (e *Engine) DecideDiscardParallel(ctx DiscardContext) int {
	counts := rule.CountsFromHand(ctx.Hand)
	seen := make(map[int]bool)

	// 收集所有唯一候选牌
	uniqueTiles := make([]int, 0, len(ctx.Hand))
	for _, t := range ctx.Hand {
		if !seen[t] {
			seen[t] = true
			uniqueTiles = append(uniqueTiles, t)
		}
	}

	// 如果候选牌太少，不需要并行化
	if len(uniqueTiles) <= 2 {
		return e.DecideDiscardEnhanced(ctx)
	}

	// 确定并行度（最多使用 CPU 核心数的一半）
	numCPU := runtime.NumCPU()
	maxWorkers := numCPU / 2
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if maxWorkers > len(uniqueTiles) {
		maxWorkers = len(uniqueTiles)
	}

	// 并行评估所有候选牌
	type result struct {
		tile   int
		score  int
		danger int
	}

	results := make([]result, len(uniqueTiles))
	var wg sync.WaitGroup

	// 押引判断：防守权重按手牌进度缩放（听牌保留进攻，见 tenpaiDefenseFactor）
	defFactor := tenpaiDefenseFactor(handBestShanten(ctx.Hand))

	// 创建工作池
	jobs := make(chan int, len(uniqueTiles))

	// 启动工作协程
	for w := 0; w < maxWorkers; w++ {
		go func() {
			for tileIdx := range jobs {
				t := uniqueTiles[tileIdx]

				// 创建独立的 counts 副本，避免竞态条件
				localCounts := make([]int, rule.NumTypes)
				copy(localCounts, counts)

				// 计算评分（难度参数：精度截断 + 快速 ukeire + 防守权重）
				cfg := ctx.Difficulty.normalize()
				shanten := rule.ShantenAfterDiscard(localCounts, t)
				if shanten > cfg.ShantenPrecision {
					shanten = cfg.ShantenPrecision
				}
				efficiencyLoss := discardLoss(localCounts, ctx.Seen, t, ctx.TurnNumber, ctx.UseFastUkeire || cfg.UkeireMode == "fast")
				danger := tileDangerLevel(t, localCounts, ctx.DiscardedTiles, ctx.OpponentModels, ctx.TurnNumber)

				defense := defenseProgress(ctx.TurnNumber) * cfg.DefenseWeight * defFactor

				finalScore := float64(shanten)*1000 +
					float64(efficiencyLoss)*(1-0.6*defense) +
					float64(danger)*10*defense

				results[tileIdx] = result{
					tile:   t,
					score:  int(finalScore),
					danger: danger,
				}

				wg.Done()
			}
		}()
	}

	// 分发任务
	wg.Add(len(uniqueTiles))
	for i := range uniqueTiles {
		jobs <- i
	}
	close(jobs)

	// 等待所有任务完成
	wg.Wait()

	// 找出最佳候选
	bestIdx := 0
	bestScore := results[0].score
	for i := 1; i < len(results); i++ {
		if results[i].score < bestScore {
			bestScore = results[i].score
			bestIdx = i
		}
	}

	bestTile := results[bestIdx].tile
	bestDanger := results[bestIdx].danger

	// 防守现物兜底策略
	hasTenpaiOpponent := false
	for _, opp := range ctx.OpponentModels {
		if opp.IsTenpai {
			hasTenpaiOpponent = true
			break
		}
	}

	if hasTenpaiOpponent && ctx.TurnNumber > 12 && bestDanger > 70 {
		safeTile := findSafeTile(ctx.Hand, ctx.MyDiscards)
		if safeTile >= 0 {
			return safeTile
		}
	}

	return bestTile
}
