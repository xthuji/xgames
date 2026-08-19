package bot

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"runtime"
	"sort"
	"sync"
	"time"

	"xgames/internal/games/chess/rule"
)

// Engine 中国象棋决策引擎：NegaMax + α-β 剪枝 + 迭代加深 + 置换表 + 历史启发 + 杀手启发 + 位置估值。
type Engine struct {
	logger *slog.Logger
}

// NewEngine 创建决策引擎
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{logger: logger}
}

// 子力分值
var pieceValues = map[int]int{
	1: 10000, // 将/帅
	2: 600,   // 车
	3: 270,   // 马
	4: 285,   // 炮
	5: 120,   // 相/象
	6: 110,   // 仕/士
	7: 30,    // 兵/卒
}

// 搜索参数
const (
	maxSearchDepth = 4 // 最大搜索深度
	kingValue      = 10000
	checkBonus     = 50

	quiescenceDepth = 4  // 静态搜索最大延伸深度
	deltaMargin     = 200  // delta 剪枝安全余量（覆盖将军加分等评估波动）
	searchTimeMs    = 2000 // 搜索时间预算（毫秒）；迭代加深超时时停止加深
	checkProbeLimit = 8    // 将军检测限流：仅对基础分前 N 的候选模拟走子检测将军（P2 热路径优化）

	contemptPlyThreshold  = 16  // 求和倾向启用阈值：halfmoveClock ≥ 16 ply（8 回合，约限着线 60%）时生效
	contemptWinScore      = 100 // 优势方阈值：根评估领先 ≥ 此值时优先吃子重置限着计数
	contemptCaptureMargin = 200 // 吃子候选与最优分的可接受差距（约一马价值内不损失优势）
)

// DecideMove 走子决策（含开局库预热 + 并发优化）。
// 未携带难度配置时按引擎既有行为决策（零值 DifficultyConfig 语义）。
func (e *Engine) DecideMove(ctx context.Context, botName string, board []int, camp int) (int, int, int, int) {
	d := e.DecideMoveDetailed(ctx, botName, board, camp, DifficultyConfig{})
	return d.FromRow, d.FromCol, d.ToRow, d.ToCol
}

// DecideMoveDetailed 落子决策（含难度配置与决策详情，无实战上下文）。
// 复盘分析等离线场景使用；实战对局建议走 DecideMoveWithContext 传入判和上下文。
func (e *Engine) DecideMoveDetailed(_ context.Context, _ string, board []int, camp int, cfg DifficultyConfig) DecisionDetail {
	return e.DecideMoveWithContext(context.Background(), "", board, camp, cfg, GameContext{})
}

