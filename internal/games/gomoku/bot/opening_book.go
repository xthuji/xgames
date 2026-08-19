package bot

import (
	"fmt"
	"math/rand/v2"
	"sync"

	"xgames/internal/games/gomoku/rule"
)

// OpeningBook 五子棋开局库
//
// 2026-09 修复与增强（见 docs/bots/gomoku-bot.md §4.1.1）：
//  1. 应手改为多应手列表：同 key 定式（花月/浦月等首两手相同）不再互相覆盖；
//  2. 注册时展开 8 重对称变体（旋转 90°/180°/270° + 镜像），应手坐标同步变换，
//     任意方位的白 2 应招均可精确命中（原仅覆盖注册方位，命中率提升约 2~4 倍）；
//  3. Lookup 增加手数门控：库只记录"黑1白1 → 黑3应手"，仅棋盘恰好 2 手时查询，
//     修复原先第 5 手起仍命中并返回已占用点的缺陷（原先靠 session 兜底救场）。
type OpeningBook struct {
	mu      sync.RWMutex
	entries map[string][]move // key: 带颜色棋谱串, value: 推荐应手列表（对称变体去重后）
}

var globalOpeningBook = &OpeningBook{
	entries: make(map[string][]move),
}

// init 初始化常见开局模式
func init() {
	globalOpeningBook.initCommonOpenings()
}

// openingPattern 开局模式定义
type openingPattern struct {
	name     string
	moves    [][3]int // [row, col, color] 序列
	response move     // 推荐应手
}

// initCommonOpenings 初始化常见开局（基于职业定式）
func (ob *OpeningBook) initCommonOpenings() {
	mid := 7 // 15×15棋盘中心

	patterns := []openingPattern{
		// === 直指类（白2在天元上下左右）===

		// 花月（直指第1种）：黑1天元，白2上方，黑3右下
		{
			name: "花月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid, rule.White},
			},
			response: move{mid + 1, mid + 1},
		},
		// 浦月（直指第2种）：黑1天元，白2上方，黑3左下
		{
			name: "浦月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid, rule.White},
			},
			response: move{mid + 1, mid - 1},
		},
		// 云月（直指第3种）：黑1天元，白2下方
		{
			name: "云月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid + 1, mid, rule.White},
			},
			response: move{mid - 1, mid - 1},
		},
		// 溪月（直指第4种）：黑1天元，白2下方，黑3左上
		{
			name: "溪月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid + 1, mid, rule.White},
			},
			response: move{mid - 1, mid + 1},
		},
		// 疏月（直指第5种）：黑1天元，白2左侧
		{
			name: "疏月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid, mid - 1, rule.White},
			},
			response: move{mid + 1, mid + 1},
		},
		// 残月（直指第6种）：黑1天元，白2右侧
		{
			name: "残月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid, mid + 1, rule.White},
			},
			response: move{mid + 1, mid - 1},
		},

		// === 斜指类（白2在天元对角线）===

		// 明星（斜指第1种）：黑1天元，白2右上
		{
			name: "明星",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid + 1, rule.White},
			},
			response: move{mid + 1, mid - 1},
		},
		// 峡月（斜指第2种）：黑1天元，白2右上，黑3左下
		{
			name: "峡月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid + 1, rule.White},
			},
			response: move{mid + 1, mid + 1},
		},
		// 新月（斜指第3种）：黑1天元，白2左上
		{
			name: "新月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid - 1, rule.White},
			},
			response: move{mid + 1, mid + 1},
		},
		// 瑞星（斜指第4种）：黑1天元，白2左上，黑3右下
		{
			name: "瑞星",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid - 1, mid - 1, rule.White},
			},
			response: move{mid + 1, mid - 1},
		},
		// 山月（斜指第5种）：黑1天元，白2左下
		{
			name: "山月",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid + 1, mid - 1, rule.White},
			},
			response: move{mid - 1, mid + 1},
		},
		// 游星（斜指第6种）：黑1天元，白2右下
		{
			name: "游星",
			moves: [][3]int{
				{mid, mid, rule.Black},
				{mid + 1, mid + 1, rule.White},
			},
			response: move{mid - 1, mid - 1},
		},
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()
	for _, p := range patterns {
		ob.addOpeningLocked(p.moves, p.response)
	}
}

