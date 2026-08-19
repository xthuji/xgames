package bot

import (
	"sync"

	"xgames/internal/games/gomoku/rule"
)

// boardPool 棋盘对象池（15×15 = 225个int）
var boardPool = sync.Pool{
	New: func() interface{} {
		b := make([]int, rule.Size*rule.Size)
		return &b
	},
}

// GetBoard 从池中获取棋盘
func GetBoard() *[]int {
	return boardPool.Get().(*[]int)
}

// PutBoard 归还棋盘到池中
func PutBoard(b *[]int) {
	// 清零避免残留数据
	for i := range *b {
		(*b)[i] = 0
	}
	boardPool.Put(b)
}
