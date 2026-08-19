package bot

import (
	"math/rand/v2"
	"os"
	"testing"

	"xgames/internal/games/ddz/card"
	"xgames/internal/games/ddz/rule"
)

// simPlayer 模拟对局中的玩家（全部由引擎驱动，复刻 Controller.buildContextLocked 的口径）
type simPlayer struct {
	isLandlord bool
	hand       []card.Card
	plays      int // 出牌（非 pass）次数
	hintTurns  int // 首次出牌前"存在可压候选"的跟牌回合数（用于区分引擎失语与合法让牌）
}

// simulateGame 用引擎完整打完一局，返回赢家座位（-1=未分出胜负）、各座位出牌次数与首次出牌前的可压回合数
// （与真实服务端规则一致：跟牌须压过上家，连续两过开新轮，领出必出）。
// 三座位难度独立配置：人格化参数（DefaultPersonalities）与记牌视图完整度均按座位生效，
// 忠实镜像 Controller 的生产决策路径。
func simulateGame(t *testing.T, eng *Engine, diffs [3]string, landlord int, rng *rand.Rand) (int, [3]int, [3]int) {
	t.Helper()

	deck := card.NewDeck()
	rng.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })

	players := make([]*simPlayer, 3)
	for i := range players {
		players[i] = &simPlayer{hand: append([]card.Card(nil), deck[i*17:(i+1)*17]...)}
	}
	players[landlord].isLandlord = true
	players[landlord].hand = append(players[landlord].hand, deck[51:]...)

	var recent [2]PlayRecord
	var playedRank map[card.Rank]int = make(map[card.Rank]int)
	bombs := 0
	cur := landlord
	lastPlayer := landlord // 首回合领出
	var lastHand rule.ParsedHand
	passes := 0

	winner := -1
	for step := 0; step < 1500; step++ {
		p := players[cur]
		if len(p.hand) == 0 {
			winner = cur
			break
		}
		mustPlay := lastHand.IsEmpty() || cur == lastPlayer
		canBeat := mustPlay || rule.CanBeatWithHand(p.hand, lastHand)

		// 首次出牌前存在可压候选的跟牌回合：此类回合未开张兜底保证必出牌，
		// 若最终整局 0 出牌即为引擎失语；反之全程无可压候选的让牌属合法牌局
		if !mustPlay && p.plays == 0 && len(rule.GenerateHints(p.hand, lastHand)) > 0 {
			p.hintTurns++
		}

		remaining := simRemaining(diffs[cur], playedRank)

		up := (cur + 2) % 3
		down := (cur + 1) % 3
		gctx := GameContext{
			IsLandlord:     p.isLandlord,
			Hand:           p.hand,
			RecentPlays:    recent,
			MustPlay:       mustPlay,
			CanBeat:        canBeat,
			PlayerCounts:   [2]int{len(players[up].hand), len(players[down].hand)},
			RemainingCards: remaining,
			PlayedBombs:    bombs,
			UpIsLandlord:   players[up].isLandlord,
			DownIsLandlord: players[down].isLandlord,
			HasPlayed:      p.plays > 0,
			Personality:    DefaultPersonalities[diffs[cur]],
		}

		cards := eng.DecidePlay(nil, "sim", gctx)

		if mustPlay && cards == nil {
			t.Fatalf("领出回合决策为空：seat=%d hand=%d", cur, len(p.hand))
		}
		if cards == nil {
			if mustPlay {
				t.Fatalf("领出回合返回 pass：seat=%d", cur)
			}
			passes++
			if passes >= 2 {
				lastHand = rule.ParsedHand{}
				lastPlayer = (cur + 1) % 3
				passes = 0
			}
			cur = (cur + 1) % 3
			continue
		}

		// 出牌合法性校验（与服务端 handlePlayCards 相同口径）
		ph, err := rule.ParseHand(cards)
		if err != nil {
			t.Fatalf("引擎出了非法牌型 seat=%d cards=%v err=%v", cur, cards, err)
		}
		if !cardsInSimHand(p.hand, cards) {
			t.Fatalf("引擎出了不在手牌中的牌 seat=%d cards=%v", cur, cards)
		}
		if !mustPlay && !rule.CanBeat(ph, lastHand) {
			t.Fatalf("引擎出了压不过上家的牌 seat=%d cards=%v target=%+v", cur, cards, lastHand)
		}

		p.plays++
		p.hand = removeCards(p.hand, cards)
		recent[1] = recent[0]
		recent[0] = PlayRecord{Played: ph, IsLandlord: p.isLandlord}
		for _, cc := range cards {
			playedRank[cc.Rank]++
		}
		if ph.Type == rule.Bomb || ph.Type == rule.Rocket {
			bombs++
		}
		lastHand = ph
		lastPlayer = cur
		passes = 0

		if len(p.hand) == 0 {
			winner = cur
			break
		}
		cur = (cur + 1) % 3
	}

	var counts [3]int
	var hintTurns [3]int
	for i, p := range players {
		counts[i] = p.plays
		hintTurns[i] = p.hintTurns
	}
	return winner, counts, hintTurns
}