// DecideMoveWithContext 落子决策（含难度配置、决策详情与实战判和上下文）。
// 决策链：开局库（难度概率）→ 迭代加深（时间预算）→ 求和倾向（contempt）→ Top-N softmax 扰动。
// RandomTopN>1 时在根候选前 N 名中按 softmax 概率选择（无紧迫战术胜机时），
// 保证"失误不离谱"；TopMoves 供复盘记录真实决策数据。
//
// 判和感知（业界引擎标准做法）：
//   - gctx.History 传入搜索：实战中已重复两次的局面计 0 分，劣势方自然倾向求和、优势方自然回避；
//   - gctx.HalfmoveClock 逼近自然限着线时，优势方优先吃子重置计数，避免被 13 回合判和。
func (e *Engine) DecideMoveWithContext(_ context.Context, _ string, board []int, camp int, cfg DifficultyConfig, gctx GameContext) DecisionDetail {
	cfg = cfg.normalize()
	// 收官粗糙（拟人化，Phase 2）：大优势局面下降 1 层搜索深度，模拟人类优势下的松懈
	// （静态评估领先 ≥5000 约等于多一车；easy 档 2 层不再降）
	cfg = applyEndgameCoarsening(cfg, board, camp)
	detail := DecisionDetail{Difficulty: cfg.Name}

	// 开局库查找：按难度概率使用（精确局面命中，中盘不会误命中）
	if rand.Float64() < cfg.OpeningBookRate {
		if move, ok := globalChessOpeningBook.Lookup(board, camp); ok {
			detail.FromRow, detail.FromCol = move.FromRow, move.FromCol
			detail.ToRow, detail.ToCol = move.ToRow, move.ToCol
			detail.BookHit = true
			detail.TopMoves = []ScoredMove{{move.FromRow, move.FromCol, move.ToRow, move.ToCol, 0}}
			return detail
		}
	}

	moves := rule.AllLegalMoves(board, camp)
	if len(moves) == 0 {
		return detail
	}

	bestMove := moves[0]
	bestScore := -1 << 30

	// 置换表跨迭代复用：在循环外创建，每轮迭代加深递增 generation
	tt := newChessTranspositionTable()
	// 历史/杀手启发表跨迭代积累（P2）：优着先搜预热置换表，后续着法剪枝更充分
	hh := newHistoryTable()
	kh := newKillerTable()
	startTime := time.Now()
	boardHash := zobristHash(board, camp)

	// 实战局面历史（含当前局面）：传入根搜索使重复局面按判和计 0 分
	gameHistory := gctx.History

	var rootResults []ScoredMove // 最后一层完整迭代的根候选评分
	completedDepth := 0

	// 迭代加深（并发优化）
	for depth := 1; depth <= cfg.MaxDepth; depth++ {
		// 检查时间预算：SearchTimeMs > 0 时启用，= 0 则不限时（跑满 MaxDepth）
		if cfg.SearchTimeMs > 0 && depth > 1 && time.Since(startTime).Milliseconds() > int64(cfg.SearchTimeMs) {
			break
		}

		tt.advanceGeneration() // 进入新世代，旧条目自动淘汰

		// 并发评估所有着法
		type scoredResult struct {
			move  rule.Move
			score int
		}

		numWorkers := minInt(runtime.NumCPU(), len(moves))
		chunkSize := (len(moves) + numWorkers - 1) / numWorkers

		resultsCh := make(chan scoredResult, len(moves))
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			start := w * chunkSize
			end := start + chunkSize
			if end > len(moves) {
				end = len(moves)
			}

			wg.Add(1)
			go func(startIdx, endIdx int) {
				defer wg.Done()

				for i := startIdx; i < endIdx; i++ {
					m := moves[i]
					nb := rule.CopyBoard(board)
					captured := rule.ApplyMoveWithCapture(nb, m)
					// 增量 Zobrist（P2）：从父哈希走一步得到子哈希，省去全盘重算
					childHash := zobristStep(boardHash, rule.Idx(m.FromRow, m.FromCol), rule.Idx(m.ToRow, m.ToCol), board[rule.Idx(m.FromRow, m.FromCol)], captured)
					// 实战历史 + 子局面哈希：子节点可对照实战重复计数判和（复制切片避免并发别名）
					var childHistory []uint64
					if len(gameHistory) > 0 {
						childHistory = make([]uint64, len(gameHistory)+1)
						copy(childHistory, gameHistory)
						childHistory[len(gameHistory)] = childHash
					}
					score := -negamax(nb, depth-1, -1<<30, 1<<30, rule.CampRed+rule.CampBlack-camp, childHash, tt, hh, kh, childHistory)
					resultsCh <- scoredResult{m, score}
				}
			}(start, end)
		}

		// 等待所有worker完成并关闭channel
		go func() {
			wg.Wait()
			close(resultsCh)
		}()

		// 收集结果并选择最佳着法
		depthResults := make([]ScoredMove, 0, len(moves))
		for result := range resultsCh {
			depthResults = append(depthResults, ScoredMove{
				result.move.FromRow, result.move.FromCol,
				result.move.ToRow, result.move.ToCol,
				result.score,
			})
			if result.score > bestScore {
				bestScore = result.score
				bestMove = result.move
			}
		}
		rootResults = depthResults
		completedDepth = depth

		// PV 复用（P2）：按本轮评分降序重排根着法搜索顺序，优着先搜为后续着法预热置换表
		sort.Slice(rootResults, func(i, j int) bool { return rootResults[i].Score > rootResults[j].Score })
		for i, sm := range rootResults {
			moves[i] = rule.Move{FromRow: sm.FromRow, FromCol: sm.FromCol, ToRow: sm.ToRow, ToCol: sm.ToCol}
		}
	}

	detail.Depth = completedDepth

	// 求和倾向（contempt，业界引擎标准做法）：自然限着计数逼近判和线时调整根选择——
	// 优势方优先选吃子候选（吃子重置限着计数，避免被 13 回合自然限着判和）；
	// 落后方不干预：实战历史传入搜索后重复局面计 0 分，劣势方天然倾向求和。
	if gctx.HalfmoveClock >= contemptPlyThreshold && bestScore >= contemptWinScore && len(rootResults) > 1 {
		if m, s, ok := bestCaptureMove(rootResults, board, bestScore); ok {
			bestMove = m
			bestScore = s
		}
	}

	// Top-N 随机扰动（难度系统）：无紧迫战术胜机（分值低于将死阈值）时生效，
	// 保证必胜/必杀着法永远不被扰动掉
	chosen := bestMove
	chosenScore := bestScore
	if cfg.RandomTopN > 1 && bestScore < 90000 && len(rootResults) > 1 {
		if m, s, ok := softmaxTopNChess(rootResults, cfg.RandomTopN); ok {
			chosen = rule.Move{FromRow: m.FromRow, FromCol: m.FromCol, ToRow: m.ToRow, ToCol: m.ToCol}
			chosenScore = s
		}
	}

	// 排序根候选，保留 Top-5
	sort.Slice(rootResults, func(i, j int) bool { return rootResults[i].Score > rootResults[j].Score })
	if len(rootResults) > 5 {
		rootResults = rootResults[:5]
	}

	detail.FromRow, detail.FromCol = chosen.FromRow, chosen.FromCol
	detail.ToRow, detail.ToCol = chosen.ToRow, chosen.ToCol
	detail.Score = chosenScore
	detail.TopMoves = rootResults
	return detail
}

