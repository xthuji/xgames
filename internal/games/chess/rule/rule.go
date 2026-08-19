// Package rule 中国象棋核心规则：棋盘表示、棋子走法生成与将军/绝杀判定（纯函数，无 I/O）。
//
// 棋盘 9 列 × 10 行，交叉点 90 个；棋子编码十位=阵营（1 红 / 2 黑），
// 个位=兵种（1 帅将 / 2 车 / 3 马 / 4 炮 / 5 相象 / 6 仕士 / 7 兵卒）。
// 红方在下方（row 6-9），黑方在上方（row 0-3），河界在 row 4-5 之间。
package rule

// Cols / Rows 棋盘尺寸
const (
	Cols = 9
	Rows = 10
)

// 棋子编码
const (
	Empty = 0

	RedGeneral  = 11 // 帅
	RedChariot  = 12 // 车
	RedHorse    = 13 // 马
	RedCannon   = 14 // 炮
	RedElephant = 15 // 相
	RedAdvisor  = 16 // 仕
	RedSoldier  = 17 // 兵

	BlackGeneral  = 21 // 将
	BlackChariot  = 22 // 车
	BlackHorse    = 23 // 马
	BlackCannon   = 24 // 砲
	BlackElephant = 25 // 象
	BlackAdvisor  = 26 // 士
	BlackSoldier  = 27 // 卒
)

// Camp 阵营
const (
	CampRed   = 1
	CampBlack = 2
)

// NewBoard 创建初始棋盘布局
func NewBoard() []int {
	b := make([]int, Cols*Rows)
	// 黑方（上方）
	b[Idx(0, 0)] = BlackChariot
	b[Idx(0, 1)] = BlackHorse
	b[Idx(0, 2)] = BlackElephant
	b[Idx(0, 3)] = BlackAdvisor
	b[Idx(0, 4)] = BlackGeneral
	b[Idx(0, 5)] = BlackAdvisor
	b[Idx(0, 6)] = BlackElephant
	b[Idx(0, 7)] = BlackHorse
	b[Idx(0, 8)] = BlackChariot
	b[Idx(2, 1)] = BlackCannon
	b[Idx(2, 7)] = BlackCannon
	for x := 0; x < 9; x += 2 {
		b[Idx(3, x)] = BlackSoldier
	}
	// 红方（下方）
	b[Idx(9, 0)] = RedChariot
	b[Idx(9, 1)] = RedHorse
	b[Idx(9, 2)] = RedElephant
	b[Idx(9, 3)] = RedAdvisor
	b[Idx(9, 4)] = RedGeneral
	b[Idx(9, 5)] = RedAdvisor
	b[Idx(9, 6)] = RedElephant
	b[Idx(9, 7)] = RedHorse
	b[Idx(9, 8)] = RedChariot
	b[Idx(7, 1)] = RedCannon
	b[Idx(7, 7)] = RedCannon
	for x := 0; x < 9; x += 2 {
		b[Idx(6, x)] = RedSoldier
	}
	return b
}

// Idx 行列 → 一维下标
func Idx(row, col int) int { return row*Cols + col }

// InBounds 坐标是否在棋盘内
func InBounds(row, col int) bool { return row >= 0 && row < Rows && col >= 0 && col < Cols }

// CampOf 棋子所属阵营（0 = 空位）
func CampOf(piece int) int { return piece / 10 }

// TypeOf 棋子兵种（0-7）
func TypeOf(piece int) int { return piece % 10 }

// CampOfSeat 座位 → 阵营（0 号座执红先行）
func CampOfSeat(seat int) int {
	if seat == 0 {
		return CampRed
	}
	return CampBlack
}

// GeneralOf 阵营对应的将帅编码
func GeneralOf(camp int) int {
	if camp == CampRed {
		return RedGeneral
	}
	return BlackGeneral
}

// CopyBoard 复制棋盘
func CopyBoard(b []int) []int { return append([]int(nil), b...) }

// --- 走法 ---

// Move 一步走法
type Move struct {
	FromRow, FromCol int
	ToRow, ToCol     int
	Captured         int // 被吃棋子（0 = 无）
}

