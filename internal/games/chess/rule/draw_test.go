package rule

import "testing"

// TestInsufficientMaterial 子力不足判定：双方均无车马炮兵卒（只剩帅仕相）→ 无法取胜判和
func TestInsufficientMaterial(t *testing.T) {
	cases := []struct {
		name  string
		board func() []int
		want  bool
	}{
		{"初始局面有攻子", NewBoard, false},
		{"红方有车", func() []int {
			b := make([]int, Rows*Cols)
			b[Idx(9, 4)] = RedGeneral
			b[Idx(9, 3)] = RedAdvisor
			b[Idx(9, 2)] = RedElephant
			b[Idx(0, 4)] = BlackGeneral
			b[Idx(0, 3)] = BlackAdvisor
			b[Idx(0, 2)] = BlackElephant
			b[Idx(5, 0)] = RedChariot
			return b
		}, false},
		{"黑方有过河卒", func() []int {
			b := make([]int, Rows*Cols)
			b[Idx(9, 4)] = RedGeneral
			b[Idx(0, 4)] = BlackGeneral
			b[Idx(4, 4)] = BlackSoldier // 黑卒过河
			return b
		}, false},
		{"只剩帅仕相（含未过河兵可忽略场景无）", func() []int {
			b := make([]int, Rows*Cols)
			b[Idx(9, 4)] = RedGeneral
			b[Idx(9, 3)] = RedAdvisor
			b[Idx(9, 5)] = RedAdvisor
			b[Idx(9, 2)] = RedElephant
			b[Idx(9, 6)] = RedElephant
			b[Idx(0, 4)] = BlackGeneral
			b[Idx(0, 3)] = BlackAdvisor
			b[Idx(0, 2)] = BlackElephant
			return b
		}, true},
		{"双方仅剩双帅", func() []int {
			b := make([]int, Rows*Cols)
			b[Idx(9, 4)] = RedGeneral
			b[Idx(0, 4)] = BlackGeneral
			return b
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InsufficientMaterial(c.board()); got != c.want {
				t.Fatalf("InsufficientMaterial = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPositionHash 局面哈希口径：同局面同执子方一致；红黑同形不同执子方不同；走子后变化
func TestPositionHash(t *testing.T) {
	b := NewBoard()
	same := PositionHash(b, CampRed)
	if same != PositionHash(CopyBoard(b), CampRed) {
		t.Fatal("同局面同执子方哈希应一致")
	}
	if PositionHash(b, CampRed) == PositionHash(b, CampBlack) {
		t.Fatal("同局面不同执子方哈希应不同")
	}

	nb := CopyBoard(b)
	ApplyMove(nb, Move{FromRow: 9, FromCol: 1, ToRow: 7, ToCol: 1})
	if PositionHash(nb, CampBlack) == PositionHash(b, CampBlack) {
		t.Fatal("走子后哈希应变化")
	}
}