// bestCaptureMove 从根候选中找分值最高的吃子着法（与最优分差距在 contemptCaptureMargin 内）。
// 供求和倾向：限着计数逼近判和线时，优势方用吃子重置计数。
func bestCaptureMove(results []ScoredMove, board []int, bestScore int) (rule.Move, int, bool) {
	best := rule.Move{}
	bestCap := -1 << 30
	for _, sm := range results {
		if board[rule.Idx(sm.ToRow, sm.ToCol)] == rule.Empty {
			continue // 非吃子
		}
		if bestScore-sm.Score > contemptCaptureMargin {
			continue // 损失优势过大，不接受
		}
		if sm.Score > bestCap {
			best = rule.Move{FromRow: sm.FromRow, FromCol: sm.FromCol, ToRow: sm.ToRow, ToCol: sm.ToCol}
			bestCap = sm.Score
		}
	}
	if bestCap == -1<<30 {
		return rule.Move{}, 0, false
	}
	return best, bestCap, true
}

// softmaxTopNChess 从根候选前 N 名中按 softmax 概率选择。
// 温度取前 N 名分数极差的 1/4（下限 1），避免大分差下退化为确定性选择；
// 返回选中的着法及其评分。候选不足 2 个时返回 false（维持最优）。
func softmaxTopNChess(results []ScoredMove, n int) (ScoredMove, int, bool) {
	top := make([]ScoredMove, len(results))
	copy(top, results)
	sort.Slice(top, func(i, j int) bool { return top[i].Score > top[j].Score })
	if len(top) > n {
		top = top[:n]
	}
	if len(top) < 2 {
		return ScoredMove{}, 0, false
	}

	temp := (top[0].Score - top[len(top)-1].Score) / 4
	if temp < 1 {
		temp = 1
	}

	weights := make([]float64, len(top))
	total := 0.0
	for i, t := range top {
		w := math.Exp(float64(t.Score-top[0].Score) / float64(temp))
		weights[i] = w
		total += w
	}

	pick := rand.Float64() * total
	var acc float64
	for i, w := range weights {
		acc += w
		if pick <= acc {
			return top[i], top[i].Score, true
		}
	}
	return top[0], top[0].Score, true
}

// applyEndgameCoarsening 收官粗糙（拟人化，Phase 2）：大优势局面下降 1 层搜索深度，
// 模拟人类优势下的松懈。静态评估领先 ≥600 分约等于多一车；easy 档 2 层不再降。
func applyEndgameCoarsening(cfg DifficultyConfig, board []int, camp int) DifficultyConfig {
	if cfg.MaxDepth > 2 && evaluate(board, camp) >= 600 {
		cfg.MaxDepth--
	}
	return cfg
}

