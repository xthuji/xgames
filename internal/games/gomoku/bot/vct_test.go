package bot

import (
	"context"
	"testing"

	"xgames/internal/games/gomoku/rule"
)

// VCT 专项正确性测试（docs/bots/gomoku-bot.md §9 方向二）。
// 历史缺陷：isFourPoint 宽松窗口语义把活三也判为四点（首着门控永远拒真）、
// findDefensesForLiveThree 只累计前向棋子（永远找不到防守点）、
// 攻方连走两手且活四过渡被排除（双活三必胜永远找不到）——三重缺陷叠加使
// VCT 在 100 局自战中几乎零命中。本组测试锁定修复后的行为。

// composeBoard 构造局面（黑：横 (7,5)(7,6) + 竖 (5,7)(6,7)，(7,7) 一子双活三；
// 白散子占边角，避免干扰）
func composeDoubleThreeBoard() []int {
	board := rule.NewBoard()
	set := func(r, c, v int) { board[rule.Idx(r, c)] = v }
	set(7, 5, rule.Black)
	set(7, 6, rule.Black)
	set(5, 7, rule.Black)
	set(6, 7, rule.Black)
	set(0, 0, rule.White)
	set(0, 1, rule.White)
	return board
}

// TestVCT_DoubleLiveThreeFound 双活三（33）必胜局面：tryVCT 必须命中，
// 且命中着法落下后确实形成严格活三（修复前恒返回 nil）
func TestVCT_DoubleLiveThreeFound(t *testing.T) {
	board := composeDoubleThreeBoard()
	mv := tryVCT(board, rule.Black, 16)
	if mv == nil {
		t.Fatal("双活三必胜局面 tryVCT 应命中（修复前恒 nil）")
	}
	board[rule.Idx(mv.row, mv.col)] = rule.Black
	if !strictLiveThreePoint(board, mv.row, mv.col, rule.Black) {
		t.Errorf("VCT 命中点 (%d,%d) 落子后应形成严格活三", mv.row, mv.col)
	}
}

// TestVCT_SingleLiveThreeNoFalsePositive 单活三非必胜：无第二威胁时
// tryVCT 不得虚报（活三被堵一端后无后续手段）
func TestVCT_SingleLiveThreeNoFalsePositive(t *testing.T) {
	board := rule.NewBoard()
	set := func(r, c, v int) { board[rule.Idx(r, c)] = v }
	// 黑仅一条横活三 (7,5)(7,6)(7,7)，白远离
	set(7, 5, rule.Black)
	set(7, 6, rule.Black)
	set(7, 7, rule.Black)
	set(0, 0, rule.White)
	set(0, 2, rule.White)
	set(0, 4, rule.White)
	if mv := tryVCT(board, rule.Black, 16); mv != nil {
		t.Errorf("单活三非必胜，tryVCT 不应命中，实际返回 (%d,%d)", mv.row, mv.col)
	}
}

// TestVCT_DecideMarksHit DecideMoveDetailed 在双活三局面应命中 VCT（决策链 2.5 步）
func TestVCT_DecideMarksHit(t *testing.T) {
	board := composeDoubleThreeBoard()
	eng := NewEngine(discardLogger())
	detail := eng.DecideMoveDetailed(context.Background(), "sim", board, rule.Black, DifficultyFor("hard"))
	if !detail.VCTFound && !detail.VCFFound && detail.Score < 900000 {
		t.Logf("着法=(%d,%d) 深度=%d 分数=%d", detail.Row, detail.Col, detail.Depth, detail.Score)
		t.Errorf("双活三局面下决策应标记 VCF/VCT 命中或给出必胜分")
	}
}

// TestVCT_ConvertsToWin 端到端 oracle：双活三必胜局面下，黑（hard）对白（hard）
// 对弈必须由黑取胜——必胜局 convertible 是 VCT/搜索正确性的最终标准
func TestVCT_ConvertsToWin(t *testing.T) {
	board := composeDoubleThreeBoard()
	// 黑已下 4 子、白 2 子，轮到白行棋（让白先防，黑随后展示必胜转化）
	eng := NewEngine(discardLogger())
	hard := DifficultyFor("hard")
	color := rule.White
	winner := 0
	for ply := 1; ply <= rule.Size*rule.Size; ply++ {
		detail := eng.DecideMoveDetailed(context.Background(), "sim", board, color, hard)
		r, c := detail.Row, detail.Col
		if !rule.InBounds(r, c) || board[rule.Idx(r, c)] != rule.Empty {
			t.Fatalf("第 %d 手非法落子 (%d,%d)", ply, r, c)
		}
		board[rule.Idx(r, c)] = color
		if rule.IsWin(board, r, c) {
			winner = color
			t.Logf("白方行棋 %d 手后黑棋于 %d 手取胜（含前置）", ply, ply+4)
			break
		}
		color = rule.Opposite(color)
	}
	if winner != rule.Black {
		t.Fatalf("双活三必胜局面黑棋必须取胜，实际 winner=%v", winner)
	}
}

// TestVCT_LiveThreeDefense 对手活三威胁：引擎应堵活三端点（或立即取胜）
func TestVCT_LiveThreeDefense(t *testing.T) {
	board := rule.NewBoard()
	set := func(r, c, v int) { board[rule.Idx(r, c)] = v }
	// 白活三 (7,4)(7,5)(7,6)，黑 (5,5)(6,6) 散子
	set(7, 4, rule.White)
	set(7, 5, rule.White)
	set(7, 6, rule.White)
	set(5, 5, rule.Black)
	set(6, 6, rule.Black)
	set(0, 0, rule.Black)

	eng := NewEngine(discardLogger())
	detail := eng.DecideMoveDetailed(context.Background(), "sim", board, rule.Black, DifficultyFor("hard"))
	r, c := detail.Row, detail.Col
	// 合法应对：白直接成五点（不存在）之外的活三端点 (7,3)/(7,7)
	if !(r == 7 && (c == 3 || c == 7)) {
		t.Errorf("对手活三应堵端点 (7,3)/(7,7)，实际 (%d,%d)", r, c)
	}
}