// simRemaining 按难度构造记牌视图（与 Controller.buildContextLocked 口径一致）：
// hard=全量 15 点数，normal=仅关键牌（A/2/大小王）；已出的牌只扣减被跟踪的点数
func simRemaining(difficulty string, played map[card.Rank]int) map[card.Rank]int {
	remaining := make(map[card.Rank]int)
	full := difficulty == "hard"
	for r := card.Rank3; r <= card.Rank2; r++ {
		if full || isKeyRank(r) {
			remaining[r] = 4
		}
	}
	remaining[card.RankBlackJoker] = 1
	remaining[card.RankRedJoker] = 1
	for r, n := range played {
		if _, ok := remaining[r]; ok {
			remaining[r] -= n
		}
	}
	return remaining
}

func cardsInSimHand(hand, cards []card.Card) bool {
	tmp := append([]card.Card(nil), hand...)
	for _, c := range cards {
		found := false
		for i, h := range tmp {
			if h == c {
				tmp = append(tmp[:i], tmp[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestSelfPlay_100Games 100 局自战回归（难度轮转分配）：三座位难度按 (game+seat)%3 轮转，
// easy/normal/hard 各恰 100 个席位数；每局保留引擎失语不变量校验（存在可压候选时不允许
// 整局沉默，复现用户反馈"有一个机器人玩家一直不出牌"的场景），并统计各难度席位胜率与
// 地主/农民胜率分布，作为策略改动的回归基线。例外：全程无可压候选且未领出 → 合法零出牌。
func TestSelfPlay_100Games(t *testing.T) {
	if os.Getenv("XGAMES_SELFPLAY") != "1" {
		t.Skip("自战测试需设置 XGAMES_SELFPLAY=1（通过 run_tools.sh bot 运行）")
	}
	if testing.Short() {
		t.Skip("跳过耗时自战模拟")
	}
	eng := newTestEngine()
	diffNames := []string{"easy", "normal", "hard"}
	type seatStat struct {
		seats, wins int
	}
	stats := map[string]*seatStat{}
	for _, d := range diffNames {
		stats[d] = &seatStat{}
	}
	landlordWins, peasantWins, draws := 0, 0, 0
	for game := 0; game < 100; game++ {
		rng := rand.New(rand.NewPCG(42, uint64(game*7+13)))
		var diffs [3]string
		for seat := range diffs {
			diffs[seat] = diffNames[(game+seat)%3]
			stats[diffs[seat]].seats++
		}
		landlord := game % 3
		winner, counts, hintTurns := simulateGame(t, eng, diffs, landlord, rng)
		for seat, n := range counts {
			if n > 0 {
				continue
			}
			if hintTurns[seat] > 0 {
				t.Errorf("[%s] game=%d 座位 %d 存在 %d 个可压回合却整局沉默（出牌次数=%v）",
					diffs[seat], game, seat, hintTurns[seat], counts)
			}
		}
		if winner < 0 {
			draws++
			continue
		}
		if winner == landlord {
			landlordWins++
			stats[diffs[landlord]].wins++
		} else {
			peasantWins++
			for seat := 0; seat < 3; seat++ {
				if seat != landlord {
					stats[diffs[seat]].wins++
				}
			}
		}
	}
	for _, d := range diffNames {
		s := stats[d]
		t.Logf("难度 %-6s 席位数=%d 胜场=%d 席位胜率=%.1f%%", d, s.seats, s.wins, 100*float64(s.wins)/float64(s.seats))
	}
	t.Logf("地主胜 %d / 农民胜 %d / 流局 %d（共 100 局）", landlordWins, peasantWins, draws)
}
