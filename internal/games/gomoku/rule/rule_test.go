package rule

import "testing"

// set 在棋盘上放置一列棋子（用于快速构造连珠局面）
func set(board []int, color int, coords ...[2]int) {
	for _, c := range coords {
		board[Idx(c[0], c[1])] = color
	}
}

func TestIsWin(t *testing.T) {
	t.Run("横向连五", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{7, 3}, [2]int{7, 4}, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7})
		if !IsWin(b, 7, 5) {
			t.Fatal("横向连五应判胜")
		}
	})

	t.Run("纵向连五", func(t *testing.T) {
		b := NewBoard()
		set(b, White, [2]int{2, 8}, [2]int{3, 8}, [2]int{4, 8}, [2]int{5, 8}, [2]int{6, 8})
		if !IsWin(b, 4, 8) {
			t.Fatal("纵向连五应判胜")
		}
	})

	t.Run("主对角连五", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{1, 1}, [2]int{2, 2}, [2]int{3, 3}, [2]int{4, 4}, [2]int{5, 5})
		if !IsWin(b, 3, 3) {
			t.Fatal("主对角连五应判胜")
		}
	})

	t.Run("副对角连五", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{0, 4}, [2]int{1, 3}, [2]int{2, 2}, [2]int{3, 1}, [2]int{4, 0})
		if !IsWin(b, 2, 2) {
			t.Fatal("副对角连五应判胜")
		}
	})

	t.Run("四连不判胜", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{7, 3}, [2]int{7, 4}, [2]int{7, 5}, [2]int{7, 6})
		if IsWin(b, 7, 5) {
			t.Fatal("四连不应判胜")
		}
	})

	t.Run("对方颜色不干扰己方连线", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{7, 3}, [2]int{7, 4}, [2]int{7, 6}, [2]int{7, 7})
		set(b, White, [2]int{7, 5})
		if IsWin(b, 7, 6) {
			t.Fatal("被对方棋子隔断不应判胜")
		}
	})
}

func TestInBoundsAndIdx(t *testing.T) {
	if !InBounds(0, 0) || !InBounds(Size-1, Size-1) {
		t.Fatal("棋盘边角应合法")
	}
	if InBounds(-1, 0) || InBounds(0, -1) || InBounds(Size, 0) || InBounds(0, Size) {
		t.Fatal("越界坐标不应合法")
	}
	if Idx(2, 3) != 2*Size+3 {
		t.Fatal("索引计算错误")
	}
}

func TestIsFull(t *testing.T) {
	b := NewBoard()
	if IsFull(b) {
		t.Fatal("空棋盘不应判满")
	}
	for i := range b {
		b[i] = Black
	}
	if !IsFull(b) {
		t.Fatal("全满棋盘应判满")
	}
}

// blockedBoard 构造双方均无连五可能的僵局底板：
// (r+2c) mod 5 < 2 为黑，其余为白——沿 4 个方向的任意 5 连窗口都恰好覆盖全部 5 个模 5 余数，
// 必然同时含黑白两色（不会出现既有连五），可选指定若干位置清空。
func blockedBoard(holes ...[2]int) []int {
	b := NewBoard()
	for r := 0; r < Size; r++ {
		for c := 0; c < Size; c++ {
			if (r+2*c)%5 < 2 {
				b[Idx(r, c)] = Black
			} else {
				b[Idx(r, c)] = White
			}
		}
	}
	for _, h := range holes {
		b[Idx(h[0], h[1])] = Empty
	}
	return b
}

func TestCanWinAndIsDead(t *testing.T) {
	t.Run("空棋盘双方均可连五", func(t *testing.T) {
		b := NewBoard()
		if !CanWin(b, Black) || !CanWin(b, White) {
			t.Fatal("空棋盘双方均应仍有连五可能")
		}
		if IsDead(b) {
			t.Fatal("空棋盘不应判僵局")
		}
	})

	t.Run("封死满盘无连五可能", func(t *testing.T) {
		b := blockedBoard()
		if !IsDead(b) {
			t.Fatal("任意 5 连窗口均含双方棋子的满盘应判僵局")
		}
	})

	t.Run("棋盘未满但双方均无连五可能判僵局", func(t *testing.T) {
		b := blockedBoard([2]int{7, 7}, [2]int{7, 8})
		if IsFull(b) {
			t.Fatal("构造的僵局棋盘不应已满")
		}
		if !IsDead(b) {
			t.Fatal("留有空位但双方均无连五可能，应判僵局")
		}
	})

	t.Run("仅一方被封死不判僵局", func(t *testing.T) {
		// 黑棋保留一个含空格的无白窗口（横向 4 黑 + 1 空），白棋所有窗口均含黑子
		b := blockedBoard()
		set(b, Black, [2]int{7, 0}, [2]int{7, 1}, [2]int{7, 2}, [2]int{7, 3})
		b[Idx(7, 4)] = Empty
		if CanWin(b, White) {
			t.Fatal("白棋所有窗口均被黑子封锁，不应有连五可能")
		}
		if !CanWin(b, Black) {
			t.Fatal("黑棋横向 4 黑+1 空仍有连五可能")
		}
		if IsDead(b) {
			t.Fatal("一方仍有连五可能时不应判僵局")
		}
	})

	t.Run("活三局面不判僵局", func(t *testing.T) {
		b := NewBoard()
		set(b, Black, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7})
		set(b, White, [2]int{6, 5}, [2]int{6, 6}, [2]int{6, 7})
		if IsDead(b) {
			t.Fatal("双方均有活三时不应判僵局")
		}
	})
}

func TestColorHelpers(t *testing.T) {
	if ColorOfSeat(0) != Black || ColorOfSeat(1) != White {
		t.Fatal("座位-颜色映射错误")
	}
	if Opposite(Black) != White || Opposite(White) != Black {
		t.Fatal("颜色取反错误")
	}
}
