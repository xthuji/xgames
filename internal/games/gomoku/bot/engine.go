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

	"xgames/internal/games/gomoku/rule"
)

// Engine 五子棋决策引擎：NegaMax + α-β 剪枝 + 迭代加深 + 置换表 + VCF 杀棋检测。
type Engine struct {
	logger *slog.Logger
}

// NewEngine 创建决策引擎
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{logger: logger}
}

// 棋型分值（连五 > 活四 > 冲四/活三 > …）
const (
	scoreFive      = 10_000_000
	scoreOpenFour  = 1_000_000
	scoreFour      = 100_000
	scoreOpenThree = 50_000
	scoreThree     = 1_000
	scoreOpenTwo   = 100
	scoreTwo       = 10
)

// 搜索参数
const (
	maxSearchDepth = 4   // 最大搜索深度（迭代加深）
	radius         = 2   // 候选点搜索半径
	vcfMaxDepth    = 16  // VCF 最大深度（降低以减少开销）
	searchTimeMs   = 2000 // 搜索时间预算（毫秒）
)

// DecideMove 落子决策（零值难度：保持引擎既有行为，兼容旧接口）
func (e *Engine) DecideMove(ctx context.Context, botName string, board []int, color int) (int, int) {
	d := e.DecideMoveDetailed(ctx, botName, board, color, DifficultyConfig{})
	return d.Row, d.Col
}

// DecideMoveDetailed 落子决策（含难度配置与决策详情）。
// 决策链：空棋盘天元 → 僵局/满盘快速落子 → 开局库（难度概率）→ 成五/堵成五 → VCF → VCT → 迭代加深。
// RandomTopN>1 时在根候选前 N 名中按 softmax 概率选择（无紧迫战术胜机时），
// 保证“失误不离谱”；TopMoves 供复盘记录真实决策数据。
func (e *Engine) DecideMoveDetailed(_ context.Context, _ string, board []int, color int, cfg DifficultyConfig) DecisionDetail {
	cfg = cfg.normalize()
	mid := rule.Size / 2

	detail := DecisionDetail{Difficulty: cfg.Name}

	// 空棋盘下天元
	hasStone := false
	for _, v := range board {
		if v != rule.Empty {
			hasStone = true
			break
		}
	}
	if !hasStone {
		detail.Row, detail.Col = mid, mid
		return detail
	}

	// 僵局/满盘感知：双方均已无连五可能时，任何落子终将以和棋告终
	//（服务端 session 在落子后以 rule.IsDead 判和），跳过搜索直接取首个空位
	if rule.IsDead(board) {
		for i, v := range board {
			if v == rule.Empty {
				detail.Row, detail.Col = i/rule.Size, i%rule.Size
				detail.Score = 0
				detail.TopMoves = []ScoredMove{{detail.Row, detail.Col, 0}}
				return detail
			}
		}
	}

	// 开局库查找：按难度概率使用（手数门控与对称命中见 opening_book.go）
	if rand.Float64() < cfg.OpeningBookRate {
		if m, ok := globalOpeningBook.Lookup(board); ok {
			detail.Row, detail.Col = m.row, m.col
			detail.BookHit = true
			detail.TopMoves = []ScoredMove{{m.row, m.col, 0}}
			return detail
		}
	}

	opp := rule.Opposite(color)

	// 1. 立即胜利检测：我方下一步能成五 → 直接下
	if moves := findWinningMoves(board, color); len(moves) > 0 {
		m := moves[0]
		detail.Row, detail.Col, detail.Score = m.row, m.col, scoreFive
		detail.TopMoves = []ScoredMove{{m.row, m.col, scoreFive}}
		detail.Threats = classifyThreats(board, m.row, m.col, color)
		return detail
	}

	// 1.5 对手立即胜利检测：对手下一步能成五 → 必须堵
	if oppMoves := findWinningMoves(board, opp); len(oppMoves) > 0 {
		m := oppMoves[0]
		detail.Row, detail.Col, detail.Score = m.row, m.col, scoreFive
		detail.TopMoves = []ScoredMove{{m.row, m.col, scoreFive}}
		detail.Threats = classifyThreats(board, m.row, m.col, color)
		return detail
	}

	// 2. VCF 检测：连续冲四能否必胜
	if vcfMove := tryVCF(board, color, cfg.VCFMaxDepth); vcfMove != nil {
		detail.Row, detail.Col, detail.Score = vcfMove.row, vcfMove.col, scoreFive
		detail.VCFFound = true
		detail.TopMoves = []ScoredMove{{vcfMove.row, vcfMove.col, scoreFive}}
		detail.Threats = classifyThreats(board, vcfMove.row, vcfMove.col, color)
		return detail
	}

	// 2.5 VCT 检测：连续活三能否必胜
	if vctMove := tryVCT(board, color, cfg.VCFMaxDepth); vctMove != nil {
		detail.Row, detail.Col, detail.Score = vctMove.row, vctMove.col, scoreFive
		detail.VCTFound = true
		detail.TopMoves = []ScoredMove{{vctMove.row, vctMove.col, scoreFive}}
		detail.Threats = classifyThreats(board, vctMove.row, vctMove.col, color)
		return detail
	}

	// 3. 迭代加深 α-β 搜索（并发优化 + 置换表跨迭代复用 + 时间预算）
	cand := generateCandidates(board, color, radius)
	if len(cand) == 0 {
		detail.Row, detail.Col = mid, mid
		return detail
	}

	bestMove := cand[0]
	bestScore := -1 << 30

	// 置换表在迭代间复用
	tt := newTranspositionTable()
	startTime := time.Now()
	baseHash := hashBoard(board)

	var rootResults []ScoredMove // 最后一层完整迭代的根候选评分
	completedDepth := 0

	for depth := 1; depth <= cfg.MaxDepth; depth++ {
		// 检查时间预算：SearchTimeMs > 0 时启用，= 0 则不限时（跑满 MaxDepth）
		if cfg.SearchTimeMs > 0 && depth > 1 && time.Since(startTime).Milliseconds() > int64(cfg.SearchTimeMs) {
			break // 超出时间预算，停止加深
		}

		// 并发评估所有候选着法
		type scoredResult struct {
			move  move
			score int
		}

		numWorkers := minInt(runtime.NumCPU(), len(cand))
		chunkSize := (len(cand) + numWorkers - 1) / numWorkers

		resultsCh := make(chan scoredResult, len(cand))
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			start := w * chunkSize
			end := start + chunkSize
			if end > len(cand) {
				end = len(cand)
			}

			wg.Add(1)
			go func(startIdx, endIdx int) {
				defer wg.Done()

				// 从对象池获取棋盘副本
				localBoard := GetBoard()
				defer PutBoard(localBoard)
				copy(*localBoard, board)
				localHash := baseHash

				for i := startIdx; i < endIdx; i++ {
					c := cand[i]
					idx := rule.Idx(c.row, c.col)
					(*localBoard)[idx] = color
					moveHash := localHash ^ zobristTable[idx*3+color]

					// 立即胜利检测
					if rule.IsWin(*localBoard, c.row, c.col) {
						resultsCh <- scoredResult{c, scoreFive}
					} else {
						// NegaMax搜索（增量哈希 + lastMove 胜负检测）
						score := -negamax(*localBoard, depth-1, -1<<30, 1<<30, opp, c.row, c.col, moveHash, tt)
						resultsCh <- scoredResult{c, score}
					}

					(*localBoard)[idx] = rule.Empty
				}
			}(start, end)
		}

		// 等待所有worker完成并关闭channel
		go func() {
			wg.Wait()
			close(resultsCh)
		}()

		// 收集结果并选择最佳着法
		depthResults := make([]ScoredMove, 0, len(cand))
		for result := range resultsCh {
			depthResults = append(depthResults, ScoredMove{result.move.row, result.move.col, result.score})
			if result.score > bestScore {
				bestScore = result.score
				bestMove = result.move
			}
		}
		rootResults = depthResults
		completedDepth = depth
	}

	detail.Depth = completedDepth

	// Top-N 随机扰动（难度系统）：无紧迫战术胜机（分值低于冲四）时生效，
	// 保证必胜/必堵着法永远不被扰动掉
	chosen := bestMove
	chosenScore := bestScore
	if cfg.RandomTopN > 1 && bestScore < scoreFour && len(rootResults) > 1 {
		if m, s, ok := softmaxTopN(rootResults, cfg.RandomTopN); ok {
			chosen, chosenScore = m, s
		}
	}

	sortScoredMoves(rootResults)
	if len(rootResults) > 5 {
		rootResults = rootResults[:5]
	}
	detail.TopMoves = rootResults
	detail.Row, detail.Col, detail.Score = chosen.row, chosen.col, chosenScore
	detail.Threats = classifyThreats(board, chosen.row, chosen.col, color)
	return detail
}

