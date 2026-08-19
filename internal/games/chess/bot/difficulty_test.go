package bot

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"xgames/internal/games/chess/rule"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- 难度预设 ---

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
	if cfg.MaxDepth != maxSearchDepth {
		t.Fatalf("零值 MaxDepth 应回退既有行为（%d 层），实际 %d", maxSearchDepth, cfg.MaxDepth)
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

// --- DecideMoveDetailed ---

func TestDecideMoveDetailedZeroConfig(t *testing.T) {
	e := NewEngine(discardLogger())

	// 初始局面：开局库命中（零值 OpeningBookRate normalize 后 = 1.0）
	d := e.DecideMoveDetailed(context.Background(), "BOT", rule.NewBoard(), rule.CampRed, DifficultyConfig{})
	if d.Difficulty != "normal" {
		t.Fatalf("零值难度 normalize 后应为 normal，实际 %q", d.Difficulty)
	}
	// 初始局面应命中开局库
	if !d.BookHit {
		t.Fatal("初始局面应命中开局库")
	}
	if d.FromRow < 0 {
		t.Fatal("开局库命中应返回合法坐标")
	}
}

func TestDecideMoveDetailedEasyDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 easy 深度测试")
	}
	e := NewEngine(discardLogger())

	// 构造一个中盘局面（跳过开局库），验证 easy 档只搜索 2 层
	board := rule.NewBoard()
	// 红中炮
	rule.ApplyMove(board, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	// 黑屏风马
	rule.ApplyMove(board, rule.Move{FromRow: 0, FromCol: 7, ToRow: 2, ToCol: 6})
	// 红跳马（脱离开局库）
	rule.ApplyMove(board, rule.Move{FromRow: 9, FromCol: 1, ToRow: 7, ToCol: 2})

	d := e.DecideMoveDetailed(context.Background(), "BOT", board, rule.CampBlack, DifficultyFor("easy"))
	if d.Depth > 2 {
		t.Fatalf("easy 难度搜索深度应 ≤ 2，实际 %d", d.Depth)
	}
	if d.FromRow < 0 {
		t.Fatal("应返回合法坐标")
	}
}

func TestDecideMoveDetailedTopMoves(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 TopMoves 测试")
	}
	e := NewEngine(discardLogger())

	// 构造中盘局面
	board := rule.NewBoard()
	rule.ApplyMove(board, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	rule.ApplyMove(board, rule.Move{FromRow: 0, FromCol: 7, ToRow: 2, ToCol: 6})
	rule.ApplyMove(board, rule.Move{FromRow: 9, FromCol: 1, ToRow: 7, ToCol: 2})

	d := e.DecideMoveDetailed(context.Background(), "BOT", board, rule.CampBlack, DifficultyFor("hard"))
	if len(d.TopMoves) == 0 {
		t.Fatal("TopMoves 不应为空")
	}
	// TopMoves 应按分数降序
	for i := 1; i < len(d.TopMoves); i++ {
		if d.TopMoves[i-1].Score < d.TopMoves[i].Score {
			t.Fatalf("TopMoves 应按分数降序: %v", d.TopMoves)
		}
	}
}

// --- Zobrist 补执子方 ---

func TestZobristHashIncludesSide(t *testing.T) {
	board := rule.NewBoard()
	hRed := zobristHash(board, rule.CampRed)
	hBlack := zobristHash(board, rule.CampBlack)
	if hRed == hBlack {
		t.Fatal("同一局面不同执子方的 Zobrist 哈希应不同")
	}
	// 同一执子方应稳定
	hRed2 := zobristHash(board, rule.CampRed)
	if hRed != hRed2 {
		t.Fatal("同一局面同一执子方的 Zobrist 哈希应稳定")
	}
}

// --- P2: 增量 Zobrist 正确性 ---