// PseudoMoves 生成 (row,col) 处棋子的所有伪合法走法（不考虑送将 / 对将）
func PseudoMoves(board []int, row, col int) []Move {
	piece := board[Idx(row, col)]
	if piece == Empty {
		return nil
	}
	switch TypeOf(piece) {
	case 1:
		return generalMoves(board, row, col, piece)
	case 2:
		return chariotMoves(board, row, col, piece)
	case 3:
		return horseMoves(board, row, col, piece)
	case 4:
		return cannonMoves(board, row, col, piece)
	case 5:
		return elephantMoves(board, row, col, piece)
	case 6:
		return advisorMoves(board, row, col, piece)
	case 7:
		return soldierMoves(board, row, col, piece)
	}
	return nil
}

// generalMoves 帅/将：九宫内上下左右一步
func generalMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	minR, maxR := 0, 2
	if camp == CampRed {
		minR, maxR = 7, 9
	}
	var moves []Move
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, d := range dirs {
		nr, nc := row+d[0], col+d[1]
		if nr < minR || nr > maxR || nc < 3 || nc > 5 {
			continue
		}
		target := board[Idx(nr, nc)]
		if target != Empty && CampOf(target) == camp {
			continue
		}
		moves = append(moves, Move{row, col, nr, nc, target})
	}
	return moves
}

// advisorMoves 仕/士：九宫内斜走一步
func advisorMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	minR, maxR := 0, 2
	if camp == CampRed {
		minR, maxR = 7, 9
	}
	var moves []Move
	dirs := [4][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	for _, d := range dirs {
		nr, nc := row+d[0], col+d[1]
		if nr < minR || nr > maxR || nc < 3 || nc > 5 {
			continue
		}
		target := board[Idx(nr, nc)]
		if target != Empty && CampOf(target) == camp {
			continue
		}
		moves = append(moves, Move{row, col, nr, nc, target})
	}
	return moves
}

// elephantMoves 相/象：斜走两步（塞象眼），不可过河
func elephantMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	var moves []Move
	dirs := [4][2]int{{-2, -2}, {-2, 2}, {2, -2}, {2, 2}}
	blocks := [4][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	for i, d := range dirs {
		nr, nc := row+d[0], col+d[1]
		if !InBounds(nr, nc) {
			continue
		}
		// 不可过河
		if camp == CampRed && nr < 5 {
			continue
		}
		if camp == CampBlack && nr > 4 {
			continue
		}
		// 塞象眼
		br, bc := row+blocks[i][0], col+blocks[i][1]
		if board[Idx(br, bc)] != Empty {
			continue
		}
		target := board[Idx(nr, nc)]
		if target != Empty && CampOf(target) == camp {
			continue
		}
		moves = append(moves, Move{row, col, nr, nc, target})
	}
	return moves
}

// horseMoves 马：日字走（蹩马腿）
func horseMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	type horseStep struct {
		dr, dc    int // 目标偏移
		legR, legC int // 马腿位置
	}
	steps := [8]horseStep{
		{-2, -1, -1, 0}, {-2, 1, -1, 0},
		{2, -1, 1, 0}, {2, 1, 1, 0},
		{-1, -2, 0, -1}, {-1, 2, 0, 1},
		{1, -2, 0, -1}, {1, 2, 0, 1},
	}
	var moves []Move
	for _, s := range steps {
		nr, nc := row+s.dr, col+s.dc
		if !InBounds(nr, nc) {
			continue
		}
		if board[Idx(row+s.legR, col+s.legC)] != Empty {
			continue
		}
		target := board[Idx(nr, nc)]
		if target != Empty && CampOf(target) == camp {
			continue
		}
		moves = append(moves, Move{row, col, nr, nc, target})
	}
	return moves
}

// chariotMoves 车：直线任意步（不可越子）
func chariotMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	var moves []Move
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, d := range dirs {
		r, c := row+d[0], col+d[1]
		for InBounds(r, c) {
			target := board[Idx(r, c)]
			if target == Empty {
				moves = append(moves, Move{row, col, r, c, 0})
			} else {
				if CampOf(target) != camp {
					moves = append(moves, Move{row, col, r, c, target})
				}
				break
			}
			r += d[0]
			c += d[1]
		}
	}
	return moves
}