// softmaxTopN 从根候选前 N 名中按 softmax 概率选择。
// 温度取前 N 名分数极差的 1/4（下限 1），避免大分差下退化为确定性选择；
// 返回选中的着法及其评分。候选不足 2 个时返回 false（维持最优）。
func softmaxTopN(results []ScoredMove, n int) (move, int, bool) {
	top := make([]ScoredMove, len(results))
	copy(top, results)
	sortScoredMoves(top)
	if len(top) > n {
		top = top[:n]
	}
	if len(top) < 2 {
		return move{}, 0, false
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
			return move{top[i].Row, top[i].Col}, top[i].Score, true
		}
	}
	return move{top[0].Row, top[0].Col}, top[0].Score, true
}

// sortScoredMoves 候选按评分降序排序
func sortScoredMoves(list []ScoredMove) {
	sort.Slice(list, func(i, j int) bool { return list[i].Score > list[j].Score })
}

// classifyThreats 落子 (row,col) 后形成的威胁分类（临时落子后还原）。
// 双向连续计数语义与 evalDirFrom 一致（活四/冲四/活三/眠三）。
func classifyThreats(board []int, row, col, color int) []string {
	idx := rule.Idx(row, col)
	board[idx] = color
	defer func() { board[idx] = rule.Empty }()

	if rule.IsWin(board, row, col) {
		return []string{"成五"}
	}

	seen := map[string]bool{}
	var threats []string
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		cnt := 1
		r, c := row+d[0], col+d[1]
		for rule.InBounds(r, c) && board[rule.Idx(r, c)] == color {
			cnt++
			r += d[0]
			c += d[1]
		}
		openEnd := rule.InBounds(r, c) && board[rule.Idx(r, c)] == rule.Empty

		r, c = row-d[0], col-d[1]
		for rule.InBounds(r, c) && board[rule.Idx(r, c)] == color {
			cnt++
			r -= d[0]
			c -= d[1]
		}
		openStart := rule.InBounds(r, c) && board[rule.Idx(r, c)] == rule.Empty

		var label string
		switch {
		case cnt == 4 && openEnd && openStart:
			label = "活四"
		case cnt == 4 && (openEnd || openStart):
			label = "冲四"
		case cnt == 3 && openEnd && openStart:
			label = "活三"
		case cnt == 3 && (openEnd || openStart):
			label = "眠三"
		}
		if label != "" && !seen[label] {
			seen[label] = true
			threats = append(threats, label)
		}
	}
	return threats
}

// move 候选落子位置
type move struct {
	row, col int
}