// negamax NegaMax + α-β 剪枝 + move ordering + 置换表（depth/bound）+ 重复局面检测
// hash 由调用方增量计算传入（根层 zobristHash，子层 zobristStep），省去每节点全盘重算（P2）
func negamax(board []int, depth, alpha, beta, camp int, hash uint64, tt *chessTranspositionTable, hh *historyTable, kh *killerTable, history []uint64) int {
	// 重复局面检测：同一哈希出现 ≥3 次 → 判和
	repeatCount := 0
	for _, h := range history {
		if h == hash {
			repeatCount++
		}
	}
	if repeatCount >= 2 {
		return 0 // 三次重复判和
	}

	// 置换表查找：如果找到且深度足够，根据边界类型决定是否可用
	if val, foundDepth, bound, ok := tt.get(hash); ok && foundDepth >= depth {
		switch bound {
		case boundExact:
			return val
		case boundLower:
			if val > alpha {
				alpha = val
			}
		case boundUpper:
			if val < beta {
				beta = val
			}
		}
		if alpha >= beta {
			return val
		}
	}

	opp := rule.CampRed + rule.CampBlack - camp

	moves := rule.AllLegalMoves(board, camp)
	if len(moves) == 0 {
		if rule.IsInCheck(board, camp) {
			return -90000 - depth // 被将死
		}
		return -90000 - depth // 困毙判负（中国象棋困毙同负，与 session 侧 IsStalemate 判负口径一致）
	}

	if depth == 0 {
		// 叶节点转入静态搜索：延伸吃子/应将至安静局面再评估，消除水平线效应
		return quiescence(board, moves, alpha, beta, camp, quiescenceDepth, hash, append(history, hash))
	}

	// Move ordering：按历史启发 / 杀手启发 / 吃子优先排序
	orderMoves(moves, board, camp, hh, kh, depth)

	// 空着裁剪（Null Move Pruning）：己方不被将军且 depth≥3 时，尝试"跳过一手"验证
	inCheck := rule.IsInCheck(board, camp)
	if !inCheck && depth >= 3 {
		// 构造空着后的局面：交换阵营，不实际走子；空着哈希 = 仅翻转执子方
		nullHash := hash ^ zobristSide
		nullScore := -negamax(board, depth-1-2, -beta, -beta+1, opp, nullHash, tt, hh, kh, history) // R=2 缩减深度
		if nullScore >= beta {
			return beta // 空着仍能守住 beta，当前节点可剪枝
		}
	}

	best := -100000
	originalAlpha := alpha
	moveCount := 0
	newHistory := append(history, hash) // 追加当前哈希到历史
	for _, m := range moves {
		moveCount++
		nb := rule.CopyBoard(board)
		captured := rule.ApplyMoveWithCapture(nb, m)
		// 增量 Zobrist（P2）：从父哈希走一步得到子哈希
		childHash := zobristStep(hash, rule.Idx(m.FromRow, m.FromCol), rule.Idx(m.ToRow, m.ToCol), board[rule.Idx(m.FromRow, m.FromCol)], captured)

		// LMR（Late Move Reductions）：后半段安静着法降深度搜索
		reduction := 0
		if moveCount > 4 && captured == 0 && !kh.isKiller(m, depth) {
			// 非吃子、非杀手、排序靠后 → 降 1 层
			reduction = 1
			if moveCount > 8 {
				reduction = 2 // 更靠后的再降 1 层
			}
		}

		searchDepth := depth - 1 - reduction
		if searchDepth < 0 {
			searchDepth = 0
		}

		score := -negamax(nb, searchDepth, -beta, -alpha, opp, childHash, tt, hh, kh, newHistory)

		// LMR 重新搜索：如果降深度后仍超过 alpha，用全深度重新验证
		if reduction > 0 && score > alpha {
			score = -negamax(nb, depth-1, -beta, -alpha, opp, childHash, tt, hh, kh, newHistory)
		}

		if score > best {
			best = score
		}
		if score > alpha {
			alpha = score
		}
		if alpha >= beta {
			// 杀手启发：导致剪枝的好着
			kh.add(m, depth)
			break
		}

		// 历史启发：被截断的好着
		if score > originalAlpha && captured == 0 {
			hh.add(m, depth)
		}
	}

	// 存入置换表：根据最终 alpha/beta 关系确定边界类型
	var bound ttBoundType
	if best <= originalAlpha {
		bound = boundUpper // fail-low
	} else if best >= beta {
		bound = boundLower // fail-high
	} else {
		bound = boundExact // exact value within window
	}
	tt.put(hash, best, depth, bound)
	return best
}

