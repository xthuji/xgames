package bot

import (
	"testing"

	"xgames/internal/games/gomoku/rule"
)

func TestDifficultyFor(t *testing.T) {
	if got := DifficultyFor("easy").Name; got != "easy" {
		t.Fatalf("easy 应命中预设，实际 %q", got)
	}
	if got := DifficultyFor("hard").MaxDepth; got != 4 {
		t.Fatalf("hard MaxDepth 应为 4，实际 %d", got)
	}
	// 空/未知回退 normal
	if got := DifficultyFor(""); got.Name != "normal" {
		t.Fatalf("空难度应回退 normal，实际 %q", got.Name)
	}
	if got := DifficultyFor("unknown"); got.Name != "normal" {
		t.Fatalf("未知难度应回退 normal，实际 %q", got.Name)
	}
}

func TestDifficultyNormalize(t *testing.T) {
	// 零值配置回退引擎既有行为
	cfg := DifficultyConfig{}.normalize()
	if cfg.MaxDepth != maxSearchDepth || cfg.VCFMaxDepth != vcfMaxDepth {
		t.Fatalf("零值应回退既有行为（4 层/16 层），实际 %d/%d", cfg.MaxDepth, cfg.VCFMaxDepth)
	}
	if cfg.OpeningBookRate != 1.0 || cfg.RandomTopN != 1 {
		t.Fatalf("零值应始终用开局库且不扰动，实际 %v/%d", cfg.OpeningBookRate, cfg.RandomTopN)
	}
	// 预设不被 normalize 改写
	for _, name := range []string{"easy", "normal", "hard"} {
		p := DifficultyFor(name)
		if got := p.normalize(); got != p {
			t.Fatalf("%s 预设不应被 normalize 改写", name)
		}
	}
}

func TestOpeningBook_HitAndGate(t *testing.T) {
	ob := GetGlobalOpeningBook()

	// 黑1天元 + 白2上方（直指注册方位）→ 应命中
	board := rule.NewBoard()
	board[rule.Idx(7, 7)] = rule.Black
	board[rule.Idx(6, 7)] = rule.White
	m, ok := ob.Lookup(board)
	if !ok {
		t.Fatal("黑1白1 应命中开局库")
	}
	if board[rule.Idx(m.row, m.col)] != rule.Empty {
		t.Fatalf("开局库应手 (%d,%d) 落在已有棋子上", m.row, m.col)
	}

	// 对称方位：白2 右侧 (7,8)（残月注册方位）与白2 下方旋转等价 → 应命中
	board2 := rule.NewBoard()
	board2[rule.Idx(7, 7)] = rule.Black
	board2[rule.Idx(7, 8)] = rule.White
	if _, ok := ob.Lookup(board2); !ok {
		t.Fatal("白2 右侧（对称变体方位）应命中开局库")
	}

	// 手数门控：3 手与 5 手局面不得命中（原先第 5 手起返回已占用点）
	board3 := rule.NewBoard()
	board3[rule.Idx(7, 7)] = rule.Black
	board3[rule.Idx(6, 7)] = rule.White
	board3[rule.Idx(8, 8)] = rule.Black
	if _, ok := ob.Lookup(board3); ok {
		t.Fatal("3 手局面不应命中开局库（手数门控失效）")
	}
	board5 := rule.NewBoard()
	board5[rule.Idx(7, 7)] = rule.Black
	board5[rule.Idx(6, 7)] = rule.White
	board5[rule.Idx(8, 8)] = rule.Black
	board5[rule.Idx(8, 6)] = rule.White
	board5[rule.Idx(6, 6)] = rule.Black
	if _, ok := ob.Lookup(board5); ok {
		t.Fatal("5 手局面不应命中开局库（手数门控失效）")
	}
}

func TestOpeningBook_DuplicateKeysKept(t *testing.T) {
	// 花/浦月（云/溪月等同理）首两手相同，修复前互相覆盖；
	// 修复后同 key 应保留两个不同应手
	ob := GetGlobalOpeningBook()
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	key := boardToKeyWithColor([][3]int{{7, 7, rule.Black}, {6, 7, rule.White}})
	responses := ob.entries[key]
	if len(responses) < 2 {
		t.Fatalf("白2 上方局面应至少保留 2 个应手（花月+浦月），实际 %d 个", len(responses))
	}
}

func TestDecideMoveDetailed_RealData(t *testing.T) {
	e := NewEngine(discardLogger())

	// 一步成五：详情应携带真实分数/威胁/胜利标记
	board := rule.NewBoard()
	set(board, rule.Black, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7}, [2]int{7, 8})
	set(board, rule.White, [2]int{6, 5}, [2]int{6, 6})

	d := e.DecideMoveDetailed(t.Context(), "BOT", board, rule.Black, DifficultyConfig{})
	if d.Difficulty != "normal" {
		t.Fatalf("零值难度 normalize 后应为 normal，实际 %q", d.Difficulty)
	}
	if !(d.Row == 7 && (d.Col == 4 || d.Col == 9)) {
		t.Fatalf("应补五连，实际 (%d,%d)", d.Row, d.Col)
	}
	if d.Score != scoreFive {
		t.Fatalf("成五分应为 %d，实际 %d", scoreFive, d.Score)
	}
	if len(d.Threats) == 0 || d.Threats[0] != "成五" {
		t.Fatalf("威胁应含 成五，实际 %v", d.Threats)
	}
	if len(d.TopMoves) == 0 {
		t.Fatal("TopMoves 不应为空")
	}

	// 搜索路径：稀疏局面（无三/四棋型，杀棋短路不触发）+ easy 档（2 层）
	// 应确定完成 2 层迭代加深，且 TopMoves 按分数降序
	board2 := rule.NewBoard()
	set(board2, rule.Black, [2]int{7, 7}, [2]int{3, 3})
	set(board2, rule.White, [2]int{0, 0}, [2]int{14, 14})
	d2 := e.DecideMoveDetailed(t.Context(), "BOT", board2, rule.Black, DifficultyFor("easy"))
	if d2.Depth != 2 {
		t.Fatalf("easy 难度应完成 2 层搜索，实际 %d", d2.Depth)
	}
	for i := 1; i < len(d2.TopMoves); i++ {
		if d2.TopMoves[i-1].Score < d2.TopMoves[i].Score {
			t.Fatalf("TopMoves 应按分数降序: %v", d2.TopMoves)
		}
	}
}

func TestEngine_PerturbationNeverMissesTactics(t *testing.T) {
	// easy 档（RandomTopN=5）在对手成五在即时必须仍堵四（扰动不得丢战术要点）
	board := rule.NewBoard()
	set(board, rule.White, [2]int{7, 5}, [2]int{7, 6}, [2]int{7, 7}, [2]int{7, 8})
	set(board, rule.Black, [2]int{5, 2}, [2]int{3, 10})

	e := NewEngine(discardLogger())
	for i := 0; i < 10; i++ {
		d := e.DecideMoveDetailed(t.Context(), "BOT", board, rule.Black, DifficultyFor("easy"))
		if !((d.Row == 7) && (d.Col == 4 || d.Col == 9)) {
			t.Fatalf("easy 随机扰动不得放弃必堵点，实际 (%d,%d)", d.Row, d.Col)
		}
		if !rule.InBounds(d.Row, d.Col) || board[rule.Idx(d.Row, d.Col)] != rule.Empty {
			t.Fatalf("扰动结果非法 (%d,%d)", d.Row, d.Col)
		}
	}
}