// generateCandidates 生成智能候选落子点（威胁感知筛选）
func generateCandidates(board []int, color int, radius int) []move {
	var cands []move
	seen := make(map[int]bool)

	// 1. 必杀点：我方下一步能成五
	if wins := findWinningMoves(board, color); len(wins) > 0 {
		return wins[:1] // 直接返回，无需搜索
	}

	// 2. 必堵点：对手下一步能成五
	opp := rule.Opposite(color)
	if blocks := findWinningMoves(board, opp); len(blocks) > 0 {
		for _, b := range blocks {
			idx := rule.Idx(b.row, b.col)
			if !seen[idx] {
				seen[idx] = true
				cands = append(cands, b)
			}
		}
	}

	// 3. 活四/冲四点：形成四的点
	fourPoints := findFourPoints(board, color)
	for _, fp := range fourPoints {
		idx := rule.Idx(fp.row, fp.col)
		if !seen[idx] {
			seen[idx] = true
			cands = append(cands, fp)
		}
	}

	// 4. 防守点：阻断对手的活三/冲四
	defPoints := findDefensivePoints(board, opp)
	for _, dp := range defPoints {
		idx := rule.Idx(dp.row, dp.col)
		if !seen[idx] {
			seen[idx] = true
			cands = append(cands, dp)
		}
	}

	// 5. 进攻点：我方的活三/眠三
	threePoints := findThreePoints(board, color)
	for _, tp := range threePoints {
		idx := rule.Idx(tp.row, tp.col)
		if !seen[idx] {
			seen[idx] = true
			cands = append(cands, tp)
		}
	}

	// 6. 邻近点：已有棋子周围的空位（兜底）
	nearPoints := findNearbyPoints(board, color, radius)
	for _, np := range nearPoints {
		idx := rule.Idx(np.row, np.col)
		if !seen[idx] {
			seen[idx] = true
			cands = append(cands, np)
		}
	}

	// 7. 限制候选数量（最多30个），按启发式评分排序
	if len(cands) > 30 {
		scored := scoreAndSortCandidates(cands, board, color)
		cands = scored[:30]
	}

	return cands
}

// findFourPoints 找出所有能形成"四"的落子点（活四或冲四）
func findFourPoints(board []int, color int) []move {
	var fours []move
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty {
				continue
			}
			board[rule.Idx(r, c)] = color
			if isFourPoint(board, r, c, color) {
				fours = append(fours, move{r, c})
			}
			board[rule.Idx(r, c)] = rule.Empty
		}
	}
	return fours
}

// findThreePoints 找出所有能形成"三"的落子点（活三或眠三）
func findThreePoints(board []int, color int) []move {
	var threes []move
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty {
				continue
			}
			board[rule.Idx(r, c)] = color
			if isThreePoint(board, r, c, color) {
				threes = append(threes, move{r, c})
			}
			board[rule.Idx(r, c)] = rule.Empty
		}
	}
	return threes
}

// findDefensivePoints 找出需要防守的点（阻断对手的活三/冲四）
func findDefensivePoints(board []int, oppColor int) []move {
	var defs []move

	// 对手的活三/冲四点需要防守
	threats := findThreePoints(board, oppColor)
	defs = append(defs, threats...)

	// 对手的四点必须防守
	fours := findFourPoints(board, oppColor)
	defs = append(defs, fours...)

	return defs
}

// findNearbyPoints 找出已有棋子半径内的空位（兜底策略）
func findNearbyPoints(board []int, _ int, radius int) []move {
	var points []move
	seen := make(map[int]bool)
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty {
				continue
			}
			if !nearStone(board, r, c, radius) {
				continue
			}
			idx := rule.Idx(r, c)
			if seen[idx] {
				continue
			}
			seen[idx] = true
			points = append(points, move{r, c})
		}
	}
	return points
}

// scoreAndSortCandidates 对候选点按启发式评分并排序
func scoreAndSortCandidates(cands []move, board []int, color int) []move {
	type scoredMove struct {
		move  move
		score int
	}

	scored := make([]scoredMove, len(cands))
	for i, m := range cands {
		s := scoreCandidate(m, board, color)
		scored[i] = scoredMove{m, s}
	}

	// 降序排序
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].score > scored[j-1].score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}

	result := make([]move, len(cands))
	for i, s := range scored {
		result[i] = s.move
	}
	return result
}

// scoreCandidate 对单个候选点评分
func scoreCandidate(m move, board []int, color int) int {
	score := 0

	// 进攻分：落下后形成的棋型
	board[rule.Idx(m.row, m.col)] = color
	score += evalPointFrom(board, m.row, m.col, color) * 2
	board[rule.Idx(m.row, m.col)] = rule.Empty

	// 防守分：阻断对手的棋型（使用整数运算：×3/2 代替 ×1.5）
	opp := rule.Opposite(color)
	board[rule.Idx(m.row, m.col)] = opp
	score += evalPointFrom(board, m.row, m.col, opp) * 3 / 2
	board[rule.Idx(m.row, m.col)] = rule.Empty

	// 位置分：中心区域加分（15×15棋盘中心为7,7）
	centerDist := absInt(m.row-7) + absInt(m.col-7)
	score -= centerDist * 2

	return score
}

// isThreePoint (row,col) 落下 color 后是否形成"三"（活三或眠三，含跳三）
func isThreePoint(board []int, row, col, color int) bool {
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		if hasNInRowWithGap(board, row, col, d[0], d[1], color, 3) {
			return true
		}
	}
	return false
}

// isFourPoint (row,col) 落下 color 后是否形成"四"（活四或冲四，含跳四）
func isFourPoint(board []int, row, col, color int) bool {
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		if hasNInRowWithGap(board, row, col, d[0], d[1], color, 4) {
			return true
		}
	}
	return false
}

