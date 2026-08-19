// Package rule 五子棋核心规则：棋盘表示、落子合法性与连五判定（纯函数，无 I/O）。
package rule

// Size 棋盘尺寸（15×15）
const Size = 15

// 棋子颜色
const (
	Empty = 0
	Black = 1
	White = 2
)

// NewBoard 创建空棋盘（行优先 Size*Size，0=空）
func NewBoard() []int {
	return make([]int, Size*Size)
}

// Idx 行列 → 一维下标（调用方保证行列合法）
func Idx(row, col int) int {
	return row*Size + col
}

// InBounds 行列是否在棋盘内
func InBounds(row, col int) bool {
	return row >= 0 && row < Size && col >= 0 && col < Size
}

// ColorOfSeat 座位 → 执子颜色（0 号座执黑先行）
func ColorOfSeat(seat int) int {
	if seat == 0 {
		return Black
	}
	return White
}

// Opposite 对手颜色
func Opposite(color int) int {
	if color == Black {
		return White
	}
	return Black
}

// IsWin (row,col) 处落子后是否形成连五（调用前提：该位置已有棋子）
func IsWin(board []int, row, col int) bool {
	color := board[Idx(row, col)]
	if color == Empty {
		return false
	}
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		count := 1 + countDir(board, row, col, d[0], d[1], color) + countDir(board, row, col, -d[0], -d[1], color)
		if count >= 5 {
			return true
		}
	}
	return false
}

// countDir 从 (row,col) 沿 (dr,dc) 方向连续同色棋子数（不含起点）
func countDir(board []int, row, col, dr, dc, color int) int {
	n := 0
	r, c := row+dr, col+dc
	for InBounds(r, c) && board[Idx(r, c)] == color {
		n++
		r += dr
		c += dc
	}
	return n
}

// IsFull 棋盘是否已满（平局判定）
func IsFull(board []int) bool {
	for _, v := range board {
		if v == Empty {
			return false
		}
	}
	return true
}

// 判和原因（GameOverPayload.DrawReason）
const (
	DrawReasonBoardFull     = "board_full"      // 棋盘下满
	DrawReasonNoWinPossible = "no_win_possible" // 双方均无连五可能（僵局）
)

// CanWin 指定颜色是否仍存在理论连五可能：
// 任意方向上存在一个 5 格连续窗口，窗口内不含对方棋子且至少有一个空格。
// 注：窗口全部为己方棋子意味着已连五获胜，不属"仍可能"范畴（调用方应先做 IsWin 判定）。
func CanWin(board []int, color int) bool {
	opp := Opposite(color)
	dirs := [4][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for r := 0; r < Size; r++ {
		for c := 0; c < Size; c++ {
			for _, d := range dirs {
				// 窗口末端越界则该方向此起点不可行
				if !InBounds(r+d[0]*4, c+d[1]*4) {
					continue
				}
				blocked, hasEmpty := false, false
				for k := 0; k < 5; k++ {
					v := board[Idx(r+d[0]*k, c+d[1]*k)]
					if v == opp {
						blocked = true
						break
					}
					if v == Empty {
						hasEmpty = true
					}
				}
				if !blocked && hasEmpty {
					return true
				}
			}
		}
	}
	return false
}

// IsDead 双方均无连五可能（僵局判和，调用前提：棋盘上无既有连五）。
// 棋盘未满但双方都已无法连五时，继续行棋也不可能分出胜负，应立即判和。
func IsDead(board []int) bool {
	return !CanWin(board, Black) && !CanWin(board, White)
}