func TestZobristStepMatchesFull(t *testing.T) {
	// 对多种局面/走法，验证 zobristStep 增量结果 == zobristHash 全盘重算
	board := rule.NewBoard()
	camp := rule.CampRed
	parentHash := zobristHash(board, camp)
	for _, m := range rule.AllLegalMoves(board, camp) {
		nb := rule.CopyBoard(board)
		captured := rule.ApplyMoveWithCapture(nb, m)
		stepHash := zobristStep(parentHash, rule.Idx(m.FromRow, m.FromCol), rule.Idx(m.ToRow, m.ToCol), board[rule.Idx(m.FromRow, m.FromCol)], captured)
		fullHash := zobristHash(nb, rule.CampRed+rule.CampBlack-camp)
		if stepHash != fullHash {
			t.Fatalf("zobristStep 与全盘重算不一致: move=%+v step=%d full=%d", m, stepHash, fullHash)
		}
	}
	// 中盘局面（红中炮后）再验证一轮
	board2 := rule.NewBoard()
	rule.ApplyMove(board2, rule.Move{FromRow: 7, FromCol: 1, ToRow: 7, ToCol: 4})
	rule.ApplyMove(board2, rule.Move{FromRow: 0, FromCol: 7, ToRow: 2, ToCol: 6})
	camp2 := rule.CampRed
	parentHash2 := zobristHash(board2, camp2)
	for _, m := range rule.AllLegalMoves(board2, camp2) {
		nb := rule.CopyBoard(board2)
		captured := rule.ApplyMoveWithCapture(nb, m)
		stepHash := zobristStep(parentHash2, rule.Idx(m.FromRow, m.FromCol), rule.Idx(m.ToRow, m.ToCol), board2[rule.Idx(m.FromRow, m.FromCol)], captured)
		fullHash := zobristHash(nb, rule.CampRed+rule.CampBlack-camp2)
		if stepHash != fullHash {
			t.Fatalf("中盘局面 zobristStep 不一致: move=%+v step=%d full=%d", m, stepHash, fullHash)
		}
	}
}

// --- P1: 收官粗糙 ---

func TestApplyEndgameCoarsening(t *testing.T) {
	// 初始局面：双方子力均等，评估接近 0，不应降深
	cfg := DifficultyConfig{MaxDepth: 4}
	board := rule.NewBoard()
	got := applyEndgameCoarsening(cfg, board, rule.CampRed)
	if got.MaxDepth != 4 {
		t.Fatalf("均势局面不应降深: got %d, want 4", got.MaxDepth)
	}

	// 构造大优势局面（红方多一车）：评估应 >5000，应降深
	winBoard := rule.NewBoard()
	// 移除黑方一个车
	winBoard[rule.Idx(0, 0)] = rule.Empty
	cfg2 := DifficultyConfig{MaxDepth: 4}
	got2 := applyEndgameCoarsening(cfg2, winBoard, rule.CampRed)
	if got2.MaxDepth != 3 {
		t.Fatalf("大优势局面应降深 1 层: got %d, want 3", got2.MaxDepth)
	}

	// easy 档 MaxDepth=2 不应再降
	cfg3 := DifficultyConfig{MaxDepth: 2}
	got3 := applyEndgameCoarsening(cfg3, winBoard, rule.CampRed)
	if got3.MaxDepth != 2 {
		t.Fatalf("easy 档不应降深: got %d, want 2", got3.MaxDepth)
	}
}

// --- P1: 动态思考时间 ---

func TestHumanDelay(t *testing.T) {
	// 简单局面：只有双将，无吃子机会（将帅不同列）
	simpleBoard := make([]int, rule.Rows*rule.Cols)
	simpleBoard[rule.Idx(9, 4)] = rule.RedGeneral
	simpleBoard[rule.Idx(0, 3)] = rule.BlackGeneral
	d := humanDelay(simpleBoard, rule.CampRed)
	if d < 500*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("简单局面延迟应在 0.5~1.5s，实际 %v", d)
	}

	// 复杂局面：红车可吃 3 个黑卒（≥3 个吃子选择）
	complexBoard := make([]int, rule.Rows*rule.Cols)
	complexBoard[rule.Idx(9, 4)] = rule.RedGeneral
	complexBoard[rule.Idx(0, 3)] = rule.BlackGeneral
	complexBoard[rule.Idx(5, 4)] = rule.RedChariot
	complexBoard[rule.Idx(4, 4)] = rule.BlackSoldier
	complexBoard[rule.Idx(6, 4)] = rule.BlackSoldier
	complexBoard[rule.Idx(5, 3)] = rule.BlackSoldier
	d2 := humanDelay(complexBoard, rule.CampRed)
	if d2 < 2500*time.Millisecond || d2 > 4000*time.Millisecond {
		t.Fatalf("复杂局面延迟应在 2.5~4s，实际 %v", d2)
	}
}
