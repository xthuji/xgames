package bot

import (
	"context"
	"fmt"
	"os"
	"testing"

	"xgames/internal/games/chess/rule"
)

// TestSelfPlay_100Games 50 局自战回归（难度分配）：
//   - 同难度对局 40 局：easy 15 / normal 15 / hard 10（红黑方各半），验证完整对局
//     无非法走子、引擎不改棋盘、将死/困毙/三次重复判和等终局判定正确；
//   - 跨难度梯度局 8 局：hard vs easy 4、normal vs easy 4（红黑方各半），
//     断言高档位对低档位保持显著压制（强度梯度未被 Top-N 扰动破坏）。
//
// 同时输出各难度席位胜率、平均手数、平均搜索深度、开局库命中率作为回归基线。
// 终局口径与服务端一致：将死/困毙判负，三次重复局面判和，13 回合未吃子（自然限着）判和，
// 双方均无取胜可能子力局面判和，300 手上限判和（防死循环兜底）。
func TestSelfPlay_100Games(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("自战测试需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时自战模拟")
	}
	eng := NewEngine(nil)

	type tally struct {
		games, wins, draws int
		plies, bookHits    int
		depthSum, maxDepth int
	}
	stats := map[string]*tally{}
	for _, d := range []string{"easy", "normal", "hard"} {
		stats[d] = &tally{}
	}

	// playGame 打完一局，返回 "red"/"black"/"draw"
	playGame := func(redName, blackName string, redCfg, blackCfg DifficultyConfig) string {
		board := rule.NewBoard()
		camp := rule.CampRed
		repetition := make(map[uint64]int)
		halfmoveClock := 0 // 连续未吃子 ply 数（与服务端口径一致）
		repetition[rule.PositionHash(board, camp)]++
		for ply := 1; ply <= 300; ply++ {
			cfg, name := redCfg, redName
			if camp == rule.CampBlack {
				cfg, name = blackCfg, blackName
			}

			// 三次重复判和（局面含执子方，rule.PositionHash 口径与服务端一致）
			key := rule.PositionHash(board, camp)
			if repetition[key] >= 3 {
				return "draw"
			}

			// 实战上下文：局面历史 + 限着计数（判和感知，与 controller 传入口径一致）
			history := make([]uint64, 0, len(repetition)+1)
			seen := make(map[uint64]bool)
			for k := range repetition {
				if !seen[k] {
					seen[k] = true
					history = append(history, k)
				}
			}
			before := rule.CopyBoard(board)
			detail := eng.DecideMoveWithContext(context.Background(), "sim", board, camp, cfg,
				GameContext{History: history, HalfmoveClock: halfmoveClock})
			move := rule.Move{FromRow: detail.FromRow, FromCol: detail.FromCol, ToRow: detail.ToRow, ToCol: detail.ToCol}
			legal := false
			for _, lm := range rule.AllLegalMoves(board, camp) {
				// 仅比坐标：AllLegalMoves 生成吃子着法时填充 Captured，引擎返回的着法不带该字段
				if lm.FromRow == move.FromRow && lm.FromCol == move.FromCol && lm.ToRow == move.ToRow && lm.ToCol == move.ToCol {
					legal = true
					break
				}
			}
			if !legal {
				piece := board[rule.Idx(move.FromRow, move.FromCol)]
				t.Fatalf("第 %d 手非法走子 (%d,%d)→(%d,%d)，难度=%s camp=%d 该格棋子=%d bookHit=%v 深度=%d TopMoves=%+v 棋盘=\n%v",
					ply, move.FromRow, move.FromCol, move.ToRow, move.ToCol, cfg.Name, camp, piece,
					detail.BookHit, detail.Depth, detail.TopMoves, dumpBoard(board))
			}
			for i := range board {
				if board[i] != before[i] {
					t.Fatalf("第 %d 手 DecideMoveDetailed 修改了棋盘（idx=%d: %d→%d）",
						ply, i, before[i], board[i])
				}
			}

			s := stats[name]
			s.plies++
			if detail.BookHit {
				s.bookHits++
			}
			s.depthSum += detail.Depth
			if detail.Depth > s.maxDepth {
				s.maxDepth = detail.Depth
			}

			rule.ApplyMove(board, move)
			next := rule.CampRed + rule.CampBlack - camp
			// 判和上下文更新：局面哈希入重复计数；吃子重置限着计数
			captured := before[rule.Idx(move.ToRow, move.ToCol)]
			repetition[rule.PositionHash(board, next)]++
			if captured != rule.Empty {
				halfmoveClock = 0
			} else {
				halfmoveClock++
			}
			// 将死/困毙：无合法走法的一方判负（中国象棋困毙同负）
			if rule.IsCheckmate(board, next) || rule.IsStalemate(board, next) {
				return name
			}
			// 双方均无取胜可能 / 13 回合（26 ply）未吃子：判和（与服务端口径一致）
			if rule.InsufficientMaterial(board) || halfmoveClock >= 26 {
				return "draw"
			}
			camp = next
		}
		return "draw"
	}

	record := func(name, result string) {
		s := stats[name]
		s.games++
		switch result {
		case name:
			s.wins++
		case "draw":
			s.draws++
		}
	}

	// 同难度对局 40 局：easy 15 / normal 15 / hard 10（红黑方各半）
	for _, spec := range []struct {
		name  string
		games int
	}{
		{"easy", 15}, {"normal", 15}, {"hard", 10},
	} {
		for i := 0; i < spec.games; i++ {
			result := playGame(spec.name, spec.name, QuickDifficultyFor(spec.name), QuickDifficultyFor(spec.name))
			record(spec.name, result)
		}
	}

	// 跨难度梯度局 8 局：高档位红黑方各半
	crossGames := 0
	hardWins, normalWins := 0, 0
	for i := 0; i < 2; i++ {
		for _, m := range []struct {
			high, low string
			highRed   bool
		}{
			{"hard", "easy", true}, {"hard", "easy", false},
			{"normal", "easy", true}, {"normal", "easy", false},
		} {
			var redName, blackName string
			if m.highRed {
				redName, blackName = m.high, m.low
			} else {
				redName, blackName = m.low, m.high
			}
			result := playGame(redName, blackName, QuickDifficultyFor(redName), QuickDifficultyFor(blackName))
			record(m.high, result)
			record(m.low, result)
			crossGames++
			switch result {
			case "hard":
				hardWins++
			case "normal":
				normalWins++
			}
			t.Logf("[跨难度 %s(红) vs %s(黑)] 结果=%s", redName, blackName, result)
		}
	}

	for _, d := range []string{"easy", "normal", "hard"} {
		s := stats[d]
		avgDepth := 0.0
		if s.plies > 0 {
			avgDepth = float64(s.depthSum) / float64(s.plies)
		}
		t.Logf("难度 %-6s 席位局数=%d 胜=%d 和=%d 平均手数=%.1f 平均深度=%.2f 最大深度=%d 开局库命中=%d",
			d, s.games, s.wins, s.draws, float64(s.plies)/float64(max(1, s.games)),
			avgDepth, s.maxDepth, s.bookHits)
	}
	// 小样本（各 4 局）下 Top-N 扰动与和棋规则（自然限着）会带来偶发，
	// 梯度断言取不倒挂下限：高档位至少 3/4 胜
	if hardWins < 3 {
		t.Errorf("跨难度梯度局：hard 对 easy 仅 %d/%d 胜，强度梯度未达预期", hardWins, crossGames/2)
	}
	if normalWins < 3 {
		t.Errorf("跨难度梯度局：normal 对 easy 仅 %d/%d 胜，强度梯度未达预期", normalWins, crossGames/2)
	}
}

// dumpBoard 棋盘 9×10 可读 dump（失败现场诊断用）
func dumpBoard(board []int) string {
	s := ""
	for r := 0; r < rule.Rows; r++ {
		for c := 0; c < rule.Cols; c++ {
			s += fmt.Sprintf("%4d", board[rule.Idx(r, c)])
		}
		s += "\n"
	}
	return s
}