// hasNInRowWithGap 检查在 (row,col) 落子后，沿 (dr,dc) 方向是否能形成 n 连（允许1个空位间隔）
func hasNInRowWithGap(board []int, row, col, dr, dc, color, n int) bool {
	// 收集该方向线上的所有相关位置
	var positions []struct{ r, c int }
	
	// 先向反方向找到起点
	r, c := row, col
	for {
		pr, pc := r-dr, c-dc
		if !rule.InBounds(pr, pc) || board[rule.Idx(pr, pc)] != color {
			break
		}
		r, c = pr, pc
	}
	
	// 从起点向正方向收集连续的同色子和空位
	for rule.InBounds(r, c) {
		v := board[rule.Idx(r, c)]
		if v == color {
			positions = append(positions, struct{ r, c int }{r, c})
		} else if v == rule.Empty {
			positions = append(positions, struct{ r, c int }{r, c})
		} else {
			break // 遇到对方棋子，中断
		}
		r += dr
		c += dc
	}

	if len(positions) < n {
		return false
	}

	// 滑动窗口：检查是否有连续 n 个位置（最多1个空位，且包含落子点）能形成有效连线
	for i := 0; i <= len(positions)-n; i++ {
		window := positions[i : i+n]
		
		// 检查窗口是否包含落子点
		hasMovePoint := false
		emptyCount := 0
		for _, p := range window {
			if p.r == row && p.c == col {
				hasMovePoint = true
			}
			if board[rule.Idx(p.r, p.c)] == rule.Empty {
				emptyCount++
			}
		}
		
		if !hasMovePoint {
			continue
		}
		
		// 空位数应 ≤ 1（落子点填入后只剩原有空位）
		if emptyCount > 1 {
			continue
		}
		
		// 检查两端是否至少有一端开放
		leftOpen := false
		rightOpen := false
		
		// 左端
		if i > 0 {
			prev := positions[i-1]
			if board[rule.Idx(prev.r, prev.c)] == rule.Empty {
				leftOpen = true
			}
		} else {
			pr, pc := window[0].r-dr, window[0].c-dc
			if rule.InBounds(pr, pc) && board[rule.Idx(pr, pc)] == rule.Empty {
				leftOpen = true
			}
		}
		
		// 右端
		if i+n < len(positions) {
			next := positions[i+n]
			if board[rule.Idx(next.r, next.c)] == rule.Empty {
				rightOpen = true
			}
		} else {
			last := window[len(window)-1]
			nr, nc := last.r+dr, last.c+dc
			if rule.InBounds(nr, nc) && board[rule.Idx(nr, nc)] == rule.Empty {
				rightOpen = true
			}
		}
		
		if leftOpen || rightOpen {
			return true
		}
	}

	return false
}

// absInt 整数绝对值
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// minInt 返回两个整数中的较小值
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// nearStone (row,col) 半径 radius 内是否有棋子
func nearStone(board []int, row, col, radius int) bool {
	for dr := -radius; dr <= radius; dr++ {
		for dc := -radius; dc <= radius; dc++ {
			r, c := row+dr, col+dc
			if rule.InBounds(r, c) && board[rule.Idx(r, c)] != rule.Empty {
				return true
			}
		}
	}
	return false
}

// findWinningMoves 找出所有直接成五的落子点
func findWinningMoves(board []int, color int) []move {
	var wins []move
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
				continue
			}
			board[rule.Idx(r, c)] = color
			if rule.IsWin(board, r, c) {
				wins = append(wins, move{r, c})
			}
			board[rule.Idx(r, c)] = rule.Empty
		}
	}
	return wins
}

// VCF（Victory by Continuous Four）：连续冲四检测
// 核心思路：我方不断冲四（活四/冲四），对手必须堵，再冲四…直到连五
// maxDepth 为杀棋搜索最大深度（难度配置：easy 6 / normal 8 / hard 12 / 零值 16）
func tryVCF(board []int, color, maxDepth int) *move {
	kc := newKillCache()
	cands := generateCandidates(board, color, radius)
	for _, c := range cands {
		board[rule.Idx(c.row, c.col)] = color
		if rule.IsWin(board, c.row, c.col) {
			board[rule.Idx(c.row, c.col)] = rule.Empty
			return &c
		}
		if vcfSearch(board, color, 0, maxDepth, kc) {
			board[rule.Idx(c.row, c.col)] = rule.Empty
			return &c
		}
		board[rule.Idx(c.row, c.col)] = rule.Empty
	}
	return nil
}

// vcfSearch VCF 递归搜索：冲四 → 对手堵 → 继续冲四。
// 带 killCache（杀棋置换表）：VCF/VCT 的结果是 bool 胜负语义，与主搜索的分值边界不同，
// 故使用独立缓存；同一局面在不同剩余深度下结论可能不同，命中判定按深度区间（见 killCache.probe）。
func vcfSearch(board []int, color, depth, maxDepth int, kc *killCache) bool {
	if depth > maxDepth {
		return false
	}
	remain := maxDepth - depth
	h := hashBoard(board)
	if win, ok := kc.probe(h, remain); ok {
		return win
	}
	win := vcfSearchInner(board, color, depth, maxDepth, kc)
	kc.store(h, remain, win)
	return win
}