// quiescence 静态搜索：在叶子处延伸吃子/应将着法直至安静局面再评估，
// 避免深度耗尽时正处于交换序列中间导致评估失真（水平线效应）。
// moves 为当前局面 camp 方的合法走法，可为 nil（内部自行生成）。
// hash 由调用方增量传入（P2）；history 为历史局面哈希序列，用于重复局面检测。
func quiescence(board []int, moves []rule.Move, alpha, beta, camp, depth int, hash uint64, history []uint64) int {
	// 重复局面检测
	repeatCount := 0
	for _, h := range history {
		if h == hash {
			repeatCount++
		}
	}
	if repeatCount >= 2 {
		return 0 // 三次重复判和
	}

	opp := rule.CampRed + rule.CampBlack - camp
	inCheck := rule.IsInCheck(board, camp)

	best := -100000
	if inCheck {
		// 被将军：stand-pat 无效，必须搜索全部应将着法（全量合法走法）
		if moves == nil {
			moves = rule.AllLegalMoves(board, camp)
		}
		if len(moves) == 0 {
			return -90000 - depth // 被将死
		}
		if depth <= -quiescenceDepth {
			// 应将链过深（连将循环等极端情形），直接静态评估收敛
			return evaluate(board, camp)
		}
	} else {
		standPat := evaluate(board, camp)
		if depth <= 0 {
			return standPat
		}
		// stand-pat：允许选择"不再走子"，当前评估即为下界
		if standPat >= beta {
			return standPat
		}
		if standPat > alpha {
			alpha = standPat
		}
		best = standPat // 不吃也是合法选择

		// 只延伸吃子着法：首节点复用上层已生成的合法走法，深层节点仅生成吃子
		var captures []rule.Move
		if moves != nil {
			for _, m := range moves {
				if m.Captured != rule.Empty {
					captures = append(captures, m)
				}
			}
		} else {
			captures = rule.AllLegalCaptures(board, camp)
		}
		if len(captures) == 0 {
			return standPat // 安静局面，无吃子可延伸
		}
		sortCaptures(captures, board)

		// delta 剪枝：吃子收益加安全余量仍追不上 alpha 的着法直接跳过
		filtered := captures[:0]
		for _, m := range captures {
			if standPat+pieceValue(m.Captured)+deltaMargin >= alpha {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) == 0 {
			return standPat
		}
		moves = filtered
	}

	newHistory := append(history, hash)
	for _, m := range moves {
		nb := rule.CopyBoard(board)
		captured := rule.ApplyMoveWithCapture(nb, m)
		// 增量 Zobrist（P2）
		childHash := zobristStep(hash, rule.Idx(m.FromRow, m.FromCol), rule.Idx(m.ToRow, m.ToCol), board[rule.Idx(m.FromRow, m.FromCol)], captured)
		score := -quiescence(nb, nil, -beta, -alpha, opp, depth-1, childHash, newHistory)
		if score > best {
			best = score
		}
		if score > alpha {
			alpha = score
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

// sortCaptures 吃子着法按 MVV-LVA 降序排列（吃子数通常很少，插入排序足够）
func sortCaptures(captures []rule.Move, board []int) {
	type scoredMove struct {
		move  rule.Move
		score int
	}
	scored := make([]scoredMove, len(captures))
	for i, m := range captures {
		attacker := board[rule.Idx(m.FromRow, m.FromCol)]
		scored[i] = scoredMove{m, pieceValue(m.Captured)*100 - pieceValue(attacker)}
	}
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].score > scored[j-1].score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}
	for i, s := range scored {
		captures[i] = s.move
	}
}

// evaluate 局面评估（从 camp 视角）
func evaluate(board []int, camp int) int {
	// 简单和棋局面：双方均无车马炮兵卒（只剩帅仕相），任何一方都无法将死对方 → 和棋 0 分
	if rule.InsufficientMaterial(board) {
		return 0
	}
	myMaterial := 0
	enemyMaterial := 0
	enemy := rule.CampRed + rule.CampBlack - camp

	for r := 0; r < rule.Rows; r++ {
		for c := 0; c < rule.Cols; c++ {
			p := board[rule.Idx(r, c)]
			if p == rule.Empty {
				continue
			}
			val := pieceValue(p) + positionBonus(p, r, c, camp)
			if rule.CampOf(p) == camp {
				myMaterial += val
			} else {
				enemyMaterial += val
			}
		}
	}

	score := myMaterial - enemyMaterial

	// 将军动态
	if rule.IsInCheck(board, enemy) {
		score += checkBonus
	}
	if rule.IsInCheck(board, camp) {
		score -= checkBonus * 2
	}

	// === P2 增强项 ===

	// 1. 将安全分：九宫守子数（士/象在位加分）
	score += kingSafety(board, camp) - kingSafety(board, enemy)

	// 2. 兵形分：过河兵协同加分
	score += pawnStructure(board, camp) - pawnStructure(board, enemy)

	// 3. 机动性粗估：车/马/炮的可攻击格数
	score += mobility(board, camp) - mobility(board, enemy)

	return score
}

// kingSafety 将安全分：九宫内士/象数量加分，将帅暴露度减分
func kingSafety(board []int, camp int) int {
	score := 0
	generalRow, generalCol := findGeneralPos(board, camp)
	if generalRow < 0 {
		return -500 // 将帅缺失，极危险
	}

	// 九宫范围
	minR, maxR := 0, 2
	if camp == rule.CampRed {
		minR, maxR = 7, 9
	}

	// 统计九宫内己方士/象数量
	defenders := 0
	for r := minR; r <= maxR; r++ {
		for c := 3; c <= 5; c++ {
			p := board[rule.Idx(r, c)]
			if p != rule.Empty && rule.CampOf(p) == camp {
				t := rule.TypeOf(p)
				if t == 5 || t == 6 { // 士或象
					defenders++
				}
			}
		}
	}
	score += defenders * 30 // 每个守子 +30

	// 将帅周围 8 格的空格数（暴露度惩罚）
	exposed := 0
	for dr := -1; dr <= 1; dr++ {
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			nr, nc := generalRow+dr, generalCol+dc
			if rule.InBounds(nr, nc) && board[rule.Idx(nr, nc)] == rule.Empty {
				exposed++
			}
		}
	}
	score -= exposed * 10 // 每空一格 -10

	return score
}

// findGeneralPos 查找阵营的将/帅位置
func findGeneralPos(board []int, camp int) (int, int) {
	g := rule.GeneralOf(camp)
	for r := 0; r < rule.Rows; r++ {
		for c := 0; c < rule.Cols; c++ {
			if board[rule.Idx(r, c)] == g {
				return r, c
			}
		}
	}
	return -1, -1
}

// pawnStructure 兵形分：过河兵协同加分（相邻过河兵互相保护）
func pawnStructure(board []int, camp int) int {
	score := 0
	for r := 0; r < rule.Rows; r++ {
		for c := 0; c < rule.Cols; c++ {
			p := board[rule.Idx(r, c)]
			if p == rule.Empty || rule.CampOf(p) != camp || rule.TypeOf(p) != 7 {
				continue
			}
			// 判断是否过河
			crossed := false
			if camp == rule.CampRed && r <= 4 {
				crossed = true
			}
			if camp == rule.CampBlack && r >= 5 {
				crossed = true
			}
			if !crossed {
				continue
			}

			// 检查左右相邻是否有己方过河兵（协同加分）
			for _, dc := range []int{-1, 1} {
				nc := c + dc
				if rule.InBounds(r, nc) {
					neighbor := board[rule.Idx(r, nc)]
					if neighbor != rule.Empty && rule.CampOf(neighbor) == camp && rule.TypeOf(neighbor) == 7 {
						// 邻居也是过河兵
						nCrossed := false
						if camp == rule.CampRed && r <= 4 {
							nCrossed = true
						}
						if camp == rule.CampBlack && r >= 5 {
							nCrossed = true
						}
						if nCrossed {
							score += 15 // 协同加分
						}
					}
				}
			}

			// 深入敌后额外加分（越接近底线分越高）
			if camp == rule.CampRed {
				score += (4 - r) * 5 // row 4→0: 0,5,10,15,20
			} else {
				score += (r - 5) * 5 // row 5→9: 0,5,10,15,20
			}
		}
	}
	return score
}

// mobility 机动性粗估：车/马/炮的可攻击/可移动格数
func mobility(board []int, camp int) int {
	score := 0
	for r := 0; r < rule.Rows; r++ {
		for c := 0; c < rule.Cols; c++ {
			p := board[rule.Idx(r, c)]
			if p == rule.Empty || rule.CampOf(p) != camp {
				continue
			}
			t := rule.TypeOf(p)
			if t != 2 && t != 3 && t != 4 { // 只计算车/马/炮
				continue
			}
			// 用 PseudoMoves 近似机动性（不检查合法性，速度快）
			moves := rule.PseudoMoves(board, r, c)
			score += len(moves) * 2 // 每格 +2
		}
	}
	return score
}

// positionBonus 位置加成：不同兵种在不同位置有不同价值（使用完整位置估值表）
func positionBonus(piece, row, col, myCamp int) int {
	return GetPositionBonus(piece, row, col, myCamp)
}

// pieceValue 棋子子力值
func pieceValue(piece int) int {
	t := rule.TypeOf(piece)
	if v, ok := pieceValues[t]; ok {
		return v
	}
	return 0
}

// minInt 返回两个整数中的较小值
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Move Ordering ---

// orderMoves 按 MVV-LVA + 杀手启发 + 历史启发 + 将军检测排序（两阶段）。
// 第一阶段：基础分（MVV-LVA/杀手/历史/兵推进）+ 排序；
// 第二阶段：仅对基础分前 checkProbeLimit 的候选做将军检测（P2 热路径优化，省去每着法一次全盘复制）。
func orderMoves(moves []rule.Move, board []int, camp int, hh *historyTable, kh *killerTable, depth int) {
	type scoredMove struct {
		move  rule.Move
		score int
	}
	scored := make([]scoredMove, len(moves))
	for i, m := range moves {
		s := 0

		// 1. MVV-LVA: 最有价值受害者 - 最不价值攻击者
		if m.Captured != rule.Empty {
			victimValue := pieceValue(m.Captured)
			attackerPiece := board[rule.Idx(m.FromRow, m.FromCol)]
			attackerValue := pieceValue(attackerPiece)
			s += victimValue*100 - attackerValue
		}

		// 2. 杀手启发
		if kh.isKiller(m, depth) {
			s += 50000
		}

		// 3. 历史启发
		s += hh.score(m) * 10

		// 4. 兵/卒推进加分（接近对方底线）
		piece := board[rule.Idx(m.FromRow, m.FromCol)]
		if rule.TypeOf(piece) == 7 { // 兵/卒
			if camp == rule.CampRed && m.ToRow < 3 {
				s += 5000 // 红兵接近九宫
			} else if camp == rule.CampBlack && m.ToRow > 6 {
				s += 5000 // 黑卒接近九宫
			}
		}

		scored[i] = scoredMove{m, s}
	}

	// 插入排序降序（第一轮，不含将军分）
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].score > scored[j-1].score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}

	// 第二轮：仅对基础分前 N 候选做将军检测（省去每着法一次全盘复制）
	opp := rule.CampRed + rule.CampBlack - camp
	n := len(scored)
	if n > checkProbeLimit {
		n = checkProbeLimit
	}
	for i := 0; i < n; i++ {
		testBoard := rule.CopyBoard(board)
		ApplyMoveWithoutCapture(testBoard, scored[i].move)
		if rule.IsInCheck(testBoard, opp) {
			scored[i].score += 20000
		}
	}
	// 前 N 个重新插入排序（仅加分，重排限制在前缀内不影响后缀相对顺序）
	for i := 1; i < n; i++ {
		for j := i; j > 0 && scored[j].score > scored[j-1].score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}

	for i, s := range scored {
		moves[i] = s.move
	}
}