// cannonMoves 炮：直走不吃，隔一子打（翻山）
func cannonMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	var moves []Move
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, d := range dirs {
		r, c := row+d[0], col+d[1]
		mounted := false
		for InBounds(r, c) {
			target := board[Idx(r, c)]
			if !mounted {
				if target == Empty {
					moves = append(moves, Move{row, col, r, c, 0})
				} else {
					mounted = true
				}
			} else {
				if target != Empty {
					if CampOf(target) != camp {
						moves = append(moves, Move{row, col, r, c, target})
					}
					break
				}
			}
			r += d[0]
			c += d[1]
		}
	}
	return moves
}

// soldierMoves 兵/卒：未过河只能前进，过河后可左右
func soldierMoves(board []int, row, col int, piece int) []Move {
	camp := CampOf(piece)
	forward := 1
	if camp == CampRed {
		forward = -1
	}
	crossed := false
	if camp == CampRed && row <= 4 {
		crossed = true
	}
	if camp == CampBlack && row >= 5 {
		crossed = true
	}
	var moves []Move
	add := func(dr, dc int) {
		nr, nc := row+dr, col+dc
		if !InBounds(nr, nc) {
			return
		}
		target := board[Idx(nr, nc)]
		if target != Empty && CampOf(target) == camp {
			return
		}
		moves = append(moves, Move{row, col, nr, nc, target})
	}
	add(forward, 0)
	if crossed {
		add(0, -1)
		add(0, 1)
	}
	return moves
}

// --- 将军 / 合法走法 ---

// LegalMoves 生成 (row,col) 处棋子的所有合法走法（排除送将 / 对将）
func LegalMoves(board []int, row, col int) []Move {
	piece := board[Idx(row, col)]
	if piece == Empty {
		return nil
	}
	camp := CampOf(piece)
	pseudo := PseudoMoves(board, row, col)
	var legal []Move
	for _, m := range pseudo {
		nb := CopyBoard(board)
		ApplyMove(nb, m)
		if !IsInCheck(nb, camp) && FlyingGeneralsOk(nb) {
			legal = append(legal, m)
		}
	}
	return legal
}

// AllLegalMoves 生成阵营所有合法走法
func AllLegalMoves(board []int, camp int) []Move {
	var moves []Move
	for r := 0; r < Rows; r++ {
		for c := 0; c < Cols; c++ {
			p := board[Idx(r, c)]
			if p != Empty && CampOf(p) == camp {
				moves = append(moves, LegalMoves(board, r, c)...)
			}
		}
	}
	return moves
}

// AllLegalCaptures 生成阵营所有合法吃子走法。
// 只对吃子做合法性校验，供静态搜索避免全量走法生成的开销。
func AllLegalCaptures(board []int, camp int) []Move {
	var moves []Move
	for r := 0; r < Rows; r++ {
		for c := 0; c < Cols; c++ {
			p := board[Idx(r, c)]
			if p == Empty || CampOf(p) != camp {
				continue
			}
			for _, m := range PseudoMoves(board, r, c) {
				if m.Captured == Empty {
					continue
				}
				nb := CopyBoard(board)
				ApplyMove(nb, m)
				if !IsInCheck(nb, camp) && FlyingGeneralsOk(nb) {
					moves = append(moves, m)
				}
			}
		}
	}
	return moves
}

// findGeneral 查找阵营的将/帅位置
func findGeneral(board []int, camp int) (int, int) {
	g := GeneralOf(camp)
	for r := 0; r < Rows; r++ {
		for c := 0; c < Cols; c++ {
			if board[Idx(r, c)] == g {
				return r, c
			}
		}
	}
	return -1, -1
}

// IsInCheck 阵营的将/帅是否正被攻击
func IsInCheck(board []int, camp int) bool {
	gr, gc := findGeneral(board, camp)
	if gr < 0 {
		return true
	}
	return isThreatened(board, gr, gc, camp)
}