// vcfSearchInner VCF 递归主体（缓存命中/写入由 vcfSearch 包装）
func vcfSearchInner(board []int, color, depth, maxDepth int, kc *killCache) bool {
	opp := rule.Opposite(color)

	// 找出所有能形成"四"的落子点（活四或冲四）
	var fourMoves []move
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
				continue
			}
			board[rule.Idx(r, c)] = color
			if rule.IsWin(board, r, c) {
				board[rule.Idx(r, c)] = rule.Empty
				return true
			}
			if isFourPoint(board, r, c, color) {
				fourMoves = append(fourMoves, move{r, c})
			}
			board[rule.Idx(r, c)] = rule.Empty
		}
	}

	if len(fourMoves) == 0 {
		return false
	}

	for _, fm := range fourMoves {
		board[rule.Idx(fm.row, fm.col)] = color

		// 检查对手是否有反杀：成五或冲四反击
		hasCounterThreat := false
		for r := range rule.Size {
			for c := range rule.Size {
				if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
					continue
				}
				board[rule.Idx(r, c)] = opp
				if rule.IsWin(board, r, c) {
					// 对手可以成五反杀，此分支失败
					board[rule.Idx(r, c)] = rule.Empty
					hasCounterThreat = true
					break
				}
				if isFourPoint(board, r, c, opp) {
					// 对手有冲四反击，此分支失败
					board[rule.Idx(r, c)] = rule.Empty
					hasCounterThreat = true
					break
				}
				board[rule.Idx(r, c)] = rule.Empty
			}
			if hasCounterThreat {
				break
			}
		}

		if hasCounterThreat {
			board[rule.Idx(fm.row, fm.col)] = rule.Empty
			continue // 尝试下一个冲四点
		}

		// 对手必须堵这一点（因为如果不堵，我方下次走必然成五）
		// 尝试对手在 fm 点堵（即对手 fm 位置下子）
		board[rule.Idx(fm.row, fm.col)] = opp
		if vcfSearch(board, color, depth+1, maxDepth, kc) {
			board[rule.Idx(fm.row, fm.col)] = rule.Empty
			return true
		}
		board[rule.Idx(fm.row, fm.col)] = rule.Empty
	}
	return false
}

// VCT (Victory by Continuous Three): 连续威胁必胜检测
// 核心思路：攻方连走活三迫使防守方应对，直至形成活四（两端全开，无法兼顾）或成五。
// 交替博弈树语义：攻方每手威胁后，防守方枚举全部应对（堵三两端 + 反打冲四），
// 攻方须在任一应对下均能续胜。防守方存在成五点时保守判攻方失败（杜绝假阳性）。
// maxDepth 与 VCF 共用难度配置的杀棋深度上限
func tryVCT(board []int, color, maxDepth int) *move {
	kc := newKillCache()
	cands := generateCandidates(board, color, radius)
	for _, c := range cands {
		board[rule.Idx(c.row, c.col)] = color
		// 首着须为严格活三（连续 3 子且两端开，冲四链归 VCF 管），
		// 注意不能用宽松的 isFourPoint 做排除（它把活三也判为四点，永远拒真）
		if strictLiveThreePoint(board, c.row, c.col, color) {
			if vctRefuteThreat(board, color, c.row, c.col, 0, maxDepth, kc) {
				board[rule.Idx(c.row, c.col)] = rule.Empty
				return &c
			}
		}
		board[rule.Idx(c.row, c.col)] = rule.Empty
	}
	return nil
}

// vctSearch VCT 递归搜索：活三 → 对手防守 → 继续活三（带 killCache，包装方式同 vcfSearch）
func vctSearch(board []int, color, depth, maxDepth int, kc *killCache) bool {
	if depth > maxDepth {
		return false
	}
	remain := maxDepth - depth
	h := hashBoard(board)
	if win, ok := kc.probe(h, remain); ok {
		return win
	}
	win := vctSearchInner(board, color, depth, maxDepth, kc)
	kc.store(h, remain, win)
	return win
}

// vctSearchInner VCT 递归主体（缓存命中/写入由 vctSearch 包装）。
// 语义：color 为攻方且轮到攻方行棋，判断能否以连续威胁（活三/活四/成五）必胜。
func vctSearchInner(board []int, color, depth, maxDepth int, kc *killCache) bool {
	opp := rule.Opposite(color)

	// 防守方存在成五点：威胁链不够快，攻方失败（保守判定，避免假阳性）
	if len(findWinningMoves(board, opp)) > 0 {
		return false
	}

	// 攻方直接成五，或一手形成活四（两端全开防守方无法兼顾）→ 必胜
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
				continue
			}
			board[rule.Idx(r, c)] = color
			win := rule.IsWin(board, r, c) || isLiveFourPoint(board, r, c, color)
			board[rule.Idx(r, c)] = rule.Empty
			if win {
				return true
			}
		}
	}

	// 活三威胁：攻方每手严格活三迫使防守方应对，持续至成活四/成五
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
				continue
			}
			board[rule.Idx(r, c)] = color
			if strictLiveThreePoint(board, r, c, color) {
				if vctRefuteThreat(board, color, r, c, depth, maxDepth, kc) {
					board[rule.Idx(r, c)] = rule.Empty
					return true
				}
			}
			board[rule.Idx(r, c)] = rule.Empty
		}
	}
	return false
}

// vctRefuteThreat 攻方刚落下威胁着法（row,col，已置于棋盘），
// 枚举防守方所有应对（堵三两端 + 反打冲四），验证攻方在任一应对下均能续胜。
// 防守方已有成五点（威胁不够快）或“三”无实质防守点（间隔形无真实威胁）→ 攻方失败。
// 修复原实现攻方连走两手、防守方从未应对首手威胁的博弈树交替错误，
// 以及活四过渡被 ！isFourPoint 排除导致 VCT 永远找不到双活三必胜的缺陷。
func vctRefuteThreat(board []int, color, row, col, depth, maxDepth int, kc *killCache) bool {
	opp := rule.Opposite(color)
	if len(findWinningMoves(board, opp)) > 0 {
		return false
	}

	defenses := findDefensesForLiveThree(board, row, col, color)
	if len(defenses) == 0 {
		// 无实质防守点（如间隔形态“三”无真实威胁）→ 不构成必胜
		return false
	}
	// 防守方反打冲四（不堵三）也是有效应对：攻方须先解冲四，威胁链断裂
	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != rule.Empty || !nearStone(board, r, c, radius) {
				continue
			}
			board[rule.Idx(r, c)] = opp
			counter := fourAfterMove(board, r, c, opp)
			board[rule.Idx(r, c)] = rule.Empty
			if counter {
				defenses = append(defenses, move{r, c})
			}
		}
	}

	canWinAll := true
	for _, def := range defenses {
		board[rule.Idx(def.row, def.col)] = opp
		if !vctSearch(board, color, depth+1, maxDepth, kc) {
			canWinAll = false
		}
		board[rule.Idx(def.row, def.col)] = rule.Empty
		if !canWinAll {
			break
		}
	}
	return canWinAll
}