// ApplyMoveWithoutCapture 应用走子但不返回吃子信息（用于测试将军）
func ApplyMoveWithoutCapture(board []int, m rule.Move) {
	board[rule.Idx(m.ToRow, m.ToCol)] = board[rule.Idx(m.FromRow, m.FromCol)]
	board[rule.Idx(m.FromRow, m.FromCol)] = rule.Empty
}

// --- 历史启发表 ---

type historyTable struct {
	mu     sync.Mutex
	scores [rule.Rows * rule.Cols * rule.Rows * rule.Cols]int
}

func newHistoryTable() *historyTable {
	return &historyTable{}
}

func (h *historyTable) key(m rule.Move) int {
	return m.FromRow*rule.Cols*rule.Rows*rule.Cols +
		m.FromCol*rule.Rows*rule.Cols +
		m.ToRow*rule.Cols +
		m.ToCol
}

func (h *historyTable) add(m rule.Move, depth int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scores[h.key(m)] += 1 << depth
}

func (h *historyTable) score(m rule.Move) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scores[h.key(m)]
}

// --- 杀手启发表 ---

type killerTable struct {
	mu    sync.Mutex
	moves [32][2]rule.Move // 每个深度存 2 个杀手着
	count [32]int
}

func newKillerTable() *killerTable {
	return &killerTable{}
}