// addOpeningLocked 添加开局记录及其全部 8 重对称变体。需持有 ob.mu。
func (ob *OpeningBook) addOpeningLocked(moves [][3]int, bestMove move) {
	for _, t := range symmetryTransforms() {
		transformed := make([][3]int, len(moves))
		for i, m := range moves {
			r, c := t(m[0], m[1])
			transformed[i] = [3]int{r, c, m[2]}
		}
		br, bc := t(bestMove.row, bestMove.col)
		key := boardToKeyWithColor(transformed)
		ob.entries[key] = appendMoveUnique(ob.entries[key], move{br, bc})
	}
}

// symmetryTransforms 15×15 棋盘关于中心 (7,7) 的 8 重对称变换
// （恒等 + 旋转 90°/180°/270° + 镜像及各镜像旋转）
func symmetryTransforms() []func(r, c int) (int, int) {
	n := rule.Size - 1 // 14
	return []func(r, c int) (int, int){
		func(r, c int) (int, int) { return r, c },             // 恒等
		func(r, c int) (int, int) { return c, n - r },         // 旋转 90°
		func(r, c int) (int, int) { return n - r, n - c },     // 旋转 180°
		func(r, c int) (int, int) { return n - c, r },         // 旋转 270°
		func(r, c int) (int, int) { return r, n - c },         // 水平镜像
		func(r, c int) (int, int) { return n - r, c },         // 垂直镜像
		func(r, c int) (int, int) { return c, r },             // 主对角线镜像
		func(r, c int) (int, int) { return n - c, n - r },     // 副对角线镜像
	}
}

// appendMoveUnique 向应手列表追加去重
func appendMoveUnique(list []move, m move) []move {
	for _, e := range list {
		if e == m {
			return list
		}
	}
	return append(list, m)
}

// Lookup 查找开局库。
// 手数门控：库只记录"黑1白1 → 黑3应手"，棋盘不是恰好 2 手时直接未命中，
// 避免后续手数命中后返回已占用的应手点。
// 命中时从该局面的全部对称等价应手中随机取一（兼顾定式多样性）。
func (ob *OpeningBook) Lookup(board []int) (move, bool) {
	stones := 0
	for _, v := range board {
		if v != rule.Empty {
			stones++
		}
	}
	if stones != 2 {
		return move{}, false
	}

	key := boardToKeyFromBoardWithColor(board)
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	cands, ok := ob.entries[key]
	if !ok || len(cands) == 0 {
		return move{}, false
	}
	if len(cands) == 1 {
		return cands[0], true
	}
	return cands[rand.IntN(len(cands))], true
}

// boardToKeyWithColor 从带颜色的走法序列生成key
func boardToKeyWithColor(moves [][3]int) string {
	key := ""
	for _, m := range moves {
		key += fmt.Sprintf("%d,%d,%d;", m[0], m[1], m[2])
	}
	return key
}

// boardToKeyFromBoardWithColor 从完整棋盘生成带颜色的简化key
// （调用方已做手数门控，此处不再截断，避免"截断后误命中"）
func boardToKeyFromBoardWithColor(board []int) string {
	var blackMoves, whiteMoves [][3]int

	for i, v := range board {
		if v == rule.Empty {
			continue
		}
		r := i / rule.Size
		c := i % rule.Size
		if v == rule.Black {
			blackMoves = append(blackMoves, [3]int{r, c, rule.Black})
		} else {
			whiteMoves = append(whiteMoves, [3]int{r, c, rule.White})
		}
	}

	// 交替合并：黑1, 白1, 黑2, 白2...
	var allMoves [][3]int
	bi, wi := 0, 0
	for bi < len(blackMoves) || wi < len(whiteMoves) {
		if bi < len(blackMoves) {
			allMoves = append(allMoves, blackMoves[bi])
			bi++
		}
		if wi < len(whiteMoves) {
			allMoves = append(allMoves, whiteMoves[wi])
			wi++
		}
	}

	return boardToKeyWithColor(allMoves)
}

// GetGlobalOpeningBook 获取全局开局库实例
func GetGlobalOpeningBook() *OpeningBook {
	return globalOpeningBook
}