// isLiveFourPoint (row,col) 落下 color 后是否形成活四：某方向连续 ≥4 子且两端均为空位。
// 活四是必胜形：防守方无法同时堵住两端。成五由调用方 IsWin 先行判定。
func isLiveFourPoint(board []int, row, col, color int) bool {
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		cnt, sr, sc, er, ec := runThroughDir(board, row, col, d[0], d[1], color)
		if cnt >= 4 && openAt(board, er+d[0], ec+d[1]) && openAt(board, sr-d[0], sc-d[1]) {
			return true
		}
	}
	return false
}

// strictLiveThreePoint 落子后是否形成严格活三：某方向连续 3 子（含落子点）且两端均为空位。
// 跳三/眠三不算（保守口径，归入 VCF/主搜索处理），避免宽松窗口语义把活三误判为四。
func strictLiveThreePoint(board []int, row, col, color int) bool {
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		cnt, sr, sc, er, ec := runThroughDir(board, row, col, d[0], d[1], color)
		if cnt == 3 && openAt(board, er+d[0], ec+d[1]) && openAt(board, sr-d[0], sc-d[1]) {
			return true
		}
	}
	return false
}

// fourAfterMove 落子后某方向是否形成连续 4 子及以上（活四或冲四，不含跳四）
func fourAfterMove(board []int, row, col, color int) bool {
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		if cnt, _, _, _, _ := runThroughDir(board, row, col, d[0], d[1], color); cnt >= 4 {
			return true
		}
	}
	return false
}

// runThroughDir 经过 (row,col) 沿 (dr,dc) 的最大同色连续段：返回（长度，起点，终点）。
// 调用方须保证 (row,col) 已置为 color。
func runThroughDir(board []int, row, col, dr, dc, color int) (cnt, sr, sc, er, ec int) {
	cnt, sr, sc, er, ec = 1, row, col, row, col
	for r, c := row+dr, col+dc; rule.InBounds(r, c) && board[rule.Idx(r, c)] == color; r, c = r+dr, c+dc {
		cnt++
		er, ec = r, c
	}
	for r, c := row-dr, col-dc; rule.InBounds(r, c) && board[rule.Idx(r, c)] == color; r, c = r-dr, c-dc {
		cnt++
		sr, sc = r, c
	}
	return
}

// openAt (r,c) 在界内且为空位
func openAt(board []int, r, c int) bool {
	return rule.InBounds(r, c) && board[rule.Idx(r, c)] == rule.Empty
}

// findDefensesForLiveThree 找出防守活三的所有位置（通常是两端）
func findDefensesForLiveThree(board []int, row, col, color int) []move {
	var defenses []move
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}

	for _, d := range dirs {
		// 经过落子点的完整连续段（修复：原实现只累计前向棋子，落子点在三连
		// 中段/尾部时 count 恒为 1，永远找不到防守点，VCT 全灭）
		cnt, sr, sc, er, ec := runThroughDir(board, row, col, d[0], d[1], color)
		if cnt != 3 {
			continue
		}
		// 活三两端即为防守点
		if openAt(board, er+d[0], ec+d[1]) {
			defenses = append(defenses, move{er + d[0], ec + d[1]})
		}
		if openAt(board, sr-d[0], sc-d[1]) {
			defenses = append(defenses, move{sr - d[0], sc - d[1]})
		}
	}

	// 去重
	seen := make(map[int]bool)
	var unique []move
	for _, d := range defenses {
		idx := rule.Idx(d.row, d.col)
		if !seen[idx] {
			seen[idx] = true
			unique = append(unique, d)
		}
	}

	return unique
}

// negamax NegaMax + α-β 剪枝。
// lastRow/lastCol 为上一手（opp 方）落点：搜索树上不存在“非最后一手形成的连五”
// （父节点已在落子时检测/短路），故 O(1) 检测最后一手即可，替代原先每节点全盘扫描；
// h 为增量维护的 Zobrist 哈希（落子/回退时 XOR 更新），替代每节点全盘重算。
func negamax(board []int, depth, alpha, beta, color, lastRow, lastCol int, h uint64, tt *transpositionTable) int {
	// 置换表查找（命中时同时取回 best move，用于候选排序）
	res := tt.get(h, depth)
	if res.ok {
		switch res.bound {
		case boundExact:
			return res.value
		case boundLower:
			if res.value > alpha {
				alpha = res.value
			}
		case boundUpper:
			if res.value < beta {
				beta = res.value
			}
		}
		if alpha >= beta {
			return res.value
		}
	}

	opp := rule.Opposite(color)

	// 上一手成五检测（替代全盘 checkWin）
	if lastRow >= 0 && rule.IsWin(board, lastRow, lastCol) {
		val := -scoreFive - depth
		tt.put(h, val, depth, boundExact, move{}, false)
		return val
	}

	// 终局和棋感知：双方均无连五可能（含满盘）→ 0（平局）。
	// 满盘时所有窗口均无空位，IsDead 同样成立，一次判定覆盖两种和棋终局；
	// 劣势方可据此主动导向僵局求和（0 分优于负分），优势方避免走入死局。
	if rule.IsDead(board) {
		tt.put(h, 0, depth, boundExact, move{}, false)
		return 0
	}

	if depth == 0 {
		score := evalBoard(board, color)
		tt.put(h, score, depth, boundExact, move{}, false)
		return score
	}

	cands := generateCandidates(board, color, radius)
	if len(cands) == 0 {
		score := evalBoard(board, color)
		tt.put(h, score, depth, boundExact, move{}, false)
		return score
	}

	// Best Move 排序：置换表历史最佳着法提前尝试，提升 α-β 剪枝效率
	if res.ok && res.hasBest {
		for i, c := range cands {
			if c.row == res.bestMove.row && c.col == res.bestMove.col {
				cands[0], cands[i] = cands[i], cands[0]
				break
			}
		}
	}

	best := -1 << 30
	var bestCand move
	hasBest := false
	bound := boundUpper
	for _, c := range cands {
		idx := rule.Idx(c.row, c.col)
		board[idx] = color
		score := -negamax(board, depth-1, -beta, -alpha, opp, c.row, c.col, h^zobristTable[idx*3+color], tt)
		board[idx] = rule.Empty

		if score > best {
			best = score
			bestCand = c
			hasBest = true
		}
		if score > alpha {
			alpha = score
			bound = boundExact
		}
		if alpha >= beta {
			bound = boundLower
			break
		}
	}

	tt.put(h, best, depth, bound, bestCand, hasBest)
	return best
}

