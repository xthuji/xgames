package rule

import "testing"

// findMoveIn 在走法列表中查找从 (fr,fc) 到 (tr,tc) 的走法
func findMoveIn(moves []Move, fr, fc, tr, tc int) bool {
	for _, m := range moves {
		if m.FromRow == fr && m.FromCol == fc && m.ToRow == tr && m.ToCol == tc {
			return true
		}
	}
	return false
}

// TestHorseMovesLegBlocking 验证马走日的蹩马腿规则（横向 +2 方向曾误用目标列做马腿）
func TestHorseMovesLegBlocking(t *testing.T) {
	mk := func(legCol2 int) []int {
		// 红马在 (5,4)，其余位置仅按需摆放
		b := make([]int, Cols*Rows)
		b[Idx(5, 4)] = RedHorse
		if legCol2 >= 0 {
			b[Idx(5, legCol2)] = RedSoldier
		}
		return b
	}

	// 马腿 (5,5) 有子：右侧两个日字位 (4,6)/(6,6) 均不可走
	b := mk(5)
	moves := horseMoves(b, 5, 4, RedHorse)
	if findMoveIn(moves, 5, 4, 4, 6) || findMoveIn(moves, 5, 4, 6, 6) {
		t.Fatal("(5,5) 马腿被蹩，右侧日字位不应可走")
	}

	// 马腿 (5,5) 空、(5,6) 有子：右侧日字位应可走（马腿在 +1 而非 +2）
	b = mk(6)
	moves = horseMoves(b, 5, 4, RedHorse)
	if !findMoveIn(moves, 5, 4, 4, 6) || !findMoveIn(moves, 5, 4, 6, 6) {
		t.Fatal("(5,5) 空、(5,6) 有子，右侧日字位应可走（+2 处有子不构成蹩腿）")
	}

	// 左侧对称：马腿 (5,3) 有子 → (4,2)/(6,2) 不可走
	b = mk(3)
	moves = horseMoves(b, 5, 4, RedHorse)
	if findMoveIn(moves, 5, 4, 4, 2) || findMoveIn(moves, 5, 4, 6, 2) {
		t.Fatal("(5,3) 马腿被蹩，左侧日字位不应可走")
	}

	// 纵向：马腿 (4,4) 有子 → (3,3)/(3,5) 不可走；(3,3) 有子但马腿空 → 可走
	b = make([]int, Cols*Rows)
	b[Idx(5, 4)] = RedHorse
	b[Idx(4, 4)] = RedSoldier
	moves = horseMoves(b, 5, 4, RedHorse)
	if findMoveIn(moves, 5, 4, 3, 3) || findMoveIn(moves, 5, 4, 3, 5) {
		t.Fatal("(4,4) 马腿被蹩，上方日字位不应可走")
	}
	b[Idx(4, 4)] = Empty
	b[Idx(3, 3)] = RedSoldier
	moves = horseMoves(b, 5, 4, RedHorse)
	if !findMoveIn(moves, 5, 4, 3, 5) {
		t.Fatal("(4,4) 空时上方日字位应可走")
	}
	if findMoveIn(moves, 5, 4, 3, 3) {
		t.Fatal("己方棋子所在位置不应可走")
	}
}

// TestIsInCheckHorseThreat 验证将军判定包含横向日字位的马（曾因马腿落在帅位而漏判）
func TestIsInCheckHorseThreat(t *testing.T) {
	mk := func(blockLeg bool) []int {
		b := make([]int, Cols*Rows)
		// 红帅 (9,4)，黑马 (8,2)：横向日字位，马腿 (9,3)
		b[Idx(9, 4)] = RedGeneral
		b[Idx(8, 2)] = BlackHorse
		if blockLeg {
			b[Idx(9, 3)] = RedSoldier
		}
		return b
	}

	if !IsInCheck(mk(false), CampRed) {
		t.Fatal("黑马横向日字位将军且马腿 (9,3) 为空，应判定被将军")
	}
	if IsInCheck(mk(true), CampRed) {
		t.Fatal("马腿 (9,3) 被蹩，不应判定被将军")
	}

	// 纵向对照：黑马 (7,5)，马腿 (8,4)
	mkVert := func(blockLeg bool) []int {
		b := make([]int, Cols*Rows)
		b[Idx(9, 4)] = RedGeneral
		b[Idx(7, 5)] = BlackHorse
		if blockLeg {
			b[Idx(8, 4)] = RedSoldier
		}
		return b
	}
	if !IsInCheck(mkVert(false), CampRed) {
		t.Fatal("黑马纵向日字位将军且马腿 (8,4) 为空，应判定被将军")
	}
	if IsInCheck(mkVert(true), CampRed) {
		t.Fatal("马腿 (8,4) 被蹩，不应判定被将军")
	}
}