func (k *killerTable) add(m rule.Move, depth int) {
	if depth >= 32 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.count[depth] == 0 {
		k.moves[depth][0] = m
		k.count[depth] = 1
	} else if k.count[depth] == 1 {
		k.moves[depth][1] = m
		k.count[depth] = 2
	} else {
		k.moves[depth][0] = k.moves[depth][1]
		k.moves[depth][1] = m
	}
}

func (k *killerTable) isKiller(m rule.Move, depth int) bool {
	if depth >= 32 {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for i := 0; i < k.count[depth]; i++ {
		if k.moves[depth][i] == m {
			return true
		}
	}
	return false
}

// --- 置换表（增强版：depth/bound/age + Zobrist + 跨迭代复用）---

// ttBoundType 置换表条目边界类型
type ttBoundType byte

const (
	boundExact ttBoundType = iota // 精确值
	boundLower                    // 下界（fail-high）
	boundUpper                    // 上界（fail-low）
)

type chessTTEntry struct {
	key       uint64      // Zobrist 哈希（用于冲突检测）
	value     int         // 评估值
	depth     int         // 搜索深度
	bound     ttBoundType // 边界类型
	age       uint16      // 年龄（用于跨迭代淘汰）
}

type chessTranspositionTable struct {
	mu          sync.Mutex
	entries     []chessTTEntry
	capacity    int
	mask        int // capacity - 1（用于快速取模）
	generation  uint16 // 当前世代（每轮迭代加深递增）
}

func newChessTranspositionTable() *chessTranspositionTable {
	cap := 65536 // 64K 条目
	return &chessTranspositionTable{
		entries:    make([]chessTTEntry, cap),
		capacity:   cap,
		mask:       cap - 1,
		generation: 1,
	}
}

// advanceGeneration 进入新世代（迭代加深循环中每轮调用一次）
func (tt *chessTranspositionTable) advanceGeneration() {
	tt.generation++
	if tt.generation == 0 {
		tt.generation = 1 // 防止溢出归零
	}
}

// get 查找置换表：返回 (value, depth, bound, found)
func (tt *chessTranspositionTable) get(hash uint64) (int, int, ttBoundType, bool) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	idx := int(hash) & tt.mask
	entry := &tt.entries[idx]
	if entry.key == hash && entry.age != 0 {
		entry.age = tt.generation // 更新为当前世代（保活）
		return entry.value, entry.depth, entry.bound, true
	}
	return 0, 0, boundExact, false
}