// evalBoard 全局局面评估（从 color 视角，我方优势为正）。
// 每方单次遍历同时统计棋型分与组合威胁分（原先 4 次全盘扫描，现合并为 2 次）。
func evalBoard(board []int, color int) int {
	myScore, myCombo := evalSideStats(board, color)
	oppScore, oppCombo := evalSideStats(board, rule.Opposite(color))
	return myScore - oppScore*9/10 + myCombo - oppCombo*9/10
}

// evalSideStats 一次遍历统计 color 方的棋型总分与组合威胁数量
func evalSideStats(board []int, color int) (int, int) {
	total := 0
	liveThreeCount := 0 // 活三数量
	openFourCount := 0  // 活四数量
	fourCount := 0      // 冲四数量

	for r := range rule.Size {
		for c := range rule.Size {
			if board[rule.Idx(r, c)] != color {
				continue
			}
			total += evalPointFrom(board, r, c, color)
			dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
			for _, d := range dirs {
				cnt, opens := countLineInfo(board, r, c, d[0], d[1], color)
				if cnt == 3 && opens == 2 {
					liveThreeCount++
				}
				if cnt == 4 && opens == 2 {
					openFourCount++
				}
				if cnt == 4 && opens == 1 {
					fourCount++
				}
			}
		}
	}
	return total, comboThreatFromCounts(liveThreeCount, openFourCount, fourCount)
}

// comboThreatFromCounts 组合威胁加分（原 comboThreatBonus 的纯计数版本）
func comboThreatFromCounts(liveThree, openFour, four int) int {
	bonus := 0

	// 双活三：极大威胁
	if liveThree >= 2 {
		bonus += scoreOpenThree * 3 // +150000
	}

	// 活四：必胜威胁
	if openFour > 0 {
		bonus += scoreOpenFour / 2 // +500000
	}

	// 冲四+活三组合：强威胁
	if four > 0 && liveThree > 0 {
		bonus += scoreFour + scoreOpenThree/2 // +125000
	}

	// 双冲四：较强威胁
	if four >= 2 {
		bonus += scoreFour // +100000
	}

	return bonus
}

// countLineInfo 统计从 (row,col) 出发沿方向的棋子数和开闭情况（复用 evalDirFrom 逻辑）
func countLineInfo(board []int, row, col, dr, dc, color int) (int, int) {
	pr, pc := row-dr, col-dc
	if rule.InBounds(pr, pc) && board[rule.Idx(pr, pc)] == color {
		return 0, 0 // 已被其他方向计数
	}
	
	count := 1
	r, c := row+dr, col+dc
	for rule.InBounds(r, c) && board[rule.Idx(r, c)] == color {
		count++
		r += dr
		c += dc
	}
	opens := 0
	if rule.InBounds(r, c) && board[rule.Idx(r, c)] == rule.Empty {
		opens++
	}
	
	pr, pc = row-dr, col-dc
	if rule.InBounds(pr, pc) && board[rule.Idx(pr, pc)] == rule.Empty {
		opens++
	}
	
	return count, opens
}

// evalPointFrom 从 (row,col) 处棋子出发，评估四个方向的棋型分
func evalPointFrom(board []int, row, col, color int) int {
	total := 0
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		total += evalDirFrom(board, row, col, d[0], d[1], color)
	}
	return total
}

// evalDirFrom 单方向棋型评分（避免重复计数：只从起点向一个方向扫）
func evalDirFrom(board []int, row, col, dr, dc, color int) int {
	// 检查反方向上一格是否也是同色（如果是，说明这个方向在那个点已算过）
	pr, pc := row-dr, col-dc
	if rule.InBounds(pr, pc) && board[rule.Idx(pr, pc)] == color {
		return 0
	}

	count := 1
	r, c := row+dr, col+dc
	for rule.InBounds(r, c) && board[rule.Idx(r, c)] == color {
		count++
		r += dr
		c += dc
	}
	openEnd := rule.InBounds(r, c) && board[rule.Idx(r, c)] == rule.Empty

	// 另一端
	pr, pc = row-dr, col-dc
	openStart := rule.InBounds(pr, pc) && board[rule.Idx(pr, pc)] == rule.Empty

	switch {
	case count >= 5:
		return scoreFive
	case count == 4:
		if openEnd && openStart {
			return scoreOpenFour
		}
		if openEnd || openStart {
			return scoreFour
		}
	case count == 3:
		if openEnd && openStart {
			return scoreOpenThree
		}
		if openEnd || openStart {
			return scoreThree
		}
	case count == 2:
		if openEnd && openStart {
			return scoreOpenTwo
		}
		if openEnd || openStart {
			return scoreTwo
		}
	}
	return 0
}