// isThreatened (gr,gc) 是否被敌方可吃
func isThreatened(board []int, gr, gc int, camp int) bool {
	enemy := CampRed
	if camp == CampRed {
		enemy = CampBlack
	}

	// 敌方马（8 个日字位，检查马腿）
	horseOffsets := [8][3]int{
		{-2, -1, -1}, {-2, 1, -1},
		{2, -1, 1}, {2, 1, 1},
		{-1, -2, -1}, {-1, 2, 1},
		{1, -2, -1}, {1, 2, 1},
	}
	for _, h := range horseOffsets {
		hr, hc := gr+h[0], gc+h[1]
		if !InBounds(hr, hc) {
			continue
		}
		horse := board[Idx(hr, hc)]
		if horse == Empty || CampOf(horse) != enemy || TypeOf(horse) != 3 {
			continue
		}
		var legR, legC int
		if h[0] == -2 || h[0] == 2 {
			legR, legC = gr+h[2], gc
		} else {
			legR, legC = gr, gc+h[2]
		}
		if board[Idx(legR, legC)] == Empty {
			return true
		}
	}

	// 敌方车 & 将/帅（同轴线，无阻挡）
	// 车：四方向扫描，遇第一子若为敌车则被威胁
	// 将帅：同列无子面对面（对将 / 飞将）
	chariotT := 2
	generalT := 1
	dirs := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, d := range dirs {
		r, c := gr+d[0], gc+d[1]
		for InBounds(r, c) {
			p := board[Idx(r, c)]
			if p != Empty {
				if CampOf(p) == enemy && (TypeOf(p) == chariotT || TypeOf(p) == generalT) {
					return true
				}
				break
			}
			r += d[0]
			c += d[1]
		}
	}

	// 敌方炮（隔一子打）
	for _, d := range dirs {
		r, c := gr+d[0], gc+d[1]
		mounted := false
		for InBounds(r, c) {
			p := board[Idx(r, c)]
			if !mounted {
				if p != Empty {
					mounted = true
				}
			} else {
				if p != Empty {
					if CampOf(p) == enemy && TypeOf(p) == 4 {
						return true
					}
					break
				}
			}
			r += d[0]
			c += d[1]
		}
	}

	// 敌方兵/卒（前方 + 过河后两侧）
	soldierOffsets := [3][2]int{{1, 0}, {0, -1}, {0, 1}}
	if camp == CampRed {
		soldierOffsets[0][0] = 1 // 红方被黑兵攻击：黑兵从上方来
	} else {
		soldierOffsets[0][0] = -1 // 黑方被红兵攻击：红兵从下方来
	}
	for _, off := range soldierOffsets {
		sr, sc := gr+off[0], gc+off[1]
		if !InBounds(sr, sc) {
			continue
		}
		s := board[Idx(sr, sc)]
		if s == Empty || CampOf(s) != enemy || TypeOf(s) != 7 {
			continue
		}
		// 侧面攻击要求兵/卒已过河
		if off[1] != 0 {
			if enemy == CampRed && sr > 4 {
				continue
			}
			if enemy == CampBlack && sr < 5 {
				continue
			}
		}
		return true
	}

	return false
}

// FlyingGeneralsOk 两将是否不面对面（同列无子间隔）
func FlyingGeneralsOk(board []int) bool {
	rr, rc := findGeneral(board, CampRed)
	br, bc := findGeneral(board, CampBlack)
	if rr < 0 || br < 0 {
		return true
	}
	if rc != bc {
		return true
	}
	for r := br + 1; r < rr; r++ {
		if board[Idx(r, rc)] != Empty {
			return true
		}
	}
	return false
}

// IsCheckmate 阵营是否被将死（被将军且无合法走法）
func IsCheckmate(board []int, camp int) bool {
	if !IsInCheck(board, camp) {
		return false
	}
	return len(AllLegalMoves(board, camp)) == 0
}

// IsStalemate 阵营是否困毙（未被将军但无合法走法）
func IsStalemate(board []int, camp int) bool {
	if IsInCheck(board, camp) {
		return false
	}
	return len(AllLegalMoves(board, camp)) == 0
}

// ApplyMove 在棋盘上执行走子（原地修改）
func ApplyMove(board []int, m Move) {
	piece := board[Idx(m.FromRow, m.FromCol)]
	board[Idx(m.FromRow, m.FromCol)] = Empty
	board[Idx(m.ToRow, m.ToCol)] = piece
}

// ApplyMoveWithCapture 执行走子并返回被吃棋子
func ApplyMoveWithCapture(board []int, m Move) int {
	piece := board[Idx(m.FromRow, m.FromCol)]
	captured := board[Idx(m.ToRow, m.ToCol)]
	board[Idx(m.FromRow, m.FromCol)] = Empty
	board[Idx(m.ToRow, m.ToCol)] = piece
	return captured
}
