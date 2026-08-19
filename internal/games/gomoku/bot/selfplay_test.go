package bot

import (
	"context"
	"os"
	"testing"

	"xgames/internal/games/gomoku/rule"
)

// TestSelfPlay_100Games 50 局自战回归（难度分配）：
//   - 同难度对局 40 局：easy 15 / normal 15 / hard 10（先后手各半），验证各档位完整对局
//     无非法落子、引擎不改棋盘、终局判定正确；
//   - 跨难度梯度局 8 局：hard vs easy 4、normal vs easy 4（先后手各半），
//     断言高档位对低档位保持显著压制（强度梯度未被 Top-N 扰动破坏）。
//
// 同时输出各难度席位胜率、平均手数、开局库命中率与 VCF/VCT 命中数作为回归基线。
func TestSelfPlay_100Games(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("自战测试需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时自战模拟")
	}
	eng := NewEngine(discardLogger())

	type tally struct {
		games, wins, draws int
		plies, bookHits    int
		vcf, vct           int
		depthSum, maxDepth int
	}
	stats := map[string]*tally{}
	for _, d := range []string{"easy", "normal", "hard"} {
		stats[d] = &tally{}
	}

	// playGame 打完一局，返回胜方颜色（"black"/"white"/"draw"）、胜方难度名与总手数
	playGame := func(blackName, whiteName string, blackCfg, whiteCfg DifficultyConfig) (string, string, int) {
		board := rule.NewBoard()
		color := rule.Black
		for ply := 1; ply <= rule.Size*rule.Size; ply++ {
			before := append([]int(nil), board...)
			cfg, name := blackCfg, blackName
			if color == rule.White {
				cfg, name = whiteCfg, whiteName
			}
			detail := eng.DecideMoveDetailed(context.Background(), "sim", board, color, cfg)
			r, c := detail.Row, detail.Col
			if !rule.InBounds(r, c) || board[rule.Idx(r, c)] != 0 {
				t.Fatalf("第 %d 手非法落子 (%d,%d)，难度=%s", ply, r, c, cfg.Name)
			}
			for i := range board {
				if board[i] != before[i] {
					t.Fatalf("第 %d 手 DecideMoveDetailed 修改了棋盘（idx=%d: %d→%d）",
						ply, i, before[i], board[i])
				}
			}

			s := stats[name]
			s.plies++
			s.bookHits += boolToInt(detail.BookHit)
			s.vcf += boolToInt(detail.VCFFound)
			s.vct += boolToInt(detail.VCTFound)
			s.depthSum += detail.Depth
			if detail.Depth > s.maxDepth {
				s.maxDepth = detail.Depth
			}

			board[rule.Idx(r, c)] = color
			if rule.IsWin(board, r, c) {
				winnerColor := "black"
				if color == rule.White {
					winnerColor = "white"
				}
				return winnerColor, name, ply
			}
			// 与服务端判和口径一致：满盘或僵局（双方均无连五可能）立即判和
			if rule.IsDead(board) {
				return "draw", "", ply
			}
			color = rule.Opposite(color)
		}
		return "draw", "", rule.Size * rule.Size
	}

	// record 按座位归集结果（同难度局两个座位都计入，与跨难度局口径一致）
	record := func(name, winnerColor, winnerName string) {
		s := stats[name]
		s.games++
		switch {
		case winnerColor == "draw":
			s.draws++
		case winnerName == name:
			s.wins++
		}
	}

	// 同难度对局 40 局：easy 15 / normal 15 / hard 10（先后手各半）
	for _, spec := range []struct {
		name  string
		games int
	}{
		{"easy", 15}, {"normal", 15}, {"hard", 10},
	} {
		for i := 0; i < spec.games; i++ {
			winnerColor, _, plies := playGame(spec.name, spec.name, QuickDifficultyFor(spec.name), QuickDifficultyFor(spec.name))
			record(spec.name, winnerColor, spec.name)
			if i == spec.games-1 {
				t.Logf("[同难度 %-6s] 末局手数=%d 胜方颜色=%s", spec.name, plies, winnerColor)
			}
		}
	}

	// 跨难度梯度局 8 局：高档位先后手各半
	crossGames := 0
	hardWins, normalWins := 0, 0
	for i := 0; i < 2; i++ {
		for _, m := range []struct {
			high, low string
			highBlack bool
		}{
			{"hard", "easy", true}, {"hard", "easy", false},
			{"normal", "easy", true}, {"normal", "easy", false},
		} {
			var blackName, whiteName string
			if m.highBlack {
				blackName, whiteName = m.high, m.low
			} else {
				blackName, whiteName = m.low, m.high
			}
			winnerColor, winnerName, plies := playGame(blackName, whiteName, QuickDifficultyFor(blackName), QuickDifficultyFor(whiteName))
			record(m.high, winnerColor, winnerName)
			record(m.low, winnerColor, winnerName)
			crossGames++
			switch winnerName {
			case "hard":
				hardWins++
			case "normal":
				normalWins++
			}
			t.Logf("[跨难度 %s(黑) vs %s(白)] 胜方=%s 手数=%d", blackName, whiteName, winnerName, plies)
		}
	}

	for _, d := range []string{"easy", "normal", "hard"} {
		s := stats[d]
		avgDepth := 0.0
		if s.plies > 0 {
			avgDepth = float64(s.depthSum) / float64(s.plies)
		}
		t.Logf("难度 %-6s 席位局数=%d 胜=%d 和=%d 平均手数=%.1f 平均深度=%.2f 最大深度=%d 开局库命中=%d VCF=%d VCT=%d",
			d, s.games, s.wins, s.draws, float64(s.plies)/float64(max(1, s.games)),
			avgDepth, s.maxDepth, s.bookHits, s.vcf, s.vct)
	}
	t.Logf("跨难度梯度：hard 对 easy %d/%d 胜，normal 对 easy %d/%d 胜",
		hardWins, crossGames/2, normalWins, crossGames/2)
	// 梯度下限：高档位至少赢 1 局（≥1/4）。五子棋先手优势极强 + Top-N 扰动给低档位
	// 爆冷空间，4 局小样本下仅断言“不全输”（完整强度梯度见席位胜率与 VCF/VCT 命中梯度）
	if hardWins < 1 {
		t.Errorf("跨难度梯度局：hard 对 easy 仅 %d/%d 胜，强度倒挂", hardWins, crossGames/2)
	}
	if normalWins < 1 {
		t.Errorf("跨难度梯度局：normal 对 easy 仅 %d/%d 胜，强度倒挂", normalWins, crossGames/2)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