// --- 置换表（LRU优化 + depth/bound 标志）---

// boundType 置换表条目的边界类型
type boundType int

const (
	boundExact    boundType = iota // 精确值
	boundLower                     // 下界（α-β 中 beta 截断产生）
	boundUpper                     // 上界（α-β 中 alpha 未提升产生）
)

type ttEntry struct {
	value     int
	depth     int       // 存储时的搜索深度
	bound     boundType // 边界类型
	bestMove  move      // 该局面下的最佳着法（Best Move 排序提示）
	hasBest   bool      // 是否存在有效 bestMove（叶/终局节点为 false）
	accessNum uint64    // 访问序号，用于LRU淘汰
}

type transpositionTable struct {
	mu        sync.Mutex
	entries   map[uint64]*ttEntry
	capacity  int
	maxAccess uint64 // 全局访问计数器
}

func newTranspositionTable() *transpositionTable {
	return &transpositionTable{
		entries:  make(map[uint64]*ttEntry, 65536),
		capacity: 65536,
	}
}

// maxTTCapacity 置换表容量上限：未达上限前倍增扩容（置换表生命周期为单次决策，
// 通常无需淘汰），超限后才走 LRU 淘汰
const maxTTCapacity = 262144

// ttResult 置换表查询结果
type ttResult struct {
	value    int
	depth    int
	bound    boundType
	bestMove move
	hasBest  bool
	ok       bool
}

func (tt *transpositionTable) get(h uint64, minDepth int) ttResult {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if entry, ok := tt.entries[h]; ok {
		if entry.depth >= minDepth {
			tt.maxAccess++
			entry.accessNum = tt.maxAccess
			return ttResult{value: entry.value, depth: entry.depth, bound: entry.bound, bestMove: entry.bestMove, hasBest: entry.hasBest, ok: true}
		}
	}
	return ttResult{ok: false}
}

func (tt *transpositionTable) put(h uint64, val int, depth int, bound boundType, bestMove move, hasBest bool) {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	// 如果已存在且新深度更深，或不存在，则更新
	if existing, ok := tt.entries[h]; !ok || depth > existing.depth {
		// 容量检查：未达上限先倍增容量；超限后淘汰最少使用的条目
		if !ok && len(tt.entries) >= tt.capacity {
			if tt.capacity < maxTTCapacity {
				tt.capacity *= 2
			} else {
				tt.evictLRU()
			}
		}
		tt.entries[h] = &ttEntry{value: val, depth: depth, bound: bound, bestMove: bestMove, hasBest: hasBest, accessNum: tt.maxAccess}
	}
}

// evictLRU 淘汰最少最近使用的条目（保留50%）。需持有 tt.mu 调用。
// 原实现为插入排序（O(n²)，64K 条目触发时秒级卡顿），改为 sort.Slice（O(n log n)）；
// 原容量自适应条件（<16384 增长）因初始值 65536 永不生效，已改为 put 侧倍增扩容。
func (tt *transpositionTable) evictLRU() {
	type kv struct {
		key   uint64
		entry *ttEntry
	}

	all := make([]kv, 0, len(tt.entries))
	for k, v := range tt.entries {
		all = append(all, kv{k, v})
	}

	// 按 accessNum 升序排序，删除最不常用的 50%
	sort.Slice(all, func(i, j int) bool { return all[i].entry.accessNum < all[j].entry.accessNum })
	toRemove := len(all) / 2
	for i := 0; i < toRemove; i++ {
		delete(tt.entries, all[i].key)
	}
}

// --- 杀棋搜索置换表（VCF/VCT 专用）---

// maxKillCacheSize 杀棋缓存条目上限（单次决策生命周期，超限后不再写入）
const maxKillCacheSize = 1 << 20

// killEntry 杀棋搜索缓存条目：记录“搜索时的剩余深度”下的胜负结论
// （同一局面在剩余深度不同时结论可能不同，如浅层下无法取胜、深层下必胜）
type killEntry struct {
	remain int  // 搜索时的剩余深度（maxDepth - depth）
	win    bool // color 方在 remain 深度内能否强制取胜
}

// killCache 杀棋搜索置换表：VCF/VCT 结果为 bool 胜负语义（与主搜索的分值边界不同），
// 与主置换表分离；命中按深度区间判定，避免跨深度误用。
type killCache struct {
	entries map[uint64]killEntry
}

func newKillCache() *killCache {
	return &killCache{entries: make(map[uint64]killEntry, 4096)}
}

// probe 缓存查询：
//   - true 结论在剩余深度 ≥ 记录值时成立（更少深度都必胜，更深自然必胜）
//   - false 结论在剩余深度 ≤ 记录值时成立（更多深度都赢不了，更浅更赢不了）
func (kc *killCache) probe(h uint64, remain int) (win, ok bool) {
	e, hit := kc.entries[h]
	if !hit {
		return false, false
	}
	if e.win && e.remain <= remain {
		return true, true
	}
	if !e.win && e.remain >= remain {
		return false, true
	}
	return false, false
}

func (kc *killCache) store(h uint64, remain int, win bool) {
	if len(kc.entries) >= maxKillCacheSize {
		return
	}
	kc.entries[h] = killEntry{remain: remain, win: win}
}

// hashBoard 简单的 zobrist 风格 hash
var zobristTable = func() [rule.Size * rule.Size * 3]uint64 {
	var t [rule.Size * rule.Size * 3]uint64
	r := rand.New(rand.NewPCG(42, 12345))
	for i := range t {
		t[i] = r.Uint64()
	}
	return t
}()

func hashBoard(board []int) uint64 {
	var h uint64
	for i, v := range board {
		if v == 0 {
			continue
		}
		h ^= zobristTable[i*3+v]
	}
	return h
}