// put 存入置换表
func (tt *chessTranspositionTable) put(hash uint64, val int, depth int, bound ttBoundType) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	idx := int(hash) & tt.mask
	entry := &tt.entries[idx]

	// 总是覆盖：如果槽位为空或新条目深度更深，或同世代
	if entry.age == 0 || entry.depth <= depth || entry.age != tt.generation {
		entry.key = hash
		entry.value = val
		entry.depth = depth
		entry.bound = bound
		entry.age = tt.generation
	}
}

// --- Zobrist 哈希 ---

// zobristTable Zobrist 随机数表：90 格 × 28 种棋子状态（0-27，实际使用 0,11-17,21-27）
var zobristTable [rule.Rows * rule.Cols][28]uint64

func init() {
	// 用确定性种子生成随机数表，保证每次运行结果一致（便于调试）
	var seed uint64 = 0x123456789abcdef0
	for i := range zobristTable {
		for j := range zobristTable[i] {
			// 简单的线性同余生成器
			seed = seed*6364136223846793005 + 1442695040888963407
			zobristTable[i][j] = seed
		}
	}
	// 执子方哈希常量（Zobrist 补执子方，修复置换表跨走方误用与重复检测误判）
	seed = seed*6364136223846793005 + 1442695040888963407
	zobristSide = seed
}

// zobristSide 执子方哈希常量（混入 Zobrist 以区分同一棋子摆放、不同轮走方的局面）
var zobristSide uint64

// zobristHash 计算局面的 Zobrist 哈希值（含执子方，修复跨走方误用缺陷）
func zobristHash(board []int, camp int) uint64 {
	var h uint64
	for i, v := range board {
		if v != rule.Empty {
			h ^= zobristTable[i][v]
		}
	}
	if camp == rule.CampBlack {
		h ^= zobristSide
	}
	return h
}

// zobristStep 增量更新 Zobrist 哈希：从旧局面走一步到新局面（含执子方翻转）。
// 每步走子执子方必然翻转，因此始终异或 zobristSide 常量。
func zobristStep(oldHash uint64, fromIdx, toIdx, piece, captured int) uint64 {
	h := oldHash
	// 移除起点棋子
	h ^= zobristTable[fromIdx][piece]
	// 添加终点棋子（象棋无升变，piece 不变）
	h ^= zobristTable[toIdx][piece]
	// 如果吃子，移除被吃棋子
	if captured != rule.Empty {
		h ^= zobristTable[toIdx][captured]
	}
	// 每步走子执子方必然翻转，异或执子方常量
	h ^= zobristSide
	return h
}
